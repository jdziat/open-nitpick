package prflow

import (
	"context"
	"strings"
	"testing"
)

func TestAnalyzeResolvesImportedPackageByModulePath(t *testing.T) {
	result, err := Analyze(context.Background(), Request{
		Modules: []Module{{Path: "example.com/app"}},
		Files: []SourceFile{
			{Path: "cmd/main.go", Content: []byte("package main\nimport \"example.com/app/internal/work\"\nfunc Changed() { work.Run() }\n"), ChangedLines: []int{3}},
			{Path: "internal/work/run.go", Content: []byte("package work\nfunc Run() {}\n")},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, edge := range result.Edges {
		if strings.HasSuffix(edge.From, ".Changed") && strings.HasSuffix(edge.To, "work.Run") {
			if edge.Resolution != ResolutionResolved {
				t.Fatalf("imported edge resolution = %s, want resolved: %+v", edge.Resolution, edge)
			}
			return
		}
	}
	t.Fatalf("imported package edge missing: %+v", result.Edges)
}

func TestAnalyzeDoesNotGuessSelectorByShortName(t *testing.T) {
	result, err := Analyze(context.Background(), Request{
		Files: []SourceFile{
			{Path: "one/a.go", Content: []byte("package one\nfunc Run() {}\n")},
			{Path: "two/a.go", Content: []byte("package two\nimport \"missing.example/one\"\nfunc Changed() { one.Run() }\n"), ChangedLines: []int{3}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusPartial {
		t.Fatalf("missing import status = %s, want partial: %+v", result.Status, result.Coverage.Omissions)
	}
	for _, edge := range result.Edges {
		if strings.HasSuffix(edge.From, ".Changed") && edge.To == "one/one.Run" {
			t.Fatalf("selector was guessed across packages: %+v", edge)
		}
	}
	for _, node := range result.Nodes {
		if node.Boundary && strings.Contains(node.Reason, "missing_import") {
			return
		}
	}
	t.Fatalf("missing-import boundary absent: status=%s omissions=%+v nodes=%+v edges=%+v", result.Status, result.Coverage.Omissions, result.Nodes, result.Edges)
}

func TestAnalyzeSkipsBuiltinsAndTypeConversions(t *testing.T) {
	result, err := Analyze(context.Background(), Request{Files: []SourceFile{{
		Path: "p.go", ChangedLines: []int{2}, Content: []byte("package p\nfunc Changed() { _ = len([]int{}); _ = int(1) }\n"),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Edges) != 0 || result.Status != StatusNoFlow {
		t.Fatalf("language operations became flow edges: status=%s edges=%+v", result.Status, result.Edges)
	}
}

func TestAnalyzePreservesInterfaceCallAsUnresolved(t *testing.T) {
	result, err := Analyze(context.Background(), Request{Files: []SourceFile{{
		Path: "p.go", ChangedLines: []int{5}, Content: []byte("package p\ntype I interface { Run() }\ntype impl struct{}\nfunc (impl) Run() {}\nfunc Changed(i I) { i.Run() }\n"),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, edge := range result.Edges {
		if strings.HasSuffix(edge.From, ".Changed") && edge.Reason == "interface_method" {
			if edge.Resolution != ResolutionUnresolved {
				t.Fatalf("interface call resolution = %s, want unresolved: %+v", edge.Resolution, edge)
			}
			return
		}
	}
	t.Fatalf("interface boundary missing: %+v", result.Edges)
}

func TestAnalyzeReportsTypeCheckErrorsAsPartial(t *testing.T) {
	result, err := Analyze(context.Background(), Request{Files: []SourceFile{{
		Path: "p.go", ChangedLines: []int{2}, Content: []byte("package p\nfunc Changed() { Missing() }\n"),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusPartial || !hasOmission(result.Coverage.Omissions, "type_check_error") {
		t.Fatalf("type-check failure was hidden: status=%s omissions=%+v", result.Status, result.Coverage.Omissions)
	}
}

func TestAnalyzeMarksUnresolvedEdgesAsPartialCoverage(t *testing.T) {
	result, err := Analyze(context.Background(), Request{Files: []SourceFile{{
		Path: "p.go", ChangedLines: []int{2}, Content: []byte("package p\nfunc Changed() {\n var callback func()\n callback()\n callback()\n}\n"),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusPartial {
		t.Fatalf("unresolved edge status = %s, want partial: %+v", result.Status, result)
	}
	dynamic := 0
	for _, omission := range result.Coverage.Omissions {
		if omission.Reason == "dynamic_call" {
			dynamic = omission.Count
		}
	}
	if dynamic != 2 {
		t.Fatalf("call-site omissions = %+v, want two dynamic calls", result.Coverage.Omissions)
	}
}

func TestAnalyzeKeepsResolvedExternalCallsComplete(t *testing.T) {
	result, err := Analyze(context.Background(), Request{Files: []SourceFile{{
		Path: "p.go", ChangedLines: []int{3}, Content: []byte("package p\nimport \"os\"\nfunc Changed() { _ = os.Remove(\"example\") }\n"),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Edges) != 1 || result.Edges[0].Resolution != ResolutionResolved || result.Edges[0].Reason != "external_call" {
		t.Fatalf("external target was not resolved: %+v", result)
	}
	if result.Status != StatusComplete || hasOmission(result.Coverage.Omissions, "unresolved_target") {
		t.Fatalf("resolved boundary reduced coverage: %+v", result)
	}
}

func TestAnalyzeHonorsFilenameBuildConstraints(t *testing.T) {
	result, err := Analyze(context.Background(), Request{Files: []SourceFile{
		{Path: "p_linux.go", ChangedLines: []int{2}, Content: []byte("package p\nfunc Changed() {}\n")},
		{Path: "p_windows.go", ChangedLines: []int{2}, Content: []byte("package p\nfunc Other() {}\n")},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range result.Nodes {
		if strings.Contains(node.Source.Path, "windows") {
			t.Fatalf("inactive GOOS file was selected: %+v", node)
		}
	}
}

func TestAnalyzeRecognizesReleaseBuildTag(t *testing.T) {
	result, err := Analyze(context.Background(), Request{Files: []SourceFile{{
		Path: "p.go", ChangedLines: []int{4}, Content: []byte("//go:build go1.25\n\npackage p\nfunc Changed() {}\n"),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status == StatusUnavailable || len(result.Nodes) == 0 {
		t.Fatalf("release tag was not recognized: %+v", result)
	}
}

func TestAnalyzeHonorsLegacyPlusBuildConstraint(t *testing.T) {
	result, err := Analyze(context.Background(), Request{Files: []SourceFile{{
		Path: "p.go", ChangedLines: []int{3}, Content: []byte("// +build windows\n\npackage p\nfunc Changed() {}\n"),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusUnavailable || !hasOmission(result.Coverage.Omissions, "build_tags") {
		t.Fatalf("legacy build constraint was ignored: %+v", result)
	}
}
