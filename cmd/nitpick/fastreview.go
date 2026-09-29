package main

import (
	"context"
	"time"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/review"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

const fastReviewLimit = 10
const fastReviewFilesPerRequest = 2
const fastReviewConcurrency = 4

const fastReviewDeadline = 55 * time.Second

// runFastReview gives a change a bounded first pass. It is intentionally not
// an approval path: the receipt names files and stages the fast pass omitted.
func runFastReview(ctx context.Context, args []string) error {
	fastCtx, cancel := context.WithTimeout(ctx, fastReviewDeadline)
	defer cancel()
	return reviewWithScope(fastCtx, "fast-review", args, applyFastReviewScope, func(engine *review.Engine) {
		engine.FastReview = true
		engine.FastLimit = fastReviewLimit
		engine.SkipTriage = true
		engine.RequestTimeout = 40 * time.Second
		engine.Policy = fastReviewScoped{inner: engine.Policy}
	})
}

// applyFastReviewScope keeps every request small enough to return promptly.
// The command accepts a reduced review surface and renders that fact.
func applyFastReviewScope(cfg *config.Config) {
	cfg.Practices.Profile = ""
	cfg.Persona.Nitpick = config.NitpickNormal
	cfg.Review.MaxFiles = fastReviewLimit
	// Five small requests stay below the provider concurrency that the regular
	// review already uses, while still covering ten changed files in one pass.
	cfg.Review.MaxFilesPerRequest = fastReviewFilesPerRequest
	cfg.Review.Concurrency = fastReviewConcurrency
	cfg.Review.IncludeFullFiles = false
	cfg.Review.RelatedContext = false
	cfg.Review.RelatedContextCallers = false
	cfg.Review.Knowledge = false
	cfg.Review.Standards = false
	cfg.Validation.Enabled = false
	cfg.Review.Incremental = false
	cfg.Review.Approve.Enabled = false
	cfg.Models.Default.Timeout = 40 * time.Second
	cfg.Models.Routes = nil
	cfg.Models.Ensemble = nil
	if cfg.Models.Review != nil {
		reviewModel := *cfg.Models.Review
		reviewModel.Timeout = 40 * time.Second
		cfg.Models.Review = &reviewModel
	}
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
