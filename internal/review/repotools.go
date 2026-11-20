package review

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jdziat/open-nitpick/internal/memo"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// Limits on what a verification tool call may return. A checker that can read
// the whole repository into its context pays for it and loses the thread, and
// a model-supplied pattern or path is input, so each bound is also a defence.
const (
	toolReadLines   = 200
	toolReadDefault = 120
	toolReadBytes   = 1 << 20
	toolGrepLines   = 30
	toolGrepWidth   = 240
	toolResultBytes = 6000
	toolGrepTimeout = 20 * time.Second
	toolCacheBytes  = 8 << 20
)

// repoTools is what the verification checker may look at: read-only access to
// the pull request's files, and search where a git checkout exists.
type repoTools struct {
	root        string
	provider    vcs.Provider
	ref         vcs.Ref
	files       memo.Cache[string, capturedFile]
	lines       memo.Cache[string, []string]
	searches    memo.Cache[[2]string, string]
	grepTimeout time.Duration
	cacheBytes  atomic.Int64
}

// turnFailuresKey carries one operation's failure counter in its context. The
// engine shares one repoTools across concurrent batches, so a counter on the
// tools themselves would charge a lookup failure to whichever batch was
// comparing it at that moment.
type turnFailuresKey struct{}

// withTurnFailures returns a context whose lookups count their failures into the
// returned counter and nowhere else.
func withTurnFailures(ctx context.Context) (context.Context, *atomic.Uint64) {
	turn := &atomic.Uint64{}
	return context.WithValue(ctx, turnFailuresKey{}, turn), turn
}

// fail records one failed lookup against the operation that issued it.
func (t *repoTools) fail(ctx context.Context) {
	if turn, ok := ctx.Value(turnFailuresKey{}).(*atomic.Uint64); ok {
		turn.Add(1)
	}
}

type capturedFile struct {
	data []byte
	err  error
}

func (t *repoTools) retainBytes(size int64) bool {
	for {
		used := t.cacheBytes.Load()
		if size > toolCacheBytes-used {
			return false
		}
		if t.cacheBytes.CompareAndSwap(used, used+size) {
			return true
		}
	}
}

// newRepoTools reads from root when it is a git checkout, and otherwise from
// the provider, which can read a file but cannot search.
func newRepoTools(root string, p vcs.Provider, ref vcs.Ref) *repoTools {
	if root != "" {
		if info, err := os.Stat(filepath.Join(root, ".git")); err != nil || info == nil {
			root = ""
		}
	}
	return &repoTools{root: root, provider: p, ref: ref, grepTimeout: toolGrepTimeout}
}

func (e *Engine) repositoryTools(ref vcs.Ref) *repoTools {
	if e.lookups != nil {
		return e.lookups
	}
	return newRepoTools(e.RepoRoot, e.Provider, ref)
}

// canSearch reports whether grep_repo works.
func (t *repoTools) canSearch() bool { return t.root != "" }

// read returns a numbered window of one file. Missing, escaping and oversized
// paths come back as text the checker can read, never as errors, because a
// wrong guess at a path is the ordinary way to look something up.
func (t *repoTools) read(ctx context.Context, path string, start, end int) string {
	path = filepath.ToSlash(filepath.Clean(strings.TrimSpace(path)))
	if path == "" || path == "." || strings.HasPrefix(path, "../") || filepath.IsAbs(path) {
		return "no such file: " + path
	}
	data, err := t.fileBytes(ctx, path)
	if err != nil {
		return "no such file: " + path
	}
	if len(data) > toolReadBytes {
		return fmt.Sprintf("%s is %d bytes, over the %d byte limit for reading; grep it instead", path, len(data), toolReadBytes)
	}
	lines, err := t.lines.GetIf(ctx, path, func() ([]string, error) {
		return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n"), nil
	}, func(lines []string) bool {
		return t.retainBytes(int64(len(data) + len(lines)*16 + len(path) + 128))
	})
	if err != nil {
		return "no such file: " + path
	}
	if start < 1 {
		start = 1
	}
	if end < start {
		end = start + toolReadDefault - 1
	}
	end = min(end, len(lines), start+toolReadLines-1)
	if start > len(lines) {
		return fmt.Sprintf("%s has %d lines", path, len(lines))
	}
	var b strings.Builder
	for i := start; i <= end; i++ {
		fmt.Fprintf(&b, "%5d %s\n", i, lines[i-1])
	}
	return b.String()
}

func (t *repoTools) fileBytes(ctx context.Context, path string) ([]byte, error) {
	result, err := t.files.GetIf(ctx, path, func() (capturedFile, error) {
		data, loadErr := t.loadFile(ctx, path)
		if loadErr != nil && !errors.Is(loadErr, vcs.ErrNotFound) && !errors.Is(loadErr, os.ErrNotExist) {
			return capturedFile{}, loadErr
		}
		return capturedFile{data: data, err: loadErr}, nil
	}, func(file capturedFile) bool {
		return len(file.data) <= toolReadBytes && t.retainBytes(int64(len(file.data)+len(path)+128))
	})
	if err != nil {
		t.fail(ctx)
		return nil, err
	}
	return result.data, result.err
}

func (t *repoTools) loadFile(ctx context.Context, path string) ([]byte, error) {
	// A commit-pinned review reads that commit. The checkout may sit on a
	// different revision or carry edits, and evidence from it would be
	// attributed to code the reviewer never saw, so a failed lookup does not
	// fall back to the checkout.
	if t.pinned() {
		if t.root != "" {
			if data, err := t.showAtRevision(ctx, path); err == nil || t.provider == nil {
				return data, err
			}
		}
		if t.provider == nil {
			return nil, errors.New("no source to read")
		}
		return t.provider.FileContent(ctx, t.ref, path)
	}
	if t.root != "" {
		full := filepath.Join(t.root, filepath.FromSlash(path))
		// A symlink in the tree must not lead outside it.
		resolved, err := filepath.EvalSymlinks(full)
		if err != nil {
			return nil, err
		}
		root, err := filepath.EvalSymlinks(t.root)
		if err != nil {
			return nil, err
		}
		if resolved != root && !strings.HasPrefix(resolved, root+string(filepath.Separator)) {
			return nil, errors.New("path leaves the checkout")
		}
		// Checked on the resolved path, so a link inside the tree cannot name a
		// file the tools refuse by its own name.
		rel, err := filepath.Rel(root, resolved)
		if err != nil || t.private(ctx, rel) {
			return nil, errors.New("not a file the review may read")
		}
		return os.ReadFile(resolved)
	}
	if t.provider == nil {
		return nil, errors.New("no source to read")
	}
	return t.provider.FileContent(ctx, t.ref, path)
}

// pinned reports whether lookups are bound to a commit rather than the
// working tree.
func (t *repoTools) pinned() bool { return t.ref.Head != vcs.Worktree }

// showAtRevision reads one file from the reviewed commit's object store. A
// symlink comes back as its target text, so nothing outside the repository is
// read, and ignored files are absent because they are not in a commit.
func (t *repoTools) showAtRevision(ctx context.Context, path string) ([]byte, error) {
	if strings.HasPrefix(t.ref.Head, "-") {
		return nil, fmt.Errorf("%s: %w", path, vcs.ErrNotFound)
	}
	ctx, cancel := context.WithTimeout(ctx, toolGrepTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "--no-pager", "show", t.ref.Head+":"+path)
	cmd.Dir = t.root
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%s at %s: %w", path, t.ref.Head, vcs.ErrNotFound)
	}
	return out, nil
}

// private reports whether a path inside the checkout is one the tools must not
// return: anything under .git, which holds the checkout's own configuration and,
// in a CI checkout, the token the job pushes with, and anything git ignores,
// which is where a working tree keeps .env files and local keys. The text the
// tools are driven by is a pull request's diff and description, which its author
// controls, so a file the tools can read is a file a finding can publish. Tracked
// files, and new files that are not ignored yet, stay readable: a review of
// uncommitted changes needs them. Anything git cannot answer about is refused.
func (t *repoTools) private(ctx context.Context, rel string) bool {
	for _, segment := range strings.Split(filepath.ToSlash(rel), "/") {
		if strings.EqualFold(segment, ".git") {
			return true
		}
	}
	ctx, cancel := context.WithTimeout(ctx, toolGrepTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "check-ignore", "-q", "--", filepath.ToSlash(rel))
	cmd.Dir = t.root
	// Exit 1 is git saying the path is not ignored. Exit 0 says it is, and any
	// other outcome means git could not tell, which is refused.
	err := cmd.Run()
	var exit *exec.ExitError
	notIgnored := errors.As(err, &exit) && exit.ExitCode() == 1
	return !notIgnored
}

// grep searches the reviewed commit, or the checkout's tracked files when no
// commit is pinned, with an extended regular expression. The pattern follows
// -e and the glob follows --, so neither can be read by git as an option.
func (t *repoTools) grep(ctx context.Context, pattern, glob string) string {
	pattern = strings.TrimSpace(pattern)
	glob = strings.TrimSpace(glob)
	result, err := t.searches.GetIf(ctx, [2]string{pattern, glob}, func() (string, error) {
		return t.search(ctx, pattern, glob)
	}, func(out string) bool {
		return t.retainBytes(int64(len(out) + len(pattern) + len(glob) + 128))
	})
	if err != nil {
		t.fail(ctx)
		if result != "" {
			return result
		}
		return "the search timed out; use a narrower pattern or a path glob"
	}
	return result
}

func (t *repoTools) search(ctx context.Context, pattern, glob string) (string, error) {
	if !t.canSearch() {
		return "search is unavailable: there is no git checkout, so read files by path", nil
	}
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return "no pattern given", nil
	}
	ctx, cancel := context.WithTimeout(ctx, t.grepTimeout)
	defer cancel()
	args := []string{"--no-pager", "grep", "-n", "-E", "-I", "--no-color", "-e", pattern}
	prefix := ""
	if t.pinned() {
		if strings.HasPrefix(t.ref.Head, "-") {
			return "the search failed: invalid revision", errors.New("invalid revision")
		}
		// git names a match in a tree as revision:path; the checker is given
		// the same path form the working-tree search returns.
		args = append(args, t.ref.Head)
		prefix = t.ref.Head + ":"
	}
	if g := strings.TrimSpace(glob); g != "" {
		args = append(args, "--", g)
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = t.root
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	// git grep exits 1 for "no match", which is an answer and not a failure.
	var exit *exec.ExitError
	noMatch := errors.As(err, &exit) && exit.ExitCode() == 1
	if err != nil && !noMatch {
		return "the search failed: " + oneLine(strings.TrimSpace(errOut.String())), err
	}
	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if l == "" {
			continue
		}
		if len(l) > toolGrepWidth {
			l = l[:toolGrepWidth]
		}
		lines = append(lines, strings.TrimPrefix(l, prefix))
		if len(lines) == toolGrepLines {
			break
		}
	}
	if len(lines) == 0 {
		return "(no matches)", nil
	}
	return strings.Join(lines, "\n"), nil
}

// bound caps a tool result at what the checker is given back.
func bound(s string) string {
	if len(s) <= toolResultBytes {
		return s
	}
	return s[:toolResultBytes] + "\n[truncated]"
}
