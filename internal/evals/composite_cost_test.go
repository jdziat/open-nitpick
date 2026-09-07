package evals

import (
	"context"
	"testing"

	llms "github.com/nocturnium/llm-go-sdk/v6"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/llm"
)

// meteredClient returns a client speaking through stub, already metered, so a
// test can state which client made which call.
func meteredClient(model string, stub *meterStub) (*llm.Client, *Meter) {
	c := &llm.Client{LLM: stub, Spec: config.ModelSpec{Provider: "openrouter", Model: model}}
	return c, MeterClient(c)
}

// A composite row's cost dropped the reviewer's spend whenever two roles
// resolved to one model: the meters were keyed by model id, so the second
// client's meter replaced the first's, and with validation off the survivor
// had recorded nothing. The routed and ensemble rows in docs/comparison.md
// were published at a fraction of what they cost. See issue 45.
func TestSumMetersKeepsBothClientsSharingAModel(t *testing.T) {
	reviewStub := &meterStub{usage: llms.Usage{PromptTokens: 1000, CompletionTokens: 200}}
	triageStub := &meterStub{usage: llms.Usage{PromptTokens: 300, CompletionTokens: 40}}

	review, reviewMeter := meteredClient("qwen/qwen3.8-27b", reviewStub)
	triage, triageMeter := meteredClient("z-ai/glm-5.3-flash", triageStub)

	// The validate client BuildRoles hands back when a route file names no
	// validate model: a separate client carrying the review model, which is
	// never called with validation off.
	validate, validateMeter := meteredClient("qwen/qwen3.8-27b", &meterStub{})

	for _, c := range []*llm.Client{review, triage} {
		if _, err := c.LLM.GenerateContent(context.Background(), nil); err != nil {
			t.Fatalf("driving the stub: %v", err)
		}
	}

	total, byModel := sumMeters(map[*llm.Client]*Meter{
		review:   reviewMeter,
		triage:   triageMeter,
		validate: validateMeter,
	})

	if got := len(total.PerCall); got != 2 {
		t.Fatalf("total calls = %d, want 2 (the review call and the triage call)", got)
	}

	reviewRow := byModel["qwen/qwen3.8-27b"]
	if len(reviewRow.PerCall) != 1 {
		t.Fatalf("qwen row calls = %d, want 1; the reviewer's spend was dropped", len(reviewRow.PerCall))
	}
	if got := reviewRow.PerCall[0].Prompt; got != 1000 {
		t.Errorf("qwen row prompt tokens = %d, want 1000", got)
	}

	triageRow := byModel["z-ai/glm-5.3-flash"]
	if len(triageRow.PerCall) != 1 {
		t.Fatalf("glm row calls = %d, want 1", len(triageRow.PerCall))
	}
	if got := triageRow.PerCall[0].Prompt; got != 300 {
		t.Errorf("glm row prompt tokens = %d, want 300", got)
	}
}

// Two clients on one model are one priced row, not two, since the price table
// is keyed by model id.
func TestSumMetersMergesCallsOnOneModel(t *testing.T) {
	stub := &meterStub{usage: llms.Usage{PromptTokens: 10, CompletionTokens: 2}}

	first, firstMeter := meteredClient("qwen/qwen3.8-27b", stub)
	second, secondMeter := meteredClient("qwen/qwen3.8-27b", stub)

	for _, c := range []*llm.Client{first, second} {
		if _, err := c.LLM.GenerateContent(context.Background(), nil); err != nil {
			t.Fatalf("driving the stub: %v", err)
		}
	}

	total, byModel := sumMeters(map[*llm.Client]*Meter{first: firstMeter, second: secondMeter})

	if got := len(byModel); got != 1 {
		t.Fatalf("model rows = %d, want 1", got)
	}
	if got := len(byModel["qwen/qwen3.8-27b"].PerCall); got != 2 {
		t.Errorf("qwen row calls = %d, want 2", got)
	}
	if got := len(total.PerCall); got != 2 {
		t.Errorf("total calls = %d, want 2", got)
	}
}

// The failing sequence from issue 45, end to end over the real BuildRoles: a
// route file that names a triage model and no validate model, all review
// traffic on the default model, validation off. The reported cost has to
// include the reviewer.
func TestCompositeMeteringKeepsTheReviewerSpend(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "or_test")

	cfg := &config.Config{Models: config.Models{
		Default: config.ModelSpec{Provider: "openrouter", Model: "qwen/qwen3.8-27b"},
		Triage:  &config.ModelSpec{Provider: "openrouter", Model: "z-ai/glm-5.3-flash"},
	}}

	roles, err := llm.BuildRoles(cfg)
	if err != nil {
		t.Fatalf("BuildRoles: %v", err)
	}

	// Stubs go in before the meters, the order runOne uses: a meter wraps
	// whatever the client held when it was installed.
	stub := &meterStub{usage: llms.Usage{PromptTokens: 1000, CompletionTokens: 200}}
	roles.Each(func(c *llm.Client) { c.LLM = stub })

	meters := map[*llm.Client]*Meter{}
	roles.Each(func(c *llm.Client) { meters[c] = MeterClient(c) })

	// Only the reviewer speaks. Validation is off in the tuning runs, so its
	// client, which carries the same model id, records nothing.
	if _, err := roles.Review.LLM.GenerateContent(context.Background(), nil); err != nil {
		t.Fatalf("driving the stub: %v", err)
	}

	total, byModel := sumMeters(meters)

	if len(total.PerCall) != 1 {
		t.Fatalf("total calls = %d, want 1: the reviewer's spend was dropped", len(total.PerCall))
	}
	if got := len(byModel["qwen/qwen3.8-27b"].PerCall); got != 1 {
		t.Errorf("qwen row calls = %d, want 1", got)
	}
	if got := len(byModel["z-ai/glm-5.3-flash"].PerCall); got != 0 {
		t.Errorf("glm row calls = %d, want 0", got)
	}
}
