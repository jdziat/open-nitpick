package review

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	llms "github.com/nocturnium/llm-go-sdk/v6"

	"github.com/nitpick-oss/nitpick/internal/vcs"
)

// The engine shares one repoTools across concurrent batches. A failure counted on
// the tools themselves was charged to whichever batch was comparing the counter at
// that moment, so a lookup round that had succeeded was reported as failed and a
// valid cached generation was discarded.

func TestOneOperationsFailureIsNotAnotherOperations(t *testing.T) {
	tools := &repoTools{}
	_, turnA := withTurnFailures(context.Background())
	ctxB, turnB := withTurnFailures(context.Background())
	tools.fail(ctxB)
	if turnA.Load() != 0 {
		t.Fatalf("operation A was charged %d failure(s) that belong to operation B", turnA.Load())
	}
	if turnB.Load() != 1 {
		t.Fatalf("operation B lost its own failure: %d", turnB.Load())
	}
}

func TestAFailureWithoutAnOperationCounterIsSafe(t *testing.T) {
	(&repoTools{}).fail(context.Background())
}

// A neighbouring batch's lookup fails while this batch's own lookup succeeds.
func TestBoundedGenerationIgnoresANeighbouringBatchsLookupFailure(t *testing.T) {
	neighbour := make(chan struct{})
	release := make(chan struct{})
	provider := &boundedTestProvider{read: func(ctx context.Context, _ vcs.Ref, path string) ([]byte, error) {
		if path == "neighbour.go" {
			defer close(neighbour)
			return nil, errors.New("upstream unavailable")
		}
		<-release // hold our own lookup open until the neighbour has failed
		return []byte("package a\n"), nil
	}}
	model := &boundedTestModel{respond: func(turn int, _ []llms.Message, _ llms.CallOptions) (*llms.Response, error) {
		if turn == 1 {
			return &llms.Response{ToolCalls: []llms.ToolCall{boundedCall("read", "read_file", `{"path":"own.go"}`)}}, nil
		}
		return &llms.Response{ToolCalls: []llms.ToolCall{boundedCall("submit", "submit_findings", `{"findings":[]}`)}}, nil
	}}
	engine, client, schema, tools := boundedTestEngine(t, model, provider)

	go func() {
		// The neighbouring batch uses the same shared tools and its own counter.
		ctx, _ := withTurnFailures(t.Context())
		tools.read(ctx, "neighbour.go", 1, 1)
		<-neighbour
		close(release)
	}()
	_, _, err := engine.boundedGeneration(t.Context(), client, nil, schema, tools)
	if err != nil {
		t.Fatalf("a neighbouring batch's failed lookup failed this batch's successful round: %v", err)
	}
}

func TestReuseGenerationIgnoresANeighbouringBatchsLookupFailure(t *testing.T) {
	root := gitRepo(t, map[string]string{"sibling.go": "package a\n"})
	e, _ := verifyEngine(t, &toolLLM{}, "")
	e.lookups = newRepoTools(root, nil, vcs.Ref{})
	args := map[string]any{"path": "sibling.go", "start_line": 1, "end_line": 1}
	answer, ok := runLookup(t.Context(), e.lookups, "read_file", args)
	if !ok {
		t.Fatal("fixture lookup was not answered")
	}
	data, err := json.Marshal(storedGeneration{Complete: true, Lookups: []verdictLookup{{
		Tool: "read_file", Arguments: `{"path":"sibling.go","start_line":1,"end_line":1}`, Digest: boundedLookupDigest(answer),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	e.progress = &reviewProgress{prior: map[string]json.RawMessage{"key": data}, current: map[string]json.RawMessage{}, rank: map[string]int{}}
	e.lookups = newRepoTools(root, nil, vcs.Ref{})

	// A neighbouring batch fails a lookup on the same shared tools.
	ctx, _ := withTurnFailures(t.Context())
	e.lookups.fail(ctx)

	if _, reused := e.reuseGeneration(t.Context(), "key"); !reused {
		t.Fatal("a neighbouring batch's failure discarded a valid cached generation")
	}
}
