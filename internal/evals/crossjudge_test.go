package evals

// Two judges, and the disagreement between them.
//
// Everything here runs in the DEFAULT build, deliberately. The reports are
// rendered from files behind the `eval` build tag and the judges cost money, so
// a guard written beside either would never run, which is how this harness
// arrived at a published table whose GRADE column was one vendor's opinion of
// three of its own models with nothing on the page saying so. The row renderers
// and the figure type live in non-test code precisely so that `go test ./...`
// can render a published row and assert what is in every cell of it.

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"math"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/review"
)

// TestVendorConflictsDetectsAJudgeScoringItsOwnVendor is the non-vacuity check
// for every claim below.
//
// A VendorConflicts that returned nothing for everything would make the clean-
// vendor test pass for the wrong reason, and the clean-vendor test is the one
// the second judge's whole existence rests on.
func TestVendorConflictsDetectsAJudgeScoringItsOwnVendor(t *testing.T) {
	battery := DefaultModels()
	if len(battery) == 0 {
		t.Fatal("the battery is empty, so every vendor claim below is vacuous")
	}

	// A judge id built from a real contender's vendor. Not a literal: a literal
	// stops being a contender's vendor the moment the battery is edited, which
	// is exactly the failure this function exists to catch.
	victim := battery[0].ID
	vendor, _, ok := strings.Cut(victim, "/")
	if !ok {
		t.Fatalf("contender %q carries no vendor prefix, so the cohort split has nothing to read", victim)
	}

	conflicts := VendorConflicts(vendor + "/some-judge")
	if len(conflicts) == 0 {
		t.Fatalf("VendorConflicts found nothing for a judge from %q, a vendor that has %q in the "+
			"battery. Every vendor claim in this file passes vacuously when this does",
			vendor, victim)
	}
	if !slicesContain(conflicts, victim) {
		t.Errorf("VendorConflicts(%q/some-judge) = %v, which omits %q — the contender it was "+
			"derived from", vendor, conflicts, victim)
	}

	// And it must not report a conflict that is not there.
	if conflicts := VendorConflicts("nobody-ships-this/model"); len(conflicts) != 0 {
		t.Errorf("VendorConflicts reported %v for a vendor with no contender; a function that "+
			"always finds a conflict cannot distinguish a clean judge from a dirty one", conflicts)
	}
}

// TestTheSecondJudgeSharesNoVendorWithAnyContender is the blocker this round
// was opened to close, checked against the battery rather than against a comment.
//
// The brief that commissioned this work listed the contender vendors as "openai,
// anthropic, z-ai, moonshotai and qwen" and concluded that google, deepseek and
// minimax were therefore clean, while DefaultModels carries a contender from
// each of those three. A judge chosen off that list would have re-created the
// conflict it was chosen to remove and nothing would have said so. This test is
// what makes the vendor claim survive an edit to the battery.
func TestTheSecondJudgeSharesNoVendorWithAnyContender(t *testing.T) {
	if conflicts := VendorConflicts(SecondJudgeModel); len(conflicts) != 0 {
		t.Errorf("the second judge %s shares a vendor with %d contender(s) it would score: %s.\n"+
			"It exists to be the opinion that is nobody's own, so a conflict here means the "+
			"corroboration answers the judge's variance and NOT the self-preference question. "+
			"Pick a judge from a vendor outside %v, or accept and state the narrower claim",
			SecondJudgeModel, len(conflicts), strings.Join(conflicts, ", "), ContenderVendors())
	}

	// The panel as a whole has to contain at least one opinion that is nobody's
	// own. Asserting the DEFAULT judge is conflicted would be asserting a defect
	// stays; asserting the panel is clean somewhere is the property that matters
	// and survives someone fixing the primary judge too.
	clean := len(VendorConflicts(DefaultJudgeModel)) == 0 || len(VendorConflicts(SecondJudgeModel)) == 0
	if !clean {
		t.Errorf("BOTH judges share a vendor with contenders they score: %s with %v, %s with %v. "+
			"Every judged figure is then two interested opinions",
			DefaultJudgeModel, VendorConflicts(DefaultJudgeModel),
			SecondJudgeModel, VendorConflicts(SecondJudgeModel))
	}

	if a, b := contenderVendor(DefaultJudgeModel), contenderVendor(SecondJudgeModel); a == b {
		t.Errorf("both judges are %s, so the second is not an independent opinion of the first — "+
			"it measures that vendor's own variance, which is the noise floor and not the answer", a)
	}

	t.Logf("contender vendors, derived from the battery: %s", strings.Join(ContenderVendors(), ", "))
	t.Logf("primary judge %s conflicts with: %v", DefaultJudgeModel, VendorConflicts(DefaultJudgeModel))
	t.Logf("second judge  %s conflicts with: %v", SecondJudgeModel, VendorConflicts(SecondJudgeModel))
}

// TestTwoJudgesDisagreeingProduceAVisibleDelta is the headline requirement.
func TestTwoJudgesDisagreeingProduceAVisibleDelta(t *testing.T) {
	f := Corroborated(3.66, 3.90)

	got := f.String()
	if got != "3.66+0.24" {
		t.Fatalf("Corroborated(3.66, 3.90) renders %q, want %q: the second judge's figure must be "+
			"recoverable from the pair, which needs the delta signed", got, "3.66+0.24")
	}

	// The other direction, because an unsigned spread would render both the
	// same and hide which judge scored higher, the exact question a vendor
	// comparison asks.
	if got, want := Corroborated(3.90, 3.66).String(), "3.90-0.24"; got != want {
		t.Errorf("Corroborated(3.90, 3.66) renders %q, want %q", got, want)
	}

	// The four temperature-0 grades this harness measured on identical
	// cached findings. The instrument has to be able to show that spread.
	for _, tc := range []struct{ a, b, want float64 }{
		{3.66, 3.98, 0.32},
		{3.90, 3.95, 0.05},
	} {
		delta := deltaOf(t, Corroborated(tc.a, tc.b).String())
		if math.Abs(delta-tc.want) > 0.005 {
			t.Errorf("Corroborated(%.2f, %.2f) shows a delta of %+.2f, want %+.2f", tc.a, tc.b, delta, tc.want)
		}
	}
}

// TestAJudgedFigureCarriesItsDisagreementUnderEveryVerb is the lock.
//
// A judged figure reaches a report through fmt, and a Format that honoured %f
// or %.2f would hand back the bare primary, the half-value this type exists to
// prevent, obtainable by a format string nobody would look at twice.
func TestAJudgedFigureCarriesItsDisagreementUnderEveryVerb(t *testing.T) {
	f := Corroborated(0.74, 0.68)
	want := "0.74-0.06"

	for _, verb := range []string{"%v", "%s", "%f", "%.2f", "%g", "%e", "%d", "%q", "%x", "%+v", "%#v"} {
		if got := fmt.Sprintf(verb, f); got != want {
			t.Errorf("fmt.Sprintf(%q, figure) = %q, want %q: a verb that reaches the primary value "+
				"alone is a route to half a published result", verb, got, want)
		}
	}

	// Width and the '-' flag are honoured, because a table cell has to be
	// padded and refusing that pushes callers back to formatting the parts.
	if got := fmt.Sprintf("%-12v|", f); got != want+"   |" {
		t.Errorf("left-padded figure = %q, want %q", got, want+"   |")
	}
	if got := fmt.Sprintf("%12v|", f); got != "   "+want+"|" {
		t.Errorf("right-padded figure = %q, want %q", got, "   "+want+"|")
	}
}

// TestASingleJudgeFigureIsNotPrintedAsAgreement separates the two states a
// reader would otherwise conflate.
func TestASingleJudgeFigureIsNotPrintedAsAgreement(t *testing.T) {
	alone := SingleJudged(0.74).String()
	agreed := Corroborated(0.74, 0.74).String()

	if alone == agreed {
		t.Fatalf("one judge and two judges who agreed exactly both render %q. A disagreement that "+
			"was never measured is not a disagreement of zero, and the difference is the whole "+
			"claim of the second judge", alone)
	}
	if want := "0.74+?"; alone != want {
		t.Errorf("SingleJudged(0.74) = %q, want %q", alone, want)
	}
	if want := "0.74+0.00"; agreed != want {
		t.Errorf("Corroborated(0.74, 0.74) = %q, want %q", agreed, want)
	}

	// "?" and not a number, so no arithmetic anywhere can absorb it.
	if strings.ContainsAny(strings.TrimPrefix(alone, "0.74"), "0123456789") {
		t.Errorf("the unmeasured disagreement in %q is spelled with digits; something will average it", alone)
	}
}

// TestAnUndefinedJudgedFigureIsNotZero. Aggregate.Precision returns NaN for a
// contender with no judged finding because silence is not perfection; printing
// that as a number would reintroduce the bug at the last step.
func TestAnUndefinedJudgedFigureIsNotZero(t *testing.T) {
	nan := math.NaN()

	for _, tc := range []struct {
		name string
		got  string
		want string
	}{
		{"one judge, nothing to score", SingleJudged(nan).String(), "n/a"},
		{"primary undefined", Corroborated(nan, 0.5).String(), "n/a"},
		{"second undefined collapses to one judge", Corroborated(0.5, nan).String(), "0.50+?"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s: rendered %q, want %q", tc.name, tc.got, tc.want)
		}
	}

	if SingleJudged(nan).Defined() {
		t.Error("an undefined figure reports itself as defined, so a ranking will sort it as a score")
	}
	if Corroborated(0.5, nan).Corroborated() {
		t.Error("a figure whose second judge said nothing reports itself as corroborated")
	}
}

// judgedFigure matches the grammar of a rendered figure and NOTHING ELSE. A
// bare "3.66" must not match it: that is the failure being tested for.
var judgedFigure = regexp.MustCompile(`^(n/a|-?\d+\.\d\d([+-]\d+\.\d\d|\+\?))$`)

// signedDelta matches a figure that published a NUMERIC disagreement, which is
// the rendering the legend calls a confidence interval.
//
// The cross-stimulus tests below assert its ABSENCE, and it is written as its
// own pattern rather than as "not judgedFigure" so that widening the accepted
// grammar cannot quietly widen what those tests allow.
var signedDelta = regexp.MustCompile(`-?\d+\.\d\d[+-]\d+\.\d\d`)

// filteredCorpus is judgedCorpus after a nitpick level has dropped everything
// below its threshold: the SAME fixtures, a SHORTER list for one of them.
//
// This is the exact shape of the demonstrated defect. The persona axis judged
// the whole corpus once with the primary judge and handed the second judge each
// level's filtered list, then printed the difference under a legend calling it
// the confidence interval on the figure beside it.
func filteredCorpus() []shownList {
	full := judgedCorpus()
	return []shownList{
		{full[0].fixture, full[0].findings[:1]},
		full[1],
	}
}

// TestADeltaFromTwoStimuliCannotRenderAsAConfidenceInterval reconstructs the
// mismatch and watches every published row refuse it.
//
// THE DEFECT THIS PINS, reproduced by rendering rather than by reading: on the
// default `make tune` axis the primary judge scored the whole corpus and the
// second scored each level's FILTERED list, so GRADE, SIGNAL, TONE and MISSED
// published a difference between two different questions under a legend saying
// the size of that difference was the confidence interval on the figure beside
// it. MISSED was biased in a known direction on top of that, filtering more
// findings legitimately raises the second judge's missed count against a primary
// frozen at the whole-corpus value.
//
// The assertion is on the RENDER, not on a flag, because the flag is not what a
// reader sees. Every judged cell of both published tables must come back either
// undefined or marked NOT COMPARABLE, and no cell anywhere may carry a signed
// numeric delta.
func TestADeltaFromTwoStimuliCannotRenderAsAConfidenceInterval(t *testing.T) {
	mismatched := CrossJudged{
		Primary:    sampleAggregateOver(4, judgedCorpus()),
		Second:     sampleAggregateOver(3, filteredCorpus()),
		HaveSecond: true,
	}

	if mismatched.SameStimulus() {
		t.Fatal("a whole-corpus primary and a filtered-subset second report themselves as the same " +
			"stimulus; the fingerprint is not covering the finding list")
	}

	for _, tc := range []struct {
		table  string
		header string
		row    string
	}{
		{"judged model ranking", CrossJudgedModelTableHeader,
			JudgedModelRow("nitpick/openai/gpt-5.6-terra", mismatched, 0)},
		{"persona comparison", CrossJudgedVariantTableHeader,
			JudgedVariantRow("nitpick=off", mismatched, 0)},
	} {
		if signedDelta.MatchString(tc.row) {
			t.Errorf("%s: the rendered row carries a signed delta somewhere, so a comparison that "+
				"was never made is published as one that was:\n%s", tc.table, tc.row)
		}

		cells := rowCells(t, tc.header, tc.row)
		checked := 0

		for name, cell := range cells {
			if !isCorroboratedColumn(name) {
				continue
			}
			checked++

			if cell == "n/a" {
				continue
			}
			if !strings.HasSuffix(cell, "+NC") {
				t.Errorf("%s: the %s cell is %q. The two judges scored different finding lists, so "+
					"there is no disagreement to size; anything but an admission here is a change "+
					"of stimulus wearing the costume of one", tc.table, name, cell)
			}
			if judgedFigure.MatchString(cell) {
				t.Errorf("%s: the %s cell %q matches the grammar of a corroborated figure, which is "+
					"the grammar the legend describes as a confidence interval", tc.table, name, cell)
			}
		}

		if checked < 8 {
			t.Errorf("%s: only %d judged column(s) were checked in a %d-column table; the scan has "+
				"stopped seeing the columns it exists to cover", tc.table, checked, len(cells))
		}
	}

	// The three states stay distinguishable. A cross-stimulus figure must not
	// be readable as a single-judge one, two judges answered and were paid
	// for, nor as an agreement, nor as nothing.
	crossed := mismatched.GradeFigure()
	for _, other := range []JudgedFigure{
		SingleJudged(2.0),
		Corroborated(2.0, 2.0),
		{},
	} {
		if crossed.String() == other.String() {
			t.Errorf("a cross-stimulus figure renders %q, which is also how %q renders; the reader "+
				"cannot tell a comparison that was refused from one that was never asked for",
				crossed, other)
		}
	}
	if crossed.Corroborated() {
		t.Error("a cross-stimulus figure reports itself as corroborated, so every derived comparison " +
			"gated on that — the rank MOVE, the second-judge order — is computed across two questions")
	}
	if !crossed.NotComparable() {
		t.Error("a cross-stimulus figure does not report itself as uncomparable, so no report can " +
			"explain the cell it just printed")
	}
}

// TestACrossStimulusFigureRefusesTheDeltaUnderEveryVerb extends the lock to the
// fourth state.
//
// A Format that honoured %f would hand back the bare primary; a String that
// spelled the refusal with digits would let something average it. Both are
// routes back to a published number that means nothing.
func TestACrossStimulusFigureRefusesTheDeltaUnderEveryVerb(t *testing.T) {
	f := NotComparable(0.74)
	want := "0.74+NC"

	for _, verb := range []string{"%v", "%s", "%f", "%.2f", "%g", "%e", "%d", "%q", "%x", "%+v", "%#v"} {
		if got := fmt.Sprintf(verb, f); got != want {
			t.Errorf("fmt.Sprintf(%q, figure) = %q, want %q: a verb that reaches the primary value "+
				"alone is a route to half a published result", verb, got, want)
		}
	}

	// The refusal carries NO digits of its own, so nothing downstream can read
	// it as a small delta or fold it into an average.
	if strings.ContainsAny(strings.TrimPrefix(want, "0.74"), "0123456789") {
		t.Errorf("the refusal in %q is spelled with digits; something will average it", want)
	}

	// An undefined primary collapses to undefined rather than inventing a
	// figure to be uncomparable about.
	if got := NotComparable(math.NaN()).String(); got != "n/a" {
		t.Errorf("NotComparable(NaN) = %q, want %q", got, "n/a")
	}

	// SplitCells is the one renderer that prints both judges' absolute values,
	// and it must not print the second's here: two numbers side by side in a
	// table whose subject is the two judges read as a difference whatever the
	// third cell says.
	primary, second, delta := f.SplitCells()
	if primary != "0.74" {
		t.Errorf("SplitCells primary = %q, want %q: the primary judge's figure is a real "+
			"measurement of a real list and is what the row publishes", primary, "0.74")
	}
	for _, tc := range []struct{ name, got string }{{"second", second}, {"delta", delta}} {
		if strings.ContainsAny(tc.got, "0123456789") {
			t.Errorf("SplitCells %s = %q, which is a number; the second judge answered a different "+
				"question and its figure must not be subtractable from the one beside it",
				tc.name, tc.got)
		}
	}
}

// TestASecondJudgeThatGradedFewerSamplesPublishesNoDelta.
//
// This is the same defect reached by the other route, and it is the one the
// package had already half-admitted: SamplesCell prints "8/6" and its own doc
// comment says the two rates "are correct over their own sample" but are "not
// the same measurement". They were nonetheless subtracted, and the difference
// printed as the confidence interval on the eight-sample figure.
func TestASecondJudgeThatGradedFewerSamplesPublishesNoDelta(t *testing.T) {
	full := judgedCorpus()

	short := CrossJudged{
		Primary:    sampleAggregateOver(4, full),
		Second:     sampleAggregateOver(3, full[:1]),
		HaveSecond: true,
	}

	if got := short.SamplesCell(); got != "2/1" {
		t.Fatalf("SamplesCell = %q, want %q: the fixture for this test is not the one it describes",
			got, "2/1")
	}
	if short.SameStimulus() {
		t.Error("a second judge that graded one of two samples reports the same stimulus as a " +
			"primary that graded both")
	}
	if got := short.PrecisionFigure().String(); !strings.HasSuffix(got, "+NC") {
		t.Errorf("PRECISION renders %q. A rate over two samples minus a rate over one is not a "+
			"disagreement between judges, and the legend beside it calls it a confidence interval", got)
	}

	// And the reader is told, where they go to check a delta, rather than
	// having to infer it from a distant cell.
	den := short.Denominators("z-ai/glm-5.2")
	if !strings.Contains(den, "DID NOT SCORE THE SAME") {
		t.Errorf("the denominators block does not say the two judges scored different lists, so a "+
			"reader who came here to check a delta is not told there is none:\n%s", den)
	}
}

// TestAnUnrecordedStimulusMatchesNothing pins the SAFE DIRECTION of the default.
//
// An aggregate folded in without stating what produced it costs a delta. The
// alternative, two unstated stimuli comparing equal, is how a future axis
// would reintroduce exactly this defect while every guard here still passed,
// because the zero value of a struct is the one value nobody writes on purpose.
func TestAnUnrecordedStimulusMatchesNothing(t *testing.T) {
	var bare Aggregate
	bare.Grades = []string{"B"}

	if bare.SameStimulusAs(bare) {
		t.Error("an aggregate with no recorded stimulus matches ITSELF, so two aggregates that " +
			"never said what they were judged over will publish a delta between them")
	}

	blind := CrossJudged{Primary: bare, Second: bare, HaveSecond: true}
	if got := blind.GradeFigure().String(); !strings.HasSuffix(got, "+NC") {
		t.Errorf("two aggregates that recorded no stimulus render GRADE as %q; an unstated "+
			"comparison must degrade to a refusal, not to a number", got)
	}

	// And the OTHER shape of unrecorded, which an empty trace does not reach:
	// a judgement that WAS folded in, over a stimulus whose fingerprint could
	// not be computed. The trace then has the right length and blank entries,
	// so a comparison that only checked lengths and equality would find two
	// blanks equal and publish a delta. This is the state JudgedOver returns
	// when it cannot encode the findings.
	//
	// Add's returned problems are dropped on purpose: these judgements carry no
	// verdicts, so it reports a shortfall that has nothing to do with what is
	// being measured here, which is the trace it recorded on the way past.
	unfingerprinted := func(seed int) Aggregate {
		var a Aggregate
		for _, s := range judgedCorpus() {
			a.Saw(s.fixture)
			_ = a.Add(&JudgeResult{
				Grade:         gradeLadder(seed)[0],
				SignalToNoise: 7 - seed,
				ToneAdherence: 8 - seed,
			}, Stimulus{n: len(s.findings)})
		}
		return a
	}

	blankA, blankB := unfingerprinted(4), unfingerprinted(3)
	if blankA.SameStimulusAs(blankB) {
		t.Error("two aggregates whose stimuli have no fingerprint report themselves as the same " +
			"stimulus. Blank is not evidence of sameness — it is the absence of evidence — and " +
			"treating two of them as equal is how a delta gets published for a comparison nobody made")
	}
	unstated := CrossJudged{Primary: blankA, Second: blankB, HaveSecond: true}
	if got := unstated.SignalFigure().String(); !strings.HasSuffix(got, "+NC") {
		t.Errorf("SIGNAL renders %q for two judgements that never said what produced them", got)
	}

	// And a stimulus whose fingerprint exists matches an identical one, or the
	// refusal above is not a guard, it is a blanket.
	one := JudgedOver("go-nil-deref", judgedCorpus()[0].findings)
	two := JudgedOver("go-nil-deref", judgedCorpus()[0].findings)
	if !one.Recorded() || one != two {
		t.Errorf("two fingerprints of the same fixture and findings differ (%v vs %v); nothing "+
			"would ever publish a delta again", one.Recorded(), two.Recorded())
	}
	if JudgedOver("clean-refactor", judgedCorpus()[0].findings) == one {
		t.Error("the fingerprint ignores the fixture, so two judgements of different changes that " +
			"produced the same findings would compare as one stimulus")
	}
	if JudgedOver("go-nil-deref", judgedCorpus()[0].findings[:1]) == one {
		t.Error("the fingerprint ignores the findings, which is the whole thing it exists to cover")
	}
}

// TestTheLegendClaimsAConfidenceIntervalOnlyForTheMatchedForm.
//
// The legend is a CONST in non-test code so that the admission cannot be edited
// out without a guard in the default build seeing it go. What it says has to be
// true of every cell it is printed under, and for half the rows of the default
// axis it was not.
func TestTheLegendClaimsAConfidenceIntervalOnlyForTheMatchedForm(t *testing.T) {
	// Every rendering the figure can produce is explained.
	for _, want := range []string{"0.74-0.06", "+?", "+NC", "n/a", "NOT COMPARABLE"} {
		if !strings.Contains(CrossJudgeLegend, want) {
			t.Errorf("CrossJudgeLegend does not mention %q, so a cell it produces is unexplained", want)
		}
	}

	// The confidence-interval claim is SCOPED. An unqualified sentence is what
	// made the legend false: it was printed under rows whose delta was a change
	// of stimulus, and it told the reader to treat that as an error bar.
	claim := strings.Index(CrossJudgeLegend, "CONFIDENCE INTERVAL")
	if claim < 0 {
		t.Fatal("the legend no longer explains what a delta means at all")
	}
	scope := CrossJudgeLegend[:claim]
	if !strings.Contains(scope, "SAME FINDING LIST") {
		t.Errorf("the legend claims a CONFIDENCE INTERVAL without first saying it holds only where "+
			"both judges scored the same finding list:\n%s", CrossJudgeLegend)
	}
	if !strings.Contains(scope, "ONLY IN THAT") {
		t.Errorf("the legend's confidence-interval sentence is unqualified, so it is read as "+
			"applying to every cell beneath it including the ones marked +NC:\n%s", CrossJudgeLegend)
	}

	// And the two-judge banner does not promise a delta on every figure either.
	banner := JudgePanel{Primary: DefaultJudgeModel, Second: SecondJudgeModel}.Banner()
	if !strings.Contains(banner, "+NC") {
		t.Errorf("the two-judge banner promises a disagreement beside every figure without "+
			"admitting that some carry none:\n%s", banner)
	}
}

// TestEveryJudgedCellInAPublishedRowCarriesItsDisagreement renders the actual
// published rows and reads every judged cell out of them.
//
// This is the assertion the type system cannot make. JudgedFigure guarantees
// that a figure prints as a pair; only rendering the row proves the row uses one
// in every judged column, which is where the equivalent guarantee about severity
// was broken before, the withdrawal lived in a renderer the table did not call.
func TestEveryJudgedCellInAPublishedRowCarriesItsDisagreement(t *testing.T) {
	corroborated := CrossJudged{
		Primary:    sampleAggregate(4),
		Second:     sampleAggregate(3),
		HaveSecond: true,
	}

	for _, tc := range []struct {
		table  string
		header string
		row    string
	}{
		{"judged model ranking", CrossJudgedModelTableHeader,
			JudgedModelRow("nitpick/openai/gpt-5.6-terra", corroborated, 0)},
		{"persona comparison", CrossJudgedVariantTableHeader,
			JudgedVariantRow("nitpick=normal", corroborated, 0)},
	} {
		cells := rowCells(t, tc.header, tc.row)
		checked := 0

		for name, cell := range cells {
			if !isCorroboratedColumn(name) {
				continue
			}
			checked++

			if !judgedFigure.MatchString(cell) {
				t.Errorf("%s: the %s cell is %q, which is not a figure carrying its cross-judge "+
					"disagreement. A judged column printed as a bare number is one opinion quoted "+
					"as a result", tc.table, name, cell)
			}
			if !strings.ContainsAny(cell, "+-") && cell != "n/a" {
				t.Errorf("%s: the %s cell %q has no delta at all", tc.table, name, cell)
			}
		}

		if checked < 8 {
			t.Errorf("%s: only %d judged column(s) were checked in a %d-column table; the scan has "+
				"stopped seeing the columns it exists to cover", tc.table, checked, len(cells))
		}

		// The two aggregates differ in every judged quantity, so every judged
		// cell must show a NON-ZERO delta. A renderer that paired an aggregate
		// with itself, or that read the second judge's figure off the first,
		// produces well-formed "+0.00" cells that the grammar check above
		// accepts and that a reader would take as perfect agreement.
		zero := 0
		for name, cell := range cells {
			if isCorroboratedColumn(name) && strings.HasSuffix(cell, "+0.00") {
				zero++
			}
		}
		if zero > 0 {
			t.Errorf("%s: %d judged cell(s) show a delta of exactly +0.00 between two aggregates "+
				"that differ in every judged quantity. The second judge's figure is being read off "+
				"the first", tc.table, zero)
		}
	}
}

// TestAMissingSecondJudgeDegradesToAStatedSingleJudgeFigure covers the state
// every run before this one was in, and the state a run falls back to whenever
// NITPICK_EVAL_JUDGE2 is unset.
//
// It has to be VISIBLY weaker rather than quietly identical, because a table
// that merely omitted the deltas would be indistinguishable from the tables this
// work was commissioned to replace.
func TestAMissingSecondJudgeDegradesToAStatedSingleJudgeFigure(t *testing.T) {
	panel := JudgePanel{Primary: DefaultJudgeModel}
	if panel.Corroborated() {
		t.Fatal("a panel with no second judge reports itself as corroborated")
	}

	one := panel.Pair("nitpick/openai/gpt-5.6-terra", sampleAggregate(4))
	row := JudgedModelRow("nitpick/openai/gpt-5.6-terra", one, 0)

	for name, cell := range rowCells(t, CrossJudgedModelTableHeader, row) {
		if !isCorroboratedColumn(name) {
			continue
		}
		if cell == "n/a" {
			continue
		}
		if !strings.HasSuffix(cell, "+?") {
			t.Errorf("with no second judge the %s cell is %q; every judged figure must say its "+
				"disagreement was NOT MEASURED rather than print as if it had been", name, cell)
		}
	}

	banner := panel.Banner()
	for _, want := range []string{"SINGLE JUDGE", "UNCORROBORATED", EnvSecondJudge, SecondJudgeModel} {
		if !strings.Contains(banner, want) {
			t.Errorf("the single-judge banner does not mention %q, so a reader is not told what "+
				"the table is missing or how to get it:\n%s", want, banner)
		}
	}

	// And the conflict that makes the single-judge table a problem is NAMED,
	// not alluded to.
	for _, conflicted := range VendorConflicts(DefaultJudgeModel) {
		if !strings.Contains(banner, conflicted) {
			t.Errorf("the banner does not name %s, a contender the judge shares a vendor with:\n%s",
				conflicted, banner)
		}
	}

	// A contender the second judge produced nothing for must degrade the same
	// way, rather than pair against an empty Aggregate and publish a delta
	// against a judgement nobody made.
	partial := JudgePanel{
		Primary:          DefaultJudgeModel,
		Second:           SecondJudgeModel,
		SecondAggregates: map[string]*Aggregate{"scored": ptrAggregate(sampleAggregate(3))},
	}
	if got := partial.Pair("never-judged", sampleAggregate(4)).GradeFigure().String(); !strings.HasSuffix(got, "+?") {
		t.Errorf("a contender the second judge produced nothing for renders %q; pairing it against "+
			"an empty aggregate publishes a delta against a measurement nobody made", got)
	}
	if !partial.Pair("scored", sampleAggregate(4)).GradeFigure().Corroborated() {
		t.Error("a contender the second judge DID score is not corroborated, so the panel lost it")
	}
}

// TestTwoJudgesAreVisibleInTheBannerAndTheLegend pins the prose that tells a
// reader how to read the cells.
func TestTwoJudgesAreVisibleInTheBannerAndTheLegend(t *testing.T) {
	panel := JudgePanel{Primary: DefaultJudgeModel, Second: SecondJudgeModel}

	banner := panel.Banner()
	if !strings.Contains(banner, "TWO JUDGES") {
		t.Errorf("the two-judge banner does not say so:\n%s", banner)
	}
	for _, judge := range []string{DefaultJudgeModel, SecondJudgeModel} {
		if !strings.Contains(banner, judge) {
			t.Errorf("the banner does not name the judge %s:\n%s", judge, banner)
		}
	}
	if !strings.Contains(banner, "shares a vendor with NO contender") {
		t.Errorf("the banner does not state that the second judge is clean, which is the only "+
			"reason its opinion answers anything the primary's does not:\n%s", banner)
	}

	// The legend has to explain "+?" as unmeasured rather than as zero, because
	// that is the reading a single-judge run depends on.
	for _, want := range []string{"+?", "UNMEASURED", "SIGNED", "O-*"} {
		if !strings.Contains(CrossJudgeLegend, want) {
			t.Errorf("CrossJudgeLegend does not mention %q, so a cell it produces is unexplained", want)
		}
	}
}

// TestCrossJudgedHeadersCarryTheSameColumnsAsTheHeadersTheyDeriveFrom.
//
// The widened headers are derived and not written out again, which is what makes
// a column impossible to add to one table and not the other. This checks the
// derivation rather than trusting it.
func TestCrossJudgedHeadersCarryTheSameColumnsAsTheHeadersTheyDeriveFrom(t *testing.T) {
	for _, tc := range []struct{ name, from, to string }{
		{"judged model ranking", JudgedModelTableHeader, CrossJudgedModelTableHeader},
		{"persona comparison", VariantTableHeader, CrossJudgedVariantTableHeader},
	} {
		from, to := tableColumns(tc.from), tableColumns(tc.to)

		if len(from) != len(to) {
			t.Fatalf("%s: %d columns widened into %d", tc.name, len(from), len(to))
		}

		widened := 0
		for i := range from {
			if from[i].name != to[i].name {
				t.Fatalf("%s: column %d is %q in the declared header and %q in the widened one",
					tc.name, i, from[i].name, to[i].name)
			}

			// The LAST column has no width to check: nothing follows it, so a
			// cell of any length there cannot push another column out of line.
			// Both these headers happen to end on a judged column, SIGNAL and
			// GRADE, so exempting it is not a loophole being opened, it is the
			// one column where the widening is a no-op.
			last := i == len(from)-1

			switch {
			case isCorroboratedColumn(from[i].name):
				if !last && to[i].width < judgedCellWidth {
					t.Errorf("%s: the judged column %s is %d wide, too narrow for a figure and its "+
						"delta; the row will silently push every column after it out of line",
						tc.name, to[i].name, to[i].width)
				}
				widened++
			case !last && to[i].width != from[i].width:
				t.Errorf("%s: the unjudged column %s was widened from %d to %d for no reason",
					tc.name, to[i].name, from[i].width, to[i].width)
			}
		}

		if widened < 8 {
			t.Errorf("%s: only %d column(s) were treated as judged; the derivation has stopped "+
				"seeing what a judge supplies", tc.name, widened)
		}
	}

	// A header with no judged column must come back byte-identical, or the
	// widening is rewriting tables it has no business touching.
	if got := WidenJudgedColumns(SummaryTableHeader); got != SummaryTableHeader {
		t.Errorf("widening the ground-truth header changed it:\n got %q\nwant %q", got, SummaryTableHeader)
	}
}

// TestTableRowSaysSoWhenItCannotAlign.
//
// A misaligned table is read as data, and both ways the row can stop matching
// the header are silent by default: too few cells, and a cell too wide for its
// column. This file's own DENOMINATORS block exists because the previous answer
// to the second was to move the value out of the table entirely.
func TestTableRowSaysSoWhenItCannotAlign(t *testing.T) {
	// A REGISTERED header, and its widths read back out of it rather than
	// written here. A probe header declared in this file would be an
	// unregistered table, which the package's own registry guard correctly
	// refuses, and hard-coded widths would stop describing the header the
	// moment a column moved.
	header := SummaryTableHeader
	cols := tableColumns(header)
	if len(cols) < 3 {
		t.Fatalf("%d column(s) in the ground-truth header; this test has nothing to misalign", len(cols))
	}

	full := make([]string, len(cols))
	for i := range full {
		full[i] = "x"
	}

	if got := TableRow(header, full); strings.Contains(got, "!!") {
		t.Errorf("a well-formed row was marked as broken: %q", got)
	}

	short := TableRow(header, full[:len(full)-1])
	if !strings.Contains(short, fmt.Sprintf("%d CELL(S) FOR A %d-COLUMN HEADER", len(cols)-1, len(cols))) {
		t.Errorf("a row with too few cells is not reported: %q", short)
	}

	over := append([]string(nil), full...)
	over[0] = strings.Repeat("x", cols[0].width+1)
	if !strings.Contains(TableRow(header, over), "CELL OVERFLOWS COLUMN: "+cols[0].name) {
		t.Errorf("a cell one character wider than its column is not reported: %q", TableRow(header, over))
	}

	// The last column is unbounded, so overflowing it is not a misalignment:
	// nothing follows it to be pushed out of line.
	last := append([]string(nil), full...)
	last[len(last)-1] = strings.Repeat("x", 80)
	if got := TableRow(header, last); strings.Contains(got, "!!") {
		t.Errorf("the last column was treated as bounded: %q", got)
	}
}

// TestSecondJudgeFromEnvResolvesTheVettedDefault.
//
// The "default" spelling exists so the id does not have to be copied into the
// Makefile, where nothing would re-check it against the battery.
func TestSecondJudgeFromEnvResolvesTheVettedDefault(t *testing.T) {
	for _, tc := range []struct{ set, want string }{
		{"", ""},
		{"   ", ""},
		{"default", SecondJudgeModel},
		{" default ", SecondJudgeModel},
		{"google/gemini-3.1-pro-preview", "google/gemini-3.1-pro-preview"},
	} {
		t.Setenv(EnvSecondJudge, tc.set)
		if got := SecondJudgeFromEnv(); got != tc.want {
			t.Errorf("%s=%q resolved to %q, want %q", EnvSecondJudge, tc.set, got, tc.want)
		}
	}
}

// TestEveryJudgedTargetCanBeGivenASecondJudge.
//
// THIS MAKEFILE HAS SHIPPED A PAID FLAG THAT DID NOTHING TWICE: AXIS was
// exported and read by nothing, so `make tune AXIS=voice` silently measured the
// other axis, and RUNS was not forwarded at all, so the SPREAD column measured
// fixture difficulty while reading as run-to-run variance. A second judge that
// one target forwards and another silently drops is the same defect with a
// larger bill, the operator pays for a corroboration and reads a table that
// says "+?".
//
// The exemption is derived from the recipe rather than from a list of target
// names: a target that already forwards a BASELINE judge is a two-judge
// comparison by construction and needs no third. A list would have to be
// maintained, and a maintained list is how the first two got through.
func TestEveryJudgedTargetCanBeGivenASecondJudge(t *testing.T) {
	src, err := os.ReadFile("../../Makefile")
	if err != nil {
		t.Fatalf("reading the Makefile: %v", err)
	}

	recipes := makeRecipes(string(src))
	if len(recipes) == 0 {
		t.Fatal("no recipes were parsed out of the Makefile, so this scan checked nothing")
	}

	judged := 0
	for target, recipe := range recipes {
		if !strings.Contains(recipe, EnvJudgeModel+"=") {
			continue
		}
		if strings.Contains(recipe, EnvBaselineJudge+"=") {
			// Already a comparison of two named judges.
			continue
		}
		judged++

		if !strings.Contains(recipe, EnvSecondJudge+"=") {
			t.Errorf("the `%s` target runs a judged test and forwards no %s, so its table can only "+
				"ever be one judge's opinion — and that judge shares a vendor with %d contender(s) "+
				"it scores. Add $(if $(JUDGE2),%s='$(JUDGE2)') to the recipe",
				target, EnvSecondJudge, len(VendorConflicts(DefaultJudgeModel)), EnvSecondJudge)
		}
	}

	if judged < 3 {
		t.Errorf("only %d judged target(s) were found; the scan has stopped seeing the recipes it "+
			"exists to cover", judged)
	}

	// And the documented spelling has to be the one the code accepts. A
	// Makefile that says JUDGE2=default against a resolver that does not know
	// the word is a flag that reaches the run as a model id nobody serves.
	if !strings.Contains(string(src), "JUDGE2=default") {
		t.Error("the Makefile does not document `JUDGE2=default`, which is the only spelling that " +
			"reaches the vendor-checked judge without copying its id into a file no test reads")
	}
	t.Setenv(EnvSecondJudge, "default")
	if got := SecondJudgeFromEnv(); got != SecondJudgeModel {
		t.Errorf("the documented `default` resolves to %q, not %q", got, SecondJudgeModel)
	}
}

// makeRecipes splits a Makefile into target names and their recipe bodies.
//
// A recipe is the tab-indented run of lines under a `target:` line, which is
// make's own rule and not a heuristic about how this file happens to be laid
// out. Continuation backslashes are left in place: the scan asks what a recipe
// contains, not what it would expand to.
func makeRecipes(src string) map[string]string {
	out := map[string]string{}

	target := ""
	var body strings.Builder

	flush := func() {
		if target != "" {
			out[target] = body.String()
		}
		target, body = "", strings.Builder{}
	}

	for _, l := range strings.Split(src, "\n") {
		if strings.HasPrefix(l, "\t") {
			if target != "" {
				body.WriteString(l)
				body.WriteString("\n")
			}
			continue
		}

		flush()
		if m := makeTargetLine.FindStringSubmatch(l); m != nil {
			target = m[1]
		}
	}
	flush()

	return out
}

// makeTargetLine matches a rule's target line and not a variable assignment:
// `JUDGE2   ?=` and `HELD_OUT := a,b` both contain a colon, and neither is a
// target.
var makeTargetLine = regexp.MustCompile(`^([A-Za-z0-9_.-]+):(?:[^=]|$)`)

// recordingJudge answers every group and records exactly what it was asked, so
// the claims about the corroboration path can be checked without a network.
type recordingJudge struct {
	// Rejudge calls this from bounded-concurrency goroutines, so the recording
	// needs a lock. Writing it without one lost half the calls and reported the
	// loss as "the second judge was asked once for two groups", which is
	// exactly what a broken corroboration would look like, and is the reason
	// `make check` runs the race detector over this package.
	mu       sync.Mutex
	asked    []recordedAsk
	failOn   string
	verdicts func(n int) *JudgeResult
}

type recordedAsk struct {
	fixture  string
	persona  config.Persona
	findings []review.Finding
}

func (r *recordingJudge) Judge(
	_ context.Context, f Fixture, persona config.Persona, findings []review.Finding,
) (*JudgeResult, error) {
	r.mu.Lock()
	r.asked = append(r.asked, recordedAsk{fixture: f.Name, persona: persona, findings: findings})
	r.mu.Unlock()

	if f.Name == r.failOn {
		return nil, fmt.Errorf("scripted failure on %s", f.Name)
	}
	if r.verdicts != nil {
		return r.verdicts(len(findings)), nil
	}

	out := &JudgeResult{Grade: "B", SignalToNoise: 7, ToneAdherence: 8}
	for i := range findings {
		out.Verdicts = append(out.Verdicts, Verdict{
			Index: i, Real: true, WorthRaising: true,
			SeverityVerdict: "accurate", ToneVerdict: "matches", ClassCorrect: true,
		})
	}
	return out, nil
}

// TestCorroborationJudgesTheRecordedFindingsAndRunsNoReview.
//
// The second judge is affordable only because it re-judges. This checks the two
// properties that makes it worth anything: it is shown the SAME findings, and it
// is shown them in the SAME positions, a verdict identifies its finding by
// position, so a reordered list would produce a full set of plausible, wrong
// pairings that look exactly like two judges disagreeing.
func TestCorroborationJudgesTheRecordedFindingsAndRunsNoReview(t *testing.T) {
	fixture := smallFixture("go-nil-deref")
	findings := []review.Finding{
		{Path: "a.go", Line: 10, Severity: "error", Class: "correctness", Title: "first"},
		{Path: "b.go", Line: 20, Severity: "warning", Class: "resource", Title: "second"},
		{Path: "c.go", Line: 30, Severity: "nit", Class: "style", Title: "third"},
	}

	samples := []DumpSample{
		{Model: "m/one", Run: 1, Fixture: fixture, Findings: findings,
			Judged: &JudgeResult{Grade: "A", Verdicts: []Verdict{{Index: 0, Real: true, WorthRaising: true}}}},
		// A review that reported nothing is submitted too: a judge that answers
		// an empty finding list is a failure this has to be able to show.
		{Model: "m/two", Run: 1, Fixture: smallFixture("clean-refactor"), Findings: nil},
	}

	groups := CorroborationGroups(samples)
	if len(groups) != 2 {
		t.Fatalf("%d group(s) from 2 samples", len(groups))
	}

	var silent int
	for _, g := range groups {
		if g.Silent {
			silent++
		}
	}
	if silent != 1 {
		t.Errorf("%d silent group(s), want 1: a review that reported nothing must be marked, or the "+
			"second judge's answer to it is counted as an ordinary review", silent)
	}

	judge := &recordingJudge{}
	aggregates, notes := Corroborate(context.Background(), judge, config.DefaultPersona(), groups, 2)

	if len(judge.asked) != 2 {
		t.Fatalf("the second judge was asked %d time(s) for 2 groups", len(judge.asked))
	}
	for _, ask := range judge.asked {
		if ask.fixture != "go-nil-deref" {
			continue
		}
		if !reflect.DeepEqual(ask.findings, findings) {
			t.Errorf("the second judge was shown a different finding list:\n got %+v\nwant %+v",
				ask.findings, findings)
		}
	}

	agg, ok := aggregates["m/one"]
	if !ok {
		t.Fatalf("no aggregate for m/one; got %v", keysOf(aggregates))
	}
	if agg.Findings != 3 {
		t.Errorf("the second judge's aggregate counted %d finding(s), want 3", agg.Findings)
	}
	if len(agg.Grades) != 1 {
		t.Errorf("the second judge's aggregate holds %d grade(s), want 1: without a grade the "+
			"GRADE column has nothing to disagree about", len(agg.Grades))
	}

	// The recorded baseline is carried through, so a caller can also compare
	// verdict against verdict rather than only rate against rate.
	if got := len(groups[indexOfFixture(groups, "go-nil-deref")].Baseline); got != 1 {
		t.Errorf("the primary judge's %d recorded verdict(s) did not reach the group, want 1", got)
	}

	if len(notes) != 0 {
		t.Errorf("a clean corroboration produced notes: %v", notes)
	}
}

// TestCorroborationIsFiledUnderTheKeyTheReportsAskFor.
//
// THE BUG THIS PINS, found by rendering the persona table rather than by reading
// the code: Corroborate files each aggregate under contenderLabel, model AND
// variant, while the persona tables identify their rows by variant alone. Ask
// for "nitpick=off" when the aggregate is under "z-ai/glm-5.2 [nitpick=off]" and
// Pair returns an uncorroborated figure. The second judge is called, billed, and
// answers; every cell still prints "+?"; and the run is indistinguishable from
// one where nobody asked for a second opinion.
//
// Both sides are strings, so nothing in the type system can catch it. What can
// is a report that says which of the second judge's answers no row claimed.
func TestCorroborationIsFiledUnderTheKeyTheReportsAskFor(t *testing.T) {
	samples := []DumpSample{
		{Model: "z-ai/glm-5.2", Variant: "nitpick=off", Run: 1, Fixture: smallFixture("go-nil-deref"),
			Findings: []review.Finding{{Path: "a.go", Line: 1, Title: "x"}}},
		{Model: "z-ai/glm-5.2", Variant: "nitpick=pedantic", Run: 1, Fixture: smallFixture("go-nil-deref"),
			Findings: []review.Finding{{Path: "a.go", Line: 1, Title: "x"}}},
	}

	aggregates, _ := Corroborate(
		context.Background(), &recordingJudge{}, config.DefaultPersona(), CorroborationGroups(samples), 2)

	want := contenderLabel("z-ai/glm-5.2", "nitpick=off")
	if _, ok := aggregates[want]; !ok {
		t.Fatalf("the second judge's answers are filed under %v, not under %q, which is the key "+
			"contenderLabel produces and the key every report has to ask with", keysOf(aggregates), want)
	}
	if _, ok := aggregates["nitpick=off"]; ok {
		t.Error("an aggregate is filed under the bare variant name; two keyings means one of the " +
			"two tables silently finds nothing")
	}

	panel := JudgePanel{Primary: DefaultJudgeModel, Second: SecondJudgeModel, SecondAggregates: aggregates}

	// The primary is built over the SAME list the samples above recorded, so
	// this test measures the keying and only the keying. Pairing a primary that
	// declared some other stimulus would fail here for the right reason and the
	// wrong one at once, and a test that can fail two ways reports neither.
	primary := func() Aggregate {
		return sampleAggregateOver(4, []shownList{
			{"go-nil-deref", []review.Finding{{Path: "a.go", Line: 1, Title: "x"}}},
		})
	}

	// Asking the way the bug asked: nothing pairs, and Unpaired names every
	// answer that went unclaimed.
	if panel.Pair("nitpick=off", primary()).HaveSecond {
		t.Error("a lookup by bare variant name paired; the two keyings cannot both be right")
	}
	if orphaned := panel.Unpaired([]string{"nitpick=off", "nitpick=pedantic"}); len(orphaned) != 2 {
		t.Errorf("Unpaired reported %v for a table that claimed neither aggregate, want both. A "+
			"paid-for corroboration that no row claims must not be able to look like no "+
			"corroboration at all", orphaned)
	}

	// Asking the way the reports now ask: everything pairs, and nothing is
	// orphaned. Without this half, blanking Unpaired's return would pass.
	var claimed []string
	for _, v := range []string{"nitpick=off", "nitpick=pedantic"} {
		key := contenderLabel("z-ai/glm-5.2", v)
		claimed = append(claimed, key)

		paired := panel.Pair(key, primary())
		if !paired.HaveSecond {
			t.Errorf("the row keyed %q did not pair with the answer filed under the same key", key)
			continue
		}
		// And the pairing is worth something: both judges scored the same
		// finding list, so the figure carries a delta rather than the admission
		// that the comparison was not made.
		if !paired.GradeFigure().Corroborated() {
			t.Errorf("the row keyed %q paired but publishes no delta (%s); the second judge scored "+
				"exactly the recorded findings, so there is a disagreement here to print",
				key, paired.GradeFigure())
		}
	}
	if orphaned := panel.Unpaired(claimed); len(orphaned) != 0 {
		t.Errorf("Unpaired reported %v for a table that claimed every aggregate; a guard that always "+
			"fires is one that gets deleted", orphaned)
	}
}

// TestCorroborationExcludesASampleTheSecondJudgeCouldNotAssess.
//
// Substituting a zero for a judgement that was never made would publish a
// disagreement against nothing, which is the one thing a delta must never be
// able to mean.
func TestCorroborationExcludesASampleTheSecondJudgeCouldNotAssess(t *testing.T) {
	samples := []DumpSample{
		{Model: "m/one", Run: 1, Fixture: smallFixture("go-nil-deref"),
			Findings: []review.Finding{{Path: "a.go", Line: 1, Title: "x"}}},
		{Model: "m/one", Run: 1, Fixture: smallFixture("clean-refactor"),
			Findings: []review.Finding{{Path: "b.go", Line: 2, Title: "y"}}},
	}

	judge := &recordingJudge{failOn: "clean-refactor"}
	aggregates, notes := Corroborate(
		context.Background(), judge, config.DefaultPersona(), CorroborationGroups(samples), 2)

	agg := aggregates["m/one"]
	if agg == nil {
		t.Fatal("the surviving sample produced no aggregate at all")
	}
	if len(agg.Grades) != 1 {
		t.Errorf("the second judge's aggregate holds %d grade(s), want 1: the failed sample was "+
			"counted, so the delta beside every figure is against a judgement nobody made",
			len(agg.Grades))
	}

	if len(notes["m/one"]) != 1 {
		t.Fatalf("%d note(s) for a failed second judgement, want 1: %v", len(notes["m/one"]), notes["m/one"])
	}
	if !strings.Contains(notes["m/one"][0], "SECOND JUDGE FAILED") {
		t.Errorf("the note does not say the second judge failed: %q", notes["m/one"][0])
	}

	// And the row has to SHOW the sample counts differing, because the two rates
	// either side of the delta then divide by different denominators.
	cross := CrossJudged{Primary: sampleAggregate(2), Second: *agg, HaveSecond: true}
	if got := cross.SamplesCell(); got != "2/1" {
		t.Errorf("N is %q, want %q: unequal sample counts behind a delta are invisible otherwise", got, "2/1")
	}
}

// TestCorroborationJudgesEachGroupUnderItsOwnPersona.
//
// The voice axis is four different personas, and judgeRequest shows
// the persona to the judge and asks it to score tone against that voice. Judging
// all four against the default would score three of them for adhering to a voice
// they were never asked to use, a change of prompt masquerading as a change of
// judge, which is the confound this whole path exists to eliminate.
func TestCorroborationJudgesEachGroupUnderItsOwnPersona(t *testing.T) {
	blunt := config.DefaultPersona()
	blunt.Politeness = config.PolitenessBlunt

	fallback := config.DefaultPersona()
	if blunt.Politeness == fallback.Politeness {
		t.Fatal("the two personas are identical, so this test cannot tell them apart")
	}

	groups := []RejudgeGroup{
		{Model: "m", Variant: "voice=blunt", Run: 1, Fixture: smallFixture("go-nil-deref"), Persona: &blunt},
		{Model: "m", Variant: "", Run: 1, Fixture: smallFixture("clean-refactor")},
	}

	judge := &recordingJudge{}
	Corroborate(context.Background(), judge, fallback, groups, 1)

	seen := map[string]config.Persona{}
	for _, ask := range judge.asked {
		seen[ask.fixture] = ask.persona
	}

	if got := seen["go-nil-deref"].Politeness; got != blunt.Politeness {
		t.Errorf("the group carrying its own persona was judged at politeness %v, want %v", got, blunt.Politeness)
	}
	if got := seen["clean-refactor"].Politeness; got != fallback.Politeness {
		t.Errorf("the group carrying no persona was judged at politeness %v, want the caller's %v",
			got, fallback.Politeness)
	}
}

// TestRejudgePrecisionCellsAllComeFromOneFigure.
//
// The re-judge diagnostic table prints both absolute precisions and their delta,
// which is right for the one table whose subject IS the two judges. They still
// arrive from a single call, so a row cannot render two of the three, the same
// shape ObjectiveSeverityCells uses for the severity triple and its denominator.
func TestRejudgePrecisionCellsAllComeFromOneFigure(t *testing.T) {
	a, b, d := Corroborated(0.80, 0.60).SplitCells()
	if a != "0.80" || b != "0.60" || d != "-0.20" {
		t.Errorf("SplitCells = (%q, %q, %q), want (0.80, 0.60, -0.20)", a, b, d)
	}

	// One judge: the second cell and the delta are UNDEFINED, not zero and not
	// a copy of the first. A diagnostic table that showed a one-judge run as a
	// zero-difference one would be the exact claim this file rejects.
	a, b, d = SingleJudged(0.80).SplitCells()
	if a != "0.80" || b != "n/a" || d != "n/a" {
		t.Errorf("SplitCells for one judge = (%q, %q, %q), want (0.80, n/a, n/a)", a, b, d)
	}

	a, b, d = SingleJudged(math.NaN()).SplitCells()
	if a != "n/a" || b != "n/a" || d != "n/a" {
		t.Errorf("SplitCells for an undefined figure = (%q, %q, %q), want all n/a", a, b, d)
	}
}

// TestNoReportFormatsAJudgedFigureDirectly ties the tables to the figure.
//
// JudgedFigure proves a figure prints as a pair, and JudgedModelRow proves the
// published rows use one. Neither can prove that some OTHER report does not
// format Aggregate.MeanGrade into a cell, which is the identical shape of the
// defect this package already found in its severity columns, where the
// withdrawal lived in a renderer the ground-truth table never called.
//
// The names are DERIVED, not listed: every method of Aggregate that returns a
// float64 is a way to obtain a judged number, including the next one somebody
// adds. Only formatting calls are inspected, because ranking and aggregating
// legitimately need the value. It is publishing it alone that is the defect.
//
// The set of report files is derived too, from whether the file underlines a
// table, so a new report is covered on the day it is written.
func TestNoReportFormatsAJudgedFigureDirectly(t *testing.T) {
	accessors := judgedFloatAccessors()
	if len(accessors) < 4 {
		t.Fatalf("derived only %d judged accessor(s) (%v); the reflection has stopped seeing the "+
			"methods it covers", len(accessors), accessors)
	}

	named := map[string]bool{}
	for _, a := range accessors {
		named[a] = true
	}

	files := packageAST(t)
	renderers := 0

	for name, file := range files {
		if !rendersATable(file) {
			continue
		}
		renderers++

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || !isFormattingCall(call) {
				return true
			}
			for _, arg := range call.Args {
				ast.Inspect(arg, func(n ast.Node) bool {
					sel, ok := n.(*ast.SelectorExpr)
					if !ok || !named[sel.Sel.Name] {
						return true
					}
					t.Errorf("%s prints a table and formats %s directly. Judged figures are rendered "+
						"by CrossJudged, which binds the figure to the disagreement between the two "+
						"judges that produced it; formatting the accessor publishes one judge's "+
						"number with nothing beside it, which is what this whole path exists to stop",
						name, sel.Sel.Name)
					return false
				})
			}
			return true
		})
	}

	if renderers == 0 {
		t.Fatal("no file in this package renders a table, so this scan checked nothing")
	}
}

// TestTheJudgedFigureScanSeesAViolation is the guard on the guard.
//
// A scan that matched nothing would pass over a report that published bare
// figures, and every version of that failure in this package has looked like a
// green suite. This feeds it the violation it exists to catch.
func TestTheJudgedFigureScanSeesAViolation(t *testing.T) {
	const src = `package evals

func report() string {
	var a Aggregate
	b := ""
	b += strings.Repeat("-", len(JudgedModelTableHeader))
	b += fmt.Sprintf("%.2f", a.MeanGrade())
	return b
}
`
	file := parseProbe(t, src)

	if !rendersATable(file) {
		t.Fatal("the probe does not register as a report, so the scan would skip it and the guard " +
			"below proves nothing")
	}

	named := map[string]bool{}
	for _, a := range judgedFloatAccessors() {
		named[a] = true
	}

	found := 0
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || !isFormattingCall(call) {
			return true
		}
		for _, arg := range call.Args {
			ast.Inspect(arg, func(n ast.Node) bool {
				if sel, ok := n.(*ast.SelectorExpr); ok && named[sel.Sel.Name] {
					found++
				}
				return true
			})
		}
		return true
	})

	if found == 0 {
		t.Error("the scan did not see a report formatting Aggregate.MeanGrade into a cell. It is " +
			"looking for the wrong shape, and every table in this package is unguarded")
	}
}

// judgedFloatAccessors derives every method of Aggregate that hands out a judged
// number.
//
// Float-returning methods only. The int counters are reachable by the same
// spelling on Verdict, JudgeResult, RejudgeGroup, DumpSample and Score, "Real",
// "Missed" and "Findings" all name something else in this package, so a
// name-based scan over them reports correct code as a violation, and a guard
// that cries wolf gets exemptions until it guards nothing. The float accessors
// are unique to Aggregate, and they are the whole surface through which GRADE,
// SPREAD, PREC, SIGNAL and TONE can be printed alone.
func judgedFloatAccessors() []string {
	var out []string

	t := reflect.TypeOf(Aggregate{})
	for i := range t.NumMethod() {
		m := t.Method(i)
		if m.Type.NumIn() == 1 && m.Type.NumOut() == 1 && m.Type.Out(0).Kind() == reflect.Float64 {
			out = append(out, m.Name)
		}
	}

	sort.Strings(out)
	return out
}

// isFormattingCall reports whether a call renders something for a reader.
//
// fmt's whole surface, plus the testing logger and a strings.Builder write,
// which are the three ways a table in this package reaches a page.
func isFormattingCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}

	if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "fmt" {
		return true
	}

	switch sel.Sel.Name {
	case "Logf", "Errorf", "Fatalf", "Skipf", "WriteString", "Write":
		return true
	}
	return false
}

// parseProbe parses a source snippet written to exercise a scan.
//
// A probe rather than a real file, for the reason TestProseCannotRegisterA
// TableHeader gives: a guard is only trustworthy if it has been shown failing,
// and the only way to show this one failing is to hand it the code it forbids.
func parseProbe(t *testing.T, src string) *ast.File {
	t.Helper()

	parsed, err := parser.ParseFile(token.NewFileSet(), "probe.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parsing the probe source: %v", err)
	}
	return parsed
}

// --- helpers ---------------------------------------------------------------

// sampleAggregate builds an Aggregate with every judged counter non-zero and
// distinguishable, so a cell that renders the wrong one is visible.
//
// seed shifts every value, which is what makes two of these disagree in every
// column at once, the state the corroborated table is being checked in.
func sampleAggregate(seed int) Aggregate { return sampleAggregateOver(seed, judgedCorpus()) }

// shownList is one judged sample: a fixture, and the findings the judge was
// shown for it.
//
// The tests carry it because a cross-judge delta is only a confidence interval
// when both sides were built over the same lists, so an aggregate that does not
// say what it was shown cannot be paired with one that does. Two aggregates
// built from the SAME lists disagree; two built from different ones do not
// disagree at all, and the difference between those two sentences is what
// JudgedFigure now has a fourth state for.
type shownList struct {
	fixture  string
	findings []review.Finding
}

// judgedCorpus is the stimulus every default sample aggregate declares: two
// fixtures, with the findings a judge saw for each.
func judgedCorpus() []shownList {
	return []shownList{
		{"go-nil-deref", []review.Finding{
			{Path: "a.go", Line: 10, Severity: "error", Class: "correctness", Title: "nil dereference"},
			{Path: "a.go", Line: 22, Severity: "nit", Class: "style", Title: "name shadows the package"},
		}},
		{"clean-refactor", []review.Finding{
			{Path: "b.go", Line: 4, Severity: "warning", Class: "resource", Title: "unbounded cache key"},
		}},
	}
}

// sampleAggregateOver builds a plausible aggregate that was judged over exactly
// the given lists.
//
// The per-sample slices are grown per shown list rather than pinned at two, so
// an aggregate's sample count and its stimulus count cannot disagree, a helper
// that claimed two grades over one judged list would be a helper capable of
// passing a test that a real aggregate could not.
func sampleAggregateOver(seed int, shown []shownList) Aggregate {
	a := Aggregate{
		Findings:     10 + seed,
		Real:         8 + seed,
		WorthRaising: 6 + seed,
		Inflated:     2 + seed,
		Understated:  1 + seed,
		ToneOff:      1 + seed,
		Misclassed:   2 + seed,
		Missed:       3 + seed,

		SevAccurate:    4,
		SevInflated:    1,
		SevUnderstated: 1,
		SevPlanted:     8,
	}

	// Declared, so the rows these tests render carry O-* cells rather than four
	// n/a. The scale is what gates that cell now; an undeclared sample aggregate
	// would leave every judged-row test above checking the withdrawal instead of
	// the columns it is about. See SeverityScale.
	//
	// Through DeclareScale rather than as a literal field, for the reason
	// TestNoAggregateLiteralSetsItsOwnScale gives: a row's scale is folded from
	// declarations, and a helper that sets it directly is a helper that can
	// build a row no fold could produce.
	a.DeclareScale(OurSeverityScale)

	// Grades and signal move with the seed too, so two of these disagree in
	// EVERY judged column and not only in the counted ones. Holding them equal
	// would let a renderer that paired an aggregate with itself pass the GRADE,
	// SPREAD and SIGNAL cells.
	ladder := gradeLadder(seed)
	for i, s := range shown {
		a.Saw(s.fixture)
		a.sawStimulus(JudgedOver(s.fixture, s.findings))
		a.Grades = append(a.Grades, ladder[i%len(ladder)])
		a.SignalToNoise = append(a.SignalToNoise, 7-seed)
		a.ToneAdherence = append(a.ToneAdherence, 8-seed)
	}
	return a
}

// gradeLadder returns two grades whose mean AND whose spread both move with the
// seed, so a corroborated GRADE and SPREAD cell have something to disagree about.
//
// One end is pinned and the other climbs an ASCENDING ladder, which makes both
// quantities monotone in the seed by construction. Two grades picked
// independently is what the first draft did, and it produced two different means
// with an identical spread, a SPREAD cell reading "+0.00" that this helper was
// written to make impossible.
func gradeLadder(seed int) []string {
	ladder := []string{"D", "C", "C+", "B-", "B", "B+", "A-", "A"}
	return []string{ladder[0], ladder[seed%len(ladder)]}
}

func ptrAggregate(a Aggregate) *Aggregate { return &a }

// smallFixture is a fixture with a name and enough shape to be judged.
func smallFixture(name string) Fixture {
	return Fixture{
		Name: name,
		Base: map[string]string{"a.go": "package a\n"},
		Head: map[string]string{"a.go": "package a\n\nfunc f() {}\n"},
	}
}

// rowCells splits a rendered row by the header's own column widths, so a cell is
// read where the header says it is rather than by counting whitespace.
func rowCells(t *testing.T, header, row string) map[string]string {
	t.Helper()

	if strings.Contains(row, "!!") {
		t.Fatalf("the row reports that it does not fit its header, so every cell below is read "+
			"from the wrong place: %q", row)
	}

	cols := tableColumns(header)
	out := make(map[string]string, len(cols))

	rest := strings.TrimRight(row, "\n")
	for i, c := range cols {
		if i == len(cols)-1 {
			out[c.name] = strings.TrimSpace(rest)
			break
		}
		if len(rest) < c.width {
			t.Fatalf("the row ended inside the %s column: %q", c.name, row)
		}
		out[c.name] = strings.TrimSpace(rest[:c.width])
		rest = strings.TrimLeft(rest[c.width:], " ")
	}
	return out
}

func isCorroboratedColumn(name string) bool {
	return slicesContain(CorroboratedColumns(), name)
}

func slicesContain(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

// deltaOf reads the signed delta back out of a rendered figure, which is how a
// test asserts the pair is lossless rather than merely present.
func deltaOf(t *testing.T, rendered string) float64 {
	t.Helper()

	i := strings.LastIndexAny(rendered, "+-")
	if i <= 0 {
		t.Fatalf("no delta in %q", rendered)
	}

	var d float64
	if _, err := fmt.Sscanf(rendered[i:], "%f", &d); err != nil {
		t.Fatalf("delta %q in %q does not parse: %v", rendered[i:], rendered, err)
	}
	return math.Abs(d)
}

func keysOf(m map[string]*Aggregate) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func indexOfFixture(groups []RejudgeGroup, name string) int {
	for i, g := range groups {
		if g.Fixture.Name == name {
			return i
		}
	}
	return 0
}
