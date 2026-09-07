package review

import (
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/bundle"
	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/diff"
)

func planOfFiles(n int) *bundle.Plan {
	var entries []bundle.Entry
	for i := 0; i < n; i++ {
		entries = append(entries, bundle.Entry{File: &diff.File{Path: "f.go"}})
	}
	return &bundle.Plan{Batches: []bundle.Batch{{Entries: entries}}}
}

// The receipt states what ran, and every number in it is counted rather than
// written, so there is nothing in it a model could have invented.
func TestTheReceiptCountsWhatTheRunDid(t *testing.T) {
	report := &Report{
		Files:    diff.Files{{Path: "a.go"}, {Path: "b.go"}, {Path: "c.go"}},
		Plan:     planOfFiles(2),
		Findings: []Finding{{Severity: "error"}, {Severity: "nit"}},
		Linters: []LinterStatus{
			{Linter: "ruff", Outcome: LinterRan},
			{Linter: "golangci-lint", Outcome: LinterRan},
			{Linter: "semgrep", Outcome: LinterSkipped},
		},
		Incomplete: []string{"c.go"},
	}
	report.Counts = counts(report.Findings)

	got := receipt(report)
	for _, want := range []string{"Read 2 of 3 changed files", "2 findings", "1 file could not be reviewed"} {
		if !strings.Contains(got, want) {
			t.Errorf("receipt is missing %q:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "Analyzers: golangci-lint, ruff.") {
		t.Errorf("analyzers wrong or unsorted:\n%s", got)
	}
	if strings.Contains(got, "semgrep") {
		t.Errorf("an analyzer that did not run was listed:\n%s", got)
	}
}

// A clean review says nothing was found in what was read, not that the change
// is clean. The difference is the whole point of the coverage notices.
func TestACleanReceiptDoesNotClaimTheChangeIsClean(t *testing.T) {
	report := &Report{Files: diff.Files{{Path: "a.go"}}, Plan: planOfFiles(1)}
	got := receipt(report)

	if !strings.Contains(got, "Nothing found in what was read") {
		t.Errorf("receipt = %q", got)
	}
	for _, forbidden := range []string{"clean", "no issues", "looks good"} {
		if strings.Contains(strings.ToLower(got), forbidden) {
			t.Errorf("receipt claims more than it knows (%q):\n%s", forbidden, got)
		}
	}
}

// Two runs over the same report print the same receipt, since nothing in it
// comes from a model.
func TestTheReceiptIsDeterministic(t *testing.T) {
	report := &Report{
		Files: diff.Files{{Path: "a.go"}}, Plan: planOfFiles(1),
		Linters: []LinterStatus{{Linter: "ruff", Outcome: LinterRan}, {Linter: "actionlint", Outcome: LinterRan}},
	}
	first, second := receipt(report), receipt(report)
	if first != second {
		t.Errorf("two renderings of one report differ:\n%s\n%s", first, second)
	}
	if !strings.Contains(first, "actionlint, ruff") {
		t.Errorf("analyzers are not in name order: %q", first)
	}
}

// The style switch decides which one is published, and the receipt is the
// default.
func TestTheStyleSwitchChoosesTheWalkthrough(t *testing.T) {
	report := &Report{
		Files: diff.Files{{Path: "a.go"}}, Plan: planOfFiles(1),
		Summary: "This change refactors the retry helper.",
	}

	cfg := config.Defaults()
	if got := walkthrough(report, cfg); !strings.Contains(got, "Read 1 changed file") {
		t.Errorf("the default is not the receipt:\n%s", got)
	}
	if got := walkthrough(report, cfg); strings.Contains(got, "refactors the retry helper") {
		t.Errorf("the generated prose was published under the receipt style:\n%s", got)
	}

	cfg.Review.SummaryStyle = config.SummaryProse
	if got := walkthrough(report, cfg); !strings.Contains(got, "refactors the retry helper") {
		t.Errorf("prose style did not publish the generated summary:\n%s", got)
	}
}

// A change with nothing in it produces no receipt rather than "Read 0 files".
func TestAnEmptyChangeProducesNoReceipt(t *testing.T) {
	if got := receipt(&Report{}); got != "" {
		t.Errorf("receipt = %q, want empty", got)
	}
	if got := receipt(nil); got != "" {
		t.Errorf("receipt(nil) = %q", got)
	}
}
