package llm

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"

	llms "github.com/nocturnium/llm-go-sdk/v6"

	"github.com/nitpick-oss/nitpick/internal/config"
)

// TestUnsetMaxTokensIsNotACap: with nothing configured, an OpenAI-compatible
// request carries no max_tokens so the model's own maximum applies, and the
// direct Anthropic path gets a floor above the SDK's 4,096 substitute. The one
// exception is OpenRouter with reasoning left on, which gets the first-attempt
// ceiling (issue #214).
func TestUnsetMaxTokensIsNotACap(t *testing.T) {
	has := func(opts []llms.CallOption) (int, bool) {
		var co llms.CallOptions
		for _, o := range opts {
			o(&co)
		}
		if co.MaxTokens == nil {
			return 0, false
		}
		return *co.MaxTokens, true
	}

	c := &Client{Spec: config.ModelSpec{Provider: "openai", Model: "gpt-4o-mini"}}
	if n, ok := has(c.CallOptions()); ok {
		t.Errorf("openai with no max_tokens configured sent %d; the model's own maximum should apply", n)
	}

	c = &Client{Spec: config.ModelSpec{Provider: "anthropic", Model: "claude-sonnet-4.6"}}
	if n, ok := has(c.CallOptions()); !ok || n != anthropicUnsetMaxTokens {
		t.Errorf("anthropic with no max_tokens configured sent %d/%v; the SDK would otherwise substitute 4096", n, ok)
	}

	c = &Client{Spec: config.ModelSpec{Provider: "anthropic", Model: "claude-sonnet-4.6", MaxTokens: 1024}}
	if n, _ := has(c.CallOptions()); n != 1024 {
		t.Errorf("a configured max_tokens must be sent as is, got %d", n)
	}
}

// A reasoning model with no cap can think for the whole call timeout, and the
// effort setting does not stop it on every upstream. OpenRouter requests that
// leave reasoning on carry the first-attempt ceiling; a cap the caller chose
// is kept, and turning reasoning off removes the need for one.
func TestOpenRouterReasoningCallsCarryAFirstAttemptCeiling(t *testing.T) {
	has := func(spec config.ModelSpec) (int, bool) {
		var co llms.CallOptions
		for _, o := range (&Client{Spec: spec}).CallOptions() {
			o(&co)
		}
		if co.MaxTokens == nil {
			return 0, false
		}
		return *co.MaxTokens, true
	}
	base := config.ModelSpec{Provider: "openrouter", Model: "deepseek/deepseek-v4.1-flash"}

	if n, ok := has(base); !ok || n != firstAttemptMaxTokens {
		t.Errorf("reasoning unset sent %d/%v, want the first-attempt ceiling %d", n, ok, firstAttemptMaxTokens)
	}
	low := base
	low.Reasoning = config.ReasoningLow
	if n, ok := has(low); !ok || n != firstAttemptMaxTokens {
		t.Errorf("reasoning low sent %d/%v, want the ceiling: effort low did not bound DeepSeek", n, ok)
	}
	off := base
	off.Reasoning = config.ReasoningOff
	if n, ok := has(off); ok {
		t.Errorf("reasoning off sent max_tokens %d; nothing is thinking, so nothing needs a ceiling", n)
	}
	chosen := base
	chosen.MaxTokens = 4096
	if n, _ := has(chosen); n != 4096 {
		t.Errorf("a configured max_tokens must be sent as is, got %d", n)
	}
}

// creditLLM answers 402 until a call carries a max_tokens cap, the way
// OpenRouter refuses to reserve a model's full output cap against a key that
// cannot afford it.
type creditLLM struct{ calls int }

func (c *creditLLM) GenerateContent(_ context.Context, _ []llms.Message, opts ...llms.CallOption) (*llms.Response, error) {
	c.calls++
	var co llms.CallOptions
	for _, o := range opts {
		o(&co)
	}
	if co.MaxTokens == nil {
		return nil, errors.New("API error (status 402): This request requires more credits, or fewer max_tokens.") //nolint:revive // Match the provider response that triggers the retry.
	}
	return &llms.Response{Content: `{"findings":[],"summary":"ok"}`}, nil
}
func (c *creditLLM) Stream(context.Context, []llms.Message, ...llms.CallOption) (<-chan llms.StreamChunk, error) {
	return nil, errors.New("not supported")
}
func (c *creditLLM) Provider() llms.Provider { return "credit" }
func (c *creditLLM) Model() string           { return "credit" }

// Reasoning is off here because an OpenRouter request that leaves it on already
// goes out under the first-attempt ceiling and never meets the 402.
func TestACreditCappedRequestIsRetriedWithACap(t *testing.T) {
	inner := &creditLLM{}
	c := NewClientForTest(inner, config.ModelSpec{Provider: "openrouter", Model: "x", Reasoning: config.ReasoningOff})
	type out struct {
		Findings []struct{} `json:"findings"`
		Summary  string     `json:"summary"`
	}
	v, err := Extract[out](context.Background(), c, []llms.Message{{Role: llms.RoleUser, Content: "hi"}})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if v.Summary != "ok" || inner.calls < 2 {
		t.Errorf("summary=%q calls=%d; the 402 must be retried once under a cap", v.Summary, inner.calls)
	}
}

// emptyLLM answers empty at the length cap until a call turns reasoning off in
// the way OpenRouter reads it, the way a reasoning model does when its
// thinking spends the whole budget.
type emptyLLM struct {
	calls  int
	offSet []bool
}

func (e *emptyLLM) GenerateContent(_ context.Context, _ []llms.Message, opts ...llms.CallOption) (*llms.Response, error) {
	e.calls++
	var co llms.CallOptions
	for _, o := range opts {
		o(&co)
	}
	r, _ := co.ExtraBody["reasoning"].(map[string]any)
	off := r["enabled"] == false
	e.offSet = append(e.offSet, off)
	if !off {
		return &llms.Response{Content: "", FinishReason: llms.FinishReasonLength,
			Usage: llms.Usage{CompletionTokens: 16384, ReasoningTokens: 16384}}, nil
	}
	return &llms.Response{Content: `{"findings":[],"summary":"ok"}`, FinishReason: llms.FinishReasonStop}, nil
}
func (e *emptyLLM) Stream(context.Context, []llms.Message, ...llms.CallOption) (<-chan llms.StreamChunk, error) {
	return nil, errors.New("not supported")
}
func (e *emptyLLM) Provider() llms.Provider { return "empty" }
func (e *emptyLLM) Model() string           { return "empty" }

// The retry after an empty answer at the cap says "reasoning off". On OpenRouter
// that has to be OpenRouter's own field, or the upstream keeps thinking and hits
// the cap a second time.
func TestTheEmptyAnswerRetryTurnsReasoningOffTheWayOpenRouterReadsIt(t *testing.T) {
	inner := &emptyLLM{}
	c := NewClientForTest(inner, config.ModelSpec{Provider: "openrouter", Model: "deepseek/deepseek-v4.1-flash"})
	type out struct {
		Findings []struct{} `json:"findings"`
		Summary  string     `json:"summary"`
	}
	v, err := Extract[out](context.Background(), c, []llms.Message{{Role: llms.RoleUser, Content: "hi"}})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if v.Summary != "ok" || len(inner.offSet) != 2 || inner.offSet[0] || !inner.offSet[1] {
		t.Errorf("summary=%q reasoning-off per call=%v, want the first call on and the retry off", v.Summary, inner.offSet)
	}
}

// A slow review is diagnosed from the log, so the line that says a call was cut
// at the cap has to say what the call spent.
func TestTheCapWarningSaysWhatTheCappedCallSpent(t *testing.T) {
	var buf bytes.Buffer
	inner := &emptyLLM{}
	c := NewClientForTest(inner, config.ModelSpec{Provider: "openrouter", Model: "deepseek/deepseek-v4.1-flash"})
	c.SetLogger(slog.New(slog.NewTextHandler(&buf, nil)))
	type out struct {
		Findings []struct{} `json:"findings"`
		Summary  string     `json:"summary"`
	}
	if _, err := Extract[out](context.Background(), c, []llms.Message{{Role: llms.RoleUser, Content: "hi"}}); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	for _, want := range []string{"completion_tokens=16384", "reasoning_tokens=16384", "elapsed="} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("log lacks %q:\n%s", want, buf.String())
		}
	}
}

// mandatoryLLM is a model that always reasons: an empty answer at the cap, and
// a 400 for any request that asks it to stop.
type mandatoryLLM struct{ calls, refused int }

func (m *mandatoryLLM) GenerateContent(_ context.Context, _ []llms.Message, opts ...llms.CallOption) (*llms.Response, error) {
	m.calls++
	var co llms.CallOptions
	for _, o := range opts {
		o(&co)
	}
	if r, _ := co.ExtraBody["reasoning"].(map[string]any); r["enabled"] == false {
		m.refused++
		return nil, errors.New("API error (status 400, code 400): Reasoning is mandatory for this endpoint and cannot be disabled.")
	}
	return &llms.Response{Content: "", FinishReason: llms.FinishReasonLength,
		Usage: llms.Usage{CompletionTokens: 16384, ReasoningTokens: 16384}}, nil
}
func (m *mandatoryLLM) Stream(context.Context, []llms.Message, ...llms.CallOption) (<-chan llms.StreamChunk, error) {
	return nil, errors.New("not supported")
}
func (m *mandatoryLLM) Provider() llms.Provider { return "mandatory" }
func (m *mandatoryLLM) Model() string           { return "mandatory" }

// A model that refuses to stop reasoning is asked once, on its first batch.
// Every later batch that ends empty at the cap goes straight to escalation
// without a request that is refused.
func TestAModelThatCannotStopReasoningIsNotAskedToAgain(t *testing.T) {
	inner := &mandatoryLLM{}
	c := NewClientForTest(inner, config.ModelSpec{Provider: "openrouter", Model: "z-ai/glm-5.3-flash"})
	type out struct {
		Summary string `json:"summary"`
	}
	msgs := []llms.Message{{Role: llms.RoleUser, Content: "hi"}}

	_, first := Extract[out](context.Background(), c, msgs)
	if !ShouldEscalate(first) {
		t.Fatalf("first batch error %v does not escalate", first)
	}
	if inner.refused != 1 || !c.ReasoningIsMandatory() {
		t.Fatalf("first batch: refused=%d mandatory=%v, want one refusal learned", inner.refused, c.ReasoningIsMandatory())
	}

	before := inner.calls
	_, second := Extract[out](context.Background(), c, msgs)
	if !ShouldEscalate(second) {
		t.Errorf("second batch error %v does not escalate", second)
	}
	if inner.refused != 1 {
		t.Errorf("a refused request was sent again: refused=%d", inner.refused)
	}
	if got := inner.calls - before; got != 1 {
		t.Errorf("second batch made %d requests, want the one capped call and no JSON-fallback repeat", got)
	}
}

// Rule 10: a model that allows reasoning off must keep its retry, or the
// learned flag has silenced a recovery that works.
func TestAModelThatAllowsReasoningOffStillGetsTheRetry(t *testing.T) {
	inner := &emptyLLM{}
	c := NewClientForTest(inner, config.ModelSpec{Provider: "openrouter", Model: "deepseek/deepseek-v4.1-flash"})
	type out struct {
		Summary string `json:"summary"`
	}
	for i := 0; i < 2; i++ {
		if _, err := Extract[out](context.Background(), c, []llms.Message{{Role: llms.RoleUser, Content: "hi"}}); err != nil {
			t.Fatalf("batch %d: %v", i, err)
		}
	}
	if c.ReasoningIsMandatory() {
		t.Error("a model that accepted reasoning off was marked mandatory")
	}
	// Whatever one batch sends, the next sends the same: learning nothing
	// from a model that accepted the retry.
	half := len(inner.offSet) / 2
	for i := 0; i < half; i++ {
		if inner.offSet[i] != inner.offSet[half+i] {
			t.Fatalf("second batch sent a different sequence: %v", inner.offSet)
		}
	}
	if !slices.Contains(inner.offSet, true) {
		t.Errorf("no reasoning-off retry was ever sent: %v", inner.offSet)
	}
}
