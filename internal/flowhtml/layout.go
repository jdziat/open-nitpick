// Package flowhtml renders a static application-flow result as a
// self-contained HTML document: a deterministic SVG call graph, syntax
// highlighted source, and client-side filtering with no network dependency.
package flowhtml

import "sort"

// LayoutNode is one input node for the layout engine.
type LayoutNode struct {
	ID    string
	Label string
	// Rank is an optional layer hint. A negative value means unknown; a
	// non-negative value is honoured only when no edge constraint forbids it.
	Rank int
}

// LayoutEdge connects node IDs. An edge naming an absent node is skipped.
type LayoutEdge struct {
	ID       string
	From, To string
}

// Point is one vertex of a routed polyline.
type Point struct{ X, Y float64 }

// Placed is a node with resolved geometry. X and Y are its top-left corner.
type Placed struct {
	ID            string
	X, Y          float64
	Width, Height float64
	Layer         int
}

// Route is an edge with resolved geometry.
type Route struct {
	ID       string
	From, To string
	// Points is a polyline from the source anchor to the target anchor and
	// always holds at least two points.
	Points []Point
	// Backward marks an edge that runs against layer order: a cycle edge.
	Backward bool
	// SelfLoop marks an edge whose endpoints are the same node.
	SelfLoop bool
}

// Layout is the resolved diagram geometry.
type Layout struct {
	Nodes         []Placed
	Edges         []Route
	Width, Height float64
}

// LayoutOptions bounds and tunes the diagram geometry. A zero field selects
// its default.
type LayoutOptions struct {
	NodeWidth     float64
	NodeHeight    float64
	LayerGap      float64
	NodeGap       float64
	Margin        float64
	MaxIterations int
	// MaxCanvasWidth bounds how wide one layer may run before it wraps onto
	// another row. Without it a wide layer produces a canvas thousands of
	// pixels across, which a viewport can only show by scaling the labels
	// below legibility.
	MaxCanvasWidth float64
}

func (o LayoutOptions) withDefaults() LayoutOptions {
	if o.NodeWidth <= 0 {
		o.NodeWidth = 220
	}
	if o.NodeHeight <= 0 {
		o.NodeHeight = 44
	}
	if o.LayerGap <= 0 {
		o.LayerGap = 90
	}
	if o.NodeGap <= 0 {
		o.NodeGap = 24
	}
	if o.Margin <= 0 {
		o.Margin = 32
	}
	if o.MaxCanvasWidth <= 0 {
		o.MaxCanvasWidth = 1100
	}
	if o.MaxIterations <= 0 {
		o.MaxIterations = 24
	}
	return o
}

// Compute lays out a directed graph top-down in layers. The same input always
// produces byte-identical geometry: every traversal runs in sorted ID order and
// no result depends on map iteration, wall-clock time, or randomness. Cycles,
// self-loops, duplicate edges, and disconnected components are all supported.
func Compute(nodes []LayoutNode, edges []LayoutEdge, o LayoutOptions) Layout {
	o = o.withDefaults()
	g := newGraph(nodes, edges)
	if len(g.order) == 0 {
		return Layout{}
	}
	g.markBackEdges()
	g.assignLayers()
	layers := g.layerOrder()
	g.reduceCrossings(layers, o.MaxIterations)
	placed := g.place(layers, o)
	routes := g.route(placed, o)
	width, height := bounds(placed, routes, o)
	out := make([]Placed, 0, len(placed))
	for _, id := range g.order {
		out = append(out, placed[id])
	}
	return Layout{Nodes: out, Edges: routes, Width: width, Height: height}
}

// graph is the working state for one Compute call. Adjacency is stored per
// node in sorted order so every sweep visits neighbours identically.
type graph struct {
	order    []string
	index    map[string]int
	label    map[string]string
	rank     map[string]int
	edges    []LayoutEdge
	backward map[string]bool
	selfLoop map[string]bool
	out      map[string][]string
	in       map[string][]string
	layer    map[string]int
}

func newGraph(nodes []LayoutNode, edges []LayoutEdge) *graph {
	g := &graph{
		index:    map[string]int{},
		label:    map[string]string{},
		rank:     map[string]int{},
		backward: map[string]bool{},
		selfLoop: map[string]bool{},
		out:      map[string][]string{},
		in:       map[string][]string{},
		layer:    map[string]int{},
	}
	for _, n := range nodes {
		if _, seen := g.index[n.ID]; seen {
			continue
		}
		g.index[n.ID] = len(g.order)
		g.order = append(g.order, n.ID)
		g.label[n.ID] = n.Label
		g.rank[n.ID] = n.Rank
	}
	sort.Strings(g.order)
	for i, id := range g.order {
		g.index[id] = i
	}
	// An edge naming an absent node is dropped rather than inventing a node,
	// so a truncated graph cannot grow phantom geometry.
	seen := map[string]bool{}
	for _, e := range edges {
		if _, ok := g.index[e.From]; !ok {
			continue
		}
		if _, ok := g.index[e.To]; !ok {
			continue
		}
		if seen[e.ID] {
			continue
		}
		seen[e.ID] = true
		g.edges = append(g.edges, e)
	}
	sort.SliceStable(g.edges, func(i, j int) bool { return g.edges[i].ID < g.edges[j].ID })
	for _, e := range g.edges {
		if e.From == e.To {
			g.selfLoop[e.ID] = true
			continue
		}
		g.out[e.From] = append(g.out[e.From], e.To)
		g.in[e.To] = append(g.in[e.To], e.From)
	}
	for id := range g.out {
		sort.Strings(g.out[id])
	}
	for id := range g.in {
		sort.Strings(g.in[id])
	}
	return g
}

// markBackEdges finds cycle edges with an iterative DFS in sorted ID order. An
// edge to a node currently on the stack is the one edge broken, which keeps the
// remaining graph acyclic for layering.
func (g *graph) markBackEdges() {
	const (
		unvisited = 0
		active    = 1
		done      = 2
	)
	state := make(map[string]int, len(g.order))
	onStack := map[string]bool{}
	type frame struct {
		id   string
		next int
	}
	for _, root := range g.order {
		if state[root] != unvisited {
			continue
		}
		stack := []frame{{id: root}}
		state[root] = active
		onStack[root] = true
		for len(stack) > 0 {
			top := &stack[len(stack)-1]
			neighbours := g.out[top.id]
			if top.next >= len(neighbours) {
				state[top.id] = done
				onStack[top.id] = false
				stack = stack[:len(stack)-1]
				continue
			}
			next := neighbours[top.next]
			top.next++
			if onStack[next] {
				g.markEdgesBetween(top.id, next)
				continue
			}
			if state[next] == unvisited {
				state[next] = active
				onStack[next] = true
				stack = append(stack, frame{id: next})
			}
		}
	}
}

func (g *graph) markEdgesBetween(from, to string) {
	for _, e := range g.edges {
		if e.From == from && e.To == to {
			g.backward[e.ID] = true
		}
	}
}

// assignLayers runs longest-path layering over the forward edges. A Rank hint
// raises a node's floor before relaxation, so honouring it also pushes every
// descendant down rather than leaving an edge pointing upward.
func (g *graph) assignLayers() {
	forward := map[string][]string{}
	indegree := map[string]int{}
	for _, id := range g.order {
		indegree[id] = 0
	}
	for _, e := range g.edges {
		if g.backward[e.ID] || g.selfLoop[e.ID] {
			continue
		}
		forward[e.From] = append(forward[e.From], e.To)
		indegree[e.To]++
	}
	for id := range forward {
		sort.Strings(forward[id])
	}
	for _, id := range g.order {
		if hint := g.rank[id]; hint > 0 {
			g.layer[id] = hint
		}
	}
	remaining := make(map[string]int, len(indegree))
	var queue []string
	for _, id := range g.order {
		remaining[id] = indegree[id]
		if indegree[id] == 0 {
			queue = append(queue, id)
		}
	}
	processed := 0
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		processed++
		for _, next := range forward[id] {
			if g.layer[id]+1 > g.layer[next] {
				g.layer[next] = g.layer[id] + 1
			}
			remaining[next]--
			if remaining[next] == 0 {
				queue = append(queue, next)
			}
		}
	}
	// A residual cycle (possible when parallel edges survive back-edge marking)
	// must not leave a node unlayered, so place the remainder below every
	// predecessor that did resolve.
	if processed < len(g.order) {
		for _, id := range g.order {
			if remaining[id] == 0 {
				continue
			}
			best := g.layer[id]
			for _, pred := range g.in[id] {
				if g.layer[pred]+1 > best {
					best = g.layer[pred] + 1
				}
			}
			g.layer[id] = best
		}
	}
}

func (g *graph) layerOrder() [][]string {
	depth := 0
	for _, id := range g.order {
		if g.layer[id] > depth {
			depth = g.layer[id]
		}
	}
	layers := make([][]string, depth+1)
	for _, id := range g.order {
		layers[g.layer[id]] = append(layers[g.layer[id]], id)
	}
	for i := range layers {
		sort.Strings(layers[i])
	}
	return layers
}

// reduceCrossings runs alternating median sweeps. Ties break on node ID, so a
// sweep that finds no improvement still produces the same order every run.
func (g *graph) reduceCrossings(layers [][]string, iterations int) {
	if len(layers) < 2 {
		return
	}
	position := map[string]int{}
	for _, layer := range layers {
		for i, id := range layer {
			position[id] = i
		}
	}
	for sweep := 0; sweep < iterations; sweep++ {
		down := sweep%2 == 0
		if down {
			for i := 1; i < len(layers); i++ {
				g.sortByMedian(layers[i], position, g.in)
				reindex(layers[i], position)
			}
			continue
		}
		for i := len(layers) - 2; i >= 0; i-- {
			g.sortByMedian(layers[i], position, g.out)
			reindex(layers[i], position)
		}
	}
}

func (g *graph) sortByMedian(layer []string, position map[string]int, adjacency map[string][]string) {
	median := make(map[string]float64, len(layer))
	for _, id := range layer {
		neighbours := adjacency[id]
		if len(neighbours) == 0 {
			// A node with no neighbour on the facing layer keeps its place
			// rather than collapsing to the left edge.
			median[id] = float64(position[id])
			continue
		}
		positions := make([]int, 0, len(neighbours))
		for _, n := range neighbours {
			positions = append(positions, position[n])
		}
		sort.Ints(positions)
		mid := len(positions) / 2
		if len(positions)%2 == 1 {
			median[id] = float64(positions[mid])
		} else {
			median[id] = float64(positions[mid-1]+positions[mid]) / 2
		}
	}
	sort.SliceStable(layer, func(i, j int) bool {
		a, b := layer[i], layer[j]
		if median[a] != median[b] {
			return median[a] < median[b]
		}
		return a < b
	})
}

func reindex(layer []string, position map[string]int) {
	for i, id := range layer {
		position[id] = i
	}
}

// place assigns coordinates. Each layer is laid out left to right at a fixed
// pitch, then centred against the widest layer, so a narrow layer sits under
// the middle of a wide one instead of hugging the left margin.
// perRow reports how many nodes fit in one row without exceeding the canvas
// budget. It is at least one, so an oversized node still places instead of
// producing an empty row.
func perRow(o LayoutOptions) int {
	pitch := o.NodeWidth + o.NodeGap
	budget := o.MaxCanvasWidth - 2*o.Margin + o.NodeGap
	n := int(budget / pitch)
	if n < 1 {
		return 1
	}
	return n
}

// place resolves geometry for every node. A layer wider than the canvas
// budget wraps onto further rows, keeping the drawing within a width a
// viewport can show at readable scale; rows of one layer stay adjacent so
// the layer still reads as a single rank.
func (g *graph) place(layers [][]string, o LayoutOptions) map[string]Placed {
	pitch := o.NodeWidth + o.NodeGap
	columns := perRow(o)
	widest := 0
	for _, layer := range layers {
		n := len(layer)
		if n > columns {
			n = columns
		}
		if n > widest {
			widest = n
		}
	}
	totalWidth := float64(widest)*pitch - o.NodeGap
	placed := make(map[string]Placed, len(g.order))
	y := o.Margin
	for depth, layer := range layers {
		for start := 0; start < len(layer); start += columns {
			end := start + columns
			if end > len(layer) {
				end = len(layer)
			}
			row := layer[start:end]
			rowWidth := float64(len(row))*pitch - o.NodeGap
			offset := o.Margin + (totalWidth-rowWidth)/2
			for i, id := range row {
				placed[id] = Placed{
					ID:     id,
					X:      offset + float64(i)*pitch,
					Y:      y,
					Width:  o.NodeWidth,
					Height: o.NodeHeight,
					Layer:  depth,
				}
			}
			y += o.NodeHeight + o.NodeGap
		}
		// Trade the last row's tight gap for the full inter-layer gap.
		y += o.LayerGap - o.NodeGap
	}
	return placed
}

// route builds polylines. A forward edge that spans more than one layer jogs
// through the gap between layers so it does not cut through the boxes in
// between; a back edge leaves the column entirely and returns on the right.
func (g *graph) route(placed map[string]Placed, o LayoutOptions) []Route {
	routes := make([]Route, 0, len(g.edges))
	rightEdge := 0.0
	for _, p := range placed {
		if p.X+p.Width > rightEdge {
			rightEdge = p.X + p.Width
		}
	}
	for _, e := range g.edges {
		from, to := placed[e.From], placed[e.To]
		switch {
		case g.selfLoop[e.ID]:
			routes = append(routes, Route{
				ID: e.ID, From: e.From, To: e.To, SelfLoop: true,
				Points: selfLoopPoints(from),
			})
		case g.backward[e.ID] || to.Y <= from.Y:
			routes = append(routes, Route{
				ID: e.ID, From: e.From, To: e.To, Backward: true,
				Points: backwardPoints(from, to, rightEdge, o),
			})
		default:
			routes = append(routes, Route{
				ID: e.ID, From: e.From, To: e.To,
				Points: forwardPoints(from, to, o),
			})
		}
	}
	return routes
}

func forwardPoints(from, to Placed, o LayoutOptions) []Point {
	start := Point{X: from.X + from.Width/2, Y: from.Y + from.Height}
	end := Point{X: to.X + to.Width/2, Y: to.Y}
	if start.X == end.X {
		return []Point{start, end}
	}
	// Turn inside the gap below the source so the diagonal never crosses the
	// row of boxes that sits between two non-adjacent layers.
	mid := start.Y + o.LayerGap/2
	return []Point{start, {X: start.X, Y: mid}, {X: end.X, Y: mid}, end}
}

func backwardPoints(from, to Placed, rightEdge float64, o LayoutOptions) []Point {
	start := Point{X: from.X + from.Width, Y: from.Y + from.Height/2}
	end := Point{X: to.X + to.Width, Y: to.Y + to.Height/2}
	lane := rightEdge + o.NodeGap + o.Margin/2
	return []Point{start, {X: lane, Y: start.Y}, {X: lane, Y: end.Y}, end}
}

func selfLoopPoints(node Placed) []Point {
	right := node.X + node.Width
	top := node.Y + node.Height/3
	bottom := node.Y + 2*node.Height/3
	lane := right + 26
	return []Point{
		{X: right, Y: top},
		{X: lane, Y: top},
		{X: lane, Y: bottom},
		{X: right, Y: bottom},
	}
}

func bounds(placed map[string]Placed, routes []Route, o LayoutOptions) (float64, float64) {
	if len(placed) == 0 {
		return 0, 0
	}
	width, height := 0.0, 0.0
	for _, p := range placed {
		if p.X+p.Width > width {
			width = p.X + p.Width
		}
		if p.Y+p.Height > height {
			height = p.Y + p.Height
		}
	}
	for _, r := range routes {
		for _, pt := range r.Points {
			if pt.X > width {
				width = pt.X
			}
			if pt.Y > height {
				height = pt.Y
			}
		}
	}
	return width + o.Margin, height + o.Margin
}
