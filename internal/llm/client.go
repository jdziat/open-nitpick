// Package llm builds configured model clients and constrains their output to a
// schema.
//
// Provider construction goes through the SDK's by-name registry, so every
// provider the SDK supports, including local ollama and llama.cpp servers and
// any OpenAI-compatible gateway reached via base_url, is usable from.
// nitpick.yaml without changes here.
package llm

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	llms "github.com/nocturnium/llm-go-sdk/v6"
	"github.com/nocturnium/llm-go-sdk/v6/pkg/middleware/resilience"
	_ "github.com/nocturnium/llm-go-sdk/v6/pkg/providers/all" // register every provider with llms.New

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

	// Log receives one line per stall retry, so a review that took forty
	// minutes says why in its own log rather than in a timing someone has to
	// notice. Nil discards.
	Log *slog.Logger

	// stallRetries is how many times a request whose answer never finished
	// arriving is sent again; see generateTyped. It shares max_retries with
	// the SDK's transient-failure retries, because both answer the same
	// question, how many times this deployment is willing to pay for one
	// batch, and a config that lowers one has no reason to want the other.
	stallRetries int

	// fallback is the client a caller escalates to when this one cannot
	// answer. Nil when the spec names none. See ShouldEscalate.
	fallback *Client

	// retries receives the SDK's retry callback. Nil on a client built for a
	// test, which bypasses the resilience wrapper entirely.
	retries *retryLog
}

// Provider returns the configured provider name.
func (c *Client) Provider() string { return c.Spec.Provider }

// Model returns the configured model name.
func (c *Client) Model() string { return c.Spec.Model }

// String describes the client as provider/model, for logs and errors.
func (c *Client) String() string { return c.Spec.Provider + "/" + c.Spec.Model }

// Build constructs a client from a model spec. Unknown providers and missing
// credentials fail here, before any request is made.
//
// It resolves the credential under a background context, so a credential_command
// is bounded by its own timeout rather than by the caller's cancellation. Use
// BuildContext from a path that has a context: on Ctrl-C, a secret manager
// waiting on a fingerprint should stop with the run.
func Build(spec config.ModelSpec) (*Client, error) {
	return BuildContext(context.Background(), spec)
}

// BuildContext is Build under a caller's context, which bounds the credential
// command it may have to run.
func BuildContext(ctx context.Context, spec config.ModelSpec) (*Client, error) {
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
		// The SDK split plain-HTTP from private-IP access in v5; the config
		// documents allow_private_endpoint as granting both.
		AllowHTTP: spec.AllowPrivateEndpoint,
	}
	// Credential resolution: the keystore and a secret manager before the
	// environment, and the SDK's own conventional variable only when none of
	// them said anything. See credential.go for the order and why.
	switch key, ok, err := resolveCredential(ctx, spec, nil); {
	case err != nil:
		return nil, fmt.Errorf("model %s/%s: %w", spec.Provider, spec.Model, err)
	case ok:
		cfg.APIKey = key
	}

	client, err := llms.New(spec.Provider, cfg)
	if err != nil {
		return nil, fmt.Errorf("build model %s/%s: %w", spec.Provider, spec.Model, err)
	}

	// The fallback is built here rather than on demand, so a misconfigured one
	// fails before any request is made instead of at the moment a batch has
	// already lost its primary.
	var fallback *Client
	if fb, ok := spec.ResolveFallback(); ok {
		if fallback, err = BuildContext(ctx, fb); err != nil {
			return nil, fmt.Errorf("build fallback for %s/%s: %w", spec.Provider, spec.Model, err)
		}
	}

	maxRetries := defaultMaxRetries
	if spec.MaxRetries != nil {
		maxRetries = *spec.MaxRetries
	}
	// The retry observer is built here because the resilient client needs it
	// and the Client needs the resilient client, so the logger cannot be known
	// yet. It arrives later through setLog.
	rl := &retryLog{model: spec.Provider + "/" + spec.Model}
	resilient := resilience.NewResilientClient(client,
		resilience.WithMaxRetries(maxRetries),
		resilience.WithOnRetry(rl.observe))

	mode := spec.StructuredOutput
	if mode == "" {
		mode = config.StructuredAuto
	}

	return &Client{LLM: resilient, Spec: spec, mode: mode, stallRetries: maxRetries, fallback: fallback, retries: rl}, nil
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
	retries := defaultMaxRetries
	if spec.MaxRetries != nil {
		retries = *spec.MaxRetries
	}
	return &Client{LLM: client, Spec: spec, mode: mode, stallRetries: retries}
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

	// Validate is the expert-validation client. It may be nil, callers that
	// assemble Roles by hand, such as the eval harness, only name the roles
	// they are measuring, so read it through Validator rather than directly.
	Validate *Client

	// Router is the batch classifier, nil when no route needs one.
	Router *Client

	// Build constructs a client for a spec a route or ensemble names. Nil
	// means Build in this package; the eval harness sets its own so every
	// client a review reaches for is metered. Clients are cached by spec
	// key, so a run pays construction once per distinct model.
	Build func(config.ModelSpec) (*Client, error)

	mu      sync.Mutex
	clients map[string]*Client
	log     *slog.Logger
}

// Each calls fn for every client the roles hold, the named ones first and
// then the cached route and ensemble clients, each once.
func (r *Roles) Each(fn func(*Client)) {
	if r == nil {
		return
	}
	seen := map[*Client]bool{}
	visit := func(c *Client) {
		if c != nil && !seen[c] {
			seen[c] = true
			fn(c)
		}
	}
	for _, c := range []*Client{r.Review, r.Triage, r.Validate, r.Router} {
		visit(c)
	}
	r.mu.Lock()
	cached := make([]*Client, 0, len(r.clients))
	for _, c := range r.clients {
		cached = append(cached, c)
	}
	r.mu.Unlock()
	for _, c := range cached {
		visit(c)
	}
}

// For returns the client for a spec, building and caching it on first use.
// The review client is returned for its own spec without a build, so a route
// that names the default model shares its client.
func (r *Roles) For(spec config.ModelSpec) (*Client, error) {
	key := spec.Key()
	if r.Review != nil && r.Review.Spec.Key() == key {
		return r.Review, nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.clients[key]; ok {
		return c, nil
	}

	build := r.Build
	if build == nil {
		build = Build
	}
	c, err := build(spec)
	if err != nil {
		return nil, err
	}
	if r.log != nil {
		c.setLog(r.log)
	}
	if r.clients == nil {
		r.clients = map[string]*Client{}
	}
	r.clients[key] = c
	return c, nil
}

// WithLogger points every client at l and returns r, for the engine to call
// once it knows where its own log goes.
func (r *Roles) WithLogger(l *slog.Logger) *Roles {
	if r == nil {
		return r
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.log = l
	for _, c := range []*Client{r.Review, r.Triage, r.Validate, r.Router} {
		c.setLog(l)
	}
	for _, c := range r.clients {
		c.setLog(l)
	}
	return r
}

// setLog points a client and its fallback at l.
//
// The fallback is reached only through its primary, so it is never in the
// roster the loop above walks. Left out, a fallback's stalls and retries go to
// a discarding logger, which is silence in the one place this feature exists
// to make visible.
func (c *Client) setLog(l *slog.Logger) {
	for ; c != nil; c = c.fallback {
		c.Log = l
		c.retries.set(l)
	}
}

// logger is Log, or a discarding logger.
func (c *Client) logger() *slog.Logger {
	if c != nil && c.Log != nil {
		return c.Log
	}
	return slog.New(slog.DiscardHandler)
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

	roles := &Roles{Review: review, Triage: triage, Validate: validate, Build: Build}

	if spec, ok := cfg.Models.ResolveRouter(); ok && cfg.Models.NeedsRouter() {
		if roles.Router, err = Build(spec); err != nil {
			return nil, fmt.Errorf("router model: %w", err)
		}
	}
	// Route and ensemble models are built here rather than on first use so
	// a misnamed provider fails before any review runs, the way the roles
	// do, instead of on the first batch that happens to match.
	for _, r := range cfg.Models.Routes {
		if _, err := roles.For(cfg.Models.ResolveRoute(r)); err != nil {
			return nil, fmt.Errorf("route %q review model: %w", r.Name, err)
		}
		for _, spec := range cfg.Models.ResolveEnsemble(&r) {
			if _, err := roles.For(spec); err != nil {
				return nil, fmt.Errorf("route %q ensemble model: %w", r.Name, err)
			}
		}
	}
	for _, spec := range cfg.Models.ResolveEnsemble(nil) {
		if _, err := roles.For(spec); err != nil {
			return nil, fmt.Errorf("ensemble model: %w", err)
		}
	}

	return roles, nil
}

// CallOptions renders the spec's generation parameters as SDK call options.
func (c *Client) CallOptions() []llms.CallOption {
	var opts []llms.CallOption

	if c.Spec.Temperature != nil {
		opts = append(opts, llms.WithTemperature(*c.Spec.Temperature))
	}
	if len(c.Spec.Providers) > 0 {
		// OpenRouter's provider routing. "only" restricts the candidate set;
		// "order" ranks whatever set the other filters leave, which is not the
		// same thing and was the bug. With order plus allow_fallbacks false,
		// a request whose ranked providers were filtered out for any other
		// reason ends with nothing left, and OpenRouter answers "No endpoints
		// found" rather than using the pin. The routing funnel showed the
		// fallback filter taking eleven endpoints to zero for a pin whose
		// providers all served the model. See issue #46.
		//
		// allow_fallbacks stays false: "only" bounds which providers may be
		// chosen, and this is what stops the router serving the request from
		// outside that list when none of them is available.
		opts = append(opts, llms.WithExtraBodyParam("provider", map[string]any{
			"only":            append([]string(nil), c.Spec.Providers...),
			"allow_fallbacks": false,
		}))
	}
	switch {
	case c.Spec.MaxTokens > 0:
		opts = append(opts, llms.WithMaxTokens(c.Spec.MaxTokens))
	case strings.EqualFold(strings.TrimSpace(c.Spec.Provider), "anthropic"):
		// Anthropic's API requires max_tokens, and the SDK fills an unset
		// one with 4,096, a cap a reasoning model's thinking exhausts on
		// an ordinary review. Every other provider is sent no cap at all
		// when none is configured, so the model's own maximum applies;
		// this is the closest the direct Anthropic path can get without
		// asking the API what each model's maximum is.
		opts = append(opts, llms.WithMaxTokens(anthropicUnsetMaxTokens))
	}

	return opts
}

// anthropicUnsetMaxTokens is sent to the direct Anthropic provider when no
// max_tokens is configured, in place of the SDK's 4,096. It is below the
// output maximum of every current Claude model, so it never causes a
// request to be rejected, and it is a floor against the SDK's default, not
// a ceiling this project chose for the model.
const anthropicUnsetMaxTokens = 32768

// Timeout returns the per-request timeout, or zero when unset.
func (c *Client) Timeout() time.Duration { return c.Spec.Timeout }
