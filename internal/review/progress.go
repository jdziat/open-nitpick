package review

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/jdziat/open-nitpick/internal/llm"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// Bump when interpretation of persisted model results changes.
const progressVersion = 1

// reviewProgress retains model output, never an approval or a completed stage.
type reviewProgress struct {
	mu      sync.Mutex
	policy  string
	prior   map[string]json.RawMessage
	current map[string]json.RawMessage
	reused  int
}

type progressRecord struct {
	Version int                        `json:"version"`
	Head    string                     `json:"head"`
	Results map[string]json.RawMessage `json:"results"`
}

func (e *Engine) startProgress(pr *vcs.PullRequest, prior *vcs.PriorReview) {
	e.progress = nil
	if !e.Resume || !e.Config.Review.Incremental || pr.HeadSHA == "" {
		return
	}
	data, err := yaml.Marshal(e.Config)
	if err != nil {
		return
	}
	sum := sha256.Sum256(data)
	p := &reviewProgress{policy: hex.EncodeToString(sum[:]), current: map[string]json.RawMessage{}}
	if !e.Full && prior != nil {
		var record progressRecord
		if json.Unmarshal(prior.Progress, &record) == nil && record.Version == progressVersion {
			p.prior = record.Results
		}
	}
	e.progress = p
}

func (p *reviewProgress) key(client *llm.Client, base, body string) string {
	if p == nil {
		return ""
	}
	data, err := json.Marshal([]any{progressVersion, p.policy, client.Spec, base, body})
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (p *reviewProgress) load(key string) ([]Finding, bool) {
	if p == nil || key == "" {
		return nil, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	data, ok := p.prior[key]
	if !ok {
		return nil, false
	}
	var result []Finding
	if json.Unmarshal(data, &result) != nil {
		return nil, false
	}
	p.current[key] = data
	p.reused++
	return result, true
}

func (p *reviewProgress) save(key string, findings []Finding) {
	if p == nil || key == "" {
		return
	}
	// Serialize before triage can mutate a finding or its nested slices.
	data, err := json.Marshal(findings)
	if err != nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.current[key] = data
}

func (p *reviewProgress) snapshot(head string) (json.RawMessage, int) {
	if p == nil {
		return nil, 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	record := progressRecord{Version: progressVersion, Head: head, Results: map[string]json.RawMessage{}}
	keys := make([]string, 0, len(p.current))
	for key := range p.current {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	// Missing entries cause more work on retry, never assumed coverage.
	for _, key := range keys {
		record.Results[key] = p.current[key]
		data, err := json.Marshal(record)
		if err != nil || len(data) > vcs.MaxProgressBytes {
			delete(record.Results, key)
		}
	}
	data, err := json.Marshal(record)
	if err != nil {
		return nil, p.reused
	}
	return data, p.reused
}
