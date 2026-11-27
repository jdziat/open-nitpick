package auth

import (
	"sync"
	"time"
)

// Limiter counts failed sign-ins and locks out what keeps failing. It is kept
// for two keys at once, the account and the address, so a guess at one account
// from many addresses and many accounts from one address are both stopped.
//
// Failures are counted in a window, not forever: a person who mistyped last week
// is not one failure from a lockout. A locked key is refused for Lockout and the
// count starts over after it. State is in memory, which a restart clears, as it
// clears the sessions.
type Limiter struct {
	Max     int           // failures in Window that lock a key
	Window  time.Duration // how long a failure counts
	Lockout time.Duration // how long a locked key is refused
	// Keys bounds the table so a flood of made-up names cannot fill memory.
	Keys int

	now func() time.Time

	mu      sync.Mutex
	entries map[string]*entry
}

type entry struct {
	failures    []time.Time
	lockedUntil time.Time
	inFlight    int // attempts Take has charged and not yet resolved
}

// DefaultLimiter is 5 failures in 15 minutes, then 15 minutes locked.
func DefaultLimiter() *Limiter {
	return &Limiter{Max: 5, Window: 15 * time.Minute, Lockout: 15 * time.Minute, Keys: 100_000}
}

func (l *Limiter) clock() time.Time {
	if l.now != nil {
		return l.now()
	}
	return time.Now()
}

// Take charges an attempt before the work it pays for, atomically with the
// check that the key is not locked. Charging and checking as two steps let a
// burst of parallel requests all pass the check before any was counted.
//
// An attempt that would be the one to reach Max is still allowed, and is charged
// without locking, so that a correct password can give its charge back with
// Refund and not leave the key locked by a success. The lock is taken by Fail, for
// an attempt that was wrong. At most Max attempts are in flight on a key that has
// not yet locked: the Max-th is let through, and any after it are refused until a
// result is known.
func (l *Limiter) Take(key string) (allowed bool, wait time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clock()
	e := l.entries[key]
	if e != nil && now.Before(e.lockedUntil) {
		return false, e.lockedUntil.Sub(now)
	}
	if e == nil {
		if l.Keys > 0 && len(l.entries) >= l.Keys {
			l.dropOldestLocked(now)
		}
		e = &entry{}
		if l.entries == nil {
			l.entries = map[string]*entry{}
		}
		l.entries[key] = e
	}
	cutoff := now.Add(-l.Window)
	kept := e.failures[:0]
	for _, t := range e.failures {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	e.failures = kept
	if len(e.failures)+e.inFlight >= l.Max {
		return false, l.Lockout
	}
	e.inFlight++
	return true, 0
}

// Refund ends an attempt that Take charged, without counting it as a failure.
func (l *Limiter) Refund(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if e := l.entries[key]; e != nil && e.inFlight > 0 {
		e.inFlight--
	}
}

// Allowed reports whether a key may try, and if not for how long it is locked.
func (l *Limiter) Allowed(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.entries[key]
	if e == nil {
		return true, 0
	}
	if now := l.clock(); now.Before(e.lockedUntil) {
		return false, e.lockedUntil.Sub(now)
	}
	return true, 0
}

// Fail records a failed attempt and reports whether it locked the key.
func (l *Limiter) Fail(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.failLocked(key, l.clock())
}

func (l *Limiter) failLocked(key string, now time.Time) bool {
	// An attempt that Take charged is resolved here as a failure.
	defer func() {
		if e := l.entries[key]; e != nil && e.inFlight > 0 {
			e.inFlight--
		}
	}()
	if l.entries == nil {
		l.entries = map[string]*entry{}
	}
	e := l.entries[key]
	if e == nil {
		if l.Keys > 0 && len(l.entries) >= l.Keys {
			l.dropOldestLocked(now)
		}
		e = &entry{}
		l.entries[key] = e
	}
	cutoff := now.Add(-l.Window)
	kept := e.failures[:0]
	for _, t := range e.failures {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	e.failures = append(kept, now)
	if len(e.failures) >= l.Max {
		e.lockedUntil = now.Add(l.Lockout)
		e.failures = nil
		return true
	}
	return false
}

// Success forgets a key's failures. A lockout that is running is not lifted by
// a correct guess: it is how a guessed password would end the lock.
func (l *Limiter) Success(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if e := l.entries[key]; e != nil && !l.clock().Before(e.lockedUntil) {
		e.failures = nil
		if e.inFlight > 0 {
			e.inFlight--
		}
		if e.inFlight == 0 {
			delete(l.entries, key)
		}
	}
}

// dropOldestLocked makes room by removing keys that are neither locked nor
// recently failed, and if there are none, an arbitrary one.
func (l *Limiter) dropOldestLocked(now time.Time) {
	for k, e := range l.entries {
		if now.After(e.lockedUntil) && (len(e.failures) == 0 || now.Sub(e.failures[len(e.failures)-1]) > l.Window) {
			delete(l.entries, k)
			return
		}
	}
	for k := range l.entries {
		delete(l.entries, k)
		return
	}
}

// Len is how many keys are tracked.
func (l *Limiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.entries)
}
