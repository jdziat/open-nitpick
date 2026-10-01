//go:build eval

package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/evals"
	"github.com/jdziat/open-nitpick/internal/llm"
	"github.com/jdziat/open-nitpick/internal/review"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// TestFastReviewLiveMeasuresReportedUsage exercises the shipped scope with a
// caller-supplied git fixture. Keep it opt-in: it calls providers and costs money.
func TestFastReviewLiveMeasuresReportedUsage(t *testing.T) {
	repo := os.Getenv("NITPICK_FAST_BENCH_REPO")
	if repo == "" {
		t.Skip("set NITPICK_FAST_BENCH_REPO and OPENROUTER_API_KEY")
	}
	models := strings.Split(os.Getenv("NITPICK_FAST_BENCH_MODELS"), ",")
	if len(models) == 1 && models[0] == "" {
		models = []string{fastReviewFallbackModel, "google/gemma-4-31b-it", "poolside/laguna-s-2.1"}
	}
	for pass := 1; pass <= 2; pass++ {
		for _, model := range models {
			cfg := config.Defaults()
			cfg.Models.Default.Provider = "openrouter"
			cfg.Models.Default.Model = model
			applyFastReviewScope(cfg)
			roles, err := llm.BuildRoles(cfg)
			if err != nil {
				t.Fatal(err)
			}
			meter := evals.MeterClient(roles.Review)
			provider := vcs.NewLocal(repo, io.Discard)
			engine := &review.Engine{Config: cfg, Roles: roles, Provider: provider, Policy: review.NoPolicy("benchmark fixture has no review policy"), FastReview: true, FastLimit: fastReviewFindings, SkipTriage: true}
			started := time.Now()
			report, err := engine.Review(context.Background(), vcs.Ref{Base: "HEAD", Head: vcs.Worktree})
			if err != nil {
				t.Fatal(err)
			}
			if report.Plan.Files() == 0 || !report.PipelineComplete() {
				t.Fatal("benchmark did not review the complete fixture")
			}
			row := struct {
				Pass     int
				Model    string
				Seconds  float64
				Files    int
				Findings []review.Finding
				Usage    evals.TokenUsage
			}{pass, model, time.Since(started).Seconds(), report.Plan.Files(), report.Findings, meter.Usage()}
			encoded, err := json.Marshal(row)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("FAST_BENCH %s", encoded)
		}
	}
}
