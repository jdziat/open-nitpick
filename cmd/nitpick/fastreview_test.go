package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/vcs"
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

	if cfg.Review.MaxFiles != 1<<30 || cfg.Review.MaxFilesPerRequest != fastReviewBatchFiles || cfg.Review.Concurrency != 8 {
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

func TestFastReviewScopesSubstitutedPolicyWithoutMutation(t *testing.T) {
	base := config.Defaults()
	base.Practices.Profile = "engineering"
	base.Review.Approve.Enabled = true
	base.Models.Default = config.ModelSpec{Provider: "ollama", Model: "local"}
	scoped := fastReviewScoped{inner: fixedPolicy{cfg: base}}
	got, modified, err := scoped.ResolvePolicy(context.Background(), vcs.Ref{}, nil, []string{config.FileName})
	if err != nil || !modified {
		t.Fatalf("substitution: modified=%v err=%v", modified, err)
	}
	if got.Review.MaxFiles != 1<<30 || got.Review.Concurrency != 2 || got.Review.Approve.Enabled || got.Practices.Profile != "" {
		t.Fatalf("unscoped substituted policy: %+v", got.Review)
	}
	if !base.Review.Approve.Enabled || base.Practices.Profile != "engineering" || base.Review.MaxFiles != config.Defaults().Review.MaxFiles {
		t.Fatal("scoping mutated the accepted policy")
	}
}

func TestFastReviewSuppliesAMeasuredFallbackModel(t *testing.T) {
	cfg := config.Defaults()
	applyFastReviewScope(cfg)
	if cfg.Models.Default.Provider != "openrouter" || cfg.Models.Default.Model != fastReviewFallbackModel || cfg.Models.Default.Timeout != config.Defaults().Models.Default.Timeout {
		t.Fatalf("fallback model = %s/%s timeout %s", cfg.Models.Default.Provider, cfg.Models.Default.Model, cfg.Models.Default.Timeout)
	}
	if cfg.Models.Review == nil || cfg.Models.Review.Provider != "openrouter" || cfg.Models.Review.Model != fastReviewFallbackModel {
		t.Fatal("fast review did not select the fallback for the review role")
	}
}

func TestFastReviewSelectsConcurrencyForEachProvider(t *testing.T) {
	for _, tc := range []struct {
		provider string
		want     int
	}{{"openrouter", 8}, {"deepseek", 6}, {"openai", 4}, {"ollama", 2}, {"llamacpp", 2}, {"unknown", 4}, {" OpenRouter ", 8}} {
		t.Run(tc.provider, func(t *testing.T) {
			cfg := config.Defaults()
			cfg.Models.Default.Provider = tc.provider
			applyFastReviewScope(cfg)
			if cfg.Review.Concurrency != tc.want {
				t.Fatalf("concurrency=%d, want %d", cfg.Review.Concurrency, tc.want)
			}
		})
	}
}

func TestFastReviewRejectsEngineeringProfile(t *testing.T) {
	err := runFastReview(t.Context(), []string{"-profile", "engineering"})
	if err == nil || !strings.Contains(err.Error(), "does not support") {
		t.Fatalf("fast-review engineering profile error = %v", err)
	}
}
