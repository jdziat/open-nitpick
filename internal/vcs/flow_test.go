package vcs

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestReviewSummaryWithFlowAppendsOnce(t *testing.T) {
	const flow = "## Application flows\n\nstatus: complete"
	cases := []struct {
		name string
		r    Review
		want string
	}{
		{name: "summary and flow", r: Review{Summary: "walkthrough", FlowMarkdown: flow}, want: "walkthrough\n\n" + flow},
		{name: "flow only", r: Review{FlowMarkdown: flow}, want: flow},
		{name: "already composed", r: Review{Summary: "walkthrough\n\n" + flow, FlowMarkdown: flow}, want: "walkthrough\n\n" + flow},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.r.SummaryWithFlow(); got != tc.want {
				t.Fatalf("SummaryWithFlow() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestReviewBodyLimitPreservesFindingsAndCompactFlowEvidence(t *testing.T) {
	summary := "## Findings\n" + strings.Repeat("A finding needs its source evidence.\n", 1000)
	flow := "## Application flows\n\nStatus: **partial**\n\n<details>" + strings.Repeat("full diagram should never be published\n", 3000) + "</details>"
	var evidence strings.Builder
	evidence.WriteString("## Application flows (static analysis)\n\nStatus: **partial**\n\nDiagrams and unchanged context were omitted to fit the review body. These are static source relationships, not runtime order or complete application coverage.\n\n| Element | Source evidence |\n| --- | --- |\n")
	for i := 0; i < 3000; i++ {
		fmt.Fprintf(&evidence, "| Node %d: changed | [changed.go:%d](<https://example.test/blob/head/changed.go#L%d>) |\n", i, i+1, i+1)
	}
	for _, composed := range []bool{true, false} {
		review := Review{Summary: summary, FlowMarkdown: flow, FlowEvidence: evidence.String()}
		if composed {
			review.Summary += "\n\n" + flow
		}
		body := review.SummaryWithFlow()
		if len(body) > 60<<10 || !strings.Contains(body, strings.TrimSpace(summary)) || !strings.Contains(body, "Status: **partial**") {
			t.Fatalf("bounded body lost findings or flow status: composed=%v bytes=%d", composed, len(body))
		}
		for _, want := range []string{"Diagrams and unchanged context were omitted", "Node 0: changed", "source-evidence row(s) were omitted"} {
			if !strings.Contains(body, want) {
				t.Errorf("bounded body omitted %q:\n%s", want, body)
			}
		}
		if strings.Contains(body, "full diagram should never be published") {
			t.Errorf("full diagram survived the compact fallback:\n%s", body)
		}
		for _, line := range strings.Split(body, "\n") {
			if strings.HasPrefix(line, "| Node ") && !strings.HasSuffix(line, "|") {
				t.Errorf("flow evidence row was split: %q", line)
			}
		}
	}
}

func TestSourceBaseReportsUnsupportedProvider(t *testing.T) {
	provider := noBaseProvider{}
	_, err := SourceBase(context.Background(), provider, Ref{}, "deadbeef")
	if !strings.Contains(err.Error(), provider.Name()) || !strings.Contains(err.Error(), ErrNoSourceLink.Error()) {
		t.Fatalf("SourceBase error = %v, want provider and ErrNoSourceLink", err)
	}
}

type sourceLinkProvider struct{ noBaseProvider }

func (sourceLinkProvider) SourceBase(context.Context, Ref, string) (string, error) {
	return "https://example.test/blob/deadbeef", nil
}

func TestSourceBaseUsesProviderLinker(t *testing.T) {
	got, err := SourceBase(context.Background(), sourceLinkProvider{}, Ref{}, "deadbeef")
	if err != nil {
		t.Fatalf("SourceBase: %v", err)
	}
	if got != "https://example.test/blob/deadbeef" {
		t.Fatalf("SourceBase = %q", got)
	}
}
