package review

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	llms "github.com/nocturnium/llm-go-sdk"

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
