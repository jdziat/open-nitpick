package llm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	llms "github.com/nocturnium/llm-go-sdk"

	"github.com/jdziat/open-nitpick/internal/config"
)

// result is the payload the tests decode. It is deliberately small but has
// enough shape (nested slice, enum-ish string) to exercise schema derivation.
type result struct {
	Findings []finding `json:"findings"`
}

type finding struct {
	Path     string `json:"path"`
	Line     int    `json:"line"`
	Severity string `json:"severity"`
	Title    string `json:"title"`
}

// fakeLLM is a scripted llms.LLM. Each call consumes the next turn, which lets
// a test express "the schema call fails, then the JSON call succeeds".
type fakeLLM struct {
	mu    sync.Mutex
	turns []turn
	calls []recordedCall
}

type turn struct {
	content string
	err     error
}

type recordedCall struct {
	messages []llms.Message
	opts     llms.CallOptions
}

func newFakeLLM(turns ...turn) *fakeLLM { return &fakeLLM{turns: turns} }

func (f *fakeLLM) GenerateContent(_ context.Context, msgs []llms.Message, opts ...llms.CallOption) (*llms.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, recordedCall{messages: msgs, opts: *llms.ApplyOptions(opts...)})

	if len(f.turns) == 0 {
		return nil, errors.New("fakeLLM: no scripted turn remaining")
	}
	next := f.turns[0]
	f.turns = f.turns[1:]

	if next.err != nil {
		return nil, next.err
	}
	return &llms.Response{Content: next.content}, nil
}

func (f *fakeLLM) Stream(context.Context, []llms.Message, ...llms.CallOption) (<-chan llms.StreamChunk, error) {
	return nil, errors.New("fakeLLM: streaming not supported")
}

func (f *fakeLLM) Provider() llms.Provider { return llms.Provider("fake") }
func (f *fakeLLM) Model() string           { return "fake-model" }

func (f *fakeLLM) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeLLM) call(i int) recordedCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[i]
}

// newTestClient wires a fake LLM into a Client with the given strategy.
func newTestClient(fake *fakeLLM, mode config.StructuredMode) *Client {
	return &Client{
		LLM:          fake,
		Spec:         config.ModelSpec{Provider: "fake", Model: "fake-model", StructuredOutput: mode},
		mode:         mode,
		stallRetries: defaultMaxRetries,
	}
}

const validJSON = `{"findings":[{"path":"a.go","line":7,"severity":"warning","title":"unchecked error"}]}`

func assertOneFinding(t *testing.T, got result) {
	t.Helper()

	if len(got.Findings) != 1 {
		t.Fatalf("findings = %d, want 1: %+v", len(got.Findings), got)
	}
	f := got.Findings[0]
	if f.Path != "a.go" || f.Line != 7 || f.Severity != "warning" {
		t.Errorf("decoded finding = %+v, want a.go:7 warning", f)
	}
}

func TestExtractSchemaPathHappy(t *testing.T) {
	fake := newFakeLLM(turn{content: validJSON})
	client := newTestClient(fake, config.StructuredSchema)

	got, err := Extract[result](context.Background(), client, nil)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	assertOneFinding(t, got)

	if fake.callCount() != 1 {
		t.Errorf("calls = %d, want 1 (no fallback in schema mode)", fake.callCount())
	}
	// The schema path must actually request a json_schema response format.
	if rf := fake.call(0).opts.ResponseFormat; rf == nil || rf.Type != llms.ResponseFormatJSONSchema {
		t.Errorf("schema mode should set a json_schema response format, got %+v", rf)
	}
}

func TestExtractSchemaModeDoesNotFallBack(t *testing.T) {
	// An explicit strategy is a user decision and must be respected even when
	// it fails, otherwise config means nothing.
	fake := newFakeLLM(turn{err: errors.New("response_format is not supported")})
	client := newTestClient(fake, config.StructuredSchema)

	if _, err := Extract[result](context.Background(), client, nil); err == nil {
		t.Fatal("want error in schema mode")
	}
	if fake.callCount() != 1 {
		t.Errorf("calls = %d, want 1: schema mode must not silently fall back", fake.callCount())
	}
}

func TestExtractAutoFallsBackToJSONMode(t *testing.T) {
	fake := newFakeLLM(
		turn{err: errors.New("400: response_format json_schema is not supported by this model")},
		turn{content: validJSON},
	)
	client := newTestClient(fake, config.StructuredAuto)

	got, err := Extract[result](context.Background(), client, nil)
	if err != nil {
		t.Fatalf("auto mode should recover via JSON fallback: %v", err)
	}
	assertOneFinding(t, got)

	if fake.callCount() != 2 {
		t.Fatalf("calls = %d, want 2 (schema attempt then JSON fallback)", fake.callCount())
	}
	// The fallback must use JSON mode and carry the schema in the prompt.
	second := fake.call(1)
	if second.opts.ResponseFormat == nil || second.opts.ResponseFormat.Type != llms.ResponseFormatJSONObject {
		t.Errorf("fallback should use JSON mode, got %+v", second.opts.ResponseFormat)
	}
	if !mentionsSchema(second.messages) {
		t.Error("fallback prompt should describe the target schema")
	}
}

func TestExtractAutoRemembersDowngrade(t *testing.T) {
	// The fallback should be paid for once per client, not once per request.
	fake := newFakeLLM(
		turn{err: errors.New("response_format not supported")},
		turn{content: validJSON},
		turn{content: validJSON},
	)
	client := newTestClient(fake, config.StructuredAuto)

	if _, err := Extract[result](context.Background(), client, nil); err != nil {
		t.Fatalf("first Extract: %v", err)
	}
	if _, err := Extract[result](context.Background(), client, nil); err != nil {
		t.Fatalf("second Extract: %v", err)
	}

	if fake.callCount() != 3 {
		t.Errorf("calls = %d, want 3: the second Extract should skip the schema attempt", fake.callCount())
	}
	if got := client.structuredMode(); got != config.StructuredJSON {
		t.Errorf("mode = %q, want json after downgrade", got)
	}
}

func TestExtractJSONModeParsesFencedOutput(t *testing.T) {
	fenced := "```json\n" + validJSON + "\n```"
	fake := newFakeLLM(turn{content: fenced})
	client := newTestClient(fake, config.StructuredJSON)

	got, err := Extract[result](context.Background(), client, nil)
	if err != nil {
		t.Fatalf("fenced JSON should parse: %v", err)
	}
	assertOneFinding(t, got)

	if fake.callCount() != 1 {
		t.Errorf("calls = %d, want 1: lenient parsing should avoid a repair round", fake.callCount())
	}
}

func TestExtractJSONModeParsesProseWrappedOutput(t *testing.T) {
	prose := "Sure! Here is the review:\n\n" + validJSON + "\n\nLet me know if you want more detail."
	fake := newFakeLLM(turn{content: prose})
	client := newTestClient(fake, config.StructuredJSON)

	got, err := Extract[result](context.Background(), client, nil)
	if err != nil {
		t.Fatalf("prose-wrapped JSON should parse: %v", err)
	}
	assertOneFinding(t, got)

	if fake.callCount() != 1 {
		t.Errorf("calls = %d, want 1", fake.callCount())
	}
}

func TestExtractJSONModeRepairsMalformedOutput(t *testing.T) {
	fake := newFakeLLM(
		turn{content: `{"findings":[{"path":"a.go",]}`}, // unparseable
		turn{content: validJSON},
	)
	client := newTestClient(fake, config.StructuredJSON)

	got, err := Extract[result](context.Background(), client, nil)
	if err != nil {
		t.Fatalf("one repair round should recover: %v", err)
	}
	assertOneFinding(t, got)

	if fake.callCount() != 2 {
		t.Fatalf("calls = %d, want 2 (original then repair)", fake.callCount())
	}
	// The repair prompt must show the model its own bad output.
	repair := fake.call(1).messages
	if len(repair) < 2 {
		t.Fatal("repair prompt is missing context")
	}
	if repair[len(repair)-2].Role != llms.RoleAssistant {
		t.Error("repair should replay the assistant's failed output")
	}
}

func TestExtractJSONModeRepairIsBounded(t *testing.T) {
	// Two bad responses must fail rather than loop, so a confused local model
	// cannot burn the budget.
	fake := newFakeLLM(
		turn{content: "not json at all"},
		turn{content: "still not json"},
	)
	client := newTestClient(fake, config.StructuredJSON)

	_, err := Extract[result](context.Background(), client, nil)
	if err == nil {
		t.Fatal("want error after the repair attempt also fails")
	}
	if fake.callCount() != 2 {
		t.Errorf("calls = %d, want exactly 2: repair must not loop", fake.callCount())
	}
}

func TestExtractEmptyResponseIsAnError(t *testing.T) {
	fake := newFakeLLM(turn{content: "   "}, turn{content: "  "})
	client := newTestClient(fake, config.StructuredJSON)

	if _, err := Extract[result](context.Background(), client, nil); err == nil {
		t.Fatal("empty content should be an error, not an empty result")
	}
}

func TestExtractCancelledContextDoesNotFallBack(t *testing.T) {
	// A cancelled run should surface the cancellation, not spend another call
	// chasing a capability problem that is not there.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	fake := newFakeLLM(turn{err: context.Canceled}, turn{content: validJSON})
	client := newTestClient(fake, config.StructuredAuto)

	if _, err := Extract[result](ctx, client, nil); err == nil {
		t.Fatal("want error for a cancelled context")
	}
	if fake.callCount() != 1 {
		t.Errorf("calls = %d, want 1: a cancelled context must not trigger the fallback", fake.callCount())
	}
	if client.structuredMode() != config.StructuredAuto {
		t.Error("a cancelled context must not downgrade the client")
	}
}

func TestExtractNilClient(t *testing.T) {
	if _, err := Extract[result](context.Background(), nil, nil); err == nil {
		t.Fatal("want error for a nil client")
	}
}

func TestExtractAutoReportsBothFailures(t *testing.T) {
	fake := newFakeLLM(
		turn{err: errors.New("schema-unsupported")},
		turn{content: "garbage"},
		turn{content: "more garbage"},
	)
	client := newTestClient(fake, config.StructuredAuto)

	_, err := Extract[result](context.Background(), client, nil)
	if err == nil {
		t.Fatal("want error when both strategies fail")
	}
	// The first error explains why the fallback ran; losing it makes the
	// failure much harder to diagnose.
	if !strings.Contains(err.Error(), "schema-unsupported") {
		t.Errorf("error should retain the schema failure, got: %v", err)
	}
}

func TestClientCallOptionsCarrySpec(t *testing.T) {
	temp := 0.2
	client := &Client{
		LLM: newFakeLLM(),
		Spec: config.ModelSpec{
			Provider: "fake", Model: "m", Temperature: &temp, MaxTokens: 4096,
		},
		mode: config.StructuredAuto,
	}

	applied := llms.ApplyOptions(client.CallOptions()...)
	if applied.Temperature == nil || *applied.Temperature != 0.2 {
		t.Errorf("temperature not applied: %+v", applied.Temperature)
	}
	if applied.MaxTokens == nil || *applied.MaxTokens != 4096 {
		t.Errorf("max tokens not applied: %+v", applied.MaxTokens)
	}
}

// mentionsSchema reports whether any message describes the JSON schema.
func mentionsSchema(msgs []llms.Message) bool {
	for _, m := range msgs {
		if strings.Contains(m.Content, "JSON Schema") && strings.Contains(m.Content, "findings") {
			return true
		}
	}
	return false
}

func TestExtractJSONObjectIgnoresBracesInStrings(t *testing.T) {
	// A finding's text can easily contain a brace; naive brace counting would
	// truncate the object and lose the finding.
	in := `prefix {"findings":[{"path":"a.go","line":1,"severity":"nit","title":"use map[string]{}"}]} suffix`

	candidates := extractJSONCandidates(in)
	if len(candidates) == 0 {
		t.Fatal("no object extracted")
	}
	got := candidates[0]
	if !strings.HasSuffix(got, "}") || strings.Contains(got, "suffix") {
		t.Errorf("extracted %q, want a balanced object", got)
	}

	var decoded result
	if _, err := decodeLenient[result](got); err != nil {
		t.Errorf("extracted object should decode: %v", err)
	}
	_ = decoded
}

func TestStripCodeFenceVariants(t *testing.T) {
	cases := []struct{ in, want string }{
		{"```json\n{\"a\":1}\n```", `{"a":1}`},
		{"```\n{\"a\":1}\n```", `{"a":1}`},
		{`{"a":1}`, `{"a":1}`},
	}
	for _, tc := range cases {
		if got := stripCodeFence(tc.in); got != tc.want {
			t.Errorf("stripCodeFence(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// stallErr is what net/http reports when its own Timeout fires mid-body.
type stallErr struct{}

func (stallErr) Error() string {
	return "context deadline exceeded (Client.Timeout or context cancellation while reading body)"
}
func (stallErr) Timeout() bool   { return true }
func (stallErr) Temporary() bool { return true }

func TestExtractRetriesStalledRequestsUpToMaxRetries(t *testing.T) {
	stall := func() turn { return turn{err: fmt.Errorf("openai: generate content: %w", stallErr{})} }

	for _, mode := range []config.StructuredMode{config.StructuredAuto, config.StructuredSchema, config.StructuredJSON} {
		// max_retries stalls, then an answer: the budget is spent and the review survives.
		fake := newFakeLLM(stall(), stall(), stall(), turn{content: validJSON})
		client := newTestClient(fake, mode)

		got, err := Extract[result](context.Background(), client, nil)
		if err != nil {
			t.Fatalf("%s: %d stalls are within the budget, got %v", mode, defaultMaxRetries, err)
		}
		assertOneFinding(t, got)
		if fake.callCount() != defaultMaxRetries+1 {
			t.Errorf("%s: calls = %d, want %d", mode, fake.callCount(), defaultMaxRetries+1)
		}
	}

	// The first attempt is never capped; every retry after a stall is, unless
	// the caller chose a cap, in which case theirs stands.
	fake := newFakeLLM(stall(), stall(), turn{content: validJSON})
	if _, err := Extract[result](context.Background(), newTestClient(fake, config.StructuredSchema), nil); err != nil {
		t.Fatal(err)
	}
	if got := fake.call(0).opts.MaxTokens; got != nil {
		t.Errorf("first attempt was capped at %d", *got)
	}
	for i := 1; i <= 2; i++ {
		if got := fake.call(i).opts.MaxTokens; got == nil || *got != creditCappedMaxTokens {
			t.Errorf("retry %d not capped at %d, so a runaway generation times out rather than returning truncated: %v", i, creditCappedMaxTokens, got)
		}
	}
	fake = newFakeLLM(stall(), turn{content: validJSON})
	if _, err := Extract[result](context.Background(), newTestClient(fake, config.StructuredSchema), nil, llms.WithMaxTokens(4096)); err != nil {
		t.Fatal(err)
	}
	if got := fake.call(1).opts.MaxTokens; got == nil || *got != 4096 {
		t.Errorf("the caller's cap of 4096 was replaced: %v", got)
	}

	// A capped retry that comes back cut is a runaway generation: the next
	// attempt samples above zero, and a caller's own temperature is kept.
	cut := func() turn {
		return turn{err: fmt.Errorf("llms: structured output is not valid JSON: %w", errors.New("unexpected end of JSON input"))}
	}
	fake = newFakeLLM(stall(), cut(), turn{content: validJSON})
	if _, err := Extract[result](context.Background(), newTestClient(fake, config.StructuredSchema), nil); err != nil {
		t.Fatal(err)
	}
	if got := fake.call(1).opts.Temperature; got != nil && *got != 0 {
		t.Errorf("the first retry keeps the caller's temperature, got %v", *got)
	}
	if got := fake.call(2).opts.Temperature; got == nil || *got != runawayRetryTemperature {
		t.Errorf("the retry after a truncation samples at %v, got %v", runawayRetryTemperature, got)
	}
	fake = newFakeLLM(stall(), cut(), turn{content: validJSON})
	if _, err := Extract[result](context.Background(), newTestClient(fake, config.StructuredSchema), nil, llms.WithTemperature(0.7)); err == nil {
		t.Fatal("a caller who set a temperature above zero gets no re-sampling: the truncation is the answer")
	}
	// A first attempt cut at a cap the caller chose is not retried.
	fake = newFakeLLM(cut(), turn{content: validJSON})
	if _, err := Extract[result](context.Background(), newTestClient(fake, config.StructuredSchema), nil, llms.WithMaxTokens(512)); err == nil || fake.callCount() != 1 {
		t.Fatalf("first-attempt truncation: calls = %d, err = %v; want 1 call and an error", fake.callCount(), err)
	}

	// One more stall than the budget is the provider's answer.
	fake = newFakeLLM(stall(), stall(), stall(), stall(), turn{content: validJSON})
	if _, err := Extract[result](context.Background(), newTestClient(fake, config.StructuredSchema), nil); err == nil {
		t.Fatal("a stall past max_retries is not retried")
	}
	if fake.callCount() != defaultMaxRetries+1 {
		t.Errorf("calls = %d, want %d", fake.callCount(), defaultMaxRetries+1)
	}

	// max_retries: 0 means what it says.
	fake = newFakeLLM(stall(), turn{content: validJSON})
	client := newTestClient(fake, config.StructuredSchema)
	client.stallRetries = 0
	if _, err := Extract[result](context.Background(), client, nil); err == nil || fake.callCount() != 1 {
		t.Fatalf("max_retries 0: calls = %d, err = %v; want 1 call and an error", fake.callCount(), err)
	}

	// A caller that gave up is not a stall.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fake = newFakeLLM(stall(), turn{content: validJSON})
	if _, err := Extract[result](ctx, newTestClient(fake, config.StructuredSchema), nil); err == nil || fake.callCount() != 1 {
		t.Fatalf("cancelled context: calls = %d, err = %v; want 1 call and an error", fake.callCount(), err)
	}
}

func TestExtractFallsBackToPlainTextWhenJSONModeIsRejected(t *testing.T) {
	rejected := &llms.APIError{StatusCode: 405, Message: `{"error":{"message":"json_object response format is not supported for model: google/gemma-4-31B-it-turbo","param":"response_format"}}`}
	fake := newFakeLLM(turn{err: rejected}, turn{content: "Here you go:\n" + validJSON})
	client := newTestClient(fake, config.StructuredJSON)

	// The engine passes the schema as a call option; the fallback must not
	// let it through as a response_format.
	schema := llms.WithJSONSchema("result", []byte(`{"type":"object"}`), true)
	got, err := Extract[result](context.Background(), client, nil, schema)
	if err != nil {
		t.Fatalf("json_object rejected must fall back to no response_format, got %v", err)
	}
	assertOneFinding(t, got)
	if fake.callCount() != 2 {
		t.Fatalf("calls = %d, want 2", fake.callCount())
	}
	if fake.call(0).opts.ResponseFormat == nil {
		t.Error("the first call asked for JSON mode")
	}
	if fake.call(1).opts.ResponseFormat != nil {
		t.Error("the fallback sends no response_format")
	}
	if client.structuredMode() != config.StructuredText {
		t.Errorf("mode after the rejection = %q, want text so the next batch skips it", client.structuredMode())
	}

	// Chosen outright, text mode never asks for a format, whatever the
	// caller's options carry.
	fake = newFakeLLM(turn{content: validJSON})
	if _, err := Extract[result](context.Background(), newTestClient(fake, config.StructuredText), nil, schema); err != nil {
		t.Fatal(err)
	}
	if fake.call(0).opts.ResponseFormat != nil {
		t.Error("text mode sent a response_format")
	}
}

func TestDecodeLenientEscapesControlCharactersInsideStrings(t *testing.T) {
	raw := "{\"findings\":[{\"path\":\"a.go\",\"line\":7,\"severity\":\"warning\",\"title\":\"tab\there\nand newline\"}]}"
	got, err := decodeLenient[result](raw)
	if err != nil {
		t.Fatalf("a raw tab inside a string literal is escaped, not fatal: %v", err)
	}
	if got.Findings[0].Title != "tab\there\nand newline" {
		t.Errorf("title = %q", got.Findings[0].Title)
	}

	// Existing escapes are not doubled, and structure outside strings is untouched.
	if s := escapeControlChars("{\"a\":\"x\\ty\"}\n"); s != "{\"a\":\"x\\ty\"}\n" {
		t.Errorf("escapeControlChars altered valid JSON: %q", s)
	}
}
