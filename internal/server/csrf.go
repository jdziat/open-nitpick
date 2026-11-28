package server

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/nitpick-oss/nitpick/internal/server/state"
)

// CSRF protects requests that change state and are authorised by a cookie. The
// token is an HMAC of a secret bound to the session; before sign-in the login
// form's secret is a cookie of its own.
type CSRF struct {
	key [32]byte
}

// PreSessionCookie holds the secret of the login form before there is a session.
const PreSessionCookie = "__Host-nitpick_csrf"

// FormField and Header are where a request carries the token.
const (
	CSRFFormField = "csrf_token"
	CSRFHeader    = "X-CSRF-Token"
)

// csrfKeyEntry is where the key is kept. It never expires: a key that changed would
// invalidate every form a person has open, and every session's token with it.
const csrfKeyEntry = "secret:csrf"

// farFuture is "never" for an entry, within what both engines can store.
func farFuture(now time.Time) time.Time { return now.AddDate(100, 0, 0) }

// NewCSRF returns a protector keyed by a secret every server shares. The first server
// to ask makes it and the rest read it, so a form served by one replica is accepted by
// another.
func NewCSRF(ctx context.Context, st *state.Store) (*CSRF, error) {
	c := &CSRF{}
	err := st.Update(ctx, csrfKeyEntry, func(cur *state.Entry) (*state.Entry, error) {
		if cur != nil && len(cur.Value) == len(c.key) {
			copy(c.key[:], cur.Value)
			return cur, nil
		}
		// None yet, or one of the wrong size, which is replaced and not trusted.
		var fresh [32]byte
		if _, err := rand.Read(fresh[:]); err != nil {
			return nil, err
		}
		c.key = fresh
		return &state.Entry{Value: fresh[:], Expires: farFuture(st.Now())}, nil
	})
	if err != nil {
		return nil, err
	}
	return c, nil
}

// Token is what a page puts in its forms for a session's secret.
func (c *CSRF) Token(secret [32]byte) string {
	mac := hmac.New(sha256.New, c.key[:])
	mac.Write([]byte("csrf-v1\x00"))
	mac.Write(secret[:])
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// Valid reports whether the request carries the token for the secret.
func (c *CSRF) Valid(r *http.Request, secret [32]byte) bool {
	got := r.Header.Get(CSRFHeader)
	if got == "" {
		// ParseForm reads a body, and only a form carries a field.
		if strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") ||
			strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
			got = r.PostFormValue(CSRFFormField)
		}
	}
	want := c.Token(secret)
	return got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// PreSession returns the secret for a login form, setting the cookie that holds
// it when the browser has none. The secret is the cookie's value, so a form
// served to one browser is no use to another.
func (c *CSRF) PreSession(w http.ResponseWriter, r *http.Request) ([32]byte, error) {
	if ck, err := r.Cookie(PreSessionCookie); err == nil && ck.Value != "" {
		return sha256.Sum256([]byte(ck.Value)), nil
	}
	value, err := newToken()
	if err != nil {
		return [32]byte{}, err
	}
	if err := SetCookie(w, &http.Cookie{
		Name: PreSessionCookie, Value: value, Path: "/",
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, MaxAge: 3600,
	}); err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256([]byte(value)), nil
}

// PreSessionSecret is the secret of a login request that already has the
// cookie, and false when it has none.
func (c *CSRF) PreSessionSecret(r *http.Request) ([32]byte, bool) {
	ck, err := r.Cookie(PreSessionCookie)
	if err != nil || ck.Value == "" {
		return [32]byte{}, false
	}
	return sha256.Sum256([]byte(ck.Value)), true
}

// safeMethod is a method that does not change state.
func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

// sameOrigin refuses a request a browser says came from another site. Origin is
// sent on every cross-origin request that changes state and names the page that
// made it; Sec-Fetch-Site says the same without a name. A request that carries
// neither is not from a browser that does, and the token still has to match.
func sameOrigin(r *http.Request) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" || origin == "null" {
		// "null" is what a sandboxed frame or a redirect chain sends. It names no
		// page of ours.
		return origin == ""
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}
