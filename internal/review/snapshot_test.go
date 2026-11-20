package review

import "testing"

func TestStageSnapshotPreservesFindingsAcrossLaterRewrites(t *testing.T) {
	var saved []Snapshot
	e := new(Engine)
	e.OnSnapshot = func(s Snapshot) { saved = append(saved, s) }
	findings := []Finding{{Path: "a.go", Line: 3, Title: "old", AlsoAt: []LineSpan{{Line: 7}}}}
	e.snapshot(Snapshot{Stage: "generation", Findings: findings})
	findings[0].Title = "new"
	findings[0].AlsoAt[0].Line = 9
	e.snapshot(Snapshot{Stage: "triage", Findings: nil, Reason: "merged"})
	if len(saved) != 2 || saved[0].Findings[0].Title != "old" || saved[0].Findings[0].AlsoAt[0].Line != 7 {
		t.Fatalf("stage evidence changed: %+v", saved)
	}
	if saved[1].Stage != "triage" || len(saved[1].Findings) != 0 || saved[1].Reason != "merged" {
		t.Fatalf("empty stage was not recorded: %+v", saved)
	}
}
