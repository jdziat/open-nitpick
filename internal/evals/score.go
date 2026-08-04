package evals

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/review"
)

// anchorTolerance is how far from the planted line a finding may sit and still
// count as detecting it.
//
// A few lines of slack is right: models legitimately anchor a nil-dereference
// at the assignment or at the dereference, and both are useful to a reader.
// Beyond that the comment stops pointing at the bug.
const anchorTolerance = 4

// Score is the outcome of one run, in terms a reviewer would care about.
type Score struct {
	RunResult

	// Detected maps a defect's Why to whether some finding reported it.
	Detected map[string]bool

	// Matched counts defects found.
	Matched int

	// Total is how many defects were planted.
	Total int

	// Unmatched are findings that correspond to no planted defect. On a clean
	// fixture every finding is unmatched by definition.
	Unmatched []review.Finding

	// Violations are breaches of invariants the tooling must uphold regardless
	// of which model is used. Any of these is a bug in open-nitpick, not a
	// weakness of the model.
	Violations []string

	// WidestAnchor is the largest number of lines any single finding claimed.
	//
	// Reported because anchorDistance measures from the nearest edge of a span,
	// which means a reviewer can earn a match by gesturing at a whole function
	// rather than pointing at the line. That trade is worth making — a region
	// containing the defect HAS found it — but it has to be visible, or
	// "matched the defect" and "said roughly where to look" become the same
	// number. One means the reviewer anchored precisely.
	WidestAnchor int

	// Severity compares the severities this run assigned against the ones the
	// fixture planted, with no judge involved.
	Severity SeverityScore
}

// Recall is the fraction of planted defects found.
func (s Score) Recall() float64 {
	if s.Total == 0 {
		return 1
	}
	return float64(s.Matched) / float64(s.Total)
}

// Findings returns the published findings.
func (s Score) Findings() []review.Finding {
	if s.Report == nil {
		return nil
	}
	return s.Report.Findings
}

// ScoreRun evaluates one run against its fixture.
func ScoreRun(r RunResult, f Fixture) Score {
	s := Score{
		RunResult: r,
		Detected:  map[string]bool{},
		Total:     len(f.Defects),
	}

	if r.Err != nil {
		s.Violations = append(s.Violations, fmt.Sprintf("review failed: %v", r.Err))
		return s
	}
	if r.Report == nil {
		s.Violations = append(s.Violations, "review returned no report")
		return s
	}

	// A review that lost batches cannot be scored for recall: absence of a
	// finding would mean nothing.
	if !r.Report.Complete() {
		s.Violations = append(s.Violations,
			fmt.Sprintf("review incomplete, %d file(s) unreviewed: %s",
				len(r.Report.Incomplete), strings.Join(r.Report.Incomplete, ", ")))
	}

	for _, finding := range r.Report.Findings {
		s.WidestAnchor = max(s.WidestAnchor, spanLength(finding))
	}

	s.Severity = ScoreSeverity(f, r.Report.Findings)

	for _, defect := range f.Defects {
		for _, finding := range r.Report.Findings {
			if matches(finding, defect) {
				s.Detected[defect.Why] = true
				s.Matched++
				break
			}
		}
		if !s.Detected[defect.Why] {
			s.Detected[defect.Why] = false
		}
	}

	// Anything not explaining a planted defect is noise on this corpus.
	for _, finding := range r.Report.Findings {
		if !explainsAny(finding, f.Defects) {
			s.Unmatched = append(s.Unmatched, finding)
		}
	}

	s.Violations = append(s.Violations, checkInvariants(r)...)

	return s
}

// The objective severity vocabulary. It is deliberately the judge's own
// (accurate | inflated | understated): the two measurements are printed side by
// side and disagree, and a reader comparing them should not have to translate
// between two vocabularies first.
const (
	SevAccurate    = "accurate"
	SevInflated    = "inflated"
	SevUnderstated = "understated"
)

// NoCrossToolSeverityScore is printed wherever a reader might go looking for a
// cross-tool severity accuracy figure, because one used to be there.
//
// A BANDED cross-tool severity accuracy number (B-ACC: both sides reduced to
// blocking/medium/low, then compared) was published from this package and is
// WITHDRAWN. It was not a mis-tuned constant, it was the wrong instrument, and
// two measurements say so rather than two opinions:
//
//   - IT IS MAXIMISED BY THE WORST PRODUCTION BEHAVIOUR. This corpus bands 12
//     blocking, 1 medium, 1 low, and every plant Incumbent locates is blocking.
//     Reconstructed over AllFixtures, a reviewer that stamps one blocking word on
//     every finding — always "critical", or always "error" — banded 12 accurate
//     and 2 inflated of 14 located, against the incumbent's 6 accurate of 10; and
//     the same strategy allowed to choose WHAT it reports — stay silent unless
//     the defect is already blocking, then call it critical — banded 12 of 12,
//     a PERFECT record no calibrated reviewer can beat. A number optimised by
//     stamping "critical" on everything would, if anyone optimised it, produce
//     exactly the review bot this project exists not to be.
//
//     THE FIGURE THIS COMMENT USED TO QUOTE — "always critical and always error
//     both scored a perfect 10 of 10" — WAS WRONG, and wrong in the direction
//     that made the argument easier. It scored those two strategies over the ten
//     plants the INCUMBENT located rather than over what they actually report,
//     so it credited them with a denominator they had not earned and hid the two
//     inflations they do commit. The conclusion survives the correction; the
//     number did not, and a retraction argued from an unreproducible measurement
//     is the same defect one level up. The live receipt is the
//     selective-reporting row in degenerateReviewers(), which is scored on every
//     run rather than remembered here.
//
//   - IT IS BLIND TO THE DEFECT IT WAS WRITTEN FOR. The parser bug behind the
//     PREVIOUS retraction (crSeverity demoting Incumbent's "critical" to our
//     "error") does not move it at all: buggy parser 6/0/4, fixed parser 6/0/4.
//     The banded column cannot see the bug it was the remedy for, and by
//     construction cannot see it recur.
//
// It was also a free parameter, and here too the figure first published was
// wrong. crSeverity records Incumbent's "major" at warning; recording it at
// error instead, with Incumbent's bytes byte-for-byte unchanged, moves the
// banded figure from 0.600 to 1.000 over AllFixtures (6/0/4 to 10/0/0) and from
// 0.714 to 1.000 over the tuning fixtures (5/0/2 to 7/0/0). The numbers quoted
// before — "0.62 to 0.88" — reproduce from nothing in this tree; the real swing
// is larger and ends at a perfect record, so the correction strengthens the
// argument it was made for, which is exactly why it had to be checked rather
// than reused. Every "major" this corpus credits sits on a plant we rate error
// or critical and none on a plant of warning, so no observation here can choose
// between the two placements.
// TestMajorIsAFreeParameterSoNoCrossToolScoreIsOffered keeps the full-resolution
// half of that measured rather than remembered; the banded half is quoted here
// because the instrument that produced it is deleted.
//
// What replaces it is DESCRIPTION, not a score: SeverityUsage reports which
// severity words each reviewer actually used, against which planted levels, so
// a reader can see a calibration difference without a single number pretending
// the two vocabularies are commensurable. Our own models keep FULL-RESOLUTION
// severity scoring against each other — that comparison is between matching
// vocabularies and nothing here touches it.
//
// Re-tuning band boundaries is not the remedy and is not open: three consecutive
// attempts to make cross-tool severity fair (demote at parse time, then band at
// comparison time, then re-tune the bands) each moved a number our way without
// new evidence. The parser fix stays — recording that Incumbent said "critical"
// is correct on its own merits and preserves evidence — it simply does not
// license a comparison.
const NoCrossToolSeverityScore = "NO CROSS-TOOL SEVERITY ACCURACY IS OFFERED, BY CONSTRUCTION. " +
	"Our five levels and " + IncumbentModel + "'s ~three are different RESOLUTIONS, and every " +
	"reduction that makes them comparable is maximised by rating everything blocking: on this corpus " +
	"(12 blocking plants, 1 medium, 1 low) 'always critical' and 'always error' banded 12 accurate of " +
	"14 against the incumbent's 6 of 10, and a reviewer that also CHOOSES to mention only the " +
	"already-blocking defects banded a perfect 12 of 12. The banded column also could not see the parser bug " +
	"that caused the previous retraction — buggy and fixed parsers scored identically on it. So no such " +
	"number is published, and the O-* cells on a row that does not publish our five levels are printed " +
	"n/a rather than filled and annotated: telling a reader not to compare two numbers printed in one " +
	"sorted ranking is the mitigation the previous retraction already found insufficient. " +
	"What IS published for such a row is the severity VOCABULARY it used against each planted level " +
	"(see the SEVERITY VOCABULARY block): a description, which a reader may compare by eye and may not " +
	"reduce to one figure. O-* remains a real score BETWEEN ROWS THAT PUBLISH OUR FIVE LEVELS and is " +
	"what our own prompt is tuned on."

// SeverityUsage tabulates which severity words a reviewer actually assigned to
// the defects it located, against the level each defect was planted at.
//
// It is the DESCRIPTION that replaces the withdrawn cross-tool score, and it is
// deliberately not reducible to one number. A reader comparing two vocabularies
// gets to see, for example, that one reviewer answered "critical" to plants of
// critical AND to plants of error while another split them — and gets to decide
// for themselves what that is worth, which is exactly the judgement a single
// accuracy figure was making silently on their behalf and getting wrong.
//
// Keyed planted severity -> assigned severity -> count. Both keys are recorded
// as the reviewer and the fixture spelled them, not coarsened: coarsening is the
// step that was withdrawn.
type SeverityUsage map[config.Severity]map[config.Severity]int

// Add records one graded defect.
func (u SeverityUsage) Add(planted, assigned config.Severity) {
	row, ok := u[planted]
	if !ok {
		row = map[config.Severity]int{}
		u[planted] = row
	}
	row[assigned]++
}

// Merge folds another tabulation in, so a per-run description can be summed
// across runs and fixtures without the caller reimplementing the nesting.
func (u SeverityUsage) Merge(other SeverityUsage) {
	for planted, row := range other {
		dst, ok := u[planted]
		if !ok {
			dst = map[config.Severity]int{}
			u[planted] = dst
		}
		for assigned, n := range row {
			dst[assigned] += n
		}
	}
}

// severityOrder is the order severities are printed in, most severe first, so
// two runs of the same corpus render identically. Map iteration order is not
// stable and a description a reader diffs between runs has to be.
var severityOrder = []config.Severity{
	config.SeverityCritical, config.SeverityError, config.SeverityWarning,
	config.SeverityInfo, config.SeverityNit,
}

// Lines renders the tabulation, one line per planted level.
//
// Levels nobody located are absent rather than shown as zero: this describes
// what a reviewer SAID about defects it found, and a defect it never mentioned
// is a miss, which RECALL already reports. Printing it here as an empty row
// would invite reading a miss as a severity result.
func (u SeverityUsage) Lines() []string {
	var out []string

	for _, planted := range orderedSeverities(u) {
		row := u[planted]

		located := 0
		var parts []string
		for _, assigned := range orderedSeverities(row) {
			located += row[assigned]
			parts = append(parts, fmt.Sprintf("%s x%d", assigned, row[assigned]))
		}

		out = append(out, fmt.Sprintf("  planted %-8s (%d located): %s",
			planted, located, strings.Join(parts, ", ")))
	}

	return out
}

// orderedSeverities lists a map's severity keys most severe first, with any key
// outside our five appended in sorted order rather than dropped — a foreign
// reviewer's vocabulary is the thing being described, so an unrecognized word
// has to survive to the page.
func orderedSeverities[V any](m map[config.Severity]V) []config.Severity {
	var out []config.Severity
	seen := map[config.Severity]bool{}

	for _, s := range severityOrder {
		if _, ok := m[s]; ok {
			out = append(out, s)
			seen[s] = true
		}
	}

	var rest []string
	for s := range m {
		if !seen[s] {
			rest = append(rest, string(s))
		}
	}
	sort.Strings(rest)

	for _, s := range rest {
		out = append(out, config.Severity(s))
	}
	return out
}

// SeverityCall is one LOCATED defect's planted severity measured against the
// severity of the finding credited with reporting it.
type SeverityCall struct {
	// FindingIndex is the position of that finding in the list handed to
	// ScoreSeverity, so a diagnostic dump can attach this verdict to the exact
	// line the tables counted rather than re-deriving the association and
	// disagreeing with them.
	FindingIndex int

	Finding review.Finding
	Defect  Defect

	// Verdict is one of SevAccurate, SevInflated, SevUnderstated, at our full
	// five-level resolution. Meaningful only between reviewers that publish
	// those five levels; see NoCrossToolSeverityScore for why there is no
	// coarser companion verdict any more.
	Verdict string
}

// SeverityScore compares assigned severities against the planted ones.
//
// It exists because severity correctness used to be an LLM opinion and nothing
// else: every fixture Defect declares WantSeverity and nothing outside
// fixtures.go read it. That opinion is measurably blind in one direction — the
// judge reported Incumbent understating NOTHING, on a corpus whose own ground
// truth says it understated defects it had located. A prompt tuned to reduce
// inflation against a scorer that cannot see under-claiming optimizes toward
// saying less and calls it progress.
//
// The comparison is at our FULL five-level resolution and is a score only
// between reviewers that publish those five levels. It used to be accompanied
// by a coarsened cross-tool figure; that figure is withdrawn and the reasons are
// measurements, not taste — see NoCrossToolSeverityScore. Usage is what a
// cross-vocabulary reader gets instead, and it is a description.
//
// WantSeverity is the TARGET, not a floor: over-claiming above the planted
// level is precisely the failure being tuned away, so it is counted rather than
// tolerated. Defect.WantSeverity documents the same contract, and the two must
// not be allowed to drift — a fixture authored against a floor reading silently
// corrupts the inflation column the tuning is aimed at.
type SeverityScore struct {
	Accurate    int
	Inflated    int
	Understated int

	// Calls is one entry per graded defect, so a report can name the finding
	// that was inflated instead of only how many were. Aggregate counts are
	// what a table shows; fixing the prompt needs the finding itself.
	Calls []SeverityCall
}

// Graded is how many planted defects were compared against a reported severity.
// It equals Score.Matched by construction, so RECALL is the denominator the
// severity columns are read against.
func (s SeverityScore) Graded() int { return s.Accurate + s.Inflated + s.Understated }

// Usage tabulates what this reviewer called each planted level. It is the
// cross-vocabulary description; see SeverityUsage.
func (s SeverityScore) Usage() SeverityUsage {
	out := SeverityUsage{}
	for _, c := range s.Calls {
		out.Add(c.Defect.WantSeverity, c.Finding.Sev())
	}
	return out
}

// ScoreSeverity grades the severity of every planted defect a review located.
//
// The unit is the DEFECT, not the finding, and that is the whole shape of this
// function. Grading per finding was wrong three ways at once, all of them
// measured on the shipped Incumbent cache:
//
//   - It counted a defect once per finding that happened to match it, so a
//     reviewer restating one correct call four ways earned four times the
//     accuracy of one that said it once. Verbosity is not severity honesty.
//   - matches() is calibrated for DETECTION, where a loose keyword is harmless
//     because ScoreRun stops at the first hit. Used per finding it graded
//     comments about entirely different bugs against a plant's WantSeverity:
//     Incumbent's "Propagate archive failures" — about tar's ignored exit
//     status — was counted as understating the command injection.
//   - Every comment describing these columns says "defects", and the numbers
//     said findings. Graded (8) exceeded the defects located (7), so the legend
//     printed under the table contradicted the column beside it.
//
// Defects no finding reports are skipped rather than counted: the corpus says
// nothing about the severity of a bug the reviewer never mentioned, and a miss
// is already reported as a miss.
func ScoreSeverity(f Fixture, findings []review.Finding) SeverityScore {
	var out SeverityScore

	for _, defect := range f.Defects {
		idx, ok := reportingFinding(findings, defect)
		if !ok {
			continue
		}

		call := SeverityCall{
			FindingIndex: idx,
			Finding:      findings[idx],
			Defect:       defect,
			Verdict:      severityVerdict(findings[idx].Sev(), defect.WantSeverity),
		}

		switch call.Verdict {
		case SevInflated:
			out.Inflated++
		case SevUnderstated:
			out.Understated++
		default:
			out.Accurate++
		}

		out.Calls = append(out.Calls, call)
	}

	return out
}

// severityVerdict compares an assigned severity against the planted one at our
// full five-level resolution.
//
// Both sides are put through Normalize before they are ranked. THE BUG THIS
// FIXES: Rank places "none" ABOVE critical, so that `fail_on: none` matches
// nothing — which meant a finding somehow carrying "none", the word for the
// ABSENCE of a severity, compared as the most severe value there is and scored
// INFLATED against a planted critical. The withdrawn banded verdict went through
// Normalize and answered UNDERSTATED for the identical input, so the two
// verdicts printed side by side on one row disagreed about direction, and only
// one of them could be right. Normalize is the single place that already decides
// what an unusable severity degrades to, and it degrades to info. checkInvariants
// reports such a finding separately, which is where that belongs; this function's
// job is only to be self-consistent about it.
func severityVerdict(got, want config.Severity) string {
	g, _ := got.Normalize()
	w, _ := want.Normalize()

	switch {
	case g.Rank() > w.Rank():
		return SevInflated
	case g.Rank() < w.Rank():
		return SevUnderstated
	default:
		return SevAccurate
	}
}

// reportingFinding picks which of a review's findings is credited with
// reporting a defect, returning its index.
//
// Several findings can match one plant — a reviewer may raise the same problem
// twice, or fold two nearby problems into one comment. The credited one is the
// MOST SEVERE of them, ties going to the first in report order.
//
// Most severe, because that is the severity the review actually HAS: `fail_on`
// gates on the worst thing said, so a reviewer that calls a defect critical
// anywhere has made a critical claim about it whatever else it also said. This
// grades a reviewer on the effect its review has rather than on its most
// flattering sentence.
//
// IT REPLACES A NEAREST-TO-THE-PLANT RULE THAT PAID FOR HEDGING, which is the
// bug. Crediting the closest severity extended a "benefit of the doubt" that
// only a multi-comment reviewer could collect: on a planted error, one comment
// saying "warning" scored UNDERSTATED, and adding a second comment saying
// "critical" — a strictly worse review, saying two different things about one
// defect — scored ACCURATE, because a band tie-break preferred the in-band
// comment. Taken to its limit the same rule let a reviewer emit one comment at
// every severity on every line and score PERFECT severity accuracy, since one of
// the five was always exact. That is the verbosity-rewards-accuracy defect the
// per-defect design already killed once, reintroduced in the tie-break; this
// function's own doc comment said so and the code did the opposite.
// TestSeverityIsNotImprovedByHedging and the degenerate-strategy table pin it.
//
// Under this rule an added comment can only move a verdict in ONE direction:
// up. A quieter one never wins, so hedging downward is free of both reward and
// penalty, and a louder one raises the claim — improving the verdict only if it
// names the planted level, which is not gaming but being right, and inflating it
// otherwise. That asymmetry is what defeats hedging in bulk: a reviewer that
// covers itself by answering several severities at once is credited with the
// loudest of them, so it is inflating on every plant below that, and answering
// all five levels is exactly as good as answering only the highest.
//
// PRINT ORDER CANNOT CHANGE A VERDICT, and unlike the previous rule that is now
// true rather than claimed: max is commutative, so reordering a review cannot
// move the credited SEVERITY. The nearest-rule could not say this — around a
// planted warning, [error, info] scored inflated and [info, error] understated
// on the same review.
//
// Ties are broken on the RAW SPELLING, not on report order, and the difference
// is not cosmetic. The comment here used to say ties were between findings
// carrying the same severity so order decided only which comment a diagnostic
// NAMED. Ties are on NORMALIZED rank, and the credited finding's raw Sev() is
// what SeverityUsage records — which is PUBLISHED, as the description that
// replaced the withdrawn cross-tool score. Two findings on one planted-info
// defect spelled "info" and "P1" both normalize to info and tie; [info, P1]
// published `planted info: info x1` and [P1, info] published `planted info:
// p1 x1`. Same review, same verdict, two different published descriptions of the
// reviewer's vocabulary. A recognized spelling wins, then the lexicographically
// smaller one, so the block is a function of the SET of findings.
//
// The benefit of the doubt deliberately does NOT extend across plants, which is
// what the earlier per-finding form did. multi-defect plants a critical
// traversal and an error descriptor leak on the same line; letting one finding
// choose which plant it was graded against made every severity from error to
// critical score accurate, so under-rating the traversal was unmeasurable and a
// reviewer could buy immunity from the column being tuned by merging comments.
// Each defect is graded against its own planted severity, so a merged comment
// rated below the worst of them is reported as understating it.
func reportingFinding(findings []review.Finding, d Defect) (int, bool) {
	var (
		best  int
		loud  config.Severity
		raw   string
		known bool
		found bool
	)

	for i, f := range findings {
		if !matches(f, d) {
			continue
		}

		// Normalize before ranking for the reason severityVerdict does: "none"
		// outranks critical so that `fail_on: none` matches nothing, and it must
		// not therefore win the credit as the loudest claim in the review.
		sev, ok := f.Sev().Normalize()
		spelling := string(f.Sev())

		switch {
		case !found, sev.Rank() > loud.Rank():
		case sev.Rank() < loud.Rank():
			continue
		// Equal rank: decide on the spelling so that the published vocabulary
		// block does not depend on the order the review happened to arrive in.
		case ok && !known:
		case ok == known && spelling < raw:
		default:
			continue
		}

		best, loud, raw, known, found = i, sev, spelling, ok, true
	}

	return best, found
}

// matches reports whether a finding plausibly reports a defect: near the right
// line, in the right file, and describing the right problem.
func matches(f review.Finding, d Defect) bool {
	if f.Path != d.Path {
		return false
	}

	if anchorDistance(f, d.Line) > anchorTolerance {
		return false
	}

	return mentionsAny(f, d.Keywords)
}

// anchorDistance is how far a finding's anchor sits from a line, measured from
// the NEAREST point of a multi-line anchor rather than its start.
//
// open-nitpick anchors to one line, so for its own findings this is the plain
// distance it always was. It matters for reviewers that report a region: taking
// the start of "lines 11-12" and calling a defect on line 12 one line away is a
// coincidence that only holds for short spans, and the same reviewer anchored
// the same defect at 7 on one run and 11-12 on the next — start-only scoring
// turned that into the difference between a hit and a miss.
//
// The tolerance still applies OUTSIDE the span, so a wide anchor buys no extra
// slack at its edges. It does mean a reviewer could earn credit by reporting
// "somewhere in this 200-line function", which is why spanLength is reported
// separately: vagueness should be visible rather than silently rewarded.
func anchorDistance(f review.Finding, line int) int {
	best := spanDistance(review.LineSpan{Line: f.Line, EndLine: f.EndLine}, line)

	// A finding that names several regions is judged by its CLOSEST one. It
	// reported them all, so measuring it by the first is arbitrary: Incumbent
	// put the SQL injection's primary anchor on the import block its fix would
	// edit and named the interpolation under "Also applies to".
	for _, s := range f.AlsoAt {
		best = min(best, spanDistance(s, line))
	}

	return best
}

// spanDistance is how far a line sits outside one span, zero when inside it.
func spanDistance(s review.LineSpan, line int) int {
	lo, hi := s.Line, s.EndLine
	if hi < lo {
		hi = lo
	}

	switch {
	case line < lo:
		return lo - line
	case line > hi:
		return line - hi
	default:
		return 0
	}
}

// spanLength is how many lines a finding's anchor covers. One for the ordinary
// single-line anchor.
//
// It measures the widest SINGLE region rather than the hull of them all: a
// finding naming lines 3-6 and 15-18 has claimed two tight regions, not one
// sixteen-line smear, and reporting the hull would invent vagueness it does not
// have.
func spanLength(f review.Finding) int {
	widest := spanLines(review.LineSpan{Line: f.Line, EndLine: f.EndLine})
	for _, s := range f.AlsoAt {
		widest = max(widest, spanLines(s))
	}
	return widest
}

func spanLines(s review.LineSpan) int {
	if s.EndLine <= s.Line {
		return 1
	}
	return s.EndLine - s.Line + 1
}

// explainsAny reports whether a finding describes any planted defect, ignoring
// line position. Used for noise counting so a correctly-identified bug anchored
// slightly too far away is not also counted as a false positive.
func explainsAny(f review.Finding, defects []Defect) bool {
	for _, d := range defects {
		if f.Path == d.Path && mentionsAny(f, d.Keywords) {
			return true
		}
	}
	return false
}

// mentionsAny reports whether a finding's text contains any keyword.
func mentionsAny(f review.Finding, keywords []string) bool {
	haystack := strings.ToLower(f.Title + " " + f.Rationale + " " + f.Category)
	for _, kw := range keywords {
		if strings.Contains(haystack, strings.ToLower(kw)) {
			return true
		}
	}
	return false
}

// checkInvariants verifies properties the tooling must guarantee for ANY model.
//
// These are the fixes from the security and correctness pass, restated as
// assertions against real output. A violation here is an open-nitpick bug: no
// model behavior should be able to produce it.
func checkInvariants(r RunResult) []string {
	var out []string

	if r.Report == nil {
		return out
	}

	for _, f := range r.Report.Findings {
		// Severity must be a real level. "none" would outrank critical, and an
		// unknown value would gate unpredictably.
		sev := config.Severity(f.Severity)
		if !sev.IsFinding() {
			out = append(out, fmt.Sprintf("finding %q has severity %q, which is not a finding severity",
				f.Title, f.Severity))
		}

		if strings.TrimSpace(f.Path) == "" || f.Line <= 0 {
			out = append(out, fmt.Sprintf("finding %q has no usable anchor (%s:%d)", f.Title, f.Path, f.Line))
		}
	}

	if r.Review == nil {
		return out
	}

	for _, c := range r.Review.Comments {
		// Every published comment must be applicable code or clearly not a
		// one-click suggestion. A multi-line or prose suggestion rendered as
		// applicable corrupts the file when clicked.
		if idx := strings.Index(c.Body, "```suggestion\n"); idx >= 0 {
			rest := c.Body[idx+len("```suggestion\n"):]
			block, _, _ := strings.Cut(rest, "\n```")

			if strings.Contains(strings.TrimRight(block, "\n"), "\n") {
				out = append(out, fmt.Sprintf("%s:%d publishes a MULTI-LINE applicable suggestion; "+
					"GitHub replaces only the anchored line, so applying it corrupts the file", c.Path, c.Line))
			}
			if !looksLikeCodeForScoring(block) {
				out = append(out, fmt.Sprintf("%s:%d publishes prose as an applicable suggestion: %q",
					c.Path, c.Line, truncate(block, 60)))
			}
		}

		if c.Side != "RIGHT" && c.Side != "LEFT" {
			out = append(out, fmt.Sprintf("%s:%d has side %q", c.Path, c.Line, c.Side))
		}
	}

	return out
}

// looksLikeCodeForScoring mirrors the renderer's heuristic. It is duplicated
// deliberately: if the renderer's own helper were reused, a bug in it would
// make this check agree with the bug.
func looksLikeCodeForScoring(s string) bool {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return false
	}

	if strings.ContainsAny(trimmed, "(){}[];=<>:.\"'`*&|!/\\%+_") {
		return true
	}

	first, _, _ := strings.Cut(trimmed, " ")
	switch strings.ToLower(first) {
	case "return", "break", "continue", "pass", "raise", "throw", "defer", "go", "del":
		return true
	}
	return false
}

func truncate(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// Summary aggregates scores across runs for reporting.
type Summary struct {
	Model   string
	Fixture string

	Runs       int
	Failed     int
	Matched    int
	Total      int
	NoiseTotal int

	// Severity totals across the runs, measured against the planted
	// WantSeverity.
	//
	// Summed rather than averaged because they are graded per LOCATED DEFECT,
	// so their total is exactly Matched — the numerator of the RECALL cell
	// printed beside them. That is the divisor, and it is on the row. The
	// earlier justification here claimed Runs was printed beside them; it never
	// was, and a bare sum with no divisor anywhere on the line is the artifact
	// that comment existed to deny.
	SevAccurate    int
	SevInflated    int
	SevUnderstated int

	// SevUsage is what this model called each planted level, summed over the
	// runs. Printed as a description beside the table rather than as a column:
	// see SeverityUsage and NoCrossToolSeverityScore.
	SevUsage SeverityUsage

	Violations []string

	// WidestAnchor is the widest single region any finding claimed across these
	// runs, printed as ANCHOR. It is part of the detection reading, not a
	// diagnostic: see CorpusTally.WidestAnchor.
	WidestAnchor int

	// FindingCounts is the number of findings produced per run, which is how
	// run-to-run stability is judged.
	FindingCounts []int
}

// Stable reports whether every run produced the same number of findings, and
// whether that question has an answer for this row.
//
// Identical inputs at temperature 0 should produce identical output. When they
// do not, a severity gate becomes a coin flip.
//
// defined is false when no run produced a finding, and that second return value
// is the fix for a defect. STABLE is printed as "yes" or "NO [3 5 4]" and a
// reader takes yes as better, so it reads as a verdict about the reviewer even
// though DescriptiveColumns classifies it as a description. Under the old
// signature five runs of SILENCE returned true — counts [0 0 0 0 0] — and so did
// every constant degenerate strategy, while a wobbly but correct reviewer
// ([1 0 1 0 1]) returned false. The column's best value went to saying nothing,
// which is the failure this package exists to catch, and the descriptive
// classification is what exempted it from the degenerate-reviewer table. A
// reviewer that never spoke has not been observed to be consistent; it has not
// been observed at all.
func (s Summary) Stable() (stable, defined bool) {
	if len(s.FindingCounts) < 2 {
		return true, false
	}

	spoke := false
	for _, n := range s.FindingCounts {
		if n > 0 {
			spoke = true
		}
		if n != s.FindingCounts[0] {
			return false, true
		}
	}
	return true, spoke
}

// Recall across all runs.
func (s Summary) Recall() float64 {
	if s.Total == 0 {
		return 1
	}
	return float64(s.Matched) / float64(s.Total)
}

// Summarize folds per-run scores into one row.
func Summarize(model, fixture string, scores []Score) Summary {
	out := Summary{Model: model, Fixture: fixture, Runs: len(scores), SevUsage: SeverityUsage{}}

	for _, s := range scores {
		if s.Err != nil {
			out.Failed++
		}
		out.Matched += s.Matched
		out.Total += s.Total
		out.NoiseTotal += len(s.Unmatched)
		out.SevAccurate += s.Severity.Accurate
		out.SevInflated += s.Severity.Inflated
		out.SevUnderstated += s.Severity.Understated
		out.SevUsage.Merge(s.Severity.Usage())
		out.Violations = append(out.Violations, s.Violations...)
		out.WidestAnchor = max(out.WidestAnchor, s.WidestAnchor)
		out.FindingCounts = append(out.FindingCounts, len(s.Findings()))
	}

	return out
}

// CorpusTally is one reviewer's MODEL-FREE result over a corpus: what it found,
// what it invented, and how it rated what it found.
//
// It exists so that the published columns can be defined once, in terms a test
// can compute for a synthetic reviewer. PublishedMetrics reads only this, which
// is what lets the degenerate-strategy table score "a reviewer that answers
// critical to everything" without a model, a judge or a network.
type CorpusTally struct {
	// Samples is how many reviews were folded in — the denominator the tables
	// divide their count columns by.
	Samples int

	Matched  int
	Planted  int
	Noise    int
	Severity SeverityScore

	// WidestAnchor is the widest single region any finding in the corpus
	// claimed, in lines.
	//
	// It is part of the detection metric rather than a diagnostic because
	// without it RECALL and NOISE are BOTH maximised by one finding per file
	// spanning the whole file, titled with every keyword in it: anchorDistance
	// is zero anywhere inside a span and explainsAny ignores line position, so
	// such a blob is credited with every plant in the file and is noise for
	// none. It scored RECALL 1.000 NOISE 0 — an exact tie with a perfectly
	// calibrated reviewer — while pointing at nothing more useful than "there is
	// a nil deref, an SQL injection and a hardcoded secret somewhere in this
	// file". Score.WidestAnchor was written for exactly that trade and was
	// reported only in a t.Logf, so no published column could see it.
	//
	// The MAX rather than the mean: one blob anywhere in the corpus is the
	// behaviour being caught, and a mean over precise findings hides it.
	WidestAnchor int
}

// TallyScores folds scored runs into a CorpusTally.
func TallyScores(scores []Score) CorpusTally {
	out := CorpusTally{Samples: len(scores)}

	for _, s := range scores {
		out.Matched += s.Matched
		out.Planted += s.Total
		out.Noise += len(s.Unmatched)
		out.WidestAnchor = max(out.WidestAnchor, s.WidestAnchor)
		out.Severity.Accurate += s.Severity.Accurate
		out.Severity.Inflated += s.Severity.Inflated
		out.Severity.Understated += s.Severity.Understated
		out.Severity.Calls = append(out.Severity.Calls, s.Severity.Calls...)
	}

	return out
}

// PublishedMetric is one READING an eval report publishes as a score — the set
// of columns that must be read together to mean anything.
//
// It is a group rather than a column because the failure this type exists to
// prevent was a group of one. B-ACC/B-INFL/B-UNDER were printed as a triple and
// quoted as a single headline, and a reviewer that stamped one blocking word on
// every finding scored the same perfect triple as a perfectly calibrated one.
// Asking "what maximises this?" of each column separately would not have caught
// it either: a reviewer that never inflates anything is trivially produced by
// never claiming anything, so O-INFL alone is maximised by silence and is only a
// score BESIDE O-UNDER and O-ACC. The unit that can be asked the question
// honestly is the group.
//
// Every model-free score any report prints must be registered here. That is what
// makes the guard structural rather than a one-off: adding a column means adding
// it to a metric, and the degenerate-strategy table then scores it against
// reviewers whose behaviour nobody would ship — always critical, always nit, one
// comment per line, silence — and fails if any of them can max it out. A metric
// they can max out does not measure what its name claims and must not be
// published. See TestNoDegenerateReviewerCanMaxOutAPublishedMetric.
type PublishedMetric struct {
	// Name is how the metric is referred to in a failure message.
	Name string

	// Renderings are the COMPLETE ways this metric may be printed, each an
	// unordered set of column headings. A header that carries any column of the
	// metric must carry every column of one whole rendering.
	//
	// Two renderings exist because the same reading is printed two ways: the
	// ground-truth table spends one cell on "SEV a/i/u" with RECALL beside it as
	// the denominator, and the judged tables spread the same triple over
	// O-ACC/O-INFL/O-UNDER with O-COV as the denominator.
	//
	// THE BUG THIS SHAPE FIXES: the type's whole justification is that a single
	// column cannot be asked "what maximises this?" honestly — O-INFL alone is
	// maximised by silence — and nothing enforced the grouping at the table
	// level. Deleting O-ACC and O-UNDER from VariantTableHeader, leaving O-INFL
	// published by itself, passed every guard in this package including the one
	// named after the failure. Columns were checked for being DECLARED somewhere
	// and never for being printed TOGETHER.
	Renderings [][]string

	// Doc says what the metric claims to measure. A degenerate strategy that
	// maxes it out is a counterexample to exactly this sentence.
	Doc string

	// Score returns the metric's components ORIENTED SO THAT HIGHER IS BETTER,
	// so "maxed out" is componentwise >= without each caller re-deriving which
	// way each column points. ok is false when the corpus gives the metric
	// nothing to measure — which is the answer silence must get, rather than a
	// perfect zero in the columns that count mistakes.
	Score func(CorpusTally) (components []float64, ok bool)
}

// Columns is every heading this metric may appear under, across all renderings.
func (m PublishedMetric) Columns() []string {
	var out []string
	seen := map[string]bool{}
	for _, r := range m.Renderings {
		for _, c := range r {
			if !seen[c] {
				seen[c] = true
				out = append(out, c)
			}
		}
	}
	return out
}

// Label names the metric's columns for a failure message.
func (m PublishedMetric) Label() string { return strings.Join(m.Columns(), "/") }

// Maxes reports whether a reviewer's tally is at least as good as a reference
// tally on EVERY component of the metric.
//
// The reference is a reviewer that is actually correct, so this answers "did
// this strategy do as well as being right?" — which is the question the withdrawn
// banded column failed. It is not "did it win": a degenerate strategy that merely
// TIES a calibrated reviewer has already shown the metric cannot tell them apart,
// and a tie is exactly what "always critical" scored on the banded column.
func (m PublishedMetric) Maxes(strategy, reference CorpusTally) (bool, bool) {
	got, ok := m.Score(strategy)
	if !ok {
		return false, false
	}

	want, ok := m.Score(reference)
	if !ok || len(got) != len(want) {
		return false, false
	}

	for i := range got {
		if got[i] < want[i] {
			return false, true
		}
	}
	return true, true
}

// PublishedMetrics is every model-free score the eval reports publish.
//
// The judge's own columns (PRECISION, GRADE, J-INFL, MISCLASS, TONE-OFF, SIGNAL)
// are absent because they are an LLM's opinion and cannot be computed for a
// synthetic reviewer. They are not thereby exempt from the question this file
// asks; they are exempt from being answered by this mechanism, and the judged
// tables print them beside these.
func PublishedMetrics() []PublishedMetric {
	return []PublishedMetric{
		{
			Name:       "detection",
			Renderings: [][]string{{"RECALL", "NOISE", "ANCHOR"}},
			Doc: "how much of what was planted the reviewer found, how much it invented, and how " +
				"precisely it said where to look",
			Score: func(t CorpusTally) ([]float64, bool) {
				if t.Planted == 0 || t.Samples == 0 {
					return nil, false
				}
				// Noise is negated so that higher is better in every position;
				// per sample, because that is how the tables print it and
				// because a corpus of more fixtures would otherwise look noisier.
				//
				// The anchor width is negated for the same reason and NOT
				// divided: it is a worst case, not a rate. See
				// CorpusTally.WidestAnchor for the strategy it exists to catch.
				return []float64{
					float64(t.Matched) / float64(t.Planted),
					-float64(t.Noise) / float64(t.Samples),
					-float64(t.WidestAnchor),
				}, true
			},
		},
		{
			Name: "objective severity",
			Renderings: [][]string{
				{"O-ACC", "O-INFL", "O-UNDER", "O-COV"},
				{"SEV", "RECALL"},
			},
			Doc: "whether the reviewer rated a located defect at the level the corpus plants it at, " +
				"at our full five-level resolution, and over how much of the corpus that reading is " +
				"computed",
			Score: func(t CorpusTally) ([]float64, bool) {
				// Undefined, not perfect, when nothing was graded. A reviewer
				// that says nothing inflates nothing and understates nothing,
				// and printing 0.00 in those two columns for it is precisely how
				// silence gets read as calibration.
				graded := t.Severity.Graded()
				if graded == 0 || t.Planted == 0 {
					return nil, false
				}
				return []float64{
					float64(t.Severity.Accurate) / float64(graded),
					-float64(t.Severity.Inflated) / float64(graded),
					-float64(t.Severity.Understated) / float64(graded),

					// COVERAGE, and the reason the triple above is not a score
					// on its own. Severity is graded only over what the reviewer
					// LOCATED, so a reviewer that reports only the defects it is
					// already certain about is scored only on those: reporting
					// nothing except the plants this corpus rates critical, and
					// calling them critical, scored O-ACC 1.000 with no
					// inflation and no understatement — an exact tie with a
					// perfectly calibrated reviewer over the whole corpus, on
					// four of its fourteen plants. Selective silence is not
					// calibration, and the previous shape of this metric could
					// not tell the two apart. The denominator makes the
					// selection visible: 4/14 against 14/14.
					float64(graded) / float64(t.Planted),
				}, true
			},
		},
	}
}

// PublishesOurSeverityLevels reports whether a contender's severity words are
// drawn from the five levels this project defines.
//
// It is false for the incumbent, and the report BLANKS its O-* cells rather than
// filling them. The columns are a comparison against WantSeverity at our
// resolution; a reviewer publishing roughly three levels cannot score accurate
// on an error plant without over-claiming on a critical one, so the number it
// gets measures the vocabulary gap and nothing about review quality.
//
// THE BUG: the banded cross-tool triple was withdrawn and the FULL-RESOLUTION
// triple was left printed on the foreign row, in the same columns, in the same
// sorted ranking as our own models — with a prose note underneath telling the
// reader not to compare them. That note is exactly the mitigation the previous
// retraction had already recorded as insufficient, and the surviving triple is
// MORE sensitive to the free "major" constant than the banded one it replaced:
// re-parsing the identical cached bytes with major recorded at error moves the
// incumbent's published O-* from 2/4/4 to 5/4/1. A figure that moves when we
// change our own constant, with no change in the reviewer's output, is not a
// measurement of that reviewer, and printing it anyway is publishing the score
// the retraction says is not offered.
func PublishesOurSeverityLevels(model string) bool {
	return model != IncumbentModel
}

// The headers of every table the eval reports print.
//
// They live in NON-TEST code, away from the code that prints them, for one
// reason: the reports are rendered from files behind the `eval` build tag, and a
// guard that only compiles under that tag cannot run in `go test ./...`. Keeping
// the headers here lets TestEveryPublishedColumnIsRegistered read them in the
// default build, so a new column cannot be added to a published table without
// being declared — and declaring it as a score subjects it to the
// degenerate-strategy table.
//
// This is the mechanism that would have stopped the withdrawn banded columns:
// B-INFL/B-UNDER/B-ACC were added to two headers and a legend with nothing
// anywhere asking what maximised them.
const (
	// SummaryTableHeader is the ground-truth battery's table: no judge, no
	// foreign reviewer, one row per model and fixture.
	SummaryTableHeader = "MODEL                                FIXTURE                   RUNS  RECALL   " +
		"SEV A/I/U  NOISE  ANCHOR  STABLE  FAILED"

	// VariantTableHeader is the prompt-variant comparison.
	VariantTableHeader = "VARIANT                 N     FAIL  FINDINGS  REAL  WORTH  PRECISION  J-INFL  " +
		"J-UNDER  O-INFL  O-UNDER  O-ACC  O-COV  MISCLASS  TONE-OFF  MISSED  SIGNAL  GRADE"

	// JudgedModelTableHeader is the model ranking, and the table the head-to-head
	// against the incumbent is printed in.
	JudgedModelTableHeader = "MODEL                                GRADE  SPREAD  PREC   COV  N    FAIL  " +
		"FIND  WORTH  J-INFL  J-UNDER  O-INFL  O-UNDER  O-ACC  O-COV  MISCLASS  MISSED  SIGNAL"
)

// ScoreTableHeaders are the tables that publish a PublishedMetrics reading:
// every column in them is a registered score, an LLM judge's opinion, or an
// identifier, and every metric they touch must be printed complete.
//
// It is NOT every header this package prints — see AllTableHeaders, and read its
// comment before assuming a guard that runs over these has seen them all.
func ScoreTableHeaders() []string {
	return []string{SummaryTableHeader, VariantTableHeader, JudgedModelTableHeader}
}

// AllTableHeaders is every table header this package prints anywhere.
//
// THE BUG: the guard against republishing a cross-tool severity column ran over
// the three score tables only, while its predecessor's doc comment claimed to
// cover "every published table header". Appending `B-ACC` to CostTableHeader
// therefore passed both that guard and the column-registration one — the
// withdrawn instrument could be reinstated in a table the mechanism did not
// know existed, which is the same hole the banded columns went through the first
// time. TestEveryTableHeaderInThePackageIsRegistered reads the package source so
// that a header added tomorrow cannot escape this list either.
//
// The cost and rejudge headers are here for the cross-tool severity guard, and
// are deliberately NOT in ScoreTableHeaders: their columns are readings those
// tracks define (CostReadingLegend, PublishedCostReadings) and classifying them
// from here would assert things about accounting this file does not compute. One
// consequence is stated rather than hidden: CostTableHeader prints RECALL as the
// denominator of $/DEFECT with no NOISE column beside it, which is the same
// shape as the defect ScoreTableHeaders' completeness rule exists to stop, and
// it belongs to the cost track to close.
func AllTableHeaders() []string {
	return append(ScoreTableHeaders(), CostTableHeader, precisionHeader, agreementHeader)
}

// JudgeOpinionColumns are the columns an LLM judge supplies.
//
// They are not exempt from "what maximises this?" — PRECISION's own doc comment
// records that returning 1 for a review with no findings made SILENCE the global
// optimum of the tuning objective, which is that same failure found the hard
// way. They are exempt from being answered by the degenerate-strategy table,
// because scoring a synthetic reviewer on them would mean calling a judge.
func JudgeOpinionColumns() []string {
	return []string{
		"PRECISION", "PREC", "GRADE", "SPREAD", "SIGNAL",
		"J-INFL", "J-UNDER", "MISCLASS", "TONE-OFF", "MISSED", "REAL", "WORTH",
	}
}

// DescriptiveColumns identify a row or state how much measurement is behind it.
// They are not scores: no reviewer is better for having a larger N.
//
// STABLE is the borderline one and is listed here deliberately, with the
// borderline handled in Summary.Stable rather than by the classification. It is
// printed "yes" or "NO [3 5 4]" and a reader does take yes as better, so leaving
// it descriptive is only honest because a reviewer that produced no findings now
// gets "n/a" instead of the best value. FINDINGS and FIND are counts and stay
// counts: no model-free column here rewards saying more.
func DescriptiveColumns() []string {
	return []string{
		"MODEL", "FIXTURE", "VARIANT", "RUNS", "N", "COV", "FAIL", "FAILED",
		"FINDINGS", "FIND", "STABLE", "A/I/U",
	}
}

// SeverityColumnLegend is printed under every table carrying a severity column.
//
// It is a const in non-test code so that the retraction it carries cannot be
// edited out of a report without the guard in the default build seeing it.
const SeverityColumnLegend = "SEVERITY IS MEASURED TWO WAYS, AND NEITHER IS A CROSS-TOOL SCORE. " +
	"J-INFL/J-UNDER are the JUDGE's opinion, per finding it returned a verdict for. " +
	"O-INFL/O-UNDER/O-ACC compare each LOCATED PLANTED DEFECT to the WantSeverity its fixture " +
	"declares, at our full five-level resolution (critical/error/warning/info/nit), with no model " +
	"involved. O-* is the column our own prompt is tuned on. " +
	"O-COV IS THE DENOMINATOR AND IS NOT OPTIONAL: severity is graded only over defects the reviewer " +
	"LOCATED, so a reviewer that mentions only the defects it is already sure about is scored only on " +
	"those — reporting nothing but this corpus's critical plants, and calling them critical, ties a " +
	"perfectly calibrated reviewer on O-ACC/O-INFL/O-UNDER while covering 4 plants of 14. Read the " +
	"triple only with O-COV beside it. " +
	"ROWS THAT DO NOT PUBLISH OUR FIVE LEVELS PRINT n/a THERE: " + IncumbentModel + " publishes one " +
	"'critical' spanning our critical AND error, so a figure for it would state a vocabulary gap rather " +
	"than review quality in either direction — it cannot score O-ACC on an error plant without " +
	"over-claiming on a critical one. " + NoCrossToolSeverityScore + " " +
	"The two groups are counted over DIFFERENT things — J-* per judged finding, O-* per planted " +
	"defect the review actually found — so neither is a share of the other, and a defect nobody " +
	"reported appears in neither. Every count column is divided by N on the same row."
