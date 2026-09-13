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
	for _, want := range []string{"class n1 changed", "n0 -->", "n1 -.->", "/routes.go#L8", "/handlers.go#L16", "Edge 0", "interface target unknown"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
	if strings.Contains(got, "class n0 changed") {
		t.Fatal("unchanged node highlighted")
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
