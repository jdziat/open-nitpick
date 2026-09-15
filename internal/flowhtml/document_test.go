package flowhtml

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/prflow"
)

// wideResult builds one flow whose size forces the focus-and-context path.
func wideResult(t *testing.T, total int, changed int) prflow.Result {
	t.Helper()
	result := prflow.Result{Status: prflow.StatusComplete}
	flow := prflow.Flow{ID: "flow:root", Name: "Root flow", Nodes: []string{"root"}}
	result.Nodes = append(result.Nodes, prflow.Node{
		ID: "root", Label: "pkg.Root", Kind: "func", State: prflow.ChangeModified,
		Changed: true, Source: prflow.Location{Path: "pkg/root.go", Line: 10},
	})
	for i := 0; i < total; i++ {
		id := fmt.Sprintf("leaf%03d", i)
		result.Nodes = append(result.Nodes, prflow.Node{
			ID: id, Label: "pkg." + id, Kind: "func",
			State:   prflow.ChangeUnchanged,
			Changed: i < changed,
			Source:  prflow.Location{Path: "pkg/leaf.go", Line: i + 1},
		})
		edgeID := "e" + id
		result.Edges = append(result.Edges, prflow.Edge{
			ID: edgeID, From: "root", To: id, Kind: "call",
			Resolution: prflow.ResolutionResolved,
			Source:     prflow.Location{Path: "pkg/root.go", Line: i + 1},
		})
		flow.Nodes = append(flow.Nodes, id)
		flow.Edges = append(flow.Edges, edgeID)
	}
	result.Flows = []prflow.Flow{flow}
	return result
}

func TestBuildDrawsNoMoreNodesThanTheVisibleBudget(t *testing.T) {
	doc := build(context.Background(), wideResult(t, 80, 3), nil, Options{MaxVisibleNodes: 24}.withDefaults())
	if len(doc.Flows) != 1 {
		t.Fatalf("expected one flow, got %d", len(doc.Flows))
	}
	if doc.Flows[0].Nodes > 24 {
		t.Fatalf("diagram drew %d nodes, over the budget of 24", doc.Flows[0].Nodes)
	}
	if !doc.Flows[0].Focused || doc.Flows[0].Hidden == 0 {
		t.Fatalf("a trimmed diagram must report that it hid nodes, got focused=%v hidden=%d",
			doc.Flows[0].Focused, doc.Flows[0].Hidden)
	}
}

// lateChangeResult puts the changed declarations last in sorted order and
// hangs them off a low-degree node, so a diagram that selects by name order or
// by connectivity alone leaves them out. Only seeding on "changed" keeps them.
func lateChangeResult(t *testing.T, total int) (prflow.Result, []string) {
	t.Helper()
	result := prflow.Result{Status: prflow.StatusComplete}
	flow := prflow.Flow{ID: "flow:hub", Name: "Hub flow", Nodes: []string{"aaa_hub", "zzz_stub"}}
	result.Nodes = append(result.Nodes,
		prflow.Node{ID: "aaa_hub", Label: "pkg.Hub", Kind: "func", State: prflow.ChangeUnchanged,
			Source: prflow.Location{Path: "pkg/hub.go", Line: 1}},
		prflow.Node{ID: "zzz_stub", Label: "pkg.Stub", Kind: "func", State: prflow.ChangeUnchanged,
			Source: prflow.Location{Path: "pkg/stub.go", Line: 1}},
	)
	for i := 0; i < total; i++ {
		id := fmt.Sprintf("mmm_ctx%03d", i)
		result.Nodes = append(result.Nodes, prflow.Node{ID: id, Label: "pkg." + id, Kind: "func",
			State: prflow.ChangeUnchanged, Source: prflow.Location{Path: "pkg/ctx.go", Line: i + 1}})
		edge := "e_hub_" + id
		result.Edges = append(result.Edges, prflow.Edge{ID: edge, From: "aaa_hub", To: id, Kind: "call",
			Resolution: prflow.ResolutionResolved, Source: prflow.Location{Path: "pkg/hub.go", Line: i + 1}})
		flow.Nodes = append(flow.Nodes, id)
		flow.Edges = append(flow.Edges, edge)
	}
	var changed []string
	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("zzz_changed%03d", i)
		changed = append(changed, id)
		result.Nodes = append(result.Nodes, prflow.Node{ID: id, Label: "pkg." + id, Kind: "func",
			State: prflow.ChangeModified, Changed: true,
			Source: prflow.Location{Path: "pkg/changed.go", Line: i + 1}})
		edge := "e_stub_" + id
		result.Edges = append(result.Edges, prflow.Edge{ID: edge, From: "zzz_stub", To: id, Kind: "call",
			Resolution: prflow.ResolutionResolved, Source: prflow.Location{Path: "pkg/stub.go", Line: i + 1}})
		flow.Nodes = append(flow.Nodes, id)
		flow.Edges = append(flow.Edges, edge)
	}
	result.Flows = []prflow.Flow{flow}
	return result, changed
}

func TestBuildKeepsEveryChangedDeclarationOnTheDiagram(t *testing.T) {
	result, changed := lateChangeResult(t, 60)
	doc := build(context.Background(), result, nil, Options{MaxVisibleNodes: 12}.withDefaults())
	drawn := map[string]bool{}
	for _, node := range doc.Nodes {
		if node.Drawn {
			drawn[node.ID] = true
		}
	}
	if doc.Flows[0].Nodes > 12 {
		t.Fatalf("diagram drew %d nodes, over the budget", doc.Flows[0].Nodes)
	}
	for _, id := range changed {
		if !drawn[id] {
			t.Fatalf("changed declaration %s was trimmed while %d unchanged nodes were drawn", id, doc.Flows[0].Nodes)
		}
	}
}

func TestBuildKeepsTrimmedNodesSearchableInThePayload(t *testing.T) {
	doc := build(context.Background(), wideResult(t, 80, 3), nil, Options{MaxVisibleNodes: 24}.withDefaults())
	if len(doc.Nodes) != 81 {
		t.Fatalf("every flow member stays listed for search; got %d of 81", len(doc.Nodes))
	}
	undrawn := 0
	for _, node := range doc.Nodes {
		if !node.Drawn {
			undrawn++
		}
	}
	if undrawn == 0 {
		t.Fatal("expected trimmed nodes to remain listed and marked undrawn")
	}
}

func TestBuildDrawsEveryNodeWhenTheFlowFitsTheBudget(t *testing.T) {
	doc := build(context.Background(), wideResult(t, 6, 1), nil, Options{MaxVisibleNodes: 24}.withDefaults())
	if doc.Flows[0].Focused || doc.Flows[0].Hidden != 0 {
		t.Fatalf("a flow inside the budget must not be trimmed, got focused=%v hidden=%d",
			doc.Flows[0].Focused, doc.Flows[0].Hidden)
	}
	for _, node := range doc.Nodes {
		if !node.Drawn {
			t.Fatalf("node %s should be drawn when the whole flow fits", node.ID)
		}
	}
}

func TestStartNodePrefersAChangedDeclaration(t *testing.T) {
	// The hub is by far the best connected node and is unchanged, so a rank
	// that ignores the change opens the document on it.
	result, changed := lateChangeResult(t, 60)
	doc := build(context.Background(), result, nil, Options{MaxVisibleNodes: 200}.withDefaults())
	start := startNode(doc)
	if start == "" {
		t.Fatal("a document with nodes must name where to start")
	}
	wanted := map[string]bool{}
	for _, id := range changed {
		wanted[id] = true
	}
	if !wanted[start] {
		t.Fatalf("document opens on %q, which is not one of the changed declarations", start)
	}
}

func TestRenderEmbedsTheStartNodeAndDrawnFlags(t *testing.T) {
	out, err := Render(context.Background(), wideResult(t, 40, 2), nil, Options{MaxVisibleNodes: 24})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	page := string(out)
	if !strings.Contains(page, "__FLOW_START__") {
		t.Fatal("the document must tell the browser where to open")
	}
	if !strings.Contains(page, `"drawn":true`) {
		t.Fatal("the payload must mark which nodes a diagram drew")
	}
	if !strings.Contains(page, "not shown") {
		t.Fatal("a trimmed diagram must say so in the document")
	}
}

func TestRenderKeepsTheTreePayloadWithinTheVisibleBudget(t *testing.T) {
	out, err := Render(context.Background(), wideResult(t, 200, 40), nil, Options{})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	// The browser payload lists every reachable node so search works, but a
	// diagram that drew all 200 nodes would not be readable. The tree view
	// opens on the changed declarations and their immediate callees instead.
	if !strings.Contains(string(out), "__FLOW_NODES__") {
		t.Fatal("the payload must still be embedded for the tree to render")
	}
	doc := build(context.Background(), wideResult(t, 200, 40), nil, Options{}.withDefaults())
	if doc.DrawnCount == 0 || doc.DrawnCount >= doc.ReachableCount {
		t.Fatalf("expected a trimmed tree, got drawn=%d reachable=%d",
			doc.DrawnCount, doc.ReachableCount)
	}
}

func TestTruncateLabelKeepsBothEndsOfASymbol(t *testing.T) {
	label := "internal/prflow.(*Materializer).ReadFileLimit"
	got := truncateLabel(label, 28)
	if len([]rune(got)) > 28 {
		t.Fatalf("truncated label %q is longer than the budget", got)
	}
	if !strings.HasPrefix(got, "internal") {
		t.Fatalf("truncation dropped the package prefix: %q", got)
	}
	if !strings.HasSuffix(got, "Limit") {
		t.Fatalf("truncation dropped the function name: %q", got)
	}
}

func TestTruncateLabelDistinguishesSymbolsSharingATail(t *testing.T) {
	a := truncateLabel("internal/prflow.(*Worker).Run", 20)
	b := truncateLabel("internal/review.(*Engine).Run", 20)
	if a == b {
		t.Fatalf("two different symbols truncated to the same label %q", a)
	}
}

func TestDisambiguateSeparatesRepeatedLabels(t *testing.T) {
	nodes := map[string]docNode{
		"a": {ID: "a", Label: "testing.Fatalf", SourceLine: 12},
		"b": {ID: "b", Label: "testing.Fatalf", SourceLine: 40},
		"c": {ID: "c", Label: "pkg.Unique", SourceLine: 7},
	}
	placed := []Placed{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	got := disambiguate(nodes, placed)
	if got["a"] == got["b"] {
		t.Fatalf("repeated labels stayed identical: %q", got["a"])
	}
	if got["c"] != "pkg.Unique" {
		t.Fatalf("a unique label must stay untouched, got %q", got["c"])
	}
}

func TestRenderIsByteIdenticalAcrossRuns(t *testing.T) {
	result, _ := lateChangeResult(t, 40)
	first, err := Render(context.Background(), result, nil, Options{})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	second, err := Render(context.Background(), result, nil, Options{})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if string(first) != string(second) {
		t.Fatal("the same result and options must produce byte-identical output")
	}
}

func TestRenderIncludesTheChangedDeclarationNavigator(t *testing.T) {
	// The navigator is how a reviewer moves between the declarations a change
	// touched without hunting for their boxes on the canvas. It is scaffolded
	// in the page and filled by the script, so the document must ship both the
	// empty list container and the code that populates it. Dropping either
	// leaves the panel with no persistent way into the changed set.
	out, err := Render(context.Background(), wideResult(t, 40, 5), nil, Options{})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	page := string(out)
	for _, marker := range []string{
		`id="panel-changed-nav"`,
		`id="panel-changed-list"`,
		"buildChangedNav",
		"updateChangedNav",
	} {
		if !strings.Contains(page, marker) {
			t.Fatalf("the changed-declaration navigator is missing %q from the document", marker)
		}
	}
}

func TestRenderKeepsSourceLinesHorizontallyScrollable(t *testing.T) {
	// A declaration's source is wider than the panel, so the code block owns a
	// horizontal scroll rather than clipping the line at the panel edge. The
	// wrapper class carries that overflow; without it a reviewer cannot read
	// the end of a long statement, which is the failure this guards.
	out, err := Render(context.Background(), wideResult(t, 12, 3), nil, Options{})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	page := string(out)
	if !strings.Contains(page, ".code-wrap{") || !strings.Contains(page, "overflow-x:auto") {
		t.Fatal("the code wrapper must enable horizontal scroll so long lines are readable")
	}
	if !strings.Contains(page, `codeWrap.className = "code-wrap"`) {
		t.Fatal("the script must place rendered source inside the scroll wrapper")
	}
}
