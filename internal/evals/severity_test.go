package evals

import (
	"bufio"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strconv"
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
	for _, tc := range []struct {
		planted config.Severity
		said    SeverityWord
	}{
		{config.SeverityCritical, SeverityWord{"error", config.SeverityError}},
		{config.SeverityError, SeverityWord{"error", config.SeverityError}},
		{config.SeverityNit, SeverityWord{"critical", config.SeverityCritical}},
	} {
		if n := usage[tc.planted][tc.said]; n != 1 {
			t.Errorf("usage[%s][%s] = %d, want 1: the description must record what was SAID about "+
				"each planted level, since no number is offered in its place",
				tc.planted, tc.said.Said, n)
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

// crSeverityCases discovers the vocabulary crSeverity enumerates, by reading the
// case literals out of its switch.
//
// Discovered rather than listed, for the reason the header registry had to learn
// twice: a second list that somebody has to keep in step with the first is the
// same defect one step removed. Adding a case and forgetting to extend a
// hand-written list is precisely the silent widening the caller guards against.
func crSeverityCases(t *testing.T) []string {
	t.Helper()

	parsed, err := parser.ParseFile(token.NewFileSet(), "incumbent.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parsing incumbent.go: %v", err)
	}

	var out []string
	ast.Inspect(parsed, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "crSeverity" {
			return true
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			clause, ok := n.(*ast.CaseClause)
			if !ok {
				return true
			}
			for _, expr := range clause.List {
				lit, ok := expr.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				word, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatalf("case literal %s in crSeverity: %v", lit.Value, err)
				}
				out = append(out, word)
			}
			return true
		})
		return false
	})

	sort.Strings(out)
	return out
}

// TestForeignSeverityWordsAreTranslatedOnPurpose pins the enumerated half of
// crSeverity, which the default-arm guard above cannot reach.
//
// crSeverity translates a foreign vocabulary. "major" and "warn" become warning
// and "nitpick" becomes nit, where those same tokens on one of our own findings
// would go through config.Severity.Normalize and land at info — so the identical
// word is worth a different level depending on who said it. The asymmetry is
// deliberate and it is load-bearing: "major" carries more than half the
// incumbent's severity words in the shipped cache, and degrading it to info
// would understate the incumbent on nearly every row of a comparison whose whole
// point is to be like-for-like.
//
// THE REASON THIS TEST EXISTS AT ALL is that the comment beside crSeverity has
// been citing it by name — "pins the enumerated set, so a fourth translation
// cannot be added silently" — while nothing of the sort was ever written. A
// citation reads as proof the claim beside it is checked, so that sentence was
// worse than saying nothing: it told every reader to stop looking. The guard is
// arriving late rather than the promise being withdrawn, because the promise is
// the right one.
func TestForeignSeverityWordsAreTranslatedOnPurpose(t *testing.T) {
	words := crSeverityCases(t)
	if len(words) == 0 {
		t.Fatal("no case literals were discovered in crSeverity, so this test says nothing about " +
			"what the incumbent's vocabulary is translated to")
	}

	// A word is TRANSLATED when the enumeration moves it somewhere Normalize
	// would not have. Words the two paths agree on — "critical", "error",
	// "minor" — cost nothing and need no argument: they are recorded, not
	// re-decided.
	translated := map[string]config.Severity{}
	for _, word := range words {
		ours, _ := config.Severity(word).Normalize()
		if theirs := crSeverity(word); theirs != ours {
			translated[word] = theirs
		}
	}

	// The declared set. Each entry is a level this harness assigned on the
	// reviewer's behalf, and each one is defensible on the record above.
	want := map[string]config.Severity{
		"major":   config.SeverityWarning,
		"warn":    config.SeverityWarning,
		"nitpick": config.SeverityNit,
	}

	for word, got := range translated {
		expect, ok := want[word]
		if !ok {
			t.Errorf("crSeverity translates %q to %q and nothing here argues for it. A translation "+
				"is a severity this harness assigns to a finding the reviewer worded differently, "+
				"and it moves that reviewer's O-INFL and O-UNDR columns. Add it above with the "+
				"reason, or let the word fall through to Normalize like any other", word, got)
			continue
		}
		if got != expect {
			t.Errorf("crSeverity translates %q to %q, and it is declared here as %q. The published "+
				"columns move with this value", word, got, expect)
		}
	}

	for word, expect := range want {
		if _, ok := translated[word]; !ok {
			t.Errorf("%q is declared a translation to %q and is no longer one. Either the case was "+
				"removed — in which case the incumbent's most common severity word is now degraded "+
				"to info and every row understates it — or Normalize learned the word and the "+
				"translation is dead code", word, expect)
		}
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
	}, JudgedOver(severityFixture().Name, findings)); len(problems) > 0 {
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
	err := SeverityWord{Said: "error", Recorded: config.SeverityError}
	if got := a.SevUsage[config.SeverityCritical][err]; got != 1 {
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
	warning := SeverityWord{Said: "warning", Recorded: config.SeverityWarning}
	if n := got.SevUsage[config.SeverityCritical][warning]; n != 2 {
		t.Errorf("usage[critical][warning] = %d, want 2: the vocabulary must be summed over the runs, "+
			"or the description under the table describes one run and is captioned as all of them", n)
	}
	critical := SeverityWord{Said: "critical", Recorded: config.SeverityCritical}
	if n := got.SevUsage[config.SeverityNit][critical]; n != 1 {
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
	said := func(word string, recorded config.Severity) SeverityWord {
		return SeverityWord{Said: word, Recorded: recorded}
	}

	usage := SeverityUsage{}
	usage.Add(config.SeverityError, said("critical", config.SeverityCritical))
	usage.Add(config.SeverityError, said("critical", config.SeverityCritical))
	usage.Add(config.SeverityError, said("warning", config.SeverityWarning))
	usage.Add(config.SeverityNit, said("critical", config.SeverityCritical))
	// A word from no vocabulary of ours, translated into one. Both halves must
	// survive to the page and stay distinguishable: the block exists to DESCRIBE
	// a foreign reviewer, so dropping the token that makes it foreign defeats the
	// point, and printing our reading of it unlabelled attributes our sentence to
	// the reviewer.
	usage.Add(config.SeverityCritical, said("major", config.SeverityWarning))

	block := SeverityVocabularyBlock([]VocabularyRow{
		{Name: "contender", Usage: usage},
		{Name: "found-nothing", Usage: SeverityUsage{}},
	})

	for _, want := range []string{
		"planted error", "critical x2", "warning x1", "planted nit",
		"major x1 [we read as warning]",
		"(3 located)", "found-nothing: no located defect to describe",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("the vocabulary block does not contain %q:\n%s", want, block)
		}
	}

	// A word we did not translate carries no marker. Otherwise every line of
	// every one of our own models would end in an annotation, and the one place
	// the annotation MATTERS — a foreign word with no counterpart among our five
	// — would be invisible in the noise.
	if strings.Contains(block, "critical x2 [we read as") {
		t.Errorf("an untranslated word is annotated as though it had been translated:\n%s", block)
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

// TestPublishedVocabularyIsQuotedFromTheRetainedReview is the guard for the
// block being a QUOTATION rather than a derivation.
//
// THE BUG: every severity word published for the incumbent was our translation
// of its word, under a caption saying it was the reviewer's. Incumbent prints
// "critical" and "major" and printed neither "error" nor "warning" anywhere in
// the shipped cache, yet the block read "planted error (7 located): critical x4,
// warning x3" — and swapping crSeverity's free "major" constant, with the cached
// bytes byte-for-byte unchanged, re-rendered the identical line as "critical x4,
// error x3". A description offered BECAUSE a score could not be justified, which
// is itself a function of the free parameter the withdrawal rested on, is the
// same defect one level down.
//
// So the assertion is not "the words look right", which a translation also
// satisfies. It is that every word published came out of the retained review
// text: the words are read straight from crCache.Raw, independently of the
// scoring path, and the published set has to be a subset of them. Print the
// translation and the test goes red, because the translation is a word the CLI
// never printed.
func TestPublishedVocabularyIsQuotedFromTheRetainedReview(t *testing.T) {
	// The evidence, read without going through the scorer: the severity token on
	// every finding header the CLI actually wrote.
	printed := map[string]bool{}
	usage := SeverityUsage{}
	cached := 0

	for _, f := range AllFixtures() {
		c, ok := readCRCache(crCacheDir, f)
		if !ok || c.Raw == "" {
			continue
		}
		cached++

		for _, line := range strings.Split(c.Raw, "\n") {
			if m := crFindingHeader.FindStringSubmatch(line); m != nil {
				printed[strings.ToLower(m[1])] = true
			}
		}

		findings, ok := CachedIncumbent(crCacheDir, f)
		if !ok {
			t.Fatalf("%s has a retained review that no longer parses", f.Name)
		}
		usage.Merge(ScoreSeverity(f, findings).Usage())
	}

	if cached == 0 || len(printed) == 0 {
		t.Fatalf("no retained review supplied a severity word (%d cache entries, %d words); this test "+
			"passes vacuously and measures nothing", cached, len(printed))
	}

	translated := 0
	for planted, row := range usage {
		for said := range row {
			if said.Said == "" {
				t.Errorf("on plants of %s the incumbent's word is unrecorded, but every review in "+
					"%s retains its raw text; the word was dropped between the parser and the page",
					planted, crCacheDir)
				continue
			}
			if !printed[strings.ToLower(said.Said)] {
				t.Errorf("the vocabulary block publishes %q as a word %s used on plants of %s. It "+
					"printed %v and nothing else. A word we assigned, published as the reviewer's, is "+
					"a claim about the reviewer that OUR constant decides — swapping crSeverity's "+
					"'major' arm re-renders this block with no change to the reviewer's output",
					said.Said, IncumbentModel, planted, sortedWords(printed))
			}
			if !strings.EqualFold(said.Said, string(said.Recorded)) {
				translated++
			}
		}
	}

	// Without a translated word in the corpus the check above cannot fail, so
	// this test would go green on a corpus that had stopped exercising the bug.
	if translated == 0 {
		t.Errorf("no word in the shipped cache is translated into a different level, so this test "+
			"cannot distinguish quoting the reviewer from republishing our reading of it. The corpus "+
			"used to contain %d 'major' findings", 7)
	}

	// And the same claim about the RENDERED page, because the data being right is
	// not the same as the page being right — the substitution lived in the
	// renderer's key, not in the scorer.
	block := SeverityVocabularyBlock([]VocabularyRow{{Name: IncumbentModel, Usage: usage}})
	if !strings.Contains(block, "major x") {
		t.Errorf("the rendered block never quotes 'major', the word carrying most of the "+
			"incumbent's severity claims:\n%s", block)
	}
	if strings.Contains(block, "warning x") {
		t.Errorf("the rendered block presents 'warning' as a word %s used. It is OUR reading of "+
			"'major' and belongs in the bracket beside it, not in the reviewer's quoted "+
			"vocabulary:\n%s", IncumbentModel, block)
	}
}

// sortedWords renders a word set for a failure message, in a fixed order.
func sortedWords(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for w := range set {
		out = append(out, w)
	}
	sort.Strings(out)
	return out
}

// TestAnUnrecoverableWordIsReportedNotSubstituted covers the one case where the
// reviewer's word genuinely cannot be quoted.
//
// A Incumbent cache entry collected before the raw review was retained replays
// a previous parser's findings, and the word that parser read is gone. The
// choice there is between saying so and printing our own reading as though the
// reviewer had said it, and the second is the defect this whole block was
// rewritten to remove — silently, and on exactly the entries whose evidence is
// missing, which is where a reader is least able to check.
func TestAnUnrecoverableWordIsReportedNotSubstituted(t *testing.T) {
	f := Fixture{
		Name: "no-raw",
		Head: map[string]string{"a.go": "package a\n"},
		Defects: []Defect{{
			Path: "a.go", Line: 1, Why: "a plant", WantSeverity: config.SeverityError,
			Class: config.ClassCorrectness, Keywords: []string{"zebra"},
		}},
	}

	// A cache entry with findings and no Raw: exactly what a pre-retention
	// collection left on disk.
	dir := t.TempDir()
	blob, err := json.MarshalIndent(crCache{
		Fixture: f.Name,
		Findings: []review.Finding{
			{Path: "a.go", Line: 1, Severity: "warning", Title: "zebra", Class: string(config.ClassCorrectness)},
		},
		Mode:        crReviewMode,
		Fingerprint: fixtureFingerprint(f),
	}, "", "  ")
	if err != nil {
		t.Fatalf("building the cache entry: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, f.Name+".json"), blob, 0o600); err != nil {
		t.Fatalf("writing the cache entry: %v", err)
	}

	findings, ok := CachedIncumbent(dir, f)
	if !ok {
		t.Fatal("an entry with findings and no raw review must still be served; there is no newer " +
			"reading of bytes that were never kept")
	}

	usage := ScoreSeverity(f, findings).Usage()
	for _, row := range usage {
		for said := range row {
			if said.Said != "" {
				t.Errorf("the replayed finding is quoted as having said %q. Its word was never "+
					"recorded; %q is what the parser of the day translated it to, and publishing that "+
					"as the reviewer's is the substitution this block exists to stop",
					said.Said, said.Recorded)
			}
		}
	}

	block := SeverityVocabularyBlock([]VocabularyRow{{Name: IncumbentModel, Usage: usage}})
	if !strings.Contains(block, UnrecordedWord) {
		t.Errorf("the block does not report the missing word:\n%s", block)
	}
	if !strings.Contains(block, "[we read as warning]") {
		t.Errorf("the block drops our own reading, so the row says nothing at all:\n%s", block)
	}

	// THE PREAMBLE HAS TO AGREE WITH THE ROWS IT INTRODUCES. This test asserted
	// only on the body and was green while the note above it read "NO WORD IN
	// THIS BLOCK WAS TRANSLATED: every contender above printed a word this
	// project already uses, so each line quotes its reviewer directly" — sitting
	// directly on top of "(word not recorded) x1 [we read as warning]", which is
	// the exact opposite claim, in the one published block whose purpose is to
	// keep our readings distinguishable from a reviewer's words. A caption that
	// contradicts its own table is worse than no caption: a reader who trusts it
	// stops looking.
	if strings.Contains(block, "each line quotes its reviewer directly") {
		t.Errorf("the preamble claims every line quotes its reviewer directly, above a row whose "+
			"word was destroyed:\n%s", block)
	}
	if !strings.Contains(block, "WITH NO WORD AT ALL") {
		t.Errorf("the preamble does not tell a reader that a word was lost, so the only notice is a "+
			"parenthesis inside one row:\n%s", block)
	}
}

// TestCaseIsDecidedInOnePlaceAcrossTheVocabularyBlock: the caption and the rows
// must answer "was this word translated?" the same way.
//
// THE BUG: SeverityUsage.Lines suppresses the "[we read as X]" marker with
// EqualFold — its comment gives the reason, that a difference of case is not a
// difference of vocabulary and flagging it would bury the ones that are — while
// translatedWordsNote compared with ==. A model printing "Critical" was
// therefore ANNOUNCED as a translated word by the caption and QUOTED unmarked by
// the row underneath, so the block disagreed with itself about a word a reader
// can see in full on the same screen.
func TestCaseIsDecidedInOnePlaceAcrossTheVocabularyBlock(t *testing.T) {
	// Exactly what Engine.recordSeverity produces for a model that prints
	// "Critical": our level, their capitalization.
	usage := SeverityUsage{}
	usage.Add(config.SeverityCritical, SeverityWord{Said: "Critical", Recorded: config.SeverityCritical})

	block := SeverityVocabularyBlock([]VocabularyRow{{Name: "some/model", Usage: usage}})

	if strings.Contains(block, "WORDS THIS PROJECT TRANSLATED") {
		t.Errorf("the caption lists a case-only difference as a translated word, while the row "+
			"below it prints that word with no [we read as] marker:\n%s", block)
	}
	if !strings.Contains(block, "Critical x1") {
		t.Errorf("the reviewer's own capitalization must survive to the page:\n%s", block)
	}

	// And a real translation still has to be announced, or the fix above is just
	// a caption that never says anything.
	usage.Add(config.SeverityError, SeverityWord{Said: "major", Recorded: config.SeverityWarning})
	block = SeverityVocabularyBlock([]VocabularyRow{{Name: "some/model", Usage: usage}})
	if !strings.Contains(block, `"major" read as warning`) {
		t.Errorf("a genuine translation is no longer announced:\n%s", block)
	}
}

// TestOurOwnTranslationsAreNotPublishedAsOurContendersWords is the other half of
// the substitution the vocabulary block exists to prevent.
//
// THE BUG: severityWasTranslated answered from the finding's SOURCE — it was
// true only for Incumbent — so it said "nothing was translated" for every model
// this project ships. review.Engine rewrites a model's severity through
// Normalize, so a model that wrote "P1" was published at info and QUOTED as
// having said "info", a word it never used. The incumbent's side had already
// been fixed for exactly this, and it is worse here: there a lost word prints
// UnrecordedWord, and here our substitute was printed as a quotation with nothing
// marking it.
//
// The fix is that the rewriter records the fact and this reads it, so the test
// is written over findings carrying the provenance the engine now sets rather
// than over a source string.
func TestOurOwnTranslationsAreNotPublishedAsOurContendersWords(t *testing.T) {
	fx := severityFixture()
	d := fx.Defects[0]

	ours := review.Finding{
		Path: d.Path, Line: d.Line, Title: strings.Join(d.Keywords, " "),
		Source: "nitpick/some-model",
		// What the engine produces for a model that answered outside our
		// vocabulary: our level, its word, and the fact that we rewrote it.
		Severity: string(config.SeverityInfo), SeverityTranslated: true, RawSeverity: "P1",
	}

	usage := ScoreSeverity(fx, []review.Finding{ours}).Usage()
	block := SeverityVocabularyBlock([]VocabularyRow{{Name: ours.Source, Usage: usage}})

	if !strings.Contains(block, `"P1"`) {
		t.Errorf("the block does not quote the word the model actually printed:\n%s", block)
	}
	if !strings.Contains(block, "[we read as info]") {
		t.Errorf("the block quotes the model's word without marking our reading as ours:\n%s", block)
	}
	for _, w := range usage {
		for said := range w {
			if said.Said != "P1" {
				t.Errorf("the model is quoted as having said %q; it said \"P1\" and %q is the word "+
					"this project substituted", said.Said, said.Said)
			}
		}
	}

	// The mirror. A model that writes a level we already use has not been
	// translated, and marking it as though it had would make every finding in the
	// tree unquotable — the failure in the opposite direction, and the reason the
	// fact is recorded rather than assumed.
	verbatim := ours
	verbatim.Severity = string(d.WantSeverity)
	verbatim.SeverityTranslated = false
	verbatim.RawSeverity = ""

	// Asserted on the ROWS rather than the whole block: the preamble explains
	// both markers by naming them, so searching the block for either finds the
	// legend rather than a row.
	for _, line := range ScoreSeverity(fx, []review.Finding{verbatim}).Usage().Lines() {
		if strings.Contains(line, "[we read as") || strings.Contains(line, UnrecordedWord) {
			t.Errorf("a finding nothing translated is annotated as though something had: %q", line)
		}
	}

	// THE CASE THAT ACTUALLY EXERCISES THE PREDICATE, and the reason it is here:
	// with RawSeverity set, severityAsSaid quotes the word and never asks whether
	// anything translated it, so the two cases above pass under the OLD
	// source-keyed rule as well. The question only bites when the word is GONE,
	// where "nobody translated this, so Severity is the reporter's word" and
	// "somebody did and the original did not survive" are opposite claims about
	// the same empty string.
	//
	// ruff is the live example: it publishes no severity at all, this package
	// assigns warning, and under the source-keyed rule the block quoted ruff as
	// having printed "warning".
	analyzer := review.Finding{
		Path: d.Path, Line: d.Line, Title: strings.Join(d.Keywords, " "),
		Source:   "ruff(E501)",
		Severity: string(config.SeverityWarning), SeverityTranslated: true,
	}
	for _, line := range ScoreSeverity(fx, []review.Finding{analyzer}).Usage().Lines() {
		if !strings.Contains(line, UnrecordedWord) {
			t.Errorf("a reporter that published NO severity is quoted as having said one: %q. The "+
				"level is entirely this package's and there is no word to attribute", line)
		}
	}
}

// TestTheDumpDoesNotPublishOurTranslationAsTheReviewersWord carries the same
// rule into the artifact every published number is re-derived from.
//
// THE BUG: the dump wrote the translated level under a bare "severity" key
// beside "model":"incumbent/cli", and review.Finding.RawSeverity carries
// `json:"-"` — so the reviewer's own word could not reach this file at all. The
// substitution the tables had been fixed for survived one layer down, in exactly
// the file a reader opens when they doubt the tables.
func TestTheDumpDoesNotPublishOurTranslationAsTheReviewersWord(t *testing.T) {
	fx := severityFixture()
	d := fx.Defects[0]

	at := func(f review.Finding) review.Finding {
		f.Path, f.Line, f.Title = d.Path, d.Line, strings.Join(d.Keywords, " ")
		return f
	}

	cases := []struct {
		name         string
		finding      review.Finding
		wantSaid     string
		wantTranslat bool
	}{
		{
			name: "translated, word kept",
			finding: at(review.Finding{
				Severity: string(config.SeverityWarning), SeverityTranslated: true, RawSeverity: "major",
			}),
			wantSaid: "major", wantTranslat: true,
		},
		{
			name:     "nothing translated it",
			finding:  at(review.Finding{Severity: string(d.WantSeverity)}),
			wantSaid: "", wantTranslat: false,
		},
		{
			name: "translated, word gone",
			finding: at(review.Finding{
				Severity: string(config.SeverityWarning), SeverityTranslated: true,
			}),
			wantSaid: "", wantTranslat: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			records := writeDump(t, DumpSample{
				Model: "some/reviewer", Run: 1, Fixture: fx,
				Findings: []review.Finding{tc.finding},
			})
			if len(records) != 1 {
				t.Fatalf("records = %d, want 1", len(records))
			}

			got := records[0]
			if got.SeveritySaid != tc.wantSaid {
				t.Errorf("severity_said = %q, want %q", got.SeveritySaid, tc.wantSaid)
			}
			if got.SeverityTranslated != tc.wantTranslat {
				t.Errorf("severity_translated = %v, want %v. Without it, a translated finding whose "+
					"word did not survive is indistinguishable from one nobody translated — and the "+
					"second reads as a quotation", got.SeverityTranslated, tc.wantTranslat)
			}

			// And the round trip: a rebuilt finding is SCORED as well as judged,
			// so one that came back claiming nothing had translated it would put
			// our word back in the reviewer's mouth through the very file written
			// to prevent that.
			back := findingFromRecord(got)
			if severityWasTranslated(back) != tc.wantTranslat || back.RawSeverity != tc.wantSaid {
				t.Errorf("the round trip lost the provenance: translated=%v raw=%q, want %v and %q",
					severityWasTranslated(back), back.RawSeverity, tc.wantTranslat, tc.wantSaid)
			}
		})
	}
}

// TestTheResolutionIsStatedAndDerived pins the sentence the methodology gate
// asked for, and pins it to the corpus rather than to a memory of it.
//
// "A harness that says 7/8 vs 3/6, and this corpus cannot resolve a difference
// smaller than one defect, is more trustworthy than one that says 88% and 50%,
// and it costs nothing but a sentence." The sentence is cheap; a WRONG sentence
// is not, and a hand-written one goes wrong the first time a fixture is added.
// So the note is computed, and this test checks it moves with the corpus.
func TestTheResolutionIsStatedAndDerived(t *testing.T) {
	full := CorpusResolution(AllFixtures())

	planted := 0
	for _, f := range AllFixtures() {
		planted += len(f.Defects)
	}
	if !strings.Contains(full, fmt.Sprintf("%d planted defect(s)", planted)) {
		t.Errorf("the note does not state the corpus's own denominator (%d planted):\n%s", planted, full)
	}
	if !strings.Contains(full, "ONE DEFECT is the smallest difference") {
		t.Errorf("the note does not say what the smallest resolvable difference is:\n%s", full)
	}

	// THE QUOTIENT IS THE ONLY NUMBER A READER ACTS ON, AND NOTHING READ IT.
	// This test asserted the "%d planted defect(s)" phrase, a fixed sentence,
	// and that the note moves for a smaller corpus — so multiplying the
	// denominator by seven published "steps of 1/98 = 0.010" with the whole
	// suite green. A derived note whose derivation is unchecked is a hardcoded
	// sentence with extra steps.
	if want := fmt.Sprintf("1/%d = %.3f", planted, 1/float64(planted)); !strings.Contains(full, want) {
		t.Errorf("the note does not state the corpus-wide step %q:\n%s", want, full)
	}

	// And the per-fixture floor, which is the one that matches a printed CELL.
	// The note used to give the corpus-wide step alone while sitting under a
	// table whose RECALL is per (model, fixture) — eleven of these fixtures
	// plant exactly one defect, so the cell it was describing moves in steps of
	// 1.000, fourteen times coarser than the number printed beside it.
	coarsest := 0
	for _, f := range AllFixtures() {
		if f.Clean() {
			continue
		}
		if n := len(f.Defects); coarsest == 0 || n < coarsest {
			coarsest = n
		}
	}
	if want := fmt.Sprintf("plants %d, so that cell moves in steps of 1/%d = %.3f",
		coarsest, coarsest, 1/float64(coarsest)); !strings.Contains(full, want) {
		t.Errorf("the note does not state the PER-FIXTURE step %q, which is the denominator of the "+
			"RECALL cells actually printed under it:\n%s", want, full)
	}

	// It must MOVE with the corpus. A note that reads identically over a
	// different set of fixtures is a hardcoded sentence however it was produced,
	// and the whole reason it is derived is that the last hand-maintained claim
	// in this package was two rounds stale when it was found.
	if half := CorpusResolution(AllFixtures()[:1]); half == full {
		t.Error("the resolution note is identical for one fixture and for the whole corpus, so it " +
			"is not describing the corpus it was handed")
	}

	// The two degenerate corpora have to be answered rather than divided by.
	if note := CorpusResolution(nil); !strings.Contains(note, "NO FIXTURE") {
		t.Errorf("an empty corpus produces %q", note)
	}
	clean := []Fixture{{Name: "clean"}}
	if note := CorpusResolution(clean); !strings.Contains(note, "RECALL is undefined") {
		t.Errorf("a corpus with nothing planted produces %q; recall over zero defects is undefined, "+
			"not perfect", note)
	}
}

// TestTheVocabularyBlockIsAFunctionOfTheWordsNotOfMapOrder pins a property
// orderedWords documents at length and nothing checked.
//
// Its doc comment states that the spelling tie-break exists precisely because
// "two foreign words mapping to one level of ours is the normal case here, not
// the exception", and that a description a reader diffs between runs has to be
// stable. Both tie-break comparisons could be deleted with the whole suite green
// at -count=5: the rendering then varies with Go's randomized map iteration, so
// the same corpus prints three different lines across runs and a reader diffing
// two reports sees a change that did not happen.
//
// Repeated because that is what a randomized order requires: one call proves
// nothing, and the failure it is looking for appears in a minority of runs.
func TestTheVocabularyBlockIsAFunctionOfTheWordsNotOfMapOrder(t *testing.T) {
	// Three foreign words on ONE level of ours — the case the comment calls
	// normal — so the tie-break is the only thing deciding their order.
	// None of these three is a substring of one of our five level names: the
	// rendered line also carries "[we read as warning]" after every word, and a
	// probe word like "warn" would match inside THAT rather than inside the
	// vocabulary it is meant to be checking.
	words := []string{"urgent", "blocker", "sev2"}

	build := func() SeverityUsage {
		u := SeverityUsage{}
		for _, w := range words {
			u.Add(config.SeverityError, SeverityWord{Said: w, Recorded: config.SeverityWarning})
		}
		return u
	}

	want := build().Lines()[0]
	for range 200 {
		if got := build().Lines()[0]; got != want {
			t.Fatalf("the same set of words rendered two ways:\n  %s\n  %s\nA reader diffing two "+
				"reports would see a change that did not happen", want, got)
		}
	}

	// The order must also be the DOCUMENTED one — most severe level first, then
	// the reviewer's spelling — or "stable" is satisfied by any arbitrary rule.
	sorted := slices.Clone(words)
	sort.Strings(sorted)
	at := -1
	for _, w := range sorted {
		i := strings.Index(want, w+" x")
		if i < 0 {
			t.Fatalf("the word %q is missing from the rendered line: %s", w, want)
		}
		if i < at {
			t.Errorf("words recorded at one level are not ordered by the reviewer's spelling, so "+
				"the line is a function of map iteration order: %s", want)
		}
		at = i
	}
}

// TestTheNoiseToleranceIsPinnedByTheCorpus pins the one constant in the scoring
// set that nothing pinned.
//
// THE BUG: noiseTolerance survived every mutation across its whole plausible
// range — 0, 1, 2, 4, 8, 12 and 20 all left the suite green, and only an absurd
// 100000 tripped anything. Its own comment disclosed that the Incumbent corpus
// could not distinguish 4 from 8 from 12, and drew from that the conclusion that
// the value could not be checked at all. It could. The incumbent is insensitive
// to it because every Incumbent finding naming a plant sits at distance zero;
// what is sensitive to it is whether the column can see a SPAMMER, and that is
// the thing the column is for.
//
// Both bounds are recomputed from the fixtures, so this fails when a fixture is
// added that breaks one rather than when someone remembers to look. Zero is
// singled out because it is the value at which the trade the constant's comment
// argues for inverts completely: explainsAny becomes stricter than matches, and
// a near-miss loses its detection AND is counted as invented.
func TestTheNoiseToleranceIsPinnedByTheCorpus(t *testing.T) {
	if noiseTolerance <= anchorTolerance {
		t.Fatalf("noiseTolerance is %d and anchorTolerance is %d. At or below it, explainsAny asks "+
			"the same question as matches or a stricter one, so a finding that named a real defect "+
			"and anchored it slightly off loses the detection AND is counted as invented — the "+
			"double penalty explainsAny exists to avoid", noiseTolerance, anchorTolerance)
	}

	// The second bound: a fixture entirely inside the radius of its own plants
	// charges a spammer nothing, so the column measures nothing there.
	vacuous := func(tol int) []string {
		var out []string
		for _, f := range AllFixtures() {
			if len(f.Defects) == 0 {
				continue
			}
			charged := false
			for _, finding := range boilerplateGridReview(f) {
				if !explainsWithin(finding, f.Defects, tol) {
					charged = true
					break
				}
			}
			if !charged {
				out = append(out, f.Name)
			}
		}
		return out
	}

	if bad := vacuous(noiseTolerance); len(bad) > 0 {
		t.Errorf("at noiseTolerance %d, NOISE charges a comment-on-every-line spammer NOTHING on "+
			"%v. Those files are shorter than 2*%d+1, so on them the rule is not \"the comment is "+
			"near the defect it names\" — it is \"the file is short\", and the column is vacuous",
			noiseTolerance, bad, noiseTolerance)
	}

	// And it is the LARGEST such value, so the constant is a point rather than a
	// band and a mutation of it fails here.
	if bad := vacuous(noiseTolerance + 1); len(bad) == 0 {
		t.Errorf("noiseTolerance is %d and %d is still non-vacuous over every planted fixture. The "+
			"rule is to take the most generous tolerance this corpus supports, because false noise "+
			"is the accepted error and every line of slack is a near-miss not punished twice",
			noiseTolerance, noiseTolerance+1)
	}
}

// explainsWithin is explainsAny with the tolerance passed in, so the test above
// can ask what a DIFFERENT constant would have done without editing the one
// under test.
func explainsWithin(f review.Finding, defects []Defect, tol int) bool {
	for _, d := range defects {
		if f.Path != d.Path || !mentionsAny(f, d.Keywords) {
			continue
		}
		if anchorDistance(f, d.Line) <= tol {
			return true
		}
	}
	return false
}

// TestTheFiguresTheseCommentsQuoteStillReproduce reads the numbers this
// package's prose quotes about its own corpus back out of the corpus.
//
// THE BUG IT FIXES: anchoredLines' doc comment, CorpusTally.WidestAnchor's, the
// scattered-anchor row below and docs/measurement.md all said a finding "naming
// 49 separate one-line regions" scored 1, and explainsAny's said "twenty-two
// boilerplate one-liners on a grid" scored NOISE 0 against three plants. Neither
// figure reproduces from this tree. scatteredAnchorReview attaches one region
// per line of the file and the largest Head file in AllFixtures is 38 lines, so
// the measured maximum is 38; boilerplateGridReview files one finding per line,
// so the only three-plant fixture yields 36 comments, not 22. The arguments were
// right and the evidence quoted for them was invented — which is the defect
// these same files retract twice over ("a retraction argued from an
// unreproducible measurement is the same defect one level up") and the one
// docs/measurement.md's own checklist asks about: is every word of it something
// that was OBSERVED?
//
// A corrected sentence is worth nothing on its own, because the next fixture
// added to the corpus makes it wrong again in silence. So the figures are
// measured here and READ BACK OUT OF THE SOURCE, and adding a fixture that moves
// them fails this test with both numbers named.
func TestTheFiguresTheseCommentsQuoteStillReproduce(t *testing.T) {
	corpus := AllFixtures()

	widest := 0
	for _, f := range corpus {
		for _, finding := range scatteredAnchorReview(f) {
			widest = max(widest, anchoredLines(finding))
		}
	}

	// The grid strategy is described against the fixture with the most plants,
	// because "against three plants" is the whole point of the sentence: one
	// comment credited with defects it is nowhere near.
	grid, plants := 0, 0
	for _, f := range corpus {
		if len(f.Defects) > plants {
			grid, plants = len(boilerplateGridReview(f)), len(f.Defects)
		}
	}

	if widest == 0 || grid == 0 {
		t.Fatal("the corpus produced no scattered anchor and no grid comment, so this test is " +
			"measuring nothing and the sentences below are unchecked")
	}

	// Read verbatim, comments included: the claim under test IS a comment, so
	// packageSources — which blanks them — is the wrong reader here.
	quoted := map[string][]string{
		"score.go": {
			"A finding naming %d separate one-line regions scored 1",
			"Someone handed %d one-line regions has %d lines to read",
			"so %d scattered lines read as 1",
		},
		"severity_test.go": {
			"reviewer with %d extra one-line regions bolted onto each finding",
		},
	}
	for file, claims := range quoted {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("reading %s: %v", file, err)
		}
		for _, claim := range claims {
			want := strings.ReplaceAll(claim, "%d", strconv.Itoa(widest))
			if !strings.Contains(string(src), want) {
				t.Errorf("%s no longer says %q. The corpus's widest scattered anchor measures %d; "+
					"a sentence quoting any other number is evidence nobody can reproduce, which is "+
					"the defect this package has now retracted three times", file, want, widest)
			}
		}
	}

	src, err := os.ReadFile("score.go")
	if err != nil {
		t.Fatalf("reading score.go: %v", err)
	}
	gridClaim := fmt.Sprintf("%s boilerplate one-liners on a grid", numberWord(grid))
	if !strings.Contains(string(src), gridClaim) {
		t.Errorf("explainsAny no longer says %q. boilerplateGridReview files %d comments on the "+
			"%d-plant fixture it is described against", gridClaim, grid, plants)
	}
}

// numberWord spells the small integers these comments write out, so the assertion
// above compares against the sentence as a reader sees it rather than against a
// digit the prose does not use.
func numberWord(n int) string {
	words := map[int]string{
		13: "Thirteen", 14: "Fourteen", 15: "Fifteen", 20: "Twenty", 22: "Twenty-two",
		28: "Twenty-eight", 33: "Thirty-three", 36: "Thirty-six",
	}
	if w, ok := words[n]; ok {
		return w
	}
	return strconv.Itoa(n)
}

// TestSeverityCountsAreWithdrawnForAForeignVocabulary: the counts behind the O-*
// rates are the withdrawn thing, not an exemption from the withdrawal.
//
// Printing "12 accurate of 14" under a table whose O-* cells read n/a restores
// the cross-tool comparison the cells refused, in a form that is easier to quote
// than the cells were.
func TestSeverityCountsAreWithdrawnForAForeignVocabulary(t *testing.T) {
	a := Aggregate{SevAccurate: 12, SevInflated: 2, SevUnderstated: 0, SevPlanted: 14}

	foreign := a.ObjectiveSeverityCounts(IncumbentModel)
	if strings.Contains(foreign, "12 accurate") {
		t.Errorf("the severity counts are published for a contender whose vocabulary is not ours: %q",
			foreign)
	}
	if !strings.Contains(foreign, "withdrawn") {
		t.Errorf("the withdrawal is silent, so a reader sees a blank rather than a reason: %q", foreign)
	}
	// Coverage survives: how many planted defects a reviewer LOCATED is a
	// statement about detection, in nobody's severity vocabulary.
	if !strings.Contains(foreign, "14 planted") {
		t.Errorf("the coverage denominator was withdrawn with the triple; it is a detection fact "+
			"and it is what says how much of the corpus the rates cover: %q", foreign)
	}

	ours := a.ObjectiveSeverityCounts("nitpick/some-model")
	if !strings.Contains(ours, "12 accurate") || !strings.Contains(ours, "14 planted") {
		t.Errorf("our own row's counts are withheld, which withdraws the comparison the prompt is "+
			"actually tuned on: %q", ours)
	}
}

// TestTheVocabularyPreambleIsDerivedFromTheRows pins the sentence that used to be
// remembered.
//
// The block carried a hand-written claim about which words a particular reviewer
// prints — "incumbent prints 'critical' and 'major'" — inside the block whose
// entire thesis is that a reviewer's vocabulary must be quoted rather than
// restated. It was the same failure in miniature, and it was already stale: the
// engine had begun translating our own models' words too, and the sentence still
// named one reviewer.
func TestTheVocabularyPreambleIsDerivedFromTheRows(t *testing.T) {
	fx := severityFixture()
	d := fx.Defects[0]

	translated := review.Finding{
		Path: d.Path, Line: d.Line, Title: strings.Join(d.Keywords, " "),
		Severity: string(config.SeverityWarning), SeverityTranslated: true, RawSeverity: "major",
	}
	block := SeverityVocabularyBlock([]VocabularyRow{{
		Name: "some/reviewer", Usage: ScoreSeverity(fx, []review.Finding{translated}).Usage(),
	}})
	if !strings.Contains(block, `"major" read as warning`) {
		t.Errorf("the preamble does not name the word this block actually translated:\n%s", block)
	}

	// And a block with nothing translated must not claim otherwise. A preamble
	// that names words no row contains is the hand-maintained sentence again,
	// however it got there.
	verbatim := translated
	verbatim.SeverityTranslated = false
	verbatim.RawSeverity = ""

	plain := SeverityVocabularyBlock([]VocabularyRow{{
		Name: "some/reviewer", Usage: ScoreSeverity(fx, []review.Finding{verbatim}).Usage(),
	}})
	if strings.Contains(plain, "major") {
		t.Errorf("the preamble names a word that appears in no row of this block:\n%s", plain)
	}
	if !strings.Contains(plain, "NO WORD IN THIS BLOCK WAS TRANSLATED") {
		t.Errorf("a block with no translation in it does not say so:\n%s", plain)
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

// fixturePaths returns the fixture's changed files in a stable order, with how
// many lines each holds.
func fixturePaths(f Fixture) ([]string, map[string]int) {
	paths := make([]string, 0, len(f.Head))
	lines := make(map[string]int, len(f.Head))
	for p, src := range f.Head {
		paths = append(paths, p)
		lines[p] = len(strings.Split(src, "\n"))
	}
	sort.Strings(paths)
	return paths, lines
}

// keywordsPerFile collects every planted keyword in each file, in the order the
// files are first planted in. It is what the two file-wide strategies below
// title their findings with: a reviewer that names every defect in a file
// without saying where any of them is.
func keywordsPerFile(f Fixture) (map[string][]string, []string) {
	keywords := map[string][]string{}
	var order []string
	for _, d := range f.Defects {
		if _, seen := keywords[d.Path]; !seen {
			order = append(order, d.Path)
		}
		keywords[d.Path] = append(keywords[d.Path], d.Keywords...)
	}
	return keywords, order
}

// scatteredAnchorReview is a line-precise reviewer that ALSO gestures at the
// whole file, one line at a time.
//
// Every finding is the calibrated reviewer's — right defect, right line, right
// severity, right words — with one extra region per line of the file attached to
// it. That costs it nothing anywhere: anchorDistance takes the MIN over regions,
// so the primary anchor still lands on the plant, and it is noise for nothing.
// Under the widest-single-region rule it also cost nothing in ANCHOR, because
// every region it added was one line long, so it tied a perfectly calibrated
// reviewer on EVERY column of the detection metric while pointing at the whole
// file. anchoredLines is what tells them apart.
//
// This is not an invented shape. crParseAlsoApplies produces exactly it —
// "Also applies to: 3, 7, 12, ..." — so the escape was one Incumbent review
// away from being live, not a thought experiment about a reviewer nobody has.
func scatteredAnchorReview(f Fixture) []review.Finding {
	_, lines := fixturePaths(f)

	out := calibratedReview(f)
	for i := range out {
		for line := 1; line <= lines[out[i].Path]; line++ {
			out[i].AlsoAt = append(out[i].AlsoAt, review.LineSpan{Line: line})
		}
	}
	return out
}

// boilerplateGridReview is the spammer that says the right words in the wrong
// places: one comment on every line of every file that holds a plant, each
// titled with every keyword planted in that file.
//
// It locates nothing. Its comment on line 1 of a 36-line file is credited with a
// path traversal on line 19, a data race on line 25 and a descriptor leak on 19,
// because it contains the word "traversal" somewhere and shares a file with
// them. explainsAny ignored line position entirely, so all 36 comments were
// credited and NONE was noise: NOISE 0, the same value a perfectly calibrated
// reviewer scores, for a review that has identified nothing at all.
//
// Its anchors are single lines, so ANCHOR cannot see it — this is the strategy
// that survives the anchor column and the one that makes NOISE load-bearing.
func boilerplateGridReview(f Fixture) []review.Finding {
	keywords, order := keywordsPerFile(f)
	_, lines := fixturePaths(f)

	var out []review.Finding
	for _, p := range order {
		for line := 1; line <= lines[p]; line++ {
			out = append(out, review.Finding{
				Path: p, Line: line,
				Severity: string(config.SeverityCritical),
				Class:    string(config.ClassCorrectness),
				Title:    strings.Join(keywords[p], " "),
			})
		}
	}
	return out
}

// radiusSpamReview is the spammer that stays INSIDE the noise tolerance: for
// every planted defect it files one comment on every line within noiseTolerance
// of it, each titled with that defect's own keywords and rated at its level.
//
// It is the boilerplate grid with one thing changed — it does not stray outside
// the radius — and that one change made it invisible to everything. Every
// comment names a defect and sits near it, so NOISE counts none of them. Every
// anchor is a single line, so ANCHOR (per FINDING) read 1. It reports every
// severity correctly, so the severity metric is entitled to say so. Run through
// ScoreRun over AllFixtures it filed 196 findings against a calibrated
// reviewer's 14 — 182 of them on lines holding no defect — and returned
// detection and objective severity BYTE-IDENTICAL to the calibrated reference,
// with Maxes true on both.
//
// It is the reason ANCHOR now also measures the union of every finding claiming
// one defect: seventeen one-line comments about a defect and one comment naming
// seventeen regions are the same seventeen lines to read, and the per-finding
// maximum could not tell them apart.
func radiusSpamReview(f Fixture) []review.Finding {
	_, lines := fixturePaths(f)

	var out []review.Finding
	for _, d := range f.Defects {
		for line := d.Line - noiseTolerance; line <= d.Line+noiseTolerance; line++ {
			if line < 1 || line > lines[d.Path] {
				continue
			}
			out = append(out, review.Finding{
				Path: d.Path, Line: line,
				Severity: string(d.WantSeverity),
				Class:    string(d.Class),
				Title:    strings.Join(d.Keywords, " "),
			})
		}
	}
	return out
}

// terseGuessSpacing is how often the guesser files a comment: dense enough to be
// visibly a guess, sparse enough that it stays CHEAPER THAN EXPLAINING, which is
// the whole content of the cost strategy it serves.
//
// The second half is arithmetic over the corpus, not a matter of taste, and it
// is why this constant is not 3 any more. A guess costs spamTokens and an
// explained finding explainedTokens, so the guesser undercuts the calibrated
// reviewer only while
//
//	spamTokens * (defects + guesses) < explainedTokens * defects
//
// and `guesses` is one per terseGuessSpacing lines of EVERY head file in
// AllFixtures, while `defects` grows only when a plant is added. Ten fixtures
// authored to fix the severity skew added far more lines than plants, and at a
// spacing of 3 the guesser tipped over into costing 3036 against the explainer's
// 2880 — so it was caught by $/DEFECT, stopped demonstrating that NOISE is
// load-bearing, and TestEachCostStrategyIsCaughtByTheColumnItClaims failed
// exactly as it is designed to. At 5 it is 2040 against 2880.
//
// The value is a band rather than a point: 4 also clears it, by 17% rather than
// 29%. 5 is chosen for the headroom, because the margin is consumed by every
// fixture anyone adds and a guard that has to be re-derived on each one teaches
// people to adjust the constant instead of reading it. That test is the guard —
// this comment is not, and a change here that breaks the inequality fails there.
const terseGuessSpacing = 5

// terseGuessReview publishes a title and nothing else for every finding it
// files: it names each planted defect on its own line in as few words as it can,
// and scatters one-line guesses through the rest of the file, each repeating
// that file's own vocabulary.
//
// It is the honest cheap-and-noisy strategy, and it replaced one that was not.
// The row it stands in for was a line-by-line spammer justified as "forty
// one-line findings are FEWER output tokens than three explained ones" — a claim
// that only held because the row declared its own token count. Run over the real
// corpus with output billed per finding, a comment on every line of a 20-line
// file with one defect costs MORE than explaining that one defect, so $/DEFECT
// caught it and NOISE could have been deleted with every guard still green. That
// is the failure TestEachCostStrategyIsCaughtByTheColumnItClaims exists to
// report, and it reported it.
//
// What is genuinely cheap is refusing to explain. Every comment here is a title
// and nothing else, so it buys the same recall for a fraction of the output, and
// the only thing separating it from a useful reviewer is that most of what it
// filed is invented.
//
// The guesses borrow the file's PLANTED WORDS rather than saying nothing, and
// that is what makes this row test the position half of the noise rule as well
// as the text half: a guess saying "sql injection" nine lines from the SQL
// injection is exactly the finding a text-only rule credits and a positional one
// does not. A file with no plants has no vocabulary to borrow, so its guesses
// fall back to text that names nothing.
func terseGuessReview(f Fixture) []review.Finding {
	paths, lines := fixturePaths(f)
	keywords, _ := keywordsPerFile(f)

	out := calibratedReview(f)
	for _, p := range paths {
		title := spamText
		if words := keywords[p]; len(words) > 0 {
			title = strings.Join(words, " ")
		}

		for line := 1; line <= lines[p]; line += terseGuessSpacing {
			out = append(out, review.Finding{
				Path: p, Line: line,
				Severity: string(config.SeverityWarning),
				Class:    string(config.ClassMaintainability),
				Title:    title,
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
			name: "a line-precise reviewer that also points at every other line",
			why: "the enormous-span strategy spelled as a list of one-line regions instead of one " +
				"span, which is the shape crParseAlsoApplies actually emits. It is the calibrated " +
				"reviewer with 38 extra one-line regions bolted onto each finding, so it is right about " +
				"every defect, its severity and its line, AND it points at the whole file. It tied a " +
				"calibrated reviewer on RECALL, NOISE and ANCHOR together, because ANCHOR reported the " +
				"widest SINGLE region and every region it added was one line long — while " +
				"anchorDistance took the MIN over the same list, so each region could only ever help " +
				"it. ANCHOR is what tells them apart, and only once it counts DISTINCT LINES",
			review: scatteredAnchorReview,
			// Severity is untouched — it is the calibrated reviewer's — so the
			// severity metric is entitled to say so, and detection is left as the
			// only thing that can catch it. That is the point of the row: it
			// isolates ANCHOR instead of being caught three ways over.
			maxes: map[string]bool{"detection": false, "objective severity": true},
		},
		{
			name: "the right words on every line",
			why: "one comment per line, each naming every defect planted in that file. It locates " +
				"nothing — its comment on line 1 is credited with a race on line 25 for containing the " +
				"word 'race' — and explainsAny ignored line position, so every one of its comments was " +
				"credited against some plant and NONE was counted as noise: NOISE 0, the value a " +
				"perfect reviewer scores, for a review that identified nothing. Its anchors are single " +
				"lines, so ANCHOR cannot see it. NOISE is what sees it, and only once it asks where " +
				"the comment is",
			review: boilerplateGridReview,
			maxes:  map[string]bool{"detection": false, "objective severity": false},
		},
		{
			name: "the right words on every line NEAR a defect",
			why: "the row above with one thing changed: it stays inside the noise tolerance. That " +
				"made it invisible to everything at once — every comment names a defect AND sits " +
				"near it, so NOISE counts none; every anchor is one line, so ANCHOR read 1; and it " +
				"rates each plant correctly, so severity is entitled to say so. 196 findings against " +
				"a calibrated reviewer's 14, 182 of them on lines holding no defect, and BOTH " +
				"published metrics came back byte-identical to a perfectly calibrated reviewer. It " +
				"is the answer to 'what maximises this?' that the table did not contain, and the " +
				"reason ANCHOR now also counts the union of every finding claiming ONE defect: " +
				"seventeen one-line comments and one seventeen-region comment are the same " +
				"seventeen lines to read",
			review: radiusSpamReview,
			// Severity is the calibrated reviewer's, so that metric is right to
			// say so and detection is left as the only thing that can catch it —
			// the same isolation the scattered-anchor row is built for.
			maxes: map[string]bool{"detection": false, "objective severity": true},
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

	// The same question for a reviewer whose severities we TRANSLATE, where the
	// tie is not an edge case but the normal shape: crSeverity maps "major",
	// "warn" and "warning" all onto warning, so any two of them tie on rank AND
	// on the level recorded. Deciding the tie on our word would compare a string
	// that is equal by construction, leaving report order to choose which of the
	// reviewer's spellings reached the page — the same defect this test was
	// written for, reachable again the moment the reviewer's own word became the
	// thing published.
	foreign := func(word string) review.Finding {
		return review.Finding{
			Path: "a.go", Line: 1, Severity: string(crSeverity(word)), RawSeverity: word,
			Class: string(config.ClassStyle), Title: "zebra", Source: IncumbentModel,
		}
	}

	fwd := ScoreSeverity(fx, []review.Finding{foreign("major"), foreign("warn")})
	rev := ScoreSeverity(fx, []review.Finding{foreign("warn"), foreign("major")})

	x := SeverityVocabularyBlock([]VocabularyRow{{Name: IncumbentModel, Usage: fwd.Usage()}})
	y := SeverityVocabularyBlock([]VocabularyRow{{Name: IncumbentModel, Usage: rev.Usage()}})
	if x != y {
		t.Errorf("reordering a translated review changed the published vocabulary block:\n%s\nversus\n%s\n"+
			"Both words are recorded at warning and tie, so the tie-break decides which of the "+
			"REVIEWER'S spellings is quoted", x, y)
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

// packageSources returns every Go file in this package, test files included,
// keyed by name, WITH COMMENTS BLANKED OUT.
//
// Test files are read because the eval reports are RENDERED from test files
// behind the `eval` build tag. A source scan that skipped them could not see the
// two tables the head-to-head is printed in, so a header declared beside its own
// renderer — the obvious place to put one — was invisible to the guard that
// exists to notice new tables.
//
// THE BUG COMMENTS CAUSED: the scans over this text were regexps, and
// registeredHeaderNames matched `registerTableHeader(kind, X)` ANYWHERE in it —
// including inside a doc comment. So a file could declare
//
//	// Registered by reference from score.go: registerTableHeader(tableUnscored, BandedTableHeader).
//	const BandedTableHeader = "MODEL     RECALL  B-ACC"
//
// and be recorded as registered by its own prose. registerTableHeader was never
// called, AllTableHeaders never contained the header, and
// TestNoTableOffersACrossToolSeverityScore therefore never saw its columns: the
// withdrawn B-ACC column shipped with the whole suite green, and deleting only
// that comment sentence made both bypass scans fire.
//
// Blanking the comments did not fix it, and that is the lesson this function is
// left here to carry. It closed the one spelling of the bypass that had been
// found and left the family open: a comment is not the only prose in a Go file,
// and a STRING LITERAL is not a comment. This package is full of long prose
// constants — NoCrossToolSeverityScore, SeverityColumnLegend, CostReadingLegend —
// and a sentence inside any of them registered a header just as effectively:
//
//	const legend = "Registered from score.go by registerTableHeader(tableUnscored, BandedTableHeader)."
//
// passed every guard in this package with a withdrawn B-ACC column shipped
// beside it, reproduced on this tree. The remedy is not a third exclusion. It is
// to stop asking a text search what the code DOES: the registration scans parse
// the package with go/ast and look at call expressions, which neither a comment
// nor a string can be. See registeredHeaderNames.
//
// What is left for this function is the scans that are genuinely textual —
// "does any report print this identifier" — where blanking comments is still the
// right precaution and the remaining error direction is safe.
func packageSources(t *testing.T) map[string]string {
	t.Helper()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}

	out := map[string]string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		src, err := os.ReadFile(e.Name())
		if err != nil {
			t.Fatalf("reading %s: %v", e.Name(), err)
		}
		out[e.Name()] = blankComments(t, e.Name(), src)
	}

	if len(out) == 0 {
		t.Fatal("no source files were read, so every scan over them passes vacuously")
	}
	return out
}

// blankComments replaces every comment in a Go source file with spaces, keeping
// the newlines inside it so line numbers and column offsets do not move.
//
// go/scanner rather than a regexp, because the thing being removed is exactly
// the thing a regexp cannot tell from its surroundings: `// "//"` and a `//`
// inside a string literal are opposite cases, and the tokenizer is the only
// component in the tree that already answers which is which.
func blankComments(t *testing.T, name string, src []byte) string {
	t.Helper()

	fset := token.NewFileSet()
	file := fset.AddFile(name, fset.Base(), len(src))

	var s scanner.Scanner
	s.Init(file, src, func(_ token.Position, msg string) {
		t.Fatalf("scanning %s: %s", name, msg)
	}, scanner.ScanComments)

	out := make([]byte, len(src))
	copy(out, src)

	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}
		if tok != token.COMMENT {
			continue
		}
		start := file.Offset(pos)
		for i := start; i < start+len(lit) && i < len(out); i++ {
			if out[i] != '\n' {
				out[i] = ' '
			}
		}
	}

	return string(out)
}

// TestEveryTableHeaderInThePackageIsRegistered checks the one thing the compiler
// cannot: that no table is built from a string the registry never saw.
//
// THE BUG, TWICE. ReportTableHeaders returned three headers while claiming to be
// "every published table header"; CostTableHeader and the two rejudge tables
// were not in it, so appending `B-ACC` to CostTableHeader passed both the
// registration guard and the cross-tool severity guard — the withdrawn
// instrument could be reinstated in a table the mechanism did not know existed.
// The fix for that was to append the missing three to the list and add a SECOND
// hand-maintained list, a name-to-value map, so a test could check the first.
// Two lists someone has to remember to extend is not a fix for one; it is the
// same failure with an extra step, and the banded column shipped through exactly
// this kind of list.
//
// Registration is now the declaration: registerTableHeader returns its argument,
// so `var X = registerTableHeader(kind, "...")` cannot be written without
// registering, and AllTableHeaders is derived rather than typed out. What is left
// for a source scan is bypass, and there are two shapes of it — declaring a
// header string without the registrar, and building a table out of something
// that was never declared as a header at all.
func TestEveryTableHeaderInThePackageIsRegistered(t *testing.T) {
	files := packageAST(t)

	registeredName := registeredHeaderNames(files)

	// Declarations whose name ends in Header and whose value BUILDS A STRING.
	// Naming the values that qualify, rather than listing the ones to skip, is
	// what stops this rule growing an exemption per false positive:
	// crFindingHeader is a regexp and `header := crFindingHeader.FindStringSubmatch(line)`
	// is a []string, and neither is excluded by name.
	for name, file := range files {
		for _, decl := range headerDeclarations(file) {
			if registeredName[decl] {
				continue
			}
			t.Errorf("%s declares the table header %q without registering it. Every guard over "+
				"published columns runs off the registry, so an unregistered header is a table where "+
				"a withdrawn column can be reinstated with every test still green. Declare it as "+
				"registerTableHeader(kind, ...), or register the name from score.go if it belongs to "+
				"another track's file", name, decl)
		}
	}

	// The second shape: a table whose header was never declared as one. Every
	// report here underlines its header with a rule as wide as the header, which
	// is the syntactic signature of "this is a table" — so the thing being
	// measured has to be a registered header, or a parameter carrying one.
	for name, file := range files {
		for _, ident := range tableUnderlines(file) {
			if registeredName[ident] {
				continue
			}
			if forwardsRegisteredHeaders(files, name, ident, registeredName) {
				continue
			}
			t.Errorf("%s underlines a table with len(%s), which is not a registered header and is "+
				"not a parameter carrying one. A table built from an undeclared string is invisible "+
				"to every column guard in this package", name, ident)
		}
	}

	// The third shape, and the reason there is a third: the two rules above are
	// both about how somebody WROTE the table — what they named the constant,
	// whether they ruled it. Miss both and a banded column ships green, which was
	// reproduced here with a `const costBanner` and no rule under it. This one
	// reads the VALUE: anything shaped like a row of column headings is a header,
	// whatever it is called and however it is printed.
	for name, file := range files {
		for _, decl := range headerShapedStrings(file) {
			if registeredName[decl] {
				continue
			}
			t.Errorf("%s declares %s, whose VALUE is a row of column headings, without registering "+
				"it. It is not named like a header and need not be ruled like one, so neither rule "+
				"above sees it — which is exactly how a withdrawn column gets published with every "+
				"test green. Declare it as registerTableHeader(kind, ...)", name, decl)
		}
	}
}

// TestProseCannotRegisterATableHeader is the guard on the guard.
//
// Every bypass the header registry has shipped had the same shape: the scan read
// text, and text includes writing that only LOOKS like code. It was found twice.
// First in a doc comment — `// Registered by reference: registerTableHeader(...)`
// beside an unregistered const — which shipped the withdrawn B-ACC column with
// the suite green. Comments were then blanked, and the identical bypass moved
// into a string literal, which is not a comment: this package declares several
// long prose constants and a sentence in any of them registered a header just as
// well. That version also shipped a B-ACC header past all four guards, on this
// tree, before this test existed.
//
// So the rule is not "ignore comments" and it is not "ignore comments and
// strings". It is that registration is a call the program makes, and this test
// states that in the only way that survives a third disguise: three headers,
// registered by a comment, by a string, and by an actual call, and only the
// third is in the registry.
func TestProseCannotRegisterATableHeader(t *testing.T) {
	const src = `package evals

// Registered by reference from score.go: registerTableHeader(tableUnscored, CommentRegisteredHeader).
const CommentRegisteredHeader = "MODEL     RECALL  B-ACC"

const legend = "Registered from score.go by registerTableHeader(tableUnscored, StringRegisteredHeader)."

const StringRegisteredHeader = "MODEL     RECALL  B-ACC"

var CalledRegisteredHeader = registerTableHeader(tableUnscored, "MODEL     RECALL")

func init() { _ = registerTableHeader(tableUnscored, ByReferenceHeader) }
`

	parsed, err := parser.ParseFile(token.NewFileSet(), "prose.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parsing the probe source: %v", err)
	}
	files := map[string]*ast.File{"prose.go": parsed}

	registered := registeredHeaderNames(files)

	for _, name := range []string{"CommentRegisteredHeader", "StringRegisteredHeader"} {
		if registered[name] {
			t.Errorf("%s is recorded as registered, and the only thing that registers it is prose. "+
				"registerTableHeader is never called for it, AllTableHeaders will never contain it, "+
				"and every column guard in this package reads the registry — so a withdrawn column "+
				"in that header ships with the whole suite green. This is the bypass that shipped "+
				"twice", name)
		}
	}
	for _, name := range []string{"CalledRegisteredHeader", "ByReferenceHeader"} {
		if !registered[name] {
			t.Errorf("%s is registered by an actual call and the scan did not record it. A registry "+
				"that misses real registrations makes the guard fire on correct code, which is how a "+
				"guard gets exemptions written into it", name)
		}
	}

	// And the header registered only by prose must be REPORTED, not merely
	// unrecorded: the declaration scan is what turns "not in the registry" into a
	// failure a reader sees.
	declared := headerDeclarations(parsed)
	for _, want := range []string{"CommentRegisteredHeader", "StringRegisteredHeader"} {
		if !slices.Contains(declared, want) {
			t.Errorf("the declaration scan did not find %q, so nothing would report it as an "+
				"unregistered table header", want)
		}
	}
}

// TestARegistrationThatNeverRunsDoesNotCount closes the hole the AST rewrite
// opened while it was closing the prose ones.
//
// Every column guard reads the RUNTIME registry through AllTableHeaders, and the
// source scan was changed to accept a registerTableHeader call ANYWHERE — so a
// call inside an ordinary function body satisfied the scan while the registry
// stayed empty. The withdrawn banded columns shipped from a perfectly reachable
// renderer with build, vet, the full suite and golangci-lint all green;
// `unused` would have caught a dead function, and caught nothing here because
// the function was reachable, it just was not called before the tests read the
// registry. The old `var X = registerTableHeader(...)` shape had the guarantee
// built in and nobody had written down that it was load-bearing.
func TestARegistrationThatNeverRunsDoesNotCount(t *testing.T) {
	const src = `package evals

const InsideAFunctionHeader = "MODEL     RECALL  B-ACC"

func render() string {
	_ = registerTableHeader(tableScored, InsideAFunctionHeader)
	return InsideAFunctionHeader
}

var AtPackageScopeHeader = registerTableHeader(tableScored, "MODEL     RECALL")

func init() { _ = registerTableHeader(tableScored, InsideInitHeader) }
`

	parsed, err := parser.ParseFile(token.NewFileSet(), "runtime.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parsing the probe source: %v", err)
	}

	registered := registeredHeaderNames(map[string]*ast.File{"runtime.go": parsed})

	if registered["InsideAFunctionHeader"] {
		t.Error("a registerTableHeader call inside an ordinary function body is recorded as a " +
			"registration. Nothing calls that function before a test reads AllTableHeaders, so the " +
			"registry never contains the header and every column guard is blind to it — while this " +
			"scan reports the header as covered")
	}
	for _, name := range []string{"AtPackageScopeHeader", "InsideInitHeader"} {
		if !registered[name] {
			t.Errorf("%s is registered somewhere Go promises to run and the scan did not record it. "+
				"A registry that misses real registrations makes the guard fire on correct code, "+
				"which is how a guard gets exemptions written into it", name)
		}
	}
}

// TestAHeaderIsRecognizedByItsValueNotItsName closes the other half of the same
// family: a table nobody named or ruled like one.
//
// The two coverage rules were both heuristics about how a table was WRITTEN —
// an identifier ending in "header", a strings.Repeat rule under it. Missing both
// is not exotic; `const costBanner = "..."` printed without a dash rule is an
// ordinary way to write a table, and it shipped B-ACC/B-INFL/B-UNDER past all
// four guards on this tree. Renaming it fired the guard, and so did adding the
// rule; only missing both escaped, which is the definition of a heuristic rather
// than a mechanism.
func TestAHeaderIsRecognizedByItsValueNotItsName(t *testing.T) {
	const src = `package evals

const costBanner = "MODEL   RECALL  B-ACC  B-INFL  B-UNDER"

const NoCrossToolSeverityScore = "No cross-tool severity score is offered, because the vocabularies differ in resolution."

const dumpNote = "written to %s"
`

	parsed, err := parser.ParseFile(token.NewFileSet(), "banner.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parsing the probe source: %v", err)
	}

	shaped := headerShapedStrings(parsed)

	if !slices.Contains(shaped, "costBanner") {
		t.Error("costBanner's VALUE is a row of column headings and the scan did not see it. It is " +
			"not named like a header and is printed with no rule under it, so neither other rule " +
			"sees it either, and its withdrawn banded columns are published with the suite green")
	}
	// A rule that fires on prose would be worse than none: the first false
	// positive buys an exemption, and an exemption list is what the header guard
	// has already been defeated through twice.
	for _, prose := range []string{"NoCrossToolSeverityScore", "dumpNote"} {
		if slices.Contains(shaped, prose) {
			t.Errorf("%s is prose, not a table header, and the value scan claims it is one. A rule "+
				"that fires on sentences gets an exemption written into it, and exemptions are the "+
				"mechanism every bypass here has walked through", prose)
		}
	}
}

// packageAST parses every Go file in this package, test files included, keyed by
// name.
//
// Test files are parsed because the eval reports are RENDERED from test files
// behind the `eval` build tag. A scan that skipped them could not see the two
// tables the head-to-head is printed in, so a header declared beside its own
// renderer — the obvious place to put one — was invisible to the guard that
// exists to notice new tables. ParseComments is off: the comments are exactly
// what these scans must not be able to read.
func packageAST(t *testing.T) map[string]*ast.File {
	t.Helper()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}

	fset := token.NewFileSet()
	out := map[string]*ast.File{}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		parsed, err := parser.ParseFile(fset, e.Name(), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parsing %s: %v", e.Name(), err)
		}
		out[e.Name()] = parsed
	}

	if len(out) == 0 {
		t.Fatal("no source files were parsed, so every scan over them passes vacuously")
	}
	return out
}

// isRegisterCall reports whether an expression is a call of the registrar.
func isRegisterCall(e ast.Expr) (*ast.CallExpr, bool) {
	call, ok := e.(*ast.CallExpr)
	if !ok {
		return nil, false
	}
	fn, ok := call.Fun.(*ast.Ident)
	if !ok || fn.Name != "registerTableHeader" {
		return nil, false
	}
	return call, true
}

// registeredHeaderNames collects the identifiers the package hands to the
// registrar, in both spellings it takes.
//
// It reads CALL EXPRESSIONS from the parsed package. That is the fix, and the
// distinction is the whole point: a call expression is something the program
// DOES, and neither a comment nor a string literal can be one. The previous
// version was a regexp over source text, so `registerTableHeader(kind, X)`
// written in prose satisfied it — first inside a doc comment, and after comments
// were blanked, inside any of this package's several long prose constants. Both
// spellings shipped a withdrawn banded column with every guard green. Excluding
// comments and then excluding strings is a list of the disguises that have been
// noticed; parsing is the answer that does not need one.
//
// Names rather than values, because a header nobody registered has no value to
// reflect over — the whole failure being guarded is a string the registry never
// saw.
//
// ONLY REGISTRATIONS GO RUNS UNCONDITIONALLY COUNT: a package-level declaration
// or a statement in an init() body. THE BUG THIS FIXES is the one the AST rewrite
// introduced while closing the prose disguises. The old `var X =
// registerTableHeader(...)` shape had a property nobody wrote down — the
// compiler guarantees it EXECUTES — and moving the scan to "any call expression
// anywhere" dropped it. Every column guard (TestNoTableOffersACrossToolSeverityScore,
// TestEveryPublishedColumnIsRegistered, TestEveryHeaderPrintsAWholeMetric) reads
// the RUNTIME registry through AllTableHeaders, so a registration written inside
// an ordinary function body satisfied this scan and left the runtime registry
// empty: a file declaring the withdrawn banded header and printing it from a
// perfectly reachable renderer shipped with build, vet, the whole suite and the
// linter green, reproduced on this tree. A dead function would at least have
// been caught by `unused`; a reachable renderer whose registration simply never
// ran was caught by nothing.
//
// The rule is not "the call must be at the top" for tidiness. It is that this
// scan's answer — "the registry knows about this header" — is only true of calls
// the language promises to make.
func registeredHeaderNames(files map[string]*ast.File) map[string]bool {
	out := map[string]bool{}

	record := func(names []ast.Expr, values []ast.Expr) {
		for i, v := range values {
			if _, ok := isRegisterCall(v); !ok || i >= len(names) {
				continue
			}
			// `X = registerTableHeader(kind, "...")` — the declaration IS the
			// registration, which is the shape a header declared in this package
			// takes.
			if id, ok := names[i].(*ast.Ident); ok && id.Name != "_" {
				out[id.Name] = true
			}
		}
	}

	scan := func(n ast.Node) {
		ast.Inspect(n, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.ValueSpec:
				names := make([]ast.Expr, 0, len(node.Names))
				for _, id := range node.Names {
					names = append(names, id)
				}
				record(names, node.Values)

			case *ast.AssignStmt:
				record(node.Lhs, node.Rhs)

			case *ast.CallExpr:
				// `_ = registerTableHeader(kind, X)` — a header declared in
				// another track's file, registered without editing it. Only the
				// SECOND argument is the header; recording every identifier
				// argument would also record the table KIND, and a registry that
				// quietly contains `tableUnscored` is one exemption away from
				// excusing a header somebody names after it.
				if call, ok := isRegisterCall(node); ok && len(call.Args) == 2 {
					if id, ok := call.Args[1].(*ast.Ident); ok {
						out[id.Name] = true
					}
				}
			}
			return true
		})
	}

	for _, file := range files {
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.GenDecl:
				// Package-scope var/const: initialized before any test reads the
				// registry.
				scan(d)
			case *ast.FuncDecl:
				// init() is the only function body Go promises to run, and the
				// registry's own "register another track's header from here"
				// idiom is written in one.
				if d.Recv == nil && d.Name != nil && d.Name.Name == "init" && d.Body != nil {
					scan(d.Body)
				}
			}
		}
	}

	return out
}

// columnRun splits a header on the whitespace between its columns. Two spaces,
// because a single one occurs INSIDE a column name — "SEV A/I/U" is one column
// of SummaryTableHeader — and splitting on it would shatter the header rather
// than read it.
var columnRun = regexp.MustCompile(`\s{2,}`)

// headerShapedStrings returns the name of every package-level string in a file
// whose VALUE is a table header, whatever it is called.
//
// THE BUG IT FIXES: headerDeclarations asks whether an identifier ends in
// "header" and tableUnderlines asks whether a table is ruled with
// strings.Repeat("-", len(X)). Those were the only two ways a table entered the
// guard, and they are both heuristics about how somebody wrote it. Fail both —
// name the constant `costBanner` and print no rule under it, which is an
// entirely ordinary way to write a table — and the withdrawn banded columns
// shipped with the whole suite green. Reproduced on this tree with
// `const costBanner = "MODEL   RECALL  B-ACC  B-INFL  B-UNDER"` and a renderer
// writing it: B-ACC/B-INFL/B-UNDER published, AllTableHeaders never told, and
// TestNoTableOffersACrossToolSeverityScore blind. Renaming it OR adding the rule
// fired the guard; only missing both escaped.
//
// So this asks what the string IS rather than what it is called: three or more
// column names, separated by runs of two spaces or more, each spelled the way a
// column heading is spelled. It is a shape, not a name, and a header cannot be
// disguised from it without ceasing to look like a header on the page.
//
// It is deliberately narrow. Over this package it selects exactly the three
// registered scored headers and nothing else — no legend, no prose constant, no
// format string — so a hit is a table and there are no exemptions to maintain.
// A header that falls outside it (lowercase columns, single-space separation) is
// still caught by the two rules above if it is named or ruled like one; the
// three rules are independent nets, which is the point of adding a third rather
// than replacing either.
func headerShapedStrings(file *ast.File) []string {
	var out []string

	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, id := range vs.Names {
				if i >= len(vs.Values) || id.Name == "_" {
					continue
				}
				if v, ok := constantString(vs.Values[i]); ok && looksLikeATableHeader(v) {
					out = append(out, id.Name)
				}
			}
		}
	}

	sort.Strings(out)
	return out
}

// constantString evaluates the string expressions a header is written as: a
// literal, a concatenation of them, or a registrar call (which returns its
// argument). Anything else — a Sprintf, a function result — is not something
// this scan can read, and reports so rather than guessing.
func constantString(e ast.Expr) (string, bool) {
	switch v := e.(type) {
	case *ast.BasicLit:
		if v.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(v.Value)
		return s, err == nil

	case *ast.BinaryExpr:
		if v.Op != token.ADD {
			return "", false
		}
		a, aok := constantString(v.X)
		b, bok := constantString(v.Y)
		return a + b, aok && bok

	case *ast.CallExpr:
		if call, ok := isRegisterCall(v); ok && len(call.Args) == 2 {
			return constantString(call.Args[1])
		}
		return "", false
	}
	return "", false
}

// looksLikeATableHeader reports whether a string is a row of column headings.
func looksLikeATableHeader(s string) bool {
	if strings.Contains(s, "\n") {
		return false
	}

	columns := columnRun.Split(strings.TrimSpace(s), -1)
	if len(columns) < 3 {
		return false
	}

	for _, c := range columns {
		if c == "" {
			return false
		}
		// Column headings in every table here are upper case, with the
		// punctuation a compound name needs: J-INFL, SEV A/I/U, $/DEFECT.
		for _, r := range c {
			switch {
			case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			case r == '-', r == '/', r == '_', r == ' ', r == '$':
			default:
				return false
			}
		}
	}
	return true
}

// headerDeclarations returns the names of every constant or variable in a file
// whose name ends in Header and whose value builds a string.
func headerDeclarations(file *ast.File) []string {
	var out []string

	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		for i, id := range spec.Names {
			if i >= len(spec.Values) || !strings.HasSuffix(strings.ToLower(id.Name), "header") {
				continue
			}
			if buildsHeaderString(spec.Values[i]) {
				out = append(out, id.Name)
			}
		}
		return true
	})

	sort.Strings(out)
	return out
}

// buildsHeaderString reports whether an expression produces a table header
// string: a string literal, a concatenation of them, a formatted one, or a
// registration (which returns its argument).
func buildsHeaderString(e ast.Expr) bool {
	switch v := e.(type) {
	case *ast.BasicLit:
		return v.Kind == token.STRING
	case *ast.BinaryExpr:
		return v.Op == token.ADD && (buildsHeaderString(v.X) || buildsHeaderString(v.Y))
	case *ast.CallExpr:
		if _, ok := isRegisterCall(v); ok {
			return true
		}
		sel, ok := v.Fun.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		pkg, ok := sel.X.(*ast.Ident)
		return ok && pkg.Name == "fmt" && sel.Sel.Name == "Sprintf"
	default:
		return false
	}
}

// tableUnderlines returns the identifier of every `strings.Repeat("-", len(X))`
// in a file — the rule a report writes under a table header, as wide as the
// header, which is the syntactic signature of "this is a table".
//
// Matched structurally rather than by a regexp assembled from an escaped
// pattern. The escaping existed so that the scan would not match its own source
// and report this file as a report renderer; an AST match cannot have that
// problem, because the pattern is not text.
func tableUnderlines(file *ast.File) []string {
	var out []string

	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) != 2 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Repeat" {
			return true
		}
		if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "strings" {
			return true
		}
		if lit, ok := call.Args[0].(*ast.BasicLit); !ok || lit.Value != `"-"` {
			return true
		}

		width, ok := call.Args[1].(*ast.CallExpr)
		if !ok || len(width.Args) != 1 {
			return true
		}
		if fn, ok := width.Fun.(*ast.Ident); !ok || fn.Name != "len" {
			return true
		}
		if id, ok := width.Args[0].(*ast.Ident); ok {
			out = append(out, id.Name)
		}
		return true
	})

	sort.Strings(out)
	return out
}

// rendersATable reports whether a file prints one.
func rendersATable(file *ast.File) bool { return len(tableUnderlines(file)) > 0 }

// forwardsRegisteredHeaders reports whether an identifier is a helper's string
// parameter that every caller passes a registered header to.
//
// rejudge prints both its tables through one helper that takes the header as a
// parameter, so the identifier at the underline is that parameter rather than
// the header itself. Exempting parameters by name would open the rule to
// anything called "header"; this follows the indirection instead and checks the
// call sites, so the helper is covered rather than excused.
//
// The helper is looked for in the file that draws the underline, not across the
// package: `registerTableHeader(kind tableKind, header string)` also takes a
// parameter named header, and searching every file made the answer depend on map
// iteration order — the guard passed or failed at random.
func forwardsRegisteredHeaders(files map[string]*ast.File, file, ident string, registered map[string]bool) bool {
	for _, decl := range files[file].Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || !hasStringParam(fn, ident) {
			continue
		}
		if callersPassRegisteredHeaders(files, fn.Name.Name, registered) {
			return true
		}
	}
	return false
}

// hasStringParam reports whether a function takes a string parameter by this
// name.
func hasStringParam(fn *ast.FuncDecl, name string) bool {
	if fn.Type.Params == nil {
		return false
	}
	for _, field := range fn.Type.Params.List {
		typ, ok := field.Type.(*ast.Ident)
		if !ok || typ.Name != "string" {
			continue
		}
		for _, id := range field.Names {
			if id.Name == name {
				return true
			}
		}
	}
	return false
}

// callersPassRegisteredHeaders reports whether every call of a function passes a
// registered header among its arguments.
//
// The declaration can no longer be mistaken for a call site. The regexp version
// needed an optional `func` prefix in its pattern to avoid reading
// `func draw(b *strings.Builder, header string)` as a call whose arguments were
// "b *strings.Builder" and "header string" — neither of which is a registered
// header, so a helper looked unsafe merely for existing. An ast.CallExpr is not
// an ast.FuncDecl and the question does not arise.
func callersPassRegisteredHeaders(files map[string]*ast.File, fn string, registered map[string]bool) bool {
	calls := 0
	everyCallPasses := true

	for _, file := range files {
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if id, ok := call.Fun.(*ast.Ident); !ok || id.Name != fn {
				return true
			}

			for _, arg := range call.Args {
				if id, ok := arg.(*ast.Ident); ok && registered[id.Name] {
					calls++
					return true
				}
			}
			everyCallPasses = false
			return true
		})
	}

	return everyCallPasses && calls > 0
}

// TestTheHeaderRegistryIsTheOnlySourceOfHeaders pins the value side, which the
// source scan cannot see.
//
// The scan proves nothing bypassed the registrar. This proves the registry is
// what the guards actually read: ScoreTableHeaders and AllTableHeaders are
// derived from it rather than listed beside it, which is what stopped them
// drifting apart the last two times.
func TestTheHeaderRegistryIsTheOnlySourceOfHeaders(t *testing.T) {
	all := AllTableHeaders()
	if len(all) != len(registeredTableHeaders) {
		t.Errorf("AllTableHeaders returns %d of the %d registered headers; it is derived from the "+
			"registry precisely so that answer cannot be no", len(all), len(registeredTableHeaders))
	}

	seen := map[string]bool{}
	for _, h := range all {
		if strings.TrimSpace(h) == "" {
			t.Error("a registered header is empty, so every column guard over it inspects nothing")
		}
		if seen[h] {
			t.Errorf("the header %q is registered twice; a duplicate means one table's registration "+
				"is silently standing in for another's", truncate(h, 40))
		}
		seen[h] = true
	}

	for _, h := range ScoreTableHeaders() {
		if !seen[h] {
			t.Errorf("the scored header %q is not in AllTableHeaders, so the guards that run over "+
				"every header do not see it", truncate(h, 40))
		}
	}

	// Both kinds have to be populated, or a classification bug empties one and
	// every guard over it passes on nothing at all.
	if len(ScoreTableHeaders()) == 0 || len(tableHeaders(tableUnscored)) == 0 {
		t.Errorf("scored/unscored headers = %d/%d; an empty kind makes every guard over it vacuous",
			len(ScoreTableHeaders()), len(tableHeaders(tableUnscored)))
	}
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

// TestEverySeverityCellIsWithdrawnForAForeignVocabulary is the completeness half
// of the withdrawal.
//
// THE BUG: the withdrawal lived in ONE of the metric's two renderings. The judged
// tables spread the reading over O-ACC/O-INFL/O-UNDER/O-COV and route it through
// ObjectiveSeverityCells, which blanks a foreign row; the ground-truth table
// folds the same reading into a single SEV a/i/u cell and formatted the counters
// inline, ungated. "Rows that do not publish our five levels print n/a" therefore
// held because the battery printing that table happens to run no foreign
// reviewer — a property of a caller, not of the withdrawal.
//
// The required set is DERIVED, not listed here: every column of the
// objective-severity metric that no vocabulary-free metric also publishes needs a
// gated renderer. Add a third rendering to that metric and this test names the
// column it cannot find a renderer for, which is what "self-enforcing" has to
// mean after the last two guards were lists.
func TestEverySeverityCellIsWithdrawnForAForeignVocabulary(t *testing.T) {
	// Columns published by some OTHER metric are vocabulary-free by that
	// metric's own definition: RECALL sits beside SEV as its denominator and
	// counts defects located, which is a statement about detection and says
	// nothing in anyone's severity vocabulary.
	elsewhere := map[string]string{}
	for _, m := range PublishedMetrics() {
		if m.Name == "objective severity" {
			continue
		}
		for _, c := range m.Columns() {
			elsewhere[c] = m.Name
		}
	}

	cells := SeverityCells()

	var severity PublishedMetric
	for _, m := range PublishedMetrics() {
		if m.Name == "objective severity" {
			severity = m
		}
	}
	if severity.Name == "" {
		t.Fatal("no metric is named 'objective severity'; this test is checking a metric that no " +
			"longer exists and would pass on any tree")
	}

	// A tally with a reading in every component, so a renderer that has been
	// gated and one that has simply been emptied are distinguishable.
	tally := CorpusTally{
		Samples: 4, Matched: 7, Planted: 14,
		Severity: SeverityScore{Accurate: 4, Inflated: 1, Understated: 2},
	}

	for _, col := range severity.Columns() {
		if by, ok := elsewhere[col]; ok {
			t.Logf("%s is also published by the %q metric, so it is not gated on vocabulary", col, by)
			continue
		}

		render, ok := cells[col]
		if !ok {
			t.Errorf("the column %s states a severity reading in our five levels and SeverityCells "+
				"has no renderer for it. Every rendering of this metric has to pass the vocabulary "+
				"gate; an ungated one is where the withdrawn cross-tool comparison stayed on the page "+
				"last time", col)
			continue
		}

		if got := render(IncumbentModel, tally); got != "n/a" {
			t.Errorf("the %s cell for %s renders %q. A row whose vocabulary is not ours must print "+
				"n/a: %s publishes one 'critical' spanning our critical AND error, so the figure "+
				"states the vocabulary gap and not review quality in either direction",
				col, IncumbentModel, got, IncumbentModel)
		}

		// And the gate must not be satisfied by blanking the column for
		// everybody, which would withdraw the comparison our own prompt is tuned
		// on and pass this test by deleting the measurement.
		if got := render("nitpick/some-model", tally); got == "n/a" || got == "" {
			t.Errorf("the %s cell renders %q for one of our own models. Blanking it for every row "+
				"passes the withdrawal by removing the score, which is not what was retracted", col, got)
		}
	}
}

// TestNoReportFormatsSeverityCountersDirectly ties the tables to the gated
// renderers.
//
// SeverityCells proves the gate is IN the renderer. It cannot prove a table used
// the renderer, and the defect was exactly that: the ground-truth table formatted
// SevAccurate/SevInflated/SevUnderstated inline, so the withdrawal was in a
// function that cell never called.
//
// The set of report files is derived rather than listed — a file that underlines
// a table is a report renderer — so a new report is covered on the day it is
// written rather than on the day someone remembers this test exists.
//
// THE COUNTER NAMES ARE DERIVED TOO, and that is the second half of the same
// bug. This scan used to hold the literal list {SevAccurate, SevInflated,
// SevUnderstated} — the field names on Summary and Aggregate — while the
// counters are ALSO reachable as CorpusTally.Severity.Accurate/.Inflated/
// .Understated, which is a different spelling of the same three integers. A
// report doing
//
//	fmt.Fprintf(&b, "%-21s %d/%d/%d\n", model,
//	    t.Severity.Accurate, t.Severity.Inflated, t.Severity.Understated)
//
// rendered `incumbent/cli  4/1/2` — the withdrawn full-resolution cross-tool
// severity triple, on the foreign row, where the gated renderer returns "n/a"
// for the identical tally — and the whole suite stayed green. A list of what to
// guard guards what was on the list; reflecting over the types the counters
// actually live on covers a spelling nobody thought of, including the next one.
func TestNoReportFormatsSeverityCountersDirectly(t *testing.T) {
	counters := severityCounterSpellings()
	if len(counters) < 6 {
		t.Fatalf("derived only %d counter spellings (%v); the reflection below has stopped "+
			"seeing the fields it is meant to cover", len(counters), counters)
	}

	files := packageAST(t)
	sources := packageSources(t)

	renderers := 0
	for name, file := range files {
		if !rendersATable(file) {
			continue
		}
		renderers++

		text := sources[name]

		for _, c := range counters {
			if strings.Contains(text, c) {
				t.Errorf("%s prints a table and also names %s. The severity counters are rendered by "+
					"Summary.SeverityCell and Aggregate.ObjectiveSeverityCells, which withdraw the "+
					"cell for a contender whose vocabulary is not ours; formatting them at the table "+
					"is how that withdrawal came to hold in one of the metric's two renderings", name, c)
			}
		}
	}

	if renderers == 0 {
		t.Fatal("no file in this package renders a table, so this scan checked nothing")
	}
}

// severityCounterSpellings derives every way a report could name one of the
// three severity counters, from the types that hold them.
//
// Two spellings exist because the counters live on two shapes: Summary and
// Aggregate flatten them as SevAccurate/SevInflated/SevUnderstated, and
// CorpusTally nests a SeverityScore whose fields are Accurate/Inflated/
// Understated. The nested ones are qualified with the field that reaches them
// (".Severity.Accurate") rather than searched for bare: "Accurate" on its own
// matches the flattened spelling too, and matches SevAccurate's own declaration
// in score.go, so an unqualified scan reports the definitions as violations.
//
// SeverityScore.Calls is skipped by type: it is the per-finding detail a report
// legitimately names, and it is not a counter.
func severityCounterSpellings() []string {
	var out []string

	flat := reflect.TypeOf(Summary{})
	for i := range flat.NumField() {
		if name := flat.Field(i).Name; strings.HasPrefix(name, "Sev") && name != "SevUsage" {
			out = append(out, name)
		}
	}

	nested := reflect.TypeOf(CorpusTally{})
	for i := range nested.NumField() {
		f := nested.Field(i)
		if f.Type != reflect.TypeOf(SeverityScore{}) {
			continue
		}
		for j := range f.Type.NumField() {
			inner := f.Type.Field(j)
			if inner.Type.Kind() != reflect.Int {
				continue
			}
			out = append(out, "."+f.Name+"."+inner.Name)
		}
	}

	sort.Strings(out)
	return out
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
