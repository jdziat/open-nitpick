package evals

import (
	"fmt"
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

	// Verdict is one of SevAccurate, SevInflated, SevUnderstated.
	Verdict string
}

// SeverityScore compares assigned severities against the planted ones.
//
// It exists because severity correctness used to be an LLM opinion and nothing
// else: every fixture Defect declares WantSeverity and nothing outside
// fixtures.go read it. That opinion is measurably blind in one direction — the
// judge reported Incumbent understating NOTHING, on a corpus whose own ground
// truth says it understated four of the seven defects it located, two of them
// forced by crSeverity mapping Incumbent's "critical" down to our "error". A
// prompt tuned to reduce inflation against a scorer that cannot see
// under-claiming optimizes toward saying less and calls it progress.
//
// Those two forced understatements are the price of comparing across two
// vocabularies, and they are the reason a plant must not be raised to
// `critical` casually: crSeverity has no branch that returns it, so every such
// plant is one Incumbent cannot score accurate on and cannot be caught
// inflating. Raising one moves O-ACC and O-UNDER with no change whatever in
// Incumbent's output. TestIncumbentCannotExpressCritical pins the count so
// the number in this comment and the corpus cannot drift apart.
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

// severityVerdict compares an assigned severity against the planted one.
func severityVerdict(got, want config.Severity) string {
	switch {
	case got.Rank() > want.Rank():
		return SevInflated
	case got.Rank() < want.Rank():
		return SevUnderstated
	default:
		return SevAccurate
	}
}

// reportingFinding picks which of a review's findings is credited with
// reporting a defect, returning its index.
//
// Several findings can match one plant — a reviewer may raise the same problem
// twice, or fold two nearby problems into one comment — so the one whose
// severity sits NEAREST the plant is taken, with ties resolved to the first in
// report order for reproducibility. The benefit of the doubt belongs here,
// among a reviewer's own findings: if it rated this defect correctly anywhere,
// that is its call, and grading it on some other comment that also happened to
// match would invent a severity error out of the keyword list.
//
// The benefit of the doubt deliberately does NOT extend across plants, which is
// what the previous per-finding form did. multi-defect plants a critical
// traversal and an error descriptor leak on the same line; letting one finding
// choose which plant it was graded against made every severity from error to
// critical score accurate, so under-rating the traversal was unmeasurable and a
// reviewer could buy immunity from the column being tuned by merging comments.
// Each defect is now graded against its own planted severity, so a merged
// comment rated below the worst of them is reported as understating it.
func reportingFinding(findings []review.Finding, d Defect) (int, bool) {
	var (
		best  int
		gap   int
		found bool
	)

	want := d.WantSeverity.Rank()
	for i, f := range findings {
		if !matches(f, d) {
			continue
		}

		delta := f.Sev().Rank() - want
		delta = max(delta, -delta)

		if !found || delta < gap {
			best, gap, found = i, delta, true
		}
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

	Violations []string

	// FindingCounts is the number of findings produced per run, which is how
	// run-to-run stability is judged.
	FindingCounts []int
}

// Stable reports whether every run produced the same number of findings.
//
// Identical inputs at temperature 0 should produce identical output. When they
// do not, a severity gate becomes a coin flip.
func (s Summary) Stable() bool {
	if len(s.FindingCounts) < 2 {
		return true
	}
	for _, n := range s.FindingCounts[1:] {
		if n != s.FindingCounts[0] {
			return false
		}
	}
	return true
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
	out := Summary{Model: model, Fixture: fixture, Runs: len(scores)}

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
		out.Violations = append(out.Violations, s.Violations...)
		out.FindingCounts = append(out.FindingCounts, len(s.Findings()))
	}

	return out
}
