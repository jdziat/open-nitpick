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

// TestSeverityProvenanceSurvivesTriage is the SEVERITY half of the property
// above, and it was missing while Source had a test.
//
// THE BUG: SeverityTranslated and RawSeverity carry `json:"-"`, so triage's
// decode zeroed both, and recordSeverity could not put them back — renderForTriage
// shows the triage model `[%s]` of an ALREADY NORMALIZED Severity, so a model
// that echoes what it was shown normalizes to itself and the call returns early.
// Every finding that survived triage was published claiming nobody had
// translated it. internal/evals' severityAsSaid reads that as "Severity is the
// reporter's own word" and quotes it unmarked, so the eval report said semgrep
// printed "error" when semgrep printed "HIGH", and said a review model printed
// "info" when it printed "P1". That is the defect recordSeverity's doc comment
// says it fixes, alive one pass downstream of the fix and on the path the eval
// battery runs.
//
// The second case is a triage RE-RATING, and who reported the finding decides
// the answer. THE BUG: it did not. Both kinds were treated as a model's — the
// word dropped, on the theory that a level somebody else chose makes the
// reporter's spelling stale — which for an analyzer is wrong twice over. semgrep
// does not retract "HIGH" because triage re-rated the impact, and the finding is
// still published as "flagged by semgrep(...)", so dropping the pair leaves the
// report asserting semgrep's own word for it is our "warning". An analyzer's
// level is never its own claim, whatever the level ends up being. The model case
// is TestAModelsOwnWordIsStaleAfterATriageRerate below.
func TestSeverityProvenanceSurvivesTriage(t *testing.T) {
	lint := Finding{
		Path: "app.go", Line: 4, Severity: "error",
		Class: string(config.ClassSecurity), Category: "lint",
		Title:              "Potential file inclusion via variable",
		Rationale:          "Reported by semgrep(go.lang.security.audit.file-inclusion).",
		Source:             "semgrep(go.lang.security.audit.file-inclusion)",
		SeverityTranslated: true, RawSeverity: "HIGH", FromAnalyzer: true,
	}

	// `echoed` is what triage's decode actually yields: the unserialized fields
	// cleared. Passing `lint` itself would let the struct smuggle the provenance
	// across a boundary that cannot carry it, and the test would pass against
	// the broken code.
	echoed := lint
	echoed.SeverityTranslated = false
	echoed.RawSeverity = ""
	echoed.FromAnalyzer = false

	report := runWithLinter(t, config.NitpickNormal, nil, []Finding{lint}, []Finding{echoed})
	if len(report.Findings) != 1 {
		t.Fatalf("findings = %d, want the linter finding published", len(report.Findings))
	}

	got := report.Findings[0]
	if !got.SeverityTranslated || got.RawSeverity != "HIGH" {
		t.Errorf("published as translated=%v raw=%q; semgrep printed \"HIGH\" and this finding is "+
			"attributed to semgrep (%s), so our word is being quoted as the analyzer's",
			got.SeverityTranslated, got.RawSeverity, got.Source)
	}

	// Triage re-rating it. The level moved; what semgrep printed did not.
	moved := echoed
	moved.Severity = string(config.SeverityWarning)

	report = runWithLinter(t, config.NitpickNormal, nil, []Finding{lint}, []Finding{moved})
	if len(report.Findings) != 1 {
		t.Fatalf("findings = %d after a triage re-rate", len(report.Findings))
	}

	got = report.Findings[0]
	if got.Severity != string(config.SeverityWarning) {
		t.Fatalf("Severity = %q, want triage's warning; the rest of this case is about that move",
			got.Severity)
	}
	if !got.SeverityTranslated || got.RawSeverity != "HIGH" {
		t.Errorf("translated=%v raw=%q after a triage re-rate to %q, on a finding still published "+
			"as %s. A re-rating is not a retraction, and dropping the analyzer's word leaves the "+
			"report quoting ours in its place", got.SeverityTranslated, got.RawSeverity,
			got.Severity, got.Source)
	}
}

// TestAModelsOwnWordIsStaleAfterATriageRerate is why the rule above is a rule
// rather than "always restore".
//
// A model's RawSeverity is the level IT assigned. Once triage rates the finding
// differently, that word describes a rating nobody now holds, and a report
// quoting it beside the published level describes a finding that never existed —
// the same judgement applyOutcomes makes about an expert's re-rate.
func TestAModelsOwnWordIsStaleAfterATriageRerate(t *testing.T) {
	found := Finding{
		Path: "app.go", Line: 4, Severity: "info",
		Class: string(config.ClassCorrectness), Category: "correctness",
		Title: "Potential file inclusion via variable", Rationale: "y",
		SeverityTranslated: true, RawSeverity: "P1",
	}

	echoed := found
	echoed.SeverityTranslated = false
	echoed.RawSeverity = ""
	echoed.Severity = string(config.SeverityWarning)

	report := runWithClasses(t, config.NitpickNormal, []Finding{found}, []Finding{echoed})
	if len(report.Findings) != 1 {
		t.Fatalf("findings = %d, want the model's finding published", len(report.Findings))
	}

	got := report.Findings[0]
	if got.Severity != string(config.SeverityWarning) {
		t.Fatalf("Severity = %q, want triage's warning", got.Severity)
	}
	if got.SeverityTranslated || got.RawSeverity != "" {
		t.Errorf("translated=%v raw=%q survived a triage re-rate to %q. The model rated this "+
			"\"P1\" and nothing is now published at the level it meant",
			got.SeverityTranslated, got.RawSeverity, got.Severity)
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
		Linters: func(*config.Config) LinterRunner {
			return &stubLinter{findings: lint}
		},
	}

	report, err := engine.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	return report
}

// A known advisory is not the triage model's to merge, reword or drop: it is
// published as the scanner reported it, under its rule and class. The scripted
// triage here answers with the bug alone, which is what a triage model that
// merged or dropped both advisories would answer; both are published anyway.
func TestKnownAdvisoriesAreNotTriaged(t *testing.T) {
	cve := Finding{
		Path: "app.go", Line: 4, Severity: "warning", Class: "security", FromAnalyzer: true,
		Title:     "Package 'golang.org/x/text@0.3.0' is vulnerable to 'CVE-2020-14040' (also known as 'GO-2020-0015').",
		Rationale: "Reported by CVE-2020-14040.", Source: "CVE-2020-14040",
	}
	second := cve
	second.Title, second.Source = "Package 'golang.org/x/text@0.3.0' is vulnerable to 'CVE-2022-32149'.", "CVE-2022-32149"
	bug := Finding{Path: "app.go", Line: 4, Severity: "error", Class: "correctness", Title: "Deferred close panics"}

	report := runWithLinter(t, config.NitpickNormal, []Finding{bug}, []Finding{cve, second}, []Finding{bug})

	var got []Finding
	for _, f := range report.Findings {
		if f.IsAdvisory() {
			got = append(got, f)
		}
	}
	if len(got) != 2 {
		t.Fatalf("advisories published = %+v, want both as the scanner reported them:\n%+v", got, report.Findings)
	}
	for i, f := range got {
		if f.Title != []Finding{cve, second}[i].Title || f.Class != "security" || f.Triager != "" || !f.FromAnalyzer {
			t.Errorf("advisory was judged by a model: %+v", f)
		}
	}
	if len(report.Findings) != 3 {
		t.Errorf("findings = %d, want the bug and both advisories", len(report.Findings))
	}
}

// The linters package qualifies every rule with its tool, so the id a report
// carries is "osv-scanner(CVE-...)"; an advisory is recognized in that form
// and in the bare one, and an unrelated rule that merely mentions one is not.
func TestAdvisoryRulesAreRecognizedQualifiedOrBare(t *testing.T) {
	for _, rule := range []string{"CVE-2020-14040", "osv-scanner(CVE-2020-14040)", "osv-scanner(GHSA-5rcv-m4m3-hfh7)", "GO-2020-0015", "osv-scanner(PYSEC-2021-1)", "RUSTSEC-2020-0001"} {
		if !IsAdvisoryRule(rule) {
			t.Errorf("%q is not recognized as an advisory", rule)
		}
	}
	for _, rule := range []string{"golangci-lint(gosec)", "semgrep(go.lang.security.audit.cve-check)", "errcheck", ""} {
		if IsAdvisoryRule(rule) {
			t.Errorf("%q is recognized as an advisory", rule)
		}
	}
	f := Finding{Source: "osv-scanner(CVE-2020-14040)"}
	if f.IsAdvisory() {
		t.Error("a finding not from an analyzer is not an advisory whatever its source says")
	}
	f.FromAnalyzer = true
	if !f.IsAdvisory() {
		t.Error("an analyzer finding with an advisory rule is an advisory")
	}
}
