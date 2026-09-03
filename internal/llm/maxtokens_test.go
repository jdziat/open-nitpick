package llm

import (
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
