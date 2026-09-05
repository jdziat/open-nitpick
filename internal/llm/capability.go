package llm

import (
	"errors"
	"net"
	"strings"

	llms "github.com/nocturnium/llm-go-sdk/v6"
)

// capabilitySignals are substrings a provider uses when it rejects the request
// *shape* rather than failing to serve it.
//
// Providers do not agree on an error code for this, so matching text is the
// only portable option. The list is deliberately specific: a false positive
// here permanently downgrades output quality for the rest of the run, so
// anything ambiguous is treated as transient instead.
var capabilitySignals = []string{
	"response_format",
	"json_schema",
	"structured output",
	"structured_output",
	"does not support",
	"not supported",
	"unsupported parameter",
	"unrecognized request argument",
	"unknown parameter",
	"invalid parameter",
	"schema",
}

// isCapabilityError reports whether an error means "this model cannot do
// schema-constrained output", as opposed to "this request happened to fail".
//
// Only the former justifies the one-way downgrade to JSON mode. Transient
// failures — rate limits, server errors, timeouts, an open circuit breaker —
// must not change strategy: the client is shared across every batch, so one bad
// minute would otherwise move the entire run onto the unenforced path.
func isCapabilityError(err error) bool {
	if err == nil {
		return false
	}

	// Transport-level problems are never capability problems.
	var netErr net.Error
	if errors.As(err, &netErr) {
		return false
	}

	// A rejected request shape is a 4xx. Rate limiting is the 4xx that is not.
	var apiErr *llms.APIError
	if errors.As(err, &apiErr) {
		switch {
		case apiErr.StatusCode == 429:
			return false
		case apiErr.StatusCode >= 500:
			return false
		case apiErr.StatusCode >= 400:
			return matchesCapabilitySignal(err.Error())
		}
		return false
	}

	return matchesCapabilitySignal(err.Error())
}

// matchesCapabilitySignal reports whether the message names a shape rejection.
func matchesCapabilitySignal(msg string) bool {
	lower := strings.ToLower(msg)

	// Our own decode failure, not the provider's rejection. The SDK reports it
	// as "llms: structured output is not valid JSON: ...", which contains two
	// of the signals below, so a fully schema-capable model that returned one
	// garbled reply was classified as incapable and downgraded the shared
	// client for every remaining batch of the run — the exact outcome the
	// comment above says must not happen. Checked first, because the signal
	// list cannot be made narrow enough to exclude it.
	if strings.Contains(lower, sdkSchemaParseFailure) {
		return false
	}

	// Rate-limit and overload wording sometimes reaches us without a typed
	// error; those must never downgrade.
	for _, transient := range []string{
		"rate limit", "rate_limit", "too many requests", "overloaded",
		"timeout", "timed out", "deadline exceeded", "circuit", "temporarily",
		"connection reset", "eof",
	} {
		if strings.Contains(lower, transient) {
			return false
		}
	}

	for _, signal := range capabilitySignals {
		if strings.Contains(lower, signal) {
			return true
		}
	}
	return false
}
