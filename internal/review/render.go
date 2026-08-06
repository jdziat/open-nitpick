package review

import (
	"fmt"
	"html"
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
//
// The policy notice and the withheld list are the two parts review.summary does
// not switch off. That setting asks for less narration; it is not permission to
// change what a review means without saying so. Everything else here describes
// FILES, and suppressing those costs a reader context — while these two describe
// a finding the reviewer produced and something else then removed, and a
// configuration the change supplied and this run refused. With summaries off and
// these suppressed too, a run whose only finding an expert overruled, or whose
// policy came from somewhere other than the file in the change, publishes
// nothing at all and is indistinguishable from a clean review.
func renderSummary(report *Report, cfg *config.Config) string {
	var b strings.Builder

	// First, because it changes how everything below it should be read: these
	// findings, these skips and these budgets are the product of a policy that
	// is not the one in the change.
	b.WriteString(policyNotice(report))

	if cfg == nil || cfg.Review.Summary {
		b.WriteString(walkthrough(report))
	}

	if notes := overruledNotes(report); notes != "" {
		// The heading has to describe what happened. These findings were
		// reported and then withheld; calling them anything else repeats an
		// error this renderer has already been fixed for once.
		b.WriteString("\n<details>\n<summary>Reported by the reviewer, then withheld after a domain expert disagreed</summary>\n\n")
		b.WriteString(notes)
		b.WriteString("\n</details>\n")
	}

	out := strings.TrimSpace(b.String())
	if out == "" {
		return ""
	}
	return out + "\n\n<sub>Reviewed by open-nitpick.</sub>"
}

// policyNotice states that the change's own configuration was not applied, and
// names the policy that was.
//
// It is neither collapsed into a <details> nor gated on review.summary. A
// contributor who edited the config file has to learn that the edit did not take
// effect for this run — otherwise they read a review that ignored their ignore
// rule as a bug — and a reviewer has to be able to see that the change tried to
// configure its own review, which is the whole signal when the edit was hostile.
//
// The wording follows the rule the "Files not reviewed" heading was fixed for:
// say exactly what happened. It names the file that was set aside, states that
// its configuration was not applied, and names what ran instead — never that the
// configuration was "ignored" or "invalid", because it was neither.
func policyNotice(report *Report) string {
	if !report.Policy.Replaced {
		return ""
	}

	// The last sentence is the part a maintainer can act on. Without it the
	// notice describes a dead end: their edit did nothing, and nothing says
	// whether it ever will. Locally there is no way to preview it either —
	// vcs.Local resolves the base of a working-tree review to HEAD, so an
	// uncommitted config edit is reviewed under the committed file.
	return blockquote(fmt.Sprintf(
		"**The configuration in this change was not applied to this review.**\n"+
			"This change edits `%s`, and a change may not supply the policy it is reviewed\n"+
			"under. This review ran under %s.\n"+
			"Those settings govern changes that do not edit them, so this file takes effect\n"+
			"for reviews after it lands. To try it out first, pass `-config` a copy kept\n"+
			"outside the repository.",
		inline(report.Policy.Modified), report.Policy.Source()))
}

// policyFailureNotice explains, on the pull request, why no review ran at all.
//
// It is published in place of a review, never alongside one: the run has no
// findings to report and no clean bill of health to give, and the maintainer
// whose configuration change triggered it would otherwise learn nothing except
// that a job went red.
//
// The wording claims only what is true on every path that reaches it. A resolver
// that errored has not said whether the change edits the configuration, so
// asserting that it does would be a guess printed as a fact on somebody's pull
// request.
func policyFailureNotice(cause error) string {
	return blockquote(fmt.Sprintf(
		"**This change was not reviewed.**\n"+
			"A change may not supply the policy it is reviewed under, so the policy for this\n"+
			"review has to come from a revision the change did not write, or from built-in\n"+
			"defaults. Neither could produce a usable one:\n"+
			"%s", inline(cause.Error())))
}

// blockquote prefixes every line so that nothing spliced into a notice can
// leave it.
//
// The values these notices carry are forge errors, parser errors and
// config.Policy.Reason, and a config that fails validation twice reports both
// messages joined by a newline. A single "> " on the first line meant the rest
// of that error rendered at top level of a comment posted under this bot's name.
func blockquote(s string) string {
	var b strings.Builder

	for line := range strings.SplitSeq(strings.TrimRight(s, "\n"), "\n") {
		b.WriteString("> ")
		b.WriteString(line)
		b.WriteByte('\n')
	}
	b.WriteByte('\n')

	return b.String()
}

// walkthrough is the narration review.summary controls.
func walkthrough(report *Report) string {
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
			// Flattened for the same reason a finding's title is: this list can
			// carry a path taken from the diff, and a name containing a newline
			// breaks out of the bullet and continues at top level.
			fmt.Fprintf(&b, "> - `%s`\n", inline(path))
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

	// Reported separately, and worded so it cannot be read as "not reviewed":
	// these files were reviewed, just from the diff alone.
	if notes := degradedNotes(report); notes != "" {
		b.WriteString("\n<details>\n<summary>Reviewed from the diff only, without full file context</summary>\n\n")
		b.WriteString(notes)
		b.WriteString("\n</details>\n")
	}

	// Distinct from both of the above: these files WERE reviewed and DID carry
	// file context, just not all of it. The heading has to say that precisely,
	// because a reader who concludes "not reviewed" from this list will
	// re-review the file by hand, and one who concludes "fully reviewed" will
	// trust an absence of findings in the part that was elided.
	if notes := windowedNotes(report); notes != "" {
		b.WriteString("\n<details>\n<summary>Reviewed with reduced file context</summary>\n\n")
		b.WriteString(notes)
		b.WriteString("\n</details>\n")
	}

	return b.String()
}

// overruledNotes lists findings an expert kept off the pull request, each with
// who overruled it, why, and — for a re-rating — where it moved to.
//
// Both the finding's own text and the expert's reason are model-authored, and
// the model wrote them after reading a diff whose author is the person under
// review. So they are flattened onto one line, because a newline would break
// out of the bullet and leave the section reading as though the expert had
// overruled something else, and their markup characters are escaped, because a
// `</details>` in a reason closes the collapsed block early and puts the rest at
// top level of a comment posted under this bot's name — where GitHub renders an
// <img> or an <a>.
func overruledNotes(report *Report) string {
	var b strings.Builder

	for _, r := range report.Overruled {
		fmt.Fprintf(&b, "- `%s:%d` — %s\n", r.Finding.Path, r.Finding.Line, inline(r.Finding.Title))

		if r.Revised != "" {
			// The FROM level is the finding's own, which the operator's ceiling
			// may have already reduced. It is quoted rather than re-capped on
			// purpose: this line explains why a finding is absent, and quoting a
			// level the reader never saw published would make the explanation
			// harder to follow, not easier. What must not happen is the reverse
			// -- a ceiling of warning rendering "re-rated this from critical" as
			// though critical had been published -- so the level printed here is
			// the one that WAS published for this finding.
			fmt.Fprintf(&b, "  - %s re-rated this from %s to %s, below this repository's minimum severity: %s\n",
				inline(r.Expert), r.Finding.Sev(), r.Revised, inline(r.Reason))
			continue
		}
		fmt.Fprintf(&b, "  - %s: %s\n", inline(r.Expert), inline(r.Reason))
	}

	return b.String()
}

// oneLine collapses text onto a single line.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// inline prepares model-authored text for a bullet: one line, and unable to
// leave the element it is rendered inside.
func inline(s string) string { return html.EscapeString(oneLine(s)) }

// skipNotes lists files that were not reviewed, grouped by reason.
func skipNotes(report *Report) string {
	if report.Plan == nil {
		return ""
	}
	// Ignored files are excluded by explicit configuration, so listing them
	// every run is noise rather than information.
	return groupByReason(report.Plan.Skipped, bundle.ReasonIgnored)
}

// degradedNotes lists files that were reviewed without their full content.
func degradedNotes(report *Report) string {
	if report.Plan == nil {
		return ""
	}
	return groupByReason(report.Plan.Degraded, "")
}

// windowedNotes lists files reviewed with only a window around their changes.
//
// A file too large for its full content used to be refused outright and land in
// Degraded, which is printed. Windowing such a file instead is a clear
// improvement — a window beats a bare diff — but it moved the file onto a list
// nothing rendered, so a reader who was previously told "this was reviewed from
// the diff alone" is now told nothing at all, even where most of the file was
// elided. Better context must not be paid for with worse disclosure.
func windowedNotes(report *Report) string {
	if report.Plan == nil {
		return ""
	}
	return groupByReason(report.Plan.Windowed, "")
}

// groupByReason renders "- reason: path, path" lines, omitting one reason.
func groupByReason(skips []bundle.Skip, omit string) string {
	byReason := map[string][]string{}
	order := []string{}

	for _, s := range skips {
		if omit != "" && s.Reason == omit {
			continue
		}
		if _, ok := byReason[s.Reason]; !ok {
			order = append(order, s.Reason)
		}
		byReason[s.Reason] = append(byReason[s.Reason], s.Path)
	}

	var b strings.Builder
	for _, reason := range order {
		fmt.Fprintf(&b, "- %s: %s\n", reason, strings.Join(byReason[reason], ", "))
	}
	return b.String()
}
