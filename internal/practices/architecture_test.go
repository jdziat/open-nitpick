package practices

import (
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/standards"
)

func TestDesignBoundariesFindForbiddenEdgesAndKeepCleanControls(t *testing.T) {
	files := []standards.File{
		{Path: "go.mod", Src: []byte("module example.com/service\ngo 1.25\n")},
		{Path: "api/api.go", Src: []byte("package api\nimport _ \"example.com/service/db\"\n")},
		{Path: "db/db.go", Src: []byte("package db\n")},
	}
	policy := []config.PracticeBoundary{{From: "example.com/service/api", Forbid: []string{"example.com/service/db"}, Reason: "API depends on the service contract"}}
	inventory, check := InspectDesign(files, policy)
	if check.State != Completed || len(check.Examined) != 2 || len(check.Findings) != 1 || check.Findings[0].Target.Line != 2 {
		t.Fatalf("boundary evidence: %+v", check)
	}
	if len(inventory.Units) != 2 || len(inventory.Units[0].Dependencies) != 1 {
		t.Fatalf("graph=%+v", inventory)
	}
	files[1].Src = []byte("package api\nimport _ \"fmt\"\n")
	_, check = InspectDesign(files, policy)
	if check.State != Completed || len(check.Examined) != 2 || len(check.Findings) != 0 {
		t.Fatalf("clean control=%+v", check)
	}
}

func TestDesignInventoryRespectsNestedModulesAndMissingMetadata(t *testing.T) {
	files := []standards.File{
		{Path: "go.mod", Src: []byte("module example.com/root\n")},
		{Path: "tools/go.mod", Src: []byte("module example.com/tools\n")},
		{Path: "tools/tool.go", Src: []byte("package tools\n")},
	}
	inventory, check := InspectDesign(files, nil)
	if check.State != NotApplicable || len(inventory.Units) != 1 || inventory.Units[0].ID != "example.com/tools" {
		t.Fatalf("nested inventory=%+v", inventory)
	}
	files = files[2:]
	_, check = InspectDesign(files, []config.PracticeBoundary{{From: "*", Forbid: []string{"*"}, Reason: "fixture"}})
	if check.State != Partial || len(check.Examined) != 0 {
		t.Fatalf("missing module silently passed: %+v", check)
	}
}

func TestMalformedNestedModuleCannotInheritParentIdentity(t *testing.T) {
	files := []standards.File{
		{Path: "go.mod", Src: []byte("module example.com/root\n")},
		{Path: "tools/go.mod", Src: []byte("module\n")},
		{Path: "tools/tool.go", Src: []byte("package tools\n")},
	}
	inventory, check := InspectDesign(files, []config.PracticeBoundary{{From: "*", Forbid: []string{"*"}, Reason: "fixture"}})
	if check.State != Partial || len(check.Examined) != 0 || len(inventory.Units) != 0 {
		t.Fatalf("malformed nested module acquired parent identity: %+v %+v", inventory, check)
	}
}

func TestUnresolvedLocalImportsRemainDistinctFromExternalDependencies(t *testing.T) {
	files := []standards.File{
		{Path: "go.mod", Src: []byte("module example.com/root\n")},
		{Path: "main.go", Src: []byte("package main\nimport (\n_ \"example.com/root/missing\"\n_ \"example.com/rootish/external\"\n)\n")},
	}
	inventory, _ := InspectDesign(files, nil)
	if len(inventory.Units) != 1 {
		t.Fatalf("missing source unit: %+v", inventory)
	}
	unit := inventory.Units[0]
	if len(unit.Unresolved) != 1 || unit.Unresolved[0] != "example.com/root/missing" || len(unit.External) != 1 || unit.External[0] != "example.com/rootish/external" {
		t.Fatalf("missing local context mislabeled: %+v", unit)
	}
}

func TestInvalidBoundaryPatternCannotReportCompletedCoverage(t *testing.T) {
	_, check := InspectDesign([]standards.File{{Path: "go.mod", Src: []byte("module example.com/app\n")}, {Path: "a.go", Src: []byte("package app\n")}}, []config.PracticeBoundary{{From: "[", Forbid: []string{"*"}, Reason: "fixture"}})
	if check.State != Failed || check.Reason == "" {
		t.Fatalf("invalid boundary reported clean: %+v", check)
	}
}
