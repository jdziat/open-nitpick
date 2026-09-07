package llm

import (
	"testing"

	llms "github.com/nocturnium/llm-go-sdk/v6"

	"github.com/jdziat/open-nitpick/internal/config"
)

// The pin restricts the candidate set rather than ranking it.
//
// "order" ranks whatever the other filters leave, so a pin sent that way with
// allow_fallbacks false can end with nothing left and answer "No endpoints
// found" for providers that all serve the model. See issue #46.
func TestProvidersPinRestrictsRatherThanRanks(t *testing.T) {
	c := &Client{Spec: config.ModelSpec{Provider: "openrouter", Model: "google/gemma-4-31b-it", Providers: []string{"deepinfra/turbo"}}}
	applied := llms.ApplyOptions(c.CallOptions()...)
	routing, ok := applied.ExtraBody["provider"].(map[string]any)
	if !ok {
		t.Fatalf("no provider routing in the body: %+v", applied.ExtraBody)
	}
	only, _ := routing["only"].([]string)
	if len(only) != 1 || only[0] != "deepinfra/turbo" {
		t.Errorf("only = %v", only)
	}
	if _, ranked := routing["order"]; ranked {
		t.Error("the pin was sent as order, which ranks a set rather than restricting it")
	}
	if fb, _ := routing["allow_fallbacks"].(bool); fb {
		t.Error("a pin with fallbacks allowed is a preference, not a pin")
	}

	c = &Client{Spec: config.ModelSpec{Provider: "openrouter", Model: "x"}}
	if _, ok := llms.ApplyOptions(c.CallOptions()...).ExtraBody["provider"]; ok {
		t.Error("no providers, no routing object")
	}
}
