package config

import (
	"strings"
	"testing"
)

// TestSecurityFailOnDefaultsToWarning pins the security gate: unlike review,
// which ships advisory (fail_on none), a security scan blocks on warning unless
// the operator raises or waives the threshold.
func TestSecurityFailOnDefaultsToWarning(t *testing.T) {
	cfg := Defaults()
	if cfg.Security.FailOn != SeverityWarning {
		t.Errorf("Security.FailOn = %q, want %q", cfg.Security.FailOn, SeverityWarning)
	}
	if !cfg.Security.ModelOn() {
		t.Error("Security.Model must default on")
	}
}

// TestSecurityRejectsInvalidFailOn fails a typo at load time rather than
// treating an unknown threshold as none or warning.
func TestSecurityRejectsInvalidFailOn(t *testing.T) {
	cfg := Defaults()
	cfg.Models.Default = ModelSpec{Provider: "openai", Model: "gpt-4o"}
	cfg.Security.FailOn = Severity("blocker")

	err := cfg.Validate()
	if err == nil {
		t.Fatal("want an error for an unrecognized security.fail_on")
	}
	if !strings.Contains(err.Error(), "security.fail_on") {
		t.Errorf("error must name the key, got: %v", err)
	}
}

// TestSecurityAllowsFailOnNoneAtLoad keeps none loadable so .nitpick.yaml can
// declare it; the command still refuses to green without the loud waiver.
func TestSecurityAllowsFailOnNoneAtLoad(t *testing.T) {
	cfg := Defaults()
	cfg.Models.Default = ModelSpec{Provider: "openai", Model: "gpt-4o"}
	cfg.Security.FailOn = SeverityNone

	if err := cfg.Validate(); err != nil {
		t.Fatalf("security.fail_on none must be valid at load: %v", err)
	}
}

// TestSecurityRejectsEmptyAnalyzerName refuses a blank extras entry that would
// otherwise look like a configured scanner and mean nothing.
func TestSecurityRejectsEmptyAnalyzerName(t *testing.T) {
	cfg := Defaults()
	cfg.Models.Default = ModelSpec{Provider: "openai", Model: "gpt-4o"}
	cfg.Security.Analyzers = []string{"semgrep", "  "}

	err := cfg.Validate()
	if err == nil {
		t.Fatal("want an error for an empty security.analyzers entry")
	}
	if !strings.Contains(err.Error(), "security.analyzers") {
		t.Errorf("error must name the key, got: %v", err)
	}
}
