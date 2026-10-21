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
	var notify func(context.Context, float64, string) error
	if req != nil && req.Session != nil && req.Params != nil && req.Params.GetProgressToken() != nil {
		token := req.Params.GetProgressToken()
		notify = func(ctx context.Context, progress float64, message string) error {
			return req.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{ProgressToken: token, Progress: progress, Message: message})
		}
	}
	return startMCPProgress(ctx, log, limit, every, notify)
}

func startMCPProgress(ctx context.Context, log *slog.Logger, limit, every time.Duration, notify func(context.Context, float64, string) error) (context.Context, *slog.Logger, func()) {
	ctx, cancel := context.WithTimeout(ctx, limit)
	if notify == nil {
		return ctx, log, cancel
	}
	started := time.Now()
	var mu sync.Mutex
	stage := "starting review"
	updates := make(chan struct{}, 1)
	update := func(message string) {
		mu.Lock()
		stage = message
		mu.Unlock()
		// Coalesce stages while a client is slow rather than blocking review work.
		select {
		case updates <- struct{}{}:
		default:
		}
	}
	exited := make(chan struct{})
	go func() {
		defer close(exited)
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		var sequence float64
		send := func(waiting bool) {
			if ctx.Err() != nil {
				return
			}
			mu.Lock()
			message := stage
			mu.Unlock()
			message = fmt.Sprintf("%s; elapsed %s", message, time.Since(started).Round(time.Second))
			if waiting {
				message += "; waiting for this stage to finish"
			}
			sequence++
			if err := notify(ctx, sequence, message); err != nil && ctx.Err() == nil {
				log.Debug("could not send review progress", "error", err)
			}
		}
		send(false)
		for {
			select {
			case <-ctx.Done():
				return
			case <-updates:
				send(false)
			case <-ticker.C:
				send(true)
			}
		}
	}()
	var once sync.Once
	finish := func() { once.Do(func() { cancel(); <-exited }) }
	return ctx, slog.New(&mcpProgressHandler{Handler: log.Handler(), update: update}), finish
}

// mcpProgressHandler forwards stage messages without putting source or credentials on the wire.
type mcpProgressHandler struct {
	slog.Handler
	update func(string)
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
	h.update(message)
	return err
}

func (h *mcpProgressHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &mcpProgressHandler{Handler: h.Handler.WithAttrs(attrs), update: h.update}
}

func (h *mcpProgressHandler) WithGroup(name string) slog.Handler {
	return &mcpProgressHandler{Handler: h.Handler.WithGroup(name), update: h.update}
}
