package review

import (
	"context"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// TestCleanReviewDoesNotCallTriage pins the rule that a review with no findings
// never reaches the triage model, review.summary notwithstanding.
//
// The triage user message is the numbered findings list plus, when the forge
// supplies one, the pull request title. It never carries the diff. With an
// empty list the model is asked to write a walkthrough of a change it was
// never shown, and it answers with an invented one, on a fine-tuned
// gemma-4-E4B, confidently describing a retry wrapper around an HTTP client
// for a fixture whose change was a SQL migration. This test fails the moment
// the guard goes back to consulting review.summary, which is what made that
// call reachable.
func TestCleanReviewDoesNotCallTriage(t *testing.T) {
	model := &scriptedLLM{fallback: `{"findings":[]}`}
	provider := &stubProvider{diff: engineDiff}

	engine := newEngine(t, model, provider, func(cfg *config.Config) {
		cfg.Review.Summary = true
	})

	report, err := engine.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}

	if len(report.Findings) != 0 {
		t.Fatalf("findings = %d, want 0", len(report.Findings))
	}

	// Asserting on the prompts rather than on the summary, because a model that
	// happened to answer with an empty summary would hide the call that should
	// not have been made.
	for _, p := range model.prompts() {
		if strings.Contains(p, "findings were reported") || strings.Contains(p, "Write the walkthrough only") {
			t.Errorf("triage was called on a clean review:\n%s", p)
		}
	}

	if report.Summary != "" {
		t.Errorf("summary = %q, want empty: a walkthrough here describes a change nothing was shown", report.Summary)
	}
}

// TestCleanReviewStillPublishesItsNotices guards what skipping triage must NOT
// take with it. The notices are rendered from the report, not from the triage
// pass, and they are the reason silence is readable: a run whose analyzer never
// executed has to say so, or "no findings" reads as a clean bill of health.
func TestCleanReviewStillPublishesItsNotices(t *testing.T) {
	model := &scriptedLLM{fallback: `{"findings":[]}`}
	provider := &stubProvider{diff: engineDiff}

	engine := newEngine(t, model, provider, func(cfg *config.Config) {
		cfg.Review.Summary = true
	})

	report, err := engine.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}

	report.Incomplete = []string{"app.go"}

	rendered := Render(report, nil, engine.Config)
	if !strings.Contains(rendered.Summary, "incomplete") {
		t.Errorf("an incomplete clean review published no notice:\n%s", rendered.Summary)
	}
}
