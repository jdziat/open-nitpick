package practices

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/bundle"
	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/standards"
)

func interactionFixture(t *testing.T) ([]standards.File, DesignInventory) {
	t.Helper()
	files := []standards.File{
		{Path: "go.mod", Src: []byte("module example.com/app\n")},
		{Path: "store/types.go", Src: []byte("package store\ntype Reader struct{}\n")},
		{Path: "store/new.go", Src: []byte("package store\nfunc New() *Reader { return &Reader{} }\n")},
		{Path: "store/read.go", Src: []byte("package store\nfunc (r *Reader) Read() int { return lookup() }\n")},
		{Path: "store/helper.go", Src: []byte("package store\nfunc lookup() int {\n return value\n}\n")},
		{Path: "store/state.go", Src: []byte("package store\nvar value int\n")},
		{Path: "store/configure.go", Src: []byte("package store\nfunc Configure() {\n value = 42\n}\n")},
		{Path: "store/unrelated.go", Src: []byte("package store\nfunc (r *Reader) Unrelated() string {\n return \"UNRELATED METHOD\"\n}\n")},
		{Path: "service/service.go", Src: []byte("package service\nimport \"example.com/app/store\"\nfunc Start() int { reader:=store.New(); return reader.Read() }\n")},
	}
	inventory, _ := InspectDesign(files, nil)
	if len(inventory.Errors) > 0 {
		t.Fatalf("invalid fixture: %v", inventory.Errors)
	}
	return files, inventory
}

func TestInteractionPlanKeepsCallerAndStateEvidenceWithoutEveryMethod(t *testing.T) {
	files, inventory := interactionFixture(t)
	plan := PlanDesignInteractions(t.Context(), inventory, files, []string{"store/read.go"})
	if len(plan.Errors) > 0 {
		t.Fatalf("planning errors: %v", plan.Errors)
	}
	cfg := config.Defaults()
	cfg.Review.MaxFilesPerRequest = 32
	packed := PackDesign(t.Context(), cfg, plan, files, nil, bundle.Reserve{})
	var matched bool
	for _, batch := range packed.Plan.Batches {
		if batch.DesignTask != "file:service/service.go" {
			continue
		}
		matched = true
		text := bundle.RenderBatch(batch)
		for _, evidence := range []string{"reader.Read()", "func New()", "func Configure()", "func lookup()", "var value int", "return value", "value = 42"} {
			if !strings.Contains(text, evidence) {
				t.Fatalf("caller request lacks %q: %s", evidence, text)
			}
		}
		if strings.Contains(text, "UNRELATED METHOD") {
			t.Fatal("receiver type expanded unrelated methods")
		}
		foundFull := false
		for _, entry := range batch.Entries {
			if entry.File.Path == "service/service.go" {
				foundFull = entry.HasContent()
			}
		}
		if !foundFull {
			t.Fatal("caller source lost whole-file slop evidence")
		}
	}
	if !matched {
		t.Fatalf("caller interaction was not packed: %+v", packed.Design)
	}
	count := 0
	for _, task := range plan.Tasks {
		if task.Source.ID == "service/service.go" {
			count++
			if len(task.Interactions) < 2 {
				t.Fatalf("constructor/read obligations lost: %+v", task)
			}
		}
	}
	if count != 1 {
		t.Fatalf("caller duplicated across %d tasks", count)
	}
}

func TestInteractionBudgetCannotSplitAnObligationIntoUnrelatedRequests(t *testing.T) {
	files, inventory := interactionFixture(t)
	plan := PlanDesignInteractions(t.Context(), inventory, files, []string{"store/read.go"})
	cfg := config.Defaults()
	cfg.Review.MaxFilesPerRequest = 1
	packed := PackDesign(t.Context(), cfg, plan, files, nil, bundle.Reserve{})
	if len(plan.Tasks) != len(packed.Design.Tasks) {
		t.Fatal("budget changed intended scope")
	}
	for _, task := range packed.Design.Tasks {
		if task.Source.ID == "service/service.go" && (len(task.Omitted) == 0 || len(task.Interactions) < 2) {
			t.Fatal("budget lost caller obligation or counted partial evidence complete")
		}
	}
	for _, batch := range packed.Plan.Batches {
		if batch.DesignTask == "file:service/service.go" {
			t.Fatal("indivisible interaction admitted with one file")
		}
	}
}

func TestInteractionPlanRetainsBlankImportInitializationContext(t *testing.T) {
	files, inventory := designPlanningFixture(t)
	plan := PlanDesignInteractions(t.Context(), inventory, files, []string{"store/read.go"})
	caller := slices.IndexFunc(plan.Tasks, func(task DesignTask) bool { return task.Source.ID == "service/service.go" })
	if caller < 0 {
		t.Fatal("blank-import caller omitted")
	}
	if !slices.Contains(plan.Tasks[caller].Context, Target{Kind: FileTarget, ID: "store/state.go"}) {
		t.Fatalf("blank import lost package initialization context: %+v", plan.Tasks[caller])
	}
}

func TestInteractionPlanRetainsCallerOfRemovedDeclaration(t *testing.T) {
	files, inventory := interactionFixture(t)
	for i := range files {
		if files[i].Path == "service/service.go" {
			files[i].Src = []byte("package service\nimport \"example.com/app/store\"\nfunc Start() { store.Removed() }\n")
		}
	}
	plan := PlanDesignInteractions(t.Context(), inventory, files, []string{"store/read.go"})
	found := false
	for _, task := range plan.Tasks {
		if task.Source.ID != "service/service.go" {
			continue
		}
		found = true
		if len(task.Interactions) == 0 || !slices.ContainsFunc(task.Omitted, func(o Omission) bool { return strings.Contains(o.Target.ID, "Removed") }) {
			t.Fatalf("missing API disappeared from scope: %+v", task)
		}
	}
	if !found {
		t.Fatal("caller of absent declaration disappeared")
	}
}

func TestInteractionPlanIsIndependentOfInputOrderAndEmptyScope(t *testing.T) {
	files, inventory := interactionFixture(t)
	original := PlanDesignInteractions(t.Context(), inventory, files, nil)
	reversed := slices.Clone(files)
	slices.Reverse(reversed)
	same := PlanDesignInteractions(t.Context(), inventory, reversed, nil)
	if len(original.Tasks) != len(same.Tasks) {
		t.Fatal("input order changed task scope")
	}
	for i, task := range original.Tasks {
		if task.ID != same.Tasks[i].ID || task.SourceDigest != same.Tasks[i].SourceDigest {
			t.Fatalf("input order changed binding for %s", task.ID)
		}
	}
	empty := PlanDesignInteractions(t.Context(), inventory, files, []string{})
	if len(empty.Tasks) != 0 || len(empty.Errors) != 0 {
		t.Fatalf("empty scope invented work: %+v", empty)
	}
}

func TestInteractionCandidatesRetainBothCoupledRuleImplementations(t *testing.T) {
	files := []standards.File{
		{Path: "go.mod", Src: []byte("module example.com/app\n")},
		{Path: "preview.go", Src: []byte("package shipping\n// Preview quotes the same retail shipping rule as Checkout.\nfunc Preview(subtotal int) int {\n if subtotal >= 6000 {return 0}\n return 500\n}\n")},
		{Path: "checkout.go", Src: []byte("package shipping\n// Checkout applies the retail shipping policy.\nfunc Checkout(subtotal int) int {\n if subtotal >= 5000 {return 0}\n // Retail delivery fee.\n return 500\n}\n")},
	}
	inventory, _ := InspectDesign(files, nil)
	plan := PlanDesignInteractions(t.Context(), inventory, files, []string{"preview.go"})
	packed := PackDesign(t.Context(), config.Defaults(), plan, files, nil, bundle.Reserve{})
	found := false
	for _, batch := range packed.Plan.Batches {
		if batch.DesignTask != "file:preview.go" {
			continue
		}
		found = true
		text := bundle.RenderBatch(batch)
		if !strings.Contains(text, "subtotal >= 6000") || !strings.Contains(text, "subtotal >= 5000") {
			t.Fatalf("coupled comparison lost an implementation: %s", text)
		}
	}
	if !found {
		t.Fatalf("comparison omitted: %+v", packed.Design)
	}
}

func TestInteractionReadsDoNotMakeEveryArgumentConsumerAStateWriter(t *testing.T) {
	files := []standards.File{
		{Path: "go.mod", Src: []byte("module example.com/app\n")},
		{Path: "read.go", Src: []byte("package fixture\nfunc Read() error {return errMissing}\n")},
		{Path: "error.go", Src: []byte("package fixture\nimport \"errors\"\nvar errMissing=errors.New(\"missing\")\n")},
		{Path: "other.go", Src: []byte("package fixture\nimport \"fmt\"\nfunc Other() error {\n return fmt.Errorf(\"UNRELATED WRAPPER: %w\",errMissing)\n}\n")},
	}
	inventory, _ := InspectDesign(files, nil)
	plan := PlanDesignInteractions(t.Context(), inventory, files, []string{"read.go"})
	packed := PackDesign(t.Context(), config.Defaults(), plan, files, nil, bundle.Reserve{})
	found := false
	for _, batch := range packed.Plan.Batches {
		if batch.DesignTask != "file:read.go" {
			continue
		}
		found = true
		text := bundle.RenderBatch(batch)
		if !strings.Contains(text, `errors.New("missing")`) || strings.Contains(text, "UNRELATED WRAPPER") {
			t.Fatalf("read expanded unrelated consumers: %s", text)
		}
	}
	if !found {
		t.Fatalf("read task omitted: %+v", packed.Design)
	}
}

func TestInteractionStateDeclarationIncludesRangeAssignmentWriter(t *testing.T) {
	files := []standards.File{
		{Path: "go.mod", Src: []byte("module example.com/app\n")},
		{Path: "state.go", Src: []byte("package fixture\nvar value int\n")},
		{Path: "write.go", Src: []byte("package fixture\nfunc Set(values []int) {\n for _, value = range values {}\n}\n")},
		{Path: "local.go", Src: []byte("package fixture\nfunc Local(values []int) {\n for _, value := range values { _ = value }\n}\n")},
	}
	inventory, _ := InspectDesign(files, nil)
	plan := PlanDesignInteractions(t.Context(), inventory, files, []string{"state.go"})
	packed := PackDesign(t.Context(), config.Defaults(), plan, files, nil, bundle.Reserve{})
	found := false
	for _, batch := range packed.Plan.Batches {
		if batch.DesignTask != "file:state.go" {
			continue
		}
		found = true
		text := bundle.RenderBatch(batch)
		if !strings.Contains(text, "for _, value = range values") || strings.Contains(text, "func Local") {
			t.Fatalf("state focus lost its writer or acquired a local shadow: %s", text)
		}
	}
	if !found {
		t.Fatalf("state task omitted: %+v", packed.Design)
	}
}

func TestPackageFunctionDoesNotSelectUnrelatedSameNamedMethods(t *testing.T) {
	files := []standards.File{
		{Path: "go.mod", Src: []byte("module example.com/app\n")},
		{Path: "call.go", Src: []byte("package fixture\nimport \"strings\"\nfunc Strip(s string) string {return strings.TrimSpace(s)}\n")},
		{Path: "method.go", Src: []byte("package fixture\ntype Unrelated struct{}\nfunc (Unrelated) TrimSpace(s string) string {return \"UNRELATED METHOD\"}\n")},
	}
	inventory, _ := InspectDesign(files, nil)
	plan := PlanDesignInteractions(t.Context(), inventory, files, []string{"call.go"})
	packed := PackDesign(t.Context(), config.Defaults(), plan, files, nil, bundle.Reserve{})
	found := false
	for _, batch := range packed.Plan.Batches {
		if batch.DesignTask != "file:call.go" {
			continue
		}
		found = true
		text := bundle.RenderBatch(batch)
		if !strings.Contains(text, "strings.TrimSpace(s)") || strings.Contains(text, "UNRELATED METHOD") {
			t.Fatalf("package selection became a receiver call: %s", text)
		}
	}
	if !found {
		t.Fatal("caller request did not run")
	}
}

func TestInteractionContractRetainsMultilineVariableType(t *testing.T) {
	files := []standards.File{
		{Path: "go.mod", Src: []byte("module example.com/app\n")},
		{Path: "call.go", Src: []byte("package fixture\nimport _ \"example.com/app/state\"\nfunc Call() {}\n")},
		{Path: "state/value.go", Src: []byte("package state\nvar Value struct {\n Count int\n Result interface {\n  Compute() int\n }\n}\n")},
	}
	inventory, _ := InspectDesign(files, nil)
	plan := PlanDesignInteractions(t.Context(), inventory, files, []string{"call.go"})
	packed := PackDesign(t.Context(), config.Defaults(), plan, files, nil, bundle.Reserve{})
	if len(packed.Plan.Batches) != 1 {
		t.Fatalf("blank-import contract did not pack: %+v", packed.Design)
	}
	text := bundle.RenderBatch(packed.Plan.Batches[0])
	for _, want := range []string{"Count int", "Result interface", "Compute() int"} {
		if !strings.Contains(text, want) {
			t.Fatalf("contract omitted type shape %q: %s", want, text)
		}
	}
}

func TestInteractionFallbackSuppliesWholeCalleeOrReportsMissingSource(t *testing.T) {
	caller := Target{Kind: FileTarget, ID: "caller.go"}
	callee := Target{Kind: FileTarget, ID: "callee.go"}
	for _, available := range []bool{true, false} {
		t.Run(fmt.Sprint(available), func(t *testing.T) {
			task := DesignTask{ID: "caller", Source: caller, Sources: []Target{caller}, Interactions: []DesignInteraction{{Caller: caller, Callee: callee}}}
			includeInteractionCallees(&task)
			sources := map[string][]byte{caller.ID: []byte("package fixture\nfunc Call(){Run()}\n")}
			if available {
				sources[callee.ID] = []byte("package fixture\nfunc Run(){panic(\"callee body\")}\n")
			}
			var files []standards.File
			for name, source := range sources {
				files = append(files, standards.File{Path: name, Src: source})
			}
			bindDesignSource(t.Context(), &task, sources)
			packed := PackDesign(t.Context(), config.Defaults(), DesignPlan{Tasks: []DesignTask{task}}, files, nil, bundle.Reserve{})
			if available {
				if len(packed.Plan.Batches) != 1 || !strings.Contains(bundle.RenderBatch(packed.Plan.Batches[0]), "callee body") {
					t.Fatalf("available obligation was lost: %+v", packed.Design)
				}
			} else if len(packed.Plan.Batches) != 0 || !slices.ContainsFunc(packed.Design.Tasks[0].Omitted, func(o Omission) bool { return o.Target == callee }) {
				t.Fatalf("missing callee claimed complete: %+v", packed.Design)
			}
		})
	}
}
