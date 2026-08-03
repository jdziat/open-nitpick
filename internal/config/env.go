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
