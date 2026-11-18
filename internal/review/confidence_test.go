package review

import (
	"context"
	"encoding/json"
	"math"
	"slices"
	"testing"

	"github.com/jdziat/open-nitpick/internal/vcs"
)

// The reviewer has to be asked for a confidence, or a publish floor has nothing
// to read. It is required so a model cannot leave it out, and the citation is
// optional so a finding with no single place to quote is not forced to invent
// one.
func TestTheReviewSchemaRequiresAConfidenceAndOffersACitation(t *testing.T) {
	raw, err := findingsSchema(offeredClasses(false))
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties struct {
			Findings struct {
				Items struct {
					Properties map[string]map[string]any `json:"properties"`
					Required   []string                  `json:"required"`
				} `json:"items"`
			} `json:"findings"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	items := schema.Properties.Findings.Items
	if items.Properties["confidence"]["type"] != "number" {
		t.Errorf("confidence = %v, want a number property", items.Properties["confidence"])
	}
	if !slices.Contains(items.Required, "confidence") {
		t.Errorf("required = %v, want confidence in it", items.Required)
	}
	if _, ok := items.Properties["citation"]; !ok {
		t.Error("citation is not offered")
	}
	if slices.Contains(items.Required, "citation") {
		t.Error("citation is required; a finding with nothing to quote would have to invent a quote")
	}
}

func TestAConfidenceOutsideTheScaleIsPinnedAndNaNCarriesNoClaim(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	cases := []struct {
		in   *float64
		want *float64
	}{
		{nil, nil},
		{f(math.NaN()), nil},
		{f(-0.2), f(0)},
		{f(0.75), f(0.75)},
		{f(1.7), f(1)},
	}
	for _, c := range cases {
		got := clampConfidence(c.in)
		switch {
		case c.want == nil && got != nil:
			t.Errorf("clamp(%v) = %v, want nil", deref(c.in), *got)
		case c.want != nil && (got == nil || *got != *c.want):
			t.Errorf("clamp(%v) = %v, want %v", deref(c.in), got, *c.want)
		}
	}
}

func deref(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}

// What the reviewer said survives to the published finding, clamped. A stage
// that read confidence downstream would otherwise see nil on every finding and
// pass everything, which is the silent failure a floor exists to prevent.
func TestTheReviewersConfidenceAndCitationReachThePublishedFinding(t *testing.T) {
	high := 1.7
	finding := Finding{
		Path: "app.go", Line: 4, Severity: "error", Category: "correctness", Class: "correctness",
		Title: "Ignored error from http.Get", Rationale: "resp may be nil, so the deferred Close panics.",
		Confidence: &high, Citation: "resp, _ := http.Get(\"http://x\")",
	}
	model := &scriptedLLM{byPrompt: map[string]string{
		"Review the following changes": mustJSON(t, Result{Findings: []Finding{finding}}),
		"triaging findings":            mustJSON(t, TriageResult{Summary: "s", Verdicts: verdictsFor([]Finding{finding})}),
	}}
	report, err := newEngine(t, model, &stubProvider{diff: engineDiff}, nil).Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if len(report.Findings) != 1 {
		t.Fatalf("findings = %d, want 1", len(report.Findings))
	}
	got := report.Findings[0]
	if got.Confidence == nil || *got.Confidence != 1 {
		t.Errorf("confidence = %v, want 1 after clamping", deref(got.Confidence))
	}
	if got.Citation != finding.Citation {
		t.Errorf("citation = %q, want the reviewer's", got.Citation)
	}
}
