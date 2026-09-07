package review

import (
	"fmt"
	"sort"
	"strings"
)

// Receipt is the walkthrough written from facts instead of generated.
//
// The generated one is asked for "a short walkthrough of the change" and the
// triage model has never seen the change, so what it writes is a description of
// the findings presented as a description of the code. Measured, most of the
// content words in it do not appear in the diff it describes.
//
// This says less and cannot be wrong. Every number here is counted from the
// report the run already produced: no model is asked, so there is nothing to
// hallucinate and nothing to guard.
//
// It deliberately does NOT restate the coverage notices. Those are rendered
// separately and carry the detail: which files were skipped, which analyzers
// did not run, which batches failed. The receipt is the top line a reader sees
// before deciding whether to open any of it.
func receipt(report *Report) string {
	if report == nil {
		return ""
	}

	var parts []string

	reviewed, changed := 0, len(report.Files)
	if report.Plan != nil {
		reviewed = report.Plan.Files()
	}
	switch {
	case changed == 0:
		return ""
	case reviewed == changed:
		parts = append(parts, fmt.Sprintf("Read %s.", plural(reviewed, "changed file")))
	default:
		parts = append(parts, fmt.Sprintf("Read %d of %s.", reviewed, plural(changed, "changed file")))
	}

	if n := len(report.Findings); n > 0 {
		parts = append(parts, fmt.Sprintf("%s: %s.", plural(n, "finding"), report.Counts))
	} else {
		// Not "the change is clean". Nothing was found, by what ran, over what
		// was read, and the notices below say what that excludes.
		parts = append(parts, "Nothing found in what was read.")
	}

	if analyzers := ranAnalyzers(report); len(analyzers) > 0 {
		parts = append(parts, fmt.Sprintf("Analyzers: %s.", strings.Join(analyzers, ", ")))
	}

	if n := len(report.Incomplete); n > 0 {
		parts = append(parts, fmt.Sprintf("%s could not be reviewed.", plural(n, "file")))
	}

	return strings.Join(parts, " ") + "\n"
}

// ranAnalyzers names the analyzers that produced a result, in name order so two
// runs over the same change print the same line.
func ranAnalyzers(report *Report) []string {
	var out []string
	for _, s := range report.Linters {
		if s.Outcome == LinterRan {
			out = append(out, s.Linter)
		}
	}
	sort.Strings(out)
	return out
}

// plural writes "1 file" and "3 files".
func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
