package flowhtml

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestComputeProducesByteIdenticalGeometryAcrossRuns(t *testing.T) {
	nodes, edges := chainGraph(t, 12)
	edges = append(edges, LayoutEdge{ID: "cycle", From: "n11", To: "n0"}, LayoutEdge{ID: "loop", From: "n5", To: "n5"})
	first, err := json.Marshal(Compute(nodes, edges, LayoutOptions{}))
	if err != nil {
		t.Fatal(err)
	}
	for run := 0; run < 5; run++ {
		next, marshalErr := json.Marshal(Compute(nodes, edges, LayoutOptions{}))
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if string(first) != string(next) {
			t.Fatalf("run %d differed:\n%s\n%s", run, first, next)
		}
	}
}

// TestComputeIgnoresInputOrderWhenPlacingNodes pins determinism against the
// caller's slice order, not just against repeated identical calls: a layout
// that depended on arrival order would pass the repeat test and fail here.
func TestComputeIgnoresInputOrderWhenPlacingNodes(t *testing.T) {
	nodes, edges := chainGraph(t, 9)
	edges = append(edges,
		LayoutEdge{ID: "fan1", From: "n0", To: "n4"},
		LayoutEdge{ID: "fan2", From: "n0", To: "n6"},
		LayoutEdge{ID: "back", From: "n8", To: "n2"},
	)
	forward, err := json.Marshal(Compute(nodes, edges, LayoutOptions{}))
	if err != nil {
		t.Fatal(err)
	}
	reversedNodes := make([]LayoutNode, len(nodes))
	for i, n := range nodes {
		reversedNodes[len(nodes)-1-i] = n
	}
	reversedEdges := make([]LayoutEdge, len(edges))
	for i, e := range edges {
		reversedEdges[len(edges)-1-i] = e
	}
	shuffled, err := json.Marshal(Compute(reversedNodes, reversedEdges, LayoutOptions{}))
	if err != nil {
		t.Fatal(err)
	}
	if string(forward) != string(shuffled) {
		t.Fatalf("input order changed the layout:\n%s\n%s", forward, shuffled)
	}
}

func TestComputeOrdersAChainIntoIncreasingLayers(t *testing.T) {
	nodes, edges := chainGraph(t, 4)
	layout := Compute(nodes, edges, LayoutOptions{})
	byID := placedByID(t, layout)
	for i := 0; i < 4; i++ {
		id := fmt.Sprintf("n%d", i)
		if byID[id].Layer != i {
			t.Fatalf("node %s layer = %d, want %d", id, byID[id].Layer, i)
		}
	}
	if byID["n0"].X >= byID["n3"].X {
		t.Fatalf("chain did not advance left to right: n0 x=%v n3 x=%v", byID["n0"].X, byID["n3"].X)
	}
}

func TestComputeTerminatesOnACycleAndMarksTheBackEdge(t *testing.T) {
	nodes := []LayoutNode{{ID: "a", Rank: -1}, {ID: "b", Rank: -1}, {ID: "c", Rank: -1}}
	edges := []LayoutEdge{
		{ID: "ab", From: "a", To: "b"},
		{ID: "bc", From: "b", To: "c"},
		{ID: "ca", From: "c", To: "a"},
	}
	layout := Compute(nodes, edges, LayoutOptions{})
	if len(layout.Nodes) != 3 {
		t.Fatalf("cycle lost nodes: %+v", layout.Nodes)
	}
	marked := map[string]bool{}
	for _, e := range layout.Edges {
		if e.Backward {
			marked[e.ID] = true
		}
	}
	if len(marked) != 1 || !marked["ca"] {
		t.Fatalf("back edge marking = %v, want exactly {ca}: %+v", marked, layout.Edges)
	}
	// Layering must break the cycle rather than stack the whole component on
	// one row, which is what an unmarked back edge produces.
	byID := placedByID(t, layout)
	if byID["a"].Layer != 0 || byID["b"].Layer != 1 || byID["c"].Layer != 2 {
		t.Fatalf("cycle was not broken for layering: a=%d b=%d c=%d", byID["a"].Layer, byID["b"].Layer, byID["c"].Layer)
	}
}

// TestComputeMarksBackEdgeThatLandsOnTheSameLayer guards back-edge detection
// itself: both endpoints resolve to one layer, so no geometric comparison can
// recover the mark if markBackEdges stops running.
func TestComputeMarksBackEdgeThatLandsOnTheSameLayer(t *testing.T) {
	nodes := []LayoutNode{{ID: "x", Rank: -1}, {ID: "y", Rank: -1}}
	layout := Compute(nodes, []LayoutEdge{
		{ID: "xy", From: "x", To: "y"},
		{ID: "yx", From: "y", To: "x"},
	}, LayoutOptions{})
	var backward []string
	for _, e := range layout.Edges {
		if e.Backward {
			backward = append(backward, e.ID)
		}
	}
	if len(backward) != 1 || backward[0] != "yx" {
		t.Fatalf("two-node cycle back edges = %v, want [yx]: %+v", backward, layout.Edges)
	}
	byID := placedByID(t, layout)
	if byID["x"].Layer == byID["y"].Layer {
		t.Fatalf("unbroken cycle collapsed both nodes onto layer %d", byID["x"].Layer)
	}
}

func TestComputeMarksSelfLoopWithoutOverlappingItsNode(t *testing.T) {
	nodes := []LayoutNode{{ID: "solo", Rank: -1}}
	layout := Compute(nodes, []LayoutEdge{{ID: "self", From: "solo", To: "solo"}}, LayoutOptions{})
	if len(layout.Edges) != 1 || !layout.Edges[0].SelfLoop {
		t.Fatalf("self loop was not marked: %+v", layout.Edges)
	}
	node := layout.Nodes[0]
	for _, pt := range layout.Edges[0].Points {
		if pt.X > node.X && pt.X < node.X+node.Width && pt.Y > node.Y && pt.Y < node.Y+node.Height {
			t.Fatalf("self loop point %+v is inside its node %+v", pt, node)
		}
	}
	if layout.Width <= node.X+node.Width {
		t.Fatalf("self loop lane fell outside the canvas: width=%v node right=%v", layout.Width, node.X+node.Width)
	}
}

func TestComputeKeepsNodesInALayerFromOverlapping(t *testing.T) {
	var nodes []LayoutNode
	var edges []LayoutEdge
	nodes = append(nodes, LayoutNode{ID: "root", Rank: -1})
	for i := 0; i < 8; i++ {
		id := fmt.Sprintf("leaf%02d", i)
		nodes = append(nodes, LayoutNode{ID: id, Rank: -1})
		edges = append(edges, LayoutEdge{ID: "e" + id, From: "root", To: id})
	}
	layout := Compute(nodes, edges, LayoutOptions{})
	byLayer := map[int][]Placed{}
	for _, n := range layout.Nodes {
		byLayer[n.Layer] = append(byLayer[n.Layer], n)
	}
	for layer, placed := range byLayer {
		for i := range placed {
			for j := i + 1; j < len(placed); j++ {
				a, b := placed[i], placed[j]
				// A wrapped layer separates rows vertically, so overlap is
				// only a failure when both axes intersect.
				if a.X < b.X+b.Width && b.X < a.X+a.Width &&
					a.Y < b.Y+b.Height && b.Y < a.Y+a.Height {
					t.Fatalf("layer %d nodes overlap: %+v and %+v", layer, a, b)
				}
			}
		}
	}
}

func TestComputeWrapsALayerWiderThanTheCanvasBudget(t *testing.T) {
	var nodes []LayoutNode
	var edges []LayoutEdge
	nodes = append(nodes, LayoutNode{ID: "root", Rank: -1})
	for i := 0; i < 30; i++ {
		id := fmt.Sprintf("leaf%02d", i)
		nodes = append(nodes, LayoutNode{ID: id, Rank: -1})
		edges = append(edges, LayoutEdge{ID: "e" + id, From: "root", To: id})
	}
	// MaxCanvasWidth is reused as the canvas height budget in left-to-right layout.
	layout := Compute(nodes, edges, LayoutOptions{MaxCanvasWidth: 1600})
	if layout.Height > 1600 {
		t.Fatalf("canvas height %v exceeds the 1600 budget", layout.Height)
	}
	cols := map[float64]int{}
	for _, n := range layout.Nodes {
		if n.Layer == 1 {
			cols[n.X]++
		}
	}
	if len(cols) < 2 {
		t.Fatalf("expected the 30-node layer to wrap onto several column bands, got %d", len(cols))
	}
}

func TestComputeKeepsEveryNodeInsideTheCanvasBounds(t *testing.T) {
	var nodes []LayoutNode
	var edges []LayoutEdge
	nodes = append(nodes, LayoutNode{ID: "root", Rank: -1})
	for i := 0; i < 40; i++ {
		id := fmt.Sprintf("leaf%02d", i)
		nodes = append(nodes, LayoutNode{ID: id, Rank: -1})
		edges = append(edges, LayoutEdge{ID: "e" + id, From: "root", To: id})
	}
	layout := Compute(nodes, edges, LayoutOptions{})
	for _, n := range layout.Nodes {
		if n.X < 0 || n.Y < 0 || n.X+n.Width > layout.Width || n.Y+n.Height > layout.Height {
			t.Fatalf("node %s at (%v,%v) falls outside the %vx%v canvas", n.ID, n.X, n.Y, layout.Width, layout.Height)
		}
	}
}

func TestComputeSkipsEdgesNamingAnAbsentNode(t *testing.T) {
	nodes := []LayoutNode{{ID: "present", Rank: -1}}
	layout := Compute(nodes, []LayoutEdge{
		{ID: "dangling", From: "present", To: "ghost"},
		{ID: "orphan", From: "ghost", To: "present"},
	}, LayoutOptions{})
	if len(layout.Edges) != 0 {
		t.Fatalf("edges naming an absent node survived: %+v", layout.Edges)
	}
	if len(layout.Nodes) != 1 {
		t.Fatalf("layout invented nodes: %+v", layout.Nodes)
	}
}

// TestComputeReturnsAnEmptyLayoutForAnEmptyGraph proves the empty result is
// empty because there was nothing to lay out, not because a later stage
// silently discarded geometry.
func TestComputeReturnsAnEmptyLayoutForAnEmptyGraph(t *testing.T) {
	layout := Compute(nil, nil, LayoutOptions{})
	if len(layout.Nodes) != 0 || len(layout.Edges) != 0 {
		t.Fatalf("empty input produced geometry: %+v", layout)
	}
	if layout.Width != 0 || layout.Height != 0 {
		t.Fatalf("empty layout claimed a canvas: %vx%v", layout.Width, layout.Height)
	}
	withEdgesOnly := Compute(nil, []LayoutEdge{{ID: "e", From: "a", To: "b"}}, LayoutOptions{})
	if len(withEdgesOnly.Nodes) != 0 || len(withEdgesOnly.Edges) != 0 || withEdgesOnly.Width != 0 {
		t.Fatalf("edges without nodes produced geometry: %+v", withEdgesOnly)
	}
}

func TestComputeHandlesALargeGraphWithoutHanging(t *testing.T) {
	const count = 1200
	nodes := make([]LayoutNode, 0, count)
	edges := make([]LayoutEdge, 0, count)
	for i := 0; i < count; i++ {
		nodes = append(nodes, LayoutNode{ID: fmt.Sprintf("n%04d", i), Rank: -1})
		if i > 0 {
			edges = append(edges, LayoutEdge{
				ID:   fmt.Sprintf("e%04d", i),
				From: fmt.Sprintf("n%04d", (i-1)/2),
				To:   fmt.Sprintf("n%04d", i),
			})
		}
	}
	layout := Compute(nodes, edges, LayoutOptions{})
	if len(layout.Nodes) != count {
		t.Fatalf("node count = %d, want %d", len(layout.Nodes), count)
	}
	if layout.Width <= 0 || layout.Height <= 0 {
		t.Fatalf("large graph produced no canvas: %vx%v", layout.Width, layout.Height)
	}
}

func TestComputeHonorsRankHintOnlyWhenEdgesAllow(t *testing.T) {
	nodes := []LayoutNode{{ID: "a", Rank: -1}, {ID: "b", Rank: 5}, {ID: "c", Rank: 0}}
	edges := []LayoutEdge{{ID: "ab", From: "a", To: "b"}, {ID: "bc", From: "b", To: "c"}}
	byID := placedByID(t, Compute(nodes, edges, LayoutOptions{}))
	if byID["b"].Layer != 5 {
		t.Fatalf("satisfiable rank hint ignored: b layer = %d", byID["b"].Layer)
	}
	if byID["c"].Layer <= byID["b"].Layer {
		t.Fatalf("rank hint of 0 broke the edge constraint b->c: b=%d c=%d", byID["b"].Layer, byID["c"].Layer)
	}
}

func TestComputeDeduplicatesRepeatedEdgeIDs(t *testing.T) {
	nodes := []LayoutNode{{ID: "a", Rank: -1}, {ID: "b", Rank: -1}}
	layout := Compute(nodes, []LayoutEdge{
		{ID: "same", From: "a", To: "b"},
		{ID: "same", From: "a", To: "b"},
	}, LayoutOptions{})
	if len(layout.Edges) != 1 {
		t.Fatalf("duplicate edge ID survived: %+v", layout.Edges)
	}
}

func TestComputeRoutesEveryEdgeWithAtLeastTwoPoints(t *testing.T) {
	nodes, edges := chainGraph(t, 6)
	edges = append(edges,
		LayoutEdge{ID: "skip", From: "n0", To: "n4"},
		LayoutEdge{ID: "back", From: "n5", To: "n1"},
		LayoutEdge{ID: "self", From: "n2", To: "n2"},
	)
	for _, route := range Compute(nodes, edges, LayoutOptions{}).Edges {
		if len(route.Points) < 2 {
			t.Fatalf("edge %s routed with %d point(s)", route.ID, len(route.Points))
		}
	}
}

func chainGraph(t *testing.T, count int) ([]LayoutNode, []LayoutEdge) {
	t.Helper()
	nodes := make([]LayoutNode, 0, count)
	edges := make([]LayoutEdge, 0, count)
	for i := 0; i < count; i++ {
		nodes = append(nodes, LayoutNode{ID: fmt.Sprintf("n%d", i), Label: fmt.Sprintf("node %d", i), Rank: -1})
		if i > 0 {
			edges = append(edges, LayoutEdge{ID: fmt.Sprintf("e%d", i), From: fmt.Sprintf("n%d", i-1), To: fmt.Sprintf("n%d", i)})
		}
	}
	return nodes, edges
}

func placedByID(t *testing.T, layout Layout) map[string]Placed {
	t.Helper()
	out := make(map[string]Placed, len(layout.Nodes))
	for _, n := range layout.Nodes {
		out[n.ID] = n
	}
	return out
}
