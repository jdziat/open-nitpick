package config

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
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
	if m.Router != nil {
		errs = append(errs, prefixAll("models.router", m.Router.validate(false))...)
	}
	for i, r := range m.Routes {
		where := fmt.Sprintf("models.routes[%d]", i)
		if r.Review != nil {
			errs = append(errs, prefixAll(where+".review", r.Review.validate(false))...)
		}
		for j := range r.Ensemble {
			errs = append(errs, prefixAll(fmt.Sprintf("%s.ensemble[%d]", where, j), r.Ensemble[j].validate(false))...)
		}
		for _, k := range r.Match.Kinds {
			if !slices.Contains(Kinds(), strings.ToLower(strings.TrimSpace(k))) {
				errs = append(errs, fmt.Errorf("%s.match.kinds: %q is not a kind (one of %s)", where, k, strings.Join(Kinds(), ", ")))
			}
		}
		if len(r.Match.Kinds) > 0 && m.Router == nil {
			errs = append(errs, fmt.Errorf("%s.match.kinds needs models.router to assign kinds", where))
		}
		if r.Match.MinFiles > 0 && r.Match.MaxFiles > 0 && r.Match.MinFiles > r.Match.MaxFiles {
			errs = append(errs, fmt.Errorf("%s.match: min_files %d exceeds max_files %d", where, r.Match.MinFiles, r.Match.MaxFiles))
		}
		if r.Review == nil && r.Ensemble == nil {
			errs = append(errs, fmt.Errorf("%s: a route needs review, ensemble, or both", where))
		}
	}
	for i := range m.Ensemble {
		errs = append(errs, prefixAll(fmt.Sprintf("models.ensemble[%d]", i), m.Ensemble[i].validate(false))...)
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
	if len(s.Providers) > 0 {
		if p := strings.ToLower(strings.TrimSpace(s.Provider)); p != "" && p != "openrouter" {
			errs = append(errs, fmt.Errorf("providers pins a router's upstreams and provider %q is not a router", s.Provider))
		}
		for _, slug := range s.Providers {
			if strings.TrimSpace(slug) == "" {
				errs = append(errs, errors.New("providers contains an empty slug"))
			}
		}
	}
	if s.Timeout < 0 {
		errs = append(errs, fmt.Errorf("timeout must not be negative, got %s", s.Timeout))
	}
	if s.MaxRetries != nil && *s.MaxRetries < 0 {
		errs = append(errs, fmt.Errorf("max_retries must not be negative, got %d", *s.MaxRetries))
	}

	switch s.StructuredOutput {
	case "", StructuredAuto, StructuredSchema, StructuredJSON, StructuredText:
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
	// Checked rather than tolerated because CapSeverity treats an unusable
	// ceiling as no ceiling, which is the safe runtime behavior and the wrong
	// answer to give an operator who meant to cap something.
	if !l.MaxSeverity.Valid() {
		errs = append(errs, fmt.Errorf("linters.max_severity %q is not a severity", l.MaxSeverity))
	}
	if l.MaxSeverity == SeverityNone {
		errs = append(errs, errors.New(
			"linters.max_severity: none is a gate threshold, not a level a finding can carry; "+
				"use linters.mode: off to stop running analyzers"))
	}
	for i, name := range l.Enabled {
		if strings.TrimSpace(name) == "" {
			errs = append(errs, fmt.Errorf("linters.enabled[%d]: name is empty", i))
		}
	}

	// A relative analyzer config is refused HERE rather than at run time,
	// because relative to the working directory means relative to the tree under
	// review — the one place a configuration may not come from. Rejecting it
	// before a review starts is the difference between an operator learning they
	// typed a path wrong and an operator learning nothing, since the runtime
	// answer to a bad path is a skipped analyzer.
	//
	// Whether an absolute path actually lands outside the repository is decided
	// by internal/linters, which is the only layer that knows where the
	// repository is.
	for _, c := range []struct{ key, path string }{
		{"linters.golangci_config", l.GolangciConfig},
		{"linters.ruff_config", l.RuffConfig},
		{"linters.eslint_config", l.ESLintConfig},
	} {
		if err := checkAnalyzerConfigPath(c.key, c.path); err != nil {
			errs = append(errs, err)
		}
	}

	for name, path := range l.Configs {
		if strings.TrimSpace(name) == "" {
			errs = append(errs, errors.New("linters.configs: an analyzer name is empty"))
			continue
		}
		if err := checkAnalyzerConfigPath("linters.configs."+name, path); err != nil {
			errs = append(errs, err)
		}
	}
	for i, name := range l.Trusted {
		if strings.TrimSpace(name) == "" {
			errs = append(errs, fmt.Errorf("linters.trusted[%d]: name is empty", i))
		}
	}

	// semgrep is the one that also accepts a registry reference, because a
	// registry rule set is a fetch the operator asked for BY NAME. That is not
	// the same as the `--config auto` this replaced, which was a network fetch
	// of rules nobody chose.
	if ref := strings.TrimSpace(l.SemgrepConfig); ref != "" && !SemgrepRegistryRef(ref) {
		if err := checkAnalyzerConfigPath("linters.semgrep_config", ref); err != nil {
			errs = append(errs, fmt.Errorf("%w, or a semgrep registry reference (p/... or r/...)", err))
		}
	}

	return errs
}

// checkAnalyzerConfigPath requires an absolute path, or nothing.
func checkAnalyzerConfigPath(key, path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("%s %q must be an absolute path outside the repository under review", key, path)
	}
	return nil
}

// SemgrepRegistryRef reports whether a semgrep config value names a registry
// rule set rather than a local file.
//
// Exported so that internal/linters, which decides containment, and this
// package, which decides validity, cannot drift on what counts as a path. A
// value that is not a registry reference has to be an absolute path outside the
// repository, and getting that wrong in one place only would let `rules/x.yml`
// pass validation and then be read out of the tree under review.
func SemgrepRegistryRef(ref string) bool {
	ref = strings.TrimSpace(ref)
	return strings.HasPrefix(ref, "p/") || strings.HasPrefix(ref, "r/")
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
