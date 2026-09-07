package llm

import (
	"fmt"
	"os"
	"strings"

	llms "github.com/nocturnium/llm-go-sdk/v6"
)

// ProviderOpenRouter is the name .nitpick.yaml selects OpenRouter with.
// Exported so the eval harness names the same provider production does rather
// than rebuilding an equivalent out of provider: openai plus base_url, which
// resolves the endpoint and the credential by a different set of rules.
const ProviderOpenRouter = "openrouter"

// openRouterBaseURL is compiled in rather than configured, which is why this
// provider exists instead of a documented base_url: config.sanitize strips
// base_url from an untrusted config, so the same default written as provider:
// openai plus base_url would send every review without
// NITPICK_TRUST_CONFIG_ENDPOINTS to api.openai.com under a model id that
// provider never heard of. sanitize leaves model: alone, so a router's routing
// key stays the pull request's to choose. See docs/trust-model.md.
const openRouterBaseURL = "https://openrouter.ai/api/v1"

// envOpenRouterAPIKey holds the OpenRouter credential.
const envOpenRouterAPIKey = "OPENROUTER_API_KEY"

// init registers the provider. Package init makes the registration
// unskippable: every path that validates a provider name or builds a client
// calls a function in this package, and Go runs init before any of them. It
// also orders correctly against the blank import of pkg/providers/all in
// client.go, since imported packages initialize first, so "openai" is
// registered by the time the factory below delegates to it.
func init() {
	llms.RegisterProvider(ProviderOpenRouter, newOpenRouter)
}

// newOpenRouter builds an OpenRouter client. OpenRouter serves the OpenAI chat
// completions API, so the openai provider is the transport and this factory
// supplies only the two things that differ.
func newOpenRouter(cfg llms.Config) (llms.LLM, error) {
	cfg, err := openRouterConfig(cfg, os.Getenv)
	if err != nil {
		return nil, err
	}
	return llms.New("openai", cfg)
}

// openRouterConfig fills in the OpenRouter endpoint and credential.
//
// Resolving the key here, and failing when there is none, is deliberate. Handed
// an empty key the openai provider falls back to OPENAI_API_KEY, which would
// send a credential minted for api.openai.com to openrouter.ai, and then blame
// the wrong environment variable when that is rejected. LLM_API_KEY is kept
// because it is provider-agnostic by definition, and it is the only channel the
// GitHub Action has for passing a key.
//
// The ORDER is load-bearing, not incidental: OPENROUTER_API_KEY names this
// vendor and LLM_API_KEY names none, so the specific one wins. Reversed, an
// operator who has OPENROUTER_API_KEY set and LLM_API_KEY exported by some
// other tool would send that other vendor's credential to openrouter.ai,
// the same failure the paragraph above exists to prevent, by a different route.
// TestOpenRouterConfigDefaults pins it.
func openRouterConfig(cfg llms.Config, getenv func(string) string) (llms.Config, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		cfg.BaseURL = openRouterBaseURL
	}

	key := strings.TrimSpace(cfg.APIKey)
	if key == "" {
		key = strings.TrimSpace(getenv(envOpenRouterAPIKey))
	}
	if key == "" {
		key = strings.TrimSpace(getenv(llms.EnvLLMAPIKey))
	}
	if key == "" {
		return cfg, fmt.Errorf("openrouter: %w (set %s or %s)",
			llms.ErrMissingAPIKey, envOpenRouterAPIKey, llms.EnvLLMAPIKey)
	}
	cfg.APIKey = key

	return cfg, nil
}
