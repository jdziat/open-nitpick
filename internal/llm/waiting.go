package llm

import (
	"context"
	"log/slog"
	"time"
)

// Saying that a request is still out.
//
// A nonstreaming call decodes one complete JSON body, so a model that reasons
// for five minutes produces nothing observable until it is done. The engine
// logs a batch starting and a batch finishing, and between them is a gap that
// looks identical to a hung process. Issue #81 measured 298.5 seconds to first
// substantive bytes on a request that was working the whole time.
//
// This says only that the wait continues. It is not evidence that inference is
// progressing, and the line says so in those words, because a progress message
// that implies more than it knows is how a stall gets mistaken for work.

// waitInterval is how often a pending request reports itself.
//
// Thirty seconds is short enough that a reader waiting on a slow call sees the
// second line before deciding the run is stuck, and long enough that an
// ordinary review of a few seconds prints nothing at all.
const waitInterval = 30 * time.Second

// whileWaiting runs f, reporting elapsed time on log until it returns.
//
// The ticker stops when f does, including when f panics, so a caller cannot
// leave a goroutine printing about a request nobody is waiting for.
// every is a parameter rather than a package variable a test can reassign.
// The variable version raced: the reporting goroutine read it while a test
// restored it, and -race said so.
func whileWaiting(ctx context.Context, log *slog.Logger, stage, model string, every time.Duration, f func() error) error {
	if log == nil || every <= 0 {
		return f()
	}

	done := make(chan struct{})
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()

		// A second ctx check inside the tick arm was tried and removed. The
		// arm above returns on the first pass once the context is done, before
		// any tick, so the extra check never ran and no test could reach it.
		started := time.Now()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case now := <-t.C:
				log.Info("still waiting on the model",
					"stage", stage,
					"model", model,
					"waited", now.Sub(started).Round(time.Second),
					"note", "the request has not answered; this is elapsed time, not progress")
			}
		}
	}()
	defer close(done)

	return f()
}
