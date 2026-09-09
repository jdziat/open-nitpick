package bundle

import (
	"context"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/diff"
)

// The framing comes out of the budget before the entries are fitted.
//
// The budget bounded the entries and nothing else, so a request estimated at
// 24,852 tokens against a 32,000 budget reached the provider at 32,653. Issue
// #81.
func TestTheBudgetLeavesRoomForTheFraming(t *testing.T) {
	cfg := config.Defaults()
	cfg.Review.TokenBudgetPerRequest = 10000
	cfg.Review.IncludeFullFiles = false

	files := diff.Files{hunked("a.go"), hunked("b.go")}

	plain, err := AssembleWith(context.Background(), cfg, files, nil, nil)
	if err != nil {
		t.Fatalf("AssembleWith: %v", err)
	}
	if plain.BudgetPerBatch != 10000 {
		t.Errorf("with no reserve, BudgetPerBatch = %d, want the configured 10000", plain.BudgetPerBatch)
	}
	if plain.FramingReserved != 0 {
		t.Errorf("with no reserve, FramingReserved = %d, want 0", plain.FramingReserved)
	}

	held, err := AssembleReserving(context.Background(), cfg, files, nil, nil, Reserve{Tokens: 2500})
	if err != nil {
		t.Fatalf("AssembleReserving: %v", err)
	}
	if held.BudgetPerBatch != 7500 {
		t.Errorf("BudgetPerBatch = %d, want 10000 less the 2500 reserved", held.BudgetPerBatch)
	}
	if held.FramingReserved != 2500 {
		t.Errorf("FramingReserved = %d, want 2500 disclosed", held.FramingReserved)
	}
}

// A reserve larger than the budget is a misconfiguration, and answering it by
// batching nothing would turn a bad number into no review.
func TestAnOversizedReserveStillReviews(t *testing.T) {
	cfg := config.Defaults()
	cfg.Review.TokenBudgetPerRequest = 100
	cfg.Review.IncludeFullFiles = false

	plan, err := AssembleReserving(context.Background(), cfg,
		diff.Files{hunked("a.go")}, nil, nil, Reserve{Tokens: 5000})
	if err != nil {
		t.Fatalf("AssembleReserving: %v", err)
	}
	if plan.BudgetPerBatch < 1 {
		t.Errorf("BudgetPerBatch = %d, want a floor of at least 1", plan.BudgetPerBatch)
	}
	if len(plan.Batches) == 0 {
		t.Error("an oversized reserve produced no batches, so nothing would be reviewed")
	}
}

// hunked is a file with something in it, so the plan has an entry to batch.
func hunked(path string) *diff.File {
	return &diff.File{
		Path: path,
		Kind: diff.ChangeAdded,
		Hunks: []diff.Hunk{{
			NewStart: 1, NewLines: 2,
			Lines: []diff.Line{
				{Kind: diff.LineAdded, Content: "package a", NewLine: 1},
				{Kind: diff.LineAdded, Content: "func F() {}", NewLine: 2},
			},
		}},
	}
}
