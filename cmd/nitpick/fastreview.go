package main

import (
	"context"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/review"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

const fastReviewLimit = 10
const fastReviewFilesPerRequest = 2

// runFastReview gives a change a bounded first pass. It is intentionally not
// an approval path: the receipt names files and stages the fast pass omitted.
func runFastReview(ctx context.Context, args []string) error {
	return reviewWithScope(ctx, "fast-review", args, applyFastReviewScope, func(engine *review.Engine) {
		engine.FastReview = true
		engine.FastLimit = fastReviewLimit
		engine.SkipTriage = true
		engine.Policy = fastReviewScoped{inner: engine.Policy}
	})
}

// applyFastReviewScope keeps every request small enough to return promptly.
// The command accepts a reduced review surface and renders that fact.
func applyFastReviewScope(cfg *config.Config) {
	cfg.Practices.Profile = ""
	cfg.Persona.Nitpick = config.NitpickNormal
	cfg.Review.MaxFiles = fastReviewLimit
	cfg.Review.MaxFilesPerRequest = fastReviewFilesPerRequest
	cfg.Review.IncludeFullFiles = false
	cfg.Review.RelatedContext = false
	cfg.Review.RelatedContextCallers = false
	cfg.Review.Knowledge = false
	cfg.Review.Standards = false
	cfg.Validation.Enabled = false
	cfg.Review.Incremental = false
	cfg.Review.Approve.Enabled = false
	// A small output cap and disabled reasoning keep the pass responsive
	// without cancelling a provider that needs longer to finish.
	cfg.Models.Default.MaxTokens = 4096
	cfg.Models.Default.Reasoning = config.ReasoningOff
	cfg.Models.Default.StructuredOutput = config.StructuredText
	cfg.Models.Routes = nil
	cfg.Models.Ensemble = nil
	// Preserve the configured provider, model, timeout, and concurrency. Each
	// provider owns its own capacity and operators can select a responsive model.
	reviewModel := config.ModelSpec{
		Provider:         cfg.Models.Default.Provider,
		Model:            cfg.Models.Default.Model,
		MaxTokens:        4096,
		Reasoning:        config.ReasoningOff,
		StructuredOutput: config.StructuredText,
		Timeout:          cfg.Models.Default.Timeout,
	}
	cfg.Models.Review = &reviewModel
}

// fastReviewScoped reapplies the command scope after policy substitution.
type fastReviewScoped struct{ inner review.PolicyResolver }

func (f fastReviewScoped) ResolvePolicy(ctx context.Context, ref vcs.Ref, pr *vcs.PullRequest, changed []string) (*config.Config, bool, error) {
	cfg, modified, err := f.inner.ResolvePolicy(ctx, ref, pr, changed)
	if cfg == nil {
		return nil, modified, err
	}
	scoped := *cfg
	applyFastReviewScope(&scoped)
	return &scoped, modified, err
}
