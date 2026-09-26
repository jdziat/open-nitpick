package config

import (
	"fmt"
	"strings"
)

// Security configures the tree security scan (`nitpick security`).
//
// Analyzers lists optional extras only; it cannot shrink the frozen required
// scanner set (osv-scanner, gitleaks, …). Enforcement of that frozen set lives
// with the security command, not here.
type Security struct {
	// FailOn is the lowest finding severity that fails a complete security run.
	// Defaults to warning. none is accepted at config load so an operator can
	// set it in .nitpick.yaml; the CLI still requires -allow-clean-with-no-gate
	// (and MCP allow_clean_with_no_gate) before a none gate may green a run.
	FailOn Severity `yaml:"fail_on"`

	// Model turns the optional security model pass on. Nil or true means on;
	// explicit false turns it off. -no-model on the CLI also skips it.
	Model *bool `yaml:"model"`

	// Analyzers names extra scanners beyond the frozen required set. Each entry
	// must be non-empty; omitting a required id here does not disable it.
	Analyzers []string `yaml:"analyzers"`
}

// ModelOn reports whether the security model pass is in force.
func (s Security) ModelOn() bool { return s.Model == nil || *s.Model }

// validate checks security settings that can be judged at config load.
func (s Security) validate() []error {
	var errs []error
	if !s.FailOn.Valid() {
		errs = append(errs, fmt.Errorf("security.fail_on %q is not a severity", s.FailOn))
	}
	for i, name := range s.Analyzers {
		if strings.TrimSpace(name) == "" {
			errs = append(errs, fmt.Errorf("security.analyzers[%d]: name is empty", i))
		}
	}
	return errs
}
