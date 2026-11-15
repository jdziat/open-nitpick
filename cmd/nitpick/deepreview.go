package main

import (
	"context"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/review"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// runDeepReview reviews a change with several agents that read the repository
// over many turns, then withholds what fails a confidence floor or a critique.
// It makes several times the model calls of review; docs/remediation-mira.md,
// "Deep review", has the design and the measurements.
func runDeepReview(ctx context.Context, args []string) error {
	return reviewWithScope(ctx, "deep-review", args, applyDeepReviewScope, func(engine *review.Engine) {
		engine.Deep = true
		engine.Policy = deepReviewScoped{inner: engine.Policy}
	})
}

// applyDeepReviewScope sends each pull request to the agents whole, within the
// limits review.deep sets.
func applyDeepReviewScope(cfg *config.Config) {
	cfg.Review.MaxFilesPerRequest = cfg.Review.Deep.MaxFilesPerRequest
	cfg.Review.TokenBudgetPerRequest = cfg.Review.Deep.TokenBudgetPerRequest
}

// deepReviewScoped applies the deep-review scope to a policy resolved for one
// pull request, which would otherwise bring back the ordinary batch limits.
type deepReviewScoped struct{ inner review.PolicyResolver }

// ResolvePolicy narrows a resolved policy to the deep-review scope while
// leaving the original config untouched.
func (d deepReviewScoped) ResolvePolicy(ctx context.Context, ref vcs.Ref, pr *vcs.PullRequest, changed []string) (*config.Config, bool, error) {
	if d.inner == nil {
		return nil, false, nil
	}
	cfg, modified, err := d.inner.ResolvePolicy(ctx, ref, pr, changed)
	if cfg == nil {
		return nil, modified, err
	}
	scoped := *cfg
	applyDeepReviewScope(&scoped)
	return &scoped, modified, err
}
