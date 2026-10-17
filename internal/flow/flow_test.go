package flow

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/diff"
	"github.com/jdziat/open-nitpick/internal/standards"
)

func src(name, body string) standards.File {
	return standards.File{Path: name, Src: []byte(body)}
}

// edit reports lines [from, to] of name as added.
func edit(name string, from, to int) diff.Files {
	var lines []diff.Line
	for n := from; n <= to; n++ {
		lines = append(lines, diff.Line{Kind: diff.LineAdded, NewLine: n})
	}
	return diff.Files{{Path: name, Kind: diff.ChangeModified, Hunks: []diff.Hunk{{Lines: lines}}}}
}

func chain() []standards.File {
	return []standards.File{
		src("go.mod", "module example.com/app\n\ngo 1.22\n"),
		src("cmd/app/main.go", "package main\n\nimport \"example.com/app/internal/run\"\n\nfunc main() { run.Dispatch() }\n"),
		src("internal/run/run.go", "package run\n\nimport \"example.com/app/internal/analyze\"\n\nfunc Dispatch() { analyze.Check() }\n"),
		src("internal/analyze/analyze.go", "package analyze\n\nimport \"os\"\n\nfunc Check() { write() }\n\nfunc write() {\n\tos.WriteFile(\"x\", nil, 0o600)\n}\n\nfunc unrelated() {}\n"),
	}
}

func TestBuildShowsTheCommandThatReachesAChangedFunction(t *testing.T) {
	d := Build(chain(), edit("internal/analyze/analyze.go", 5, 5))
	if d == nil {
		t.Fatal("no diagram for a function with callers and callees")
	}
	if len(d.Changed) != 1 || d.Changed[0] != "internal/analyze.Check" {
		t.Fatalf("changed = %v", d.Changed)
	}
	if len(d.Entries) != 1 || d.Entries[0] != "cmd/app.main" {
		t.Fatalf("entries = %v, want the main function that starts the chain", d.Entries)
	}
	for _, want := range []string{"flowchart LR", "internal/run.Dispatch", "internal/analyze.write", "os.WriteFile", ":::changed", ":::entry", ":::external"} {
		if !strings.Contains(d.Mermaid, want) {
			t.Errorf("diagram lacks %q:\n%s", want, d.Mermaid)
		}
	}
}

func TestBuildDrawsNothingForAnEditOutsideAnyConnectedFunction(t *testing.T) {
	if d := Build(chain(), edit("internal/analyze/analyze.go", 10, 10)); d != nil {
		t.Fatalf("an unconnected function drew a diagram:\n%s", d.Mermaid)
	}
	if d := Build(chain(), edit("internal/analyze/analyze.go", 1, 3)); d != nil {
		t.Fatalf("an edit outside every function drew a diagram:\n%s", d.Mermaid)
	}
	if d := Build(chain(), edit("README.md", 1, 3)); d != nil {
		t.Fatalf("a non-Go edit drew a diagram:\n%s", d.Mermaid)
	}
}

func TestBuildKeepsSourceTextOutOfTheMermaidSyntax(t *testing.T) {
	files := []standards.File{
		src("go.mod", "module example.com/app\n"),
		src("a\"]-->evil[\"x/a.go", "package a\n\nfunc Top() { Leaf() }\n\nfunc Leaf() {}\n"),
	}
	d := Build(files, edit("a\"]-->evil[\"x/a.go", 5, 5))
	if d == nil {
		t.Fatal("no diagram")
	}
	for _, line := range strings.Split(d.Mermaid, "\n") {
		if strings.Contains(line, "[\"") && strings.Count(line, "\"") != 2 {
			t.Errorf("a label escaped its quotes: %s", line)
		}
		if strings.Contains(line, "evil") && strings.Contains(line, "-->") && strings.Contains(line, "evil[") {
			t.Errorf("source text added an edge: %s", line)
		}
	}
}

func TestBuildStopsAtTheNodeCeilingAndSaysSo(t *testing.T) {
	var b strings.Builder
	b.WriteString("package p\n\nfunc Leaf() {}\n")
	for i := range maxNodes + 10 {
		fmt.Fprintf(&b, "\nfunc Caller%d() { Leaf() }\n", i)
	}
	files := []standards.File{src("go.mod", "module example.com/app\n"), src("p/p.go", b.String())}
	d := Build(files, edit("p/p.go", 3, 3))
	if d == nil {
		t.Fatal("no diagram")
	}
	if d.Nodes != maxNodes || !d.Truncated {
		t.Fatalf("nodes = %d truncated = %v, want %d and true", d.Nodes, d.Truncated, maxNodes)
	}
}

func TestBuildIgnoresACallThroughAShadowingParameter(t *testing.T) {
	files := []standards.File{
		src("go.mod", "module example.com/app\n"),
		src("p/p.go", "package p\n\nfunc Leaf() {}\n\nfunc Top(Leaf func()) { Leaf() }\n"),
	}
	if d := Build(files, edit("p/p.go", 3, 3)); d != nil {
		t.Fatalf("a parameter named Leaf was drawn as a call to the function Leaf:\n%s", d.Mermaid)
	}
}
