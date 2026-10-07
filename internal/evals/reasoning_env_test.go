package evals

import (
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
)

// A level set for the run has to arrive on the model the review calls, and a
// run that sets none must send none, because every earlier number in
// docs/findings.md was taken with the model's own default.
func TestEvalReasoningReachesTheReviewModelOnlyWhenSet(t *testing.T) {
	t.Setenv(EnvReasoning, "")
	if got := evalConfig(ParseModel("z-ai/glm-5.3-flash")).Models.Default.Reasoning; got != "" {
		t.Errorf("an unset %s sent %q", EnvReasoning, got)
	}

	for _, level := range config.ReasoningLevels() {
		t.Setenv(EnvReasoning, level)
		if got := evalConfig(ParseModel("z-ai/glm-5.3-flash")).Models.Default.Reasoning; string(got) != level {
			t.Errorf("%s=%s reached the model as %q", EnvReasoning, level, got)
		}
	}
}

// A misspelt level must stop the run before it spends anything; reviewing at
// the default under a label it did not earn is the failure this guards.
func TestEvalReasoningRejectsALevelConfigDoesNotAccept(t *testing.T) {
	t.Setenv(EnvReasoning, "extreme")
	_, err := OptionsFromEnv()
	if err == nil || !strings.Contains(err.Error(), EnvReasoning) {
		t.Fatalf("want a setup error naming %s, got %v", EnvReasoning, err)
	}
}
