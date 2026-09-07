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

	// WidestAnchor is the most DISTINCT LINES the review pointed at about any one
	// thing: the widest single finding, and the union of every finding claiming a
	// given defect, whichever is larger. See anchoredLines and defectAnchoredLines.
	//
	// Reported because anchorDistance measures from the nearest edge of a span
	// and takes the minimum over every region, which means a reviewer can earn a
	// match by gesturing at a whole function rather than pointing at the line.
	// That trade is worth making, a region containing the defect HAS found it,
	// but it has to be visible, or "matched the defect" and "said roughly where
	// to look" become the same number. One means the reviewer anchored precisely.
	WidestAnchor int

	// AnchoredLines is the same measurement summed rather than maxed, over the
	// defects this run LOCATED. See DetectionScore.AnchoredLines, which is this
	// same number and carries the argument for publishing both folds.
	AnchoredLines int

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
		Severity:  SeverityScore{Planted: PlantedLevels{}},
	}

	// The census is a fact about the fixture, so it is taken before anything
	// that can return early. Taken inside ScoreSeverity, which the two returns
	// below skip, a failed run contributes its plants to Total and nothing to
	// the per-level census. Over AllFixtures with every third run failing, that
	// reports a census summing to 17 against a planted total of 29, and the
	// block beside the recall cell carries no "(0 of N located): nothing
	// located" line anywhere, the absence-is-invisible failure
	// SeverityUsage.Lines exists to close, reached by an ordinary provider
	// error rather than by a degenerate reviewer.
	//
	// ScoreSeverity takes the same census again on the path that reaches it, and
	// the duplication is deliberate: it is exported and its own contract is that
	// a score carries its denominators, so neither caller may rely on the other
	// having done it.
	s.Severity.Planted.Add(f)

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

	// Every DETECTION READING COMES FROM ScoreDetection, including the located
	// set this function used to compute for itself. Two reasons, and the second
	// is the one that matters: the judged batteries hold a finding list and never
	// build a RunResult for the incumbent, so they must read these off the same
	// code; and "which defects were located" was written out twice, here and
	// inside the anchor arithmetic, which is two expressions for one integer and
	// free to drift under an edit to either.
	det := ScoreDetection(f, r.Report.Findings)
	s.WidestAnchor = det.WidestAnchor
	s.Unmatched = det.Unmatched
	s.Detected = det.Detected
	s.Matched = det.Matched
	s.AnchoredLines = det.AnchoredLines

	s.Severity = ScoreSeverity(f, r.Report.Findings)

	s.Violations = append(s.Violations, checkInvariants(r)...)

	return s
}

// DetectionScore is what a review INVENTED and how vaguely it pointed, with no
// judge, no model, no token accounting and no RunResult involved.
//
// It exists because the two readings it carries were computable only through
// ScoreRun, and ScoreRun takes a RunResult. The judged batteries hold a bare
// (fixture, findings) pair for both contenders, Incumbent is shelled out to and
// has no RunResult at all, so for those tables NOISE and ANCHOR were not
// discarded, they were never computed. Splitting the computation out is what lets
// the head-to-head fill the columns from the same functions the ground-truth
// battery fills them from. Rule 6c-i in docs/measurement.md records the cost of
// the alternative: hand-written Detections literals declared Noise 40 and
// WidestAnchor 900 for behaviours the real scorer answered 0 and 1 for.
type DetectionScore struct {
	// Detected maps a planted defect's Why to whether some finding matched it,
	// and Matched counts the defects that were. They are ScoreRun's Detected and
	// Matched: that function read them off its own copy of the matches() loop
	// until this type needed the same set for AnchoredLines.
	Detected map[string]bool
	Matched  int

	// NearMisses counts findings that name a plant's keywords in the plant's
	// file but sit beyond the anchor tolerance, a reviewer that found the
	// defect and pointed at the wrong line. They are noise by the rules, and
	// counted separately so that a near miss is visible instead of folded
	// into the same column as a fabrication.
	NearMisses int

	// Unmatched are the findings that explain no planted defect. On a clean
	// fixture every finding is unmatched by definition.
	Unmatched []review.Finding

	// WidestAnchor is the most DISTINCT LINES this review pointed at about any
	// one thing. See Score.WidestAnchor, which is this same number.
	WidestAnchor int

	// AnchoredLines is how many DISTINCT LINES this review pointed at in total
	// while claiming the defects it LOCATED, the same per-defect union
	// WidestAnchor takes the MAXIMUM of, summed instead.
	//
	// THE MAXIMUM and THE SUM CATCH DIFFERENT REVIEWERS and NEITHER SUBSUMES THE
	// OTHER. A max is what sees one blob among precise findings, which is what
	// CorpusTally.WidestAnchor was added for and remains right about. A max is
	// also blind to UNIFORM vagueness: a reviewer line-precise on 28 plants with
	// one 13-line comment and a reviewer that smears every one of its 29 anchors
	// over 13 lines are the same maximum, and to a reader they are 41 lines to
	// read against 377. Read per located defect, they are 1.41 and 13.
	//
	// It matters most where a threshold is read off the max RELATIVE TO ANOTHER
	// REVIEWER, because that reviewer's worst single finding then becomes a width
	// every finding may spend. See docs/measurement.md's Rule 14 note, which
	// records that reading as a defect in the rule rather than retuning a
	// pre-registered threshold.
	//
	// Restricted to LOCATED defects because it is published as a per-defect rate
	// and this is its numerator: lines a reader is pointed at, per defect the
	// review found. Every located defect contributes at least one line,
	// matches() implies explainsAny(), so the union claiming it is never empty,
	// so the rate is at least 1.00 for any reviewer that found anything, and is
	// undefined rather than zero for one that found nothing.
	AnchoredLines int
}

// Noise is how many findings explained nothing the fixture planted.
func (d DetectionScore) Noise() int { return len(d.Unmatched) }

// ScoreDetection measures a finding list against the defects a fixture plants.
//
// It is a pure function of its two arguments, which is what makes it usable on
// the cached incumbent's side of the head-to-head and re-runnable offline over a
// retained dump: no run index, no usage record and no judge verdict is consulted.
func ScoreDetection(f Fixture, findings []review.Finding) DetectionScore {
	out := DetectionScore{Detected: map[string]bool{}}

	for _, finding := range findings {
		out.WidestAnchor = max(out.WidestAnchor, anchoredLines(finding))
	}

	// Per FINDING is not enough on its own: it cannot tell one comment claiming
	// seventeen regions from seventeen comments claiming one line each about the
	// same defect, and for a reader those are the same seventeen lines. Both are
	// taken because neither subsumes the other, a wide finding on a fixture that
	// plants nothing belongs to no defect and would vanish from the second.
	claimed := defectAnchoredLines(findings, f.Defects)

	for i, d := range f.Defects {
		out.WidestAnchor = max(out.WidestAnchor, claimed[i])

		found := false
		for _, finding := range findings {
			if matches(finding, d) {
				found = true
				break
			}
		}

		// OR rather than assignment: two defects may share a Why, and one of them
		// being located is what the map is asked about.
		out.Detected[d.Why] = out.Detected[d.Why] || found
		if !found {
			continue
		}
		out.Matched++
		out.AnchoredLines += claimed[i]
	}

	// Anything not explaining a planted defect is noise on this corpus.
	for _, finding := range findings {
		if !explainsAny(finding, f.Defects) {
			out.Unmatched = append(out.Unmatched, finding)
			if nearMiss(finding, f.Defects) {
				out.NearMisses++
			}
		}
	}

	return out
}

// nearMiss reports whether a finding names a plant's keywords in its file
// but anchors beyond the tolerance.
func nearMiss(f review.Finding, defects []Defect) bool {
	for _, d := range defects {
		if f.Path == d.Path && mentionsAny(f, d.Keywords) {
			return true
		}
	}
	return false
}

// maxFixLinesForScoring mirrors the engine's maxFixLines, duplicated on
// purpose so the invariant does not agree with a regression in it.
const maxFixLinesForScoring = 40

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
// cross-tool severity accuracy figure.
//
// The note behind it is in docs/measurement.md#nocrosstoolseverityscore.
const NoCrossToolSeverityScore = "NO CROSS-TOOL SEVERITY ACCURACY IS OFFERED, BY CONSTRUCTION. " +
	"Our five levels and " + IncumbentModel + "'s ~three are different RESOLUTIONS, and every " +
	"reduction that makes them comparable is maximised by a reviewer that also picks what to mention: " +
	"on this corpus (29 plants over 30 fixtures, banded 12 blocking, 6 medium, 11 low) a reviewer that " +
	"stays silent unless the defect is already blocking and then calls it critical banded a perfect " +
	"12/0/0 over 12 of the 29 plants — an exact tie with a calibrated reviewer's 29/0/0. The banded " +
	"column also could not see the parser bug that caused the previous retraction — buggy and fixed " +
	"parsers both banded 10/0/4 on the incumbent, while our full resolution moved 6/4/4 to 8/0/6. So no " +
	"such number is published, and the O-* cells on a row that does not publish our five levels are " +
	"printed n/a rather than filled and annotated: telling a reader not to compare two numbers printed " +
	"in one sorted ranking is the mitigation the previous retraction already found insufficient. " +
	"What IS published for such a row is the severity VOCABULARY it used against each planted level " +
	"(see the SEVERITY VOCABULARY block): a description, which a reader may compare by eye and may not " +
	"reduce to one figure. O-* remains a real score BETWEEN ROWS THAT PUBLISH OUR FIVE LEVELS and is " +
	"what our own prompt is tuned on."

// SeverityScale is DECLARED by whatever adapter produced a row's findings, and
// it is not derivable from them.
//
// The note behind it is in docs/measurement.md#severityscale.
type SeverityScale string

const (
	// UndeclaredSeverityScale is the zero value: nobody said what scale this
	// row's severities are on, so nothing that reads them as our five levels is
	// published for it.
	UndeclaredSeverityScale SeverityScale = ""

	// OurSeverityScale is a row whose severity words are the five this project
	// defines, written by the reporter rather than translated into them.
	OurSeverityScale SeverityScale = "our five levels"

	// ForeignSeverityScale is a row whose reporter publishes some other
	// vocabulary, whatever this package translated it into.
	ForeignSeverityScale SeverityScale = "not our five levels"
)

// PublishesOurLevels reports whether a row's severity comparison against
// Defect.WantSeverity is a score rather than a measurement of the vocabulary
// gap.
func (s SeverityScale) PublishesOurLevels() bool { return s == OurSeverityScale }

// describe names the scale for a message, so a row that declared nothing says
// so instead of reading as a row someone declared foreign.
func (s SeverityScale) describe() string {
	if s == UndeclaredSeverityScale {
		return "undeclared"
	}
	return string(s)
}

// SeverityWord is one severity as a reviewer PRINTED it, beside the level this
// package recorded it at.
//
// The two are separate fields because they are not always the same string, and
// collapsing them is the defect this type exists to prevent. Our own models
// write Severity themselves, so for them Said and Recorded are the same word and
// nothing was translated. A reviewer with a different vocabulary has its word
// rewritten into ours at parse time, and publishing only the result attributes
// our sentence to it.
type SeverityWord struct {
	// Said is the token the reviewer emitted, verbatim.
	//
	// EMPTY MEANS UNRECOVERABLE, and is published as such. The one case that
	// produces it is a Incumbent cache entry collected before the raw review
	// was retained: its findings are a previous parser's reading with the
	// original word already discarded. Filling Said in from Recorded there would
	// quote the reviewer as having said a word we chose for it, which is the
	// whole failure, so the block says the word is not recorded instead.
	Said string

	// Recorded is the level this package scored the finding at, OUR reading,
	// and labelled as ours wherever it differs from Said.
	Recorded config.Severity
}

// SeverityUsage tabulates which severity words a reviewer printed for
// the defects it located, against the level each defect was planted at.
//
// The note behind it is in docs/measurement.md#severityusage.
type SeverityUsage map[config.Severity]map[SeverityWord]int

// Add records one graded defect.
func (u SeverityUsage) Add(planted config.Severity, said SeverityWord) {
	row, ok := u[planted]
	if !ok {
		row = map[SeverityWord]int{}
		u[planted] = row
	}
	row[said]++
}

// PlantedLevels is how many defects the fixtures behind a row planted at each
// level, the DENOMINATOR of the contingency table, and the half of it that has
// nothing to do with what the reviewer said.
//
// The note behind it is in docs/measurement.md#plantedlevels.
type PlantedLevels map[config.Severity]int

// Add counts one fixture's plants.
func (p PlantedLevels) Add(f Fixture) {
	for _, d := range f.Defects {
		p[d.WantSeverity]++
	}
}

// Merge folds another census in, so a row summed over runs and fixtures carries
// the denominator those runs and fixtures planted.
func (p PlantedLevels) Merge(other PlantedLevels) {
	for level, n := range other {
		p[level] += n
	}
}

// Total is how many defects were planted across every level.
func (p PlantedLevels) Total() int {
	out := 0
	for _, n := range p {
		out += n
	}
	return out
}

// Merge folds another tabulation in, so a per-run description can be summed
// across runs and fixtures without the caller reimplementing the nesting.
func (u SeverityUsage) Merge(other SeverityUsage) {
	for planted, row := range other {
		dst, ok := u[planted]
		if !ok {
			dst = map[SeverityWord]int{}
			u[planted] = dst
		}
		for said, n := range row {
			dst[said] += n
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

// UnrecordedWord is printed in place of a reviewer's severity token when the
// token cannot be recovered.
//
// It is a phrase rather than a blank so that a reader meets the gap instead of
// skipping it, and it is exported so a test can assert the gap is reported
// rather than filled. Substituting our own reading here is the substitution this
// whole block was rewritten to stop.
const UnrecordedWord = "(word not recorded)"

// NothingLocated is printed on the row of a planted level the reviewer never
// reached, in place of the words it did not say.
// TestEveryPlantedLevelAppearsWithItsDenominator covers that row.
//
// A phrase rather than a blank, for the reason UnrecordedWord is one: a reader
// has to meet the row rather than skip past a gap that looks like formatting.
const NothingLocated = "nothing located"

// UndeclaredPlantedTotal is printed in place of a level's denominator when the
// caller handed this block no census of what the corpus planted.
//
// It exists so that the one thing a row may not do is imply a denominator it
// does not have. A row carrying it is a bug in the caller, and
// TestEveryPlantedLevelAppearsWithItsDenominator is where that is caught over
// the real corpus.
const UndeclaredPlantedTotal = "PLANTED TOTAL NOT DECLARED"

// Lines renders the tabulation, one line per level THE CORPUS PLANTS, present
// whether or not anything was located there, with the planted count as its
// denominator.
//
// Omitting levels nobody located, on the reasoning that a miss is recall's
// job, makes the worst strategy's page the cleanest one. The reviewer that
// reports only the plants we rate critical and calls them critical renders
//
//	planted critical (4 located): critical x4
//
// and nothing else, a proper SUBSTRING of a calibrated reviewer's whole block,
// with O-COV blanked in the cell on that same row. Absence is invisible; a
// denominator is not. Under this rendering the same strategy prints four rows
// reading "(0 of N located): nothing located" and stops resembling a calibrated
// reviewer at a glance. TestEveryPlantedLevelAppearsWithItsDenominator asserts
// the rule over AllFixtures, and restoring the omit-empty behaviour fails there.
//
// Each entry is the reviewer's own word. Where this package translated that word
// into one of our five levels the level follows it, marked as ours, so the two
// cannot be read as one statement by the reviewer, "major x2 [we read as
// warning]" says who said what. Our own models translate nothing, so their lines
// carry no marker at all.
func (u SeverityUsage) Lines(planted PlantedLevels) []string {
	var out []string

	// The union, so a level the corpus plants survives a reviewer that never
	// reached it and a word that landed on a level the census does not carry
	// survives a caller that handed over the wrong census. Neither is silently
	// dropped, because dropping either is how a denominator stops matching its
	// numerator without anyone seeing it.
	levels := map[config.Severity]bool{}
	for level := range u {
		levels[level] = true
	}
	for level := range planted {
		levels[level] = true
	}

	for _, level := range orderedSeverities(levels) {
		row := u[level]

		located := 0
		var parts []string
		for _, said := range orderedWords(row) {
			located += row[said]

			word := said.Said
			if word == "" {
				word = UnrecordedWord
			}

			part := fmt.Sprintf("%s x%d", word, row[said])
			// A difference of CASE is not a difference of vocabulary, and
			// flagging it would bury the translations that are, "major" against
			// a reviewer whose word has no counterpart among our five, in noise
			// about "Critical" versus "critical". The verbatim token is printed
			// either way, so the reader still sees the case the reviewer used.
			if said.Said == "" || !strings.EqualFold(said.Said, string(said.Recorded)) {
				part += fmt.Sprintf(" [we read as %s]", said.Recorded)
			}
			parts = append(parts, part)
		}

		if len(parts) == 0 {
			parts = []string{NothingLocated}
		}

		count := fmt.Sprintf("%d located, %s", located, UndeclaredPlantedTotal)
		if total, ok := planted[level]; ok {
			count = fmt.Sprintf("%d of %d located", located, total)
		}

		out = append(out, fmt.Sprintf("  planted %-8s (%s): %s",
			level, count, strings.Join(parts, ", ")))
	}

	return out
}

// orderedWords lists a row's words in a stable print order: the level we
// recorded them at, most severe first, then the reviewer's spelling.
//
// Ordering on OUR level keeps the line legible. It still reads most-severe
// first, as it did when our level was the key, while the spelling tie-break is
// what makes the render a function of the SET of words rather than of map
// iteration order. A description a reader diffs between runs has to be stable,
// and two foreign words mapping to one level of ours is the normal case here,
// not the exception.
func orderedWords(row map[SeverityWord]int) []SeverityWord {
	out := make([]SeverityWord, 0, len(row))
	for w := range row {
		out = append(out, w)
	}

	rank := map[config.Severity]int{}
	for i, s := range severityOrder {
		rank[s] = i
	}
	// A level outside our five sorts after all of them, which is where an
	// unrecognized spelling of OUR OWN belongs: it is not a level, so it cannot
	// claim a place in the ordering of levels.
	rankOf := func(s config.Severity) int {
		if i, ok := rank[s]; ok {
			return i
		}
		return len(severityOrder)
	}

	sort.Slice(out, func(i, j int) bool {
		if a, b := rankOf(out[i].Recorded), rankOf(out[j].Recorded); a != b {
			return a < b
		}
		if out[i].Recorded != out[j].Recorded {
			return out[i].Recorded < out[j].Recorded
		}
		return out[i].Said < out[j].Said
	})

	return out
}

// orderedSeverities lists a map's severity keys most severe first, with any key
// outside our five appended in sorted order rather than dropped, a foreign
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
// The note behind it is in docs/measurement.md#severityscore.
type SeverityScore struct {
	Accurate    int
	Inflated    int
	Understated int

	// Calls is one entry per graded defect, so a report can name the finding
	// that was inflated instead of only how many were. Aggregate counts are
	// what a table shows; fixing the prompt needs the finding itself.
	Calls []SeverityCall

	// Planted is every level the fixture planted at, counted, including the
	// levels this review located nothing at.
	//
	// It is carried on the SCORE rather than recomputed at the renderer because
	// the renderer does not have the fixture, and a denominator derived from a
	// different pass over the corpus than its numerator is how the two come to
	// disagree. See SeverityUsage.Lines for what it is for.
	Planted PlantedLevels
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
		out.Add(c.Defect.WantSeverity, severityAsSaid(c.Finding))
	}
	return out
}

// severityAsSaid pairs the word a finding's reviewer printed with the level
// this
// package recorded it at.
//
// The note behind it is in docs/measurement.md#severityassaid.
func severityAsSaid(f review.Finding) SeverityWord {
	w := SeverityWord{Said: f.RawSeverity, Recorded: f.Sev()}
	if w.Said == "" && !severityWasTranslated(f) {
		w.Said = f.Severity
	}
	return w
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
//     Incumbent's "Propagate archive failures", about tar's ignored exit
//     status, was counted as understating the command injection.
//   - Every comment describing these columns says "defects", and the numbers
//     said findings. Graded (8) exceeded the defects located (7), so the legend
//     printed under the table contradicted the column beside it.
//
// Defects no finding reports are skipped rather than counted: the corpus says
// nothing about the severity of a bug the reviewer never mentioned, and a miss
// is already reported as a miss.
func ScoreSeverity(f Fixture, findings []review.Finding) SeverityScore {
	out := SeverityScore{Planted: PlantedLevels{}}

	// Every planted level, before any finding is consulted: the census is of the
	// FIXTURE, so a review that located nothing still carries the denominators
	// its corpus defines.
	out.Planted.Add(f)

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
// The note behind it is in docs/measurement.md#severityverdict.
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
// The note behind it is in docs/measurement.md#reportingfinding.
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
		spelling := severityAsSaid(f).Said

		switch {
		case !found, sev.Rank() > loud.Rank():
		case sev.Rank() < loud.Rank():
			continue

		// Equal rank from here: decide on the word that will be PUBLISHED, so
		// the vocabulary block does not depend on the order the review arrived
		// in.
		case ok && !known:
		case !ok && known:
			continue

		// A word the review KEPT beats a word that was destroyed. An empty
		// spelling is not a spelling, it renders as UnrecordedWord, so
		// comparing it lexicographically against a real one lets the gap win
		// every time, since "" sorts before everything.
		case spelling != "" && raw == "":
		case spelling == "" && raw != "":
			continue

		case spelling < raw:
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
// The note behind it is in docs/measurement.md#anchordistance.
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

// anchoredLines is how much of a file a finding is pointing at: the number of
// DISTINCT lines its anchor claims, counting its primary region and every
// region
// in AlsoAt as one set. One for the ordinary single-line anchor.
//
// The note behind it is in docs/measurement.md#anchoredlines.
func anchoredLines(f review.Finding) int {
	claimed := map[int]bool{}
	coverInto(claimed, f)
	return len(claimed)
}

// coverInto marks every line one finding claims.
func coverInto(claimed map[int]bool, f review.Finding) {
	cover := func(s review.LineSpan) {
		hi := s.EndLine
		if hi < s.Line {
			hi = s.Line
		}
		for line := s.Line; line <= hi; line++ {
			claimed[line] = true
		}
	}

	cover(review.LineSpan{Line: f.Line, EndLine: f.EndLine})
	for _, s := range f.AlsoAt {
		cover(s)
	}
}

// defectAnchoredLines is the same measurement one level up: for each planted
// defect, how many DISTINCT LINES did the whole review point at while claiming
// that defect, counting every finding that names and sits near it as one set.
// It
// returns one count per defect, in the order they are planted.
//
// The note behind it is in docs/measurement.md#defectanchoredlines.
func defectAnchoredLines(findings []review.Finding, defects []Defect) []int {
	out := make([]int, len(defects))

	for i, d := range defects {
		claimed := map[int]bool{}
		for _, f := range findings {
			if f.Path != d.Path || !explainsAny(f, []Defect{d}) {
				continue
			}
			coverInto(claimed, f)
		}
		out[i] = len(claimed)
	}

	return out
}

// noiseTolerance is how far from a planted defect a finding may sit and still
// count as being ABOUT it rather than as invented.
//
// The note behind it is in docs/measurement.md#noisetolerance.
const noiseTolerance = 6

// explainsAny reports whether a finding describes any planted defect: it names
// the defect and sits near it.
//
// The note behind it is in docs/measurement.md#explainsany.
func explainsAny(f review.Finding, defects []Defect) bool {
	for _, d := range defects {
		if f.Path != d.Path || !mentionsAny(f, d.Keywords) {
			continue
		}
		if anchorDistance(f, d.Line) <= noiseTolerance {
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

// checkInvariants verifies properties the tooling must guarantee for any model.
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
		// one-click suggestion. A prose suggestion rendered as applicable
		// corrupts the file when clicked, and a multi-line one does unless
		// the comment spans exactly the lines it replaces, which is what the
		// engine's validation guarantees, and what StartLine says.
		if idx := strings.Index(c.Body, "```suggestion\n"); idx >= 0 {
			rest := c.Body[idx+len("```suggestion\n"):]
			block, _, _ := strings.Cut(rest, "\n```")

			if strings.Contains(strings.TrimRight(block, "\n"), "\n") && c.StartLine == 0 {
				out = append(out, fmt.Sprintf("%s:%d publishes a MULTI-LINE applicable suggestion on a single-line comment; "+
					"GitHub replaces only the anchored line, so applying it corrupts the file", c.Path, c.Line))
			}
			if c.StartLine > 0 {
				want := c.Line - c.StartLine + 1
				if got := strings.Count(strings.TrimRight(block, "\n"), "\n") + 1; got > maxFixLinesForScoring {
					out = append(out, fmt.Sprintf("%s:%d-%d publishes a %d-line suggestion", c.Path, c.StartLine, c.Line, got))
				} else if want <= 0 {
					out = append(out, fmt.Sprintf("%s:%d-%d spans no lines", c.Path, c.StartLine, c.Line))
				}
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
	// so their total is exactly Matched, the numerator of the RECALL cell
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

	// SevPlantedLevels is the denominator SevUsage is read against: what these
	// runs planted at each level, located or not.
	SevPlantedLevels PlantedLevels

	// Scale is what vocabulary this row's severities are on, DECLARED by the
	// adapter that produced its findings and defaulting to withheld. See
	// SeverityScale.
	Scale SeverityScale

	Violations []string

	// WidestAnchor is the widest single region any finding claimed across these
	// runs, printed as ANCHOR. It is part of the detection reading, not a
	// diagnostic: see CorpusTally.WidestAnchor.
	WidestAnchor int

	// AnchoredLines is the total lines pointed at while claiming the defects
	// these runs located, printed as L/DEF against Matched. The per-run form
	// carries the same name.
	AnchoredLines int

	// FindingCounts is the number of findings produced per run, which is how
	// run-to-run stability is judged.
	FindingCounts []int
}

// Spread is how many lines this row pointed at per defect it LOCATED, and
// whether that question has an answer.
//
// Undefined rather than zero when nothing was located: zero is the best value
// this reading can take and a reviewer that found nothing has not earned it. The
// rate is at least 1.00 for anything that located a defect, because a located
// defect is claimed by at least one line.
func (s Summary) Spread() (float64, bool) {
	if s.Matched == 0 {
		return 0, false
	}
	return float64(s.AnchoredLines) / float64(s.Matched), true
}

// Stable reports whether every run produced the same number of findings, and
// whether that question has an answer for this row.
//
// The note behind it is in docs/measurement.md#stable.
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

// RateLegend is printed under every table that carries a rate.
//
// The note behind it is in docs/measurement.md#ratelegend.
const RateLegend = "EVERY RATE HERE IS A QUOTIENT OF TWO SMALL INTEGERS, AND THE COUNTS ARE PRINTED " +
	"BESIDE IT FOR THAT REASON. A rate measured over d observations moves only in steps of 1/d, so " +
	"two rows differing by less than 1/d are not distinguishable by it — the decimals are arithmetic, " +
	"not resolution. Read the counts first and treat the quotient as a convenience."

// CorpusResolution states, for the corpus under measurement, the
// smallest difference each published rate can express.
//
// It is DERIVED from the fixtures rather than written down. The claim "this
// corpus cannot resolve a difference smaller than one defect" is a fact about
// how many defects are planted, and the last hand-maintained sentence in this
// package, the one naming which words a reviewer prints, was stale by two
// rounds when it was found. A note that goes stale the moment the corpus grows
// is worse than none, because it will be believed.
//
// IT NAMED A DENOMINATOR NO PRINTED CELL HAS. The note said "RECALL therefore
// moves in steps of 1/<planted across the whole corpus>" and was printed under
// two tables, in neither of which that is a cell's step. Under the summary table
// RECALL is per (model, fixture) and its denominator is that fixture's plants
// times the run count, eleven of these fifteen fixtures plant exactly one, so
// the coarsest cell moves in steps of 1.000, fourteen times what the note
// claimed. Under the judged table RECALL is per CONTENDER, pooled over every
// review folded into the row, so its step is 1/(the plants those reviews saw):
// the corpus-wide figure TIMES THE RUN COUNT for a contender that covered the
// corpus once per fixture, larger with more runs and smaller with less coverage.
// The run multiplier is stated because the sentence here used to offer a
// dichotomy, equal, or smaller, that omitted the case the benchmark is
// documented to run: `make benchmark RUNS=2` folds two reviews per fixture into
// one row, so the step is 1/58 where this note says 1/29. The per-row count is
// in the DENOMINATORS block and is the authority for the judged cells; the
// figure below is the CORPUS's resolution, which is the right one for the totals
// a reader adds up, and the per-cell floor is derived from the same fixtures
// rather than asserted.
func CorpusResolution(fixtures []Fixture) string {
	planted := 0
	clean := 0
	coarsest := 0
	for _, f := range fixtures {
		planted += len(f.Defects)
		if f.Clean() {
			clean++
			continue
		}
		if n := len(f.Defects); coarsest == 0 || n < coarsest {
			coarsest = n
		}
	}

	if len(fixtures) == 0 {
		return "NO FIXTURE WAS MEASURED, so no rate below has a denominator at all."
	}
	if planted == 0 {
		return fmt.Sprintf("RESOLUTION: %d fixture(s), NONE of which plants a defect. RECALL is "+
			"undefined here and any severity column is undefined with it; only NOISE says anything.",
			len(fixtures))
	}

	return fmt.Sprintf("RESOLUTION: %d planted defect(s) across %d fixture(s), %d of them clean. "+
		"A RECALL TOTAL over the whole corpus moves in steps of 1/%d = %.3f — ONE DEFECT is the "+
		"smallest difference this corpus can express, and two reviewers within one defect of each "+
		"other are TIED as far as anything here can tell. THIS IS THE CORPUS'S RESOLUTION AND NOT "+
		"ANY ROW'S: a judged row pools every review folded into it, so its RECALL divides by the "+
		"plants THOSE reviews saw — the figure here times the run count for a contender that covered "+
		"everything once, less for one that covered less — and the DENOMINATORS block under the "+
		"table is the authority for each row. A PER-FIXTURE RECALL CELL is far coarser: "+
		"the thinnest fixture here plants %d, so that cell moves in steps of 1/%d = %.3f per run, and "+
		"a table of such cells is a table of very small integers however many decimals it prints. A "+
		"severity column is scored only over what a reviewer LOCATED, so its denominator is smaller "+
		"still and its step correspondingly coarser; the coverage cell beside it is what says how "+
		"much smaller.",
		planted, len(fixtures), clean,
		planted, 1/float64(planted),
		coarsest, coarsest, 1/float64(coarsest))
}

// SeverityCell renders this row's SEV a/i/u cell.
//
// The note behind it is in docs/measurement.md#severitycell.
func (s Summary) SeverityCell() string {
	if !s.Scale.PublishesOurLevels() {
		return "n/a"
	}
	if s.SevAccurate+s.SevInflated+s.SevUnderstated == 0 {
		return ""
	}
	return fmt.Sprintf("%d/%d/%d", s.SevAccurate, s.SevInflated, s.SevUnderstated)
}

// Summarize folds per-run scores into one row.
func Summarize(model, fixture string, scores []Score) Summary {
	out := Summary{
		Model: model, Fixture: fixture, Runs: len(scores),
		SevUsage:         SeverityUsage{},
		SevPlantedLevels: PlantedLevels{},
		Scale:            commonScale(scores),
	}

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
		out.SevPlantedLevels.Merge(s.Severity.Planted)
		out.Violations = append(out.Violations, s.Violations...)
		out.WidestAnchor = max(out.WidestAnchor, s.WidestAnchor)
		out.AnchoredLines += s.AnchoredLines
		out.FindingCounts = append(out.FindingCounts, len(s.Findings()))
	}

	return out
}

// commonScale is the severity scale a set of runs agree on, and UNDECLARED when
// they do not.
//
// Disagreement withdraws rather than picks a winner, and the two ways to reach
// it are both real. A row folding one adapter's runs together with another's is
// not on one scale at all, and a row folding a declared run with an undeclared
// one is a row half of whose findings nobody has vouched for; publishing either
// at our resolution states more than was declared.
// TestASummaryOfMixedScalesIsWithheld pins it.
func commonScale(scores []Score) SeverityScale {
	if len(scores) == 0 {
		return UndeclaredSeverityScale
	}

	out := scores[0].Scale
	for _, s := range scores[1:] {
		if s.Scale != out {
			return UndeclaredSeverityScale
		}
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
	// Samples is how many reviews were folded in, the denominator the tables
	// divide their count columns by.
	Samples int

	Matched  int
	Planted  int
	Noise    int
	Severity SeverityScore

	// Scale is what vocabulary these severities are on, declared by the adapter
	// that produced the findings. Undeclared is the zero value and withholds
	// every cell that reads them as our five levels; see SeverityScale.
	Scale SeverityScale

	// WidestAnchor is the most distinct lines any one finding in the corpus
	// claimed, counting all of its regions together. See anchoredLines.
	//
	// It is part of the detection metric rather than a diagnostic because
	// without it RECALL and NOISE are both maximised by one finding per file
	// covering the whole file, titled with every keyword in it: anchorDistance
	// is zero anywhere inside a span, so such a blob is credited with every
	// plant in the file and is noise for none. It scored RECALL 1.000 NOISE 0,
	// an exact tie with a perfectly calibrated reviewer, while pointing at
	// nothing more useful than "there is a nil deref, an SQL injection and a
	// hardcoded secret somewhere in this file". Score.WidestAnchor was written
	// for exactly that trade and was reported only in a t.Logf, so no published
	// column could see it.
	//
	// The version of that strategy this column could not see was the same blob
	// spelled as a list of one-line regions instead of one span. It measured the
	// widest single region, so 38 scattered lines read as 1 and the guard passed
	// on the behaviour it was added to catch. anchoredLines is why it now reads
	// 36.
	//
	// THE MAX and THE SUM are both PUBLISHED, and the argument for the max used
	// to be written one-sidedly: "one blob anywhere in the corpus is the
	// behaviour being caught, and a mean over precise findings hides it." That
	// half is true and is why this field exists. The converse is equally true and
	// went unsaid, a MAX hides UNIFORM vagueness, because a reviewer that is
	// line-precise except for one 13-line comment and a reviewer that smears
	// every anchor over 13 lines report the same 13. Neither fold subsumes the
	// other, so both are published: see AnchoredLines, which is the same
	// per-defect measurement summed over located defects.
	WidestAnchor int

	// AnchoredLines is the total lines pointed at while claiming the defects
	// these reviews located, published as L/DEF against Matched.
	//
	// It is the fold that sees a reviewer hedging everywhere rather than blobbing
	// once, which is the shape a MAX is blind to and the shape a threshold read
	// against ANOTHER reviewer's max invites: that reviewer's single worst
	// finding becomes a width every finding may spend. The per-run form carries
	// the same name.
	AnchoredLines int
}

// Spread is how many lines these reviews pointed at per defect they LOCATED, and
// whether the question has an answer. See Summary.Spread, which is the same
// reading on the other row type.
func (t CorpusTally) Spread() (float64, bool) {
	if t.Matched == 0 {
		return 0, false
	}
	return float64(t.AnchoredLines) / float64(t.Matched), true
}

// TallyScores folds scored runs into a CorpusTally.
func TallyScores(scores []Score) CorpusTally {
	out := CorpusTally{Samples: len(scores), Scale: commonScale(scores)}
	out.Severity.Planted = PlantedLevels{}

	for _, s := range scores {
		out.Matched += s.Matched
		out.Planted += s.Total
		out.Noise += len(s.Unmatched)
		out.WidestAnchor = max(out.WidestAnchor, s.WidestAnchor)
		out.AnchoredLines += s.AnchoredLines
		out.Severity.Accurate += s.Severity.Accurate
		out.Severity.Inflated += s.Severity.Inflated
		out.Severity.Understated += s.Severity.Understated
		out.Severity.Calls = append(out.Severity.Calls, s.Severity.Calls...)
		out.Severity.Planted.Merge(s.Severity.Planted)
	}

	return out
}

// PublishedMetric is one READING an eval report publishes as a score, the set
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
// is the group.
//
// Every model-free score any report prints must be registered here. That is what
// makes the guard structural rather than a one-off: adding a column means adding
// it to a metric, and the degenerate-strategy table then scores it against
// reviewers whose behaviour nobody would ship, always critical, always nit, one
// comment per line, silence, and fails if any of them can max it out. A metric
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
	// This shape is what enforces the grouping. The type's justification is that
	// a single column cannot be asked "what maximises this?", O-INFL alone
	// being maximised by silence, and nothing else enforces it at the table
	// level. Deleting O-ACC and O-UNDER from VariantTableHeader, leaving O-INFL
	// published by itself, passed every guard in this package including the one
	// named after the failure. Columns were checked for being DECLARED somewhere
	// and never for being printed TOGETHER.
	Renderings [][]string

	// Doc says what the metric claims to measure. A degenerate strategy that
	// maxes it out is a counterexample to exactly this sentence.
	Doc string

	// Score returns the metric's components ORIENTED SO that HIGHER IS BETTER,
	// so "maxed out" is componentwise >= without each caller re-deriving which
	// way each column points. ok is false when the corpus gives the metric
	// nothing to measure, which is the answer silence must get, rather than a
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
// tally on every component of the metric.
//
// The reference is a reviewer that is correct, so this answers "did
// this strategy do as well as being right?", which is the question the withdrawn
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
			Renderings: [][]string{{"RECALL", "NOISE", "ANCHOR", "L/DEF"}},
			Doc: "how much of what was planted the reviewer found, how much it invented, how " +
				"precisely it said where to look at its vaguest, and how much of the file it asked " +
				"a reader to read per defect it found",
			Score: func(t CorpusTally) ([]float64, bool) {
				if t.Planted == 0 || t.Samples == 0 {
					return nil, false
				}
				// Noise is negated so that higher is better in every position;
				// per sample, because that is how the tables print it and
				// because a corpus of more fixtures would otherwise look noisier.
				//
				// The anchor width is negated for the same reason and not
				// divided: it is a worst case, not a rate. See
				// CorpusTally.WidestAnchor for the strategy it exists to catch.
				//
				// The spread is negated and IS divided, per defect located. It
				// is zero exactly when nothing was located, since every located
				// defect is claimed by at least one line, so the one strategy
				// that scores its best value here is one scoring 0 on the
				// component beside it, which is what makes the group a group.
				// The rendered cell says n/a there rather than 0.00: a cell is
				// read alone and a component never is.
				spread, _ := t.Spread()

				return []float64{
					float64(t.Matched) / float64(t.Planted),
					-float64(t.Noise) / float64(t.Samples),
					-float64(t.WidestAnchor),
					-spread,
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
					// inflation and no understatement, an exact tie with a
					// perfectly calibrated reviewer over the whole corpus, on
					// four of its twenty-nine plants. Selective silence is not
					// calibration, and the previous shape of this metric could
					// not tell the two apart. The denominator makes the
					// selection visible: 4/29 against 29/29.
					float64(graded) / float64(t.Planted),
				}, true
			},
		},
	}
}

// PublishedDescription is a model-free artifact a report publishes that is not
// a
// score. "What maximises this?" has no answer for a page of text, so the
// question
// asked here is the one that does: CAN A REVIEWER nobody WOULD SHIP PRODUCE
// THE
// DESCRIPTION A CALIBRATED ONE PRODUCES?
//
// The note behind it is in docs/measurement.md#publisheddescription.
type PublishedDescription struct {
	// Name is how the artifact is referred to in a failure message and in the
	// degenerate table's declarations.
	Name string

	// Doc says what the artifact claims to describe. A degenerate strategy that
	// produces a calibrated reviewer's page is a counterexample to this sentence.
	Doc string

	// Render produces the artifact for one reviewer's tally, with no model,
	// judge or network, the same constraint PublishedMetric.Score is under, and
	// for the same reason: a synthetic reviewer has to be able to produce one.
	Render func(CorpusTally) string
}

// PublishedDescriptions is every model-free artifact these reports publish that
// is not a score.
func PublishedDescriptions() []PublishedDescription {
	return []PublishedDescription{
		{
			Name: "severity vocabulary",
			Doc: "which severity words the reviewer printed against each planted level, with the " +
				"planted count per level beside them, so two vocabularies can be compared by eye " +
				"without a figure pretending they are commensurable",
			Render: func(t CorpusTally) string {
				// One fixed row name, so two strategies' pages differ only where
				// their severity behaviour does. A name in the output would make
				// every comparison trivially unequal and the crossing vacuous.
				return SeverityVocabularyBlock([]VocabularyRow{{
					Name:    "reviewer",
					Usage:   t.Severity.Usage(),
					Planted: t.Severity.Planted,
				}})
			},
		},
	}
}

// SeverityCells maps every published column that states a severity reading in
// OUR five levels to the function that renders it.
//
// The note behind it is in docs/measurement.md#severitycells.
func SeverityCells() map[string]func(t CorpusTally) string {
	objective := func(pick int) func(CorpusTally) string {
		return func(t CorpusTally) string {
			infl, under, acc, cov := severityAggregate(t).ObjectiveSeverityCells(t.Samples)
			return []string{acc, infl, under, cov}[pick]
		}
	}

	return map[string]func(CorpusTally) string{
		"O-ACC":   objective(0),
		"O-INFL":  objective(1),
		"O-UNDER": objective(2),
		"O-COV":   objective(3),
		"SEV": func(t CorpusTally) string {
			return severitySummary(t).SeverityCell()
		},
	}
}

// severityAggregate and severitySummary lift a CorpusTally into the two row
// types the tables render from, carrying only the severity fields.
//
// A tally is what a synthetic reviewer can be scored into without a model, a
// judge or a network, see CorpusTally, so it is the input a guard can drive
// the real renderers with.
func severityAggregate(t CorpusTally) Aggregate {
	out := Aggregate{
		SevAccurate:    t.Severity.Accurate,
		SevInflated:    t.Severity.Inflated,
		SevUnderstated: t.Severity.Understated,
		SevPlanted:     t.Planted,
	}

	// Through DeclareScale rather than as a literal field. The tally's scale is
	// already the fold commonScale produced, so this particular lift could set
	// it directly and be right, but "this one is safe" is the reasoning that
	// left one of the two judged paths without the disagreement check for a
	// round. One way in, no exceptions to audit.
	out.DeclareScale(t.Scale)
	return out
}

func severitySummary(t CorpusTally) Summary {
	return Summary{
		Runs:           t.Samples,
		Matched:        t.Matched,
		Total:          t.Planted,
		SevAccurate:    t.Severity.Accurate,
		SevInflated:    t.Severity.Inflated,
		SevUnderstated: t.Severity.Understated,
		Scale:          t.Scale,
	}
}

// tableKind says whether a header's columns are classified by this file.
type tableKind int

const (
	// tableScored is a table whose every column must be a registered score, an
	// LLM judge's opinion, or an identifier, and whose metrics must be printed
	// complete. Choosing it subjects the table to the column-registration and
	// whole-metric guards.
	tableScored tableKind = iota

	// tableUnscored is a table whose columns are readings another track defines,
	// cost accounting, judge-swap agreement. Choosing it is a CLAIM: that this
	// file cannot classify those columns without asserting things it does not
	// compute. They are still subject to every guard that runs over all headers,
	// which is what the cross-tool severity withdrawal needs.
	tableUnscored
)

// registeredTableHeaders is every table header this package prints, populated
// by
// declaration.
//
// The note behind it is in docs/measurement.md#registeredtableheaders.
var registeredTableHeaders []struct {
	kind   tableKind
	header string
}

// registerTableHeader records a header and returns it, so that a declaration
// registers itself.
func registerTableHeader(kind tableKind, header string) string {
	registeredTableHeaders = append(registeredTableHeaders, struct {
		kind   tableKind
		header string
	}{kind, header})
	return header
}

// The headers of every table the eval reports print.
//
// The note behind it is in docs/measurement.md#the-headers-of-every-table-the-eval-reports-print.
var (
	// SummaryTableHeader is the ground-truth battery's table: no judge, no
	// foreign reviewer, one row per model and fixture.
	SummaryTableHeader = registerTableHeader(tableScored,
		"MODEL                                FIXTURE                   RUNS  RECALL   "+
			"SEV A/I/U  NOISE  ANCHOR  L/DEF  STABLE  FAILED")

	// VariantTableHeader compares one prompt variant against another over the
	// same corpus.
	VariantTableHeader = registerTableHeader(tableScored,
		"VARIANT                 N     FAIL  FINDINGS  REAL  WORTH  PRECISION  J-INFL  "+
			"J-UNDER  O-INFL  O-UNDER  O-ACC  O-COV  MISCLASS  TONE-OFF  MISSED  SIGNAL  GRADE")

	// JudgedModelTableHeader is the model ranking, and the table the head-to-head
	// against the incumbent is printed in.
	JudgedModelTableHeader = registerTableHeader(tableScored,
		"MODEL                                GRADE  SPREAD  PREC   COV  N    FAIL  "+
			"FIND  WORTH  J-INFL  J-UNDER  O-INFL  O-UNDER  O-ACC  O-COV  RECALL  NOISE  ANCHOR  "+
			"L/DEF  MISCLASS  MISSED  SIGNAL")
)

// The cost and judge-swap tables, registered from here rather than beside
// their
// own declarations.
//
// The note behind it is in docs/measurement.md#the-cost-and-judge-swap-tables-registered-from-here-rather.
var (
	_ = registerTableHeader(tableUnscored, CostTableHeader)
	_ = registerTableHeader(tableUnscored, precisionHeader)
	_ = registerTableHeader(tableUnscored, agreementHeader)
)

// ScoreTableHeaders are the tables that publish a PublishedMetrics reading:
// every column in them is a registered score, an LLM judge's opinion, or an
// identifier, and every metric they touch must be printed complete.
//
// It is not every header this package prints, see AllTableHeaders, and read its
// comment before assuming a guard that runs over these has seen them all.
func ScoreTableHeaders() []string {
	return tableHeaders(tableScored)
}

// AllTableHeaders is every table header this package prints anywhere.
//
// It is derived from the registry rather than listed, so the answer to "have I
// seen them all?" is yes by construction and not by anyone's diligence.
func AllTableHeaders() []string {
	return append(tableHeaders(tableScored), tableHeaders(tableUnscored)...)
}

// tableHeaders returns the registered headers of one kind, in declaration order
// so a failure message reads the same way twice.
func tableHeaders(kind tableKind) []string {
	var out []string
	for _, h := range registeredTableHeaders {
		if h.kind == kind {
			out = append(out, h.header)
		}
	}
	return out
}

// JudgeOpinionColumns are the columns an LLM judge supplies.
//
// They are not exempt from "what maximises this?", PRECISION's own doc comment
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

// DescriptiveColumns identify a row or state how much measurement is behind
// it.
// They are not scores: no reviewer is better for having a larger N.
//
// The note behind it is in docs/measurement.md#descriptivecolumns.
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
	"perfectly calibrated reviewer on O-ACC/O-INFL/O-UNDER while covering 4 plants of 29. Read the " +
	"triple only with O-COV beside it. " +
	"A ROW WHOSE SEVERITY SCALE IS NOT DECLARED AS OURS PRINTS n/a THERE, and the scale is declared by " +
	"the adapter that produced the row rather than inferred from its words: " + IncumbentModel + " " +
	"publishes one 'critical' spanning our critical AND error, so a figure for it would state a " +
	"vocabulary gap rather than review quality in either direction — it cannot score O-ACC on an error " +
	"plant without over-claiming on a critical one, and it prints our own spelling of 'critical' while " +
	"doing it, which is why a shared spelling is not treated as a shared scale. " +
	NoCrossToolSeverityScore + " " +
	"The two groups are counted over DIFFERENT things — J-* per judged finding, O-* per planted " +
	"defect the review actually found — so neither is a share of the other, and a defect nobody " +
	"reported appears in neither. Every count column is divided by N on the same row."
