package auth

import (
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
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

// A burst of parallel wrong guesses at one account gets at most Max answers that
// reach the hash. Checking the limiter and counting the failure as two steps let
// nearly the whole burst through; the attempt is now charged before it is hashed.
func TestAParallelBurstOfGuessesIsHeldToTheLimit(t *testing.T) {
	pw := newPassword(t)
	pw.ByAddress = &Limiter{Max: 1000, Window: time.Hour, Lockout: time.Hour, Keys: 100}
	s := newSite(t, pw)
	resp, err := s.c.Get(s.ts.URL + "/auth/login")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	i := strings.Index(string(page), `name="csrf_token" value="`) + len(`name="csrf_token" value="`)
	token := string(page)[i : i+strings.Index(string(page)[i:], `"`)]

	const burst = 100
	var wg sync.WaitGroup
	var mu sync.Mutex
	counts := map[int]int{}
	for n := 0; n < burst; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			r, _ := s.post(url.Values{"username": {"alice"}, "password": {fmt.Sprintf("guess-%d", n)}, "csrf_token": {token}, "next": {"/ui/"}})
			mu.Lock()
			counts[r.StatusCode]++
			mu.Unlock()
		}(n)
	}
	wg.Wait()
	if counts[http.StatusUnauthorized] > pw.ByAccount.Max {
		t.Errorf("%d of %d parallel guesses were answered as wrong passwords, want at most %d: %v", counts[http.StatusUnauthorized], burst, pw.ByAccount.Max, counts)
	}
	if counts[http.StatusTooManyRequests] < burst-pw.ByAccount.Max {
		t.Errorf("only %d of %d were refused as locked: %v", counts[http.StatusTooManyRequests], burst, counts)
	}
}

// Take is the check and the charge in one step: of many callers at once, exactly
// Max are allowed.
func TestTakeAllowsExactlyMaxCallersUnderConcurrency(t *testing.T) {
	for round := 0; round < 50; round++ {
		l := &Limiter{Max: 5, Window: time.Hour, Lockout: time.Hour, Keys: 10}
		var wg sync.WaitGroup
		var mu sync.Mutex
		allowed := 0
		for i := 0; i < 40; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if ok, _ := l.Take("k"); ok {
					mu.Lock()
					allowed++
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		if allowed != 5 {
			t.Fatalf("round %d: %d of 40 were allowed at a limit of 5", round, allowed)
		}
	}
}

// A correct password clears what its own attempt was charged.
func TestACorrectPasswordGivesBackItsCharge(t *testing.T) {
	pw := newPassword(t)
	pw.ByAddress = &Limiter{Max: 1000, Window: time.Hour, Lockout: time.Hour, Keys: 100}
	s := newSite(t, pw)
	for round := 0; round < 3; round++ {
		jar, _ := cookiejar.New(nil)
		s.c.Jar = jar
		for i := 0; i < 4; i++ {
			s.login("alice", "wrong")
		}
		if resp, _ := s.login("alice", "correct horse"); resp.StatusCode != http.StatusSeeOther {
			t.Fatalf("round %d: the correct password after four misses = %d", round, resp.StatusCode)
		}
	}
}

// No more than maxConcurrentHashes hashes run at once, however many sign-ins
// arrive. The semaphore is the only thing holding memory to 4 x 64 MiB.
func TestConcurrentHashesAreBounded(t *testing.T) {
	pw := newPassword(t)
	if cap(pw.hashing) != maxConcurrentHashes || maxConcurrentHashes < 1 {
		t.Fatalf("the hashing bound is %d, want %d", cap(pw.hashing), maxConcurrentHashes)
	}
	// With every slot held, a request waits and gives up with its client, and
	// does not hash.
	for i := 0; i < maxConcurrentHashes; i++ {
		pw.hashing <- struct{}{}
	}
	pw.ByAddress = &Limiter{Max: 1000, Window: time.Hour, Lockout: time.Hour, Keys: 100}
	s := newSite(t, pw)
	client := *s.c
	client.Timeout = 300 * time.Millisecond
	s.c = &client
	resp, err := s.c.Get(s.ts.URL + "/auth/login")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	i := strings.Index(string(page), `name="csrf_token" value="`) + len(`name="csrf_token" value="`)
	token := string(page)[i : i+strings.Index(string(page)[i:], `"`)]
	_, err = s.c.PostForm(s.ts.URL+"/auth/password/login", url.Values{"username": {"alice"}, "password": {"correct horse"}, "csrf_token": {token}})
	if err == nil {
		t.Error("a sign-in went ahead while every hashing slot was held")
	}
	// Released, the same sign-in goes through.
	for i := 0; i < maxConcurrentHashes; i++ {
		<-pw.hashing
	}
	s.c.Timeout = 0
	if r, _ := s.login("alice", "correct horse"); r.StatusCode != http.StatusSeeOther {
		t.Errorf("a sign-in with free slots = %d", r.StatusCode)
	}
}

// A charge is never leaked: a sign-in that is refused by the second limiter, or
// that gives up while waiting for a hashing slot, gives back what the first
// charged. A leak would lock an account with no failed guess ever made.
func TestARefusedOrAbandonedSignInLeavesNoChargeBehind(t *testing.T) {
	pw := newPassword(t)
	pw.ByAddress = &Limiter{Max: 1000, Window: time.Hour, Lockout: time.Hour, Keys: 100}
	s := newSite(t, pw)

	// An account that is locked refuses a request that the address limiter let in.
	for i := 0; i < pw.ByAccount.Max; i++ {
		pw.ByAccount.Fail("account:alice")
	}
	before := inFlight(pw.ByAddress, "address:127.0.0.1")
	for i := 0; i < 20; i++ {
		s.login("alice", "x")
	}
	if got := inFlight(pw.ByAddress, "address:127.0.0.1"); got != before {
		t.Errorf("refusals by the account limiter left %d charges on the address, want %d", got, before)
	}

	// A request that gives up while every hashing slot is held leaves nothing either.
	pw2 := newPassword(t)
	pw2.ByAddress = &Limiter{Max: 1000, Window: time.Hour, Lockout: time.Hour, Keys: 100}
	for i := 0; i < maxConcurrentHashes; i++ {
		pw2.hashing <- struct{}{}
	}
	s2 := newSite(t, pw2)
	client := *s2.c
	client.Timeout = 200 * time.Millisecond
	s2.c = &client
	resp, err := s2.c.Get(s2.ts.URL + "/auth/login")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	i := strings.Index(string(page), `name="csrf_token" value="`) + len(`name="csrf_token" value="`)
	token := string(page)[i : i+strings.Index(string(page)[i:], `"`)]
	for n := 0; n < 3; n++ {
		_, _ = s2.c.PostForm(s2.ts.URL+"/auth/password/login", url.Values{"username": {"bob"}, "password": {"x"}, "csrf_token": {token}})
	}
	time.Sleep(300 * time.Millisecond) // the abandoned handlers finish
	if got := inFlight(pw2.ByAccount, "account:bob"); got != 0 {
		t.Errorf("abandoned sign-ins left %d charges on the account", got)
	}
}

func inFlight(l *Limiter, key string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	if e := l.entries[key]; e != nil {
		return e.inFlight
	}
	return 0
}
