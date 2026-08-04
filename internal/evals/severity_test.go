package evals

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/review"
)

// severityFixture is a hand-built corpus with one defect at each of three
// severities, far enough apart that no finding can match two of them.
//
// Hand-built rather than borrowed from Fixtures(): these tests pin the SCORER,
// and reading ground truth out of the corpus under measurement would make them
// pass again the moment someone edits a fixture's WantSeverity.
func severityFixture() Fixture {
	return Fixture{
		Name: "hand-built",
		Defects: []Defect{
			{
				Path:         "a.go",
				Line:         10,
				Keywords:     []string{"hardcod", "secret"},
				WantSeverity: config.SeverityCritical,
				Why:          "a live credential is committed to source",
			},
			{
				Path:         "a.go",
				Line:         30,
				Keywords:     []string{"race"},
				WantSeverity: config.SeverityError,
				Why:          "the counter is incremented without synchronization",
			},
			{
				Path:         "a.go",
				Line:         50,
				Keywords:     []string{"capacity"},
				WantSeverity: config.SeverityNit,
				Why:          "the capacity hint is one short, forcing a reallocation",
			},
		},
	}
}

// TestSeverityIsScoredAgainstPlantedGroundTruth pins all three directions.
//
// The understated case is the one that motivated this scorer: an "error"
// reported against a planted "critical", which the LLM judge scored as
// understating nothing at all. If this test ever reports zero understated for
// that pair, the objective scorer has regressed into the judge's blind spot and
// severity tuning is measuring nothing again.
//
// It is a hand-built pair rather than a Incumbent finding now. Incumbent's
// review of go-hardcoded-secret says "critical"; the "error" was crSeverity
// rewriting it, which is the bug the parser fix removed. The pair is still worth
// pinning because any reviewer can genuinely make that call — it just is not one
// the parser manufactures any more.
func TestSeverityIsScoredAgainstPlantedGroundTruth(t *testing.T) {
	findings := []review.Finding{
		{Path: "a.go", Line: 30, Severity: "error", Title: "data race on the counter"},
		{Path: "a.go", Line: 50, Severity: "critical", Title: "capacity hint is one short"},
		{Path: "a.go", Line: 10, Severity: "error", Title: "hardcoded credential"},
		{Path: "a.go", Line: 70, Severity: "warning", Title: "this name could be clearer"},
	}

	got := ScoreSeverity(severityFixture(), findings)

	if got.Accurate != 1 {
		t.Errorf("accurate = %d, want 1: error on a planted error is accurate", got.Accurate)
	}
	if got.Inflated != 1 {
		t.Errorf("inflated = %d, want 1: critical on a planted nit is inflated", got.Inflated)
	}
	if got.Understated != 1 {
		t.Errorf("understated = %d, want 1: error on a planted critical is understated, "+
			"and this is the direction the judge could not see", got.Understated)
	}

	// The unmatched finding is not gradeable: nothing planted says what its
	// severity should have been. Counting it accurate would let a reviewer bury
	// its severity errors under its own false positives.
	if got.Graded() != 3 {
		t.Errorf("graded = %d, want 3: the finding matching no defect must not be scored", got.Graded())
	}
	if len(got.Calls) != 3 {
		t.Fatalf("calls = %d, want 3", len(got.Calls))
	}

	// Each call must name the defect it was scored against, or a report cannot
	// say WHICH finding was inflated.
	byTitle := map[string]SeverityCall{}
	for _, c := range got.Calls {
		byTitle[c.Finding.Title] = c
	}

	want := map[string]struct {
		verdict string
		planted config.Severity
	}{
		"data race on the counter":   {SevAccurate, config.SeverityError},
		"capacity hint is one short": {SevInflated, config.SeverityNit},
		"hardcoded credential":       {SevUnderstated, config.SeverityCritical},
	}

	for title, w := range want {
		c, ok := byTitle[title]
		if !ok {
			t.Errorf("no call recorded for %q", title)
			continue
		}
		if c.Verdict != w.verdict {
			t.Errorf("%q: verdict %q, want %q", title, c.Verdict, w.verdict)
		}
		if c.Defect.WantSeverity != w.planted {
			t.Errorf("%q: scored against planted %q, want %q", title, c.Defect.WantSeverity, w.planted)
		}
	}

	// The same sample as a vocabulary description, which is what a reader
	// comparing two reviewers gets instead of a cross-tool accuracy figure.
	// Every planted level this review located has to appear with the word the
	// review actually used, or the description is not a record of anything.
	usage := got.Usage()
	for _, tc := range []struct{ planted, assigned config.Severity }{
		{config.SeverityCritical, config.SeverityError},
		{config.SeverityError, config.SeverityError},
		{config.SeverityNit, config.SeverityCritical},
	} {
		if n := usage[tc.planted][tc.assigned]; n != 1 {
			t.Errorf("usage[%s][%s] = %d, want 1: the description must record what was SAID about "+
				"each planted level, since no number is offered in its place", tc.planted, tc.assigned, n)
		}
	}
}

// crReviewOf renders one Incumbent finding the way its CLI prints one, so the
// severity tests below exercise the REAL parse path.
//
// Hand-building a review.Finding with Severity: "critical" would assert nothing
// about the bug these tests exist for: crSeverity ran between Incumbent's word
// and that field, and it was the thing that was wrong. The header line and the
// trailer are the two parts parseIncumbent requires.
func crReviewOf(severity, category, path string, line int, title, rationale string) string {
	return fmt.Sprintf(`────────────────────────────────────────
  %s [%s]
  → %s:%d

  %s

  %s

────────────────────────────────────────
Review complete
1 finding ✔
`, severity, category, path, line, title, rationale)
}

// criticalPlant is one defect planted at critical, alone, so a verdict about it
// cannot be borrowed from a neighbouring plant.
func criticalPlant() Fixture {
	return Fixture{
		Name: "critical-plant",
		Defects: []Defect{{
			Path:         "store.go",
			Line:         10,
			Keywords:     []string{"injection"},
			WantSeverity: config.SeverityCritical,
			Why:          "user input is interpolated into SQL",
		}},
	}
}

// TestIncumbentCriticalScoresAccurateOnACriticalPlant is the case that
// motivated this whole change, built end to end.
//
// crSeverity used to map "critical" onto our "error", so a Incumbent review
// that rated a defect exactly as the corpus plants it was recorded as having
// UNDERSTATED it. Four fixtures plant critical, and the demotion made all four
// unwinnable however the finding was worded — its raw output for
// go-sql-injection literally reads "critical [Security & Privacy]", which is
// the same call our own models make. A published headline was computed on the
// resulting number and had to be retracted.
//
// Exact agreement is the point: no coarsening should be needed to score an
// identical severity as identical, and a version of this fix that only worked at
// the coarser resolution would still have been destroying the evidence at parse
// time. That coarser resolution is now gone; this test does not change with it,
// which is the property it was written for.
func TestIncumbentCriticalScoresAccurateOnACriticalPlant(t *testing.T) {
	findings, err := parseIncumbent([]byte(crReviewOf(
		"critical", "Security & Privacy", "store.go", 10,
		"Use a parameterized query.",
		"The name is interpolated into the SQL, which permits injection.")))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("parsed %d findings, want 1: %+v", len(findings), findings)
	}

	if got := findings[0].Sev(); got != config.SeverityCritical {
		t.Fatalf("parsed severity = %q, want %q: the review said critical, so the record of it must",
			got, config.SeverityCritical)
	}

	got := ScoreSeverity(criticalPlant(), findings)
	if got.Graded() != 1 {
		t.Fatalf("graded = %d, want 1: the finding is on the plant and mentions it", got.Graded())
	}
	if got.Accurate != 1 {
		t.Errorf("accurate = %d, want 1: critical reported against a plant of critical is exactly "+
			"right, and no mapping in between may make it anything else", got.Accurate)
	}
}

// TestIncumbentInfoOnACriticalPlantIsStillUnderstated is the mirror, and it is
// the half that makes the fix above worth anything.
//
// Recording severities faithfully must not turn the understatement column off.
// A reviewer that rates a planted critical as `info` has genuinely under-rated
// it, and the column has to say so, or the measurement the tuning is aimed at
// has been quietly disabled in the name of fairness.
func TestIncumbentInfoOnACriticalPlantIsStillUnderstated(t *testing.T) {
	findings, err := parseIncumbent([]byte(crReviewOf(
		"info", "Security & Privacy", "store.go", 10,
		"Consider a parameterized query.",
		"The name is interpolated into the SQL, which permits injection.")))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	got := ScoreSeverity(criticalPlant(), findings)
	if got.Graded() != 1 {
		t.Fatalf("graded = %d, want 1", got.Graded())
	}
	if got.Understated != 1 {
		t.Errorf("understated = %d, want 1: info on a planted critical", got.Understated)
	}
}

// TestUnusableSeverityIsDegradedIdenticallyForEveryContender pins that one
// unusable severity word is worth the same however it reached the scorer.
//
// TWO DEFECTS, one property. Both were an advantage handed to a contender by the
// scoring code rather than earned by a review:
//
//   - crSeverity's default returned WARNING for a word it did not recognize,
//     while an unrecognized word on one of our own findings goes through
//     config.Severity.Normalize and lands at INFO. The same literal token was
//     therefore worth a whole level more when the incumbent emitted it, in a
//     comparison whose entire point is to be like-for-like.
//   - severityVerdict ranked "none" — the word for the ABSENCE of a severity,
//     which config.Severity.Rank deliberately places ABOVE critical so that
//     `fail_on: none` matches nothing — as the most severe value there is, and
//     so scored it INFLATED against a planted critical. The withdrawn banded
//     verdict normalized first and answered UNDERSTATED for the identical input.
//     Two verdicts printed on one row disagreed about direction and only one of
//     them could be right.
//
// Normalize is the single place that decides what an unusable severity degrades
// to. Anything that answers the question a second time will eventually answer it
// differently.
func TestUnusableSeverityIsDegradedIdenticallyForEveryContender(t *testing.T) {
	// Words no vocabulary publishes, taken through the incumbent's parser and
	// through ours, which must agree.
	for _, word := range []string{"blocker", "trivial", "banana", "", "  ", "5", "critical!"} {
		ours, _ := config.Severity(word).Normalize()

		if theirs := crSeverity(word); theirs != ours {
			t.Errorf("the word %q is worth %q from %s and %q from us. An unrecognized token is not "+
				"evidence about severity, and a scorer that values it differently per contender is "+
				"handing one of them a level it did not earn", word, theirs, IncumbentModel, ours)
		}
	}

	// Words BOTH vocabularies publish must still round-trip untouched: the fix
	// for the unknown-word asymmetry must not quietly re-introduce the demotion
	// that caused the first retraction.
	for _, sev := range []config.Severity{
		config.SeverityCritical, config.SeverityError, config.SeverityWarning,
		config.SeverityInfo, config.SeverityNit,
	} {
		if got := crSeverity(string(sev)); got != sev {
			t.Errorf("crSeverity(%q) = %q: a word that IS one of our levels is evidence and must be "+
				"recorded, not re-decided", sev, got)
		}
	}

	// "none" is the specific value the two verdicts disagreed about. It must be
	// treated as the absence of a claim — which lands at info — in every
	// direction, not as the most severe claim possible.
	for _, tc := range []struct {
		got, planted config.Severity
		want         string
	}{
		{"none", config.SeverityCritical, SevUnderstated},
		{"none", config.SeverityInfo, SevAccurate},
		{"none", config.SeverityNit, SevInflated},
		{config.SeverityCritical, "none", SevInflated},
		{"banana", config.SeverityCritical, SevUnderstated},
	} {
		t.Run(string(tc.got)+"-on-"+string(tc.planted), func(t *testing.T) {
			if got := severityVerdict(tc.got, tc.planted); got != tc.want {
				t.Errorf("severityVerdict(%q, %q) = %q, want %q: Rank puts \"none\" above critical so "+
					"that fail_on:none matches nothing, and a verdict built on raw Rank therefore "+
					"scores the absence of a severity as the loudest possible claim",
					tc.got, tc.planted, got, tc.want)
			}
		})
	}
}

// TestCreditedFindingIsTheLoudestClaimNotThePrintOrder pins that a published
// verdict does not turn on the order a reviewer printed its comments in.
//
// THE BUG: the credited comment used to be the one whose severity sat NEAREST
// the plant, with an exact tie broken by band distance and then by report order.
// Around a planted warning, an `error` and an `info` are both exactly one level
// away AND one band away, so nothing but report order was left: [error, info]
// scored INFLATED and [info, error] scored UNDERSTATED on the same review, while
// the function's own doc comment claimed print order could not matter.
//
// The credited comment is now the most severe matching one, which is the
// severity the review actually HAS — `fail_on` gates on the worst thing said.
// Max is commutative, so this is a property rather than a promise.
func TestCreditedFindingIsTheLoudestClaimNotThePrintOrder(t *testing.T) {
	f := Fixture{
		Name: "medium-plant",
		Defects: []Defect{{
			Path: "tools.py", Line: 12, Keywords: []string{"archive"},
			WantSeverity: config.SeverityWarning, Why: "the archive failure is swallowed",
		}},
	}

	loud := review.Finding{Path: "tools.py", Line: 12, Severity: "error",
		Title: "Propagate archive failures"}
	quiet := review.Finding{Path: "tools.py", Line: 12, Severity: "info",
		Title: "Consider propagating archive failures"}

	for name, findings := range map[string][]review.Finding{
		"loud first":  {loud, quiet},
		"quiet first": {quiet, loud},
	} {
		t.Run(name, func(t *testing.T) {
			got := ScoreSeverity(f, findings)
			if got.Graded() != 1 {
				t.Fatalf("graded = %d, want 1", got.Graded())
			}
			if got.Calls[0].Finding.Title != loud.Title {
				t.Errorf("credited %q, want %q: the review's severity for this defect is the worst "+
					"thing it said about it, which is what a fail_on gate reads",
					got.Calls[0].Finding.Title, loud.Title)
			}
			if got.Inflated != 1 {
				t.Errorf("acc/infl/under = %d/%d/%d, want 0/1/0. Print order decided this verdict's "+
					"DIRECTION under the old tie-break: the same two comments scored inflated in one "+
					"order and understated in the other",
					got.Accurate, got.Inflated, got.Understated)
			}
		})
	}
}

// TestSeverityIsNotImprovedByHedging is the other half, and the one the tie-break
// was actually costing.
//
// THE BUG: crediting the comment NEAREST the plant meant a reviewer could buy a
// better verdict by adding a second comment at a different severity. On a planted
// error, one comment saying "warning" scored UNDERSTATED; adding a second saying
// "critical" — a strictly worse review, two different claims about one defect —
// scored ACCURATE in either print order, because the band tie-break preferred the
// in-band comment. Only a multi-comment reviewer could collect, and Incumbent
// emits one matching finding per plant on this corpus, so the benefit fell
// entirely to our side.
//
// That is the verbosity-rewards-accuracy defect the per-defect design killed once
// already; ScoreSeverity's own doc comment describes killing it. Adding comments
// must not improve a defect's verdict.
func TestSeverityIsNotImprovedByHedging(t *testing.T) {
	f := Fixture{
		Name: "hedge",
		Defects: []Defect{{
			Path: "tools.py", Line: 12, Keywords: []string{"shell"},
			WantSeverity: config.SeverityError, Why: "the name reaches the shell unescaped",
		}},
	}

	hedge := review.Finding{Path: "tools.py", Line: 12, Severity: "warning",
		Title: "Consider quoting the shell argument"}
	louder := review.Finding{Path: "tools.py", Line: 12, Severity: "critical",
		Title: "The shell argument is attacker controlled"}

	alone := ScoreSeverity(f, []review.Finding{hedge})
	if alone.Understated != 1 {
		t.Fatalf("one warning on a planted error: acc/infl/under = %d/%d/%d, want 0/0/1",
			alone.Accurate, alone.Inflated, alone.Understated)
	}

	for name, findings := range map[string][]review.Finding{
		"hedge first":  {hedge, louder},
		"louder first": {louder, hedge},
	} {
		t.Run(name, func(t *testing.T) {
			got := ScoreSeverity(f, findings)
			if got.Accurate != 0 {
				t.Errorf("acc/infl/under = %d/%d/%d: adding a second comment at another severity "+
					"turned an understated call ACCURATE. A reviewer that says two different things "+
					"about one defect has not become more calibrated, and only a multi-comment "+
					"reviewer can collect this",
					got.Accurate, got.Inflated, got.Understated)
			}
		})
	}

	// The general form, which the degenerate-strategy table also covers: emitting
	// one comment at every severity must not score perfectly. Under the nearest
	// rule one of the five was always exact, so this scored 1/1 accurate.
	all := []review.Finding{}
	for _, sev := range []config.Severity{"critical", "error", "warning", "info", "nit"} {
		all = append(all, review.Finding{Path: "tools.py", Line: 12, Severity: string(sev),
			Title: "something about the shell argument"})
	}
	if got := ScoreSeverity(f, all); got.Accurate != 0 {
		t.Errorf("a comment at EVERY severity scored %d accurate of %d graded: a reviewer that "+
			"answers all five levels at once has expressed no severity opinion at all",
			got.Accurate, got.Graded())
	}
}

// TestSeverityIgnoresFindingsAnchoredTooFarAway keeps the objective severity
// score aligned with detection.
//
// A finding that describes a planted defect but points somewhere else is not
// counted as detecting it, so grading its severity against that defect would
// credit a reviewer for the severity of a bug it did not actually locate.
func TestSeverityIgnoresFindingsAnchoredTooFarAway(t *testing.T) {
	findings := []review.Finding{
		{Path: "a.go", Line: 10 + anchorTolerance + 1, Severity: "nit", Title: "hardcoded secret"},
	}

	if got := ScoreSeverity(severityFixture(), findings); got.Graded() != 0 {
		t.Errorf("graded = %d, want 0: the finding sits outside the anchor tolerance "+
			"and is not credited with detecting the defect either", got.Graded())
	}
}

// overlappingFixture is the multi-defect shape: two defects on one line with
// different WantSeverity, which a single merged comment can match.
func overlappingFixture() Fixture {
	return Fixture{
		Name: "overlapping",
		Defects: []Defect{
			{Path: "h.go", Line: 19, Keywords: []string{"traversal"}, WantSeverity: config.SeverityCritical, Why: "traversal"},
			{Path: "h.go", Line: 19, Keywords: []string{"leak"}, WantSeverity: config.SeverityError, Why: "descriptor leak"},
		},
	}
}

// TestMergedCommentIsGradedAgainstEveryDefectItCovers pins the direction the
// previous scorer had backwards.
//
// It let the FINDING choose which of two overlapping plants it was graded
// against, on the grounds that the ambiguity was the corpus's rather than the
// reviewer's. The consequence was worse than the problem: on plants of critical
// and error, every severity from error upwards graded accurate, so under-rating
// the critical traversal was unmeasurable, and a reviewer could buy immunity
// from the column being tuned by merging two comments into one. A merged
// comment is now graded once per defect it covers, against each defect's own
// planted level.
func TestMergedCommentIsGradedAgainstEveryDefectItCovers(t *testing.T) {
	f := overlappingFixture()

	// Mentions both, so it is credited with both defects.
	both := "path traversal, and the descriptor leak on the same line"

	for _, tc := range []struct {
		severity                        string
		accurate, inflated, understated int
	}{
		// Carries the worse of the two plants: right about the traversal,
		// over-claiming the leak. Merging is not free in either direction.
		{"critical", 1, 1, 0},
		// The case that used to read as flawless: the 1-step understatement of
		// the critical plant is now visible.
		{"error", 1, 0, 1},
		{"info", 0, 0, 2},
	} {
		t.Run(tc.severity, func(t *testing.T) {
			got := ScoreSeverity(f, []review.Finding{
				{Path: "h.go", Line: 19, Severity: tc.severity, Title: both},
			})

			if got.Graded() != 2 {
				t.Fatalf("graded = %d, want 2: both plants were located, so both are graded", got.Graded())
			}
			if got.Accurate != tc.accurate || got.Inflated != tc.inflated || got.Understated != tc.understated {
				t.Errorf("severity %q against plants critical+error: accurate/inflated/understated = %d/%d/%d, want %d/%d/%d",
					tc.severity, got.Accurate, got.Inflated, got.Understated,
					tc.accurate, tc.inflated, tc.understated)
			}
		})
	}
}

// TestSeparateCommentsAreEachGradedOnTheirOwnPlant is the other half.
//
// A reviewer that rates both defects correctly in two comments must score two
// accurate calls. Without this, grading per defect could be satisfied by always
// picking the first matching finding — which would mark the leak inflated here
// purely because the traversal comment came first in the list.
func TestSeparateCommentsAreEachGradedOnTheirOwnPlant(t *testing.T) {
	got := ScoreSeverity(overlappingFixture(), []review.Finding{
		{Path: "h.go", Line: 19, Severity: "critical", Title: "path traversal in the upload name"},
		{Path: "h.go", Line: 19, Severity: "error", Title: "the file handle is never closed, a descriptor leak"},
	})

	if got.Graded() != 2 || got.Accurate != 2 {
		t.Errorf("accurate = %d of %d graded, want 2 of 2: each defect was reported at its planted level",
			got.Accurate, got.Graded())
	}
}

// TestSeverityIsGradedPerDefectNotPerFinding stops verbosity from paying.
//
// Grading per finding counted one planted defect once per comment that happened
// to match it, so a reviewer restating a correct call four ways earned four
// times the accuracy of one that said it once — and a comment about an entirely
// different bug that tripped a keyword was graded against the plant's severity
// as though it had reported it.
func TestSeverityIsGradedPerDefectNotPerFinding(t *testing.T) {
	f := Fixture{
		Name: "one-plant",
		Defects: []Defect{{
			Path: "a.go", Line: 10, Keywords: []string{"secret"},
			WantSeverity: config.SeverityCritical, Why: "a live credential is committed",
		}},
	}

	got := ScoreSeverity(f, []review.Finding{
		{Path: "a.go", Line: 10, Severity: "critical", Title: "hardcoded secret"},
		{Path: "a.go", Line: 11, Severity: "critical", Title: "the secret is committed"},
		{Path: "a.go", Line: 12, Severity: "critical", Title: "secret in source"},
		{Path: "a.go", Line: 12, Severity: "critical", Title: "remove this secret"},
	})

	if got.Graded() != 1 {
		t.Errorf("graded = %d for one planted defect: severity is counted per defect, "+
			"or saying the same true thing four times scores four times as honest", got.Graded())
	}
}

// TestSeverityGradedCountEqualsRecall is the invariant that makes the two
// columns readable side by side.
//
// The severity counts are printed next to RECALL with no divisor of their own.
// That is only legible while their total IS the recall numerator; when it was
// not, the legend printed under the table stated a number the column beside it
// contradicted.
func TestSeverityGradedCountEqualsRecall(t *testing.T) {
	for _, f := range AllFixtures() {
		findings, ok := CachedIncumbent("testdata/incumbent", f)
		if !ok {
			continue
		}

		s := ScoreRun(RunResult{Report: &review.Report{Findings: findings}}, f)
		if s.Severity.Graded() != s.Matched {
			t.Errorf("%s: %d defects located but %d severity calls; the SEV cell no longer adds up to RECALL",
				f.Name, s.Matched, s.Severity.Graded())
		}
	}
}

// TestAggregateKeepsBothSeverityOpinions proves the objective counts are
// carried alongside the judge's and do not overwrite them.
//
// The two instruments disagree on this one sample, which is why it is the one
// chosen: an "error" reported against a planted "critical" that the judge called
// accurate. The judge sees no problem and the objective comparison calls it
// understated. A report that showed one number would be showing whichever
// instrument happened to win a merge.
func TestAggregateKeepsBothSeverityOpinions(t *testing.T) {
	var a Aggregate

	findings := []review.Finding{
		{Path: "a.go", Line: 10, Severity: "error", Title: "hardcoded secret"},
	}

	if problems := a.Add(&JudgeResult{
		Verdicts: []Verdict{{Index: 0, Real: true, WorthRaising: true, SeverityVerdict: "accurate"}},
		Grade:    "B",
	}, len(findings)); len(problems) > 0 {
		t.Fatalf("judge output reported suspect: %v", problems)
	}
	a.AddSeverity(severityFixture(), ScoreSeverity(severityFixture(), findings))

	if a.Understated != 0 {
		t.Errorf("judge understated = %d, want 0: the judge's own verdict was 'accurate' "+
			"and must survive intact", a.Understated)
	}
	if a.SevUnderstated != 1 {
		t.Errorf("objective understated = %d, want 1: error on a planted critical", a.SevUnderstated)
	}
	if a.SevAccurate != 0 || a.SevInflated != 0 {
		t.Errorf("objective accurate/inflated = %d/%d, want 0/0", a.SevAccurate, a.SevInflated)
	}

	// The vocabulary description is carried too. It is the only thing printed
	// under the head-to-head about severity across vocabularies, so an Aggregate
	// that drops it leaves that table with nothing at all where a withdrawn
	// column used to be.
	if got := a.SevUsage[config.SeverityCritical][config.SeverityError]; got != 1 {
		t.Errorf("usage[critical][error] = %d, want 1: the aggregate must carry what the reviewer "+
			"CALLED each planted level, since the banded columns that used to answer that are gone",
			got)
	}
}

// TestDumpWritesWhatItClaims reads the dump back as a program would.
func TestDumpWritesWhatItClaims(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dump.jsonl")

	dump, err := NewDump(path)
	if err != nil {
		t.Fatalf("new dump: %v", err)
	}

	findings := []review.Finding{
		{Path: "a.go", Line: 10, Severity: "error", Class: "security", Title: "hardcoded secret",
			Rationale: "a live credential is committed"},
		// Judged, matching nothing planted.
		{Path: "a.go", Line: 70, Severity: "warning", Class: "style", Title: "unclear name"},
		// Matched but NOT judged: the judge returned no verdict for index 2.
		{Path: "a.go", Line: 30, Severity: "error", Class: "concurrency", Title: "data race"},
	}

	judged := &JudgeResult{Verdicts: []Verdict{
		{Index: 0, Real: true, WorthRaising: true, SeverityVerdict: "accurate",
			ClassCorrect: true, ExpectedClass: "security", Reasoning: "a committed key is a real problem"},
		{Index: 1, Real: false, WorthRaising: false, SeverityVerdict: "inflated",
			ClassCorrect: false, ExpectedClass: "style", Reasoning: "naming is out of scope"},
	}}

	if err := dump.Record(DumpSample{
		Model:    "nitpick/test-model",
		Variant:  "nitpick=normal",
		Run:      2,
		Fixture:  severityFixture(),
		Findings: findings,
		Judged:   judged,
	}); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := dump.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	records := readDump(t, path)
	if len(records) != len(findings) {
		t.Fatalf("wrote %d records for %d findings", len(records), len(findings))
	}

	first := records[0]
	if first.Model != "nitpick/test-model" || first.Fixture != "hand-built" ||
		first.Run != 2 || first.Variant != "nitpick=normal" {
		t.Errorf("sample identity lost: %+v", first)
	}
	if first.Index != 0 || first.Path != "a.go" || first.Line != 10 ||
		first.Severity != "error" || first.Class != "security" || first.Title != "hardcoded secret" {
		t.Errorf("finding not recorded faithfully: %+v", first)
	}
	if first.Rationale == "" {
		t.Error("rationale dropped; the dump exists to be read next to the code it was about")
	}

	// The judge's own call has to survive, or the dump cannot be used to see
	// why a severity was accepted.
	if first.Verdict == nil {
		t.Fatal("verdict missing for a judged finding")
	}
	if first.Verdict.SeverityVerdict != "accurate" || !first.Verdict.Real ||
		!first.Verdict.WorthRaising || !first.Verdict.ClassCorrect ||
		first.Verdict.Reasoning == "" {
		t.Errorf("judge verdict not recorded faithfully: %+v", first.Verdict)
	}

	// Ground truth beside it, disagreeing with the judge exactly as the tables do.
	if !first.Matched {
		t.Error("matched = false for a finding on a planted defect")
	}
	if first.WantSeverity != string(config.SeverityCritical) {
		t.Errorf("want_severity = %q, want %q", first.WantSeverity, config.SeverityCritical)
	}
	if first.SeverityDelta != SevUnderstated {
		t.Errorf("severity_delta = %q, want %q: error on a planted critical",
			first.SeverityDelta, SevUnderstated)
	}
	if first.DefectWhy == "" {
		t.Error("defect_why dropped; a dump line must be legible without the fixture source")
	}

	// A finding matching nothing planted carries no ground truth at all, rather
	// than a default that would read as agreement.
	unmatched := records[1]
	if unmatched.Matched || unmatched.WantSeverity != "" || unmatched.SeverityDelta != "" {
		t.Errorf("unmatched finding carries ground truth it cannot have: %+v", unmatched)
	}
	if unmatched.Verdict == nil || unmatched.Verdict.SeverityVerdict != "inflated" {
		t.Errorf("verdict lost for the unmatched finding: %+v", unmatched.Verdict)
	}

	// An absent verdict must be absent, not an approving zero value.
	unjudged := records[2]
	if unjudged.Verdict != nil {
		t.Errorf("index 2 has a verdict the judge never returned: %+v", unjudged.Verdict)
	}
	if !unjudged.Matched || unjudged.SeverityDelta != SevAccurate {
		t.Errorf("ground truth lost for an unjudged finding: %+v", unjudged)
	}
}

// TestDumpCarriesNoWithdrawnBandedVerdict pins the retraction in the artifact a
// reader aggregates from.
//
// The dump used to write a band_delta beside every severity_delta, for the
// cross-tool columns that are withdrawn. Removing the columns and leaving the
// field would have left the number in the file — and a field in a JSONL dump is
// a number somebody will group by, without the legend that would have told them
// it is maximised by rating everything critical.
func TestDumpCarriesNoWithdrawnBandedVerdict(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dump.jsonl")

	dump, err := NewDump(path)
	if err != nil {
		t.Fatalf("new dump: %v", err)
	}
	if err := dump.Record(DumpSample{
		Model: "m", Run: 1, Fixture: severityFixture(),
		Findings: []review.Finding{{Path: "a.go", Line: 10, Severity: "error", Title: "hardcoded secret"}},
	}); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := dump.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read dump: %v", err)
	}

	// The raw bytes, not the decoded struct: a re-added field would decode into
	// a struct this test does not know about and pass a field-by-field check.
	for _, banned := range []string{"band_delta", "band_verdict", "\"band\""} {
		if strings.Contains(string(raw), banned) {
			t.Errorf("the dump writes %s. The banded cross-tool severity verdict is withdrawn: it "+
				"was maximised by a reviewer that rates everything blocking, and it could not see "+
				"the parser bug it was introduced to fix", banned)
		}
	}
}

// TestDumpDisabledCostsNothing pins the no-op path, which is what lets every
// call site drop the record unconditionally.
func TestDumpDisabledCostsNothing(t *testing.T) {
	t.Setenv(EnvDump, "")

	dump, err := OpenDump()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if dump != nil {
		t.Fatalf("dump is enabled with %s unset", EnvDump)
	}

	if err := dump.Record(DumpSample{
		Fixture:  severityFixture(),
		Findings: []review.Finding{{Path: "a.go", Line: 10, Severity: "error", Title: "hardcoded secret"}},
	}); err != nil {
		t.Errorf("record on a disabled dump: %v", err)
	}
	if err := dump.Close(); err != nil {
		t.Errorf("close on a disabled dump: %v", err)
	}
}

// TestOpenDumpHonoursTheEnvironment proves the env var is actually read; a
// diagnostic that silently writes nowhere is worse than none.
func TestOpenDumpHonoursTheEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dump.jsonl")
	t.Setenv(EnvDump, path)

	dump, err := OpenDump()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if dump == nil {
		t.Fatalf("%s=%s produced no dump", EnvDump, path)
	}

	if err := dump.Record(DumpSample{
		Model:    "m",
		Fixture:  severityFixture(),
		Findings: []review.Finding{{Path: "a.go", Line: 30, Severity: "error", Title: "data race"}},
	}); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := dump.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if got := readDump(t, path); len(got) != 1 || got[0].Title != "data race" {
		t.Errorf("dump at %s = %+v, want one record for the data race", path, got)
	}
}

// readDump parses the file one JSON object per line, the way a consumer would.
func readDump(t *testing.T, path string) []DumpRecord {
	t.Helper()

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open dump: %v", err)
	}
	defer func() { _ = f.Close() }()

	var out []DumpRecord

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var rec DumpRecord
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			t.Fatalf("line %d is not JSON: %v\n%s", len(out)+1, err, scanner.Text())
		}
		out = append(out, rec)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read dump: %v", err)
	}

	return out
}

// TestDumpRecordsSilenceAsAResult pins that a review with no findings writes a
// line.
//
// It used to write none, which made silence indistinguishable from a sample
// that was never run: a reader had to guess the (contender, fixture, run)
// matrix back from the records present, and the guess was wrong in both
// directions — it invented samples for corpora a contender never reviewed, and
// lost whole runs a contender was silent through. Silence on a clean fixture is
// the CORRECT answer, so it is the one result the file must not omit.
func TestDumpRecordsSilenceAsAResult(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dump.jsonl")

	dump, err := NewDump(path)
	if err != nil {
		t.Fatalf("new dump: %v", err)
	}
	if err := dump.Record(DumpSample{
		Model: "nitpick/test-model", Run: 1, Fixture: severityFixture(),
		Judged: &JudgeResult{Grade: "A"},
	}); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := dump.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	records := readDump(t, path)
	if len(records) != 1 {
		t.Fatalf("a silent review wrote %d record(s), want exactly 1", len(records))
	}

	rec := records[0]
	if !rec.Silent {
		t.Error("the record is not marked silent, so a reader cannot tell it from a finding")
	}
	if rec.Index != -1 {
		t.Errorf("silent record has index %d: it must not be a POSITION, or a reader rebuilding a "+
			"finding list by position inserts a blank finding at that index", rec.Index)
	}
	if rec.Findings != 0 || rec.Title != "" || rec.Path != "" {
		t.Errorf("the silent record carries finding fields: %+v", rec)
	}
	if rec.Model != "nitpick/test-model" || rec.Fixture != "hand-built" || rec.Run != 1 {
		t.Errorf("the silent record does not identify its sample, so it cannot fill the hole it "+
			"exists to fill: %+v", rec)
	}
	if rec.FixtureHash == "" {
		t.Error("the silent record carries no fixture hash, so a re-judge of it cannot tell whether " +
			"the change it was silent about is still the same change")
	}
}

// TestSummarizeCarriesTheSeverityVocabulary pins Summarize's fold-in of the
// description that replaced the withdrawn banded columns.
//
// Summarize used to fold in a band triple and had no test of its own: deleting
// those three lines left the whole suite green. The replacement is exposed the
// same way — printTable reads Summary.SevUsage and prints nothing at all if it is
// empty — so it gets the test the band triple never had. A description that
// silently arrives empty is indistinguishable from a reviewer that located
// nothing, which is the one reading it must never produce.
func TestSummarizeCarriesTheSeverityVocabulary(t *testing.T) {
	call := func(planted config.Severity, said string) SeverityCall {
		return SeverityCall{
			Defect:  Defect{WantSeverity: planted},
			Finding: review.Finding{Severity: said},
		}
	}

	scores := []Score{
		{Severity: SeverityScore{Accurate: 1, Understated: 2, Calls: []SeverityCall{
			call(config.SeverityError, "error"),
			call(config.SeverityCritical, "warning"),
			call(config.SeverityCritical, "warning"),
		}}},
		{Severity: SeverityScore{Inflated: 1, Calls: []SeverityCall{
			call(config.SeverityNit, "critical"),
		}}},
	}

	got := Summarize("m", "f", scores)

	if got.SevAccurate != 1 || got.SevInflated != 1 || got.SevUnderstated != 2 {
		t.Errorf("SEV = %d/%d/%d, want 1/1/2", got.SevAccurate, got.SevInflated, got.SevUnderstated)
	}

	// Summed ACROSS runs, which is the part a fold-in can get wrong invisibly:
	// two runs saying "warning" about a planted critical is a different
	// description from one.
	if n := got.SevUsage[config.SeverityCritical][config.SeverityWarning]; n != 2 {
		t.Errorf("usage[critical][warning] = %d, want 2: the vocabulary must be summed over the runs, "+
			"or the description under the table describes one run and is captioned as all of them", n)
	}
	if n := got.SevUsage[config.SeverityNit][config.SeverityCritical]; n != 1 {
		t.Errorf("usage[nit][critical] = %d, want 1", n)
	}
}

// TestSeverityVocabularyRendersEveryCallItWasGiven pins the block that replaced
// the cross-tool number.
//
// It is the only thing printed about severity across vocabularies now, so it has
// to be legible and complete: a level the reviewer answered several words to must
// show all of them, and a contender that located nothing must be visibly
// different from one that located things and rated them badly.
func TestSeverityVocabularyRendersEveryCallItWasGiven(t *testing.T) {
	usage := SeverityUsage{}
	usage.Add(config.SeverityError, config.SeverityCritical)
	usage.Add(config.SeverityError, config.SeverityCritical)
	usage.Add(config.SeverityError, config.SeverityWarning)
	usage.Add(config.SeverityNit, config.SeverityCritical)
	// A word from no vocabulary of ours. It must survive to the page: the block
	// exists to DESCRIBE a foreign reviewer, and dropping the tokens that make it
	// foreign would defeat the point.
	usage.Add(config.SeverityCritical, "major")

	block := SeverityVocabularyBlock([]VocabularyRow{
		{Name: "contender", Usage: usage},
		{Name: "found-nothing", Usage: SeverityUsage{}},
	})

	for _, want := range []string{
		"planted error", "critical x2", "warning x1", "planted nit", "major x1",
		"(3 located)", "found-nothing: no located defect to describe",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("the vocabulary block does not contain %q:\n%s", want, block)
		}
	}

	// Deterministic: a description a reader diffs between runs cannot depend on
	// Go's map iteration order.
	for range 20 {
		if again := SeverityVocabularyBlock([]VocabularyRow{{Name: "contender", Usage: usage}}); !strings.Contains(again, "critical x2") ||
			again != SeverityVocabularyBlock([]VocabularyRow{{Name: "contender", Usage: usage}}) {
			t.Fatalf("the block is not stable across renders:\n%s", again)
		}
	}
}

// ---------------------------------------------------------------------------
// WHAT MAXIMISES THIS? — the question nobody asked of the withdrawn cross-tool
// severity column, asked here of every model-free number the reports publish.
// ---------------------------------------------------------------------------

// calibratedReview is the reviewer that is actually correct: one comment per
// planted defect, on its line, naming it, at the level the corpus plants it at.
//
// It is the reference every degenerate strategy is measured against. A published
// metric is worth publishing only if being right beats being degenerate on it.
func calibratedReview(f Fixture) []review.Finding {
	var out []review.Finding
	for _, d := range f.Defects {
		out = append(out, review.Finding{
			Path:     d.Path,
			Line:     d.Line,
			Severity: string(d.WantSeverity),
			Class:    string(d.Class),
			Title:    strings.Join(d.Keywords, " "),
		})
	}
	return out
}

// oneCommentPerPlant locates every defect correctly and stamps ONE severity word
// on all of them — the reviewer the withdrawn banded column scored as perfect.
func oneCommentPerPlant(sev config.Severity) func(Fixture) []review.Finding {
	return func(f Fixture) []review.Finding {
		out := calibratedReview(f)
		for i := range out {
			out[i].Severity = string(sev)
		}
		return out
	}
}

// spamText names no planted defect. Asserted rather than assumed: the strategies
// built on it declare that they cannot max out detection, and a phrase that
// tripped a keyword would make that declaration a lie.
const spamText = "consider revisiting this"

// oneCommentPerLine is the review bot everyone has worked with: a comment on
// every line, saying nothing.
func oneCommentPerLine(f Fixture) []review.Finding {
	var out []review.Finding

	paths := make([]string, 0, len(f.Head))
	for p := range f.Head {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	for _, p := range paths {
		for i := range strings.Split(f.Head[p], "\n") {
			out = append(out, review.Finding{
				Path: p, Line: i + 1, Severity: string(config.SeverityWarning),
				Class: string(config.ClassMaintainability), Title: spamText,
			})
		}
	}
	return out
}

// degenerateReviewer is one way of producing findings that nobody would ship,
// crossed against every published metric.
type degenerateReviewer struct {
	name   string
	why    string
	review func(Fixture) []review.Finding

	// maxes declares, for EVERY published metric by name, whether this strategy
	// is expected to score at least as well as calibratedReview on it.
	//
	// Every cell must be filled. That is the structural part: publishing a new
	// metric means answering, for each of these reviewers, "can this behaviour
	// score as well as being right?" — and a metric where the answer is yes does
	// not measure what its name claims.
	maxes map[string]bool
}

// degenerateReviewers is the table. The two names in each `maxes` map are
// PublishedMetrics' names; a metric missing from any of them fails the test.
func degenerateReviewers() []degenerateReviewer {
	// Locating every defect perfectly and rating them all the same word is
	// PERFECT DETECTION and NO severity opinion whatever. Detection is entitled
	// to say so; a severity metric that agrees is not measuring severity.
	oneWord := map[string]bool{"detection": true, "objective severity": false}

	return []degenerateReviewer{
		{
			name:   "always critical",
			why:    "stamps the loudest blocking word on every finding; the bot this project exists not to be",
			review: oneCommentPerPlant(config.SeverityCritical),
			maxes:  oneWord,
		},
		{
			name:   "always error",
			why:    "the same, one level down; it scored a perfect banded record too",
			review: oneCommentPerPlant(config.SeverityError),
			maxes:  oneWord,
		},
		{
			name:   "always warning",
			why:    "hedges everything into the middle, which no gate ever blocks on",
			review: oneCommentPerPlant(config.SeverityWarning),
			maxes:  oneWord,
		},
		{
			name:   "always nit",
			why:    "the mirror of always-critical: it can never INFLATE, so any column counting only inflation is maximised by it",
			review: oneCommentPerPlant(config.SeverityNit),
			maxes:  oneWord,
		},
		{
			name: "always critical, and only about defects we already rate error or critical",
			why: "raises nothing that is not already obviously serious and calls all of it critical. " +
				"THIS IS THE STRATEGY THAT SCORED A PERFECT BANDED RECORD: a metric coarse enough to " +
				"compare two vocabularies cannot tell it from a calibrated reviewer, because every " +
				"defect it CHOOSES to report is one where its one word happens to be in the right " +
				"region. A degenerate strategy gets to pick what it reports, and a coarse metric is " +
				"scored only over what it picked",
			review: func(f Fixture) []review.Finding {
				var out []review.Finding
				for _, c := range calibratedReview(f) {
					for _, d := range f.Defects {
						if d.Path == c.Path && d.Line == c.Line &&
							d.WantSeverity.Rank() >= config.SeverityError.Rank() {
							c.Severity = string(config.SeverityCritical)
							out = append(out, c)
							break
						}
					}
				}
				return out
			},
			// It misses the quieter plants, so detection catches it — but only
			// because detection is scored over what was PLANTED rather than over
			// what the reviewer chose to mention. A severity metric is scored over
			// what it located, which is the loophole this strategy walks through.
			maxes: map[string]bool{"detection": false, "objective severity": false},
		},
		{
			name: "report only the plants we rate critical, and call them critical",
			why: "the previous row stopped one level short and the metric survived it. This one " +
				"reports nothing it is not already certain about, so every severity it states is " +
				"exactly right: O-ACC 1.000 with no inflation and no understatement, an EXACT TIE " +
				"with a perfectly calibrated reviewer, over four of the corpus's fourteen plants. " +
				"Selective silence is not calibration. It is caught by O-COV, the denominator that " +
				"was missing when this table was first written",
			review: func(f Fixture) []review.Finding {
				var out []review.Finding
				for _, d := range f.Defects {
					if d.WantSeverity != config.SeverityCritical {
						continue
					}
					out = append(out, review.Finding{
						Path: d.Path, Line: d.Line,
						Severity: string(config.SeverityCritical),
						Class:    string(d.Class),
						Title:    strings.Join(d.Keywords, " "),
					})
				}
				return out
			},
			maxes: map[string]bool{"detection": false, "objective severity": false},
		},
		{
			name: "one enormous anchor span per file",
			why: "one finding per file spanning the whole file, titled with every keyword in it. " +
				"anchorDistance is zero anywhere inside a span and explainsAny ignores line position, " +
				"so the blob is credited with every plant in its file and is noise for none: RECALL " +
				"1.000, NOISE 0, an EXACT TIE with a calibrated reviewer while pointing at nothing " +
				"more useful than 'there is a nil deref, an SQL injection and a hardcoded secret " +
				"somewhere in this file'. ANCHOR is what tells them apart",
			review: func(f Fixture) []review.Finding {
				keywords := map[string][]string{}
				var order []string
				for _, d := range f.Defects {
					if _, seen := keywords[d.Path]; !seen {
						order = append(order, d.Path)
					}
					keywords[d.Path] = append(keywords[d.Path], d.Keywords...)
				}

				var out []review.Finding
				for _, p := range order {
					out = append(out, review.Finding{
						Path: p, Line: 1, EndLine: len(strings.Split(f.Head[p], "\n")) + 1000,
						Severity: string(config.SeverityCritical),
						Class:    string(config.ClassCorrectness),
						Title:    strings.Join(keywords[p], " "),
					})
				}
				return out
			},
			// Severity: it calls everything critical, so it inflates on every
			// plant below critical exactly as "always critical" does.
			maxes: map[string]bool{"detection": false, "objective severity": false},
		},
		{
			name: "every correct comment, five times",
			why: "restates a good review five times over. Not a reviewer anyone would ship, and the " +
				"honest answer is that no model-free column here counts findings",
			review: func(f Fixture) []review.Finding {
				var out []review.Finding
				for _, c := range calibratedReview(f) {
					for range 5 {
						out = append(out, c)
					}
				}
				return out
			},
			// It maxes both, declared rather than omitted. Score.Matched breaks
			// at the first match per defect, explainsAny makes every duplicate
			// non-noise, and reportingFinding's max over matching findings is
			// idempotent across identical copies — so duplication is invisible
			// to every model-free column by construction. Leaving the row out
			// would have left the question unasked rather than answered; the
			// judge's PRECISION is where verbosity is meant to be paid for.
			maxes: map[string]bool{"detection": true, "objective severity": true},
		},
		{
			name: "one comment per severity, on every plant",
			why: "expresses no severity opinion by expressing all five; under the withdrawn " +
				"nearest-severity credit rule one of them was always exact, so this scored perfectly",
			review: func(f Fixture) []review.Finding {
				var out []review.Finding
				for _, sev := range severityOrder {
					out = append(out, oneCommentPerPlant(sev)(f)...)
				}
				return out
			},
			maxes: map[string]bool{"detection": true, "objective severity": false},
		},
		{
			name:   "one comment per line of the diff",
			why:    "the review bot that comments on everything and notices nothing",
			review: oneCommentPerLine,
			// Severity is UNDEFINED for it rather than false: it grades no
			// defect, and a reviewer that says nothing about severity inflates
			// nothing. TestSilenceScoresUndefinedNotPerfect pins that separately.
			maxes: map[string]bool{"detection": false, "objective severity": false},
		},
		{
			name: "one comment per line, plus the truth",
			why: "finds everything by saying everything — perfect RECALL, which is why RECALL is " +
				"never published without NOISE beside it",
			review: func(f Fixture) []review.Finding {
				return append(calibratedReview(f), oneCommentPerLine(f)...)
			},
			// Its severity behaviour is NOT degenerate — it rates the defects it
			// names correctly — so the severity metric is entitled to say so.
			// Detection is the one it must not win, and noise is what stops it.
			maxes: map[string]bool{"detection": false, "objective severity": true},
		},
		{
			name:   "silence",
			why:    "the global optimum of any column that counts mistakes without a denominator",
			review: func(Fixture) []review.Finding { return nil },
			maxes:  map[string]bool{"detection": false, "objective severity": false},
		},
		{
			name: "always the same class",
			why: "correct in every respect these metrics measure, and wrong about what KIND of " +
				"problem each defect is",
			review: func(f Fixture) []review.Finding {
				out := calibratedReview(f)
				for i := range out {
					out[i].Class = string(config.ClassStyle)
				}
				return out
			},
			// It maxes both, and that is the honest answer: no model-free metric
			// reads Class at all. It is in this table so that publishing one
			// forces someone to come here and change these two cells.
			maxes: map[string]bool{"detection": true, "objective severity": true},
		},
	}
}

// tallyOver scores one reviewer over a corpus, with no model, judge or network.
func tallyOver(corpus []Fixture, reviewer func(Fixture) []review.Finding) CorpusTally {
	scores := make([]Score, 0, len(corpus))
	for _, f := range corpus {
		scores = append(scores, ScoreRun(RunResult{
			Report: &review.Report{Findings: reviewer(f)},
		}, f))
	}
	return TallyScores(scores)
}

// TestNoDegenerateReviewerCanMaxOutAPublishedMetric asks, of every model-free
// number these reports publish, the question nobody asked of the one that was
// retracted: WHAT MAXIMISES THIS?
//
// The failure this exists to prevent was not a bad constant. A banded cross-tool
// severity accuracy figure was published, and 12 of the 14 plants sit in one
// band: a reviewer that stamps one blocking word on every finding banded 12
// accurate and 2 inflated of 14 against the incumbent's 6 of 10, and one that
// also picks WHAT to report — stay silent unless the defect is already blocking,
// then call it critical — banded a perfect 12 of 12. The column was maximised by
// the worst production behaviour there is, and nothing in the tree asked. A
// metric that rewards stamping "critical" on everything would, if anyone
// optimised against it, produce exactly the review bot this project exists not
// to be.
//
// (This comment used to say "a PERFECT record — 10 of 10". That figure scored
// the degenerate strategy over the plants the INCUMBENT located rather than over
// what it reports, and does not reproduce. The correction did not change the
// conclusion, and finding it is what added the selective-reporting row below.)
//
// So every published metric is crossed with a table of reviewers nobody would
// ship, and each cell is DECLARED. A metric a degenerate strategy can score as
// well as a correct reviewer on does not measure what its name claims and must
// not be published; the failure below names which metric and which strategy.
func TestNoDegenerateReviewerCanMaxOutAPublishedMetric(t *testing.T) {
	corpus := AllFixtures()
	metrics := PublishedMetrics()

	if len(metrics) == 0 {
		t.Fatal("no published metric is registered, so this test proves nothing about the tables")
	}

	reference := tallyOver(corpus, calibratedReview)

	// The reference has to be a reviewer this corpus can actually reward, or
	// "no strategy beats it" is satisfied by it being unbeatable-because-broken.
	if reference.Matched != reference.Planted {
		t.Fatalf("the calibrated reviewer detected %d of %d planted defects. It names each defect "+
			"with that defect's own keywords on that defect's own line, so a miss means matches() "+
			"and the corpus disagree, and every comparison below is against a crippled reference",
			reference.Matched, reference.Planted)
	}
	for _, m := range metrics {
		if _, ok := m.Score(reference); !ok {
			t.Fatalf("metric %q is undefined for a perfectly calibrated reviewer over the whole "+
				"corpus; it cannot be compared against anything", m.Name)
		}
	}

	for _, d := range degenerateReviewers() {
		t.Run(d.name, func(t *testing.T) {
			got := tallyOver(corpus, d.review)

			for _, m := range metrics {
				want, declared := d.maxes[m.Name]
				if !declared {
					t.Errorf("metric %q (%s) is published and this table does not say whether %q can "+
						"max it out. Fill the cell: if the answer is yes, the metric does not measure "+
						"what it claims", m.Name, m.Label(), d.name)
					continue
				}

				maxed, defined := m.Maxes(got, reference)

				switch {
				case maxed && !want:
					t.Errorf("METRIC %q (columns %s) IS MAXED OUT BY %q, which %s.\n"+
						"  calibrated: %v\n  degenerate: %v\n"+
						"That reviewer is not better than a correct one and this metric cannot tell "+
						"them apart, so it does not measure %q and MUST NOT BE PUBLISHED. Withdraw the "+
						"column or change what it measures; do not re-tune it, which is what was tried "+
						"the last three times.",
						m.Name, m.Label(), d.name, d.why,
						scoreOf(m, reference), scoreOf(m, got), m.Doc)
				case !maxed && want && defined:
					t.Errorf("this table declares that %q maxes out %q and it does not (%v against a "+
						"calibrated %v). A stale declaration hides the next real one",
						d.name, m.Name, scoreOf(m, got), scoreOf(m, reference))
				case !defined && want:
					t.Errorf("this table declares that %q maxes out %q, but the metric is UNDEFINED "+
						"for it — it produced nothing to measure", d.name, m.Name)
				}
			}
		})
	}

	// Every metric must be falsifiable by SOMETHING here, or the crossing above
	// is decoration: a metric no degenerate strategy is expected to fail is one
	// this table never actually tests.
	for _, m := range metrics {
		falsifiable := false
		for _, d := range degenerateReviewers() {
			if !d.maxes[m.Name] {
				falsifiable = true
			}
		}
		if !falsifiable {
			t.Errorf("every strategy in the degenerate table is declared able to max out %q, so the "+
				"table asserts nothing about it. Either it is not a score, or a reviewer that would "+
				"break it is missing from the table", m.Name)
		}
	}
}

// scoreOf renders a metric for a failure message, so a reader sees the numbers
// rather than being told they disagreed.
func scoreOf(m PublishedMetric, t CorpusTally) string {
	got, ok := m.Score(t)
	if !ok {
		return "undefined"
	}

	// The first rendering is the component-per-column one, so its names line up
	// with Score's return in order.
	var names []string
	if len(m.Renderings) > 0 {
		names = m.Renderings[0]
	}

	parts := make([]string, 0, len(got))
	for i, v := range got {
		name := "?"
		if i < len(names) {
			name = names[i]
		}
		parts = append(parts, fmt.Sprintf("%s=%.3f", name, v))
	}
	return strings.Join(parts, " ")
}

// TestOneCommentPerLineNamesNoPlantedDefect keeps the degenerate table honest.
//
// Two strategies there declare that they cannot max out detection, and both rest
// on their spam text naming nothing. If a phrase in it ever tripped a plant's
// keyword, those declarations would become false and the table would be
// asserting the opposite of what it says.
func TestOneCommentPerLineNamesNoPlantedDefect(t *testing.T) {
	got := tallyOver(AllFixtures(), oneCommentPerLine)

	if got.Matched != 0 {
		t.Errorf("the line-spammer detected %d planted defect(s) with the text %q. It is supposed to "+
			"name nothing; a keyword collision makes the degenerate table's detection column vacuous",
			got.Matched, spamText)
	}
	if got.Noise == 0 {
		t.Error("the line-spammer produced no noise at all, so NOISE is not the counterweight the " +
			"detection metric relies on it being")
	}
}

// TestSilenceScoresUndefinedNotPerfect pins the trap that any column counting
// mistakes falls into.
//
// A reviewer that says nothing inflates nothing and understates nothing. Printed
// as 0.00 those two columns are the best score on the table, which is how
// silence becomes the global optimum of a tuning objective — the exact failure
// Aggregate.Precision's doc comment records having shipped once, where a variant
// with no findings sorted to the top AND switched off the suite's only assertion.
// TestSilenceIsNotStable pins the second return value of Summary.Stable.
//
// STABLE is printed "yes" or "NO [3 5 4]" and a reader takes yes as better. Five
// runs of silence used to return true — the column's BEST value went to a
// reviewer that said nothing, while a wobbly but correct reviewer got NO. Being
// classified descriptive is what kept it out of the degenerate-reviewer table,
// so nothing asked what maximised it.
func TestSilenceIsNotStable(t *testing.T) {
	fx := AllFixtures()[0]

	run := func(findings []review.Finding) Score {
		return ScoreRun(RunResult{Report: &review.Report{Findings: findings}}, fx)
	}

	var silent, wobbly []Score
	for i := range 5 {
		silent = append(silent, run(nil))
		if i%2 == 0 {
			wobbly = append(wobbly, run(calibratedReview(fx)))
			continue
		}
		wobbly = append(wobbly, run(nil))
	}

	if _, defined := Summarize("silence", fx.Name, silent).Stable(); defined {
		t.Error("five runs of silence report a DEFINED stability verdict. A reviewer that never " +
			"spoke has not been observed to be consistent; it has not been observed at all, and " +
			"'yes' is the best value this column has")
	}

	stable, defined := Summarize("wobbly", fx.Name, wobbly).Stable()
	if !defined || stable {
		t.Errorf("a reviewer whose finding count moves between runs reports stable=%v defined=%v, "+
			"want false/true — that is the instability the column exists to show", stable, defined)
	}

	var steady []Score
	for range 5 {
		steady = append(steady, run(calibratedReview(fx)))
	}
	if stable, defined := Summarize("steady", fx.Name, steady).Stable(); !stable || !defined {
		t.Errorf("a reviewer that says the same thing five times reports stable=%v defined=%v, "+
			"want true/true", stable, defined)
	}
}

// TestTheCreditedSpellingDoesNotDependOnReportOrder pins the tie-break in
// reportingFinding, whose doc comment used to claim more than it delivered.
//
// It said ties were between findings carrying the SAME severity, so order
// decided only which comment a diagnostic NAMED. Ties are on NORMALIZED rank,
// and the credited finding's RAW spelling is what SeverityUsage records — which
// is published, as the description that replaced the withdrawn cross-tool score.
// Two findings spelled "info" and "P1" both normalize to info and tie, so the
// same review published two different vocabulary blocks depending on the order
// its findings arrived in.
func TestTheCreditedSpellingDoesNotDependOnReportOrder(t *testing.T) {
	fx := Fixture{
		Name: "tie",
		Head: map[string]string{"a.go": "package a\n"},
		Defects: []Defect{{
			Path: "a.go", Line: 1, Why: "a tie", WantSeverity: config.SeverityInfo,
			Class: config.ClassStyle, Keywords: []string{"zebra"},
		}},
	}

	comment := func(sev string) review.Finding {
		return review.Finding{
			Path: "a.go", Line: 1, Severity: sev,
			Class: string(config.ClassStyle), Title: "zebra",
		}
	}

	forward := ScoreSeverity(fx, []review.Finding{comment("info"), comment("P1")})
	reverse := ScoreSeverity(fx, []review.Finding{comment("P1"), comment("info")})

	a := SeverityVocabularyBlock([]VocabularyRow{{Name: "r", Usage: forward.Usage()}})
	b := SeverityVocabularyBlock([]VocabularyRow{{Name: "r", Usage: reverse.Usage()}})
	if a != b {
		t.Errorf("reordering a review changed the PUBLISHED severity vocabulary block:\n%s\nversus\n%s\n"+
			"Both findings normalize to info and tie on rank, so the tie-break decides which raw "+
			"spelling is recorded — and a description a reader diffs between runs may not depend on "+
			"the order findings happened to arrive in", a, b)
	}

	// And the recognized spelling is the one recorded, not whichever arrived
	// first: "p1" describes no vocabulary anyone can act on.
	if !strings.Contains(a, "info x1") {
		t.Errorf("the vocabulary block records %q; with a recognized spelling and an unrecognized one "+
			"tied, the recognized one is what describes the reviewer", a)
	}
}

func TestSilenceScoresUndefinedNotPerfect(t *testing.T) {
	silent := tallyOver(AllFixtures(), func(Fixture) []review.Finding { return nil })

	if silent.Severity.Graded() != 0 {
		t.Fatalf("a silent reviewer graded %d defect(s)", silent.Severity.Graded())
	}

	for _, m := range PublishedMetrics() {
		if m.Name != "objective severity" {
			continue
		}
		if _, ok := m.Score(silent); ok {
			t.Errorf("metric %q is DEFINED for a reviewer that produced no findings. Its inflation "+
				"and understatement components are then zero — the best value either can take — so "+
				"silence reads as perfect calibration", m.Name)
		}
	}

	// And the table that prints it must leave the cell blank rather than
	// printing a triple of zeros, which is the same claim in a different place.
	summary := Summarize("silent", "any", []Score{{}})
	if summary.SevAccurate+summary.SevInflated+summary.SevUnderstated != 0 {
		t.Errorf("a silent run summed to a non-zero severity triple: %d/%d/%d",
			summary.SevAccurate, summary.SevInflated, summary.SevUnderstated)
	}
	if len(summary.SevUsage) != 0 {
		t.Errorf("a silent run produced a severity vocabulary: %v", summary.SevUsage)
	}
}

// TestEveryPublishedColumnIsRegistered is the structural half of the guard.
//
// The degenerate table can only ask "what maximises this?" of metrics it knows
// about. The withdrawn banded columns were added to two table headers and a
// legend, and nothing anywhere required them to be declared — which is why the
// question was never asked of them. Every column in every published header must
// now be either a registered metric, an LLM judge's opinion, or an identifier,
// and adding a score means adding it to PublishedMetrics, which puts it in front
// of the degenerate reviewers.
func TestEveryPublishedColumnIsRegistered(t *testing.T) {
	known := map[string]string{}
	for _, m := range PublishedMetrics() {
		for _, c := range m.Columns() {
			known[c] = "published metric " + m.Name
		}
	}
	for _, c := range JudgeOpinionColumns() {
		known[c] = "judge opinion"
	}
	for _, c := range DescriptiveColumns() {
		known[c] = "descriptive"
	}

	for _, header := range ScoreTableHeaders() {
		for _, col := range strings.Fields(header) {
			if _, ok := known[col]; !ok {
				t.Errorf("the published column %q in header %q is declared nowhere. A score must be "+
					"registered in PublishedMetrics, where the degenerate-reviewer table asks what "+
					"maximises it; the alternative is a column nobody has asked that of, which is how "+
					"the banded cross-tool figure shipped", col, truncate(header, 40))
			}
		}
	}
}

// TestEveryHeaderPrintsAWholeMetric is the other half of registration: a
// declared column is not enough, it has to be printed with the columns it is
// only meaningful beside.
//
// PublishedMetric is a GROUP for a stated reason — "O-INFL alone is maximised by
// silence and is only a score BESIDE O-UNDER and O-ACC" — and nothing enforced
// it. Deleting O-ACC and O-UNDER from VariantTableHeader, leaving O-INFL
// published by itself, passed the whole package including the guard named after
// this failure. A metric may be rendered more than one way (one cell holding
// "SEV a/i/u" beside RECALL, or the triple spread over O-* beside O-COV); a
// header must carry one rendering WHOLE.
func TestEveryHeaderPrintsAWholeMetric(t *testing.T) {
	for _, header := range ScoreTableHeaders() {
		cols := map[string]bool{}
		for _, c := range strings.Fields(header) {
			cols[c] = true
		}

		for _, m := range PublishedMetrics() {
			touches := false
			for _, c := range m.Columns() {
				if cols[c] {
					touches = true
				}
			}
			if !touches {
				continue
			}

			complete := false
			var missing []string
			for _, rendering := range m.Renderings {
				var absent []string
				for _, c := range rendering {
					if !cols[c] {
						absent = append(absent, c)
					}
				}
				if len(absent) == 0 {
					complete = true
					break
				}
				missing = append(missing,
					fmt.Sprintf("[%s] is missing %s", strings.Join(rendering, " "), strings.Join(absent, " ")))
			}

			if !complete {
				t.Errorf("header %q prints part of the metric %q and no whole rendering of it: %s.\n"+
					"That metric's columns are only a score read TOGETHER — %s — so a header offering "+
					"a subset offers a number whose meaning depends on one nobody printed",
					truncate(header, 40), m.Name, strings.Join(missing, "; "), m.Doc)
			}
		}
	}
}

// TestEveryTableHeaderInThePackageIsRegistered reads the source, because the
// guards below are exactly as complete as AllTableHeaders and nothing made that
// list complete.
//
// THE BUG: ReportTableHeaders returned three headers while claiming to be "every
// published table header". CostTableHeader and the two rejudge tables were not
// in it, so appending `B-ACC` to CostTableHeader passed both the registration
// guard and the cross-tool severity guard — the withdrawn instrument could be
// reinstated in a table the mechanism did not know existed. A guard over a
// hand-maintained list of what to guard has to check the list.
func TestEveryTableHeaderInThePackageIsRegistered(t *testing.T) {
	registered := map[string]bool{}
	for _, h := range AllTableHeaders() {
		registered[h] = true
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}

	// Declarations whose name ends in Header, with or without a leading const or
	// var — inside a grouped block the keyword is on its own line, and at top
	// level it is on the same line. The first version of this pattern required
	// the name to start the line, so `const SmuggledTableHeader = "MODEL B-ACC"`
	// at top level was invisible to it and the guard passed on the exact
	// smuggling it exists to stop.
	//
	// Matching the source rather than reflecting over values is deliberate: a
	// header nobody registered has no value to reflect.
	// The value is captured too, so that a regexp named ...Header — the parser
	// matches one line of Incumbent output with crFindingHeader — is not
	// mistaken for a table. Everything else named Header must be registered:
	// erring toward more checking is the right direction for a guard whose
	// failure mode is not knowing a table exists.
	decl := regexp.MustCompile(`(?m)^\s*(?:const\s+|var\s+)?(\w*[Hh]eader)\s*=\s*(.*)$`)

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		src, err := os.ReadFile(e.Name())
		if err != nil {
			t.Fatalf("reading %s: %v", e.Name(), err)
		}
		for _, m := range decl.FindAllStringSubmatch(string(src), -1) {
			name, value := m[1], strings.TrimSpace(m[2])
			if strings.HasPrefix(value, "regexp.MustCompile") {
				continue
			}
			if !headerIsRegistered(name, registered) {
				t.Errorf("%s declares a table header %q that AllTableHeaders() does not return. Every "+
					"guard over published columns runs off that list, so a header missing from it is a "+
					"table where a withdrawn column can be reinstated with every test still green",
					e.Name(), name)
			}
		}
	}
}

// headerIsRegistered resolves a declared header name against the registered
// values. The names are known at compile time, so the mapping is explicit rather
// than reflective — and a new one falls through to "not registered", which is
// the answer that fails loudly.
func headerIsRegistered(name string, registered map[string]bool) bool {
	byName := map[string]string{
		"SummaryTableHeader":     SummaryTableHeader,
		"VariantTableHeader":     VariantTableHeader,
		"JudgedModelTableHeader": JudgedModelTableHeader,
		"CostTableHeader":        CostTableHeader,
		"precisionHeader":        precisionHeader,
		"agreementHeader":        agreementHeader,
	}
	v, ok := byName[name]
	return ok && registered[v]
}

// TestNoTableOffersACrossToolSeverityScore is the retraction itself, asserted.
//
// Not a general rule but a specific one, aimed at the specific thing that was
// published twice and withdrawn twice. A reduction that makes our five levels and
// a foreign reviewer's three comparable is maximised by rating everything
// blocking; re-tuning the boundaries is the fourth attempt at the same mistake
// and this is what it runs into.
func TestNoTableOffersACrossToolSeverityScore(t *testing.T) {
	// Over ALL headers, not the score tables: the previous version ran over
	// three of six, so the withdrawn column could be reinstated in the cost
	// table with every guard green.
	// The spellings a banded severity column can take: anything mentioning a
	// band, or B- carrying one of the severity triple's suffixes. A bare "B-"
	// prefix is too wide — the agreement table's B-ONLY counts findings only
	// judge B raised and has nothing to do with severity, and matching it would
	// have made this guard fire on an unrelated table, which is how a guard
	// gets loosened until it catches nothing.
	banded := regexp.MustCompile(`BAND|^B-(ACC|INFL|UNDER)$`)

	for _, header := range AllTableHeaders() {
		for _, col := range strings.Fields(header) {
			if banded.MatchString(col) {
				t.Errorf("header %q publishes a column %q. A banded cross-tool severity score is "+
					"withdrawn: a reviewer stamping one blocking word on every finding scored a "+
					"perfect record on it, and it could not see the parser bug it was introduced to "+
					"fix. Describe the vocabularies (SeverityVocabularyBlock); do not score them "+
					"against each other", truncate(header, 40), col)
			}
		}
	}

	// The full-resolution triple on a foreign row is the SAME offer in a
	// different spelling, and the previous guard could not see it: it matched
	// column names, and the columns kept their names. What changed was which
	// rows get a number in them.
	a := Aggregate{SevAccurate: 4, SevInflated: 1, SevUnderstated: 2, SevPlanted: 8}
	infl, under, acc, cov := a.ObjectiveSeverityCells(IncumbentModel, 4)
	for name, cell := range map[string]string{"O-INFL": infl, "O-UNDER": under, "O-ACC": acc, "O-COV": cov} {
		if cell != "n/a" {
			t.Errorf("the %s cell for %s renders %q. A row whose vocabulary is not ours must print "+
				"n/a there: the banded triple was withdrawn and the full-resolution one was left in "+
				"the same columns of the same sorted ranking, annotated with a note telling the "+
				"reader not to compare it — which is the mitigation the previous retraction had "+
				"already recorded as insufficient", name, IncumbentModel, cell)
		}
	}

	ours := "nitpick/some-model"
	if infl, _, _, cov := a.ObjectiveSeverityCells(ours, 4); infl == "n/a" || cov == "n/a" {
		t.Errorf("the O-* cells for %q render n/a (%s, %s). Blanking them for OUR OWN rows would "+
			"withdraw the comparison the prompt is actually tuned on, which is not what was retracted",
			ours, infl, cov)
	}

	// The legend must SAY so, where a reader meets the numbers. A silent
	// withdrawal leaves the next person to reinvent it.
	if !strings.Contains(SeverityColumnLegend, NoCrossToolSeverityScore) {
		t.Error("the severity legend no longer carries the statement that no cross-tool severity " +
			"score is offered; a reader who is not told will go looking for one, or make one")
	}
	for _, gone := range []string{"B-ACC", "B-INFL", "B-UNDER"} {
		if strings.Contains(SeverityColumnLegend, gone) {
			t.Errorf("the severity legend still describes %s as a column to read", gone)
		}
	}
}

// TestMajorIsAFreeParameterSoNoCrossToolScoreIsOffered is the third measurement
// behind the retraction, and the one that is easiest to argue with.
//
// crSeverity records Incumbent's "major" — a word with no counterpart among our
// five — at warning. That is a guess. This test re-parses the SHIPPED reviews
// with the guess changed to error, byte-for-byte identical input otherwise, and
// shows two things:
//
//   - the severity result MOVES. A published figure that changes when we change
//     our own constant, with no change whatever in the reviewer's output, is not
//     a measurement of the reviewer.
//   - the corpus cannot say which guess is right. Every plant a "major" is
//     credited on is planted ABOVE warning, so no observation here distinguishes
//     "major means warning" from "major means error" — one is understated on
//     every observation and the other is accurate or understated on every one.
//
// The remedy is NOT to re-tune the constant, which would move the number our way
// on no evidence for the fourth time. It is to publish no cross-tool severity
// score, which is what NoCrossToolSeverityScore says and what the tables now do.
func TestMajorIsAFreeParameterSoNoCrossToolScoreIsOffered(t *testing.T) {
	var (
		shipped, swapped SeverityScore
		majorPlants      []config.Severity
		majors           int
	)

	// Every cached review, tuning and held-out alike. This tunes nothing — it
	// demonstrates that a constant in OUR parser is unconstrained by the corpus —
	// so the held-out set is not being spent, and the more observations the claim
	// rests on the harder it is to argue with.
	for _, f := range AllFixtures() {
		c, ok := readCRCache(crCacheDir, f)
		if !ok || c.Raw == "" {
			continue
		}

		majors += strings.Count(c.Raw, "major [")

		asShipped, err := parseIncumbent([]byte(c.Raw))
		if err != nil {
			t.Fatalf("%s: the shipped review no longer parses: %v", f.Name, err)
		}
		// The ONLY change: the word this parser maps by guesswork. Everything
		// else about the review — its findings, anchors, prose — is identical.
		asError, err := parseIncumbent([]byte(strings.ReplaceAll(c.Raw, "major [", "error [")))
		if err != nil {
			t.Fatalf("%s: substituting the severity word broke the parse: %v", f.Name, err)
		}
		if len(asShipped) != len(asError) {
			t.Fatalf("%s: the substitution changed the finding count (%d -> %d), so the two scores "+
				"are not of the same review", f.Name, len(asShipped), len(asError))
		}

		a, b := ScoreSeverity(f, asShipped), ScoreSeverity(f, asError)
		shipped.Accurate, shipped.Inflated, shipped.Understated =
			shipped.Accurate+a.Accurate, shipped.Inflated+a.Inflated, shipped.Understated+a.Understated
		swapped.Accurate, swapped.Inflated, swapped.Understated =
			swapped.Accurate+b.Accurate, swapped.Inflated+b.Inflated, swapped.Understated+b.Understated

		for _, call := range a.Calls {
			if asShipped[call.FindingIndex].Severity != asError[call.FindingIndex].Severity {
				majorPlants = append(majorPlants, call.Defect.WantSeverity)
			}
		}
	}

	if majors == 0 {
		t.Skip("no cached review uses the word 'major'; there is nothing to be free about")
	}
	if len(majorPlants) == 0 {
		t.Fatalf("%d 'major' finding(s) are cached and none is credited with a plant, so this test "+
			"measures nothing", majors)
	}

	if shipped.Accurate == swapped.Accurate && shipped.Inflated == swapped.Inflated &&
		shipped.Understated == swapped.Understated {
		t.Errorf("changing what 'major' maps to did not move the severity result (%d/%d/%d either "+
			"way). If that is genuinely true the corpus has changed; check before concluding the "+
			"parameter is pinned down", shipped.Accurate, shipped.Inflated, shipped.Understated)
	}

	for _, planted := range majorPlants {
		if planted == config.SeverityWarning {
			t.Errorf("a 'major' is credited on a plant of warning, so the corpus DOES now contain " +
				"evidence about where 'major' belongs. That is new information: revisit crSeverity's " +
				"guess deliberately, in a diff that says so")
		}
	}

	t.Logf("'major' -> warning: %d/%d/%d accurate/inflated/understated; 'major' -> error: %d/%d/%d. "+
		"Same bytes from %s, %d credited observation(s), none of them on a plant of warning: this "+
		"corpus cannot choose between the two, which is why no cross-tool severity score is published",
		shipped.Accurate, shipped.Inflated, shipped.Understated,
		swapped.Accurate, swapped.Inflated, swapped.Understated,
		IncumbentModel, len(majorPlants))
}
