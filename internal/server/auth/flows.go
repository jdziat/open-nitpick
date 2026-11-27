package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/nitpick-oss/nitpick/internal/server"
)

// flowTTL is how long a sign-in may take between the redirect out and the
// answer back. It is long enough for a person to type a password and a
// second factor, and short enough that an abandoned one does not linger.
const flowTTL = 10 * time.Minute

// flow is a sign-in that has started and not finished. It is kept on the server,
// keyed by a value that goes through the identity provider and comes back, so
// what it holds (the nonce, the PKCE verifier, where to go next) never travels
// through the browser.
type flow struct {
	binding  [32]byte // the hash of the cookie value of the browser that started it
	secret   string   // the nonce for OIDC, the request ID for SAML
	verifier string   // the PKCE verifier, for OIDC
	next     string
	expires  time.Time
}

// flows is a bounded, expiring table of started sign-ins. A table nobody drains
// would let anyone fill it by starting sign-ins and never finishing, so it is
// capped and drops the oldest.
type flows struct {
	mu     sync.Mutex
	max    int
	now    func() time.Time
	items  map[string]*flow
	starts map[string][]time.Time // by client address, the starts in the last startWindow
}

func newFlows(max int) *flows {
	return &flows{max: max, items: map[string]*flow{}, starts: map[string][]time.Time{}}
}

// A client address may start startMax sign-ins in startWindow. The table drops its
// oldest flow when full, so without a limit anyone could loop on the login URL and
// push real sign-ins out. The limit is far above what a person or an office behind
// one address does, and far below the table's size.
const (
	startMax      = 20
	startWindow   = time.Minute
	maxStartAddrs = 100_000
)

// admit reports whether addr may start a sign-in now, and counts it if so. When
// the table of addresses is full of recent ones, it refuses: a flood from many
// addresses is then a refusal to start, not an eviction of sign-ins in progress.
func (f *flows) admit(addr string) bool {
	now := f.clock()
	cutoff := now.Add(-startWindow)
	f.mu.Lock()
	defer f.mu.Unlock()
	recent := f.starts[addr][:0]
	for _, t := range f.starts[addr] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	if len(recent) >= startMax {
		f.starts[addr] = recent
		return false
	}
	if _, known := f.starts[addr]; !known && len(f.starts) >= maxStartAddrs {
		for a, ts := range f.starts {
			if len(ts) == 0 || !ts[len(ts)-1].After(cutoff) {
				delete(f.starts, a)
			}
		}
		if len(f.starts) >= maxStartAddrs {
			return false
		}
	}
	f.starts[addr] = append(recent, now)
	return true
}

func (f *flows) clock() time.Time {
	if f.now != nil {
		return f.now()
	}
	return time.Now()
}

// put stores a flow under a fresh random key and returns the key.
func (f *flows) put(fl *flow) (string, error) {
	key, err := randomToken()
	if err != nil {
		return "", err
	}
	now := f.clock()
	fl.expires = now.Add(flowTTL)
	f.mu.Lock()
	defer f.mu.Unlock()
	for k, v := range f.items {
		if now.After(v.expires) {
			delete(f.items, k)
		}
	}
	if f.max > 0 && len(f.items) >= f.max {
		var oldest string
		var at time.Time
		for k, v := range f.items {
			if oldest == "" || v.expires.Before(at) {
				oldest, at = k, v.expires
			}
		}
		delete(f.items, oldest)
	}
	f.items[key] = fl
	return key, nil
}

// take removes and returns a flow. A key is good for one answer: taking it first
// and checking it after means a replayed or raced answer finds nothing.
func (f *flows) take(key string) (*flow, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fl, ok := f.items[key]
	if !ok {
		return nil, false
	}
	delete(f.items, key)
	if f.clock().After(fl.expires) {
		return nil, false
	}
	return fl, true
}

func (f *flows) len() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.items)
}

func randomToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// tooManyStarts answers an address that is starting sign-ins too fast.
func tooManyStarts(w http.ResponseWriter) {
	w.Header().Set("Retry-After", strconv.Itoa(int(startWindow/time.Second)))
	http.Error(w, "too many sign-in attempts from this address; try again in a minute", http.StatusTooManyRequests)
}

// bindCookie ties a flow to the browser that started it. Without it, an attacker
// could start a sign-in as themselves and get a victim's browser to finish it,
// signing the victim in to the attacker's account.
func bindCookie(w http.ResponseWriter, name, path string, sameSite http.SameSite) ([32]byte, error) {
	value, err := randomToken()
	if err != nil {
		return [32]byte{}, err
	}
	if err := server.SetCookie(w, &http.Cookie{
		Name: name, Value: value, Path: path, MaxAge: int(flowTTL / time.Second),
		HttpOnly: true, Secure: true, SameSite: sameSite,
	}); err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256([]byte(value)), nil
}

// bound reports whether the request's cookie is the one the flow was started
// with.
func bound(r *http.Request, name string, want [32]byte) bool {
	c, err := r.Cookie(name)
	if err != nil || c.Value == "" {
		return false
	}
	got := sha256.Sum256([]byte(c.Value))
	return subtle.ConstantTimeCompare(got[:], want[:]) == 1
}
