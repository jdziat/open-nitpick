package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/review"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// GitHub Actions integration.
//
// Inside a job the run has two more places to report than a terminal: the
// step's outputs, which a later step reads, and the job summary, which a
// person reads without opening the log. Both are files named by the
// environment, and both are written by the CLI itself rather than scraped
// from its output by the Action's shell — which is how the `findings` output
// spent a release reading zero, because the line it grepped for was on
// stderr and the grep read stdout.
//
// Nothing here changes behaviour outside Actions: with neither variable set,
// every function is a no-op.

// actionResult is the value of the `result` output.
type actionResult string

const (
	resultClean    actionResult = "clean"    // no findings at or above the gate
	resultFindings actionResult = "findings" // the gate was tripped
	resultSkipped  actionResult = "skipped"  // nothing was reviewed on purpose (a draft)
	resultError    actionResult = "error"    // the review could not run
)

// actionsEnv reads the two files, when Actions provides them.
type actionsEnv struct {
	outputs string // $GITHUB_OUTPUT
	summary string // $GITHUB_STEP_SUMMARY
}

func actionsFromEnv() actionsEnv {
	return actionsEnv{outputs: os.Getenv("GITHUB_OUTPUT"), summary: os.Getenv("GITHUB_STEP_SUMMARY")}
}

func (a actionsEnv) active() bool { return a.outputs != "" || a.summary != "" }

// appendFile appends to one of the files, tolerating its absence: a missing
// summary file is a log line, not a failed review.
func appendFile(path, content string) {
	if path == "" || content == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "could not write %s: %v\n", path, err)
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = f.WriteString(content)
}

// setOutputs records the run's outputs. Every count is written, including
// zeros, so a workflow expression never reads an unset output as empty.
func (a actionsEnv) setOutputs(result actionResult, report *review.Report) {
	if a.outputs == "" {
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "result=%s\n", result)

	var counts review.Counts
	files, withheld := 0, 0
	if report != nil {
		counts = report.Counts
		if report.Plan != nil {
			files = report.Plan.Files()
		}
		withheld = len(report.AlreadyReported)
	}
	total := 0
	for _, s := range severityOrder {
		n := counts[s]
		total += n
		fmt.Fprintf(&b, "%s=%d\n", s, n)
	}
	fmt.Fprintf(&b, "findings=%d\n", total)
	fmt.Fprintf(&b, "files=%d\n", files)
	fmt.Fprintf(&b, "withheld=%d\n", withheld)
	appendFile(a.outputs, b.String())
}

var severityOrder = []config.Severity{
	config.SeverityCritical, config.SeverityError, config.SeverityWarning, config.SeverityInfo, config.SeverityNit,
}

// writeSummary appends the review to the job summary: the walkthrough, a
// table of findings, and the analyzer roster. The inline comments are on the
// pull request; this is the one place a reader who never opens it sees what
// happened, and the only place a review of a fork's pull request lands at
// all when the token cannot post.
func (a actionsEnv) writeSummary(result actionResult, report *review.Report, rendered *vcs.Review, ref vcs.Ref, note string) {
	if a.summary == "" {
		return
	}
	var b strings.Builder
	b.WriteString("## open-nitpick\n\n")
	if note != "" {
		fmt.Fprintf(&b, "> %s\n\n", note)
	}
	if report == nil {
		fmt.Fprintf(&b, "Result: **%s**\n", result)
		appendFile(a.summary, b.String())
		return
	}

	files := 0
	if report.Plan != nil {
		files = report.Plan.Files()
	}
	fmt.Fprintf(&b, "Result: **%s** — %d file(s) reviewed, %s.\n\n", result, files, countsOr(report.Counts, "no findings"))

	if rendered != nil && strings.TrimSpace(rendered.Summary) != "" {
		b.WriteString(rendered.Summary)
		b.WriteString("\n\n")
	}

	if len(report.Findings) > 0 {
		b.WriteString("| severity | where | finding |\n|---|---|---|\n")
		findings := append([]review.Finding(nil), report.Findings...)
		sort.SliceStable(findings, func(i, j int) bool { return findings[i].Sev().Rank() > findings[j].Sev().Rank() })
		for _, f := range findings {
			where := fmt.Sprintf("`%s:%d`", f.Path, f.Line)
			if ref.Owner != "" && ref.Number > 0 {
				where = fmt.Sprintf("[`%s:%d`](https://github.com/%s/%s/pull/%d/files)", f.Path, f.Line, ref.Owner, ref.Repo, ref.Number)
			}
			fmt.Fprintf(&b, "| %s | %s | %s |\n", f.Sev(), where, mdCell(f.Title))
		}
		b.WriteString("\n")
	}
	if n := len(report.AlreadyReported); n > 0 {
		fmt.Fprintf(&b, "%d finding(s) were already posted by an earlier review and were not posted again.\n\n", n)
	}
	if len(report.Incomplete) > 0 {
		fmt.Fprintf(&b, "**Not reviewed** (a batch failed): %s\n\n", mdCell(strings.Join(report.Incomplete, ", ")))
	}
	if len(report.Linters) > 0 {
		b.WriteString("<details><summary>Analyzers</summary>\n\n")
		for _, s := range report.Linters {
			fmt.Fprintf(&b, "- %s — %s: %s\n", s.Linter, s.Outcome, mdCell(s.State))
		}
		b.WriteString("\n</details>\n\n")
	}
	appendFile(a.summary, b.String())
}

func countsOr(c review.Counts, empty string) string {
	if s := c.String(); s != "" {
		return s
	}
	return empty
}

// mdCell keeps a value inside one table cell: no pipes, no newlines.
func mdCell(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	return strings.Join(strings.Fields(s), " ")
}

// resultFor classifies a finished review the way the Action's outputs do.
func resultFor(report *review.Report, gate config.Severity) actionResult {
	if report == nil {
		return resultError
	}
	if report.Failed(gate) {
		return resultFindings
	}
	return resultClean
}
