package review

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"sync"

	llms "github.com/nocturnium/llm-go-sdk/v6"
	"gopkg.in/yaml.v3"

	"github.com/jdziat/open-nitpick/internal/llm"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// Bump when interpretation of persisted model results changes.
const progressVersion = 1

// Stage answers have stricter evidence keys than the original batch cache.
// Version them separately so compatible, paid batch analysis remains reusable.
const stageProgressVersion = 2

// reviewProgress retains model output, never an approval or a completed stage.
type reviewProgress struct {
	mu     sync.Mutex
	policy string
	// head is the head the checkpoint was written at; now is the head under
	// review, which the checks that read the repository key on.
	head    string
	now     string
	prior   map[string]json.RawMessage
	current map[string]json.RawMessage
	// rank orders what a full checkpoint keeps. Batch analysis costs the most
	// to redo, so it is written first and a stage result is dropped before it.
	rank   map[string]int
	reused int
}

// Ranks, most expensive to redo first.
const (
	rankAnalysis = iota
	rankStage
	rankVerify
)

type progressRecord struct {
	Version int                        `json:"version"`
	Head    string                     `json:"head"`
	Results map[string]json.RawMessage `json:"results"`
}

func (e *Engine) startProgress(pr *vcs.PullRequest, prior *vcs.PriorReview) {
	e.progress = nil
	if e.Resume && !e.Config.Review.Incremental {
		e.log().Warn("resume disabled: set review.incremental=true to reuse prior model results; reviewing the whole change")
	}
	if !e.Resume || !e.Config.Review.Incremental || pr.HeadSHA == "" {
		return
	}
	data, err := yaml.Marshal(e.Config)
	if err != nil {
		return
	}
	sum := sha256.Sum256(data)
	p := &reviewProgress{policy: hex.EncodeToString(sum[:]), now: pr.HeadSHA, current: map[string]json.RawMessage{}, rank: map[string]int{}}
	if !e.Full && prior != nil {
		var record progressRecord
		if json.Unmarshal(prior.Progress, &record) == nil && record.Version == progressVersion {
			p.head = record.Head
			p.prior = record.Results
		}
	}
	e.progress = p
}

// savedHead is the revision the earlier attempt's checkpoint was taken at,
// empty when no checkpoint was read. It can name a head the forge has no
// completion marker for, which is the case that matters here: a run that
// crashed after saving progress left files this one can trust as unchanged.
func (p *reviewProgress) savedHead() string {
	if p == nil {
		return ""
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.head
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

// load answers cache hits. An empty key never matches: the standing-only
// recheck clears the key to force a live call, and every key a real batch
// renders is a hash of content that cannot be empty.
func (p *reviewProgress) load(key string) ([]Finding, bool) {
	return loadStage[[]Finding](p, key, rankAnalysis)
}

// loadStage reads one stored result as T. A record that does not decode is a
// miss, so a changed shape costs a request and never a wrong answer.
func loadStage[T any](p *reviewProgress, key string, rank int) (T, bool) {
	return loadStageChecked[T](p, key, rank, nil)
}

func loadStageChecked[T any](p *reviewProgress, key string, rank int, accept func(T) bool) (T, bool) {
	var result T
	if p == nil || key == "" {
		return result, false
	}
	p.mu.Lock()
	data, ok := p.prior[key]
	p.mu.Unlock()
	if !ok {
		return result, false
	}
	if json.Unmarshal(data, &result) != nil || (accept != nil && !accept(result)) {
		var zero T
		return zero, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.current[key] = data
	p.setRank(key, rank)
	p.reused++
	return result, true
}

// save records a batch's findings under the same key contract load reads.
func (p *reviewProgress) save(key string, findings []Finding) {
	saveStage(p, key, rankAnalysis, findings)
}

// saveStage records one completed result. The caller serializes nothing
// itself: the value is encoded here, before a later stage can change it.
func saveStage(p *reviewProgress, key string, rank int, value any) {
	if p == nil || key == "" {
		return
	}
	data, err := json.Marshal(value)
	if err != nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.current[key] = data
	p.setRank(key, rank)
}

// setRank is called with the lock held. The map is built on first use so a
// progress value assembled without one still orders its snapshot.
func (p *reviewProgress) setRank(key string, rank int) {
	if p.rank == nil {
		p.rank = map[string]int{}
	}
	p.rank[key] = rank
}

// stageKey names a stage's request. The stage and the head under review are in
// the hash, so a verdict from another stage or another head never matches.
func (p *reviewProgress) stageKey(stage string, client *llm.Client, system, request string, opts ...llms.CallOption) string {
	if p == nil {
		return ""
	}
	applied := *llms.ApplyOptions(opts...)
	applied.Trace = nil
	data, err := json.Marshal([]any{stageProgressVersion, request, applied})
	if err != nil {
		return ""
	}
	return p.key(client, stage+"\x00"+p.now+"\x00"+system, string(data))
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
	sort.Slice(keys, func(i, j int) bool {
		if p.rank[keys[i]] != p.rank[keys[j]] {
			return p.rank[keys[i]] < p.rank[keys[j]]
		}
		return keys[i] < keys[j]
	})
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

// stageKeyFor is stageKey for a request about particular files. A file in scope
// only to recheck an open comment on unchanged content must get a fresh check,
// so a request that names one has no key, which never matches and is never saved.
func (e *Engine) stageKeyFor(stage string, client *llm.Client, system, request string, paths []string, opts ...llms.CallOption) string {
	for _, path := range paths {
		if e.standingOnly[path] {
			return ""
		}
	}
	return e.progress.stageKey(stage, client, system, request, opts...)
}

func hasTriageAnswer(result TriageResult) bool {
	return len(result.Verdicts) > 0 || len(result.Dropped) > 0 || result.Summary != ""
}
