package practices

import (
	"github.com/jdziat/open-nitpick/internal/bundle"
	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/standards"
	"slices"
	"strings"
	"testing"
)

func spanPlanningFixture(t *testing.T) (DesignPlan, []standards.File) {
	t.Helper()
	files, inventory := designPlanningFixture(t)
	plan := PlanDesign(t.Context(), inventory, files, []string{"store/read.go"})
	files[3].Src = []byte("package service\nHIDDEN\nfunc Caller() {}\nHIDDEN TOO\n")
	plan.Tasks[0].ContextSpans = []ContextSpan{{Path: "service/service.go", SourceSpan: bundle.SourceSpan{Start: 1, End: 1}}, {Path: "service/service.go", SourceSpan: bundle.SourceSpan{Start: 3, End: 3}}}
	sources := map[string][]byte{}
	for _, file := range files {
		sources[file.Path] = file.Src
	}
	bindDesignSource(t.Context(), &plan.Tasks[0], sources)
	return plan, files
}

func TestDesignSpanPackingBindsOnlySuppliedEvidence(t *testing.T) {
	plan, files := spanPlanningFixture(t)
	cfg := config.Defaults()
	pack := func(files []standards.File) DesignPacking {
		return PackDesign(t.Context(), cfg, plan, files, nil, bundle.Reserve{})
	}
	packed := pack(files)
	if len(packed.Plan.Batches) != 1 {
		t.Fatalf("valid spans rejected: %+v", packed.Design)
	}
	text := bundle.RenderBatch(packed.Plan.Batches[0])
	if strings.Contains(text, "HIDDEN") || !strings.Contains(text, "func Caller() {}") {
		t.Fatal("wrong evidence rendered")
	}
	changed := slices.Clone(files)
	changed[3].Src = []byte("package service\nDIFFERENT HIDDEN\nfunc Caller() {}\nOTHER HIDDEN\n")
	if len(pack(changed).Plan.Batches) != 1 {
		t.Fatal("unseen bytes invalidated actual evidence binding")
	}
	changed[3].Src = []byte("package service\nHIDDEN\nfunc Changed() {}\nHIDDEN TOO\n")
	if len(pack(changed).Plan.Batches) != 0 {
		t.Fatal("changed evidence retained source binding")
	}
	plan.Tasks[0].ContextSpans[1].Start = 4
	plan.Tasks[0].ContextSpans[1].End = 4
	if len(pack(files).Plan.Batches) != 0 {
		t.Fatal("range metadata did not invalidate binding")
	}
}

func TestDesignSpanPackingRejectsMalformedScopeBeforeRequests(t *testing.T) {
	for _, spans := range [][]ContextSpan{
		{{Path: "missing.go", SourceSpan: bundle.SourceSpan{Start: 1, End: 1}}},
		{{Path: "store/read.go", SourceSpan: bundle.SourceSpan{Start: 1, End: 1}}},
		{{Path: "service/service.go", SourceSpan: bundle.SourceSpan{Start: 1, End: 99}}},
		{{Path: "service/service.go", SourceSpan: bundle.SourceSpan{Start: 1, End: 2}}, {Path: "service/service.go", SourceSpan: bundle.SourceSpan{Start: 2, End: 3}}},
	} {
		plan, files := spanPlanningFixture(t)
		plan.Tasks[0].ContextSpans = spans
		packed := PackDesign(t.Context(), config.Defaults(), plan, files, nil, bundle.Reserve{})
		if len(packed.Plan.Batches) != 0 || len(packed.Design.Tasks[0].Omitted) == 0 {
			t.Fatalf("malformed scope admitted: %+v", spans)
		}
	}
}

func TestDesignReportRejectsFindingsInUnsuppliedContextGaps(t *testing.T) {
	plan, _ := spanPlanningFixture(t)
	task := plan.Tasks[0]
	unit := Target{Kind: UnitTarget, ID: task.ID}
	report := Report{SchemaVersion: SchemaVersion, Profile: "engineering", Revision: "fixture", PolicySource: "operator", PolicyDigest: "fixture", Checks: []Check{{ID: "design", Version: "1", Instrument: Model, State: Completed, Planned: []Target{unit}, Examined: []Target{unit}, Tasks: []DesignTask{task}, Findings: []Finding{{Rule: "contract", Target: Target{Kind: FileTarget, ID: "service/service.go", Line: 3}}}}}}
	if problems := report.Problems(); len(problems) != 0 {
		t.Fatalf("visible control rejected: %v", problems)
	}
	report.Checks[0].Findings[0].Target.Line = 2
	if len(report.Problems()) == 0 {
		t.Fatal("gap claimed as examined evidence")
	}
}

func TestPrimarySourceExcerptsCannotSupportUnseenFindingLocations(t *testing.T) {
	source := Target{Kind: FileTarget, ID: "source.go"}
	task := DesignTask{ID: "focus", Source: source, Sources: []Target{source}, Purpose: "inspect function contract", SourceSpans: []ContextSpan{{Path: source.ID, SourceSpan: bundle.SourceSpan{Start: 1, End: 1}}, {Path: source.ID, SourceSpan: bundle.SourceSpan{Start: 3, End: 3}}}, Focus: []ContextSpan{{Path: source.ID, SourceSpan: bundle.SourceSpan{Start: 3, End: 3}}}}
	files := []standards.File{{Path: source.ID, Src: []byte("package fixture\n// Hidden commentary.\nfunc Call(){}\n")}}
	bindDesignSource(t.Context(), &task, map[string][]byte{source.ID: files[0].Src})
	plan := DesignPlan{Tasks: []DesignTask{task}}
	packed := PackDesign(t.Context(), config.Defaults(), plan, files, nil, bundle.Reserve{})
	if len(packed.Plan.Batches) != 1 || strings.Contains(bundle.RenderBatch(packed.Plan.Batches[0]), "Hidden commentary") || packed.Plan.Batches[0].Entries[0].HasContent() {
		t.Fatalf("primary excerpt claimed whole source: %+v", packed)
	}
	unit := Target{Kind: UnitTarget, ID: task.ID}
	report := Report{SchemaVersion: SchemaVersion, Profile: "engineering", Revision: "fixture", PolicySource: "operator", PolicyDigest: "fixture", Checks: []Check{{ID: "design", Version: "1", Instrument: Model, State: Completed, Planned: []Target{unit}, Examined: []Target{unit}, Tasks: packed.Design.Tasks, Findings: []Finding{{Rule: "contract", Target: Target{Kind: FileTarget, ID: source.ID, Line: 3}}}}}}
	if problems := report.Problems(); len(problems) > 0 {
		t.Fatalf("visible finding rejected: %v", problems)
	}
	report.Checks[0].Findings[0].Target.Line = 2
	if len(report.Problems()) == 0 {
		t.Fatal("primary source gap accepted as finding evidence")
	}
	task.Focus[0].Start = 2
	task.Focus[0].End = 2
	bindDesignSource(t.Context(), &task, map[string][]byte{source.ID: files[0].Src})
	if len(task.Omitted) == 0 {
		t.Fatal("focus outside supplied primary excerpt remained bound")
	}
}
