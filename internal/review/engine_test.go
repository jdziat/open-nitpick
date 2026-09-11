package review

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	llms "github.com/nocturnium/llm-go-sdk/v6"

	"github.com/jdziat/open-nitpick/internal/bundle"
	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/diff"
	"github.com/jdziat/open-nitpick/internal/llm"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// scriptedLLM answers each call with the next scripted response, matched by a
// substring of the prompt so review and triage can be scripted independently
// regardless of the order concurrent batches run in.
type scriptedLLM struct {
	mu sync.Mutex

	// byPrompt maps a prompt substring to the JSON to return.
	byPrompt map[string]string

	// fallback answers anything unmatched.
	fallback string

	// err, when set, fails every call.
	err error

	// onCall, when set, runs for every request before the answer is chosen. It
	// exists so a test can observe how many calls are in flight at once.
	onCall func()

	// seen records every prompt sent, so a test can assert on what the model
	// was TOLD. Asserting only on what came back would pass against a
	// prompt still carrying an injection the model happened to ignore.
	seen []string

	calls int
}

func (s *scriptedLLM) GenerateContent(_ context.Context, msgs []llms.Message, _ ...llms.CallOption) (*llms.Response, error) {
	// Outside the lock, because the lock serialises every call and a hook
	// counting requests in flight would then always see one.
	s.mu.Lock()
	hook := s.onCall
	s.mu.Unlock()
	if hook != nil {
		hook()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.calls++

	var joined strings.Builder
	for _, m := range msgs {
		joined.WriteString(m.Content)
	}
	text := joined.String()
	s.seen = append(s.seen, text)

	if s.err != nil {
		return nil, s.err
	}

	for needle, response := range s.byPrompt {
		if strings.Contains(text, needle) {
			return &llms.Response{Content: response}, nil
		}
	}
	// A fallback is written for the review pass, and the triage pass has a
	// different shape. Answering triage with a review-shaped reply is not a
	// no-op: every verdict names no finding, so triage is skipped entirely and
	// a test that meant "triage is not the subject here" has quietly stopped
	// exercising it. An empty verdict list says the same thing truthfully, and
	// the findings restore.
	if strings.Contains(text, "triaging findings") && !strings.Contains(s.fallback, `"number"`) {
		return &llms.Response{Content: `{"findings":[],"summary":""}`}, nil
	}
	return &llms.Response{Content: s.fallback}, nil
}

func (s *scriptedLLM) Stream(context.Context, []llms.Message, ...llms.CallOption) (<-chan llms.StreamChunk, error) {
	return nil, errors.New("not supported")
}

func (s *scriptedLLM) Provider() llms.Provider { return "scripted" }
func (s *scriptedLLM) Model() string           { return "scripted" }

func (s *scriptedLLM) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// prompts returns every prompt the model was sent.
func (s *scriptedLLM) prompts() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.seen...)
}

// mustJSON encodes a Result as the model would return it.
func mustJSON[T Result | TriageResult](t *testing.T, r T) string {
	t.Helper()

	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// stubProvider serves a fixed diff and captures the published review.
type stubProvider struct {
	diff      string
	content   map[string]string
	published *vcs.Review
	err       error
}

func (s *stubProvider) Name() string { return "stub" }

func (s *stubProvider) PullRequest(context.Context, vcs.Ref) (*vcs.PullRequest, error) {
	return &vcs.PullRequest{Number: 1, Title: "Add retry", Body: "Retries transient failures."}, nil
}

func (s *stubProvider) Diff(context.Context, vcs.Ref) ([]byte, error) {
	return []byte(s.diff), nil
}

func (s *stubProvider) FileContent(_ context.Context, _ vcs.Ref, path string) ([]byte, error) {
	body, ok := s.content[path]
	if !ok {
		return nil, vcs.ErrNotFound
	}
	return []byte(body), nil
}

func (s *stubProvider) PublishReview(_ context.Context, _ vcs.Ref, r vcs.Review) error {
	if s.err != nil {
		return s.err
	}
	s.published = &r
	return nil
}

const engineDiff = `diff --git a/app.go b/app.go
index 111..222 100644
--- a/app.go
+++ b/app.go
@@ -1,4 +1,7 @@
 package app

-func Get() error {
-	return nil
+func Get() error {
+	resp, _ := http.Get("http://x")
+	defer resp.Body.Close()
+
+	return nil
 }
`

// newEngine wires an engine around one scripted model.
func newEngine(t *testing.T, model *scriptedLLM, provider vcs.Provider, tune func(*config.Config)) *Engine {
	t.Helper()

	cfg := config.Defaults()
	cfg.Models.Default = config.ModelSpec{Provider: "openai", Model: "gpt-4o"}
	if tune != nil {
		tune(cfg)
	}

	client := llm.NewClientForTest(model, cfg.Models.Default)

	return &Engine{
		Config:   cfg,
		Roles:    &llm.Roles{Review: client, Triage: client},
		Provider: provider,
	}
}

func TestReviewEndToEnd(t *testing.T) {
	reviewOut := mustJSON(t, Result{Findings: []Finding{{
		Path: "app.go", Line: 4, Severity: "error", Category: "correctness",
		Title: "Ignored error from http.Get", Rationale: "resp may be nil, so the deferred Close panics.",
	}}})
	triageOut := mustJSON(t, TriageResult{
		Summary: "Adds a retry path to Get.",
		Verdicts: verdictsFor([]Finding{{
			Path: "app.go", Line: 4, Severity: "error", Category: "correctness",
			Title: "Ignored error from http.Get", Rationale: "resp may be nil, so the deferred Close panics.",
		}}),
	})

	model := &scriptedLLM{byPrompt: map[string]string{
		"triaging findings":            triageOut,
		"Review the following changes": reviewOut,
	}}
	provider := &stubProvider{diff: engineDiff}

	engine := newEngine(t, model, provider, nil)

	report, err := engine.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}

	if len(report.Findings) != 1 {
		t.Fatalf("findings = %d, want 1: %+v", len(report.Findings), report.Findings)
	}
	if report.Summary != "Adds a retry path to Get." {
		t.Errorf("summary = %q", report.Summary)
	}

	if provider.published == nil {
		t.Fatal("review was not published")
	}
	if len(provider.published.Comments) != 1 {
		t.Fatalf("comments = %d, want 1", len(provider.published.Comments))
	}

	c := provider.published.Comments[0]
	if c.Path != "app.go" || c.Line != 4 {
		t.Errorf("comment anchored to %s:%d, want app.go:4", c.Path, c.Line)
	}
	if c.Side != string(diff.SideRight) {
		t.Errorf("side = %q, want RIGHT", c.Side)
	}
	if !strings.Contains(c.Body, "Ignored error from http.Get") {
		t.Errorf("comment body missing the title:\n%s", c.Body)
	}
}

// TestRewritingAModelsSeverityRecordsTheModelsWord pins the provenance half of
// severity normalization.
//
// THE BUG: normalizeSeverity returned only the level and threw the model's
// word away. internal/evals then published a block captioned as each
// contender's own severity vocabulary, and answered "was this translated?"
// from the finding's SOURCE, so every model this project ships was reported as
// having printed the word we had just written over it. The same defect had
// been found and fixed for the incumbent, where a lost word at least prints
// "(word not recorded)"; here the substitute was quoted silently as the
// model's own.
//
// Both directions are asserted, and the second is the one that keeps the fix
// honest: a model writing a level we already use has not been translated, and
// marking it as though it had would make every finding in the tree unquotable
// and the flag meaningless.
func TestRewritingAModelsSeverityRecordsTheModelsWord(t *testing.T) {
	cases := map[string]struct {
		said       string
		want       string
		translated bool
	}{
		"unrecognized vocabulary": {said: "P1", want: "info", translated: true},
		"our word, wrong case":    {said: "Warning", want: "warning", translated: true},
		"our own word":            {said: "warning", want: "warning", translated: false},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			out := mustJSON(t, Result{Findings: []Finding{
				{Path: "app.go", Line: 4, Severity: tc.said, Title: "Ignored error from http.Get"},
			}})

			model := &scriptedLLM{fallback: out}
			engine := newEngine(t, model, &stubProvider{diff: engineDiff}, func(c *config.Config) {
				c.Review.MinSeverity = config.SeverityNit
			})

			report, err := engine.Review(context.Background(), vcs.Ref{})
			if err != nil {
				t.Fatalf("Review: %v", err)
			}
			if len(report.Findings) != 1 {
				t.Fatalf("findings = %d, want 1: %+v", len(report.Findings), report.Findings)
			}

			f := report.Findings[0]
			if f.Severity != tc.want {
				t.Errorf("severity = %q, want %q", f.Severity, tc.want)
			}
			if f.SeverityTranslated != tc.translated {
				t.Errorf("SeverityTranslated = %v, want %v for a model that said %q and was published "+
					"at %q", f.SeverityTranslated, tc.translated, tc.said, f.Severity)
			}

			switch {
			case tc.translated && f.RawSeverity != tc.said:
				t.Errorf("RawSeverity = %q, want %q. The model's word was destroyed here, so every "+
					"report downstream quotes it as having said %q — a word this package chose",
					f.RawSeverity, tc.said, f.Severity)
			case !tc.translated && f.RawSeverity != "":
				t.Errorf("RawSeverity = %q for a finding nothing translated; Severity is already the "+
					"model's own word and a second copy of it invents a distinction", f.RawSeverity)
			}
		})
	}
}

// TestAnExpertRerateClearsTheEarlierTranslation: a re-rating is a fresh claim by
// a reporter, so any record of an earlier translation is stale.
//
// Left in place, a review model's "P1" would keep travelling beside a severity
// the EXPERT chose, and the eval report would quote the finding as saying "P1"
// while publishing the expert's word.
func TestAnExpertRerateClearsTheEarlierTranslation(t *testing.T) {
	f := Finding{
		Path: "app.go", Line: 4, Class: string(config.ClassCorrectness),
		Severity: "info", SeverityTranslated: true, RawSeverity: "P1",
		Title: "Ignored error",
	}

	kept, _ := applyOutcomes([]outcome{{finding: f, revised: config.SeverityWarning, expert: "go"}})

	if len(kept) != 1 {
		t.Fatalf("kept = %d, want 1", len(kept))
	}
	if kept[0].Severity != "warning" {
		t.Fatalf("severity = %q, want warning", kept[0].Severity)
	}
	if kept[0].SeverityTranslated || kept[0].RawSeverity != "" {
		t.Errorf("the re-rated finding still carries translated=%v raw=%q from before the expert "+
			"rewrote it. The expert WROTE this level, so it is a reporter's own word again",
			kept[0].SeverityTranslated, kept[0].RawSeverity)
	}
}

// TestAnExpertRerateKeepsAnAnalyzersWord is the opposite rule for the opposite
// kind of reporter, and clearing the pair for both was a bug.
//
// A model's raw word is its own RATING, so an expert re-rating makes it stale.
// An analyzer's raw word is what the tool PRINTED, and semgrep does not
// retract CRITICAL because an expert disagreed about impact. The finding is
// still published as "flagged by semgrep(...)" with a level that is ours
// rather than semgrep's, so zeroing the pair here made the report assert
// semgrep's own word for it was "warning", the substitution these two fields
// exist to prevent, on the one class of finding that names a third party.
func TestAnExpertRerateKeepsAnAnalyzersWord(t *testing.T) {
	f := Finding{
		Path: "app.go", Line: 4, Class: string(config.ClassSecurity),
		Severity: "critical", SeverityTranslated: true, RawSeverity: "CRITICAL",
		FromAnalyzer: true, Source: "semgrep(go.lang.security.audit.dangerous-exec-command)",
		Title: "Command built from user input",
	}

	kept, _ := applyOutcomes([]outcome{{finding: f, revised: config.SeverityWarning, expert: "go"}})

	if len(kept) != 1 {
		t.Fatalf("kept = %d, want 1", len(kept))
	}
	if kept[0].Severity != "warning" {
		t.Fatalf("severity = %q, want warning", kept[0].Severity)
	}
	if !kept[0].SeverityTranslated || kept[0].RawSeverity != "CRITICAL" {
		t.Errorf("the re-rated finding is published as %s with translated=%v raw=%q, which reads "+
			"as that analyzer having printed our level", kept[0].Source,
			kept[0].SeverityTranslated, kept[0].RawSeverity)
	}
}

func TestFindingsOutsideTheDiffAreDropped(t *testing.T) {
	// Anchoring a comment to a line the change did not touch produces a
	// comment on unrelated code, which the forge may reject outright.
	out := mustJSON(t, Result{Findings: []Finding{
		{Path: "app.go", Line: 9999, Severity: "error", Title: "Nowhere near the diff"},
		{Path: "ghost.go", Line: 1, Severity: "error", Title: "File not in the diff"},
	}})

	model := &scriptedLLM{fallback: out}
	engine := newEngine(t, model, &stubProvider{diff: engineDiff}, nil)

	report, err := engine.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if len(report.Findings) != 0 {
		t.Errorf("findings = %+v, want none to survive anchoring", report.Findings)
	}
}

func TestNearMissFindingsAreSnapped(t *testing.T) {
	// Models routinely anchor a line or two off; discarding those loses real
	// issues, so they are snapped onto the nearest changed line.
	reviewOut := mustJSON(t, Result{Findings: []Finding{
		{Path: "app.go", Line: 7, Severity: "warning", Title: "Close may panic"},
	}})

	model := &scriptedLLM{byPrompt: map[string]string{
		"triaging findings":            mustJSON(t, TriageResult{Summary: "s", Verdicts: verdictsFor([]Finding{{Path: "app.go", Line: 6, Severity: "warning", Title: "Close may panic"}})}),
		"Review the following changes": reviewOut,
	}}
	engine := newEngine(t, model, &stubProvider{diff: engineDiff}, nil)

	report, err := engine.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if len(report.Findings) != 1 {
		t.Fatalf("findings = %d, want the near miss to be recovered", len(report.Findings))
	}
}

func TestInvalidFindingsAreDiscarded(t *testing.T) {
	// A model satisfying the schema with an empty object would otherwise
	// produce a blank comment on line zero.
	out := mustJSON(t, Result{Findings: []Finding{
		{},
		{Path: "app.go", Line: 0, Title: "no line"},
		{Path: "", Line: 4, Title: "no path"},
	}})

	model := &scriptedLLM{fallback: out}
	engine := newEngine(t, model, &stubProvider{diff: engineDiff}, nil)

	report, err := engine.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if len(report.Findings) != 0 {
		t.Errorf("findings = %+v, want all invalid ones dropped", report.Findings)
	}
}

// A triage pass could once place a comment on a file nobody reported on, by
// returning a finding with a path of its own. TestTriageCannotInventFindingsForOtherFiles
// guarded that. A verdict carries no path: it names a finding by its number
// and the engine publishes its own object, so there is no longer a way to
// express the claim the test refuted. Deleted rather than rewritten, because a
// test of an inexpressible state asserts nothing.
func TestMinSeverityGate(t *testing.T) {
	out := mustJSON(t, Result{Summary: "s", Findings: []Finding{
		{Path: "app.go", Line: 4, Severity: "nit", Title: "A nit"},
		{Path: "app.go", Line: 5, Severity: "error", Title: "An error"},
	}})

	model := &scriptedLLM{fallback: out}
	engine := newEngine(t, model, &stubProvider{diff: engineDiff}, func(c *config.Config) {
		c.Review.MinSeverity = config.SeverityWarning
	})

	report, err := engine.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if len(report.Findings) != 1 || report.Findings[0].Severity != "error" {
		t.Errorf("findings = %+v, want only the error to survive the gate", report.Findings)
	}
}

func TestFailedAppliesTheGate(t *testing.T) {
	report := &Report{Findings: []Finding{{Severity: "warning"}}}

	if !report.Failed(config.SeverityWarning) {
		t.Error("a warning should trip a warning gate")
	}
	if report.Failed(config.SeverityError) {
		t.Error("a warning should not trip an error gate")
	}
	// fail_on: none is a never-fail policy.
	if report.Failed(config.SeverityNone) {
		t.Error("fail_on none must never fail the run")
	}
}

func TestTriageFailureStillPublishesFindings(t *testing.T) {
	// Losing a whole review because the summarizer failed would be a bad
	// trade; deduplicated findings are still worth publishing.
	reviewOut := mustJSON(t, Result{Findings: []Finding{
		{Path: "app.go", Line: 4, Severity: "error", Title: "Real finding"},
	}})

	model := &scriptedLLM{byPrompt: map[string]string{
		"Review the following changes": reviewOut,
		"triaging findings":            "this is not json and never will be",
	}}
	engine := newEngine(t, model, &stubProvider{diff: engineDiff}, nil)

	report, err := engine.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatalf("Review should survive a triage failure: %v", err)
	}
	if len(report.Findings) != 1 {
		t.Errorf("findings = %+v, want the review findings to survive", report.Findings)
	}

	// The half that was missing. Surviving the failure is right; reporting the
	// pipeline finished is what made a dead triage read as a clean review.
	if report.PipelineComplete() {
		t.Error("PipelineComplete() = true after triage failed")
	}
	if !report.Complete() {
		t.Error("Complete() = false; triage failing is not a file going unread")
	}
	if got := report.FailedStages(); len(got) != 1 || got[0] != "triage" {
		t.Errorf("FailedStages() = %v, want [triage]", got)
	}
	// The reason is this code's own word, not the model's answer, because it
	// reaches a pull request comment.
	if r := report.Stages[0].Reason; r == "" || strings.Contains(r, "not json") {
		t.Errorf("Stages[0].Reason = %q, want a sanitized kind", r)
	}
}

// A clean review never calls triage at all, so there is no stage to fail. If
// the skip started reporting one, every clean review would exit 2.
func TestACleanReviewRecordsNoStageFailure(t *testing.T) {
	model := &scriptedLLM{byPrompt: map[string]string{
		"Review the following changes": mustJSON(t, Result{Findings: nil}),
	}}
	engine := newEngine(t, model, &stubProvider{diff: engineDiff}, nil)

	report, err := engine.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if !report.PipelineComplete() {
		t.Errorf("PipelineComplete() = false on a clean review; stages = %+v", report.Stages)
	}
}

func TestAllBatchesFailingIsAnError(t *testing.T) {
	// Reporting "no issues found" when every call failed would be a lie, and
	// in CI it would look like a passing review.
	model := &scriptedLLM{err: errors.New("401 unauthorized")}
	engine := newEngine(t, model, &stubProvider{diff: engineDiff}, nil)

	_, err := engine.Review(context.Background(), vcs.Ref{})
	if err == nil {
		t.Fatal("want an error when every batch fails")
	}
	if !strings.Contains(err.Error(), "batches failed") {
		t.Errorf("error should explain the systemic failure, got: %v", err)
	}
}

func TestEmptyDiffPublishesNothing(t *testing.T) {
	model := &scriptedLLM{fallback: "{}"}
	provider := &stubProvider{diff: ""}
	engine := newEngine(t, model, provider, nil)

	report, err := engine.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if len(report.Findings) != 0 {
		t.Errorf("findings = %+v, want none", report.Findings)
	}
	if model.callCount() != 0 {
		t.Errorf("model calls = %d, want 0: an empty diff should cost nothing", model.callCount())
	}
	if provider.published != nil {
		t.Error("nothing should be published for an empty diff")
	}
}

func TestSkippedFilesAreSurfacedInTheSummary(t *testing.T) {
	// A review that quietly ignored files otherwise reads as a clean bill of
	// health.
	const d = engineDiff + `diff --git a/huge.bin b/huge.bin
index 1..2 100644
Binary files a/huge.bin and b/huge.bin differ
`
	out := mustJSON(t, Result{Summary: "Walkthrough."})

	model := &scriptedLLM{fallback: out}
	provider := &stubProvider{diff: d}
	engine := newEngine(t, model, provider, nil)

	if _, err := engine.Review(context.Background(), vcs.Ref{}); err != nil {
		t.Fatalf("Review: %v", err)
	}

	if provider.published == nil {
		t.Fatal("expected a published summary")
	}
	summary := provider.published.Summary
	if !strings.Contains(summary, "huge.bin") {
		t.Errorf("summary should name the skipped file:\n%s", summary)
	}
	if !strings.Contains(summary, "Files not reviewed") {
		t.Errorf("summary should have a skipped-files section:\n%s", summary)
	}
}

func TestDegradedFilesAreNotReportedAsUnreviewed(t *testing.T) {
	// Found by running open-nitpick on its own history. A file whose content
	// fetch fails is reviewed from the diff alone, but was recorded in
	// Plan.Skipped, which the summary prints under "Files not reviewed". Claiming
	// a reviewed file was not reviewed is the same class of error as hiding a
	// skipped one, just in the opposite direction.
	report := &Report{
		Summary: "Walkthrough.",
		Plan: &bundle.Plan{
			Degraded: []bundle.Skip{{Path: "a.go", Reason: bundle.ReasonUnavailable}},
		},
	}

	summary := renderSummary(report, nil)

	if !strings.Contains(summary, "a.go") {
		t.Errorf("summary should name the degraded file:\n%s", summary)
	}
	if strings.Contains(summary, "Files not reviewed") {
		t.Errorf("a file reviewed diff-only must not be listed as not reviewed:\n%s", summary)
	}
}

func TestRenderCommentIncludesSuggestionBlock(t *testing.T) {
	// A single line of real code is the only shape GitHub can apply correctly
	// to a single-line anchor.
	body := renderComment2(Finding{
		Severity: "warning", Category: "correctness",
		Title: "Check the error", Rationale: "resp may be nil.",
		Suggestion: "\tresp, err := http.Get(u)",
	})

	if !strings.Contains(body, "```suggestion\n") {
		t.Errorf("a single-line code suggestion should render as an applicable block:\n%s", body)
	}
	if !strings.Contains(body, "warning") {
		t.Errorf("severity should be visible:\n%s", body)
	}
}

func TestRenderCommentOmitsEmptySuggestion(t *testing.T) {
	body := renderComment2(Finding{Severity: "nit", Title: "x", Suggestion: "   \n"})

	if strings.Contains(body, "```suggestion") {
		t.Errorf("a blank suggestion should not produce an empty block:\n%s", body)
	}
}

func TestSortFindingsMostSevereFirst(t *testing.T) {
	findings := []Finding{
		{Path: "b.go", Line: 1, Severity: "nit"},
		{Path: "a.go", Line: 5, Severity: "critical"},
		{Path: "a.go", Line: 2, Severity: "warning"},
		{Path: "a.go", Line: 1, Severity: "warning"},
	}
	sortFindings(findings)

	if findings[0].Severity != "critical" {
		t.Errorf("first = %+v, want the critical finding", findings[0])
	}
	// Stable within a severity: path then line, so runs are diffable.
	if findings[1].Line != 1 || findings[2].Line != 2 {
		t.Errorf("warnings should be ordered by line: %+v", findings[1:3])
	}
	if findings[3].Severity != "nit" {
		t.Errorf("last = %+v, want the nit", findings[3])
	}
}

func TestDedupeCollapsesIdenticalFindings(t *testing.T) {
	findings := []Finding{
		{Path: "a.go", Line: 4, Title: "Unchecked error"},
		{Path: "a.go", Line: 4, Title: "  unchecked   ERROR "},
		{Path: "a.go", Line: 5, Title: "Unchecked error"},
	}

	got := dedupe(findings)
	if len(got) != 2 {
		t.Errorf("dedupe = %d findings, want 2 (wording and case must not defeat it)", len(got))
	}
}

func TestCountsRenderingIncludesSeverityTotals(t *testing.T) {
	c := counts([]Finding{
		{Severity: "error"}, {Severity: "error"}, {Severity: "nit"},
	})

	got := c.String()
	if !strings.Contains(got, "2 error") || !strings.Contains(got, "1 nit") {
		t.Errorf("counts = %q", got)
	}
	if counts(nil).String() != "no findings" {
		t.Errorf("empty counts = %q", counts(nil).String())
	}
}

func TestEngineValidatesWiring(t *testing.T) {
	cases := map[string]*Engine{
		"no config":   {Roles: &llm.Roles{}, Provider: &stubProvider{}},
		"no models":   {Config: config.Defaults(), Provider: &stubProvider{}},
		"no provider": {Config: config.Defaults(), Roles: &llm.Roles{}},
	}

	for name, e := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := e.Review(context.Background(), vcs.Ref{}); err == nil {
				t.Error("want a wiring error before any work is done")
			}
		})
	}
}

func TestLocalProviderRendersReview(t *testing.T) {
	// The offline path is how the engine is developed, so it has to work.
	var out bytes.Buffer

	out.Reset()
	local := &vcs.Local{Dir: ".", Out: &out}

	err := local.PublishReview(context.Background(), vcs.Ref{}, vcs.Review{
		Summary:  "Summary here.",
		Comments: []vcs.Comment{{Path: "app.go", Line: 4, Body: "Something is wrong."}},
	})
	if err != nil {
		t.Fatalf("PublishReview: %v", err)
	}
	if !strings.Contains(out.String(), "app.go:4") {
		t.Errorf("local output should be clickable:\n%s", out.String())
	}
}

// A model that writes an em dash, filler and a chat opener has all three
// removed before the review is published: the prompt's voice layer asks,
// this enforces. Code spans are left as the model wrote them.
func TestPublishedProseIsScrubbed(t *testing.T) {
	out := mustJSON(t, Result{
		Summary: "Sure! This change actually adds a retry — with backoff.",
		Findings: []Finding{{
			Path: "app.go", Line: 4, Severity: "error", Category: "correctness",
			Title:     "Deferred close panics — resp is nil",
			Rationale: "It is actually unchecked. Use `a — b` as written. Let me know if you want more.",
		}},
	})
	model := &scriptedLLM{fallback: out}
	report, err := newEngine(t, model, &stubProvider{diff: engineDiff}, nil).Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if len(report.Findings) != 1 {
		t.Fatalf("findings = %+v", report.Findings)
	}
	f := report.Findings[0]
	if strings.Contains(f.Title+f.Rationale+report.Summary, "—") && !strings.Contains(f.Rationale, "`a — b`") {
		t.Errorf("an em dash survived outside code: %+v", f)
	}
	if f.Title != "Deferred close panics, resp is nil" {
		t.Errorf("title = %q", f.Title)
	}
	if !strings.Contains(f.Rationale, "`a — b`") {
		t.Errorf("a code span was rewritten: %q", f.Rationale)
	}
	if strings.Contains(f.Rationale, "actually") || strings.Contains(f.Rationale, "Let me know") {
		t.Errorf("filler or chat survived: %q", f.Rationale)
	}
	if strings.HasPrefix(report.Summary, "Sure!") || strings.Contains(report.Summary, "actually") {
		t.Errorf("summary = %q", report.Summary)
	}
}

// The style pass runs beside the defect review, not after it.
//
// It reads the plan and not the findings, so nothing in it depended on the
// review it used to wait for. Issue #81, cause 4: a one-batch change could not
// use its configured concurrency because every stage was serial.
func TestTheStylePassRunsBesideTheReview(t *testing.T) {
	var (
		mu       sync.Mutex
		inFlight int
		peak     int
	)
	model := &scriptedLLM{fallback: `{"findings":[]}`, onCall: func() {
		mu.Lock()
		inFlight++
		if inFlight > peak {
			peak = inFlight
		}
		mu.Unlock()
		time.Sleep(20 * time.Millisecond)
		mu.Lock()
		inFlight--
		mu.Unlock()
	}}

	engine := newEngine(t, model, &stubProvider{diff: engineDiff}, nil)
	engine.Config.Persona.Nitpick = config.NitpickPedantic
	engine.Config.Review.Concurrency = 4

	if _, err := engine.Review(context.Background(), vcs.Ref{}); err != nil {
		t.Fatalf("Review: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if peak < 2 {
		t.Errorf("peak in-flight calls = %d, want the style pass overlapping the review", peak)
	}
	// And the shared bound holds: two semaphores would have allowed eight.
	if peak > 4 {
		t.Errorf("peak in-flight calls = %d, above review.concurrency of 4", peak)
	}
}

// A long pull request body is measured, not allowed for.
//
// The body is whatever its author wrote, so a flat allowance is a number that
// is right until someone writes a long one, and the budget it protects is what
// keeps a request inside the model's input window.
func TestTheFramingReserveGrowsWithThePullRequestBody(t *testing.T) {
	engine := newEngine(t, &scriptedLLM{fallback: `{"findings":[]}`}, &stubProvider{diff: engineDiff}, nil)

	short := engine.framingTokens(&vcs.PullRequest{Title: "t", Body: "short"})
	long := engine.framingTokens(&vcs.PullRequest{
		Title: "t",
		Body:  strings.Repeat("a paragraph of release notes nobody trimmed. ", 400),
	})

	if long <= short {
		t.Errorf("a long body reserved %d tokens and a short one %d; the body is not being measured", long, short)
	}
	// And a nil pull request is not a panic: the local driver has none.
	if engine.framingTokens(nil) <= 0 {
		t.Error("a run with no pull request reserved nothing for its system prompt")
	}
}
