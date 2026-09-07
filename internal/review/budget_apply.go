package review

import (
	"context"
	"fmt"

	"github.com/jdziat/open-nitpick/internal/bundle"
	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/diff"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// applyBudget enforces the spending ceiling, returning what it decided and a
// re-assembled plan when files had to be dropped.
//
// A nil fit means no ceiling was configured. A nil plan means the original
// stands: either everything fit, or nothing was dropped.
func (e *Engine) applyBudget(
	ctx context.Context,
	ref vcs.Ref,
	prior *vcs.PriorReview,
	plan *bundle.Plan,
	files diff.Files,
	fetch bundle.ContentFetcher,
) (*Fit, *bundle.Plan, error) {
	b := e.Config.Review.Budget
	if !b.Enabled() {
		return nil, nil, nil
	}

	remaining := b.MaxSpend
	if b.EffectiveScope() == config.BudgetScopePullRequest {
		spent := e.priorSpend(ctx, ref, prior)
		remaining -= spent
		if spent > 0 {
			e.log().Info("prior spend on this pull request counts against the ceiling",
				"spent", fmt.Sprintf("$%.4f", spent), "remaining", fmt.Sprintf("$%.4f", remaining))
		}
		if remaining < 0 {
			remaining = 0
		}
	}

	ranked := Rank(files)
	keep, fit := FitToBudget(e.Config, plan, ranked, remaining)
	fit.Prior = b.MaxSpend - remaining

	if !fit.Trimmed() {
		e.log().Info("within the spending ceiling",
			"estimate", fit.Before.String(), "ceiling", b.String())
		return &fit, nil, nil
	}

	e.log().Warn("the spending ceiling does not pay for the whole diff; "+
		"reviewing the highest-ranked files and reporting the rest",
		"estimate", fit.Before.String(),
		"remaining", fmt.Sprintf("$%.4f", remaining),
		"kept", len(fit.Kept), "dropped", len(fit.Dropped))

	if fit.Forced {
		e.log().Warn("review.budget.min_files keeps files the ceiling does not pay for",
			"min_files", b.MinFiles, "estimate", fit.After.String())
	}

	// Re-assemble from the kept files. Packing is a function of which files are
	// present, so a trimmed plan has to be built rather than edited: dropping
	// entries from batches would leave the token totals describing a prompt
	// that is no longer sent.
	kept := make(diff.Files, 0, len(keep))
	inKeep := make(map[string]bool, len(keep))
	for _, p := range keep {
		inKeep[p] = true
	}
	for _, f := range files {
		if inKeep[f.Path] {
			kept = append(kept, f)
		}
	}

	if len(kept) == 0 {
		// Nothing fits. The plan becomes empty rather than partial, and the
		// report says the ceiling is why. Reviewing one file and calling it a
		// review would be worse than reviewing none and saying so.
		empty := &bundle.Plan{Skipped: plan.Skipped}
		empty.Skipped = append(empty.Skipped, budgetSkips(fit.Dropped)...)
		return &fit, empty, nil
	}

	trimmed, err := bundle.AssembleWith(ctx, e.Config, kept, fetch, bundle.ListerFrom(e.Provider, ref))
	if err != nil {
		return nil, nil, fmt.Errorf("re-assemble under the spending ceiling: %w", err)
	}
	trimmed.Skipped = append(trimmed.Skipped, budgetSkips(fit.Dropped)...)

	// The trimmed plan is what gets sent, so the reported estimate is
	// recomputed from it rather than left as the projection that chose it.
	fit.After = EstimatePlan(e.Config, trimmed)
	fit.Forced = fit.After.Dollars > remaining

	return &fit, trimmed, nil
}

// budgetSkips turns dropped paths into the planner's skip records, so they are
// published by the same path every other exclusion takes.
func budgetSkips(dropped []string) []bundle.Skip {
	out := make([]bundle.Skip, 0, len(dropped))
	for _, p := range dropped {
		out = append(out, bundle.Skip{Path: p, Reason: "over the review.budget.max_spend ceiling"})
	}
	return out
}

// priorSpend totals what earlier runs on this pull request recorded spending.
//
// The total comes off the pull request itself, from the marker each review body
// carries, because a CI job keeps nothing between runs and the pull request is
// the one place both ends of the question can reach.
//
// The read is made whether or not review.incremental is on. Incremental
// reviewing and a pull-request ceiling ask the forge the same question for
// different reasons, and a ceiling that silently became per-run because an
// unrelated setting was off would be the quiet failure this package exists to
// avoid. Every way of not getting an answer returns zero and says so: a ceiling
// bounded as if this were the first run is the wrong answer, and one nobody was
// told about is worse.
func (e *Engine) priorSpend(ctx context.Context, ref vcs.Ref, prior *vcs.PriorReview) float64 {
	if prior == nil {
		reader, ok := e.Provider.(vcs.PriorReviewer)
		if !ok {
			e.log().Warn("review.budget.scope is pull_request and this provider cannot read " +
				"earlier reviews, so this run is bounded as if it were the first")
			return 0
		}
		read, err := reader.PriorReview(ctx, ref)
		if err != nil {
			e.log().Warn("review.budget.scope is pull_request and earlier reviews could not be "+
				"read, so this run is bounded as if it were the first", "error", err)
			return 0
		}
		prior = read
	}

	if prior == nil || prior.Spend <= 0 {
		// Nothing recorded is the ordinary first run, and it is also a pull
		// request whose earlier runs had no ceiling and therefore priced
		// nothing. The two are indistinguishable here, so the report says what
		// was found rather than what it means.
		return 0
	}
	return prior.Spend
}
