package prflow

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/jdziat/open-nitpick/internal/prdiagram"
)

const (
	maxInlineNodes   = 30
	maxInlineEdges   = 50
	maxMarkdownBytes = 48 << 10
)

// Markdown renders the versioned result for a review body or local command.
// A nonempty sourceBase must be the provider's immutable revision URL; an
// empty one retains plain path:line evidence for a local working tree.
func (r Result) Markdown(sourceBase string) (string, error) {
	return r.MarkdownSources(sourceBase, "")
}

// MarkdownSources renders current evidence against headBase and removed
// declarations against baseBase. Empty bases intentionally produce plain
// path:line evidence for local or unavailable-source-link runs.
func (r Result) MarkdownSources(headBase, baseBase string) (string, error) {
	// The graph renderer validates both optional bases before emitting links.
	var b strings.Builder
	b.WriteString("## Application flows (static analysis)\n\n")
	r.writeMarker(&b)
	fmt.Fprintf(&b, "Status: **%s**", r.Status)
	if r.Explanation != "" {
		fmt.Fprintf(&b, " — %s", inline(r.Explanation))
	}
	b.WriteString("\n\n")
	switch {
	case r.hasRemoved() && r.Base.SHA != "":
		head := "working tree"
		if r.Head.SHA != "" {
			head = "`" + inline(r.Head.SHA) + "`"
		}
		fmt.Fprintf(&b, "Source revisions: base `%s` and head %s. Removed declarations use base-side evidence. This describes source relationships, not runtime order or complete application coverage.\n\n", inline(r.Base.SHA), head)
	case r.Head.SHA != "":
		fmt.Fprintf(&b, "Source revision: `%s`. This describes source relationships, not runtime order or complete application coverage.\n\n", inline(r.Head.SHA))
	default:
		b.WriteString("Source revision: working tree. This describes source relationships, not runtime order or complete application coverage.\n\n")
	}
	appendCoverage(&b, r.Coverage)
	if len(r.Nodes) == 0 {
		if r.Status == StatusNoFlow || r.Status == StatusSkipped {
			b.WriteString("No path was resolved. This is not evidence that the change has no effect.\n")
		}
		return b.String(), nil
	}

	fullGraph, err := r.graph()
	if err != nil {
		return "", err
	}
	flows := append([]Flow(nil), r.Flows...)
	sort.SliceStable(flows, func(i, j int) bool { return flows[i].ID < flows[j].ID })
	if len(flows) == 0 {
		flows = []Flow{{ID: "flow:all", Name: "Application flow", Nodes: nodeIDs(r.Nodes), Edges: edgeIDs(r.Edges)}}
	}
	shown := make(map[string]bool, len(r.Nodes))
	for _, flow := range flows {
		for _, id := range flow.Nodes {
			shown[id] = true
		}
	}
	omittedInline := len(shown) < len(r.Nodes)
	for _, flow := range flows {
		flowGraph := graphForFlow(fullGraph, flow)
		displayGraph := inlineGraph(flowGraph)
		diagram, diagramErr := displayGraph.MarkdownSources(headBase, baseBase)
		if diagramErr != nil {
			return "", diagramErr
		}
		if len(displayGraph.Nodes) != len(flowGraph.Nodes) || len(displayGraph.Edges) != len(flowGraph.Edges) {
			omittedInline = true
		}
		fmt.Fprintf(&b, "<details><summary>%s (%d node(s), %d edge(s))</summary>\n\n", inline(flow.Name), len(flowGraph.Nodes), len(flowGraph.Edges))
		b.WriteString(strings.TrimPrefix(diagram, "## Changed application flow\n\n"))
		b.WriteString("\n</details>\n")
	}
	if omittedInline {
		// Keep every node and edge's evidence available even when Mermaid is
		// reduced to the small, readable inline graph.
		fullEvidence, evidenceErr := fullGraph.MarkdownSources(headBase, baseBase)
		if evidenceErr != nil {
			return "", evidenceErr
		}
		fullEvidence = stripMermaid(fullEvidence)
		b.WriteString("\n<details><summary>Complete source evidence</summary>\n\n")
		b.WriteString(strings.TrimPrefix(fullEvidence, "## Changed application flow\n\n"))
		b.WriteString("\n</details>\n")
	}
	if b.Len() > maxMarkdownBytes {
		return r.textEvidence(headBase, baseBase), nil
	}
	return b.String(), nil
}

func (r Result) hasRemoved() bool {
	for _, node := range r.Nodes {
		if node.State == ChangeRemoved {
			return true
		}
	}
	return false
}

// ChangedMarkdownSources retains changed declaration evidence when a review
// body has room for a compact table but not diagrams or unchanged context.
func (r Result) ChangedMarkdownSources(headBase, baseBase string) (string, error) {
	var b strings.Builder
	b.WriteString("## Application flows (static analysis)\n\n")
	r.writeMarker(&b)
	fmt.Fprintf(&b, "Status: **%s**\n\n", r.Status)
	b.WriteString("Diagrams and unchanged context were omitted to fit the review body. These are static source relationships, not runtime order or complete application coverage.\n\n")
	appendCoverage(&b, r.Coverage)
	graph := prdiagram.Graph{}
	for _, node := range r.Nodes {
		if !node.Changed {
			continue
		}
		graph.Nodes = append(graph.Nodes, prdiagram.Node{ID: node.ID, Label: node.Label, State: string(node.State), Changed: true, Source: prdiagram.Location{Path: node.Source.Path, Line: node.Source.Line}})
	}
	evidence, err := graph.MarkdownSources(headBase, baseBase)
	if err != nil {
		return "", err
	}
	if start := strings.Index(evidence, "| Element | Source evidence |"); start >= 0 {
		b.WriteString(evidence[start:])
	}
	return b.String(), nil
}

func (r Result) graph() (prdiagram.Graph, error) {
	g := prdiagram.Graph{}
	index := make(map[string]int, len(r.Nodes))
	for _, n := range r.Nodes {
		if _, exists := index[n.ID]; exists {
			return prdiagram.Graph{}, fmt.Errorf("flow result contains duplicate node %q", n.ID)
		}
		index[n.ID] = len(g.Nodes)
		g.Nodes = append(g.Nodes, prdiagram.Node{ID: n.ID, Label: n.Label, Source: prdiagram.Location{Path: n.Source.Path, Line: n.Source.Line}, Changed: n.Changed, State: string(n.State), Boundary: n.Boundary, Reason: n.Reason})
	}
	for _, edge := range r.Edges {
		from, fromOK := index[edge.From]
		to, toOK := index[edge.To]
		if !fromOK || !toOK {
			return prdiagram.Graph{}, fmt.Errorf("flow edge %q refers to a node outside its result", edge.ID)
		}
		label := edge.Kind
		if edge.Resolution != ResolutionResolved {
			label += " (" + string(edge.Resolution) + ")"
		}
		removed := r.Nodes[from].State == ChangeRemoved || r.Nodes[to].State == ChangeRemoved
		g.Edges = append(g.Edges, prdiagram.Edge{ID: edge.ID, From: from, To: to, Label: label, Source: prdiagram.Location{Path: edge.Source.Path, Line: edge.Source.Line}, Inferred: edge.Resolution == ResolutionInferred, Unresolved: edge.Resolution == ResolutionUnresolved, Removed: removed, Reason: edge.Reason})
		if edge.Resolution == ResolutionUnresolved && edge.Reason != "" {
			g.Unresolved = append(g.Unresolved, edge.Reason)
		}
	}
	for _, omission := range r.Coverage.Omissions {
		g.Unresolved = append(g.Unresolved, fmt.Sprintf("%s: %d", omission.Reason, omission.Count))
	}
	return g, nil
}

func nodeIDs(nodes []Node) []string {
	ids := make([]string, 0, len(nodes))
	for _, node := range nodes {
		ids = append(ids, node.ID)
	}
	return ids
}

func edgeIDs(edges []Edge) []string {
	ids := make([]string, 0, len(edges))
	for _, edge := range edges {
		ids = append(ids, edge.ID)
	}
	return ids
}

func graphForFlow(full prdiagram.Graph, flow Flow) prdiagram.Graph {
	nodeSet := make(map[string]bool, len(flow.Nodes))
	for _, id := range flow.Nodes {
		nodeSet[id] = true
	}
	// Flow node IDs are the source result IDs, while prdiagram intentionally
	// stores compact indexes. The stable order from Result.graph is retained.
	selected := make([]int, 0, len(nodeSet))
	for i, node := range full.Nodes {
		if nodeSet[flowNodeID(node)] {
			selected = append(selected, i)
		}
	}
	if len(selected) == 0 {
		// A malformed or future flow result should still render a bounded empty
		// component rather than panic or claim evidence for another component.
		return prdiagram.Graph{}
	}
	index := make(map[int]int, len(selected))
	out := prdiagram.Graph{Nodes: make([]prdiagram.Node, len(selected)), Unresolved: append([]string(nil), full.Unresolved...)}
	for i, old := range selected {
		index[old] = i
		out.Nodes[i] = full.Nodes[old]
	}
	edgeSet := make(map[string]bool, len(flow.Edges))
	for _, id := range flow.Edges {
		edgeSet[id] = true
	}
	for _, edge := range full.Edges {
		if len(edgeSet) > 0 && !edgeSet[diagramEdgeID(edge)] {
			continue
		}
		from, fromOK := index[edge.From]
		to, toOK := index[edge.To]
		if !fromOK || !toOK {
			continue
		}
		edge.From, edge.To = from, to
		out.Edges = append(out.Edges, edge)
	}
	return out
}

func flowNodeID(node prdiagram.Node) string    { return node.ID }
func diagramEdgeID(edge prdiagram.Edge) string { return edge.ID }

func inlineGraph(g prdiagram.Graph) prdiagram.Graph {
	if len(g.Nodes) <= maxInlineNodes && len(g.Edges) <= maxInlineEdges {
		return g
	}
	chosen := make(map[int]int, maxInlineNodes)
	for _, changed := range []bool{true, false} {
		for old, node := range g.Nodes {
			if node.Changed != changed || len(chosen) >= maxInlineNodes {
				continue
			}
			if _, exists := chosen[old]; !exists {
				chosen[old] = len(chosen)
			}
		}
	}
	out := prdiagram.Graph{Nodes: make([]prdiagram.Node, len(chosen)), Unresolved: append([]string(nil), g.Unresolved...)}
	for old, next := range chosen {
		out.Nodes[next] = g.Nodes[old]
	}
	for _, edge := range g.Edges {
		from, fromOK := chosen[edge.From]
		to, toOK := chosen[edge.To]
		if !fromOK || !toOK || len(out.Edges) >= maxInlineEdges {
			continue
		}
		edge.From, edge.To = from, to
		out.Edges = append(out.Edges, edge)
	}
	out.Unresolved = append(out.Unresolved, fmt.Sprintf("inline display limit: %d node(s), %d edge(s)", len(g.Nodes)-len(out.Nodes), len(g.Edges)-len(out.Edges)))
	return out
}

func appendOmissions(b *strings.Builder, omissions []Omission) {
	if len(omissions) == 0 {
		return
	}
	b.WriteString("\nCoverage omissions:\n")
	for _, omission := range omissions {
		fmt.Fprintf(b, "- %s: %d\n", inline(omission.Reason), omission.Count)
	}
}

func appendCoverage(b *strings.Builder, coverage Coverage) {
	fmt.Fprintf(b, "Coverage: %d file(s), %d byte(s), %d declaration(s), %d edge(s).\n", coverage.Files, coverage.Bytes, coverage.Declarations, coverage.Edges)
	appendOmissions(b, coverage.Omissions)
	b.WriteByte('\n')
}

func stripMermaid(markdown string) string {
	start := strings.Index(markdown, "```mermaid")
	if start < 0 {
		return markdown
	}
	endRel := strings.Index(markdown[start+len("```mermaid"):], "```")
	if endRel < 0 {
		return markdown[:start]
	}
	end := start + len("```mermaid") + endRel + len("```")
	return markdown[:start] + markdown[end:]
}

func (r Result) textEvidence(headBase, baseBase string) string {
	var b strings.Builder
	b.WriteString("## Application flows (static analysis)\n\n")
	r.writeMarker(&b)
	fmt.Fprintf(&b, "Status: **%s**", r.Status)
	if r.Explanation != "" {
		fmt.Fprintf(&b, " — %s", inline(r.Explanation))
	}
	b.WriteString("\n\nMermaid was omitted because the inline flow exceeded the display limit. Source evidence is retained below. This describes static source relationships, not runtime order or complete application coverage.\n\n")
	appendCoverage(&b, r.Coverage)
	r.Nodes = append([]Node(nil), r.Nodes...)
	sort.SliceStable(r.Nodes, func(i, j int) bool { return r.Nodes[i].Changed && !r.Nodes[j].Changed })
	graph, err := r.graph()
	if err == nil {
		evidence, evidenceErr := graph.MarkdownSources(headBase, baseBase)
		if evidenceErr == nil {
			evidence = stripMermaid(evidence)
			evidence = strings.TrimPrefix(evidence, "## Changed application flow\n\n")
			maxEvidence := maxMarkdownBytes - b.Len() - len("\nEvidence truncated at the inline display limit; JSON output retains the complete bounded result.\n")
			if maxEvidence > 0 {
				b.WriteString(trimUTF8(evidence, maxEvidence))
			}
		}
	}
	b.WriteString("\nEvidence truncated at the inline display limit; JSON output retains the complete bounded result.\n")
	return trimUTF8(b.String(), maxMarkdownBytes)
}

func (r Result) writeMarker(b *strings.Builder) {
	head := r.Head.SHA
	if head == "" {
		head = r.Head.Snapshot
	}
	identity := struct {
		Version   int    `json:"version"`
		Generator string `json:"generator"`
		Head      string `json:"head"`
		Options   string `json:"options"`
	}{r.Version, r.Generator, head, r.OptionsDigest}
	data, _ := json.Marshal(identity)
	fmt.Fprintf(b, "<!-- nitpick:flow %s -->\n\n", data)
}

func trimUTF8(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(s) <= max {
		return s
	}
	cut := s[:max]
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut
}

func inline(s string) string {
	return strings.NewReplacer("\n", " ", "\r", " ", "`", "'", "<", "&lt;", ">", "&gt;").Replace(s)
}
