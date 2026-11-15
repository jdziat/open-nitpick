package llm

import (
	"testing"

	llms "github.com/nocturnium/llm-go-sdk/v6"

	"github.com/jdziat/open-nitpick/internal/config"
)

// A stage that samples on its own terms gets its temperature and reasoning
// level, and keeps the model's when it names neither.
func TestCallOptionsWithReplacesOnlyWhatTheCallerNames(t *testing.T) {
	own := 0.0
	client := &Client{LLM: newFakeLLM(), Spec: config.ModelSpec{
		Provider: "fake", Model: "m", Temperature: &own, Reasoning: config.ReasoningLow, MaxTokens: 4096,
	}}

	stage := 0.2
	applied := llms.ApplyOptions(client.CallOptionsWith(&stage, config.ReasoningMedium)...)
	if applied.Temperature == nil || *applied.Temperature != 0.2 {
		t.Errorf("temperature = %v, want the stage's 0.2", applied.Temperature)
	}
	if applied.Reasoning == nil || applied.Reasoning.Effort != llms.ReasoningEffort(config.ReasoningMedium) {
		t.Errorf("reasoning = %+v, want the stage's medium", applied.Reasoning)
	}
	if applied.MaxTokens == nil || *applied.MaxTokens != 4096 {
		t.Errorf("max tokens = %v, want the model's 4096 kept", applied.MaxTokens)
	}

	kept := llms.ApplyOptions(client.CallOptionsWith(nil, "")...)
	if kept.Temperature == nil || *kept.Temperature != 0 || kept.Reasoning == nil || kept.Reasoning.Effort != llms.ReasoningEffort(config.ReasoningLow) {
		t.Errorf("unnamed: temperature %v, reasoning %+v; want the model's own", kept.Temperature, kept.Reasoning)
	}
	if *client.Spec.Temperature != 0 || client.Spec.Reasoning != config.ReasoningLow {
		t.Error("CallOptionsWith changed the client's own spec")
	}
}
