package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/v2/internal/bundle"
	"github.com/jdziat/open-nitpick/v2/internal/config"
	"github.com/jdziat/open-nitpick/v2/internal/review"
	"github.com/jdziat/open-nitpick/v2/internal/vcs"
)

func TestActionsOutputsAndSummaryAreWrittenByTheCLI(t *testing.T) {
	dir := t.TempDir()
	a := actionsEnv{outputs: filepath.Join(dir, "out"), summary: filepath.Join(dir, "summary.md")}

	report := &review.Report{
		Findings: []review.Finding{
			{Path: "a.go", Line: 4, Severity: "error", Title: "Ignored error | from Get"},
			{Path: "b.go", Line: 9, Severity: "nit", Title: "Spelling"},
		},
		Counts:          review.Counts{config.SeverityError: 1, config.SeverityNit: 1},
		Plan:            &bundle.Plan{Batches: []bundle.Batch{{Entries: make([]bundle.Entry, 3)}}},
		AlreadyReported: []review.Finding{{Path: "c.go"}},
		Linters:         []review.LinterStatus{{Linter: "golangci-lint", Outcome: review.LinterRan, State: "isolated"}},
	}
	rendered := vcs.Review{Summary: "Adds a retry path."}
	a.setOutputs(resultFindings, report)
	a.writeSummary(resultFindings, report, &rendered, vcs.Ref{Owner: "o", Repo: "r", Number: 7}, "")

	out, _ := os.ReadFile(a.outputs)
	for _, want := range []string{"result=findings\n", "findings=2\n", "error=1\n", "nit=1\n", "critical=0\n", "files=3\n", "withheld=1\n"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("outputs lack %q:\n%s", want, out)
		}
	}

	summary, _ := os.ReadFile(a.summary)
	s := string(summary)
	for _, want := range []string{"## open-nitpick", "Result: **findings**", "3 file(s) reviewed", "Adds a retry path.",
		"| error | [`a.go:4`](https://github.com/o/r/pull/7/files) | Ignored error \\| from Get |", "golangci-lint — ran", "already posted"} {
		if !strings.Contains(s, want) {
			t.Errorf("summary lacks %q:\n%s", want, s)
		}
	}
	// The error row sorts above the nit row whatever order the report holds.
	if strings.Index(s, "| error |") > strings.Index(s, "| nit |") {
		t.Error("findings are not ordered most severe first")
	}
}

func TestActionsAreSilentOutsideActions(t *testing.T) {
	a := actionsEnv{}
	if a.active() {
		t.Fatal("no files, not active")
	}
	// Neither call may write anywhere or panic with nothing configured.
	a.setOutputs(resultError, nil)
	a.writeSummary(resultError, nil, nil, vcs.Ref{}, "note")
}

func TestResultForFollowsTheGate(t *testing.T) {
	report := &review.Report{Findings: []review.Finding{{Severity: "warning"}}}
	if resultFor(report, config.SeverityError) != resultClean {
		t.Error("a warning under an error gate is clean")
	}
	if resultFor(report, config.SeverityWarning) != resultFindings {
		t.Error("a warning at a warning gate trips it")
	}
	if resultFor(nil, config.SeverityNone) != resultError {
		t.Error("no report is an error")
	}
}
