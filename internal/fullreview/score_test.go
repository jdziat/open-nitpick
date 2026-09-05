package fullreview

import (
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/review"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

func TestScoreIsPerLanguageWithDenominators(t *testing.T) {
	tree := &vcs.Tree{
		Covered: []string{"a.go", "b.go", "app/x.py", "go.mod"},
		Lines:   map[string]int{"a.go": 400, "b.go": 600, "app/x.py": 500, "go.mod": 3},
	}
	report := &review.Report{Findings: []review.Finding{
		{Path: "a.go", Severity: "nit", Class: "slop"},
		{Path: "a.go", Severity: "warning", Class: "slop"},
		{Path: "b.go", Severity: "error", Class: "correctness"},
		{Path: "app/x.py", Severity: "critical", Class: "security"},
		{Path: "go.mod", Severity: "warning", Class: "security", FromAnalyzer: true, Source: "GHSA-1"},
	}}
	card := Score(report, tree)
	if card.Total.Lines != 1503 || card.Total.Files != 4 {
		t.Fatalf("total = %+v", card.Total)
	}
	var goRow LanguageScore
	for _, r := range card.Languages {
		if r.Language == "go" {
			goRow = r
		}
	}
	if goRow.Slop != 2 || goRow.SlopWeighted != 2.5 || goRow.PerKLOC(goRow.SlopWeighted) != 2.5 {
		t.Errorf("go slop = %+v", goRow)
	}
	if card.Total.Security != 1 {
		t.Errorf("the advisory was scored as a security finding: %+v", card.Total)
	}
	out := card.String()
	for _, want := range []string{"go ", "python", "config/docs", "(2, 2.5 wt)", "threshold of 2.0"} {
		if !strings.Contains(out, want) {
			t.Errorf("scorecard lacks %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "is below the threshold") {
		t.Errorf("1.66 weighted slop per thousand lines over 1503 lines should be below 2.0:\n%s", out)
	}
}
