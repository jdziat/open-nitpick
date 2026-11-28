package auth

import (
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nitpick-oss/nitpick/internal/server/state"
)

// The limiter is hit by every failed sign-in. Concurrent failures against one
// key must lock it exactly when the limit is reached and never let more through,
// and concurrent keys must not interfere.
func TestTheLimiterLocksExactlyAtTheLimitUnderConcurrentFailures(t *testing.T) {
	l := testLimiter(t, nil, 5, time.Hour, time.Hour, 1000)
	var wg sync.WaitGroup
	var mu sync.Mutex
	locked := 0
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if l.Fail(bg, "shared") {
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
	if ok, _ := l.Allowed(bg, "shared"); ok {
		t.Error("a key that failed fifty times is allowed")
	}
	for i := 0; i < 20; i++ {
		if ok, _ := l.Allowed(bg, fmt.Sprintf("other-%d", i)); !ok {
			t.Error("an unrelated key was locked by another's failures")
		}
	}
}

// Started sign-ins are recorded, taken and expired from many requests at once.
func TestFlowsSurviveConcurrentStartsAndAnswers(t *testing.T) {
	f := testFlows(t, nil, 500)
	var wg sync.WaitGroup
	taken := make(chan string, 400)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				key, err := f.put(bg, &flow{secret: "n"})
				if err != nil {
					t.Error(err)
					return
				}
				if _, ok := f.take(bg, key); !ok {
					t.Error("a flow just stored was not found")
					return
				}
				// A second take must find nothing: a state is good once.
				if _, ok := f.take(bg, key); ok {
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
	f := testFlows(t, nil, 10_000)
	for round := 0; round < 200; round++ {
		key, err := f.put(bg, &flow{secret: "n"})
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
				if _, ok := f.take(bg, key); ok {
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
	s := testSeen(t, nil)
	for round := 0; round < 100; round++ {
		id := fmt.Sprintf("_one-id-%d", round)
		var wg sync.WaitGroup
		var mu sync.Mutex
		wins := 0
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if s.firstTime(bg, id, time.Now().Add(time.Hour)) {
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
	pw.ByAddress = &Limiter{Name: "t", Max: 1000, Window: time.Hour, Lockout: time.Hour, Keys: 100}
	s := newSite(t, pw)
	resp, err := s.c.Get(s.ts.URL + "/auth/login")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	token := tokenIn(t, string(page))

	const burst = 100
	var wg sync.WaitGroup
	var mu sync.Mutex
	counts := map[int]int{}
	var transportErrs []error
	for n := 0; n < burst; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			r, _, err := s.postE(url.Values{"username": {"alice"}, "password": {fmt.Sprintf("guess-%d", n)}, "csrf_token": {token}, "next": {"/ui/"}})
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				// Reported by the test goroutine below: FailNow is not allowed here.
				transportErrs = append(transportErrs, err)
				return
			}
			counts[r.StatusCode]++
		}(n)
	}
	wg.Wait()
	if len(transportErrs) > 0 {
		t.Fatalf("%d of %d guesses did not complete: %v", len(transportErrs), burst, transportErrs[0])
	}
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
	l := testLimiter(t, nil, 5, time.Hour, time.Hour, 10_000)
	for round := 0; round < 50; round++ {
		key := fmt.Sprintf("k%d", round)
		var wg sync.WaitGroup
		var mu sync.Mutex
		allowed := 0
		for i := 0; i < 40; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if ok, _ := l.Take(bg, key); ok {
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
	pw.ByAddress = &Limiter{Name: "t", Max: 1000, Window: time.Hour, Lockout: time.Hour, Keys: 100}
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
	// The literal, not the constant: each hash holds 64 MiB, so 4 is a memory
	// ceiling of 256 MiB, and raising it is a decision this test makes visible.
	if cap(pw.hashing) != 4 || maxConcurrentHashes != 4 {
		t.Fatalf("the hashing bound is %d (constant %d), want 4", cap(pw.hashing), maxConcurrentHashes)
	}
	// With every slot held, a request waits and gives up with its client, and
	// does not hash.
	for i := 0; i < maxConcurrentHashes; i++ {
		pw.hashing <- struct{}{}
	}
	pw.ByAddress = &Limiter{Name: "t", Max: 1000, Window: time.Hour, Lockout: time.Hour, Keys: 100}
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
	token := tokenIn(t, string(page))
	slow, err := s.c.PostForm(s.ts.URL+"/auth/password/login", url.Values{"username": {"alice"}, "password": {"correct horse"}, "csrf_token": {token}})
	if slow != nil {
		_ = slow.Body.Close()
	}
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
	pw.ByAddress = &Limiter{Name: "t", Max: 1000, Window: time.Hour, Lockout: time.Hour, Keys: 100}
	s := newSite(t, pw)

	// An account that is locked refuses a request that the address limiter let in.
	for i := 0; i < pw.ByAccount.Max; i++ {
		pw.ByAccount.Fail(bg, "account:alice")
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
	pw2.ByAddress = &Limiter{Name: "t", Max: 1000, Window: time.Hour, Lockout: time.Hour, Keys: 100}
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
	token := tokenIn(t, string(page))
	for n := 0; n < 3; n++ {
		if r, err := s2.c.PostForm(s2.ts.URL+"/auth/password/login", url.Values{"username": {"bob"}, "password": {"x"}, "csrf_token": {token}}); err == nil {
			_ = r.Body.Close()
		}
	}
	time.Sleep(300 * time.Millisecond) // the abandoned handlers finish
	if got := inFlight(pw2.ByAccount, "account:bob"); got != 0 {
		t.Errorf("abandoned sign-ins left %d charges on the account", got)
	}
}

// Measured, not read from the constant: under a burst of sign-ins no more than
// four argon2id hashes are ever in flight.
func TestNoMoreThanFourHashesRunAtOnceUnderABurst(t *testing.T) {
	var running, peak atomic.Int64
	hold := make(chan struct{})
	pw := newPassword(t)
	pw.ByAddress = &Limiter{Name: "t", Max: 10000, Window: time.Hour, Lockout: time.Hour, Keys: 100}
	pw.ByAccount = &Limiter{Name: "t", Max: 10000, Window: time.Hour, Lockout: time.Hour, Keys: 100}
	pw.Users.observe = func() func() {
		n := running.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		<-hold
		return func() { running.Add(-1) }
	}
	s := newSite(t, pw)
	resp, err := s.c.Get(s.ts.URL + "/auth/login")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	token := tokenIn(t, string(page))

	var wg sync.WaitGroup
	for n := 0; n < 40; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			r, err := s.c.PostForm(s.ts.URL+"/auth/password/login", url.Values{"username": {"alice"}, "password": {fmt.Sprintf("g%d", n)}, "csrf_token": {token}})
			if err == nil {
				_ = r.Body.Close()
			}
		}(n)
	}
	time.Sleep(400 * time.Millisecond) // let the burst pile up behind the semaphore
	if got := running.Load(); got != 4 {
		t.Errorf("%d hashes in flight while 40 sign-ins wait, want exactly 4", got)
	}
	close(hold)
	wg.Wait()
	if p := peak.Load(); p != 4 {
		t.Errorf("peak concurrent hashes = %d, want 4", p)
	}
}

// Failures older than the window do not count toward a lock, in Take as well as in
// Fail. Without this, an account that mistyped four times last week would be one
// charge from a lock today.
func TestTakeIgnoresFailuresOlderThanTheWindow(t *testing.T) {
	now := time.Now()
	l := testLimiter(t, func() time.Time { return now }, 5, 10*time.Minute, time.Hour, 10)
	for i := 0; i < 4; i++ {
		l.Fail(bg, "k")
	}
	// Inside the window, four failures leave room for one attempt and no more.
	if ok, _ := l.Take(bg, "k"); !ok {
		t.Fatal("the fifth attempt was refused with four failures in the window")
	}
	if ok, _ := l.Take(bg, "k"); ok {
		t.Fatal("a sixth attempt was allowed while the fifth was still unresolved")
	}
	l.Refund(bg, "k")

	// After the window the four failures no longer count: five attempts are owed again.
	now = now.Add(11 * time.Minute)
	allowed := 0
	for i := 0; i < 8; i++ {
		if ok, _ := l.Take(bg, "k"); ok {
			allowed++
		}
	}
	if allowed != 5 {
		t.Errorf("after the window, %d of 8 attempts were allowed, want 5", allowed)
	}
}

// A limiter that cannot read its counts cannot say an attempt is allowed. If it
// answered yes, taking the database down would lift every limit, for as long as the
// outage lasted, on any path that does not also need the database to finish.
func TestALimiterThatCannotReachItsDatabaseAllowsNothing(t *testing.T) {
	db := testDB(t)
	l := &Limiter{Name: "t", Max: 5, Window: time.Hour, Lockout: time.Hour, Keys: 10}
	var told []string
	l.Use(state.New(db), func(op string, _ error) { told = append(told, op) })
	if ok, _ := l.Take(bg, "k"); !ok {
		t.Fatal("a fresh key was refused with the database up")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if ok, wait := l.Take(bg, "k"); ok || wait != l.Lockout {
		t.Errorf("Take = %v, %v with the database gone, want a refusal for the lockout", ok, wait)
	}
	if ok, _ := l.Allowed(bg, "k"); ok {
		t.Error("Allowed said yes with the database gone")
	}
	if len(told) < 2 {
		t.Errorf("the failures were reported %d times, want each one logged: %v", len(told), told)
	}
}
