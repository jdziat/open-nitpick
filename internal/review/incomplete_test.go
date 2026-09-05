package review

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	llms "github.com/nocturnium/llm-go-sdk/v6"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/llm"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// partialLLM fails review calls for batches containing a chosen file and
// succeeds otherwise, so a run can be forced into a partially-failed state.
type partialLLM struct {
	mu       sync.Mutex
	failOn   string
	reviewed string
	triage   string
}

func (p *partialLLM) GenerateContent(_ context.Context, msgs []llms.Message, _ ...llms.CallOption) (*llms.Response, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	var joined strings.Builder
	for _, m := range msgs {
		joined.WriteString(m.Content)
	}
	text := joined.String()

	if strings.Contains(text, "triaging findings") {
		return &llms.Response{Content: p.triage}, nil
	}
	if p.failOn != "" && strings.Contains(text, p.failOn) {
		return nil, errors.New("simulated provider failure")
	}
	return &llms.Response{Content: p.reviewed}, nil
}

func (p *partialLLM) Stream(context.Context, []llms.Message, ...llms.CallOption) (<-chan llms.StreamChunk, error) {
	return nil, errors.New("not supported")
}
func (p *partialLLM) Provider() llms.Provider { return "partial" }
func (p *partialLLM) Model() string           { return "partial" }

const twoFileDiff = `diff --git a/good.go b/good.go
--- a/good.go
+++ b/good.go
@@ -0,0 +1,2 @@
+package good
+var A = 1
diff --git a/doomed.go b/doomed.go
--- a/doomed.go
+++ b/doomed.go
@@ -0,0 +1,2 @@
+package doomed
+var B = 2
`

// TestPartialBatchFailureIsSurfaced is the regression test for the most
// misleading thing this tool could print.
//
// When some batches fail, the surviving findings are still published — but the
// files whose batch failed were never reviewed. Reporting that as a clean
// review tells the user their code passed when nobody looked at it.
func TestPartialBatchFailureIsSurfaced(t *testing.T) {
	model := &partialLLM{
		failOn:   "doomed.go",
		reviewed: `{"findings":[]}`,
		triage:   `{"findings":[],"summary":"Walkthrough."}`,
	}

	cfg := config.Defaults()
	cfg.Models.Default = config.ModelSpec{Provider: "openai", Model: "gpt-4o"}
	// One file per batch, so exactly one batch fails.
	cfg.Review.MaxFilesPerRequest = 1

	client := llm.NewClientForTest(model, cfg.Models.Default)
	provider := &stubProvider{diff: twoFileDiff}

	engine := &Engine{
		Config:   cfg,
		Roles:    &llm.Roles{Review: client, Triage: client},
		Provider: provider,
	}

	report, err := engine.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatalf("a partial failure should still produce a report: %v", err)
	}

	if report.Complete() {
		t.Fatal("report claims completeness despite a failed batch")
	}
	if len(report.Incomplete) != 1 || report.Incomplete[0] != "doomed.go" {
		t.Fatalf("Incomplete = %v, want [doomed.go]", report.Incomplete)
	}

	// The user has to be able to see it, not just the report struct.
	if provider.published == nil {
		t.Fatal("nothing was published")
	}
	summary := provider.published.Summary
	if !strings.Contains(summary, "incomplete") {
		t.Errorf("published summary must say the review is incomplete:\n%s", summary)
	}
	if !strings.Contains(summary, "doomed.go") {
		t.Errorf("published summary must name the unreviewed file:\n%s", summary)
	}
}

func TestCompleteReviewSaysNothingAboutIncompleteness(t *testing.T) {
	model := &partialLLM{
		reviewed: `{"findings":[]}`,
		triage:   `{"findings":[],"summary":"All good."}`,
	}

	cfg := config.Defaults()
	cfg.Models.Default = config.ModelSpec{Provider: "openai", Model: "gpt-4o"}

	client := llm.NewClientForTest(model, cfg.Models.Default)
	provider := &stubProvider{diff: twoFileDiff}

	engine := &Engine{
		Config:   cfg,
		Roles:    &llm.Roles{Review: client, Triage: client},
		Provider: provider,
	}

	report, err := engine.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if !report.Complete() {
		t.Errorf("a fully successful review should be complete, got Incomplete=%v", report.Incomplete)
	}
	if provider.published != nil && strings.Contains(provider.published.Summary, "incomplete") {
		t.Error("a complete review must not warn about incompleteness")
	}
}

// orderedLLM answers each batch with a finding naming that batch's file, and
// delays whichever file it is told to, so the batches finish in a chosen order
// rather than in path order. It keeps the triage prompt it was handed.
type orderedLLM struct {
	mu     sync.Mutex
	delay  string // finish the batch containing this file LAST
	triage string // the prompt renderForTriage produced
}

func (o *orderedLLM) GenerateContent(_ context.Context, msgs []llms.Message, _ ...llms.CallOption) (*llms.Response, error) {
	var joined strings.Builder
	for _, m := range msgs {
		joined.WriteString(m.Content)
	}
	text := joined.String()

	if strings.Contains(text, "triaging findings") {
		o.mu.Lock()
		o.triage = text
		o.mu.Unlock()
		return &llms.Response{Content: `{"findings":[],"summary":"Walkthrough."}`}, nil
	}

	file := "good.go"
	if strings.Contains(text, "doomed.go") {
		file = "doomed.go"
	}
	if file == o.delay {
		time.Sleep(75 * time.Millisecond)
	}

	return &llms.Response{Content: `{"findings":[{"path":"` + file +
		`","line":2,"severity":"warning","title":"finding in ` + file +
		`","rationale":"the declared value is never read","class":"correctness"}]}`}, nil
}

func (o *orderedLLM) Stream(context.Context, []llms.Message, ...llms.CallOption) (<-chan llms.StreamChunk, error) {
	return nil, errors.New("not supported")
}
func (o *orderedLLM) Provider() llms.Provider { return "ordered" }
func (o *orderedLLM) Model() string           { return "ordered" }

// TestTriageSeesTheSameOrderWhicheverBatchAnswersFirst pins the merge order of
// a multi-batch review.
//
// analyze runs batches concurrently and appends each result under a mutex, so
// the combined slice is in goroutine-COMPLETION order — a property of the
// scheduler, not of the change. Everything downstream reads that slice in
// order: renderForTriage numbers the findings for the triage model exactly as
// they sit, and dedupe keeps the FIRST of two equivalent findings. So a review
// pinned to temperature 0 because "reviews should be reproducible" was sending
// a different triage prompt on every run as soon as a change needed more than
// one request, and the eval corpus has just acquired its first fixture that
// does. The sibling slice one line away, unreviewed, was sorted for this exact
// reason; findings was missed.
//
// The test runs the SAME review twice with the batches finishing in opposite
// orders and requires one prompt.
func TestTriageSeesTheSameOrderWhicheverBatchAnswersFirst(t *testing.T) {
	run := func(delay string) string {
		model := &orderedLLM{delay: delay}

		cfg := config.Defaults()
		cfg.Models.Default = config.ModelSpec{Provider: "openai", Model: "gpt-4o"}
		// One file per batch, so both batches are in flight together and the
		// sleep decides which one lands first.
		cfg.Review.MaxFilesPerRequest = 1
		cfg.Review.MinSeverity = config.SeverityNit

		client := llm.NewClientForTest(model, cfg.Models.Default)
		engine := &Engine{
			Config:   cfg,
			Roles:    &llm.Roles{Review: client, Triage: client},
			Provider: &stubProvider{diff: twoFileDiff},
		}

		if _, err := engine.Review(context.Background(), vcs.Ref{}); err != nil {
			t.Fatalf("Review: %v", err)
		}

		model.mu.Lock()
		defer model.mu.Unlock()
		if model.triage == "" {
			t.Fatal("triage was never called, so this test proves nothing")
		}
		return model.triage
	}

	first := run("doomed.go") // good.go finishes first
	second := run("good.go")  // doomed.go finishes first

	if first != second {
		t.Errorf("the triage prompt depends on which batch answered first, so a multi-batch "+
			"review is not reproducible at temperature 0.\n--- good.go first ---\n%s\n--- doomed.go first ---\n%s",
			listing(first), listing(second))
	}
}

// listing extracts the numbered finding lines from a triage prompt, which is
// the part that differs, so a failure prints those rather than the whole
// system prompt.
func listing(prompt string) string {
	var out []string
	for _, line := range strings.Split(prompt, "\n") {
		if strings.Contains(line, ".go:") {
			out = append(out, strings.TrimSpace(line))
		}
	}
	return strings.Join(out, "\n")
}
