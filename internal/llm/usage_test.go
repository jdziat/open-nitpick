package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	llms "github.com/nocturnium/llm-go-sdk/v6"

	"github.com/jdziat/open-nitpick/internal/config"
)

func TestUsageRetainsFailedAttemptsBeforeSuccessfulRetries(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"private prompt text"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":5}}`))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("NITPICK_TEST_KEY", "not-a-real-key")
	retries := 1
	c, err := Build(config.ModelSpec{Provider: "openai", Model: "gpt-4o", BaseURL: srv.URL,
		APIKeyEnv: "NITPICK_TEST_KEY", MaxRetries: &retries, AllowPrivateEndpoint: true})
	if err != nil {
		t.Fatal(err)
	}
	var records []CallRecord
	c.ObserveCalls(func(record CallRecord) { records = append(records, record) })
	ctx := WithCallRole(t.Context(), "verification")
	if _, err := c.LLM.GenerateContent(ctx, []llms.Message{{Role: llms.RoleUser, Content: "private prompt text"}}); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 2 || len(records) != 2 {
		t.Fatalf("requests=%d records=%+v; want both provider attempts", requests.Load(), records)
	}
	if !records[0].Failed || records[0].Usage != nil || records[1].Failed || records[1].Usage == nil {
		t.Fatalf("failed and successful usage were conflated: %+v", records)
	}
	if records[1].Usage.PromptTokens != 12 || records[1].Usage.CompletionTokens != 5 {
		t.Fatalf("provider usage changed: %+v", records[1])
	}
	for _, record := range records {
		if record.Role != "verification" || record.Provider != "openai" || record.Model != "gpt-4o" || record.StartedAt.IsZero() || record.DurationMS < 0 {
			t.Fatalf("missing attempt provenance: %+v", record)
		}
	}
	raw, err := json.Marshal(records)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "private prompt text") || strings.Contains(string(raw), "not-a-real-key") {
		t.Fatalf("usage records contain provider or prompt text: %s", raw)
	}
}

func TestUsageWithoutPricedTokensRemainsUnknown(t *testing.T) {
	model := &usageLLM{LLM: newFakeLLM(), usage: llms.Usage{TotalTokens: 123}}
	c := NewClientForTest(model, config.ModelSpec{Provider: "fixture", Model: "shared"})
	var records []CallRecord
	c.ObserveCalls(func(record CallRecord) { records = append(records, record) })
	if _, err := c.LLM.GenerateContent(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Usage != nil || records[0].Role != "unclassified" || records[0].Failed {
		t.Fatalf("missing usage was reported as known or failed: %+v", records)
	}
}

type usageLLM struct {
	llms.LLM
	usage llms.Usage
}

func (m *usageLLM) GenerateContent(context.Context, []llms.Message, ...llms.CallOption) (*llms.Response, error) {
	return &llms.Response{Content: "ok", Usage: m.usage}, nil
}
