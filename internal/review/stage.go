package review

import (
	"context"
	"errors"
	"net/http"
	"strings"

	llms "github.com/nocturnium/llm-go-sdk/v6"
)

// Stage failures: a required stage that did not complete, and the sanitized
// kind that is safe to publish.

// errStageDegraded marks a stage failure that leaves usable output behind.
//
// It travels as a wrapped error rather than a widened return, so a caller that
// forgets to look at it still sees an error rather than a nil, and the one
// caller that does look at it says so in a named branch. The failure this
// closes went the other way: triage returned nil, and every reader downstream
// was right to believe it.
var errStageDegraded = errors.New("review: a required stage did not complete")

// errorKind reduces an error to a word that can be published.
//
// A stage's reason reaches a pull request comment and the Action's outputs,
// and what a gateway returns on failure is untrusted text of its choosing. The
// kinds below are this code's own vocabulary, chosen from the shape of the
// error rather than copied out of it.
func errorKind(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	}

	// The status when the SDK typed the error, which is the only place a
	// status can be read without guessing.
	var apiErr *llms.APIError
	if errors.As(err, &apiErr) {
		switch {
		case apiErr.StatusCode == http.StatusTooManyRequests:
			return "rate-limited"
		case apiErr.StatusCode == http.StatusUnauthorized,
			apiErr.StatusCode == http.StatusForbidden:
			return "credential"
		case apiErr.StatusCode >= 500:
			return "provider-error"
		}
	}

	// Otherwise words, never numbers. A provider's message routinely carries
	// milliseconds and token counts, so matching "503" anywhere in it reads
	// "retry after 5034 ms" as a server error and publishes that word on a
	// pull request.
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "rate limit"), strings.Contains(msg, "too many requests"):
		return "rate-limited"
	case strings.Contains(msg, "timeout"), strings.Contains(msg, "deadline"):
		return "timeout"
	case strings.Contains(msg, "api key"), strings.Contains(msg, "unauthorized"),
		strings.Contains(msg, "forbidden"):
		return "credential"
	case strings.Contains(msg, "json"), strings.Contains(msg, "schema"):
		return "malformed-output"
	default:
		return "failed"
	}
}
