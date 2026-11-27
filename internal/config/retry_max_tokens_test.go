package config

import (
	"strings"
	"testing"
)

const retryBase = "models:\n  default: {provider: openrouter, model: z-ai/glm-5.3-flash, retry_max_tokens: 40000}\n"

// The ceiling is per role: a reviewer can set its own and the default's applies
// to a role that sets none.
func TestRetryMaxTokensIsKeptPerRoleAndInheritedFromTheDefault(t *testing.T) {
	root := writeConfig(t, retryBase+"  review: {provider: openrouter, model: z-ai/glm-5.3-flash, retry_max_tokens: 20000}\n"+
		"  triage: {provider: openrouter, model: qwen/qwen3.8-27b}\n")
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Models.ResolveModel(RoleReview).RetryMaxTokens; got != 20000 {
		t.Errorf("review retry_max_tokens = %d, want its own 20000", got)
	}
	if got := cfg.Models.ResolveModel(RoleTriage).RetryMaxTokens; got != 40000 {
		t.Errorf("triage retry_max_tokens = %d, want the default's 40000 inherited", got)
	}
	if got := cfg.Models.Default.RetryMaxTokens; got != 40000 {
		t.Errorf("default retry_max_tokens = %d, want 40000", got)
	}
}

// Rule 10: the refusal is for the stated reason, not a parse failure elsewhere.
func TestNegativeRetryMaxTokensIsRefusedByName(t *testing.T) {
	root := writeConfig(t, "models:\n  default: {provider: openrouter, model: m, retry_max_tokens: -1}\n")
	_, err := Load(root)
	if err == nil || !strings.Contains(err.Error(), "retry_max_tokens must not be negative") {
		t.Fatalf("err = %v, want the negative retry_max_tokens named", err)
	}
}
