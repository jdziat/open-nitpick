package practices

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/bundle"
	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/diff"
	"github.com/jdziat/open-nitpick/internal/standards"
)

func TestFocusedTasksShareSourceWithoutLosingObligations(t *testing.T) {
	files := []standards.File{
		{Path: "go.mod", Src: []byte("module example.com/app\n")},
		{Path: "main.go", Src: []byte("package app\nfunc First() int {return one()}\nfunc Second() int {return two()}\n")},
		{Path: "helpers.go", Src: []byte("package app\nfunc one() int {return 1}\n\nfunc unrelated() int {return 99}\n\nfunc two() int {return 2}\n")},
	}
	inventory, _ := InspectDesign(files, nil)
	plan := PlanDesignInteractions(t.Context(), inventory, files, []string{"main.go"})
	plan = isolateSourceTasks(t, plan, "main.go")
	cfg := config.Defaults()
	packed := PackDesign(t.Context(), cfg, plan, files, nil, bundle.Reserve{})
	var taskIDs []string
	for _, task := range plan.Tasks {
		if task.Source.ID == "main.go" {
			taskIDs = append(taskIDs, task.ID)
		}
	}
	var matched []bundle.Batch
	for _, batch := range packed.Plan.Batches {
		if slices.Contains(taskIDs, batch.DesignTask) {
			matched = append(matched, batch)
		}
	}
	if len(taskIDs) != 2 || len(matched) != 1 || !slices.Equal(matched[0].DesignTaskIDs(), taskIDs) {
		t.Fatalf("focused tasks not shared: tasks=%v batches=%+v", taskIDs, matched)
	}
	text := bundle.RenderBatch(matched[0])
	for _, want := range []string{"return one()", "return two()", "return 1", "return 2"} {
		if !strings.Contains(text, want) {
			t.Fatalf("lost %q from shared request", want)
		}
	}
	if strings.Contains(text, "return 99") {
		t.Fatal("merging spans widened over an unseen declaration")
	}
	if strings.Count(text, "func First()") != 1 {
		t.Fatal("shared primary rendered repeatedly")
	}
	for _, task := range plan.Tasks {
		if slices.Contains(taskIDs, task.ID) && !strings.Contains(text, task.SourceDigest) {
			t.Fatal("shared request lost source binding")
		}
	}
	// A merged request must fit including all task descriptions and instructions.
	cfg.Review.TokenBudgetPerRequest = matched[0].Tokens - 1
	limited := PackDesign(t.Context(), cfg, plan, files, nil, bundle.Reserve{})
	separated := 0
	for _, batch := range limited.Plan.Batches {
		if slices.Contains(taskIDs, batch.DesignTask) {
			separated++
			if len(batch.DesignTaskIDs()) != 1 {
				t.Fatal("merge exceeded token limit")
			}
		}
	}
	if separated != 2 {
		t.Fatalf("budget lost complete tasks instead of keeping separate requests: %+v", limited.Design)
	}
}

func TestSharedDesignSourcePreservesExactCitationIntervals(t *testing.T) {
	file := &diff.File{Path: "source.go", Kind: diff.ChangeModified}
	source := "one\ntwo\nthree\nfour\nfive\nsix\n"
	first := bundle.Batch{DesignTask: "a", Entries: []bundle.Entry{{File: file, Content: source, SourceOnly: true, SourceSpans: []bundle.SourceSpan{{Start: 2, End: 3}}}}}
	next := bundle.Batch{DesignTask: "b", Entries: []bundle.Entry{{File: file, Content: source, SourceOnly: true, SourceSpans: []bundle.SourceSpan{{Start: 5, End: 6}}}}}
	merged, ok := combineDesignBatches(first, next, 2, 10000)
	if !ok || !slices.Equal(merged.Entries[0].SourceSpans, []bundle.SourceSpan{{Start: 2, End: 3}, {Start: 5, End: 6}}) {
		t.Fatalf("merged unseen lines: %+v", merged)
	}
	if len(first.Entries[0].SourceSpans) != 1 || len(first.AdditionalDesignTasks) != 0 {
		t.Fatal("merge modified original evidence")
	}
	next.Entries[0].SourceSpans = nil
	next.Entries[0].SourceOnly = false
	full, ok := combineDesignBatches(first, next, 2, 10000)
	if !ok || !full.Entries[0].HasContent() || full.Entries[0].SourceOnly {
		t.Fatalf("whole changed source demoted: %+v", full)
	}
	next.Entries[0].Content = "different snapshot"
	if _, ok := combineDesignBatches(first, next, 2, 10000); ok {
		t.Fatal("merged inconsistent snapshots")
	}
	next.Entries[0].Content = source
	next.Entries = append(next.Entries, bundle.Entry{File: &diff.File{Path: "other.go"}, Content: "other"})
	if _, ok := combineDesignBatches(first, next, 1, 10000); ok {
		t.Fatal("merged beyond file limit")
	}
}

func TestPackageSharingPreservesEveryTaskAndSuppliesSharedSourceOnce(t *testing.T) {
	files := []standards.File{
		{Path: "go.mod", Src: []byte("module example.com/app\n")},
		{Path: "first.go", Src: []byte("package app\nfunc First() int {return shared}\n")},
		{Path: "second.go", Src: []byte("package app\nfunc Second() int {return shared}\n")},
		{Path: "shared.go", Src: []byte("package app\nconst shared = 42\n")},
		{Path: "other/other.go", Src: []byte("package other\nfunc Other() {}\n")},
	}
	inventory, _ := InspectDesign(files, nil)
	plan := PlanDesignInteractions(t.Context(), inventory, files, []string{"first.go", "second.go", "shared.go", "other/other.go"})
	cfg := config.Defaults()
	packed := PackDesign(t.Context(), cfg, plan, files, nil, bundle.Reserve{})
	if len(plan.Tasks) != 8 || len(packed.Plan.Batches) != 2 {
		t.Fatalf("package grouping lost tasks: %+v", packed.Design)
	}
	found := map[string]bool{}
	for _, batch := range packed.Plan.Batches {
		text := bundle.RenderBatch(batch)
		for _, id := range batch.DesignTaskIDs() {
			if found[id] {
				t.Fatalf("task duplicated: %s", id)
			}
			found[id] = true
			task := plan.Tasks[slices.IndexFunc(plan.Tasks, func(task DesignTask) bool { return task.ID == id })]
			if !strings.Contains(text, task.SourceDigest) {
				t.Fatalf("shared request lost %s binding", id)
			}
		}
		if slices.Contains(batch.DesignTaskIDs(), "file:first.go") {
			if len(batch.DesignTaskIDs()) != 6 || strings.Count(text, "const shared = 42") != 1 || !strings.Contains(text, "func Second()") || strings.Contains(text, "func Other()") {
				t.Fatalf("source or package boundary lost: %s", text)
			}
		}
	}
	if len(found) != len(plan.Tasks) {
		t.Fatal("planned task was not assigned a request")
	}
}

func TestLargeFocusSetKeepsEveryTaskWithinRequestCountLimit(t *testing.T) {
	var source strings.Builder
	source.WriteString("package fixture\n")
	for i := 0; i < 129; i++ {
		fmt.Fprintf(&source, "func F%d() {}\n", i)
	}
	files := []standards.File{{Path: "go.mod", Src: []byte("module example.com/app\n")}, {Path: "app.go", Src: []byte(source.String())}}
	inventory, _ := InspectDesign(files, nil)
	plan := PlanDesignInteractions(t.Context(), inventory, files, []string{"app.go"})
	cfg := config.Defaults()
	packed := PackDesign(t.Context(), cfg, plan, files, nil, bundle.Reserve{})
	if len(plan.Tasks) != 130 || len(packed.Plan.Batches) != 3 {
		t.Fatalf("large focus set lost work or ignored task limit: tasks=%d requests=%d", len(plan.Tasks), len(packed.Plan.Batches))
	}
	seen := map[string]bool{}
	full := 0
	for _, batch := range packed.Plan.Batches {
		if len(batch.DesignTaskIDs()) > 64 || batch.Tokens > cfg.Review.TokenBudgetPerRequest {
			t.Fatal("request exceeded task or token limit")
		}
		for _, id := range batch.DesignTaskIDs() {
			if seen[id] {
				t.Fatalf("duplicate task %s", id)
			}
			seen[id] = true
		}
		for _, entry := range batch.Entries {
			if entry.File.Path == "app.go" && entry.HasContent() {
				full++
			}
		}
	}
	if len(seen) != 130 || full != 1 {
		t.Fatalf("focus or whole-source slop evidence lost: tasks=%d full reads=%d", len(seen), full)
	}
}
