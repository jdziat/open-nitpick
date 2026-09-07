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
		Head:     report.Head,
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

		c := vcs.Comment{
			Path:        f.Path,
			Line:        f.Line,
			Side:        side,
			Body:        renderComment(f, emoji),
			Fingerprint: Fingerprint(f),
			Class:       f.Class,
		}
		if f.FixValidated {
			// A committable multi-line suggestion is a comment on the range
			// it replaces: start_line at the anchor, line at the end.
			c.StartLine, c.Line = f.Line, f.FixEndLine
		}
		review.Comments = append(review.Comments, c)
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
	// whether a deterministic analyzer found it or a model inferred it, those
	// warrant different levels of trust, and only one of them can be wrong about
	// whether the code even does what it says.
	if attribution := attribution(f); attribution != "" {
		fmt.Fprintf(&b, "\n<sub>%s</sub>\n", attribution)
	}

	// A GitHub suggestion block is one click to apply. Which makes it the most
	// valuable thing a review bot offers and the most damaging thing it can get
	// wrong. A suggestion is only rendered as applicable code when it plausibly
	// IS code for a single line; anything else is shown as an ordinary quote so a
	// reader can act on it deliberately.
	if s := strings.TrimRight(f.Suggestion, "\n"); strings.TrimSpace(s) != "" {
		if f.FixValidated || suggestionIsApplicable(s) {
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
// The notices are the parts review.summary does not switch off: the policy
// notice, the incremental and nothing-reviewed notices, the analyzer roster,
// the uncovered list, the discard notice, the caller-walk notice and the
// withheld list. That setting asks for less narration; it is not permission to
// change what a review means without saying so. The walkthrough describes
// FILES, and suppressing it costs a reader context, while each notice
// describes something that makes silence mean less than it appears to: a
// finding the reviewer produced and something else then removed, a
// configuration the change supplied and this run refused, a deterministic
// analyzer that did not run, a deterministic finding this tool discarded
// before anything judged it, a search for callers that stopped short. With
// summaries off and these suppressed too, a run whose only finding an expert
// overruled, whose policy came from somewhere other than the file in the
// change, whose Go analyzer never produced a report, or whose analyzer
// findings were all thrown away for a forged path, publishes nothing at all
// and is indistinguishable from a clean review.
func renderSummary(report *Report, cfg *config.Config) string {
	var b strings.Builder

	// First, because it changes how everything below it should be read: these
	// findings, these skips and these budgets are the product of a policy that
	// is not the one in the change.
	b.WriteString(policyNotice(report))
	b.WriteString(incrementalNotice(report))
	b.WriteString(nothingReviewedNotice(report))
	b.WriteString(linterNotice(report))
	b.WriteString(uncoveredNotice(report))
	b.WriteString(discardNotice(report))
	b.WriteString(callerWalkNotice(report))
	// With the others, and NOT inside the walkthrough: a ceiling that changed
	// what was reviewed is a fact about coverage, and review.summary turning
	// the walkthrough off must not turn it into a silent trim.
	b.WriteString(budgetNote(report))

	if cfg == nil || cfg.Review.Summary {
		b.WriteString(walkthrough(report, cfg))
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

// incrementalNotice states that this run read only part of the change, and
// why, and how many of its findings were withheld as already posted.
//
// It is not collapsed and not gated on review.summary, for the same reason as
// policyNotice: a review of three files on a pull request that touches twenty
// has to say it read three, or an absence of findings on the other seventeen
// reads as seventeen clean files this run looked at.
func incrementalNotice(report *Report) string {
	inc := report.Incremental
	if inc == nil && len(report.AlreadyReported) == 0 && len(report.Superseded) == 0 {
		return ""
	}

	var b strings.Builder
	if inc != nil {
		since := inc.Since
		if len(since) > 7 {
			since = since[:7]
		}
		switch {
		case len(inc.Reviewed) == 0:
			fmt.Fprintf(&b, "**Nothing in this change has moved since the review at `%s`.**\n", since)
		default:
			fmt.Fprintf(&b, "**Reviewed the %d file(s) changed since the review at `%s`.**\n",
				len(inc.Reviewed), since)
		}
		if n := len(inc.Unchanged); n > 0 {
			fmt.Fprintf(&b, "%d other changed file(s) in this pull request were reviewed on an earlier\n"+
				"push and were not re-read; findings on them are in the earlier review.\n", n)
		}
	}
	if n := len(report.AlreadyReported); n > 0 {
		fmt.Fprintf(&b, "%d finding(s) from this run were already posted by an earlier review and\n"+
			"were not posted again.\n", n)
	}
	if n := len(report.Superseded); n > 0 {
		fmt.Fprintf(&b, "%d earlier comment thread(s) were resolved: the lines they pointed at changed\n"+
			"and the finding did not recur.\n", n)
	}
	return blockquote(b.String())
}

// nothingReviewedNotice states that a change reached the end of a run without
// any of it being reviewed.
//
// THE BUG IT ANSWERS is the emptiest possible review reading as the cleanest.
// When every changed file is set aside, there are no batches to send, so the
// engine returns before a model or an analyzer is asked anything, and the
// summary rendered as "", which the forge replaces with its default body:
// "open-nitpick found nothing to comment on." Measured on a report whose only
// changed file was `vendor/evil.go`. Vendored code is compiled into the
// binary, and `**/vendor/**` and `**/testdata/**` are in the shipped ignore
// list, so this is one file move away from any change that wants to go
// unlooked-at.
//
// Reasons are COUNTED and the files are not named, unlike the coverage block.
// `go mod vendor` is hundreds of files, and the reader can enumerate them from
// the ignore list, which is their own configuration rather than something the
// change wrote, a change may not supply the policy it is reviewed under. What
// they cannot reconstruct is that this run looked at nothing, which is the one
// sentence here.
//
// It is neither collapsed nor gated on review.summary, for policyNotice's
// reason: a reader who turned the walkthrough off asked for less narration, not
// to be left with a clean bill of health for a change nothing read.
func nothingReviewedNotice(report *Report) string {
	if report.Plan == nil || len(report.Plan.Batches) > 0 || len(report.Plan.Skipped) == 0 {
		return ""
	}

	counts := map[string]int{}
	var order []string
	for _, s := range report.Plan.Skipped {
		if _, seen := counts[s.Reason]; !seen {
			order = append(order, s.Reason)
		}
		counts[s.Reason]++
	}

	var b strings.Builder
	b.WriteString("**Nothing in this change was reviewed.**\n" +
		"Every changed file was set aside before the review began, so the absence of\n" +
		"findings below says nothing about this change:\n")
	for _, reason := range order {
		fmt.Fprintf(&b, "- %s: %d file(s)\n", inline(reason), counts[reason])
	}

	return blockquote(b.String())
}

// policyNotice states that the change's own configuration was not applied, and
// names the policy that was.
//
// It is neither collapsed into a <details> nor gated on review.summary. A
// contributor who edited the config file has to learn that the edit did not
// take effect for this run (otherwise they read a review that ignored their
// ignore rule as a bug), and a reviewer has to be able to see that the change
// tried to configure its own review, which is the whole signal when the edit
// was hostile.
//
// The wording follows the rule the "Files not reviewed" heading was fixed for:
// say exactly what happened. It names the file that was set aside, states that
// its configuration was not applied, and names what ran instead, never that
// the configuration was "ignored" or "invalid", because it was neither.
func policyNotice(report *Report) string {
	if !report.Policy.Replaced {
		return ""
	}

	// The last sentence is the part a maintainer can act on. Without it the
	// notice describes a dead end: their edit did nothing, and nothing says
	// whether it ever will. Locally there is no way to preview it either,
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

// linterNotice states, ON THE PULL REQUEST, what each deterministic analyzer
// did.
//
// THE BUG: these statuses reached os.Stderr and nowhere else, while the README
// said they appeared "beside the notice about a substituted .nitpick.yaml".
// They did not, and the difference is the whole point of both. policyNotice is
// published where the reviewer reads; a Fprintf into a CI log is not, so a
// reviewer could not tell a clean Go review from one whose Go analyzer never
// produced a report, which a pull request could arrange by adding a go.work.
//
// The headline goes in the <summary>, which forges render whether or not anyone
// expands the block: a reader who never opens it still learns that something did
// not run. The roster inside is COMPLETE rather than only the degradations,
// because a shorter list next run tells nobody which line went missing.
func linterNotice(report *Report) string {
	if len(report.Linters) == 0 {
		return ""
	}

	var b strings.Builder

	fmt.Fprintf(&b, "\n<details>\n<summary>%s</summary>\n\n", linterHeadline(report.Linters))
	b.WriteString("Analyzer configuration is policy, and a change may not supply the policy it is\n" +
		"reviewed under, so no analyzer read this repository's own lint settings.\n\n")

	// inline on both, because State carries an analyzer's own words and those
	// quote the tree under review, golangci-lint's typechecking errors name paths
	// from it. A reason spanning two lines, or carrying markup, would be text the
	// change wrote rendering as markup in a comment posted under this bot's name.
	for _, s := range report.Linters {
		fmt.Fprintf(&b, "- %s — %s: %s\n", inline(s.Linter), s.Outcome, inline(s.State))
	}

	b.WriteString("\n</details>\n")
	return b.String()
}

// linterHeadline counts the outcomes for the one line a reader sees collapsed.
//
// It counts rather than names, and it never says "all clear": the summary of a
// run where nothing ran has to read as a run where nothing ran.
func linterHeadline(statuses []LinterStatus) string {
	counts := map[LinterOutcome]int{}
	for _, s := range statuses {
		counts[s.Outcome]++
	}

	var parts []string
	for _, outcome := range []LinterOutcome{LinterRan, LinterFailed, LinterSkipped} {
		if n := counts[outcome]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, outcome))
		}
	}
	if len(parts) == 0 {
		return "Deterministic analyzers"
	}

	return "Deterministic analyzers: " + strings.Join(parts, ", ")
}

// discardNotice states, ON THE PULL REQUEST, how many findings a deterministic
// analyzer produced that this review threw away, and why.
//
// THE BUG: they were dropped by a bare `continue` in the analyzer set's
// normalize step. No counter, no log, no status, a finding entered and nothing
// recorded that it had gone. That single line silently absorbed a line
// directive forging the reported path, and it silently absorbed every finding
// from an operator's own analyzer config, whose paths arrived relative to the
// wrong directory. Both looked exactly like a clean Go review.
//
// It is counted per reason and not listed per finding, with ONE exception. Two
// of the reasons are this repository's publication policy working as
// configured and are routinely in the dozens on a normal pull request, a
// per-finding list of those is a wall of text that teaches a reader to
// collapse the block and never open it again, which is how the interesting
// line gets missed. The exception is the reason that is not policy: a path
// that is not in this checkout is evidence about the run, there is no healthy
// tree that produces one, and a count alone would not let anyone go and look.
func discardNotice(report *Report) string {
	if len(report.Discarded) == 0 {
		return ""
	}

	var b strings.Builder

	fmt.Fprintf(&b, "\n<details>\n<summary>%s</summary>\n\n", discardHeadline(report.Discarded))
	// It used to say "removed before triage, so nothing judged them", which
	// stopped being true when the anchor filter downstream of triage began
	// reporting its own drops. The distinction the sentence was making (these
	// were not weighed and rejected, they were never weighed), survives the
	// correction; where in the pipeline that happened does not change it.
	b.WriteString("A deterministic analyzer reported these and this review did not publish them. " +
		"They were removed by anchoring, not by judgement: nothing weighed them and decided against them.\n\n")

	counts := map[DiscardReason]int{}
	var order []DiscardReason
	for _, d := range report.Discarded {
		if _, seen := counts[d.Reason]; !seen {
			order = append(order, d.Reason)
		}
		counts[d.Reason]++
	}

	for _, reason := range order {
		fmt.Fprintf(&b, "- %d %s\n", counts[reason], reason)

		if reason != DiscardPathNotInCheckout {
			continue
		}
		// inline, because Path is what the ANALYZER printed and the point of
		// this branch is that the change chose it. It is wrapped in a code span
		// for the same reason every other path here is, and that span is why
		// this text can still carry markdown; see inline.
		for _, d := range report.Discarded {
			if d.Reason != reason {
				continue
			}
			fmt.Fprintf(&b, "  - `%s` said `%s:%d`\n", inline(d.Rule), inline(d.Path), d.Line)
		}
		b.WriteString("\n  Nothing in a healthy tree reports a path that is not in the checkout. " +
			"A Go line directive does, and the same directive aimed at a file that DOES exist moves a " +
			"finding onto code this change did not write.\n")
	}

	b.WriteString("\n</details>\n")
	return b.String()
}

// discardHeadline is the one line a reader sees collapsed.
//
// It leads with the total rather than the breakdown, because the number that
// matters to somebody scrolling past is how many deterministic findings did
// not make it, and it names the not-in-checkout count separately whenever
// there is one. That reason is the only one here that means something went
// wrong.
//
// WHAT THE TOTAL COUNTS, stated because it was wrong once and read as
// complete. It is every analyzer finding removed by ANCHORING: the analyzer
// set's normalize step and both of Engine.filterAnchors' passes. It was the
// first of those alone until filterAnchors was found dropping analyzer
// findings on a diff context line at Debug level, after this number had
// already been computed. It is still not "every analyzer finding that did not
// reach the pull request": triage sits between the two anchor passes and may
// merge one finding into another or drop it as noise, which is the job it is
// there to do and is a judgement, not silence, but nothing enumerates those
// either, so a reader comparing this total against a count of published
// findings will not balance the books.
func discardHeadline(discarded []LinterDiscard) string {
	forged := 0
	for _, d := range discarded {
		if d.Reason == DiscardPathNotInCheckout {
			forged++
		}
	}

	headline := fmt.Sprintf("Analyzer findings not published: %d", len(discarded))
	if forged > 0 {
		headline += fmt.Sprintf(", %d for a path that is not in this checkout", forged)
	}
	return headline
}

// uncoveredNotice states, ON THE PULL REQUEST, which parts of the change a
// deterministic analyzer ran over and did not fully cover.
//
// THE BUG IT ANSWERS: the roster said "golangci-lint, ran", and that was true
// and was read as "the Go analyzer looked at this change". It had not. A build
// constraint on the changed file with one unconstrained sibling beside it
// leaves the package loading perfectly while the changed file is never read,
// and a //nolint attached to the package clause has the analyzer read it and
// say nothing. Both produced zero findings, a nil error and a roster line
// saying the analyzer ran, the byte-identical shape of a clean Go review, and
// the silencing that reaches this state without an attack is the commoner one:
// an ordinary `foo_windows.go` reviewed on a Linux runner.
//
// It sits between the roster and the discard block because that is the order the
// three facts are read in: whether the analyzer ran, what it did not look at,
// and what it reported that this review then dropped.
//
// It is NOT gated on review.summary, for linterNotice's reason: a reader turning
// the walkthrough off is asking for less narration, not for the report to stop
// saying which parts of their change were never analyzed.
//
// Files are named individually and suppressions are listed with their line,
// because both are things a reviewer has to go and look at. A two-line count
// would leave nobody able to find them. Individually up to a bound: see
// maxUncoveredPerReason. The path is inline-escaped for the reason every other
// path in this file is: it comes from the diff, and a filename can carry
// markdown. callerWalkNotice says when the search for callers of what the
// change redefines stopped short, so a file with no callers attached is not
// read as a file with no callers.
func callerWalkNotice(report *Report) string {
	if report.Plan == nil || !report.Plan.CallerWalkTruncated {
		return ""
	}
	return "\nThe search for callers of what this change redefines stopped at its ceiling of candidate files, so some callers in this repository were not read and any break in them is not reported.\n"
}

func uncoveredNotice(report *Report) string {
	if len(report.Uncovered) == 0 {
		return ""
	}

	var b strings.Builder

	fmt.Fprintf(&b, "\n<details>\n<summary>%s</summary>\n\n", uncoveredHeadline(report.Uncovered))
	// "reported nothing about these" was the wording, and it stopped being true
	// when the list grew a reason that is a REDUCED analysis rather than an
	// absent one: a module's old `go` directive leaves the file read and part of
	// the ruleset switched off. Not covered fully is the claim every entry
	// supports.
	b.WriteString("The analyzers did not fully cover these parts of the change. " +
		"That is not the same as reporting them clean.\n\n")

	var order []UncoveredReason
	grouped := map[UncoveredReason][]LinterUncovered{}
	for _, u := range report.Uncovered {
		if _, seen := grouped[u.Reason]; !seen {
			order = append(order, u.Reason)
		}
		grouped[u.Reason] = append(grouped[u.Reason], u)
	}

	for _, reason := range order {
		entries := grouped[reason]
		fmt.Fprintf(&b, "- %s\n", reason)

		for _, u := range entries[:min(len(entries), maxUncoveredPerReason)] {
			if u.Line > 0 {
				fmt.Fprintf(&b, "  - `%s:%d` (`%s`)\n", inline(u.Path), u.Line, inline(u.Linter))
				continue
			}
			fmt.Fprintf(&b, "  - `%s` (`%s`)\n", inline(u.Path), inline(u.Linter))
		}
		if rest := len(entries) - maxUncoveredPerReason; rest > 0 {
			fmt.Fprintf(&b, "  - …and %d more\n", rest)
		}
	}

	b.WriteString("\n</details>\n")
	return b.String()
}

// maxUncoveredPerReason bounds how many entries one reason lists before the
// remainder is counted instead.
//
// A BODY THIS TOOL CANNOT PUBLISH IS WORSE THAN A SHORTER LIST, and every
// reason here can arrive in bulk from an ordinary pull request: `go mod
// vendor` adds hundreds of Go files that review.ignore withholds, and a port
// adds a directory of _windows.go. GitHub caps a review body at 65536 bytes
// (summaryFallback already exists because "a PR deleting thousands of files
// produces an enormous skipped-files section"), so an unbounded list here
// would trade a coverage notice for the whole review.
//
// PER REASON rather than overall, so one bulk route cannot push the others off
// the end: the vendored files and the one platform-specific file in the same
// change are different facts, and the second is the one nobody would find alone.
// The headline still counts every file, so the total is never the truncated one.
const maxUncoveredPerReason = 20

// uncoveredHeadline is the one line a reader sees collapsed.
//
// It counts FILES rather than entries, because a file with three added //nolint
// comments is one file the analyzer said nothing about and "3 uncovered" would
// overstate it. It never says "all covered": this block is absent when there is
// nothing to report, and a headline claiming full coverage would be a promise
// about every analyzer that has no Uncovered implementation, and about whatever
// coverage gap nobody has measured yet.
//
// It says "did not fully cover" rather than "reported nothing about", which is
// what it used to say. That was accurate while every entry meant the file went
// unread, and became false when UncoveredLanguageVersion arrived: there the file
// IS read and a version-gated part of the ruleset is not applied. A headline
// that overstates the gap teaches a reader to discount the block, which costs
// the entries that do mean the file went unread.
func uncoveredHeadline(uncovered []LinterUncovered) string {
	files := map[string]struct{}{}
	for _, u := range uncovered {
		files[u.Path] = struct{}{}
	}

	return fmt.Sprintf("Analyzed less than it ran over: %d file(s) the analyzers did not fully cover", len(files))
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
func walkthrough(report *Report, cfg *config.Config) string {
	var b strings.Builder

	// The receipt is counted from the report; the prose was written by a model
	// that never saw the change. Which one appears is review.summary_style,
	// and the receipt is the default.
	if cfg != nil && cfg.Review.EffectiveSummaryStyle() == config.SummaryReceipt {
		b.WriteString(receipt(report))
	} else if s := strings.TrimSpace(report.Summary); s != "" {
		b.WriteString(s)
		b.WriteString("\n")
	}

	if len(report.Findings) > 0 {
		fmt.Fprintf(&b, "\n**Findings:** %s\n", report.Counts)
	}

	// A review that lost batches must say so. Without this, "no issues found" on
	// a partially-failed run is indistinguishable from a clean bill of health,
	// the single most misleading thing this tool could print.
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
// who overruled it, why, and (for a re-rating), where it moved to.
//
// Both the finding's own text and the expert's reason are model-authored, and
// the model wrote them after reading a diff whose author is the person under
// review. So they are flattened onto one line, because a newline would break
// out of the bullet and leave the section reading as though the expert had
// overruled something else, and their markup characters are escaped, because a
// `</details>` in a reason closes the collapsed block early and puts the rest
// at top level of a comment posted under this bot's name, where GitHub renders
// an <img> or an <a>.
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

// inline prepares untrusted text for a bullet: one line, and no raw HTML.
//
// IT DOES NOT NEUTRALIZE MARKDOWN, and the comment here used to say it made
// text "unable to leave the element it is rendered inside", which is false in a
// forge comment where markdown IS the rendering language. `<b>` is escaped;
// `[text](https://example)` is not, and renders as a live link.
//
// That matters because some of what reaches here is written by the change
// under review, not by a model: an analyzer's failure reason quotes the tree,
// and a Go compile error quotes source verbatim, `var X int =
// "[CLICK](https://...)"` puts that string in golangci-lint's message,
// measured against 2.8.0. The result is a link the change authored, rendered
// inside a comment posted under this bot's name.
//
// Not fixed here because the fix is not this function: two call sites already
// wrap it in a code span, where backslash escapes would render literally and
// corrupt the paths they show. Neutralizing markdown means deciding per call
// site whether the text is prose or a code span, and giving the code-span cases
// a backtick-safe fence like fenceFor does for blocks. Recorded in the README's
// security section rather than half-done.
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
// A file too large for its full content used to be refused outright and land
// in Degraded, which is printed. Windowing such a file instead is a clear
// improvement (a window beats a bare diff), but it moved the file onto a list
// nothing rendered, so a reader who was previously told "this was reviewed
// from the diff alone" is now told nothing at all, even where most of the file
// was elided. Better context must not be paid for with worse disclosure.
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

// budgetNote states what a spending ceiling changed, and returns "" when no
// ceiling was configured or the whole diff fit under it.
//
// It reports the estimate as an estimate. The number is computed from token
// counts the run has not yet spent, at rates the operator supplied, so calling
// it the cost would be a claim this tool cannot make.
func budgetNote(report *Report) string {
	fit := report.Budget
	if fit == nil || (!fit.Trimmed() && !fit.Forced) {
		return ""
	}

	var b strings.Builder

	// min_files can keep every file and still exceed the ceiling, which drops
	// nothing and so has nothing to warn about coverage. It is still worth a
	// line: the operator set a limit and this run is expected to pass it.
	if !fit.Trimmed() {
		fmt.Fprintf(&b, "\n> **This review is expected to exceed its spending ceiling.** "+
			"Every changed file was reviewed at an estimated $%.2f against a $%.2f "+
			"ceiling, because `review.budget.min_files` is set to %d. Coverage is "+
			"unaffected.\n", fit.After.Dollars, fit.Ceiling, len(fit.Kept))
		return b.String()
	}

	fmt.Fprintf(&b, "\n> **This review was bounded by a spending ceiling.** "+
		"Reviewing every changed file was estimated at $%.2f against a $%.2f ceiling, "+
		"so the %d highest-ranked file(s) were read by a model at an estimated $%.2f "+
		"and %d were not. The absence of a model finding on those %d says only that "+
		"no model read them. Analyzers run over the whole change and are unaffected, "+
		"so an analyzer finding on a file in this list is still a real one.\n",
		fit.Before.Dollars, fit.Ceiling, len(fit.Kept), fit.After.Dollars,
		len(fit.Dropped), len(fit.Dropped))

	if fit.Forced {
		fmt.Fprintf(&b, ">\n> `review.budget.min_files` kept files the ceiling does not "+
			"pay for, so this run is expected to exceed it.\n")
	}

	return b.String()
}
