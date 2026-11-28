//go:build eval

package evals

import (
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The thresholds in the pre-registration were read off the development observations. If the
// constants and the data drift apart, the held-out run is judged at thresholds nobody chose.
func TestTheFrozenThresholdsComeFromTheDevelopmentObservations(t *testing.T) {
	var stratum []studyObservation
	for _, o := range studyLoadFile(t, "testdata/jev-dev-observations.json") {
		if studyInStratum(o) {
			stratum = append(stratum, o)
		}
	}
	if len(stratum) != 89 {
		t.Fatalf("the development stratum holds %d findings, the pre-registration says 89", len(stratum))
	}
	for method, want := range studyFrozen {
		var xs []studyScored
		for _, o := range stratum {
			v, _ := studyValue(o, method)
			xs = append(xs, studyScored{o.Real, v, o.Fixture})
		}
		if got := studyBestTrade(xs); math.Abs(got-want) > 1e-9 {
			t.Errorf("%s: the best trade on the development data is %.4f, frozen at %.4f", method, got, want)
		}
	}
}

func TestTheBestTradeIsTheThresholdThatWithholdsMostNoiseForTheFewestRealFindings(t *testing.T) {
	// not real: 0.1 0.2 0.3 0.8     real: 0.4 0.6 0.7 0.9
	// At 0.4: noise withheld 3/4, real lost 0, so 0.75. At 0.6: 3/4 and 1/4, so 0.5. At 0.9: 1 and 3/4, so 0.25.
	set := []studyScored{{false, 0.1, ""}, {false, 0.2, ""}, {false, 0.3, ""}, {false, 0.8, ""}, {true, 0.4, ""}, {true, 0.6, ""}, {true, 0.7, ""}, {true, 0.9, ""}}
	if got := studyBestTrade(set); got != 0.4 {
		t.Errorf("best trade = %.2f, want 0.40", got)
	}
	// Two thresholds tie and the higher wins. Not real {0.2, 0.6}, real {0.4, 0.8}.
	// At 0.4: noise withheld 1/2, real lost 0, so 0.5. At 0.8: noise withheld 1 and real lost 1/2, so 0.5.
	tie := []studyScored{{false, 0.2, ""}, {true, 0.4, ""}, {false, 0.6, ""}, {true, 0.8, ""}}
	if got := studyBestTrade(tie); got != 0.8 {
		t.Errorf("tied thresholds gave %.2f, want the higher, 0.80", got)
	}
	if got := studyBestTrade([]studyScored{{true, 0.9, ""}}); got != 0 {
		t.Errorf("a set with no not-real findings gave %.2f, want 0", got)
	}
}

// Findings from one fixture are not independent, so an interval from resampling findings is too
// narrow. Two fixtures that point opposite ways are the clearest case.
func TestTheClusterIntervalSpansWhatTwoOpposedFixturesDo(t *testing.T) {
	mk := func(fixture string, xGood bool) []studyObservation {
		var out []studyObservation
		for i := 0; i < 6; i++ {
			real := i < 3
			hi, lo := 0.9, 0.1
			x, y := lo, hi
			if real == xGood {
				x, y = hi, lo
			}
			out = append(out, studyObservation{Fixture: fixture, Real: real, Confidence: x, Severity: y})
		}
		return out
	}
	// Fixture a: confidence separates perfectly and severity inverts it. Fixture b: the reverse.
	obs := append(mk("a", true), mk("b", false)...)
	point, lo, hi := studyClusterDiff(obs, "confidence", "severity", 400, rand.New(rand.NewPCG(1, 2)))
	if math.Abs(point) > 1e-9 {
		t.Errorf("point estimate %.3f, want 0: the two fixtures cancel", point)
	}
	if lo > -0.9 || hi < 0.9 {
		t.Errorf("interval [%.2f, %.2f] does not span what one fixture alone does (+1 or -1)", lo, hi)
	}
	// The same findings resampled one at a time would give a narrow interval around zero.
	// Twenty agreeing fixtures give a narrow one here.
	var many []studyObservation
	for i := 0; i < 20; i++ {
		many = append(many, mk(string(rune('a'+i)), true)...)
	}
	p2, lo2, hi2 := studyClusterDiff(many, "confidence", "severity", 400, rand.New(rand.NewPCG(1, 2)))
	if math.Abs(p2-1) > 1e-9 || hi2-lo2 > 1e-9 {
		t.Errorf("agreeing fixtures: point %.3f interval [%.3f, %.3f], want 1 and no width", p2, lo2, hi2)
	}
}

func TestTheVerdictFollowsThePreRegisteredRules(t *testing.T) {
	for _, tc := range []struct {
		name         string
		notReal      int
		lo, supportA float64
		want         string
	}{
		{"too few not-real findings", 14, 0.2, 0.95, "underpowered"},
		{"exactly the minimum is enough to judge", 15, 0.2, 0.95, "confirmed"},
		{"interval touching zero", 30, 0.0, 0.95, "not confirmed"},
		{"interval below zero", 30, -0.3, 0.70, "not confirmed"},
		{"better than confidence but not good enough", 30, 0.1, 0.79, "not confirmed"},
		{"exactly the AUC floor", 30, 0.1, 0.80, "confirmed"},
	} {
		if got := studyVerdict(tc.notReal, tc.lo, tc.supportA); got != tc.want {
			t.Errorf("%s: verdict %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestTheStratumIsWhatTheReviewerRatedWarningOrAbove(t *testing.T) {
	for severity, want := range map[float64]bool{0.1: false, 0.3: false, 0.5: true, 0.8: true, 1: true} {
		if got := studyInStratum(studyObservation{Severity: severity}); got != want {
			t.Errorf("severity value %.1f in the stratum = %v, want %v", severity, got, want)
		}
	}
	if studySeverity("warning") < studyWarningFloor || studySeverity("info") >= studyWarningFloor {
		t.Error("the floor does not separate warning from info")
	}
}

func TestAHeldOutCollectionIsSpentOnceAndOnlyOnPurpose(t *testing.T) {
	dir := t.TempDir()
	fresh := filepath.Join(dir, "heldout.json")
	if err := studyGuardHeldOut("tuning", "", ""); err != nil {
		t.Errorf("the tuning corpus was guarded: %v", err)
	}
	if err := studyGuardHeldOut("", "", ""); err != nil {
		t.Errorf("the default corpus was guarded: %v", err)
	}
	if err := studyGuardHeldOut("heldout", "", fresh); err == nil || !strings.Contains(err.Error(), "SPEND_HELDOUT") {
		t.Errorf("a held-out run without the spend switch was allowed: %v", err)
	}
	if err := studyGuardHeldOut("heldout", "1", ""); err == nil {
		t.Error("a held-out run with no output file was allowed, and would use the default one")
	}
	if err := studyGuardHeldOut("heldout", "1", fresh); err != nil {
		t.Errorf("a deliberate first run was refused: %v", err)
	}
	if err := os.WriteFile(fresh, []byte("[]"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := studyGuardHeldOut("heldout", "1", fresh); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("a second run into a spent file was allowed: %v", err)
	}
}

func TestTheHeldOutCorpusIsTheHeldOutFixturesAndTheTuningCorpusHasNone(t *testing.T) {
	held, err := studyCorpus("heldout")
	if err != nil || len(held) != len(HeldOutFixtures()) || len(held) == 0 {
		t.Fatalf("held-out corpus: %d fixtures, err %v", len(held), err)
	}
	tuning, err := studyCorpus("")
	if err != nil {
		t.Fatal(err)
	}
	heldNames := map[string]bool{}
	for _, f := range held {
		heldNames[f.Name] = true
	}
	for _, f := range tuning {
		if heldNames[f.Name] {
			t.Errorf("held-out fixture %s is in the tuning corpus, so tuning data would no longer be tuning data", f.Name)
		}
	}
	if _, err := studyCorpus("everything"); err == nil {
		t.Error("an unknown corpus name was accepted")
	}
}

func TestTheIntervalIsTheMiddleNinetyFivePercentOfTheDraws(t *testing.T) {
	draws := make([]float64, studyDraws)
	for i := range draws {
		draws[i] = float64(studyDraws - 1 - i) // out of order on purpose
	}
	lo, hi := studyInterval(draws)
	// 2.5% of 2000 is 50 draws below the lower end and 50 above the upper.
	if lo != 50 || hi != 1949 {
		t.Errorf("interval [%v, %v] of 0..1999, want [50, 1949]", lo, hi)
	}
}

// Bug plus error is a sum of two probabilities. 0.01 + 0.93 is 0.9400000000000001 in floating
// point and 0.06 + 0.88 is 0.94, so against a threshold written as 0.94 the first would be kept
// and the second not, though both are the probability 0.94.
func TestTwoSumsThatAreTheSameScoreAreKeptOrWithheldTogether(t *testing.T) {
	x, y := 0.01, 0.93
	a := studyObservation{Jev: map[string]float64{"jev": x + y}}
	b := studyObservation{Jev: map[string]float64{"jev": 0.06 + 0.88}}
	if a.Jev["jev"] == b.Jev["jev"] {
		t.Fatal("setup: these two sums no longer differ in floating point, so the test guards nothing")
	}
	va, _ := studyValue(a, "jev")
	vb, _ := studyValue(b, "jev")
	if va != vb {
		t.Errorf("0.01+0.93 scores %v and 0.06+0.88 scores %v: the same probability, two thresholds' sides", va, vb)
	}
	if (va < 0.94) != (vb < 0.94) || va < 0.94 {
		t.Errorf("at a threshold of 0.94 both should be kept: scores %v and %v", va, vb)
	}
}
