package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	llms "github.com/nocturnium/llm-go-sdk/v6"

	"github.com/jdziat/open-nitpick/internal/config"
)

// A rate-limited request that succeeds on the next attempt must not read like
// one that succeeded at once. The SDK's retry loop writes nowhere on its own,
// so without the callback a review that spent minutes waiting reports only its
// total.
//
// The endpoint is local and answers 429 then 200, so this needs no credential
// and no provider.
func TestARetriedRequestSaysSo(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"slow down, and here is the prompt you sent"}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "1",
			"object":  "chat.completion",
			"choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": "ok"}, "finish_reason": "stop"}},
		})
	}))
	defer srv.Close()

	// A value, not a real key: the local endpoint never checks it and the
	// client refuses to build without one.
	t.Setenv("NITPICK_TEST_KEY", "not-a-real-key")

	retries := 1
	c, err := Build(config.ModelSpec{
		Provider:             "openai",
		Model:                "gpt-4o",
		BaseURL:              srv.URL,
		APIKeyEnv:            "NITPICK_TEST_KEY",
		MaxRetries:           &retries,
		AllowPrivateEndpoint: true,
	})
	if err != nil {
		t.Fatalf("Build against a local endpoint: %v", err)
	}

	var out bytes.Buffer
	c.Log = slog.New(slog.NewTextHandler(&out, &slog.HandlerOptions{Level: slog.LevelWarn}))
	c.retries.set(c.Log)

	if _, err := c.LLM.GenerateContent(context.Background(),
		[]llms.Message{{Role: llms.RoleUser, Content: "hello"}}); err != nil {
		t.Fatalf("GenerateContent after one 429: %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("requests = %d, want 2 (one refused, one answered)", got)
	}

	logged := out.String()
	if !strings.Contains(logged, "provider retried the request") {
		t.Fatalf("the retry was not reported:\n%s", logged)
	}
	for _, want := range []string{"attempt=1", "kind=rate-limited", "status=429"} {
		if !strings.Contains(logged, want) {
			t.Errorf("retry line does not carry %q:\n%s", want, logged)
		}
	}
	// The provider's own message is its choosing and can echo the request, so
	// none of it belongs in a line someone reads to find out what is happening.
	if strings.Contains(logged, "the prompt you sent") {
		t.Errorf("the provider's message reached the log:\n%s", logged)
	}
}
