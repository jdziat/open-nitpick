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

func TestADesignTaskCarryingCallerObligationsOutranksOneWithMoreChurn(t *testing.T) {
	busy := task("busy", 1, 0)
	bound := task("bound", 1, 2)
	out := ranked(busy, bound)
	changes := diff.Files{added("busya.go", 500)}
	capDesignTasks(out, 1, changes)
	if got := taskIDs(out.Design.Tasks); !slices.Equal(got, []string{"bound"}) {
		t.Errorf("kept %v: obligations rank before churn, so the bound task stays", got)
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
