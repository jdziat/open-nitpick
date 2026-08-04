// Package llm builds configured model clients and constrains their output to a
// schema.
//
// Provider construction goes through the SDK's by-name registry, so every
// provider the SDK supports — including local ollama and llama.cpp servers and
// any OpenAI-compatible gateway reached via base_url — is usable from
// .nitpick.yaml without changes here.
package llm

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	llms "github.com/nocturnium/llm-go-sdk"
	_ "github.com/nocturnium/llm-go-sdk/pkg/providers/all" // register every provider with llms.New

	"github.com/jdziat/open-nitpick/internal/config"
)

// defaultMaxRetries bounds transient-failure retries when the config is silent.
const defaultMaxRetries = 3

// Client is a configured model ready to answer review requests.
type Client struct {
	// LLM is the underlying SDK client, already wrapped for resilience.
	LLM llms.LLM

	// Spec is the configuration this client was built from.
	Spec config.ModelSpec

	// mode is the resolved structured-output strategy. It starts from the
	// spec and may be downgraded at runtime when a provider rejects the
	// schema response format. modeMu guards it because review runs call a
	// single client from concurrent workers.
	mode   config.StructuredMode
	modeMu sync.RWMutex
}

// Provider returns the configured provider name.
func (c *Client) Provider() string { return c.Spec.Provider }

// Model returns the configured model name.
func (c *Client) Model() string { return c.Spec.Model }

// String describes the client as provider/model, for logs and errors.
func (c *Client) String() string { return c.Spec.Provider + "/" + c.Spec.Model }

// Build constructs a client from a model spec. Unknown providers and missing
// credentials fail here, before any request is made.
func Build(spec config.ModelSpec) (*Client, error) {
	if err := validateProvider(spec.Provider); err != nil {
		return nil, err
	}

	cfg := llms.Config{
		Model:   strings.TrimSpace(spec.Model),
		BaseURL: strings.TrimSpace(spec.BaseURL),
		Timeout: spec.Timeout,
		Extra:   spec.Extra,

		// Opt-in only. Providers that target localhost by design (ollama,
		// llamacpp) enable this themselves, so leaving it off here does not
		// break the ordinary local-model path.
		AllowPrivateIPs: spec.AllowPrivateEndpoint,
	}
	if key, ok := spec.APIKey(nil); ok {
		cfg.APIKey = key
	} else if spec.APIKeyEnv != "" {
		return nil, fmt.Errorf("model %s/%s: %s is empty", spec.Provider, spec.Model, spec.APIKeyEnv)
	}

	client, err := llms.New(spec.Provider, cfg)
	if err != nil {
		return nil, fmt.Errorf("build model %s/%s: %w", spec.Provider, spec.Model, err)
	}

	maxRetries := defaultMaxRetries
	if spec.MaxRetries != nil {
		maxRetries = *spec.MaxRetries
	}
	resilient := llms.NewResilientClient(client, llms.WithMaxRetries(maxRetries))

	mode := spec.StructuredOutput
	if mode == "" {
		mode = config.StructuredAuto
	}

	return &Client{LLM: resilient, Spec: spec, mode: mode}, nil
}

// NewClientForTest wraps an arbitrary SDK client, bypassing provider
// construction. It exists so the review engine can be exercised end to end
// against a scripted model with no network and no credentials, which is the
// only way to test the pipeline's behavior rather than a provider's.
func NewClientForTest(client llms.LLM, spec config.ModelSpec) *Client {
	mode := spec.StructuredOutput
	if mode == "" {
		mode = config.StructuredAuto
	}
	return &Client{LLM: client, Spec: spec, mode: mode}
}

// validateProvider checks a provider name against the SDK registry and reports
// the available names when it does not match, since a typo here is the most
// likely configuration mistake.
func validateProvider(name string) error {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return fmt.Errorf("provider is required")
	}

	available := llms.RegisteredProviders()
	if slices.Contains(available, name) {
		return nil
	}

	sorted := slices.Sorted(slices.Values(available))
	return fmt.Errorf("unknown provider %q (available: %s)", name, strings.Join(sorted, ", "))
}

// Roles holds the clients a review run uses. BuildRoles fills every field; when
// the config names no override they are distinct clients built from the same
// spec.
type Roles struct {
	Review *Client
	Triage *Client

	// Validate is the expert-validation client. It may be nil — callers that
	// assemble Roles by hand, such as the eval harness, only name the roles
	// they are measuring — so read it through Validator rather than directly.
	Validate *Client
}

// Validator returns the client the expert-validation pass speaks through,
// falling back to the review client.
//
// The fallback is what makes validation A/B-able by flipping one config field:
// a harness that wires Roles itself and never heard of this role still gets a
// working validation pass, and validating with the reviewing model is the
// honest default when nothing else was named.
func (r *Roles) Validator() *Client {
	if r == nil {
		return nil
	}
	if r.Validate != nil {
		return r.Validate
	}
	return r.Review
}

// BuildRoles constructs every client a review run needs. Building them all up
// front means a misconfigured triage or validation model fails immediately
// rather than after the expensive review calls have already been paid for.
func BuildRoles(cfg *config.Config) (*Roles, error) {
	review, err := Build(cfg.Models.ResolveModel(config.RoleReview))
	if err != nil {
		return nil, fmt.Errorf("review model: %w", err)
	}

	triage, err := Build(cfg.Models.ResolveModel(config.RoleTriage))
	if err != nil {
		return nil, fmt.Errorf("triage model: %w", err)
	}

	validate, err := Build(cfg.Models.ResolveModel(config.RoleValidate))
	if err != nil {
		return nil, fmt.Errorf("validation model: %w", err)
	}

	return &Roles{Review: review, Triage: triage, Validate: validate}, nil
}

// CallOptions renders the spec's generation parameters as SDK call options.
func (c *Client) CallOptions() []llms.CallOption {
	var opts []llms.CallOption

	if c.Spec.Temperature != nil {
		opts = append(opts, llms.WithTemperature(*c.Spec.Temperature))
	}
	if c.Spec.MaxTokens > 0 {
		opts = append(opts, llms.WithMaxTokens(c.Spec.MaxTokens))
	}

	return opts
}

// Timeout returns the per-request timeout, or zero when unset.
func (c *Client) Timeout() time.Duration { return c.Spec.Timeout }
