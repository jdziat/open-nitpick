package main

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/jdziat/open-nitpick/internal/converse"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

type fakeThreads struct {
	thread   []vcs.ThreadComment
	resolved bool
}

func (f fakeThreads) ThreadComments(context.Context, vcs.Ref, int64) ([]vcs.ThreadComment, error) {
	return f.thread, nil
}

func (f fakeThreads) ThreadResolved(context.Context, vcs.Ref, int64) (bool, error) {
	return f.resolved, nil
}

const findingBody = "**Warning**\n\nx\n<!-- open-nitpick fp:aa11 class:correctness -->"

func replyEvent() *converse.Event {
	return &converse.Event{CommentID: 2, RootID: 1, Inline: true, Path: "a.go", Line: 3, Body: "intentional, see the RFC"}
}

func lookup(t *testing.T, f fakeThreads) *replyTarget {
	t.Helper()
	got, err := findingBeingAnswered(context.Background(), f, vcs.Ref{Owner: "o", Repo: "r", Number: 1},
		replyEvent(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestAReplyUnderOneOfOurFindingsIsRead(t *testing.T) {
	got := lookup(t, fakeThreads{thread: []vcs.ThreadComment{
		{ID: 1, Own: true, Body: findingBody}, {ID: 2, Author: "alice", Body: "intentional"},
	}})
	if got == nil || got.root.ID != 1 {
		t.Fatalf("target = %+v, want the thread rooted at 1", got)
	}
}

func TestAReplyIsIgnoredWhenTheThreadIsNotOurFinding(t *testing.T) {
	cases := map[string]fakeThreads{
		"a person's thread": {thread: []vcs.ThreadComment{{ID: 1, Author: "bob", Body: "nit"}, {ID: 2, Author: "alice"}}},
		// Ours, but an answer or a summary line: no fingerprint, so not a finding.
		"our answer": {thread: []vcs.ThreadComment{{ID: 1, Own: true, Body: "Because of X."}, {ID: 2}}},
		// A person pasting a fingerprint does not make their comment a finding.
		"a pasted marker":  {thread: []vcs.ThreadComment{{ID: 1, Author: "bob", Body: findingBody}, {ID: 2}}},
		"already resolved": {resolved: true, thread: []vcs.ThreadComment{{ID: 1, Own: true, Body: findingBody}, {ID: 2}}},
		"root missing":     {thread: []vcs.ThreadComment{{ID: 2}}},
	}
	for name, f := range cases {
		if got := lookup(t, f); got != nil {
			t.Errorf("%s: a reply was taken up: %+v", name, got)
		}
	}
}
