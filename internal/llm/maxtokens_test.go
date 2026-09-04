package llm

import (
	"context"
	"errors"
	"testing"

	llms "github.com/nocturnium/llm-go-sdk"

	"github.com/jdziat/open-nitpick/internal/config"
)

// TestUnsetMaxTokensIsNotACap: with nothing configured, an OpenAI-compatible
// request carries no max_tokens so the model's own maximum applies, and the
// direct Anthropic path gets a floor above the SDK's 4,096 substitute.
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

	c := &Client{Spec: config.ModelSpec{Provider: "openrouter", Model: "z-ai/glm-5.3-flash"}}
	if n, ok := has(c.CallOptions()); ok {
		t.Errorf("openrouter with no max_tokens configured sent %d; the model's own maximum should apply", n)
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
		return nil, errors.New("API error (status 402): This request requires more credits, or fewer max_tokens.")
	}
	return &llms.Response{Content: `{"findings":[],"summary":"ok"}`}, nil
}
func (c *creditLLM) Stream(context.Context, []llms.Message, ...llms.CallOption) (<-chan llms.StreamChunk, error) {
	return nil, errors.New("not supported")
}
func (c *creditLLM) Provider() llms.Provider { return "credit" }
func (c *creditLLM) Model() string           { return "credit" }

func TestACreditCappedRequestIsRetriedWithACap(t *testing.T) {
	inner := &creditLLM{}
	c := NewClientForTest(inner, config.ModelSpec{Provider: "openrouter", Model: "x"})
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
