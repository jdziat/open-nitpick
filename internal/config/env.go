package config

import (
	"os"
	"strings"
)

// Environment variables that supply a model when the config file does not.
// These mirror the SDK's own NewFromEnv convention so a user who already has
// them exported can run open-nitpick with no config file at all.
const (
	EnvProvider = "LLM_PROVIDER"
	EnvModel    = "LLM_MODEL"
	EnvBaseURL  = "LLM_BASE_URL"

	// EnvReviewProvider and its model variables let an operator pin the models
	// used by a review after its policy has been resolved. They are for a
	// trusted runner, not repository configuration a pull request can change.
	EnvReviewProvider = "NITPICK_REVIEW_PROVIDER"
	EnvReviewModel    = "NITPICK_REVIEW_MODEL"
	EnvTriageModel    = "NITPICK_TRIAGE_MODEL"
)

// applyEnv fills unset default-model fields from the environment. The config
// file wins: environment variables are a fallback for what it does not say.
func (c *Config) applyEnv(getenv func(string) string) {
	if getenv == nil {
		getenv = os.Getenv
	}

	d := &c.Models.Default
	if strings.TrimSpace(d.Provider) == "" {
		d.Provider = strings.TrimSpace(getenv(EnvProvider))
	}
	if strings.TrimSpace(d.Model) == "" {
		d.Model = strings.TrimSpace(getenv(EnvModel))
	}
	if strings.TrimSpace(d.BaseURL) == "" {
		d.BaseURL = strings.TrimSpace(getenv(EnvBaseURL))
	}
}

// WithReviewModelOverrides returns c with trusted runner model choices applied.
// The environment only changes models when it supplies a provider; ordinary
// LLM_PROVIDER remains a fallback for an otherwise incomplete configuration.
func WithReviewModelOverrides(c *Config, getenv func(string) string) *Config {
	if getenv == nil {
		getenv = os.Getenv
	}
	provider := strings.TrimSpace(getenv(EnvReviewProvider))
	if provider == "" {
		return c
	}

	out := *c
	out.Models = c.Models
	out.Models.Default.Provider = provider
	if model := strings.TrimSpace(getenv(EnvReviewModel)); model != "" {
		out.Models.Default.Model = model
	}
	if c.Models.Triage != nil {
		triage := *c.Models.Triage
		triage.Provider = provider
		if model := strings.TrimSpace(getenv(EnvTriageModel)); model != "" {
			triage.Model = model
		}
		out.Models.Triage = &triage
	}
	if c.Models.Validate != nil {
		validate := *c.Models.Validate
		validate.Provider = provider
		out.Models.Validate = &validate
	}
	return &out
}

// APIKey resolves the credential for a model spec. An explicit api_key_env is
// honored first; otherwise resolution is left to the SDK provider, which knows
// its own conventional variable. The returned bool reports whether this config
// resolved a key at all.
func (s ModelSpec) APIKey(getenv func(string) string) (string, bool) {
	if getenv == nil {
		getenv = os.Getenv
	}
	if s.APIKeyEnv == "" {
		return "", false
	}

	key := strings.TrimSpace(getenv(s.APIKeyEnv))
	return key, key != ""
}
