package llm

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	llms "github.com/nocturnium/llm-go-sdk/v6"
)

type countingLLM struct {
	calls  atomic.Int64
	resp   llms.Response
	err    error
	gate   chan struct{}
	failed atomic.Int64
}

func (c *countingLLM) Provider() llms.Provider { return "test" }
func (c *countingLLM) Model() string           { return "m" }
func (c *countingLLM) Stream(context.Context, []llms.Message, ...llms.CallOption) (<-chan llms.StreamChunk, error) {
	return nil, errors.New("no stream")
}
func (c *countingLLM) GenerateContent(context.Context, []llms.Message, ...llms.CallOption) (*llms.Response, error) {
	c.calls.Add(1)
	if c.gate != nil {
		<-c.gate
	}
	if c.err != nil {
		c.failed.Add(1)
		return nil, c.err
	}
	resp := c.resp
	return &resp, nil
}

func ask(t *testing.T, r *replayLLM, prompt string, opts ...llms.CallOption) (*llms.Response, error) {
	t.Helper()
	return r.GenerateContent(context.Background(), []llms.Message{{Role: llms.RoleUser, Content: prompt}}, opts...)
}

func TestReplayCacheAnswersARepeatedRequestWithoutAnotherPurchase(t *testing.T) {
	inner := &countingLLM{resp: llms.Response{Content: "answer", FinishReason: llms.FinishReasonStop}}
	dir := t.TempDir()
	first := newReplayLLM(inner, dir)
	if _, err := ask(t, first, "review this"); err != nil {
		t.Fatal(err)
	}
	// A second process, as a new build would be, reads what the first paid for.
	second := newReplayLLM(inner, dir)
	got, err := ask(t, second, "review this")
	if err != nil || got.Content != "answer" {
		t.Fatalf("got %+v err %v", got, err)
	}
	if inner.calls.Load() != 1 {
		t.Fatalf("provider was called %d times, want 1", inner.calls.Load())
	}
	if hits := second.hits.Load(); hits != 1 {
		t.Fatalf("second run reported %d hits, want 1", hits)
	}
}

func TestReplayCacheKeepsDifferentRequestsApart(t *testing.T) {
	inner := &countingLLM{resp: llms.Response{Content: "answer", FinishReason: llms.FinishReasonStop}}
	r := newReplayLLM(inner, t.TempDir())
	_, _ = ask(t, r, "one")
	_, _ = ask(t, r, "two")
	_, _ = ask(t, r, "one", llms.WithMaxTokens(10))
	if inner.calls.Load() != 3 {
		t.Fatalf("distinct prompts or options shared an answer: %d calls", inner.calls.Load())
	}
}

func TestReplayCacheBlocksRepeatedUnfinishedPurchases(t *testing.T) {
	for name, inner := range map[string]*countingLLM{
		"error":     {err: errors.New("503")},
		"truncated": {resp: llms.Response{Content: "half", FinishReason: llms.FinishReasonLength}},
		"empty":     {resp: llms.Response{FinishReason: llms.FinishReasonStop}},
	} {
		t.Run(name, func(t *testing.T) {
			r := newReplayLLM(inner, t.TempDir())
			_, _ = ask(t, r, "same")
			_, err := ask(t, r, "same")
			if !errors.Is(err, ErrRecordedAttempt) || inner.calls.Load() != 1 {
				t.Fatalf("%s repeated purchase: err %v, calls %d", name, err, inner.calls.Load())
			}
		})
	}
}

func TestReplayCacheRequiresAnIdentityForSampledRequests(t *testing.T) {
	inner := &countingLLM{resp: llms.Response{Content: "a", FinishReason: llms.FinishReasonStop}}
	r := newReplayLLM(inner, t.TempDir())
	_, _ = ask(t, r, "x", llms.WithTemperature(0.7))
	_, _ = ask(t, r, "x", llms.WithTemperature(0.7))
	if inner.calls.Load() != 0 {
		t.Fatalf("unnamed sampled requests made %d purchases", inner.calls.Load())
	}
	_, _ = ask(t, r, "x", llms.WithTemperature(0))
	_, _ = ask(t, r, "x", llms.WithTemperature(0))
	if inner.calls.Load() != 1 {
		t.Fatalf("a zero-temperature request was not cached: %d calls", inner.calls.Load())
	}
}

func TestReplayCacheSharesOnePurchaseBetweenConcurrentWorkers(t *testing.T) {
	inner := &countingLLM{resp: llms.Response{Content: "a", FinishReason: llms.FinishReasonStop}, gate: make(chan struct{})}
	r := newReplayLLM(inner, t.TempDir())
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := ask(t, r, "same"); err != nil {
				t.Error(err)
			}
		}()
	}
	deadline := time.After(time.Second)
	for inner.calls.Load() == 0 {
		select {
		case <-deadline:
			close(inner.gate)
			t.Fatal("provider did not start")
		case <-time.After(time.Millisecond):
		}
	}
	close(inner.gate)
	wg.Wait()
	if inner.calls.Load() != 1 {
		t.Fatalf("eight concurrent identical requests made %d purchases, want 1", inner.calls.Load())
	}
}

func TestReplayCacheDoesNotRepurchaseACorruptEntry(t *testing.T) {
	inner := &countingLLM{resp: llms.Response{Content: "a", FinishReason: llms.FinishReasonStop}}
	dir := t.TempDir()
	r := newReplayLLM(inner, dir)
	key, _ := r.key([]llms.Message{{Role: llms.RoleUser, Content: "x"}}, nil)
	if err := r.write(key, &llms.Response{Content: "a", FinishReason: llms.FinishReasonStop}); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(r.path(key), "{not json"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(r.path(key)+".pending", []byte("already paid"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ask(t, r, "x")
	if !errors.Is(err, ErrRecordedAttempt) || got != nil || inner.calls.Load() != 0 {
		t.Fatalf("corrupt paid entry must not trigger a purchase: %+v %v calls=%d", got, err, inner.calls.Load())
	}
}

func writeFile(path, content string) error { return os.WriteFile(path, []byte(content), 0o600) }
