package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/nitpick-oss/nitpick/internal/server/state"
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

// Sessions holds the signed-in sessions in the shared state, so a session started
// on one replica is valid on every other, and a restart signs nobody out. The cookie
// is the only thing a browser holds; the identity and the CSRF secret stay on the
// server side.
type Sessions struct {
	cfg SessionConfig
	st  *state.Store
	// OnError is told when the shared state could not be read or written. A session
	// that cannot be looked up is treated as signed out, which is safe and, without
	// this, silent.
	OnError func(op string, err error)

	mu    sync.Mutex
	swept time.Time // the last time expired sessions were swept
}

// stored is a session as it is kept.
type stored struct {
	Identity Identity  `json:"identity"`
	Created  time.Time `json:"created"`
	LastSeen time.Time `json:"last_seen"`
	CSRF     [32]byte  `json:"csrf"`
}

const sessionPrefix = "session"

// NewSessions returns a session store over the shared state. Zero fields of cfg take
// the defaults.
func NewSessions(st *state.Store, cfg SessionConfig) *Sessions {
	if cfg.Idle <= 0 {
		cfg.Idle = DefaultSessions.Idle
	}
	if cfg.Absolute <= 0 {
		cfg.Absolute = DefaultSessions.Absolute
	}
	return &Sessions{cfg: cfg, st: st}
}

func (s *Sessions) fail(op string, err error) {
	if s.OnError != nil {
		s.OnError(op, err)
	}
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

// sessionKey is the key a cookie value is kept under. It is the hash of the value,
// so a dump of the table holds nothing that is a valid cookie.
func sessionKey(token string) string {
	k := keyOf(token)
	return sessionPrefix + ":" + hex.EncodeToString(k[:])
}

// entryFor is the stored session as an entry that ends with the earlier of its idle
// and absolute limits, so the table also drops it without being asked.
func (s *Sessions) entryFor(sess stored) (state.Entry, error) {
	b, err := json.Marshal(sess)
	if err != nil {
		return state.Entry{}, err
	}
	end := sess.LastSeen.Add(s.cfg.Idle)
	if abs := sess.Created.Add(s.cfg.Absolute); abs.Before(end) {
		end = abs
	}
	return state.Entry{Value: b, Expires: end}, nil
}

// Start signs an identity in and sets the session cookie. It always issues a new
// session ID, so an ID that was known before sign-in is never the one after it.
func (s *Sessions) Start(ctx context.Context, w http.ResponseWriter, id Identity) error {
	token, err := newToken()
	if err != nil {
		return err
	}
	var csrf [32]byte
	if _, err := rand.Read(csrf[:]); err != nil {
		return err
	}
	now := s.st.Now()
	e, err := s.entryFor(stored{Identity: id, Created: now, LastSeen: now, CSRF: csrf})
	if err != nil {
		return err
	}
	s.sweep(ctx, now)
	if err := s.st.Put(ctx, sessionKey(token), e); err != nil {
		return err
	}
	return SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(s.cfg.Absolute / time.Second),
	})
}

// touchEvery is how stale a session's last-seen time may get before a request
// writes it again. A page fires several requests at once, and one write for each
// would be a database write for every read. A stale time ends a session sooner, never
// later, since the idle limit counts from it.
func (s *Sessions) touchEvery() time.Duration { return min(s.cfg.Idle/10, 30*time.Second) }

// Lookup returns the identity of the request's session and the CSRF secret bound to
// it, and counts the request as activity. A session past either timeout is removed
// and not returned.
func (s *Sessions) Lookup(r *http.Request) (Identity, [32]byte, bool) {
	c, err := r.Cookie(SessionCookie)
	if err != nil || c.Value == "" {
		return Identity{}, [32]byte{}, false
	}
	ctx := r.Context()
	key := sessionKey(c.Value)
	e, ok, err := s.st.Get(ctx, key)
	if err != nil {
		s.fail("read a session", err)
		return Identity{}, [32]byte{}, false
	}
	if !ok {
		return Identity{}, [32]byte{}, false
	}
	var sess stored
	if err := json.Unmarshal(e.Value, &sess); err != nil {
		s.fail("decode a session", err)
		_ = s.st.Delete(ctx, key)
		return Identity{}, [32]byte{}, false
	}
	now := s.st.Now()
	if s.expired(sess, now) {
		_ = s.st.Delete(ctx, key)
		return Identity{}, [32]byte{}, false
	}
	if now.Sub(sess.LastSeen) >= s.touchEvery() {
		alive := false
		err := s.st.Update(ctx, key, func(cur *state.Entry) (*state.Entry, error) {
			alive = false
			if cur == nil {
				return nil, nil
			}
			var latest stored
			if err := json.Unmarshal(cur.Value, &latest); err != nil || s.expired(latest, now) {
				return nil, nil
			}
			latest.LastSeen = now
			next, err := s.entryFor(latest)
			if err != nil {
				return nil, err
			}
			alive, sess = true, latest
			return &next, nil
		})
		if err != nil {
			s.fail("record session activity", err)
			// The session was read and is valid; only its last-seen time could not be
			// written, which ends it a little sooner and does not need to end it now.
			return sess.Identity, sess.CSRF, true
		}
		if !alive {
			return Identity{}, [32]byte{}, false
		}
	}
	return sess.Identity, sess.CSRF, true
}

// End signs the request's session out and tells the browser to drop the cookie.
func (s *Sessions) End(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(SessionCookie); err == nil {
		if err := s.st.Delete(r.Context(), sessionKey(c.Value)); err != nil {
			s.fail("end a session", err)
		}
	}
	// The cookie is built from constants that CheckCookie accepts, so a refusal is
	// a bug in this file and not an input; the session is already gone either way.
	_ = SetCookie(w, &http.Cookie{
		Name: SessionCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
	})
}

// Len is how many sessions are held, expired ones not counted.
func (s *Sessions) Len(ctx context.Context) (int, error) { return s.st.Count(ctx, sessionPrefix+":") }

func (s *Sessions) expired(sess stored, now time.Time) bool {
	return now.Sub(sess.LastSeen) >= s.cfg.Idle || now.Sub(sess.Created) >= s.cfg.Absolute
}

// sweep drops expired sessions, at most once a minute per server, so a sign-in does
// not run a delete every time and a table nobody reads still empties.
func (s *Sessions) sweep(ctx context.Context, now time.Time) {
	s.mu.Lock()
	due := now.Sub(s.swept) >= time.Minute
	if due {
		s.swept = now
	}
	s.mu.Unlock()
	if due {
		if err := s.st.Sweep(ctx); err != nil {
			s.fail("sweep sessions", err)
		}
	}
}
