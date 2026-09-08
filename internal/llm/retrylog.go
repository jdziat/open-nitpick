package llm

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	llms "github.com/nocturnium/llm-go-sdk/v6"
)

// Observing the SDK's own retries.
//
// The resilience wrapper retries a transient failure up to max_retries times
// and writes nothing anywhere, so a request that was rate limited three times
// and then answered is indistinguishable in a log from one that answered at
// once. A review that took eleven minutes said only that it took eleven
// minutes. The SDK has always offered a callback for this; nothing passed one.

// retryLog receives the SDK's retry callback.
//
// It is built before the Client that owns it, because the resilient client has
// to exist before the Client wrapping it does, so the logger arrives later
// through set. The pointer is atomic: WithLogger walks the roster from one
// goroutine while requests may already be in flight from another.
type retryLog struct {
	model string
	log   atomic.Pointer[slog.Logger]
}

// set points the observer at l. A nil logger leaves the previous one, since
// the alternative is losing lines to an ordering nobody controls.
func (r *retryLog) set(l *slog.Logger) {
	if r != nil && l != nil {
		r.log.Store(l)
	}
}

// observe records one retry: which attempt failed, why, and how long the SDK
// will wait before the next.
//
// attempt is 1-based and counts the attempt that just failed, so the line
// reads as a report rather than a prediction. There is deliberately no line
// for the outcome: the callback fires before each sleep and never after the
// last attempt, so a success and an exhaustion look identical from here, and
// structured.go already reports both.
func (r *retryLog) observe(attempt int, err error, delay time.Duration) {
	if r == nil {
		return
	}
	l := r.log.Load()
	if l == nil {
		return
	}

	args := []any{
		"model", r.model,
		"attempt", attempt,
		"kind", retryKind(err),
		"delay", delay,
	}
	// The status and the server's own Retry-After when the SDK could parse
	// them, which is what distinguishes "the provider is shedding load" from
	// "the connection broke".
	var apiErr *llms.APIError
	if errors.As(err, &apiErr) {
		if apiErr.StatusCode != 0 {
			args = append(args, "status", apiErr.StatusCode)
		}
		if apiErr.RetryAfter > 0 {
			args = append(args, "retry_after", apiErr.RetryAfter)
			// The SDK clamps a retry delay at 30 seconds, so a provider
			// asking for longer is asked again early. Worth saying where it
			// happens rather than leaving the two numbers to be compared.
			if apiErr.RetryAfter > delay {
				args = append(args, "asked_for_longer", true)
			}
		}
	}
	l.Warn("provider retried the request", args...)
}

// retryKind names why an attempt failed, in this package's vocabulary.
//
// The error's own text is never logged. A provider's message is its choosing,
// it can be long, and it has been known to echo the request; a retry line is
// read by someone asking what is happening, not what was sent.
func retryKind(err error) string {
	if err == nil {
		return "unknown"
	}
	var apiErr *llms.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case http.StatusTooManyRequests:
			return "rate-limited"
		case http.StatusRequestTimeout, http.StatusGatewayTimeout:
			return "timeout"
		case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable:
			return "provider-error"
		}
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "timeout"), strings.Contains(msg, "deadline"):
		return "timeout"
	case strings.Contains(msg, "connection reset"), strings.Contains(msg, "connection refused"),
		strings.Contains(msg, "eof"):
		return "connection"
	default:
		return "transient"
	}
}
