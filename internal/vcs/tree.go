package vcs

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

// Tree is the provider behind `nitpick full-review`: the working tree read as
// one change that adds every file, so the engine that reviews a pull request
// reviews a repository with nothing else changed. Every line is a changed
// line, so analyzers run on the whole file, the model is shown the whole
// file, and a finding may sit anywhere in it.
//
// It wraps Local for everything but the diff, which it synthesizes from the
// files git knows about (tracked, or untracked and not ignored) under the
// paths asked for, in path order, leaving out what is not text, what is
// larger than MaxBytes, and, once Budget tokens have been spent, everything
// after. What it left out is recorded, because a review that covered half
// the tree must not read as a review of the tree.
type Tree struct {
	*Local

	// Paths restricts the review to files under these prefixes (or equal to
	// them). Empty means the whole tree.
	Paths []string

	// MaxBytes leaves out a file larger than this. Zero means no limit.
	MaxBytes int

	// Budget is a ceiling in estimated tokens (bytes over four) on what is
	// reviewed; files in path order are included until it is reached. Zero
	// means no ceiling.
	Budget int

	// Covered, Unbudgeted and Skipped are filled by Diff: the files in the
	// review, the files the budget stopped before, and the files left out
	// for what they are (binary, oversized), each with its reason.
	Covered    []string
	Unbudgeted []string
	Skipped    []TreeSkip

	// Lines is the line count of each covered file, the denominator a score
	// per thousand lines needs.
	Lines map[string]int

	// Snapshot supplies immutable content instead of reading the checkout.
	Snapshot map[string][]byte
	// Capture freezes the first successful Diff and its coverage. Create a new
	// provider to assess a different scope.
	Capture      bool
	captured     bool
	capturedDiff []byte
}

// TreeSkip is one file the tree review left out on purpose.
type TreeSkip struct {
	Path   string
	Reason string
}

// NewTree wraps a Local provider for a whole-tree review.
func NewTree(local *Local, paths []string) *Tree {
	clean := make([]string, 0, len(paths))
	for _, p := range paths {
		p = strings.Trim(path.Clean(strings.ReplaceAll(p, "\\", "/")), "/")
		if p == "." {
			p = ""
		}
		clean = append(clean, p)
	}
	return &Tree{Local: local, Paths: clean}
}

// NewSnapshot copies a selected tree so every model batch reads the same bytes.
func NewSnapshot(local *Local, files map[string][]byte) *Tree {
	tree := NewTree(local, nil)
	tree.Snapshot = make(map[string][]byte, len(files))
	for name, content := range files {
		tree.Snapshot[name] = bytes.Clone(content)
	}
	return tree
}

// FileContent reads the frozen source when this provider owns a snapshot.
func (t *Tree) FileContent(ctx context.Context, ref Ref, name string) ([]byte, error) {
	if t.Snapshot == nil {
		return t.Local.FileContent(ctx, ref, name)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	content, ok := t.Snapshot[name]
	if !ok {
		return nil, ErrNotFound
	}
	return bytes.Clone(content), nil
}

// ListDir lists the snapshot when present, otherwise the live checkout.
func (t *Tree) ListDir(ctx context.Context, ref Ref, dir string) ([]string, error) {
	if t.Snapshot == nil {
		return t.Local.ListDir(ctx, ref, dir)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if dir != "" {
		dir = strings.TrimSuffix(dir, "/") + "/"
	}
	seen := map[string]bool{}
	for name := range t.Snapshot {
		if !strings.HasPrefix(name, dir) {
			continue
		}
		rest := strings.TrimPrefix(name, dir)
		if slash := strings.IndexByte(rest, '/'); slash >= 0 {
			rest = rest[:slash+1]
		}
		if rest != "" {
			seen[rest] = true
		}
	}
	var entries []string
	for name := range seen {
		entries = append(entries, name)
	}
	sort.Strings(entries)
	return entries, nil
}

// Name identifies the provider in logs and the review header.
func (t *Tree) Name() string { return "tree" }

// PullRequest describes the tree review to the model as what it is, so the
// summary it writes is about a repository rather than about a change: with
// no title and body the summarizer reports that no change was supplied.
func (t *Tree) PullRequest(ctx context.Context, ref Ref) (*PullRequest, error) {
	pr, err := t.Local.PullRequest(ctx, ref)
	if err != nil {
		// A tree review reads files, not history: a repository with no
		// commits yet still lists its files, and the review never needs
		// a SHA.
		if _, rpErr := t.revParse(ctx, "HEAD"); rpErr == nil {
			return nil, err
		}
		pr = &PullRequest{HeadRef: Worktree}
	}
	scope := "the whole repository"
	if len(t.Paths) > 0 {
		scope = "the paths " + strings.Join(t.Paths, ", ")
	}
	pr.Title = "Full review of " + scope
	pr.Body = "This is not a change. Every file under review is shown in full and marked as added because the whole of it is under review; nothing was modified. Judge each file as it stands, and write the summary as a description of the repository's state, not of a diff."
	return pr, nil
}

// Diff lists the tree and renders every included file as an addition.
func (t *Tree) Diff(ctx context.Context, ref Ref) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if ref.Head != Worktree {
		return nil, fmt.Errorf("a tree review reads the working tree; a revision cannot be reviewed whole")
	}
	if t.captured {
		return bytes.Clone(t.capturedDiff), nil
	}
	names, err := t.list(ctx)
	if err != nil {
		return nil, err
	}

	// A path argument that names nothing git lists is a mistake (an
	// absolute path, a glob, a typo, a path relative to somewhere else),
	// and the review it would produce is a successful-looking review of
	// nothing. Said before any file is read.
	matched := make([]bool, len(t.Paths))
	for _, name := range names {
		for i, p := range t.Paths {
			if p == "" || name == p || strings.HasPrefix(name, p+"/") {
				matched[i] = true
			}
		}
	}
	for i, p := range t.Paths {
		if !matched[i] {
			return nil, fmt.Errorf("path %q matches no file git lists under the repository; paths are relative to the repository root, without globs", p)
		}
	}

	t.Covered, t.Unbudgeted, t.Skipped = nil, nil, nil
	t.Lines = map[string]int{}
	var out bytes.Buffer
	captured := map[string][]byte{}
	capture := t.Capture && t.Snapshot == nil
	spent := 0
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !t.wanted(name) {
			continue
		}
		if t.Budget > 0 && spent >= t.Budget {
			t.Unbudgeted = append(t.Unbudgeted, name)
			continue
		}
		// Avoid loading oversized files from disk; snapshot bytes are already held.
		if t.Snapshot == nil {
			if info, err := os.Stat(filepath.Join(t.Dir, filepath.FromSlash(name))); err == nil && t.MaxBytes > 0 && info.Size() > int64(t.MaxBytes) {
				reason := fmt.Sprintf("larger than %d bytes", t.MaxBytes)
				if prefix, err := t.readContainedLimit(name, 4096); err == nil && bytes.ContainsRune(prefix, 0) {
					reason = "binary"
				}
				t.Skipped = append(t.Skipped, TreeSkip{Path: name, Reason: reason})
				continue
			}
		}
		content, err := t.FileContent(ctx, ref, name)
		if err != nil {
			t.Skipped = append(t.Skipped, TreeSkip{Path: name, Reason: "unreadable: " + err.Error()})
			continue
		}
		if len(content) == 0 {
			t.Skipped = append(t.Skipped, TreeSkip{Path: name, Reason: "empty"})
			continue
		}
		if !isText(content) {
			t.Skipped = append(t.Skipped, TreeSkip{Path: name, Reason: "binary"})
			continue
		}
		if t.MaxBytes > 0 && len(content) > t.MaxBytes {
			t.Skipped = append(t.Skipped, TreeSkip{Path: name, Reason: fmt.Sprintf("larger than %d bytes", t.MaxBytes)})
			continue
		}
		spent += len(content) / 4
		t.Covered = append(t.Covered, name)
		if capture {
			captured[name] = bytes.Clone(content)
		}
		t.Lines[name] = len(strings.Split(strings.TrimSuffix(string(content), "\n"), "\n"))
		writeAddition(&out, name, content)
	}
	if capture {
		t.Snapshot = captured
		t.captured, t.capturedDiff = true, bytes.Clone(out.Bytes())
	}
	return out.Bytes(), nil
}

// list names the files git sees in the tree: tracked, plus untracked files
// that are not ignored, in path order.
func (t *Tree) list(ctx context.Context) ([]string, error) {
	if t.Snapshot != nil {
		var names []string
		for name := range t.Snapshot {
			names = append(names, name)
		}
		sort.Strings(names)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return names, nil
	}
	raw, err := t.gitRaw(ctx, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return nil, fmt.Errorf("list the tree: %w", err)
	}
	seen := map[string]bool{}
	var names []string
	for name := range strings.SplitSeq(string(raw), "\x00") {
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

func (t *Tree) wanted(name string) bool {
	if len(t.Paths) == 0 {
		return true
	}
	for _, p := range t.Paths {
		if p == "" || name == p || strings.HasPrefix(name, p+"/") {
			return true
		}
	}
	return false
}

// isText refuses what a model cannot read: a NUL byte, or bytes that are not
// UTF-8.
func isText(b []byte) bool {
	return !bytes.ContainsRune(b, 0) && utf8.Valid(b)
}

// writeAddition renders content as a unified diff adding name.
func writeAddition(out *bytes.Buffer, name string, content []byte) {
	text := string(content)
	trailing := strings.HasSuffix(text, "\n")
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	fmt.Fprintf(out, "diff --git a/%s b/%s\nnew file mode 100644\n--- /dev/null\n+++ b/%s\n@@ -0,0 +1,%d @@\n", name, name, name, len(lines))
	for _, line := range lines {
		out.WriteString("+")
		out.WriteString(line)
		out.WriteString("\n")
	}
	if !trailing {
		out.WriteString("\\ No newline at end of file\n")
	}
}
