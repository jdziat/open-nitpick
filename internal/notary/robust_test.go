package notary

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v4"

	"github.com/nitpick-oss/nitpick/internal/wire"
)

func robustServer(t *testing.T) (srv *httptest.Server, dir string) {
	t.Helper()
	dir = t.TempDir()
	fs, err := wire.NewFileStore(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	srv = httptest.NewServer(New(fs, []Token{
		NewToken("ci", RoleAuthoritative, "ci-secret"),
		NewToken("dev", RoleProvisional, "dev-secret"),
		NewToken("viewer", RoleRead, "view-secret"),
	}, true))
	t.Cleanup(srv.Close)
	return srv, dir
}

// do sends a raw request so nothing normalises the path or the headers first.
func do(t *testing.T, method, url string, hdr map[string]string, body io.Reader) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return resp.StatusCode, string(b)
}

func snapshot(t *testing.T, root string) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err == nil {
			out[p] = info.Size()
		}
		return nil
	})
	return out
}

func TestAMalformedOrTraversingHashNeverReachesTheFilesystem(t *testing.T) {
	srv, dir := robustServer(t)
	before := snapshot(t, dir)
	auth := map[string]string{"Authorization": "Bearer ci-secret"}
	good := strings.Repeat("a", 64)
	for name, path := range map[string]string{
		"uppercase":          "/v1/records/" + strings.Repeat("A", 64),
		"63 characters":      "/v1/records/" + good[1:],
		"65 characters":      "/v1/records/" + good + "a",
		"non hex":            "/v1/records/" + strings.Repeat("g", 64),
		"dot dot":            "/v1/records/../" + good,
		"encoded dot dot":    "/v1/records/%2e%2e%2f" + good,
		"encoded slash":      "/v1/records/..%2f..%2f" + good,
		"trailing slash":     "/v1/records/" + good + "/",
		"nested":             "/v1/records/x/" + good,
		"with a null byte":   "/v1/records/" + good[:60] + "%00aaa",
		"with a space":       "/v1/records/" + good[:60] + "%20aaa",
		"unicode":            "/v1/records/" + strings.Repeat("é", 32),
		"history directory":  "/v1/records/history",
		"empty":              "/v1/records/",
		"the data dir":       "/v1/records/data",
		"hash with .json":    "/v1/records/" + good + ".json",
		"double slash":       "/v1/records//" + good,
		"a path outside /v1": "/" + good,
	} {
		for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
			var body io.Reader
			if method == http.MethodPut {
				body = strings.NewReader(`{"schema":4,"hash":"` + good + `","answers":{"k":"{}"},"batches":1,"complete":true}`)
			}
			status, _ := do(t, method, srv.URL+path, auth, body)
			// 400 for a hash that is not one, 404 for a path that is not a record.
			// Never a 5xx: that would be the store rejecting what the server let
			// through, and never a success.
			if status != http.StatusBadRequest && status != http.StatusNotFound {
				t.Errorf("%s %s (%s) answered %d, want 400 or 404", method, name, path, status)
			}
		}
	}
	after := snapshot(t, dir)
	if len(after) != len(before) {
		t.Errorf("rejected requests changed the filesystem: %d entries before, %d after: %v", len(before), len(after), after)
	}
	// And nothing was written outside the data directory.
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("the server's parent directory holds %d entries, want just data/", len(entries))
	}
}

func TestAuthorizationHeaderVariants(t *testing.T) {
	srv, _ := robustServer(t)
	url := srv.URL + "/v1/records/" + strings.Repeat("b", 64)
	cases := []struct {
		name string
		hdr  func(*http.Request)
		want int
	}{
		{"exact", func(r *http.Request) { r.Header.Set("Authorization", "Bearer ci-secret") }, http.StatusNotFound},
		{"none", func(r *http.Request) {}, http.StatusUnauthorized},
		{"empty", func(r *http.Request) { r.Header.Set("Authorization", "") }, http.StatusUnauthorized},
		{"scheme only", func(r *http.Request) { r.Header.Set("Authorization", "Bearer") }, http.StatusUnauthorized},
		{"scheme and space only", func(r *http.Request) { r.Header.Set("Authorization", "Bearer ") }, http.StatusUnauthorized},
		{"basic scheme", func(r *http.Request) { r.Header.Set("Authorization", "Basic Y2k6Y2ktc2VjcmV0") }, http.StatusUnauthorized},
		// HTTP strips trailing whitespace from a header value before the server
		// sees it, so this is the exact secret and is meant to authenticate.
		{"trailing space is stripped by HTTP", func(r *http.Request) { r.Header.Set("Authorization", "Bearer ci-secret ") }, http.StatusNotFound},
		{"a lower case scheme is not accepted", func(r *http.Request) { r.Header.Set("Authorization", "bearer ci-secret") }, http.StatusUnauthorized},
		{"leading space on the secret", func(r *http.Request) { r.Header.Set("Authorization", "Bearer  ci-secret") }, http.StatusUnauthorized},
		{"wrong case of the secret", func(r *http.Request) { r.Header.Set("Authorization", "Bearer CI-SECRET") }, http.StatusUnauthorized},
		{"the secret's prefix", func(r *http.Request) { r.Header.Set("Authorization", "Bearer ci-secre") }, http.StatusUnauthorized},
		{"the secret plus a suffix", func(r *http.Request) { r.Header.Set("Authorization", "Bearer ci-secretX") }, http.StatusUnauthorized},
		{"two headers, bad first", func(r *http.Request) {
			r.Header.Add("Authorization", "Bearer nope")
			r.Header.Add("Authorization", "Bearer ci-secret")
		}, http.StatusUnauthorized},
		{"a 1MB token", func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 1<<20)) }, 0},
		{"the access token as a query parameter", func(r *http.Request) { r.URL.RawQuery = "token=ci-secret" }, http.StatusUnauthorized},
		{"the secret in a cookie", func(r *http.Request) { r.Header.Set("Cookie", "token=ci-secret") }, http.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodGet, url, nil)
			tc.hdr(req)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				if tc.want == 0 {
					return // a header that large may be refused by the transport
				}
				t.Fatal(err)
			}
			defer func() { _ = resp.Body.Close() }()
			if tc.want != 0 && resp.StatusCode != tc.want {
				t.Errorf("status = %d, want %d", resp.StatusCode, tc.want)
			}
			if tc.want == 0 && resp.StatusCode < 400 {
				t.Errorf("a 1MB token was accepted: %d", resp.StatusCode)
			}
		})
	}
}

func TestMethodsOtherThanGetPutDeleteAreRefused(t *testing.T) {
	srv, _ := robustServer(t)
	url := srv.URL + "/v1/records/" + strings.Repeat("b", 64)
	for _, m := range []string{http.MethodHead, http.MethodOptions, http.MethodPost, http.MethodPatch, http.MethodTrace, "PROPFIND"} {
		status, _ := do(t, m, url, map[string]string{"Authorization": "Bearer ci-secret"}, nil)
		if status != http.StatusMethodNotAllowed {
			t.Errorf("%s = %d, want 405", m, status)
		}
	}
}

func TestARecordPastTheSizeLimitIsRefusedNotStoredTruncated(t *testing.T) {
	if testing.Short() {
		t.Skip("writes and reads 30MB records")
	}
	srv, dir := robustServer(t)
	hash := strings.Repeat("d", 64)
	put := func(size int) (int, string) {
		rec := wire.Record{Schema: wire.Schema, Hash: hash, Complete: true, Batches: 1,
			Answers: map[string]string{"k": strings.Repeat("x", size)}}
		b, _ := json.Marshal(rec)
		return do(t, http.MethodPut, srv.URL+"/v1/records/"+hash, map[string]string{"Authorization": "Bearer ci-secret"}, bytes.NewReader(b))
	}
	if status, body := put(30 << 20); status != http.StatusNoContent {
		t.Fatalf("a 30MB record was refused: %d %s", status, body)
	}
	if status, _ := put(33 << 20); status < 400 {
		t.Fatalf("a record over the limit was accepted: %d", status)
	}
	// The 30MB record is still the one stored: a refused write changed nothing.
	fs, _ := wire.NewFileStore(filepath.Join(dir, "data"))
	got, ok, err := fs.Get(context.Background(), hash)
	if err != nil || !ok || len(got.Answers["k"]) != 30<<20 {
		t.Errorf("after the refused write: ok=%v err=%v size=%d, want the 30MB record intact", ok, err, len(got.Answers["k"]))
	}
}

func TestAnErrorBodyNeverEchoesATokenOrAFilesystemPath(t *testing.T) {
	srv, dir := robustServer(t)
	hash := strings.Repeat("e", 64)
	secrets := []string{"ci-secret", "dev-secret", "view-secret", dir}
	bad := []struct {
		method, path, body string
		token              string
	}{
		{http.MethodGet, "/v1/records/" + hash, "", "wrong-token-value"},
		{http.MethodPut, "/v1/records/" + hash, `{not json`, "ci-secret"},
		{http.MethodPut, "/v1/records/" + hash, `{"schema":4,"hash":"` + strings.Repeat("f", 64) + `"}`, "ci-secret"},
		{http.MethodPut, "/v1/records/" + hash, `{"unknown":1}`, "ci-secret"},
		{http.MethodPut, "/v1/records/" + hash, `{"schema":4,"hash":"` + hash + `"}`, "view-secret"},
		{http.MethodDelete, "/v1/records/" + hash, "", "view-secret"},
		{http.MethodGet, "/v1/records/not-a-hash", "", "ci-secret"},
	}
	for _, b := range bad {
		_, body := do(t, b.method, srv.URL+b.path, map[string]string{"Authorization": "Bearer " + b.token}, strings.NewReader(b.body))
		for _, s := range secrets {
			if strings.Contains(body, s) {
				t.Errorf("%s %s answered with %q, which contains %q", b.method, b.path, body, s)
			}
		}
	}
}

func TestADeleteOfARecordThatNeverExistedIsHarmlessAndLeavesNoFile(t *testing.T) {
	srv, dir := robustServer(t)
	before := snapshot(t, dir)
	status, _ := do(t, http.MethodDelete, srv.URL+"/v1/records/"+strings.Repeat("9", 64), map[string]string{"Authorization": "Bearer ci-secret"}, nil)
	if status != http.StatusNoContent {
		t.Errorf("delete of nothing = %d, want 204", status)
	}
	if after := snapshot(t, dir); len(after) != len(before) {
		t.Errorf("a clear of nothing created files: %v", after)
	}
}

func TestASlowKeyFetchForAnUnknownKeyDoesNotStallTokensWhoseKeyIsCached(t *testing.T) {
	p := newIDP(t)
	slow := make(chan struct{})
	hold := p.srv.Config.Handler
	p.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-slow:
			time.Sleep(1500 * time.Millisecond)
		default:
		}
		hold.ServeHTTP(w, r)
	})
	v := p.verifier(newOIDCRule("*", RoleAuthoritative))
	good := p.token(t, nil)
	if _, ok := v.Authenticate(context.Background(), good); !ok {
		t.Fatal("setup: the cached-key token was refused")
	}
	// Make the cache stale enough that an unknown key id triggers a fetch, and
	// make that fetch slow.
	v.mu.Lock()
	v.fetched = time.Now().Add(-2 * keyRefresh)
	v.mu.Unlock()
	close(slow)

	unknown := jwt.NewWithClaims(jwt.SigningMethodRS256, &jwt.RegisteredClaims{})
	unknown.Header["kid"] = "never-published"
	bad, _ := unknown.SignedString(p.key)
	go v.Authenticate(context.Background(), bad)
	time.Sleep(200 * time.Millisecond) // the slow fetch is now in flight

	started := time.Now()
	if _, ok := v.Authenticate(context.Background(), good); !ok {
		t.Fatal("a token with a cached key was refused during a fetch")
	}
	if took := time.Since(started); took > 500*time.Millisecond {
		t.Errorf("a valid token waited %v behind a slow fetch for a key it did not need: one unauthenticated request with an unknown key id can stall every workflow", took)
	}
}

func TestManySimultaneousUnknownKeysCostTheIssuerOneRequest(t *testing.T) {
	p := newIDP(t)
	var hits atomic.Int64
	inner := p.srv.Config.Handler
	p.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		time.Sleep(300 * time.Millisecond)
		inner.ServeHTTP(w, r)
	})
	v := p.verifier(newOIDCRule("*", RoleAuthoritative))
	unknown := jwt.NewWithClaims(jwt.SigningMethodRS256, &jwt.RegisteredClaims{})
	unknown.Header["kid"] = "never-published"
	bad, _ := unknown.SignedString(p.key)

	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v.Authenticate(context.Background(), bad)
		}()
	}
	wg.Wait()
	if n := hits.Load(); n != 1 {
		t.Errorf("20 simultaneous tokens with an unknown key id made %d requests to the issuer, want 1", n)
	}
}
