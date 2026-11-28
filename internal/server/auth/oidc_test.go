package auth

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v4"

	"github.com/nitpick-oss/nitpick/internal/server"
)

type oidcSite struct {
	*site
	idp *fakeIDP
	o   *OIDC
}

func newOIDCSite(t *testing.T, idp *fakeIDP, rules ...server.RoleRule) *oidcSite {
	t.Helper()
	// The provider must be told the server's address before the server has one, so
	// serve through a placeholder and fill it in.
	var handler http.Handler
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler.ServeHTTP(w, r) }))
	t.Cleanup(ts.Close)
	o, err := NewOIDC(context.Background(), OIDCConfig{
		Issuer: idp.srv.URL, ClientID: "client-1", ClientSecret: "secret", RedirectURL: ts.URL + "/auth/oidc/callback",
		GroupsClaim: "groups", HTTPClient: idp.srv.Client(),
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	roles, err := server.NewRoleMap(rules)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := server.New(server.Config{
		Components: []server.Component{echo{}}, Providers: []server.Provider{o}, Roles: roles, Assets: builtUI(),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	handler = srv
	jar, _ := cookiejar.New(nil)
	c := ts.Client()
	c.Jar = jar
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &oidcSite{site: &site{t: t, ts: ts, c: c, srv: srv}, idp: idp, o: o}
}

// begin starts a sign-in and walks the browser through the provider. It stops
// before the callback and returns the callback URL.
func (s *oidcSite) begin(next string) string {
	s.t.Helper()
	resp, err := s.c.Get(s.ts.URL + "/auth/oidc/login?next=" + url.QueryEscape(next))
	if err != nil {
		s.t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		s.t.Fatalf("login = %d", resp.StatusCode)
	}
	authURL := resp.Header.Get("Location")
	if !strings.HasPrefix(authURL, s.idp.srv.URL+"/authorize") {
		s.t.Fatalf("the browser was sent to %q, not the provider", authURL)
	}
	// The provider's own client carries none of this site's cookies.
	pc := s.idp.srv.Client()
	pc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	r2, err := pc.Get(authURL)
	if err != nil {
		s.t.Fatal(err)
	}
	_ = r2.Body.Close()
	return r2.Header.Get("Location")
}

func (s *oidcSite) finish(callback string) (reply, string) {
	s.t.Helper()
	resp, err := s.c.Get(callback)
	if err != nil {
		s.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return replyOf(resp), string(b)
}

func (s *oidcSite) whoami() (int, string) {
	s.t.Helper()
	resp, err := s.c.Get(s.ts.URL + "/v1/echo/who")
	if err != nil {
		s.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestAnOIDCSignInSucceedsAndMapsGroupsToRoles(t *testing.T) {
	idp := newFakeIDP(t)
	idp.groups = []any{"staff", "ops"}
	s := newOIDCSite(t, idp, server.RoleRule{Roles: []server.Role{"admin"}, Provider: "oidc", Group: "ops"})

	resp, body := s.finish(s.begin("/ui/somewhere"))
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/ui/somewhere" {
		t.Fatalf("callback = %d %q to %q", resp.StatusCode, body, resp.Header.Get("Location"))
	}
	id := lastIdentity(t, s.site)
	if id.Provider != "oidc" || id.Subject != idp.srv.URL+"#user-1" || id.Name != "Una Person" {
		t.Errorf("identity = %+v", id)
	}
	if !id.Has("admin") {
		t.Errorf("a member of ops holds %v, want admin", id.Roles)
	}
}

// A callback that is wrong in any way ends with no session. Each case changes
// one thing about a good sign-in.
func TestAnOIDCCallbackThatIsWrongInAnyWayDoesNotSignIn(t *testing.T) {
	for name, tamper := range map[string]func(*fakeIDP){
		"an expired token":   func(p *fakeIDP) { p.expires = -time.Hour },
		"the wrong audience": func(p *fakeIDP) { p.audience = "someone-elses-client" },
		"the wrong issuer":   func(p *fakeIDP) { p.issuer = "https://evil.example" },
		"a different nonce":  func(p *fakeIDP) { p.tamperNonce = true },
		"no nonce at all":    func(p *fakeIDP) { p.nonce = "-" },
		"a token signed by another key": func(p *fakeIDP) {
			k := newFakeIDP(t)
			p.signWith = k.key
		},
		"an unsigned token (alg none)": func(p *fakeIDP) { p.alg = jwt.SigningMethodNone },
		"no ID token":                  func(p *fakeIDP) { p.omitIDToken = true },
		"no subject":                   func(p *fakeIDP) { p.subject = "" },
	} {
		idp := newFakeIDP(t)
		tamper(idp)
		s := newOIDCSite(t, idp)
		resp, _ := s.finish(s.begin("/ui/"))
		if resp.StatusCode == http.StatusSeeOther {
			t.Errorf("%s: the callback signed someone in", name)
		}
		if code, _ := s.whoami(); code != http.StatusUnauthorized {
			t.Errorf("%s: a session exists afterwards (%d)", name, code)
		}
	}
	// The untampered flow signs in, so each refusal above is its one change.
	idp := newFakeIDP(t)
	s := newOIDCSite(t, idp)
	if resp, _ := s.finish(s.begin("/ui/")); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("the untampered flow = %d, want a sign-in", resp.StatusCode)
	}
}

// The first answer spends the state, whatever it says. A replay finds nothing.
func TestAnOIDCStateCanBeUsedOnce(t *testing.T) {
	idp := newFakeIDP(t)
	s := newOIDCSite(t, idp)
	callback := s.begin("/ui/")
	if resp, _ := s.finish(callback); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("the first answer = %d", resp.StatusCode)
	}
	jar, _ := cookiejar.New(nil)
	s.c.Jar = jar // forget the session; keep nothing
	// The state is gone, so a replay is refused even with the binding cookie.
	s2 := s.begin("/ui/") // a fresh flow, to get a cookie the replay can reuse
	_ = s2
	if resp, _ := s.finish(callback); resp.StatusCode == http.StatusSeeOther {
		t.Error("a replayed callback signed someone in")
	}
}

func TestAnOIDCStateThatWasNeverIssuedIsRefused(t *testing.T) {
	idp := newFakeIDP(t)
	s := newOIDCSite(t, idp)
	s.begin("/ui/") // so the cookie exists
	for name, q := range map[string]string{
		"a made-up state": "code=x&state=made-up",
		"no state":        "code=x",
		"an empty state":  "code=x&state=",
	} {
		if resp, _ := s.finish(s.ts.URL + "/auth/oidc/callback?" + q); resp.StatusCode == http.StatusSeeOther {
			t.Errorf("%s signed someone in", name)
		}
	}
}

// Login CSRF. An attacker signs in as themselves and hands a victim the callback
// URL. The victim's browser did not start that flow and cannot finish it.
func TestAnOIDCFlowCannotBeFinishedInAnotherBrowser(t *testing.T) {
	idp := newFakeIDP(t)
	s := newOIDCSite(t, idp)
	callback := s.begin("/ui/") // the attacker's browser started it

	victim := &http.Client{Transport: s.c.Transport, CheckRedirect: s.c.CheckRedirect}
	jar, _ := cookiejar.New(nil)
	victim.Jar = jar
	resp, err := victim.Get(callback)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode == http.StatusSeeOther {
		t.Fatal("a callback finished in a different browser signed it in")
	}
	// The attempt spent the state, so the attacker cannot retry it either.
	if resp, _ := s.finish(callback); resp.StatusCode == http.StatusSeeOther {
		t.Error("the state survived a failed attempt and was usable afterwards")
	}
	// A fresh flow in the browser that started it completes.
	if resp, _ := s.finish(s.begin("/ui/")); resp.StatusCode != http.StatusSeeOther {
		t.Errorf("the same browser's own flow = %d, want a sign-in", resp.StatusCode)
	}
}

// PKCE binds the code to a verifier only this server knows. A stolen code is no use.
func TestAnOIDCCodeIsBoundToThisServersPKCEVerifier(t *testing.T) {
	idp := newFakeIDP(t)
	s := newOIDCSite(t, idp)
	callback := s.begin("/ui/")
	u, _ := url.Parse(callback)
	code := u.Query().Get("code")

	// The provider refuses a code exchanged without the verifier.
	pc := idp.srv.Client()
	resp, err := pc.PostForm(idp.srv.URL+"/token", url.Values{"code": {code}, "client_id": {"client-1"}, "code_verifier": {"guess"}})
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("an exchange with the wrong verifier = %d, want 400", resp.StatusCode)
	}
}

func TestAnAuthorizationRequestCarriesPKCEAndANonce(t *testing.T) {
	idp := newFakeIDP(t)
	s := newOIDCSite(t, idp)
	resp, err := s.c.Get(s.ts.URL + "/auth/oidc/login")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	u, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" || len(q.Get("code_challenge")) < 40 {
		t.Errorf("the request lacks an S256 challenge: %v", q)
	}
	if len(q.Get("nonce")) < 40 || len(q.Get("state")) < 40 {
		t.Errorf("state or nonce too short to be random: %v", q)
	}
	if q.Get("response_type") != "code" || q.Get("client_id") != "client-1" || !strings.Contains(q.Get("scope"), "openid") {
		t.Errorf("request = %v", q)
	}
	// The cookie that binds the flow to the browser.
	var bind *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == oidcCookie {
			bind = c
		}
	}
	if bind == nil || !bind.HttpOnly || !bind.Secure || bind.SameSite != http.SameSiteLaxMode || bind.Path != "/" {
		t.Errorf("binding cookie = %+v", bind)
	}
}

// The provider refusing the sign-in is not a sign-in.
func TestAnErrorFromTheProviderIsNotASignIn(t *testing.T) {
	idp := newFakeIDP(t)
	s := newOIDCSite(t, idp)
	cb := s.begin("/ui/")
	u, _ := url.Parse(cb)
	q := u.Query()
	q.Del("code")
	q.Set("error", "access_denied")
	u.RawQuery = q.Encode()
	resp, body := s.finish(u.String())
	if resp.StatusCode == http.StatusSeeOther {
		t.Error("an access_denied answer signed someone in")
	}
	if strings.Contains(body, "access_denied") {
		t.Error("the provider's error text was shown to the person")
	}
}

// What the person is told does not carry the reason.
func TestAnOIDCRefusalDoesNotShowTheReason(t *testing.T) {
	idp := newFakeIDP(t)
	idp.audience = "wrong"
	s := newOIDCSite(t, idp)
	_, body := s.finish(s.begin("/ui/"))
	for _, leak := range []string{"audience", "wrong", "verify", idp.srv.URL, "jose", "oidc:"} {
		if strings.Contains(body, leak) {
			t.Errorf("the refusal shows %q:\n%s", leak, body)
		}
	}
	if !strings.Contains(body, "did not complete") {
		t.Errorf("the refusal does not say the sign-in failed:\n%s", body)
	}
}

// A group claim may be one string or a list, and anything else is no groups.
func TestGroupsAreReadFromAStringOrAList(t *testing.T) {
	for name, tc := range map[string]struct {
		claim any
		want  []string
	}{
		"a list":       {[]any{"a", "b"}, []string{"a", "b"}},
		"one string":   {"a", []string{"a"}},
		"empty items":  {[]any{"a", "", 3}, []string{"a"}},
		"a number":     {7, nil},
		"nothing":      {nil, nil},
		"empty string": {"", nil},
	} {
		got := stringList(tc.claim)
		if len(got) != len(tc.want) {
			t.Errorf("%s: %v, want %v", name, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s: %v, want %v", name, got, tc.want)
			}
		}
	}
}

// The flow table is bounded: starting sign-ins and never finishing them cannot
// fill memory.
func TestStartedFlowsAreBoundedAndExpire(t *testing.T) {
	now := time.Now()
	f := newFlows(3)
	f.now = func() time.Time { return now }
	var keys []string
	for i := 0; i < 5; i++ {
		k, err := f.put(&flow{secret: "n"})
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, k)
		now = now.Add(time.Second)
	}
	if f.len() > 3 {
		t.Errorf("the table holds %d flows, want at most 3", f.len())
	}
	if _, ok := f.take(keys[0]); ok {
		t.Error("the oldest flow survived the bound")
	}
	if _, ok := f.take(keys[4]); !ok {
		t.Error("the newest flow was dropped")
	}
	k, _ := f.put(&flow{})
	now = now.Add(flowTTL + time.Second)
	if _, ok := f.take(k); ok {
		t.Error("an expired flow was returned")
	}
}

func TestDiscoveryThatFailsStopsStartUp(t *testing.T) {
	dead := httptest.NewServer(http.NotFoundHandler())
	dead.Close()
	if _, err := NewOIDC(context.Background(), OIDCConfig{Issuer: dead.URL, ClientID: "c", RedirectURL: "https://x/cb"}, nil); err == nil {
		t.Error("an unreachable provider was accepted at start-up")
	}
	_ = dead
	for name, cfg := range map[string]OIDCConfig{
		"no issuer":       {ClientID: "c", RedirectURL: "https://x/cb"},
		"no client id":    {Issuer: "https://i", RedirectURL: "https://x/cb"},
		"no redirect url": {Issuer: "https://i", ClientID: "c"},
	} {
		if _, err := NewOIDC(context.Background(), cfg, nil); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

// An error answer from the provider is final. The server does not go on to ask
// for a token with it, so the token endpoint is never reached.
func TestAnErrorAnswerNeverReachesTheTokenEndpoint(t *testing.T) {
	idp := newFakeIDP(t)
	s := newOIDCSite(t, idp)
	cb := s.begin("/ui/")
	u, _ := url.Parse(cb)
	q := u.Query()
	q.Set("error", "access_denied") // the code is a valid one: only the error should stop it
	u.RawQuery = q.Encode()
	s.finish(u.String())
	if idp.tokenRequests != 0 {
		t.Errorf("the provider's token endpoint was asked %d times after an error answer", idp.tokenRequests)
	}
	// Rule 10: without the error parameter, the same valid code is exchanged.
	s2 := newOIDCSite(t, idp)
	s2.finish(s2.begin("/ui/"))
	if idp.tokenRequests != 1 {
		t.Errorf("a normal sign-in asked the token endpoint %d times, want once", idp.tokenRequests)
	}
}

func TestACallbackWithNoCodeNeverReachesTheTokenEndpoint(t *testing.T) {
	idp := newFakeIDP(t)
	s := newOIDCSite(t, idp)
	cb := s.begin("/ui/")
	u, _ := url.Parse(cb)
	q := u.Query()
	q.Del("code")
	u.RawQuery = q.Encode()
	s.finish(u.String())
	if idp.tokenRequests != 0 {
		t.Errorf("the token endpoint was asked %d times with no code", idp.tokenRequests)
	}
}

// A token response with no ID token is not a sign-in, and is not read as one even
// though the access token is fine.
func TestATokenResponseWithoutAnIDTokenIsRefusedByName(t *testing.T) {
	idp := newFakeIDP(t)
	idp.omitIDToken = true
	var buf strings.Builder
	s := newOIDCSite(t, idp)
	s.o.log = slog.New(slog.NewTextHandler(&buf, nil))
	resp, _ := s.finish(s.begin("/ui/"))
	if resp.StatusCode == http.StatusSeeOther {
		t.Error("a token response with no ID token signed someone in")
	}
	if !strings.Contains(buf.String(), "returned no ID token") {
		t.Errorf("the refusal was not for the missing ID token:\n%s", buf.String())
	}
}

// The openid scope is always asked for, whatever the operator listed: without it
// the provider returns no ID token and the flow is not OpenID Connect.
func TestTheOpenIDScopeIsAlwaysRequested(t *testing.T) {
	for _, scopes := range [][]string{nil, {"profile"}, {"email", "groups"}, {"openid"}} {
		idp := newFakeIDP(t)
		o, err := NewOIDC(context.Background(), OIDCConfig{
			Issuer: idp.srv.URL, ClientID: "c", RedirectURL: "https://x/cb", Scopes: scopes, HTTPClient: idp.srv.Client(),
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, sc := range o.oauth.Scopes {
			if sc == "openid" {
				found = true
			}
		}
		if !found {
			t.Errorf("scopes %v became %v, which lacks openid", scopes, o.oauth.Scopes)
		}
		for _, want := range scopes {
			has := false
			for _, sc := range o.oauth.Scopes {
				has = has || sc == want
			}
			if !has {
				t.Errorf("scopes %v lost %q: %v", scopes, want, o.oauth.Scopes)
			}
		}
	}
}

// Config that would only fail at a person's first sign-in fails at start-up.
func TestOIDCConfigThatCouldNotWorkIsRefusedAtStartUp(t *testing.T) {
	idp := newFakeIDP(t)
	for name, cfg := range map[string]OIDCConfig{
		"no client id":    {Issuer: idp.srv.URL, RedirectURL: "https://x/cb"},
		"no redirect url": {Issuer: idp.srv.URL, ClientID: "c"},
	} {
		cfg.HTTPClient = idp.srv.Client()
		if _, err := NewOIDC(context.Background(), cfg, nil); err == nil {
			t.Errorf("%s was accepted although discovery would have succeeded", name)
		}
	}
}

// A browser that has its own binding cookie, from a sign-in of its own, still
// cannot finish a flow another browser started. This is the case that matters:
// the attacker's victim is a person with a cookie, not one with none.
func TestABrowserWithADifferentBindingCookieCannotFinishAFlow(t *testing.T) {
	idp := newFakeIDP(t)
	s := newOIDCSite(t, idp)
	attacker := s.begin("/ui/") // the attacker's browser starts a flow and keeps the callback

	// The victim starts a sign-in of their own, so their browser holds a binding
	// cookie, a real one, for a different flow.
	victimJar, _ := cookiejar.New(nil)
	victim := &http.Client{Transport: s.c.Transport, CheckRedirect: s.c.CheckRedirect, Jar: victimJar}
	r, err := victim.Get(s.ts.URL + "/auth/oidc/login")
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Body.Close()
	u, _ := url.Parse(s.ts.URL)
	if len(victimJar.Cookies(u)) == 0 {
		t.Fatal("the victim has no binding cookie, so this test would not be the case it claims to be")
	}

	// The victim's browser is made to complete the attacker's callback.
	resp, err := victim.Get(attacker)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode == http.StatusSeeOther {
		t.Fatal("a browser with a different binding cookie finished another browser's flow")
	}
}
