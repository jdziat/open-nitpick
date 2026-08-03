package review

import (
	"context"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/diff"
	"github.com/jdziat/open-nitpick/internal/llm"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// classDiff has one added line so any finding on line 4 is placeable.
const classDiff = engineDiff

// runWithClasses reviews with a scripted model returning the given findings and
// a triage pass that echoes them with a possibly-different class.
func runWithClasses(t *testing.T, level config.NitpickLevel, review, triage []Finding) *Report {
	t.Helper()

	model := &scriptedLLM{
		byPrompt: map[string]string{
			"triaging findings":            mustJSON(t, Result{Summary: "s", Findings: triage}),
			"Review the following changes": mustJSON(t, Result{Findings: review}),
		},
	}

	cfg := config.Defaults()
	cfg.Models.Default = config.ModelSpec{Provider: "openai", Model: "gpt-4o"}
	cfg.Persona.Nitpick = level

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
	return report
}

// TestTriageCannotReclassifyAFinding is the regression test for a real defect
// disappearing because a summarizing model relabelled it.
//
// Triage may drop, merge, and reword. It must not be able to re-author policy:
// a finding the reviewer classed `security` coming back as `style` was silently
// filtered out at the DEFAULT level, with the build left green.
func TestTriageCannotReclassifyAFinding(t *testing.T) {
	f := Finding{
		Path: "app.go", Line: 4, Severity: "critical",
		Class: string(config.ClassSecurity), Category: "security",
		Title: "Command injection", Rationale: "user input reaches a shell",
	}

	relabelled := f
	relabelled.Class = string(config.ClassStyle)

	report := runWithClasses(t, config.NitpickNormal, []Finding{f}, []Finding{relabelled})

	if len(report.Findings) != 1 {
		t.Fatalf("findings = %d; a security finding was dropped after triage relabelled it style",
			len(report.Findings))
	}
	if got := report.Findings[0].Class; got != string(config.ClassSecurity) {
		t.Errorf("class = %q, want the review pass's %q to survive triage", got, config.ClassSecurity)
	}
	if !report.Failed(config.SeverityError) {
		t.Error("a surviving critical finding must still trip the gate")
	}
}

// TestNoLevelSilencesADefect wires the invariant the config test states purely.
func TestNoLevelSilencesADefect(t *testing.T) {
	for _, level := range []config.NitpickLevel{
		config.NitpickOff, config.NitpickMinimal, config.NitpickNormal, config.NitpickPedantic,
	} {
		for _, class := range []config.Class{
			config.ClassCorrectness, config.ClassConcurrency, config.ClassSecurity,
			config.ClassResource, config.ClassDataLoss,
		} {
			f := Finding{
				Path: "app.go", Line: 4, Severity: "error",
				Class: string(class), Title: "real defect", Rationale: "it breaks",
			}

			report := runWithClasses(t, level, []Finding{f}, []Finding{f})
			if len(report.Findings) != 1 {
				t.Errorf("nitpick=%s dropped a %s finding; no level may silence a defect", level, class)
			}
		}
	}
}

// TestClasslessFindingSurvivesTheStrictestLevel covers findings that never had
// a class — every linter result before classForRule existed.
func TestClasslessFindingSurvivesTheStrictestLevel(t *testing.T) {
	f := Finding{
		Path: "app.go", Line: 4, Severity: "critical",
		Title: "hardcoded credential", Rationale: "committed secret",
		// Class deliberately empty.
	}

	report := runWithClasses(t, config.NitpickOff, []Finding{f}, []Finding{f})
	if len(report.Findings) != 1 {
		t.Fatal("a classless finding was dropped; an unset field must not delete a defect")
	}
}

// TestStyleIsFilteredBelowPedantic checks the knob actually does its job.
func TestStyleIsFilteredBelowPedantic(t *testing.T) {
	f := Finding{
		Path: "app.go", Line: 4, Severity: "nit",
		Class: string(config.ClassStyle), Title: "rename this", Rationale: "clarity",
	}

	for _, level := range []config.NitpickLevel{
		config.NitpickOff, config.NitpickMinimal, config.NitpickNormal,
	} {
		report := runWithClasses(t, level, []Finding{f}, []Finding{f})
		if len(report.Findings) != 0 {
			t.Errorf("nitpick=%s published a style finding", level)
		}
	}
}

// TestTestsClassRespectsLevel exercises a class that is genuinely level-gated.
func TestTestsClassRespectsLevel(t *testing.T) {
	f := Finding{
		Path: "app.go", Line: 4, Severity: "info",
		Class: string(config.ClassTests), Title: "no coverage", Rationale: "new branch untested",
	}

	if got := runWithClasses(t, config.NitpickOff, []Finding{f}, []Finding{f}); len(got.Findings) != 0 {
		t.Error("nitpick=off should not publish a tests finding")
	}
	if got := runWithClasses(t, config.NitpickNormal, []Finding{f}, []Finding{f}); len(got.Findings) != 1 {
		t.Error("nitpick=normal should publish a tests finding")
	}
}

// TestGenerationPromptIsLevelIndependent checks the engine, not just the
// prompt package: a level-dependent branch here would defeat the design.
func TestGenerationPromptIsLevelIndependent(t *testing.T) {
	var prompts []string

	for _, level := range []config.NitpickLevel{
		config.NitpickOff, config.NitpickMinimal, config.NitpickNormal, config.NitpickPedantic,
	} {
		cfg := config.Defaults()
		cfg.Models.Default = config.ModelSpec{Provider: "openai", Model: "gpt-4o"}
		cfg.Persona.Nitpick = level

		e := &Engine{Config: cfg}
		p, err := e.reviewPrompt()
		if err != nil {
			t.Fatalf("reviewPrompt: %v", err)
		}
		prompts = append(prompts, p)
	}

	for i := 1; i < len(prompts); i++ {
		if prompts[i] != prompts[0] {
			t.Error("the engine's review prompt varies by nitpick level; it must not")
		}
	}
	if strings.Contains(prompts[0], "You may ALSO report naming") {
		t.Error("the generation prompt invites style commentary")
	}
}

// TestLinterAttributionSurvivesTriage is the regression test for the product's
// central claim.
//
// "flagged by golangci-lint(gosec), triaged by <model>" is the sentence this
// tool exists to be able to write: a deterministic analyzer found it, a model
// judged whether it mattered here, and the reader sees both. Triage used to
// overwrite Source with the triage model's name, so every linter finding
// claimed to have come from the model — destroying the attribution chain — and
// the renderer never printed it anyway.
func TestLinterAttributionSurvivesTriage(t *testing.T) {
	lint := Finding{
		Path: "app.go", Line: 4, Severity: "error",
		Class: string(config.ClassSecurity), Category: "lint",
		Title: "Potential file inclusion via variable", Rationale: "Reported by golangci-lint(gosec).",
		Source: "golangci-lint(gosec)",
	}

	// Linter findings enter through the LinterRunner, not the model, which is
	// exactly why their provenance is worth preserving.
	report := runWithLinter(t, config.NitpickNormal, nil, []Finding{lint}, []Finding{lint})

	if len(report.Findings) != 1 {
		t.Fatalf("findings = %d, want the linter finding published", len(report.Findings))
	}

	got := report.Findings[0]
	if got.Source != "golangci-lint(gosec)" {
		t.Errorf("Source = %q, want the original reporter to survive triage", got.Source)
	}
	if got.Triager == "" {
		t.Error("Triager should record who triaged it")
	}

	// And the reader must actually see it.
	body := renderComment(got, true)
	if !strings.Contains(body, "flagged by golangci-lint(gosec)") {
		t.Errorf("published comment must attribute the analyzer:\n%s", body)
	}
	if !strings.Contains(body, "triaged by") {
		t.Errorf("published comment should name the triager:\n%s", body)
	}
}

// TestModelFindingAttributionIsNotDoubled: when the reviewer and triager are
// both models, do not print a confusing double attribution.
func TestModelFindingAttributionIsNotDoubled(t *testing.T) {
	f := Finding{
		Path: "app.go", Line: 4, Severity: "warning",
		Class: string(config.ClassCorrectness), Title: "x", Rationale: "y",
	}

	report := runWithClasses(t, config.NitpickNormal, []Finding{f}, []Finding{f})
	if len(report.Findings) != 1 {
		t.Fatalf("findings = %d", len(report.Findings))
	}

	body := renderComment(report.Findings[0], true)
	if strings.Count(body, "flagged by") > 1 {
		t.Errorf("attribution should appear once:\n%s", body)
	}
}

// stubLinter feeds fixed findings through the real linter path.
type stubLinter struct{ findings []Finding }

func (s *stubLinter) Run(context.Context, diff.Files) ([]Finding, error) { return s.findings, nil }

// runWithLinter reviews with both a scripted model and a stub linter.
func runWithLinter(t *testing.T, level config.NitpickLevel, review, lint, triage []Finding) *Report {
	t.Helper()

	model := &scriptedLLM{
		byPrompt: map[string]string{
			"triaging findings":            mustJSON(t, Result{Summary: "s", Findings: triage}),
			"Review the following changes": mustJSON(t, Result{Findings: review}),
		},
	}

	cfg := config.Defaults()
	cfg.Models.Default = config.ModelSpec{Provider: "openai", Model: "gpt-4o"}
	cfg.Persona.Nitpick = level

	client := llm.NewClientForTest(model, cfg.Models.Default)

	engine := &Engine{
		Config:   cfg,
		Roles:    &llm.Roles{Review: client, Triage: client},
		Provider: &stubProvider{diff: classDiff},
		Linters:  &stubLinter{findings: lint},
	}

	report, err := engine.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	return report
}
