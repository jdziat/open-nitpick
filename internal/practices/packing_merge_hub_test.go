package practices

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/bundle"
	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/standards"
)

// TestHubFileDeclarationsMergeAcrossDistinctCallerFiles pins the bug this
// package shipped: a changed file whose declarations each call a different
// helper produced one design task per declaration, each needing only its own
// helper as context, well inside review.max_files_per_request. Reusing that
// same cap as the ceiling on a *merged* batch stopped those tasks from
// combining the moment the batch already held that many distinct files, so a
// file with enough declarations turned into one request per declaration
// regardless of how much token budget was left. A fix that only widens the
// cap does not catch this: it just moves the same wall further out. This
// pins that the batch count stays proportional to the token budget, not to
// review.max_files_per_request, once every per-task admission already fits.
func TestHubFileDeclarationsMergeAcrossDistinctCallerFiles(t *testing.T) {
	const helperCount = 10
	files := []standards.File{{Path: "go.mod", Src: []byte("module example.com/app\n")}}
	var hub strings.Builder
	hub.WriteString("package fixture\n")
	for i := 0; i < helperCount; i++ {
		fmt.Fprintf(&hub, "import \"example.com/app/helper%d\"\n", i)
	}
	for i := 0; i < helperCount; i++ {
		fmt.Fprintf(&hub, "func Hub%d() int { return helper%d.Value%d() }\n", i, i, i)
	}
	files = append(files, standards.File{Path: "hub.go", Src: []byte(hub.String())})
	for i := 0; i < helperCount; i++ {
		src := fmt.Sprintf("package helper%d\nfunc Value%d() int { return %d }\n", i, i, i)
		files = append(files, standards.File{Path: fmt.Sprintf("helper%d/helper.go", i), Src: []byte(src)})
	}
	inventory, errs := InspectDesign(files, nil)
	if len(errs.Findings) > 0 {
		t.Fatalf("invalid fixture: %+v", errs)
	}
	plan := PlanDesignInteractions(t.Context(), inventory, files, []string{"hub.go"})
	if len(plan.Errors) > 0 {
		t.Fatalf("planning errors: %v", plan.Errors)
	}
	plan = isolateSourceTasks(t, plan, "hub.go")
	if len(plan.Tasks) != helperCount {
		t.Fatalf("fixture drifted: want %d declaration tasks, got %d", helperCount, len(plan.Tasks))
	}
	cfg := config.Defaults()
	// Each task alone needs 2 files (hub.go plus its one helper), well under
	// the cap; the regression was in merging tasks that already fit.
	cfg.Review.MaxFilesPerRequest = 6
	packed := PackDesign(t.Context(), cfg, plan, files, nil, bundle.Reserve{})
	for _, task := range plan.Tasks {
		for _, omission := range task.Omitted {
			t.Fatalf("task %s was skipped: %s", task.ID, omission.Reason)
		}
	}
	for _, skip := range packed.Plan.Skipped {
		t.Fatalf("a packable task was recorded as skipped: %+v", skip)
	}
	// Merged requests stay well under the token budget here, so every
	// declaration's batch should have combined into one request; the bug
	// produced helperCount separate batches instead.
	if got := len(packed.Plan.Batches); got != 1 {
		t.Fatalf("hub file declarations did not merge across distinct caller files: got %d batches, want 1", got)
	}
}
