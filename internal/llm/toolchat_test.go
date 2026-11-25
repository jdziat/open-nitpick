package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/nitpick-oss/nitpick/internal/config"
	llms "github.com/nocturnium/llm-go-sdk/v6"
)

func TestToolChatBypassesRetriesAndRetainsCaptureAndMetering(t *testing.T) {
	var requests atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	t.Setenv("NITPICK_TOOLCHAT_TEST_KEY", "test-key")
	retries := 2
	client, err := Build(config.ModelSpec{Provider: ProviderOpenRouter, Model: "test", BaseURL: srv.URL,
		APIKeyEnv: "NITPICK_TOOLCHAT_TEST_KEY", AllowPrivateEndpoint: true, MaxRetries: &retries})
	if err != nil {
		t.Fatal(err)
	}
	var metered, captured int
	client.ObserveCalls(func(record CallRecord) {
		metered++
		if !record.Failed || record.Role != "review" {
			t.Errorf("failed request lost its stage: %+v", record)
		}
	})
	ctx := WithCallRole(context.Background(), "review")
	ctx = WithToolChatObserver(ctx, func(_ context.Context, observation ToolChatObservation) error {
		captured++
		if observation.Err == nil || observation.Client != client || len(observation.Messages) != 1 {
			t.Errorf("failed request capture was incomplete: %+v", observation)
		}
		return nil
	})
	_, err = client.ToolChat(ctx, []llms.Message{{Role: llms.RoleUser, Content: "request"}}, client.CallOptions()...)
	if err == nil || requests.Load() != 1 || metered != 1 || captured != 1 {
		t.Fatalf("bounded call retried or disappeared: requests=%d metered=%d captured=%d err=%v",
			requests.Load(), metered, captured, err)
	}
}

func TestBoundedExperimentDoesNotRepairOrEnlargeAChargedTruncation(t *testing.T) {
	preparePurchaseBudget(t, 1_000_000)
	model := &purchaseModel{finish: llms.FinishReasonLength}
	client := NewClientForTest(model, config.ModelSpec{Provider: ProviderOpenRouter, Model: "budget-model"})
	_, err := Extract[struct{ Value string }](t.Context(), client,
		[]llms.Message{{Role: llms.RoleUser, Content: "request"}}, llms.WithMaxTokens(10))
	if err == nil || model.calls.Load() != 1 {
		t.Fatalf("charged truncation was bought again: calls=%d err=%v", model.calls.Load(), err)
	}
}
