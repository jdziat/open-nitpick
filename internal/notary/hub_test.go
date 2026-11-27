package notary

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/nitpick-oss/nitpick/internal/server"
	"github.com/nitpick-oss/nitpick/internal/wire"
)

// loginAs signs in whoever it is told, with the groups it is given. These tests
// exercise sessions and roles, so they skip proving who the person is.
type loginAs struct{}

func (loginAs) ID() string               { return "test" }
func (loginAs) Label() string            { return "Test" }
func (loginAs) Form() server.LoginForm   { return server.FormCredentials }
func (loginAs) CrossSitePosts() []string { return nil }
func (loginAs) Mount(mux *http.ServeMux, host server.Host) {
	mux.HandleFunc("POST /auth/test/login", func(w http.ResponseWriter, r *http.Request) {
		secret, ok := host.CSRF().PreSessionSecret(r)
		if !ok || host.CSRF().Token(secret) != r.PostFormValue(server.CSRFFormField) {
			http.Error(w, "csrf", http.StatusForbidden)
			return
		}
		host.SignIn(w, r, server.Identity{Provider: "test", Subject: r.PostFormValue("user"), Groups: strings.Fields(r.PostFormValue("groups"))}, "/ui/")
	})
}

func builtUI() fstest.MapFS {
	return fstest.MapFS{
		"manifest.json":        {Data: []byte(`{"app.css":"app.aaaaaaaaaaaa.css","app.js":"app.aaaaaaaaaaaa.js"}`)},
		"app.aaaaaaaaaaaa.css": {Data: []byte("x")}, "app.aaaaaaaaaaaa.js": {Data: []byte("x")},
	}
}

type hub struct {
	t     *testing.T
	ts    *httptest.Server
	c     *http.Client
	store *wire.FileStore
}

// newHub mounts the notary behind a sign-in. The group "keepers" is authoritative,
// "readers" is read-only, and there is one token of each role.
func newHub(t *testing.T, store wire.Store) *hub {
	t.Helper()
	roles, err := server.NewRoleMap([]server.RoleRule{
		{Roles: []server.Role{server.Role(RoleAuthoritative)}, Provider: "test", Group: "keepers"},
		{Roles: []server.Role{server.Role(RoleRead)}, Provider: "test", Group: "readers"},
	})
	if err != nil {
		t.Fatal(err)
	}
	comp := NewComponent(store, []Token{
		NewToken("ci", RoleAuthoritative, "auth-secret"),
		NewToken("pr", RoleProvisional, "prov-secret"),
		NewToken("look", RoleRead, "read-secret"),
	}, false)
	srv, err := server.New(server.Config{
		Components: []server.Component{comp}, Providers: []server.Provider{loginAs{}}, Roles: roles,
		Assets: builtUI(), Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)
	jar, _ := cookiejar.New(nil)
	c := ts.Client()
	c.Jar = jar
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	h := &hub{t: t, ts: ts, c: c}
	if fs, ok := store.(*wire.FileStore); ok {
		h.store = fs
	}
	return h
}

// reply carries the status of a response. do closes the body before returning.
type reply struct{ StatusCode int }

func (h *hub) do(method, path string, hdr map[string]string, body string) (reply, string) {
	h.t.Helper()
	req, err := http.NewRequest(method, h.ts.URL+path, strings.NewReader(body))
	if err != nil {
		h.t.Fatal(err)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := h.c.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return reply{StatusCode: resp.StatusCode}, string(b)
}

func bearer(secret string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + secret}
}

// signIn goes through the real login form as a person in the given groups.
func (h *hub) signIn(user string, groups ...string) {
	h.t.Helper()
	_, page := h.do("GET", "/auth/login", nil, "")
	token := between(h.t, page, `name="csrf_token" value="`, `"`)
	form := url.Values{"user": {user}, "groups": {strings.Join(groups, " ")}, "csrf_token": {token}}
	resp, body := h.do("POST", "/auth/test/login", map[string]string{"Content-Type": "application/x-www-form-urlencoded"}, form.Encode())
	if resp.StatusCode != http.StatusSeeOther {
		h.t.Fatalf("sign-in = %d: %s", resp.StatusCode, body)
	}
}

// storeDirs maps a test store to its directory, so a test can set a record's file
// time. A listing orders by it.
var storeDirs = map[*wire.FileStore]string{}

func fileStore(t *testing.T) *wire.FileStore {
	t.Helper()
	dir := t.TempDir()
	fs, err := wire.NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	storeDirs[fs] = dir
	t.Cleanup(func() { delete(storeDirs, fs) })
	return fs
}

func storeDirOf(fs *wire.FileStore) string { return storeDirs[fs] }

func hashOf(n int) string { return fmt.Sprintf("%064x", n) }

// putRecord writes a record at a time, so a listing has an order to show.
func putRecord(t *testing.T, fs *wire.FileStore, n int, mod func(*wire.Record)) wire.Record {
	t.Helper()
	rec := wire.Record{
		Schema: wire.Schema, Hash: hashOf(n), Tool: "v2.10.0", Provider: "openrouter", Model: "z-ai/glm",
		Created: time.Now().Add(-time.Duration(100-n) * time.Minute), Answers: map[string]string{"r1#0": `{"content":"fine"}`},
		Batches: 1, Complete: true, Source: "ci",
	}
	if mod != nil {
		mod(&rec)
	}
	if err := fs.Put(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	return rec
}

func TestTheListIsRestrictedToTheAuthoritativeRole(t *testing.T) {
	fs := fileStore(t)
	putRecord(t, fs, 1, nil)
	h := newHub(t, fs)

	// Each credential below the authoritative role is refused.
	for name, hdr := range map[string]map[string]string{
		"a provisional token": bearer("prov-secret"),
		"a read token":        bearer("read-secret"),
	} {
		if resp, _ := h.do("GET", "/v1/records", hdr, ""); resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s = %d, want 403", name, resp.StatusCode)
		}
	}
	if resp, _ := h.do("GET", "/v1/records", nil, ""); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("no credential = %d, want 401", resp.StatusCode)
	}
	if resp, _ := h.do("GET", "/v1/records", bearer("not-a-token"), ""); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("an unknown token = %d, want 401", resp.StatusCode)
	}
	// The authoritative token gets in, so the refusals above are the role's.
	resp, body := h.do("GET", "/v1/records", bearer("auth-secret"), "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, hashOf(1)) {
		t.Fatalf("the authoritative token = %d %q", resp.StatusCode, body)
	}
}

func TestASessionNeedsTheAuthoritativeRoleToListToo(t *testing.T) {
	fs := fileStore(t)
	putRecord(t, fs, 1, nil)

	keeper := newHub(t, fs)
	keeper.signIn("kay", "keepers")
	if resp, body := keeper.do("GET", "/v1/records", nil, ""); resp.StatusCode != http.StatusOK || !strings.Contains(body, hashOf(1)) {
		t.Errorf("an authoritative session = %d %q", resp.StatusCode, body)
	}

	for name, groups := range map[string][]string{"a read-only session": {"readers"}, "a session with no role": {"interns"}} {
		h := newHub(t, fs)
		h.signIn("rae", groups...)
		if resp, _ := h.do("GET", "/v1/records", nil, ""); resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s = %d, want 403", name, resp.StatusCode)
		}
	}
}

// An empty list from an empty store is not the same answer as a failed read, and
// this test tells them apart.
func TestAnEmptyStoreListsNothingWith200AndABrokenStoreIs500(t *testing.T) {
	empty := newHub(t, fileStore(t))
	resp, body := empty.do("GET", "/v1/records", bearer("auth-secret"), "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("an empty store = %d %q", resp.StatusCode, body)
	}
	var got struct {
		Records []wire.Summary `json:"records"`
		Next    string         `json:"next_cursor"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	if got.Records == nil || len(got.Records) != 0 || got.Next != "" {
		t.Errorf("an empty store = %+v, want records: [] and no cursor", got)
	}
	if !strings.Contains(body, `"records":[]`) {
		t.Errorf("the empty list is not an empty array: %s", body)
	}

	// A store that cannot read its directory.
	broken := newHub(t, &brokenLister{})
	resp, body = broken.do("GET", "/v1/records", bearer("auth-secret"), "")
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("a store that cannot list = %d, want 500", resp.StatusCode)
	}
	if strings.Contains(body, "/srv/") || strings.Contains(body, "denied") {
		t.Errorf("the failure shows the store's own words: %q", body)
	}
	// A store with a record does not list as empty.
	full := fileStore(t)
	putRecord(t, full, 1, nil)
	if _, body := newHub(t, full).do("GET", "/v1/records", bearer("auth-secret"), ""); strings.Contains(body, `"records":[]`) {
		t.Error("a store with a record listed as empty")
	}
}

type brokenLister struct{ wire.FileStore }

func (b *brokenLister) Get(context.Context, string) (wire.Record, bool, error) {
	return wire.Record{}, false, nil
}
func (b *brokenLister) Put(context.Context, wire.Record) error      { return nil }
func (b *brokenLister) Clear(context.Context, string, string) error { return nil }
func (b *brokenLister) List(context.Context, wire.ListQuery) ([]wire.Summary, string, error) {
	return nil, "", errors.New("read /srv/secret/records: permission denied")
}

// A store that cannot list says so, and not "empty".
type plainStore struct{}

func (plainStore) Get(context.Context, string) (wire.Record, bool, error) {
	return wire.Record{}, false, nil
}
func (plainStore) Put(context.Context, wire.Record) error      { return nil }
func (plainStore) Clear(context.Context, string, string) error { return nil }

func TestAStoreThatCannotListSaysSo(t *testing.T) {
	h := newHub(t, plainStore{})
	if resp, _ := h.do("GET", "/v1/records", bearer("auth-secret"), ""); resp.StatusCode != http.StatusNotImplemented {
		t.Errorf("a store with no listing = %d, want 501", resp.StatusCode)
	}
}

func TestTheListTakesFiltersAndPages(t *testing.T) {
	fs := fileStore(t)
	for i := 1; i <= 5; i++ {
		putRecord(t, fs, i, func(r *wire.Record) {
			if i%2 == 0 {
				r.Model = "other/model"
			}
			if i == 5 {
				r.Tool = "v3.0.0"
			}
		})
		// Each record's file time is what orders a listing.
		at := time.Now().Add(-time.Duration(10-i) * time.Minute)
		if err := os.Chtimes(filepath.Join(storeDirOf(fs), hashOf(i)+".json"), at, at); err != nil {
			t.Fatal(err)
		}
	}
	h := newHub(t, fs)
	get := func(query string) (int, struct {
		Records []wire.Summary `json:"records"`
		Next    string         `json:"next_cursor"`
	}) {
		var out struct {
			Records []wire.Summary `json:"records"`
			Next    string         `json:"next_cursor"`
		}
		resp, body := h.do("GET", "/v1/records?"+query, bearer("auth-secret"), "")
		if resp.StatusCode == http.StatusOK {
			if err := json.Unmarshal([]byte(body), &out); err != nil {
				t.Fatal(err)
			}
		}
		return resp.StatusCode, out
	}
	if _, g := get("model=other/model"); len(g.Records) != 2 {
		t.Errorf("model filter = %d records, want 2", len(g.Records))
	}
	if _, g := get("version=v3.0.0"); len(g.Records) != 1 || g.Records[0].Hash != hashOf(5) {
		t.Errorf("version filter = %v", g.Records)
	}
	if _, g := get("limit=2"); len(g.Records) != 2 || g.Next == "" {
		t.Errorf("limit=2 = %d records, next %q", len(g.Records), g.Next)
	} else if _, g2 := get("limit=2&cursor=" + url.QueryEscape(g.Next)); len(g2.Records) != 2 || g2.Records[0].Hash == g.Records[0].Hash {
		t.Errorf("the second page repeats or is short: %v", g2.Records)
	}
	since := time.Now().Add(-7*time.Minute - 30*time.Second).UTC().Format(time.RFC3339)
	if code, g := get("since=" + url.QueryEscape(since)); code != 200 {
		t.Errorf("a since filter = %d", code)
	} else {
		for _, r := range g.Records {
			if r.Created.Before(time.Now().Add(-8 * time.Minute)) {
				t.Errorf("a record from before the since time was listed: %v", r.Created)
			}
		}
	}
	// A value that cannot be read is a 400, never "no filter".
	for _, q := range []string{"limit=abc", "limit=0", "limit=-1", "since=yesterday", "until=2026-13-45", "cursor=bad"} {
		if code, _ := get(q); code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", q, code)
		}
	}
}

// POST, PUT and the like on the collection are not listing.
func TestTheCollectionAcceptsOnlyGET(t *testing.T) {
	h := newHub(t, fileStore(t))
	for _, m := range []string{"POST", "PUT", "DELETE", "PATCH"} {
		if resp, _ := h.do(m, "/v1/records", bearer("auth-secret"), "{}"); resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("%s /v1/records = %d, want 405", m, resp.StatusCode)
		}
	}
}

// The machine routes are the same ones as before: a token still writes, reads and
// clears a record by its hash, and a session alone does not stand in for one.
func TestTheRecordRoutesStillWantAToken(t *testing.T) {
	fs := fileStore(t)
	rec := putRecord(t, fs, 1, nil)
	h := newHub(t, fs)
	h.signIn("kay", "keepers") // an authoritative session, and no token

	if resp, _ := h.do("GET", "/v1/records/"+rec.Hash, nil, ""); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("reading a record with a session and no token = %d, want 401", resp.StatusCode)
	}
	// A state change authorised only by a cookie is refused by the server's CSRF
	// layer before the notary sees it (403); a token-less request that gets past
	// that, with the header a page would send, is the notary's 401. Either way the
	// record is untouched, which is the property.
	if resp, _ := h.do("DELETE", "/v1/records/"+rec.Hash, nil, ""); resp.StatusCode != http.StatusForbidden {
		t.Errorf("clearing a record with a session and no token or CSRF token = %d, want the CSRF layer's 403", resp.StatusCode)
	}
	if got, ok, _ := fs.Get(context.Background(), rec.Hash); !ok || got.Clears != 0 {
		t.Error("a session with no token cleared a record")
	}
	if resp, body := h.do("GET", "/v1/records/"+rec.Hash, bearer("read-secret"), ""); resp.StatusCode != http.StatusOK || !strings.Contains(body, rec.Hash) {
		t.Errorf("a read token reads a record = %d", resp.StatusCode)
	}
}

// The notary is healthy when its store can answer, and says so through /healthz
// without saying why it is not. A probe is what decides whether it gets traffic.
func TestHealthFollowsTheStore(t *testing.T) {
	good := newHub(t, fileStore(t))
	if resp, body := good.do("GET", "/healthz", nil, ""); resp.StatusCode != http.StatusOK || !strings.Contains(body, "notary: ok") {
		t.Errorf("a notary with a working store = %d %q", resp.StatusCode, body)
	}
	bad := newHub(t, failingStore{})
	resp, body := bad.do("GET", "/healthz", nil, "")
	if resp.StatusCode != http.StatusServiceUnavailable || !strings.Contains(body, "notary: fail") {
		t.Errorf("a notary whose store fails = %d %q, want 503 and the component named", resp.StatusCode, body)
	}
	if strings.Contains(body, "/srv/") || strings.Contains(body, "input/output") {
		t.Errorf("health shows why the store failed: %q", body)
	}
}

type failingStore struct{}

func (failingStore) Get(context.Context, string) (wire.Record, bool, error) {
	return wire.Record{}, false, errors.New("read /srv/records: input/output error")
}
func (failingStore) Put(context.Context, wire.Record) error      { return nil }
func (failingStore) Clear(context.Context, string, string) error { return nil }

// The notary owns /v1/records and nothing wider: another component can have the
// rest of /v1/. A notary that claimed /v1/ would make that a start-up error.
func TestTheNotaryOwnsOnlyItsOwnPrefix(t *testing.T) {
	c := NewComponent(fileStore(t), nil, false)
	if got := c.Prefixes(); len(got) != 1 || got[0] != "/v1/records/" {
		t.Errorf("prefixes = %v, want only /v1/records/", got)
	}
	other := stubOther{}
	if _, err := server.New(server.Config{Components: []server.Component{c, other}}); err != nil {
		t.Errorf("the notary and a component under /v1/other/ cannot be mounted together: %v", err)
	}
}

type stubOther struct{}

func (stubOther) Name() string                 { return "other" }
func (stubOther) Prefixes() []string           { return []string{"/v1/other/"} }
func (stubOther) Health(context.Context) error { return nil }
func (stubOther) Routes() []server.Route {
	return []server.Route{{Pattern: "GET /v1/other/x", Handler: http.NotFoundHandler()}}
}

// between returns the text after start and before the next end, and fails the test
// when either is missing, so a changed page is a failure and not a slice at -1.
func between(tb testing.TB, s, start, end string) string {
	tb.Helper()
	i := strings.Index(s, start)
	if i < 0 {
		tb.Fatalf("%q is not in:\n%s", start, s)
	}
	rest := s[i+len(start):]
	j := strings.Index(rest, end)
	if j < 0 {
		tb.Fatalf("%q does not follow %q in:\n%s", end, start, s)
	}
	return rest[:j]
}

func getView(t *testing.T, h *hub, path string, hdr map[string]string) (int, RecordView) {
	t.Helper()
	resp, body := h.do("GET", path, hdr, "")
	var v RecordView
	if resp.StatusCode == http.StatusOK {
		if err := json.Unmarshal([]byte(body), &v); err != nil {
			t.Fatalf("%s: %v\n%s", path, err, body)
		}
	}
	return resp.StatusCode, v
}

func TestTheViewRoutesAreForTheAuthoritativeRoleOnly(t *testing.T) {
	fs := fileStore(t)
	putRecord(t, fs, 1, nil)
	h := newHub(t, fs)
	for _, path := range []string{"/v1/records/view", "/v1/records/view/" + hashOf(1)} {
		if resp, _ := h.do("GET", path, nil, ""); resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s with no credential = %d, want 401", path, resp.StatusCode)
		}
		for name, secret := range map[string]string{"provisional": "prov-secret", "read": "read-secret"} {
			if resp, _ := h.do("GET", path, bearer(secret), ""); resp.StatusCode != http.StatusForbidden {
				t.Errorf("%s with a %s token = %d, want 403", path, name, resp.StatusCode)
			}
		}
		if resp, _ := h.do("GET", path, bearer("auth-secret"), ""); resp.StatusCode != http.StatusOK {
			t.Errorf("%s with an authoritative token = %d", path, resp.StatusCode)
		}
	}
	// A person is held to the same rule: signed in is not enough.
	reader := newHub(t, fs)
	reader.signIn("rae", "readers")
	for _, path := range []string{"/v1/records/view", "/v1/records/view/" + hashOf(1)} {
		if resp, _ := reader.do("GET", path, nil, ""); resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s for a read-only person = %d, want 403", path, resp.StatusCode)
		}
	}
	keeper := newHub(t, fs)
	keeper.signIn("kay", "keepers")
	for _, path := range []string{"/v1/records/view", "/v1/records/view/" + hashOf(1)} {
		if resp, _ := keeper.do("GET", path, nil, ""); resp.StatusCode != http.StatusOK {
			t.Errorf("%s for a keeper = %d", path, resp.StatusCode)
		}
	}
}

func TestTheViewOfARecordSaysWhetherItReplaysAndMarksUnusableAnswers(t *testing.T) {
	fs := fileStore(t)
	putRecord(t, fs, 1, func(r *wire.Record) {
		r.Effort = "medium"
		r.Answers = map[string]string{"req1#0": `{"content":"good"}`, "req1#1": "!" + `{"content":"rejected"}`}
	})
	putRecord(t, fs, 2, func(r *wire.Record) { r.Complete = false; r.Batches = 3; r.Effort = "" })
	h := newHub(t, fs)
	h.signIn("kay", "keepers")

	code, v := getView(t, h, "/v1/records/view/"+hashOf(1), nil)
	if code != 200 || !v.Complete || v.Effort != "medium" || len(v.Answers) != 2 {
		t.Fatalf("view = %d %+v", code, v)
	}
	var unusable int
	for _, a := range v.Answers {
		if a.Unusable {
			unusable++
			if strings.HasPrefix(a.Text, "!") {
				t.Errorf("the unusable mark was left on the text: %q", a.Text)
			}
		}
		if a.Request != "req1" || (a.Slot != "0" && a.Slot != "1") {
			t.Errorf("identical requests must show as separate answers by slot: %+v", a)
		}
	}
	if unusable != 1 {
		t.Errorf("%d answers marked unusable, want exactly one", unusable)
	}
	if _, partial := getView(t, h, "/v1/records/view/"+hashOf(2), nil); partial.Complete || partial.Effort != "" {
		t.Errorf("an incomplete record without an effort = %+v", partial)
	}
}

func TestTheViewOfAnUnknownOrMalformedRecordIsNotFound(t *testing.T) {
	h := newHub(t, fileStore(t))
	h.signIn("kay", "keepers")
	for _, hash := range []string{hashOf(99), "short", strings.ToUpper(hashOf(255)), "..%2f..%2fetc", hashOf(1) + "0"} {
		if code, _ := getView(t, h, "/v1/records/view/"+hash, nil); code != http.StatusNotFound {
			t.Errorf("/v1/records/view/%s = %d, want 404", hash, code)
		}
	}
}

func TestTheViewRoutesOnlyRead(t *testing.T) {
	fs := fileStore(t)
	putRecord(t, fs, 1, nil)
	h := newHub(t, fs)
	for _, path := range []string{"/v1/records/view", "/v1/records/view/" + hashOf(1)} {
		for _, method := range []string{"POST", "PUT", "DELETE", "PATCH"} {
			if resp, _ := h.do(method, path, bearer("auth-secret"), "{}"); resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusCreated {
				t.Errorf("%s %s = %d: a view route must not change a record", method, path, resp.StatusCode)
			}
		}
	}
	if _, found, _ := fs.Get(context.Background(), hashOf(1)); !found {
		t.Fatal("a view request deleted a record")
	}
}

func TestASecretInARecordIsScrubbedFromTheViewsAndKeptInTheRecordAndTheMachineList(t *testing.T) {
	aws := strings.ToUpper("akia") + strings.Repeat("7", 16)
	vendor := "ghp_" + strings.Repeat("a1B2", 8)
	labeled := "api_key=" + "hunter2hunter2hunter2"
	fs := fileStore(t)
	rec := putRecord(t, fs, 1, func(r *wire.Record) {
		r.Model = "model-" + vendor
		r.Source = "ci " + labeled
		r.Tool = "v1 " + aws
		r.Provider = labeled
		r.Effort = aws
		r.ClearedBy = []string{"by " + vendor}
		r.Clears = 1
		r.Answers = map[string]string{
			"req#0":    `{"content":"the key is ` + aws + ` and ` + labeled + `"}`,
			"req#1":    "!" + vendor,
			aws + "#2": "keyed",
		}
	})
	h := newHub(t, fs)
	h.signIn("kay", "keepers")
	secrets := []string{aws, vendor, "hunter2hunter2hunter2"}
	for _, path := range []string{"/v1/records/view", "/v1/records/view/" + rec.Hash} {
		resp, body := h.do("GET", path, nil, "")
		if resp.StatusCode != 200 {
			t.Fatalf("%s = %d", path, resp.StatusCode)
		}
		for _, secret := range secrets {
			if strings.Contains(body, secret) {
				t.Errorf("%s shows %q", path, secret)
			}
		}
		if !strings.Contains(body, "REDACTED") {
			t.Errorf("%s shows no redaction at all, so the secrets may be absent for another reason", path)
		}
	}
	// The machine listing is the record's own summary, as stored: a client may
	// compare it with what it wrote.
	_, raw := h.do("GET", "/v1/records", nil, "")
	if !strings.Contains(raw, vendor) {
		t.Error("the machine listing was scrubbed; it is data and is left as stored")
	}
	stored, ok, err := fs.Get(context.Background(), rec.Hash)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if !strings.Contains(stored.Answers["req#0"], aws) || stored.Model != rec.Model {
		t.Error("the scrubbing changed the stored record, which replay needs as it was given")
	}
}

func TestTheViewListTakesTheSameFiltersAndCursorAsTheMachineList(t *testing.T) {
	fs := fileStore(t)
	for n := 1; n <= 12; n++ {
		putRecord(t, fs, n, func(r *wire.Record) {
			r.Model = "m-a"
			if n%2 == 0 {
				r.Model = "m-b"
			}
		})
	}
	h := newHub(t, fs)
	h.signIn("kay", "keepers")
	var out struct {
		Records []struct{ Model string } `json:"records"`
		Next    string                   `json:"next_cursor"`
	}
	_, body := h.do("GET", "/v1/records/view?model=m-b&limit=4", nil, "")
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Records) != 4 || out.Next == "" {
		t.Fatalf("a filtered, limited listing = %d records, next %q", len(out.Records), out.Next)
	}
	for _, r := range out.Records {
		if r.Model != "m-b" {
			t.Errorf("the model filter let %q through", r.Model)
		}
	}
	if resp, _ := h.do("GET", "/v1/records/view?since=yesterday", nil, ""); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("a bad time = %d, want 400", resp.StatusCode)
	}
}
