package main

import "github.com/nitpick-oss/nitpick/internal/llm"

type usageJSON struct {
	SchemaVersion   int              `json:"schema_version"`
	Metered         bool             `json:"metered"`
	TokensComplete  bool             `json:"tokens_complete"`
	UnknownUsage    int              `json:"unknown_usage_attempts"`
	ReportedCostUSD *float64         `json:"reported_cost_usd"`
	Reason          string           `json:"reason,omitempty"`
	TokenSemantics  string           `json:"token_semantics"`
	Attempts        []llm.CallRecord `json:"attempts"`
}

func usageForJSON(records []llm.CallRecord) usageJSON {
	u := usageJSON{SchemaVersion: 1, Metered: records != nil, TokensComplete: records != nil,
		TokenSemantics: "prompt excludes cache reads and writes; reasoning is a subset of completion",
		Attempts:       records}
	if records == nil {
		u.Reason = "meter unavailable"
		return u
	}
	if len(records) == 0 {
		u.Reason = "no model calls"
	}
	cost := 0.0
	priced := true
	for _, record := range records {
		if record.Usage == nil {
			u.UnknownUsage++
			u.TokensComplete = false
		}
		if record.Usage == nil || record.Usage.Cost == nil || *record.Usage.Cost < 0 {
			priced = false
		} else {
			cost += *record.Usage.Cost
		}
	}
	if priced {
		u.ReportedCostUSD = &cost
	}
	return u
}
