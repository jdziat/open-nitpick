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
// pinning because any reviewer can make that call. It just is not one
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
	// review used, or the description is not a record of anything.
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
// severity tests below exercise the real parse path.
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
// unwinnable however the finding was worded, its raw output for
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

// TestIncumbentInfoOnACriticalPlantIsStillUnderstated is the mirror, and the
// half that makes the case above worth anything.
//
// Recording severities faithfully must not turn the understatement column off.
// A reviewer that rates a planted critical as `info` has under-rated
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
//   - severityVerdict ranked "none", the word for the ABSENCE of a severity,
//     which config.Severity.Rank deliberately places ABOVE critical so that
//     `fail_on: none` matches nothing, as the most severe value there is, and
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

	// Words both vocabularies publish must still round-trip untouched: the fix
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
	// treated as the absence of a claim, which lands at info, in every
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
// would go through config.Severity.Normalize and land at info, so the identical
// word is worth a different level depending on who said it. The asymmetry is
// deliberate and it is load-bearing: "major" carries more than half the
// incumbent's severity words in the shipped cache, and degrading it to info
// would understate the incumbent on nearly every row of a comparison whose whole
// point is to be like-for-like.
//
// THE REASON this TEST EXISTS AT all is that the comment beside crSeverity has
// been citing it by name, "pins the enumerated set, so a fourth translation
// cannot be added silently", while nothing of the sort was ever written. A
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
	// would not have. Words the two paths agree on, "critical", "error",
	// "minor", cost nothing and need no argument: they are recorded, not
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
// Crediting the comment whose severity sits nearest the plant, with an exact
// tie broken by band distance and then by report order, does not.
// Around a planted warning, an `error` and an `info` are both exactly one level
// away and one band away, so nothing but report order was left: [error, info]
// scored INFLATED and [info, error] scored UNDERSTATED on the same review, while
// the function's own doc comment claimed print order could not matter.
//
// The credited comment is now the most severe matching one, which is the
// severity the review HAS, `fail_on` gates on the worst thing said.
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
// was costing.
//
// THE BUG: crediting the comment NEAREST the plant meant a reviewer could buy a
// better verdict by adding a second comment at a different severity. On a planted
// error, one comment saying "warning" scored UNDERSTATED; adding a second saying
// "critical", a strictly worse review, two different claims about one defect,
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
// credit a reviewer for the severity of a bug it did not locate.
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
		// The case a per-comment reading calls flawless: the 1-step
		// understatement of the critical plant has to stay visible.
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
// picking the first matching finding, which would mark the leak inflated here
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
// times the accuracy of one that said it once, and a comment about an entirely
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
	// that drops it leaves that table with nothing where the withdrawn column
	// stood.
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
		// Matched but not judged: the judge returned no verdict for index 2.
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
// field would have left the number in the file, and a field in a JSONL dump is
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

// TestOpenDumpHonoursTheEnvironment proves the env var is read; a
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
// directions, it invented samples for corpora a contender never reviewed, and
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
// same way, printTable reads Summary.SevUsage and prints nothing at all if it is
// empty, so it gets the test the band triple never had. A description that
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

	// The census the rows are read against, stated separately from the usage
	// because the two are counted over different things: a level appears here
	// whether or not any finding reached it. The warning row is the one that
	// matters, nothing located there, and the corpus planted two.
	planted := PlantedLevels{
		config.SeverityCritical: 1,
		config.SeverityError:    4,
		config.SeverityWarning:  2,
		config.SeverityNit:      1,
	}

	block := SeverityVocabularyBlock([]VocabularyRow{
		{Name: "contender", Usage: usage, Planted: planted},
		{Name: "found-nothing", Usage: SeverityUsage{}},
	})

	for _, want := range []string{
		"planted error", "critical x2", "warning x1", "planted nit",
		"major x1 [we read as warning]",
		"(3 of 4 located)", "found-nothing: no located defect to describe",
		"planted warning  (0 of 2 located): " + NothingLocated,
	} {
		if !strings.Contains(block, want) {
			t.Errorf("the vocabulary block does not contain %q:\n%s", want, block)
		}
	}

	// A word we did not translate carries no marker. Otherwise every line of
	// every one of our own models would end in an annotation, and the one place
	// the annotation MATTERS, a foreign word with no counterpart among our five,
	// would be invisible in the noise.
	if strings.Contains(block, "critical x2 [we read as") {
		t.Errorf("an untranslated word is annotated as though it had been translated:\n%s", block)
	}

	// Deterministic: a description a reader diffs between runs cannot depend on
	// Go's map iteration order.
	for range 20 {
		row := []VocabularyRow{{Name: "contender", Usage: usage, Planted: planted}}
		if again := SeverityVocabularyBlock(row); !strings.Contains(again, "critical x2") ||
			again != SeverityVocabularyBlock(row) {
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
// warning x3", and swapping crSeverity's free "major" constant, with the cached
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
	// every finding header the CLI wrote.
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
	// not the same as the page being right, the substitution lived in the
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
// reviewer's word cannot be quoted.
//
// A Incumbent cache entry collected before the raw review was retained replays
// a previous parser's findings, and the word that parser read is gone. The
// choice there is between saying so and printing our own reading as though the
// reviewer had said it, and the second is the defect this whole block was
// rewritten to remove, silently, and on exactly the entries whose evidence is
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
	// This BLOCK was TRANSLATED: every contender above printed a word this
	// project already uses, so each line quotes its reviewer directly", sitting
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
// EqualFold. Its comment gives the reason, that a difference of case is not a
// difference of vocabulary and flagging it would bury the ones that are, while
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

	// And a real translation still has to be announced, or the case above is a
	// caption that never says anything.
	usage.Add(config.SeverityError, SeverityWord{Said: "major", Recorded: config.SeverityWarning})
	block = SeverityVocabularyBlock([]VocabularyRow{{Name: "some/model", Usage: usage}})
	if !strings.Contains(block, `"major" read as warning`) {
		t.Errorf("a genuine translation is no longer announced:\n%s", block)
	}
}

// TestOurOwnTranslationsAreNotPublishedAsOurContendersWords is the other half of
// the substitution the vocabulary block exists to prevent.
//
// THE BUG: severityWasTranslated answered from the finding's SOURCE, it was
// true only for Incumbent, so it said "nothing was translated" for every model
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
	// tree unquotable, the failure in the opposite direction, and the reason the
	// fact is recorded rather than assumed.
	verbatim := ours
	verbatim.Severity = string(d.WantSeverity)
	verbatim.SeverityTranslated = false
	verbatim.RawSeverity = ""

	// Asserted on the ROWS rather than the whole block: the preamble explains
	// both markers by naming them, so searching the block for either finds the
	// legend rather than a row.
	verbatimScore := ScoreSeverity(fx, []review.Finding{verbatim})
	for _, line := range verbatimScore.Usage().Lines(verbatimScore.Planted) {
		if strings.Contains(line, "[we read as") || strings.Contains(line, UnrecordedWord) {
			t.Errorf("a finding nothing translated is annotated as though something had: %q", line)
		}
	}

	// The case that exercises the predicate, and the reason it is here: with
	// RawSeverity set, severityAsSaid quotes the word and never asks whether
	// anything translated it, so the two cases above pass under a source-keyed
	// rule as well. The question only bites when the word is gone,
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
	analyzerScore := ScoreSeverity(fx, []review.Finding{analyzer})
	for _, line := range analyzerScore.Usage().Lines(analyzerScore.Planted) {
		if strings.Contains(line, NothingLocated) {
			// The fixture plants at more than one level and this review reaches
			// one of them; the rows for the rest carry no word to check.
			continue
		}
		if !strings.Contains(line, UnrecordedWord) {
			t.Errorf("a reporter that published NO severity is quoted as having said one: %q. The "+
				"level is entirely this package's and there is no word to attribute", line)
		}
	}
}

// TestTheDumpDoesNotPublishOurTranslationAsTheReviewersWord carries the same
// rule into the artifact every published number is re-derived from.
//
// Writing the translated level under a bare "severity" key beside
// "model":"incumbent/cli" puts our word where the reviewer's belongs, and
// review.Finding.RawSeverity carries `json:"-"`, so the reviewer's own word
// cannot reach this file on its own. The substitution the tables were fixed for
// then reappears one layer down, in the file a reader opens when they doubt
// the tables.
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
// and it costs nothing but a sentence." The sentence is cheap; a wrong sentence
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

	// THE QUOTIENT IS THE only NUMBER A READER ACTS ON, and nothing read IT.
	// This test asserted the "%d planted defect(s)" phrase, a fixed sentence,
	// and that the note moves for a smaller corpus, so multiplying the
	// denominator by seven published "steps of 1/98 = 0.010" with the whole
	// suite green. A derived note whose derivation is unchecked is a hardcoded
	// sentence with extra steps.
	if want := fmt.Sprintf("1/%d = %.3f", planted, 1/float64(planted)); !strings.Contains(full, want) {
		t.Errorf("the note does not state the corpus-wide step %q:\n%s", want, full)
	}

	// And the per-fixture floor, which is the one that matches a printed CELL.
	// The note used to give the corpus-wide step alone while sitting under a
	// table whose RECALL is per (model, fixture), eleven of these fixtures
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
	// Three foreign words on ONE level of ours, the case the comment calls
	// normal, so the tie-break is the only thing deciding their order.
	// None of these three is a substring of one of our five level names: the
	// rendered line also carries "[we read as warning]" after every word, and a
	// probe word like "warn" would match inside that rather than inside the
	// vocabulary it is meant to be checking.
	words := []string{"urgent", "blocker", "sev2"}

	build := func() SeverityUsage {
		u := SeverityUsage{}
		for _, w := range words {
			u.Add(config.SeverityError, SeverityWord{Said: w, Recorded: config.SeverityWarning})
		}
		return u
	}

	planted := PlantedLevels{config.SeverityError: 3}

	want := build().Lines(planted)[0]
	for range 200 {
		if got := build().Lines(planted)[0]; got != want {
			t.Fatalf("the same set of words rendered two ways:\n  %s\n  %s\nA reader diffing two "+
				"reports would see a change that did not happen", want, got)
		}
	}

	// The order must also be the DOCUMENTED one, most severe level first, then
	// the reviewer's spelling, or "stable" is satisfied by any arbitrary rule.
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
// range, 0, 1, 2, 4, 8, 12 and 20 all left the suite green, and only an absurd
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
// a near-miss loses its detection and is counted as invented.
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
// right and the evidence quoted for them was invented, which is the defect
// these same files retract twice over ("a retraction argued from an
// unreproducible measurement is the same defect one level up") and the one
// docs/measurement.md's own checklist asks about: is every word of it something
// that was OBSERVED?
//
// A corrected sentence is worth nothing on its own, because the next fixture
// added to the corpus makes it wrong again in silence. So the figures are
// measured here and read BACK OUT OF THE SOURCE, and adding a fixture that moves
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
	// packageSources, which blanks them, is the wrong reader here.
	measurement := measurementDocs
	quoted := map[string][]string{
		measurement: {
			"A finding naming %d separate one-line regions scored 1",
			"Someone handed %d one-line regions has %d lines to read",
		},
		"score.go": {
			"so %d scattered lines read as 1",
		},
		"severity_test.go": {
			"reviewer with %d extra one-line regions bolted onto each finding",
		},
	}
	for file, claims := range quoted {
		prose := verbatim(t, file)
		for _, claim := range claims {
			want := strings.ReplaceAll(claim, "%d", strconv.Itoa(widest))
			if !strings.Contains(prose, want) {
				t.Errorf("%s no longer says %q. The corpus's widest scattered anchor measures %d; "+
					"a sentence quoting any other number is evidence nobody can reproduce, which is "+
					"the defect this package has now retracted three times", file, want, widest)
			}
		}
	}

	gridClaim := fmt.Sprintf("%s boilerplate one-liners on a grid", numberWord(grid))
	if !strings.Contains(verbatim(t, measurement), gridClaim) {
		t.Errorf("explainsAny no longer says %q. boilerplateGridReview files %d comments on the "+
			"%d-plant fixture it is described against", gridClaim, grid, plants)
	}
}

// measurementDocs names the pair of pages the harness's notes are published
// across, rather than one file.
//
// docs/measurement.md is nav-linked as Rules and was 83% commentary on
// internal/evals, so the notes moved to docs/harness-notes.md and about half
// of the sentences below went with them. A figure has to reproduce wherever a
// reader finds it, and tracking which of the two pages each sentence landed on
// would be a second thing to keep right, so the guard reads both.
const measurementDocs = "docs/measurement.md and docs/harness-notes.md"

// sourcePaths resolves a claim group's key to the files behind it, so a key
// naming two documents is read as two.
func sourcePaths(key string) []string {
	if key == measurementDocs {
		return []string{
			filepath.Join("..", "..", "docs", "measurement.md"),
			filepath.Join("..", "..", "docs", "harness-notes.md"),
		}
	}
	return []string{key}
}

// measurementProse is the prose of both pages, joined.
func measurementProse(t *testing.T) string {
	t.Helper()
	return docProse(t, filepath.Join("..", "..", "docs", "measurement.md")) + "\n\n" +
		docProse(t, filepath.Join("..", "..", "docs", "harness-notes.md"))
}

// quotedProse reads a claim's home, whether that is a Go file's comments or a
// shipped document. A note that outgrew its declaration moved to docs/ and the
// figure in it still has to reproduce, so the reader follows it there.
func quotedProse(t *testing.T, path string) string {
	t.Helper()
	if path == measurementDocs {
		return measurementProse(t)
	}
	if strings.HasSuffix(path, ".md") {
		return docProse(t, path)
	}
	return commentProse(t, path)
}

// verbatim is quotedProse for a claim that may sit in a string literal as well
// as in a comment, which is the case for a failure message quoting its own
// figure back at the reader.
func verbatim(t *testing.T, path string) string {
	t.Helper()
	if path == measurementDocs {
		return measurementProse(t)
	}
	if strings.HasSuffix(path, ".md") {
		return docProse(t, path)
	}
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(src)
}

// numberWord spells the small integers these comments write out, so the assertion
// above compares against the sentence as a reader sees it rather than against a
// digit the prose does not use.
func numberWord(n int) string {
	words := map[int]string{
		13: "Thirteen", 14: "Fourteen", 15: "Fifteen", 20: "Twenty", 22: "Twenty-two",
		28: "Twenty-eight", 29: "Twenty-nine", 33: "Thirty-three", 36: "Thirty-six",
	}
	if w, ok := words[n]; ok {
		return w
	}
	return strconv.Itoa(n)
}

// TestAnUndeclaredScaleIsWithheld pins the three states of SeverityScale.
//
// An identity check has only two. Publishing a row at our resolution unless its
// model string is IncumbentModel leaves "undeclared" as
// not a state a row could be in: a contender nobody had thought about got the
// full five-level comparison by default, purely by not being the one reviewer
// the check named. Now the default is withheld and somebody has to say what
// scale a row is on.
//
// The word is not the scale, and this is the case that says so: a row spelling
// its severities exactly like ours is still withheld when it declares the
// foreign scale, because incumbent/cli prints "critical" for plants of both
// critical and error.
func TestAnUndeclaredScaleIsWithheld(t *testing.T) {
	base := Aggregate{SevAccurate: 4, SevInflated: 1, SevUnderstated: 2, SevPlanted: 8}

	for _, tc := range []struct {
		scale     SeverityScale
		published bool
	}{
		{UndeclaredSeverityScale, false},
		{ForeignSeverityScale, false},
		{IncumbentSeverityScale, false},
		{OurSeverityScale, true},
	} {
		row := base
		row.Scale = tc.scale

		_, _, acc, _ := row.ObjectiveSeverityCells(4)
		if published := acc != "n/a"; published != tc.published {
			t.Errorf("a row on scale %q renders O-ACC %q; published=%v, want published=%v",
				tc.scale.describe(), acc, published, tc.published)
		}

		cell := Summary{
			SevAccurate: 4, SevInflated: 1, SevUnderstated: 2, Scale: tc.scale,
		}.SeverityCell()
		if published := cell != "n/a"; published != tc.published {
			t.Errorf("a row on scale %q renders SEV %q; published=%v, want published=%v",
				tc.scale.describe(), cell, published, tc.published)
		}
	}

	// The zero value is the withheld one. A row someone forgot to declare has to
	// fall on the safe side of the withdrawal, and "safe" here means the side
	// that publishes nothing rather than the side that ranks a stranger.
	var zero SeverityScale
	if zero.PublishesOurLevels() {
		t.Error("the zero value of SeverityScale publishes our five levels, so a contender added " +
			"without a declaration is ranked on severity by default — which is the identity check " +
			"this type replaced, with a different default")
	}

	// And the incumbent's own adapter declares the foreign scale rather than
	// being recognized downstream by name.
	if IncumbentSeverityScale.PublishesOurLevels() {
		t.Errorf("%s declares %q, which publishes our five levels", IncumbentModel, IncumbentSeverityScale)
	}
}

// TestASummaryOfMixedScalesIsWithheld pins what happens when one row's runs do
// not agree about what vocabulary they are on.
//
// A row is a fold of several runs, and nothing structurally stops two adapters'
// runs landing in one. Picking whichever declaration arrived first would publish
// half a row's findings at a resolution nobody claimed for them, so disagreement
// withdraws.
func TestASummaryOfMixedScalesIsWithheld(t *testing.T) {
	fx := severityFixture()
	findings := calibratedReview(fx)

	run := func(scale SeverityScale) Score {
		return ScoreRun(RunResult{Report: &review.Report{Findings: findings}, Scale: scale}, fx)
	}

	ours := Summarize("m", fx.Name, []Score{run(OurSeverityScale), run(OurSeverityScale)})
	if ours.Scale != OurSeverityScale || ours.SeverityCell() == "n/a" {
		t.Errorf("two runs that agree on our scale produced a row on scale %q rendering %q; agreement "+
			"has to keep the comparison the prompt is tuned on", ours.Scale.describe(), ours.SeverityCell())
	}

	for _, other := range []SeverityScale{ForeignSeverityScale, UndeclaredSeverityScale} {
		mixed := Summarize("m", fx.Name, []Score{run(OurSeverityScale), run(other)})
		if mixed.Scale != UndeclaredSeverityScale || mixed.SeverityCell() != "n/a" {
			t.Errorf("a row folding a run on %q with a run on %q came out on scale %q rendering %q. "+
				"Half its findings were never declared to be on our five levels, and publishing the "+
				"row states more than was declared",
				OurSeverityScale, other.describe(), mixed.Scale.describe(), mixed.SeverityCell())
		}
	}

	// A row of no runs at all declares nothing either, rather than inheriting
	// the zero value's meaning by accident.
	if empty := Summarize("m", fx.Name, nil); empty.Scale != UndeclaredSeverityScale {
		t.Errorf("a row folding no runs came out on scale %q", empty.Scale.describe())
	}
}

// TestEveryPlantedLevelAppearsWithItsDenominator is guard 1 on the description
// that replaced the withdrawn score: over the real corpus, every level the
// fixtures plant appears on every reviewer's page with the count planted there.
//
// THE BUG IT PINS. Lines omitted the levels nobody located, on the reasoning
// that a miss is RECALL's job. Measured, that made the worst strategy's page the
// cleanest one: "report only the plants we rate critical, and call them
// critical" rendered a single line, planted critical (4 located): critical x4,
// which is a proper SUBSTRING of a calibrated reviewer's whole block, on the
// same row whose O-COV cell is blank. Absence was invisible. Restoring the
// omit-empty rule fails here, and so does handing the block no census: a count
// with no denominator is the same claim with the evidence removed.
//
// THE SECOND BUG IT PINS, and the reason every case below runs twice. The first
// version of this test built its runs as RunResult{Report: ...}, which is a run
// that always succeeded, so it could only ever ask whether the RENDERER omits a
// level, never whether the census reached the renderer. It did not: ScoreRun
// returns before ScoreSeverity when a provider errors, so a failed run
// contributed its plants to the planted TOTAL and nothing to the per-level
// census. Every third run failing over AllFixtures produced a census summing to
// 17 against a total of 29 and a page with no "nothing located" line on it, the
// same invisible absence, reached by an ordinary provider error instead of by a
// reviewer strategy. deliveries() is what makes a failed run one of the cases.
//
// THE THIRD BUG IT PINS, and the reason every case runs once per row SHAPE. Both
// halves above fold a CorpusTally, and the judged reports do not: they render
// the block from an Aggregate. That shape was never folded here, so the line
// that supplies its denominators could be deleted with this test, and the whole
// default suite, green. vocabularyRowFolds carries the measurement.
func TestEveryPlantedLevelAppearsWithItsDenominator(t *testing.T) {
	corpus := AllFixtures()

	census := PlantedLevels{}
	for _, f := range corpus {
		census.Add(f)
	}
	if len(census) < 2 {
		t.Fatalf("the corpus plants at %d level(s), so a rule about levels nobody located cannot "+
			"be tested against it", len(census))
	}

	for _, d := range append(degenerateReviewers(), degenerateReviewer{
		name: "calibrated", review: calibratedReview,
	}) {
		for _, delivery := range deliveries() {
			t.Run(d.name+"/"+delivery.name, func(t *testing.T) {
				tally := delivery.tally(corpus, d.review)
				lines := tally.Severity.Usage().Lines(tally.Severity.Planted)

				// The census and O-COV's denominator have to be the same number, or
				// a reader adding up the rows of the block gets a different corpus
				// from the one the coverage cell divides by.
				if got, want := tally.Severity.Planted.Total(), tally.Planted; got != want {
					t.Errorf("the per-level census sums to %d and the coverage denominator is %d; the "+
						"description and O-COV are being read against different corpora", got, want)
				}

				if len(lines) != len(census) {
					t.Fatalf("the block renders %d line(s) for a corpus planting at %d level(s):\n%s",
						len(lines), len(census), strings.Join(lines, "\n"))
				}

				for level, planted := range census {
					want := fmt.Sprintf("planted %-8s (", level)
					found := ""
					for _, l := range lines {
						if strings.Contains(l, want) {
							found = l
						}
					}
					if found == "" {
						t.Errorf("no line for planted %s, which this corpus plants %d of. A level a "+
							"reviewer never reached is the thing this denominator exists to show:\n%s",
							level, planted, strings.Join(lines, "\n"))
						continue
					}
					if !strings.Contains(found, fmt.Sprintf("of %d located", planted)) {
						t.Errorf("the %s line does not carry its planted total of %d: %q",
							level, planted, found)
					}
					if strings.Contains(found, UndeclaredPlantedTotal) {
						t.Errorf("the %s line renders %s even though the tally carries a census: %q",
							level, UndeclaredPlantedTotal, found)
					}
				}
			})
		}
	}

	// Every SHAPE A REPORT RENDERS THE BLOCK FROM, folded through the real
	// builder for that shape, under both deliveries. See vocabularyRowFolds for
	// the third bug this pins.
	folds := vocabularyRowFolds()
	for _, fold := range folds {
		for _, delivery := range deliveries() {
			for _, d := range append(degenerateReviewers(), degenerateReviewer{
				name: "calibrated", review: calibratedReview,
			}) {
				t.Run(fold.name+"/"+delivery.name+"/"+d.name, func(t *testing.T) {
					row, planted := fold.row(corpus, delivery.scores(corpus, d.review))

					if got := row.Planted.Total(); got != planted {
						t.Errorf("the per-level census sums to %d against this row's own planted "+
							"total of %d; the block and the coverage denominator printed on the "+
							"same row disagree about how big the corpus is", got, planted)
					}
					if len(row.Planted) < 2 {
						t.Fatalf("this fold produced a census over %d level(s), so the checks below "+
							"are satisfied by there being nothing to check", len(row.Planted))
					}

					lines := row.Usage.Lines(row.Planted)
					if len(lines) != len(row.Planted) {
						t.Fatalf("the block renders %d line(s) for a census over %d level(s):\n%s",
							len(lines), len(row.Planted), strings.Join(lines, "\n"))
					}
					for level, n := range row.Planted {
						found := ""
						for _, l := range lines {
							if strings.HasPrefix(strings.TrimSpace(l), strings.TrimSpace(
								fmt.Sprintf("planted %-8s (", level))) {
								found = l
							}
						}
						if found == "" {
							t.Errorf("no line for planted %s, which this row's census counts %d of. "+
								"A level the reviewer never reached is what the denominator exists "+
								"to show:\n%s", level, n, strings.Join(lines, "\n"))
							continue
						}
						if !strings.Contains(found, fmt.Sprintf("of %d located", n)) {
							t.Errorf("the %s line does not carry its planted total of %d: %q",
								level, n, found)
						}
						if strings.Contains(found, UndeclaredPlantedTotal) {
							t.Errorf("the %s line renders %s even though this row carries a census: "+
								"%q", level, UndeclaredPlantedTotal, found)
						}
					}
				})
			}
		}
	}

	// The folds cover every shape a row is rendered from, asserted BY TYPE
	// IDENTITY for the reason TestTheCounterScanCoversEveryShapeAReportRendersFrom
	// is: the three shapes agree on today's corpus, so dropping one changes no
	// output and the omission is invisible from the results alone.
	covered := map[reflect.Type]bool{}
	for _, fold := range folds {
		covered[fold.shape] = true
	}
	for _, shape := range reportRowShapes() {
		if !carriesSeverityUsage(shape) {
			continue
		}
		if !covered[shape] {
			t.Errorf("%s carries a severity usage map and a report renders the vocabulary block "+
				"from it, but no fold here exercises it. Its denominators are unguarded, and "+
				"because the shapes agree on this corpus that is invisible in the output",
				shape.Name())
		}
	}

	// A caller that hands over no census gets told so on every line, rather than
	// a bare count that reads like one.
	tally := tallyOver(corpus, calibratedReview)
	for _, line := range tally.Severity.Usage().Lines(nil) {
		if !strings.Contains(line, UndeclaredPlantedTotal) {
			t.Errorf("a row rendered with no planted census prints a count that reads as a "+
				"denominator: %q", line)
		}
	}
}

// TestTheVocabularyBlockStatesWhatItCannotSay keeps the limits on the page.
//
// Every one of them is something a reader would otherwise supply for themselves
// from a page of counts, that the reviewer under-claims, that a hedging
// reviewer is visible, that these are per-review figures, that one block beats
// another. A limitation recorded only in the source is a limitation nobody
// outside the source knows about, and this block is what a reader is handed IN
// PLACE OF a number, so it carries a heavier version of that duty than a column
// does.
func TestTheVocabularyBlockStatesWhatItCannotSay(t *testing.T) {
	fx := severityFixture()
	score := ScoreSeverity(fx, calibratedReview(fx))

	block := SeverityVocabularyBlock([]VocabularyRow{{
		Name: "some/model", Usage: score.Usage(), Planted: score.Planted,
	}})

	if !strings.Contains(block, VocabularyBlockLimits) {
		t.Errorf("the block does not carry its own limits:\n%s", block)
	}

	// Each limit named, so deleting one of the five is a failure rather than a
	// shorter paragraph that still contains the heading.
	for _, limit := range []string{
		"NOT DIRECTION", "NOT HEDGING", "NOT PER-REVIEW STRUCTURE",
		"NOT AN ORDER BETWEEN REVIEWERS", "NOT THE DEFECTS NOBODY REPORTED",
	} {
		if !strings.Contains(VocabularyBlockLimits, limit) {
			t.Errorf("the printed limits no longer state %q. Every one of them is a reading a "+
				"reader would otherwise take from the counts", limit)
		}
	}

	// THE HEDGING LIMIT, DEMONSTRATED RATHER THAN ASSERTED. The comment on
	// SeverityVocabularyBlock says a reviewer answering all five severities
	// renders exactly as one answering only the loudest, because
	// reportingFinding credits the loudest claim. That is a property of this
	// package, not of the corpus, so it is shown: the two strategies produce one
	// page. If it stops being true the block has gained expressiveness and the
	// paragraph naming this limit is what has to change.
	corpus := AllFixtures()
	pageOf := func(rev func(Fixture) []review.Finding) string {
		tally := tallyOver(corpus, rev)
		return strings.Join(tally.Severity.Usage().Lines(tally.Severity.Planted), "\n")
	}

	allFive := func(f Fixture) []review.Finding {
		var out []review.Finding
		for _, sev := range severityOrder {
			out = append(out, oneCommentPerPlant(sev)(f)...)
		}
		return out
	}
	loudest := oneCommentPerPlant(config.SeverityCritical)

	if hedged, loud := pageOf(allFive), pageOf(loudest); hedged != loud {
		t.Errorf("a reviewer answering all five severities no longer renders as one answering only "+
			"the loudest:\n--- all five\n%s\n--- loudest only\n%s\nThe block's own limits paragraph "+
			"says it cannot show hedging; if it now can, say so there", hedged, loud)
	}

	// The printed list must not name a reviewer's word. The preamble beside it
	// is DERIVED from the rows and says which words this block translated, so a
	// fixed sentence quoting one would be the hand-maintained claim that was
	// already removed from this block once, and would make a block containing
	// no such word announce it. See TestTheVocabularyPreambleIsDerivedFromTheRows.
	for _, word := range []string{"major", "minor", "incumbent"} {
		if strings.Contains(strings.ToLower(VocabularyBlockLimits), word) {
			t.Errorf("the printed limits name %q, a word a particular reviewer prints. The rows "+
				"quote vocabularies; this paragraph states what the block cannot express", word)
		}
	}
}

// TestTurningLintersOnWithdrawsTheSeverityComparison pins the one configuration
// question our own scale declaration depends on.
//
// review.Engine writes our five levels for a model's own findings, but a report
// is the union of the model's findings and the analyzers', and
// internal/linters' mapSeverity folds HIGH/ERROR/CRITICAL onto our error and
// MEDIUM onto warning, a codomain excluding critical and nit, which is the
// exact shape of the first retraction this package made. Those findings are
// absent from the eval today only because evalConfig turns linters off. A
// declaration that ASSERTED our scale would have published them at our
// resolution the moment anyone changed that line; a declaration DERIVED from it
// withdraws instead.
func TestTurningLintersOnWithdrawsTheSeverityComparison(t *testing.T) {
	cfg := evalConfig(Model{ID: "some/model"})

	if cfg.Linters.Mode != config.LinterOff {
		t.Fatalf("evalConfig no longer turns linters off (mode %q), so the eval's findings already "+
			"include analyzer severities folded onto three of our five levels and the declaration "+
			"below is measuring something else", cfg.Linters.Mode)
	}
	if got := ourSeverityScale(cfg); got != OurSeverityScale {
		t.Errorf("a run with linters off declares scale %q; that is the configuration the whole "+
			"O-* comparison is computed under", got.describe())
	}

	for _, mode := range []config.LinterMode{config.LinterAuto, config.LinterStrict, ""} {
		mixed := evalConfig(Model{ID: "some/model"})
		mixed.Linters.Mode = mode

		if got := ourSeverityScale(mixed); got.PublishesOurLevels() {
			t.Errorf("a run with linters %q declares scale %q, so analyzer findings whose severities "+
				"were folded onto a codomain excluding critical and nit would be scored against "+
				"WantSeverity at our full resolution", mode, got.describe())
		}
	}

	// And a run declares what its own configuration says, rather than the
	// harness asserting it once at the top of the file.
	if got := ourSeverityScale(nil); got.PublishesOurLevels() {
		t.Errorf("a run with no configuration at all declares %q", got.describe())
	}
}

// TestSeverityCountsAreWithdrawnForAForeignVocabulary: the counts behind the O-*
// rates are the withdrawn thing, not an exemption from the withdrawal.
//
// Printing "12 accurate of 14" under a table whose O-* cells read n/a restores
// the cross-tool comparison the cells refused, in a form that is easier to quote
// than the cells were.
func TestSeverityCountsAreWithdrawnForAForeignVocabulary(t *testing.T) {
	a := Aggregate{SevAccurate: 12, SevInflated: 2, SevUnderstated: 0, SevPlanted: 14}

	withheld := a
	withheld.Scale = IncumbentSeverityScale
	foreign := withheld.ObjectiveSeverityCounts(IncumbentModel)
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

	published := a
	published.Scale = OurSeverityScale
	ours := published.ObjectiveSeverityCounts("nitpick/some-model")
	if !strings.Contains(ours, "12 accurate") || !strings.Contains(ours, "14 planted") {
		t.Errorf("our own row's counts are withheld, which withdraws the comparison the prompt is "+
			"actually tuned on: %q", ours)
	}
}

// TestTheVocabularyPreambleIsDerivedFromTheRows pins a sentence that is
// otherwise kept in step by memory.
//
// The block carried a hand-written claim about which words a particular reviewer
// prints, "incumbent prints 'critical' and 'major'", inside the block whose
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
// WHAT MAXIMISES this?, the question nobody asked of the withdrawn cross-tool
// severity column, asked here of every model-free number the reports publish.
// ---------------------------------------------------------------------------

// calibratedReview is the reviewer that is correct: one comment per
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
// on all of them, the reviewer the withdrawn banded column scored as perfect.
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
// The note behind it is in docs/harness-notes.md#scatteredanchorreview.
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
// Its anchors are single lines, so ANCHOR cannot see it. This is the strategy
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
// It is the boilerplate grid with one thing changed. It does not stray outside
// the radius, and that one change made it invisible to everything. Every
// comment names a defect and sits near it, so NOISE counts none of them. Every
// anchor is a single line, so ANCHOR (per FINDING) read 1. It reports every
// severity correctly, so the severity metric is entitled to say so. Run through
// ScoreRun over AllFixtures it filed 196 findings against a calibrated
// reviewer's 14, 182 of them on lines holding no defect, and returned
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

// terseGuessSpacing is how often the guesser files a comment: dense enough to
// be
// visibly a guess, sparse enough that it stays CHEAPER THAN EXPLAINING, which
// is
// the whole content of the cost strategy it serves.
//
// The note behind it is in docs/harness-notes.md#terseguessspacing.
const terseGuessSpacing = 5

// terseGuessReview publishes a title and nothing else for every finding it
// files: it names each planted defect on its own line in as few words as it
// can,
// and scatters one-line guesses through the rest of the file, each repeating
// that file's own vocabulary.
//
// The note behind it is in docs/harness-notes.md#terseguessreview.
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

	// maxes declares, for every published metric and every published description
	// by name, whether this strategy is expected to be indistinguishable from
	// calibratedReview on it.
	//
	// For a METRIC that means scoring at least as well on every component. For a
	// DESCRIPTION there is no ordering, so it means producing a BYTE-IDENTICAL
	// page: the artifact is compared by eye, and the only honest question a
	// mechanism can ask of it is whether the two pages a reader would compare
	// are the same page.
	//
	// Every cell must be filled. That is the structural part: publishing a new
	// metric or description means answering, for each of these reviewers, "can
	// this behaviour pass for being right?", and one where the answer is yes
	// does not measure what its name claims.
	maxes map[string]bool
}

// degenerateReviewers is the table. The names in each `maxes` map are
// PublishedMetrics' and PublishedDescriptions' names; one missing from any of
// them fails the test.
func degenerateReviewers() []degenerateReviewer {
	// Locating every defect perfectly and rating them all the same word is
	// PERFECT DETECTION and NO severity opinion whatever. Detection is entitled
	// to say so; a severity metric that agrees is not measuring severity, and
	// the vocabulary block prints a different word on every row than a
	// calibrated reviewer does, so it tells them apart.
	oneWord := map[string]bool{
		"detection": true, "objective severity": false, "severity vocabulary": false,
	}

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
			// It misses the quieter plants, so detection catches it, but only
			// because detection is scored over what was PLANTED rather than over
			// what the reviewer chose to mention. A severity metric is scored over
			// what it located, which is the loophole this strategy walks through.
			maxes: map[string]bool{"detection": false, "objective severity": false, "severity vocabulary": false},
		},
		{
			name: "report only the plants we rate critical, and call them critical",
			why: "the previous row stopped one level short and the metric survived it. This one " +
				"reports nothing it is not already certain about, so every severity it states is " +
				"exactly right: O-ACC 1.000 with no inflation and no understatement, an EXACT TIE " +
				"with a perfectly calibrated reviewer, over four of the corpus's twenty-nine plants. " +
				"Selective silence is not calibration. It is caught by O-COV, the denominator that " +
				"was missing when this table was first written — and, since the denominators went " +
				"onto the vocabulary block, by the block too: its page used to be a proper SUBSTRING " +
				"of a calibrated reviewer's, one clean line reading 'planted critical (4 located)', " +
				"and it now carries four more lines reading '(0 of N located): nothing located'",
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
			maxes: map[string]bool{"detection": false, "objective severity": false, "severity vocabulary": false},
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
			maxes: map[string]bool{"detection": false, "objective severity": false, "severity vocabulary": false},
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
			// Severity is untouched. It is the calibrated reviewer's, so the
			// severity metric is entitled to say so, and detection is left as the
			// only thing that can catch it. That is the point of the row: it
			// isolates ANCHOR instead of being caught three ways over.
			maxes: map[string]bool{"detection": false, "objective severity": true, "severity vocabulary": true},
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
			maxes:  map[string]bool{"detection": false, "objective severity": false, "severity vocabulary": false},
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
			// say so and detection is left as the only thing that can catch it,
			// the same isolation the scattered-anchor row is built for.
			maxes: map[string]bool{"detection": false, "objective severity": true, "severity vocabulary": true},
		},
		{
			name: "right about everything, and pointing at a block every time",
			why: "THE SHAPE THIS TABLE DID NOT CONTAIN. Every vague row above is vague ENORMOUSLY — a " +
				"whole file, a whole grid, thirty-eight regions — and each is caught by width alone. " +
				"This one is the calibrated reviewer with each anchor widened to a modest block, the " +
				"'somewhere in this function' behaviour anchorDistance's own comment warns about, and " +
				"it needs no oracle a real reviewer lacks: widening is STRICTLY FREE, because " +
				"spanDistance is zero anywhere inside a span, so matches() and explainsAny() can only " +
				"improve. It ties a calibrated reviewer on RECALL and on NOISE and differs from it on " +
				"the worst case by exactly the width it chose — which is the point. ANCHOR is a " +
				"MAXIMUM, so a reviewer line-precise except for one wide comment reports the same " +
				"number, and any threshold read off another reviewer's maximum hands this strategy " +
				"that reviewer's single worst finding as a budget for all of its own. L/DEF is the " +
				"fold that charges it: lines pointed at per defect found",
			review: hedgedTo(hedgeSpan),
			// Severity is the calibrated reviewer's, same words on the same
			// plants, so that metric and the vocabulary block are entitled to
			// say so, and detection is left as the only thing that can catch it.
			maxes: map[string]bool{"detection": false, "objective severity": true, "severity vocabulary": true},
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
			// idempotent across identical copies, so duplication is invisible
			// to every model-free column by construction. Leaving the row out
			// would have left the question unasked rather than answered; the
			// judge's PRECISION is where verbosity is meant to be paid for.
			maxes: map[string]bool{"detection": true, "objective severity": true, "severity vocabulary": true},
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
			maxes: map[string]bool{"detection": true, "objective severity": false, "severity vocabulary": false},
		},
		{
			name:   "one comment per line of the diff",
			why:    "the review bot that comments on everything and notices nothing",
			review: oneCommentPerLine,
			// Severity is UNDEFINED for it rather than false: it grades no
			// defect, and a reviewer that says nothing about severity inflates
			// nothing. TestSilenceScoresUndefinedNotPerfect pins that separately.
			maxes: map[string]bool{"detection": false, "objective severity": false, "severity vocabulary": false},
		},
		{
			name: "one comment per line, plus the truth",
			why: "finds everything by saying everything — perfect RECALL, which is why RECALL is " +
				"never published without NOISE beside it",
			review: func(f Fixture) []review.Finding {
				return append(calibratedReview(f), oneCommentPerLine(f)...)
			},
			// Its severity behaviour is not degenerate, it rates the defects it
			// names correctly, so the severity metric is entitled to say so.
			// Detection is the one it must not win, and noise is what stops it.
			maxes: map[string]bool{"detection": false, "objective severity": true, "severity vocabulary": true},
		},
		{
			name:   "silence",
			why:    "the global optimum of any column that counts mistakes without a denominator",
			review: func(Fixture) []review.Finding { return nil },
			maxes:  map[string]bool{"detection": false, "objective severity": false, "severity vocabulary": false},
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
			maxes: map[string]bool{"detection": true, "objective severity": true, "severity vocabulary": true},
		},
	}
}

// tallyOver scores one reviewer over a corpus, with no model, judge or network.
func tallyOver(corpus []Fixture, reviewer func(Fixture) []review.Finding) CorpusTally {
	return scoresOver(corpus, reviewer, nil).tally()
}

// delivery is how a corpus of reviews REACHED the scorer, which is a separate
// axis from what the reviewer said.
//
// It exists because every model-free guard in this file used to build its runs
// as RunResult{Report: ...}, a run that always succeeded. That is the shape
// nobody was thinking about, and a whole class of bug lives in it: ScoreRun has
// two early returns for a run with no report, so anything derived from the
// FIXTURE rather than from the findings can be silently skipped on exactly the
// runs a real battery produces when a provider rate-limits. Running each case
// under both deliveries is what makes those returns part of the tested surface.
type delivery struct {
	name string

	// failing reports whether this delivery loses runs, so an assertion that
	// only holds for complete data can say which case it is looking at.
	failing bool

	scores func([]Fixture, func(Fixture) []review.Finding) corpusScores
	tally  func([]Fixture, func(Fixture) []review.Finding) CorpusTally
}

// corpusScores is one scored corpus, kept as the slice so a caller can fold it
// through whichever of TallyScores and Summarize it is testing.
type corpusScores []Score

func (s corpusScores) tally() CorpusTally { return TallyScores(s) }

// scoresOver scores a reviewer over a corpus. fails, when non-nil, decides which
// fixtures come back as a provider error instead of a report.
func scoresOver(corpus []Fixture, reviewer func(Fixture) []review.Finding, fails func(int) bool) corpusScores {
	out := make(corpusScores, 0, len(corpus))
	for i, f := range corpus {
		r := RunResult{
			Report: &review.Report{Findings: reviewer(f)},
			Scale:  OurSeverityScale,
		}
		if fails != nil && fails(i) {
			// Deliberately not a report with no findings: an empty report is a
			// review that ran and said nothing, and that is already covered by
			// the silent reviewer. This is the run that never happened.
			r = RunResult{Err: fmt.Errorf("provider 503"), Scale: OurSeverityScale}
		}
		out = append(out, ScoreRun(r, f))
	}
	return out
}

// deliveries is the two ways a battery's runs arrive: all of them, and one in
// three lost to the provider.
//
// One in three rather than one, so a failure is not confined to whichever
// fixture happens to sit first and so the surviving runs still cover every
// planted level. Every fixture failing would make the corpus empty and prove
// nothing about a partial one.
func deliveries() []delivery {
	every3rd := func(i int) bool { return i%3 == 0 }
	return []delivery{
		{
			name: "every run delivered",
			scores: func(c []Fixture, r func(Fixture) []review.Finding) corpusScores {
				return scoresOver(c, r, nil)
			},
			tally: func(c []Fixture, r func(Fixture) []review.Finding) CorpusTally {
				return scoresOver(c, r, nil).tally()
			},
		},
		{
			name:    "every third run lost to the provider",
			failing: true,
			scores: func(c []Fixture, r func(Fixture) []review.Finding) corpusScores {
				return scoresOver(c, r, every3rd)
			},
			tally: func(c []Fixture, r func(Fixture) []review.Finding) CorpusTally {
				return scoresOver(c, r, every3rd).tally()
			},
		},
	}
}

// TestNoDegenerateReviewerCanMaxOutAPublishedMetric asks, of every model-free
// number these reports publish, the question nobody asked of the one that was
// retracted: WHAT MAXIMISES this?
//
// The failure this exists to prevent was not a bad constant. A banded cross-tool
// severity accuracy figure was published, and 12 of the 29 plants sit in the
// blocking band: a reviewer that picks WHAT to report, stay silent unless the
// defect is already blocking, then call it critical, banded a perfect 12/0/0
// over those 12, an exact tie with a calibrated reviewer's 29/0/0. The column
// was maximised by the worst production behaviour there is, and nothing in the
// tree asked. A metric that rewards stamping "critical" on everything a reviewer
// bothers to mention would, if anyone optimised against it, produce exactly the
// review bot this project exists not to be.
//
// (TWO FIGURES IN this COMMENT HAVE BEEN CORRECTED. It said "a PERFECT record,
// 10 of 10", which scored the degenerate strategy over the plants the INCUMBENT
// located rather than over what it reports; finding that is what added the
// selective-reporting row below. It then said the two stampers banded 12
// accurate and 2 inflated of 14 against the incumbent's 6 of 10, which was true
// of a fourteen-plant corpus and is not true of this one, on the corpus in this
// tree they band 12/17/0 of 29 and no longer tie. The stamper half of the
// argument is withdrawn; the selective half reproduces and is what the sentence
// above now rests on. TestTheSeverityFiguresTheseCommentsQuoteStillReproduce
// reads both back out of the corpus.)
//
// So every published metric and every published description is crossed with a
// table of reviewers nobody would ship, and each cell is DECLARED. A metric a
// degenerate strategy can score as well as a correct reviewer on does not
// measure what its name claims and must not be published; a description a
// degenerate strategy can reproduce BYTE FOR BYTE is not telling a reader the
// two reviewers apart. The failure below names which artifact and which strategy.
func TestNoDegenerateReviewerCanMaxOutAPublishedMetric(t *testing.T) {
	corpus := AllFixtures()
	metrics := PublishedMetrics()
	descriptions := PublishedDescriptions()

	if len(metrics) == 0 {
		t.Fatal("no published metric is registered, so this test proves nothing about the tables")
	}
	if len(descriptions) == 0 {
		t.Fatal("no published description is registered. The severity vocabulary block is what " +
			"REPLACED a withdrawn score, so it inherits that score's question; an unregistered one " +
			"is the question going unasked again")
	}

	reference := tallyOver(corpus, calibratedReview)

	// The reference has to be a reviewer this corpus can reward, or
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

			// The descriptions, on the same terms. There is no ordering on a
			// page of text, so the comparison is byte-identity: the question is
			// whether a reader diffing the two artifacts would see a difference
			// at all.
			for _, desc := range descriptions {
				want, declared := d.maxes[desc.Name]
				if !declared {
					t.Errorf("description %q is published and this table does not say whether %q "+
						"produces the same page as a calibrated reviewer. Fill the cell: if the "+
						"answer is yes, the description does not distinguish them", desc.Name, d.name)
					continue
				}

				same := desc.Render(got) == desc.Render(reference)

				switch {
				case same && !want:
					t.Errorf("DESCRIPTION %q IS REPRODUCED BYTE FOR BYTE BY %q, which %s.\n%s\n"+
						"A reader comparing the two pages by eye sees no difference, so this artifact "+
						"does not describe %q for this strategy. Add what it is missing — the last "+
						"time, that was the planted denominator on every level — or stop publishing "+
						"it as a description of severity behaviour.",
						desc.Name, d.name, d.why, desc.Render(got), desc.Doc)
				case !same && want:
					t.Errorf("this table declares that %q produces the calibrated page for %q and it "+
						"does not:\n--- calibrated\n%s\n--- degenerate\n%s\nA stale declaration hides "+
						"the next real one", d.name, desc.Name, desc.Render(reference), desc.Render(got))
				}
			}
		})
	}

	// Every metric must be falsifiable by SOMETHING here, or the crossing above
	// is decoration: a metric no degenerate strategy is expected to fail is one
	// this table never tests.
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

	// And the same for the descriptions, which is the assertion the crossing
	// above would otherwise be missing entirely: a description every strategy is
	// declared able to reproduce is a page that says nothing about any of them,
	// and it would pass the loop above by being declared true everywhere.
	for _, desc := range descriptions {
		distinguishes := false
		for _, d := range degenerateReviewers() {
			if !d.maxes[desc.Name] {
				distinguishes = true
			}
		}
		if !distinguishes {
			t.Errorf("every strategy in the degenerate table is declared to produce the calibrated "+
				"page for %q, so the table asserts nothing about it. Either it is not a description "+
				"of the reviewer, or a strategy it would distinguish is missing from the table",
				desc.Name)
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
// silence becomes the global optimum of a tuning objective, the exact failure
// Aggregate.Precision's doc comment records having shipped once, where a variant
// with no findings sorted to the top and switched off the suite's only assertion.
// TestSilenceIsNotStable pins the second return value of Summary.Stable.
//
// STABLE is printed "yes" or "NO [3 5 4]" and a reader takes yes as better. Five
// runs of silence used to return true, the column's BEST value went to a
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
// It said ties were between findings carrying the same severity, so order
// decided only which comment a diagnostic NAMED. Ties are on NORMALIZED rank,
// and the credited finding's RAW spelling is what SeverityUsage records, which
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
	// "warn" and "warning" all onto warning, so any two of them tie on rank and
	// on the level recorded. Deciding the tie on our word would compare a string
	// that is equal by construction, leaving report order to choose which of the
	// reviewer's spellings reached the page, the same defect this test was
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

// TestADestroyedWordDoesNotOutrankAKeptOne is the third state of the same
// tie-break, and it was the one the ordering rule got backwards.
//
// severityAsSaid returns an EMPTY Said for a finding something translated
// without keeping the original, and the block renders that as UnrecordedWord.
// The tie-break compared spellings lexicographically, and "" sorts before every
// real word, so a defect matched by one finding carrying RawSeverity "Error"
// and one carrying none published `(word not recorded)` in both report orders,
// with the reviewer's word sitting in the finding list beside it. Order
// independence held; it held on the wrong answer, which is why the test above
// could not see this.
//
// The shape is not contrived. internal/linters marks every analyzer finding
// translated and gives it no raw word, so a run with linters on is a run where
// half the findings are the empty side of this tie. ourSeverityScale withdraws
// the SCORE in that configuration, and the DESCRIPTION is published regardless,
// so the gap phrase was reachable in exactly the configuration the withdrawal
// was written for.
func TestADestroyedWordDoesNotOutrankAKeptOne(t *testing.T) {
	fx := Fixture{
		Name: "destroyed",
		Head: map[string]string{"a.go": "package a\n"},
		Defects: []Defect{{
			Path: "a.go", Line: 1, Why: "a kept word", WantSeverity: config.SeverityError,
			Class: config.ClassCorrectness, Keywords: []string{"zebra"},
		}},
	}

	// Both are translated and both are recorded at error, so they tie on rank
	// and on our word: only the reviewer's spelling separates them.
	comment := func(raw string) review.Finding {
		return review.Finding{
			Path: "a.go", Line: 1, Title: "zebra",
			Severity: string(config.SeverityError), SeverityTranslated: true, RawSeverity: raw,
			Class: string(config.ClassCorrectness),
		}
	}

	for _, order := range [][]review.Finding{
		{comment("Error"), comment("")},
		{comment(""), comment("Error")},
	} {
		s := ScoreSeverity(fx, order)
		lines := strings.Join(s.Usage().Lines(s.Planted), "\n")

		if strings.Contains(lines, UnrecordedWord) {
			t.Errorf("with findings arriving %q then %q, the block reports the reviewer's word as "+
				"unrecoverable:\n%s\nOne of the two findings kept %q. %s is what this block prints "+
				"when there is nothing else to print; it may not outrank something",
				order[0].RawSeverity, order[1].RawSeverity, lines, "Error", UnrecordedWord)
		}
		if !strings.Contains(lines, "Error x1") {
			t.Errorf("with findings arriving %q then %q, the block does not quote the word the "+
				"review kept:\n%s", order[0].RawSeverity, order[1].RawSeverity, lines)
		}
	}

	// A defect matched only by findings whose word was destroyed still reports
	// the gap: the fix is about which of two available answers wins, not about
	// filling a gap in from our own reading.
	only := ScoreSeverity(fx, []review.Finding{comment(""), comment("")})
	if lines := strings.Join(only.Usage().Lines(only.Planted), "\n"); !strings.Contains(lines, UnrecordedWord) {
		t.Errorf("with every matching finding's word destroyed, the block no longer reports the "+
			"gap:\n%s\nQuoting the reviewer as having said a word we chose is the substitution this "+
			"whole block was rewritten to stop", lines)
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
// legend, and nothing anywhere required them to be declared, which is why the
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
// PublishedMetric is a GROUP for a stated reason, "O-INFL alone is maximised by
// silence and is only a score BESIDE O-UNDER and O-ACC", and nothing enforced
// it. Deleting O-ACC and O-UNDER from VariantTableHeader, leaving O-INFL
// published by itself, passed the whole package including the guard named after
// this failure. A metric may be rendered more than one way (one cell holding
// "SEV a/i/u" beside RECALL, or the triple spread over O-* beside O-COV); a
// header must carry one rendering whole.
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
// The note behind it is in docs/harness-notes.md#packagesources.
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
// registration guard and the cross-tool severity guard, the withdrawn
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
// for a source scan is bypass, and there are two shapes of it, declaring a
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
	// is the syntactic signature of "this is a table", so the thing being
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
	// both about how somebody WROTE the table, what they named the constant,
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
// First in a doc comment, `// Registered by reference: registerTableHeader(...)`
// beside an unregistered const, which shipped the withdrawn B-ACC column with
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
// The note behind it is in docs/harness-notes.md#testaregistrationthatneverrunsdoesnotcount.
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
// The two coverage rules were both heuristics about how a table was WRITTEN,
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
// renderer, the obvious place to put one, was invisible to the guard that
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
// Does, and neither a comment nor a string literal can be one. The previous
// version was a regexp over source text, so `registerTableHeader(kind, X)`
// written in prose satisfied it, first inside a doc comment, and after comments
// were blanked, inside any of this package's several long prose constants. Both
// spellings shipped a withdrawn banded column with every guard green. Excluding
// comments and then excluding strings is a list of the disguises that have been
// noticed; parsing is the answer that does not need one.
//
// Names rather than values, because a header nobody registered has no value to
// reflect over, the whole failure being guarded is a string the registry never
// saw.
//
// Only registrations Go runs unconditionally count: a package-level declaration
// or a statement in an init() body. This is the hole the AST rewrite opened
// while closing the prose disguises. A `var X =
// registerTableHeader(...)` shape had a property nobody wrote down, the
// compiler guarantees it EXECUTES, and moving the scan to "any call expression
// anywhere" dropped it. Every column guard (TestNoTableOffersACrossToolSeverityScore,
// TestEveryPublishedColumnIsRegistered, TestEveryHeaderPrintsAWholeMetric) reads
// the RUNTIME registry through AllTableHeaders, so a registration written inside
// an ordinary function body satisfied this scan and left the runtime registry
// empty: a file declaring the withdrawn banded header and printing it from a
// perfectly reachable renderer shipped with build, vet, the whole suite and the
// linter green, reproduced on this tree. A dead function would at least have
// been caught by `unused`; a reachable renderer whose registration never
// ran was caught by nothing.
//
// The rule is not "the call must be at the top" for tidiness. It is that this
// scan's answer, "the registry knows about this header", is only true of calls
// the language promises to make.
func registeredHeaderNames(files map[string]*ast.File) map[string]bool {
	out := map[string]bool{}

	record := func(names []ast.Expr, values []ast.Expr) {
		for i, v := range values {
			if _, ok := isRegisterCall(v); !ok || i >= len(names) {
				continue
			}
			// `X = registerTableHeader(kind, "...")`. The declaration IS the
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
				// `_ = registerTableHeader(kind, X)`, a header declared in
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
// because a single one occurs INSIDE a column name, "SEV A/I/U" is one column
// of SummaryTableHeader, and splitting on it would shatter the header rather
// than read it.
var columnRun = regexp.MustCompile(`\s{2,}`)

// headerShapedStrings returns the name of every package-level string in a file
// whose VALUE is a table header, whatever it is called.
//
// THE BUG IT FIXES: headerDeclarations asks whether an identifier ends in
// "header" and tableUnderlines asks whether a table is ruled with
// strings.Repeat("-", len(X)). Those were the only two ways a table entered the
// guard, and they are both heuristics about how somebody wrote it. Fail both,
// name the constant `costBanner` and print no rule under it, which is an
// entirely ordinary way to write a table, and the withdrawn banded columns
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
// registered scored headers and nothing else, no legend, no prose constant, no
// format string, so a hit is a table and there are no exemptions to maintain.
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
// argument). Anything else, a Sprintf, a function result, is not something
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
// in a file, the rule a report writes under a table header, as wide as the
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
// The note behind it is in docs/harness-notes.md#forwardsregisteredheaders.
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
// "b *strings.Builder" and "header string", neither of which is a registered
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
// what the guards read: ScoreTableHeaders and AllTableHeaders are
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
	// Over all headers, not the score tables: the previous version ran over
	// three of six, so the withdrawn column could be reinstated in the cost
	// table with every guard green.
	// The spellings a banded severity column can take: anything mentioning a
	// band, or B- carrying one of the severity triple's suffixes. A bare "B-"
	// prefix is too wide, the agreement table's B-only counts findings only
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

	// The full-resolution triple on a foreign row is the same offer in a
	// different spelling, and the previous guard could not see it: it matched
	// column names, and the columns kept their names. What changed was which
	// rows get a number in them.
	a := Aggregate{SevAccurate: 4, SevInflated: 1, SevUnderstated: 2, SevPlanted: 8}

	for _, withheld := range []SeverityScale{IncumbentSeverityScale, UndeclaredSeverityScale} {
		row := a
		row.Scale = withheld

		infl, under, acc, cov := row.ObjectiveSeverityCells(4)
		for name, cell := range map[string]string{
			"O-INFL": infl, "O-UNDER": under, "O-ACC": acc, "O-COV": cov,
		} {
			if cell != "n/a" {
				t.Errorf("the %s cell for a row on scale %q renders %q. A row that has not declared "+
					"our five levels must print n/a there: the banded triple was withdrawn and the "+
					"full-resolution one was left in the same columns of the same sorted ranking, "+
					"annotated with a note telling the reader not to compare it — which is the "+
					"mitigation the previous retraction had already recorded as insufficient",
					name, withheld.describe(), cell)
			}
		}
	}

	ours := a
	ours.Scale = OurSeverityScale
	if infl, _, _, cov := ours.ObjectiveSeverityCells(4); infl == "n/a" || cov == "n/a" {
		t.Errorf("the O-* cells for a row declared on our own scale render n/a (%s, %s). Blanking "+
			"them for OUR OWN rows would withdraw the comparison the prompt is actually tuned on, "+
			"which is not what was retracted", infl, cov)
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
// reviewer, a property of a caller, not of the withdrawal.
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
	// gated and one that has been emptied are distinguishable.
	tally := CorpusTally{
		Samples: 4, Matched: 7, Planted: 14,
		Severity: SeverityScore{Accurate: 4, Inflated: 1, Understated: 2},
	}

	// Undeclared is tested beside foreign, and it is the state a name-check gate
	// does not have. Under one, a contender added without a declaration
	// publishes at our resolution by default for not being spelled
	// IncumbentModel; here it is withheld by default and someone has to
	// say what scale it is on. See SeverityScale.
	withheld := map[SeverityScale]CorpusTally{}
	for _, scale := range []SeverityScale{IncumbentSeverityScale, UndeclaredSeverityScale} {
		t := tally
		t.Scale = scale
		withheld[scale] = t
	}

	published := tally
	published.Scale = OurSeverityScale

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

		for scale, row := range withheld {
			if got := render(row); got != "n/a" {
				t.Errorf("the %s cell for a row on scale %q renders %q. A row that has not declared "+
					"our five levels must print n/a: %s publishes one 'critical' spanning our critical "+
					"AND error, so the figure states the vocabulary gap and not review quality in "+
					"either direction, and a row nobody declared has not even said that much",
					col, scale.describe(), got, IncumbentModel)
			}
		}

		// And the gate must not be satisfied by blanking the column for
		// everybody, which would withdraw the comparison our own prompt is tuned
		// on and pass this test by deleting the measurement.
		if got := render(published); got == "n/a" || got == "" {
			t.Errorf("the %s cell renders %q for one of our own models. Blanking it for every row "+
				"passes the withdrawal by removing the score, which is not what was retracted", col, got)
		}
	}
}

// TestAFailedRunStillDeclaresItsScale pins where the declaration is made, which
// turns out to be a question about which severity cell a reader is shown.
//
// The declaration depends only on the configuration, and the configuration is
// fully determined before RunWithPersona does anything that can fail. It was
// nonetheless assigned after the temp-directory and fixture-build returns, so a
// run that died in either carried none, and commonScale withdraws a row whose
// runs disagree. Folded with a good run, a failure carrying the declaration
// leaves the row on our scale; a failure carrying none takes the row to n/a.
// One class of infrastructure failure, two different published cells, decided by
// which side of one line the provider happened to break on.
//
// The fold is exercised rather than the harness, because RunWithPersona reaches
// a provider and this suite does not. What is asserted is the CONSEQUENCE, that
// a failed run's declaration decides a published cell, plus the source fact
// that the assignment precedes the returns.
func TestAFailedRunStillDeclaresItsScale(t *testing.T) {
	fx := severityFixture()
	good := ScoreRun(RunResult{
		Report: &review.Report{Findings: calibratedReview(fx)},
		Scale:  OurSeverityScale,
	}, fx)

	declared := ScoreRun(RunResult{Err: fmt.Errorf("mkdir temp: read-only"), Scale: OurSeverityScale}, fx)
	silent := ScoreRun(RunResult{Err: fmt.Errorf("mkdir temp: read-only")}, fx)

	withDecl := Summarize("m", fx.Name, []Score{good, declared}).SeverityCell()
	without := Summarize("m", fx.Name, []Score{good, silent}).SeverityCell()
	if withDecl == without {
		t.Fatalf("a failed run's declaration makes no difference to the published cell (%q both "+
			"ways), so this test cannot show that where it is assigned matters. Check the fold "+
			"before concluding the harness is safe", withDecl)
	}

	// The source fact that makes the consequence unreachable: nothing that can
	// fail runs before the declaration.
	src, err := os.ReadFile("harness.go")
	if err != nil {
		t.Fatalf("reading harness.go: %v", err)
	}
	// Anchored on the CALL, not on the syntax around it: whether the
	// declaration is a composite-literal field or an assignment is a style
	// question, and pinning the spelling would make a refactor look like the
	// regression while letting the regression through under the other spelling.
	body := string(src)
	decl := strings.Index(body, "ourSeverityScale(cfg)")
	if decl < 0 {
		t.Fatal("RunWithPersona no longer calls ourSeverityScale(cfg); if the declaration moved, " +
			"this test is checking a line that does not exist")
	}
	for _, canFail := range []string{`os.MkdirTemp("", "nitpick-eval-")`, "buildRepo(dir, f)"} {
		at := strings.Index(body, canFail)
		if at < 0 {
			t.Errorf("harness.go no longer contains %q, so this test cannot show the declaration "+
				"precedes it", canFail)
			continue
		}
		if at < decl {
			t.Errorf("%q runs before the severity scale is declared. A run that dies there carries no "+
				"declaration, and commonScale then withdraws the whole row — so an infrastructure "+
				"failure decides which severity cell a reader is shown, differently depending on "+
				"where it happened", canFail)
		}
	}
}

// TestARowWithdrawsWhenItsAdaptersDisagree pins Aggregate.DeclareScale against
// the first-declaration-wins rule it replaced.
//
// Both judged paths build their rows one result at a time. One wrote out the
// disagreement check by hand; the other constructed `&Aggregate{Scale:
// result.Scale}` on whichever goroutine reached the map first and never looked
// again. That is the rule commonScale refuses on the un-judged path, live on the
// judged one, in a shape no test covered, a row folding two adapters was
// published at whichever resolution won the race.
func TestARowWithdrawsWhenItsAdaptersDisagree(t *testing.T) {
	fold := func(scales ...SeverityScale) SeverityScale {
		var a Aggregate
		for _, s := range scales {
			a.DeclareScale(s)
		}
		return a.Scale
	}

	for _, c := range []struct {
		name   string
		scales []SeverityScale
		want   SeverityScale
	}{
		{"a single declaration stands", []SeverityScale{OurSeverityScale}, OurSeverityScale},
		{"agreeing declarations stand", []SeverityScale{
			OurSeverityScale, OurSeverityScale, OurSeverityScale,
		}, OurSeverityScale},
		{"a foreign row stays foreign", []SeverityScale{
			IncumbentSeverityScale, IncumbentSeverityScale,
		}, IncumbentSeverityScale},
		{"two adapters withdraw", []SeverityScale{
			OurSeverityScale, IncumbentSeverityScale,
		}, UndeclaredSeverityScale},
		{"order does not decide it", []SeverityScale{
			IncumbentSeverityScale, OurSeverityScale,
		}, UndeclaredSeverityScale},
		{"an undeclared result withdraws a declared row", []SeverityScale{
			OurSeverityScale, UndeclaredSeverityScale,
		}, UndeclaredSeverityScale},
		{"withdrawal is sticky", []SeverityScale{
			OurSeverityScale, IncumbentSeverityScale, OurSeverityScale, OurSeverityScale,
		}, UndeclaredSeverityScale},
		{"declaring nothing first does not withdraw a row on its own", []SeverityScale{
			UndeclaredSeverityScale, UndeclaredSeverityScale,
		}, UndeclaredSeverityScale},
	} {
		if got := fold(c.scales...); got != c.want {
			t.Errorf("%s: folding %v gives scale %q, want %q", c.name, c.scales,
				got.describe(), c.want.describe())
		}
	}

	// The first declaration on a fresh row is not a disagreement with the zero
	// value. Without the separate "has anybody declared yet" bit both states are
	// the empty string and every row would withdraw.
	var a Aggregate
	a.DeclareScale(OurSeverityScale)
	if !a.Scale.PublishesOurLevels() {
		t.Errorf("a row's first declaration left it on scale %q; the zero value and a declaration "+
			"of undeclared are the same string, so telling them apart is what keeps this from "+
			"withdrawing everything", a.Scale.describe())
	}
}

// TestNoAggregateLiteralSetsItsOwnScale keeps the fold in one place.
//
// A row's scale is the fold of what its results declared, and DeclareScale is
// where that fold lives. A composite literal that sets Scale directly bypasses
// it, which is how one of the two judged paths came to have the disagreement
// check and the other not. The scan is over the AST rather than the text so that
// the field name appearing in a comment or a message is not a violation.
func TestNoAggregateLiteralSetsItsOwnScale(t *testing.T) {
	literals := 0
	for name, file := range packageAST(t) {
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			ident, ok := lit.Type.(*ast.Ident)
			if !ok || ident.Name != "Aggregate" {
				return true
			}
			literals++

			for _, elt := range lit.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "Scale" {
					t.Errorf("%s builds an Aggregate literal that sets Scale directly. A row's scale "+
						"is folded from what each result DECLARED — call DeclareScale, which withdraws "+
						"the row when two adapters disagree. Setting it in the literal takes whichever "+
						"declaration arrived first, which is the rule commonScale refuses and which "+
						"one judged path had for a whole round", name)
				}
			}
			return true
		})
	}

	if literals == 0 {
		t.Fatal("no Aggregate literal was found in this package, so this scan checked nothing")
	}
}

// TestAWithdrawnRowsProseDoesNotLeakASeverityVerdict closes the one position
// TestNoReportFormatsSeverityCountersDirectly cannot see: INSIDE the gated
// renderer.
//
// That scan looks for a counter named in a file that draws a table. judge.go
// draws none. It is where the gated renderers live, and it names the counters
// legitimately, in the branch that publishes them. So the withdrawn branch of
// ObjectiveSeverityCounts is a blind spot on both guards at once, and it is the
// most attractive place in the tree to put a cross-tool figure: it is the prose
// line printed under the table for precisely the row whose four O-* cells read
// n/a. Appending `; B-ACC %d/%d` there put the retracted banded triple back on
// the incumbent's row with the whole default suite green.
//
// THE RULE IS not A LIST OF FORBIDDEN FIELDS, because that is what returned
// three times. It is: a withdrawn row's line may depend on HOW MANY defects were
// graded and not on HOW they were graded. Two aggregates are rendered whose
// verdicts are distributed differently and whose SevGraded is identical, and the
// withdrawn output must be byte-identical between them. SevGraded survives the
// withdrawal on its own argument, how many planted defects a reviewer LOCATED
// is a detection fact in nobody's severity vocabulary, so holding it constant
// is what isolates the part that does not.
//
// Every verdict-naming int on Aggregate is varied, not just today's three, so a
// counter added at a coarser resolution is covered by existing. The published
// branch is required to DIFFER between the same two aggregates, or the whole
// test would pass on a renderer that prints nothing at all.
func TestAWithdrawnRowsProseDoesNotLeakASeverityVerdict(t *testing.T) {
	// spread and lump grade the same number of defects, three, and disagree
	// about every verdict. Fields beyond the core triple are varied too, at a
	// value SevGraded does not sum, so a new counter changes the output without
	// changing the quantity that is allowed to reach the page.
	build := func(accurate, inflated, understated, others int) Aggregate {
		a := Aggregate{
			SevAccurate: accurate, SevInflated: inflated, SevUnderstated: understated,
			SevPlanted: 29,
		}

		v := reflect.ValueOf(&a).Elem()
		typ := v.Type()
		varied := 0
		for i := range typ.NumField() {
			f := typ.Field(i)
			if f.Type.Kind() != reflect.Int || !namesAVerdict(f.Name) {
				continue
			}
			varied++
			switch f.Name {
			case "SevAccurate", "SevInflated", "SevUnderstated":
			default:
				v.Field(i).SetInt(int64(others))
			}
		}
		if varied < 3 {
			t.Fatalf("only %d verdict-naming int field(s) on Aggregate; this test varies the "+
				"counters by reflection and has stopped finding them", varied)
		}
		return a
	}

	spread := build(1, 1, 1, 0)
	lump := build(3, 0, 0, 41)

	if spread.SevGraded() != lump.SevGraded() {
		t.Fatalf("the two aggregates grade %d and %d defects; they have to agree, or a difference "+
			"in the withdrawn line would be the legitimate one", spread.SevGraded(), lump.SevGraded())
	}

	for _, scale := range []SeverityScale{IncumbentSeverityScale, UndeclaredSeverityScale} {
		a, b := spread, lump
		a.Scale, b.Scale = scale, scale

		if x, y := a.ObjectiveSeverityCounts("some/model"), b.ObjectiveSeverityCounts("some/model"); x != y {
			t.Errorf("on scale %q the counts line changes with how the located defects were GRADED:\n"+
				"%s\nversus\n%s\nBoth graded %d defects. The cells on that row read n/a because our five "+
				"levels and this reviewer's are not commensurable; restating the comparison in prose "+
				"under the table publishes it in a form that is easier to quote",
				scale.describe(), x, y, a.SevGraded())
		}

		ai, au, aa, ac := a.ObjectiveSeverityCells(1)
		bi, bu, ba, bc := b.ObjectiveSeverityCells(1)
		if ai != bi || au != bu || aa != ba || ac != bc {
			t.Errorf("on scale %q the O-* cells change with the verdict distribution: %s/%s/%s/%s "+
				"against %s/%s/%s/%s", scale.describe(), ai, au, aa, ac, bi, bu, ba, bc)
		}
	}

	// The gate is not satisfied by a renderer that says nothing. On our own
	// scale the same two aggregates must be distinguishable, or the equality
	// above is measuring a function that ignores its input.
	ours, theirs := spread, lump
	ours.Scale, theirs.Scale = OurSeverityScale, OurSeverityScale
	if x, y := ours.ObjectiveSeverityCounts("ours"), theirs.ObjectiveSeverityCounts("ours"); x == y {
		t.Errorf("on our own scale the counts line is the same for two different verdict "+
			"distributions (%s). The equality checked above then proves nothing: it would hold for a "+
			"renderer that prints no severity reading at all", x)
	}
}

// namesAVerdict reports whether a field name states one of the published
// severity verdicts, which is what makes a counter a counter rather than a
// denominator. Derived from the constants for the reason severityCounterSpellings
// derives its spellings from them.
func namesAVerdict(field string) bool {
	lower := strings.ToLower(field)
	for _, v := range []string{SevAccurate, SevInflated, SevUnderstated} {
		if strings.Contains(lower, v) {
			return true
		}
	}
	return false
}

// TestNoReportFormatsSeverityCountersDirectly ties the tables to the gated
// renderers.
//
// The note behind it is in docs/harness-notes.md#testnoreportformatsseveritycountersdirectly.
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
// severity counters, from the types that hold them.
//
// The note behind it is in docs/harness-notes.md#severitycounterspellings.
func severityCounterSpellings() []string { return counterSpellingsOver(reportRowShapes()) }

// reportRowShapes is every type a published row is rendered from.
//
// The note behind it is in docs/harness-notes.md#reportrowshapes.
func reportRowShapes() []reflect.Type {
	return []reflect.Type{
		reflect.TypeOf(Summary{}),
		reflect.TypeOf(Aggregate{}),
		reflect.TypeOf(CorpusTally{}),
	}
}

// carriesSeverityUsage reports whether a row shape holds a severity vocabulary
// tabulation, directly or through the SeverityScore it nests.
//
// Derived rather than listed because it decides which shapes
// TestEveryPlantedLevelAppearsWithItsDenominator is REQUIRED to fold through: a
// shape that gains a usage map gains a denominator to keep honest, and a list
// would go stale exactly when that happened.
func carriesSeverityUsage(shape reflect.Type) bool {
	for i := range shape.NumField() {
		switch f := shape.Field(i); {
		case f.Type == reflect.TypeOf(SeverityUsage{}):
			return true
		case f.Type == reflect.TypeOf(SeverityScore{}):
			// SeverityScore reaches its tabulation through a method rather than
			// a field, so the nesting is what identifies it.
			return true
		}
	}
	return false
}

// vocabularyRowFold is one shape's real path from a scored corpus to the row the
// vocabulary block is rendered from, beside the planted total that shape
// publishes as its own denominator.
type vocabularyRowFold struct {
	name  string
	shape reflect.Type
	row   func([]Fixture, corpusScores) (VocabularyRow, int)
}

// vocabularyRowFolds is every way a published row reaches SeverityVocabularyBlock.
//
// THE BUG this PINS. The census that makes a level with nothing located visible
// was checked on CorpusTally and on Summary, and Aggregate, the shape both
// judged reports render the block from, including the head-to-head against the
// incumbent, was never folded. Deleting `a.SevPlantedLevels.Add(f)` from
// Aggregate.AddSeverity, the one line that supplies those denominators on that
// path, left the entire default suite green. Under that deletion the most
// selective strategy's judged page collapses back to one line reading
// "planted critical (4 located, PLANTED TOTAL not DECLARED): critical x4",
// a proper substring of a calibrated reviewer's page, which is precisely the
// absence-is-invisible failure the census exists to close, restored on the only
// path a reader reads.
//
// It is the same omission twice: the counter scan walked two of these three
// shapes for two rounds for the same reason, that Summary and Aggregate agree on
// today's corpus so dropping either changes no output. Coverage is asserted
// against reportRowShapes by type identity for that reason.
//
// Each fold returns the shape's OWN planted denominator rather than the corpus
// census, because the two legitimately differ: TallyScores and Summarize count a
// failed run's plants (the review never happened, but the defects were still
// planted), while the judged path calls AddSeverity only for a sample that was
// also judged, so numerator and denominator drop together there. The invariant
// that holds for all three is that a row's per-level census sums to the
// denominator that row publishes beside it.
func vocabularyRowFolds() []vocabularyRowFold {
	return []vocabularyRowFold{
		{
			name:  "CorpusTally",
			shape: reflect.TypeOf(CorpusTally{}),
			row: func(_ []Fixture, scores corpusScores) (VocabularyRow, int) {
				t := scores.tally()
				return VocabularyRow{
					Name:    "reviewer",
					Usage:   t.Severity.Usage(),
					Planted: t.Severity.Planted,
				}, t.Planted
			},
		},
		{
			name:  "Summary",
			shape: reflect.TypeOf(Summary{}),
			row: func(_ []Fixture, scores corpusScores) (VocabularyRow, int) {
				s := Summarize("m", "corpus", scores)
				return VocabularyRow{
					Name:    "reviewer",
					Usage:   s.SevUsage,
					Planted: s.SevPlantedLevels,
				}, s.Total
			},
		},
		{
			name:  "Aggregate",
			shape: reflect.TypeOf(Aggregate{}),
			// Built exactly the way the judged paths build it: AddSeverity is
			// called for a sample that produced findings and nowhere else, so a
			// lost run contributes to neither half. Writing the fold any other
			// way would test a path no report takes.
			row: func(corpus []Fixture, scores corpusScores) (VocabularyRow, int) {
				var a Aggregate
				a.DeclareScale(OurSeverityScale)
				for i, f := range corpus {
					if s := scores[i]; s.Err == nil && s.Report != nil {
						a.AddSeverity(f, ScoreSeverity(f, s.Report.Findings))
					}
				}
				return VocabularyRow{
					Name:    "reviewer",
					Usage:   a.SevUsage,
					Planted: a.SevPlantedLevels,
				}, a.SevPlanted
			},
		},
	}
}

// counterSpellingsOver applies the rule to a given set of shapes, so the rule can
// be asked about counters nobody has written yet.
func counterSpellingsOver(shapes []reflect.Type) []string {
	// The published verdict vocabulary, from the constants rather than as
	// literals: a fourth verdict gets guarded by being declared.
	counts := namesAVerdict

	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}

	for _, shape := range shapes {
		for i := range shape.NumField() {
			f := shape.Field(i)

			if f.Type.Kind() == reflect.Int && counts(f.Name) {
				add(f.Name)
				continue
			}

			if f.Type != reflect.TypeOf(SeverityScore{}) {
				continue
			}
			for j := range f.Type.NumField() {
				inner := f.Type.Field(j)
				if inner.Type.Kind() == reflect.Int && counts(inner.Name) {
					add("." + f.Name + "." + inner.Name)
				}
			}
		}
	}

	sort.Strings(out)
	return out
}

// syntheticRow is a shape nobody renders, used to ask the derivation about
// counters that do not exist in the tree.
//
// It carries one of each thing the rule has to tell apart: a counter at today's
// resolution, a counter at the coarser one the withdrawn B-ACC came back as, a
// nested triple, and three quantities that are not verdicts, the corpus census,
// its per-level split, and a detection count.
type syntheticRow struct {
	SevAccurate       int
	SevBandedAccurate int
	SevPlanted        int
	SevPlantedLevels  PlantedLevels
	Matched           int
	Severity          SeverityScore
}

// TestTheCounterScanCoversEveryShapeAReportRendersFrom is the falsifiability
// half of severityCounterSpellings, and it is in two parts because the bug had
// two halves that no single assertion can see at once.
//
// THE SHAPE LIST IS CHECKED BY TYPE IDENTITY, not behaviourally, and that is
// forced rather than lazy. The scan walked Summary and CorpusTally and not
// Aggregate for two rounds; Summary and Aggregate spell the counters
// identically, so removing Aggregate from the walk changes not ONE derived
// spelling. Coverage and non-coverage are indistinguishable in the output, and
// the only statement that separates them is "this type is in the list".
//
// THE RULE IS CHECKED OVER A SHAPE that does not EXIST, because the question
// that matters is what happens to the next counter rather than to today's three.
func TestTheCounterScanCoversEveryShapeAReportRendersFrom(t *testing.T) {
	got := severityCounterSpellings()
	if len(got) == 0 {
		t.Fatal("the scan derives no counter spellings at all, so TestNoReportFormatsSeverityCounters" +
			"Directly is scanning report sources for nothing and passes on any tree")
	}

	walked := map[reflect.Type]bool{}
	for _, s := range reportRowShapes() {
		walked[s] = true
	}
	for _, want := range []struct {
		name string
		typ  reflect.Type
	}{
		{"Summary", reflect.TypeOf(Summary{})},
		{"Aggregate", reflect.TypeOf(Aggregate{})},
		{"CorpusTally", reflect.TypeOf(CorpusTally{})},
	} {
		if !walked[want.typ] {
			t.Errorf("%s is not among the shapes the counter scan walks, so a severity counter "+
				"named on it is unguarded. Its spellings coincide with another shape's today, which "+
				"is exactly why this is asserted about the LIST and not about the output: dropping it "+
				"is invisible in the derived spellings and was invisible for two rounds", want.name)
		}
	}

	// The rule, over a shape nobody renders. Exact set, not "contains": guarding
	// a denominator makes the scan cry wolf, and a scan that cries wolf gets
	// exemptions until it guards nothing.
	rule := counterSpellingsOver([]reflect.Type{reflect.TypeOf(syntheticRow{})})
	want := []string{
		".Severity.Accurate", ".Severity.Inflated", ".Severity.Understated",
		"SevAccurate", "SevBandedAccurate",
	}
	if !slices.Equal(rule, want) {
		t.Errorf("over a synthetic row the derivation returns %v, want %v.\n"+
			"SevBandedAccurate is the field the withdrawn cross-tool triple came back as and has to "+
			"be guarded by the verdict it names. SevPlanted, SevPlantedLevels and Matched are "+
			"denominators and detection counts — quantities a report is supposed to print — and "+
			"guarding them is how the previous two versions of this rule broke", rule, want)
	}
}

// TestMajorIsAFreeParameterSoNoCrossToolScoreIsOffered is the third measurement
// behind the retraction, and the one that is easiest to argue with.
//
// crSeverity records Incumbent's "major", a word with no counterpart among our
// five, at warning. That is a guess. This test re-parses the SHIPPED reviews
// with the guess changed to error, byte-for-byte identical input otherwise, and
// shows two things:
//
//   - the severity result MOVES. A published figure that changes when we change
//     our own constant, with no change whatever in the reviewer's output, is not
//     a measurement of the reviewer.
//   - the corpus cannot settle the guess, because the word STRADDLES. 'major' is
//     credited on 8 plants, landing 1 critical, 3 error and 4 warning, so no
//     single level of ours is right for every plant it lands on and neither
//     candidate mapping is a description of the word.
//
// The stronger claim, that every plant a 'major' is credited on is planted
// above warning so no observation here distinguishes the two mappings, is
// false. Measured, half of them land at warning, and the corpus distinguishes
// the mappings sharply: the full-resolution triple moves 6/4/4 to 5/8/1 when
// the constant is swapped, which is this test's own first bullet. The body
// below asserts the opposite
// ("warning is the PLURALITY landing"), so the comment and the code it
// introduces disagreed about the evidence. What survives is the conclusion, on
// better grounds: the reason to publish no cross-tool score is that the word
// spans three of our levels, not that the corpus is silent about it.
// TestTheSeverityFiguresTheseCommentsQuoteStillReproduce reads the split back
// out of the cache, which is what the previous version of this sentence lacked.
//
// The remedy is not to re-tune the constant, which would move the number our way
// on no evidence for the fourth time. It is to publish no cross-tool severity
// score, which is what NoCrossToolSeverityScore says and what the tables now do.
func TestMajorIsAFreeParameterSoNoCrossToolScoreIsOffered(t *testing.T) {
	var (
		shipped, swapped SeverityScore
		majorPlants      []config.Severity
		majors           int
	)

	// Every cached review, tuning and held-out alike. This tunes nothing, it
	// demonstrates that a constant in OUR parser is unconstrained by the corpus,
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
		// The only change: the word this parser maps by guesswork. Everything
		// else about the review, its findings, anchors, prose, is identical.
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

	// The revisit this test once demanded, carried out.
	//
	// Erroring the moment a 'major' landed on a plant of warning made sense
	// while none ever had and the mapping was a guess with nothing to check it
	// against. Evidence exists now, so the question changes from "is this
	// arbitrary" to "what does the evidence say".
	//
	// It says the word straddles. Across both corpora 'major' is credited on
	// plants of critical, error and warning, and 'critical' is credited on
	// plants of critical and error. Neither vendor word corresponds to one of
	// ours, which is the same conclusion that withdrew the cross-tool severity
	// score, now held up by measurement rather than by the absence of it.
	//
	// Within that, warning is the PLURALITY landing for 'major', so the shipped
	// mapping is the best single answer available. Recording that is not a
	// licence to publish a score built on it: a plurality of a straddling word
	// is still an approximation, and the previous three attempts here each moved
	// a number toward this project's side on less evidence than this.
	spread := map[config.Severity]int{}
	for _, planted := range majorPlants {
		spread[planted]++
	}

	if len(spread) < 2 {
		t.Errorf("'major' now lands on a single planted level %v, so it no longer straddles and the "+
			"reason no cross-tool severity score is offered has changed. Revisit that decision "+
			"deliberately rather than leaving this comment describing a corpus that moved", spread)
	}

	if spread[config.SeverityWarning] <= spread[config.SeverityError] {
		t.Errorf("warning is no longer the plurality landing for 'major' (%v); crSeverity maps it "+
			"to warning on exactly that basis, so the constant and its evidence have parted company",
			spread)
	}

	t.Logf("'major' -> warning: %d/%d/%d accurate/inflated/understated; 'major' -> error: %d/%d/%d. "+
		"Same bytes from %s over %d credited observation(s), landing %v: the word straddles, which is "+
		"why no cross-tool severity score is published, and warning is its plurality, which is why "+
		"crSeverity maps it there",
		shipped.Accurate, shipped.Inflated, shipped.Understated,
		swapped.Accurate, swapped.Inflated, swapped.Understated,
		IncumbentModel, len(majorPlants), spread)
}

// TestTheDescriptionDoesNotMoveWhenOurConstantDoes is guard 4 on the artifact
// that replaced a withdrawn score, and it is the one that already failed once in
// a different form.
//
// The block was published INSTEAD of a figure that was withdrawn for being a
// function of crSeverity's free "major" constant. Its first version printed the
// level that constant translated each foreign word TO, so the replacement was a
// function of the same constant, presented as an observation. The words are now
// quoted from the retained review; this asserts the consequence, that changing
// the constant, with the cached bytes untouched, changes only the part of the
// page LABELLED AS OURS.
//
// The normalization is exactly that labelling: every "read as <level>" is
// replaced, which covers both the "[we read as X]" marker on each word and the
// derived preamble naming what this block translated. Nothing else in the page
// may move.
//
// IT CAN FAIL, and the mechanism is worth stating because it is not obvious.
// reportingFinding credits the MOST SEVERE matching finding and ranks on our
// TRANSLATED level, so a different constant can promote a different finding to
// the credit and change which WORD is printed. On the shipped cache it does not.
// If it ever does, the description has become a function of the constant it was
// published in place of, which is the failure that already happened here.
func TestTheDescriptionDoesNotMoveWhenOurConstantDoes(t *testing.T) {
	// Every "read as <one of our five levels>", wherever it appears, is our
	// reading and is allowed to move. Anchored on our own level names rather than
	// on `\w+` so that a reviewer's word is never swallowed by the pattern.
	ourReading := regexp.MustCompile(`read as (critical|error|warning|info|nit)`)

	block := func(recordMajorAt config.Severity) string {
		usage := SeverityUsage{}
		planted := PlantedLevels{}
		majors := 0

		for _, f := range AllFixtures() {
			c, ok := readCRCache(crCacheDir, f)
			if !ok || c.Raw == "" {
				continue
			}
			findings, err := parseIncumbent([]byte(c.Raw))
			if err != nil {
				t.Fatalf("%s: the shipped review no longer parses: %v", f.Name, err)
			}

			// The only change: the level OUR parser records the reviewer's word
			// at. The bytes, the findings, the anchors and the printed words are
			// identical, which is what makes any difference in the page ours.
			for i := range findings {
				if strings.EqualFold(findings[i].RawSeverity, "major") {
					findings[i].Severity = string(recordMajorAt)
					majors++
				}
			}

			s := ScoreSeverity(f, findings)
			usage.Merge(s.Usage())
			planted.Merge(s.Planted)
		}

		if majors == 0 {
			t.Skip("no cached review uses the word this constant translates; there is nothing to move")
		}
		return SeverityVocabularyBlock([]VocabularyRow{{
			Name: IncumbentModel, Usage: usage, Planted: planted,
		}})
	}

	shipped := block(config.SeverityWarning)
	swapped := block(config.SeverityError)

	if shipped == swapped {
		t.Fatalf("moving the constant did not change the page AT ALL, including the part marked as "+
			"our reading. The marker is what makes our translation visible, so a page that ignores "+
			"the constant entirely has stopped publishing it:\n%s", shipped)
	}

	got := ourReading.ReplaceAllString(shipped, "read as OUR-READING")
	want := ourReading.ReplaceAllString(swapped, "read as OUR-READING")

	if got != want {
		t.Errorf("re-recording the incumbent's word at a different level of OURS changed the "+
			"description beyond the readings marked as ours:\n--- major recorded at warning\n%s\n"+
			"--- major recorded at error\n%s\n"+
			"The block was published INSTEAD of a figure withdrawn for being a function of this "+
			"constant. reportingFinding credits the most severe matching finding and ranks on our "+
			"TRANSLATED level, so a different constant can credit a different finding and change "+
			"which of the reviewer's words is printed — that is the mechanism to look at first",
			got, want)
	}
}

// TestTheSeverityFiguresTheseCommentsQuoteStillReproduce is the same rule as the
// test above, applied to the numbers the SEVERITY retraction rests on.
//
// Those figures were the ones that went stale, and they went stale in the
// direction that flattered this project. "This corpus bands 12 blocking, 1
// medium, 1 low" and "every plant Incumbent locates is blocking" described a
// fourteen-plant corpus; the corpus is now twenty-nine plants over thirty
// fixtures, and on it the two stampers no longer tie a calibrated reviewer at
// all, so half the maximisation argument had to be withdrawn rather than
// restated. Nothing in the tree noticed, because the anchor-width test above was
// the only thing reading figures back out of the corpus and it read only
// anchors.
//
// THE BANDED VERDICT IS RECONSTRUCTED HERE, inside this function, and nowhere
// else. It is the instrument that was deleted, so no report may compute it, but
// a retraction whose own evidence cannot be recomputed is the defect this
// package has now corrected three times, and quoting a banded figure that
// nothing can check is exactly that. A closure is the narrowest scope Go
// offers: it exists for the length of this test and no caller outside it can
// reach it.
func TestTheSeverityFiguresTheseCommentsQuoteStillReproduce(t *testing.T) {
	corpus := AllFixtures()

	// blocking / medium / low, the reduction the withdrawn column used.
	band := func(s config.Severity) int {
		n, _ := s.Normalize()
		switch {
		case n.Rank() >= config.SeverityError.Rank():
			return 2
		case n.Rank() >= config.SeverityWarning.Rank():
			return 1
		default:
			return 0
		}
	}

	type triple struct{ a, i, u int }
	render := func(x triple) string { return fmt.Sprintf("%d/%d/%d", x.a, x.i, x.u) }
	acc := func(x triple) float64 {
		if n := x.a + x.i + x.u; n > 0 {
			return float64(x.a) / float64(n)
		}
		return 0
	}

	bandedOver := func(f Fixture, findings []review.Finding) triple {
		var out triple
		for _, c := range ScoreSeverity(f, findings).Calls {
			switch got, want := band(c.Finding.Sev()), band(c.Defect.WantSeverity); {
			case got > want:
				out.i++
			case got < want:
				out.u++
			default:
				out.a++
			}
		}
		return out
	}
	fullOver := func(f Fixture, findings []review.Finding) triple {
		s := ScoreSeverity(f, findings)
		return triple{s.Accurate, s.Inflated, s.Understated}
	}

	sum := func(over []Fixture, per func(Fixture) triple) triple {
		var out triple
		for _, f := range over {
			x := per(f)
			out.a, out.i, out.u = out.a+x.a, out.i+x.i, out.u+x.u
		}
		return out
	}

	// --- the corpus census ------------------------------------------------
	plants, bands := 0, map[int]int{}
	for _, f := range corpus {
		for _, d := range f.Defects {
			plants++
			bands[band(d.WantSeverity)]++
		}
	}

	// --- the incumbent, from the shipped cache ----------------------------
	//
	// Read through readCRCache and re-parsed rather than through
	// CachedIncumbent, because three of these readings need the RAW bytes
	// altered, the free constant moved, and the parser bug reintroduced, and
	// all four have to come from the same entries or they are not comparable.
	var (
		crLocated                                  = map[config.Severity]int{}
		crBands                                    = map[int]int{}
		crFull, crSwapped, crDemoted               triple
		crBanded, crBandedSwapped, crBandedDemoted triple
		cached                                     int
	)
	for _, f := range corpus {
		c, ok := readCRCache(crCacheDir, f)
		if !ok || c.Raw == "" {
			continue
		}
		cached++

		shipped, err := parseIncumbent([]byte(c.Raw))
		if err != nil {
			t.Fatalf("%s: the shipped review no longer parses: %v", f.Name, err)
		}

		// The free constant moved: our recorded level for the reviewer's own
		// word, with its bytes untouched.
		swapped := append([]review.Finding(nil), shipped...)
		for i := range swapped {
			if strings.EqualFold(swapped[i].RawSeverity, "major") {
				swapped[i].Severity = string(config.SeverityError)
			}
		}

		// The parser bug behind the FIRST retraction: crSeverity demoted every
		// "critical" the CLI printed to our error.
		demoted := append([]review.Finding(nil), shipped...)
		for i := range demoted {
			if strings.EqualFold(demoted[i].RawSeverity, "critical") {
				demoted[i].Severity = string(config.SeverityError)
			}
		}

		for _, call := range ScoreSeverity(f, shipped).Calls {
			crLocated[call.Defect.WantSeverity]++
			crBands[band(call.Defect.WantSeverity)]++
		}

		one := []Fixture{f}
		add := func(dst *triple, x triple) { dst.a, dst.i, dst.u = dst.a+x.a, dst.i+x.i, dst.u+x.u }
		add(&crFull, sum(one, func(f Fixture) triple { return fullOver(f, shipped) }))
		add(&crSwapped, sum(one, func(f Fixture) triple { return fullOver(f, swapped) }))
		add(&crDemoted, sum(one, func(f Fixture) triple { return fullOver(f, demoted) }))
		add(&crBanded, sum(one, func(f Fixture) triple { return bandedOver(f, shipped) }))
		add(&crBandedSwapped, sum(one, func(f Fixture) triple { return bandedOver(f, swapped) }))
		add(&crBandedDemoted, sum(one, func(f Fixture) triple { return bandedOver(f, demoted) }))
	}
	if cached == 0 {
		t.Fatalf("no cached review under %s, so every incumbent figure below is measured over "+
			"nothing and this test would pass on an empty cache", crCacheDir)
	}

	crGraded := crLocated[config.SeverityCritical] + crLocated[config.SeverityError] +
		crLocated[config.SeverityWarning] + crLocated[config.SeverityInfo] + crLocated[config.SeverityNit]

	// --- the INTERVAL instrument, rejected on measurement -------------------
	//
	// Reconstructed here for the same reason the banded verdict is: the comment
	// on SeverityUsage rejects it with a number, and a rejection argued from a
	// figure nothing recomputes is the defect this package keeps correcting.
	// Each raw word is credited against the HULL of the planted levels it is
	// observed on, fitted over the very observations it is then scored against,
	// which is why a perfect score is the definition of the fit rather than a
	// result.
	interval := func(mutate func([]review.Finding)) (accurate, not int) {
		type obs struct {
			word    string
			planted config.Severity
		}
		var seen []obs

		for _, f := range corpus {
			c, ok := readCRCache(crCacheDir, f)
			if !ok || c.Raw == "" {
				continue
			}
			findings, err := parseIncumbent([]byte(c.Raw))
			if err != nil {
				t.Fatalf("%s: %v", f.Name, err)
			}
			mutate(findings)
			for _, call := range ScoreSeverity(f, findings).Calls {
				seen = append(seen, obs{strings.ToLower(call.Finding.RawSeverity), call.Defect.WantSeverity})
			}
		}

		lo, hi := map[string]int{}, map[string]int{}
		for _, x := range seen {
			r := x.planted.Rank()
			if _, ok := lo[x.word]; !ok {
				lo[x.word], hi[x.word] = r, r
			}
			lo[x.word], hi[x.word] = min(lo[x.word], r), max(hi[x.word], r)
		}
		for _, x := range seen {
			if r := x.planted.Rank(); r >= lo[x.word] && r <= hi[x.word] {
				accurate++
			} else {
				not++
			}
		}
		return accurate, not
	}

	fitAcc, fitNot := interval(func([]review.Finding) {})
	demotedAcc, demotedNot := interval(func(fs []review.Finding) {
		for i := range fs {
			if strings.EqualFold(fs[i].RawSeverity, "critical") {
				fs[i].Severity = string(config.SeverityError)
			}
		}
	})
	if fitNot != 0 {
		t.Errorf("the interval fit scores %d observation(s) inaccurate; it is fitted to the "+
			"observations it is scored against, so anything but a perfect record means the "+
			"reconstruction has stopped matching the instrument the comment rejects", fitNot)
	}
	if fitAcc != demotedAcc || fitNot != demotedNot {
		t.Errorf("the interval instrument moves under the parser bug (%d/%d against %d/%d); "+
			"SeverityUsage rejects it for being blind to exactly that, and that rejection no "+
			"longer reproduces", fitAcc, fitNot, demotedAcc, demotedNot)
	}

	// --- the strategies -----------------------------------------------------
	strategy := func(name string) func(Fixture) []review.Finding {
		for _, d := range degenerateReviewers() {
			if d.name == name {
				return d.review
			}
		}
		t.Fatalf("the degenerate table no longer contains %q, so the figure quoted for it cannot "+
			"be reproduced and the sentence quoting it is unchecked", name)
		return nil
	}

	selective := strategy("always critical, and only about defects we already rate error or critical")

	const critOnlyName = "report only the plants we rate critical, and call them critical"
	critOnly := strategy(critOnlyName)

	calibratedBanded := sum(corpus, func(f Fixture) triple { return bandedOver(f, calibratedReview(f)) })
	selectiveBanded := sum(corpus, func(f Fixture) triple { return bandedOver(f, selective(f)) })
	stampCritical := sum(corpus, func(f Fixture) triple {
		return bandedOver(f, oneCommentPerPlant(config.SeverityCritical)(f))
	})
	stampError := sum(corpus, func(f Fixture) triple {
		return bandedOver(f, oneCommentPerPlant(config.SeverityError)(f))
	})
	if stampCritical != stampError {
		t.Errorf("'always critical' and 'always error' no longer band alike (%s against %s); the "+
			"comments quote ONE figure for both", render(stampCritical), render(stampError))
	}

	critOnlyGraded := 0
	for _, f := range corpus {
		critOnlyGraded += len(ScoreSeverity(f, critOnly(f)).Calls)
	}

	// The two claims the vocabulary block's own "what this cannot say" paragraph
	// rests on: what the incumbent's one straddling word lands on, and how many
	// cached reviews could support a within-review reading at all. Both are
	// stated in judge.go as facts about this cache, so both are measured here.
	majorPlants, majorBlocking := 0, 0
	locating, multiLevel := 0, 0

	// The per-level split of what 'major' lands on. It is measured here because
	// the sentence that used to describe it, "every plant a 'major' is credited
	// on is planted ABOVE warning", was false and unchecked, in the same file
	// as this test and 75 lines above a body asserting the opposite.
	majorSpread := map[config.Severity]int{}

	for _, f := range corpus {
		findings, ok := CachedIncumbent(crCacheDir, f)
		if !ok {
			continue
		}
		calls := ScoreSeverity(f, findings).Calls
		if len(calls) == 0 {
			continue
		}
		locating++

		levels := map[config.Severity]bool{}
		for _, c := range calls {
			levels[c.Defect.WantSeverity] = true
			if !strings.EqualFold(c.Finding.RawSeverity, "major") {
				continue
			}
			majorPlants++
			majorSpread[c.Defect.WantSeverity]++
			if band(c.Defect.WantSeverity) == 2 {
				majorBlocking++
			}
		}
		if len(levels) >= 2 {
			multiLevel++
		}
	}

	if len(majorSpread) < 2 {
		t.Fatalf("'major' lands on %d planted level(s) %v, so it no longer straddles and the "+
			"sentences below describe a corpus that moved", len(majorSpread), majorSpread)
	}

	// Everything below measures the tuning corpus rather than the held-out one.
	tuning := sum(Fixtures(), func(f Fixture) triple {
		findings, ok := CachedIncumbent(crCacheDir, f)
		if !ok {
			return triple{}
		}
		return fullOver(f, findings)
	})

	selectiveGraded := selectiveBanded.a + selectiveBanded.i + selectiveBanded.u
	stampGraded := stampCritical.a + stampCritical.i + stampCritical.u

	// --- the sentences ------------------------------------------------------
	//
	// Checked against the prose a reader reads rather than the bytes gofmt
	// produced: a claim that fails only because a line was rewrapped is a false
	// alarm, and a guard that cries wolf gets exemptions until it guards nothing.
	docPath := measurementDocs
	quoted := map[string][]string{
		// The notes carrying these figures live in the measurement documents;
		// the declarations they were attached to point at them.
		docPath: {
			fmt.Sprintf("plants %d defects over %d fixtures and bands them %d blocking, %d medium, %d low",
				plants, len(corpus), bands[2], bands[1], bands[0]),
			fmt.Sprintf("bands a perfect %s", render(selectiveBanded)),
			fmt.Sprintf("over %d of the %d plants", selectiveGraded, plants),
			fmt.Sprintf("the two stampers band %s of %d (B-ACC %.3f)", render(stampCritical), stampGraded, acc(stampCritical)),
			fmt.Sprintf("calibrated reviewer's %s", render(calibratedBanded)),
			fmt.Sprintf("it locates %d critical, %d error and %d warning",
				crLocated[config.SeverityCritical], crLocated[config.SeverityError], crLocated[config.SeverityWarning]),
			fmt.Sprintf("so %d of the %d are blocking", crBands[2], crGraded),
			fmt.Sprintf("bands %s over them", render(crBanded)),
			fmt.Sprintf("buggy parser %s, fixed parser %s", render(crBandedDemoted), render(crBanded)),
			fmt.Sprintf("moves the incumbent's triple from %s to %s", render(crFull), render(crDemoted)),
			fmt.Sprintf("B-ACC %.3f either way (%s to %s)", acc(crBanded), render(crBanded), render(crBandedSwapped)),
			fmt.Sprintf("FULL-RESOLUTION triple from %s to %s", render(crFull), render(crSwapped)),
			fmt.Sprintf("the incumbent scores %d accurate / %d not, and %d/%d again with the",
				fitAcc, fitNot, demotedAcc, demotedNot),
			fmt.Sprintf("triple from %s to %s", render(crFull), render(crSwapped)),
			fmt.Sprintf("is credited on %d plants, %d of them blocking", majorPlants, majorBlocking),
			fmt.Sprintf("Of the %d cached reviews that locate anything, exactly one locates defects at two or more distinct planted levels",
				locating),
		},
		"score.go": {
			fmt.Sprintf("%d/%d against %d/%d", critOnlyGraded, plants, plants, plants),
		},
		"severity_test.go": {
			fmt.Sprintf("%d of the %d plants sit in the blocking band", bands[2], plants),
			fmt.Sprintf("they band %s of %d", render(stampCritical), plants),
			fmt.Sprintf("banded a perfect %s over those %d", render(selectiveBanded), selectiveGraded),

			// The straddle, per level. This claim exists because the sentence it
			// replaces, that every plant a 'major' lands on is above warning,
			// was false, sat in this file, and was not among the figures
			// anything read back out of the corpus.
			fmt.Sprintf("credited on %d plants, landing %d critical, %d error and %d warning",
				majorPlants, majorSpread[config.SeverityCritical],
				majorSpread[config.SeverityError], majorSpread[config.SeverityWarning]),
			fmt.Sprintf("the full-resolution triple moves %s to %s when the constant is swapped",
				render(crFull), render(crSwapped)),
		},
	}
	for file, claims := range quoted {
		// One file carries two independent groups of claims, so the key names
		// the group and the path is taken from in front of the "?".
		prose := quotedProse(t, strings.SplitN(file, "?", 2)[0])
		for _, claim := range claims {
			if !strings.Contains(prose, claim) {
				t.Errorf("%s no longer says %q. That figure reproduces from this corpus today, and a "+
					"sentence quoting any other number is evidence nobody can check — which is the "+
					"defect this package has now retracted three times", file, claim)
			}
		}
	}

	// A "SEE X" BESIDE A PINNED FIGURE HAS TO NAME THE GUARD that PINS IT.
	//
	// The note behind it is in docs/harness-notes.md#self.
	const self = "TestTheSeverityFiguresTheseCommentsQuoteStillReproduce"
	guards := figurePinningGuards(t)
	if !slices.Contains(guards, self) {
		t.Fatalf("this test is not among the derived figure-pinning guards %v, so the rule below "+
			"is checking citations against a set that does not contain the right answer", guards)
	}

	for file, claims := range quoted {
		for _, path := range sourcePaths(strings.SplitN(file, "?", 2)[0]) {
			for _, group := range commentGroups(t, path) {
				var quotes []string
				for _, claim := range claims {
					if strings.Contains(group, claim) {
						quotes = append(quotes, claim)
					}
				}
				if len(quotes) == 0 {
					continue
				}

				for _, g := range guards {
					if g == self || !strings.Contains(group, g) {
						continue
					}
					t.Errorf("%s: a paragraph quoting %v cites %s, which does not read those figures. "+
						"The guard that does is %s. A 'see X' pointing at a test that passes when the "+
						"sentence is wrong is worse than no citation — it tells the next reader the "+
						"number is covered, which is how the banded column kept its place on the page",
						path, quotes, g, self)
				}
			}
		}
	}

	// The two published constants carry the same numbers and are checked as
	// VALUES, because they are what a reader of a report meets.
	for _, want := range []string{
		fmt.Sprintf("%d plants over %d fixtures, banded %d blocking, %d medium, %d low",
			plants, len(corpus), bands[2], bands[1], bands[0]),
		fmt.Sprintf("banded a perfect %s over %d of the %d plants", render(selectiveBanded), selectiveGraded, plants),
		fmt.Sprintf("calibrated reviewer's %s", render(calibratedBanded)),
		fmt.Sprintf("both banded %s on the incumbent", render(crBanded)),
		fmt.Sprintf("full resolution moved %s to %s", render(crFull), render(crDemoted)),
	} {
		if !strings.Contains(NoCrossToolSeverityScore, want) {
			t.Errorf("NoCrossToolSeverityScore no longer says %q; it is the sentence printed under "+
				"every severity table, so a stale figure there is one a reader is handed", want)
		}
	}
	if want := fmt.Sprintf("covering %d plants of %d", critOnlyGraded, plants); !strings.Contains(SeverityColumnLegend, want) {
		t.Errorf("SeverityColumnLegend no longer says %q", want)
	}

	// The degenerate table's own prose is a VALUE, not a comment, and it is
	// printed in this test's failure messages, so it is checked against the
	// table rather than against the file.
	for _, d := range degenerateReviewers() {
		if d.name != critOnlyName {
			continue
		}
		want := fmt.Sprintf("over four of the corpus's %s plants", strings.ToLower(numberWord(plants)))
		if !strings.Contains(d.why, want) {
			t.Errorf("the %q row no longer says %q; that sentence is printed verbatim whenever this "+
				"strategy defeats a metric, so a stale number in it is one a failure message hands "+
				"to whoever is debugging", critOnlyName, want)
		}
	}

	if multiLevel != 1 {
		t.Errorf("%d cached review(s) locate defects at two or more distinct planted levels, not 1. "+
			"Both the block's own limits paragraph and the argument against an ordinal instrument "+
			"say 'exactly one'; a corpus that moved makes a within-review reading newly possible and "+
			"that decision has to be revisited deliberately", multiLevel)
	}

	t.Logf("corpus %d plants over %d fixtures (%d blocking, %d medium, %d low); incumbent located %d "+
		"(%d blocking) scoring %s full / %s banded; major->error %s full / %s banded; demoted parser "+
		"%s full / %s banded; stampers %s banded, selective %s banded over %d, calibrated %s banded; "+
		"tuning half %s full",
		plants, len(corpus), bands[2], bands[1], bands[0], crGraded, crBands[2],
		render(crFull), render(crBanded), render(crSwapped), render(crBandedSwapped),
		render(crDemoted), render(crBandedDemoted),
		render(stampCritical), render(selectiveBanded), selectiveGraded, render(calibratedBanded),
		render(tuning))

	// docs/findings.md quotes the incumbent's two full-resolution readings with
	// their accuracies, and the per-level split of the one word that straddles.
	// It is checked here rather than left to a reader because the paragraph it
	// sits in is a RETRACTION, and the previous retraction in that same spot was
	// itself argued from figures that did not reproduce. The straddle line is
	// checked for the narrower reason that its predecessor, that the corpus
	// could not check the word's placement at all, was an overstatement of what
	// nothing here measured.
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "findings.md"))
	if err != nil {
		t.Fatalf("reading docs/findings.md: %v", err)
	}
	for _, want := range []string{
		fmt.Sprintf("%d accurate / %d inflated / %d\nunderstated on the tuning fixtures and %s over all of them: O-ACC %.2f and\n%.2f",
			tuning.a, tuning.i, tuning.u, render(crFull), acc(tuning), acc(crFull)),
		fmt.Sprintf("credited\non %d plants here, landing %d `critical`, %d `error` and %d `warning`",
			majorPlants, majorSpread[config.SeverityCritical],
			majorSpread[config.SeverityError], majorSpread[config.SeverityWarning]),
	} {
		if !strings.Contains(string(doc), want) {
			t.Errorf("docs/findings.md no longer says %q. The retraction there is argued from these "+
				"two readings, and a retraction argued from an unreproducible measurement is the "+
				"same defect one level up", want)
		}
	}

	// docs/measurement.md's Rule 6 IS the retraction, and it ended a paragraph
	// claiming "every figure in this rule is read back out of the corpus by
	// TestTheSeverityFiguresTheseCommentsQuoteStillReproduce".
	//
	// That SENTENCE was FALSE. This test read four source files and
	// docs/findings.md and never opened docs/measurement.md. Changing "stampers
	// band 12/17/0 of 29" to 13/17/0, "it bands 10/0/4 over them" to 11/0/4 and
	// "buggy 10/0/4, fixed 10/0/4" to 9/0/4 left the whole default suite green.
	// It is the same defect the citation rule above was written for, a "see X"
	// naming a guard that does not check the sentence, one level out, in the
	// document a reader is handed, and stated there as a positive
	// guarantee rather than a pointer.
	calibratedFull := sum(corpus, func(f Fixture) triple { return fullOver(f, calibratedReview(f)) })
	for _, want := range []string{
		fmt.Sprintf("plants %d defects over %d fixtures and bands them %d blocking, %d medium, %d low",
			plants, len(corpus), bands[2], bands[1], bands[0]),
		fmt.Sprintf("banded %s, a perfect record, an exact tie with a calibrated reviewer's %s, over %d of the %d plants",
			render(selectiveBanded), render(calibratedBanded), selectiveGraded, plants),
		fmt.Sprintf("the stampers band %s of %d (B-ACC %.3f)", render(stampCritical), plants, acc(stampCritical)),
		fmt.Sprintf("it locates %d critical, %d error and %d warning, so %d of its %d are blocking, and it bands %s over them",
			crLocated[config.SeverityCritical], crLocated[config.SeverityError],
			crLocated[config.SeverityWarning], crBands[2], crGraded, render(crBanded)),
		fmt.Sprintf("buggy %s, fixed %s", render(crBandedDemoted), render(crBanded)),
		fmt.Sprintf("moves the incumbent's triple from %s to %s", render(crFull), render(crDemoted)),
		fmt.Sprintf("banded figure at B-ACC %.3f either way (%s to %s) and moves the full-resolution triple from %s to %s",
			acc(crBanded), render(crBanded), render(crBandedSwapped), render(crFull), render(crSwapped)),
		fmt.Sprintf("O-ACC over the shipped cache is %.3f, against %.3f for a calibrated reviewer",
			acc(crFull), acc(calibratedFull)),
	} {
		if !strings.Contains(docProse(t, filepath.Join("..", "..", "docs", "measurement.md")), want) {
			t.Errorf("docs/measurement.md no longer says %q. Rule 6 there ends by claiming every "+
				"figure in it is read back out of the corpus by this test, so a figure it states "+
				"that nothing recomputes makes that sentence the thing it warns about", want)
		}
	}
}

// commentProse is a source file's COMMENTS as continuous prose: the leading
// slashes stripped and every run of whitespace collapsed to one space.
//
// The note behind it is in docs/harness-notes.md#commentprose.
func commentProse(t *testing.T, name string) string {
	t.Helper()
	return strings.Join(commentGroups(t, name), " \x00 ")
}

// docProse is a markdown file as continuous prose: emphasis and code markers
// removed and every run of whitespace collapsed to one space.
//
// The same reasoning as commentProse. A figure in these documents is a sentence,
// and the author's line wrapping and their choice to write `critical` or
// **12/0/0** are formatting. A guard that goes red when a paragraph is re-flowed
// or a word is emphasised is a guard that earns exemptions until it guards
// nothing.
func docProse(t *testing.T, path string) string {
	t.Helper()

	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return strings.Join(strings.Fields(
		strings.NewReplacer("`", "", "*", "", "_", "").Replace(string(src))), " ")
}

// commentGroups is a source file's comments as one collapsed string PER
// CONTIGUOUS RUN of comment lines, a doc comment, a block above a case, a
// trailing note.
//
// The group is the unit a claim and its "see X" share. Checking a citation
// against the whole file would let a guard named anywhere in a 5000-line file
// vouch for a figure quoted anywhere else in it, which is not a check.
func commentGroups(t *testing.T, name string) []string {
	t.Helper()

	src, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}

	var (
		out     []string
		current strings.Builder
	)
	flush := func() {
		if g := strings.Join(strings.Fields(current.String()), " "); g != "" {
			out = append(out, g)
		}
		current.Reset()
	}

	for _, line := range strings.Split(string(src), "\n") {
		trimmed := strings.TrimSpace(line)
		if after, ok := strings.CutPrefix(trimmed, "//"); ok {
			current.WriteString(after)
			current.WriteString(" ")
			continue
		}
		// A code line ends the run, so two comment groups either side of a
		// declaration cannot be read as one sentence.
		flush()
	}
	flush()

	return out
}

// figurePinningGuards names every test in this package that reads quoted figures
// back out of the corpus, derived from the package rather than listed.
//
// Derived because the defect it serves is a citation naming the WRONG one of
// them, and a hand-written list of guards would go stale in exactly the way the
// citations did.
func figurePinningGuards(t *testing.T) []string {
	t.Helper()

	const mark = "TheseCommentsQuoteStillReproduce"

	var out []string
	for _, file := range packageAST(t) {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || !strings.HasPrefix(fn.Name.Name, "Test") {
				continue
			}
			if strings.Contains(fn.Name.Name, mark) {
				out = append(out, fn.Name.Name)
			}
		}
	}

	if len(out) < 2 {
		t.Fatalf("found %d figure-pinning guard(s) %v; the rule below is about a citation naming "+
			"the wrong one of them, which needs at least two to be a real question", len(out), out)
	}

	sort.Strings(out)
	return out
}
