package practices

import (
	"slices"
	"strings"
	"testing"

	"github.com/nitpick-oss/nitpick/internal/bundle"
	"github.com/nitpick-oss/nitpick/internal/config"
	"github.com/nitpick-oss/nitpick/internal/diff"
)

// added is a change that adds n lines to a file, so a task has churn to be ranked by.
func added(path string, n int) *diff.File {
	lines := make([]diff.Line, n)
	for i := range lines {
		lines[i] = diff.Line{Kind: diff.LineAdded, NewLine: i + 1}
	}
	return &diff.File{Path: path, Kind: diff.ChangeModified, Hunks: []diff.Hunk{{Lines: lines}}}
}

func taskIDs(tasks []DesignTask) []string {
	var ids []string
	for _, task := range tasks {
		ids = append(ids, task.ID)
	}
	return ids
}

func TestDesignTasksOverTheCapKeepTheMostChangedAndNameTheRest(t *testing.T) {
	files, inventory := designPlanningFixture(t)
	changes := diff.Files{added("store/read.go", 3), added("service/service.go", 40), added("client/client.go", 12)}
	design := PlanDesign(t.Context(), inventory, files, []string{"store/read.go", "service/service.go", "client/client.go"})
	if len(design.Tasks) != 3 {
		t.Fatalf("setup: want 3 tasks, got %v", taskIDs(design.Tasks))
	}
	cfg := config.Defaults()
	cfg.Review.MaxDesignTasks = 2
	packed := PackDesign(t.Context(), cfg, design, files, changes, bundle.Reserve{})

	if got, want := taskIDs(packed.Design.Tasks), []string{"package:example.com/app/client", "package:example.com/app/service"}; !slices.Equal(got, want) {
		t.Errorf("kept tasks = %v, want the two with the most added lines %v", got, want)
	}
	if len(packed.Design.Deferred) != 1 || packed.Design.Deferred[0].ID != "package:example.com/app/store" {
		t.Fatalf("deferred = %+v, want the store task alone", packed.Design.Deferred)
	}
	if d := packed.Design.Deferred[0]; d.Rank != 3 || d.Of != 3 {
		t.Errorf("deferred rank = %d of %d, want 3 of 3", d.Rank, d.Of)
	}
	if len(packed.Plan.Batches) != 2 {
		t.Errorf("batches = %d, want one per kept task", len(packed.Plan.Batches))
	}
	for _, task := range packed.Design.Tasks {
		if len(task.Omitted) != 0 {
			t.Errorf("a kept task carries an omission, which would mark the check partial: %+v", task)
		}
	}
	joined := strings.Join(packed.Design.Limitations, "\n")
	if !strings.Contains(joined, "review.max_design_tasks") || !strings.Contains(joined, "1 of 3") || !strings.Contains(joined, "store") {
		t.Errorf("the limitations do not say what was left out and why:\n%s", joined)
	}
}

func TestDesignTasksAtOrUnderTheCapAreAllKeptAndSayNothing(t *testing.T) {
	files, inventory := designPlanningFixture(t)
	design := PlanDesign(t.Context(), inventory, files, []string{"store/read.go", "service/service.go"})
	for _, limit := range []int{0, 2, 5} {
		cfg := config.Defaults()
		cfg.Review.MaxDesignTasks = limit
		packed := PackDesign(t.Context(), cfg, design, files, nil, bundle.Reserve{})
		if len(packed.Design.Tasks) != 2 || len(packed.Design.Deferred) != 0 {
			t.Errorf("cap %d: kept %d, deferred %d, want 2 and 0", limit, len(packed.Design.Tasks), len(packed.Design.Deferred))
		}
		if strings.Contains(strings.Join(packed.Design.Limitations, "\n"), "max_design_tasks") {
			t.Errorf("cap %d: a limitation names a cut that did not happen", limit)
		}
	}
}

func TestDesignTaskRankingBreaksTiesBySourcesThenID(t *testing.T) {
	files, inventory := designPlanningFixture(t)
	design := PlanDesign(t.Context(), inventory, files, []string{"store/read.go", "service/service.go", "client/client.go"})
	cfg := config.Defaults()
	cfg.Review.MaxDesignTasks = 1
	// With no churn the order falls to source count (the store task holds two files, the
	// others one) and then to the ID, so the first of client and service wins.
	packed := PackDesign(t.Context(), cfg, design, files, nil, bundle.Reserve{})
	if got := taskIDs(packed.Design.Tasks); !slices.Equal(got, []string{"package:example.com/app/client"}) {
		t.Errorf("kept %v, want the client task", got)
	}
	again := PackDesign(t.Context(), cfg, design, files, nil, bundle.Reserve{})
	if !slices.Equal(taskIDs(packed.Design.Tasks), taskIDs(again.Design.Tasks)) {
		t.Error("the same input kept different tasks twice")
	}
}

// ranked builds the packing state capDesignTasks reads, from tasks written by hand,
// so each ranking rule is tried with the others held equal.
func ranked(tasks ...DesignTask) *DesignPacking {
	return &DesignPacking{Design: DesignPlan{Tasks: tasks}, Plan: &bundle.Plan{}}
}

func task(id string, sources int, obligations int) DesignTask {
	t := DesignTask{ID: id, Source: Target{Kind: FileTarget, ID: id + ".go"}}
	for i := range sources {
		t.Sources = append(t.Sources, Target{Kind: FileTarget, ID: id + string(rune('a'+i)) + ".go"})
	}
	for range obligations {
		t.Interactions = append(t.Interactions, DesignInteraction{})
	}
	return t
}

func TestMoreChurnOutranksCallerObligationsAndObligationsBreakAChurnTie(t *testing.T) {
	busy := task("busy", 1, 0)
	bound := task("bound", 1, 2)
	out := ranked(busy, bound)
	capDesignTasks(out, 1, diff.Files{added("busya.go", 500)})
	if got := taskIDs(out.Design.Tasks); !slices.Equal(got, []string{"busy"}) {
		t.Errorf("kept %v: churn ranks before obligations, so the busy task stays", got)
	}
	// With the churn equal, the obligations decide, whatever the IDs say.
	out = ranked(task("aaa", 1, 0), task("zzz", 1, 2))
	capDesignTasks(out, 1, diff.Files{added("aaaa.go", 4), added("zzza.go", 4)})
	if got := taskIDs(out.Design.Tasks); !slices.Equal(got, []string{"zzz"}) {
		t.Errorf("kept %v: equal churn falls to the task carrying obligations", got)
	}
}

func TestADesignTaskWithFewerSourcesRanksFirstWhenNothingElseDiffers(t *testing.T) {
	out := ranked(task("aaa-wide", 3, 0), task("zzz-narrow", 1, 0))
	capDesignTasks(out, 1, nil)
	if got := taskIDs(out.Design.Tasks); !slices.Equal(got, []string{"zzz-narrow"}) {
		t.Errorf("kept %v: with equal obligations and churn the task with fewer sources wins, whatever its ID", got)
	}
}

func TestAnAddedLineIsWhatChurnCountsAndAContextLineIsNot(t *testing.T) {
	quiet := task("quiet", 1, 0)
	loud := task("loud", 1, 0)
	file := func(path string, kind diff.LineKind, n int) *diff.File {
		lines := make([]diff.Line, n)
		for i := range lines {
			lines[i] = diff.Line{Kind: kind}
		}
		return &diff.File{Path: path, Hunks: []diff.Hunk{{Lines: lines}}}
	}
	out := ranked(quiet, loud)
	// quiet has the larger hunk, but its lines are unchanged context and removals.
	capDesignTasks(out, 1, diff.Files{file("quiet"+"a.go", diff.LineContext, 30), file("louda.go", diff.LineAdded, 2)})
	if got := taskIDs(out.Design.Tasks); !slices.Equal(got, []string{"loud"}) {
		t.Errorf("kept %v: only added lines are churn", got)
	}
}

func TestADeferredTaskIsListedAsSkippedWithItsPlaceInTheRanking(t *testing.T) {
	out := ranked(task("first", 1, 0), task("second", 1, 0), task("third", 1, 0))
	capDesignTasks(out, 1, nil)
	var reasons []string
	for _, skip := range out.Plan.Skipped {
		reasons = append(reasons, skip.Reason)
	}
	want := []string{
		"second: left out by review.max_design_tasks (rank 2 of 3)",
		"third: left out by review.max_design_tasks (rank 3 of 3)",
	}
	if !slices.Equal(reasons, want) {
		t.Errorf("skipped = %q, want %q", reasons, want)
	}
}

func TestSlopOnlyTasksAreNeitherCountedNorCut(t *testing.T) {
	slop := task("slop", 1, 0)
	slop.SlopOnly = true
	out := ranked(task("one", 1, 0), slop, task("two", 1, 0))
	capDesignTasks(out, 2, nil)
	if got := taskIDs(out.Design.Tasks); !slices.Equal(got, []string{"one", "slop", "two"}) {
		t.Errorf("kept %v: two design tasks fit a cap of two however many slop-only tasks sit beside them", got)
	}
	capDesignTasks(out, 1, nil)
	if got := taskIDs(out.Design.Tasks); !slices.Equal(got, []string{"one", "slop"}) {
		t.Errorf("kept %v: the cap cuts design tasks and leaves the slop-only task", got)
	}
}

func TestMoreAddedLinesRankFirstWhenObligationsAndSourcesAreEqual(t *testing.T) {
	// The IDs run against the churn, so only the churn can put "zzz" first.
	out := ranked(task("aaa", 1, 0), task("zzz", 1, 0))
	capDesignTasks(out, 1, diff.Files{added("aaaa.go", 1), added("zzza.go", 5)})
	if got := taskIDs(out.Design.Tasks); !slices.Equal(got, []string{"zzz"}) {
		t.Errorf("kept %v, want the task with five added lines over the one with one", got)
	}
}

// declaration is a task about one declaration: its focus span is the only part of
// the file it is about.
func declaration(id, path string, start, end int) DesignTask {
	source := Target{Kind: FileTarget, ID: path}
	return DesignTask{ID: id, Source: source, Sources: []Target{source},
		Focus: []ContextSpan{{Path: path, SourceSpan: bundle.SourceSpan{Start: start, End: end}}}}
}

// hunk is a change written as the diff reads it: "+" adds a line, "-" removes one,
// and anything else is kept. Line numbers are the new file's, counted from one.
func hunk(path string, marks string) *diff.File {
	var lines []diff.Line
	newLine := 0
	for _, mark := range marks {
		switch mark {
		case '+':
			newLine++
			lines = append(lines, diff.Line{Kind: diff.LineAdded, NewLine: newLine})
		case '-':
			lines = append(lines, diff.Line{Kind: diff.LineRemoved})
		default:
			newLine++
			lines = append(lines, diff.Line{Kind: diff.LineContext, NewLine: newLine})
		}
	}
	return &diff.File{Path: path, Kind: diff.ChangeModified, Hunks: []diff.Hunk{{Lines: lines}}}
}

func TestADeclarationTaskCountsOnlyTheChangeInsideItsOwnSpan(t *testing.T) {
	// One busy file, two declarations: lines 1-5 are untouched and lines 6-10 are rewritten.
	untouched := declaration("declaration:f.go:0", "f.go", 1, 5)
	rewritten := declaration("declaration:f.go:1", "f.go", 6, 10)
	// A second file with a small change, so a whole-file count would rank both f.go
	// declarations above it and the untouched one would be kept.
	small := declaration("declaration:g.go:0", "g.go", 1, 5)
	out := ranked(untouched, rewritten, small)
	capDesignTasks(out, 2, diff.Files{hunk("f.go", "     +++++"), hunk("g.go", "+")})
	if got := taskIDs(out.Design.Tasks); !slices.Equal(got, []string{"declaration:f.go:1", "declaration:g.go:0"}) {
		t.Errorf("kept %v: the declaration the change never reached must rank below one it touched", got)
	}
}

func TestARemovedLineCountsForTheSpanOfTheLineThatFollowsIt(t *testing.T) {
	first := declaration("declaration:f.go:0", "f.go", 1, 3)
	second := declaration("declaration:f.go:1", "f.go", 4, 6)
	out := ranked(first, second)
	// Three removals sit before new line 4, so they belong to the second declaration,
	// and one added line sits in the first.
	capDesignTasks(out, 1, diff.Files{hunk("f.go", "+  ---   ")})
	if got := taskIDs(out.Design.Tasks); !slices.Equal(got, []string{"declaration:f.go:1"}) {
		t.Errorf("kept %v: three removed lines before line 4 outweigh one added line in lines 1 to 3", got)
	}
}

func TestATaskWithNoSpanCountsEveryChangedLineInItsSources(t *testing.T) {
	whole := task("whole", 1, 0)
	elsewhere := declaration("declaration:other.go:0", "other.go", 1, 5)
	out := ranked(whole, elsewhere)
	capDesignTasks(out, 1, diff.Files{added("wholea.go", 6), hunk("other.go", "++")})
	if got := taskIDs(out.Design.Tasks); !slices.Equal(got, []string{"whole"}) {
		t.Errorf("kept %v: a task about whole files counts the six lines changed in its file", got)
	}
}

func TestAChangeBeforeADeclarationDoesNotCountForIt(t *testing.T) {
	// Lines 1-4 are rewritten and belong to the first declaration; the second starts
	// at line 5 and the change never reaches it. A second file has a small change.
	first := declaration("declaration:f.go:0", "f.go", 1, 4)
	second := declaration("declaration:f.go:1", "f.go", 5, 9)
	small := declaration("declaration:g.go:0", "g.go", 1, 5)
	out := ranked(second, first, small)
	capDesignTasks(out, 2, diff.Files{hunk("f.go", "++++     "), hunk("g.go", "+")})
	if got := taskIDs(out.Design.Tasks); !slices.Equal(got, []string{"declaration:f.go:0", "declaration:g.go:0"}) {
		t.Errorf("kept %v: four lines changed above the second declaration are not its churn", got)
	}
}

// The default is the cap the measurement in docs/findings.md chose, so a change to it
// is a change to that measurement.
func TestTheDefaultCapIsTwentyFourDesignTasks(t *testing.T) {
	if got := config.Defaults().Review.MaxDesignTasks; got != 24 {
		t.Errorf("review.max_design_tasks defaults to %d, want 24", got)
	}
}
