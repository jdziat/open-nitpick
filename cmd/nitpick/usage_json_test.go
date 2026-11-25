package main

import (
	"context"
	"testing"

	llms "github.com/nocturnium/llm-go-sdk/v6"

	"github.com/nitpick-oss/nitpick/internal/config"
	"github.com/nitpick-oss/nitpick/internal/llm"
)

func TestUsageDistinguishesNoCallsFromUnavailableMeter(t *testing.T) {
	missing := usageForJSON(nil)
	empty := usageForJSON([]llm.CallRecord{})
	if missing.Metered || missing.TokensComplete || missing.ReportedCostUSD != nil || missing.Reason != "meter unavailable" {
		t.Fatalf("unavailable meter looked complete: %+v", missing)
	}
	if !empty.Metered || !empty.TokensComplete || empty.ReportedCostUSD == nil || *empty.ReportedCostUSD != 0 || empty.Reason != "no model calls" {
		t.Fatalf("empty meter lost the reason for zero: %+v", empty)
	}
}

func TestUsageDoesNotPriceAttemptsWithMissingUsage(t *testing.T) {
	cost := 0.25
	u := usageForJSON([]llm.CallRecord{
		{Role: "deep_agent", Failed: true},
		{Role: "deep_forced_submission", Usage: &llms.Usage{PromptTokens: 12, CompletionTokens: 5, Cost: &cost}},
	})
	if !u.Metered || u.TokensComplete || u.UnknownUsage != 1 || u.ReportedCostUSD != nil || len(u.Attempts) != 2 {
		t.Fatalf("missing attempt was priced as free: %+v", u)
	}
	u = usageForJSON([]llm.CallRecord{{Failed: true, Usage: &llms.Usage{PromptTokens: 1, Cost: &cost}}})
	if !u.TokensComplete || u.ReportedCostUSD == nil || *u.ReportedCostUSD != cost {
		t.Fatalf("reported usage on a failed attempt disappeared: %+v", u)
	}
}

func TestSharedModelsRetainTheirSeparateCallRoles(t *testing.T) {
	a := llm.NewClientForTest(&usageProbeLLM{}, config.ModelSpec{Provider: "fixture", Model: "shared"})
	b := llm.NewClientForTest(&usageProbeLLM{}, config.ModelSpec{Provider: "fixture", Model: "shared"})
	roles := &llm.Roles{Review: a, Triage: a, Validate: b}
	var usage engineeringUsage
	usage.attach(roles)
	usage.attach(roles)
	for _, call := range []struct {
		client *llm.Client
		role   string
	}{{a, "review"}, {a, "triage"}, {b, "verification"}} {
		if _, err := call.client.LLM.GenerateContent(llm.WithCallRole(t.Context(), call.role), nil); err != nil {
			t.Fatal(err)
		}
	}
	calls := usage.calls()
	if len(calls) != 3 {
		t.Fatalf("shared role clients were counted twice: %+v", calls)
	}
	seen := map[string]int{}
	for _, call := range calls {
		seen[call.Role]++
		if call.Usage == nil || call.Usage.CacheReadTokens != 3 || call.Usage.ReasoningTokens != 4 {
			t.Fatalf("cache or reasoning usage disappeared: %+v", call)
		}
	}
	for _, role := range []string{"review", "triage", "verification"} {
		if seen[role] != 1 {
			t.Errorf("role %s count=%d", role, seen[role])
		}
	}
	if u := usageForJSON(calls); !u.TokensComplete || u.ReportedCostUSD != nil {
		t.Fatalf("unpriced usage was marked priced: %+v", u)
	}
}

type usageProbeLLM struct{ llms.LLM }

func (*usageProbeLLM) Provider() llms.Provider { return "fixture" }
func (*usageProbeLLM) GenerateContent(context.Context, []llms.Message, ...llms.CallOption) (*llms.Response, error) {
	return &llms.Response{Content: "ok", Usage: llms.Usage{PromptTokens: 12, CompletionTokens: 7, CacheReadTokens: 3, ReasoningTokens: 4}}, nil
}
