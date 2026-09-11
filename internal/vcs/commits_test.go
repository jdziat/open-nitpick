package vcs

import (
	"context"
	"path/filepath"
	"testing"
)

func TestCommitRangeIncludesSideBranchesAndRealParentCounts(t *testing.T) {
	dir := newRepo(t)
	base := gitIn(t, dir, "rev-parse", "HEAD")
	gitIn(t, dir, "checkout", "-qb", "side")
	gitIn(t, dir, "commit", "--allow-empty", "-qm", "fix: side")
	gitIn(t, dir, "checkout", "-q", "main")
	gitIn(t, dir, "commit", "--allow-empty", "-qm", "Merge spoof")
	gitIn(t, dir, "merge", "--no-ff", "-qm", "Merge side", "side")
	r, err := NewLocal(dir, nil).Commits(context.Background(), base, "HEAD")
	if err != nil || len(r.Commits) != 3 || r.BaseSHA != base || r.HeadSHA == "" {
		t.Fatalf("range=%+v err=%v", r, err)
	}
	parents := map[string]int{}
	for _, c := range r.Commits {
		parents[c.Subject] = c.ParentCount
	}
	if parents["Merge side"] != 2 || parents["Merge spoof"] != 1 || parents["fix: side"] != 1 {
		t.Fatalf("wrong topology: %v", parents)
	}
}

func TestCommitRangeDistinguishesEmptyRangeFromUnavailableHistory(t *testing.T) {
	dir := newRepo(t)
	l := NewLocal(dir, nil)
	r, err := l.Commits(context.Background(), "HEAD", "HEAD")
	if err != nil || len(r.Commits) != 0 || r.BaseSHA == "" || r.HeadSHA != r.BaseSHA {
		t.Fatalf("empty range: %+v %v", r, err)
	}
	for _, base := range []string{"", "missing", "--all", "HEAD:a.go"} {
		if _, err := l.Commits(context.Background(), base, "HEAD"); err == nil {
			t.Errorf("invalid base %q passed", base)
		}
	}
	shallow := filepath.Join(t.TempDir(), "shallow")
	gitIn(t, dir, "clone", "-q", "--depth=1", "file://"+dir, shallow)
	if _, err := NewLocal(shallow, nil).Commits(context.Background(), "HEAD", "HEAD"); err == nil {
		t.Fatal("shallow history was silently accepted")
	}
}

func TestCommitSubjectsPreserveUnicodeAndDoNotIncludeBodies(t *testing.T) {
	dir := newRepo(t)
	gitIn(t, dir, "commit", "--allow-empty", "-qm", "feat: café", "-m", "body\nwith extra lines")
	r, err := NewLocal(dir, nil).Commits(context.Background(), "HEAD^", "HEAD")
	if err != nil || len(r.Commits) != 1 || r.Commits[0].Subject != "feat: café" {
		t.Fatalf("range=%+v err=%v", r, err)
	}
}
