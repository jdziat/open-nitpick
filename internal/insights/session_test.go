package insights

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
	"testing/fstest"
	"time"

	"github.com/nitpick-oss/nitpick/internal/server"
)

// groupLogin signs in whoever it is told, with the groups it is given, so a test
// can exercise roles without proving who the person is.
type groupLogin struct{}

func (groupLogin) ID() string               { return "test" }
func (groupLogin) Label() string            { return "Test" }
func (groupLogin) Form() server.LoginForm   { return server.FormCredentials }
func (groupLogin) CrossSitePosts() []string { return nil }
func (groupLogin) Mount(mux *http.ServeMux, host server.Host) {
	mux.HandleFunc("POST /auth/test/login", func(w http.ResponseWriter, r *http.Request) {
		secret, ok := host.CSRF().PreSessionSecret(r)
		if !ok || host.CSRF().Token(secret) != r.PostFormValue(server.CSRFFormField) {
			http.Error(w, "csrf", http.StatusForbidden)
			return
		}
		host.SignIn(w, r, server.Identity{Provider: "test", Subject: r.PostFormValue("user"), Groups: strings.Fields(r.PostFormValue("groups"))}, "/ui/")
	})
}

// sessionClient returns a client signed in through the real login form as a
// person in the given groups. "eng" holds the insights role and nothing else does.
func sessionClient(t *testing.T, baseURL string, tc *http.Client, groups string) *http.Client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	c := *tc
	c.Jar = jar
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := c.Get(baseURL + "/auth/login")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	page := string(raw)
	_, rest, ok := strings.Cut(page, `name="csrf_token" value="`)
	if !ok {
		t.Fatalf("no csrf token on the login page: %s", page)
	}
	token, _, _ := strings.Cut(rest, `"`)
	form := url.Values{"user": {"pat"}, "groups": {groups}, "csrf_token": {token}}
	resp, err = c.PostForm(baseURL+"/auth/test/login", form)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("sign-in = %d", resp.StatusCode)
	}
	return &c
}

func TestASignedInPersonNeedsTheInsightsRoleToReadAnything(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	comp := NewComponent(store, nil, nil)
	comp.now = func() time.Time { return t0 }
	roles, err := server.NewRoleMap([]server.RoleRule{{Roles: []server.Role{server.Role(RoleView)}, Provider: "test", Group: "eng"}})
	if err != nil {
		t.Fatal(err)
	}
	srv, err := server.New(server.Config{
		Components: []server.Component{comp}, Providers: []server.Provider{groupLogin{}}, Roles: roles,
		Assets: fstest.MapFS{"manifest.json": {Data: []byte(`{"app.css":"app.aaaaaaaaaaaa.css","app.js":"app.aaaaaaaaaaaa.js"}`)}, "app.aaaaaaaaaaaa.css": {Data: []byte("x")}, "app.aaaaaaaaaaaa.js": {Data: []byte("x")}},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	routes := []string{"/v1/reviews", "/v1/insights/overview", "/v1/insights/hotspots", "/v1/insights/findings", "/v1/insights/repos", "/v1/insights/facets", "/mcp"}
	status := func(c *http.Client, path string) int {
		t.Helper()
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+path, nil)
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}

	// The control: a person who holds the role reads the list, so a 401 below is
	// the missing role and not a broken sign-in.
	member := sessionClient(t, ts.URL, ts.Client(), "eng")
	if got := status(member, "/v1/reviews"); got != http.StatusOK {
		t.Fatalf("a person with the insights role got %d from /v1/reviews, want 200", got)
	}

	outsider := sessionClient(t, ts.URL, ts.Client(), "sales")
	for _, path := range routes {
		if got := status(outsider, path); got != http.StatusUnauthorized {
			t.Errorf("a signed-in person without the role got %d from %s, want 401", got, path)
		}
	}
}
