package config

import (
	"slices"
	"strings"
	"testing"
)

func TestRepositoryCannotAuthorizeItsOwnCodeExecution(t *testing.T) {
	writeUser(t, jevBase)
	root := writeConfig(t, "execution: {enabled: true, runtime: runc, trusted_corpus: true, risk_accepted: true}\n")
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Execution.Enabled || !slices.Contains(cfg.Dropped, "execution") {
		t.Fatalf("repository activated execution: %+v dropped=%v", cfg.Execution, cfg.Dropped)
	}
}

func TestOperatorCanSelectTrustedCorpusAndBoundedProfiles(t *testing.T) {
	t.Setenv(EnvTrustConfigEndpoints, "1")
	root := writeConfig(t, jevBase+"execution:\n  enabled: true\n  image: sha256:"+strings.Repeat("a", 64)+"\n  runtime: runc\n  trusted_corpus: true\n  risk_accepted: true\n  checks: [{kind: go_test, package: '.'}]\n")
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Execution.Enabled || cfg.Execution.Runtime != "runc" {
		t.Fatalf("operator choice lost: %+v", cfg.Execution)
	}
	for _, mutate := range []func(*Execution){func(e *Execution) { e.TrustedCorpus = false }, func(e *Execution) { e.Image = "mutable:latest" }, func(e *Execution) { e.Checks[0].Kind = "shell" }, func(e *Execution) { e.Checks = nil }} {
		settings := cfg.Execution
		settings.Checks = slices.Clone(settings.Checks)
		mutate(&settings)
		if settings.validate() == nil {
			t.Fatalf("invalid policy accepted: %+v", settings)
		}
	}
}

func TestAliasesCannotSmuggleExecutionIntoRepositoryPolicy(t *testing.T) {
	t.Setenv(EnvIgnoreUnknownKeys, "1")
	root := writeConfig(t, jevBase+"x: &code\n  execution: {enabled: true}\n<<: *code\n")
	if _, err := Load(root); err == nil {
		t.Fatal("execution survived through a YAML merge")
	}
}

func TestClaimDigestMustBeLowercaseHex(t *testing.T) {
	settings := Execution{Enabled: true, Image: "sha256:" + strings.Repeat("a", 64), Runtime: "runc", TrustedCorpus: true, RiskAccepted: true,
		Checks: []ExecutionCheck{{Kind: "go_build", Package: "."}}}
	claim := ExecutionClaim{Kind: "go_build_failure", CheckIndex: 0, Path: "a.go", Head: "abc"}
	for digest, want := range map[string]bool{strings.Repeat("a", 64): true, strings.Repeat("g", 64): false, strings.Repeat("A", 64): false} {
		claim.FindingSHA256 = digest
		settings.Claims = []ExecutionClaim{claim}
		if got := settings.validate() == nil; got != want {
			t.Fatalf("digest %q accepted=%v, want %v", digest[:4], got, want)
		}
	}
}
