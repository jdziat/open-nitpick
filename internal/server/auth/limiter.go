package auth

import (
	"context"
	"encoding/json"
	"time"

	"github.com/nitpick-oss/nitpick/internal/server/state"
)

// Limiter counts failed sign-ins and locks out what keeps failing. It is kept
// for two keys at once, the account and the address, so a guess at one account
// from many addresses and many accounts from one address are both stopped.
//
// Failures are counted in a window, not forever: a person who mistyped last week
// is not one failure from a lockout. A locked key is refused for Lockout and the
// count starts over after it. The counts are in the shared state, so every replica
// counts the same failures: an attacker spreading guesses over replicas gets no
// more of them for it. A limiter that cannot reach the state refuses, since one
// that cannot count must not let attempts through.
type Limiter struct {
	// Name keeps this limiter's keys apart from another's in the shared state.
	Name    string
	Max     int           // failures in Window that lock a key
	Window  time.Duration // how long a failure counts
	Lockout time.Duration // how long a locked key is refused
	// Keys bounds the table so a flood of made-up names cannot fill it.
	Keys int

	st    *state.Store
	onErr func(op string, err error)
}

// entry is what is kept for one key.
type entry struct {
	Failures    []int64 `json:"f"` // unix nanoseconds
	LockedUntil int64   `json:"l"`
	InFlight    int     `json:"i"` // attempts Take has charged and not yet resolved
}

// DefaultLimiter is 5 failures in 15 minutes, then 15 minutes locked.
func DefaultLimiter(name string) *Limiter {
	return &Limiter{Name: name, Max: 5, Window: 15 * time.Minute, Lockout: 15 * time.Minute, Keys: 100_000}
}

// Use gives the limiter the shared state to count in. onErr is told of a failure to
// reach it.
func (l *Limiter) Use(st *state.Store, onErr func(op string, err error)) {
	l.st, l.onErr = st, onErr
}

func (l *Limiter) fail(op string, err error) {
	if l.onErr != nil {
		l.onErr(op, err)
	}
}

func (l *Limiter) prefix() string { return "limit:" + l.Name }

func (l *Limiter) key(k string) string { return state.Key(l.prefix(), k) }

func decodeEntry(cur *state.Entry) entry {
	var e entry
	if cur != nil {
		_ = json.Unmarshal(cur.Value, &e) // a value that cannot be read counts as none
	}
	return e
}

// forget drops failures that have left the window.
func (l *Limiter) forget(e *entry, now time.Time) {
	cutoff := now.Add(-l.Window).UnixNano()
	kept := e.Failures[:0]
	for _, t := range e.Failures {
		if t > cutoff {
			kept = append(kept, t)
		}
	}
	e.Failures = kept
}

// change applies fn to a key's entry under a lock and keeps what it leaves. An entry
// with nothing in it is not kept.
func (l *Limiter) change(ctx context.Context, key string, fn func(e *entry, now time.Time)) error {
	return l.st.Update(ctx, l.key(key), func(cur *state.Entry) (*state.Entry, error) {
		now := l.st.Now()
		e := decodeEntry(cur)
		fn(&e, now)
		// The entry matters until the last of: the lock ending, the newest failure
		// leaving the window, and (for a charge no one has resolved) the window, so a
		// charge lost to a crash does not hold a key for ever.
		var until time.Time
		if e.LockedUntil > 0 {
			until = time.Unix(0, e.LockedUntil)
		}
		if n := len(e.Failures); n > 0 {
			if t := time.Unix(0, e.Failures[n-1]).Add(l.Window); t.After(until) {
				until = t
			}
		}
		if e.InFlight > 0 {
			if t := now.Add(l.Window); t.After(until) {
				until = t
			}
		}
		if !until.After(now) {
			return nil, nil
		}
		b, err := json.Marshal(e)
		if err != nil {
			return nil, err
		}
		return &state.Entry{Value: b, Expires: until}, nil
	})
}

// makeRoom keeps the table within Keys by dropping the entry that ends soonest.
func (l *Limiter) makeRoom(ctx context.Context) {
	if l.Keys <= 0 {
		return
	}
	n, err := l.st.Count(ctx, l.prefix()+":")
	if err != nil || n < l.Keys {
		return
	}
	_, _ = l.st.DeleteOldest(ctx, l.prefix()+":")
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
func (l *Limiter) Take(ctx context.Context, key string) (allowed bool, wait time.Duration) {
	l.makeRoom(ctx)
	err := l.change(ctx, key, func(e *entry, now time.Time) {
		allowed, wait = false, 0
		if until := time.Unix(0, e.LockedUntil); now.Before(until) {
			wait = until.Sub(now)
			return
		}
		l.forget(e, now)
		if len(e.Failures)+e.InFlight >= l.Max {
			wait = l.Lockout
			return
		}
		e.InFlight++
		allowed = true
	})
	if err != nil {
		l.fail("charge a sign-in attempt", err)
		return false, l.Lockout
	}
	return allowed, wait
}

// Refund ends an attempt that Take charged, without counting it as a failure. It
// runs on its own deadline rather than ctx's: the usual reason to refund is that the
// request was abandoned, and a write on a cancelled context would fail and leave
// the charge on the account.
func (l *Limiter) Refund(ctx context.Context, key string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	err := l.change(ctx, key, func(e *entry, _ time.Time) {
		if e.InFlight > 0 {
			e.InFlight--
		}
	})
	if err != nil {
		l.fail("refund a sign-in attempt", err)
	}
}

// Allowed reports whether a key may try, and if not for how long it is locked.
func (l *Limiter) Allowed(ctx context.Context, key string) (bool, time.Duration) {
	cur, ok, err := l.st.Get(ctx, l.key(key))
	if err != nil {
		l.fail("read a sign-in limit", err)
		return false, l.Lockout
	}
	if !ok {
		return true, 0
	}
	e := decodeEntry(&cur)
	if now := l.st.Now(); now.Before(time.Unix(0, e.LockedUntil)) {
		return false, time.Unix(0, e.LockedUntil).Sub(now)
	}
	return true, 0
}

// Fail records a failed attempt and reports whether it locked the key.
func (l *Limiter) Fail(ctx context.Context, key string) (locked bool) {
	l.makeRoom(ctx)
	err := l.change(ctx, key, func(e *entry, now time.Time) {
		locked = false
		// An attempt that Take charged is resolved here as a failure.
		if e.InFlight > 0 {
			e.InFlight--
		}
		l.forget(e, now)
		e.Failures = append(e.Failures, now.UnixNano())
		if len(e.Failures) >= l.Max {
			e.LockedUntil = now.Add(l.Lockout).UnixNano()
			e.Failures = nil
			locked = true
		}
	})
	if err != nil {
		l.fail("record a failed sign-in", err)
		return false
	}
	return locked
}

// Success forgets a key's failures. A lockout that is running is not lifted by
// a correct guess: it is how a guessed password would end the lock.
func (l *Limiter) Success(ctx context.Context, key string) {
	err := l.change(ctx, key, func(e *entry, now time.Time) {
		if now.Before(time.Unix(0, e.LockedUntil)) {
			return
		}
		e.Failures = nil
		if e.InFlight > 0 {
			e.InFlight--
		}
	})
	if err != nil {
		l.fail("record a sign-in", err)
	}
}

// Len is how many keys are tracked.
func (l *Limiter) Len(ctx context.Context) int {
	n, err := l.st.Count(ctx, l.prefix()+":")
	if err != nil {
		l.fail("count sign-in limits", err)
	}
	return n
}
