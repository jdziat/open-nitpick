package evals

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/review"
)

// foldDetection folds a reviewer's whole corpus into one Aggregate exactly as
// the judged batteries do: AddSeverity and AddDetection at one site, over one
// set of reviews, so the row's columns share a sample count.
func foldDetection(corpus []Fixture, reviewer func(Fixture) []review.Finding, scale SeverityScale) Aggregate {
	var a Aggregate
	for _, f := range corpus {
		findings := reviewer(f)
		a.DeclareScale(scale)
		a.AddSeverity(f, ScoreSeverity(f, findings))
		a.AddDetection(ScoreDetection(f, findings))
	}
	return a
}

// halfCalibrated reports every OTHER planted defect, correctly.
//
// A reviewer that finds everything scores RECALL 1.00 and a reviewer that finds
// nothing scores 0.00, and both of those are values an arithmetic mistake can
// also produce. Half is the reading that has to be computed to be right.
func halfCalibrated(f Fixture) []review.Finding {
	all := calibratedReview(f)

	var out []review.Finding
	for i := range all {
		if i%2 == 0 {
			out = append(out, all[i])
		}
	}
	return out
}

// degenerateNamed returns one strategy from the published degenerate table,
// rather than re-writing it here.
//
// Rule 6c-i: the strategies this package scores are real reviewers put through
// the real scorer, because the hand-written literals that preceded them declared
// numbers the scorer disagreed with.
func degenerateNamed(t *testing.T, want string) func(Fixture) []review.Finding {
	t.Helper()

	for _, d := range degenerateReviewers() {
		if d.name == want {
			return d.review
		}
	}

	t.Fatalf("the degenerate table no longer declares %q, so this test would be scoring a strategy "+
		"it wrote for itself rather than the one the package publishes", want)
	return nil
}

// TestRecallAndCoverageAreOneReadingOfOneCorpus pins the identity
// Aggregate.locatedShare rests on: the defects a review LOCATED, which RECALL
// divides by the plants and which O-COV divides by the same plants, is one
// integer and not two.
//
// It is asserted against ScoreRun rather than against the two cells alone. Both
// cells come from locatedShare, so comparing them to each other would pass on
// any expression whatever, including a wrong one, what makes the identity worth
// anything is that ScoreSeverity grades a defect exactly when ScoreRun counts it
// as detected, and that is a property of two functions in score.go which this
// test reads directly.
func TestRecallAndCoverageAreOneReadingOfOneCorpus(t *testing.T) {
	corpus := AllFixtures()

	agg := foldDetection(corpus, halfCalibrated, OurSeverityScale)

	matched, planted := 0, 0
	for _, f := range corpus {
		s := ScoreRun(RunResult{
			Report: &review.Report{Findings: halfCalibrated(f)},
			Scale:  OurSeverityScale,
		}, f)
		matched += s.Matched
		planted += s.Total
	}

	if planted == 0 || matched == 0 || matched == planted {
		t.Fatalf("the reviewer under test located %d of %d planted defects; at 0 or at all of them "+
			"this test passes on a rate that was never divided", matched, planted)
	}

	if agg.SevGraded() != matched {
		t.Errorf("the judged row says %d defects were located and ScoreRun says %d over the same "+
			"reviews. RECALL and O-COV are rendered from SevGraded, so a divergence here means the "+
			"table publishes a located-defect count the scorer disagrees with",
			agg.SevGraded(), matched)
	}
	if agg.SevPlanted != planted {
		t.Errorf("the judged row says %d defects were planted and ScoreRun says %d; the two rates "+
			"sharing a denominator is what makes them one reading", agg.SevPlanted, planted)
	}

	recall, _, _, _ := agg.DetectionCells()
	_, _, _, cov := agg.ObjectiveSeverityCells(len(agg.Grades))

	if recall != cov {
		t.Errorf("RECALL renders %q and O-COV renders %q on one row for one contender. They are the "+
			"same quantity three columns apart, and two spellings of it are two answers free to "+
			"drift", recall, cov)
	}
	if want := fmt.Sprintf("%.2f", float64(matched)/float64(planted)); recall != want {
		t.Errorf("RECALL renders %q over %d located of %d planted; %q is the quotient",
			recall, matched, planted, want)
	}
}

// TestTheDetectionColumnsAreFilledForAForeignVocabulary is the head-to-head's
// half of the withdrawal, from the side the withdrawal does not cover.
//
// What was retracted is a comparison of two severity LADDERS, and the four O-*
// cells carry it. Defects located, findings invented and lines pointed at are
// counted against the fixtures' own plants, in nobody's severity vocabulary, so
// blanking them for the incumbent would withdraw a measurement that is defined
// and computed, the opposite error from the one the retraction fixed. Rule 14's
// conditions 3 and 4 are read off these cells on exactly this row.
//
// It is the only test that turns red when DetectionCells is gated on the severity
// vocabulary, which took a mutation to establish and is why the comment there
// cites this one. The columns are read out of the register so a fifth inherits
// the requirement.
func TestTheDetectionColumnsAreFilledForAForeignVocabulary(t *testing.T) {
	agg := foldDetection(AllFixtures(), halfCalibrated, IncumbentSeverityScale)

	row := JudgedModelRow(IncumbentModel, CrossJudged{Primary: agg}, 0)
	cells := rowCells(t, CrossJudgedModelTableHeader, row)

	for _, col := range []string{"O-ACC", "O-INFL", "O-UNDER", "O-COV"} {
		if cells[col] != "n/a" {
			t.Errorf("the %s cell for a row on %s reads %q; the severity withdrawal is what this "+
				"column is for", col, IncumbentSeverityScale.describe(), cells[col])
		}
	}

	for _, col := range detectionColumns(t) {
		cell, ok := cells[col]
		if !ok {
			t.Errorf("the head-to-head table has no %s column, so Rule 14 cannot be applied to the "+
				"run it governs — which is the failure this column was added for", col)
			continue
		}
		if cell == "" || cell == "n/a" {
			t.Errorf("the %s cell for a row on %s reads %q. It is computable from a cached single "+
				"review — the cache retains the raw text and is re-parsed, so secondary spans reach "+
				"the scorer — and n/a here withdraws a reading that exists",
				col, IncumbentSeverityScale.describe(), cell)
		}
	}
}

// TestRecallAndNoiseCannotSeeAVagueReviewerAndAnchorCan is why the head-to-head
// gained more columns than the two Rule 14 names.
//
// Both strategies below tie a calibrated reviewer on RECALL and on NOISE:
// anchorDistance is zero anywhere inside a span and explainsAny is satisfied by
// any finding near the plant it names, so a reviewer that gestures at the whole
// file is credited with every plant in it and is noise for none. A table
// publishing RECALL and NOISE without ANCHOR ranks them level with a reviewer
// that is right.
//
// Three spellings, because the scorer needs both of its anchor measurements to
// see them all and this column has to inherit both. One span per file and a
// finding carrying thirty-eight secondary regions are counted by anchoredLines,
// which unions a finding's own regions. The second is the shape
// crParseAlsoApplies emits from an "Also applies to" line, which is the
// incumbent's own. Vagueness spread across SEPARATE one-line findings is 1 there,
// and is counted only by defectAnchoredLines, which unions every finding
// claiming one defect.
func TestRecallAndNoiseCannotSeeAVagueReviewerAndAnchorCan(t *testing.T) {
	corpus := AllFixtures()
	good := foldDetection(corpus, calibratedReview, OurSeverityScale)
	goodRecall, goodNoise, goodAnchor, _ := good.DetectionCells()

	for _, name := range []string{
		"one enormous anchor span per file",
		"a line-precise reviewer that also points at every other line",
		"the right words on every line NEAR a defect",
	} {
		vague := foldDetection(corpus, degenerateNamed(t, name), OurSeverityScale)
		recall, noise, anchor, _ := vague.DetectionCells()

		if recall != goodRecall {
			t.Errorf("%q scores RECALL %q against a calibrated %q; this test's premise is that "+
				"RECALL alone does not separate them, and it no longer holds", name, recall, goodRecall)
		}
		if noise != goodNoise {
			t.Errorf("%q scores NOISE %q against a calibrated %q; same premise", name, noise, goodNoise)
		}

		if anchor == goodAnchor {
			t.Errorf("%q and a calibrated reviewer both render ANCHOR %q while tying on RECALL and "+
				"NOISE. Every model-free column in this table then reports a reviewer that points at "+
				"whole files as indistinguishable from one that points at lines", name, anchor)
		}
		if vague.DetWidestAnchor <= good.DetWidestAnchor {
			t.Errorf("%q has a widest anchor of %d lines against a calibrated reviewer's %d; the "+
				"column is oriented so that pointing at more lines about one thing reads worse",
				name, vague.DetWidestAnchor, good.DetWidestAnchor)
		}
	}
}

// hedgeSpan is how wide the hedging reviewers below smear each anchor.
//
// Any width above one exhibits the conflation a MAXIMUM cannot see; this one is
// wide enough to be legible in a failure message. It is not tuned to any
// fixture: the assertions that depend on it check the width they got
// rather than assuming this one.
const hedgeSpan = 13

// hedgedTo returns a reviewer that is right about every defect and points at a
// BLOCK instead of at a line.
//
// It needs no oracle beyond the one every reviewer in this file has: it is the
// calibrated reviewer with each anchor smeared over `span` lines centred on the
// same place. The widening is strictly free, spanDistance is zero anywhere
// inside a span, so matches() and explainsAny() can only improve, which is the
// "somewhere in this function" behaviour anchorDistance's own comment warns
// about, spelled as a reviewer.
func hedgedTo(span int) func(Fixture) []review.Finding {
	return func(f Fixture) []review.Finding {
		out := calibratedReview(f)
		for i := range out {
			out[i].Line = max(1, out[i].Line-span/2)
			out[i].EndLine = out[i].Line + span - 1
		}
		return out
	}
}

// preciseExceptOnce is line-precise everywhere, with ONE finding in the whole
// corpus smeared over the same span hedgedTo uses, the shape ANCHOR's maximum
// reports identically to a reviewer that hedges every anchor.
// TestAUniformlyVagueReviewerIsNotOneWideFinding asserts that identity as its
// own premise before asking whether anything else separates the two.
//
// It is not in the degenerate table because it is not a degenerate reviewer. It
// is a good one that gestured once, and a reader handed its review reads one
// wide comment and n-1 precise ones. That is the comparison the max collapses.
func preciseExceptOnce(corpus []Fixture, span int) func(Fixture) []review.Finding {
	var where string
	for _, f := range corpus {
		if len(calibratedReview(f)) > 0 {
			where = f.Name
			break
		}
	}

	return func(f Fixture) []review.Finding {
		out := calibratedReview(f)
		if f.Name != where || len(out) == 0 {
			return out
		}
		out[0].Line = max(1, out[0].Line-span/2)
		out[0].EndLine = out[0].Line + span - 1
		return out
	}
}

// detectionMetric is the registered detection reading, so these tests ask what
// the package publishes rather than what they list for themselves.
func detectionMetric(t *testing.T) PublishedMetric {
	t.Helper()

	for _, m := range PublishedMetrics() {
		if m.Name == "detection" {
			return m
		}
	}
	t.Fatal("no metric named \"detection\" is published, so these tests are asserting things about " +
		"columns nobody registered")
	return PublishedMetric{}
}

// detectionColumns is the detection metric's published columns, read out of the
// register rather than listed, so a column added there is one these tests
// immediately require to separate something.
func detectionColumns(t *testing.T) []string {
	t.Helper()
	return detectionMetric(t).Columns()
}

// detectionRow renders one folded contender's head-to-head row and returns its
// cells, which is the artifact a reader applies Rule 14 to.
func detectionRow(t *testing.T, name string, agg Aggregate) map[string]string {
	t.Helper()
	return rowCells(t, CrossJudgedModelTableHeader, JudgedModelRow(name, CrossJudged{Primary: agg}, 0))
}

// TestAUniformlyVagueReviewerIsNotOneWideFinding.
//
// ANCHOR IS A MAXIMUM, and A MAXIMUM HIDES UNIFORM VAGUENESS exactly as a mean
// hides one blob. CorpusTally.WidestAnchor justified the max one-sidedly for
// years, "a mean over precise findings hides it", and the converse went
// unstated: a reviewer that smears every anchor over k lines and one that is
// line-precise except for a single k-line comment are the same number, and to a
// reader they are not remotely the same review.
//
// This is not an argument about a hypothetical threshold. It is what makes the
// incumbent-relative reading of Rule 14's condition 4 a per-finding budget: the
// incumbent's ANCHOR is its single worst finding, so "no wider than theirs"
// licenses that width on every finding. The instrument's job is to publish a
// reading that separates the two shapes; the ship rule's own reading of it is a
// separate matter and is recorded in docs/measurement.md rather than retuned.
//
// The columns are read out of the register, so this asks "does anything this
// table publishes tell them apart" rather than naming a column and passing when
// that one column happens to exist.
func TestAUniformlyVagueReviewerIsNotOneWideFinding(t *testing.T) {
	corpus := AllFixtures()

	const span = hedgeSpan

	hedged := foldDetection(corpus, hedgedTo(span), OurSeverityScale)
	once := foldDetection(corpus, preciseExceptOnce(corpus, span), OurSeverityScale)

	if hedged.DetWidestAnchor != span || once.DetWidestAnchor != span {
		t.Fatalf("the two reviewers anchor %d and %d lines at their widest against a span of %d; "+
			"this test's premise is that ANCHOR cannot tell them apart",
			hedged.DetWidestAnchor, once.DetWidestAnchor, span)
	}

	hedgedCells := detectionRow(t, "hedged-everywhere", hedged)
	onceCells := detectionRow(t, "precise-except-once", once)

	var same, differ []string
	for _, col := range detectionColumns(t) {
		if hedgedCells[col] == onceCells[col] {
			same = append(same, fmt.Sprintf("%s %s", col, hedgedCells[col]))
			continue
		}
		differ = append(differ, fmt.Sprintf("%s %s vs %s", col, hedgedCells[col], onceCells[col]))
	}

	for _, col := range []string{"RECALL", "NOISE", "ANCHOR"} {
		if hedgedCells[col] != onceCells[col] {
			t.Fatalf("%s reads %q for a reviewer that smears every anchor over %d lines and %q for "+
				"one that did it once. The premise of this test is that these three tie; it no "+
				"longer holds and the test below is asserting nothing",
				col, hedgedCells[col], span, onceCells[col])
		}
	}

	if len(differ) == 0 {
		t.Errorf("a reviewer that points at %d lines about EVERY defect and one that did it about a "+
			"single defect render identically in every published detection column (%s). The table "+
			"has no reading of how much a reader is asked to read, so a reviewer can spend the "+
			"incumbent's single worst anchor as a budget on all of its findings and no cell moves",
			span, strings.Join(same, ", "))
	}
}

// TestTheIncumbentsWorstAnchorIsNotABudgetEveryFindingMaySpend.
//
// The same conflation, read the way the ship decision reads it: against the
// INCUMBENT rather than against a calibrated reviewer. The package's own
// degenerate guard scores every strategy against `calibratedReview`, and Rule 14
// does not, its four conditions threshold against what Incumbent did. A
// reviewer hedged to exactly the incumbent's own worst anchored span ties or
// beats it on every published rate and on the worst case, because the worst case
// is where its budget came from.
//
// Both sides here are the real scorer over the real cache: the incumbent's
// findings are re-parsed from testdata/incumbent through CachedIncumbent, and
// no paid battery is involved.
func TestTheIncumbentsWorstAnchorIsNotABudgetEveryFindingMaySpend(t *testing.T) {
	var covered []Fixture
	for _, f := range AllFixtures() {
		if _, ok := CachedIncumbent(crCacheDir, f); ok {
			covered = append(covered, f)
		}
	}
	if len(covered) == 0 {
		t.Skip("no cached incumbent review, so there is no incumbent reading to threshold against")
	}

	theirs := tallyOver(covered, func(f Fixture) []review.Finding {
		cached, _ := CachedIncumbent(crCacheDir, f)
		return cached
	})

	span := theirs.WidestAnchor
	if span < 2 {
		t.Skipf("the cached incumbent's worst anchored span is %d line(s); there is no budget to "+
			"spend and nothing here to demonstrate", span)
	}

	ours := tallyOver(covered, hedgedTo(span))

	// The premise, and it is Rule 14's conditions 1, 3 and 4 in the units the
	// rule states them in. If any of these stops holding, the strategy no longer
	// passes the ship rule and the assertion below is about nothing.
	if ours.Matched < theirs.Matched {
		t.Fatalf("the hedged reviewer located %d plants to the incumbent's %d, so it fails the ship "+
			"rule on locate count and this test is asserting nothing", ours.Matched, theirs.Matched)
	}
	if ours.Samples == 0 || theirs.Samples == 0 {
		t.Fatal("one side was folded over no review at all")
	}
	if a, b := float64(ours.Noise)/float64(ours.Samples),
		float64(theirs.Noise)/float64(theirs.Samples); a > 1.5*b {
		t.Fatalf("the hedged reviewer invents %.4f findings per review against the incumbent's %.4f, "+
			"so it fails the ship rule on noise and this test is asserting nothing", a, b)
	}
	if ours.WidestAnchor > theirs.WidestAnchor {
		t.Fatalf("the hedged reviewer's worst anchor is %d lines against the incumbent's %d; it was "+
			"built from theirs, so this is an arithmetic bug rather than a result",
			ours.WidestAnchor, theirs.WidestAnchor)
	}

	// Asked of the REGISTERED metric with the incumbent as the reference, which
	// is the substitution the package's own degenerate guard does not make: that
	// guard's only reference is calibratedReview, and Rule 14 thresholds against
	// what Incumbent did.
	detection := detectionMetric(t)
	maxed, defined := detection.Maxes(ours, theirs)
	if !defined {
		t.Fatalf("the detection metric is undefined for one of the two sides (ours %v, theirs %v)",
			ours, theirs)
	}
	if maxed {
		t.Errorf("a reviewer that smears every anchor over the incumbent's own worst span (%d lines) "+
			"scores at least as well as the incumbent on EVERY component of the %q metric (columns "+
			"%s), while pointing a reader at far more of each file per defect it found. The worst "+
			"case is a maximum, so thresholding it against the incumbent hands every finding a "+
			"budget the incumbent spent once — and no published column charges for spending it",
			span, detection.Name, detection.Label())
	}
}

// TestEveryDetectionRateIsPrintedWithTheCountsItCameFrom is Rule 6c for the four
// detection columns: a score is read as a group and the group includes its
// denominator.
//
// The cells are fixed-width and a count pair outgrows one, so the counts go in
// the DENOMINATORS block beneath the table. The arrangement the cost table
// arrived at after inlining them misaligned every column to their right. ANCHOR
// is not a rate and has no denominator to divide by; what it needs printed is the
// number of DRAWS its maximum was taken over, since a maximum over more draws is
// weakly larger. L/DEF's denominator is RECALL's numerator, which is why the pair
// has to appear on one line.
func TestEveryDetectionRateIsPrintedWithTheCountsItCameFrom(t *testing.T) {
	agg := foldDetection(AllFixtures(), halfCalibrated, OurSeverityScale)
	counts := agg.DetectionCounts()

	recall, noise, anchor, spread := agg.DetectionCells()
	if recall == "n/a" || noise == "n/a" || anchor == "n/a" || spread == "n/a" {
		t.Fatalf("the fold produced no reading to print counts for: RECALL %q NOISE %q ANCHOR %q "+
			"L/DEF %q", recall, noise, anchor, spread)
	}

	for _, want := range []string{
		fmt.Sprintf("RECALL %d/%d defects", agg.SevGraded(), agg.SevPlanted),
		fmt.Sprintf("NOISE %d invented finding(s) over %d review(s)", agg.DetNoise, agg.DetReviews),
		fmt.Sprintf("ANCHOR %d line(s), a worst case over those %d review(s)",
			agg.DetWidestAnchor, agg.DetReviews),
		fmt.Sprintf("L/DEF %d line(s) over %d located defect(s)",
			agg.DetAnchoredLines, agg.SevGraded()),
	} {
		if !strings.Contains(counts, want) {
			t.Errorf("the DENOMINATORS line does not carry %q, so a rate in the table above it is "+
				"published without the counts it steps by:\n%s", want, counts)
		}
	}
}

// TestADetectionCellIsBlankRatherThanFlatteringWhenNothingWasMeasured.
//
// Zero invented findings, a zero-line anchor and a zero spread are the BEST
// values in their columns. They are what a reviewer that said nothing earns, so
// a row no review was folded into rendering 0.00 in any of them would publish the
// top of the column for an absence of measurement. That is the shape of failure
// this package has retracted an instrument over twice, and n/a is what keeps the
// two states apart.
//
// L/DEF is blank under a STRICTER condition than the other three, and the extra
// case is the one that matters for Rule 6d: a row that was measured over real
// reviews and LOCATED nothing has a numerator of zero, so a printed 0.00 would
// hand the column's best value to the strategy this package spends the most
// effort not rewarding.
func TestADetectionCellIsBlankRatherThanFlatteringWhenNothingWasMeasured(t *testing.T) {
	var empty Aggregate

	recall, noise, anchor, spread := empty.DetectionCells()
	for _, tc := range []struct{ col, cell string }{
		{"RECALL", recall}, {"NOISE", noise}, {"ANCHOR", anchor}, {"L/DEF", spread},
	} {
		if tc.cell != "n/a" {
			t.Errorf("with no review folded in, %s renders %q rather than n/a", tc.col, tc.cell)
		}
	}

	// And a row that was measured and invented nothing still prints
	// its zero: 0.00 earned over reviews is a reading, and blanking it would
	// hide the difference between the two rows this test is about.
	clean := foldDetection([]Fixture{cleanFixtureForDetection()}, func(Fixture) []review.Finding {
		return nil
	}, OurSeverityScale)
	if _, got, _, _ := clean.DetectionCells(); got != "0.00" {
		t.Errorf("a silent reviewer over one clean review renders NOISE %q; it invented nothing over "+
			"one review, which is a measurement of 0.00 and not an absence of one", got)
	}

	// A reviewer measured over a PLANTED corpus that located nothing: NOISE and
	// ANCHOR are defined for it and L/DEF is not.
	silent := foldDetection(AllFixtures(), func(Fixture) []review.Finding { return nil },
		OurSeverityScale)
	_, silentNoise, silentAnchor, silentSpread := silent.DetectionCells()
	if silentNoise == "n/a" || silentAnchor == "n/a" {
		t.Errorf("a silent reviewer over the whole corpus renders NOISE %q and ANCHOR %q; both were "+
			"measured over real reviews and withholding them would withdraw a reading that exists",
			silentNoise, silentAnchor)
	}
	if silentSpread != "n/a" {
		t.Errorf("a reviewer that located NOTHING renders L/DEF %q. Zero lines per zero defects is "+
			"the best value the column can take, so printing it puts silence at the top of it",
			silentSpread)
	}
}

// foldAtDepth folds a corpus the way a battery with RUNS>1 does: it ATTEMPTS
// `runs` reviews of every fixture and folds all of them except the ones `lost`
// names, which stand in for a review or a judge call that failed.
//
// It exists because the shape nobody was thinking about is not a missing
// FIXTURE, it is a missing RUN of a fixture that other runs still cover.
func foldAtDepth(corpus []Fixture, reviewer func(Fixture) []review.Finding, runs int,
	lost map[string]int) Aggregate {
	var a Aggregate
	for _, f := range corpus {
		for run := 1; run <= runs; run++ {
			a.Attempted(f.Name)
			if run <= lost[f.Name] {
				continue
			}
			a.DeclareScale(OurSeverityScale)
			a.Saw(f.Name)
			a.AddSeverity(f, ScoreSeverity(f, reviewer(f)))
			a.AddDetection(ScoreDetection(f, reviewer(f)))
		}
	}
	return a
}

// noisyOn returns a calibrated reviewer that also invents findings on ONE
// fixture, so that losing runs of that fixture moves the published NOISE rate.
func noisyOn(fixture string, invented int) func(Fixture) []review.Finding {
	return func(f Fixture) []review.Finding {
		out := calibratedReview(f)
		if f.Name != fixture {
			return out
		}
		for i := range invented {
			out = append(out, review.Finding{
				Path: firstPath(f), Line: 1 + i,
				Severity: string(config.SeverityWarning),
				Class:    string(config.ClassMaintainability),
				Title:    spamText,
			})
		}
		return out
	}
}

// firstPath is the fixture's first changed file in a stable order.
func firstPath(f Fixture) string {
	paths, _ := fixturePaths(f)
	if len(paths) == 0 {
		return "a.go"
	}
	return paths[0]
}

// TestARowShortOfItsOwnAttemptedRunsSaysSo.
//
// RECALL, NOISE and L/DEF are rates over the reviews that SURVIVED, and the
// surviving subset is not chosen at random: a lost run is one whose review
// errored or whose judge call failed, and nothing rules out that those correlate
// with what the review said. The only comparability guard the judged report has
// reads Coverage, a set of fixture NAMES, so losing runs of a fixture that
// other runs still cover moves every rate on the row and trips nothing.
//
// The asymmetry runs one way. The incumbent is served at depth one per fixture,
// so any loss on its side removes the fixture, drops Coverage and fires the
// existing guard. Only OUR side can lose depth without losing coverage, so only
// our side can be silently flattered, which is why the marker is not optional.
func TestARowShortOfItsOwnAttemptedRunsSaysSo(t *testing.T) {
	corpus := AllFixtures()

	var noisy string
	for _, f := range corpus {
		if len(f.Defects) > 0 {
			noisy = f.Name
			break
		}
	}
	if noisy == "" {
		t.Fatal("no fixture plants a defect, so there is no row for a loss to reweight")
	}

	reviewer := noisyOn(noisy, 4)
	whole := foldAtDepth(corpus, reviewer, 3, nil)
	short := foldAtDepth(corpus, reviewer, 3, map[string]int{noisy: 2})

	// The premise: the two rows cover the same fixtures, so the guard the report
	// already has cannot tell them apart, and their NOISE rates differ.
	if whole.Coverage() != short.Coverage() {
		t.Fatalf("the two rows cover %d and %d fixtures; this test is about a loss the COVERAGE "+
			"guard cannot see", whole.Coverage(), short.Coverage())
	}
	_, wholeNoise, _, _ := whole.DetectionCells()
	_, shortNoise, _, _ := short.DetectionCells()
	if strings.TrimSuffix(wholeNoise, shortSampleMark) == strings.TrimSuffix(shortNoise, shortSampleMark) {
		t.Fatalf("losing two runs left NOISE at %q either way; this test is asserting nothing about "+
			"a rate that moved", wholeNoise)
	}

	if len(whole.ShortFixtures()) != 0 {
		t.Errorf("a row that folded every review it attempted reports %v as short",
			whole.ShortFixtures())
	}
	if got := short.ShortFixtures(); len(got) != 1 || got[0].Fixture != noisy ||
		got[0].Priced != 1 || got[0].Peer != 3 {
		t.Errorf("a row that folded 1 of 3 attempted reviews of %s reports %v", noisy, got)
	}

	wholeCells := detectionRow(t, "every-run-folded", whole)
	shortCells := detectionRow(t, "two-runs-lost", short)

	for _, col := range detectionColumns(t) {
		if strings.HasSuffix(wholeCells[col], shortSampleMark) {
			t.Errorf("the %s cell of a row that lost nothing is marked %q", col, wholeCells[col])
		}
		if !strings.HasSuffix(shortCells[col], shortSampleMark) {
			t.Errorf("the %s cell of a row that folded 1 of 3 attempted reviews of one fixture reads "+
				"%q, unmarked. It is folded over the reviews that survived, and the ones that did "+
				"not are a subset the row did not choose at random", col, shortCells[col])
		}
	}

	if why := short.CoverageShortfall("two-runs-lost"); why == "" {
		t.Error("a short row says nothing about why its cells are marked, so the marker sends a " +
			"reader to a footnote that is not there")
	}
	if why := whole.CoverageShortfall("every-run-folded"); why != "" {
		t.Errorf("a row that lost nothing carries the shortfall footnote anyway: %s", why)
	}
}

// TestNoShippedDocumentSaysAPrintedColumnIsMissing.
//
// docs/findings.md carried "the benchmark table has no ANCHOR column" in the
// present tense for as long as it took somebody to notice, while
// docs/measurement.md said the opposite two files away, and the stale one is the
// document a reader quotes the ship verdict from. Nothing tests a document, so a
// claim about the instrument outlives the instrument silently.
//
// The columns are read out of the header registry, so a column added to a table
// is one these documents may no longer be caught denying. The patterns are
// PRESENT-TENSE claims about the tree; a document recording what a past run
// printed is welcome to, and should say so in words that are not these.
func TestNoShippedDocumentSaysAPrintedColumnIsMissing(t *testing.T) {
	printed := map[string]bool{}
	for _, header := range AllTableHeaders() {
		for _, col := range strings.Fields(header) {
			printed[col] = true
		}
	}
	if !printed["ANCHOR"] {
		t.Fatal("no registered header carries ANCHOR, so this guard is scanning for claims about a " +
			"column nobody prints and would pass on any document")
	}

	cols := make([]string, 0, len(printed))
	for col := range printed {
		cols = append(cols, col)
	}
	sort.Strings(cols)

	// harness-notes.md carries the prose that used to be the second half of
	// measurement.md, and a claim about a column nobody prints is as wrong
	// there as it was there.
	for _, doc := range []string{"findings.md", "measurement.md", "harness-notes.md"} {
		prose := docProse(t, filepath.Join("..", "..", "docs", doc))
		for _, col := range cols {
			for _, claim := range []string{
				"no " + col + " column",
				"has no " + col,
				"does not print " + col,
				col + " is not printed",
			} {
				if strings.Contains(prose, claim) {
					t.Errorf("docs/%s says %q, and %q is a column of a registered table header. A "+
						"reader quoting that document is told the instrument cannot answer something "+
						"it answers", doc, claim, col)
				}
			}
		}
	}
}

// cleanFixtureForDetection is a fixture planting nothing, for the one assertion
// that needs a measured review with no defects behind it.
func cleanFixtureForDetection() Fixture {
	for _, f := range AllFixtures() {
		if f.Clean() {
			return f
		}
	}
	return Fixture{Name: "synthetic-clean", Base: map[string]string{"a.go": "package a\n"},
		Head: map[string]string{"a.go": "package a\n\nfunc f() {}\n"}}
}

// TestAFixtureFoldedNoTimesIsReportedAsAbsentAndNotAsThin covers the difference
// between a row that measured a fixture thinly and one that did not measure it.
//
// ShortFixtures was extracted from the cost ledger's shortfall reading and lost
// that ledger's `runs == 0` arm on the way, CostRow keeps Missing separate from
// Shallow, the extraction kept only the second. So a fixture folded ZERO times
// satisfied `got < tried` and joined the short list, under a sentence that opens
// "covers every fixture" and then names it "(0 of 1)". The two halves of that
// sentence contradict each other and the row is not comparable to the one beside
// it at all: its rates are over a SMALLER CORPUS, not a thinner sample.
//
// The direction is what makes it worth a test. A not-comparable row reading as
// merely thin tells a reader to discount a number they should refuse, and the
// case is most reachable on exactly the battery this was built for, one lost
// run of a fixture served at depth one removes it outright.
//
// The marker half is a regression guard on the fix rather than on the bug. Once
// the zero-fold case moved out of ShortFixtures, the marker predicate, which
// asked only that list, stopped firing for it, which would have printed four
// unmarked cells for the worse of the two shortfalls. Both halves are asserted
// here because the fix is only correct with both.
func TestAFixtureFoldedNoTimesIsReportedAsAbsentAndNotAsThin(t *testing.T) {
	corpus := AllFixtures()

	var planted string
	for _, f := range corpus {
		if len(f.Defects) > 0 {
			planted = f.Name
			break
		}
	}
	if planted == "" {
		t.Fatal("no fixture plants a defect, so there is no row for a loss to reweight")
	}

	reviewer := noisyOn(planted, 4)
	// Attempted 3, folded 0: every review of this fixture was lost.
	absent := foldAtDepth(corpus, reviewer, 3, map[string]int{planted: 3})

	if got := absent.ShortFixtures(); len(got) != 0 {
		t.Errorf("a fixture folded 0 times is reported as SHORT %v; short means measured thinly, "+
			"and this one was not measured", got)
	}

	missing := absent.UnmeasuredFixtures()
	if len(missing) != 1 || missing[0] != planted {
		t.Fatalf("UnmeasuredFixtures = %v, want exactly [%s]", missing, planted)
	}

	// The footnote must not claim coverage it does not have. "covers every
	// fixture" is ShallowSampleWarning's opening clause and is false here.
	note := absent.CoverageShortfall("nitpick/probe")
	switch {
	case note == "":
		t.Fatal("a row that folded nothing for a fixture prints no footnote at all")
	case strings.Contains(note, "covers every fixture"):
		t.Errorf("the footnote says the row covers every fixture while naming one it folded "+
			"nothing for:\n  %s", note)
	case !strings.Contains(note, planted):
		t.Errorf("the footnote does not name the fixture that went unmeasured:\n  %s", note)
	}

	// And the cells still carry the marker, which the split nearly removed.
	cells := detectionRow(t, "nothing-folded", absent)
	for _, col := range detectionColumns(t) {
		if cells[col] == "n/a" {
			continue
		}
		if !strings.HasSuffix(cells[col], shortSampleMark) {
			t.Errorf("the %s cell of a row that folded nothing for %s is unmarked (%q); the worse "+
				"of the two shortfalls must not print quieter than the lesser one",
				col, planted, cells[col])
		}
	}
}
