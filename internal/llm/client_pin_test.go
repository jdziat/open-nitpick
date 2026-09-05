package llm

import (
	"testing"

	llms "github.com/nocturnium/llm-go-sdk"

	"github.com/jdziat/open-nitpick/internal/config"
)

func TestProvidersPinIsOrderWithoutFallbacks(t *testing.T) {
	c := &Client{Spec: config.ModelSpec{Provider: "openrouter", Model: "google/gemma-4-31b-it", Providers: []string{"deepinfra/turbo"}}}
	applied := llms.ApplyOptions(c.CallOptions()...)
	routing, ok := applied.ExtraBody["provider"].(map[string]any)
	if !ok {
		t.Fatalf("no provider routing in the body: %+v", applied.ExtraBody)
	}
	order, _ := routing["order"].([]string)
	if len(order) != 1 || order[0] != "deepinfra/turbo" {
		t.Errorf("order = %v", order)
	}
	if fb, _ := routing["allow_fallbacks"].(bool); fb {
		t.Error("a pin with fallbacks allowed is a preference, not a pin")
	}

	c = &Client{Spec: config.ModelSpec{Provider: "openrouter", Model: "x"}}
	if _, ok := llms.ApplyOptions(c.CallOptions()...).ExtraBody["provider"]; ok {
		t.Error("no providers, no routing object")
	}
}
