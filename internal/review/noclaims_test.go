package review

import (
	"context"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/llm"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

func origin(path string, line int, title, rationale string) Finding {
	return Finding{Path: path, Line: line, Title: title, Rationale: rationale,
		Severity: "warning", Class: "defect"}
}

// The case the path check was assumed to catch and never did: a reported path,
// the reviewer's line, and triage's words.
func TestTheContractHoldsThroughAWholeReview(t *testing.T) {
	reviewed := Finding{
		Path: "app.go", Line: 4, Severity: "critical",
		Class: string(config.ClassSecurity), Category: "security",
		Title: "Command injection", Rationale: "user input reaches a shell",
	}

	rewritten := reviewed
	rewritten.Title = "Potential shell metacharacter handling concern"
	rewritten.Rationale = "The input should be sanitized before it is passed along."

	for _, on := range []bool{false, true} {
		model := &scriptedLLM{byPrompt: map[string]string{
			"triaging findings":            mustJSON(t, TriageResult{Summary: "s", Verdicts: verdictsFor([]Finding{rewritten})}),
			"Review the following changes": mustJSON(t, Result{Findings: []Finding{reviewed}}),
		}}

		cfg := config.Defaults()
		cfg.Models.Default = config.ModelSpec{Provider: "openai", Model: "gpt-4o"}
		cfg.Review.TriageNoNewClaims = on

		client := llm.NewClientForTest(model, cfg.Models.Default)
		engine := &Engine{
			Config:   cfg,
			Roles:    &llm.Roles{Review: client, Triage: client},
			Provider: &stubProvider{diff: classDiff},
		}

		report, err := engine.Review(context.Background(), vcs.Ref{})
		if err != nil {
			t.Fatalf("Review: %v", err)
		}
		if len(report.Findings) != 1 {
			t.Fatalf("findings = %d, want 1", len(report.Findings))
		}

		got := report.Findings[0].Title
		if on && got != reviewed.Title {
			t.Errorf("with the contract on, title = %q, want the reviewer's %q", got, reviewed.Title)
		}
		if !on && got != rewritten.Title {
			t.Errorf("with the contract off, title = %q, want triage's %q; the test proves "+
				"nothing if both arms behave the same", got, rewritten.Title)
		}
	}
}

// A claim at a line no reviewer reported is dropped rather than published on a
// path that happens to be allowed.
func TestAClaimAtAnUnreportedLineIsDropped(t *testing.T) {
	reviewed := Finding{
		Path: "app.go", Line: 4, Severity: "warning",
		Class: string(config.ClassCorrectness), Title: "t", Rationale: "r",
	}
	elsewhere := reviewed
	elsewhere.Line = 4 + anchorTolerance + 40
	elsewhere.Title = "Something else entirely"

	model := &scriptedLLM{byPrompt: map[string]string{
		"triaging findings":            mustJSON(t, TriageResult{Summary: "s", Verdicts: verdictsFor([]Finding{elsewhere})}),
		"Review the following changes": mustJSON(t, Result{Findings: []Finding{reviewed}}),
	}}

	cfg := config.Defaults()
	cfg.Models.Default = config.ModelSpec{Provider: "openai", Model: "gpt-4o"}
	cfg.Review.TriageNoNewClaims = true

	client := llm.NewClientForTest(model, cfg.Models.Default)
	engine := &Engine{
		Config:   cfg,
		Roles:    &llm.Roles{Review: client, Triage: client},
		Provider: &stubProvider{diff: classDiff},
	}

	report, err := engine.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	for _, f := range report.Findings {
		if f.Title == "Something else entirely" {
			t.Errorf("a claim at an unreported line was published: %+v", f)
		}
	}
}

// A suggestion replaces the lines it is attached to, so restoring the
// reviewer's patch onto a line triage moved would offer a one-click commit
// over the wrong code.
