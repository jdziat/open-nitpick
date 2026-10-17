package practices

import (
	"slices"
	"testing"

	"github.com/jdziat/open-nitpick/internal/bundle"
	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/diff"
	"github.com/jdziat/open-nitpick/internal/standards"
)

func interactionTask(t *testing.T, callees, extra int) (DesignPlan, []standards.File) {
	t.Helper()
	source := Target{Kind: FileTarget, ID: "hub.go"}
	task := DesignTask{ID: "declaration:hub", Source: source, Sources: []Target{source}, Purpose: "hub contracts"}
	contents := map[string][]byte{source.ID: []byte("package hub")}
	files := []standards.File{{Path: source.ID, Src: contents[source.ID]}}
	for i := range extra + callees {
		target := Target{Kind: FileTarget, ID: string(rune('a'+i)) + ".go"}
		if i >= extra {
			task.Interactions = append(task.Interactions, DesignInteraction{Caller: source, Callee: target})
		}
		task.Context = append(task.Context, target)
		contents[target.ID] = []byte("package hub")
		files = append(files, standards.File{Path: target.ID, Src: contents[target.ID]})
	}
	bindDesignSource(t.Context(), &task, contents)
	return DesignPlan{Tasks: []DesignTask{task}}, files
}

// Context beyond the file cap is cut for an interaction task as for any
// other; only the files its caller obligations name are kept, because the
// report rejects a task that declares an obligation it does not carry.
func TestInteractionTaskKeepsItsCalleesAndTrimsTheRestOfItsContext(t *testing.T) {
	design, files := interactionTask(t, 2, 8)
	cfg := config.Defaults()
	cfg.Review.MaxFilesPerRequest = 6
	packed := PackDesign(t.Context(), cfg, design, files, diff.Files{&diff.File{Path: "hub.go"}}, bundle.Reserve{})
	if len(packed.Plan.Batches) != 1 || len(packed.Plan.Skipped) != 0 {
		t.Fatalf("an interaction task with trimmable context was not packed: %+v", packed.Plan)
	}
	paths := packed.Plan.Batches[0].Paths()
	if len(paths) > cfg.Review.MaxFilesPerRequest {
		t.Fatalf("request carries %d files, cap is %d", len(paths), cfg.Review.MaxFilesPerRequest)
	}
	for _, callee := range []string{"i.go", "j.go"} {
		if !slices.Contains(paths, callee) {
			t.Fatalf("callee %s named by a caller obligation was trimmed: %v", callee, paths)
		}
	}
}

// Callees cannot be trimmed, so a task whose obligations alone pass the cap
// is still skipped and says so.
func TestInteractionTaskWithTooManyCalleesIsStillSkipped(t *testing.T) {
	design, files := interactionTask(t, 6, 0)
	cfg := config.Defaults()
	cfg.Review.MaxFilesPerRequest = 6
	packed := PackDesign(t.Context(), cfg, design, files, diff.Files{&diff.File{Path: "hub.go"}}, bundle.Reserve{})
	if len(packed.Plan.Batches) != 0 || len(packed.Plan.Skipped) != 1 {
		t.Fatalf("a task whose callees exceed the cap was packed: %+v", packed.Plan)
	}
}
