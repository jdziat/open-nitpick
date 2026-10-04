package review

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

func TestResumeWarnsWhenIncrementalIsDisabled(t *testing.T) {
	for _, resume := range []bool{false, true} {
		for _, incremental := range []bool{false, true} {
			t.Run(fmt.Sprintf("resume=%t/incremental=%t", resume, incremental), func(t *testing.T) {
				var logs bytes.Buffer
				cfg := config.Defaults()
				cfg.Review.Incremental = incremental
				engine := &Engine{Config: cfg, Resume: resume, Log: slog.New(slog.NewTextHandler(&logs, nil))}
				engine.startProgress(&vcs.PullRequest{HeadSHA: "abcdef"}, nil)
				warn := resume && !incremental
				if got := strings.Contains(logs.String(), "review.incremental=true"); got != warn {
					t.Fatalf("resume warning=%t, want %t: %s", got, warn, logs.String())
				}
				if got := engine.progress != nil; got != (resume && incremental) {
					t.Fatalf("progress enabled=%t for resume=%t incremental=%t", got, resume, incremental)
				}
			})
		}
	}
}

func resumeEngine(t *testing.T, model *scriptedLLM, provider *incrementalProvider) *Engine {
	t.Helper()
	e := newEngine(t, model, provider, func(c *config.Config) {
		c.Review.MaxFilesPerRequest = 1
		c.Review.Approve.Enabled = true
		c.Review.Approve.RequireAnalyzers = false
		c.Review.RelatedContext = false
		c.Review.Incremental = true
	})
	e.Resume = true
	return e
}

func TestResumeRetriesFailedBatchAndProvesEmptyCoverage(t *testing.T) {
	model := &scriptedLLM{byPrompt: map[string]string{"other.go": "not JSON"}, fallback: `{"findings":[]}`}
	provider := &incrementalProvider{stubProvider: stubProvider{diff: incrementalDiff}, head: "abcdef"}
	e := resumeEngine(t, model, provider)
	first, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if first.Complete() || len(first.Incomplete) != 1 || first.Incomplete[0] != "other.go" || provider.published.Event != vcs.EventComment {
		t.Fatalf("failed batch counted as reviewed: %+v", first)
	}
	if len(provider.published.Progress) == 0 {
		t.Fatal("completed work was not persisted")
	}
	provider.prior = &vcs.PriorReview{Progress: provider.published.Progress}
	model.byPrompt = nil
	model.seen = nil
	second, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if !second.Complete() || len(second.Findings) != 0 || second.ReusedRequests != 1 || provider.published.Event != vcs.EventApprove {
		t.Fatalf("retry did not establish complete clean coverage: complete=%v reused=%d event=%s", second.Complete(), second.ReusedRequests, provider.published.Event)
	}
	prompts := strings.Join(model.prompts(), "\n")
	if strings.Contains(prompts, "app.go") || !strings.Contains(prompts, "other.go") {
		t.Fatalf("wrong retry scope: %s", prompts)
	}
	provider.prior = &vcs.PriorReview{Progress: provider.published.Progress}
	model.seen = nil
	third, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if third.ReusedRequests != 2 || len(model.prompts()) != 0 || provider.published.Event != vcs.EventApprove {
		t.Fatalf("completed clean review not reusable: reused=%d calls=%v", third.ReusedRequests, model.prompts())
	}
}

// TestResumeRetainsFindingsAndStandingThreads pins the fix for threads that
// never closed: a file in scope only because a comment stands on it must get
// a live model call on every resumed run, even though its content, and so
// its batch hash, did not change. Reusing the cached verdict here would mean
// the finding "recurs" because it was never asked about again, not because
// the model reaffirmed it, and superseded would then never see it stop
// recurring. The finding is still withheld as already-reported, by
// fingerprint match against the standing comment rather than by cache reuse.
func TestResumeRetainsFindingsAndStandingThreads(t *testing.T) {
	finding := Finding{Path: "app.go", Line: 4, Severity: "error", Class: "correctness", Category: "correctness", Title: "Ignored response error", Rationale: "The response may be nil."}
	model := &scriptedLLM{fallback: mustJSON(t, Result{Findings: []Finding{finding}})}
	provider := &incrementalProvider{stubProvider: stubProvider{diff: engineDiff}, head: "abcdef"}
	e := resumeEngine(t, model, provider)
	if _, err := e.Review(context.Background(), vcs.Ref{}); err != nil {
		t.Fatal(err)
	}
	provider.prior = &vcs.PriorReview{Progress: provider.published.Progress, Comments: []vcs.PriorComment{{ID: 1, Path: finding.Path, Line: finding.Line, Fingerprint: Fingerprint(finding), Class: finding.Class}}}
	model.seen = nil
	report, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if report.ReusedRequests != 0 || len(report.AlreadyReported) != 1 || len(provider.published.Comments) != 0 || provider.published.Event != vcs.EventComment {
		t.Fatalf("resume lost standing finding: reused=%d already=%v event=%s", report.ReusedRequests, report.AlreadyReported, provider.published.Event)
	}
	if !strings.Contains(strings.Join(model.prompts(), "\n"), "triaging findings") {
		t.Fatal("combined findings bypassed triage")
	}
	if !strings.Contains(strings.Join(model.prompts(), "\n"), "Review the following changes") {
		t.Fatal("standing comment's file answered from the progress cache instead of a live call")
	}
}

// TestStandingOnlyScopeDoesNotOutliveTheRun pins the other half of the same
// fix: Review can be called more than once on one Engine, so the standing-only
// map is per-run. A run with no Incremental note must not inherit the previous
// run's map and exempt files from the progress cache forever.
func TestStandingOnlyScopeDoesNotOutliveTheRun(t *testing.T) {
	finding := Finding{Path: "app.go", Line: 4, Severity: "error", Class: "correctness", Category: "correctness", Title: "Ignored response error", Rationale: "The response may be nil."}
	model := &scriptedLLM{fallback: mustJSON(t, Result{Findings: []Finding{finding}})}
	provider := &incrementalProvider{stubProvider: stubProvider{diff: engineDiff}, head: "abcdef"}
	e := resumeEngine(t, model, provider)
	if _, err := e.Review(context.Background(), vcs.Ref{}); err != nil {
		t.Fatal(err)
	}

	provider.prior = &vcs.PriorReview{Progress: provider.published.Progress, Comments: []vcs.PriorComment{{ID: 1, Path: finding.Path, Line: finding.Line, Fingerprint: Fingerprint(finding), Class: finding.Class}}}
	if _, err := e.Review(context.Background(), vcs.Ref{}); err != nil {
		t.Fatal(err)
	}
	if len(e.standingOnly) == 0 {
		t.Fatal("standing recheck did not mark its file, so nothing exercised the per-run reset")
	}

	// The comment is gone, so this run has no standing files at all.
	provider.prior = &vcs.PriorReview{Progress: provider.published.Progress}
	if _, err := e.Review(context.Background(), vcs.Ref{}); err != nil {
		t.Fatal(err)
	}
	if len(e.standingOnly) != 0 {
		t.Fatalf("standing-only scope outlived its comment: %v", e.standingOnly)
	}
}

func TestResumeInvalidatesChangedInputsAndHonorsRestart(t *testing.T) {
	for _, change := range []string{"unchanged", "code", "policy", "instruction", "model", "restart", "disabled", "legacy", "malformed"} {
		t.Run(change, func(t *testing.T) {
			model := &scriptedLLM{fallback: `{"findings":[]}`}
			provider := &incrementalProvider{stubProvider: stubProvider{diff: incrementalDiff}, head: "abcdef"}
			e := resumeEngine(t, model, provider)
			if _, err := e.Review(context.Background(), vcs.Ref{}); err != nil {
				t.Fatal(err)
			}
			provider.prior = &vcs.PriorReview{Head: provider.head, Progress: provider.published.Progress}
			want := 0
			switch change {
			case "unchanged":
				want = 2
			case "code":
				provider.head = "abcdef01"
				provider.diff = strings.ReplaceAll(incrementalDiff, "Unused = 1", "Unused = 2")
				want = 1
			case "policy":
				e.Config.Review.MinSeverity = config.SeverityWarning
			case "instruction":
				e.Instruction = "Check error handling carefully."
			case "model":
				e.Roles.Review.Spec.Model = "different-model"
			case "disabled":
				e.Config.Review.Incremental = false
			case "restart":
				e.Full = true
			case "legacy":
				provider.prior.Progress = nil
			case "malformed":
				provider.prior.Progress = json.RawMessage(`{"version":100,"results":{}}`)
			}
			model.seen = nil
			report, err := e.Review(context.Background(), vcs.Ref{})
			if err != nil {
				t.Fatal(err)
			}
			if report.ReusedRequests != want || len(model.prompts()) != 2-want {
				t.Fatalf("reused=%d model calls=%d want=%d", report.ReusedRequests, len(model.prompts()), want)
			}
		})
	}
}

func TestResumeRetriesTriageWithoutRepeatingCompletedAnalysis(t *testing.T) {
	finding := Finding{Path: "app.go", Line: 4, Severity: "warning", Class: "correctness", Category: "correctness", Title: "Ignored response error", Rationale: "The response may be nil."}
	model := &scriptedLLM{byPrompt: map[string]string{"triaging findings": "not JSON"}, fallback: mustJSON(t, Result{Findings: []Finding{finding}})}
	provider := &incrementalProvider{stubProvider: stubProvider{diff: engineDiff}, head: "abcdef"}
	engine := resumeEngine(t, model, provider)
	first, err := engine.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if first.PipelineComplete() || len(first.Findings) != 1 {
		t.Fatalf("triage failure lost: %+v", first.Stages)
	}
	provider.prior = &vcs.PriorReview{Progress: provider.published.Progress}
	retryModel := &scriptedLLM{fallback: mustJSON(t, Result{Findings: []Finding{finding}})}
	second, err := resumeEngine(t, retryModel, provider).Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if !second.PipelineComplete() || second.ReusedRequests != 1 || len(second.Findings) != 1 {
		t.Fatalf("retry incomplete or lost findings: %+v", second.Stages)
	}
	prompts := strings.Join(retryModel.prompts(), "\n")
	if strings.Contains(prompts, "Review the following changes") || !strings.Contains(prompts, "triaging findings") {
		t.Fatal("retry did not isolate unfinished triage")
	}
}

func TestProgressLimitDropsWorkInsteadOfClaimingCompletion(t *testing.T) {
	p := &reviewProgress{current: map[string]json.RawMessage{}}
	p.save("large", []Finding{{Rationale: strings.Repeat("x", vcs.MaxProgressBytes)}})
	p.save("empty", nil)
	data, _ := p.snapshot("abcdef")
	if len(data) > vcs.MaxProgressBytes {
		t.Fatal("checkpoint exceeded limit")
	}
	var record progressRecord
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	restored := &reviewProgress{prior: record.Results, current: map[string]json.RawMessage{}}
	if _, ok := restored.load("large"); ok {
		t.Fatal("omitted work counted as complete")
	}
	if _, ok := restored.load("empty"); !ok {
		t.Fatal("successful zero-finding result was lost")
	}
}

func TestStandingRechecksShareScopeAcrossReviewModes(t *testing.T) {
	for _, resume := range []bool{false, true} {
		for _, head := range []string{"abcdef", ""} {
			name := fmt.Sprintf("resume=%t/prior-head=%s", resume, head)
			t.Run(name, func(t *testing.T) {
				model := &scriptedLLM{fallback: `{"findings":[]}`}
				provider := &incrementalProvider{
					stubProvider: stubProvider{diff: incrementalDiff}, head: "abcdef",
					prior: &vcs.PriorReview{Head: head, Comments: []vcs.PriorComment{{ID: 1, Path: "app.go", Line: 4, Fingerprint: "standing"}}},
				}
				engine := resumeEngine(t, model, provider)
				engine.Resume = resume
				report, err := engine.Review(context.Background(), vcs.Ref{})
				if err != nil {
					t.Fatal(err)
				}
				note := report.Incremental
				if note == nil || !note.Recheck || note.Since != head || !slices.Equal(note.Reviewed, []string{"app.go", "other.go"}) || len(note.Unchanged) != 0 {
					t.Fatalf("standing recheck lost scope: %+v", note)
				}
				if report.Plan.Files() != 2 || provider.published.Event != vcs.EventComment {
					t.Fatalf("standing threads bypassed: files=%d event=%s", report.Plan.Files(), provider.published.Event)
				}
				summary := provider.published.Summary
				if !strings.Contains(summary, "Rechecked the whole change") || strings.Contains(summary, "``") {
					t.Fatalf("invalid recheck notice: %s", summary)
				}
			})
		}
	}
}
