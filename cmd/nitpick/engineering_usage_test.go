package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/llm"
	llms "github.com/nocturnium/llm-go-sdk/v6"
)

type practiceUsageModel struct{ usage llms.Usage }

func (m *practiceUsageModel) GenerateContent(context.Context, []llms.Message, ...llms.CallOption) (*llms.Response, error) {
	return &llms.Response{Content: strings.Repeat("response ", 1000), Usage: m.usage}, nil
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
