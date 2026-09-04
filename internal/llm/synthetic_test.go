package llm

import (
	"errors"
	"testing"

	llms "github.com/nocturnium/llm-go-sdk"
)

func TestSyntheticConfigDefaults(t *testing.T) {
	env := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}

	cfg, err := syntheticConfig(llms.Config{}, env(map[string]string{envSyntheticAPIKey: "syn-1", llms.EnvLLMAPIKey: "generic"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BaseURL != syntheticBaseURL {
		t.Errorf("base url = %q", cfg.BaseURL)
	}
	if cfg.APIKey != "syn-1" {
		t.Errorf("the vendor's own key must win over LLM_API_KEY, got %q", cfg.APIKey)
	}

	cfg, err = syntheticConfig(llms.Config{}, env(map[string]string{llms.EnvLLMAPIKey: "generic", "OPENAI_API_KEY": "sk-openai"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APIKey != "generic" {
		t.Errorf("LLM_API_KEY is the fallback, got %q", cfg.APIKey)
	}

	_, err = syntheticConfig(llms.Config{}, env(map[string]string{"OPENAI_API_KEY": "sk-openai"}))
	if !errors.Is(err, llms.ErrMissingAPIKey) {
		t.Errorf("OPENAI_API_KEY must never be sent to synthetic; err = %v", err)
	}

	cfg, err = syntheticConfig(llms.Config{BaseURL: "http://gateway.local/v1", APIKey: "explicit"}, env(nil))
	if err != nil || cfg.BaseURL != "http://gateway.local/v1" || cfg.APIKey != "explicit" {
		t.Errorf("explicit values are kept: %+v %v", cfg, err)
	}
}
