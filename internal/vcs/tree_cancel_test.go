package vcs

import (
	"context"
	"errors"
	"io"
	"testing"
)

// A cancelled context makes every git call fail, including the HEAD probe, and
// that failure is not evidence of an unborn repository. Treating it as one
// returned a normal-looking tree review for a run that had been stopped.
func TestTreePullRequestReportsCancellationNotAnUnbornHead(t *testing.T) {
	for name, dir := range map[string]string{"committed": newRepo(t), "unborn": unbornRepo(t)} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			tree := NewTree(NewLocal(dir, io.Discard), nil)
			pr, err := tree.PullRequest(ctx, Ref{Head: Worktree})
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("PullRequest err = %v (pr %+v), want context.Canceled", err, pr)
			}
		})
	}
}

func unbornRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	write(t, dir, "a.go", "package a\n")
	return dir
}
