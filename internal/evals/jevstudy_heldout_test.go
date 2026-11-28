//go:build eval

package evals

// Pre-registered 2026-11-28, before any held-out finding exists. Do not edit the rules
// below after the held-out run; add a dated note instead.
//
// Why this run. The first study (jevstudy_test.go, docs/findings.md) found Jev's bug-plus-error
// score no better than chance on the tuning corpora, and an exploratory score, one minus Jev's
// noise probability ("support"), that separated better. The exploratory stratum that made support
// look strong picked fixtures by their planted severity, which a filter cannot do. A filter can
// pick findings by the severity the reviewer gave them. On that stratum the development data
// reads: confidence 0.805, severity 0.713, support 0.672, bug plus error 0.484 (AUC, 89 findings,
// 72 real). So the development data does not favour Jev, and this run exists to settle the
// question on data nothing was tuned on, not because it is expected to favour it.
//
// DATA. The held-out corpus, evals.HeldOutFixtures (14 fixtures), spent once. Reviewers
// deepseek/deepseek-v4.1-flash, qwen/qwen3.8-27b and z-ai/glm-5.3-flash, four runs each at
// temperature 0, repository check off (#274 blocks it on glm and it refuted nothing in the first
// study), Jev enabled and withholding nothing. Labels as in the first study. The development
// observations are testdata/jev-dev-observations.json, the plain arm of the first study.
//
// STRATUM. Findings the reviewer rated warning or above (severity value 0.5 or more).
//
// PRIMARY. AUC(support) minus AUC(confidence) in that stratum, with a 95% interval from
// resampling whole fixtures (2000 draws, seed 17), because findings from one fixture and one
// reviewer are not independent.
//
// SECONDARY. The same difference against severity and against bug plus error. The operating
// point of each method at the threshold below, reporting the share of not-real findings
// withheld and the share of real findings lost.
//
// FROZEN THRESHOLDS, from the development observations in the same stratum: the value that
// maximises (not-real withheld) minus (real lost), ties to the higher value.
//     confidence 0.92   severity 0.80   bug plus error 0.95   support 0.99
// TestTheFrozenThresholdsComeFromTheDevelopmentObservations recomputes them.
// Correction made before any held-out call: bug plus error was first written as 0.94, from a
// sum of two probabilities that carried floating-point noise (0.9400000000000001). Scores are
// now rounded to six places and the value on the same data is 0.95.
//
// DECISION RULES.
//   Underpowered: fewer than 15 not-real findings in the stratum. Nothing is confirmed or refuted.
//   Confirmed: the primary interval lies wholly above zero and support's AUC is at least 0.80.
//   Not confirmed: anything else.
//
// What each outcome changes. Nothing turns review.jev on by default, whatever the result.
//   Confirmed: a separate issue proposes scoping the filter to findings of warning or above and
//   reading support, to be built and measured on its own.
//   Not confirmed or underpowered: the comment on JevFilter stops recommending 0.3 and says
//   the score was not shown better than the reviewer's own confidence. The corpus is then spent
//   for this question and is not run against Jev again.
//
// WHAT IT CANNOT SAY. 14 fixtures in languages the tuning corpora barely cover, cheap reviewers,
// planted defects. A confirmed result is one sample of a small corpus.

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"sort"
	"strings"
	"testing"
)

// studyFrozen are the thresholds fixed before the held-out run.
var studyFrozen = map[string]float64{"confidence": 0.92, "severity": 0.80, "jev": 0.95, "support:jev": 0.99}

const (
	studyWarningFloor      = 0.5
	studyMinNotReal        = 15
	studySupportConfirmAUC = 0.80
	studyDraws             = 2000
)

func studyInStratum(o studyObservation) bool { return o.Severity >= studyWarningFloor }

// studyBestTrade is the threshold that maximises the share of not-real findings withheld minus
// the share of real findings lost, ties to the higher value.
func studyBestTrade(xs []studyScored) float64 {
	var pos, neg []float64
	candidates := map[float64]bool{}
	for _, x := range xs {
		candidates[x.score] = true
		if x.real {
			pos = append(pos, x.score)
		} else {
			neg = append(neg, x.score)
		}
	}
	if len(pos) == 0 || len(neg) == 0 {
		return 0
	}
	under := func(vs []float64, t float64) float64 {
		n := 0.0
		for _, v := range vs {
			if v < t {
				n++
			}
		}
		return n / float64(len(vs))
	}
	best, bestScore := 0.0, math.Inf(-1)
	var ts []float64
	for t := range candidates {
		ts = append(ts, t)
	}
	sort.Float64s(ts)
	for _, t := range ts {
		if j := under(neg, t) - under(pos, t); j >= bestScore-1e-12 {
			best, bestScore = t, j
		}
	}
	return best
}

// studyClusterDiff is AUC(a) minus AUC(b) over the observations in the stratum, with a 95%
// interval from resampling fixtures with replacement.
func studyClusterDiff(obs []studyObservation, a, b string, draws int, rng *rand.Rand) (point, lo, hi float64) {
	byFixture := map[string][]studyObservation{}
	for _, o := range obs {
		byFixture[o.Fixture] = append(byFixture[o.Fixture], o)
	}
	names := make([]string, 0, len(byFixture))
	for name := range byFixture {
		names = append(names, name)
	}
	sort.Strings(names)
	diff := func(set []studyObservation) float64 {
		var xs, ys []studyScored
		for _, o := range set {
			va, okA := studyValue(o, a)
			vb, okB := studyValue(o, b)
			if okA && okB {
				xs = append(xs, studyScored{o.Real, va, o.Fixture})
				ys = append(ys, studyScored{o.Real, vb, o.Fixture})
			}
		}
		return studyAUC(xs) - studyAUC(ys)
	}
	point = diff(obs)
	if len(names) == 0 {
		return point, point, point
	}
	ds := make([]float64, draws)
	for i := range ds {
		var set []studyObservation
		for range names {
			set = append(set, byFixture[names[rng.IntN(len(names))]]...)
		}
		ds[i] = diff(set)
	}
	lo, hi = studyInterval(ds)
	return point, lo, hi
}

// studyInterval is the 95% interval of a set of draws: the values 2.5% and 97.5% of the way
// through them in order. The draws are sorted in place.
func studyInterval(draws []float64) (lo, hi float64) {
	sort.Float64s(draws)
	n := len(draws)
	return draws[int(float64(n)*0.025)], draws[int(float64(n)*0.975)-1]
}

// studyVerdict applies the decision rules to what was measured.
func studyVerdict(notReal int, lo, supportAUC float64) string {
	switch {
	case notReal < studyMinNotReal:
		return "underpowered"
	case lo > 0 && supportAUC >= studySupportConfirmAUC:
		return "confirmed"
	}
	return "not confirmed"
}

type studyConfirm struct {
	n, real, notReal int
	auc              map[string]float64
	diffs            map[string][3]float64 // point, lo and hi of support minus a method, by method
	verdict          string
}

func studyConfirmAnalysis(obs []studyObservation) studyConfirm {
	var stratum []studyObservation
	for _, o := range obs {
		if studyInStratum(o) {
			stratum = append(stratum, o)
		}
	}
	out := studyConfirm{n: len(stratum), auc: map[string]float64{}, diffs: map[string][3]float64{}}
	for _, o := range stratum {
		if o.Real {
			out.real++
		} else {
			out.notReal++
		}
	}
	rng := rand.New(rand.NewPCG(17, 17))
	for _, m := range []string{"confidence", "severity", "jev", "support:jev"} {
		var xs []studyScored
		for _, o := range stratum {
			if v, ok := studyValue(o, m); ok {
				xs = append(xs, studyScored{o.Real, v, o.Fixture})
			}
		}
		out.auc[m] = studyAUC(xs)
	}
	for _, m := range []string{"confidence", "severity", "jev"} {
		p, lo, hi := studyClusterDiff(stratum, "support:jev", m, studyDraws, rng)
		out.diffs[m] = [3]float64{p, lo, hi}
	}
	out.verdict = studyVerdict(out.notReal, out.diffs["confidence"][1], out.auc["support:jev"])
	return out
}

func studyLoadFile(t *testing.T, path string) []studyObservation {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var obs []studyObservation
	if err := json.Unmarshal(raw, &obs); err != nil {
		t.Fatal(err)
	}
	return obs
}

func studyReport(t *testing.T, title string, obs []studyObservation) studyConfirm {
	t.Helper()
	c := studyConfirmAnalysis(obs)
	var b strings.Builder
	fmt.Fprintf(&b, "\n%s: %d findings in the stratum (%d real, %d not real)\n", title, c.n, c.real, c.notReal)
	b.WriteString("method        AUC    support minus method [95% interval over fixtures]   at the frozen threshold: not-real withheld / real lost\n")
	for _, m := range []string{"confidence", "severity", "jev", "support:jev"} {
		var neg, pos, negUnder, posUnder float64
		for _, o := range obs {
			if !studyInStratum(o) {
				continue
			}
			v, ok := studyValue(o, m)
			if !ok {
				continue
			}
			if o.Real {
				pos++
				if v < studyFrozen[m] {
					posUnder++
				}
			} else {
				neg++
				if v < studyFrozen[m] {
					negUnder++
				}
			}
		}
		d := "                                    "
		if x, ok := c.diffs[m]; ok {
			d = fmt.Sprintf("%+.3f [%+.3f, %+.3f]                     ", x[0], x[1], x[2])
		}
		fmt.Fprintf(&b, "%-12s %.3f  %s  %.3f / %.3f\n", m, c.auc[m], d, safeDiv(negUnder, neg), safeDiv(posUnder, pos))
	}
	fmt.Fprintf(&b, "verdict by the pre-registered rules: %s\n", c.verdict)
	t.Log(b.String())
	return c
}

func safeDiv(a, b float64) float64 {
	if b == 0 {
		return 0
	}
	return a / b
}

// TestJevHeldOutRehearsalOnTheDevelopmentData runs the pre-registered analysis on the data the
// thresholds came from, so the code is known to run end to end before there is anything to
// protect. Its verdict is not a result: the thresholds were chosen on this data.
func TestJevHeldOutRehearsalOnTheDevelopmentData(t *testing.T) {
	if os.Getenv("NITPICK_JEV_STUDY") != "1" {
		t.Skip("set NITPICK_JEV_STUDY=1")
	}
	studyReport(t, "REHEARSAL on the development observations", studyLoadFile(t, "testdata/jev-dev-observations.json"))
}

// TestJevHeldOutConfirm is the one look at the held-out collection.
func TestJevHeldOutConfirm(t *testing.T) {
	path := os.Getenv("NITPICK_JEV_HELDOUT_FILE")
	if os.Getenv("NITPICK_JEV_STUDY") != "1" || path == "" {
		t.Skip("set NITPICK_JEV_STUDY=1 and NITPICK_JEV_HELDOUT_FILE")
	}
	studyReport(t, "HELD-OUT", studyLoadFile(t, path))
}
