package practices

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/standards"
)

func designPlanningFixture() ([]standards.File, DesignInventory) {
	files := []standards.File{
		{Path: "go.mod", Src: []byte("module example.com/app\n\ngo 1.25\n")},
		{Path: "store/read.go", Src: []byte("package store\nfunc Read() {}\n")},
		{Path: "store/state.go", Src: []byte("package store\nvar state int\n")},
		{Path: "service/service.go", Src: []byte("package service\nimport _ \"example.com/app/store\"\n")},
		{Path: "client/client.go", Src: []byte("package client\nimport _ \"example.com/app/service\"\n")},
		{Path: "unrelated/other.go", Src: []byte("package unrelated\n")},
	}
	inventory, _ := InspectDesign(files, nil)
	return files, inventory
}

func TestDesignPlanIncludesPackageSiblingsDependenciesAndCallers(t *testing.T) {
	files, inventory := designPlanningFixture()
	plan := PlanDesign(t.Context(), inventory, files, []string{"service/service.go"})
	if len(plan.Errors) != 0 || len(plan.Tasks) != 1 {
		t.Fatalf("plan=%+v", plan)
	}
	task := plan.Tasks[0]
	if task.ID != "package:example.com/app/service" || len(task.Sources) != 1 || len(task.Omitted) != 0 || task.SourceDigest == "" {
		t.Fatalf("task=%+v", task)
	}
	var names []string
	for _, target := range task.Context {
		names = append(names, target.ID)
	}
	if !slices.Equal(names, []string{"client/client.go", "store/read.go", "store/state.go"}) {
		t.Fatalf("context=%v", names)
	}
	sibling := PlanDesign(t.Context(), inventory, files, []string{"store/read.go"})
	if len(sibling.Tasks) != 1 || len(sibling.Tasks[0].Sources) != 2 {
		t.Fatalf("package sibling disappeared: %+v", sibling)
	}
}

func TestDesignPlanDistinguishesEmptyChangeFromWholeTree(t *testing.T) {
	files, inventory := designPlanningFixture()
	tree := PlanDesign(t.Context(), inventory, files, nil)
	if len(tree.Tasks) != 5 {
		t.Fatalf("tree did not plan four packages and manifest: %+v", tree)
	}
	empty := PlanDesign(t.Context(), inventory, files, []string{})
	if len(empty.Tasks) != 0 || len(empty.Errors) != 0 {
		t.Fatalf("empty scope=%+v", empty)
	}
	manifest := PlanDesign(t.Context(), inventory, files, []string{"go.mod"})
	if len(manifest.Tasks) != 5 {
		t.Fatalf("module change lost affected packages: %+v", manifest)
	}
}

func TestDesignPlanDigestChangesWithUnchangedFilesDependencies(t *testing.T) {
	files, inventory := designPlanningFixture()
	original := PlanDesign(t.Context(), inventory, files, []string{"service/service.go"})
	reversed := slices.Clone(files)
	slices.Reverse(reversed)
	same := PlanDesign(t.Context(), inventory, reversed, []string{"service/service.go"})
	if original.Tasks[0].SourceDigest != same.Tasks[0].SourceDigest {
		t.Fatal("input order changed source identity")
	}
	changedSource := []byte("package store\nvar state any\n")
	if len(changedSource) != len(files[2].Src) {
		t.Fatal("digest control must change bytes without changing length")
	}
	files[2].Src = changedSource
	changed := PlanDesign(t.Context(), inventory, files, []string{"service/service.go"})
	if original.Tasks[0].SourceDigest == changed.Tasks[0].SourceDigest {
		t.Fatal("dependency change reused source identity")
	}
}

func TestDesignPlanKeepsMissingSourceAndImportsVisible(t *testing.T) {
	files, inventory := designPlanningFixture()
	for i := range inventory.Units {
		if inventory.Units[i].ID == "example.com/app/service" {
			inventory.Units[i].Unresolved = []string{"example.com/app/missing"}
		}
	}
	files = slices.Delete(files, 2, 3)
	plan := PlanDesign(t.Context(), inventory, files, []string{"service/service.go"})
	if len(plan.Tasks) != 1 || len(plan.Tasks[0].Omitted) != 2 {
		t.Fatalf("missing context disappeared: %+v", plan)
	}
	for _, omission := range plan.Tasks[0].Omitted {
		if omission.Reason == "" {
			t.Fatal("missing context has no explanation")
		}
	}
}

func TestDesignPlanReportsCancellationInsteadOfAnEmptyAssessment(t *testing.T) {
	files, inventory := designPlanningFixture()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	plan := PlanDesign(ctx, inventory, files, nil)
	if !strings.Contains(strings.Join(plan.Errors, ";"), "canceled") || len(plan.Tasks) != 5 {
		t.Fatalf("cancellation lost intended scope: %+v", plan)
	}
	for _, task := range plan.Tasks {
		if len(task.Omitted) == 0 || task.SourceDigest != "" {
			t.Fatalf("cancelled task claimed source capture: %+v", task)
		}
	}
}

func TestPackageTaskRequiresCompleteSourcesBeforePromotingCoverage(t *testing.T) {
	files, inventory := designPlanningFixture()
	task := PlanDesign(t.Context(), inventory, files, []string{"store/read.go"}).Tasks[0]
	unit := Target{Kind: UnitTarget, ID: task.ID}
	report := Report{SchemaVersion: 1, Profile: "engineering", Revision: "fixture", PolicySource: "operator", PolicyDigest: "fixture", Checks: []Check{{ID: "design", Version: "1", Instrument: Model, State: Completed, Planned: []Target{unit}, Examined: []Target{unit}, Tasks: []DesignTask{task}, Findings: []Finding{{Rule: "state", Target: Target{Kind: FileTarget, ID: "store/state.go", Line: 2}}}}}}
	if problems := report.Problems(); len(problems) != 0 {
		t.Fatalf("complete package control rejected: %v", problems)
	}
	report.Checks[0].Tasks[0].Omitted = []Omission{{Target: task.Sources[1], Reason: "budget"}}
	if len(report.Problems()) == 0 {
		t.Fatal("omitted package source claimed complete coverage")
	}
	report.Checks[0].Tasks[0].Omitted = nil
	savedFindings := report.Checks[0].Findings
	report.Checks[0].Findings = nil
	report.Checks[0].Tasks[0].Sources = nil
	report.Checks[0].Tasks[0].SourceDigest = ""
	if len(report.Problems()) == 0 {
		t.Fatal("package task omitted its source list to bypass digest binding")
	}
	report.Checks[0].Tasks[0].Sources = task.Sources
	report.Checks[0].Findings = savedFindings
	report.Checks[0].Tasks[0].SourceDigest = ""
	if len(report.Problems()) == 0 {
		t.Fatal("unbound source claimed complete coverage")
	}
}

func TestDesignPlanRetainsRemovedSourceAndSurvivingPackage(t *testing.T) {
	files, _ := designPlanningFixture()
	files = slices.Delete(files, 1, 2)
	inventory, _ := InspectDesign(files, nil)
	plan := PlanDesign(t.Context(), inventory, files, []string{"store/read.go"})
	if len(plan.Tasks) != 2 {
		t.Fatalf("removed source lost affected package or omission: %+v", plan)
	}
	var removed, surviving bool
	for _, task := range plan.Tasks {
		if task.ID == "source:store/read.go" {
			removed = len(task.Omitted) > 0
		}
		if task.ID == "package:example.com/app/store" {
			surviving = slices.Contains(task.Context, Target{Kind: FileTarget, ID: "service/service.go"})
		}
	}
	if !removed || !surviving {
		t.Fatalf("removed=%t surviving=%t: %+v", removed, surviving, plan)
	}
}
