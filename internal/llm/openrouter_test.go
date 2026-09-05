package llm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	llms "github.com/nocturnium/llm-go-sdk/v6"

	"github.com/jdziat/open-nitpick/internal/config"
)

// clearModelEnv empties every variable that can supply a model's credential OR
// its endpoint, so a test claiming "only OPENROUTER_API_KEY is set" is telling
// the truth on a developer machine that has the others exported.
//
// LLM_BASE_URL is in the list because config.LoadFile calls applyEnv AFTER
// sanitize, and applyEnv fills Models.Default.BaseURL whenever it is empty —
// which is exactly the post-sanitize state of the shipped config. Without this,
// TestDefaultConfigSurvivesSanitize passed or failed according to the
// developer's shell rather than according to the code.
func clearModelEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		envOpenRouterAPIKey, "OPENAI_API_KEY", llms.EnvLLMAPIKey,
		config.EnvBaseURL, config.EnvProvider, config.EnvModel,
	} {
		t.Setenv(name, "")
	}
}

// TestOpenRouterBuildsWithOnlyItsOwnKey is the shipped default's contract: a
// config naming openrouter, with nothing else in the environment, produces a
// client.
func TestOpenRouterBuildsWithOnlyItsOwnKey(t *testing.T) {
	clearModelEnv(t)
	t.Setenv(envOpenRouterAPIKey, "sk-or-test")

	if got := llms.RegisteredProviders(); !slices.Contains(got, ProviderOpenRouter) {
		t.Fatalf("RegisteredProviders() = %v, want it to contain %q", got, ProviderOpenRouter)
	}

	client, err := Build(config.ModelSpec{
		Provider: ProviderOpenRouter,
		Model:    "anthropic/claude-sonnet-4.6",
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if client.String() != "openrouter/anthropic/claude-sonnet-4.6" {
		t.Errorf("String() = %q", client.String())
	}
}

// TestOpenRouterMissingKeyIsNamed covers the failure an operator will actually
// hit. The OPENAI_API_KEY case is the one with teeth: delegating the lookup to
// the openai provider would accept that key, send a credential minted for
// api.openai.com to openrouter.ai, and report the wrong variable when it was
// rejected.
func TestOpenRouterMissingKeyIsNamed(t *testing.T) {
	clearModelEnv(t)
	t.Setenv("OPENAI_API_KEY", "sk-openai-must-not-be-used")

	_, err := Build(config.ModelSpec{Provider: ProviderOpenRouter, Model: "qwen/qwen3.7-flash"})
	if err == nil {
		t.Fatal("Build should fail with no OpenRouter credential, not borrow the OpenAI one")
	}
	if !errors.Is(err, llms.ErrMissingAPIKey) {
		t.Errorf("error should wrap ErrMissingAPIKey, got %v", err)
	}
	if !strings.Contains(err.Error(), envOpenRouterAPIKey) {
		t.Errorf("error must name the variable to set, got: %v", err)
	}
}

func TestOpenRouterConfigDefaults(t *testing.T) {
	env := func(vars map[string]string) func(string) string {
		return func(k string) string { return vars[k] }
	}

	t.Run("endpoint is supplied when the config cannot", func(t *testing.T) {
		got, err := openRouterConfig(llms.Config{}, env(map[string]string{envOpenRouterAPIKey: "k"}))
		if err != nil {
			t.Fatalf("openRouterConfig: %v", err)
		}
		// Spelled out rather than compared to openRouterBaseURL: restating the
		// constant would pass for any value, including the openai provider's
		// own default, which is precisely the failure this guards.
		if got.BaseURL != "https://openrouter.ai/api/v1" {
			t.Errorf("BaseURL = %q, want OpenRouter's endpoint", got.BaseURL)
		}
		if got.APIKey != "k" {
			t.Errorf("APIKey = %q, want the value of %s", got.APIKey, envOpenRouterAPIKey)
		}
	})

	t.Run("explicit values win", func(t *testing.T) {
		// Reachable only through a trusted config, but it must still behave:
		// self-hosted operators put an OpenRouter-compatible proxy in front.
		in := llms.Config{BaseURL: "https://proxy.example.com/v1", APIKey: "explicit"}
		got, err := openRouterConfig(in, env(map[string]string{envOpenRouterAPIKey: "from-env"}))
		if err != nil {
			t.Fatalf("openRouterConfig: %v", err)
		}
		if got.BaseURL != in.BaseURL {
			t.Errorf("BaseURL = %q, want the configured %q", got.BaseURL, in.BaseURL)
		}
		if got.APIKey != "explicit" {
			t.Errorf("APIKey = %q, want the explicit key", got.APIKey)
		}
	})

	t.Run("LLM_API_KEY is accepted", func(t *testing.T) {
		// The GitHub Action has no other channel for a key: it passes the
		// api-key input as LLM_API_KEY regardless of provider.
		got, err := openRouterConfig(llms.Config{}, env(map[string]string{llms.EnvLLMAPIKey: "generic"}))
		if err != nil {
			t.Fatalf("openRouterConfig: %v", err)
		}
		if got.APIKey != "generic" {
			t.Errorf("APIKey = %q, want the LLM_API_KEY value", got.APIKey)
		}
	})

	t.Run("the vendor-specific key beats the generic one", func(t *testing.T) {
		// The subtest above proves LLM_API_KEY is ACCEPTED, which is silent
		// about ORDER: swapping the two lookups in openRouterConfig left the
		// whole package green. Order is the point of this factory. An operator
		// with OPENROUTER_API_KEY set, and LLM_API_KEY exported by some other
		// tool for some other vendor, must not have that other credential sent
		// to openrouter.ai.
		got, err := openRouterConfig(llms.Config{}, env(map[string]string{
			envOpenRouterAPIKey: "openrouter-key",
			llms.EnvLLMAPIKey:   "some-other-vendors-key",
		}))
		if err != nil {
			t.Fatalf("openRouterConfig: %v", err)
		}
		if got.APIKey != "openrouter-key" {
			t.Errorf("APIKey = %q, want the %s value: the vendor-specific variable "+
				"must win over the provider-agnostic one", got.APIKey, envOpenRouterAPIKey)
		}
	})
}

// TestOpenRouterSendsResolvedKey proves the credential reaches the wire as an
// OpenAI-style bearer token, rather than merely being stored on a struct.
func TestOpenRouterSendsResolvedKey(t *testing.T) {
	clearModelEnv(t)
	t.Setenv(envOpenRouterAPIKey, "sk-or-test")

	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"1","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer srv.Close()

	// The stub is on loopback, which the SDK's SSRF guard blocks by default.
	// Both opt-ins are set here on the spec directly; a config file cannot do
	// this, which is the property TestDefaultConfigSurvivesSanitize covers.
	client, err := Build(config.ModelSpec{
		Provider:             ProviderOpenRouter,
		Model:                "qwen/qwen3.7-flash",
		BaseURL:              srv.URL,
		AllowPrivateEndpoint: true,
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if _, err := client.LLM.GenerateContent(context.Background(),
		[]llms.Message{{Role: llms.RoleUser, Content: "hi"}}); err != nil {
		t.Fatalf("GenerateContent: %v", err)
	}

	if gotAuth != "Bearer sk-or-test" {
		t.Errorf("Authorization = %q, want the OpenRouter key as a bearer token", gotAuth)
	}
	if !strings.HasSuffix(gotPath, "/chat/completions") {
		t.Errorf("path = %q, want the OpenAI-compatible chat completions route", gotPath)
	}
}

// TestDefaultConfigSurvivesSanitize is the property the whole provider exists
// for, asserted against the file this repository actually ships.
//
// sanitize() strips base_url and api_key_env from an untrusted config, so a
// default built on those keys would work only for whoever exported
// NITPICK_TRUST_CONFIG_ENDPOINTS. This asserts the committed default needs
// neither: nothing is dropped, and both roles build with only OPENROUTER_API_KEY
// in the environment.
//
// What it does NOT assert: that the endpoint is unreachable by any means. An
// operator who exports LLM_BASE_URL still redirects it, because applyEnv fills
// an empty BaseURL from the environment. That is deliberate — the environment
// belongs to whoever runs the tool, and the untrusted input this guards against
// is the config FILE, which a pull request can rewrite. clearModelEnv is what
// keeps the distinction from turning into a flaky assertion.
func TestDefaultConfigSurvivesSanitize(t *testing.T) {
	clearModelEnv(t)
	t.Setenv(config.EnvTrustConfigEndpoints, "")
	t.Setenv(envSyntheticAPIKey, "syn_test")

	// Relative to the package directory, which is where go test runs.
	cfg, err := config.LoadFile("../../.nitpick.yaml")
	if err != nil {
		t.Fatalf("load the repository's own config: %v", err)
	}

	if len(cfg.Dropped) != 0 {
		t.Errorf("Dropped = %v, want nothing dropped: the shipped default must not "+
			"depend on keys an untrusted config cannot supply", cfg.Dropped)
	}

	for _, role := range []config.Role{config.RoleReview, config.RoleTriage} {
		spec := cfg.Models.ResolveModel(role)
		if spec.Provider != ProviderSynthetic {
			t.Errorf("%s provider = %q, want %q", role, spec.Provider, ProviderSynthetic)
		}
		if spec.BaseURL != "" || spec.APIKeyEnv != "" {
			t.Errorf("%s names base_url=%q api_key_env=%q; the compiled-in endpoint is "+
				"the point, and these do not survive an untrusted config",
				role, spec.BaseURL, spec.APIKeyEnv)
		}
	}

	if _, err := BuildRoles(cfg); err != nil {
		t.Fatalf("the shipped default must build with only %s set: %v", envSyntheticAPIKey, err)
	}
}
