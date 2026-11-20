package llm

import (
	"context"
	"slices"
	"sync"
	"time"

	llms "github.com/nocturnium/llm-go-sdk/v6"
)

// CallRecord is one provider attempt, including attempts retried by the SDK.
// Usage is absent when the provider did not report billable token counts.
type CallRecord struct {
	Provider   string      `json:"provider"`
	Model      string      `json:"model"`
	Role       string      `json:"role"`
	StartedAt  time.Time   `json:"started_at"`
	DurationMS int64       `json:"duration_ms"`
	Failed     bool        `json:"failed"`
	Usage      *llms.Usage `json:"usage"`
}

type callRoleKey struct{}
type callObserverKey struct{}

// WithCallRole attributes attempts to a stage even when stages share a client.
func WithCallRole(ctx context.Context, role string) context.Context {
	return context.WithValue(ctx, callRoleKey{}, role)
}

// CallRole identifies the stage making this request.
func CallRole(ctx context.Context) string {
	role, _ := ctx.Value(callRoleKey{}).(string)
	if role == "" {
		return "unclassified"
	}
	return role
}

// WithCallObserver records non-chat usage in the same review ledger.
func WithCallObserver(ctx context.Context, fn func(CallRecord)) context.Context {
	return context.WithValue(ctx, callObserverKey{}, fn)
}

// ObserveCalls attaches an observer before this client is used. Built clients
// observe inside resilience so failed retry attempts cannot disappear.
func (c *Client) ObserveCalls(fn func(CallRecord)) {
	if c.observed == nil {
		c.observed = &observedLLM{inner: c.LLM, model: c.Spec.Model}
		c.LLM = c.observed
	}
	c.observed.mu.Lock()
	defer c.observed.mu.Unlock()
	c.observed.observers = append(c.observed.observers, fn)
}

type observedLLM struct {
	inner     llms.LLM
	model     string
	mu        sync.RWMutex
	observers []func(CallRecord)
}

func (o *observedLLM) Provider() llms.Provider { return o.inner.Provider() }
func (o *observedLLM) Model() string           { return o.model }

func (o *observedLLM) GenerateContent(ctx context.Context, msgs []llms.Message, opts ...llms.CallOption) (*llms.Response, error) {
	o.mu.RLock()
	observers := slices.Clone(o.observers)
	o.mu.RUnlock()
	started := time.Now().UTC()
	resp, err := o.inner.GenerateContent(ctx, msgs, opts...)
	if len(observers) == 0 {
		return resp, err
	}
	role, _ := ctx.Value(callRoleKey{}).(string)
	if role == "" {
		role = "unclassified"
	}
	record := CallRecord{Provider: string(o.Provider()), Model: o.model, Role: role,
		StartedAt: started, DurationMS: time.Since(started).Milliseconds(), Failed: err != nil}
	if resp != nil && reportedUsage(resp.Usage) {
		usage := resp.Usage
		record.Usage = &usage
	}
	for _, fn := range observers {
		fn(record)
	}
	return resp, err
}

func (o *observedLLM) Stream(ctx context.Context, msgs []llms.Message, opts ...llms.CallOption) (<-chan llms.StreamChunk, error) {
	started := time.Now().UTC()
	input, err := o.inner.Stream(ctx, msgs, opts...)
	if err != nil {
		o.recordStream(ctx, started, nil, true)
		return nil, err
	}
	output := make(chan llms.StreamChunk)
	go func() {
		defer close(output)
		var usage *llms.Usage
		failed := true
		defer func() { o.recordStream(ctx, started, usage, failed) }()
		for {
			select {
			case <-ctx.Done():
				return
			case chunk, ok := <-input:
				if !ok {
					return
				}
				if chunk.Usage != nil {
					copied := *chunk.Usage
					usage = &copied
				}
				select {
				case output <- chunk:
				case <-ctx.Done():
					return
				}
				if chunk.Error != nil {
					return
				}
				if chunk.Done {
					failed = false
					return
				}
			}
		}
	}()
	return output, nil
}

func (o *observedLLM) recordStream(ctx context.Context, started time.Time, usage *llms.Usage, failed bool) {
	o.mu.RLock()
	observers := slices.Clone(o.observers)
	o.mu.RUnlock()
	role, _ := ctx.Value(callRoleKey{}).(string)
	record := CallRecord{Provider: string(o.Provider()), Model: o.Model(), Role: role, StartedAt: started,
		DurationMS: time.Since(started).Milliseconds(), Failed: failed, Usage: usage}
	for _, observe := range observers {
		observe(record)
	}
}

func reportedUsage(u llms.Usage) bool {
	return u.PromptTokens >= 0 && u.CompletionTokens >= 0 && u.CacheReadTokens >= 0 &&
		u.CacheCreationTokens >= 0 && u.ReasoningTokens >= 0 &&
		u.PromptTokens+u.CompletionTokens+u.CacheReadTokens+u.CacheCreationTokens > 0
}
