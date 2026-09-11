package config

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func practiceGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git failed: %v %s", err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestPracticePolicyUsesAcceptedRulesWithoutModelCredentials(t *testing.T) {
	root := t.TempDir()
	practiceGit(t, root, "init", "-q")
	path := filepath.Join(root, FileName)
	if err := os.WriteFile(path, []byte("models: {nonsense: ignored}\npractices:\n  commits:\n    types: [fix]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	practiceGit(t, root, "add", ".")
	practiceGit(t, root, "commit", "-qm", "fix: initial")
	base := practiceGit(t, root, "rev-parse", "HEAD")
	if err := os.WriteFile(path, []byte("practices:\n  commits:\n    types: [anything]\n  required: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := ReadPracticePolicy(context.Background(), root, "", base)
	if err != nil || len(p.Practices.Commits.Types) != 1 || p.Practices.Commits.Types[0] != "fix" || len(p.Practices.Required) == 0 || p.Digest == "" || !strings.Contains(p.Source, base) {
		t.Fatalf("policy=%+v err=%v", p, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	after, err := ReadPracticePolicy(context.Background(), root, "", base)
	if err != nil || after.Digest != p.Digest {
		t.Fatalf("deletion weakened accepted policy: %+v %v", after, err)
	}
}

func TestPracticePolicyRejectsUnknownKeysAndInvalidRequirements(t *testing.T) {
	t.Setenv(EnvIgnoreUnknownKeys, "0")
	defaults := Defaults()
	for _, raw := range []string{
		"practices: {requird: []}", "practices: {required: [typo]}",
		"practices: {commits: {max_description_runes: 0}}",
		"practices: {profile: mystery}", "standards: {min_sites: -1}",
		"practices: {}\npractices: {}",
	} {
		p := PracticePolicy{Practices: DefaultPractices(), Review: defaults.Review, Linters: defaults.Linters}
		if err := decodePracticeBlocks(nil, &p); err != nil {
			t.Fatalf("invalid policy control: %v", err)
		}
		if err := decodePracticeBlocks([]byte(raw), &p); err == nil {
			t.Errorf("accepted invalid policy %s", raw)
		}
	}
}

func TestPracticePolicyReportsDefaultsAndHonorsExternalOperatorFile(t *testing.T) {
	root := t.TempDir()
	practiceGit(t, root, "init", "-q")
	practiceGit(t, root, "commit", "--allow-empty", "-qm", "feat: initial")
	p, err := ReadPracticePolicy(context.Background(), root, "", "HEAD")
	if err != nil || !strings.HasPrefix(p.Source, "defaults (") {
		t.Fatalf("missing policy: %+v %v", p, err)
	}
	path := filepath.Join(t.TempDir(), "operator.yml")
	if err := os.WriteFile(path, []byte("practices: {commits: {types: [chore]}}"), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err = ReadPracticePolicy(context.Background(), root, path, "HEAD")
	if err != nil || p.Practices.Commits.Types[0] != "chore" || !strings.HasPrefix(p.Source, "operator:") {
		t.Fatalf("operator policy: %+v %v", p, err)
	}
	if _, err := ReadPracticePolicy(context.Background(), root, path+"missing", "HEAD"); err == nil {
		t.Fatal("missing explicit operator file silently defaulted")
	}
}

func TestPracticePolicyValidatesInstrumentSettingsWithoutModels(t *testing.T) {
	for _, raw := range []string{
		"review: {max_file_bytes: -1}",
		"review: {ignore: ['[']}",
		"linters: {mode: typo}",
		"linters: {timeout: -1s}",
		"practices: {ignore: ['[']}",
	} {
		t.Run(raw, func(t *testing.T) {
			defaults := Defaults()
			p := PracticePolicy{Practices: DefaultPractices(), Review: defaults.Review, Linters: defaults.Linters}
			if err := decodePracticeBlocks(nil, &p); err != nil {
				t.Fatalf("default instruments require models: %v", err)
			}
			if err := decodePracticeBlocks([]byte(raw), &p); err == nil {
				t.Fatalf("accepted unusable instrument policy: %s", raw)
			}
		})
	}
}

func TestPracticePolicyOverlaysUserRulesBeforeAcceptedRepositoryRules(t *testing.T) {
	root := t.TempDir()
	practiceGit(t, root, "init", "-q")
	if err := os.WriteFile(filepath.Join(root, FileName), []byte("practices: {commits: {types: [fix]}}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	practiceGit(t, root, "add", ".")
	practiceGit(t, root, "commit", "-qm", "fix: initial")
	userPath := filepath.Join(t.TempDir(), "user.yaml")
	if err := os.WriteFile(userPath, []byte("models: {not_loaded: true}\npractices: {commits: {types: [feat], max_description_runes: 40}}\nlinters: {enabled: [ruff]}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvNoUserConfig, "false")
	t.Setenv(EnvUserConfig, userPath)
	policy, err := ReadPracticePolicy(context.Background(), root, "", "HEAD")
	if err != nil || policy.Practices.Commits.Types[0] != "fix" || policy.Practices.Commits.MaxDescriptionRunes != 40 || len(policy.Linters.Enabled) != 1 || policy.Linters.Enabled[0] != "ruff" || !strings.Contains(policy.Source, userPath) {
		t.Fatalf("user/repository precedence diverged: %+v %v", policy, err)
	}
}

func TestPracticePolicyRejectsMisspelledTopLevelBlocks(t *testing.T) {
	t.Setenv(EnvIgnoreUnknownKeys, "0")
	defaults := Defaults()
	defaults.Models.Default = ModelSpec{Provider: "synthetic", Model: "fixture"}
	if err := defaults.Validate(); err != nil {
		t.Fatalf("invalid full-config control: %v", err)
	}
	policy := PracticePolicy{Practices: defaults.Practices, Review: defaults.Review, Linters: defaults.Linters}
	if err := decodePracticeBlocks([]byte("practisez: {required: [commits]}"), &policy); err == nil {
		t.Fatal("misspelled policy silently defaulted")
	}
	for _, rules := range []Practices{{SlopRules: []string{" "}}, {RequiredConventions: []string{""}}} {
		rules.Commits = defaults.Practices.Commits
		cfg := *defaults
		cfg.Practices = rules
		if err := cfg.Validate(); err == nil {
			t.Fatal("full configuration validation accepted a blank rule")
		}
	}
}

func TestPracticePolicyHonorsOperatorUnknownKeysWithoutIgnoringInvalidValues(t *testing.T) {
	t.Setenv(EnvIgnoreUnknownKeys, "1")
	defaults := Defaults()
	policy := PracticePolicy{Practices: defaults.Practices, Review: defaults.Review, Linters: defaults.Linters}
	raw := "future_block: true\nreview: {ignore: &paths [vendor/**]}\npractices: {ignore: *paths, future_rule: true, required: [commits]}\n"
	if err := decodePracticeBlocks([]byte(raw), &policy); err != nil || strings.Join(policy.IgnoredUnknown, ",") != "future_block,future_rule" || len(policy.Practices.Ignore) != 1 || policy.Practices.Ignore[0] != "vendor/**" || len(policy.Practices.Required) != 1 || policy.Practices.Required[0] != "commits" {
		t.Fatalf("operator compatibility or aliased policy lost: %+v %v", policy, err)
	}
	for _, raw := range []string{"practices: {future_rule: true, budget: invalid}", "practices: {required: [unknown]}", "future_block: 1\nfuture_block: 2"} {
		candidate := PracticePolicy{Practices: DefaultPractices(), Review: defaults.Review, Linters: defaults.Linters}
		if err := decodePracticeBlocks(nil, &candidate); err != nil {
			t.Fatal(err)
		}
		if err := decodePracticeBlocks([]byte(raw), &candidate); err == nil {
			t.Fatalf("ignore-unknown masked invalid policy: %s", raw)
		}
	}
}

func TestIgnoredPolicyKeysRemainVisibleInAcceptedProvenance(t *testing.T) {
	t.Setenv(EnvNoUserConfig, "1")
	t.Setenv(EnvIgnoreUnknownKeys, "1")
	operator := filepath.Join(t.TempDir(), "operator.yaml")
	if err := os.WriteFile(operator, []byte("future_policy: true\npractices: {future_rule: true}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	policy, err := ReadPracticePolicy(t.Context(), t.TempDir(), operator, "")
	if err != nil || len(policy.IgnoredUnknown) != 2 || !strings.Contains(policy.Source, "ignored unknown keys: future_policy, future_rule") || policy.Digest == "" {
		t.Fatalf("ignored policy became invisible: %+v %v", policy, err)
	}
}
