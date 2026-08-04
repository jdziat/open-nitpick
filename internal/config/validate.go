package config

import (
	"errors"
	"fmt"
	"strings"
)

// Validate checks the configuration for structural errors. It deliberately runs
// before any client is constructed or any token is spent.
//
// Provider *names* are not checked here: that requires the SDK's provider
// registry, and keeping this package free of provider imports keeps config
// loading cheap and testable. internal/llm validates names against the registry
// when it builds clients, which still happens before the first API call.
func (c *Config) Validate() error {
	var errs []error

	errs = append(errs, c.Models.validate()...)
	errs = append(errs, c.Review.validate()...)
	errs = append(errs, c.Linters.validate()...)
	errs = append(errs, c.Persona.validate()...)
	errs = append(errs, c.Validation.validate()...)

	for i, ins := range c.Instructions {
		if strings.TrimSpace(ins.Path) == "" {
			errs = append(errs, fmt.Errorf("instructions[%d]: path is required", i))
		} else if !validGlob(ins.Path) {
			errs = append(errs, fmt.Errorf("instructions[%d]: invalid path glob %q", i, ins.Path))
		}
		if strings.TrimSpace(ins.Prompt) == "" {
			errs = append(errs, fmt.Errorf("instructions[%d]: prompt is required", i))
		}
	}

	return errors.Join(errs...)
}

func (m Models) validate() []error {
	var errs []error

	// The default model is the fallback for every role, so it must be complete
	// even when a role overrides it.
	errs = append(errs, prefixAll("models.default", m.Default.validate(true))...)

	if m.Review != nil {
		errs = append(errs, prefixAll("models.review", m.Review.validate(false))...)
	}
	if m.Triage != nil {
		errs = append(errs, prefixAll("models.triage", m.Triage.validate(false))...)
	}
	if m.Validate != nil {
		errs = append(errs, prefixAll("models.validate", m.Validate.validate(false))...)
	}

	return errs
}

// validate checks one model spec. When required is true the provider and model
// must be present; role overrides may be partial because they inherit.
func (s ModelSpec) validate(required bool) []error {
	var errs []error

	if required {
		if strings.TrimSpace(s.Provider) == "" {
			errs = append(errs, errors.New("provider is required"))
		}
		if strings.TrimSpace(s.Model) == "" {
			errs = append(errs, errors.New("model is required"))
		}
	}

	if s.Temperature != nil && (*s.Temperature < 0 || *s.Temperature > 2) {
		errs = append(errs, fmt.Errorf("temperature %.2f out of range [0, 2]", *s.Temperature))
	}
	if s.MaxTokens < 0 {
		errs = append(errs, fmt.Errorf("max_tokens must not be negative, got %d", s.MaxTokens))
	}
	if s.Timeout < 0 {
		errs = append(errs, fmt.Errorf("timeout must not be negative, got %s", s.Timeout))
	}
	if s.MaxRetries != nil && *s.MaxRetries < 0 {
		errs = append(errs, fmt.Errorf("max_retries must not be negative, got %d", *s.MaxRetries))
	}

	switch s.StructuredOutput {
	case "", StructuredAuto, StructuredSchema, StructuredJSON:
	default:
		errs = append(errs, fmt.Errorf("unknown structured_output %q (want auto, schema, or json)", s.StructuredOutput))
	}

	if s.BaseURL != "" {
		if !strings.Contains(s.BaseURL, "://") {
			errs = append(errs, fmt.Errorf("base_url %q must include a scheme", s.BaseURL))
		} else if needsPrivateEndpoint(s.Provider, s.BaseURL) && !s.AllowPrivateEndpoint {
			// Failing here, with the fix named, beats failing later inside the
			// provider with an opaque SSRF rejection.
			errs = append(errs, fmt.Errorf(
				"base_url %q uses plain HTTP or a private address; set %s=1 and "+
					"allow_private_endpoint: true to permit it (only for endpoints you control) "+
					"- local models via provider: ollama or llamacpp need neither",
				s.BaseURL, EnvTrustConfigEndpoints))
		}
	}

	if !s.checkAPIKeyEnv() {
		// There is no legitimate reason to hand the forge credential to a model
		// endpoint, so this is refused even in a trusted config.
		errs = append(errs, fmt.Errorf(
			"api_key_env %q names a forge credential; a model provider must never receive it",
			s.APIKeyEnv))
	}

	return errs
}

func (r Review) validate() []error {
	var errs []error

	if r.MaxFiles <= 0 {
		errs = append(errs, fmt.Errorf("review.max_files must be positive, got %d", r.MaxFiles))
	}
	if r.TokenBudgetPerRequest <= 0 {
		errs = append(errs, fmt.Errorf("review.token_budget_per_request must be positive, got %d", r.TokenBudgetPerRequest))
	}
	if r.MaxFilesPerRequest <= 0 {
		errs = append(errs, fmt.Errorf("review.max_files_per_request must be positive, got %d", r.MaxFilesPerRequest))
	}
	if r.Concurrency <= 0 {
		errs = append(errs, fmt.Errorf("review.concurrency must be positive, got %d", r.Concurrency))
	}
	if r.MaxFileBytes <= 0 {
		errs = append(errs, fmt.Errorf("review.max_file_bytes must be positive, got %d", r.MaxFileBytes))
	}
	if !r.FailOn.Valid() {
		errs = append(errs, fmt.Errorf("review.fail_on %q is not a severity", r.FailOn))
	}
	if !r.MinSeverity.Valid() {
		errs = append(errs, fmt.Errorf("review.min_severity %q is not a severity", r.MinSeverity))
	}
	if r.MinSeverity == SeverityNone {
		errs = append(errs, errors.New("review.min_severity: none would discard every finding"))
	}
	for i, pattern := range r.Ignore {
		if !validGlob(pattern) {
			errs = append(errs, fmt.Errorf("review.ignore[%d]: invalid glob %q", i, pattern))
		}
	}

	return errs
}

func (l Linters) validate() []error {
	var errs []error

	switch l.Mode {
	case "", LinterAuto, LinterStrict, LinterOff:
	default:
		errs = append(errs, fmt.Errorf("linters.mode %q is not one of auto, strict, off", l.Mode))
	}
	if l.Timeout < 0 {
		errs = append(errs, fmt.Errorf("linters.timeout must not be negative, got %s", l.Timeout))
	}
	for i, name := range l.Enabled {
		if strings.TrimSpace(name) == "" {
			errs = append(errs, fmt.Errorf("linters.enabled[%d]: name is empty", i))
		}
	}

	return errs
}

// prefixAll qualifies each error with its configuration path so a validation
// failure names the key the user has to edit.
func prefixAll(prefix string, errs []error) []error {
	out := make([]error, 0, len(errs))
	for _, err := range errs {
		out = append(out, fmt.Errorf("%s: %w", prefix, err))
	}
	return out
}
