package config

import (
	"slices"
	"testing"
)

func TestRepositoryCannotActivateOrRedirectAcceptedLearnings(t *testing.T) {
	writeUser(t, jevBase+"review:\n  learnings: {enabled: true, repository: example/project, max_tokens: 1500}\n")
	root := writeConfig(t, "review:\n  learnings: {enabled: false, repository: attacker/project, max_tokens: 8192}\n")
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Review.Learnings; !got.Enabled || got.Repository != "example/project" || got.MaxTokens != 1500 || !slices.Contains(cfg.Dropped, "review.learnings") {
		t.Fatalf("repository replaced operator settings: %+v dropped=%v", got, cfg.Dropped)
	}
}

func TestOperatorCanEnableBoundedAcceptedLearnings(t *testing.T) {
	t.Setenv(EnvTrustConfigEndpoints, "1")
	root := writeConfig(t, jevBase+"review:\n  learnings: {enabled: true, repository: example/project, max_tokens: 1500}\n")
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Review.Learnings.Enabled || cfg.Review.Learnings.MaxTokens != 1500 {
		t.Fatalf("operator setting disappeared: %+v", cfg.Review.Learnings)
	}
	root = writeConfig(t, jevBase+"review:\n  learnings: {enabled: true, max_tokens: 8193}\n")
	if _, err := Load(root); err == nil {
		t.Fatal("unbounded policy budget was accepted")
	}
}
