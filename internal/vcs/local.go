package vcs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Local reviews a git checkout without contacting any forge. Reviews are
// rendered to a writer instead of being posted, which makes the whole engine
// runnable with no credentials — the fast path for development and for anyone
// evaluating the tool before wiring it into CI.
type Local struct {
	// Dir is the repository root.
	Dir string

	// Out receives rendered reviews.
	Out io.Writer

	// Renderer formats a review for Out. When nil, a plain text rendering is
	// used.
	Renderer func(Review) string
}

// NewLocal returns a Local provider rooted at dir.
func NewLocal(dir string, out io.Writer) *Local {
	return &Local{Dir: dir, Out: out}
}

// Name identifies the provider.
func (l *Local) Name() string { return "local" }

// PullRequest synthesizes metadata from the revision range. There is no pull
// request title or body locally, and inventing one would mislead the model
// about author intent, so both are left empty.
func (l *Local) PullRequest(ctx context.Context, ref Ref) (*PullRequest, error) {
	head, err := l.revParse(ctx, refOr(ref.Head, "HEAD"))
	if err != nil {
		return nil, err
	}

	out := &PullRequest{
		BaseRef: ref.Base,
		HeadRef: refOr(ref.Head, "HEAD"),
		HeadSHA: head,
	}

	// For a working-tree review the changes are uncommitted, so HEAD's commit
	// message describes the PREVIOUS change, not this one. Passing it through
	// primes the reviewer with a description that contradicts the diff — every
	// finding then gets framed against the wrong intent.
	if ref.Head == Worktree {
		return out, nil
	}

	title, err := l.git(ctx, "log", "-1", "--pretty=%s", head)
	if err != nil {
		return nil, err
	}
	body, err := l.git(ctx, "log", "-1", "--pretty=%b", head)
	if err != nil {
		return nil, err
	}
	author, err := l.git(ctx, "log", "-1", "--pretty=%an", head)
	if err != nil {
		return nil, err
	}

	out.Title = strings.TrimSpace(title)
	out.Body = strings.TrimSpace(body)
	out.Author = strings.TrimSpace(author)

	return out, nil
}

// Worktree is the Head value meaning "the working tree as it is on disk",
// including uncommitted edits. It is the default for local reviews, because
// reviewing your work before committing is the point of running locally.
const Worktree = ""

// Diff returns the unified diff for the ref's range.
//
// For a committed range the three-dot form is deliberate: it diffs head against
// the merge base, which is what a pull request shows. Two-dot would also
// surface changes that landed on the base branch after the fork point, and
// attributing those to the author is both wrong and infuriating.
func (l *Local) Diff(ctx context.Context, ref Ref) ([]byte, error) {
	args := []string{"diff", "--no-color", "--no-ext-diff", "--find-renames"}

	switch {
	case ref.Head == Worktree && ref.Base == "":
		// Everything not yet committed, staged or not.
		args = append(args, "HEAD")
	case ref.Head == Worktree:
		// Working tree against an explicit base.
		args = append(args, ref.Base)
	case ref.Base == "":
		args = append(args, ref.Head)
	default:
		args = append(args, ref.Base+"..."+ref.Head)
	}

	return l.gitRaw(ctx, args...)
}

// BaseRevision resolves the revision the ref's diff was computed against.
//
// Every case below mirrors a case in Diff, and that correspondence is the whole
// point: a caller reading a file "as it was before this change" has to read it
// at the same revision the diff subtracted, or the two disagree about what the
// change did.
func (l *Local) BaseRevision(ctx context.Context, ref Ref) (string, error) {
	switch {
	case ref.Head == Worktree && ref.Base == "":
		return l.revParse(ctx, "HEAD")
	case ref.Head == Worktree:
		return l.revParse(ctx, ref.Base)
	case ref.Base == "":
		// `git diff <head>` compares the working tree against head, so head is
		// what these changes were measured against.
		return l.revParse(ctx, ref.Head)
	default:
		return l.mergeBase(ctx, ref.Base, ref.Head)
	}
}

// mergeBase resolves the fork point, matching the three-dot range Diff uses.
//
// Unrelated histories and shallow clones have no merge base. The named base is
// still a revision this change did not author — the property a caller is after
// — so it is used rather than failing the resolution outright.
func (l *Local) mergeBase(ctx context.Context, base, head string) (string, error) {
	out, err := l.git(ctx, "merge-base", base, head)
	if err != nil {
		return l.revParse(ctx, base)
	}
	return strings.TrimSpace(out), nil
}

// FileContent reads a file at the ref's head. For a working-tree review the
// file is read from disk, so uncommitted edits are reviewed as they actually
// are rather than as they were last committed.
//
// Working-tree reads are confined to the repository. The reviewed branch is
// frequently untrusted — `gh pr checkout` on an outside contributor's PR is the
// documented workflow — and its diff decides which paths get read and sent to
// the model. Without containment, a committed symlink such as
// `notes.md -> ~/.ssh/id_rsa` would put a private key in the prompt.
func (l *Local) FileContent(ctx context.Context, ref Ref, path string) ([]byte, error) {
	if ref.Head == Worktree {
		return l.readContained(path)
	}

	out, err := l.gitRaw(ctx, "show", ref.Head+":"+path)
	if err != nil {
		// git reports a missing path at a revision on stderr; that is a
		// missing file, not a broken repository.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, fmt.Errorf("%s at %s: %w", path, ref.Head, ErrNotFound)
		}
		return nil, err
	}
	return out, nil
}

// readContained reads a repository-relative path, refusing anything that
// escapes the repository root.
//
// os.Root confines traversal to the root for us, including `..` segments and
// symlinks that point outside. The explicit Lstat is belt-and-braces: an
// in-repo symlink to an in-repo file would be permitted by os.Root, but there
// is no reason for a reviewer to follow links at all, and refusing them keeps
// the rule easy to state.
func (l *Local) readContained(path string) ([]byte, error) {
	clean := filepath.FromSlash(strings.TrimPrefix(path, "/"))

	root, err := os.OpenRoot(l.Dir)
	if err != nil {
		return nil, fmt.Errorf("open repository root: %w", err)
	}
	defer func() { _ = root.Close() }()

	// Lstat does not follow the final symlink, so this detects a link rather
	// than reading through it.
	info, err := root.Lstat(clean)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%s: %w", path, ErrNotFound)
		}
		// os.Root reports an escape as a generic path error; treat any failure
		// to stat inside the root as "not available" rather than leaking the
		// reason, which could confirm the existence of a file outside.
		return nil, fmt.Errorf("%s: %w", path, ErrNotFound)
	}

	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%s is a symlink: %w", path, ErrNotFound)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file: %w", path, ErrNotFound)
	}

	f, err := root.Open(clean)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, ErrNotFound)
	}
	defer func() { _ = f.Close() }()

	return io.ReadAll(f)
}

// PublishReview writes the review to Out. Nothing is sent anywhere.
func (l *Local) PublishReview(_ context.Context, _ Ref, review Review) error {
	if l.Out == nil {
		return nil
	}

	render := l.Renderer
	if render == nil {
		render = renderText
	}

	_, err := io.WriteString(l.Out, render(review))
	return err
}

// renderText is the default local rendering: a summary followed by inline
// comments as path:line, which terminals and editors turn into links.
func renderText(r Review) string {
	var b strings.Builder

	if r.Summary != "" {
		b.WriteString(r.Summary)
		b.WriteString("\n\n")
	}

	for _, c := range r.Comments {
		fmt.Fprintf(&b, "%s:%d\n", c.Path, c.Line)

		for _, line := range strings.Split(strings.TrimRight(c.Body, "\n"), "\n") {
			b.WriteString("  ")
			b.WriteString(line)
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}

	return b.String()
}

// revParse resolves a revision to a SHA.
func (l *Local) revParse(ctx context.Context, rev string) (string, error) {
	out, err := l.git(ctx, "rev-parse", rev)
	if err != nil {
		return "", fmt.Errorf("resolve %q: %w", rev, err)
	}
	return strings.TrimSpace(out), nil
}

// git runs a git command and returns trimmed stdout.
func (l *Local) git(ctx context.Context, args ...string) (string, error) {
	out, err := l.gitRaw(ctx, args...)
	return string(out), err
}

// gitRaw runs a git command and returns stdout verbatim, which matters for
// diffs and file contents where trailing bytes are significant.
func (l *Local) gitRaw(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = l.Dir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		}
		return nil, fmt.Errorf("git %s: %s: %w", strings.Join(args, " "), msg, err)
	}
	return stdout.Bytes(), nil
}

// refOr returns ref, or fallback when ref is empty.
func refOr(ref, fallback string) string {
	if strings.TrimSpace(ref) == "" {
		return fallback
	}
	return ref
}
