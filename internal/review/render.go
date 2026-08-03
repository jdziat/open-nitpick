package review

import (
	"fmt"
	"strings"

	"github.com/jdziat/open-nitpick/internal/bundle"
	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/diff"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// severityLabels give each severity a short visual marker. Emoji are used
// sparingly and only here: a reviewer scanning twenty comments needs to sort
// them at a glance.
var severityLabels = map[config.Severity]string{
	config.SeverityCritical: "🔴 critical",
	config.SeverityError:    "🟠 error",
	config.SeverityWarning:  "🟡 warning",
	config.SeverityInfo:     "🔵 info",
	config.SeverityNit:      "⚪ nit",
}

// label renders a severity for display.
func label(s config.Severity, emoji bool) string {
	if !emoji {
		return string(s)
	}
	if l, ok := severityLabels[s]; ok {
		return l
	}
	return string(s)
}

// Render turns a report into a publishable review.
func Render(report *Report, files diff.Files, cfg *config.Config) vcs.Review {
	emoji := true
	if cfg != nil {
		emoji = cfg.Persona.EmojiEnabled()
	}

	review := vcs.Review{
		Event:    vcs.EventComment,
		Comments: make([]vcs.Comment, 0, len(report.Findings)),
	}

	for _, f := range report.Findings {
		file := files.Find(f.Path)
		if file == nil {
			continue
		}

		side := string(diff.SideRight)
		if _, ok := file.Position(f.Line); !ok {
			// Should not happen after anchor filtering, but publishing a
			// comment on a line the forge will reject fails the whole review.
			continue
		}

		review.Comments = append(review.Comments, vcs.Comment{
			Path: f.Path,
			Line: f.Line,
			Side: side,
			Body: renderComment(f, emoji),
		})
	}

	review.Summary = renderSummary(report, cfg)
	return review
}

// renderComment formats one finding as a review comment.
func renderComment(f Finding, emoji bool) string {
	var b strings.Builder

	fmt.Fprintf(&b, "**%s", label(f.Sev(), emoji))
	if f.Category != "" {
		fmt.Fprintf(&b, " · %s", f.Category)
	}
	b.WriteString("**\n\n")

	fmt.Fprintf(&b, "%s\n", strings.TrimSpace(f.Title))

	if r := strings.TrimSpace(f.Rationale); r != "" {
		fmt.Fprintf(&b, "\n%s\n", r)
	}

	// Provenance. A reader deciding whether to act on a comment wants to know
	// whether a deterministic analyzer found it or a model inferred it — those
	// warrant different levels of trust, and only one of them can be wrong
	// about whether the code even does what it says.
	if attribution := attribution(f); attribution != "" {
		fmt.Fprintf(&b, "\n<sub>%s</sub>\n", attribution)
	}

	// A GitHub suggestion block is one click to apply — which makes it the most
	// valuable thing a review bot offers and the most damaging thing it can get
	// wrong. A suggestion is only rendered as applicable code when it plausibly
	// IS code for a single line; anything else is shown as an ordinary quote so
	// a reader can act on it deliberately.
	if s := strings.TrimRight(f.Suggestion, "\n"); strings.TrimSpace(s) != "" {
		if suggestionIsApplicable(s) {
			fence := fenceFor(s)
			fmt.Fprintf(&b, "\n%ssuggestion\n%s\n%s\n", fence, s, fence)
		} else {
			b.WriteString("\nSuggested change:\n\n")
			fence := fenceFor(s)
			fmt.Fprintf(&b, "%s\n%s\n%s\n", fence, s, fence)
		}
	}

	return b.String()
}

// attribution renders where a finding came from and who triaged it.
func attribution(f Finding) string {
	source := strings.TrimSpace(f.Source)
	if source == "" {
		return ""
	}

	if triager := strings.TrimSpace(f.Triager); triager != "" && triager != source {
		return fmt.Sprintf("flagged by %s · triaged by %s", source, triager)
	}
	return fmt.Sprintf("flagged by %s", source)
}

// suggestionIsApplicable reports whether a suggestion can safely be offered as
// a one-click replacement.
//
// A review comment anchors to one line, and GitHub replaces exactly that line
// with the block's contents. Two things therefore disqualify a suggestion:
// spanning multiple lines (the extra lines would be inserted while the
// originals survive, which is how a duplicated `if` block and an unbalanced
// brace get committed), and being prose rather than code (observed live: the
// model answered "Sanitize and validate the user input before using it").
func suggestionIsApplicable(s string) bool {
	if strings.Contains(strings.TrimRight(s, "\n"), "\n") {
		return false
	}
	return looksLikeCode(s)
}

// codeSignals are characters and tokens that prose essentially never contains
// but code almost always does.
var codeSignals = []string{
	"(", ")", "{", "}", "[", "]", ";", "=", "<", ">", ":=", "->", "=>", "::",
	".", "_", "\"", "'", "`", "*", "&", "|", "!", "/", "\\", "%", "+",
}

// looksLikeCode is a deliberately conservative heuristic: when in doubt it says
// no, because a wrongly-applicable suggestion corrupts a file while a wrongly-
// inapplicable one is merely a quote the reader applies by hand.
func looksLikeCode(s string) bool {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return false
	}

	for _, signal := range codeSignals {
		if strings.Contains(trimmed, signal) {
			return true
		}
	}

	// Some real statements carry no punctuation at all ("return err",
	// "continue", "pass"), so a leading keyword also counts.
	first, _, _ := strings.Cut(trimmed, " ")
	return codeKeywords[strings.ToLower(first)]
}

// codeKeywords are statement openers common across the languages this tool
// reviews. They exist so a punctuation-free line of real code is not mistaken
// for prose.
var codeKeywords = map[string]bool{
	"return": true, "break": true, "continue": true, "pass": true,
	"raise": true, "throw": true, "yield": true, "await": true, "defer": true,
	"go": true, "del": true, "assert": true, "import": true, "from": true,
	"package": true, "use": true, "let": true, "var": true, "const": true,
	"func": true, "def": true, "class": true, "if": true, "else": true,
	"for": true, "while": true, "switch": true, "case": true, "try": true,
	"catch": true, "finally": true, "with": true, "match": true, "fn": true,
}

// fenceFor returns a code fence long enough to contain s. A suggestion that
// itself contains a triple backtick would otherwise terminate its own block and
// spill the rest into the comment body as markdown.
func fenceFor(s string) string {
	longest := 0
	run := 0

	for _, r := range s {
		if r == '`' {
			run++
			if run > longest {
				longest = run
			}
			continue
		}
		run = 0
	}

	n := max(3, longest+1)
	return strings.Repeat("`", n)
}

// renderSummary builds the walkthrough comment.
func renderSummary(report *Report, cfg *config.Config) string {
	if cfg != nil && !cfg.Review.Summary {
		return ""
	}

	var b strings.Builder

	if s := strings.TrimSpace(report.Summary); s != "" {
		b.WriteString(s)
		b.WriteString("\n")
	}

	if len(report.Findings) > 0 {
		fmt.Fprintf(&b, "\n**Findings:** %s\n", report.Counts)
	}

	// A review that lost batches must say so. Without this, "no issues found"
	// on a partially-failed run is indistinguishable from a clean bill of
	// health — the single most misleading thing this tool could print.
	if !report.Complete() {
		fmt.Fprintf(&b, "\n> **This review is incomplete.** %d file(s) could not be reviewed, "+
			"so the absence of findings for them means nothing:\n>\n", len(report.Incomplete))
		for _, path := range report.Incomplete {
			fmt.Fprintf(&b, "> - `%s`\n", path)
		}
	}

	// Surfacing skips is a correctness matter, not a nicety: a review that
	// quietly ignored most of the diff otherwise looks like a clean bill of
	// health.
	if notes := skipNotes(report); notes != "" {
		b.WriteString("\n<details>\n<summary>Files not reviewed</summary>\n\n")
		b.WriteString(notes)
		b.WriteString("\n</details>\n")
	}

	out := strings.TrimSpace(b.String())
	if out == "" {
		return ""
	}
	return out + "\n\n<sub>Reviewed by open-nitpick.</sub>"
}

// skipNotes lists skipped files grouped by reason.
func skipNotes(report *Report) string {
	if report.Plan == nil || len(report.Plan.Skipped) == 0 {
		return ""
	}

	// Ignored files are excluded by explicit configuration, so listing them
	// every run is noise rather than information.
	byReason := map[string][]string{}
	order := []string{}

	for _, s := range report.Plan.Skipped {
		if s.Reason == bundle.ReasonIgnored {
			continue
		}
		if _, ok := byReason[s.Reason]; !ok {
			order = append(order, s.Reason)
		}
		byReason[s.Reason] = append(byReason[s.Reason], s.Path)
	}

	if len(order) == 0 {
		return ""
	}

	var b strings.Builder
	for _, reason := range order {
		fmt.Fprintf(&b, "- %s: %s\n", reason, strings.Join(byReason[reason], ", "))
	}
	return b.String()
}
