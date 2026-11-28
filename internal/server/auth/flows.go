package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/nitpick-oss/nitpick/internal/server"
	"github.com/nitpick-oss/nitpick/internal/server/state"
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

// flows is a bounded, expiring table of started sign-ins, in the shared state so the
// replica that gets the answer need not be the one that sent the person away. A table
// nobody drains would let anyone fill it by starting sign-ins and never finishing, so
// it is capped and drops the oldest.
type flows struct {
	max   int
	st    *state.Store
	onErr func(op string, err error)
}

func newFlows(max int) *flows { return &flows{max: max} }

// use gives the table the shared state to keep flows in.
func (f *flows) use(st *state.Store, onErr func(op string, err error)) { f.st, f.onErr = st, onErr }

func (f *flows) fail(op string, err error) {
	if f.onErr != nil {
		f.onErr(op, err)
	}
}

// A client address may start startMax sign-ins in startWindow. The table drops its
// oldest flow when full, so without a limit anyone could loop on the login URL and
// push real sign-ins out. The limit is far above what a person or an office behind
// one address does, and far below the table's size.
const (
	startMax    = 20
	startWindow = time.Minute
)

// maxStartAddrs bounds the addresses tracked at once. It is a variable only so a
// test can bound it at something a database can be filled to.
var maxStartAddrs = 100_000

// admit reports whether addr may start a sign-in now, and counts it if so. When
// the table of addresses is full of recent ones, it refuses: a flood from many
// addresses is then a refusal to start, not an eviction of sign-ins in progress. A
// store that cannot be reached refuses too.
func (f *flows) admit(ctx context.Context, addr string) bool {
	key := state.Key("start", addr)
	if n, err := f.st.Count(ctx, "start:"); err == nil && n >= maxStartAddrs {
		_ = f.st.Sweep(ctx)
		if _, known, _ := f.st.Get(ctx, key); !known {
			if n, _ := f.st.Count(ctx, "start:"); n >= maxStartAddrs {
				return false
			}
		}
	}
	allowed := false
	err := f.st.Update(ctx, key, func(cur *state.Entry) (*state.Entry, error) {
		allowed = false
		now := f.st.Now()
		cutoff := now.Add(-startWindow).UnixNano()
		var times []int64
		if cur != nil {
			_ = json.Unmarshal(cur.Value, &times)
		}
		recent := times[:0]
		for _, t := range times {
			if t > cutoff {
				recent = append(recent, t)
			}
		}
		if len(recent) >= startMax {
			b, _ := json.Marshal(recent)
			return &state.Entry{Value: b, Expires: now.Add(startWindow)}, nil
		}
		recent = append(recent, now.UnixNano())
		b, err := json.Marshal(recent)
		if err != nil {
			return nil, err
		}
		allowed = true
		return &state.Entry{Value: b, Expires: now.Add(startWindow)}, nil
	})
	if err != nil {
		f.fail("count sign-in starts", err)
		return false
	}
	return allowed
}

// flowFields is a flow as it is kept.
type flowFields struct {
	Binding  [32]byte `json:"b"`
	Secret   string   `json:"s"`
	Verifier string   `json:"v,omitempty"`
	Next     string   `json:"n,omitempty"`
}

// put stores a flow under a fresh random key and returns the key. The table holds
// the key's hash, so a copy of it holds nothing that completes a sign-in.
func (f *flows) put(ctx context.Context, fl *flow) (string, error) {
	key, err := randomToken()
	if err != nil {
		return "", err
	}
	if f.max > 0 {
		if n, err := f.st.Count(ctx, "flow:"); err == nil && n >= f.max {
			_, _ = f.st.DeleteOldest(ctx, "flow:")
		}
	}
	b, err := json.Marshal(flowFields{Binding: fl.binding, Secret: fl.secret, Verifier: fl.verifier, Next: fl.next})
	if err != nil {
		return "", err
	}
	fl.expires = f.st.Now().Add(flowTTL)
	if err := f.st.Put(ctx, state.Key("flow", key), state.Entry{Value: b, Expires: fl.expires}); err != nil {
		return "", err
	}
	return key, nil
}

// take removes and returns a flow. A key is good for one answer: taking it first
// and checking it after means a replayed or raced answer finds nothing, on whichever
// replica it arrives.
func (f *flows) take(ctx context.Context, key string) (*flow, bool) {
	e, ok, err := f.st.Take(ctx, state.Key("flow", key))
	if err != nil {
		f.fail("take a sign-in", err)
		return nil, false
	}
	if !ok {
		return nil, false
	}
	var ff flowFields
	if err := json.Unmarshal(e.Value, &ff); err != nil {
		f.fail("decode a sign-in", err)
		return nil, false
	}
	return &flow{binding: ff.Binding, secret: ff.Secret, verifier: ff.Verifier, next: ff.Next, expires: e.Expires}, true
}

func (f *flows) len(ctx context.Context) int {
	n, _ := f.st.Count(ctx, "flow:")
	return n
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
