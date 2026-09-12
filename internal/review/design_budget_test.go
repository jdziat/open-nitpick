package review

import (
	"slices"
	"testing"

	"github.com/jdziat/open-nitpick/internal/bundle"
	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/practices"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

func TestDesignBudgetDropsWholeTasksAndPricesFraming(t *testing.T) {
	cfg := config.Defaults()
	cfg.Review.Budget = config.Budget{MaxSpend: 0.015, Prices: config.BudgetPrices{Input: 1, Output: 1}, CompletionRatio: 1, Overhead: 1}
	first := practices.Target{Kind: practices.FileTarget, ID: "first.go"}
	second := practices.Target{Kind: practices.FileTarget, ID: "second.go"}
	packed := practices.DesignPacking{Design: practices.DesignPlan{Tasks: []practices.DesignTask{{ID: "first", Source: first, Sources: []practices.Target{first}}, {ID: "second", Source: second, Sources: []practices.Target{second}}}}, Plan: &bundle.Plan{FramingReserved: 1000, Batches: []bundle.Batch{{DesignTask: "first", Tokens: 4000}, {DesignTask: "second", Tokens: 4000}}}}
	e := &Engine{Config: cfg}
	fit := e.applyDesignBudget(t.Context(), vcs.Ref{}, nil, &packed, nil)
	if fit.Before.PromptTokens != 10000 || fit.After.PromptTokens != 5000 || len(packed.Plan.Batches) != 1 || packed.Plan.Batches[0].DesignTask != "first" {
		t.Fatalf("task budget lost framing or scope: %+v %+v", fit, packed)
	}
	if !slices.Equal(fit.Dropped, []string{"second.go"}) || len(packed.Design.Tasks[1].Omitted) != 1 || len(packed.Design.Tasks) != 2 {
		t.Fatalf("dropped task disappeared: %+v %+v", fit, packed.Design)
	}
}

func TestDesignBudgetMinimumDoesNotForceEveryBackgroundPackage(t *testing.T) {
	cfg := config.Defaults()
	cfg.Review.Budget = config.Budget{MaxSpend: 0.001, Prices: config.BudgetPrices{Input: 1, Output: 1}, CompletionRatio: 1, Overhead: 1, MinFiles: 1}
	var tasks []practices.DesignTask
	var batches []bundle.Batch
	for _, name := range []string{"first.go", "second.go"} {
		target := practices.Target{Kind: practices.FileTarget, ID: name}
		tasks = append(tasks, practices.DesignTask{ID: name, Source: target, Sources: []practices.Target{target}})
		batches = append(batches, bundle.Batch{DesignTask: name, Tokens: 4000})
	}
	packed := practices.DesignPacking{Design: practices.DesignPlan{Tasks: tasks}, Plan: &bundle.Plan{Batches: batches}}
	fit := (&Engine{Config: cfg}).applyDesignBudget(t.Context(), vcs.Ref{}, nil, &packed, nil)
	if !fit.Forced || len(packed.Plan.Batches) != 1 || len(fit.Kept) != 1 || len(fit.Dropped) != 1 {
		t.Fatalf("minimum forced unbounded background scope: %+v %+v", fit, packed)
	}
}

func TestDesignBudgetRetainsEveryOmissionInSharedRequest(t *testing.T) {
	cfg := config.Defaults()
	cfg.Review.Budget = config.Budget{MaxSpend: 0.001, Prices: config.BudgetPrices{Input: 1, Output: 1}, CompletionRatio: 1, Overhead: 1}
	first := practices.Target{Kind: practices.FileTarget, ID: "first.go"}
	second := practices.Target{Kind: practices.FileTarget, ID: "second.go"}
	packed := practices.DesignPacking{Design: practices.DesignPlan{Tasks: []practices.DesignTask{{ID: "first", Source: first, Sources: []practices.Target{first}}, {ID: "second", Source: second, Sources: []practices.Target{second}}}}, Plan: &bundle.Plan{Batches: []bundle.Batch{{DesignTask: "first", AdditionalDesignTasks: []string{"second"}, Tokens: 4000}}}}
	fit := (&Engine{Config: cfg}).applyDesignBudget(t.Context(), vcs.Ref{}, nil, &packed, nil)
	if len(packed.Plan.Batches) != 0 || len(fit.Dropped) != 2 || len(packed.Design.Tasks[0].Omitted) != 1 || len(packed.Design.Tasks[1].Omitted) != 1 {
		t.Fatalf("budget lost shared obligation: %+v %+v", fit, packed)
	}
}
