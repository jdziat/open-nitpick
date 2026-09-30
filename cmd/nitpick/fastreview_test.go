package main

import (
	"strings"
	"testing"
	"time"

	"github.com/jdziat/open-nitpick/internal/config"
)

func TestFastReviewScopeBoundsTheModelWork(t *testing.T) {
	cfg := config.Defaults()
	cfg.Practices.Profile = "engineering"
	cfg.Review.RelatedContext = true
	cfg.Review.Knowledge = true
	cfg.Review.Standards = true
	cfg.Validation.Enabled = true
	cfg.Review.Approve.Enabled = true
	cfg.Models.Routes = []config.Route{{Name: "special"}}
	cfg.Models.Ensemble = []config.ModelSpec{{Model: "second"}}
	cfg.Models.Default = config.ModelSpec{Provider: "openrouter", Model: "openai/gpt-5.6-luna", Timeout: 3 * time.Minute}
	cfg.Review.Concurrency = 7
	applyFastReviewScope(cfg)

	if cfg.Review.MaxFiles != fastReviewLimit || cfg.Review.MaxFilesPerRequest != fastReviewFilesPerRequest || cfg.Review.Concurrency != 7 {
		t.Fatalf("fast bounds = files:%d batch:%d concurrency:%d", cfg.Review.MaxFiles, cfg.Review.MaxFilesPerRequest, cfg.Review.Concurrency)
	}
	if cfg.Review.IncludeFullFiles || cfg.Review.RelatedContext || cfg.Review.RelatedContextCallers {
		t.Error("fast review must send changed diffs without repository context")
	}
	if cfg.Practices.Profile != "" || cfg.Review.Knowledge || cfg.Review.Standards || cfg.Validation.Enabled || cfg.Review.Approve.Enabled {
		t.Error("fast review retained an optional deep stage")
	}
	if cfg.Models.Default.Provider != "openrouter" || cfg.Models.Default.Model != "openai/gpt-5.6-luna" || cfg.Models.Default.Timeout != 3*time.Minute || cfg.Models.Default.MaxTokens != 4096 || cfg.Models.Default.Reasoning != config.ReasoningOff || cfg.Models.Default.StructuredOutput != config.StructuredText || len(cfg.Models.Routes) != 0 || len(cfg.Models.Ensemble) != 0 {
		t.Error("fast review did not bound every selected model request")
	}
	if cfg.Models.Review == nil || cfg.Models.Review.Provider != "openrouter" || cfg.Models.Review.Model != "openai/gpt-5.6-luna" || cfg.Models.Review.Timeout != 3*time.Minute {
		t.Error("fast review did not preserve its provider model and timeout")
	}
}

func TestFastReviewRejectsEngineeringProfile(t *testing.T) {
	err := runFastReview(t.Context(), []string{"-profile", "engineering"})
	if err == nil || !strings.Contains(err.Error(), "does not support") {
		t.Fatalf("fast-review engineering profile error = %v", err)
	}
}
