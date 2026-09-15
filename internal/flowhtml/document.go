package flowhtml

import (
	"context"
	"html/template"
	"sort"
	"strings"

	"github.com/jdziat/open-nitpick/internal/prflow"
)

// SourceReader supplies file contents for the snippet panel. It is optional:
// without one, the document renders labels, paths, lines, and links only.
type SourceReader interface {
	ReadFile(ctx context.Context, revision prflow.Revision, path string) ([]byte, error)
}

// Options controls how a result becomes an HTML document.
type Options struct {
	// Title heads the document and its browser tab.
	Title string
	// HeadSourceBase and BaseSourceBase are immutable revision URLs used for
	// evidence links. Empty bases render plain path:line text.
	HeadSourceBase string
	BaseSourceBase string
	// IncludeSource embeds bounded source snippets. This is off by default
	// because the flow result itself is links-only; a document built with it
	// contains repository source and says so in its header.
	IncludeSource bool
	// SnippetBefore and SnippetAfter bound each embedded snippet.
	SnippetBefore int
	SnippetAfter  int
	// MaxSnippetBytes caps the total embedded source in one document.
	MaxSnippetBytes int
	// Layout tunes diagram geometry.
	Layout LayoutOptions
	// MaxVisibleNodes bounds how many nodes one diagram draws before it falls
	// back to focus and context: the changed declarations and what they touch
	// directly. A reader cannot parse a hundred boxes at once, and a diagram
	// that draws them all is read as a wall rather than as a call graph.
	MaxVisibleNodes int
}

func (o Options) withDefaults() Options {
	if o.Title == "" {
		o.Title = "Application flows"
	}
	if o.SnippetBefore <= 0 {
		o.SnippetBefore = 6
	}
	if o.SnippetAfter <= 0 {
		o.SnippetAfter = 14
	}
	if o.MaxSnippetBytes <= 0 {
		o.MaxSnippetBytes = 4 << 20
	}
	if o.MaxVisibleNodes <= 0 {
		o.MaxVisibleNodes = 24
	}
	return o
}

// docNode is one node prepared for rendering.
type docNode struct {
	ID         string
	Label      string
	Kind       string
	State      string
	Changed    bool
	Boundary   bool
	Reason     string
	SourcePath string
	SourceLine int
	Link       string
	// Drawn says a diagram placed this node. An undrawn node is still listed
	// and searchable, so the panel can explain why it is not on the canvas.
	Drawn   bool
	Snippet *snippet
	Callers []docRef
	Callees []docRef
}

// docEdge is one edge prepared for rendering.
type docEdge struct {
	ID         string
	From, To   string
	FromLabel  string
	ToLabel    string
	Kind       string
	Resolution string
	Reason     string
	SourcePath string
	SourceLine int
	Link       string
}

// docRef names a neighbour in the side panel.
type docRef struct {
	ID    string
	Label string
	Kind  string
	// Resolution carries the edge's confidence so the panel can mark an
	// inferred or unresolved neighbour rather than implying a certain call.
	Resolution string
	Line       int
}

// docFlow is one connected component with its diagram. SVG is marked as
// trusted markup because svgDiagram builds every element itself and escapes
// each symbol name it embeds; no caller-supplied HTML reaches it.
type docFlow struct {
	ID    string
	Name  string
	SVG   template.HTML
	Nodes int
	Edges int
	// Hidden counts the nodes this diagram left out to stay readable, and
	// Focused says the diagram is a neighbourhood rather than the whole flow.
	// A diagram that silently drops nodes would misreport coverage.
	Hidden  int
	Focused bool
}

// document is the complete view model handed to the template.
type document struct {
	Title           string
	Status          string
	StatusClass     string
	Explanation     string
	Generator       string
	HeadSHA         string
	BaseSHA         string
	Snapshot        string
	GOOS            string
	GOARCH          string
	OptionsDigest   string
	Coverage        prflow.Coverage
	Omissions       []prflow.Omission
	Flows           []docFlow
	Nodes           []docNode
	Edges           []docEdge
	NodeJSON        string
	ChangedCount    int
	BoundaryCount   int
	UnresolvedCount int
	AddedCount      int
	ModifiedCount   int
	RemovedCount    int
	DrawnCount      int
	ReachableCount  int
	TreeTrimmed     bool
	IncludesSource  bool
	SourceBytes     int
	SourceTruncated bool
	Legend          []legendItem
}

type legendItem struct {
	Class, Title, Detail string
}

// build converts a result into the view model. A nil reader, or IncludeSource
// left off, yields a links-only document; the header states which it is.
func build(ctx context.Context, result prflow.Result, reader SourceReader, o Options) document {
	o = o.withDefaults()
	doc := document{
		Title:          o.Title,
		Status:         string(result.Status),
		StatusClass:    statusClass(result.Status),
		Explanation:    result.Explanation,
		Generator:      result.Generator,
		HeadSHA:        result.Head.SHA,
		BaseSHA:        result.Base.SHA,
		Snapshot:       result.Head.Snapshot,
		GOOS:           result.GOOS,
		GOARCH:         result.GOARCH,
		OptionsDigest:  result.OptionsDigest,
		Coverage:       result.Coverage,
		Omissions:      result.Coverage.Omissions,
		IncludesSource: o.IncludeSource && reader != nil,
		Legend:         legend(),
	}

	byID := make(map[string]docNode, len(result.Nodes))
	for _, node := range result.Nodes {
		base := o.HeadSourceBase
		if node.State == prflow.ChangeRemoved {
			base = o.BaseSourceBase
		}
		entry := docNode{
			ID:         node.ID,
			Label:      node.Label,
			Kind:       node.Kind,
			State:      string(node.State),
			Changed:    node.Changed,
			Boundary:   node.Boundary,
			Reason:     node.Reason,
			SourcePath: node.Source.Path,
			SourceLine: node.Source.Line,
			Link:       sourceLink(base, node.Source.Path, node.Source.Line),
		}
		byID[node.ID] = entry
	}

	labels := func(id string) string {
		if n, ok := byID[id]; ok {
			return n.Label
		}
		return id
	}
	edgeByID := make(map[string]docEdge, len(result.Edges))
	for _, edge := range result.Edges {
		base := o.HeadSourceBase
		if from, ok := byID[edge.From]; ok && from.State == "removed" {
			base = o.BaseSourceBase
		}
		entry := docEdge{
			ID:         edge.ID,
			From:       edge.From,
			To:         edge.To,
			FromLabel:  labels(edge.From),
			ToLabel:    labels(edge.To),
			Kind:       edge.Kind,
			Resolution: string(edge.Resolution),
			Reason:     edge.Reason,
			SourcePath: edge.Source.Path,
			SourceLine: edge.Source.Line,
			Link:       sourceLink(base, edge.Source.Path, edge.Source.Line),
		}
		edgeByID[edge.ID] = entry
		doc.Edges = append(doc.Edges, entry)

		if from, ok := byID[edge.From]; ok {
			from.Callees = appendRef(from.Callees, docRef{ID: edge.To, Label: labels(edge.To), Kind: edge.Kind, Resolution: entry.Resolution, Line: edge.Source.Line})
			byID[edge.From] = from
		}
		if to, ok := byID[edge.To]; ok {
			to.Callers = appendRef(to.Callers, docRef{ID: edge.From, Label: labels(edge.From), Kind: edge.Kind, Resolution: entry.Resolution, Line: edge.Source.Line})
			byID[edge.To] = to
		}
	}
	sort.SliceStable(doc.Edges, func(i, j int) bool { return doc.Edges[i].ID < doc.Edges[j].ID })

	if doc.IncludesSource {
		doc.SourceBytes, doc.SourceTruncated = attachSnippets(ctx, byID, result, reader, o)
	}

	flows, drawn := buildFlows(result, byID, edgeByID, o)
	doc.Flows = flows

	// Only the nodes and edges placed in a rendered diagram reach the panel
	// and its search index. result.Nodes can exceed what max_flows drew, and
	// a payload listing more than the diagrams show makes "N of M" and search
	// claim a symbol is absent when it was only out of scope for this page.
	for _, node := range result.Nodes {
		if !drawn.nodes[node.ID] {
			continue
		}
		entry := byID[node.ID]
		entry.Drawn = drawn.placed[node.ID]
		if entry.Drawn {
			doc.DrawnCount++
		}
		entry.Callers = filterRefs(entry.Callers, drawn.nodes)
		entry.Callees = filterRefs(entry.Callees, drawn.nodes)
		doc.Nodes = append(doc.Nodes, entry)
		if entry.Changed {
			doc.ChangedCount++
			switch entry.State {
			case "added":
				doc.AddedCount++
			case "modified":
				doc.ModifiedCount++
			case "removed":
				doc.RemovedCount++
			}
		}
		if entry.Boundary {
			doc.BoundaryCount++
		}
	}
	sort.SliceStable(doc.Nodes, func(i, j int) bool { return doc.Nodes[i].ID < doc.Nodes[j].ID })

	kept := doc.Edges[:0]
	for _, edge := range doc.Edges {
		if !drawn.edges[edge.ID] {
			continue
		}
		if edge.Resolution == string(prflow.ResolutionUnresolved) {
			doc.UnresolvedCount++
		}
		kept = append(kept, edge)
	}
	doc.Edges = kept
	doc.ReachableCount = len(doc.Nodes)
	doc.TreeTrimmed = doc.DrawnCount < doc.ReachableCount
	return doc
}

// filterRefs drops a neighbour reference to a node no diagram drew, so the
// panel never offers a "Calls" or "Called by" button that selects nothing.
func filterRefs(refs []docRef, drawn map[string]bool) []docRef {
	if len(refs) == 0 {
		return refs
	}
	out := make([]docRef, 0, len(refs))
	for _, ref := range refs {
		if drawn[ref.ID] {
			out = append(out, ref)
		}
	}
	return out
}

func appendRef(refs []docRef, next docRef) []docRef {
	for _, existing := range refs {
		if existing.ID == next.ID && existing.Kind == next.Kind && existing.Line == next.Line {
			return refs
		}
	}
	refs = append(refs, next)
	sort.SliceStable(refs, func(i, j int) bool {
		if refs[i].Label != refs[j].Label {
			return refs[i].Label < refs[j].Label
		}
		return refs[i].Line < refs[j].Line
	})
	return refs
}

// drawnSet names the nodes and edges that made it into a rendered diagram.
// A result can carry more nodes and edges than max_flows lets it draw, and
// the document trims its panel and search index to match what a reader sees,
// rather than listing a symbol the page never shows.
type drawnSet struct {
	// nodes and edges are everything a rendered flow contains, which is what
	// the panel and the search index may reach.
	nodes map[string]bool
	edges map[string]bool
	// placed is the subset a diagram draws. Focus and context can
	// leave a flow member undrawn while keeping it findable, so the two sets
	// are not the same and the status line reports both.
	placed map[string]bool
}

// buildFlows lays out each declared flow, falling back to one diagram over the
// whole graph when the result declared no components. It reports which nodes
// and edges were placed, so the caller can trim the document to what is drawn.
func buildFlows(result prflow.Result, nodes map[string]docNode, edges map[string]docEdge, o Options) ([]docFlow, drawnSet) {
	layout := o.Layout
	flows := append([]prflow.Flow(nil), result.Flows...)
	if len(flows) == 0 && len(result.Nodes) > 0 {
		all := prflow.Flow{ID: "flow:all", Name: "Application flow"}
		for _, node := range result.Nodes {
			all.Nodes = append(all.Nodes, node.ID)
		}
		for _, edge := range result.Edges {
			all.Edges = append(all.Edges, edge.ID)
		}
		flows = []prflow.Flow{all}
	}
	// Surface the most informative flows first: rank by changed-node count
	// descending, so a reader sees what the PR did rather than an alphabetical
	// list that starts with closures.
	changedInFlow := func(f prflow.Flow) int {
		n := 0
		for _, id := range f.Nodes {
			if nd, ok := nodes[id]; ok && nd.Changed {
				n++
			}
		}
		return n
	}
	sort.SliceStable(flows, func(i, j int) bool {
		ci, cj := changedInFlow(flows[i]), changedInFlow(flows[j])
		if ci != cj {
			return ci > cj // more changed nodes first
		}
		return flows[i].ID < flows[j].ID // stable tiebreak
	})
	out := make([]docFlow, 0, len(flows))
	drawn := drawnSet{nodes: map[string]bool{}, edges: map[string]bool{}, placed: map[string]bool{}}
	for _, flow := range flows {
		member := make(map[string]bool, len(flow.Nodes))
		for _, id := range flow.Nodes {
			member[id] = true
		}
		visible, trimmed := focusContext(flow, nodes, edges, o.MaxVisibleNodes)
		var ln []LayoutNode
		for _, id := range flow.Nodes {
			node, ok := nodes[id]
			if !ok || !visible[id] {
				continue
			}
			ln = append(ln, LayoutNode{ID: node.ID, Label: node.Label, Rank: -1})
		}
		selected := make(map[string]bool, len(flow.Edges))
		for _, id := range flow.Edges {
			selected[id] = true
		}
		var le []LayoutEdge
		for _, edge := range sortedEdges(edges) {
			if len(selected) > 0 && !selected[edge.ID] {
				continue
			}
			if !member[edge.From] || !member[edge.To] {
				continue
			}
			if !visible[edge.From] || !visible[edge.To] {
				continue
			}
			le = append(le, LayoutEdge{ID: edge.ID, From: edge.From, To: edge.To})
		}
		geometry := Compute(ln, le, layout)
		for _, placed := range geometry.Nodes {
			drawn.placed[placed.ID] = true
		}
		// Every member of a rendered flow stays reachable by search and by the
		// panel's caller and callee links, drawn or not.
		for _, id := range flow.Nodes {
			if _, ok := nodes[id]; ok {
				drawn.nodes[id] = true
			}
		}
		for _, id := range flow.Edges {
			if _, ok := edges[id]; ok {
				drawn.edges[id] = true
			}
		}
		// A flow that declared no edge list still owns every edge between its
		// members, and those edges carry the panel's caller and callee links.
		if len(flow.Edges) == 0 {
			for _, edge := range sortedEdges(edges) {
				if member[edge.From] && member[edge.To] {
					drawn.edges[edge.ID] = true
				}
			}
		}
		for _, route := range geometry.Edges {
			drawn.edges[route.ID] = true
		}
		out = append(out, docFlow{
			ID:      flow.ID,
			Name:    flowName(flow, nodes),
			SVG:     template.HTML(svgDiagram(flow.ID, geometry, nodes, edges)),
			Nodes:   len(geometry.Nodes),
			Edges:   len(geometry.Edges),
			Hidden:  trimmed,
			Focused: trimmed > 0,
		})
	}
	return out, drawn
}

func sortedEdges(edges map[string]docEdge) []docEdge {
	out := make([]docEdge, 0, len(edges))
	for _, edge := range edges {
		out = append(out, edge)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func flowName(flow prflow.Flow, nodes map[string]docNode) string {
	if flow.Name != "" {
		return flow.Name
	}
	root := strings.TrimPrefix(flow.ID, "flow:")
	if node, ok := nodes[root]; ok {
		return node.Label
	}
	return root
}

func statusClass(status prflow.Status) string {
	switch status {
	case prflow.StatusComplete:
		return "ok"
	case prflow.StatusPartial:
		return "warn"
	case prflow.StatusFailed, prflow.StatusUnavailable:
		return "bad"
	default:
		return "muted"
	}
}

func legend() []legendItem {
	return []legendItem{
		{Class: "res-resolved", Title: "Resolved", Detail: "a direct static call"},
		{Class: "res-inferred", Title: "Inferred", Detail: "a justified candidate, such as a callback passed as a value"},
		{Class: "res-unresolved", Title: "Unresolved", Detail: "a dynamic or unavailable target, shown as a boundary"},
		{Class: "state-added", Title: "Added", Detail: "introduced by this change"},
		{Class: "state-modified", Title: "Modified", Detail: "touched by this change"},
		{Class: "state-removed", Title: "Removed", Detail: "present only on the base revision"},
		{Class: "state-unchanged", Title: "Unchanged", Detail: "retained as static context"},
	}
}

// focusContext picks the nodes one diagram draws. Under the budget it draws
// the whole flow. Over it, it keeps the changed declarations and everything
// one call away from them, then fills any remaining budget with the
// best-connected neighbours, so the diagram answers "what did this change
// reach" instead of showing every box at an unreadable size. It returns the
// visible set and how many nodes it left out.
func focusContext(flow prflow.Flow, nodes map[string]docNode, edges map[string]docEdge, budget int) (map[string]bool, int) {
	member := make([]string, 0, len(flow.Nodes))
	for _, id := range flow.Nodes {
		if _, ok := nodes[id]; ok {
			member = append(member, id)
		}
	}
	visible := make(map[string]bool, len(member))
	if budget <= 0 || len(member) <= budget {
		for _, id := range member {
			visible[id] = true
		}
		return visible, 0
	}
	inFlow := make(map[string]bool, len(member))
	for _, id := range member {
		inFlow[id] = true
	}
	adjacency := map[string][]string{}
	for _, edge := range sortedEdges(edges) {
		if !inFlow[edge.From] || !inFlow[edge.To] {
			continue
		}
		adjacency[edge.From] = append(adjacency[edge.From], edge.To)
		adjacency[edge.To] = append(adjacency[edge.To], edge.From)
	}

	seeds := make([]string, 0, len(member))
	for _, id := range member {
		if nodes[id].Changed {
			seeds = append(seeds, id)
		}
	}
	// A flow with no changed declaration still needs an anchor, so fall back
	// to the best-connected node rather than drawing an arbitrary slice.
	if len(seeds) == 0 {
		best, bestDegree := "", -1
		for _, id := range member {
			if d := len(adjacency[id]); d > bestDegree {
				best, bestDegree = id, d
			}
		}
		if best != "" {
			seeds = append(seeds, best)
		}
	}
	for _, id := range seeds {
		if len(visible) >= budget {
			break
		}
		visible[id] = true
	}
	// One hop out from each seed, in sorted order so the choice is stable.
	for _, id := range seeds {
		for _, next := range adjacency[id] {
			if len(visible) >= budget {
				break
			}
			visible[next] = true
		}
	}
	if len(visible) < budget {
		ranked := append([]string(nil), member...)
		sort.SliceStable(ranked, func(i, j int) bool {
			di, dj := len(adjacency[ranked[i]]), len(adjacency[ranked[j]])
			if di != dj {
				return di > dj
			}
			return ranked[i] < ranked[j]
		})
		for _, id := range ranked {
			if len(visible) >= budget {
				break
			}
			visible[id] = true
		}
	}
	return visible, len(member) - len(visible)
}
