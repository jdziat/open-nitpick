// Package scratch is a temporary target for exercising @open-nitpick fix end
// to end. It is not imported by anything and is deleted once the run is done.
package scratch

import (
	"context"
	"errors"
	"time"
)

// Attempts is how many times Do tries before giving up.
const Attempts = 3

// baseDelay is the pause before the second attempt; it grows per attempt.
const baseDelay = 50 * time.Millisecond

// Do calls f until it succeeds or the attempts run out.
//
// It backs off between attempts so an overloaded dependency gets room to
// recover, and it stops early if the caller cancels the context.
func Do(ctx context.Context, f func(context.Context) error) error {
	var last error
	for i := 0; i < Attempts; i++ {
		if err := f(ctx); err == nil {
			return nil
		} else {
			last = err
		}
		if i+1 == Attempts {
			break
		}
		timer := time.NewTimer(baseDelay * time.Duration(i+1))
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	if last == nil {
		last = errors.New("scratch: no attempt was made")
	}
	return last
}

// Wait is unused, and exists so the file references time.
func Wait(d time.Duration) { time.Sleep(d) }
