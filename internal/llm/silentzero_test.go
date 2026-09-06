package llm

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
)

// realAnswer is what the model actually reported. Every case below wraps it in
// something a real model plausibly emits.
const realAnswer = `{"findings":[{"path":"a.go","line":7,"severity":"error","title":"real finding"}],"summary":"walk"}`

// TestNeverSilentlyReturnsZeroFindings is the regression test for the worst
// failure this tool can have.
//
// Previously, any response whose first balanced brace pair was not the answer
// decoded to an empty Result with a NIL error. The engine recorded no failure,
// the batch contributed nothing, and the run exited 0 reporting a clean pull
// request — while the model's real findings were thrown away.
//
// The contract now: either the findings come back, or an error does. Never
// silence.
func TestNeverSilentlyReturnsZeroFindings(t *testing.T) {
	cases := []struct {
		name string
		in   string
		// recoverable means we expect the real answer to be extracted rather
		// than merely erroring.
		recoverable bool
	}{
		{"composite literal in prose", "The code uses a map[string]struct{} set.\n" + realAnswer, true},
		{"empty braces in prose", "Analysis: {}\n" + realAnswer, true},
		{"schema echo", `The schema is {"type":"object","properties":{}}. Now: ` + realAnswer, true},
		{"think block", "<think>\nThe guard `if err != nil {` returns early.\n</think>\n" + realAnswer, true},
		{"thinking block", "<thinking>x { y</thinking>" + realAnswer, true},
		{"harmony channel", "<|channel|>analysis stuff {\n<|channel|>final\n" + realAnswer, true},
		{"fenced with prose", "Here you go:\n```json\n" + realAnswer + "\n```\nHope that helps!", true},
		{"trailing prose", realAnswer + "\n\nLet me know if you want more detail.", true},

		// These carry no recoverable answer: the requirement is a loud error.
		{"bare empty object", `{}`, false},
		{"json null", `null`, false},
		{"unrelated object", `{"status":"ok","message":"nothing to do"}`, false},
		{"prose only", `I reviewed the diff and found no issues.`, false},
		{"empty", ``, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := decodeLenient[result](tc.in)

			if tc.recoverable {
				if err != nil {
					t.Fatalf("should have recovered the real answer, got error: %v", err)
				}
				if len(got.Findings) != 1 || got.Findings[0].Path != "a.go" {
					t.Fatalf("recovered the wrong object: %+v", got)
				}
				return
			}

			// The critical assertion: no silent zero.
			if err == nil {
				t.Fatalf("SILENT ZERO: err==nil with %d findings — a failure must be loud", len(got.Findings))
			}
		})
	}
}

// TestTopLevelArrayIsNotMistakenForTheAnswer covers the case where the model
// returns a bare array. Previously the first array *element* was extracted and
// decoded into Result as all-zero.
func TestTopLevelArrayIsNotMistakenForTheAnswer(t *testing.T) {
	_, err := decodeLenient[result](`[{"path":"a.go","line":7,"title":"boom"}]`)
	if err == nil {
		t.Fatal("a bare array is not a Result; want an error so the repair round-trip runs")
	}
}

func TestWrapperKeyIsRejected(t *testing.T) {
	// {"result":{...}} decodes into Result as all-zero without error.
	_, err := decodeLenient[result](`{"result":` + realAnswer + `}`)
	if err == nil {
		t.Fatal("a wrapper object has none of the expected fields; want an error")
	}
}

func TestStripReasoning(t *testing.T) {
	cases := []struct{ in, want string }{
		{"<think>a{b</think>x", "x"},
		{"<thinking>a</thinking>x", "x"},
		{"pre<think>mid</think>post", "prepost"},
		// Unterminated reasoning must not swallow a later answer... but there
		// is no later answer to keep, so everything before it is what remains.
		{"keep<think>never closed", "keep"},
		{"no markers", "no markers"},
	}
	for _, tc := range cases {
		if got := stripReasoning(tc.in); got != tc.want {
			t.Errorf("stripReasoning(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestExtractJSONCandidatesFindsAll(t *testing.T) {
	got := extractJSONCandidates(`prefix {"a":1} middle {"b":{"c":2}} suffix`)
	if len(got) != 2 {
		t.Fatalf("candidates = %v, want 2 top-level objects", got)
	}
	if got[0] != `{"a":1}` {
		t.Errorf("first = %q", got[0])
	}
	if got[1] != `{"b":{"c":2}}` {
		t.Errorf("second = %q — nested objects must not be returned separately", got[1])
	}
}

func TestExtractJSONCandidatesIgnoresBracesInStrings(t *testing.T) {
	got := extractJSONCandidates(`{"title":"use map[string]{} here","n":1}`)
	if len(got) != 1 {
		t.Fatalf("candidates = %v, want 1", got)
	}
	if !strings.HasSuffix(got[0], `"n":1}`) {
		t.Errorf("object was truncated at a brace inside a string: %q", got[0])
	}
}

// TestDowngradeOnlyOnCapabilityErrors is the regression test for one transient
// failure permanently moving the whole run onto the weaker JSON path. The
// client is shared by every batch, so that degradation is global.
func TestDowngradeOnlyOnCapabilityErrors(t *testing.T) {
	transient := []error{
		errors.New("429 Too Many Requests"),
		errors.New("rate limit exceeded"),
		errors.New("500 internal server error"),
		errors.New("upstream overloaded, please retry"),
		errors.New("context deadline exceeded"),
		errors.New("circuit breaker is open"),
		errors.New("connection reset by peer"),

		// The SDK's own wording when GenerateTyped cannot unmarshal what came
		// back. Previously this matched two entries of capabilitySignals at
		// once ("structured output", "schema"), so one garbled reply from a
		// fully schema-capable model downgraded the shared client for the rest
		// of the run. The list above is hand-written provider wording, which is
		// why it never caught this: the string that mattered was ours.
		errors.New("llms: structured output is not valid JSON: invalid character 'I' looking for beginning of value"),
	}
	for _, err := range transient {
		if isCapabilityError(err) {
			t.Errorf("%v must NOT downgrade: it says nothing about schema support", err)
		}
	}

	capability := []error{
		errors.New("400: response_format is not supported by this model"),
		errors.New("json_schema response format unavailable"),
		errors.New("unrecognized request argument: response_format"),
		errors.New("this model does not support structured output"),
	}
	for _, err := range capability {
		if !isCapabilityError(err) {
			t.Errorf("%v SHOULD downgrade: the provider rejected the request shape", err)
		}
	}
}

// TestSchemaPathNeverSilentlyReturnsZero is the same contract as
// TestNeverSilentlyReturnsZeroFindings, asserted through Extract rather than
// through decodeLenient.
//
// That distinction is the whole bug. decodeLenient guards the JSON path and the
// test above calls it directly, so it never touched the SCHEMA path — which
// returned llms.GenerateTyped's value whenever its error was nil, and
// GenerateTyped is a bare json.Unmarshal. `{}`, `null` and an unrelated object
// therefore decoded to an empty Result with a NIL error, the batch was counted
// as SUCCEEDED, and the run reported a clean pull request. auto is the mode
// config.Defaults() sets, so this is the shipped path.
func TestSchemaPathNeverSilentlyReturnsZero(t *testing.T) {
	empty := []string{`{}`, `null`, `{"status":"ok","message":"nothing to do"}`}

	for _, mode := range []config.StructuredMode{config.StructuredAuto, config.StructuredSchema} {
		for _, content := range empty {
			t.Run(string(mode)+"/"+content, func(t *testing.T) {
				// In auto the schema attempt is followed by the JSON fallback,
				// which gets the same unusable content plus one repair turn.
				fake := newFakeLLM(
					turn{content: content}, turn{content: content}, turn{content: content},
				)
				got, err := Extract[result](context.Background(), newTestClient(fake, mode), nil)
				if err == nil {
					t.Fatalf("SILENT ZERO: content %s decoded to %d finding(s) with err==nil", content, len(got.Findings))
				}
			})
		}
	}

	// The boundary: an explicitly empty review is a real answer and must still
	// come back clean, or every genuinely clean diff becomes a failed batch.
	fake := newFakeLLM(turn{content: `{"findings":[]}`})
	got, err := Extract[result](context.Background(), newTestClient(fake, config.StructuredAuto), nil)
	if err != nil {
		t.Fatalf("an explicit empty findings list is a valid review, got: %v", err)
	}
	if len(got.Findings) != 0 {
		t.Fatalf("findings = %d, want 0", len(got.Findings))
	}
	if fake.callCount() != 1 {
		t.Errorf("calls = %d, want 1: a valid schema response must not fall through", fake.callCount())
	}
}

// TestGarbledSchemaResponseDoesNotDowngradeTheClient covers the other half of
// the same defect: recovery must not be permanent.
//
// A response the schema should have prevented says nothing about whether the
// provider supports schemas — under a router, consecutive requests can land on
// different upstreams. Downgrading on one would move every remaining batch of
// the run onto the unenforced JSON path, and nothing in Report records that it
// happened, so half a run would execute under a different strategy than the
// other half with no trace.
func TestGarbledSchemaResponseDoesNotDowngradeTheClient(t *testing.T) {
	// Turn 1: the schema attempt returns prose-wrapped JSON, which the SDK
	// cannot unmarshal. Turn 2: the JSON fallback recovers it leniently.
	fake := newFakeLLM(
		turn{content: "Here you go:\n```json\n" + realAnswer + "\n```"},
		turn{content: realAnswer},
	)
	client := newTestClient(fake, config.StructuredAuto)

	got, err := Extract[result](context.Background(), client, nil)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(got.Findings) != 1 {
		t.Fatalf("findings = %d, want the recovered answer", len(got.Findings))
	}

	if mode := client.structuredMode(); mode != config.StructuredAuto {
		t.Errorf("mode = %q after one garbled response, want %q: the client is shared by "+
			"every batch, so this downgrade is global and permanent", mode, config.StructuredAuto)
	}
}
