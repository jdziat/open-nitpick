package server

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"sync"
	"time"
)

// SessionCookie is the name of the session cookie. The __Host- prefix makes a
// browser refuse it unless it is Secure, has Path=/ and names no Domain, so a
// sibling subdomain cannot set or shadow it.
const SessionCookie = "__Host-nitpick_session"

// SessionConfig bounds a session. A session ends at the first of: it has been
// idle for Idle, it is Absolute old, or it is signed out.
type SessionConfig struct {
	Idle     time.Duration
	Absolute time.Duration
}

// DefaultSessions is what an unconfigured server uses.
var DefaultSessions = SessionConfig{Idle: 30 * time.Minute, Absolute: 12 * time.Hour}

// Sessions holds the signed-in sessions in memory. A restart signs everyone
// out, which is the point: nothing a session needs survives on disk.
type Sessions struct {
	cfg SessionConfig
	now func() time.Time

	mu    sync.Mutex
	byKey map[[32]byte]*session
	last  time.Time // the last time expired sessions were swept
}

type session struct {
	id       Identity
	created  time.Time
	lastSeen time.Time
	csrf     [32]byte
}

// NewSessions returns an empty store. Zero fields of cfg take the defaults.
func NewSessions(cfg SessionConfig) *Sessions {
	if cfg.Idle <= 0 {
		cfg.Idle = DefaultSessions.Idle
	}
	if cfg.Absolute <= 0 {
		cfg.Absolute = DefaultSessions.Absolute
	}
	return &Sessions{cfg: cfg, now: time.Now, byKey: map[[32]byte]*session{}}
}

// token is 256 random bits, base64url. It is the cookie's value and is never
// stored: the store is keyed by its hash, so a dump of the store holds nothing
// that is a valid cookie.
func newToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

func keyOf(token string) [32]byte { return sha256.Sum256([]byte(token)) }

// Start signs an identity in and sets the session cookie. It always issues a new
// session ID, so an ID that was known before sign-in is never the one after it.
func (s *Sessions) Start(w http.ResponseWriter, id Identity) error {
	token, err := newToken()
	if err != nil {
		return err
	}
	var csrf [32]byte
	if _, err := rand.Read(csrf[:]); err != nil {
		return err
	}
	now := s.now()
	s.mu.Lock()
	s.sweepLocked(now)
	s.byKey[keyOf(token)] = &session{id: id, created: now, lastSeen: now, csrf: csrf}
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(s.cfg.Absolute / time.Second),
	})
	return nil
}

// Lookup returns the identity of the request's session and the CSRF secret
// bound to it, and counts the request as activity. A session past either
// timeout is removed and not returned.
func (s *Sessions) Lookup(r *http.Request) (Identity, [32]byte, bool) {
	c, err := r.Cookie(SessionCookie)
	if err != nil || c.Value == "" {
		return Identity{}, [32]byte{}, false
	}
	key := keyOf(c.Value)
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.byKey[key]
	if !ok {
		return Identity{}, [32]byte{}, false
	}
	if s.expired(sess, now) {
		delete(s.byKey, key)
		return Identity{}, [32]byte{}, false
	}
	sess.lastSeen = now
	return sess.id, sess.csrf, true
}

// End signs the request's session out and tells the browser to drop the cookie.
func (s *Sessions) End(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(SessionCookie); err == nil {
		s.mu.Lock()
		delete(s.byKey, keyOf(c.Value))
		s.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{
		Name: SessionCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
	})
}

// Len is how many sessions are held, expired ones included until the next sweep.
func (s *Sessions) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.byKey)
}

func (s *Sessions) expired(sess *session, now time.Time) bool {
	return now.Sub(sess.lastSeen) >= s.cfg.Idle || now.Sub(sess.created) >= s.cfg.Absolute
}

// sweepLocked drops expired sessions, at most once a minute, so a sign-in does
// not walk the whole map every time and a store nobody reads still empties.
func (s *Sessions) sweepLocked(now time.Time) {
	if now.Sub(s.last) < time.Minute {
		return
	}
	s.last = now
	for k, sess := range s.byKey {
		if s.expired(sess, now) {
			delete(s.byKey, k)
		}
	}
}
