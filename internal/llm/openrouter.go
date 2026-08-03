package llm

import (
	"fmt"
	"os"
	"strings"

	llms "github.com/nocturnium/llm-go-sdk"
)

// ProviderOpenRouter is the name .nitpick.yaml selects OpenRouter with.
//
// Exported so the eval harness names the same provider production does. It
// previously rebuilt an equivalent out of provider: openai + base_url, which is
// how the harness and the shipped client came to resolve their endpoint and
// their credential by different rules.
const ProviderOpenRouter = "openrouter"

// openRouterBaseURL is compiled in rather than configured, which is the entire
// reason this provider exists instead of a documented base_url.
//
// config.sanitize strips base_url and api_key_env from any config file that has
// not been trusted out of band, because a pull request can edit the file it is
// reviewed under and redirect the reviewer at an endpoint it controls. So a
// committed default written as provider: openai + base_url works only for
// whoever exported NITPICK_TRUST_CONFIG_ENDPOINTS, and silently sends everyone
// else's review to api.openai.com under a model id that provider has never
// heard of. An endpoint the config cannot name survives sanitize.
//
// Scope of that claim, because a router earns a caveat the other providers do
// not: what an untrusted config cannot choose is the FIRST hop and the bearer
// token. sanitize does not strip provider: or model:, and for a router the
// model id is the routing key that selects which upstream operator receives the
// request — including the full file bodies review.include_full_files sends. So
// a pull request editing its own .nitpick.yaml can still change which third
// party reads the code under review, and can still point the review at a weak
// or free-tier model. That is one line of an unreviewed-config diff, and it is
// the residual risk this provider does not remove; NITPICK_TRUST_CONFIG_ENDPOINTS
// is orthogonal to it. Reviewing .nitpick.yaml changes on their own merits is
// the mitigation. See README's trust model.
const openRouterBaseURL = "https://openrouter.ai/api/v1"

// envOpenRouterAPIKey holds the OpenRouter credential.
const envOpenRouterAPIKey = "OPENROUTER_API_KEY"

// init registers the provider.
//
// Package init is what makes the registration unskippable: every path that
// validates a provider name or builds a client calls a function in this
// package, and Go runs a package's init before any of its functions. It also
// orders correctly against the blank import of pkg/providers/all in client.go —
// imported packages initialize first, so "openai" is already registered by the
// time the factory below can delegate to it.
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
// an empty key the openai provider falls back to OPENAI_API_KEY — which would
// send a credential minted for api.openai.com to openrouter.ai, and then blame
// the wrong environment variable when that is rejected. LLM_API_KEY is kept
// because it is provider-agnostic by definition, and it is the only channel the
// GitHub Action has for passing a key.
//
// The ORDER is load-bearing, not incidental: OPENROUTER_API_KEY names this
// vendor and LLM_API_KEY names none, so the specific one wins. Reversed, an
// operator who has OPENROUTER_API_KEY set and LLM_API_KEY exported by some
// other tool would send that other vendor's credential to openrouter.ai —
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
