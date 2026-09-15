package prdiagram

import (
	"strings"
	"testing"
)

func TestMarkdownLinksNodesAndEdgesToEvidence(t *testing.T) {
	g := Graph{
		Nodes:      []Node{{Label: "POST users", Source: Location{"routes.go", 8}}, {Label: "CreateUser", Source: Location{"handlers.go", 12}, Changed: true}},
		Edges:      []Edge{{From: 0, To: 1, Label: "handler", Source: Location{"routes.go", 8}}, {From: 1, To: 1, Label: "dynamic call", Source: Location{"handlers.go", 16}, Inferred: true}},
		Unresolved: []string{"interface target unknown"},
	}
	got, err := g.Markdown("https://github.com/example/app/blob/abc123")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"class n1 changed", "n0 -->", "n1 -.->", "/routes.go#L8", "/handlers.go#L12", "/handlers.go#L16", "Node 0: POST users", "Node 1: CreateUser", "Edge 0: node 0 → node 1; handler", "interface target unknown"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
	if strings.Contains(got, "class n0 changed") {
		t.Fatal("unchanged node highlighted")
	}
}

func TestMarkdownRendersMermaidSafePunctuationAndEmptyEdges(t *testing.T) {
	got, err := (Graph{
		Nodes: []Node{{Label: "POST /users", Source: Location{"api/routes.go", 1}}, {Label: "quoted \"value\"", Source: Location{"api/handler.go", 2}}},
		Edges: []Edge{{From: 0, To: 1, Source: Location{"api/routes.go", 1}}},
	}).Markdown("https://example.com/blob/abc123")
	if err != nil {
		t.Fatal(err)
	}
	_, mermaid, ok := strings.Cut(got, "```mermaid\n")
	if !ok {
		t.Fatalf("missing Mermaid block: %s", got)
	}
	mermaid, _, _ = strings.Cut(mermaid, "\n```")
	if strings.Contains(mermaid, "&#47;") || !strings.Contains(mermaid, "POST #47;users") {
		t.Fatalf("Mermaid labels use the wrong entity syntax: %s", got)
	}
	if !strings.Contains(got, "n0 --> n1") || strings.Contains(got, "|\"\"|") {
		t.Fatalf("empty edge label was not rendered as an unlabeled edge: %s", got)
	}
}

func TestMarkdownUsesPlainEvidenceForAWorkingTree(t *testing.T) {
	got, err := (Graph{Nodes: []Node{{Label: "main", Source: Location{"cmd/main.go", 7}}}}).Markdown("")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "cmd&#47;main&#46;go:7") || strings.Contains(got, "https://") {
		t.Fatalf("working-tree evidence should remain plain: %s", got)
	}
}

func TestMarkdownRejectsBrokenEvidenceAndUnsafeLinks(t *testing.T) {
	for _, loc := range []Location{{"../secret", 1}, {"/absolute", 1}, {"file.go", 0}} {
		if _, err := (Graph{Nodes: []Node{{Source: loc}}}).Markdown("https://example.com/blob/revision"); err == nil {
			t.Errorf("accepted invalid source: %v", loc)
		}
	}
	if _, err := (Graph{Edges: []Edge{{From: 0, To: 1, Source: Location{"a.go", 1}}}}).Markdown("https://example.com/blob/revision"); err == nil {
		t.Fatal("accepted missing nodes")
	}
	if _, err := (Graph{}).Markdown("javascript:alert(1)"); err == nil {
		t.Fatal("accepted executable URL")
	}
}

func TestMarkdownKeepsUntrustedTextInsideLabels(t *testing.T) {
	got, err := (Graph{Nodes: []Node{{Label: "\"]\n```\nclick n0 javascript:bad", Source: Location{"a.go", 1}}}}).Markdown("https://example.com/blob/revision")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(got, "```") != 2 || strings.Contains(got, "\nclick n0") {
		t.Fatalf("label escaped diagram: %s", got)
	}
}

func TestMarkdownEmptyScopeDoesNotClaimCompleteCoverage(t *testing.T) {
	got, err := (Graph{}).Markdown("https://example.com/blob/revision")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "No flow was resolved") || strings.Contains(got, "```mermaid") {
		t.Fatalf("empty diagram implies a trace: %s", got)
	}
}

func TestMarkdownSourcesUsesBaseForRemovedNodes(t *testing.T) {
	graph := Graph{
		Nodes: []Node{
			{Label: "gone", State: "removed", Source: Location{"gone.go", 2}},
			{Label: "current", State: "modified", Source: Location{"current.go", 3}, Changed: true},
		},
		Edges: []Edge{{From: 0, To: 1, Label: "call", Source: Location{"gone.go", 2}, Removed: true}},
	}
	got, err := graph.MarkdownSources("https://github.com/acme/app/blob/head", "https://github.com/acme/app/blob/base")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "/blob/base/gone.go#L2") {
		t.Fatalf("removed evidence did not use base URL: %s", got)
	}
	if !strings.Contains(got, "/blob/head/current.go#L3") {
		t.Fatalf("current evidence did not use head URL: %s", got)
	}
}

func TestMarkdownRendersStateAndBoundaryReason(t *testing.T) {
	got, err := (Graph{Nodes: []Node{{Label: "dynamic", State: "unchanged", Boundary: true, Reason: "dynamic_call", Source: Location{"a.go", 1}}}}).Markdown("")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "boundary&#58; dynamic&#95;call") || !strings.Contains(got, "unchanged") {
		t.Fatalf("state/boundary reason missing: %s", got)
	}
}
