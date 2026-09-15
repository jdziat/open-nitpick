package prflow

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFinalizeResultRecordsDeterministicAnalysisMetadata(t *testing.T) {
	result := Result{
		Base: Revision{Repository: "base/repo", SHA: "base"}, Head: Revision{Repository: "head/repo", SHA: "head"},
		Nodes:    []Node{{ID: "old", State: ChangeRemoved, Source: Location{Path: "old.go", Line: 1}}, {ID: "new", State: ChangeModified, Source: Location{Path: "new.go", Line: 2}}},
		Edges:    []Edge{{From: "old", To: "new", Source: Location{Path: "old.go", Line: 1}}},
		Coverage: Coverage{Omissions: []Omission{{Reason: "z", Count: 1}, {Reason: "z", Count: 2}, {Reason: "a", Count: 1}}},
	}
	finalizeResult(&result, Options{BuildTags: []string{"two", "one"}, MaxFlows: 3})
	if result.Generator == "" || result.GOOS == "" || result.GOARCH == "" || result.OptionsDigest == "" || strings.Join(result.BuildTags, ",") != "one,two" {
		t.Fatalf("incomplete deterministic metadata: %+v", result)
	}
	if result.Nodes[0].Source.Revision.SHA != "base" || result.Nodes[1].Source.Revision.SHA != "head" || result.Edges[0].Source.Revision.SHA != "base" {
		t.Fatalf("source revisions lost: %+v", result)
	}
	if got := result.Coverage.Omissions; len(got) != 2 || got[0] != (Omission{Reason: "a", Count: 1}) || got[1] != (Omission{Reason: "z", Count: 3}) {
		t.Fatalf("omissions=%+v, want stable aggregate", got)
	}
}

func TestFinalizeResultBoundsJSONWithoutDroppingChangedRoot(t *testing.T) {
	result := Result{Status: StatusComplete}
	for i := 0; i < 1000; i++ {
		result.Nodes = append(result.Nodes, Node{ID: string(rune('a'+i%26)) + strings.Repeat("x", 4096) + string(rune(i)), Label: strings.Repeat("context", 1024), Source: Location{Path: "p.go", Line: i + 1}, Changed: i == 0})
	}
	finalizeResult(&result, Options{MaxFlows: 3})
	data, err := json.Marshal(result)
	if err != nil || len(data) > maxJSONBytes || len(result.Nodes) == 0 || !result.Nodes[0].Changed || result.Status != StatusPartial {
		t.Fatalf("bytes=%d nodes=%d changed=%v status=%s err=%v", len(data), len(result.Nodes), len(result.Nodes) > 0 && result.Nodes[0].Changed, result.Status, err)
	}
}
