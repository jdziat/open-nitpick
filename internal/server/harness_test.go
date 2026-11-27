package server

import (
	"context"
	"encoding/json"
	"errors"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
)

// builtAssets is a UI that was built: a manifest and the two files it names.
func builtAssets() fstest.MapFS {
	return fstest.MapFS{
		"manifest.json":         {Data: []byte(`{"app.css":"app.0123456789ab.css","app.js":"app.0123456789ab.js","theme.js":"theme.0123456789ab.js"}`)},
		"app.0123456789ab.css":  {Data: []byte("body{}")},
		"app.0123456789ab.js":   {Data: []byte("//")},
		"theme.0123456789ab.js": {Data: []byte("//")},
	}
}

// stubComponent is a component used only in tests: one machine-authenticated
// route and two that need a role.
type stubComponent struct {
	name     string
	prefixes []string
	healthy  error
	extra    []Route
}

func (c *stubComponent) Name() string { return c.name }
func (c *stubComponent) Prefixes() []string {
	if c.prefixes != nil {
		return c.prefixes
	}
	return []string{"/v1/" + c.name + "/"}
}
func (c *stubComponent) Health(context.Context) error { return c.healthy }
func (c *stubComponent) Routes() []Route {
	return append([]Route{
		{Pattern: "GET /v1/" + c.name + "/open", Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, "open")
		})},
		{Pattern: "GET /v1/" + c.name + "/secret", Need: "admin", Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, "secret")
		})},
		{Pattern: "POST /v1/" + c.name + "/change", Need: "admin", Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, "changed")
		})},
	}, c.extra...)
}

// fakeProvider signs in whoever the form names, with no check: the server's
// handling of sessions is what is under test, not a credential.
type fakeProvider struct{ id string }

func (p fakeProvider) ID() string               { return p.id }
func (p fakeProvider) Label() string            { return "Fake" }
func (p fakeProvider) Form() LoginForm          { return FormCredentials }
func (p fakeProvider) CrossSitePosts() []string { return nil }
func (p fakeProvider) Mount(mux *http.ServeMux, host Host) {
	mux.HandleFunc("POST /auth/"+p.id+"/login", func(w http.ResponseWriter, r *http.Request) {
		secret, ok := host.CSRF().PreSessionSecret(r)
		if !ok || host.CSRF().Token(secret) != r.PostFormValue(CSRFFormField) {
			http.Error(w, "csrf", http.StatusForbidden)
			return
		}
		host.SignIn(w, r, Identity{Provider: p.id, Subject: r.PostFormValue("username"), Groups: strings.Fields(r.PostFormValue("groups"))}, r.PostFormValue("next"))
	})
}

// sessionOf reads the session the shell page tells the web app about, from the
// meta tag the server renders it into.
func sessionOf(t *testing.T, page string) SessionInfo {
	t.Helper()
	raw := between(t, page, `<meta name="nitpick-session" content="`, `">`)
	var info SessionInfo
	if err := json.Unmarshal([]byte(html.UnescapeString(raw)), &info); err != nil {
		t.Fatalf("the session meta tag is not JSON: %v\n%s", err, raw)
	}
	return info
}

func newRoles(t *testing.T, rules ...RoleRule) *RoleMap {
	t.Helper()
	m, err := NewRoleMap(rules)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

type env struct {
	t   *testing.T
	srv *Server
	ts  *httptest.Server
	c   *http.Client
}

func newEnv(t *testing.T, cfg Config) *env {
	t.Helper()
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewTLSServer(s)
	t.Cleanup(ts.Close)
	jar, _ := cookiejar.New(nil)
	c := ts.Client()
	c.Jar = jar
	// Redirects are read, not followed: a test says what the first answer was.
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &env{t: t, srv: s, ts: ts, c: c}
}

// reply is what a test needs of a response. The body is read and closed inside
// do, so no test holds a response it could forget to close.
type reply struct {
	StatusCode int
	Header     http.Header
	SetCookies []*http.Cookie
}

// Cookies are the cookies the response set.
func (r reply) Cookies() []*http.Cookie { return r.SetCookies }

func (e *env) do(method, path string, body url.Values, hdr map[string]string) (reply, string) {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		rd = strings.NewReader(body.Encode())
	}
	req, err := http.NewRequest(method, e.ts.URL+path, rd)
	if err != nil {
		e.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := e.c.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return reply{StatusCode: resp.StatusCode, Header: resp.Header, SetCookies: resp.Cookies()}, string(b)
}

// signIn goes through the real login form as the stub provider: it reads the
// token from the page, posts it, and leaves the session cookie in the jar.
func (e *env) signIn(user string, groups ...string) {
	e.t.Helper()
	_, page := e.do("GET", "/auth/login", nil, nil)
	token := between(e.t, page, `name="csrf_token" value="`, `"`)
	resp, body := e.do("POST", "/auth/fake/login", url.Values{
		"username": {user}, "groups": {strings.Join(groups, " ")}, "csrf_token": {token}, "next": {"/ui/"},
	}, nil)
	if resp.StatusCode != http.StatusSeeOther {
		e.t.Fatalf("sign-in answered %d: %s", resp.StatusCode, body)
	}
}

func between(t *testing.T, s, a, b string) string {
	t.Helper()
	i := strings.Index(s, a)
	if i < 0 {
		t.Fatalf("%q not in %q", a, s)
	}
	i += len(a)
	j := strings.Index(s[i:], b)
	if j < 0 {
		t.Fatalf("%q not after %q", b, a)
	}
	return s[i : i+j]
}

func (e *env) cookie(name string) *http.Cookie {
	u, _ := url.Parse(e.ts.URL)
	for _, c := range e.c.Jar.Cookies(u) {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// standard is a server with one component, one provider and a role rule that
// makes the group "ops" admins.
func standard(t *testing.T) *env {
	t.Helper()
	return newEnv(t, Config{
		Components: []Component{&stubComponent{name: "stub"}},
		Providers:  []Provider{fakeProvider{id: "fake"}},
		Roles:      newRoles(t, RoleRule{Roles: []Role{"admin"}, Provider: "fake", Group: "ops"}),
		Assets:     builtAssets(),
	})
}

var errBoom = errors.New("open /var/secret/dir: boom")

// regexpFindAll returns the first group of every match of pattern in text.
func regexpFindAll(pattern, text string) []string {
	var out []string
	for _, m := range regexp.MustCompile(pattern).FindAllStringSubmatch(text, -1) {
		out = append(out, strings.Trim(m[1], `"'`))
	}
	return out
}
