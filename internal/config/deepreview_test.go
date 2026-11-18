package config

import (
	"math"
	"strings"
	"testing"
	"time"
)

// The defaults are the settings Mira reviews with, which is what the deep
// review was measured against.
func TestDeepReviewDefaultsAreMirasSettings(t *testing.T) {
	d := Defaults().Review.Deep
	if strings.Join(d.Agents, ",") != "general,security" || d.MaxTurns != 10 || d.Temperature != 0.2 ||
		d.Reasoning != ReasoningMedium || d.ConfidenceFloor != 0.7 || d.LowPriorityCap != 5 || !d.Critique ||
		d.MaxFilesPerRequest != 60 || d.TokenBudgetPerRequest != 120000 {
		t.Errorf("review.deep defaults = %+v", d)
	}
}

func TestDeepReviewValidationRejectsWhatCannotRun(t *testing.T) {
	for name, tc := range map[string]struct {
		tune func(*DeepReview)
		want string
	}{
		"no agents":          {func(d *DeepReview) { d.Agents = nil }, "at least one agent"},
		"an unknown agent":   {func(d *DeepReview) { d.Agents = []string{"general", "perf"} }, `"perf"`},
		"an agent twice":     {func(d *DeepReview) { d.Agents = []string{"general", "general"} }, "twice"},
		"zero turns":         {func(d *DeepReview) { d.MaxTurns = 0 }, "max_turns"},
		"a floor above 1":    {func(d *DeepReview) { d.ConfidenceFloor = 1.5 }, "confidence_floor"},
		"a negative cap":     {func(d *DeepReview) { d.LowPriorityCap = -1 }, "low_priority_cap"},
		"a bad reasoning":    {func(d *DeepReview) { d.Reasoning = "lots" }, "reasoning"},
		"a hot sampler":      {func(d *DeepReview) { d.Temperature = 3 }, "temperature"},
		"a NaN sampler":      {func(d *DeepReview) { d.Temperature = math.NaN() }, "temperature"},
		"a NaN floor":        {func(d *DeepReview) { d.ConfidenceFloor = math.NaN() }, "confidence_floor"},
		"no batch files":     {func(d *DeepReview) { d.MaxFilesPerRequest = 0 }, "max_files_per_request"},
		"a negative timeout": {func(d *DeepReview) { d.Timeout = -time.Second }, "timeout"},
	} {
		cfg := Defaults()
		cfg.Models.Default = ModelSpec{Provider: "openai", Model: "gpt-4o"}
		tc.tune(&cfg.Review.Deep)
		err := cfg.Validate()
		if err == nil || !strings.Contains(err.Error(), "review.deep") || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error = %v, want one naming review.deep and %s", name, err, tc.want)
		}
	}
	cfg := Defaults()
	cfg.Models.Default = ModelSpec{Provider: "openai", Model: "gpt-4o"}
	if err := cfg.Validate(); err != nil {
		t.Errorf("the defaults do not validate: %v", err)
	}
}

// An agent has a wall-clock budget by default, so a slow provider cannot hold a
// review open for as long as it likes, and zero turns it off.
func TestDeepReviewHasAWallClockBudgetByDefault(t *testing.T) {
	if got := Defaults().Review.Deep.Timeout; got != 15*time.Minute {
		t.Errorf("review.deep.timeout default = %s, want 15m", got)
	}
}
