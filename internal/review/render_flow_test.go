package review

import (
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/diff"
)

// TestRenderKeepsFlowWhenNarrativeSummaryIsDisabled pins the flow contract:
// static coverage is independent of model-authored walkthrough prose.
func TestRenderKeepsFlowWhenNarrativeSummaryIsDisabled(t *testing.T) {
	cfg := config.Defaults()
	cfg.Review.Summary = false
	report := &Report{FlowMarkdown: "## Application flows\n\nstatus: partial", FlowEvidence: "## Application flows\n\nstatus: partial\n\n| Element | Source evidence |"}

	got := Render(report, diff.Files{}, cfg)
	if !strings.Contains(got.Summary, "## Application flows") {
		t.Fatalf("flow section disappeared with review.summary=false:\n%s", got.Summary)
	}
	if got.FlowMarkdown != report.FlowMarkdown {
		t.Fatalf("FlowMarkdown = %q, want %q", got.FlowMarkdown, report.FlowMarkdown)
	}
	if got.FlowEvidence != report.FlowEvidence {
		t.Fatalf("FlowEvidence = %q, want %q", got.FlowEvidence, report.FlowEvidence)
	}
}

// TestRenderDoesNotDuplicateFlowSection ensures providers can compose the
// review's Summary and FlowMarkdown fields without publishing two copies.
func TestRenderDoesNotDuplicateFlowSection(t *testing.T) {
	report := &Report{Summary: "walkthrough", FlowMarkdown: "## Application flows"}
	review := Render(report, diff.Files{}, config.Defaults())
	if got := strings.Count(review.Summary, "## Application flows"); got != 1 {
		t.Fatalf("flow section count = %d, want 1:\n%s", got, review.Summary)
	}
	if got := review.SummaryWithFlow(); got != review.Summary {
		t.Fatalf("SummaryWithFlow duplicated flow section:\n%s", got)
	}
}
