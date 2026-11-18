package config

import (
	"errors"
	"fmt"
	"path"

	"github.com/jdziat/open-nitpick/internal/execution"
)

// Execution configures optional checks in the operator-selected Docker image.
// It is operator-only: source under review cannot authorize its own execution.
type Execution struct {
	// Enabled permits the operator's registered checks to execute.
	Enabled bool `yaml:"enabled"`
	// Image is a locally available immutable Docker image ID.
	Image string `yaml:"image"`
	// Runtime selects runsc or an explicitly accepted trusted-corpus runtime.
	Runtime string `yaml:"runtime"`
	// TrustedCorpus limits shared-kernel execution to operator-selected source.
	TrustedCorpus bool `yaml:"trusted_corpus"`
	// RiskAccepted acknowledges the shared-kernel runtime boundary.
	RiskAccepted bool `yaml:"risk_accepted"`
	// RequiredForFix refuses proposals unless every baseline and candidate check passes.
	RequiredForFix bool `yaml:"required_for_fix"`
	// Checks are trusted profiles; repository findings never supply command arguments.
	Checks []ExecutionCheck `yaml:"checks"`
	// Claims registers exact human-mapped findings for the go_build_failure pilot.
	Claims []ExecutionClaim `yaml:"claims"`
}

// ExecutionClaim maps a finding to a selected build profile, never a shell command.
type ExecutionClaim struct {
	// FindingSHA256 binds the claim to the exact finding text.
	FindingSHA256 string `yaml:"finding_sha256"`
	// Head is the immutable source revision the claim names.
	Head string `yaml:"head"`
	// Path is the finding's repository-relative source file.
	Path string `yaml:"path"`
	// Kind selects the supported go_build_failure claim.
	Kind string `yaml:"kind"`
	// CheckIndex selects a go_build profile from execution.checks.
	CheckIndex int `yaml:"check_index"`
}

// ExecutionCheck selects go_test or go_build for one module-relative package.
type ExecutionCheck struct {
	// Kind selects go_test or go_build.
	Kind string `yaml:"kind"`
	// Package is one module-relative package, such as . or ./internal/example.
	Package string `yaml:"package"`
}

// Policy fixes per-check limits for the experimental execution pilot.
func (e Execution) Policy() execution.Policy {
	runtime := e.Runtime
	if runtime == "" {
		runtime = "runsc"
	}
	return execution.Policy{Image: e.Image, Runtime: runtime, TrustedCorpus: e.TrustedCorpus, RiskAccepted: e.RiskAccepted,
		TimeoutSeconds: 120, MemoryMiB: 2048, CPUs: 2, Pids: 128, DiskMiB: 1024, MaxOutputBytes: 256 * 1024}
}

func (e Execution) validate() error {
	if !e.Enabled {
		if e.RequiredForFix {
			return errors.New("execution.required_for_fix requires enabled checks")
		}
		return nil
	}
	if len(e.Checks) == 0 || len(e.Checks) > 8 {
		return errors.New("execution requires between one and eight trusted checks")
	}
	if err := execution.ValidatePolicy(e.Policy()); err != nil {
		return fmt.Errorf("execution: %w", err)
	}
	for _, c := range e.Checks {
		if err := (execution.Check{Kind: c.Kind, Package: c.Package}).Validate(); err != nil {
			return fmt.Errorf("execution.checks: %w", err)
		}
	}
	for _, claim := range e.Claims {
		if claim.Kind != "go_build_failure" || claim.CheckIndex < 0 || claim.CheckIndex >= len(e.Checks) || e.Checks[claim.CheckIndex].Kind != "go_build" || claim.Path == "" || claim.Head == "" || !validDigest(claim.FindingSHA256) {
			return errors.New("execution.claims needs an exact finding, revision, path and go_build profile")
		}
		if path.Dir(claim.Path) != e.Checks[claim.CheckIndex].Package {
			return errors.New("execution.claims path is outside the package its go_build profile builds")
		}
	}
	return nil
}

// validDigest accepts only a lowercase hex SHA-256, the form a finding digest
// is compared against, so a claim cannot carry a value that never matches.
func validDigest(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}
