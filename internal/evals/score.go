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
	// That trade is worth making — a region containing the defect HAS found it —
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

	// The census is a fact about the FIXTURE, so it is taken before anything
	// that can return early. THE BUG THIS FIXES: it was taken inside
	// ScoreSeverity, which the two returns below skip, so a run that failed
	// contributed its plants to Total and nothing to the per-level census. Over
	// AllFixtures with every third run failing, TallyScores reported a census
	// summing to 17 against a planted total of 29 — and the block rendered
	// beside that RECALL cell carried no "(0 of N located): nothing located"
	// line anywhere, which is precisely the absence-is-invisible failure
	// SeverityUsage.Lines exists to close, re-created by an ordinary provider
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

	// EVERY DETECTION READING COMES FROM ScoreDetection, including the located
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
// (fixture, findings) pair for both contenders — Incumbent is shelled out to and
// has no RunResult at all — so for those tables NOISE and ANCHOR were not
// discarded, they were never computed. Splitting the computation out is what lets
// the head-to-head fill the columns from the SAME functions the ground-truth
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

	// Unmatched are the findings that explain no planted defect. On a clean
	// fixture every finding is unmatched by definition.
	Unmatched []review.Finding

	// WidestAnchor is the most DISTINCT LINES this review pointed at about any
	// one thing. See Score.WidestAnchor, which is this same number.
	WidestAnchor int

	// AnchoredLines is how many DISTINCT LINES this review pointed at in total
	// while claiming the defects it LOCATED — the same per-defect union
	// WidestAnchor takes the MAXIMUM of, summed instead.
	//
	// THE MAXIMUM AND THE SUM CATCH DIFFERENT REVIEWERS AND NEITHER SUBSUMES THE
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
	// review actually found. Every located defect contributes at least one line —
	// matches() implies explainsAny(), so the union claiming it is never empty —
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
	// taken because neither subsumes the other — a wide finding on a fixture that
	// plants NOTHING belongs to no defect and would vanish from the second.
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
		}
	}

	return out
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
//   - IT IS MAXIMISED BY A REVIEWER THAT ALSO CHOOSES WHAT TO REPORT. This
//     corpus plants 29 defects over 30 fixtures and bands them 12 blocking, 6
//     medium, 11 low. Reconstructed over AllFixtures, the strategy that stays
//     silent unless the defect is ALREADY blocking and then calls it critical
//     bands a perfect 12/0/0 — an exact tie with a calibrated reviewer on the
//     triple, over 12 of the 29 plants, because every defect it CHOOSES to
//     report is one where its single word happens to land in the right band. A
//     number optimised by stamping "critical" on everything a reviewer bothers
//     to mention would, if anyone optimised it, produce exactly the review bot
//     this project exists not to be.
//
//     TWO FIGURES THIS COMMENT USED TO QUOTE HAVE BEEN CORRECTED, and the second
//     correction weakens half the argument rather than strengthening it, which
//     is why it is written down. It first said "always critical and always error
//     both scored a perfect 10 of 10", scoring those strategies over the plants
//     the INCUMBENT located rather than over what they report. It then said they
//     banded 12 accurate and 2 inflated of 14 against the incumbent's 6 of 10 —
//     true of a fourteen-plant corpus that no longer exists. On the corpus in
//     this tree the two stampers band 12/17/0 of 29 (B-ACC 0.414) against a
//     calibrated reviewer's 29/0/0, so THE STAMPERS NO LONGER TIE and the
//     maximisation argument now rests on the selective reviewer above, which
//     does. Nor does the incumbent locate only blocking plants: it locates 3
//     critical, 7 error and 4 warning, so 10 of the 14 are blocking, and it
//     bands 10/0/4 over them.
//     TestTheSeverityFiguresTheseCommentsQuoteStillReproduce reads every one of
//     those numbers back out of the corpus.
//
//   - IT IS BLIND TO THE DEFECT IT WAS WRITTEN FOR. The parser bug behind the
//     PREVIOUS retraction (crSeverity demoting Incumbent's "critical" to our
//     "error") does not move it at all: buggy parser 10/0/4, fixed parser
//     10/0/4. At our full resolution the same bug moves the incumbent's triple
//     from 6/4/4 to 8/0/6. The banded column could not see the bug it was the
//     remedy for, and by construction could not see it recur.
//
// It was also a free parameter, and here too the figure first published was
// wrong. crSeverity records Incumbent's "major" at warning; recording it at
// error instead, with Incumbent's bytes byte-for-byte unchanged, leaves the
// banded figure at B-ACC 0.714 either way (10/0/4 to 10/4/0) and moves the
// FULL-RESOLUTION triple from 6/4/4 to 5/8/1 over AllFixtures. The numbers
// quoted before — "0.62 to 0.88", then "0.600 to 1.000" — reproduce from
// nothing in this tree. The corrected swing is at full resolution only, which
// is where the published cells are, so the free parameter still moves a
// published number; it is the banded column that turns out to be insensitive to
// this too. TestMajorIsAFreeParameterSoNoCrossToolScoreIsOffered keeps the
// full-resolution half measured rather than remembered, and
// TestTheSeverityFiguresTheseCommentsQuoteStillReproduce pins both halves against the
// corpus. The banded figures are reconstructed inside that test, because the
// instrument that produced them is deleted and a retraction argued from an
// unreproducible measurement is the same defect one level up.
//
// What replaces it is DESCRIPTION, not a score: SeverityUsage reports which
// severity words each reviewer actually PRINTED, against which planted levels, so
// a reader can see a calibration difference without a single number pretending
// the two vocabularies are commensurable. Our own models keep FULL-RESOLUTION
// severity scoring against each other — that comparison is between matching
// vocabularies and nothing here touches it.
//
// That description was itself derived for one round, and the correction belongs
// beside the withdrawal it serves: it published the level crSeverity translated
// each foreign word TO, so the replacement for a figure withdrawn as a function
// of the free "major" constant was a function of the same constant. The words are
// now quoted from the retained review and our reading of them is marked as ours;
// see SeverityUsage.
//
// Re-tuning band boundaries is not the remedy and is not open: three consecutive
// attempts to make cross-tool severity fair (demote at parse time, then band at
// comparison time, then re-tune the bands) each moved a number our way without
// new evidence. The parser fix stays — recording that Incumbent said "critical"
// is correct on its own merits and preserves evidence — it simply does not
// license a comparison.
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
// IT CANNOT BE DERIVED FROM THE WORD. incumbent/cli prints "critical", spelled
// exactly like ours, and the shipped cache credits that word on 2 plants of
// critical and 4 of error: a shared spelling is not a shared scale. A gate built
// on strings.EqualFold(said, recorded) would score the incumbent's "critical"
// findings at our resolution and withdraw only its "major" ones, publishing a
// fraction of the retracted comparison.
//
// NOR FROM THE TRANSLATION FLAG. internal/linters marks EVERY analyzer finding
// translated, and review.Engine marks a model's finding translated when it
// writes "Critical" with a capital C, so the flag answers "was one word
// rewritten" — a fact about a finding — where the question is "what scale did
// this reviewer publish on", a fact about the reviewer. severityWasTranslated
// answers the first question and is used for the [we read as X] marker; this
// type answers the second and is used for the withdrawal.
//
// Undeclared is the zero value and prints n/a, so a contender added without a
// declaration is withheld rather than ranked.
//
// IT REPLACES AN IDENTITY CHECK. The withdrawal used to be
// PublishesOurSeverityLevels(model) == (model != IncumbentModel): a reporter's
// NAME standing in for a fact about its vocabulary. That is live rather than
// hypothetical — internal/linters' mapSeverity translates semgrep's HIGH onto
// our error and its MEDIUM onto warning, which is the exact shape of the first
// retraction, and semgrep's CRITICAL now reaches our critical with the spelling
// unchanged, which is worse rather than better: semgrep's own documentation
// makes ERROR a synonym for HIGH inside ITS scale, so a matching spelling is
// never evidence of a matching scale. Those findings are spared today only
// because evalConfig sets cfg.Linters.Mode = LinterOff. Under the name check
// they would have been published at our resolution the moment anyone turned
// linters on. TestAnUndeclaredScaleIsWithheld and
// TestEverySeverityCellIsWithdrawnForAForeignVocabulary pin the three states.
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

	// Recorded is the level this package scored the finding at — OUR reading,
	// and labelled as ours wherever it differs from Said.
	Recorded config.Severity
}

// SeverityUsage tabulates which severity words a reviewer actually printed for
// the defects it located, against the level each defect was planted at.
//
// Severity across two vocabularies is a CONTINGENCY TABLE, not a number.
//
// The unit is (planted level x the word the reviewer printed), counted, with the
// planted total beside it. It is deliberately not reducible: on the shipped
// cache incumbent/cli's "critical" is credited on plants of critical AND error,
// and its "major" on plants of critical, error AND warning, so no single-valued
// mapping of either word is right for every plant it lands on and no reduction
// of both sides to a common resolution is right either. What a reader gets is
// what was observed; what they do with it is theirs.
//
// TWO INSTRUMENTS PROPOSED IN PLACE OF THAT WERE REJECTED ON MEASUREMENT, and
// the measurements are recorded so the next proposal starts from them.
//
//   - AN INTERVAL — credit a foreign word against the hull of the planted levels
//     it is observed on — is fitted to the observations it is then scored
//     against, so a perfect score is the definition of the fit. Over the shipped
//     cache the incumbent scores 14 accurate / 0 not, and 14/0 BOTH with the
//     crSeverity bug that demoted "critical" and without it, where the
//     point-valued reading moves 6/4/4 to 8/0/6. It is blind to the bug that
//     caused the first retraction.
//   - AN ORDINAL AGREEMENT — score the ORDER a reviewer puts plants in rather
//     than the level it names — is not computable within a review here: of the
//     12 cached reviews that locate anything, exactly one locates defects at two
//     or more distinct planted levels. Pooled over the corpus it is invariant
//     under every order-preserving relabeling, so a reviewer that keeps the
//     planted order and files everything at nit — below every fail_on this
//     project defines — ties a calibrated one. Severity's production function is
//     a threshold, not a permutation.
//
// A reader comparing two vocabularies gets to see, for example, that one
// reviewer answered "critical" to plants of critical AND to plants of error
// while another split them — and gets to decide for themselves what that is
// worth, which is exactly the judgement a single accuracy figure was making
// silently on their behalf and getting wrong.
//
// THE BUG: the inner key used to be the severity this package had RECORDED, and
// the doc comment here claimed both keys were "recorded as the reviewer spelled
// them". They were not. Incumbent prints "critical" and "major" and printed
// neither "error" nor "warning" anywhere in the shipped corpus, yet the published
// block read "planted error (7 located): critical x4, warning x3" — and swapping
// crSeverity's free "major" constant, with the cached bytes untouched, re-rendered
// the same line as "critical x4, error x3". The block that replaced a withdrawn
// score was therefore itself a function of the free parameter the withdrawal was
// justified by, presented as observation. Said is now the reviewer's word and
// Recorded is ours, marked as ours.
//
// The OUTER key is the fixture's planted level, which is ours by construction:
// fixtures.go writes it in our five levels because it is the ground truth those
// levels define.
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
// level — the DENOMINATOR of the contingency table, and the half of it that has
// nothing to do with what the reviewer said.
//
// It is a separate map rather than a field on SeverityUsage because the two are
// counted over different things: a usage row exists only where a defect was
// LOCATED, and this exists wherever a defect was PLANTED.
//
// Summing it gives the planted total the coverage cell divides by — Score.Total
// on one run, Aggregate.SevPlanted on a judged row — so the description and the
// coverage cell are read against the same number. THAT SENTENCE USED TO BE FALSE
// FOR ANY RUN THAT PRODUCED NO REPORT: the census was taken inside
// ScoreSeverity, which ScoreRun skips when a provider errors, so a failed run
// added its plants to the total and nothing to the census. It is now taken from
// the fixture before ScoreRun can return early, which is the only place both
// halves are available whatever the provider did.
// TestEveryPlantedLevelAppearsWithItsDenominator covers a failing corpus as well
// as a clean one, because the flattering direction here is the one only a
// failure produces.
type PlantedLevels map[config.Severity]int

// Add counts one fixture's plants.
func (p PlantedLevels) Add(f Fixture) {
	for _, d := range f.Defects {
		p[d.WantSeverity]++
	}
}

// Merge folds another census in, so a row summed over runs and fixtures carries
// the denominator those runs and fixtures actually planted.
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
// THE BUG THIS FIXES: levels nobody located were omitted, on the reasoning that
// a miss is RECALL's job. Measured, that made the worst strategy's page the
// cleanest one. The reviewer that reports only the plants we rate critical and
// calls them critical rendered
//
//	planted critical (4 located): critical x4
//
// and nothing else — a proper SUBSTRING of a calibrated reviewer's whole block,
// with O-COV blanked in the cell on that same row. Absence is invisible; a
// denominator is not. Under this rendering the same strategy prints four rows
// reading "(0 of N located): nothing located" and stops resembling a calibrated
// reviewer at a glance. TestEveryPlantedLevelAppearsWithItsDenominator asserts
// the rule over AllFixtures, and restoring the omit-empty behaviour fails there.
//
// Each entry is the reviewer's own word. Where this package translated that word
// into one of our five levels the level follows it, marked as ours, so the two
// cannot be read as one statement by the reviewer — "major x2 [we read as
// warning]" says who said what. Our own models translate nothing, so their lines
// carry no marker at all.
func (u SeverityUsage) Lines(planted PlantedLevels) []string {
	var out []string

	// The union, so a level the corpus plants survives a reviewer that never
	// reached it AND a word that landed on a level the census does not carry
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
			// flagging it would bury the translations that are — "major" against
			// a reviewer whose word has no counterpart among our five — in noise
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
// Ordering on OUR level keeps the line legible — it still reads most-severe
// first, as it did when our level was the key — while the spelling tie-break is
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

	// Planted is every level the fixture planted at, counted — including the
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

// severityAsSaid pairs the word a finding's reviewer printed with the level this
// package recorded it at.
//
// The three cases are exhaustive and each is a different claim:
//
//   - RawSeverity is set. Something translated the word and kept the original,
//     so both halves are known and the block prints the original with our
//     reading marked as ours.
//   - Nothing translated this finding. The reporter wrote Severity itself, so
//     that field IS its word, and the two halves are the same string.
//   - Something translated it and the original is gone. Said stays empty and the
//     block says so, because the alternative is quoting the reviewer as having
//     said a word we chose. Two paths reach it: a Incumbent cache entry
//     collected before the raw review was retained, whose stored findings are a
//     previous parser's output; and an analyzer that published no severity at
//     all, where the level is entirely ours and there is no word to quote.
//
// The second case USED TO BE "this is every one of our own models", and that was
// the defect: review.Engine rewrites a model's severity through Normalize, so
// our contenders belonged in the first or third case and were answered into the
// second. See severityWasTranslated.
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
// Ties are broken on the PUBLISHED WORD, not on report order, and the difference
// is not cosmetic. The comment here used to say ties were between findings
// carrying the same severity so order decided only which comment a diagnostic
// NAMED. Ties are on NORMALIZED rank, and the credited finding's word is what
// SeverityUsage records — which is PUBLISHED, as the description that replaced
// the withdrawn cross-tool score. Two findings on one planted-info defect spelled
// "info" and "P1" both normalize to info and tie; [info, P1] published `planted
// info: info x1` and [P1, info] published `planted info: p1 x1`. Same review,
// same verdict, two different published descriptions of the reviewer's
// vocabulary. A recognized spelling wins, then a RECORDED one, then the
// lexicographically smaller one, so the block is a function of the SET of
// findings.
//
// THE MIDDLE RULE IS A FIX, and the bug was that ordering on spelling alone let
// a destroyed word beat a kept one. severityAsSaid returns an empty Said for a
// finding something translated without keeping the original, and "" sorts before
// every real spelling — so on a defect matched by one finding carrying
// RawSeverity "Error" and one carrying none, BOTH report orders published
// `(word not recorded) x1 [we read as error]` while the reviewer's word sat in
// the finding list beside it. That is not hypothetical: internal/linters
// produces findings in exactly that shape — translated, with no raw word —
// whenever the analyzer publishes no severity of its own, which is ruff always
// and golangci-lint until someone configures severity rules. So the
// configuration ourSeverityScale withdraws the SCORE for was still publishing
// our gap phrase over a word the review kept. A gap is what this block prints
// when there is nothing else; it may not outrank something.
//
// The word compared is severityAsSaid's, not Sev(). For a reviewer whose
// severities we translate, Sev() is OUR word and several of the reviewer's words
// map onto it — "major", "warn" and "warning" all land on warning — so ties would
// have been decided on a string that is equal by construction, and report order
// would have chosen which of the reviewer's spellings got published. The tie-break
// has to be on the thing that reaches the page.
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
		// spelling is not a spelling — it renders as UnrecordedWord — so
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
// open-nitpick anchors to one line, so for its own findings this is the plain
// distance it always was. It matters for reviewers that report a region: taking
// the start of "lines 11-12" and calling a defect on line 12 one line away is a
// coincidence that only holds for short spans, and the same reviewer anchored
// the same defect at 7 on one run and 11-12 on the next — start-only scoring
// turned that into the difference between a hit and a miss.
//
// The tolerance still applies OUTSIDE the span, so a wide anchor buys no extra
// slack at its edges. It does mean a reviewer could earn credit by reporting
// "somewhere in this 200-line function", which is why anchoredLines is reported
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

// anchoredLines is how much of a file a finding is pointing at: the number of
// DISTINCT lines its anchor claims, counting its primary region and every region
// in AlsoAt as one set. One for the ordinary single-line anchor.
//
// THE BUG IT FIXES: this measured the widest SINGLE region, and the ANCHOR column
// is the only thing standing between a precise reviewer and one that gestures at
// a whole file. A finding naming 38 separate one-line regions scored 1 —
// identical to a reviewer that anchored one comment on one line — while being
// credited with every defect in the file, because anchorDistance takes the MIN
// over those regions. crParseAlsoApplies already emits exactly that shape, so
// this was not a hypothetical strategy but a parse away from a real one.
//
// Three answers to "how much of the file is this finding pointing at" were
// available, and the union is the honest one:
//
//   - THE WIDEST REGION is what was here. It answers a different question —
//     how long is the longest thing it pointed at — and that question has no
//     reader. Someone handed 38 one-line regions has 38 lines to read; being
//     told the answer is 1 is not an approximation of their work, it is
//     unrelated to it.
//   - THE HULL, first line to last, charges for the gaps. A finding naming 3-6
//     and 15-18 has claimed two tight regions, not one sixteen-line smear, and
//     billing it for the eight lines between them invents vagueness it does not
//     have. This was the argument for the widest region and it is correct
//     against the hull. It is not an argument for the widest region against the
//     union.
//   - THE UNION counts every line claimed once and no line that was not. It is
//     the only one of the three that is a function of what the reviewer actually
//     said, and it is exactly the reader's work.
//
// The decisive asymmetry is with anchorDistance, which takes the MIN over the
// same regions: every region a finding adds can only ever help it match. A width
// that does not grow with the number of regions therefore sells unlimited
// matching power for nothing, and no tuning of the tolerance can fix that,
// because the two functions would still be reading the same list in opposite
// directions. Under the union each added region costs precisely the lines it
// bought.
//
// It does not move the incumbent. Over the shipped Incumbent corpus the widest
// region and the union differ on exactly one finding — the SQL injection's 3-6
// plus 15-18, four lines against eight — and the corpus maximum both ways is the
// same 13. The change was chosen because it is right, and it is worth recording
// that it cost the competitor nothing, because a scoring change that only ever
// moves numbers our way is one nobody should believe.
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
// that defect, counting every finding that names and sits near it as one set. It
// returns one count per defect, in the order they are planted.
//
// PER DEFECT RATHER THAN AS A MAXIMUM, and the maximum is taken by the caller.
// Both published readings of it are folds over this slice — ANCHOR maxes it,
// L/DEF sums the located entries — and computing them from one slice is what
// stops the pair being two answers to "how many lines is this review pointing
// at" that an edit to either can part.
//
// THE BUG IT FIXES, and it is the SAME BUG anchoredLines fixed, spelled as a
// count of findings instead of a count of regions. anchoredLines made a finding
// pay for the lines it claims; per FINDING, so a reviewer that emits one comment
// on every line within the noise tolerance of each plant scored WidestAnchor 1 —
// every anchor is one line — while pointing at seventeen. Run through ScoreRun
// over AllFixtures, that reviewer filed 196 findings against a calibrated
// reviewer's 14, 182 of them on lines holding no defect, and returned
// RECALL 1.000 NOISE 0.000 ANCHOR -1.000 and O-ACC/O-INFL/O-UNDER/O-COV
// byte-identical to the calibrated reference: both PublishedMetrics reported
// Maxes true, an exact tie with being right, on every model-free column these
// reports publish. NOISE could not see it because each comment sits inside the
// tolerance; ANCHOR could not see it because the vagueness was spread across
// findings rather than across regions.
//
// The asymmetry that decided anchoredLines decides this too, at one remove:
// matches() and explainsAny() are satisfied by ANY finding in the review, so
// every extra comment can only ever help a reviewer match and never cost it.
// Charging per finding leaves that free; charging for the union of what they all
// claimed makes seventeen one-line comments cost exactly what one seventeen-line
// gesture costs, which is what they are worth to a reader.
//
// NEAR-AND-NAMING rather than every finding in the file: explainsAny is already
// the package's answer to "is this comment about that defect", and reusing it
// keeps ANCHOR from charging a reviewer for unrelated work elsewhere in the file.
//
// IT COSTS THE INCUMBENT NOTHING. Over the shipped Incumbent corpus the maximum
// is 13 either way — the multi-defect review, unchanged — and no fixture's value
// rises. Recorded because a scoring change that only ever moves numbers our way
// is one nobody should believe, and this one was measured against the competitor
// before it was adopted.
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
// It is deliberately wider than anchorTolerance because the two answer different
// questions. Detection asks whether a comment points AT the defect and is tight
// on purpose. Noise asks whether the comment is about it at all, and a reviewer
// that identified a real bug and anchored it a few lines off has already lost
// the detection credit — counting it as a false positive as well punishes one
// near-miss twice, which is the failure explainsAny was written to avoid.
//
// THE PREVIOUS VALUE WAS 2 * anchorTolerance, JUSTIFIED BY A CLAIM THAT WAS
// FALSE. The comment here said "THE VALUE IS A CHOICE AND THIS CORPUS CANNOT
// CHECK IT", and that reasoning was wrong twice over. It was inert — 0, 1, 2, 4,
// 8, 12 and 20 all left the entire suite green, including 0, at which explainsAny
// becomes STRICTER than matches and the double penalty this comment spends its
// first paragraph rejecting comes back. And the corpus can check it, by a
// question nobody had asked: on how many planted fixtures is a spammer charged
// NOTHING because the whole file fits inside the radius? At 8 the answer was two
// of twelve — contract-break and data-loss-migration were 15 and 13 lines, so
// "the comment is near the defect it names" reduced there to "the file is
// shorter than 17 lines", and the column measured nothing at all on them.
//
// So the value is now the LARGEST tolerance meeting both bounds, and both are
// measured rather than argued:
//
//   - STRICTLY GREATER THAN anchorTolerance, or explainsAny and matches ask the
//     same question and a near-miss is punished twice — once by losing the
//     detection and again by being counted as invented.
//   - SMALL ENOUGH THAT NO PLANTED FIXTURE IS ENTIRELY INSIDE IT, or the column
//     is vacuous on that fixture and a spammer there is free.
//
// The largest rather than the smallest, because the trade this constant exists
// to make wants the tolerance as generous as the corpus will support: false
// noise is the accepted error, and every line of slack is a near-miss not
// punished twice.
//
// IT COSTS THE INCUMBENT NOTHING, which is why the bound could be chosen on its
// merits. Over the shipped Incumbent corpus the noise count is 3 at 4, 5, 6, 8
// and 12 alike: every finding there that names a plant's keywords sits at
// distance ZERO from it, so there is no observed misanchoring for this number to
// move. TestTheNoiseToleranceIsPinnedByTheCorpus recomputes both bounds from the
// fixtures, so adding a shorter one fails rather than quietly making the column
// vacuous again.
const noiseTolerance = 6

// explainsAny reports whether a finding describes any planted defect: it names
// the defect AND sits near it.
//
// THE BUG IT FIXES: it ignored line position entirely, so a finding was credited
// against every plant in its file whose keywords its text happened to contain.
// Thirty-six boilerplate one-liners on a grid, each titled "check nil, race and
// secret handling", scored NOISE 0 against three plants — the same value a
// perfectly calibrated reviewer gets — while pointing at nothing. A column whose
// best value is reachable by saying nothing useful is not a column.
//
// WHICH ERROR THIS TRADES. The rule now has two ways to be wrong and both are
// real:
//
//   - FALSE NOISE. A correct finding anchored more than noiseTolerance from its
//     defect is counted as invented. This is the error the position-free version
//     existed to avoid, and it is the one now accepted.
//   - FALSE SIGNAL. Boilerplate nowhere near a defect is counted as explaining
//     it. This is what was happening.
//
// False noise was chosen because it is the bounded and the honest-direction
// error. Bounded: it costs a reviewer at most one count per misanchored finding,
// and that finding has ALREADY been ruled not to point at the bug by the
// detection tolerance, so the two verdicts now agree instead of contradicting
// each other. Honest-direction: false noise makes a reviewer look worse than it
// is and false signal makes it look better, and this package has twice had to
// retract an instrument that flattered. A reviewer that anchors loosely will see
// a higher NOISE than it deserves; that is visible in a column read beside
// RECALL, whereas the strategy the old rule admitted was invisible by
// construction.
//
// A finding must name and be near THE SAME defect. Matching one plant's keywords
// while sitting beside a different plant is not an explanation of either.
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
	// these runs located, printed as L/DEF against Matched. See
	// DetectionScore.AnchoredLines.
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

// RateLegend is printed under every table that carries a rate.
//
// A rate over a small denominator is a quotient of two small integers wearing
// three decimal places. 7/8 and 3/6 are the same facts as 0.88 and 0.50, and the
// first pair tells a reader something the second hides: that one is eight
// observations and the other six, that neither can move by less than an eighth
// or a sixth, and that a gap of 0.05 between two such rows is not a result.
//
// This is the cheapest honest thing this harness can do and it went unsaid for
// three rounds while the reports printed percentages to two decimals over
// fourteen planted defects. It costs a sentence.
const RateLegend = "EVERY RATE HERE IS A QUOTIENT OF TWO SMALL INTEGERS, AND THE COUNTS ARE PRINTED " +
	"BESIDE IT FOR THAT REASON. A rate measured over d observations moves only in steps of 1/d, so " +
	"two rows differing by less than 1/d are not distinguishable by it — the decimals are arithmetic, " +
	"not resolution. Read the counts first and treat the quotient as a convenience."

// CorpusResolution states, for the corpus actually under measurement, the
// smallest difference each published rate can express.
//
// It is DERIVED from the fixtures rather than written down. The claim "this
// corpus cannot resolve a difference smaller than one defect" is a fact about
// how many defects are planted, and the last hand-maintained sentence in this
// package — the one naming which words a reviewer prints — was stale by two
// rounds when it was found. A note that goes stale the moment the corpus grows
// is worse than none, because it will be believed.
//
// IT NAMED A DENOMINATOR NO PRINTED CELL HAS. The note said "RECALL therefore
// moves in steps of 1/<planted across the whole corpus>" and was printed under
// two tables, in neither of which that is a cell's step. Under the summary table
// RECALL is per (model, fixture) and its denominator is that fixture's plants
// times the run count — eleven of these fifteen fixtures plant exactly one, so
// the coarsest cell moves in steps of 1.000, fourteen times what the note
// claimed. Under the judged table RECALL is per CONTENDER, pooled over every
// review folded into the row, so its step is 1/(the plants those reviews saw):
// the corpus-wide figure TIMES THE RUN COUNT for a contender that covered the
// corpus once per fixture, larger with more runs and smaller with less coverage.
// The run multiplier is stated because the sentence here used to offer a
// dichotomy — equal, or smaller — that omitted the case the benchmark is
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
// It is the ground-truth table's rendering of the same reading the judged tables
// spread over O-ACC/O-INFL/O-UNDER, so it passes the same vocabulary gate:
// "n/a" for a row that does not publish our five levels. The cell used to be
// formatted inline at the table, which meant the withdrawal lived in ONE of the
// metric's two renderings. That the battery printing this table happens to run
// no foreign reviewer today is a property of a caller, not of the withdrawal —
// and the guard has already been defeated once by a table the mechanism did not
// know about. SeverityCells registers both renderings so neither can be the one
// that got missed.
//
// Empty, not zero and not n/a, when nothing was graded: a clean fixture plants
// no severity to compare against, and 0/0/0 there reads as "nothing was wrong"
// rather than "nothing was measured". n/a is reserved for the vocabulary
// withdrawal so the two reasons a cell is blank stay distinguishable.
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
	// Samples is how many reviews were folded in — the denominator the tables
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
	// without it RECALL and NOISE are BOTH maximised by one finding per file
	// covering the whole file, titled with every keyword in it: anchorDistance
	// is zero anywhere inside a span, so such a blob is credited with every
	// plant in the file and is noise for none. It scored RECALL 1.000 NOISE 0 —
	// an exact tie with a perfectly calibrated reviewer — while pointing at
	// nothing more useful than "there is a nil deref, an SQL injection and a
	// hardcoded secret somewhere in this file". Score.WidestAnchor was written
	// for exactly that trade and was reported only in a t.Logf, so no published
	// column could see it.
	//
	// The version of that strategy this column could NOT see was the same blob
	// spelled as a list of one-line regions instead of one span. It measured the
	// widest single region, so 38 scattered lines read as 1 and the guard passed
	// on the behaviour it was added to catch. anchoredLines is why it now reads
	// 36.
	//
	// THE MAX AND THE SUM ARE BOTH PUBLISHED, and the argument for the max used
	// to be written one-sidedly: "one blob anywhere in the corpus is the
	// behaviour being caught, and a mean over precise findings hides it." That
	// half is true and is why this field exists. The converse is equally true and
	// went unsaid — a MAX hides UNIFORM vagueness, because a reviewer that is
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
	// finding becomes a width every finding may spend. See
	// DetectionScore.AnchoredLines.
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
				// The anchor width is negated for the same reason and NOT
				// divided: it is a worst case, not a rate. See
				// CorpusTally.WidestAnchor for the strategy it exists to catch.
				//
				// The spread is negated and IS divided, per defect located. It
				// is zero exactly when nothing was located, since every located
				// defect is claimed by at least one line — so the one strategy
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
					// inflation and no understatement — an exact tie with a
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

// PublishedDescription is a model-free artifact a report publishes that is NOT a
// score. "What maximises this?" has no answer for a page of text, so the question
// asked here is the one that does: CAN A REVIEWER NOBODY WOULD SHIP PRODUCE THE
// DESCRIPTION A CALIBRATED ONE PRODUCES?
//
// It is registered for the same reason PublishedMetric is. The severity
// vocabulary block is the artifact that REPLACED a withdrawn score, so it
// inherits the question that score failed — and it inherits it unasked unless
// something asks. TestNoDegenerateReviewerCanMaxOutAPublishedMetric crosses these
// with the same table of reviewers nobody would ship, and a strategy whose page
// is byte-identical to a calibrated reviewer's must be DECLARED there, exactly as
// a metric it can max out must be.
//
// Byte-identity rather than a distance: the artifact is compared by eye, so the
// only question this mechanism can honestly ask of it is whether the two pages a
// reader would compare are the same page.
type PublishedDescription struct {
	// Name is how the artifact is referred to in a failure message and in the
	// degenerate table's declarations.
	Name string

	// Doc says what the artifact claims to describe. A degenerate strategy that
	// produces a calibrated reviewer's page is a counterexample to this sentence.
	Doc string

	// Render produces the artifact for one reviewer's tally, with no model,
	// judge or network — the same constraint PublishedMetric.Score is under, and
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
// It exists because the withdrawal is only as complete as the number of places
// that apply it, and that count was one. The objective-severity metric is
// printed two ways — spread over O-ACC/O-INFL/O-UNDER/O-COV in the judged
// tables, and folded into a single SEV a/i/u cell in the ground-truth table —
// and only the first was gated. The second formatted the counters inline at the
// table, so "the foreign row prints n/a" held in one rendering of one metric
// because a caller happened not to put a foreign row in the other.
//
// TestEverySeverityCellIsWithdrawnForAForeignVocabulary derives the required
// keys from PublishedMetrics rather than from a list here: a column of the
// objective-severity metric that is not also part of a vocabulary-free metric
// must have a renderer, and every renderer must answer n/a for a row whose scale
// is not ours AND for a row that declared none. Adding a third rendering
// therefore fails until it is gated, which is what "self-enforcing" has to mean
// after a hand-maintained list shipped the banded column.
//
// THE ROW'S DECLARED SCALE IS WHAT THE RENDERERS READ, not the contender's name.
// These took a model string and compared it against IncumbentModel, so the
// withdrawal was an identity check standing in for a fact about a vocabulary;
// see SeverityScale.
//
// The renderers call the SAME functions the tables call. That is the load-bearing
// property and also the limit: this proves the gate is in the renderer, not that
// a table used the renderer. TestEveryHeaderPrintsAWholeMetric ties a header to
// its metric, and TestNoReportFormatsSeverityCountersDirectly ties the tables to
// these functions.
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
// judge or a network — see CorpusTally — so it is the input a guard can drive
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
	// it directly and be right — but "this one is safe" is the reasoning that
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

	// tableUnscored is a table whose columns are readings another track defines
	// — cost accounting, judge-swap agreement. Choosing it is a CLAIM: that this
	// file cannot classify those columns without asserting things it does not
	// compute. They are still subject to every guard that runs over all headers,
	// which is what the cross-tool severity withdrawal needs.
	tableUnscored
)

// registeredTableHeaders is every table header this package prints, populated by
// declaration.
//
// THE BUG: registration was a hand-maintained list, twice over. AllTableHeaders
// returned three headers while its predecessor's doc comment claimed to cover
// "every published table header", so appending `B-ACC` to CostTableHeader passed
// both the registration guard and the cross-tool severity guard — the withdrawn
// instrument could be reinstated in a table the mechanism did not know existed.
// That was patched by adding the missing three to the list and adding a second
// hand-maintained list, a name-to-value map, to check the first. A list that has
// to be maintained is how the banded column shipped, and two of them is not a
// fix.
//
// Declaring a header is now the only way to have one: registerTableHeader
// returns the value, so the declaration IS the registration and there is nothing
// to remember. TestEveryTableHeaderInThePackageIsRegistered enforces the one
// remaining thing a compiler cannot — that no declaration bypasses the
// registrar, and that no table is built from a string that was never declared as
// a header at all.
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
//
// They are vars rather than consts because a const cannot call the registrar,
// and an unregistered header is a table every guard here is blind to.
var (
	// SummaryTableHeader is the ground-truth battery's table: no judge, no
	// foreign reviewer, one row per model and fixture.
	SummaryTableHeader = registerTableHeader(tableScored,
		"MODEL                                FIXTURE                   RUNS  RECALL   "+
			"SEV A/I/U  NOISE  ANCHOR  L/DEF  STABLE  FAILED")

	// VariantTableHeader is the prompt-variant comparison.
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

// The cost and judge-swap tables, registered from here rather than beside their
// own declarations.
//
// Their columns are readings those tracks define (CostReadingLegend,
// PublishedCostReadings) and classifying them from this file would assert things
// about accounting it does not compute — hence tableUnscored.
//
// THIS COMMENT USED TO NAME AN OPEN GAP THAT HAD ALREADY BEEN CLOSED: it said
// CostTableHeader printed RECALL with no NOISE column beside it and handed that
// to the cost track. The cost track closed it — the header carries RECALL, NOISE
// and ANCHOR, and PublishedCostReadings scores all three — while this file went
// on publishing the gap as current, and cost.go cited this comment as the reason
// it did so. Two files describing each other's state is how a claim outlives the
// thing it was about; nothing tests a comment, so it stays wrong silently.
//
// tableUnscored REMAINS A SELF-DECLARED EXEMPTION, and that is the real caveat
// to state here. TestEveryPublishedColumnIsRegistered and
// TestEveryHeaderPrintsAWholeMetric run over ScoreTableHeaders only, so a header
// registered with this kind may publish O-ACC without O-INFL/O-UNDER/O-COV, or
// RECALL without NOISE/ANCHOR, with every test green — which is the shape those
// rules exist to stop. It is checked by hand at the declaration site, and a
// declaration site is not a mechanism.
//
// ONE SUCH GAP IS OPEN NOW AND IS DISCLOSED RATHER THAN CLOSED. The detection
// metric gained a fourth column, L/DEF, and the cost table carries RECALL, NOISE
// and ANCHOR without it. Under tableScored that would fail the whole-metric
// guard; under this kind nothing asks. The cost columns are the cost track's own
// reading — PublishedCostReadings scores them against its own degenerate table,
// over PRICED reviews rather than over judged ones — and threading a per-defect
// anchor sum through modelSpend means extending priced-run accounting and its
// comparability machinery to carry a number that is not about dollars. That is
// the named cost of closing it. The direction of the gap: a reviewer hedging
// every anchor is charged for it in the two SCORED tables and not in the cost
// one, so a reader who ranks on $/DEFECT alone is the one who cannot see it.
//
// Registering them here keeps their files untouched. Go initializes these after
// the values they name, so the registry is complete before any test reads it.
var (
	_ = registerTableHeader(tableUnscored, CostTableHeader)
	_ = registerTableHeader(tableUnscored, precisionHeader)
	_ = registerTableHeader(tableUnscored, agreementHeader)
)

// ScoreTableHeaders are the tables that publish a PublishedMetrics reading:
// every column in them is a registered score, an LLM judge's opinion, or an
// identifier, and every metric they touch must be printed complete.
//
// It is NOT every header this package prints — see AllTableHeaders, and read its
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
