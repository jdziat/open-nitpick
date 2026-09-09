package llm

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// A slow request reports itself while it is still out.
//
// The failure this closes: a nonstreaming call decodes one whole body, so a
// model reasoning for five minutes produces nothing observable and the run
// looks hung. Issue #81 measured 298.5 seconds of exactly that.
func TestASlowRequestSaysItIsStillWaiting(t *testing.T) {
	var buf bytes.Buffer
	var mu sync.Mutex
	log := slog.New(slog.NewTextHandler(&writerFunc{&buf, &mu}, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// Not a real 30 seconds: the behaviour under test is that something is
	// emitted while f is still running.
	err := whileWaiting(context.Background(), log, "model call", "p/m", 20*time.Millisecond, func() error {
		time.Sleep(90 * time.Millisecond)
		return nil
	})
	if err != nil {
		t.Fatalf("whileWaiting: %v", err)
	}

	mu.Lock()
	got := buf.String()
	mu.Unlock()

	if !strings.Contains(got, "still waiting on the model") {
		t.Fatalf("a request that took 90ms at a 20ms interval reported nothing:\n%s", got)
	}
	for _, want := range []string{`stage="model call"`, "model=p/m", "waited="} {
		if !strings.Contains(got, want) {
			t.Errorf("the line does not carry %q:\n%s", want, got)
		}
	}
	// The line must not claim the model is making progress. It knows only
	// that the request has not answered.
	if !strings.Contains(got, "not progress") {
		t.Errorf("the line does not say elapsed time is not progress:\n%s", got)
	}
}

// A fast request says nothing, so an ordinary review is not narrated.
func TestAFastRequestIsSilent(t *testing.T) {
	var buf bytes.Buffer
	var mu sync.Mutex
	log := slog.New(slog.NewTextHandler(&writerFunc{&buf, &mu}, nil))
	_ = whileWaiting(context.Background(), log, "model call", "p/m", time.Hour, func() error { return nil })

	mu.Lock()
	defer mu.Unlock()
	if buf.Len() != 0 {
		t.Errorf("a fast request logged:\n%s", buf.String())
	}
}

// The error is the caller's, unchanged.
func TestWhileWaitingReturnsTheCallsError(t *testing.T) {
	want := errors.New("boom")
	got := whileWaiting(context.Background(), nil, "s", "m", time.Second, func() error { return want })
	if !errors.Is(got, want) {
		t.Errorf("err = %v, want %v", got, want)
	}
}

// A cancelled run stops reporting rather than narrating a request nobody
// awaits.
func TestCancellationStopsTheReports(t *testing.T) {
	var buf bytes.Buffer
	var mu sync.Mutex
	log := slog.New(slog.NewTextHandler(&writerFunc{&buf, &mu}, nil))
	ctx, cancel := context.WithCancel(context.Background())
	_ = whileWaiting(ctx, log, "model call", "p/m", 10*time.Millisecond, func() error {
		cancel()
		time.Sleep(60 * time.Millisecond)
		return nil
	})

	mu.Lock()
	defer mu.Unlock()
	if strings.Contains(buf.String(), "still waiting") {
		t.Errorf("a cancelled request kept reporting:\n%s", buf.String())
	}
}

// writerFunc serialises writes from the reporting goroutine and the test.
type writerFunc struct {
	buf *bytes.Buffer
	mu  *sync.Mutex
}

func (w *writerFunc) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}
