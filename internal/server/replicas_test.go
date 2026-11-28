package server

import (
	"context"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nitpick-oss/nitpick/internal/server/state"
)

// replicas starts n servers over one database, as n pods of one release.
func replicas(t *testing.T, n int, cfg Config) []*env {
	t.Helper()
	db := testDB(t)
	out := make([]*env, n)
	for i := range out {
		c := cfg
		c.DB = db
		out[i] = newEnv(t, c)
	}
	return out
}

func twoStandard() Config {
	return Config{
		Components: []Component{&stubComponent{name: "stub"}},
		Providers:  []Provider{fakeProvider{id: "fake"}},
		Roles:      nil,
		Assets:     builtAssets(),
	}
}

func TestASessionStartedOnOneServerIsValidOnAnotherAndSigningOutEndsItOnBoth(t *testing.T) {
	r := replicas(t, 2, twoStandard())
	a, b := r[0], r[1]
	a.signIn("alice")
	cookie := a.cookie(SessionCookie)
	if cookie == nil {
		t.Fatal("no session cookie after sign-in")
	}
	asCookie := map[string]string{"Cookie": SessionCookie + "=" + cookie.Value}

	resp, page := b.do("GET", "/ui/", nil, asCookie)
	if resp.StatusCode != 200 {
		t.Fatalf("the second server answered %d to a session the first started", resp.StatusCode)
	}
	if got := sessionOf(t, page).Name; got != "alice" {
		t.Errorf("the second server knows the session as %q, want alice", got)
	}

	token := sessionOf(t, page).CSRF
	if resp, _ := b.do("POST", "/auth/logout", url.Values{"csrf_token": {token}}, asCookie); resp.StatusCode != 303 {
		t.Fatalf("sign-out on the second server = %d", resp.StatusCode)
	}
	if resp, _ := a.do("GET", "/ui/", nil, asCookie); resp.StatusCode != 303 {
		t.Errorf("after signing out on the second server the first still honoured the cookie: %d", resp.StatusCode)
	}
}

func TestALoginFormServedByOneServerIsAcceptedByAnother(t *testing.T) {
	r := replicas(t, 2, twoStandard())
	a, b := r[0], r[1]
	_, page := a.do("GET", "/auth/login", nil, nil)
	token := between(t, page, `name="csrf_token" value="`, `"`)
	pre := a.cookie(PreSessionCookie)
	if pre == nil {
		t.Fatal("the login page set no pre-session cookie")
	}
	resp, body := b.do("POST", "/auth/fake/login", url.Values{"username": {"carol"}, "csrf_token": {token}, "next": {"/ui/"}},
		map[string]string{"Cookie": PreSessionCookie + "=" + pre.Value})
	if resp.StatusCode != 303 {
		t.Errorf("the second server refused the first one's login form (CSRF keys differ?): %d %s", resp.StatusCode, body)
	}
}

func TestEveryServerStartedAtOnceOnAnEmptyDatabaseSharesOneCSRFKey(t *testing.T) {
	db := testDB(t)
	st := state.New(db)
	keys := make([][32]byte, 12)
	var wg sync.WaitGroup
	for i := range keys {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := NewCSRF(context.Background(), st)
			if err != nil {
				t.Error(err)
				return
			}
			keys[i] = c.key
		}()
	}
	wg.Wait()
	for i, k := range keys {
		if k != keys[0] || k == ([32]byte{}) {
			t.Fatalf("server %d has key %x and server 0 has %x: replicas that started together disagree", i, k[:4], keys[0][:4])
		}
	}
	later, err := NewCSRF(context.Background(), st)
	if err != nil || later.key != keys[0] {
		t.Errorf("a server started afterwards has a different key: %v", err)
	}
}

func TestACSRFKeyOfTheWrongSizeIsReplacedAndNotTrusted(t *testing.T) {
	db := testDB(t)
	st := state.New(db)
	if err := st.Put(context.Background(), csrfKeyEntry, state.Entry{Value: []byte("short"), Expires: farFuture(st.Now())}); err != nil {
		t.Fatal(err)
	}
	c, err := NewCSRF(context.Background(), st)
	if err != nil || c.key == ([32]byte{}) {
		t.Fatalf("NewCSRF = %v, %v", c, err)
	}
	if e, _, _ := st.Get(context.Background(), csrfKeyEntry); len(e.Value) != 32 {
		t.Errorf("the stored key is %d bytes after it was replaced, want 32", len(e.Value))
	}
}

func TestAnActiveSessionIsWrittenAtMostOnceEveryFewSecondsAndNeverExtendsPastIdle(t *testing.T) {
	db := testDB(t)
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	st := state.New(db).WithClock(func() time.Time { return now })
	s := NewSessions(st, SessionConfig{Idle: 20 * time.Minute, Absolute: 12 * time.Hour})
	rec := httptest.NewRecorder()
	if err := s.Start(context.Background(), rec, Identity{Subject: "alice"}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/", nil)
	for _, c := range rec.Result().Cookies() {
		req.AddCookie(c)
	}
	cookie := rec.Result().Cookies()[0]
	expires := func() time.Time {
		e, ok, err := st.Get(context.Background(), sessionKey(cookie.Value))
		if err != nil || !ok {
			t.Fatalf("session entry: %v %v", ok, err)
		}
		return e.Expires
	}
	started := expires()
	if want := now.Add(20 * time.Minute); !started.Equal(want) {
		t.Fatalf("a new session ends at %v, want the idle limit %v", started, want)
	}
	now = now.Add(10 * time.Second) // under touchEvery
	if _, _, ok := s.Lookup(req); !ok {
		t.Fatal("a fresh session was not found")
	}
	if got := expires(); !got.Equal(started) {
		t.Errorf("a lookup 10s after the last write wrote again: ends %v, was %v", got, started)
	}
	now = now.Add(31 * time.Second) // past touchEvery (30s)
	if _, _, ok := s.Lookup(req); !ok {
		t.Fatal("an active session was not found")
	}
	if got, want := expires(), now.Add(20*time.Minute); !got.Equal(want) {
		t.Errorf("a lookup after the throttle did not record activity: ends %v, want %v", got, want)
	}
	// The stale write errs toward ending sooner: 19m59s after the last write it is still
	// there, and 20m after it is not.
	last := now
	now = last.Add(19*time.Minute + 59*time.Second)
	if _, _, ok := s.Lookup(req); !ok {
		t.Error("a session was ended just under its idle limit")
	}
	last = now
	now = last.Add(20*time.Minute + time.Second)
	if _, _, ok := s.Lookup(req); ok {
		t.Error("a session that was idle for longer than the limit was honoured")
	}
}

func TestASessionThatCannotBeLookedUpIsSignedOutAndTheFailureIsReported(t *testing.T) {
	db := testDB(t)
	s := NewSessions(state.New(db), SessionConfig{})
	var reported []string
	s.OnError = func(op string, err error) { reported = append(reported, op) }
	rec := httptest.NewRecorder()
	if err := s.Start(context.Background(), rec, Identity{Subject: "alice"}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/", nil)
	for _, c := range rec.Result().Cookies() {
		req.AddCookie(c)
	}
	if _, _, ok := s.Lookup(req); !ok {
		t.Fatal("setup: the session was not found")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := s.Lookup(req); ok {
		t.Error("a session was honoured while the database could not be read")
	}
	if len(reported) == 0 {
		t.Error("the failure to read a session was not reported, so an outage looks like everyone signing out for no reason")
	}
}

func TestTheDatabaseHoldsNothingThatIsAValidCookie(t *testing.T) {
	db := testDB(t)
	st := state.New(db)
	s := NewSessions(st, SessionConfig{})
	rec := httptest.NewRecorder()
	if err := s.Start(context.Background(), rec, Identity{Subject: "alice", Name: "Alice"}); err != nil {
		t.Fatal(err)
	}
	cookie := rec.Result().Cookies()[0]
	var rows []struct {
		StateKey string
		Value    []byte
	}
	if err := db.Raw("SELECT state_key, value FROM server_state").Scan(&rows).Error; err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if strings.Contains(r.StateKey, cookie.Value) || strings.Contains(string(r.Value), cookie.Value) {
			t.Errorf("the cookie value is in the database: key %q", r.StateKey)
		}
	}
	if len(rows) == 0 {
		t.Fatal("no row was written")
	}
}

// The idle and the absolute limit are each enforced twice, once in the session's own
// check and once in the time the table drops it, so removing one layer changes nothing
// a request can see. Each layer is tested alone.
func TestAnExpiredSessionIsSeenAsExpiredAtExactlyItsLimits(t *testing.T) {
	s := NewSessions(state.New(testDB(t)), SessionConfig{Idle: 10 * time.Minute, Absolute: time.Hour})
	created := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	sess := stored{Created: created, LastSeen: created}
	for name, tc := range map[string]struct {
		last, now time.Time
		want      bool
	}{
		"just under idle":             {created, created.Add(10*time.Minute - time.Second), false},
		"exactly idle":                {created, created.Add(10 * time.Minute), true},
		"active, just under absolute": {created.Add(59 * time.Minute), created.Add(time.Hour - time.Second), false},
		"active, exactly absolute":    {created.Add(59*time.Minute + 30*time.Second), created.Add(time.Hour), true},
	} {
		sess.LastSeen = tc.last
		if got := s.expired(sess, tc.now); got != tc.want {
			t.Errorf("%s: expired = %v, want %v", name, got, tc.want)
		}
	}
}

func TestTheTableDropsASessionAtTheEarlierOfItsIdleAndAbsoluteEnd(t *testing.T) {
	s := NewSessions(state.New(testDB(t)), SessionConfig{Idle: 30 * time.Minute, Absolute: time.Hour})
	created := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	idle, err := s.entryFor(stored{Created: created, LastSeen: created})
	if err != nil {
		t.Fatal(err)
	}
	if want := created.Add(30 * time.Minute); !idle.Expires.Equal(want) {
		t.Errorf("a new session is dropped at %v, want its idle end %v", idle.Expires, want)
	}
	late, _ := s.entryFor(stored{Created: created, LastSeen: created.Add(50 * time.Minute)})
	if want := created.Add(time.Hour); !late.Expires.Equal(want) {
		t.Errorf("a session active at 50m is dropped at %v, want its absolute end %v and not 80m", late.Expires, want)
	}
}

func TestTheCSRFKeyIsStillThereAYearLater(t *testing.T) {
	db := testDB(t)
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	st := state.New(db).WithClock(func() time.Time { return now })
	first, err := NewCSRF(context.Background(), st)
	if err != nil {
		t.Fatal(err)
	}
	now = now.AddDate(1, 0, 0)
	later, err := NewCSRF(context.Background(), st)
	if err != nil || later.key != first.key {
		t.Errorf("a year later the key changed (%v), which would invalidate every form and session token", err)
	}
}
