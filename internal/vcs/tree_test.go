package vcs

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/diff"
)

func TestTreeDiffAddsEveryFileUnderThePathsAndSaysWhatItLeftOut(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "pkg/b.go", "package pkg\n\nfunc G() {}\n")
	write(t, dir, "pkg/untracked.py", "print(1)") // no trailing newline, untracked
	write(t, dir, "pkg/blob.bin", "\x00\x01\x02")
	write(t, dir, "pkg/empty.txt", "")
	write(t, dir, "docs/ignored.log", "x\n")
	write(t, dir, ".gitignore", "*.log\n")

	tree := NewTree(NewLocal(dir, io.Discard), []string{"pkg"})
	raw, err := tree.Diff(context.Background(), Ref{Head: Worktree})
	if err != nil {
		t.Fatal(err)
	}
	files, err := diff.Parse(raw)
	if err != nil {
		t.Fatalf("the synthesized diff does not parse: %v\n%s", err, raw)
	}
	if got := strings.Join(files.Paths(), ","); got != "pkg/b.go,pkg/untracked.py" {
		t.Fatalf("reviewed %s", got)
	}
	for _, f := range files {
		if f.Kind != diff.ChangeAdded || len(f.ChangedLines()) == 0 {
			t.Errorf("%s: kind %s with %d changed lines", f.Path, f.Kind, len(f.ChangedLines()))
		}
	}
	if got := strings.Join(tree.Covered, ","); got != "pkg/b.go,pkg/untracked.py" {
		t.Errorf("covered %s", got)
	}
	reasons := map[string]string{}
	for _, s := range tree.Skipped {
		reasons[s.Path] = s.Reason
	}
	if reasons["pkg/blob.bin"] != "binary" || reasons["pkg/empty.txt"] != "empty" {
		t.Errorf("skips = %v", reasons)
	}
	if _, listed := reasons["docs/ignored.log"]; listed {
		t.Errorf("an ignored file outside the paths was considered")
	}

	// A revision cannot be reviewed whole; only the working tree can.
	if _, err := tree.Diff(context.Background(), Ref{Head: "HEAD"}); err == nil {
		t.Error("a tree diff of a revision did not fail")
	}
}

func TestTreeBudgetStopsInPathOrderAndNamesTheRest(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "b.go", "package b\n"+strings.Repeat("// padding line\n", 50))
	write(t, dir, "c.go", "package c\n")

	tree := NewTree(NewLocal(dir, io.Discard), nil)
	tree.Budget = 100 // tokens: a.go (~10) fits, b.go (~200) fits as the file that crosses, c.go does not
	if _, err := tree.Diff(context.Background(), Ref{Head: Worktree}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(tree.Covered, ","); got != "a.go,b.go" {
		t.Errorf("covered %s", got)
	}
	if got := strings.Join(tree.Unbudgeted, ","); got != "c.go" {
		t.Errorf("unbudgeted %s", got)
	}
}
