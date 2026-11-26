package llm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"sync/atomic"

	llms "github.com/nocturnium/llm-go-sdk/v6"
)

// Ledger answers a model request from a record kept elsewhere and keeps the
// answers that were paid for. The notary is the one implementation: its record
// is what lets the same inputs replay with no model call and an interrupted
// review ask only for what is missing.
type Ledger interface {
	// Lookup returns the recorded answer for a request key.
	Lookup(ctx context.Context, key string) (*llms.Response, bool)
	// Record keeps an answer a live call produced.
	Record(ctx context.Context, key string, resp *llms.Response)
}

// ledgerLLM sits outside the resilience wrapper, so a recorded answer skips
// the retries and the usage meter along with the provider: a replay that
// still counted calls would say it had asked a model.
type ledgerLLM struct {
	inner  llms.LLM
	ledger atomic.Pointer[ledgerBox]
}

type ledgerBox struct{ l Ledger }

func (l *ledgerLLM) Provider() llms.Provider { return l.inner.Provider() }
func (l *ledgerLLM) Model() string           { return l.inner.Model() }

func (l *ledgerLLM) Stream(ctx context.Context, msgs []llms.Message, opts ...llms.CallOption) (<-chan llms.StreamChunk, error) {
	return l.inner.Stream(ctx, msgs, opts...)
}

func (l *ledgerLLM) GenerateContent(ctx context.Context, msgs []llms.Message, opts ...llms.CallOption) (*llms.Response, error) {
	box := l.ledger.Load()
	if box == nil || box.l == nil {
		return l.inner.GenerateContent(ctx, msgs, opts...)
	}
	key, ok := l.requestKey(msgs, opts)
	if !ok {
		return l.inner.GenerateContent(ctx, msgs, opts...)
	}
	if resp, hit := box.l.Lookup(ctx, key); hit {
		return resp, nil
	}
	resp, err := l.inner.GenerateContent(ctx, msgs, opts...)
	if err == nil && recordable(resp) && wellFormed(resp, opts) {
		box.l.Record(ctx, key, resp)
	}
	return resp, err
}

// requestKey names a request by everything that shapes its answer except the
// reasoning level. Trace is dropped because it carries a per-call callback.
func (l *ledgerLLM) requestKey(msgs []llms.Message, opts []llms.CallOption) (string, bool) {
	applied := *llms.ApplyOptions(opts...)
	applied.Trace = nil
	// The reasoning level is recorded beside the answers rather than keyed on:
	// a replay must find the answers the recorded level produced, whatever the
	// configuration says today. See wire.Record.Effort.
	applied.Reasoning = nil
	data, err := json.Marshal([]any{string(l.inner.Provider()), l.inner.Model(), msgs, applied})
	if err != nil {
		return "", false
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), true
}

// over returns a wrapper with the same ledger around another model.
func (l *ledgerLLM) over(inner llms.LLM) llms.LLM {
	next := &ledgerLLM{inner: inner}
	next.ledger.Store(l.ledger.Load())
	return next
}

// UseLedger routes this client's requests through l, and stops doing so for
// nil. Calling it again swaps the ledger rather than stacking a second wrapper,
// because one client serves every review a long-lived engine runs.
func (c *Client) UseLedger(l Ledger) {
	if c.ledgered == nil {
		if l == nil {
			return
		}
		c.ledgered = &ledgerLLM{inner: c.LLM}
		c.LLM = c.ledgered
	}
	c.ledgered.ledger.Store(&ledgerBox{l: l})
}

// UseLedger routes every client the roles hold, and every client built later
// for a route or an ensemble, through l.
func (r *Roles) UseLedger(l Ledger) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.ledger = l
	r.mu.Unlock()
	r.Each(func(c *Client) { c.UseLedger(l) })
}

// recordable keeps an answer only when it finished. It is looser than the
// response cache's test in one way: a provider that reports no finish reason
// still counts, because refusing it would make the notary record nothing for
// such a provider and call the silence a clean review. A truncated or filtered
// answer is refused, since replaying it would repeat the truncation forever.
func recordable(resp *llms.Response) bool {
	if resp == nil {
		return false
	}
	switch resp.FinishReason {
	case "", llms.FinishReasonStop, llms.FinishReasonToolCalls:
		return strings.TrimSpace(resp.Content) != "" || len(resp.ToolCalls) > 0
	default:
		return false
	}
}

// wellFormed refuses to keep a JSON answer that is not a usable JSON object. The ledger sees
// the raw reply before the caller parses it, so without this a reply that
// failed to parse would be recorded, replayed on the next run, and fail the
// same way every time: a partial record that resume could never finish.
// Replies that carry tool calls, and requests that asked for no JSON, are kept
// as they come.
func wellFormed(resp *llms.Response, opts []llms.CallOption) bool {
	if len(resp.ToolCalls) > 0 || llms.ApplyOptions(opts...).ResponseFormat == nil {
		return true
	}
	// A populated object, because that is what every structured request in this
	// tool decodes into. {} and null are valid JSON that the decoder rejects on
	// every later run, and a record cannot be replaced once it holds one.
	var object map[string]json.RawMessage
	return json.Unmarshal([]byte(extractJSONText(resp.Content)), &object) == nil && len(object) > 0
}

// extractJSONText strips the fence a model sometimes wraps a JSON answer in.
func extractJSONText(s string) string {
	s = strings.TrimSpace(s)
	if rest, ok := strings.CutPrefix(s, "```"); ok {
		if i := strings.Index(rest, "\n"); i >= 0 {
			rest = rest[i+1:]
		}
		s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(rest), "```"))
	}
	return s
}
