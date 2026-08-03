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
	lo, hi := f.Line, f.EndLine
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
func spanLength(f review.Finding) int {
	if f.EndLine <= f.Line {
		return 1
	}
	return f.EndLine - f.Line + 1
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
		out.Violations = append(out.Violations, s.Violations...)
		out.FindingCounts = append(out.FindingCounts, len(s.Findings()))
	}

	return out
}
