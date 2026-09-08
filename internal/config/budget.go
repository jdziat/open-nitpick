package config

import (
	"errors"
	"fmt"
	"strings"
)

// Budget bounds what one review may spend.
//
// The ceiling changes what the run does rather than stopping it partway: a
// review that died at ninety cents would publish a partial result that reads
// like a complete one, the failure this package spends most of its effort
// preventing. Over the ceiling, files are ranked by how much review
// attention they warrant and the plan keeps as many as fit.
type Budget struct {
	// MaxSpend is the ceiling in US dollars. Zero, the default, is no ceiling
	// and no estimation.
	MaxSpend float64 `yaml:"max_spend"`

	// Scope says what the ceiling covers.
	Scope BudgetScope `yaml:"scope"`

	// Prices are the rates the estimate is computed at, in US dollars per
	// MILLION tokens. They are required whenever MaxSpend is set.
	//
	// They are supplied rather than looked up because a rate is a claim about
	// what a vendor charges you today, on your account, at your tier. The
	// price table in internal/evals is dated evidence for twenty models in a
	// measurement, not a promise about anybody's bill, and a ceiling computed
	// from a stale rate is worse than no ceiling: it reports a dollar figure
	// the operator did not choose and cannot check.
	Prices BudgetPrices `yaml:"prices"`

	// CompletionRatio estimates output tokens as a fraction of prompt tokens,
	// since what a model will write is not knowable before it writes it.
	//
	// The default is deliberately generous. Under-estimating output spends
	// more than the ceiling allows, the one direction a ceiling must not fail
	// in; over-estimating reviews fewer files than it could have, and says so.
	CompletionRatio float64 `yaml:"completion_ratio"`

	// Overhead scales the review estimate to cover the triage pass, the
	// optional router, and validation. One, the default, adds nothing.
	Overhead float64 `yaml:"overhead"`

	// MinFiles is how many of the highest-ranked files are reviewed even when
	// the estimate says they do not fit. Zero refuses to exceed the ceiling at
	// any cost, and a diff whose cheapest file is over it is then reviewed not
	// at all, reported rather than silent.
	MinFiles int `yaml:"min_files"`
}

// BudgetPrices are per-million-token rates.
type BudgetPrices struct {
	// Input is dollars per million prompt tokens, as you supply it. Nothing
	// here knows a vendor's price list, so an estimate is only as current as
	// this number.
	Input float64 `yaml:"input"`

	// Output is dollars per million completion tokens, on the same terms.
	Output float64 `yaml:"output"`
}

// BudgetScope names what a ceiling covers.
type BudgetScope string

// The supported scopes.
const (
	// BudgetScopeRun bounds this invocation alone. Three pushes to a pull
	// request may each spend the ceiling.
	BudgetScopeRun BudgetScope = "run"

	// BudgetScopePullRequest bounds every run on the same pull request added
	// together, so a branch that is pushed to twenty times costs what one
	// review costs. Prior spend is read from the summary comment this tool
	// already writes, so it needs a forge and falls back to run scope with a
	// reported reason where the prior total cannot be read.
	BudgetScopePullRequest BudgetScope = "pull_request"
)

// Enabled reports whether a ceiling is in force.
func (b Budget) Enabled() bool { return b.MaxSpend > 0 }

// EffectiveScope resolves the default.
func (b Budget) EffectiveScope() BudgetScope {
	if b.Scope == "" {
		return BudgetScopeRun
	}
	return b.Scope
}

// EffectiveCompletionRatio resolves the default.
func (b Budget) EffectiveCompletionRatio() float64 {
	if b.CompletionRatio <= 0 {
		return DefaultCompletionRatio
	}
	return b.CompletionRatio
}

// EffectiveOverhead resolves the default.
func (b Budget) EffectiveOverhead() float64 {
	if b.Overhead <= 0 {
		return 1
	}
	return b.Overhead
}

// DefaultCompletionRatio is the assumed output-to-prompt token ratio.
//
// A review's answer is short next to what it reads: the prompt carries whole
// files and the answer carries findings. 0.25 is four times the ratio a clean
// review produces, chosen so the estimate errs toward reviewing less rather
// than toward spending more.
const DefaultCompletionRatio = 0.25

// validate checks the budget block.
func (b Budget) validate() []error {
	var errs []error

	if b.MaxSpend < 0 {
		errs = append(errs, errors.New("review.budget.max_spend cannot be negative"))
	}

	switch b.EffectiveScope() {
	case BudgetScopeRun, BudgetScopePullRequest:
	default:
		errs = append(errs, fmt.Errorf("review.budget.scope %q is not one of %s, %s",
			b.Scope, BudgetScopeRun, BudgetScopePullRequest))
	}

	if !b.Enabled() {
		// The rest describes how to compute a ceiling nobody asked for. A
		// price left in the file with the ceiling removed is not an error.
		return errs
	}

	// A ceiling with no rate cannot bind, and the failure would be silent. The
	// estimate would be zero, every file would fit, and the run would report a
	// budget it never applied.
	if b.Prices.Input <= 0 || b.Prices.Output <= 0 {
		errs = append(errs, errors.New(
			"review.budget.max_spend needs review.budget.prices.input and .output, "+
				"in US dollars per million tokens, or the ceiling cannot be computed"))
	}

	if b.CompletionRatio < 0 {
		errs = append(errs, errors.New("review.budget.completion_ratio cannot be negative"))
	}
	if b.Overhead < 0 {
		errs = append(errs, errors.New("review.budget.overhead cannot be negative"))
	}
	if b.MinFiles < 0 {
		errs = append(errs, errors.New("review.budget.min_files cannot be negative"))
	}

	return errs
}

// String renders the ceiling for a log line.
func (b Budget) String() string {
	if !b.Enabled() {
		return "off"
	}
	return fmt.Sprintf("$%.2f per %s", b.MaxSpend, strings.ReplaceAll(string(b.EffectiveScope()), "_", " "))
}
