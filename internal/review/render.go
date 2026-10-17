package review

import (
	"fmt"
	"html"
	"net/url"
	"path"
	"sort"
	"strings"

	"github.com/jdziat/open-nitpick/internal/bundle"
	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/diff"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// severityMarks give each severity a short visual marker. Emoji are used
// sparingly and only here: a reviewer scanning twenty comments needs to sort
// them at a glance.
var severityMarks = map[config.Severity]string{
	config.SeverityCritical: "🔴",
	config.SeverityError:    "🟠",
	config.SeverityWarning:  "🟡",
	config.SeverityInfo:     "🔵",
	config.SeverityNit:      "⚪",
}

// Render turns a report into a publishable review.
func Render(report *Report, files diff.Files, cfg *config.Config) vcs.Review {
	emoji := true
	agent := false
	invite := true
	if cfg != nil {
		emoji = cfg.Persona.EmojiEnabled()
		agent = cfg.Review.AgentPrompt
		invite = cfg.Review.Respond.RepliesEnabled()
	}

	var agentPrompt map[string][]string
	if agent {
		agentPrompt = batchMates(report.Plan)
	}

	review := vcs.Review{
		Event:      reviewEvent(report, cfg),
		Comments:   make([]vcs.Comment, 0, len(report.Findings)),
		Head:       report.Head,
		Progress:   report.Progress,
		Incomplete: !report.reusableCoverage(),
	}

	// What this run was priced at, recorded with the review so a later run
	// under a pull-request ceiling can subtract it. After is the estimate for
	// what is being sent, and equals Before whenever nothing was trimmed.
	if report.Budget != nil {
		review.Spend = report.Budget.After.Dollars
	}

	for _, f := range report.Findings {
		if f.SummaryOnly {
			continue
		}
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
			Body:        composeComment(f, emoji, agentPrompt, invite),
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

// batchMates maps each reviewed path to the files that shared its batch.
//
// That set is what the reviewer had in front of it when it wrote the finding,
// so it is the blast radius an agent would otherwise re-derive. Taken from the
// plan rather than recomputed, because the plan is what was sent.
func batchMates(plan *bundle.Plan) map[string][]string {
	if plan == nil {
		return nil
	}

	out := map[string][]string{}
	for _, batch := range plan.Batches {
		paths := batch.Paths()
		for _, p := range paths {
			mates := make([]string, 0, len(paths)-1)
			for _, other := range paths {
				if other != p {
					mates = append(mates, other)
				}
			}
			sort.Strings(mates)
			out[p] = mates
		}
	}
	return out
}

// agentPromptBlock is what a coding agent needs to act on a finding, folded
// so a human reader scrolls past it.
//
// Assembled from fields the engine already holds. Nothing here is generated,
// so the block cannot assert anything the review did not establish. That is
// also its limit: it does not say what would make the fix wrong, because
// nothing in a finding records that, and inventing it would be the kind of
// unmeasured claim this project refuses everywhere else.
func agentPromptBlock(f Finding, read map[string][]string) string {
	var inner strings.Builder

	fmt.Fprintf(&inner, "anchor:    %s\n", span(f.Path, f.Line, f.EndLine))
	for _, also := range f.AlsoAt {
		fmt.Fprintf(&inner, "also:      %s\n", span(f.Path, also.Line, also.EndLine))
	}
	if f.Class != "" {
		fmt.Fprintf(&inner, "class:     %s\n", f.Class)
	}
	fmt.Fprintf(&inner, "severity:  %s\n", f.Sev())
	if src := strings.TrimSpace(f.Source); src != "" {
		fmt.Fprintf(&inner, "found by:  %s\n", src)
	}

	// Only for a finding a model wrote. An analyzer read one file and knows
	// nothing of the batch, so naming its mates here would assert reading that
	// did not happen, which is the claim this block promises not to make.
	if mates := read[f.Path]; len(mates) > 0 && !f.FromAnalyzer {
		fmt.Fprintf(&inner, "also read: %s\n", strings.Join(mates, ", "))
	}

	fmt.Fprintf(&inner, "\n%s\n", strings.TrimSpace(f.Title))
	if r := strings.TrimSpace(f.Rationale); r != "" {
		fmt.Fprintf(&inner, "\n%s\n", r)
	}

	// The fence is widened past anything the content holds. Title and
	// Rationale are model-authored and can quote the diff under review, so a
	// triple backtick in either would close a fixed fence early and spill the
	// rest, the closing tag included, as rendered markdown in a comment posted
	// under this tool's name.
	body := inner.String()
	fence := fenceFor(body)

	return fmt.Sprintf("\n<details><summary>Fix prompt</summary>\n\n%s\n%s%s\n\n</details>\n",
		fence, body, fence)
}

// span renders a path and the lines a finding covers.
func span(path string, line, end int) string {
	if end > line {
		return fmt.Sprintf("%s:%d-%d", path, line, end)
	}
	return fmt.Sprintf("%s:%d", path, line)
}

// renderComment formats one finding as a review comment. read names the files
// the reviewer had in front of it for each path's batch, or is nil when no
// agent prompt was asked for.
func renderComment(f Finding, emoji bool, read map[string][]string) string {
	return composeComment(f, emoji, read, false)
}

// ReplyInvitation is the line under a published finding that tells its reader
// the thread is a conversation. The reviewer reads a reply under its own
// comment without being mentioned, so the invitation has to say so.
const ReplyInvitation = "**Reply to explain or disagree.** I read replies here and resolve the thread if the reason holds."

// composeComment lays a finding out in the order a reader acts on it: how
// serious and what kind, what is wrong, why, the change that fixes it, and
// only then who found it. invite adds the reply line, which belongs on an
// inline comment and not on a finding quoted inside the summary.
func composeComment(f Finding, emoji bool, read map[string][]string, invite bool) string {
	var b strings.Builder

	b.WriteString("**" + severityChip(f.Sev(), emoji) + "**")
	if f.Category != "" {
		fmt.Fprintf(&b, " &nbsp;·&nbsp; %s", inline(f.Category))
	}
	b.WriteString("\n\n")

	fmt.Fprintf(&b, "**%s**\n", inline(strings.TrimSpace(f.Title)))

	if r := strings.TrimSpace(f.Rationale); r != "" {
		fmt.Fprintf(&b, "\n%s\n", r)
	}

	// Stated doubt, kept where it cannot be missed: a reader deciding whether
	// to act wants to know that the expert check ran and came back undecided,
	// which is otherwise indistinguishable from the check having agreed.
	//
	// Flattened and escaped, because the expert wrote it after reading a diff
	// the change's author controls. A newline would put what follows outside
	// the alert, and markup would render live in a comment posted under this
	// tool's name.
	if u := inline(strings.TrimSpace(f.Unresolved)); u != "" {
		// The expert's name is set beside the reason on every path that writes
		// one, so an empty one is a caller that built the finding by hand. It
		// still must not render "resolved by : ".
		if by := inline(strings.TrimSpace(f.UnresolvedBy)); by != "" {
			fmt.Fprintf(&b, "\n> [!NOTE]\n> Not settled. The %s check could not decide: %s\n", by, u)
		} else {
			fmt.Fprintf(&b, "\n> [!NOTE]\n> Not settled. A check could not decide: %s\n", u)
		}
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
			b.WriteString("\n**Suggested change**\n\n")
			fence := fenceFor(s)
			fmt.Fprintf(&b, "%s\n%s\n%s\n", fence, s, fence)
		}
	}

	// What the reviewer read, so a reader can go and look. Ids only: the
	// entries ship in this repository under internal/knowledge/corpus, and a
	// reader who wants the text has a filename. It is not a claim that any of
	// them produced the finding, which is why the line says "read", see
	// evidence.go.
	//
	// Every id resolves to a file, and nothing here checks that, because
	// nothing here could be wrong about it: Evidence is json:"-" and is filled
	// from the entries retrieval returned, which are the corpus. A model can
	// neither write the field nor name an entry that is not in it. See
	// TestEvidenceIsOnlyEverCorpusIDs.
	if len(f.Evidence) > 0 {
		ids := make([]string, 0, len(f.Evidence))
		for _, id := range f.Evidence {
			ids = append(ids, "`"+inline(id)+"`")
		}
		fmt.Fprintf(&b, "\n<details><summary>Reference read</summary>\n\nreference read: %s\n\n</details>\n", strings.Join(ids, ", "))
	}

	// Folded: a coding agent needs it and a person scrolls past it. A finding
	// whose suggestion GitHub can apply in one click still gets one, since the
	// block says where else the finding reaches and what the reviewer read.
	if read != nil {
		b.WriteString(agentPromptBlock(f, read))
	}

	// Provenance. A reader deciding whether to act on a comment wants to know
	// whether a deterministic analyzer found it or a model inferred it, those
	// warrant different levels of trust, and only one of them can be wrong about
	// whether the code even does what it says.
	//
	//
	// Plain paragraphs, source first and the invitation under it. <sub> is out:
	// GitHub gives it a zero line height, so a source line that wraps on a
	// narrow screen prints over itself.
	if footer := attribution(f); footer != "" {
		fmt.Fprintf(&b, "\n%s\n", footer)
	}
	if invite {
		fmt.Fprintf(&b, "\n💬 %s\n", ReplyInvitation)
	}

	return b.String()
}

// severityChip is the severity as the first word of a comment: capitalised,
// beside its marker when markers are on.
func severityChip(s config.Severity, emoji bool) string {
	name := string(s)
	if name != "" {
		name = strings.ToUpper(name[:1]) + name[1:]
	}
	if !emoji {
		return name
	}
	if mark, ok := severityMarks[s]; ok {
		return mark + " " + name
	}
	return name
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
// A review comment anchors to one line, and GitHub replaces that line with the
// block's contents. Spanning lines disqualifies a suggestion, since the extra
// lines are inserted while the originals survive, committing a duplicated `if`
// block. So does prose, observed live as "Sanitize and validate the input".
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
	"_", "\"", "`", "*", "&", "|", "!", "/", "\\", "%", "+",
}

// codeSignalsTight are code markers that also appear in ordinary prose. A
// sentence ends in a period and a contraction carries an apostrophe, so these
// count only when the line has no whitespace to separate words.
var codeSignalsTight = []string{".", "'"}

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
	if !strings.ContainsAny(trimmed, " \t") {
		for _, signal := range codeSignalsTight {
			if strings.Contains(trimmed, signal) {
				return true
			}
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
	b.WriteString(fastReviewNotice(report))
	b.WriteString(unknownKeyNotice(report))
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
	if report.ReusedRequests > 0 {
		fmt.Fprintf(&b, "Reused %d completed model request(s) from the previous review. Downstream checks ran again.\n\n", report.ReusedRequests)
	}
	// Same rule, same reason. A stage that did not run is a fact about what
	// the findings below have been through, and review.summary is a setting
	// about prose.
	b.WriteString(stageNotice(report))
	b.WriteString(EscalationNotice(report))

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

	for _, finding := range report.Findings {
		if finding.SummaryOnly {
			fmt.Fprintf(&b, "\n\n<details><summary>Design evidence: %s</summary>\n\n%s\n</details>\n", designEvidenceLocation(report, finding), renderComment(finding, cfg == nil || cfg.Persona.EmojiEnabled(), nil))
		}
	}
	if len(report.UnpublishedModelFindings) > 0 {
		b.WriteString("\n\n<details><summary>Model claims with unsupported source locations</summary>\n\n")
		for _, finding := range report.UnpublishedModelFindings {
			fmt.Fprintf(&b, "<p><code>%s:%d</code>: %s — %s</p>\n", inline(finding.Path), finding.Line, inline(finding.Title), inline(finding.Unresolved))
		}
		b.WriteString("\n</details>\n")
	}
	if report.Practices != nil {
		b.WriteString("\n\n<details><summary>Engineering practices</summary>\n\n<pre>" + html.EscapeString(report.Practices.Text()) + "</pre>\n\n</details>")
	}
	out := strings.TrimSpace(b.String())
	if out == "" {
		return ""
	}
	return out + "\n\n<sub>Reviewed by open-nitpick.</sub>"
}

// fastReviewNotice states the deliberate limits of the bounded command.
func fastReviewNotice(report *Report) string {
	if report == nil || !report.FastReview {
		return ""
	}
	var b strings.Builder
	b.WriteString("**Fast review:** changed-file diffs only; related context, analyzers, validation, and model triage were not run.\n")
	if report.OmittedFindings > 0 {
		limit := report.FastLimit
		if limit <= 0 {
			limit = 10
		}
		fmt.Fprintf(&b, "Showing the %d highest-ranked findings; %d lower-ranked finding(s) were omitted. Run `nitpick review` for the full result.\n", limit, report.OmittedFindings)
	}
	return blockquote(b.String())
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
		case inc.Recheck && since == "":
			b.WriteString("**Rechecked the whole change because earlier findings remain.**\n")
		case inc.Recheck && len(inc.Unchanged) == 0:
			fmt.Fprintf(&b, "**Rechecked the whole change because findings from the review at `%s` remain.**\n", since)
		case inc.Recheck:
			fmt.Fprintf(&b, "**Reviewed the %d file(s) changed since the review at `%s`, or still carrying findings from it.**\n",
				len(inc.Reviewed), since)
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

// unknownKeyNotice states, on the pull request, which config keys this build
// does not have and therefore did not apply.
//
// Beside policyNotice for its reason: a setting a reader believes is in force
// and is not changes how every finding below should be read. It is published
// rather than only logged because the person who wrote the key reads the pull
// request, and the operator who set the environment variable reads the CI log.
//
// The keys were ignored on that operator's word that the binary is behind the
// config. Nothing here can check that, so the notice says what was ignored and
// leaves the reading to whoever knows which it was.
func unknownKeyNotice(report *Report) string {
	cfg := report.Policy.Config
	if cfg == nil || len(cfg.Unknown) == 0 {
		return ""
	}

	// inline escapes HTML and flattens, and does not neutralise markdown: a
	// backtick in the key closes the code span and what follows renders as
	// markdown, a link included, in a comment posted under this tool's name.
	// The key is bytes the change under review chose. A config key never
	// contains one, so replacing it costs a reader nothing and closes the span.
	keys := make([]string, 0, len(cfg.Unknown))
	for _, k := range cfg.Unknown {
		keys = append(keys, "`"+inline(strings.ReplaceAll(k, "`", "'"))+"`")
	}
	return blockquote(fmt.Sprintf(
		"**This nitpick does not have %s.**\n"+
			"They were ignored rather than failing the run, because %s is set.\n"+
			"A key added to a newer nitpick reads exactly like a typo here, so if one of\n"+
			"these is a typo it is doing nothing.",
		strings.Join(keys, ", "), config.EnvIgnoreUnknownKeys))
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

// discardHeadline is the line a reader sees while the block is collapsed. It
// leads with the total, and names the not-in-checkout count separately because
// that is the one reason that means something went wrong.
//
// The total counts findings removed by anchoring: the analyzer set's normalize
// step and both of Engine.filterAnchors' passes. Triage sits between the two
// anchor passes and may merge or drop a finding as noise, which nothing
// enumerates, so this total will not balance against a count of published
// findings.
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

// callerWalkNotice says when the search for callers of what the change
// redefines stopped short, so a file with no callers attached is not read as a
// file with no callers.
func callerWalkNotice(report *Report) string {
	if report.Plan == nil || !report.Plan.CallerWalkTruncated {
		return ""
	}
	return "\nThe search for callers of what this change redefines stopped at its ceiling of candidate files, so some callers in this repository were not read and any break in them is not reported.\n"
}

// uncoveredNotice states on the pull request which changed files an analyzer
// ran over without fully covering, such as a file excluded by a build
// constraint or one silenced by a //nolint on its package clause. The roster
// alone would read "ran" for these. It is not gated on review.summary: turning
// the walkthrough off asks for less narration, not for coverage gaps to go
// unreported. Paths are inline-escaped because a filename can carry markdown.
func uncoveredNotice(report *Report) string {
	// Placed between the roster and the discard block, the order the three facts
	// are read in: whether the analyzer ran, what it did not look at, and what
	// it reported that this review then dropped.
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
// remainder is counted. Every reason can arrive in bulk (`go mod vendor`, a
// directory of _windows.go files) and GitHub caps a review body at 65536
// bytes, so an unbounded list could cost the whole review. The bound is per
// reason, so a bulk route cannot push a lone platform-specific file off the
// end, and the headline still counts every file.
const maxUncoveredPerReason = 20

// uncoveredHeadline is the line a reader sees while the block is collapsed. It
// counts files, since three //nolint comments in one file are one file the
// analyzer said nothing about, and it never claims full coverage: the block is
// absent when there is nothing to report. "Did not fully cover" is deliberate,
// because a language-version gap reads the file and skips only part of the
// ruleset.
func uncoveredHeadline(uncovered []LinterUncovered) string {
	files := map[string]struct{}{}
	for _, u := range uncovered {
		files[u.Path] = struct{}{}
	}

	return fmt.Sprintf("Analyzed less than it ran over: %d file(s) the analyzers did not fully cover", len(files))
}

// policyFailureNotice explains on the pull request why no review ran. It is
// published in place of a review, never alongside one. The wording claims only
// what holds on every path that reaches it: a resolver that errored has not
// said whether the change edits the configuration.
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

	// review.summary_style picks between the receipt, counted from the report,
	// and prose a model wrote; the receipt is the default.
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

	// Distinct from both above: these files were reviewed with some, not all,
	// of their context. The heading must say so, since "not reviewed" sends a
	// reader to redo the file and "fully reviewed" trusts the elided part.
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
			// The "from" level is the one published for this finding, after the
			// operator's ceiling. Quoting an unpublished level ("from critical"
			// under a warning ceiling) would explain an absence with a level the
			// reader never saw.
			fmt.Fprintf(&b, "  - %s re-rated this from %s to %s, below this repository's minimum severity: %s%s\n",
				inline(r.Expert), r.Finding.Sev(), r.Revised, inline(r.Reason), citedNote(r))
			continue
		}
		fmt.Fprintf(&b, "  - %s: %s%s\n", inline(r.Expert), inline(r.Reason), citedNote(r))
	}

	return b.String()
}

// oneLine collapses text onto a single line.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// inline prepares untrusted text for a bullet: one line, and no raw HTML.
//
// It does not neutralize markdown: "<b>" is escaped, but "[text](https://x)"
// still renders as a live link. Some of this text is written by the change
// under review (a Go compile error quotes source verbatim, so a string literal
// reaches golangci-lint's message), which makes that a link the change authored
// inside a comment under this bot's name. Two call sites wrap it in a code
// span, where backslash escapes would render literally, so escaping belongs at
// each call site by whether the text is prose or code. The README's security
// section records the gap.
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
// A file too large for its full content is windowed rather than refused, and
// windowed files are listed so a reader is still told when most of a file was
// elided.
func windowedNotes(report *Report) string {
	if report.Plan == nil {
		return ""
	}
	return groupByReason(report.Plan.Windowed, "")
}

// maxPathsPerReason bounds how many paths one skip reason lists before the
// rest are counted. A change touching go.mod made every whole-module task too
// big and listed 447 paths, which pushed a review body past GitHub's limit and
// lost the review's inline comments with it.
const maxPathsPerReason = 8

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
		all := byReason[reason]
		shown := all
		if len(shown) > maxPathsPerReason {
			shown = shown[:maxPathsPerReason]
		}
		paths := make([]string, len(shown))
		for i, p := range shown {
			paths[i] = inline(p)
		}
		line := strings.Join(paths, ", ")
		if more := len(all) - len(shown); more > 0 {
			line += fmt.Sprintf(", and %d more (%d in all)", more, len(all))
		}
		fmt.Fprintf(&b, "- %s: %s\n", inline(reason), line)
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

	// Under pull_request scope the ceiling quoted above is what is LEFT, and a
	// reader comparing it against the max_spend in their config would find two
	// different numbers with no explanation between them.
	if fit.Prior > 0 {
		fmt.Fprintf(&b, ">\n> Earlier runs on this pull request were recorded at an estimated "+
			"$%.2f, which `review.budget.scope: pull_request` subtracts, so the ceiling "+
			"quoted here is what remained rather than the whole of "+
			"`review.budget.max_spend`.\n", fit.Prior)
	}

	return b.String()
}

// stageNotice reports a required stage that did not complete.
//
// Ungated, beside budgetNote and for its reason: review.summary chooses
// whether a model's prose is published, and a reader who turned that off has
// not asked to stop being told the findings were never ranked.
func stageNotice(report *Report) string {
	if len(report.Stages) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n> **This review did not complete.**\n>\n")
	for _, st := range report.Stages {
		fmt.Fprintf(&b, "> - %s\n", inline(stageSentence(st)))
	}
	return b.String()
}

// citedNote names the reference entry an expert said decided its verdict.
//
// Rendered wherever a reason is, because the reason is the claim and this is
// what it rests on. A citation naming an entry the expert was not shown never
// reaches here: it is dropped at the verdict, see citation.
func citedNote(r Overruled) string {
	if r.Cited == "" {
		return ""
	}
	return fmt.Sprintf(" (citing `%s`)", inline(r.Cited))
}

func designEvidenceLocation(report *Report, finding Finding) string {
	label := fmt.Sprintf("<code>%s:%d</code>", inline(finding.Path), finding.Line)
	if report.PullRequest == nil || report.PullRequest.SourceBaseURL == "" {
		return label
	}
	base, err := url.Parse(report.PullRequest.SourceBaseURL)
	if err != nil || (base.Scheme != "https" && base.Scheme != "http") || base.Host == "" || base.User != nil {
		return label
	}
	if finding.Line < 1 || path.Clean(finding.Path) != finding.Path || strings.HasPrefix(finding.Path, "/") || strings.HasPrefix(finding.Path, "../") || strings.ContainsAny(finding.Path, "\\\x00") {
		return label
	}
	base.Path = strings.TrimSuffix(base.Path, "/") + "/" + finding.Path
	base.RawPath, base.RawQuery = "", ""
	base.Fragment = fmt.Sprintf("L%d", finding.Line)
	return fmt.Sprintf("<a href=\"%s\">%s</a>", html.EscapeString(base.String()), label)
}
