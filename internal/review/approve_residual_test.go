package review

import (
	"context"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

func TestResidualApproveEndToEndSubmitsApprove(t *testing.T) {
	f := Finding{
		Path: "app.go", Line: 4, Severity: "info", Class: "correctness",
		Title: "document the ignored error", Rationale: "the blank identifier hides a failure mode",
	}
	model := &scriptedLLM{byPrompt: map[string]string{
		"Review the following changes":             mustJSON(t, Result{Findings: []Finding{f}}),
		"triaging findings":                        mustJSON(t, TriageResult{Verdicts: verdictsFor([]Finding{f})}),
		"You decide whether a pull request review": `{"approve":true,"reason":"advisory documentation only"}`,
	}}
	provider := &stubProvider{diff: engineDiff}
	report, err := newEngine(t, model, provider, func(c *config.Config) {
		c.Review.Approve.Enabled = true
		c.Review.Approve.Residual.Enabled = true
		c.Review.MinSeverity = config.SeverityInfo
	}).Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if len(report.Findings) != 1 {
		t.Fatalf("findings = %d, want the info residual", len(report.Findings))
	}
	if !report.ResidualApprove {
		t.Fatal("ResidualApprove must be set after a yes judgment")
	}
	if provider.published == nil || provider.published.Event != vcs.EventApprove {
		t.Fatalf("published event = %v, want APPROVE", provider.published)
	}
}

func TestResidualApproveJudgeErrorHoldsAtComment(t *testing.T) {
	f := Finding{
		Path: "app.go", Line: 4, Severity: "info", Class: "correctness",
		Title: "document the ignored error", Rationale: "the blank identifier hides a failure mode",
	}
	model := &scriptedLLM{byPrompt: map[string]string{
		"Review the following changes":             mustJSON(t, Result{Findings: []Finding{f}}),
		"triaging findings":                        mustJSON(t, TriageResult{Verdicts: verdictsFor([]Finding{f})}),
		"You decide whether a pull request review": `not-json`,
	}}
	provider := &stubProvider{diff: engineDiff}
	report, err := newEngine(t, model, provider, func(c *config.Config) {
		c.Review.Approve.Enabled = true
		c.Review.Approve.Residual.Enabled = true
	}).Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if report.ResidualApprove {
		t.Fatal("judge parse failure must not set ResidualApprove")
	}
	if provider.published == nil || provider.published.Event != vcs.EventComment {
		t.Fatalf("published event = %v, want COMMENT", provider.published)
	}
}

func TestCleanApproveDoesNotCallResidualJudge(t *testing.T) {
	model := &scriptedLLM{byPrompt: map[string]string{
		"Review the following changes":             `{"findings":[]}`,
		"You decide whether a pull request review": `{"approve":true,"reason":"should not run"}`,
	}}
	provider := &stubProvider{diff: engineDiff}
	_, err := newEngine(t, model, provider, func(c *config.Config) {
		c.Review.Approve.Enabled = true
		c.Review.Approve.Residual.Enabled = true
	}).Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	for _, p := range model.prompts() {
		if strings.Contains(p, "You decide whether a pull request review") {
			t.Fatal("clean zero-finding approve must not call the residual judge")
		}
	}
	if provider.published == nil || provider.published.Event != vcs.EventApprove {
		t.Fatalf("published event = %v, want APPROVE", provider.published)
	}
}

func TestResidualFloorBlocksEngineApproveDespiteJudgeYes(t *testing.T) {
	f := Finding{
		Path: "app.go", Line: 4, Severity: "warning", Class: "correctness",
		Title: "ignored error", Rationale: "resp may be nil",
	}
	model := &scriptedLLM{byPrompt: map[string]string{
		"Review the following changes":             mustJSON(t, Result{Findings: []Finding{f}}),
		"triaging findings":                        mustJSON(t, TriageResult{Verdicts: verdictsFor([]Finding{f})}),
		"You decide whether a pull request review": `{"approve":true,"reason":"should not run"}`,
	}}
	provider := &stubProvider{diff: engineDiff}
	report, err := newEngine(t, model, provider, func(c *config.Config) {
		c.Review.Approve.Enabled = true
		c.Review.Approve.Residual.Enabled = true
		c.Review.Approve.Residual.MaxSeverity = config.SeverityInfo
	}).Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	for _, p := range model.prompts() {
		if strings.Contains(p, "You decide whether a pull request review") {
			t.Fatal("warning above the floor must not call the residual judge")
		}
	}
	if report.ResidualApprove {
		t.Fatal("ResidualApprove must stay false when the floor refuses")
	}
	if provider.published == nil || provider.published.Event != vcs.EventComment {
		t.Fatalf("published event = %v, want COMMENT", provider.published)
	}
}

// TestResidualApproveClearedWhenStandingRemain pins that a judge yes does not
// leave ResidualApprove set when standing threads still refuse the event.
func TestResidualApproveClearedWhenStandingRemain(t *testing.T) {
	f := Finding{
		Path: "app.go", Line: 4, Severity: "info", Class: "correctness",
		Title: "document the ignored error", Rationale: "the blank identifier hides a failure mode",
	}
	model := &scriptedLLM{byPrompt: map[string]string{
		"Review the following changes":             mustJSON(t, Result{Findings: []Finding{f}}),
		"triaging findings":                        mustJSON(t, TriageResult{Verdicts: verdictsFor([]Finding{f})}),
		"You decide whether a pull request review": `{"approve":true,"reason":"advisory only"}`,
	}}
	// No ThreadResolver: resolveClearedForApprove cannot close the prior, so
	// standing remains and ResidualApprove must be cleared before Render.
	provider := &incrementalProvider{
		stubProvider: stubProvider{diff: engineDiff},
		head:         "beef02",
		prior: &vcs.PriorReview{Head: "beef01", Comments: []vcs.PriorComment{
			{ID: 9, Path: "app.go", Line: 4, Fingerprint: "stale", Class: "style"},
		}},
	}
	report, err := newEngine(t, model, provider, func(c *config.Config) {
		c.Review.Approve.Enabled = true
		c.Review.Approve.Residual.Enabled = true
		c.Review.MinSeverity = config.SeverityInfo
	}).Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if report.ResidualApprove {
		t.Fatal("ResidualApprove must clear when standing threads remain")
	}
	if provider.published == nil || provider.published.Event != vcs.EventComment {
		t.Fatalf("published event = %v, want COMMENT", provider.published)
	}
}
