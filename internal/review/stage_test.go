package review

import (
	"errors"
	"strings"
	"testing"
)

// A provider's message carries milliseconds and token counts, and a status
// code matched anywhere inside one reads "retry after 5034 ms" as a server
// error. The kind reaches a pull request comment, so a wrong one is published.
func TestErrorKindDoesNotReadDigitsOutOfAMessage(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{errors.New("upstream asked to retry after 5034 ms"), "failed"},
		{errors.New("queued, retry in 4290 ms"), "failed"},
		{errors.New("429 too many requests"), "rate-limited"},
		{errors.New("rate limit exceeded for this key"), "rate-limited"},
		{errors.New("context deadline exceeded"), "timeout"},
		{errors.New("invalid api key"), "credential"},
		{errors.New("structured output is not valid json"), "malformed-output"},
	} {
		if got := errorKind(tc.err); got != tc.want {
			t.Errorf("errorKind(%q) = %q, want %q", tc.err, got, tc.want)
		}
	}
}

// The reason is published, so it must never be the provider's own words.
func TestErrorKindNeverQuotesTheProvider(t *testing.T) {
	err := errors.New("your prompt was: SECRET-CANARY, and the account is over quota")
	got := errorKind(err)
	if strings.Contains(got, "CANARY") || strings.Contains(got, "prompt") {
		t.Errorf("errorKind leaked the message: %q", got)
	}
}
