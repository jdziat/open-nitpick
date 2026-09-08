package review

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jdziat/open-nitpick/internal/bundle"
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
// It deliberately does not restate the coverage notices. Those are rendered
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

	// A file with nothing to review and a file the reviewer could not read are
	// both absent from the numerator, and they are not the same news. Deleting
	// a file is routine; failing to fetch one is a hole. Pooling them made
	// "Read 2 of 5" read like a coverage failure on a change whose other three
	// files were a deletion and two with no added lines.
	if routine, unread := classifySkips(report); routine > 0 || unread > 0 {
		var notes []string
		if routine > 0 {
			notes = append(notes, fmt.Sprintf("%s had nothing to review", plural(routine, "file")))
		}
		if unread > 0 {
			notes = append(notes, fmt.Sprintf("%s could not be read", plural(unread, "file")))
		}
		parts = append(parts, capitalize(strings.Join(notes, ", "))+".")
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

	// Files only. A failed stage is reported by stageNotice, which renders
	// whether or not the walkthrough does, and saying it in both put the
	// sentence on the page twice.
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

// stageSentence says what a failed stage cost the reader, in that stage's own
// terms. A stage nobody has written a sentence for still gets one, because a
// stage that fails silently is the defect this whole path exists to close.
func stageSentence(st StageStatus) string {
	switch st.Stage {
	case "triage":
		return fmt.Sprintf("Triage failed (%s), so these findings were not "+
			"deduplicated, ranked or summarized.", st.Reason)
	case "style":
		return fmt.Sprintf("The style pass failed (%s), so style findings are missing.", st.Reason)
	default:
		return fmt.Sprintf("The %s stage failed (%s).", st.Stage, st.Reason)
	}
}

// unreadable names the skip reasons that mean the reviewer wanted the file and
// did not get it, or a limit cut it off. Everything else is routine: a
// deletion, a binary, a generated file or an
// ignored path carries nothing a comment could attach to, and reporting the
// two together makes a clean review look partial. The reasons are prefixes,
// since the planner appends a size or budget figure to several and an
// exact-match table reclassifies each of those as routine.
var unreadable = []string{
	bundle.ReasonUnavailable,
	bundle.ReasonTooLarge,
	bundle.ReasonOverBudget,
	bundle.ReasonFileLimit,
	"over the review.budget.max_spend ceiling",
}

// classifySkips splits the planner's skips into the two kinds.
func classifySkips(report *Report) (routine, unread int) {
	if report.Plan == nil {
		return 0, 0
	}
	for _, s := range report.Plan.Skipped {
		hole := false
		for _, prefix := range unreadable {
			if strings.HasPrefix(s.Reason, prefix) {
				hole = true
				break
			}
		}
		if hole {
			unread++
			continue
		}
		routine++
	}
	return routine, unread
}

// capitalize upper-cases the first letter, since these notes are sentences.
func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// Escalation records that a fallback model reviewed a batch the primary could
// not, so a reader can tell which findings came from which model.
//
// Named rather than logged alone: a weaker model's findings sitting beside a
// stronger one's with nothing to separate them is the kind of silence this
// package reports everywhere else.
type Escalation struct {
	// Files are the batch's paths, From the model that could not answer, and
	// To the one that did.
	Files    []string
	From, To string
}

// EscalationNotice says which batches a fallback model reviewed, and returns
// "" when none did.
func EscalationNotice(report *Report) string {
	if report == nil || len(report.Escalated) == 0 {
		return ""
	}

	byModel := map[string]int{}
	files := 0
	for _, e := range report.Escalated {
		byModel[e.From+" to "+e.To]++
		files += len(e.Files)
	}

	pairs := make([]string, 0, len(byModel))
	for pair, n := range byModel {
		pairs = append(pairs, fmt.Sprintf("%d batch(es) %s", n, pair))
	}
	sort.Strings(pairs)

	return fmt.Sprintf("\n%d file(s) were reviewed by a fallback model after the primary could not answer: %s.\n",
		files, strings.Join(pairs, "; "))
}
