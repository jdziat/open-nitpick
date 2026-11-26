package llm

import (
	"context"
	"errors"
	"sync"
	"testing"

	llms "github.com/nocturnium/llm-go-sdk/v6"

	"github.com/nitpick-oss/nitpick/internal/config"
)

type memLedger struct {
	mu      sync.Mutex
	answers map[string]*llms.Response
	lookups int
}

func newMemLedger() *memLedger { return &memLedger{answers: map[string]*llms.Response{}} }

func (m *memLedger) Lookup(_ context.Context, key string) (*llms.Response, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lookups++
	r, ok := m.answers[key]
	return r, ok
}

func (m *memLedger) Record(_ context.Context, key string, r *llms.Response) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.answers[key] = r
}

type ledgerModel struct {
	mu    sync.Mutex
	calls int
	reply *llms.Response
	err   error
}

func (c *ledgerModel) GenerateContent(context.Context, []llms.Message, ...llms.CallOption) (*llms.Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	return c.reply, c.err
}
func (c *ledgerModel) Stream(context.Context, []llms.Message, ...llms.CallOption) (<-chan llms.StreamChunk, error) {
	return nil, errors.New("no")
}
func (c *ledgerModel) Provider() llms.Provider { return "counting" }
func (c *ledgerModel) Model() string           { return "counting" }

func withLedger(reply *llms.Response, l Ledger) (*Client, *ledgerModel) {
	inner := &ledgerModel{reply: reply}
	c := NewClientForTest(inner, config.ModelSpec{Provider: "openai", Model: "m"})
	c.UseLedger(l)
	return c, inner
}

var question = []llms.Message{{Role: llms.RoleUser, Content: "q"}}

func TestARecordedAnswerIsServedWithoutReachingTheModel(t *testing.T) {
	l := newMemLedger()
	c, inner := withLedger(&llms.Response{Content: `{"a":1}`, FinishReason: llms.FinishReasonStop}, l)
	for range 3 {
		resp, err := c.LLM.GenerateContent(context.Background(), question)
		if err != nil || resp.Content != `{"a":1}` {
			t.Fatalf("resp=%v err=%v", resp, err)
		}
	}
	if inner.calls != 1 {
		t.Errorf("the model was reached %d times for one question, want 1", inner.calls)
	}
}

func TestADifferentQuestionIsNotAnsweredFromAnotherQuestionsRecord(t *testing.T) {
	l := newMemLedger()
	c, inner := withLedger(&llms.Response{Content: "x", FinishReason: llms.FinishReasonStop}, l)
	_, _ = c.LLM.GenerateContent(context.Background(), question)
	_, _ = c.LLM.GenerateContent(context.Background(), []llms.Message{{Role: llms.RoleUser, Content: "other"}})
	if inner.calls != 2 {
		t.Errorf("calls = %d, want 2", inner.calls)
	}
}

func TestOnlyAFinishedAnswerIsKept(t *testing.T) {
	for name, resp := range map[string]*llms.Response{
		"truncated":  {Content: "partial", FinishReason: llms.FinishReasonLength},
		"empty":      {Content: "  ", FinishReason: llms.FinishReasonStop},
		"no reason":  nil,
		"not stated": {Content: "ok"},
	} {
		t.Run(name, func(t *testing.T) {
			l := newMemLedger()
			c, _ := withLedger(resp, l)
			_, _ = c.LLM.GenerateContent(context.Background(), question)
			kept := len(l.answers) == 1
			wantKept := name == "not stated"
			if kept != wantKept {
				t.Errorf("kept=%v want %v", kept, wantKept)
			}
		})
	}
}

func TestAJSONRequestIsNotRecordedWhenTheAnswerIsNotJSON(t *testing.T) {
	l := newMemLedger()
	c, _ := withLedger(&llms.Response{Content: "I could not do that.", FinishReason: llms.FinishReasonStop}, l)
	_, _ = c.LLM.GenerateContent(context.Background(), question, llms.WithJSONMode())
	if len(l.answers) != 0 {
		t.Fatal("a reply that is not JSON was kept, and would be replayed as the answer forever")
	}
	fenced, _ := withLedger(&llms.Response{Content: "```json\n{\"a\":1}\n```", FinishReason: llms.FinishReasonStop}, l)
	_, _ = fenced.LLM.GenerateContent(context.Background(), question, llms.WithJSONMode())
	if len(l.answers) != 1 {
		t.Error("a fenced JSON answer is still an answer")
	}
}

func TestAFailedCallIsNotRecorded(t *testing.T) {
	l := newMemLedger()
	c, inner := withLedger(&llms.Response{Content: "x", FinishReason: llms.FinishReasonStop}, l)
	inner.err = errors.New("reset")
	if _, err := c.LLM.GenerateContent(context.Background(), question); err == nil {
		t.Fatal("the error was swallowed")
	}
	if len(l.answers) != 0 {
		t.Error("an error left an answer behind")
	}
}

func TestTheReasoningLevelDoesNotChangeWhichRecordedAnswerIsFound(t *testing.T) {
	l := newMemLedger()
	c, inner := withLedger(&llms.Response{Content: "x", FinishReason: llms.FinishReasonStop}, l)
	_, _ = c.LLM.GenerateContent(context.Background(), question, llms.WithReasoningEffort("low"))
	_, _ = c.LLM.GenerateContent(context.Background(), question, llms.WithReasoningEffort("high"))
	if inner.calls != 1 {
		t.Errorf("a different reasoning level asked the model again: %d calls", inner.calls)
	}
}

func TestDetachingTheLedgerSendsEveryRequestToTheModel(t *testing.T) {
	l := newMemLedger()
	c, inner := withLedger(&llms.Response{Content: "x", FinishReason: llms.FinishReasonStop}, l)
	_, _ = c.LLM.GenerateContent(context.Background(), question)
	c.UseLedger(nil)
	_, _ = c.LLM.GenerateContent(context.Background(), question)
	if inner.calls != 2 {
		t.Errorf("a detached ledger still answered: %d calls", inner.calls)
	}
}

func TestUsingALedgerTwiceSwapsItInsteadOfWrappingTheClientTwice(t *testing.T) {
	c, _ := withLedger(&llms.Response{Content: "x", FinishReason: llms.FinishReasonStop}, newMemLedger())
	first := c.LLM
	c.UseLedger(newMemLedger())
	if c.LLM != first {
		t.Error("a second UseLedger stacked another wrapper")
	}
}

func TestAClientBuiltForARouteAfterTheLedgerIsSetIsRecordedToo(t *testing.T) {
	l := newMemLedger()
	inner := &ledgerModel{reply: &llms.Response{Content: "x", FinishReason: llms.FinishReasonStop}}
	roles := &Roles{Build: func(spec config.ModelSpec) (*Client, error) { return NewClientForTest(inner, spec), nil }}
	roles.UseLedger(l)
	c, err := roles.For(config.ModelSpec{Provider: "openai", Model: "routed"})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = c.LLM.GenerateContent(context.Background(), question)
	_, _ = c.LLM.GenerateContent(context.Background(), question)
	if inner.calls != 1 {
		t.Errorf("a routed model bypassed the ledger: %d calls", inner.calls)
	}
}

func TestToolChatIsAnsweredFromTheLedgerToo(t *testing.T) {
	l := newMemLedger()
	c, inner := withLedger(&llms.Response{Content: `{"a":1}`, FinishReason: llms.FinishReasonStop}, l)
	// A production client routes ToolChat past the wrapper the hook sits in.
	c.observed = &observedLLM{inner: inner, model: "m"}
	for range 2 {
		if _, err := c.ToolChat(context.Background(), question); err != nil {
			t.Fatal(err)
		}
	}
	if inner.calls != 1 {
		t.Errorf("a repeated agent turn reached the model %d times, want 1", inner.calls)
	}
}

func TestAFallbackClientIsRecordedThroughTheSameLedger(t *testing.T) {
	l := newMemLedger()
	primary, _ := withLedger(&llms.Response{Content: "p", FinishReason: llms.FinishReasonStop}, nil)
	fallbackModel := &ledgerModel{reply: &llms.Response{Content: "f", FinishReason: llms.FinishReasonStop}}
	fallback := NewClientForTest(fallbackModel, config.ModelSpec{Provider: "openai", Model: "fb"})
	primary.fallback = fallback
	roles := &Roles{Review: primary}
	roles.UseLedger(l)
	for range 2 {
		_, _ = fallback.LLM.GenerateContent(context.Background(), question)
	}
	if fallbackModel.calls != 1 {
		t.Errorf("the fallback was reached %d times, so its answers were not recorded", fallbackModel.calls)
	}
}

func TestADegenerateJSONAnswerIsNotRecorded(t *testing.T) {
	for _, body := range []string{"{}", "null", "[]", `"text"`, "3"} {
		l := newMemLedger()
		c, _ := withLedger(&llms.Response{Content: body, FinishReason: llms.FinishReasonStop}, l)
		_, _ = c.LLM.GenerateContent(context.Background(), question, llms.WithJSONMode())
		if len(l.answers) != 0 {
			t.Errorf("%s was recorded, and the decoder rejects it on every replay", body)
		}
	}
}
