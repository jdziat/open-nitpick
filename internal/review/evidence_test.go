package review

import (
	"context"
	"testing"

	"github.com/jdziat/open-nitpick/internal/knowledge"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

func hitOn(id, path string) knowledge.Hit {
	return knowledge.Hit{Entry: knowledge.Entry{ID: id}, Path: path}
}

// Evidence orders by whose file retrieved the entry, and withholds nothing.
//
// An entry another file pulled in is weaker evidence about this finding, so it
// sorts later. It is not dropped: a resource entry beside a correctness
// finding is often the reason the finding is right, and filtering on class
// would take away the one the validator needed.
func TestEvidenceOrdersByFileAndWithholdsNothing(t *testing.T) {
	f := Finding{Path: "a.go"}
	hits := []knowledge.Hit{
		hitOn("from-b", "b.go"),
		hitOn("batch-wide", ""),
		hitOn("from-a", "a.go"),
	}

	got := evidenceFor(f, hits)
	if len(got) != 3 {
		t.Fatalf("evidence = %v, want all three kept", got)
	}
	if got[2] != "from-b" {
		t.Errorf("evidence = %v, want the other file's entry last", got)
	}
	for _, want := range []string{"from-a", "batch-wide"} {
		if got[0] != want && got[1] != want {
			t.Errorf("evidence = %v, want %q in the first two", got, want)
		}
	}
}

// Three, because a finding naming all five entries a batch retrieved tells a
// reader nothing about which mattered.
func TestEvidenceIsCapped(t *testing.T) {
	f := Finding{Path: "a.go"}
	var hits []knowledge.Hit
	for _, id := range []string{"one", "two", "three", "four", "five"} {
		hits = append(hits, hitOn(id, "a.go"))
	}
	if got := evidenceFor(f, hits); len(got) != evidenceCap {
		t.Errorf("evidence = %v, want %d entries", got, evidenceCap)
	}
}

// A batch that retrieved nothing produces no evidence rather than an empty
// non-nil slice a consumer would render as a heading with nothing under it.
func TestNoHitsIsNoEvidence(t *testing.T) {
	if got := evidenceFor(Finding{Path: "a.go"}, nil); got != nil {
		t.Errorf("evidence = %v, want nil", got)
	}
}

func TestEvidenceSurvivesTriage(t *testing.T) {
	reviewed := mustJSON(t, Result{Findings: []Finding{
		{Path: "app.go", Line: 4, Severity: "error", Title: "Real finding"},
	}})
	triaged := mustJSON(t, Result{
		Summary: "one finding",
		Findings: []Finding{
			{Path: "app.go", Line: 4, Severity: "error", Title: "Real finding"},
		},
	})

	model := &scriptedLLM{byPrompt: map[string]string{
		"Review the following changes": reviewed,
		"triaging findings":            triaged,
	}}
	engine := newEngine(t, model, &stubProvider{diff: engineDiff}, nil)

	before := []Finding{{
		Path: "app.go", Line: 4, Severity: "error", Title: "Real finding",
		Evidence: []string{"go-defer-in-loop"},
	}}

	_, kept, _, err := engine.triage(context.Background(), &vcs.PullRequest{}, before)
	if err != nil {
		t.Fatalf("triage: %v", err)
	}
	if len(kept) != 1 {
		t.Fatalf("kept = %d findings, want 1", len(kept))
	}
	if got := kept[0].Evidence; len(got) != 1 || got[0] != "go-defer-in-loop" {
		t.Errorf("evidence after triage = %v, want it restored", got)
	}
}
