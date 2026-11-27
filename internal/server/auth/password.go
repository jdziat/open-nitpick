package auth

import (
	"log/slog"
	"net"
	"net/http"
	"strings"

	"github.com/nitpick-oss/nitpick/internal/server"
)

// Password signs in local accounts from a users file.
type Password struct {
	Users *Users

	// ByAccount and ByAddress lock a name and a source that keep failing. Each
	// is its own limiter, with its own counts.
	ByAccount *Limiter
	ByAddress *Limiter

	// TrustForwardedFor reads the source address from X-Forwarded-For, for a
	// deployment behind a proxy. Off, the address is the connection's, which a
	// client cannot choose. On, it is whatever the proxy says: set it only where
	// the proxy overwrites the header.
	TrustForwardedFor bool

	Log *slog.Logger

	// hashing bounds the argon2id hashes in flight.
	hashing chan struct{}
}

// maxConcurrentHashes is how many password hashes run at once.
const maxConcurrentHashes = 4

// NewPassword builds the provider with the default limits.
func NewPassword(users *Users, log *slog.Logger) *Password {
	if log == nil {
		log = slog.Default()
	}
	return &Password{Users: users, ByAccount: DefaultLimiter(), ByAddress: &Limiter{
		// An address is allowed more misses than an account: an office shares one.
		Max: 20, Window: DefaultLimiter().Window, Lockout: DefaultLimiter().Lockout, Keys: 100_000,
	}, Log: log, hashing: make(chan struct{}, maxConcurrentHashes)}
}

// ID implements server.Provider.
func (p *Password) ID() string { return "password" }

// Label implements server.Provider.
func (p *Password) Label() string { return "Password" }

// Form implements server.Provider.
func (p *Password) Form() server.LoginForm { return server.FormCredentials }

// CrossSitePosts implements server.Provider. A password form is posted by this
// site's own page, so it has no cross-site entry and its CSRF token is required.
func (p *Password) CrossSitePosts() []string { return nil }

// Mount implements server.Provider.
func (p *Password) Mount(mux *http.ServeMux, host server.Host) {
	mux.HandleFunc("POST /auth/password/login", func(w http.ResponseWriter, r *http.Request) { p.login(w, r, host) })
}

// refused is every refusal's text. It is the same for an unknown account, a
// wrong password and a locked one, so a response does not say which names exist
// or which are being guessed at.
const refused = "That username and password did not match, or sign-in is paused for this account. Try again later."

func (p *Password) login(w http.ResponseWriter, r *http.Request, host server.Host) {
	// The form's own CSRF check, before anything about the account is touched:
	// without it another site could make a visitor's browser guess for it.
	secret, ok := host.CSRF().PreSessionSecret(r)
	if !ok || !validToken(host.CSRF(), r, secret) {
		http.Error(w, "the request did not come from this site", http.StatusForbidden)
		return
	}

	name := strings.TrimSpace(r.PostFormValue("username"))
	password := r.PostFormValue("password")
	// A password is bounded before it is hashed: argon2 work grows with input
	// only to its block size, but a megabyte form field is not a password.
	if name == "" || password == "" || len(name) > 256 || len(password) > 1024 {
		host.LoginFailed(w, r, http.StatusUnauthorized, refused)
		return
	}
	addr := p.address(r)
	acct := "account:" + strings.ToLower(name)
	src := "address:" + addr

	// Each attempt is charged before it is hashed, atomically with the check, so
	// parallel requests cannot all pass the check before any is counted. A charge
	// is resolved by Fail (a wrong password) or Success (a right one). Any other
	// way out of this function refunds it, so no path leaks a charge.
	limits := []struct {
		l   *Limiter
		key string
	}{{p.ByAddress, src}, {p.ByAccount, acct}}
	charged := 0
	resolved := false
	defer func() {
		if resolved {
			return
		}
		for _, c := range limits[:charged] {
			c.l.Refund(c.key)
		}
	}()
	for _, c := range limits {
		if allowed, _ := c.l.Take(c.key); !allowed {
			// Refused before the hash is computed: a locked account costs the
			// server nothing, which is the point of locking it.
			p.Log.Warn("sign-in refused while locked", "provider", "password", "key", c.key)
			host.LoginFailed(w, r, http.StatusTooManyRequests, refused)
			return
		}
		charged++
	}

	// One argon2id hash holds 64 MiB. Hashing is bounded so many sources at once
	// cannot exhaust memory, and a request waits its turn or gives up with its
	// client.
	if p.hashing != nil {
		select {
		case p.hashing <- struct{}{}:
			defer func() { <-p.hashing }()
		case <-r.Context().Done():
			return
		}
	}
	account, groups, ok := p.Users.Verify(name, password)
	resolved = true
	if !ok {
		p.ByAccount.Fail(acct)
		p.ByAddress.Fail(src)
		p.Log.Warn("sign-in failed", "provider", "password", "account", strings.ToLower(name), "address", addr)
		host.LoginFailed(w, r, http.StatusUnauthorized, refused)
		return
	}
	p.ByAccount.Success(acct)
	p.ByAddress.Success(src)
	host.SignIn(w, r, server.Identity{Provider: p.ID(), Subject: account, Name: account, Groups: groups}, r.PostFormValue("next"))
}

// validToken reads the token the login form carries.
func validToken(c *server.CSRF, r *http.Request, secret [32]byte) bool {
	return c.Valid(r, secret)
}

func (p *Password) address(r *http.Request) string { return clientAddress(r, p.TrustForwardedFor) }

// clientAddress is who a request is from, for a limiter. Behind a proxy that
// overwrites X-Forwarded-For, the last entry is the one the nearest proxy added;
// earlier ones are what the client claimed.
func clientAddress(r *http.Request, trustForwardedFor bool) string {
	if trustForwardedFor {
		if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
			parts := strings.Split(fwd, ",")
			if a := strings.TrimSpace(parts[len(parts)-1]); a != "" {
				return a
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
