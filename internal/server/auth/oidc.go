package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/nitpick-oss/nitpick/internal/server"
)

// OIDCConfig is one OpenID Connect identity provider.
type OIDCConfig struct {
	Issuer       string
	ClientID     string
	ClientSecret string
	// RedirectURL is where the provider sends the person back: this server's
	// /auth/oidc/callback as the provider was told it.
	RedirectURL string
	Scopes      []string // default openid, profile, email
	// GroupsClaim is the ID token claim that lists the person's groups, for the
	// role rules. Empty means "groups".
	GroupsClaim string
	// Label is what the login page calls the provider.
	Label string
	// HTTPClient reaches the provider. Nil is the default client.
	HTTPClient *http.Client
}

// OIDC signs people in with an OpenID Connect provider using the authorization
// code flow with PKCE.
type OIDC struct {
	cfg      OIDCConfig
	oauth    *oauth2.Config
	verifier *oidc.IDTokenVerifier
	flows    *flows
	log      *slog.Logger
	label    string
}

const oidcCookie = "__Host-nitpick_oidc"

// NewOIDC discovers the provider and builds the flow. It fails when the provider
// cannot be reached or named, at start-up and not at the first sign-in: a server
// that starts with a sign-in that cannot work is worse than one that does not.
func NewOIDC(ctx context.Context, cfg OIDCConfig, log *slog.Logger) (*OIDC, error) {
	if cfg.Issuer == "" || cfg.ClientID == "" || cfg.RedirectURL == "" {
		return nil, errors.New("oidc needs an issuer, a client id and a redirect url")
	}
	if log == nil {
		log = slog.Default()
	}
	if cfg.HTTPClient != nil {
		ctx = oidc.ClientContext(ctx, cfg.HTTPClient)
	}
	provider, err := oidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery for %s: %w", cfg.Issuer, err)
	}
	scopes := cfg.Scopes
	if len(scopes) == 0 {
		scopes = []string{oidc.ScopeOpenID, "profile", "email"}
	}
	if !containsScope(scopes, oidc.ScopeOpenID) {
		scopes = append([]string{oidc.ScopeOpenID}, scopes...)
	}
	label := cfg.Label
	if label == "" {
		label = "OpenID Connect"
	}
	if cfg.GroupsClaim == "" {
		cfg.GroupsClaim = "groups"
	}
	return &OIDC{
		cfg: cfg,
		oauth: &oauth2.Config{
			ClientID: cfg.ClientID, ClientSecret: cfg.ClientSecret, RedirectURL: cfg.RedirectURL,
			Endpoint: provider.Endpoint(), Scopes: scopes,
		},
		verifier: provider.Verifier(&oidc.Config{ClientID: cfg.ClientID}),
		flows:    newFlows(10_000),
		log:      log,
		label:    label,
	}, nil
}

func containsScope(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// ID implements server.Provider.
func (o *OIDC) ID() string { return "oidc" }

// Label implements server.Provider.
func (o *OIDC) Label() string { return o.label }

// Form implements server.Provider.
func (o *OIDC) Form() server.LoginForm { return server.FormRedirect }

// CrossSitePosts implements server.Provider. The callback is a GET, which a
// redirect from the provider makes with the cookie SameSite=Lax allows.
func (o *OIDC) CrossSitePosts() []string { return nil }

// Mount implements server.Provider.
func (o *OIDC) Mount(mux *http.ServeMux, host server.Host) {
	mux.HandleFunc("GET /auth/oidc/login", o.login)
	mux.HandleFunc("GET /auth/oidc/callback", func(w http.ResponseWriter, r *http.Request) { o.callback(w, r, host) })
}

func (o *OIDC) login(w http.ResponseWriter, r *http.Request) {
	nonce, err := randomToken()
	if err != nil {
		http.Error(w, "could not start the sign-in", http.StatusInternalServerError)
		return
	}
	verifier := oauth2.GenerateVerifier()
	binding, err := bindCookie(w, oidcCookie, "/", http.SameSiteLaxMode)
	if err != nil {
		http.Error(w, "could not start the sign-in", http.StatusInternalServerError)
		return
	}
	state, err := o.flows.put(&flow{binding: binding, secret: nonce, verifier: verifier, next: r.URL.Query().Get("next")})
	if err != nil {
		http.Error(w, "could not start the sign-in", http.StatusInternalServerError)
		return
	}
	url := o.oauth.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier), oidc.Nonce(nonce))
	http.Redirect(w, r, url, http.StatusFound)
}

func (o *OIDC) callback(w http.ResponseWriter, r *http.Request, host server.Host) {
	q := r.URL.Query()
	// The flow is taken before anything is checked: whatever happens next, this
	// state is spent, so an answer cannot be tried twice.
	fl, ok := o.flows.take(q.Get("state"))
	if !ok {
		o.fail(w, r, host, "unknown or expired state")
		return
	}
	if !bound(r, oidcCookie, fl.binding) {
		o.fail(w, r, host, "the sign-in was not started by this browser")
		return
	}
	if e := q.Get("error"); e != "" {
		o.fail(w, r, host, "the provider refused the sign-in: "+e)
		return
	}
	code := q.Get("code")
	if code == "" {
		o.fail(w, r, host, "no authorization code")
		return
	}
	ctx := r.Context()
	if o.cfg.HTTPClient != nil {
		ctx = oidc.ClientContext(ctx, o.cfg.HTTPClient)
	}
	token, err := o.oauth.Exchange(ctx, code, oauth2.VerifierOption(fl.verifier))
	if err != nil {
		o.fail(w, r, host, "the code exchange failed: "+err.Error())
		return
	}
	raw, _ := token.Extra("id_token").(string)
	if raw == "" {
		o.fail(w, r, host, "the provider returned no ID token")
		return
	}
	idt, err := o.verifier.Verify(ctx, raw)
	if err != nil {
		o.fail(w, r, host, "the ID token did not verify: "+err.Error())
		return
	}
	if idt.Nonce != fl.secret {
		o.fail(w, r, host, "the ID token's nonce does not match")
		return
	}
	var claims map[string]any
	if err := idt.Claims(&claims); err != nil {
		o.fail(w, r, host, "the ID token claims could not be read")
		return
	}
	if idt.Subject == "" {
		o.fail(w, r, host, "the ID token has no subject")
		return
	}
	host.SignIn(w, r, server.Identity{
		Provider: o.ID(),
		Subject:  idt.Issuer + "#" + idt.Subject,
		Name:     firstString(claims, "name", "preferred_username", "email"),
		Groups:   stringList(claims[o.cfg.GroupsClaim]),
	}, fl.next)
}

// fail answers a refused sign-in. The reason goes to the log; the person is told
// only that it did not work, because the reason can name a key or an endpoint.
func (o *OIDC) fail(w http.ResponseWriter, r *http.Request, host server.Host, reason string) {
	o.log.Warn("oidc sign-in refused", "reason", reason)
	host.LoginFailed(w, r, http.StatusUnauthorized, "Sign-in with "+o.label+" did not complete. Try again.")
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := m[k].(string); ok && strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

// stringList reads a claim that is a list of strings, or one string.
func stringList(v any) []string {
	switch x := v.(type) {
	case []any:
		var out []string
		for _, e := range x {
			if s, ok := e.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	case string:
		if x != "" {
			return []string{x}
		}
	}
	return nil
}
