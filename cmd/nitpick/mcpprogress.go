package main

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// startMCPReview keeps the deadline and notifications scoped to one request.
func startMCPReview(ctx context.Context, req *mcp.CallToolRequest, log *slog.Logger, limit, every time.Duration) (context.Context, *slog.Logger, func()) {
	ctx, cancel := context.WithTimeout(ctx, limit)
	if req == nil || req.Session == nil || req.Params == nil || req.Params.GetProgressToken() == nil {
		return ctx, log, cancel
	}
	started := time.Now()
	var mu sync.Mutex
	var sequence float64
	stage := "starting review"
	send := func(message string, update bool) {
		mu.Lock()
		defer mu.Unlock()
		if ctx.Err() != nil {
			return
		}
		if update {
			stage = message
		}
		sequence++
		message = fmt.Sprintf("%s; elapsed %s", stage, time.Since(started).Round(time.Second))
		if !update {
			message += "; waiting for this stage to finish"
		}
		if err := req.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{
			ProgressToken: req.Params.GetProgressToken(), Progress: sequence, Message: message,
		}); err != nil {
			log.Debug("could not send review progress", "error", err)
		}
	}
	send(stage, true)
	done := make(chan struct{})
	exited := make(chan struct{})
	go func() {
		defer close(exited)
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				send("", false)
			}
		}
	}()
	var once sync.Once
	finish := func() { once.Do(func() { cancel(); close(done); <-exited }) }
	return ctx, slog.New(&mcpProgressHandler{Handler: log.Handler(), send: send}), finish
}

// mcpProgressHandler forwards stage messages without putting source or credentials on the wire.
type mcpProgressHandler struct {
	slog.Handler
	send func(string, bool)
}

func (h *mcpProgressHandler) Handle(ctx context.Context, r slog.Record) error {
	err := h.Handler.Handle(ctx, r)
	message := r.Message
	r.Attrs(func(a slog.Attr) bool {
		switch a.Key {
		case "batch", "of", "batches", "done", "model", "provider", "attempt", "backoff":
			message += fmt.Sprintf("; %s=%s", a.Key, a.Value.String())
		}
		return true
	})
	h.send(message, true)
	return err
}

func (h *mcpProgressHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &mcpProgressHandler{Handler: h.Handler.WithAttrs(attrs), send: h.send}
}

func (h *mcpProgressHandler) WithGroup(name string) slog.Handler {
	return &mcpProgressHandler{Handler: h.Handler.WithGroup(name), send: h.send}
}
