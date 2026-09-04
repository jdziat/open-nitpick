package llm

import (
	"fmt"
	"os"
	"strings"

	llms "github.com/nocturnium/llm-go-sdk"
)

// ProviderSynthetic is the name .nitpick.yaml selects Synthetic
// (https://synthetic.new) with.
//
// Synthetic hosts open-weight models behind an OpenAI-compatible endpoint on a
// subscription rather than per-token billing, which makes it the second
// provider worth a compiled-in endpoint: a committed config can name it and
// survive config.sanitize, for the same reason openrouter.go gives.
const ProviderSynthetic = "synthetic"

// syntheticBaseURL is the OpenAI-compatible surface. The Anthropic-compatible
// one at /anthropic/v1 serves the same models; the openai transport is what
// the structured-output negotiation in this package is written against.
const syntheticBaseURL = "https://api.synthetic.new/openai/v1"

// envSyntheticAPIKey holds the Synthetic credential.
const envSyntheticAPIKey = "SYNTHETIC_API_KEY"

func init() {
	llms.RegisterProvider(ProviderSynthetic, newSynthetic)
}

func newSynthetic(cfg llms.Config) (llms.LLM, error) {
	cfg, err := syntheticConfig(cfg, os.Getenv)
	if err != nil {
		return nil, err
	}
	return llms.New("openai", cfg)
}

// syntheticConfig fills in the Synthetic endpoint and credential, resolving
// SYNTHETIC_API_KEY before the provider-agnostic LLM_API_KEY and never
// OPENAI_API_KEY, by openRouterConfig's reasoning.
func syntheticConfig(cfg llms.Config, getenv func(string) string) (llms.Config, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		cfg.BaseURL = syntheticBaseURL
	}

	key := strings.TrimSpace(cfg.APIKey)
	if key == "" {
		key = strings.TrimSpace(getenv(envSyntheticAPIKey))
	}
	if key == "" {
		key = strings.TrimSpace(getenv(llms.EnvLLMAPIKey))
	}
	if key == "" {
		return cfg, fmt.Errorf("synthetic: %w (set %s or %s)",
			llms.ErrMissingAPIKey, envSyntheticAPIKey, llms.EnvLLMAPIKey)
	}
	cfg.APIKey = key

	return cfg, nil
}
