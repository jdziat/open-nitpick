package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// Sessions are shared by every request. Many sign-ins, lookups and sign-outs at
// once must neither race nor lose or invent a session; run under -race this is
// the check that the lock covers every access.
func TestSessionsSurviveConcurrentUse(t *testing.T) {
	s := NewSessions(SessionConfig{Idle: time.Hour, Absolute: time.Hour})
	const workers, rounds = 16, 40
	var wg sync.WaitGroup
	errs := make(chan string, workers*rounds)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				rec := httptest.NewRecorder()
				who := fmt.Sprintf("user-%d-%d", w, i)
				if err := s.Start(rec, Identity{Subject: who}); err != nil {
					errs <- err.Error()
					return
				}
				req := httptest.NewRequest("GET", "/", nil)
				for _, c := range rec.Result().Cookies() {
					req.AddCookie(c)
				}
				id, _, ok := s.Lookup(req)
				if !ok || id.Subject != who {
					errs <- fmt.Sprintf("%s got %q, %v", who, id.Subject, ok)
					return
				}
				s.End(httptest.NewRecorder(), req)
				if _, _, ok := s.Lookup(req); ok {
					errs <- who + " survived sign-out"
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
	if s.Len() != 0 {
		t.Errorf("%d sessions remain after every one signed out", s.Len())
	}
}

// The whole server, with signed-in browsers hitting it at once.
func TestTheServerSurvivesConcurrentSignedInRequests(t *testing.T) {
	e := newEnv(t, Config{
		Components: []Component{&stubComponent{name: "stub", text: "x"}},
		Providers:  []Provider{fakeProvider{id: "fake"}},
		Roles:      newRoles(t, RoleRule{Roles: []Role{"admin"}, Group: "ops"}),
		Assets:     builtAssets(),
	})
	e.signIn("alice", "ops")
	var wg sync.WaitGroup
	bad := make(chan string, 64)
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, path := range []string{"/ui/", "/ui/stub/", "/ui/stub/admin", "/v1/stub/secret", "/healthz"} {
				req, _ := http.NewRequest("GET", e.ts.URL+path, nil)
				resp, err := e.c.Do(req)
				if err != nil {
					bad <- err.Error()
					return
				}
				_ = resp.Body.Close()
				if resp.StatusCode != 200 {
					bad <- fmt.Sprintf("%s = %d", path, resp.StatusCode)
				}
			}
		}()
	}
	wg.Wait()
	close(bad)
	for b := range bad {
		t.Error(b)
	}
}
