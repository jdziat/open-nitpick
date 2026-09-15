// Command flow-mermaid-fixture emits the real prdiagram Markdown used by the
// Mermaid syntax smoke. Keeping the graph here makes the smoke exercise the
// renderer's escaping and edge styles instead of parsing a hand-written graph.
package main

import (
	"fmt"
	"os"

	"github.com/jdziat/open-nitpick/internal/prdiagram"
)

func main() {
	markdown, err := (prdiagram.Graph{
		Nodes: []prdiagram.Node{
			{Label: "POST /users [x] \"quoted\" <tag> & pipe | `tick` …", State: "unchanged", Source: prdiagram.Location{Path: "routes.go", Line: 8}},
			{Label: "changed -> \"value\"", State: "modified", Source: prdiagram.Location{Path: "handler.go", Line: 21}, Changed: true},
			{Label: "boundary: reflection target", Boundary: true, Reason: "reflection", Source: prdiagram.Location{Path: "handler.go", Line: 34}},
			{Label: "unresolved [generated]", State: "removed", Source: prdiagram.Location{Path: "generated.go", Line: 5}},
		},
		Edges: []prdiagram.Edge{
			{From: 0, To: 1, Label: "resolved | \"quoted\"", Source: prdiagram.Location{Path: "routes.go", Line: 8}},
			{From: 1, To: 2, Label: "callback / inferred", Source: prdiagram.Location{Path: "handler.go", Line: 27}, Inferred: true},
			{From: 2, To: 3, Reason: "dynamic dispatch", Unresolved: true, Removed: true, Source: prdiagram.Location{Path: "handler.go", Line: 34}},
		},
		Unresolved: []string{"reflection target <unknown>", "generated metadata | unavailable"},
	}).Markdown("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "render flow fixture: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(markdown)
}
