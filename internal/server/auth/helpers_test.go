package auth

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/nitpick-oss/nitpick/internal/database"
	"github.com/nitpick-oss/nitpick/internal/database/databasetest"
	"github.com/nitpick-oss/nitpick/internal/server"
	"github.com/nitpick-oss/nitpick/internal/server/state"
)

var bg = context.Background()

// testDB is a fresh database with the server's tables.
func testDB(t *testing.T) *database.DB {
	t.Helper()
	return databasetest.One(t, server.Models())
}

// testStore is the shared state over a fresh database. With a clock, time is whatever
// the clock says, for a test of what happens as it passes.
func testStore(t *testing.T, now func() time.Time) *state.Store {
	t.Helper()
	st := state.New(databasetest.One(t, state.Models()))
	if now != nil {
		st = st.WithClock(now)
	}
	return st
}

// testLimiter is a limiter over its own state.
func testLimiter(t *testing.T, now func() time.Time, max int, window, lockout time.Duration, keys int) *Limiter {
	t.Helper()
	l := &Limiter{Name: "test", Max: max, Window: window, Lockout: lockout, Keys: keys}
	l.Use(testStore(t, now), nil)
	return l
}

func testFlows(t *testing.T, now func() time.Time, max int) *flows {
	t.Helper()
	f := newFlows(max)
	f.use(testStore(t, now), nil)
	return f
}

func testSeen(t *testing.T, now func() time.Time) *seenAssertions {
	t.Helper()
	s := newSeenAssertions()
	s.use(testStore(t, now), nil)
	return s
}

// inFlight is how many attempts Take has charged to a key and nothing has resolved.
func inFlight(l *Limiter, key string) int {
	e, ok, err := l.st.Get(bg, l.key(key))
	if err != nil || !ok {
		return 0
	}
	var en entry
	_ = json.Unmarshal(e.Value, &en)
	return en.InFlight
}
