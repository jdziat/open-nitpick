package review

import (
	"context"
	"errors"
	"strings"
)

// Stage failures: a required stage that did not complete, and the sanitized
// kind that is safe to publish.

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

	// Matched on the error's text because the SDK reports a rate limit as a
	// status inside a message rather than as a type this package can name
	// without depending on it. A kind that is not recognised is reported as
	// unrecognised, never quoted.
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "429"), strings.Contains(msg, "rate limit"):
		return "rate-limited"
	case strings.Contains(msg, "timeout"), strings.Contains(msg, "deadline"):
		return "timeout"
	case strings.Contains(msg, "401"), strings.Contains(msg, "403"),
		strings.Contains(msg, "api key"):
		return "credential"
	case strings.Contains(msg, "500"), strings.Contains(msg, "502"),
		strings.Contains(msg, "503"), strings.Contains(msg, "504"):
		return "provider-error"
	case strings.Contains(msg, "json"), strings.Contains(msg, "schema"):
		return "malformed-output"
	default:
		return "failed"
	}
}
