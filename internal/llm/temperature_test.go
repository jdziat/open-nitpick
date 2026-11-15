package llm

import (
	"context"
	"errors"
	"sync"
	"testing"

	llms "github.com/nocturnium/llm-go-sdk/v6"

	"github.com/jdziat/open-nitpick/internal/config"
)

// errRouterRefusal is the answer OpenRouter gives when no endpoint for the model
// takes a parameter in the request and require_parameters is set.
var errRouterRefusal = errors.New("openrouter: generate content: No endpoints found that can handle the requested parameters. To learn more about provider routing, visit: https://openrouter.ai/docs/guides/routing/provider-selection")

// noTempLLM refuses every request that carries a temperature, the way the
// newest OpenAI and Anthropic models do behind require_parameters.
type noTempLLM struct {
	mu      sync.Mutex
	sent    []*float64
	content string

	// alwaysRefuse refuses every request with the router's words, whether or not
	// it carries a temperature, as a router does when some other parameter is the
	// one no endpoint takes.
	alwaysRefuse bool
}

func (f *noTempLLM) GenerateContent(_ context.Context, _ []llms.Message, opts ...llms.CallOption) (*llms.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	applied := llms.ApplyOptions(opts...)
	f.sent = append(f.sent, applied.Temperature)
	if applied.Temperature != nil || f.alwaysRefuse {
		return nil, errRouterRefusal
	}
	return &llms.Response{Content: f.content, FinishReason: llms.FinishReasonStop}, nil
}
func (f *noTempLLM) Stream(context.Context, []llms.Message, ...llms.CallOption) (<-chan llms.StreamChunk, error) {
	return nil, errors.New("not supported")
}
func (f *noTempLLM) Provider() llms.Provider { return "fake" }
func (f *noTempLLM) Model() string           { return "no-temp" }

func (f *noTempLLM) requests() []*float64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*float64(nil), f.sent...)
}

func zeroTemperature() *float64 { z := 0.0; return &z }

func noTempClient(fake *noTempLLM) *Client {
	return &Client{
		LLM:          fake,
		Spec:         config.ModelSpec{Provider: "fake", Model: "no-temp", Temperature: zeroTemperature(), StructuredOutput: config.StructuredJSON},
		mode:         config.StructuredJSON,
		stallRetries: defaultMaxRetries,
	}
}

// A model whose router refuses a temperature is asked again without one, and
// the review gets its answer instead of failing every batch.
func TestAModelThatTakesNoTemperatureIsAskedAgainWithoutOne(t *testing.T) {
	fake := &noTempLLM{content: validJSON}
	c := noTempClient(fake)
	got, err := Extract[result](context.Background(), c, []llms.Message{{Role: llms.RoleUser, Content: "review"}})
	if err != nil {
		t.Fatalf("Extract failed on a model that takes no temperature: %v", err)
	}
	assertOneFinding(t, got)
	sent := fake.requests()
	if len(sent) != 2 || sent[0] == nil || sent[1] != nil {
		t.Errorf("temperatures sent = %v, want one refused request with a temperature, then one without", sent)
	}
}

// The refusal is remembered: the second call leaves the temperature out from
// the start instead of paying for a refused request again.
func TestAClientRemembersThatItsModelTakesNoTemperature(t *testing.T) {
	fake := &noTempLLM{content: validJSON}
	c := noTempClient(fake)
	for range 2 {
		if _, err := Extract[result](context.Background(), c, []llms.Message{{Role: llms.RoleUser, Content: "review"}}); err != nil {
			t.Fatal(err)
		}
	}
	sent := fake.requests()
	if len(sent) != 3 {
		t.Fatalf("requests = %d, want 3: a refused one, its retry, and one clean second call", len(sent))
	}
	if sent[2] != nil || !c.OmitsTemperature() {
		t.Errorf("third request carried temperature %v, omits = %v; want the temperature left out", sent[2], c.OmitsTemperature())
	}
	if opts := llms.ApplyOptions(c.CallOptions()...); opts.Temperature != nil {
		t.Errorf("CallOptions still carries temperature %v after the refusal", *opts.Temperature)
	}
	if opts := llms.ApplyOptions(c.CallOptionsWith(zeroTemperature(), "")...); opts.Temperature != nil {
		t.Errorf("CallOptionsWith still carries temperature %v after the refusal", *opts.Temperature)
	}
}

// Only that refusal, and only when a temperature was sent, is answered by
// dropping the temperature. Any other error, and the same words about a
// request that carried none, are left alone.
func TestOnlyARouterRefusalOfARequestWithATemperatureDropsIt(t *testing.T) {
	with := []llms.CallOption{llms.WithTemperature(0.2)}
	without := []llms.CallOption{llms.WithMaxTokens(100)}
	for name, tc := range map[string]struct {
		err  error
		call []llms.CallOption
		want bool
	}{
		"the refusal with a temperature":     {errRouterRefusal, with, true},
		"the refusal without a temperature":  {errRouterRefusal, without, false},
		"a rate limit with a temperature":    {errors.New("429 too many requests"), with, false},
		"an unrelated 404":                   {errors.New("404 model not found"), with, false},
		"a 400 about bad parameters":         {errors.New("400 invalid request parameters"), with, false},
		"a 404 for a model with no endpoint": {errors.New("404 No endpoints found for model x"), with, false},
		"no error":                           {nil, with, false},
	} {
		c := noTempClient(&noTempLLM{})
		if got := c.RejectsTemperature(tc.err, tc.call); got != tc.want {
			t.Errorf("%s: RejectsTemperature = %v, want %v", name, got, tc.want)
		}
		if c.OmitsTemperature() {
			t.Errorf("%s: classifying an error taught the client to omit temperature; only a retry that succeeds may", name)
		}
	}
}

// WithoutTemperature unsets what an earlier option set and touches nothing else.
func TestWithoutTemperatureUnsetsOnlyTheTemperature(t *testing.T) {
	call := []llms.CallOption{llms.WithTemperature(0.2), llms.WithMaxTokens(512)}
	got := llms.ApplyOptions(WithoutTemperature(call)...)
	if got.Temperature != nil || got.MaxTokens == nil || *got.MaxTokens != 512 {
		t.Errorf("temperature %v, max tokens %v; want the temperature gone and the cap kept", got.Temperature, got.MaxTokens)
	}
	if kept := llms.ApplyOptions(call...); kept.Temperature == nil {
		t.Error("WithoutTemperature edited the options it was given")
	}
}

// A refusal whose cause was some other parameter is not a model that takes no
// temperature. The retry without one fails the same way, and the client must not
// learn from it to drop a temperature the model accepts.
func TestAClientDoesNotLearnToOmitTemperatureFromARefusalTheRetryDidNotFix(t *testing.T) {
	fake := &noTempLLM{alwaysRefuse: true}
	c := noTempClient(fake)
	if _, err := Extract[result](context.Background(), c, []llms.Message{{Role: llms.RoleUser, Content: "review"}}); err == nil {
		t.Fatal("Extract succeeded against a model that refuses every request")
	}
	if len(fake.requests()) < 2 {
		t.Fatalf("requests = %d, want the refused one and its retry without a temperature", len(fake.requests()))
	}
	if c.OmitsTemperature() {
		t.Error("the client learned to omit temperature from a retry that did not succeed")
	}
}

func schemaClient(fake *noTempLLM) *Client {
	c := noTempClient(fake)
	c.mode = config.StructuredSchema
	c.Spec.StructuredOutput = config.StructuredSchema
	return c
}

// The schema path has its own retry loop, and a model that takes no temperature
// is handled there the same way: asked again without one, and remembered.
func TestTheSchemaPathAlsoLearnsThatAModelTakesNoTemperature(t *testing.T) {
	fake := &noTempLLM{content: validJSON}
	c := schemaClient(fake)
	got, err := Extract[result](context.Background(), c, []llms.Message{{Role: llms.RoleUser, Content: "review"}})
	if err != nil {
		t.Fatalf("Extract failed on a model that takes no temperature: %v", err)
	}
	assertOneFinding(t, got)
	if sent := fake.requests(); len(sent) != 2 || sent[0] == nil || sent[1] != nil {
		t.Errorf("temperatures sent = %v, want one refused request with a temperature, then one without", sent)
	}
	if !c.OmitsTemperature() {
		t.Error("the client did not learn from a retry that succeeded")
	}
}

// And on the schema path a retry that fails too teaches the client nothing.
func TestTheSchemaPathDoesNotLearnFromARetryThatFailedToo(t *testing.T) {
	fake := &noTempLLM{alwaysRefuse: true}
	c := schemaClient(fake)
	if _, err := Extract[result](context.Background(), c, []llms.Message{{Role: llms.RoleUser, Content: "review"}}); err == nil {
		t.Fatal("Extract succeeded against a model that refuses every request")
	}
	if c.OmitsTemperature() {
		t.Error("the client learned to omit temperature from a retry that did not succeed")
	}
}
