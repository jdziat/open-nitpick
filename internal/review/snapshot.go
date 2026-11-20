package review

import (
	"encoding/json"
)

// Snapshot records one stage's output before a later stage can rewrite it.
type Snapshot struct {
	Stage    string      `json:"stage"`
	Paths    []string    `json:"paths,omitempty"`
	Findings []Finding   `json:"findings"`
	Withheld []Overruled `json:"withheld,omitempty"`
	Reason   string      `json:"reason,omitempty"`
}

func (e *Engine) snapshot(s Snapshot) {
	if e.OnSnapshot == nil {
		return
	}
	// The observer must not share slices with stages that normalize in place.
	data, _ := json.Marshal(s)
	var copied Snapshot
	_ = json.Unmarshal(data, &copied)
	e.OnSnapshot(copied)
}
