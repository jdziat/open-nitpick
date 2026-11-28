package server

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/nitpick-oss/nitpick/internal/server/state"
)

// A disabled component mounts nothing. Rule 10: it is 404 because it is not
// mounted, and the same request is 200 when it is.
func TestADisabledComponentsRoutesAreNotFound(t *testing.T) {
	on := newEnv(t, Config{Components: []Component{&stubComponent{name: "stub"}}})
	if resp, body := on.do("GET", "/v1/stub/open", nil, nil); resp.StatusCode != 200 || body != "open" {
		t.Fatalf("the enabled component answered %d %q", resp.StatusCode, body)
	}
	off := newEnv(t, Config{Components: nil})
	for _, p := range []string{"/v1/stub/open", "/v1/stub/secret", "/ui/stub/"} {
		if resp, _ := off.do("GET", p, nil, nil); resp.StatusCode != 404 {
			t.Errorf("%s with the component disabled = %d, want 404", p, resp.StatusCode)
		}
	}
}

// With no provider configured the UI is off: nothing under /ui/ or /auth/ is
// served, however the components are set up.
func TestWithNoProviderTheUIIsNotFound(t *testing.T) {
	e := newEnv(t, Config{Components: []Component{&stubComponent{name: "stub"}}, Assets: builtAssets()})
	for _, p := range []string{"/ui/", "/ui/stub/", "/ui/assets/app.0123456789ab.css", "/auth/login"} {
		if resp, _ := e.do("GET", p, nil, nil); resp.StatusCode != 404 {
			t.Errorf("%s with no provider = %d, want 404", p, resp.StatusCode)
		}
	}
	// The component's own routes are untouched.
	if resp, _ := e.do("GET", "/v1/stub/open", nil, nil); resp.StatusCode != 200 {
		t.Errorf("a component route = %d with no provider", resp.StatusCode)
	}
}

// With a provider, an anonymous request is refused, each in its own way.
func TestAnAnonymousRequestIsRefused(t *testing.T) {
	e := standard(t)
	if resp, _ := e.do("GET", "/ui/", nil, nil); resp.StatusCode != 303 || !strings.HasPrefix(resp.Header.Get("Location"), "/auth/login?next=") {
		t.Errorf("/ui/ anonymous = %d to %q, want a redirect to login", resp.StatusCode, resp.Header.Get("Location"))
	}
	if resp, _ := e.do("GET", "/ui/stub/", nil, nil); resp.StatusCode != 303 {
		t.Errorf("a component page anonymous = %d, want a redirect to login", resp.StatusCode)
	}
	if resp, _ := e.do("GET", "/v1/stub/secret", nil, nil); resp.StatusCode != 401 {
		t.Errorf("a role route anonymous = %d, want 401", resp.StatusCode)
	}
	// A route that names no role is the component's to authenticate.
	if resp, body := e.do("GET", "/v1/stub/open", nil, nil); resp.StatusCode != 200 || body != "open" {
		t.Errorf("a self-authenticated route = %d %q", resp.StatusCode, body)
	}
}

// Rule 9: a route that declares a role refuses a session without it.
func TestARouteThatNeedsARoleRefusesASessionWithoutIt(t *testing.T) {
	e := standard(t)
	e.signIn("alice") // no group, so no roles
	if resp, _ := e.do("GET", "/v1/stub/secret", nil, nil); resp.StatusCode != 403 {
		t.Errorf("a role route without the role = %d, want 403", resp.StatusCode)
	}
	// Rule 10: the refusal is the role's. The same routes answer for an admin.
	admin := standard(t)
	admin.signIn("bob", "ops")
	if resp, body := admin.do("GET", "/v1/stub/secret", nil, nil); resp.StatusCode != 200 || body != "secret" {
		t.Errorf("a role route with the role = %d %q", resp.StatusCode, body)
	}
}

// The session cookie and the security headers are asserted, not assumed.
func TestTheSessionCookieAndSecurityHeaders(t *testing.T) {
	e := standard(t)
	_, page := e.do("GET", "/auth/login", nil, nil)
	token := between(t, page, `name="csrf_token" value="`, `"`)
	resp, _ := e.do("POST", "/auth/fake/login", url.Values{"username": {"alice"}, "csrf_token": {token}}, nil)
	var session *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == SessionCookie {
			session = c
		}
	}
	if session == nil {
		t.Fatal("no session cookie was set")
	}
	if !session.HttpOnly || !session.Secure || session.SameSite != http.SameSiteLaxMode || session.Path != "/" || session.Domain != "" {
		t.Errorf("session cookie = %+v, want HttpOnly, Secure, SameSite=Lax, Path=/ and no Domain", session)
	}
	if !strings.HasPrefix(session.Name, "__Host-") {
		t.Errorf("session cookie name %q lacks the __Host- prefix", session.Name)
	}
	if len(session.Value) < 40 {
		t.Errorf("session id %q is too short to be 256 random bits", session.Value)
	}

	resp, _ = e.do("GET", "/ui/", nil, nil)
	for h, want := range map[string]string{
		"X-Content-Type-Options":     "nosniff",
		"Referrer-Policy":            "no-referrer",
		"Content-Security-Policy":    ContentSecurityPolicy,
		"Cross-Origin-Opener-Policy": "same-origin",
		"Cache-Control":              "no-store",
	} {
		if got := resp.Header.Get(h); got != want {
			t.Errorf("%s = %q, want %q", h, got, want)
		}
	}
	csp := resp.Header.Get("Content-Security-Policy")
	for _, banned := range []string{"unsafe-inline", "unsafe-eval", "http:", "https:", "*"} {
		if strings.Contains(csp, banned) {
			t.Errorf("the policy %q contains %q", csp, banned)
		}
	}
	for _, need := range []string{"default-src 'none'", "frame-ancestors 'none'", "base-uri 'none'", "form-action 'self'"} {
		if !strings.Contains(csp, need) {
			t.Errorf("the policy %q lacks %q", csp, need)
		}
	}
	// The headers are on every response, not only pages.
	resp, _ = e.do("GET", "/v1/stub/open", nil, nil)
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("a component's JSON route lacks nosniff")
	}
}

// Rule 9: state-changing requests without a valid CSRF token are refused, and
// the same request with one is not.
func TestAStateChangingRequestWithoutACSRFTokenIsRefused(t *testing.T) {
	e := standard(t)
	e.signIn("bob", "ops")

	if resp, _ := e.do("POST", "/v1/stub/change", url.Values{}, nil); resp.StatusCode != 403 {
		t.Errorf("a POST with a session and no token = %d, want 403", resp.StatusCode)
	}
	if resp, _ := e.do("POST", "/v1/stub/change", url.Values{"csrf_token": {"forged"}}, nil); resp.StatusCode != 403 {
		t.Errorf("a POST with a forged token = %d, want 403", resp.StatusCode)
	}
	if resp, _ := e.do("POST", "/auth/logout", url.Values{}, nil); resp.StatusCode != 403 {
		t.Errorf("sign-out with no token = %d, want 403", resp.StatusCode)
	}
	if sessionCount(t, e) != 1 {
		t.Error("a refused sign-out ended the session")
	}

	_, page := e.do("GET", "/ui/", nil, nil)
	token := sessionOf(t, page).CSRF
	if resp, body := e.do("POST", "/v1/stub/change", url.Values{"csrf_token": {token}}, nil); resp.StatusCode != 200 || body != "changed" {
		t.Errorf("a POST with the token = %d %q", resp.StatusCode, body)
	}
	if resp, _ := e.do("POST", "/v1/stub/change", nil, map[string]string{CSRFHeader: token}); resp.StatusCode != 200 {
		t.Errorf("a POST with the token in the header = %d", resp.StatusCode)
	}
}

// Another site cannot make the browser's POST count, even with a token it
// somehow has: a request that says it is cross-site is refused first.
func TestACrossSiteRequestIsRefusedEvenWithAToken(t *testing.T) {
	e := standard(t)
	e.signIn("bob", "ops")
	_, page := e.do("GET", "/ui/", nil, nil)
	token := sessionOf(t, page).CSRF
	for name, hdr := range map[string]map[string]string{
		"cross-site fetch metadata": {"Sec-Fetch-Site": "cross-site"},
		"a foreign origin":          {"Origin": "https://evil.example"},
		"a null origin":             {"Origin": "null"},
	} {
		if resp, _ := e.do("POST", "/v1/stub/change", url.Values{"csrf_token": {token}}, hdr); resp.StatusCode != 403 {
			t.Errorf("%s = %d, want 403", name, resp.StatusCode)
		}
	}
	if resp, _ := e.do("POST", "/v1/stub/change", url.Values{"csrf_token": {token}}, map[string]string{"Sec-Fetch-Site": "same-origin", "Origin": e.ts.URL}); resp.StatusCode != 200 {
		t.Errorf("a same-origin POST with the token = %d", resp.StatusCode)
	}
}

// A bearer token is not a cookie: another site cannot attach one, so a request
// carrying it is not asked for the CSRF token even when a session exists.
func TestABearerRequestIsNotAskedForACSRFToken(t *testing.T) {
	e := newEnv(t, Config{
		Components: []Component{&stubComponent{name: "stub", extra: []Route{{
			Pattern: "PUT /v1/stub/put", Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }),
		}}}},
		Providers: []Provider{fakeProvider{id: "fake"}},
		Assets:    builtAssets(),
	})
	e.signIn("bob")
	if resp, _ := e.do("PUT", "/v1/stub/put", nil, nil); resp.StatusCode != 403 {
		t.Errorf("a cookie request with no token = %d, want 403", resp.StatusCode)
	}
	if resp, _ := e.do("PUT", "/v1/stub/put", nil, map[string]string{"Authorization": "Bearer abc"}); resp.StatusCode != 204 {
		t.Errorf("a bearer request = %d, want it let through to the component", resp.StatusCode)
	}
}

// Sign-out ends the session, and the old cookie is no longer a session.
func TestSigningOutEndsTheSession(t *testing.T) {
	e := standard(t)
	e.signIn("alice")
	old := e.cookie(SessionCookie)
	if old == nil {
		t.Fatal("no session cookie after sign-in")
	}
	_, page := e.do("GET", "/ui/", nil, nil)
	token := sessionOf(t, page).CSRF
	if resp, _ := e.do("POST", "/auth/logout", url.Values{"csrf_token": {token}}, nil); resp.StatusCode != 303 {
		t.Fatalf("sign-out = %d", resp.StatusCode)
	}
	if sessionCount(t, e) != 0 {
		t.Error("the session is still held after sign-out")
	}
	// Replaying the old cookie by hand: it must be dead on the server, not just
	// dropped by the browser.
	if resp, _ := e.do("GET", "/ui/", nil, map[string]string{"Cookie": SessionCookie + "=" + old.Value}); resp.StatusCode != 303 {
		t.Errorf("the old cookie still worked: %d", resp.StatusCode)
	}
}

// Rule 9: a session ends at the idle timeout and at the absolute timeout.
func TestSessionsEndWhenIdleAndWhenOld(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	e := newEnv(t, Config{
		Components: []Component{&stubComponent{name: "stub"}},
		Providers:  []Provider{fakeProvider{id: "fake"}},
		Sessions:   SessionConfig{Idle: 10 * time.Minute, Absolute: time.Hour},
		Assets:     builtAssets(),
		State:      state.New(testDB(t)).WithClock(clock),
	})

	e.signIn("alice")
	if resp, _ := e.do("GET", "/ui/", nil, nil); resp.StatusCode != 200 {
		t.Fatalf("a fresh session = %d", resp.StatusCode)
	}
	// Activity keeps it alive past the idle limit measured from sign-in.
	for i := 0; i < 4; i++ {
		now = now.Add(8 * time.Minute)
		if resp, _ := e.do("GET", "/ui/", nil, nil); resp.StatusCode != 200 {
			t.Fatalf("an active session ended after %d minutes", 8*(i+1))
		}
	}
	// Idle: no request for longer than the limit.
	now = now.Add(11 * time.Minute)
	if resp, _ := e.do("GET", "/ui/", nil, nil); resp.StatusCode != 303 {
		t.Errorf("an idle session = %d, want it ended", resp.StatusCode)
	}

	// Absolute: activity does not keep a session past its age.
	e.signIn("alice")
	for i := 0; i < 8; i++ {
		now = now.Add(9 * time.Minute)
		resp, _ := e.do("GET", "/ui/", nil, nil)
		if i < 5 && resp.StatusCode != 200 {
			t.Fatalf("an active session ended early at step %d", i)
		}
		if i >= 6 && resp.StatusCode != 303 {
			t.Fatalf("a session older than the absolute limit survived at step %d (%d)", i, resp.StatusCode)
		}
	}
}

// Signing in always issues a new session id: one known before sign-in is not the
// one after, so a fixed id cannot be carried across the boundary.
func TestSigningInIssuesANewSessionID(t *testing.T) {
	e := standard(t)
	e.signIn("alice")
	first := e.cookie(SessionCookie).Value
	// A second browser: the login page is only shown to someone not signed in.
	e.c.Jar, _ = cookiejar.New(nil)
	e.signIn("alice")
	second := e.cookie(SessionCookie).Value
	if first == second {
		t.Error("two sign-ins produced the same session id")
	}
}

// The login redirect stays on this site.
func TestTheLoginRedirectStaysOnThisSite(t *testing.T) {
	for next, want := range map[string]string{
		"":                        "/ui/",
		"/ui/stub/":               "/ui/stub/",
		"/ui/stub/item/7?a=b":     "/ui/stub/item/7?a=b",
		"/ui/a\\b":                "/ui/",
		"/ui/a\x7fb":              "/ui/",
		"/ui/a\tb":                "/ui/",
		"/ui/%zz":                 "/ui/%zz",
		"/uix/":                   "/ui/",
		"/UI/stub/":               "/ui/",
		"/ui":                     "/ui",
		"https://evil.example/":   "/ui/",
		"//evil.example/ui/":      "/ui/",
		"/\\evil.example":         "/ui/",
		"/auth/logout":            "/ui/",
		"/ui/../v1/stub/open":     "/ui/../v1/stub/open", // still a local path; the router cleans it
		"javascript:alert(1)":     "/ui/",
		"/ui/\r\nSet-Cookie: x=1": "/ui/",
	} {
		if got := safeNext(next); got != want {
			t.Errorf("safeNext(%q) = %q, want %q", next, got, want)
		}
	}
}

// Bad components are an error from New, not a crash at the first request.
func TestBadComponentsAreRefusedAtStartup(t *testing.T) {
	for name, cfg := range map[string]Config{
		"a route outside the prefixes": {Components: []Component{&stubComponent{name: "a", prefixes: []string{"/v1/other/"}}}},
		"two components, one prefix":   {Components: []Component{&stubComponent{name: "a", prefixes: []string{"/v1/x/"}}, &stubComponent{name: "b", prefixes: []string{"/v1/x/"}}}},
		"nested prefixes":              {Components: []Component{&stubComponent{name: "a", prefixes: []string{"/v1/"}}, &stubComponent{name: "b"}}},
		"a repeated name":              {Components: []Component{&stubComponent{name: "a"}, &stubComponent{name: "a", prefixes: []string{"/v1/zz/"}}}},
		"an empty name":                {Components: []Component{&stubComponent{name: ""}}},
		"a prefix without a slash":     {Components: []Component{&stubComponent{name: "a", prefixes: []string{"/v1/a"}}}},
		"the root as a prefix":         {Components: []Component{&stubComponent{name: "a", prefixes: []string{"/"}}}},
		"a repeated provider":          {Providers: []Provider{fakeProvider{id: "p"}, fakeProvider{id: "p"}}},
		"a reserved provider id":       {Providers: []Provider{fakeProvider{id: "login"}}},
	} {
		if _, err := New(cfg); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

// Role rules that would match everyone, or no one, are refused.
func TestRoleRulesThatCouldNeverMeanWhatTheySayAreRefused(t *testing.T) {
	for name, rule := range map[string]RoleRule{
		"no role":       {Provider: "p", Group: "g"},
		"an empty role": {Provider: "p", Roles: []Role{" "}, Group: "g"},
		"no selector":   {Provider: "p", Roles: []Role{"r"}},
		"two selectors": {Provider: "p", Roles: []Role{"r"}, Group: "g", User: "u"},
		// A rule that omits the provider would match its subject or group on every
		// provider, so each one must be named.
		"no provider, by user":  {Roles: []Role{"r"}, User: "u"},
		"no provider, by group": {Roles: []Role{"r"}, Group: "g"},
	} {
		if _, err := NewRoleMap([]RoleRule{rule}); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	m, err := NewRoleMap([]RoleRule{
		{Roles: []Role{"a"}, Group: "g", Provider: "oidc"},
		{Roles: []Role{"a", "b"}, User: "u", Provider: "oidc"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Grant(Identity{Provider: "oidc", Subject: "u", Groups: []string{"g"}}); !got.Has("a") || !got.Has("b") || len(got.Roles) != 2 {
		t.Errorf("roles = %v, want a and b once each", got.Roles)
	}
	if got := m.Grant(Identity{Provider: "saml", Subject: "u"}); len(got.Roles) != 0 {
		t.Errorf("a rule limited to oidc granted %v to a saml identity", got.Roles)
	}
}

// /healthz reports every enabled component and says nothing about why one
// failed.
func TestHealthReportsEachComponentWithoutTheReason(t *testing.T) {
	e := newEnv(t, Config{Components: []Component{
		&stubComponent{name: "good"},
		&stubComponent{name: "bad", healthy: errBoom},
	}})
	resp, body := e.do("GET", "/healthz", nil, nil)
	if resp.StatusCode != 503 || !strings.HasPrefix(body, "unhealthy\n") || !strings.Contains(body, "bad: fail") || !strings.Contains(body, "good: ok") {
		t.Errorf("health = %d %q", resp.StatusCode, body)
	}
	if strings.Contains(body, "boom") || strings.Contains(body, "/var") {
		t.Errorf("health shows why a component failed: %q", body)
	}
	ok := newEnv(t, Config{Components: []Component{&stubComponent{name: "good"}}})
	if resp, body := ok.do("GET", "/healthz", nil, nil); resp.StatusCode != 200 || !strings.HasPrefix(body, "ok\n") {
		t.Errorf("healthy = %d %q", resp.StatusCode, body)
	}
	if resp, body := ok.do("HEAD", "/healthz", nil, nil); resp.StatusCode != 200 || body != "" {
		t.Errorf("HEAD = %d %q", resp.StatusCode, body)
	}
}

// A provider hands the server whatever next it was given, and the server is the
// one that keeps the redirect on this site. Sign-in goes through the real form.
func TestSignInNeverRedirectsOffThisSite(t *testing.T) {
	for _, next := range []string{"https://evil.example/", "//evil.example/ui/", "/auth/logout", "javascript:alert(1)", "/v1/stub/open"} {
		e := standard(t)
		_, page := e.do("GET", "/auth/login", nil, nil)
		token := between(t, page, `name="csrf_token" value="`, `"`)
		resp, _ := e.do("POST", "/auth/fake/login", url.Values{"username": {"alice"}, "csrf_token": {token}, "next": {next}}, nil)
		if resp.StatusCode != 303 || resp.Header.Get("Location") != "/ui/" {
			t.Errorf("next=%q sent the browser to %q (%d), want the UI home", next, resp.Header.Get("Location"), resp.StatusCode)
		}
	}
	// Rule 10: a local next is honoured, so the above is the check's doing.
	e := standard(t)
	_, page := e.do("GET", "/auth/login", nil, nil)
	token := between(t, page, `name="csrf_token" value="`, `"`)
	resp, _ := e.do("POST", "/auth/fake/login", url.Values{"username": {"alice"}, "csrf_token": {token}, "next": {"/ui/stub/admin"}}, nil)
	if resp.Header.Get("Location") != "/ui/stub/admin" {
		t.Errorf("a local next went to %q", resp.Header.Get("Location"))
	}
}

// A login form that a page offers carries a next that is already checked, so a
// crafted ?next= cannot be planted in the form.
func TestTheLoginFormDoesNotEchoAnUnsafeNext(t *testing.T) {
	e := standard(t)
	_, page := e.do("GET", "/auth/login?next="+url.QueryEscape(`"><script>alert(1)</script>`), nil, nil)
	if strings.Contains(page, "<script>alert") {
		t.Errorf("a crafted next reached the login page as markup:\n%s", page)
	}
	if !strings.Contains(page, `name="next" value="/ui/"`) {
		t.Errorf("an unsafe next was not replaced by the UI home:\n%s", page)
	}
}

// The page a person asked for survives the trip through the login page: the link
// that starts a redirect sign-in carries it once-escaped, so the provider route
// reads back the same path.
func TestTheRedirectLoginLinkCarriesTheRequestedPageOnceEscaped(t *testing.T) {
	e := newEnv(t, Config{
		Components: []Component{&stubComponent{name: "stub"}},
		Providers:  []Provider{redirectProvider{}},
		Assets:     builtAssets(),
	})
	_, page := e.do("GET", "/auth/login?next="+url.QueryEscape("/ui/stub/item/7?a=b&c=d"), nil, nil)
	href := between(t, page, `href="/auth/`, `"`)
	href = "/auth/" + href
	u, err := url.Parse(strings.ReplaceAll(href, "&amp;", "&"))
	if err != nil {
		t.Fatal(err)
	}
	if got := u.Query().Get("next"); got != "/ui/stub/item/7?a=b&c=d" {
		t.Errorf("the link %q carries next=%q, want the page that was asked for", href, got)
	}
}

type redirectProvider struct{}

func (redirectProvider) ID() string                 { return "red" }
func (redirectProvider) Label() string              { return "Red" }
func (redirectProvider) Form() LoginForm            { return FormRedirect }
func (redirectProvider) CrossSitePosts() []string   { return nil }
func (redirectProvider) Mount(*http.ServeMux, Host) {}

// A prefix owns the collection at its own name, "/v1/stub", as well as what is
// beneath it. A route elsewhere, or one that only shares the letters, is refused.
func TestAPrefixOwnsItsCollectionButNotAMereLookalike(t *testing.T) {
	ok := &stubComponent{name: "stub", extra: []Route{{
		Pattern: "GET /v1/stub", Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "collection") }),
	}}}
	e := newEnv(t, Config{Components: []Component{ok}})
	if resp, body := e.do("GET", "/v1/stub", nil, nil); resp.StatusCode != 200 || body != "collection" {
		t.Errorf("the collection route = %d %q", resp.StatusCode, body)
	}
	for _, pattern := range []string{"GET /v1/stubs", "GET /v1/stu", "GET /v1"} {
		bad := &stubComponent{name: "stub", extra: []Route{{Pattern: pattern, Handler: http.NotFoundHandler()}}}
		if _, err := New(Config{DB: testDB(t), Components: []Component{bad}}); err == nil {
			t.Errorf("%q was accepted as under the prefix /v1/stub/", pattern)
		}
	}
}

// A path that ServeMux would clean is refused, for every method, with no redirect.
// A redirect that keeps the method and body would perform a write on a path the
// server was never sent.
func TestAnUncleanPathIsNotFoundAndNeverRedirected(t *testing.T) {
	e := newEnv(t, Config{Components: []Component{&stubComponent{name: "stub"}}})
	for _, path := range []string{
		"/v1//stub/open", "/v1/stub//open", "//v1/stub/open", "/v1/stub/./open", "/v1/stub/x/../open", "/v1/stub/../stub/open",
		"/./v1/stub/open", "/v1/stub/open/.", "/v1/stub/open/..", "/healthz/..", "//healthz",
	} {
		for _, method := range []string{"GET", "POST", "PUT", "DELETE"} {
			resp, _ := e.do(method, path, nil, nil)
			if resp.StatusCode != http.StatusNotFound || resp.Header.Get("Location") != "" {
				t.Errorf("%s %s = %d (Location %q), want a plain 404", method, path, resp.StatusCode, resp.Header.Get("Location"))
			}
		}
	}
	// Rule 10: the same routes by their clean paths work, and a trailing slash on
	// a subtree is not unclean.
	if resp, body := e.do("GET", "/v1/stub/open", nil, nil); resp.StatusCode != 200 || body != "open" {
		t.Errorf("the clean path = %d %q", resp.StatusCode, body)
	}
	for path, want := range map[string]bool{
		"/": false, "/ui/": false, "/ui": false, "/v1/records/abc": false, "/healthz": false,
		"//": true, "/a//": true, "/a/./b": true, "/a/../b": true, "/..": true, "/.": true,
	} {
		if got := unclean(path); got != want {
			t.Errorf("unclean(%q) = %v, want %v", path, got, want)
		}
	}
}

type declaring struct{ stubComponent }

func (declaring) Roles() []Role { return []Role{"admin", "viewer"} }

// A role rule naming a role no component declares is refused, naming the roles
// that exist; with nothing declaring any, there is nothing to check against.
func TestARoleNoComponentDeclaresIsRefusedAtStartUp(t *testing.T) {
	comp := &declaring{stubComponent{name: "stub"}}
	for _, role := range []Role{"admn", "Admin", "root", "admin "} {
		_, err := New(Config{DB: testDB(t), Components: []Component{comp}, Roles: newRoles(t, RoleRule{Roles: []Role{role}, Provider: "fake", User: "u"})})
		if err == nil || !strings.Contains(err.Error(), "admin, viewer") {
			t.Errorf("role %q: err = %v, want it refused with the real roles named", role, err)
		}
	}
	// Rule 10: the declared roles are accepted, alone and together.
	for _, roles := range [][]Role{{"admin"}, {"viewer"}, {"admin", "viewer"}} {
		if _, err := New(Config{DB: testDB(t), Components: []Component{comp}, Roles: newRoles(t, RoleRule{Roles: roles, Provider: "fake", User: "u"})}); err != nil {
			t.Errorf("roles %v were refused: %v", roles, err)
		}
	}
	if _, err := New(Config{DB: testDB(t), Components: []Component{&stubComponent{name: "stub"}}, Roles: newRoles(t, RoleRule{Roles: []Role{"anything"}, Provider: "fake", User: "u"})}); err != nil {
		t.Errorf("with nothing declaring roles a rule was refused: %v", err)
	}
}

// The login body is executed with the View as its root, so its $.CSRF and $.Data
// are the view's and not the layout's. A page that failed to execute, or rendered
// an empty token, would make every sign-in unreachable. This reads the rendered
// form and holds each of its fields to a real value.
func TestTheLoginPageRendersItsFormWithARealTokenAndTheRequestedPage(t *testing.T) {
	e := standard(t)
	resp, page := e.do("GET", "/auth/login?next="+url.QueryEscape("/ui/stub/item/7"), nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("the login page = %d:\n%s", resp.StatusCode, page)
	}
	token := between(t, page, `name="csrf_token" value="`, `"`)
	if len(token) < 40 {
		t.Errorf("the form's token is %q, want a real token", token)
	}
	if got := between(t, page, `name="next" value="`, `"`); got != "/ui/stub/item/7" {
		t.Errorf("the form's next = %q, want the page that was asked for", got)
	}
	if got := between(t, page, `action="/auth/`, `"`); got != "fake/login" {
		t.Errorf("the form posts to /auth/%s, want /auth/fake/login", got)
	}
	// The token the page shows is the one the server accepts: sign in with it.
	post, body := e.do("POST", "/auth/fake/login", url.Values{"username": {"alice"}, "csrf_token": {token}, "next": {"/ui/"}}, nil)
	if post.StatusCode != 303 {
		t.Errorf("signing in with the token the page showed = %d: %s", post.StatusCode, body)
	}
	// Rule 10: a page with no provider is not a login page, so the form above
	// came from the provider and not from a template that always prints one.
	bare := newEnv(t, Config{Components: []Component{&stubComponent{name: "stub"}}, Providers: []Provider{redirectProvider{}}, Assets: builtAssets()})
	_, other := bare.do("GET", "/auth/login", nil, nil)
	if strings.Contains(other, `name="csrf_token"`) {
		t.Errorf("a redirect-only login page carries a credentials form:\n%s", other)
	}
}

// The page for the web app is the one thing the server renders after sign-in. It
// tells the app who is signed in and nothing else, and it loads only files the
// server itself serves.
func TestTheShellTellsTheAppWhoIsSignedInAndLoadsOnlyBuiltFiles(t *testing.T) {
	e := standard(t)
	e.signIn("bob", "ops")
	for _, path := range []string{"/ui/", "/ui/insights/findings", "/ui/some/deep/link"} {
		resp, page := e.do("GET", path, nil, nil)
		if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Type"), "text/html") {
			t.Fatalf("%s = %d %s, want the shell", path, resp.StatusCode, resp.Header.Get("Content-Type"))
		}
		info := sessionOf(t, page)
		if info.Name != "bob" || info.Provider != "fake" || len(info.Roles) != 1 || info.Roles[0] != "admin" {
			t.Errorf("%s: session = %+v, want bob with the admin role", path, info)
		}
		if len(info.Components) != 1 || info.Components[0] != "stub" {
			t.Errorf("%s: components = %v, want the enabled ones", path, info.Components)
		}
		if info.CSRF == "" {
			t.Errorf("%s: no CSRF token for the app's sign-out", path)
		}
		for _, want := range []string{`href="/ui/assets/app.0123456789ab.css"`, `src="/ui/assets/app.0123456789ab.js"`, `src="/ui/assets/theme.0123456789ab.js"`} {
			if !strings.Contains(page, want) {
				t.Errorf("%s: the shell lacks %s", path, want)
			}
		}
	}
}

// The policy forbids inline script and style, so the shell must not need either.
func TestTheShellHasNoInlineScriptOrStyle(t *testing.T) {
	e := standard(t)
	e.signIn("bob")
	_, page := e.do("GET", "/ui/", nil, nil)
	for _, bad := range []string{"<style", ` style="`, "javascript:", " onclick=", " onload="} {
		if strings.Contains(page, bad) {
			t.Errorf("the shell has %q, which the page's policy would block", bad)
		}
	}
	for _, tag := range regexp.MustCompile(`<script[^>]*>`).FindAllString(page, -1) {
		if !strings.Contains(tag, "src=") {
			t.Errorf("an inline script: %s", tag)
		}
	}
}

// The name is the person's, set by the identity provider, so it is untrusted text
// that ends up in an attribute. It must not be able to leave it.
func TestAnAccountNameCannotBreakOutOfTheShellsSessionTag(t *testing.T) {
	const evil = `x"><script>alert(1)</script><meta name="y" content="`
	e := standard(t)
	e.signIn(evil)
	_, page := e.do("GET", "/ui/", nil, nil)
	if strings.Contains(page, "<script>alert") || strings.Contains(page, `<meta name="y"`) {
		t.Fatalf("the account name reached the page as markup:\n%s", page)
	}
	if got := sessionOf(t, page).Name; got != evil {
		t.Errorf("the name was altered on the way: %q", got)
	}
}

// With the UI unbuilt every address under /ui/ says so, and loads nothing.
func TestTheShellIsTheStubWhenTheUIWasNotBuilt(t *testing.T) {
	e := newEnv(t, Config{Components: []Component{&stubComponent{name: "stub"}}, Providers: []Provider{fakeProvider{id: "fake"}}, Assets: fstest.MapFS{}})
	for _, path := range []string{"/ui/", "/ui/insights/findings"} {
		resp, body := e.do("GET", path, nil, nil)
		if resp.StatusCode != 200 || !strings.Contains(body, "The UI was not built") || strings.Contains(body, "<script") || strings.Contains(body, "stylesheet") {
			t.Errorf("%s = %d %q", path, resp.StatusCode, body)
		}
	}
}

// A deep link survives the sign-in: the person lands where they were going.
func TestADeepLinkSurvivesSigningIn(t *testing.T) {
	e := standard(t)
	resp, _ := e.do("GET", "/ui/insights/findings?path=a.go&days=30", nil, nil)
	if resp.StatusCode != 303 {
		t.Fatalf("anonymous deep link = %d, want the login redirect", resp.StatusCode)
	}
	loc, _ := url.Parse(resp.Header.Get("Location"))
	if got := loc.Query().Get("next"); got != "/ui/insights/findings?path=a.go&days=30" {
		t.Errorf("next = %q, want the page that was asked for", got)
	}
}

// sessionCount is how many sessions the server holds.
func sessionCount(t *testing.T, e *env) int {
	t.Helper()
	n, err := e.srv.Sessions().Len(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return n
}
