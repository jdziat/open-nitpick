package review

import (
	"context"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/diff"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// spendProvider answers with an earlier run's recorded spend, and records
// whether it was asked at all.
type spendProvider struct {
	stubProvider
	prior *vcs.PriorReview
	err   error
	asked bool
}

func (p *spendProvider) PriorReview(context.Context, vcs.Ref) (*vcs.PriorReview, error) {
	p.asked = true
	if p.err != nil {
		return nil, p.err
	}
	return p.prior, nil
}

func spendEngine(t *testing.T, provider vcs.Provider, scope config.BudgetScope) *Engine {
	t.Helper()
	cfg := budgetConfig(1.00)
	cfg.Review.Budget.Scope = scope
	return &Engine{Config: cfg, Provider: provider}
}

// The whole of what issue #12 left unbuilt: a ceiling that covers a pull
// request has to subtract what earlier runs on it already spent.
func TestPullRequestScopeSubtractsWhatEarlierRunsRecorded(t *testing.T) {
	p := &spendProvider{prior: &vcs.PriorReview{Spend: 0.60}}
	e := spendEngine(t, p, config.BudgetScopePullRequest)

	if got := e.priorSpend(context.Background(), vcs.Ref{}, nil); got != 0.60 {
		t.Errorf("priorSpend = %v, want 0.60", got)
	}
	if !p.asked {
		t.Error("the forge was never asked what earlier runs spent")
	}
}

// The read is made whether or not incremental reviewing is on. They ask the
// forge the same question for unrelated reasons, and a ceiling that quietly
// became per-run because incremental was off is the silent failure.
func TestPriorSpendIsReadEvenWithIncrementalOff(t *testing.T) {
	p := &spendProvider{prior: &vcs.PriorReview{Spend: 0.25}}
	e := spendEngine(t, p, config.BudgetScopePullRequest)
	e.Config.Review.Incremental = false

	if got := e.priorSpend(context.Background(), vcs.Ref{}, nil); got != 0.25 {
		t.Errorf("priorSpend = %v with incremental off, want 0.25", got)
	}
}

// A prior read the caller already made is used rather than repeated: the forge
// answer is expensive, and asking twice can return two different totals for
// one review.
func TestAPriorAlreadyReadIsNotFetchedAgain(t *testing.T) {
	p := &spendProvider{}
	e := spendEngine(t, p, config.BudgetScopePullRequest)

	if got := e.priorSpend(context.Background(), vcs.Ref{}, &vcs.PriorReview{Spend: 0.10}); got != 0.10 {
		t.Errorf("priorSpend = %v, want 0.10", got)
	}
	if p.asked {
		t.Error("the forge was asked again for an answer the caller already had")
	}
}

// Every way of not getting an answer is zero, which bounds the run as if it
// were the first. That is the wrong number, so it is logged rather than
// assumed; the assertion here is only that it does not invent one.
func TestAnUnavailablePriorSpendIsZeroRatherThanAGuess(t *testing.T) {
	for name, p := range map[string]vcs.Provider{
		"the read failed":           &spendProvider{err: context.DeadlineExceeded},
		"no earlier run":            &spendProvider{prior: &vcs.PriorReview{}},
		"provider cannot read them": &stubProvider{},
	} {
		t.Run(name, func(t *testing.T) {
			e := spendEngine(t, p, config.BudgetScopePullRequest)
			if got := e.priorSpend(context.Background(), vcs.Ref{}, nil); got != 0 {
				t.Errorf("priorSpend = %v, want 0", got)
			}
		})
	}
}

// Run scope never asks. Three pushes may each spend the ceiling, which is what
// the scope means, and the forge round trip that would answer a question
// nobody asked is not made.
func TestRunScopeNeverReadsPriorSpend(t *testing.T) {
	a := file("a.go", 10, 5)
	plan := planOf(entry(a, 100))

	for scope, want := range map[config.BudgetScope]bool{
		config.BudgetScopeRun:         false,
		config.BudgetScopePullRequest: true,
	} {
		p := &spendProvider{prior: &vcs.PriorReview{Spend: 0.10}}
		e := spendEngine(t, p, scope)

		if _, _, err := e.applyBudget(context.Background(), vcs.Ref{}, nil, plan, diff.Files{a}, nil); err != nil {
			t.Fatalf("%s: %v", scope, err)
		}
		if p.asked != want {
			t.Errorf("%s scope: asked the forge = %v, want %v", scope, p.asked, want)
		}
	}
}

// What a run was priced at rides on the review it publishes, because a CI job
// keeps nothing between runs.
func TestTheEstimateIsRecordedOnThePublishedReview(t *testing.T) {
	report := &Report{Budget: &Fit{After: Estimate{Dollars: 0.4212}}}
	if got := Render(report, nil, config.Defaults()).Spend; got != 0.4212 {
		t.Errorf("published Spend = %v, want 0.4212", got)
	}

	if got := Render(&Report{}, nil, config.Defaults()).Spend; got != 0 {
		t.Errorf("published Spend = %v with no ceiling configured, want 0", got)
	}
}

// A ceiling reduced by earlier spend is not the ceiling in the operator's
// config, and a report quoting one without the other is a report that reads
// wrong.
func TestTheReportSaysWhenEarlierSpendReducedTheCeiling(t *testing.T) {
	note := budgetNote(&Report{Budget: &Fit{
		Kept: []string{"a.go"}, Dropped: []string{"b.go"},
		Before: Estimate{Dollars: 1.0}, After: Estimate{Dollars: 0.3},
		Ceiling: 0.40, Prior: 0.60,
	}})

	if !strings.Contains(note, "$0.60") || !strings.Contains(note, "pull_request") {
		t.Errorf("the note does not account for the earlier spend:\n%s", note)
	}

	plain := budgetNote(&Report{Budget: &Fit{
		Kept: []string{"a.go"}, Dropped: []string{"b.go"},
		Before: Estimate{Dollars: 1.0}, After: Estimate{Dollars: 0.3}, Ceiling: 1.0,
	}})
	if strings.Contains(plain, "pull_request") {
		t.Errorf("run scope mentioned a subtraction that never happened:\n%s", plain)
	}
}
