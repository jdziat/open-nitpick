package flowhtml

import (
	"fmt"
	"html"
	"sort"
	"strings"
)

// svgDiagram renders one laid-out flow as inline SVG. Every value that reaches
// the markup is escaped here: the diagram carries user-controlled symbol names
// and must not be able to introduce elements of its own.
func svgDiagram(flowID string, layout Layout, nodes map[string]docNode, edges map[string]docEdge) string {
	if len(layout.Nodes) == 0 {
		return ""
	}
	var b strings.Builder
	// The viewport is sized by CSS and the drawing keeps its intrinsic scale,
	// so the script can pan and zoom by transforming the viewport group
	// rather than letting the browser shrink a wide graph to fit.
	fmt.Fprintf(&b, "<svg class=\"graph\" data-flow=\"%s\" data-width=\"%.0f\" data-height=\"%.0f\" viewBox=\"0 0 %.0f %.0f\" preserveAspectRatio=\"xMidYMid meet\" role=\"group\" aria-label=\"Application flow diagram, %d nodes\" xmlns=\"http://www.w3.org/2000/svg\">",
		html.EscapeString(flowID), layout.Width, layout.Height, layout.Width, layout.Height, len(layout.Nodes))
	b.WriteString(svgDefs())
	b.WriteString("<g class=\"viewport\">")

	// Edges first so a node box always paints over the line that reaches it.
	b.WriteString("<g class=\"edges\">")
	routes := append([]Route(nil), layout.Edges...)
	sort.SliceStable(routes, func(i, j int) bool { return routes[i].ID < routes[j].ID })
	for _, route := range routes {
		edge := edges[route.ID]
		classes := []string{"edge", "res-" + edge.Resolution}
		if route.Backward {
			classes = append(classes, "backward")
		}
		if route.SelfLoop {
			classes = append(classes, "selfloop")
		}
		label := edge.Kind
		if edge.Resolution != "resolved" {
			label += " (" + edge.Resolution + ")"
		}
		fmt.Fprintf(&b, "<g class=\"%s\" data-edge=\"%s\" data-from=\"%s\" data-to=\"%s\"><title>%s</title><path d=\"%s\" marker-end=\"url(#arrow-%s)\"/></g>",
			strings.Join(classes, " "),
			html.EscapeString(route.ID),
			html.EscapeString(route.From),
			html.EscapeString(route.To),
			html.EscapeString(label+" — "+edge.SourcePath+":"+fmt.Sprint(edge.SourceLine)),
			pathData(route.Points),
			html.EscapeString(edge.Resolution),
		)
	}
	b.WriteString("</g>")

	b.WriteString("<g class=\"nodes\">")
	placed := append([]Placed(nil), layout.Nodes...)
	sort.SliceStable(placed, func(i, j int) bool { return placed[i].ID < placed[j].ID })
	shown := disambiguate(nodes, placed)
	for _, p := range placed {
		node := nodes[p.ID]
		classes := []string{"node", "state-" + node.State}
		if node.Boundary {
			classes = append(classes, "boundary")
		}
		if node.Changed {
			classes = append(classes, "changed")
		}
		fmt.Fprintf(&b, "<g class=\"%s\" data-node=\"%s\" data-label=\"%s\" tabindex=\"-1\" role=\"button\" aria-label=\"%s\">",
			strings.Join(classes, " "),
			html.EscapeString(p.ID),
			html.EscapeString(node.Label),
			html.EscapeString(nodeAriaLabel(node)),
		)
		fmt.Fprintf(&b, "<rect x=\"%.1f\" y=\"%.1f\" width=\"%.1f\" height=\"%.1f\" rx=\"8\"/>", p.X, p.Y, p.Width, p.Height)
		fmt.Fprintf(&b, "<rect class=\"halo\" x=\"%.1f\" y=\"%.1f\" width=\"%.1f\" height=\"%.1f\" rx=\"11\" fill=\"none\" stroke=\"none\"/>", p.X-5, p.Y-5, p.Width+10, p.Height+10)
		fmt.Fprintf(&b, "<text x=\"%.1f\" y=\"%.1f\" class=\"label\">%s</text>",
			p.X+12, p.Y+p.Height/2-1, html.EscapeString(truncateLabel(shown[p.ID], 32)))
		fmt.Fprintf(&b, "<text x=\"%.1f\" y=\"%.1f\" class=\"sub\">%s</text>",
			p.X+12, p.Y+p.Height-9, html.EscapeString(node.State))
		b.WriteString("<title>" + html.EscapeString(node.Label+"\n"+node.SourcePath+":"+fmt.Sprint(node.SourceLine)) + "</title>")
		b.WriteString("</g>")
	}
	b.WriteString("</g></g></svg>")
	return b.String()
}

func svgDefs() string {
	var b strings.Builder
	b.WriteString("<defs>")
	for _, marker := range []struct{ id, class string }{
		{"resolved", "m-resolved"},
		{"inferred", "m-inferred"},
		{"unresolved", "m-unresolved"},
	} {
		fmt.Fprintf(&b, "<marker id=\"arrow-%s\" viewBox=\"0 0 10 10\" refX=\"9\" refY=\"5\" markerWidth=\"7\" markerHeight=\"7\" orient=\"auto-start-reverse\"><path class=\"%s\" d=\"M0,0 L10,5 L0,10 z\"/></marker>",
			marker.id, marker.class)
	}
	b.WriteString("</defs>")
	return b.String()
}

// pathData builds a smoothed polyline. Straight runs stay straight and only
// the corners are rounded, which keeps a long orthogonal route readable.
func pathData(points []Point) string {
	if len(points) < 2 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "M%.1f,%.1f", points[0].X, points[0].Y)
	const radius = 8.0
	for i := 1; i < len(points)-1; i++ {
		prev, corner, next := points[i-1], points[i], points[i+1]
		in := shorten(corner, prev, radius)
		out := shorten(corner, next, radius)
		fmt.Fprintf(&b, " L%.1f,%.1f Q%.1f,%.1f %.1f,%.1f", in.X, in.Y, corner.X, corner.Y, out.X, out.Y)
	}
	last := points[len(points)-1]
	fmt.Fprintf(&b, " L%.1f,%.1f", last.X, last.Y)
	return b.String()
}

// shorten steps from corner toward target by at most radius, so the quadratic
// control point never overshoots a short segment.
func shorten(corner, target Point, radius float64) Point {
	dx, dy := target.X-corner.X, target.Y-corner.Y
	length := dx*dx + dy*dy
	if length == 0 {
		return corner
	}
	distance := sqrt(length)
	step := radius
	if distance/2 < step {
		step = distance / 2
	}
	scale := step / distance
	return Point{X: corner.X + dx*scale, Y: corner.Y + dy*scale}
}

// sqrt avoids importing math for one call in a package that is otherwise
// arithmetic-free, using Newton's method on a positive input.
func sqrt(v float64) float64 {
	if v <= 0 {
		return 0
	}
	guess := v
	for i := 0; i < 20; i++ {
		next := 0.5 * (guess + v/guess)
		if next == guess {
			break
		}
		guess = next
	}
	return guess
}

// truncateLabel shortens a symbol by eliding its middle. Both ends carry
// identity for a Go name - the package or receiver on the left, the function
// on the right - so dropping either end makes two different symbols read the
// same.
func truncateLabel(label string, max int) string {
	runes := []rune(label)
	if len(runes) <= max {
		return label
	}
	if max <= 1 {
		return string(runes[:max])
	}
	if max <= 4 {
		return string(runes[:max-1]) + "…"
	}
	// Favour the tail slightly: the function name is usually the answer to
	// "what is this", while the prefix answers "whose".
	head := (max - 1) / 2
	tail := max - 1 - head
	return string(runes[:head]) + "…" + string(runes[len(runes)-tail:])
}

// disambiguate appends a source line to labels that would otherwise render
// identically in one diagram, so two boxes are never indistinguishable.
func disambiguate(nodes map[string]docNode, placed []Placed) map[string]string {
	counts := make(map[string]int, len(placed))
	for _, p := range placed {
		counts[nodes[p.ID].Label]++
	}
	out := make(map[string]string, len(placed))
	for _, p := range placed {
		node := nodes[p.ID]
		label := node.Label
		if counts[label] > 1 && node.SourceLine > 0 {
			label += ":" + fmt.Sprint(node.SourceLine)
		}
		out[p.ID] = label
	}
	return out
}

// nodeAriaLabel describes a node for a screen reader in the order a reader
// needs it: what it is, whether the change touched it, and where it lives.
func nodeAriaLabel(node docNode) string {
	parts := []string{node.Label}
	if node.Changed {
		parts = append(parts, "changed, "+node.State)
	} else {
		parts = append(parts, node.State)
	}
	if node.Boundary {
		parts = append(parts, "boundary")
	}
	if node.SourcePath != "" {
		parts = append(parts, node.SourcePath+" line "+fmt.Sprint(node.SourceLine))
	}
	return strings.Join(parts, ", ")
}
