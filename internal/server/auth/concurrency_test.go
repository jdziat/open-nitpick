package auth

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// The limiter is hit by every failed sign-in. Concurrent failures against one
// key must lock it exactly when the limit is reached and never let more through,
// and concurrent keys must not interfere.
func TestTheLimiterLocksExactlyAtTheLimitUnderConcurrentFailures(t *testing.T) {
	l := &Limiter{Max: 5, Window: time.Hour, Lockout: time.Hour, Keys: 1000}
	var wg sync.WaitGroup
	var mu sync.Mutex
	locked := 0
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if l.Fail("shared") {
				mu.Lock()
				locked++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	// Fifty failures at five a lock: the lock is taken ten times over, each time
	// the count reaches five and starts again. What must not happen is a lost
	// update that skips a lock or a count that is never reset.
	if locked != 10 {
		t.Errorf("fifty concurrent failures at a limit of five locked %d times, want 10", locked)
	}
	if ok, _ := l.Allowed("shared"); ok {
		t.Error("a key that failed fifty times is allowed")
	}
	for i := 0; i < 20; i++ {
		if ok, _ := l.Allowed(fmt.Sprintf("other-%d", i)); !ok {
			t.Error("an unrelated key was locked by another's failures")
		}
	}
}

// Started sign-ins are recorded, taken and expired from many requests at once.
func TestFlowsSurviveConcurrentStartsAndAnswers(t *testing.T) {
	f := newFlows(500)
	var wg sync.WaitGroup
	taken := make(chan string, 400)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				key, err := f.put(&flow{secret: "n"})
				if err != nil {
					t.Error(err)
					return
				}
				if _, ok := f.take(key); !ok {
					t.Error("a flow just stored was not found")
					return
				}
				// A second take must find nothing: a state is good once.
				if _, ok := f.take(key); ok {
					taken <- key
				}
			}
		}()
	}
	wg.Wait()
	close(taken)
	for k := range taken {
		t.Errorf("flow %s was taken twice", k[:8])
	}
}

// Two answers racing for one state: exactly one wins.
func TestRacingAnswersForOneStateHaveOneWinner(t *testing.T) {
	for round := 0; round < 200; round++ {
		f := newFlows(10)
		key, err := f.put(&flow{secret: "n"})
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		var mu sync.Mutex
		wins := 0
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, ok := f.take(key); ok {
					mu.Lock()
					wins++
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		if wins != 1 {
			t.Fatalf("round %d: %d answers won the same state, want exactly one", round, wins)
		}
	}
}

// The assertion table: the same ID presented at once from many requests is accepted
// by one.
func TestRacingAssertionsWithOneIDHaveOneWinner(t *testing.T) {
	for round := 0; round < 100; round++ {
		s := newSeenAssertions()
		var wg sync.WaitGroup
		var mu sync.Mutex
		wins := 0
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if s.firstTime("_one-id", time.Now().Add(time.Hour)) {
					mu.Lock()
					wins++
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		if wins != 1 {
			t.Fatalf("round %d: %d requests were the first to use one assertion ID", round, wins)
		}
	}
}
