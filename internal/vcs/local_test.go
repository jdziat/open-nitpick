package vcs

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// newRepo creates a git repository with one commit and returns its path.
func newRepo(t *testing.T) string {
	t.Helper()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()

		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=t@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}

	write(t, dir, "a.go", "package a\n\nfunc F() int {\n\treturn 1\n}\n")
	run("init", "-q", "-b", "main")
	run("add", "-A")
	run("commit", "-qm", "initial")

	return dir
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()

	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLocalDiffOfWorkingTree(t *testing.T) {
	dir := newRepo(t)
	// An uncommitted edit is exactly what a developer wants reviewed before
	// they commit, so it must show up with no base or head specified.
	write(t, dir, "a.go", "package a\n\nfunc F() int {\n\treturn 2\n}\n")

	local := NewLocal(dir, nil)

	got, err := local.Diff(context.Background(), Ref{})
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !bytes.Contains(got, []byte("-\treturn 1")) || !bytes.Contains(got, []byte("+\treturn 2")) {
		t.Errorf("diff should contain the uncommitted change:\n%s", got)
	}
}

func TestLocalFileContentReadsWorkingTree(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "a.go", "package a\n// edited\n")

	local := NewLocal(dir, nil)

	got, err := local.FileContent(context.Background(), Ref{}, "a.go")
	if err != nil {
		t.Fatalf("FileContent: %v", err)
	}
	// Reading the committed version here would review code the author has
	// already changed.
	if !strings.Contains(string(got), "// edited") {
		t.Errorf("content = %q, want the uncommitted version", got)
	}
}

func TestLocalFileContentAtRevision(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "a.go", "package a\n// edited\n")

	local := NewLocal(dir, nil)

	got, err := local.FileContent(context.Background(), Ref{Head: "HEAD"}, "a.go")
	if err != nil {
		t.Fatalf("FileContent: %v", err)
	}
	if strings.Contains(string(got), "// edited") {
		t.Error("an explicit revision should read the committed version")
	}
}

func TestLocalFileContentMissing(t *testing.T) {
	local := NewLocal(newRepo(t), nil)

	_, err := local.FileContent(context.Background(), Ref{}, "nope.go")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestLocalFileContentMissingAtRevision(t *testing.T) {
	local := NewLocal(newRepo(t), nil)

	_, err := local.FileContent(context.Background(), Ref{Head: "HEAD"}, "nope.go")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestLocalPullRequestUsesCommitMetadata(t *testing.T) {
	local := NewLocal(newRepo(t), nil)

	// An explicit committed revision does describe the change under review.
	pr, err := local.PullRequest(context.Background(), Ref{Head: "HEAD"})
	if err != nil {
		t.Fatalf("PullRequest: %v", err)
	}
	if pr.Title != "initial" {
		t.Errorf("Title = %q, want the head commit subject", pr.Title)
	}
	if pr.Author != "Test" {
		t.Errorf("Author = %q", pr.Author)
	}
	if pr.HeadSHA == "" {
		t.Error("HeadSHA should be resolved")
	}
}

// TestLocalWorktreeHasNoTitle is the regression test for priming the reviewer
// with the wrong change.
//
// For a working-tree review the changes are uncommitted, so HEAD's commit
// message describes the PREVIOUS change. Passing it through made every local
// review open with "This change initializes a new project" regardless of what
// the diff actually did.
func TestLocalWorktreeHasNoTitle(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "a.go", "package a\n\nfunc F() int {\n\treturn 2\n}\n")

	local := NewLocal(dir, nil)

	pr, err := local.PullRequest(context.Background(), Ref{})
	if err != nil {
		t.Fatalf("PullRequest: %v", err)
	}
	if pr.Title != "" {
		t.Errorf("Title = %q, want empty: the last commit does not describe uncommitted work", pr.Title)
	}
	if pr.Body != "" {
		t.Errorf("Body = %q, want empty", pr.Body)
	}
	// The revision is still resolved, since it is genuine information.
	if pr.HeadSHA == "" {
		t.Error("HeadSHA should still be resolved")
	}
}

func TestLocalPublishReviewWritesToOut(t *testing.T) {
	var out bytes.Buffer
	local := NewLocal(newRepo(t), &out)

	err := local.PublishReview(context.Background(), Ref{}, Review{
		Summary: "One issue found.",
		Comments: []Comment{
			{Path: "a.go", Line: 4, Side: "RIGHT", Body: "This returns the wrong value.\nConsider returning 2."},
		},
	})
	if err != nil {
		t.Fatalf("PublishReview: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "One issue found.") {
		t.Error("summary should be rendered")
	}
	// path:line is what makes terminal output clickable.
	if !strings.Contains(got, "a.go:4") {
		t.Errorf("comment should be rendered as path:line, got:\n%s", got)
	}
	if !strings.Contains(got, "  Consider returning 2.") {
		t.Errorf("multi-line bodies should stay indented, got:\n%s", got)
	}
}

func TestLocalPublishReviewWithNilOutIsSafe(t *testing.T) {
	local := NewLocal(newRepo(t), nil)

	if err := local.PublishReview(context.Background(), Ref{}, Review{Summary: "x"}); err != nil {
		t.Fatalf("publishing with no writer should be a no-op: %v", err)
	}
}

func TestLocalDiffAgainstBaseUsesMergeBase(t *testing.T) {
	dir := newRepo(t)

	run := func(args ...string) {
		t.Helper()

		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=t@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}

	// Branch off, change one file, then advance main separately. A two-dot
	// diff would attribute main's change to this branch.
	run("checkout", "-qb", "feature")
	write(t, dir, "a.go", "package a\n\nfunc F() int {\n\treturn 2\n}\n")
	run("add", "-A")
	run("commit", "-qm", "feature change")

	run("checkout", "-q", "main")
	write(t, dir, "other.go", "package a\n\nvar Unrelated = true\n")
	run("add", "-A")
	run("commit", "-qm", "unrelated main change")

	local := NewLocal(dir, nil)

	got, err := local.Diff(context.Background(), Ref{Base: "main", Head: "feature"})
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !bytes.Contains(got, []byte("a.go")) {
		t.Errorf("diff should include the branch's own change:\n%s", got)
	}
	if bytes.Contains(got, []byte("other.go")) {
		t.Errorf("three-dot diff must not attribute the base branch's change to this branch:\n%s", got)
	}
}

func TestLocalName(t *testing.T) {
	if got := NewLocal(".", nil).Name(); got != "local" {
		t.Errorf("Name = %q, want local", got)
	}
}
