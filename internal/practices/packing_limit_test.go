package practices

import (
	"slices"
	"testing"

	"github.com/jdziat/open-nitpick/internal/bundle"
	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/diff"
	"github.com/jdziat/open-nitpick/internal/standards"
)

func TestDesignPackingAdmitsOversizedTaskWithTrimmedContext(t *testing.T) {
	source := Target{Kind: FileTarget, ID: "hub.go"}
	var context []Target
	for i := range 8 {
		context = append(context, Target{Kind: FileTarget, ID: string(rune('a'+i)) + ".go"})
	}
	task := DesignTask{ID: "declaration:hub", Source: source, Sources: []Target{source}, Context: context,
		Purpose: "hub contracts"}
	sourceText := map[string][]byte{source.ID: []byte("package hub")}
	for _, target := range context {
		sourceText[target.ID] = []byte("package hub")
	}
	bindDesignSource(t.Context(), &task, sourceText)
	design := DesignPlan{Tasks: []DesignTask{task}}
	files := []standards.File{{Path: source.ID, Src: []byte("package hub")}}
	for _, target := range context {
		files = append(files, standards.File{Path: target.ID, Src: []byte("package hub")})
	}
	cfg := config.Defaults()
	cfg.Review.MaxFilesPerRequest = 6
	packed := PackDesign(t.Context(), cfg, design, files, diff.Files{&diff.File{Path: source.ID}}, bundle.Reserve{})
	if len(packed.Plan.Batches) != 1 {
		t.Fatalf("oversized task produced no request: %+v", packed.Plan)
	}
	batch := packed.Plan.Batches[0]
	// The change surface must include one of the context files so the
	// changed-first sort has something to reorder; without it the comparator
	// is a no-op and this test cannot fail if the sort is deleted.
	packed = PackDesign(t.Context(), cfg, design, files,
		diff.Files{&diff.File{Path: source.ID}, &diff.File{Path: context[6].ID}}, bundle.Reserve{})
	if len(packed.Plan.Batches) != 1 {
		t.Fatalf("changed-context run produced no request: %+v", packed.Plan)
	}
	batch = packed.Plan.Batches[0]
	// Six requests slots: the source plus five contexts. The changed file
	// sorts first, so it must survive; the last unchanged file is cut.
	for _, kept := range context[:4] {
		if !slices.Contains(batch.Paths(), kept.ID) {
			t.Fatalf("dropped unchanged context %s while slots remained", kept.ID)
		}
	}
	if !slices.Contains(batch.Paths(), context[6].ID) {
		t.Fatalf("changed context file %s was trimmed before its unchanged peers", context[6].ID)
	}
	if slices.Contains(batch.Paths(), context[5].ID) {
		t.Fatalf("kept unchanged context %s beyond the cap while a changed file was present", context[5].ID)
	}
	if len(batch.Entries) != cfg.Review.MaxFilesPerRequest || !slices.Contains(batch.Paths(), source.ID) {
		t.Fatalf("request lost its source or ignored the file cap: %+v", batch.Entries)
	}
	// Only the unchanged file the changed one displaced is missing now;
	// the changed file itself is back in its sorted position.
	if slices.Contains(batch.Paths(), context[7].ID) {
		t.Fatalf("kept unchanged context %s beyond the cap", context[7].ID)
	}
	if len(packed.Design.Tasks[0].Omitted) != 0 {
		t.Fatalf("optional context made the task incomplete: %+v", packed.Design.Tasks[0].Omitted)
	}
	for _, skip := range packed.Plan.Skipped {
		t.Fatalf("a trimmed request was still recorded as unread: %+v", skip)
	}
}
