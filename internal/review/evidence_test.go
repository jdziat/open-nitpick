package review

import (
	"context"
	"fmt"
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

// Evidence survives triage, which re-decodes every finding from JSON.
//
// Source, Triager and the severity fields are all restored the same way and
// for the same reason: they are json:"-", so they arrive from triage's decode
// zeroed. A field that skipped this would be attributed at the reviewer and
// gone by the time anything published it.
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

// Ties keep the order retrieval gave them, so two runs publish the same
// evidence in the same order.
//
// The pattern is deliberate. An unstable sort leaves an all-equal slice alone,
// so a test built from twenty tied hits passes with sort.Slice and pins
// nothing. Interleaving the two groups is what makes the reordering reach the
// three ids that are published.
func TestTiedEvidenceKeepsRetrievalOrder(t *testing.T) {
	const n = 20
	hits := make([]knowledge.Hit, 0, n)
	for i := range n {
		path := "a.go"
		if i%2 == 1 {
			path = "b.go"
		}
		hits = append(hits, hitOn(fmt.Sprintf("entry-%02d", i), path))
	}

	got := evidenceFor(Finding{Path: "a.go"}, hits)
	want := []string{"entry-00", "entry-02", "entry-04"}
	if len(got) != len(want) {
		t.Fatalf("evidence = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("evidence = %v, want %v: tied hits were reordered", got, want)
		}
	}
}
