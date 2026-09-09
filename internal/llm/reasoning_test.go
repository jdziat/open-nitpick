package llm

import (
	"testing"

	llms "github.com/nocturnium/llm-go-sdk/v6"

	"github.com/jdziat/open-nitpick/internal/config"
)

// An unset level sends nothing.
//
// Every number in docs/findings.md was measured with whatever the model does
// by default, so sending a level nobody asked for would change every published
// result without changing the document that reports it.
func TestNoReasoningLevelSendsNothing(t *testing.T) {
	if _, ok := reasoningOption(""); ok {
		t.Error("an unset reasoning level produced a call option")
	}
}

// Each level reaches the SDK as the effort it names, and "off" as a switch
// rather than an effort, because a provider with a boolean is the only one
// that can be told not to think at all.
func TestEachReasoningLevelReachesTheProvider(t *testing.T) {
	for _, level := range []config.ReasoningLevel{
		config.ReasoningMinimal, config.ReasoningLow,
		config.ReasoningMedium, config.ReasoningHigh,
	} {
		opt, ok := reasoningOption(level)
		if !ok {
			t.Fatalf("%s produced no option", level)
		}
		var o llms.CallOptions
		opt(&o)
		if o.Reasoning == nil {
			t.Fatalf("%s set no reasoning config", level)
		}
		if got := o.Reasoning.Effort; string(got) != string(level) {
			t.Errorf("%s reached the provider as %q", level, got)
		}
		if !o.Reasoning.IsEnabled() {
			t.Errorf("%s did not enable reasoning", level)
		}
	}

	opt, ok := reasoningOption(config.ReasoningOff)
	if !ok {
		t.Fatal("off produced no option")
	}
	var o llms.CallOptions
	opt(&o)
	if o.Reasoning == nil || o.Reasoning.Enabled == nil || *o.Reasoning.Enabled {
		t.Errorf("off did not ask the provider to stop reasoning: %+v", o.Reasoning)
	}
	if o.Reasoning.IsEnabled() {
		t.Error("off reported itself as enabled")
	}
}
