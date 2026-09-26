package review

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/bundle"
	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/diff"
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
	if !report.PipelineComplete() {
		t.Fatalf("judge parse failure must not degrade the run: stages=%v", report.Stages)
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
	// ThreadResolver that closes nothing: the judge still runs, ResidualApprove
	// is set, then publish clears it because standing threads remain.
	provider := &noopResolverProvider{incrementalProvider: incrementalProvider{
		stubProvider: stubProvider{diff: engineDiff},
		head:         "beef02",
		prior: &vcs.PriorReview{Head: "beef01", Comments: []vcs.PriorComment{
			{ID: 9, Path: "app.go", Line: 4, Fingerprint: "stale", Class: "style"},
		}},
	}}
	report, err := newEngine(t, model, provider, func(c *config.Config) {
		c.Review.Approve.Enabled = true
		c.Review.Approve.Residual.Enabled = true
		c.Review.MinSeverity = config.SeverityInfo
	}).Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	for _, p := range model.prompts() {
		if strings.Contains(p, "You decide whether a pull request review") {
			goto judged
		}
	}
	t.Fatal("judge must run when a ThreadResolver is present")
judged:
	if report.ResidualApprove {
		t.Fatal("ResidualApprove must clear when standing threads remain")
	}
	if provider.published == nil || provider.published.Event != vcs.EventComment {
		t.Fatalf("published event = %v, want COMMENT", provider.published)
	}
}

// noopResolverProvider implements ThreadResolver but closes no threads.
type noopResolverProvider struct {
	incrementalProvider
}

func (p *noopResolverProvider) ResolveThreads(context.Context, vcs.Ref, []int64, string) ([]int64, error) {
	return nil, nil
}

// TestResidualApproveClearedWhenIncrementalOff pins that PriorComments is
// populated from the residual prior read, so standing threads still refuse
// APPROVE when review.incremental is off.
func TestResidualApproveClearedWhenIncrementalOff(t *testing.T) {
	f := Finding{
		Path: "app.go", Line: 4, Severity: "info", Class: "correctness",
		Title: "document the ignored error", Rationale: "the blank identifier hides a failure mode",
	}
	model := &scriptedLLM{byPrompt: map[string]string{
		"Review the following changes":             mustJSON(t, Result{Findings: []Finding{f}}),
		"triaging findings":                        mustJSON(t, TriageResult{Verdicts: verdictsFor([]Finding{f})}),
		"You decide whether a pull request review": `{"approve":true,"reason":"advisory only"}`,
	}}
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
		c.Review.Incremental = false
		c.Review.MinSeverity = config.SeverityInfo
	}).Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if report.PriorComments == 0 {
		t.Fatal("PriorComments must be set from the residual prior read")
	}
	if report.ResidualApprove {
		t.Fatal("ResidualApprove must clear when standing threads remain")
	}
	if provider.published == nil || provider.published.Event != vcs.EventComment {
		t.Fatalf("published event = %v, want COMMENT", provider.published)
	}
}

type failingPriorProvider struct {
	stubProvider
}

func (p *failingPriorProvider) PriorReview(context.Context, vcs.Ref) (*vcs.PriorReview, error) {
	return nil, errors.New("forge unavailable")
}

// TestResidualApproveResolvesStandingWhenIncrementalOff pins that a full
// re-read (incremental off) can close line-carrying prior threads.
func TestResidualApproveResolvesStandingWhenIncrementalOff(t *testing.T) {
	f := Finding{
		Path: "app.go", Line: 4, Severity: "info", Class: "correctness",
		Title: "document the ignored error", Rationale: "the blank identifier hides a failure mode",
	}
	model := &scriptedLLM{byPrompt: map[string]string{
		"Review the following changes":             mustJSON(t, Result{Findings: []Finding{f}}),
		"triaging findings":                        mustJSON(t, TriageResult{Verdicts: verdictsFor([]Finding{f})}),
		"You decide whether a pull request review": `{"approve":true,"reason":"advisory only"}`,
	}}
	provider := &resolvingProvider{incrementalProvider: incrementalProvider{
		stubProvider: stubProvider{diff: engineDiff},
		head:         "beef02",
		prior: &vcs.PriorReview{Head: "beef01", Comments: []vcs.PriorComment{
			{ID: 9, Path: "app.go", Line: 4, Fingerprint: "stale", Class: "style"},
		}},
	}}
	report, err := newEngine(t, model, provider, func(c *config.Config) {
		c.Review.Approve.Enabled = true
		c.Review.Approve.Residual.Enabled = true
		c.Review.Incremental = false
		c.Review.MinSeverity = config.SeverityInfo
	}).Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if !report.ResidualApprove {
		t.Fatal("ResidualApprove must stay set after standing threads resolve")
	}
	if len(provider.resolved) == 0 {
		t.Fatal("expected standing threads to be resolved")
	}
	if provider.published == nil || provider.published.Event != vcs.EventApprove {
		t.Fatalf("published event = %v, want APPROVE", provider.published)
	}
	sawStanding := false
	for _, p := range model.prompts() {
		if strings.Contains(p, "Standing earlier comments this run would close on approve") {
			sawStanding = true
			break
		}
	}
	if !sawStanding {
		t.Fatal("judge must see standing comments it would close")
	}
}

// TestResidualApproveDoesNotClearUnreadPriorThread pins that a Line-zero
// comment on a file this run did not re-read stays open and holds COMMENT,
// without spending a residual judge call that cannot earn APPROVE.
func TestResidualApproveDoesNotClearUnreadPriorThread(t *testing.T) {
	f := Finding{
		Path: "app.go", Line: 4, Severity: "info", Class: "correctness",
		Title: "document the ignored error", Rationale: "the blank identifier hides a failure mode",
	}
	model := &scriptedLLM{byPrompt: map[string]string{
		"Review the following changes":             mustJSON(t, Result{Findings: []Finding{f}}),
		"triaging findings":                        mustJSON(t, TriageResult{Verdicts: verdictsFor([]Finding{f})}),
		"You decide whether a pull request review": `{"approve":true,"reason":"must not run"}`,
	}}
	provider := &resolvingProvider{incrementalProvider: incrementalProvider{
		stubProvider: stubProvider{diff: engineDiff},
		head:         "beef02",
		prior: &vcs.PriorReview{Head: "beef01", Comments: []vcs.PriorComment{
			{ID: 9, Path: "other.go", Line: 0, Fingerprint: "stale", Class: "correctness", Body: "old warning on a file this run did not read"},
		}},
	}}
	report, err := newEngine(t, model, provider, func(c *config.Config) {
		c.Review.Approve.Enabled = true
		c.Review.Approve.Residual.Enabled = true
		c.Review.Incremental = false
		c.Review.MinSeverity = config.SeverityInfo
	}).Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	for _, p := range model.prompts() {
		if strings.Contains(p, "You decide whether a pull request review") {
			t.Fatal("judge must not run when no standing thread is closable")
		}
	}
	if len(provider.resolved) != 0 {
		t.Fatalf("resolved unread prior thread ids = %v, want none", provider.resolved)
	}
	if report.ResidualApprove {
		t.Fatal("ResidualApprove must stay false when an unread prior thread remains")
	}
	if provider.published == nil || provider.published.Event != vcs.EventComment {
		t.Fatalf("published event = %v, want COMMENT", provider.published)
	}
}

// TestResidualApproveHeldWhenPriorReadFails pins that a PriorReviewer error
// is not treated as "no standing threads".
func TestResidualApproveHeldWhenPriorReadFails(t *testing.T) {
	f := Finding{
		Path: "app.go", Line: 4, Severity: "info", Class: "correctness",
		Title: "document the ignored error", Rationale: "the blank identifier hides a failure mode",
	}
	model := &scriptedLLM{byPrompt: map[string]string{
		"Review the following changes":             mustJSON(t, Result{Findings: []Finding{f}}),
		"triaging findings":                        mustJSON(t, TriageResult{Verdicts: verdictsFor([]Finding{f})}),
		"You decide whether a pull request review": `{"approve":true,"reason":"advisory only"}`,
	}}
	provider := &failingPriorProvider{stubProvider: stubProvider{diff: engineDiff}}
	report, err := newEngine(t, model, provider, func(c *config.Config) {
		c.Review.Approve.Enabled = true
		c.Review.Approve.Residual.Enabled = true
		c.Review.Incremental = false
		c.Review.MinSeverity = config.SeverityInfo
	}).Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if report.ResidualApprove {
		t.Fatal("ResidualApprove must clear when PriorReview fails")
	}
	if provider.published == nil || provider.published.Event != vcs.EventComment {
		t.Fatalf("published event = %v, want COMMENT", provider.published)
	}
}

// TestResidualStandingForJudgeOmitsPlanSkipped pins that a prior on a
// plan-skipped path is not shown to the judge: resolveClearedForApprove
// will not close it either.
func TestResidualStandingForJudgeOmitsPlanSkipped(t *testing.T) {
	report := &Report{
		Files: diff.Files{{Path: "app.go"}, {Path: "skip.go"}},
		Plan:  &bundle.Plan{Skipped: []bundle.Skip{{Path: "skip.go", Reason: bundle.ReasonIgnored}}},
		prior: &vcs.PriorReview{Comments: []vcs.PriorComment{
			{ID: 1, Path: "app.go", Line: 4, Class: "style"},
			{ID: 2, Path: "skip.go", Line: 1, Class: "correctness", Body: "on an ignored file"},
		}},
	}
	got := residualStandingForJudge(report)
	if len(got) != 1 || got[0].ID != 1 {
		t.Fatalf("standing = %+v, want only the re-read non-skipped comment", got)
	}
}

// TestCleanApproveHeldWhenResidualPriorReadFails pins that a failed prior
// read under residual also holds the zero-finding approve path.
func TestCleanApproveHeldWhenResidualPriorReadFails(t *testing.T) {
	model := &scriptedLLM{byPrompt: map[string]string{
		"Review the following changes": `{"findings":[]}`,
	}}
	provider := &failingPriorProvider{stubProvider: stubProvider{diff: engineDiff}}
	report, err := newEngine(t, model, provider, func(c *config.Config) {
		c.Review.Approve.Enabled = true
		c.Review.Approve.Residual.Enabled = true
		c.Review.Incremental = false
	}).Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if !report.priorReadFailed {
		t.Fatal("priorReadFailed must be set when PriorReview errors")
	}
	if provider.published == nil || provider.published.Event != vcs.EventComment {
		t.Fatalf("published event = %v, want COMMENT", provider.published)
	}
}
