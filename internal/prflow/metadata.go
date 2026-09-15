package prflow

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"runtime"
	"sort"
)

const maxJSONBytes = 2 << 20

func finalizeResult(result *Result, options Options) {
	result.Generator = "open-nitpick/prflow-v1"
	result.GOOS, result.GOARCH = runtime.GOOS, runtime.GOARCH
	result.BuildTags = append([]string(nil), options.BuildTags...)
	sort.Strings(result.BuildTags)
	options.BuildTags = result.BuildTags
	data, _ := json.Marshal(options)
	result.OptionsDigest = fmt.Sprintf("%x", sha256.Sum256(data))
	sides := make(map[string]Revision, len(result.Nodes))
	for i := range result.Nodes {
		node := &result.Nodes[i]
		node.Source.Revision = result.Head
		if node.State == ChangeRemoved {
			node.Source.Revision = result.Base
		}
		sides[node.ID] = node.Source.Revision
	}
	for i := range result.Edges {
		edge := &result.Edges[i]
		edge.Source.Revision = sides[edge.From]
	}
	boundJSON(result, options.MaxFlows)
	counts := map[string]int{}
	for _, omission := range result.Coverage.Omissions {
		counts[omission.Reason] += omission.Count
	}
	result.Coverage.Omissions = nil
	for reason, count := range counts {
		result.Coverage.Omissions = append(result.Coverage.Omissions, Omission{Reason: reason, Count: count})
	}
	sort.Slice(result.Coverage.Omissions, func(i, j int) bool { return result.Coverage.Omissions[i].Reason < result.Coverage.Omissions[j].Reason })
}

func boundJSON(result *Result, maxFlows int) {
	for {
		encoded, err := json.Marshal(result)
		if err != nil || len(encoded) <= maxJSONBytes {
			return
		}
		result.Status = StatusPartial
		result.Explanation = "the JSON size limit reduced the retained graph"
		if len(result.Nodes) == 0 {
			result.Edges, result.Flows = nil, nil
			return
		}
		// Remove context before changed roots, and drop half at a time so very
		// large labels cannot turn size enforcement into quadratic encoding.
		nodes := append([]Node(nil), result.Nodes...)
		sort.SliceStable(nodes, func(i, j int) bool { return nodes[i].Changed && !nodes[j].Changed })
		nodes = nodes[:len(nodes)/2]
		kept := map[string]bool{}
		for _, node := range nodes {
			kept[node.ID] = true
		}
		edges := result.Edges[:0]
		for _, edge := range result.Edges {
			if kept[edge.From] && kept[edge.To] {
				edges = append(edges, edge)
			}
		}
		addOmission(&result.Coverage, "json_bytes", len(result.Nodes)-len(nodes)+len(result.Edges)-len(edges))
		sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
		result.Nodes, result.Edges = nodes, edges
		result.Flows = buildFlows(nodes, edges, maxFlows)
	}
}
