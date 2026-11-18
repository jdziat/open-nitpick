package memo

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestConcurrentLoadsShareOneSuccessfulResult(t *testing.T) {
	var cache Cache[string, string]
	var calls atomic.Int64
	load := func() (string, error) {
		calls.Add(1)
		return "captured", nil
	}
	var workers sync.WaitGroup
	for range 20 {
		workers.Go(func() {
			got, err := cache.Get(t.Context(), "file", load)
			if got != "captured" || err != nil {
				t.Errorf("cached result = %q, %v", got, err)
			}
		})
	}
	workers.Wait()
	if got := calls.Load(); got != 1 {
		t.Fatalf("loads = %d, want one captured result", got)
	}
}

func TestFailedLoadsCanRecoverWithinTheSameCache(t *testing.T) {
	var cache Cache[string, int]
	sentinel := errors.New("temporary failure")
	_, err := cache.Get(t.Context(), "key", func() (int, error) { return 0, sentinel })
	if !errors.Is(err, sentinel) {
		t.Fatalf("first load = %v", err)
	}
	got, err := cache.Get(t.Context(), "key", func() (int, error) { return 9, nil })
	if got != 9 || err != nil {
		t.Fatalf("failed load was retained: %d, %v", got, err)
	}
}

func TestUnretainedLoadsStillReturnTheirResults(t *testing.T) {
	var cache Cache[string, int]
	loads := 0
	for range 2 {
		got, err := cache.GetIf(t.Context(), "key", func() (int, error) {
			loads++
			return 9, nil
		}, func(int) bool { return false })
		if got != 9 || err != nil {
			t.Fatalf("unretained result = %d, %v", got, err)
		}
	}
	if loads != 2 {
		t.Fatalf("unretained result survived: loads=%d", loads)
	}
}

type observedContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *observedContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func TestLiveWaiterRetriesAnotherLoadersCancellation(t *testing.T) {
	var cache Cache[string, int]
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	leaderCtx, cancelLeader := context.WithCancel(ctx)
	defer cancelLeader()
	started := make(chan struct{})
	leaderDone := make(chan error, 1)
	go func() {
		_, err := cache.Get(leaderCtx, "key", func() (int, error) {
			close(started)
			<-leaderCtx.Done()
			return 0, leaderCtx.Err()
		})
		leaderDone <- err
	}()
	<-started
	waiterCtx := &observedContext{Context: ctx, waiting: make(chan struct{})}
	type result struct {
		value int
		err   error
	}
	waiterDone := make(chan result, 1)
	go func() {
		value, err := cache.Get(waiterCtx, "key", func() (int, error) { return 9, nil })
		waiterDone <- result{value, err}
	}()
	select {
	case <-waiterCtx.waiting:
	case <-ctx.Done():
		t.Fatal("waiter did not join the pending load")
	}
	cancelLeader()
	if err := <-leaderDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("leader = %v", err)
	}
	select {
	case got := <-waiterDone:
		if got.value != 9 || got.err != nil {
			t.Fatalf("live waiter inherited another loader's cancellation: %+v", got)
		}
	case <-ctx.Done():
		t.Fatal("live waiter did not finish")
	}
}

func TestCancelledWaiterDoesNotCancelTheSharedLoad(t *testing.T) {
	var cache Cache[string, int]
	started, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(finished)
		_, _ = cache.Get(t.Context(), "key", func() (int, error) {
			close(started)
			<-release
			return 7, nil
		})
	}()
	<-started
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	got, err := cache.Get(ctx, "key", func() (int, error) {
		t.Error("waiter started another load")
		return 0, nil
	})
	close(release)
	<-finished
	if got != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled waiter = %d, %v", got, err)
	}
	got, err = cache.Get(t.Context(), "key", func() (int, error) {
		t.Error("successful load was not retained")
		return 0, nil
	})
	if got != 7 || err != nil {
		t.Fatalf("retained result = %d, %v", got, err)
	}
}

func TestCancelledLoaderCanBeReplacedByFreshWork(t *testing.T) {
	var cache Cache[string, int]
	ctx, cancel := context.WithCancel(t.Context())
	_, err := cache.Get(ctx, "key", func() (int, error) {
		cancel()
		return 0, ctx.Err()
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled loader = %v", err)
	}
	got, err := cache.Get(t.Context(), "key", func() (int, error) { return 9, nil })
	if got != 9 || err != nil {
		t.Fatalf("fresh work inherited cancellation: %d, %v", got, err)
	}
}
