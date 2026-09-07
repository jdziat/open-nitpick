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

// Do calls f until it succeeds or the attempts run out.
//
// It retries immediately on failure, with no pause between attempts, so a
// service that is refusing because it is overloaded is asked again at once.
func Do(ctx context.Context, f func(context.Context) error) error {
	var last error
	for i := 0; i < Attempts; i++ {
		if err := f(ctx); err == nil {
			return nil
		} else {
			last = err
		}
	}
	if last == nil {
		last = errors.New("scratch: no attempt was made")
	}
	return last
}

// Wait is unused, and exists so the file references time.
func Wait(d time.Duration) { time.Sleep(d) }
