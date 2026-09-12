package review

import (
	"context"
	"slices"
	"sort"

	"github.com/jdziat/open-nitpick/internal/bundle"
	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/diff"
	"github.com/jdziat/open-nitpick/internal/practices"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// applyDesignBudget selects whole requests so a file-level trim cannot lose context.
func (e *Engine) applyDesignBudget(ctx context.Context, ref vcs.Ref, prior *vcs.PriorReview, packed *practices.DesignPacking, files diff.Files) *Fit {
	policy := e.Config.Review.Budget
	if !policy.Enabled() {
		return nil
	}
	remaining, spent := policy.MaxSpend, 0.0
	if policy.EffectiveScope() == config.BudgetScopePullRequest {
		spent = e.priorSpend(ctx, ref, prior)
		remaining = max(0, remaining-spent)
	}
	fit := &Fit{Before: EstimatePlan(e.Config, packed.Plan), Ceiling: remaining, Prior: spent}
	ranks := map[string]int{}
	for i, file := range Rank(files) {
		ranks[file.Path] = i
	}
	sourcesByTask := map[string][]practices.Target{}
	for _, task := range packed.Design.Tasks {
		sourcesByTask[task.ID] = task.Sources
	}
	batches := slices.Clone(packed.Plan.Batches)
	rank := func(batch bundle.Batch) int {
		best := len(ranks)
		for _, entry := range batch.Entries {
			if r, ok := ranks[entry.File.Path]; ok && !entry.SourceOnly {
				best = min(best, r)
			}
		}
		return best
	}
	sort.SliceStable(batches, func(i, j int) bool { return rank(batches[i]) < rank(batches[j]) })
	kept := make([]bundle.Batch, 0, len(batches))
	admitted := map[string]bool{}
	for _, batch := range batches {
		candidate := *packed.Plan
		candidate.Batches = append(slices.Clone(kept), batch)
		estimate := EstimatePlan(e.Config, &candidate)
		force := len(admitted) < policy.MinFiles
		if estimate.Dollars > remaining && !force {
			reason := "over the review.budget.max_spend ceiling"
			for i := range packed.Design.Tasks {
				task := &packed.Design.Tasks[i]
				if slices.Contains(batch.DesignTaskIDs(), task.ID) {
					task.Omitted = append(task.Omitted, practices.Omission{Target: practices.Target{Kind: practices.UnitTarget, ID: task.ID}, Reason: reason})
					packed.Plan.Skipped = append(packed.Plan.Skipped, bundle.Skip{Path: task.Source.ID, Reason: reason})
					for _, source := range task.Sources {
						fit.Dropped = append(fit.Dropped, source.ID)
					}
				}
			}
			continue
		}
		kept = append(kept, batch)
		for _, id := range batch.DesignTaskIDs() {
			for _, source := range sourcesByTask[id] {
				if !admitted[source.ID] {
					admitted[source.ID] = true
					fit.Kept = append(fit.Kept, source.ID)
				}
			}
		}
	}
	packed.Plan.Batches = kept
	fit.After = EstimatePlan(e.Config, packed.Plan)
	fit.Forced = fit.After.Dollars > remaining
	return fit
}
