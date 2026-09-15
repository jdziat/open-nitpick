package prflow

import (
	"context"
	"testing"
)

func TestAnalyzeNamesCommandDispatchAndFilesystemBoundary(t *testing.T) {
	source := `package main
import "os"
func main() { run() }
func run() {
 switch command := os.Args[1]; command {
 case "review": changed()
 case "unused": unrelated()
 }
}
func changed() { _ = os.WriteFile("out", nil, 0600) }
func unrelated() {}
`
	result, err := Analyze(context.Background(), Request{Files: []SourceFile{{Path: "main.go", Content: []byte(source), ChangedLines: []int{10}}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Flows) != 1 || result.Flows[0].Name != "main.main" {
		t.Fatalf("flow lacks its command root: %+v", result.Flows)
	}
	dispatch, filesystem := false, false
	for _, edge := range result.Edges {
		dispatch = dispatch || edge.Kind == "command: review" && edge.Resolution == ResolutionResolved
		filesystem = filesystem || edge.Kind == "filesystem" && edge.Resolution == ResolutionResolved
		if edge.To == "main.unrelated" {
			t.Fatalf("unrelated sibling command included in changed flow: %+v", edge)
		}
	}
	if !dispatch || !filesystem {
		t.Fatalf("dispatch=%v filesystem=%v edges=%+v coverage=%+v", dispatch, filesystem, result.Edges, result.Coverage)
	}
}

func TestAnalyzeKeepsZeroTraversalDepthAtChangedRoot(t *testing.T) {
	result, err := Analyze(context.Background(), Request{
		Files:   []SourceFile{{Path: "p.go", Content: []byte("package p\nfunc Caller(){Changed()}\nfunc Changed(){Callee()}\nfunc Callee(){}\n"), ChangedLines: []int{3}}},
		Options: Options{DepthLimitsSet: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Nodes) != 1 || result.Nodes[0].Label != "p.Changed" || len(result.Edges) != 0 || !hasOmission(result.Coverage.Omissions, "limit_reached") {
		t.Fatalf("zero depths escaped changed root: %+v", result)
	}
}

func TestAnalyzeHandlesExpressionlessSwitchInCLI(t *testing.T) {
	result, err := Analyze(context.Background(), Request{Files: []SourceFile{{Path: "main.go", Content: []byte("package main\nfunc main(){switch { case true: helper() }}\nfunc helper(){}\n"), ChangedLines: []int{2}}}})
	if err != nil || len(result.Edges) != 1 {
		t.Fatalf("expressionless switch flow: %+v, error: %v", result, err)
	}
}

func TestAnalyzeNamesLibraryFlowByChangedSymbolWithoutCLIRoot(t *testing.T) {
	result, err := Analyze(context.Background(), Request{Files: []SourceFile{{
		Path: "lib.go", ChangedLines: []int{3}, Content: []byte("package p\nfunc main() { changed() }\nfunc changed() {}\n"),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Flows) != 1 || result.Flows[0].Name != "p.changed" {
		t.Fatalf("library flow chose a non-root main-like symbol: %+v", result.Flows)
	}
	if result.Status != StatusComplete || !hasOmission(result.Coverage.Omissions, "root_not_found") {
		t.Fatalf("library root coverage = %+v, want complete with root_not_found", result)
	}
}
