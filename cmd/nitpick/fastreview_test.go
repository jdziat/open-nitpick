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
	cfg.Models.Review = nil

	applyFastReviewScope(cfg)

	if cfg.Review.MaxFiles != fastReviewLimit || cfg.Review.MaxFilesPerRequest != fastReviewFilesPerRequest || cfg.Review.Concurrency != fastReviewConcurrency {
		t.Fatalf("fast bounds = files:%d batch:%d concurrency:%d", cfg.Review.MaxFiles, cfg.Review.MaxFilesPerRequest, cfg.Review.Concurrency)
	}
	if cfg.Review.IncludeFullFiles || cfg.Review.RelatedContext || cfg.Review.RelatedContextCallers {
		t.Error("fast review must send changed diffs without repository context")
	}
	if cfg.Practices.Profile != "" || cfg.Review.Knowledge || cfg.Review.Standards || cfg.Validation.Enabled || cfg.Review.Approve.Enabled {
		t.Error("fast review retained an optional deep stage")
	}
	if cfg.Models.Default.Timeout != 55*time.Second || cfg.Models.Default.MaxTokens != 4096 || cfg.Models.Default.Reasoning != config.ReasoningOff || len(cfg.Models.Routes) != 0 || len(cfg.Models.Ensemble) != 0 {
		t.Error("fast review did not bound every selected model request")
	}
	if cfg.Models.Review == nil || cfg.Models.Review.Model != "deepseek/deepseek-v4.1-flash" {
		t.Error("fast review did not select the measured fast reviewer")
	}
}

func TestFastReviewRejectsEngineeringProfile(t *testing.T) {
	err := runFastReview(t.Context(), []string{"-profile", "engineering"})
	if err == nil || !strings.Contains(err.Error(), "does not support") {
		t.Fatalf("fast-review engineering profile error = %v", err)
	}
}
