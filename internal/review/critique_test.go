package review

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/diff"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

const critiqueFence = "grading draft pull request comments"

func TestTheCritiqueKeepRuleIsProvenAlwaysPlausibleAtWarningAndUnsupportedNever(t *testing.T) {
	cases := []struct {
		evidence string
		sev      config.Severity
		want     bool
	}{
		{evidenceProven, config.SeverityNit, true},
		{evidenceProven, config.SeverityCritical, true},
		{evidencePlausible, config.SeverityError, true},
		{evidencePlausible, config.SeverityWarning, true},
		{evidencePlausible, config.SeverityInfo, false},
		{evidencePlausible, config.SeverityNit, false},
		{evidenceUnsupported, config.SeverityCritical, false},
		// A grader answering outside its vocabulary has not graded.
		{"", config.SeverityInfo, true},
		{"maybe", config.SeverityInfo, true},
	}
	for _, c := range cases {
		if got := keepAfterCritique(c.evidence, c.sev); got != c.want {
			t.Errorf("keepAfterCritique(%q, %s) = %v, want %v", c.evidence, c.sev, got, c.want)
		}
	}
}

func gradesJSON(t *testing.T, r critiqueResult) string {
	t.Helper()
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func twoFindings() []Finding {
	return []Finding{
		{Path: "app.go", Line: 4, Severity: "error", Category: "correctness", Class: "correctness",
			Title: "Ignored error from http.Get", Rationale: "resp may be nil, so the deferred Close panics."},
		{Path: "app.go", Line: 5, Severity: "info", Category: "correctness", Class: "correctness",
			Title: "Close error is dropped", Rationale: "Body.Close returns an error nobody reads."},
	}
}

func critiqueModel(t *testing.T, grades string) *scriptedLLM {
	t.Helper()
	fs := twoFindings()
	return &scriptedLLM{byPrompt: map[string]string{
		"Review the following changes": mustJSON(t, Result{Findings: fs}),
		"triaging findings":            mustJSON(t, TriageResult{Summary: "s", Verdicts: verdictsFor(fs)}),
		critiqueFence:                  grades,
	}}
}

func critiqueCalls(m *scriptedLLM) int {
	n := 0
	for _, p := range m.seen {
		if strings.Contains(p, critiqueFence) {
			n++
		}
	}
	return n
}

// Off is the default, and off means the grader is never called: a pass that
// costs a model call per pull request must not run on a repository that did
// not ask for it.
func TestTheCritiqueNeverRunsUnlessAskedFor(t *testing.T) {
	model := critiqueModel(t, `{"grades":[]}`)
	report, err := newEngine(t, model, &stubProvider{diff: engineDiff}, nil).Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if critiqueCalls(model) != 0 {
		t.Errorf("the grader was called %d times with review.critique off", critiqueCalls(model))
	}
	if len(report.Findings) != 2 || len(report.Overruled) != 0 {
		t.Errorf("findings %d, withheld %d, want both published untouched", len(report.Findings), len(report.Overruled))
	}
}

func TestACritiqueWithholdsWhatTheDiffDoesNotSupportAndSaysWhy(t *testing.T) {
	model := critiqueModel(t, gradesJSON(t, critiqueResult{Grades: []critiqueGrade{
		{Number: 1, Evidence: evidenceProven, Reason: "line 4 discards the error"},
		{Number: 2, Evidence: evidencePlausible, Reason: "depends on the Body type"},
	}}))
	report, err := newEngine(t, model, &stubProvider{diff: engineDiff}, func(c *config.Config) {
		c.Review.Critique = true
	}).Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if critiqueCalls(model) != 1 {
		t.Errorf("grader calls = %d, want one batched call", critiqueCalls(model))
	}
	if len(report.Findings) != 1 || report.Findings[0].Title != "Ignored error from http.Get" {
		t.Fatalf("findings = %+v, want only the proven error", report.Findings)
	}
	if len(report.Overruled) != 1 {
		t.Fatalf("withheld = %d, want the plausible info", len(report.Overruled))
	}
	got := report.Overruled[0]
	if got.Expert != critiqueExpert || !strings.Contains(got.Reason, "below warning") || got.Finding.Title != "Close error is dropped" {
		t.Errorf("withheld record = %+v, want the info finding, attributed, with its reason", got)
	}
	if len(report.Stages) != 0 {
		t.Errorf("stages = %v, want none for a clean critique", report.Stages)
	}
}

// An outage costs precision, which the stage status shows. Dropping the
// findings would cost recall nobody is told about.
func TestAFailedCritiqueKeepsEveryFindingAndSaysItDidNotRun(t *testing.T) {
	model := critiqueModel(t, `not json at all`)
	report, err := newEngine(t, model, &stubProvider{diff: engineDiff}, func(c *config.Config) {
		c.Review.Critique = true
	}).Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Findings) != 2 || len(report.Overruled) != 0 {
		t.Errorf("findings %d, withheld %d, want both kept", len(report.Findings), len(report.Overruled))
	}
	found := false
	for _, s := range report.Stages {
		found = found || s.Stage == "critique"
	}
	if !found {
		t.Errorf("stages = %v, want a critique entry so the run does not read as clean", report.Stages)
	}
}

// A finding the grader skipped was not graded, and is kept. Withholding it for
// silence would let a short answer delete findings.
func TestAFindingTheGraderSkippedIsKept(t *testing.T) {
	model := critiqueModel(t, gradesJSON(t, critiqueResult{Grades: []critiqueGrade{
		{Number: 1, Evidence: evidenceUnsupported, Reason: "the error is checked on line 6"},
	}}))
	report, err := newEngine(t, model, &stubProvider{diff: engineDiff}, func(c *config.Config) {
		c.Review.Critique = true
	}).Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Findings) != 1 || report.Findings[0].Title != "Close error is dropped" {
		t.Errorf("findings = %+v, want only the finding the grader did not number", report.Findings)
	}
	if len(report.Overruled) != 1 || report.Overruled[0].Finding.Title != "Ignored error from http.Get" {
		t.Errorf("withheld = %+v, want the unsupported error", report.Overruled)
	}
}

func TestADiffWindowShowsTheCitedLineWithItsMarginNumberAndRemovedLines(t *testing.T) {
	files, err := parseDiff(engineDiff)
	if err != nil {
		t.Fatal(err)
	}
	var file *diff.File
	for _, f := range files {
		if f.Path == "app.go" {
			file = f
		}
	}
	got := diffWindow(file, 4, 3)
	for _, want := range []string{"    4 +\tresp, _ := http.Get", "-func Get() error {", "    5 +\tdefer resp.Body.Close()"} {
		if !strings.Contains(got, want) {
			t.Errorf("window lacks %q:\n%s", want, got)
		}
	}
	if diffWindow(nil, 4, 2) != "" || diffWindow(&diff.File{}, 4, 2) != "" {
		t.Error("a missing or empty file produced a window")
	}
}
