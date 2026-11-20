package evals

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/llm"
)

func TestCompositeFixtureReviewsRetainTheirRawResponses(t *testing.T) {
	t.Setenv("NITPICK_TEST_CAPTURE_KEY", "test")
	const content = `{"walkthrough":"bounded fixture review","findings":[]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": content}, "finish_reason": "stop"}},
			"usage":   map[string]int{"prompt_tokens": 2, "completion_tokens": 1, "total_tokens": 3},
		})
	}))
	defer server.Close()
	route := filepath.Join(t.TempDir(), "routes.yaml")
	if err := os.WriteFile(route, []byte("models:\n  default:\n    provider: openai\n    model: gpt-4o\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	zero := 0
	spec := config.ModelSpec{Provider: "openai", Model: "gpt-4o", BaseURL: server.URL,
		APIKeyEnv: "NITPICK_TEST_CAPTURE_KEY", AllowPrivateEndpoint: true, MaxRetries: &zero}
	result := Run(t.Context(), Model{ID: "capture", RouteFile: route}, Fixtures()[0], 1, Options{
		CaptureDir: t.TempDir(),
		Tune: func(cfg *config.Config) {
			cfg.Models.Default = spec
			cfg.Models.Review, cfg.Models.Triage, cfg.Models.Validate = &spec, &spec, &spec
		},
	})
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	if result.Usage.Calls() == 0 || result.Usage.Unreported != 0 {
		t.Fatalf("composite review bypassed its meter: %+v", result.Usage)
	}
	if len(result.Responses) == 0 || result.Responses[0] != content {
		t.Fatalf("composite review lost the response used as evidence: %q", result.Responses)
	}
	if len(result.Calls) == 0 || result.Calls[0].RequestID == "" || len(result.Calls[0].Messages) == 0 {
		t.Fatalf("request identity was not captured: %+v", result.Calls)
	}
	stages := map[string]bool{}
	for _, snapshot := range result.Snapshots {
		stages[snapshot.Stage] = true
	}
	for _, stage := range []string{"batch-generation", "generation", "triage-input", "triage", "validation", "verification", "publication-gate"} {
		if !stages[stage] {
			t.Errorf("silent review omitted stage %s: %+v", stage, result.Snapshots)
		}
	}
}

func TestInvalidRouteFileFailsBeforeAnyClientIsBuilt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing-route.yaml")
	builds := 0
	result := Run(t.Context(), Model{ID: "route-test", RouteFile: path}, Fixtures()[0], 1, Options{buildClient: func(config.ModelSpec) (*llm.Client, error) {
		builds++
		return nil, errors.New("client must not be built")
	}})
	if result.Err == nil || !strings.Contains(result.Err.Error(), path) || builds != 0 {
		t.Fatalf("invalid route: err %v, client builds %d", result.Err, builds)
	}
}
