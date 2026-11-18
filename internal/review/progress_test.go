package review

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	llms "github.com/nocturnium/llm-go-sdk/v6"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/llm"
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

// verifiedResumeEngine is resumeEngine with the repository check switched on,
// so one review reaches analysis, triage and verification.
func verifiedResumeEngine(t *testing.T, model *scriptedLLM, checker *toolLLM, provider *incrementalProvider) *Engine {
	t.Helper()
	e := resumeEngine(t, model, provider)
	e.Config.Review.Verify = config.VerifyConfirmed
	e.Roles.Validate = llm.NewClientForTest(checker, e.Config.Models.Default)
	return e
}

func resumeFindings() []Finding {
	return []Finding{{Path: "app.go", Line: 4, Severity: "error", Category: "correctness", Class: "correctness",
		Title: "Ignored error from http.Get", Rationale: "resp may be nil, so the deferred Close panics."}}
}

// A retry of a finished review must not pay for any of it again: the batch,
// the triage and the repository check each came back once already.
func TestResumeReusesTriageAndVerificationWithoutAnyNewModelCall(t *testing.T) {
	fs := resumeFindings()
	script := map[string][]llms.Response{"Ignored error from http.Get": {verdict(verdictConfirmed, "app.go:4 discards the error")}}
	newModel := func() *scriptedLLM {
		return &scriptedLLM{byPrompt: map[string]string{
			"Review the following changes": mustJSON(t, Result{Findings: fs}),
			"triaging findings":            mustJSON(t, TriageResult{Summary: "s", Verdicts: verdictsFor(fs)}),
		}}
	}
	provider := &incrementalProvider{stubProvider: stubProvider{diff: engineDiff, content: map[string]string{"app.go": "package app\n"}}, head: "abcdef"}
	firstChecker := &toolLLM{script: script}
	first, err := verifiedResumeEngine(t, newModel(), firstChecker, provider).Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Findings) != 1 || firstChecker.calls == 0 {
		t.Fatalf("first review did not verify: findings=%d checks=%d", len(first.Findings), firstChecker.calls)
	}

	provider.prior = &vcs.PriorReview{Progress: provider.published.Progress}
	retryModel, retryChecker := newModel(), &toolLLM{script: script}
	second, err := verifiedResumeEngine(t, retryModel, retryChecker, provider).Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if calls := len(retryModel.prompts()); calls != 0 || retryChecker.calls != 0 {
		t.Fatalf("retry paid again: model calls=%d checker calls=%d", calls, retryChecker.calls)
	}
	if second.ReusedRequests != 3 || len(second.Findings) != 1 || second.Findings[0].Title != fs[0].Title {
		t.Fatalf("retry changed the outcome: reused=%d findings=%+v", second.ReusedRequests, second.Findings)
	}
}

// The check is not saved when it failed or stopped without a verdict, because
// that is the case a retry exists to repeat, and a refutation is kept so the
// same finding is not asked about twice.
func TestResumeRepeatsAVerificationThatNeverReachedAVerdict(t *testing.T) {
	fs := resumeFindings()
	model := func() *scriptedLLM {
		return &scriptedLLM{byPrompt: map[string]string{
			"Review the following changes": mustJSON(t, Result{Findings: fs}),
			"triaging findings":            mustJSON(t, TriageResult{Summary: "s", Verdicts: verdictsFor(fs)}),
		}}
	}
	provider := &incrementalProvider{stubProvider: stubProvider{diff: engineDiff, content: map[string]string{"app.go": "package app\n"}}, head: "abcdef"}
	down := &toolLLM{err: errors.New("provider down")}
	if _, err := verifiedResumeEngine(t, model(), down, provider).Review(context.Background(), vcs.Ref{}); err != nil {
		t.Fatal(err)
	}
	provider.prior = &vcs.PriorReview{Progress: provider.published.Progress}
	up := &toolLLM{script: map[string][]llms.Response{"Ignored error from http.Get": {verdict(verdictRefuted, "app.go:4 checks the error")}}}
	second, err := verifiedResumeEngine(t, model(), up, provider).Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if up.calls == 0 || len(second.Findings) != 0 || len(second.Overruled) != 1 {
		t.Fatalf("failed check was replayed: checks=%d findings=%d withheld=%d", up.calls, len(second.Findings), len(second.Overruled))
	}
	provider.prior = &vcs.PriorReview{Progress: provider.published.Progress}
	again := &toolLLM{}
	third, err := verifiedResumeEngine(t, model(), again, provider).Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if again.calls != 0 || len(third.Overruled) != 1 {
		t.Fatalf("refutation was not reused: checks=%d withheld=%d", again.calls, len(third.Overruled))
	}
}

// A stage answer belongs to the head it was asked at: a push changes what the
// checker could read, so the same finding on a new head is checked again.
func TestResumeDoesNotServeAVerdictAcrossHeads(t *testing.T) {
	p := &reviewProgress{now: "aaaaaa", policy: "p", current: map[string]json.RawMessage{}}
	client := llm.NewClientForTest(&scriptedLLM{}, config.ModelSpec{Provider: "openai", Model: "gpt-4o"})
	old := p.stageKey("verify", client, "system", "request")
	saveStage(p, old, rankVerify, storedVerdict{Verdict: verdictConfirmed})
	p.now = "bbbbbb"
	if p.stageKey("verify", client, "system", "request") == old {
		t.Fatal("a verdict key did not change with the head")
	}
	if p.stageKey("triage", client, "system", "request") == p.stageKey("verify", client, "system", "request") {
		t.Fatal("two stages share a key for the same request")
	}
}

// A full checkpoint drops the cheap-to-redo stage results before it drops a
// batch's analysis, which costs the most to repeat.
func TestProgressLimitKeepsAnalysisBeforeStageResults(t *testing.T) {
	p := &reviewProgress{current: map[string]json.RawMessage{}}
	saveStage(p, "a-verify", rankVerify, storedVerdict{Reason: strings.Repeat("x", vcs.MaxProgressBytes/2)})
	p.save("z-analysis", []Finding{{Rationale: strings.Repeat("y", vcs.MaxProgressBytes/2)}})
	data, _ := p.snapshot("abcdef")
	var record progressRecord
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	if _, ok := record.Results["z-analysis"]; !ok {
		t.Fatal("analysis was dropped to make room for a verification")
	}
	if _, ok := record.Results["a-verify"]; ok {
		t.Fatal("test does not exercise the limit: both results fit")
	}
}

// A check that never reached a verdict leaves nothing in the checkpoint, so the
// space goes to work that can be reused.
func TestAFailedVerificationLeavesNoCheckpointEntry(t *testing.T) {
	fs := resumeFindings()
	model := &scriptedLLM{byPrompt: map[string]string{
		"Review the following changes": mustJSON(t, Result{Findings: fs}),
		"triaging findings":            mustJSON(t, TriageResult{Summary: "s", Verdicts: verdictsFor(fs)}),
	}}
	provider := &incrementalProvider{stubProvider: stubProvider{diff: engineDiff, content: map[string]string{"app.go": "package app\n"}}, head: "abcdef"}
	if _, err := verifiedResumeEngine(t, model, &toolLLM{err: errors.New("provider down")}, provider).Review(context.Background(), vcs.Ref{}); err != nil {
		t.Fatal(err)
	}
	var record progressRecord
	if err := json.Unmarshal(provider.published.Progress, &record); err != nil {
		t.Fatal(err)
	}
	if len(record.Results) != 2 {
		t.Fatalf("checkpoint holds %d results, want the batch and the triage only", len(record.Results))
	}
}

func TestResumeRetriesMalformedVerificationBeforeSavingAValidVerdict(t *testing.T) {
	cases := map[string]map[string]any{
		"missing":    {"reason": "not a verdict"},
		"unknown":    {"verdict": "probably", "reason": "not a supported verdict"},
		"wrong type": {"verdict": true},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			fs := resumeFindings()
			newModel := func() *scriptedLLM {
				return &scriptedLLM{byPrompt: map[string]string{
					"Review the following changes": mustJSON(t, Result{Findings: fs}),
					"triaging findings":            mustJSON(t, TriageResult{Summary: "s", Verdicts: verdictsFor(fs)}),
				}}
			}
			provider := &incrementalProvider{stubProvider: stubProvider{diff: engineDiff, content: map[string]string{"app.go": "package app\n"}}, head: "abcdef"}
			malformed := &toolLLM{script: map[string][]llms.Response{
				fs[0].Title: {{ToolCalls: []llms.ToolCall{call("v", "submit_verdict", args)}}},
			}}
			if _, err := verifiedResumeEngine(t, newModel(), malformed, provider).Review(context.Background(), vcs.Ref{}); err != nil {
				t.Fatal(err)
			}
			var record progressRecord
			if err := json.Unmarshal(provider.published.Progress, &record); err != nil {
				t.Fatal(err)
			}
			if len(record.Results) != 2 {
				t.Errorf("malformed verdict was checkpointed: got %d entries, want analysis and triage", len(record.Results))
			}
			provider.prior = &vcs.PriorReview{Progress: provider.published.Progress}
			valid := &toolLLM{script: map[string][]llms.Response{
				fs[0].Title: {verdict(verdictConfirmed, "app.go:4 discards the error")},
			}}
			retryModel := newModel()
			retry, err := verifiedResumeEngine(t, retryModel, valid, provider).Review(context.Background(), vcs.Ref{})
			if err != nil {
				t.Fatal(err)
			}
			if valid.calls != 1 || len(retry.Findings) != 1 || len(retryModel.prompts()) != 0 {
				t.Fatalf("retry did not reuse completed stages and replace the malformed verdict: checks=%d findings=%d model calls=%d", valid.calls, len(retry.Findings), len(retryModel.prompts()))
			}
		})
	}
}

// An answer with nothing in it is what a flaky provider returns. Saving it
// would hand the same empty answer back on every retry.
func TestResumeAsksTriageAgainAfterAnEmptyAnswer(t *testing.T) {
	fs := resumeFindings()
	provider := &incrementalProvider{stubProvider: stubProvider{diff: engineDiff}, head: "abcdef"}
	empty := &scriptedLLM{byPrompt: map[string]string{
		"Review the following changes": mustJSON(t, Result{Findings: fs}),
		"triaging findings":            `{"findings":[],"summary":""}`,
	}}
	if _, err := resumeEngine(t, empty, provider).Review(context.Background(), vcs.Ref{}); err != nil {
		t.Fatal(err)
	}
	var record progressRecord
	if err := json.Unmarshal(provider.published.Progress, &record); err != nil {
		t.Fatal(err)
	}
	if len(record.Results) != 1 {
		t.Fatalf("empty triage consumed checkpoint space: entries=%d, want analysis only", len(record.Results))
	}
	provider.prior = &vcs.PriorReview{Progress: provider.published.Progress}
	good := &scriptedLLM{byPrompt: map[string]string{
		"triaging findings": mustJSON(t, TriageResult{Summary: "reviewed", Verdicts: verdictsFor(fs)}),
	}}
	if _, err := resumeEngine(t, good, provider).Review(context.Background(), vcs.Ref{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(good.prompts(), "\n"), "triaging findings") {
		t.Fatal("an empty triage answer was replayed instead of asked again")
	}
}

func TestStageKeyIsEmptyForAFileInScopeOnlyToRecheckAComment(t *testing.T) {
	client := llm.NewClientForTest(&scriptedLLM{}, config.ModelSpec{Provider: "openai", Model: "gpt-4o"})
	e := &Engine{progress: &reviewProgress{now: "abcdef", policy: "p", current: map[string]json.RawMessage{}}, standingOnly: map[string]bool{"app.go": true}}
	if key := e.stageKeyFor("verify", client, "s", "r", []string{"other.go", "app.go"}); key != "" {
		t.Fatalf("a standing-only file got key %q, so its recheck could be replayed", key)
	}
	if key := e.stageKeyFor("verify", client, "s", "r", []string{"other.go"}); key == "" {
		t.Fatal("an ordinary file got no key, so nothing would be reused")
	}
}

func TestResumeRechecksEveryVerificationLookupBeforeReusingItsVerdict(t *testing.T) {
	for _, search := range []bool{false, true} {
		t.Run(fmt.Sprintf("search=%t", search), func(t *testing.T) {
			root := gitRepo(t, map[string]string{
				"large.go":   strings.Repeat(strings.Repeat("x", 80)+"\n", toolReadDefault),
				"sibling.go": "const Value = 1\n",
			})
			fs := twoFindings()
			lastLookup := lookup("sibling.go")
			if search {
				lastLookup = llms.Response{ToolCalls: []llms.ToolCall{call("g", "grep_repo", map[string]any{"pattern": "Value", "path_glob": "sibling.go"})}}
			}
			script := map[string][]llms.Response{fs[0].Title: {
				lookup("large.go"), lookup("large.go"), lastLookup, verdict(verdictConfirmed, "sibling.go:1 establishes the claim"),
			}}
			checker := &toolLLM{script: script}
			e, _ := verifyEngine(t, checker, config.VerifyConfirmed)
			e.progress = &reviewProgress{now: "abcdef", current: map[string]json.RawMessage{}}
			ref := vcs.Ref{Head: "abcdef"}
			first := e.verifyCached(t.Context(), ref, "system", newRepoTools(root, nil, ref), fs[0], nil)
			if !first.submitted || checker.calls != 4 || strings.Contains(first.evidence, "const Value") {
				t.Fatalf("control did not put the changed lookup beyond the evidence log: submitted=%t calls=%d evidence bytes=%d", first.submitted, checker.calls, len(first.evidence))
			}
			e.progress = &reviewProgress{now: "abcdef", prior: e.progress.current, current: map[string]json.RawMessage{}}
			unchanged := e.verifyCached(t.Context(), ref, "system", newRepoTools(root, nil, ref), fs[0], nil)
			if checker.calls != 4 || unchanged.verdict != verdictConfirmed || e.progress.reused != 1 {
				t.Fatalf("unchanged evidence was not reused: calls=%d reused=%d verdict=%s", checker.calls, e.progress.reused, unchanged.verdict)
			}
			if err := os.WriteFile(filepath.Join(root, "sibling.go"), []byte("const Value = 2\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			e.progress = &reviewProgress{now: "abcdef", prior: e.progress.current, current: map[string]json.RawMessage{}}
			changed := e.verifyCached(t.Context(), ref, "system", newRepoTools(root, nil, ref), fs[0], nil)
			if checker.calls != 8 || e.progress.reused != 0 || !changed.submitted {
				t.Fatalf("changed sibling evidence reused an old verdict: calls=%d reused=%d submitted=%t", checker.calls, e.progress.reused, changed.submitted)
			}
		})
	}
}

func TestResumeChecksAgainWhenRepositorySearchBecomesAvailable(t *testing.T) {
	fs := twoFindings()
	checker := &toolLLM{script: map[string][]llms.Response{fs[0].Title: {verdict(verdictUncertain, "no search available")}}}
	e, _ := verifyEngine(t, checker, config.VerifyConfirmed)
	e.progress = &reviewProgress{now: "abcdef", current: map[string]json.RawMessage{}}
	ref := vcs.Ref{Head: "abcdef"}
	e.verifyCached(t.Context(), ref, "system", newRepoTools("", nil, ref), fs[0], nil)
	e.progress = &reviewProgress{now: "abcdef", prior: e.progress.current, current: map[string]json.RawMessage{}}
	root := gitRepo(t, map[string]string{"sibling.go": "package sample\n"})
	e.verifyCached(t.Context(), ref, "system", newRepoTools(root, nil, ref), fs[0], nil)
	if checker.calls != 2 || e.progress.reused != 0 {
		t.Fatalf("a limited checker result survived the new search capability: calls=%d reused=%d", checker.calls, e.progress.reused)
	}
}

func TestStageKeyIncludesEffectiveToolsAndCallOptions(t *testing.T) {
	p := &reviewProgress{now: "abcdef"}
	client := llm.NewClientForTest(&scriptedLLM{}, config.ModelSpec{Provider: "openai", Model: "gpt-4o"})
	base := p.stageKey("verify", client, "system", "request", llms.WithTools(verifyTools(false)), llms.WithToolChoiceAuto())
	for name, opts := range map[string][]llms.CallOption{
		"search tool": {llms.WithTools(verifyTools(true)), llms.WithToolChoiceAuto()},
		"tool choice": {llms.WithTools(verifyTools(false)), llms.WithToolChoiceTool("submit_verdict")},
		"output cap":  {llms.WithTools(verifyTools(false)), llms.WithToolChoiceAuto(), llms.WithMaxTokens(100)},
	} {
		if p.stageKey("verify", client, "system", "request", opts...) == base {
			t.Errorf("%s did not change the key", name)
		}
	}
}

func TestResumeRetriesEmptyStoredTriageAnswers(t *testing.T) {
	for _, raw := range []string{"null", "{}", `{"findings":[],"summary":""}`} {
		t.Run(raw, func(t *testing.T) {
			fs := resumeFindings()
			model := &scriptedLLM{byPrompt: map[string]string{
				"triaging findings": mustJSON(t, TriageResult{Summary: "s", Verdicts: verdictsFor(fs)}),
			}}
			e := resumeEngine(t, model, &incrementalProvider{})
			e.progress = &reviewProgress{now: "abcdef", current: map[string]json.RawMessage{}}
			pr := &vcs.PullRequest{HeadSHA: "abcdef"}
			if _, _, _, err := e.triage(t.Context(), pr, fs); err != nil {
				t.Fatal(err)
			}
			if len(e.progress.current) != 1 {
				t.Fatal("control did not save triage")
			}
			for key := range e.progress.current {
				e.progress.current[key] = json.RawMessage(raw)
			}
			e.progress = &reviewProgress{now: "abcdef", prior: e.progress.current, current: map[string]json.RawMessage{}}
			if _, _, _, err := e.triage(t.Context(), pr, fs); err != nil {
				t.Fatal(err)
			}
			if len(model.prompts()) != 2 || e.progress.reused != 0 {
				t.Fatalf("empty stored triage was counted as a hit: calls=%d reused=%d", len(model.prompts()), e.progress.reused)
			}
		})
	}
}

func TestResumeReusesCritiqueAndRetriesEmptyStoredGrades(t *testing.T) {
	for _, raw := range []string{"", "null", "[]"} {
		t.Run(raw, func(t *testing.T) {
			reviewer := &deepLLM{critiqueReplies: []llms.Response{*gradeAll(t, "proven", "unsupported"), *gradeAll(t, "proven", "unsupported")}}
			e := critiqueEngine(t, reviewer)
			e.progress = &reviewProgress{now: "abcdef", current: map[string]json.RawMessage{}}
			fs := apart([]Finding{finding("real", "error", 0.9), finding("wrong", "error", 0.9)})
			first, dropped, failed := e.deepCritique(t.Context(), fs, nil)
			if failed != nil || len(first) != 1 || len(dropped) != 1 || len(e.progress.current) != 1 {
				t.Fatalf("critique control did not save grades: kept=%d dropped=%d failure=%v", len(first), len(dropped), failed)
			}
			if raw != "" {
				for key := range e.progress.current {
					e.progress.current[key] = json.RawMessage(raw)
				}
			}
			e.progress = &reviewProgress{now: "abcdef", prior: e.progress.current, current: map[string]json.RawMessage{}}
			second, dropped, failed := e.deepCritique(t.Context(), fs, nil)
			wantCalls, wantReused := 1, 1
			if raw != "" {
				wantCalls, wantReused = 2, 0
			}
			if failed != nil || len(second) != 1 || len(dropped) != 1 || reviewer.critiqued != wantCalls || e.progress.reused != wantReused {
				t.Fatalf("stored grades were mishandled: calls=%d reused=%d kept=%d dropped=%d failure=%v", reviewer.critiqued, e.progress.reused, len(second), len(dropped), failed)
			}
		})
	}
}

func TestVerificationTraceRejectsIncompleteCancelledOrFailedRevalidation(t *testing.T) {
	args := `{"path":"sibling.go"}`
	for _, name := range []string{"incomplete", "cancelled", "failed lookup"} {
		t.Run(name, func(t *testing.T) {
			provider := &lookupProvider{read: func(context.Context, string) error {
				return errors.New("temporary provider failure")
			}}
			tools := newRepoTools("", provider, vcs.Ref{})
			stored := storedVerdict{Verdict: verdictConfirmed, Complete: true}
			ctx := t.Context()
			switch name {
			case "incomplete":
				stored.Complete = false
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "failed lookup":
				// Even identical failure text is not successfully revalidated evidence.
				failed := tools.read(ctx, "sibling.go", 0, 0)
				stored.Lookups = []verdictLookup{{Tool: "read_file", Arguments: args, Digest: lookupDigest(failed)}}
			}
			p := &reviewProgress{prior: map[string]json.RawMessage{"v": json.RawMessage(mustJSONAny(t, stored))}, current: map[string]json.RawMessage{}}
			_, reused := loadStageChecked[storedVerdict](p, "v", rankVerify, func(s storedVerdict) bool { return s.matches(ctx, tools) })
			if reused || p.reused != 0 || len(p.current) != 0 {
				t.Fatalf("unvalidated record was promoted: reused=%t count=%d entries=%d", reused, p.reused, len(p.current))
			}
		})
	}
}
