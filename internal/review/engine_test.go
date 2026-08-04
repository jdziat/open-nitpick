package review

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	llms "github.com/nocturnium/llm-go-sdk"

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

	// seen records every prompt sent, so a test can assert on what the model
	// was actually TOLD. Asserting only on what came back would pass against a
	// prompt still carrying an injection the model happened to ignore.
	seen []string

	calls int
}

func (s *scriptedLLM) GenerateContent(_ context.Context, msgs []llms.Message, _ ...llms.CallOption) (*llms.Response, error) {
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
func mustJSON(t *testing.T, r Result) string {
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
	triageOut := mustJSON(t, Result{
		Summary: "Adds a retry path to Get.",
		Findings: []Finding{{
			Path: "app.go", Line: 4, Severity: "error", Category: "correctness",
			Title: "Ignored error from http.Get", Rationale: "resp may be nil, so the deferred Close panics.",
		}},
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
		"triaging findings":            mustJSON(t, Result{Summary: "s", Findings: []Finding{{Path: "app.go", Line: 6, Severity: "warning", Title: "Close may panic"}}}),
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

func TestTriageCannotInventFindingsForOtherFiles(t *testing.T) {
	// Triage may merge and reword, but a summarizing model must not be able to
	// place comments on files nobody reported on.
	reviewOut := mustJSON(t, Result{Findings: []Finding{
		{Path: "app.go", Line: 4, Severity: "warning", Title: "Real finding"},
	}})
	triageOut := mustJSON(t, Result{
		Summary: "ok",
		Findings: []Finding{
			{Path: "app.go", Line: 4, Severity: "warning", Title: "Real finding"},
			{Path: "/etc/passwd", Line: 1, Severity: "critical", Title: "Invented"},
		},
	})

	model := &scriptedLLM{byPrompt: map[string]string{
		"triaging findings":            triageOut,
		"Review the following changes": reviewOut,
	}}
	engine := newEngine(t, model, &stubProvider{diff: engineDiff}, nil)

	report, err := engine.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	for _, f := range report.Findings {
		if f.Path != "app.go" {
			t.Errorf("triage invented a finding for %q", f.Path)
		}
	}
}

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
	// Plan.Skipped — which the summary prints under "Files not reviewed".
	// Claiming a reviewed file was not reviewed is the same class of error as
	// hiding a skipped one, just in the opposite direction.
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

func TestCountsRendering(t *testing.T) {
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
