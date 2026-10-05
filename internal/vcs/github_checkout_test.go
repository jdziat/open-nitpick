package vcs

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

// countingGitHub serves one head commit and counts what it was asked for.
func countingGitHub(t *testing.T, sha string, pulls, contents *atomic.Int64) *GitHub {
	t.Helper()
	return newFakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/pulls/7"):
			pulls.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"number": 7, "head": map[string]any{"sha": sha}})
		case strings.Contains(r.URL.Path, "/contents/"):
			contents.Add(1)
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	})
}

func headOf(t *testing.T, dir string) string {
	t.Helper()
	return strings.TrimSpace(gitIn(t, dir, "rev-parse", "HEAD"))
}

// The checkout answers reads of the commit under review, so a repository's
// size no longer sets the number of requests. The pull request is asked for
// once however many files follow.
func TestGitHubReadsTheReviewedCommitFromTheCheckout(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "pkg/b.go", "package pkg\n")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-qm", "second")
	var pulls, contents atomic.Int64
	gh := countingGitHub(t, headOf(t, dir), &pulls, &contents)
	gh.Checkout = dir

	for range 5 {
		got, err := gh.FileContent(context.Background(), testRef(), "pkg/b.go")
		if err != nil || string(got) != "package pkg\n" {
			t.Fatalf("FileContent = %q, %v", got, err)
		}
	}
	names, err := gh.ListDir(context.Background(), testRef(), "")
	if err != nil || !slices.Equal(names, []string{"a.go", "pkg/"}) {
		t.Fatalf("ListDir = %v, %v", names, err)
	}
	if pulls.Load() != 1 || contents.Load() != 0 {
		t.Fatalf("pull request asked %d times and contents %d times, want 1 and 0", pulls.Load(), contents.Load())
	}
}

// The commit is read from git objects. A checkout with something else out, or
// an edited file, must not change what is reviewed.
func TestGitHubCheckoutReadsIgnoreTheWorkingTree(t *testing.T) {
	dir := newRepo(t)
	sha := headOf(t, dir)
	write(t, dir, "a.go", "package edited\n")
	write(t, dir, "untracked.go", "package untracked\n")
	var pulls, contents atomic.Int64
	gh := countingGitHub(t, sha, &pulls, &contents)
	gh.Checkout = dir

	got, err := gh.FileContent(context.Background(), testRef(), "a.go")
	if err != nil || strings.Contains(string(got), "edited") {
		t.Fatalf("read %q, %v: the working tree leaked into the review", got, err)
	}
	names, err := gh.ListDir(context.Background(), testRef(), "")
	if err != nil || contents.Load() != 0 {
		t.Fatalf("ListDir = %v, %v after %d API reads: the checkout did not answer, so the isolation below proves nothing", names, err, contents.Load())
	}
	if slices.Contains(names, "untracked.go") {
		t.Fatalf("ListDir = %v, want only what the commit holds", names)
	}
}

// A git failure after the commit was found may be transient, so it must not be
// remembered. The tree object is hidden for one read and put back; the second
// read has to come from the checkout, not stay on the API for the rest of the
// run.
func TestGitHubRetriesTheCheckoutAfterATransientFailure(t *testing.T) {
	dir := newRepo(t)
	sha := headOf(t, dir)
	tree := strings.TrimSpace(gitIn(t, dir, "rev-parse", sha+"^{tree}"))
	object := filepath.Join(dir, ".git", "objects", tree[:2], tree[2:])
	hidden := object + ".hidden"
	if err := os.Rename(object, hidden); err != nil {
		t.Skipf("tree object is not loose: %v", err)
	}
	var pulls, contents atomic.Int64
	gh := countingGitHub(t, sha, &pulls, &contents)
	gh.Checkout = dir

	_, _ = gh.ListDir(context.Background(), testRef(), "")
	if contents.Load() != 1 {
		t.Fatalf("API asked %d times while the checkout was broken, want 1", contents.Load())
	}
	if err := os.Rename(hidden, object); err != nil {
		t.Fatal(err)
	}
	names, err := gh.ListDir(context.Background(), testRef(), "")
	if err != nil || !slices.Equal(names, []string{"a.go"}) || contents.Load() != 1 {
		t.Fatalf("ListDir = %v, %v after %d API reads, want the recovered checkout to answer", names, err, contents.Load())
	}
}

// A checkout that lacks the commit, or a file the checkout cannot serve as
// source, goes to the API as before.
func TestGitHubFallsBackToTheAPIWhenTheCheckoutCannotAnswer(t *testing.T) {
	dir := newRepo(t)
	if err := os.Symlink("a.go", filepath.Join(dir, "link.go")); err != nil {
		t.Skip("symlinks unavailable")
	}
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-qm", "link")

	var pulls, contents atomic.Int64
	gh := countingGitHub(t, headOf(t, dir), &pulls, &contents)
	gh.Checkout = dir
	if _, err := gh.FileContent(context.Background(), testRef(), "link.go"); err == nil {
		t.Fatal("a symlink was served as source")
	}
	if contents.Load() != 1 {
		t.Fatalf("contents asked %d times for a symlink, want 1: the API must refuse it", contents.Load())
	}

	var pulls2, contents2 atomic.Int64
	absent := countingGitHub(t, strings.Repeat("0", 40), &pulls2, &contents2)
	absent.Checkout = dir
	_, _ = absent.FileContent(context.Background(), testRef(), "a.go")
	_, _ = absent.ListDir(context.Background(), testRef(), "")
	if contents2.Load() != 2 {
		t.Fatalf("contents asked %d times with a commit the checkout lacks, want 2", contents2.Load())
	}
}

// The contents API types a symlink as a file, measured against git/git's
// RelNotes. A directory must list the same names whichever side answers.
func TestGitHubListsASymlinkWhetherTheCheckoutOrTheAPIAnswers(t *testing.T) {
	dir := newRepo(t)
	if err := os.Symlink("a.go", filepath.Join(dir, "link.go")); err != nil {
		t.Skip("symlinks unavailable")
	}
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-qm", "link")
	var pulls, contents atomic.Int64
	gh := countingGitHub(t, headOf(t, dir), &pulls, &contents)
	gh.Checkout = dir

	names, err := gh.ListDir(context.Background(), testRef(), "")
	if err != nil || !slices.Equal(names, []string{"a.go", "link.go"}) {
		t.Fatalf("ListDir = %v, %v, want the symlink listed beside its target", names, err)
	}
}

// The contents API types a submodule as a file, measured against apache/arrow's
// cpp/submodules/parquet-testing. The checkout must list it and must not claim
// to read it, since there is no blob behind it.
func TestGitHubListsASubmoduleButDoesNotReadIt(t *testing.T) {
	dir := newRepo(t)
	gitIn(t, dir, "update-index", "--add", "--cacheinfo", "160000,"+headOf(t, dir)+",vendor")
	gitIn(t, dir, "commit", "-qm", "submodule")
	var pulls, contents atomic.Int64
	gh := countingGitHub(t, headOf(t, dir), &pulls, &contents)
	gh.Checkout = dir

	names, err := gh.ListDir(context.Background(), testRef(), "")
	if err != nil || !slices.Equal(names, []string{"a.go", "vendor"}) {
		t.Fatalf("ListDir = %v, %v, want the submodule listed as a file", names, err)
	}
	if _, err := gh.FileContent(context.Background(), testRef(), "vendor"); err == nil || contents.Load() != 1 {
		t.Fatalf("FileContent = %v after %d API reads, want the API to answer and refuse", err, contents.Load())
	}
}
