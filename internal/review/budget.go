package review

import (
	"fmt"
	"sort"

	"github.com/jdziat/open-nitpick/internal/bundle"
	"github.com/jdziat/open-nitpick/internal/config"
)

// Estimate is what a plan is projected to cost, and how that number was
// reached.
//
// Every field is published rather than kept internal. A ceiling that silently
// removed files from a review would be the same silence the coverage reporting
// exists to prevent: a reader has to be able to see the number, the rate it was
// computed at, and that it was an estimate.
type Estimate struct {
	// PromptTokens is what the plan's batches are estimated to send, summed
	// across every reviewer.
	PromptTokens int

	// CompletionTokens is the assumed answer size, PromptTokens scaled by the
	// configured ratio. It is a guess, and it is the reason the estimate errs
	// high.
	CompletionTokens int

	// Reviewers is how many models read each batch: one, plus the ensemble.
	Reviewers int

	// Dollars is the projected spend, including the overhead multiplier.
	Dollars float64
}

// String renders the estimate for a log line or a report.
func (e Estimate) String() string {
	return fmt.Sprintf("$%.4f (%d prompt + ~%d completion tokens across %d reviewer(s))",
		e.Dollars, e.PromptTokens, e.CompletionTokens, e.Reviewers)
}

// EstimatePlan projects what reviewing every batch would cost.
func EstimatePlan(cfg *config.Config, plan *bundle.Plan) Estimate {
	b := cfg.Review.Budget

	reviewers := 1 + len(cfg.Models.Ensemble)
	est := Estimate{Reviewers: reviewers}
	for _, batch := range plan.Batches {
		est.PromptTokens += batch.Tokens * reviewers
	}
	est.CompletionTokens = int(float64(est.PromptTokens) * b.EffectiveCompletionRatio())
	est.Dollars = dollars(b, est.PromptTokens, est.CompletionTokens)
	return est
}

// dollars prices tokens at the configured per-million rates.
func dollars(b config.Budget, prompt, completion int) float64 {
	const perMillion = 1_000_000.0
	raw := (float64(prompt)*b.Prices.Input + float64(completion)*b.Prices.Output) / perMillion
	return raw * b.EffectiveOverhead()
}

// Fit trims a plan to what the remaining budget pays for, keeping the
// highest-ranked files, and reports what it did.
//
// remaining is the ceiling minus anything already spent on this pull request
// under pull_request scope; under run scope it is the whole ceiling.
//
// Trimming works on FILES and re-plans, rather than dropping whole batches,
// because a batch is a packing decision and the ranking is a value judgment.
// Dropping the last batch would discard whichever files happened to be packed
// together last.
type Fit struct {
	// Kept and Dropped are file paths, highest-ranked first.
	Kept    []string
	Dropped []string

	// Before is the estimate for the whole plan, After for the trimmed one.
	Before, After Estimate

	// Ceiling is what After had to come in under.
	Ceiling float64

	// Forced is set when MinFiles kept files the ceiling did not pay for. The
	// run then exceeds the ceiling deliberately, and says so.
	Forced bool
}

// Trimmed reports whether anything was dropped.
func (f Fit) Trimmed() bool { return len(f.Dropped) > 0 }

// FitToBudget selects the files a plan can afford.
//
// It returns the paths to keep, in ranked order, and the fit that produced
// them. The caller re-assembles the plan from the kept files: this function
// does not rebuild the plan itself, because packing depends on fetching file
// content and this has to stay testable without a forge.
//
// The estimate is linear in prompt tokens, so a subset's cost is taken as its
// share of the whole plan's tokens. That holds while every batch is priced at
// the same rate, which is the case here: routes may send batches to different
// models, and pricing each route separately is the next thing this should
// learn. Until it does, a routed configuration is estimated at the configured
// rate for every batch, which the report states.
func FitToBudget(cfg *config.Config, plan *bundle.Plan, ranked []Complexity, remaining float64) (keep []string, fit Fit) {
	b := cfg.Review.Budget
	fit.Ceiling = remaining
	fit.Before = EstimatePlan(cfg, plan)
	fit.After = fit.Before

	all := planPaths(plan)
	if !b.Enabled() || fit.Before.Dollars <= remaining {
		fit.Kept = orderByRank(all, ranked)
		return fit.Kept, fit
	}

	// Per-file token cost, from the batch each file landed in. A batch's
	// tokens are shared by its entries in proportion to what each contributed.
	perFile := tokensPerFile(plan)

	reviewers := fit.Before.Reviewers
	ratio := b.EffectiveCompletionRatio()

	var (
		kept    []string
		tokens  int
		dropped []string
	)
	for _, c := range ranked {
		t, ok := perFile[c.Path]
		if !ok {
			// Not in a batch: already skipped by the planner, so it costs
			// nothing and is not this function's to report.
			continue
		}

		next := tokens + t*reviewers
		cost := dollars(b, next, int(float64(next)*ratio))
		if cost > remaining && len(kept) >= b.MinFiles {
			dropped = append(dropped, c.Path)
			continue
		}

		kept = append(kept, c.Path)
		tokens = next
	}

	fit.Kept = kept
	fit.Dropped = dropped
	fit.After = Estimate{
		PromptTokens:     tokens,
		CompletionTokens: int(float64(tokens) * ratio),
		Reviewers:        reviewers,
	}
	fit.After.Dollars = dollars(b, fit.After.PromptTokens, fit.After.CompletionTokens)
	fit.Forced = fit.After.Dollars > remaining

	return kept, fit
}

// planPaths lists every path the plan would review.
func planPaths(plan *bundle.Plan) []string {
	var out []string
	for _, batch := range plan.Batches {
		out = append(out, batch.Paths()...)
	}
	return out
}

// tokensPerFile splits each batch's token estimate across its entries.
//
// Entry.Tokens is the entry's own rendered size, and a batch's total is more
// than their sum: the prompt carries instructions and the diff around them. The
// difference is shared equally, since it is not attributable to one file.
func tokensPerFile(plan *bundle.Plan) map[string]int {
	out := map[string]int{}
	for _, batch := range plan.Batches {
		if len(batch.Entries) == 0 {
			continue
		}

		var entrySum int
		for _, e := range batch.Entries {
			entrySum += e.Tokens
		}
		overhead := batch.Tokens - entrySum
		if overhead < 0 {
			overhead = 0
		}
		share := overhead / len(batch.Entries)

		for _, e := range batch.Entries {
			out[e.File.Path] = e.Tokens + share
		}
	}
	return out
}

// orderByRank puts paths in ranked order, appending anything the ranking did
// not cover so nothing is lost by an incomplete ranking.
func orderByRank(paths []string, ranked []Complexity) []string {
	pos := make(map[string]int, len(ranked))
	for i, c := range ranked {
		pos[c.Path] = i
	}

	out := append([]string(nil), paths...)
	sort.SliceStable(out, func(i, j int) bool {
		pi, iOK := pos[out[i]]
		pj, jOK := pos[out[j]]
		switch {
		case iOK && jOK:
			return pi < pj
		case iOK:
			return true
		case jOK:
			return false
		default:
			return out[i] < out[j]
		}
	})
	return out
}
