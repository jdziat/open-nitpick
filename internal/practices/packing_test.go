package practices

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/bundle"
	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/diff"
	llms "github.com/nocturnium/llm-go-sdk/v6"
)

func TestDesignPackingKeepsWholeTaskInOneRequest(t *testing.T) {
	files, inventory := designPlanningFixture()
	design := PlanDesign(t.Context(), inventory, files, []string{"store/read.go"})
	cfg := config.Defaults()
	cfg.Review.MaxFilesPerRequest = 6
	packed := PackDesign(t.Context(), cfg, design, files, diff.Files{&diff.File{Path: "store/read.go", Kind: diff.ChangeModified}}, bundle.Reserve{Tokens: 100})
	if len(packed.Plan.Batches) != 1 || len(packed.Design.Tasks[0].Omitted) != 0 {
		t.Fatalf("packing=%+v", packed)
	}
	batch := packed.Plan.Batches[0]
	if batch.DesignTask != design.Tasks[0].ID {
		t.Fatal("request lost task identity")
	}
	for _, name := range []string{"store/read.go", "store/state.go", "service/service.go"} {
		if !slices.Contains(batch.Paths(), name) {
			t.Fatalf("same request lost %s", name)
		}
	}
	rendered := bundle.RenderBatch(batch)
	if !strings.Contains(rendered, "var state int") || !strings.Contains(rendered, design.Tasks[0].SourceDigest) {
		t.Fatal("source or task binding absent from rendered request")
	}
	if batch.Tokens != llms.DefaultTokenEstimator().EstimateTokens(rendered) {
		t.Fatal("budget omitted rendered task overhead")
	}
	if packed.Plan.BudgetPerBatch != cfg.Review.TokenBudgetPerRequest-100 {
		t.Fatal("framing reserve lost")
	}
}

func TestDesignPackingDoesNotPromoteLimitedOrMissingContext(t *testing.T) {
	files, inventory := designPlanningFixture()
	design := PlanDesign(t.Context(), inventory, files, []string{"store/read.go"})
	for _, cause := range []string{"file count", "total files", "tokens", "bytes", "missing", "excluded", "changed source"} {
		t.Run(cause, func(t *testing.T) {
			cfg := config.Defaults()
			source := slices.Clone(files)
			switch cause {
			case "total files":
				cfg.Review.MaxFiles = 1
			case "file count":
				cfg.Review.MaxFilesPerRequest = 1
			case "tokens":
				cfg.Review.TokenBudgetPerRequest = 101
			case "bytes":
				cfg.Review.MaxFileBytes = 1
			case "missing":
				source = slices.Delete(source, 2, 3)
			case "changed source":
				source[2].Src = []byte("package store\nvar state any\n")
			case "excluded":
				cfg.Review.Ignore = append(cfg.Review.Ignore, "store/state.go")
			}
			packed := PackDesign(t.Context(), cfg, design, source, nil, bundle.Reserve{Tokens: 100})
			if len(packed.Plan.Batches) != 0 || len(packed.Design.Tasks) != 1 || len(packed.Design.Tasks[0].Omitted) == 0 {
				t.Fatalf("limited package claimed a request: %+v", packed)
			}
			if len(design.Tasks[0].Omitted) != 0 {
				t.Fatal("packing mutated intended input")
			}
		})
	}
}

func TestDesignPackingCountsRepeatedContextOnceAgainstFileLimit(t *testing.T) {
	files, inventory := designPlanningFixture()
	design := PlanDesign(t.Context(), inventory, files, []string{"store/read.go", "service/service.go"})
	cfg := config.Defaults()
	cfg.Review.MaxFiles = 4
	packed := PackDesign(t.Context(), cfg, design, files, nil, bundle.Reserve{})
	if len(packed.Plan.Batches) != 2 {
		t.Fatalf("repeated context consumed extra file slots: %+v", packed.Design.Tasks)
	}
	cfg.Review.MaxFiles = 3
	limited := PackDesign(t.Context(), cfg, design, files, nil, bundle.Reserve{})
	if len(limited.Plan.Batches) != 1 {
		t.Fatalf("whole tasks ignored total file bound: %+v", limited.Design.Tasks)
	}
}

func TestDesignPackingRejectsUnsetLimitsWithoutLosingTasks(t *testing.T) {
	files, inventory := designPlanningFixture()
	design := PlanDesign(t.Context(), inventory, files, []string{"store/read.go"})
	for _, cause := range []string{"nil", "files", "request files", "bytes", "tokens", "reserve"} {
		t.Run(cause, func(t *testing.T) {
			cfg := config.Defaults()
			reserve := bundle.Reserve{}
			switch cause {
			case "nil":
				cfg = nil
			case "files":
				cfg.Review.MaxFiles = 0
			case "request files":
				cfg.Review.MaxFilesPerRequest = 0
			case "bytes":
				cfg.Review.MaxFileBytes = 0
			case "tokens":
				cfg.Review.TokenBudgetPerRequest = 0
			case "reserve":
				reserve.Tokens = -1
			}
			packed := PackDesign(t.Context(), cfg, design, files, nil, reserve)
			if len(packed.Design.Errors) == 0 || len(packed.Design.Tasks) != 1 || len(packed.Plan.Batches) != 0 {
				t.Fatalf("invalid limits became a usable plan: %+v", packed)
			}
		})
	}
}

func TestDesignPackingKeepsCancellationDistinctFromChangedSource(t *testing.T) {
	files, inventory := designPlanningFixture()
	design := PlanDesign(t.Context(), inventory, files, []string{"store/read.go"})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	packed := PackDesign(ctx, config.Defaults(), design, files, nil, bundle.Reserve{})
	if len(packed.Plan.Batches) != 0 || len(packed.Design.Tasks) != 1 || len(packed.Design.Errors) == 0 {
		t.Fatalf("cancellation lost its cause or scope: %+v", packed)
	}
	for _, omission := range packed.Design.Tasks[0].Omitted {
		if strings.Contains(omission.Reason, "digest") {
			t.Fatalf("cancellation became changed source: %+v", omission)
		}
	}
}

type cancelAfterPoll struct {
	context.Context
	cancel    context.CancelFunc
	remaining int
}

func (c *cancelAfterPoll) Err() error {
	err := c.Context.Err()
	c.remaining--
	if c.remaining == 0 {
		c.cancel()
	}
	return err
}

func TestDesignPackingChecksCancellationAfterRendering(t *testing.T) {
	files, inventory := designPlanningFixture()
	design := PlanDesign(t.Context(), inventory, files, []string{"store/read.go"})
	base, cancel := context.WithCancel(t.Context())
	defer cancel()
	task := design.Tasks[0]
	// Cancel just after the last source poll returns nil, before it is rendered.
	ctx := &cancelAfterPoll{Context: base, cancel: cancel, remaining: 2 + 2*(len(task.Sources)+len(task.Context))}
	packed := PackDesign(ctx, config.Defaults(), design, files, nil, bundle.Reserve{})
	if base.Err() == nil {
		t.Fatal("control never cancelled")
	}
	if len(packed.Plan.Batches) != 0 || len(packed.Design.Errors) == 0 {
		t.Fatalf("late cancellation admitted a task: %+v", packed)
	}
	if len(packed.Plan.Skipped) != 1 || !strings.Contains(packed.Plan.Skipped[0].Reason, context.Canceled.Error()) {
		t.Fatalf("skip lost cancellation cause: %+v", packed.Plan.Skipped)
	}
}
