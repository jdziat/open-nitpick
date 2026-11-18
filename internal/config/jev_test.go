package config

import (
	"strings"
	"testing"
)

const jevBase = "models:\n  default: {provider: openai, model: gpt-4o}\n"

// Switching the filter on sends source code to OpenRouter and spends the
// operator's key. A pull request edits the repository's file, so that file must
// not be able to do either.
func TestARepositoryCannotTurnOnTheJevFilter(t *testing.T) {
	writeUser(t, jevBase+"review:\n  jev: {threshold: 0.4}\n")
	root := writeConfig(t, "review:\n  jev: {threshold: 0.9, api_key_env: ATTACKER_KEY, model: attacker/model}\n")

	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Review.Jev; got.Threshold != 0.4 || got.APIKeyEnv != "" || got.Model != "" {
		t.Errorf("review.jev = %+v, want only the user file's own settings", got)
	}
	if !strings.Contains(strings.Join(cfg.Dropped, ", "), "review.jev") {
		t.Errorf("Dropped = %v, want review.jev named so the operator learns it was ignored", cfg.Dropped)
	}
}

// With no user setting the repository's is dropped to the default, which is off.
func TestARepositoryThatAsksForJevLeavesItOffAndSaysSo(t *testing.T) {
	writeUser(t, jevBase)
	root := writeConfig(t, "review:\n  jev: {threshold: 0.3}\n")
	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Review.Jev.Enabled() {
		t.Errorf("review.jev = %+v, want the filter off", cfg.Review.Jev)
	}
}

// A trusted file keeps the setting, which is also the control: without it the
// tests above would pass whenever nothing could be read at all.
func TestATrustedFileMaySetTheJevFilter(t *testing.T) {
	t.Setenv(EnvTrustConfigEndpoints, "1")
	root := writeConfig(t, jevBase+"review:\n  jev: {threshold: 0.3, model: '~typesafe/jev-latest', api_key_env: MY_KEY}\n")
	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Review.Jev; !got.Enabled() || got.Threshold != 0.3 || got.APIKeyEnv != "MY_KEY" {
		t.Errorf("review.jev = %+v, want the trusted file's own", got)
	}
	if len(cfg.Dropped) != 0 {
		t.Errorf("Dropped = %v, want nothing dropped from a trusted file", cfg.Dropped)
	}
}

// The route the prune's walk cannot see is refused, as it is for the other keys.
func TestAnAnchorCannotSmuggleTheJevFilterIn(t *testing.T) {
	t.Setenv(EnvIgnoreUnknownKeys, "1")
	root := writeConfig(t, jevBase+"x: &leak\n  jev: {threshold: 0.5}\nreview:\n  <<: *leak\n")
	if _, err := Load(root); err == nil {
		t.Fatal("an anchor put review.jev on the config anyway")
	}
}

func TestTheJevThresholdMustBeFromZeroToOne(t *testing.T) {
	t.Setenv(EnvTrustConfigEndpoints, "1")
	for threshold, ok := range map[string]bool{"0": true, "0.3": true, "1": true, "-0.1": false, "1.5": false, ".nan": false} {
		root := writeConfig(t, jevBase+"review:\n  jev: {threshold: "+threshold+"}\n")
		_, err := Load(root)
		if (err == nil) != ok {
			t.Errorf("threshold %s: err = %v, want accepted %v", threshold, err, ok)
		}
		if err != nil && !strings.Contains(err.Error(), "review.jev.threshold") {
			t.Errorf("threshold %s: err = %v, want it to name review.jev.threshold", threshold, err)
		}
	}
}

func TestZeroIsTheDefaultAndMeansTheFilterIsOff(t *testing.T) {
	writeUser(t, jevBase)
	cfg, err := Load(writeConfig(t, ""))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Review.Jev.Enabled() || cfg.Review.Jev.Threshold != 0 {
		t.Errorf("review.jev = %+v, want off by default", cfg.Review.Jev)
	}
}

func TestDeepPromptsAcceptsOnlyMiraOrNothing(t *testing.T) {
	t.Setenv(EnvTrustConfigEndpoints, "1")
	for value, ok := range map[string]bool{"''": true, "mira": true, "ours": false, "Mira": false} {
		root := writeConfig(t, jevBase+"review:\n  deep: {prompts: "+value+"}\n")
		_, err := Load(root)
		if (err == nil) != ok {
			t.Errorf("prompts %s: err = %v, want accepted %v", value, err, ok)
		}
		if err != nil && !strings.Contains(err.Error(), "review.deep.prompts") {
			t.Errorf("prompts %s: err = %v, want it to name review.deep.prompts", value, err)
		}
	}
}

func TestDeepPromptsDefaultsToThisToolsOwn(t *testing.T) {
	writeUser(t, jevBase)
	cfg, err := Load(writeConfig(t, ""))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Review.Deep.Prompts != "" {
		t.Errorf("review.deep.prompts = %q, want the empty default", cfg.Review.Deep.Prompts)
	}
}
