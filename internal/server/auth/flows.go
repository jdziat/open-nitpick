package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"sync"
	"time"
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
	mu    sync.Mutex
	max   int
	now   func() time.Time
	items map[string]*flow
}

func newFlows(max int) *flows { return &flows{max: max, items: map[string]*flow{}} }

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

// bindCookie ties a flow to the browser that started it. Without it, an attacker
// could start a sign-in as themselves and get a victim's browser to finish it,
// signing the victim in to the attacker's account.
func bindCookie(w http.ResponseWriter, name, path string, sameSite http.SameSite) ([32]byte, error) {
	value, err := randomToken()
	if err != nil {
		return [32]byte{}, err
	}
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: value, Path: path, MaxAge: int(flowTTL / time.Second),
		HttpOnly: true, Secure: true, SameSite: sameSite,
	})
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
