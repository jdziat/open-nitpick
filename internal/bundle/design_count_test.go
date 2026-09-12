package bundle

import (
	"testing"

	"github.com/jdziat/open-nitpick/internal/diff"
)

func TestDesignFileCountExcludesContextAndRepeatedPaths(t *testing.T) {
	changed := Entry{File: &diff.File{Path: "changed.go"}}
	context := Entry{File: &diff.File{Path: "caller.go"}, SourceOnly: true}
	plan := &Plan{Batches: []Batch{{DesignTask: "first", Entries: []Entry{changed, context}}, {DesignTask: "second", Entries: []Entry{changed, context}}}}
	if got := plan.Files(); got != 1 {
		t.Fatalf("context and duplicate appearances inflated changed files: %d", got)
	}
	plan.Batches = []Batch{{Entries: []Entry{changed, context}}}
	if got := plan.Files(); got != 2 {
		t.Fatalf("ordinary batch count changed: %d", got)
	}
}
