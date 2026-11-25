package main

import (
	stdcmp "cmp"
	"slices"
	"sort"
	"sync"

	"github.com/nitpick-oss/nitpick/internal/config"
	"github.com/nitpick-oss/nitpick/internal/evals"
	"github.com/nitpick-oss/nitpick/internal/llm"
	"github.com/nitpick-oss/nitpick/internal/practices"
)

type engineeringUsage struct {
	mu       sync.Mutex
	meters   map[*llm.Client]*evals.Meter
	external []llm.CallRecord
}

func (u *engineeringUsage) reset() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.meters = map[*llm.Client]*evals.Meter{}
	u.external = nil
}

func (u *engineeringUsage) attach(roles *llm.Roles) {
	if roles == nil {
		return
	}
	meter := func(client *llm.Client) {
		if client == nil {
			return
		}
		u.mu.Lock()
		defer u.mu.Unlock()
		if u.meters == nil {
			u.meters = map[*llm.Client]*evals.Meter{}
		}
		if _, exists := u.meters[client]; !exists {
			u.meters[client] = evals.MeterClient(client)
		}
	}
	roles.Each(meter)
	build := roles.Build
	if build == nil {
		build = llm.Build
	}
	roles.Build = func(spec config.ModelSpec) (*llm.Client, error) {
		client, err := build(spec)
		if err == nil {
			(&llm.Roles{Review: client}).Each(meter)
		}
		return client, err
	}
}

func (u *engineeringUsage) snapshot() []practices.ModelUsage {
	u.mu.Lock()
	defer u.mu.Unlock()
	grouped := map[string]evals.TokenUsage{}
	for client, meter := range u.meters {
		name := client.String()
		grouped[name] = grouped[name].Add(meter.Usage())
	}
	var names []string
	for name := range grouped {
		names = append(names, name)
	}
	sort.Strings(names)
	var out []practices.ModelUsage
	for _, name := range names {
		usage := grouped[name]
		if usage.Calls()+usage.Unreported+usage.Failed == 0 {
			continue
		}
		out = append(out, practices.ModelUsage{Model: name, ReportedCalls: usage.Calls(), UnreportedCalls: usage.Unreported, FailedCalls: usage.Failed,
			PromptTokens: usage.Prompt(), CompletionTokens: usage.Completion(), CacheReadTokens: usage.CacheRead(), CacheWriteTokens: usage.CacheWrite(), ReasoningTokens: usage.Reasoning()})
	}
	return out
}

func (u *engineeringUsage) calls() []llm.CallRecord {
	u.mu.Lock()
	defer u.mu.Unlock()
	out := append([]llm.CallRecord{}, u.external...)
	for _, meter := range u.meters {
		out = append(out, meter.Records()...)
	}
	slices.SortFunc(out, func(a, b llm.CallRecord) int {
		return stdcmp.Or(a.StartedAt.Compare(b.StartedAt), stdcmp.Compare(a.Provider, b.Provider), stdcmp.Compare(a.Model, b.Model), stdcmp.Compare(a.Role, b.Role))
	})
	return out
}

func (u *engineeringUsage) observe(call llm.CallRecord) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.external = append(u.external, call)
}
