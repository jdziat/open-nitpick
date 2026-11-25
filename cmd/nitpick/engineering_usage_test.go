package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nitpick-oss/nitpick/internal/config"
	"github.com/nitpick-oss/nitpick/internal/llm"
	llms "github.com/nocturnium/llm-go-sdk/v6"
)

type practiceUsageModel struct{ usage llms.Usage }

func (m *practiceUsageModel) GenerateContent(context.Context, []llms.Message, ...llms.CallOption) (*llms.Response, error) {
	return &llms.Response{Content: strings.Repeat("response ", 1000), Usage: m.usage}, nil
}

func TestDynamicallyBuiltFallbackClientsRetainTheirAttemptUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":2}}`))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("NITPICK_TEST_KEY", "fixture")
	retries := 0
	fallback := config.ModelSpec{Provider: "openai", Model: "fallback", APIKeyEnv: "NITPICK_TEST_KEY", BaseURL: srv.URL, AllowPrivateEndpoint: true, MaxRetries: &retries}
	spec := fallback
	spec.Model = "route"
	spec.Fallback = &fallback
	roles := &llm.Roles{Build: llm.Build}
	var usage engineeringUsage
	usage.reset()
	usage.attach(roles)
	client, err := roles.For(spec)
	if err != nil {
		t.Fatal(err)
	}
	if client.Fallback() == nil {
		t.Fatal("fixture did not build a fallback")
	}
	if _, err := client.Fallback().LLM.GenerateContent(llm.WithCallRole(t.Context(), "review"), []llms.Message{{Role: llms.RoleUser, Content: "probe"}}); err != nil {
		t.Fatal(err)
	}
	receipts := usage.calls()
	if len(receipts) != 1 || receipts[0].Model != "fallback" || receipts[0].Usage == nil || receipts[0].Usage.PromptTokens != 7 {
		t.Fatalf("fallback usage was lost: %+v", receipts)
	}
}

func (*practiceUsageModel) Stream(context.Context, []llms.Message, ...llms.CallOption) (<-chan llms.StreamChunk, error) {
	return nil, errors.New("fixture does not stream")
}

func (*practiceUsageModel) Provider() llms.Provider { return llms.Provider("fixture") }
func (*practiceUsageModel) Model() string           { return "fixture" }

func TestEngineeringUsageMetersDistinctAndRoutedClientsWithoutEstimating(t *testing.T) {
	makeClient := func(name string, usage llms.Usage) *llm.Client {
		return &llm.Client{Spec: config.ModelSpec{Provider: "fixture", Model: name}, LLM: &practiceUsageModel{usage: usage}}
	}
	primary := makeClient("shared", llms.Usage{PromptTokens: 10, CompletionTokens: 3})
	validator := makeClient("shared", llms.Usage{})
	routed := makeClient("routed", llms.Usage{PromptTokens: 4, CompletionTokens: 2})
	roles := &llm.Roles{Review: primary, Triage: primary, Validate: validator, Build: func(config.ModelSpec) (*llm.Client, error) { return routed, nil }}
	var usage engineeringUsage
	usage.reset()
	usage.attach(roles)
	extra, err := roles.For(routed.Spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, client := range []*llm.Client{primary, validator, extra} {
		if _, err := client.LLM.GenerateContent(t.Context(), nil); err != nil {
			t.Fatal(err)
		}
	}
	got := usage.snapshot()
	if len(got) != 2 || got[0].Model != "fixture/routed" || got[0].PromptTokens != 4 || got[0].ReportedCalls != 1 || got[1].PromptTokens != 10 || got[1].CompletionTokens != 3 || got[1].ReportedCalls != 1 || got[1].UnreportedCalls != 1 {
		t.Fatalf("usage lost a client, doubled a shared role, or estimated missing tokens: %+v", got)
	}
	usage.reset()
	if len(usage.snapshot()) != 0 {
		t.Fatal("a later run inherited earlier usage")
	}
}
