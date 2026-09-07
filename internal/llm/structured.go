package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"reflect"
	"sort"
	"strings"

	llms "github.com/nocturnium/llm-go-sdk/v6"

	"github.com/jdziat/open-nitpick/internal/config"
)

// Extract runs a generation and decodes the result into T.
//
// Two strategies are available, because "every provider supports structured
// output" is not true in practice, particularly for the local models this tool
// is meant to support:
//
//   - schema: a JSON-Schema response format derived from T. Preferred, since
//     the provider enforces the shape.
//   - json: JSON mode with the schema described in the prompt, followed by
//     lenient extraction and one bounded repair attempt.
//
// In auto mode the schema path is tried first and a provider that rejects it is
// remembered, so the fallback is paid for at most once per client rather than
// once per request.
func Extract[T any](ctx context.Context, c *Client, msgs []llms.Message, opts ...llms.CallOption) (T, error) {
	var zero T

	if c == nil || c.LLM == nil {
		return zero, errors.New("llm: nil client")
	}

	call := append(c.CallOptions(), opts...)

	switch c.structuredMode() {
	case config.StructuredJSON, config.StructuredText:
		return extractJSON[T](ctx, c, msgs, call)

	case config.StructuredSchema:
		value, raw, err := generateTyped[T](ctx, c, msgs, call)
		if err == nil {
			err = requireSchemaEnforced(raw, value)
		}
		if err != nil {
			return zero, fmt.Errorf("%s: structured output failed: %w", c, err)
		}
		return value, nil

	default: // auto
		value, raw, err := generateTyped[T](ctx, c, msgs, call)
		if err == nil {
			err = requireSchemaEnforced(raw, value)
			if err == nil {
				return value, nil
			}
		}
		// OpenRouter reserves the whole output cap against the key's balance
		// before the call, so with no max_tokens set a model whose cap is
		// 64k needs a dollar of headroom per request, and a key near its
		// daily limit answers 402 with "fewer max_tokens". That is the
		// remaining balance talking, not the model: retry once with a cap
		// that fits, and say so, rather than lose the review.
		if creditCapped(err) && !hasMaxTokens(call) {
			capped := append(append([]llms.CallOption(nil), call...), llms.WithMaxTokens(creditCappedMaxTokens))
			value, raw, err = generateTyped[T](ctx, c, msgs, capped)
			if err == nil {
				if err = requireSchemaEnforced(raw, value); err == nil {
					return value, nil
				}
			}
			call = capped
		}
		// A cancelled context is not a capability problem; retrying under a
		// different strategy would only produce a second, more confusing error.
		if ctx.Err() != nil {
			return zero, err
		}

		switch {
		case schemaNotEnforced(err):
			// The provider took the json_schema request format and answered
			// anyway with something the schema forbids. That is a fact about
			// this RESPONSE, not about the provider, a router hands
			// consecutive requests to different upstreams, so the JSON path
			// is retried for this request only. Deliberately no downgrade:
			// the client is shared by every batch, and half a run executing
			// under a different strategy than the other half is not a result.

		case isCapabilityError(err):
			// The provider rejected the request SHAPE, which is a fact about
			// the provider. Remember it so the fallback is paid for once per
			// client rather than once per request.
			c.downgrade()

		default:
			// A 429 or a 500 says nothing about whether the provider supports
			// schemas, and neither retrying nor downgrading on one is honest.
			return zero, fmt.Errorf("%s: structured output failed: %w", c, err)
		}

		result, jsonErr := extractJSON[T](ctx, c, msgs, call)
		if jsonErr != nil {
			// Report both, since the first error explains why the fallback ran.
			return zero, fmt.Errorf("%s: schema path failed (%w); json fallback failed: %w", c, err, jsonErr)
		}
		return result, nil
	}
}

// generateTyped is llms.GenerateTyped with stall retries.
//
// A stall is the HTTP client's timeout firing mid-body: the request left, the
// provider accepted it, the answer never finished. Two causes look identical
// from here, both met on gemma-4-31b in one eval battery: an upstream hung
// behind the router, which a fresh route answers (15 of 26), and a generation
// running past any sensible length, which loops again unchanged (11 of 26). So
// the first retry carries an output cap when the caller set none. A hung
// upstream answers under it; a runaway comes back cut, and that truncation is
// the signal to sample at a small temperature rather than zero, which the log
// records because such a review will not reproduce. Budget is max_retries.
func generateTyped[T any](ctx context.Context, c *Client, msgs []llms.Message, call []llms.CallOption) (T, *llms.Response, error) {
	for attempt := 0; ; attempt++ {
		value, raw, err := llms.GenerateTyped[T](ctx, c.LLM, msgs, call...)
		if next, ok := c.retryAfter(ctx, attempt, call, raw, err); ok {
			call = next
			continue
		}
		c.logRetryOutcome(attempt, err)
		return value, raw, err
	}
}

// generateContent is GenerateContent with generateTyped's retries.
func generateContent(ctx context.Context, c *Client, msgs []llms.Message, call []llms.CallOption) (*llms.Response, error) {
	for attempt := 0; ; attempt++ {
		resp, err := c.LLM.GenerateContent(ctx, msgs, call...)
		if next, ok := c.retryAfter(ctx, attempt, call, resp, err); ok {
			call = next
			continue
		}
		c.logRetryOutcome(attempt, err)
		return resp, err
	}
}

// retryAfter decides whether an attempt is sent again and with what options.
func (c *Client) retryAfter(ctx context.Context, attempt int, call []llms.CallOption, resp *llms.Response, err error) ([]llms.CallOption, bool) {
	if attempt >= c.stallRetries {
		return nil, false
	}
	switch {
	case stalled(ctx, err):
		next := stallRetryOptions(call)
		c.logger().Warn("request stalled; sending again",
			"model", c.String(),
			"attempt", attempt+1,
			"retries_left", c.stallRetries-attempt,
			"timeout", c.Timeout(),
			"output_capped", !hasMaxTokens(call) && hasMaxTokens(next))
		return next, true
	case attempt > 0 && truncated(resp, err) && !hasTemperatureAbove(call, 0):
		// Only after a retry: a first attempt cut at a cap the caller chose is
		// the caller's to handle. Only from temperature zero: a sampling
		// temperature the caller set is kept, and a loop at 0.3 is the model's
		// answer.
		c.logger().Warn("retried request was cut at the output cap: runaway generation; sending again at temperature 0.3",
			"model", c.String(),
			"attempt", attempt+1,
			"retries_left", c.stallRetries-attempt)
		return append(append([]llms.CallOption(nil), call...), llms.WithTemperature(runawayRetryTemperature)), true
	}
	return nil, false
}

// runawayRetryTemperature is what a request is re-sampled at after a
// truncated retry. Small, because the goal is a different path through the
// same answer, not a different answer.
const runawayRetryTemperature = 0.3

// truncated reports a response cut at a length limit: the provider says so,
// or the body ends mid-JSON.
func truncated(resp *llms.Response, err error) bool {
	if resp != nil && resp.FinishReason == llms.FinishReasonLength {
		return true
	}
	return err != nil && strings.Contains(err.Error(), "unexpected end of JSON input")
}

// hasTemperatureAbove reports whether the options set a temperature above t.
func hasTemperatureAbove(opts []llms.CallOption, t float64) bool {
	applied := llms.ApplyOptions(opts...)
	return applied.Temperature != nil && *applied.Temperature > t
}

// logRetryOutcome records how a request that was retried ended, so the log
// says which attempt answered and whether any did.
func (c *Client) logRetryOutcome(attempt int, err error) {
	switch {
	case attempt == 0:
	case err == nil:
		c.logger().Warn("request answered after retries", "model", c.String(), "attempts", attempt+1)
	default:
		c.logger().Error("request failed on every attempt", "model", c.String(), "attempts", attempt+1, "err", err)
	}
}

// stallRetryOptions caps the output of a retried request when the caller set
// no cap, at the same size the 402 path retries with. A cap the caller chose
// is kept as chosen.
func stallRetryOptions(call []llms.CallOption) []llms.CallOption {
	if hasMaxTokens(call) {
		return call
	}
	return append(append([]llms.CallOption(nil), call...), llms.WithMaxTokens(creditCappedMaxTokens))
}

// stalled reports a client-side timeout on a request whose caller is still
// waiting. The HTTP client reports its own deadline as a net.Error with
// Timeout() true, wrapped in whatever the SDK adds; the string check is for
// the SDK paths that flatten the error before wrapping it.
func stalled(ctx context.Context, err error) bool {
	if err == nil || ctx.Err() != nil {
		return false
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return true
	}
	return strings.Contains(err.Error(), "Client.Timeout")
}

// creditCappedMaxTokens is the output cap retried with when the provider
// refuses to reserve the model's full cap against the key's balance. Large
// enough for any review this tool asks for; small enough to fit a key with a
// few dollars left.
const creditCappedMaxTokens = 16384

// creditCapped reports whether an error is OpenRouter's 402 asking for a
// smaller max_tokens.
func creditCapped(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "402") && strings.Contains(msg, "max_tokens")
}

// hasMaxTokens reports whether the call already names an output cap.
func hasMaxTokens(opts []llms.CallOption) bool {
	var co llms.CallOptions
	for _, o := range opts {
		o(&co)
	}
	return co.MaxTokens != nil
}

// errSchemaNotEnforced marks a response that came back through the
// schema-constrained path without satisfying the schema.
var errSchemaNotEnforced = errors.New("provider did not enforce the response schema")

// requireSchemaEnforced rejects a schema-path response that decoded cleanly and
// carries nothing. GenerateTyped decodes with a bare json.Unmarshal, so `{}`,
// `null` and any unrelated object reach a struct as its zero value with no
// error; without this guard a batch that produced nothing is recorded as
// succeeded, never reaches Report.Incomplete, and the run exits 0 calling the
// pull request clean. decodeLenient rejects those three shapes on the JSON
// path. A router that forwards response_format to whichever upstream it picked
// without requiring that upstream to honor it is what makes an unenforced
// schema response reachable in a shipped configuration.
func requireSchemaEnforced[T any](resp *llms.Response, value T) error {
	if resp == nil {
		return fmt.Errorf("%w: empty response", errSchemaNotEnforced)
	}
	if err := requirePopulated(strings.TrimSpace(resp.Content), value); err != nil {
		return fmt.Errorf("%w: %w", errSchemaNotEnforced, err)
	}
	return nil
}

// schemaNotEnforced reports whether an error means the schema was accepted and
// then not honored, as opposed to rejected outright.
//
// Two sources, one meaning: the check above, and the SDK's own decode failure
// when the model answered with something that is not JSON at all. The latter is
// matched on text because the SDK returns it untyped.
func schemaNotEnforced(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, errSchemaNotEnforced) ||
		strings.Contains(strings.ToLower(err.Error()), sdkSchemaParseFailure)
}

// sdkSchemaParseFailure is the SDK's wording for "GenerateTyped could not
// unmarshal what came back" (llms: structured output is not valid JSON: ...).
const sdkSchemaParseFailure = "structured output is not valid json"

// extractJSON asks for JSON mode with the target schema described in the
// prompt, then parses leniently. Providers in this path do not enforce the
// schema, so a single repair round-trip handles the common failure of a model
// wrapping JSON in prose or emitting a trailing comma.
func extractJSON[T any](ctx context.Context, c *Client, msgs []llms.Message, opts []llms.CallOption) (T, error) {
	var zero T

	// Prefer a caller-supplied schema so both strategies describe the same
	// contract. Deriving it from T here would silently disagree with the
	// schema the provider was given, notably about which fields are required.
	schema := suppliedSchema(opts)
	if schema == nil {
		var err error
		if schema, err = llms.SchemaFrom[T](); err != nil {
			return zero, fmt.Errorf("derive schema: %w", err)
		}
	}

	prompted := withSchemaInstruction(msgs, schema)
	// Read once: batches share the client, and one batch downgrading it
	// between this call's construction and its error check made the other
	// batch skip the fallback for a rejection it had just received.
	textMode := c.structuredMode() == config.StructuredText
	call := append([]llms.CallOption(nil), opts...)
	if textMode {
		call = append(call, withoutResponseFormat())
	} else {
		call = append(call, llms.WithJSONMode())
	}

	resp, err := generateContent(ctx, c, prompted, call)
	if err != nil && !textMode && isCapabilityError(err) && ctx.Err() == nil {
		// json_object rejected too. The schema is already in the prompt and
		// the decoder already tolerates prose around the object, so the
		// request goes again with no response_format, and the client
		// remembers so the next batch does not pay for the rejection.
		c.downgradeToText()
		call = append(append([]llms.CallOption(nil), opts...), withoutResponseFormat())
		resp, err = generateContent(ctx, c, prompted, call)
	}
	if err != nil {
		return zero, fmt.Errorf("%s: %w", c, err)
	}
	if resp == nil {
		return zero, fmt.Errorf("%s: empty response", c)
	}

	value, parseErr := decodeLenient[T](resp.Content)
	if parseErr == nil {
		return value, nil
	}
	if ctx.Err() != nil {
		return zero, parseErr
	}

	// Repair: show the model its own output and the parse error.
	repair := append(prompted,
		llms.Message{Role: llms.RoleAssistant, Content: resp.Content},
		llms.Message{Role: llms.RoleUser, Content: fmt.Sprintf(
			"That response could not be parsed: %v\n\n"+
				"Reply again with the corrected JSON object only. No prose, no markdown fences.",
			parseErr)},
	)

	retry, err := generateContent(ctx, c, repair, call)
	if err != nil {
		return zero, fmt.Errorf("%s: repair attempt failed: %w (original parse error: %w)", c, err, parseErr)
	}
	if retry == nil {
		return zero, fmt.Errorf("%s: repair attempt returned an empty response (original parse error: %w)", c, parseErr)
	}

	value, err = decodeLenient[T](retry.Content)
	if err != nil {
		return zero, fmt.Errorf("%s: response was not valid JSON after one repair attempt: %w", c, err)
	}
	return value, nil
}

// suppliedSchema returns the JSON Schema a caller attached to the options, or
// nil when there is none.
func suppliedSchema(opts []llms.CallOption) json.RawMessage {
	applied := llms.ApplyOptions(opts...)
	if applied.ResponseFormat == nil || applied.ResponseFormat.JSONSchema == nil {
		return nil
	}
	return applied.ResponseFormat.JSONSchema.Schema
}

// withSchemaInstruction appends a system message carrying the target schema.
// It is appended rather than merged into an existing system message so callers
// keep full control over their own prompt text.
func withSchemaInstruction(msgs []llms.Message, schema json.RawMessage) []llms.Message {
	instruction := llms.Message{
		Role: llms.RoleSystem,
		Content: "Reply with a single JSON object conforming to this JSON Schema. " +
			"Output only the object: no prose, no explanation, no markdown code fences.\n\n" +
			string(schema),
	}

	out := make([]llms.Message, 0, len(msgs)+1)
	out = append(out, msgs...)
	return append(out, instruction)
}

// decodeLenient unmarshals JSON that a model may have wrapped in fences or
// prose. Strict decoding is tried first so well-behaved output costs nothing.
//
// The hard requirement here is that a failure must be LOUD. Returning a zero
// value with a nil error is the worst possible outcome: the engine records no
// error, the failure counter never increments, and the run reports a clean pull
// request while the model's actual findings are discarded. Every path below
// either returns a populated value or an error.
func decodeLenient[T any](content string) (T, error) {
	var zero T

	trimmed := stripReasoning(strings.TrimSpace(content))
	if trimmed == "" {
		return zero, errors.New("model returned empty content")
	}

	// Strict first: well-behaved output costs nothing extra.
	trimmed = unwrapSingleElement[T](trimmed)

	var value T
	if err := json.Unmarshal([]byte(trimmed), &value); err == nil {
		if err := requirePopulated(trimmed, value); err != nil {
			return zero, err
		}
		return value, nil
	}
	// A model writing a rationale with a real tab or newline inside the
	// string, rather than the escape, produces JSON that no parser accepts
	// and every reader understands. Escape those and try once more before
	// hunting for candidates: gemma-4-31b did this on most replies when no
	// response_format was constraining it.
	if escaped := escapeControlChars(trimmed); escaped != trimmed {
		if err := json.Unmarshal([]byte(escaped), &value); err == nil {
			if err := requirePopulated(escaped, value); err != nil {
				return zero, err
			}
			return value, nil
		}
		trimmed = escaped
	}

	// Otherwise consider every candidate object in the text, preferring the
	// last one that carries the fields we asked for. Taking the first
	// balanced object is what let prose like "uses a map[string]struct{}" or a
	// leading "Analysis: {}" swallow the real answer.
	candidates := extractJSONCandidates(trimmed)
	if len(candidates) == 0 {
		return zero, fmt.Errorf("no JSON object found in response (%s)", snippet(trimmed))
	}

	var lastErr error
	for i := len(candidates) - 1; i >= 0; i-- {
		text := unwrapSingleElement[T](candidates[i])

		var candidateValue T
		if err := json.Unmarshal([]byte(text), &candidateValue); err != nil {
			lastErr = err
			continue
		}
		if err := requirePopulated(text, candidateValue); err != nil {
			lastErr = err
			continue
		}
		return candidateValue, nil
	}

	if lastErr != nil {
		return zero, fmt.Errorf("no JSON object in the response matched the expected shape: %w (%s)",
			lastErr, snippet(trimmed))
	}
	return zero, fmt.Errorf("no JSON object in the response matched the expected shape (%s)", snippet(trimmed))
}

// requirePopulated rejects a document that decoded successfully but carries
// none of the target's fields.
//
// Go's json.Unmarshal happily decodes `{}`, `null`, or an unrelated object into
// any struct, yielding a zero value and no error. For a review that means
// "found nothing", indistinguishable from a clean diff. Requiring at
// least one recognized key forces those into the repair path instead.
func requirePopulated[T any](raw string, value T) error {
	if strings.TrimSpace(raw) == "null" {
		return errors.New("model returned JSON null")
	}

	var probe map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &probe); err != nil {
		// Not an object (an array, a scalar). Decoding into a struct would
		// have failed already, so anything reaching here is not our shape.
		return fmt.Errorf("expected a JSON object, got %s", snippet(raw))
	}
	if len(probe) == 0 {
		return errors.New("model returned an empty JSON object")
	}

	known := knownFields(value)
	for key := range probe {
		if known[strings.ToLower(key)] {
			return nil
		}
	}

	keys := make([]string, 0, len(probe))
	for k := range probe {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	return fmt.Errorf("JSON object has none of the expected fields (got keys: %s)", strings.Join(keys, ", "))
}

// knownFields returns the JSON field names of T, lowercased.
func knownFields(value any) map[string]bool {
	out := map[string]bool{}

	t := reflect.TypeOf(value)
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil || t.Kind() != reflect.Struct {
		return out
	}

	for i := range t.NumField() {
		field := t.Field(i)
		if field.PkgPath != "" {
			continue
		}

		name := field.Name
		if tag := field.Tag.Get("json"); tag != "" {
			if before, _, _ := strings.Cut(tag, ","); before != "" {
				if before == "-" {
					continue
				}
				name = before
			}
		}
		out[strings.ToLower(name)] = true
	}

	return out
}

// stripReasoning removes the thinking preamble reasoning models emit before
// their answer. Without this, an unbalanced brace inside the reasoning text
// defeats extraction entirely and the batch fails outright.
func stripReasoning(s string) string {
	for _, pair := range [][2]string{
		{"<think>", "</think>"},
		{"<thinking>", "</thinking>"},
		{"<reasoning>", "</reasoning>"},
		{"<|channel|>analysis", "<|channel|>final"},
	} {
		for {
			start := strings.Index(s, pair[0])
			if start < 0 {
				break
			}
			end := strings.Index(s[start:], pair[1])
			if end < 0 {
				// Unterminated: drop everything up to the opener and stop, so
				// a truncated reasoning block cannot swallow the answer.
				s = s[:start]
				break
			}
			s = s[:start] + s[start+end+len(pair[1]):]
		}
	}

	return strings.TrimSpace(s)
}

// escapeControlChars rewrites raw control characters that sit inside JSON
// string literals as their escapes, and leaves everything outside strings
// alone. It tracks the backslash so an existing escape is not doubled.
func escapeControlChars(s string) string {
	var b strings.Builder
	inString, escaped := false, false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case escaped:
			escaped = false
		case inString && ch == '\\':
			escaped = true
		case ch == '"':
			inString = !inString
		case inString && ch < 0x20:
			switch ch {
			case '\t':
				b.WriteString(`\t`)
			case '\n':
				b.WriteString(`\n`)
			case '\r':
				b.WriteString(`\r`)
			default:
				fmt.Fprintf(&b, `\u%04x`, ch)
			}
			continue
		}
		b.WriteByte(ch)
	}
	return b.String()
}

// extractJSONCandidates returns every balanced top-level JSON object or array
// in the text, in the order they appear, ignoring braces inside strings.
//
// All of them are returned rather than just the first because a model's answer
// is routinely preceded by a brace pair that is not the answer: prose
// mentioning `map[string]struct{}`, a leading `Analysis: {}`, or an echo of the
// requested schema. The caller picks the one that fits the target shape.
// unwrapSingleElement returns the one element of a single-element JSON array,
// when the value being decoded into is not itself a slice.
//
// Some OpenRouter upstreams wrap a schema-constrained object in an array, and
// they are the ones carrying the largest context, so routing prefers them for
// the batches a lost review costs most on. See issue #46.
//
// A slice caller is left alone: for them the array IS the answer.
func unwrapSingleElement[T any](text string) string {
	var zero T
	switch reflect.ValueOf(&zero).Elem().Kind() {
	case reflect.Slice, reflect.Array, reflect.Interface:
		return text
	}

	if !strings.HasPrefix(strings.TrimSpace(text), "[") {
		return text
	}

	var elems []json.RawMessage
	if err := json.Unmarshal([]byte(text), &elems); err != nil || len(elems) != 1 {
		return text
	}
	return string(elems[0])
}

func extractJSONCandidates(s string) []string {
	s = stripCodeFence(s)

	var out []string
	for i := 0; i < len(s); i++ {
		if s[i] != '{' && s[i] != '[' {
			continue
		}

		end, ok := matchBalanced(s, i)
		if !ok {
			// Unterminated from here; nothing later can close it either.
			break
		}

		out = append(out, s[i:end+1])

		// Skip past this value rather than descending into it: a nested object
		// cannot be the whole answer.
		i = end
	}

	return out
}

// matchBalanced returns the index of the byte closing the bracket that opens at
// start, honoring JSON string and escape rules.
func matchBalanced(s string, start int) (int, bool) {
	open := s[start]
	close := byte('}')
	if open == '[' {
		close = ']'
	}

	var (
		depth    int
		inString bool
		escaped  bool
	)

	for i := start; i < len(s); i++ {
		ch := s[i]

		if escaped {
			escaped = false
			continue
		}

		switch {
		case inString && ch == '\\':
			escaped = true
		case ch == '"':
			inString = !inString
		case inString:
			// Brackets inside strings must not affect depth.
		case ch == open:
			depth++
		case ch == close:
			depth--
			if depth == 0 {
				return i, true
			}
		}
	}

	return 0, false
}

// stripCodeFence removes a surrounding markdown fence, with or without a
// language tag.
func stripCodeFence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}

	// Drop the opening fence line, which may carry a language tag.
	if nl := strings.IndexByte(s, '\n'); nl >= 0 {
		s = s[nl+1:]
	} else {
		return s
	}

	if end := strings.LastIndex(s, "```"); end >= 0 {
		s = s[:end]
	}
	return strings.TrimSpace(s)
}

// snippet truncates text for inclusion in an error message.
func snippet(s string) string {
	const max = 160

	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= max {
		return "got: " + s
	}
	return "got: " + s[:max] + "…"
}

// structuredMode reports the strategy to use for the next call.
func (c *Client) structuredMode() config.StructuredMode {
	c.modeMu.RLock()
	defer c.modeMu.RUnlock()
	return c.mode
}

// withoutResponseFormat clears any response_format an earlier option set.
// The caller's options carry the json_schema request the schema path was
// built with, and applied after them this is what makes "no format" true
// rather than "the schema format again", which is how the first text-mode
// fallback re-sent the very header the provider had just rejected.
func withoutResponseFormat() llms.CallOption {
	return func(o *llms.CallOptions) { o.ResponseFormat = nil }
}

// downgradeToText records that JSON mode does not work for this provider
// either. Auto and json both downgrade: json was auto's own fallback, and an
// operator who chose json outright asked for a JSON reply, which text mode
// still delivers, not for the header that requested it.
func (c *Client) downgradeToText() {
	c.modeMu.Lock()
	defer c.modeMu.Unlock()

	if c.mode == config.StructuredAuto || c.mode == config.StructuredJSON {
		c.mode = config.StructuredText
	}
}

// downgrade records that the schema path does not work for this provider, so
// later calls go straight to JSON mode. Only auto mode downgrades; an explicit
// choice is respected.
func (c *Client) downgrade() {
	c.modeMu.Lock()
	defer c.modeMu.Unlock()

	if c.mode == config.StructuredAuto {
		c.mode = config.StructuredJSON
	}
}
