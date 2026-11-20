package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
	llms "github.com/nocturnium/llm-go-sdk/v6"
)

func TestOpenRouterCompatibleTransportUsesLogicalPurchaseTariff(t *testing.T) {
	path := preparePurchaseBudget(t, 100_000)
	t.Setenv(EnvResponseCache, t.TempDir())
	t.Setenv("NITPICK_PURCHASE_TEST_KEY", "test-key")
	var requests atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		provider, _ := body["provider"].(map[string]any)
		prices, _ := provider["max_price"].(map[string]any)
		if prices["prompt"] != float64(1) || prices["completion"] != float64(1) {
			t.Errorf("routing price bound absent: %+v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"test","model":"budget-model","choices":[{"index":0,"message":{"role":"assistant","content":"answer"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":2,"total_tokens":4,"cost":0.000123}}`))
	}))
	defer srv.Close()
	client, err := Build(config.ModelSpec{Provider: ProviderOpenRouter, Model: "budget-model", BaseURL: srv.URL,
		APIKeyEnv: "NITPICK_PURCHASE_TEST_KEY", AllowPrivateEndpoint: true, MaxTokens: 10, Reasoning: config.ReasoningOff})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.LLM.GenerateContent(context.Background(),
		[]llms.Message{{Role: llms.RoleUser, Content: "request"}}, client.CallOptions()...)
	if err != nil || requests.Load() != 1 {
		t.Fatalf("logical provider tariff was not used: requests=%d err=%v", requests.Load(), err)
	}
	budget, err := loadPurchaseBudget(path)
	if err != nil || budget.Halted != "" || len(budget.Entries) != 1 {
		t.Fatalf("OpenRouter charge was not available for settlement: %+v err=%v", budget, err)
	}
	for _, entry := range budget.Entries {
		if entry.Settled == nil {
			t.Fatal("authoritative usage cost was lost by compatible transport")
		}
	}
}

type purchaseModel struct {
	calls  atomic.Int64
	cost   *float64
	fail   bool
	finish llms.FinishReason
	err    error
}

func (m *purchaseModel) Provider() llms.Provider { return llms.Provider(ProviderOpenRouter) }
func (m *purchaseModel) Model() string           { return "budget-model" }
func (m *purchaseModel) GenerateContent(context.Context, []llms.Message, ...llms.CallOption) (*llms.Response, error) {
	m.calls.Add(1)
	if m.fail {
		return nil, errors.New("connection lost")
	}
	finish := m.finish
	if finish == "" {
		finish = llms.FinishReasonStop
	}
	return &llms.Response{Content: "answer", FinishReason: finish, Usage: llms.Usage{Cost: m.cost}}, m.err
}
func (m *purchaseModel) Stream(context.Context, []llms.Message, ...llms.CallOption) (<-chan llms.StreamChunk, error) {
	m.calls.Add(1)
	return nil, errors.New("unexpected stream")
}

func preparePurchaseBudget(t *testing.T, limit int64) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "budget.json")
	budget := PurchaseBudget{Version: 1, Limit: limit, Tariffs: map[string]PurchaseTariff{
		"openrouter/budget-model": {Input: 1_000_000, Output: 1_000_000},
	}, Entries: map[string]PurchaseEntry{}}
	if err := writePurchaseBudget(path, budget); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvPurchaseBudget, path)
	return path
}

func TestPurchaseBudgetBoundsConcurrentDistinctRequestsAcrossClients(t *testing.T) {
	path := preparePurchaseBudget(t, 6_000)
	model := &purchaseModel{}
	var wg sync.WaitGroup
	var accepted atomic.Int64
	for i := range 8 {
		wg.Go(func() {
			client := newReplayLLM(model, t.TempDir())
			_, err := client.GenerateContent(context.Background(),
				[]llms.Message{{Role: llms.RoleUser, Content: string(rune('a' + i))}}, llms.WithMaxTokens(10))
			if err == nil {
				accepted.Add(1)
			}
		})
	}
	wg.Wait()
	if accepted.Load() != 1 || model.calls.Load() != 1 {
		t.Fatalf("cap did not bound concurrent purchases: accepted=%d calls=%d", accepted.Load(), model.calls.Load())
	}
	budget, err := loadPurchaseBudget(path)
	if err != nil || len(budget.Entries) != 1 {
		t.Fatalf("reservation was not durable: %+v %v", budget, err)
	}
	restarted := newReplayLLM(model, t.TempDir())
	if _, err := restarted.GenerateContent(context.Background(), []llms.Message{{Role: llms.RoleUser, Content: "restart"}},
		llms.WithMaxTokens(10)); err == nil || model.calls.Load() != 1 {
		t.Fatalf("restart forgot outstanding reservation: %v, calls=%d", err, model.calls.Load())
	}
}

func TestPurchaseRefusesUnaffordableFirstProviderCall(t *testing.T) {
	preparePurchaseBudget(t, 1)
	model := &purchaseModel{}
	client := newReplayLLM(model, t.TempDir())
	_, err := client.GenerateContent(context.Background(), []llms.Message{{Role: llms.RoleUser, Content: "request"}},
		llms.WithMaxTokens(10))
	if err == nil || model.calls.Load() != 0 {
		t.Fatalf("unaffordable call reached provider: calls=%d err=%v", model.calls.Load(), err)
	}
}

func TestTruncatedPurchaseSettlesKnownChargeWithoutAutomaticRetry(t *testing.T) {
	path := preparePurchaseBudget(t, 1_000_000)
	cost := 0.000123
	model := &purchaseModel{cost: &cost, finish: llms.FinishReasonLength}
	client := newReplayLLM(model, t.TempDir())
	msgs := []llms.Message{{Role: llms.RoleUser, Content: "request"}}
	for range 2 {
		_, err := client.GenerateContent(context.Background(), msgs, llms.WithMaxTokens(10))
		if !errors.Is(err, ErrRecordedAttempt) {
			t.Fatalf("truncation permitted automatic repurchase: %v", err)
		}
	}
	budget, err := loadPurchaseBudget(path)
	if err != nil || budget.Halted != "" || len(budget.Entries) != 1 || model.calls.Load() != 1 {
		t.Fatalf("known charge remained ambiguous: %+v calls=%d err=%v", budget, model.calls.Load(), err)
	}
	for _, entry := range budget.Entries {
		if entry.Settled == nil {
			t.Fatal("truncation charge was not retained")
		}
	}
}

func TestPurchaseLedgerRefusesRepurchaseWhenBothCacheMarkersAreLost(t *testing.T) {
	path := preparePurchaseBudget(t, 1_000_000)
	cost := 0.000123
	model := &purchaseModel{cost: &cost}
	cache := t.TempDir()
	client := newReplayLLM(model, cache)
	msgs := []llms.Message{{Role: llms.RoleUser, Content: "request"}}
	if _, err := client.GenerateContent(context.Background(), msgs, llms.WithMaxTokens(10)); err != nil {
		t.Fatal(err)
	}
	budget, err := loadPurchaseBudget(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range budget.Entries {
		key := filepath.Base(entry.Request)
		for _, file := range []string{client.path(key), client.path(key) + ".pending"} {
			if err := os.Remove(file); err != nil {
				t.Fatal(err)
			}
		}
	}
	_, err = newReplayLLM(model, cache).GenerateContent(context.Background(), msgs, llms.WithMaxTokens(10))
	if !errors.Is(err, ErrRecordedAttempt) || model.calls.Load() != 1 {
		t.Fatalf("lost cache was charged again: calls=%d err=%v", model.calls.Load(), err)
	}
}

func TestPurchaseReplayCostsNothingAndSettlesOnlyKnownCharges(t *testing.T) {
	path := preparePurchaseBudget(t, 6_000)
	cost := 0.000123
	model := &purchaseModel{cost: &cost}
	cache := t.TempDir()
	client := newReplayLLM(model, cache)
	msgs := []llms.Message{{Role: llms.RoleUser, Content: "same request"}}
	for range 2 {
		if _, err := client.GenerateContent(context.Background(), msgs, llms.WithMaxTokens(10)); err != nil {
			t.Fatal(err)
		}
	}
	restarted := newReplayLLM(model, cache)
	if _, err := restarted.GenerateContent(context.Background(), msgs, llms.WithMaxTokens(10)); err != nil {
		t.Fatal(err)
	}
	budget, err := loadPurchaseBudget(path)
	if err != nil || len(budget.Entries) != 1 || model.calls.Load() != 1 {
		t.Fatalf("replay was bought again: %+v calls=%d err=%v", budget, model.calls.Load(), err)
	}
	for _, entry := range budget.Entries {
		if entry.Settled == nil || *entry.Settled != 124 && *entry.Settled != 123 {
			t.Fatalf("known charge not settled conservatively: %+v", entry)
		}
	}
}

func TestUnknownPurchaseRetainsReservationAndRefusesAutomaticRetry(t *testing.T) {
	path := preparePurchaseBudget(t, 1_000_000)
	model := &purchaseModel{fail: true}
	client := newReplayLLM(model, t.TempDir())
	msgs := []llms.Message{{Role: llms.RoleUser, Content: "request"}}
	for range 2 {
		_, _ = client.GenerateContent(context.Background(), msgs, llms.WithMaxTokens(10))
	}
	_, _ = client.GenerateContent(context.Background(), []llms.Message{{Role: llms.RoleUser, Content: "different"}},
		llms.WithMaxTokens(10))
	budget, err := loadPurchaseBudget(path)
	if err != nil || len(budget.Entries) != 1 || model.calls.Load() != 1 {
		t.Fatalf("ambiguous purchase repeated: %+v calls=%d err=%v", budget, model.calls.Load(), err)
	}
	for _, entry := range budget.Entries {
		if entry.Settled != nil {
			t.Fatalf("unknown charge was refunded: %+v", entry)
		}
	}
}

func TestOfflineReplayMissCannotDispatchAProviderCall(t *testing.T) {
	t.Setenv(EnvReplayOnly, "1")
	model := &purchaseModel{}
	client := newReplayLLM(model, t.TempDir())
	if _, err := client.GenerateContent(context.Background(), []llms.Message{{Role: llms.RoleUser, Content: "missing"}}); err == nil {
		t.Fatal("offline miss returned success")
	}
	if model.calls.Load() != 0 {
		t.Fatal("offline miss bought another response")
	}
}

func TestOfflineReplayStreamingCannotDispatchAProviderCall(t *testing.T) {
	t.Setenv(EnvPurchaseBudget, "")
	t.Setenv(EnvReplayOnly, "1")
	model := &purchaseModel{}
	client := newReplayLLM(model, t.TempDir())
	stream, err := client.Stream(context.Background(), nil)
	if stream != nil || err == nil || model.calls.Load() != 0 {
		t.Fatalf("offline streaming reached provider: stream=%v calls=%d err=%v", stream, model.calls.Load(), err)
	}
}

func TestFailedPaidResponseCannotBecomeASuccessfulReplay(t *testing.T) {
	path := preparePurchaseBudget(t, 1_000_000)
	cost := 0.000123
	providerErr := errors.New("response interrupted")
	model := &purchaseModel{cost: &cost, err: providerErr}
	cache := t.TempDir()
	msgs := []llms.Message{{Role: llms.RoleUser, Content: "request"}}
	client := newReplayLLM(model, cache)
	response, err := client.GenerateContent(context.Background(), msgs, llms.WithMaxTokens(10))
	if !errors.Is(err, providerErr) || !errors.Is(err, ErrRecordedAttempt) || response == nil {
		t.Fatalf("failed response lost outcome: response=%+v err=%v", response, err)
	}
	_, err = newReplayLLM(model, cache).GenerateContent(context.Background(), msgs, llms.WithMaxTokens(10))
	if !errors.Is(err, ErrRecordedAttempt) || model.calls.Load() != 1 {
		t.Fatalf("failed purchase replayed as success or repurchased: calls=%d err=%v", model.calls.Load(), err)
	}
	budget, err := loadPurchaseBudget(path)
	if err != nil || budget.Halted != "" || len(budget.Entries) != 1 {
		t.Fatalf("failed charge not reconciled: %+v err=%v", budget, err)
	}
	for _, entry := range budget.Entries {
		if entry.Settled == nil || *entry.Settled < 123 || *entry.Settled > 124 {
			t.Fatalf("known failed charge not settled: %+v", entry)
		}
		key := filepath.Base(entry.Request)
		data, err := os.ReadFile(client.path(key) + ".attempt")
		var attempt llms.Response
		if err != nil || json.Unmarshal(data, &attempt) != nil || attempt.Usage.Cost == nil || *attempt.Usage.Cost != cost {
			t.Fatalf("failed charge receipt was not retained: %q err=%v", data, err)
		}
	}
}

func TestPurchaseRejectsCorruptLedgerUnboundedOutputAndStreaming(t *testing.T) {
	path := preparePurchaseBudget(t, 6_000)
	model := &purchaseModel{}
	client := newReplayLLM(model, t.TempDir())
	if _, err := client.Stream(context.Background(), nil); err == nil {
		t.Fatal("stream bypassed cap")
	}
	if _, err := client.GenerateContent(context.Background(), nil); err == nil {
		t.Fatal("unbounded output was purchased")
	}
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := client.GenerateContent(context.Background(), nil, llms.WithMaxTokens(10)); err == nil {
		t.Fatal("corrupt ledger authorized purchase")
	}
	if model.calls.Load() != 0 {
		t.Fatal("rejected request reached provider")
	}
}

func TestPurchaseRoutingCapParticipatesInRequestIdentity(t *testing.T) {
	path := preparePurchaseBudget(t, 6_000)
	model := &purchaseModel{}
	opts, err := purchaseOptions(model.Provider(), model.Model(), []llms.CallOption{llms.WithMaxTokens(10)})
	if err != nil {
		t.Fatal(err)
	}
	first, ok := newReplayLLM(model, t.TempDir()).keyFor(context.Background(), nil, opts)
	if !ok {
		t.Fatal("request was not serializable")
	}
	budget, err := loadPurchaseBudget(path)
	if err != nil {
		t.Fatal(err)
	}
	budget.Tariffs["openrouter/budget-model"] = PurchaseTariff{Input: 500_000, Output: 500_000}
	data, err := json.Marshal(budget)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	opts, err = purchaseOptions(model.Provider(), model.Model(), []llms.CallOption{llms.WithMaxTokens(10)})
	if err != nil {
		t.Fatal(err)
	}
	second, ok := newReplayLLM(model, t.TempDir()).keyFor(context.Background(), nil, opts)
	if !ok || first == second {
		t.Fatal("routing cap change reused another purchase identity")
	}
}
