package prflow

import (
	"sort"
	"strings"
)

func traverse(seeds []*decl, edges []rawEdge, o Options) ([]*decl, []rawEdge, int) {
	incoming, outgoing := map[*decl][]*decl{}, map[*decl][]*decl{}
	for _, edge := range edges {
		if edge.from != nil && edge.to != nil {
			outgoing[edge.from] = append(outgoing[edge.from], edge.to)
			incoming[edge.to] = append(incoming[edge.to], edge.from)
		}
	}
	for _, adjacent := range []map[*decl][]*decl{incoming, outgoing} {
		for node := range adjacent {
			sort.Slice(adjacent[node], func(i, j int) bool { return adjacent[node][i].node.ID < adjacent[node][j].node.ID })
		}
	}
	selected := map[*decl]bool{}
	omitted := map[*decl]bool{}
	for _, seed := range seeds {
		if len(selected) < o.MaxNodes {
			selected[seed] = true
		} else {
			omitted[seed] = true
		}
	}
	for _, walk := range []struct {
		adjacent map[*decl][]*decl
		limit    int
	}{{incoming, o.MaxCallerDepth}, {outgoing, o.MaxCalleeDepth}} {
		type visit struct {
			decl  *decl
			depth int
		}
		queue := make([]visit, 0, len(seeds))
		visited := map[*decl]bool{}
		for _, seed := range seeds {
			if selected[seed] {
				queue = append(queue, visit{decl: seed})
				visited[seed] = true
			}
		}
		for len(queue) > 0 {
			current := queue[0]
			queue = queue[1:]
			for _, next := range walk.adjacent[current.decl] {
				if visited[next] {
					continue
				}
				visited[next] = true
				if current.depth >= walk.limit || (!selected[next] && len(selected) >= o.MaxNodes) {
					if !selected[next] {
						omitted[next] = true
					}
					continue
				}
				selected[next] = true
				delete(omitted, next)
				queue = append(queue, visit{decl: next, depth: current.depth + 1})
			}
		}
	}
	list := make([]*decl, 0, len(selected))
	for declaration := range selected {
		list = append(list, declaration)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].node.ID < list[j].node.ID })
	var keptEdges []rawEdge
	truncated := len(omitted)
	for _, edge := range edges {
		if !selected[edge.from] || (edge.to != nil && !selected[edge.to]) {
			continue
		}
		if len(keptEdges) >= o.MaxEdges {
			truncated++
			continue
		}
		keptEdges = append(keptEdges, edge)
	}
	return list, keptEdges, truncated
}

func flowRoot(ids []string, nodes []Node, edges []Edge) Node {
	incoming := map[string]int{}
	for _, edge := range edges {
		incoming[edge.To]++
	}
	selected := map[string]bool{}
	for _, id := range ids {
		selected[id] = true
	}
	var candidates []Node
	for _, node := range nodes {
		if selected[node.ID] && !node.Boundary {
			candidates = append(candidates, node)
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		cliA, cliB := supportedCLIRoot(a, edges), supportedCLIRoot(b, edges)
		if cliA != cliB {
			return cliA
		}
		if a.Changed != b.Changed {
			return a.Changed
		}
		if (incoming[a.ID] == 0) != (incoming[b.ID] == 0) {
			return incoming[a.ID] == 0
		}
		return a.ID < b.ID
	})
	if len(candidates) > 0 {
		return candidates[0]
	}
	return nodes[0]
}

func supportedCLIRoot(node Node, edges []Edge) bool {
	// A package named main with a declaration named main is the Go command
	// entrypoint. A function that switches on os.Args or flag.Arg is also a
	// supported adapter root, represented by its command-labelled edges.
	if node.Label == "main.main" {
		return true
	}
	for _, edge := range edges {
		if edge.From == node.ID && strings.HasPrefix(edge.Kind, "command: ") {
			return true
		}
	}
	return false
}
