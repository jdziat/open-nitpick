package review

import (
	"context"
	"strings"
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
func TestTriagesRewriteIsReplacedByTheReviewersWords(t *testing.T) {
	origins := originsOf([]Finding{
		origin("a.go", 42, "Unchecked error from Close", "The write may be lost."),
	})

	rewritten := Finding{Path: "a.go", Line: 42,
		Title:     "Resource leak on the error path",
		Rationale: "The file handle is never released, so the process leaks descriptors."}

	got, ok := origins.find(rewritten.Path, rewritten.Line)
	if !ok {
		t.Fatal("the finding did not match its own origin")
	}
	changed := restore(&rewritten, got)

	if rewritten.Title != "Unchecked error from Close" {
		t.Errorf("title = %q, want the reviewer's", rewritten.Title)
	}
	if rewritten.Rationale != "The write may be lost." {
		t.Errorf("rationale = %q, want the reviewer's", rewritten.Rationale)
	}
	if len(changed) != 2 {
		t.Errorf("changed = %v, want title and rationale", changed)
	}
}

// Key() carries the normalized title, so a reworded finding does not match
// itself by key. That is why identity here is path and line.
func TestARewordedFindingDoesNotMatchItselfByKey(t *testing.T) {
	a := origin("a.go", 42, "Unchecked error from Close", "x")
	b := origin("a.go", 42, "Resource leak on the error path", "y")

	if a.Key() == b.Key() {
		t.Fatal("Key() ignores the title, so this guard could have used it")
	}
	if _, ok := originsOf([]Finding{a}).find(b.Path, b.Line); !ok {
		t.Error("path and line failed to recognize the same finding reworded")
	}
}

// Triage may move a finding onto the line that changed. Beyond tolerance it is
// a claim about somewhere else.
func TestReAnchoringIsAllowedWithinTolerance(t *testing.T) {
	o := originsOf([]Finding{origin("a.go", 100, "t", "r")})

	if _, ok := o.find("a.go", 100+anchorTolerance); !ok {
		t.Errorf("a move of %d lines was refused", anchorTolerance)
	}
	if _, ok := o.find("a.go", 100+anchorTolerance+1); ok {
		t.Errorf("a move of %d lines was allowed", anchorTolerance+1)
	}
	if _, ok := o.find("b.go", 100); ok {
		t.Error("a finding matched an origin in another file")
	}
}

// The nearest origin wins, so a merged finding takes the words of the one it
// sits closest to rather than whichever was listed first.
func TestTheNearestOriginSuppliesTheWords(t *testing.T) {
	o := originsOf([]Finding{
		origin("a.go", 10, "first", "r1"),
		origin("a.go", 30, "second", "r2"),
	})

	got, ok := o.find("a.go", 28)
	if !ok || got.Title != "second" {
		t.Errorf("matched %+v, want the finding at line 30", got)
	}
}

// A suggestion is a claim about what the code should be, so it is restored
// with the other two.
func TestASuggestionIsRestoredToo(t *testing.T) {
	o := origin("a.go", 5, "t", "r")
	o.Suggestion = "if err != nil {\n\treturn err\n}"

	f := Finding{Path: "a.go", Line: 5, Title: "t", Rationale: "r",
		Suggestion: "_ = err // ignore"}

	changed := restore(&f, o)
	if f.Suggestion != o.Suggestion {
		t.Errorf("suggestion = %q, want the reviewer's", f.Suggestion)
	}
	if !strings.Contains(strings.Join(changed, ","), "suggestion") {
		t.Errorf("changed = %v, want suggestion named", changed)
	}
}

// An unchanged finding reports nothing changed, so the log stays quiet on the
// ordinary case.
func TestAnUntouchedFindingReportsNoChange(t *testing.T) {
	o := origin("a.go", 5, "t", "r")
	f := o
	if changed := restore(&f, o); len(changed) != 0 {
		t.Errorf("changed = %v on an identical finding", changed)
	}
}

// The contract end to end: a review reports a finding, triage returns the same
// finding with its own words, and what reaches the pull request is the
// reviewer's.
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
			"triaging findings":            mustJSON(t, Result{Summary: "s", Findings: []Finding{rewritten}}),
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
		"triaging findings":            mustJSON(t, Result{Summary: "s", Findings: []Finding{elsewhere}}),
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
