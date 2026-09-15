package prflow

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestResultMarkdownRendersDiagramAndBoundaryEvidence(t *testing.T) {
	result := Result{
		Version: 1,
		Status:  StatusPartial,
		Head:    Revision{SHA: "deadbeef"},
		Nodes: []Node{
			{ID: "main.run", Label: "main.run", Source: Location{Path: "cmd/main.go", Line: 4}, Changed: true},
			{ID: "boundary:callback", Label: "callback", Source: Location{Path: "cmd/main.go", Line: 5}, Boundary: true, Reason: "dynamic_call"},
		},
		Edges:    []Edge{{ID: "edge", From: "main.run", To: "boundary:callback", Kind: "dynamic", Resolution: ResolutionUnresolved, Reason: "dynamic_call", Source: Location{Path: "cmd/main.go", Line: 5}}},
		Coverage: Coverage{Omissions: []Omission{{Reason: "max_nodes", Count: 1}}},
	}
	markdown, err := result.Markdown("https://github.example/project/blob/deadbeef")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Status: **partial**", "```mermaid", "main#46;run", "/cmd/main.go#L4", "dynamic&#95;call", "max&#95;nodes&#58; 1"} {
		if !strings.Contains(markdown, want) {
			t.Errorf("markdown missing %q:\n%s", want, markdown)
		}
	}
}

func TestResultMarkdownRejectsInconsistentEdges(t *testing.T) {
	_, err := (Result{Status: StatusComplete, Nodes: []Node{{ID: "known", Source: Location{Path: "a.go", Line: 1}}}, Edges: []Edge{{ID: "broken", From: "known", To: "missing", Source: Location{Path: "a.go", Line: 1}}}}).Markdown("")
	if err == nil || !strings.Contains(err.Error(), "outside its result") {
		t.Fatalf("Markdown error = %v, want inconsistent edge", err)
	}
}

func TestResultMarkdownCapsTheInlineDiagram(t *testing.T) {
	result := Result{Status: StatusComplete}
	for i := 0; i < maxInlineNodes+1; i++ {
		id := fmt.Sprintf("p.F%d", i)
		result.Nodes = append(result.Nodes, Node{ID: id, Label: id, Source: Location{Path: "p.go", Line: i + 1}, Changed: i == 0})
		if i > 0 {
			result.Edges = append(result.Edges, Edge{ID: fmt.Sprintf("e%d", i), From: "p.F0", To: id, Kind: "call", Resolution: ResolutionResolved, Source: Location{Path: "p.go", Line: i + 1}})
		}
	}
	markdown, err := result.Markdown("")
	if err != nil {
		t.Fatal(err)
	}
	if len(markdown) > maxMarkdownBytes || !strings.Contains(markdown, "inline display limit") {
		t.Fatalf("inline render was not bounded: %d bytes\n%s", len(markdown), markdown)
	}
}

func TestResultMarkdownUsesBaseEvidenceForRemovedNodes(t *testing.T) {
	result := Result{
		Status: StatusComplete,
		Base:   Revision{SHA: "base1234"},
		Head:   Revision{SHA: "head5678"},
		Nodes: []Node{
			{ID: "gone", Label: "gone", Source: Location{Path: "gone.go", Line: 3}, State: ChangeRemoved, Changed: true},
			{ID: "current", Label: "current", Source: Location{Path: "current.go", Line: 4}, State: ChangeModified, Changed: true},
		},
		Edges: []Edge{{ID: "edge", From: "gone", To: "current", Kind: "call", Resolution: ResolutionResolved, Source: Location{Path: "gone.go", Line: 3}}},
	}
	markdown, err := result.MarkdownSources("https://github.com/acme/app/blob/head5678", "https://github.com/acme/app/blob/base1234")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(markdown, "/blob/base1234/gone.go#L3") {
		t.Fatalf("removed node did not use base source: %s", markdown)
	}
	if !strings.Contains(markdown, "/blob/head5678/current.go#L4") {
		t.Fatalf("current node did not use head source: %s", markdown)
	}
	if !strings.Contains(markdown, "/blob/base1234/gone.go#L3") {
		t.Fatalf("removed edge did not use base source: %s", markdown)
	}
}

func TestResultMarkdownRetainsEvidenceWhenMermaidIsDropped(t *testing.T) {
	result := Result{Status: StatusPartial}
	for i := 0; i < 40; i++ {
		result.Nodes = append(result.Nodes, Node{ID: fmt.Sprintf("p.F%d", i), Label: strings.Repeat("large label ", 120), Source: Location{Path: fmt.Sprintf("p/%02d.go", i), Line: i + 1}, State: ChangeUnchanged})
	}
	markdown, err := result.Markdown("")
	if err != nil {
		t.Fatal(err)
	}
	if len(markdown) > maxMarkdownBytes {
		t.Fatalf("markdown exceeded bound: %d", len(markdown))
	}
	if !strings.Contains(markdown, "Mermaid was omitted") || !strings.Contains(markdown, "p&#47;00&#46;go") {
		t.Fatalf("bounded fallback lost status or source evidence: %s", markdown[:min(len(markdown), 1000)])
	}
}

func TestResultMarkdownRendersNamedFlowComponents(t *testing.T) {
	result := Result{
		Status: StatusComplete,
		Nodes: []Node{
			{ID: "a", Label: "first", Source: Location{Path: "a.go", Line: 1}, State: ChangeModified, Changed: true},
			{ID: "b", Label: "second", Source: Location{Path: "b.go", Line: 2}, State: ChangeModified, Changed: true},
		},
		Flows: []Flow{
			{ID: "flow:b", Name: "Second flow", Nodes: []string{"b"}},
			{ID: "flow:a", Name: "First flow", Nodes: []string{"a"}},
		},
	}
	markdown, err := result.Markdown("")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(markdown, "```mermaid"); got != 2 {
		t.Fatalf("Mermaid component count = %d, want 2: %s", got, markdown)
	}
	if strings.Index(markdown, "First flow") > strings.Index(markdown, "Second flow") {
		t.Fatalf("flow components were not stable-sorted: %s", markdown)
	}
}

func TestTrimUTF8DoesNotLeavePartialRune(t *testing.T) {
	got := trimUTF8("prefix—suffix", len("prefix—")-1)
	if !strings.HasPrefix(got, "prefix") || !utf8.ValidString(got) {
		t.Fatalf("trimUTF8 returned invalid or unexpected text %q", got)
	}
}

func TestResultMarkdownKeepsEvidenceOutsideDisplayedFlows(t *testing.T) {
	result := Result{
		Version: 1, Generator: "open-nitpick/prflow-v1", Head: Revision{SHA: "deadbeef"}, OptionsDigest: "options123",
		Status: StatusPartial,
		Nodes: []Node{
			{ID: "a", Label: "First", Source: Location{Path: "a.go", Line: 1}, Changed: true},
			{ID: "b", Label: "Second", Source: Location{Path: "b.go", Line: 2}, Changed: true},
		},
		Flows: []Flow{{ID: "flow:a", Name: "First", Nodes: []string{"a"}}},
	}
	markdown, err := result.Markdown("")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"b&#46;go:2", "<!-- nitpick:flow ", `"version":1`, `"generator":"open-nitpick/prflow-v1"`, `"head":"deadbeef"`, `"options":"options123"`} {
		if !strings.Contains(markdown, want) {
			t.Errorf("omitted flow evidence or publication identity %q:\n%s", want, markdown)
		}
	}
}

func TestChangedMarkdownRetainsOnlyChangedRevisionEvidence(t *testing.T) {
	result := Result{Version: 1, Status: StatusPartial, Nodes: []Node{
		{ID: "a", Label: "Changed", Changed: true, State: ChangeModified, Source: Location{Path: "a.go", Line: 2}},
		{ID: "b", Label: "Context", State: ChangeUnchanged, Source: Location{Path: "b.go", Line: 3}},
		{ID: "c", Label: "Removed", Changed: true, State: ChangeRemoved, Source: Location{Path: "c.go", Line: 4}},
	}}
	markdown, err := result.ChangedMarkdownSources("https://github.com/fork/app/blob/deadbeef", "https://github.com/base/app/blob/abcdef01")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Status: **partial**", "static source relationships", "/fork/app/blob/deadbeef/a.go#L2", "/base/app/blob/abcdef01/c.go#L4"} {
		if !strings.Contains(markdown, want) {
			t.Errorf("compact evidence omitted %q: %s", want, markdown)
		}
	}
	if strings.Contains(markdown, "```mermaid") || strings.Contains(markdown, "Context") || strings.Contains(markdown, "b.go") {
		t.Fatalf("compact evidence retained diagram or unchanged context: %s", markdown)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
