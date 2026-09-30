package main

import (
	"context"
	"strings"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/review"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// fastReviewFindings bounds output without dropping changed files.
const fastReviewFindings = 10

// fastReviewBatchFiles bounds how many diffs ride in a single request. It
// bounds request size, not how many files are reviewed.
const fastReviewBatchFiles = 4

// fastReviewFallbackModel is the measured fastest cheap reviewer on the
// tuning corpus (2026-09-29 OpenRouter battery: 2/2 recall, zero noise,
// 1-4 s per single-file request, ~$0.0007/review).
const fastReviewFallbackModel = "deepseek/deepseek-v4.1-flash"

// runFastReview gives a change a bounded first pass. It is intentionally not
// an approval path: the receipt names the stages the fast pass omitted and the
// highest-ranked findings it withheld.
func runFastReview(ctx context.Context, args []string) error {
	return reviewWithScope(ctx, "fast-review", args, applyFastReviewScope, func(engine *review.Engine) {
		engine.FastReview = true
		engine.FastLimit = fastReviewFindings
		engine.SkipTriage = true
		engine.Resume = false
		engine.Policy = fastReviewScoped{inner: engine.Policy}
	})
}

// applyFastReviewScope keeps every request small enough to return promptly
// while still reading the whole change. The command accepts a reduced review
// surface and renders that fact.
func applyFastReviewScope(cfg *config.Config) {
	cfg.Practices.Profile = ""
	cfg.Persona.Nitpick = config.NitpickNormal
	// The planner requires a positive limit even when every file is wanted.
	cfg.Review.MaxFiles = 1 << 30
	cfg.Review.MaxFilesPerRequest = fastReviewBatchFiles
	cfg.Review.TokenBudgetPerRequest = min(cfg.Review.TokenBudgetPerRequest, 12000)
	cfg.Review.IncludeFullFiles = false
	cfg.Review.RelatedContext = false
	cfg.Review.RelatedContextCallers = false
	cfg.Review.Knowledge = false
	cfg.Review.Standards = false
	cfg.Validation.Enabled = false
	cfg.Review.Incremental = false
	cfg.Review.Approve.Enabled = false
	// OpenRouter is the fallback only when neither config nor environment names a model.
	if cfg.Models.Default.Provider == "" {
		cfg.Models.Default.Provider = "openrouter"
	}
	if cfg.Models.Default.Model == "" {
		cfg.Models.Default.Model = fastReviewFallbackModel
	}
	// A small output cap and disabled reasoning keep the pass responsive
	// without cancelling a provider that needs longer to finish.
	cfg.Models.Default.MaxTokens = 4096
	cfg.Models.Default.Reasoning = config.ReasoningOff
	cfg.Models.Default.StructuredOutput = config.StructuredText
	cfg.Models.Routes = nil
	cfg.Models.Ensemble = nil
	cfg.Review.Concurrency = fastReviewConcurrency(cfg.Models.Default.Provider)
	// Preserve the configured provider, model, and timeout; bound tokens and
	// reasoning so each request returns quickly.
	reviewModel := cfg.Models.Default
	cfg.Models.Review = &reviewModel
}

// fastReviewConcurrency chooses a conservative starting width by provider.
// Account-specific rate limits can be lower than these defaults.
func fastReviewConcurrency(provider string) int {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "openrouter":
		// OpenRouter fronts many upstreams and tolerates wide fan-out.
		return 8
	case "deepseek", "fireworks", "togetherai", "groq", "cerebras":
		return 6
	case "openai", "anthropic", "azure", "mistral":
		return 4
	case "ollama", "llamacpp":
		// Local models are bounded by the machine, not a vendor.
		return 2
	default:
		return 4
	}
}

// fastReviewScoped reapplies the command scope after policy substitution.
type fastReviewScoped struct{ inner review.PolicyResolver }

// ResolvePolicy narrows a resolved policy to the fast-review scope while
// leaving the original config untouched.
func (f fastReviewScoped) ResolvePolicy(ctx context.Context, ref vcs.Ref, pr *vcs.PullRequest, changed []string) (*config.Config, bool, error) {
	if f.inner == nil {
		return nil, false, nil
	}
	cfg, modified, err := f.inner.ResolvePolicy(ctx, ref, pr, changed)
	if cfg == nil {
		return nil, modified, err
	}
	scoped := *cfg
	applyFastReviewScope(&scoped)
	return &scoped, modified, err
}
