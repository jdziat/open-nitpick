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

const fastReviewModel = "hf:deepseek/deepseek-v4.1-flash"

// runFastReview gives a change a bounded first pass. It is intentionally not
// an approval path: the receipt names files and stages the fast pass omitted.
func runFastReview(ctx context.Context, args []string) error {
	fastCtx, cancel := context.WithTimeout(ctx, fastReviewDeadline)
	defer cancel()
	return reviewWithScope(fastCtx, "fast-review", args, applyFastReviewScope, func(engine *review.Engine) {
		engine.FastReview = true
		engine.FastLimit = fastReviewLimit
		engine.SkipTriage = true
		engine.RequestTimeout = fastReviewDeadline
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
	// A bounded pass must also bound generation and reasoning. Without a cap
	// the provider's own output maximum applies, and a reasoning model can
	// spend the whole deadline thinking. 4096 is enough for two diffs' worth
	// of findings; reasoning off keeps the cap answering rather than thinking.
	cfg.Models.Default.MaxTokens = 4096
	cfg.Models.Default.Reasoning = config.ReasoningOff
	cfg.Models.Default.StructuredOutput = config.StructuredText
	cfg.Models.Default.Provider = "synthetic"
	cfg.Models.Default.Model = fastReviewModel
	cfg.Models.Default.Timeout = 55 * time.Second
	cfg.Models.Routes = nil
	cfg.Models.Ensemble = nil
	// The review role is explicit so a slow operator default cannot defeat the
	// command's one-minute contract. This model is the measured responsive
	// Synthetic reviewer; the ordinary review configuration remains untouched.
	reviewModel := config.ModelSpec{
		Provider:         "synthetic",
		Model:            fastReviewModel,
		MaxTokens:        4096,
		Reasoning:        config.ReasoningOff,
		StructuredOutput: config.StructuredText,
		Timeout:          fastReviewDeadline,
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
