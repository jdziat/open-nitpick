package review

import (
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/bundle"
	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/diff"
)

func budgetConfig(maxSpend float64) *config.Config {
	cfg := config.Defaults()
	cfg.Review.Budget = config.Budget{
		MaxSpend: maxSpend,
		Prices:   config.BudgetPrices{Input: 1.0, Output: 4.0},
	}
	return cfg
}

// file builds a changed file with n added lines, m of them branchy.
func file(path string, added, branchy int) *diff.File {
	h := diff.Hunk{NewStart: 1, NewLines: added}
	for i := 0; i < added; i++ {
		content := "x := 1"
		if i < branchy {
			content = "if err != nil { return err }"
		}
		h.Lines = append(h.Lines, diff.Line{Kind: diff.LineAdded, Content: content, NewLine: i + 1})
	}
	return &diff.File{Path: path, Kind: diff.ChangeModified, Hunks: []diff.Hunk{h}}
}

func planOf(entries ...bundle.Entry) *bundle.Plan {
	var total int
	for _, e := range entries {
		total += e.Tokens
	}
	return &bundle.Plan{Batches: []bundle.Batch{{Entries: entries, Tokens: total}}}
}

func entry(f *diff.File, tokens int) bundle.Entry {
	return bundle.Entry{File: f, Tokens: tokens}
}

// A ceiling the plan already fits under changes nothing, and the fit says so.
func TestAPlanUnderTheCeilingIsNotTrimmed(t *testing.T) {
	a, b := file("a.go", 10, 5), file("b.go", 10, 0)
	plan := planOf(entry(a, 1000), entry(b, 1000))

	keep, fit := FitToBudget(budgetConfig(1.00), plan, Rank([]*diff.File{a, b}), 1.00)

	if fit.Trimmed() {
		t.Errorf("trimmed a plan that fits: dropped %v", fit.Dropped)
	}
	if len(keep) != 2 {
		t.Errorf("kept %v, want both files", keep)
	}
	// 2000 prompt at $1/M plus 500 completion at $4/M is $0.004.
	if got := fit.Before.Dollars; got < 0.0039 || got > 0.0041 {
		t.Errorf("estimate = %f, want about 0.004", got)
	}
}

// The ceiling drops the lowest-ranked files and keeps the highest.
func TestTheCeilingKeepsTheHighestRankedFiles(t *testing.T) {
	risky := file("internal/auth/session.go", 40, 20)
	plain := file("internal/util/strings.go", 40, 0)
	test := file("internal/util/strings_test.go", 40, 0)

	files := []*diff.File{risky, plain, test}
	plan := planOf(entry(risky, 100_000), entry(plain, 100_000), entry(test, 100_000))

	// One file at 100k prompt tokens plus 25k completion is $0.20. Two is
	// $0.40. A ceiling of $0.25 pays for one.
	keep, fit := FitToBudget(budgetConfig(0.25), plan, Rank(files), 0.25)

	if len(keep) != 1 || keep[0] != "internal/auth/session.go" {
		t.Fatalf("kept %v, want the auth file alone", keep)
	}
	if len(fit.Dropped) != 2 {
		t.Errorf("dropped %v, want the other two", fit.Dropped)
	}
	if fit.After.Dollars > 0.25 {
		t.Errorf("trimmed plan costs %f, over the %f ceiling", fit.After.Dollars, fit.Ceiling)
	}
	if fit.Forced {
		t.Error("reported forced when the kept file fits")
	}
}

// min_files reviews the top file even when nothing fits, and says the ceiling
// was exceeded rather than reviewing nothing in silence.
func TestMinFilesExceedsTheCeilingAndReportsIt(t *testing.T) {
	huge := file("internal/auth/session.go", 40, 20)
	plan := planOf(entry(huge, 1_000_000))

	cfg := budgetConfig(0.01)
	cfg.Review.Budget.MinFiles = 1

	keep, fit := FitToBudget(cfg, plan, Rank([]*diff.File{huge}), 0.01)

	if len(keep) != 1 {
		t.Fatalf("kept %v, want the one file min_files guarantees", keep)
	}
	if !fit.Forced {
		t.Errorf("Forced = false, but the plan costs %f against a %f ceiling", fit.After.Dollars, fit.Ceiling)
	}
}

// Zero min_files reviews nothing rather than overspending, which the caller
// must report as an empty review.
func TestWithoutMinFilesNothingIsReviewedWhenNothingFits(t *testing.T) {
	huge := file("a.go", 40, 20)
	plan := planOf(entry(huge, 1_000_000))

	keep, fit := FitToBudget(budgetConfig(0.01), plan, Rank([]*diff.File{huge}), 0.01)

	if len(keep) != 0 {
		t.Errorf("kept %v, want nothing", keep)
	}
	if len(fit.Dropped) != 1 || fit.Forced {
		t.Errorf("fit = %+v, want one dropped file and no forcing", fit)
	}
}

// An ensemble multiplies the cost, so the same ceiling pays for fewer files.
func TestAnEnsembleSpendsTheCeilingFaster(t *testing.T) {
	a, b := file("a.go", 10, 5), file("b.go", 10, 5)
	plan := planOf(entry(a, 100_000), entry(b, 100_000))

	solo := budgetConfig(0.45)
	keepSolo, _ := FitToBudget(solo, plan, Rank([]*diff.File{a, b}), 0.45)

	pair := budgetConfig(0.45)
	pair.Models.Ensemble = []config.ModelSpec{{Model: "second"}}
	keepPair, fit := FitToBudget(pair, plan, Rank([]*diff.File{a, b}), 0.45)

	if len(keepSolo) != 2 {
		t.Errorf("one reviewer kept %v, want both files", keepSolo)
	}
	if len(keepPair) != 1 {
		t.Errorf("two reviewers kept %v, want one file", keepPair)
	}
	if fit.Before.Reviewers != 2 {
		t.Errorf("Reviewers = %d, want 2", fit.Before.Reviewers)
	}
}

// The overhead multiplier is applied, so a triage allowance shrinks what fits.
func TestOverheadCountsAgainstTheCeiling(t *testing.T) {
	a := file("a.go", 10, 5)
	plan := planOf(entry(a, 100_000))

	cfg := budgetConfig(0.25)
	cfg.Review.Budget.Overhead = 2

	_, fit := FitToBudget(cfg, plan, Rank([]*diff.File{a}), 0.25)

	if fit.Before.Dollars < 0.39 || fit.Before.Dollars > 0.41 {
		t.Errorf("estimate = %f, want about 0.40 with the overhead doubled", fit.Before.Dollars)
	}
	if len(fit.Dropped) != 1 {
		t.Errorf("dropped %v, want the file the overhead priced out", fit.Dropped)
	}
}

// A budget that is off leaves every file in place and costs nothing to decide.
func TestNoCeilingKeepsEverything(t *testing.T) {
	a, b := file("a.go", 10, 5), file("b.go", 10, 0)
	plan := planOf(entry(a, 1_000_000), entry(b, 1_000_000))

	cfg := config.Defaults()
	keep, fit := FitToBudget(cfg, plan, Rank([]*diff.File{a, b}), 0)

	if len(keep) != 2 || fit.Trimmed() {
		t.Errorf("keep=%v fit=%+v, want everything kept", keep, fit)
	}
}

// The ranking is the part a reader has to be able to argue with, so it is
// asserted directly rather than only through the trimming above.
func TestRankingPutsRiskyControlFlowAboveMechanicalChanges(t *testing.T) {
	files := []*diff.File{
		file("internal/util/strings_test.go", 30, 10),
		file("internal/auth/token.go", 30, 15),
		file("docs/notes.md", 30, 0),
		file("internal/util/strings.go", 30, 5),
	}
	ranked := Rank(files)

	var order []string
	for _, c := range ranked {
		order = append(order, c.Path)
	}
	want := []string{
		"internal/auth/token.go",        // sensitive path, densest control flow
		"internal/util/strings.go",      // ordinary source with some branching
		"internal/util/strings_test.go", // same density, halved for being a test
		"docs/notes.md",                 // prose, no control flow
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("ranked %v, want %v", order, want)
		}
	}
	if !strings.Contains(ranked[0].Reasons(), "sensitive path") {
		t.Errorf("reasons = %q, want the sensitive path named", ranked[0].Reasons())
	}
}

// The score must not be dominated by size alone: a huge mechanical change
// should not outrank a small dense one in a sensitive place.
func TestSizeAloneDoesNotWinTheRanking(t *testing.T) {
	generated := file("internal/api/models.go", 4000, 0)
	small := file("internal/auth/verify.go", 12, 8)

	ranked := Rank([]*diff.File{generated, small})
	if ranked[0].Path != "internal/auth/verify.go" {
		t.Errorf("ranked %v first, want the small sensitive change", ranked[0].Path)
	}
}

// Ranking is stable: the same input produces the same order, so two runs on
// the same diff review the same files.
func TestRankingIsStable(t *testing.T) {
	files := []*diff.File{file("b.go", 10, 2), file("a.go", 10, 2), file("c.go", 10, 2)}
	first, second := Rank(files), Rank(files)

	for i := range first {
		if first[i].Path != second[i].Path {
			t.Fatalf("unstable ranking: %v then %v", first, second)
		}
	}
	if first[0].Path != "a.go" {
		t.Errorf("tie broken to %q, want the lowest path", first[0].Path)
	}
}

// The summary comment has to say a ceiling changed the review. A reader who
// sees no findings for a file must not conclude the file is clean.
func TestTheSummarySaysWhenACeilingTrimmedTheReview(t *testing.T) {
	report := &Report{Budget: &Fit{
		Kept:    []string{"a.go"},
		Dropped: []string{"b.go", "c.go"},
		Before:  Estimate{Dollars: 2.40},
		After:   Estimate{Dollars: 0.80},
		Ceiling: 1.00,
	}}

	note := budgetNote(report)
	for _, want := range []string{"spending ceiling", "$2.40", "$1.00", "$0.80", "nobody looked"} {
		if !strings.Contains(note, want) {
			t.Errorf("note is missing %q:\n%s", want, note)
		}
	}

	if got := budgetNote(&Report{}); got != "" {
		t.Errorf("a run with no ceiling wrote %q", got)
	}
	if got := budgetNote(&Report{Budget: &Fit{Kept: []string{"a.go"}}}); got != "" {
		t.Errorf("a ceiling that trimmed nothing wrote %q", got)
	}
}

// min_files is announced too, because the run then costs more than the ceiling
// the operator set.
func TestTheSummarySaysWhenMinFilesExceedsTheCeiling(t *testing.T) {
	note := budgetNote(&Report{Budget: &Fit{
		Kept: []string{"a.go"}, Dropped: []string{"b.go"},
		Before: Estimate{Dollars: 9}, After: Estimate{Dollars: 4}, Ceiling: 1, Forced: true,
	}})
	if !strings.Contains(note, "min_files") {
		t.Errorf("forced run does not name min_files:\n%s", note)
	}
}
