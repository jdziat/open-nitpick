package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v4"

	"github.com/nitpick-oss/nitpick/internal/config"
	"github.com/nitpick-oss/nitpick/internal/notary"
	"github.com/nitpick-oss/nitpick/internal/wire"
)

// fakeModel is an OpenAI-compatible endpoint that counts the requests it
// answers. It is the one thing in this test the real tool does not own, and
// the count is what "zero model calls" is measured against.
type fakeModel struct {
	srv   *httptest.Server
	calls atomic.Int64
	// failTriage makes any request that asks for triage fail, the way a
	// dropped connection would, so a run can be interrupted partway.
	failTriage atomic.Bool
	// severity is what the review finding is rated, "error" when unset. The
	// residual approval judge only runs for findings at or below warning.
	severity atomic.Pointer[string]
	// seen counts requests by a marker the test asks about.
	mu   sync.Mutex
	seen []string
}

// callsMatching reports how many requests contained marker.
func (m *fakeModel) callsMatching(marker string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, s := range m.seen {
		if strings.Contains(s, marker) {
			n++
		}
	}
	return n
}

func newFakeModel(t *testing.T) *fakeModel {
	t.Helper()
	m := &fakeModel{}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		m.calls.Add(1)
		text := string(body)
		m.mu.Lock()
		m.seen = append(m.seen, text)
		m.mu.Unlock()
		var content string
		switch {
		case strings.Contains(text, "triaging findings"):
			if m.failTriage.Load() {
				http.Error(w, "upstream reset", http.StatusBadRequest)
				return
			}
			content = `{"summary":"Adds a greeting.","verdicts":[]}`
		case strings.Contains(text, "You decide whether a pull request review"):
			content = `{"approve":true,"reason":"advisory only"}`
		default:
			severity := "error"
			if v := m.severity.Load(); v != nil {
				severity = *v
			}
			content = `{"findings":[{"path":"hello.go","line":4,"severity":"` + severity + `","category":"correctness","title":"Ignored error from http.Get","rationale":"resp may be nil, so the deferred Close panics.","confidence":0.9}]}`
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "x", "object": "chat.completion", "model": "fake",
			"choices": []map[string]any{{
				"index": 0, "finish_reason": "stop",
				"message": map[string]any{"role": "assistant", "content": content},
			}},
			"usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15},
		})
	}))
	t.Cleanup(m.srv.Close)
	return m
}

func gitRepo(t *testing.T, model *fakeModel) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	run("init", "-q", "-b", "main")
	write(".nitpick.yaml", fmt.Sprintf(`models:
  default:
    provider: llamacpp
    model: fake
    base_url: %s
review:
  incremental: false
  critique: false
`, model.srv.URL))
	write("hello.go", "package hello\n\nfunc Hi() {}\n")
	run("add", ".")
	run("commit", "-q", "-m", "base")
	write("hello.go", "package hello\n\nfunc Hi() {\n\tresp, _ := http.Get(\"http://x\")\n\tdefer resp.Body.Close()\n}\n")
	return dir
}

// reviewJSON runs the real review command and returns what it printed as JSON.
func reviewJSON(t *testing.T, repo string, args ...string) (map[string]any, error) {
	t.Helper()
	t.Setenv(config.EnvTrustConfigEndpoints, "1")
	// A CI runner sets these, and RefFromEnv turns them into a pull request
	// review against GitHub. The tests review a local repository.
	for _, name := range []string{"GITHUB_ACTIONS", "GITHUB_REPOSITORY", "GITHUB_REF", "PR_NUMBER", "GITHUB_EVENT_PATH", "GITHUB_TOKEN", "NITPICK_GITHUB_TOKEN"} {
		t.Setenv(name, "")
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	runErr := runReview(context.Background(), append([]string{"-repo", repo, "-dry-run", "-format", "json", "-no-linters", "-fail-on", "none"}, args...))
	_ = w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	var out map[string]any
	if buf.Len() > 0 {
		// The text review precedes the JSON object on a dry run.
		raw := buf.Bytes()
		if i := bytes.LastIndex(raw, []byte("\n{")); i >= 0 {
			raw = raw[i+1:]
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("output is not JSON: %v\n%s", err, buf.String())
		}
	}
	return out, runErr
}

func notaryServer(t *testing.T) (url string, store *wire.FileStore) {
	t.Helper()
	store, err := wire.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(notary.New(store, []notary.Token{
		notary.NewToken("ci", notary.RoleAuthoritative, "ci-secret"),
		notary.NewToken("viewer", notary.RoleRead, "view-secret"),
	}, false))
	t.Cleanup(srv.Close)
	return srv.URL, store
}

func notaryField(t *testing.T, out map[string]any, key string) any {
	t.Helper()
	n, ok := out["notary"].(map[string]any)
	if !ok {
		t.Fatalf("report has no notary object: %v", out)
	}
	return n[key]
}

func TestNotaryEndToEndRecordReplayResumeFreshAndRoles(t *testing.T) {
	model := newFakeModel(t)
	repo := gitRepo(t, model)
	url, store := notaryServer(t)
	t.Setenv(EnvNotaryToken, "ci-secret")

	// 1. First review is live and leaves a complete record.
	first, err := reviewJSON(t, repo, "-notary", url)
	if err != nil {
		t.Fatalf("first review: %v", err)
	}
	liveCalls := model.calls.Load()
	if liveCalls == 0 {
		t.Fatal("the first review made no model call")
	}
	if got := notaryField(t, first, "Mode"); got != "live" {
		t.Fatalf("first mode = %v", got)
	}
	if got := notaryField(t, first, "Complete"); got != true {
		t.Fatalf("first record is not complete: %v", first["notary"])
	}
	findings, _ := first["findings"].([]any)
	if len(findings) != 1 {
		t.Fatalf("first findings = %d, want 1", len(findings))
	}

	// 2. The same inputs replay, with zero model calls and the same finding.
	second, err := reviewJSON(t, repo, "-notary", url)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if extra := model.calls.Load() - liveCalls; extra != 0 {
		t.Fatalf("the replay made %d model requests, want 0", extra)
	}
	if got := notaryField(t, second, "Mode"); got != "replayed" {
		t.Fatalf("replay mode = %v", got)
	}
	replayed, _ := second["findings"].([]any)
	if len(replayed) != 1 || replayed[0].(map[string]any)["title"] != findings[0].(map[string]any)["title"] {
		t.Fatalf("replayed findings differ: %v vs %v", replayed, findings)
	}
	if usage, _ := second["usage"].(map[string]any); usage != nil {
		if attempts, _ := usage["attempts"].([]any); len(attempts) != 0 {
			t.Errorf("a replay's usage ledger lists %d provider attempts, want none", len(attempts))
		}
	}

	// 3. A read token replays, and never writes.
	t.Setenv(EnvNotaryToken, "view-secret")
	ro, err := reviewJSON(t, repo, "-notary", url)
	if err != nil {
		t.Fatalf("read-token replay: %v", err)
	}
	if got := notaryField(t, ro, "Mode"); got != "replayed" {
		t.Errorf("read token replay mode = %v", got)
	}
	t.Setenv(EnvNotaryToken, "ci-secret")

	// 4. -fresh clears and re-reviews, and the history survives.
	before := model.calls.Load()
	fresh, err := reviewJSON(t, repo, "-notary", url, "-fresh", "-clear-by", "alice")
	if err != nil {
		t.Fatalf("fresh: %v", err)
	}
	if model.calls.Load() == before {
		t.Fatal("-fresh did not ask the model again")
	}
	hash, _ := notaryField(t, fresh, "Hash").(string)
	rec, ok, err := store.Get(context.Background(), hash)
	if err != nil || !ok || rec.Clears != 1 || len(rec.ClearedBy) != 1 || rec.ClearedBy[0] != "alice" || !rec.Complete {
		t.Fatalf("record after -fresh: ok=%v err=%v %+v", ok, err, rec)
	}

	// 5. Without a notary the same review says so, and is not mistaken for a
	// replay of a clean review.
	none, err := reviewJSON(t, repo)
	if err != nil {
		t.Fatalf("no notary: %v", err)
	}
	if got := notaryField(t, none, "Mode"); got != "" {
		t.Errorf("a review with no notary reports mode %q", got)
	}
}

func TestNotaryEndToEndAnInterruptedReviewResumes(t *testing.T) {
	model := newFakeModel(t)
	repo := gitRepo(t, model)
	url, store := notaryServer(t)
	t.Setenv(EnvNotaryToken, "ci-secret")

	model.failTriage.Store(true)
	// The triage stage fails; the review stage's answer is already recorded.
	broken, _ := reviewJSON(t, repo, "-notary", url, "-allow-degraded-triage")
	if broken != nil && notaryField(t, broken, "Complete") == true {
		t.Fatalf("an interrupted review left a complete record: %v", broken["notary"])
	}

	model.failTriage.Store(false)
	var reviewAsks atomic.Int64
	model.srv.Config.Handler = countingReview(model.srv.Config.Handler, &reviewAsks)
	resumed, err := reviewJSON(t, repo, "-notary", url)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if n := reviewAsks.Load(); n != 0 {
		t.Errorf("the resumed run re-asked the review stage %d times", n)
	}
	if got := notaryField(t, resumed, "Mode"); got != "resumed" {
		t.Errorf("resume mode = %v", got)
	}
	if got := notaryField(t, resumed, "Complete"); got != true {
		t.Errorf("the finished resume must complete the record: %v", resumed["notary"])
	}
	hash, _ := notaryField(t, resumed, "Hash").(string)
	if rec, ok, _ := store.Get(context.Background(), hash); !ok || !rec.Complete {
		t.Errorf("stored record after resume: ok=%v complete=%v", ok, rec.Complete)
	}
}

func countingReview(next http.Handler, n *atomic.Int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		if !strings.Contains(string(body), "triaging findings") {
			n.Add(1)
		}
		next.ServeHTTP(w, r)
	})
}

func TestNotaryEndToEndRequireNotaryWithholdsAnUnofficialReview(t *testing.T) {
	model := newFakeModel(t)
	repo := gitRepo(t, model)

	// Nothing listens here: the notary is unreachable.
	t.Setenv(EnvNotaryToken, "ci-secret")
	out, err := reviewJSON(t, repo, "-notary", "http://127.0.0.1:1", "-require-notary")
	if err != nil {
		t.Fatalf("an unofficial review still reports its findings: %v", err)
	}
	if out["unofficial"] != true {
		t.Errorf("report is not labelled unofficial: %v", out)
	}
	if got := notaryField(t, out, "Mode"); got != "unreachable" {
		t.Errorf("mode = %v, want unreachable", got)
	}
}

func TestNotaryFlagsAreRefusedWhenTheyCannotWork(t *testing.T) {
	t.Setenv(EnvNotaryToken, "")
	// A repository with a model configured: with none, every case fails on
	// that first and the flag check is never reached.
	repo := gitRepo(t, newFakeModel(t))
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"fresh with no notary":      {[]string{"-fresh"}, "-fresh needs -notary"},
		"audience with no notary":   {[]string{"-notary-oidc-audience", "x"}, "-notary-oidc-audience needs -notary"},
		"clear-by with no notary":   {[]string{"-clear-by", "alice"}, "-clear-by needs -notary"},
		"notary with no credential": {[]string{"-notary", "http://127.0.0.1:1"}, "-notary needs a credential"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := reviewJSON(t, repo, tc.args...)
			// The message, not just an error: any other failure would satisfy a
			// bare nil check.
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want one containing %q", err, tc.want)
			}
		})
	}
}

// mintingEndpoint stands in for the GitHub Actions token service and the
// issuer's key set, signing with a key the notary is told to trust.
func mintingEndpoint(t *testing.T, subject string) (mint, jwks string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer request-token" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		tok := jwt.NewWithClaims(jwt.SigningMethodRS256, &jwt.RegisteredClaims{
			Issuer: "https://issuer.test", Subject: subject,
			Audience:  jwt.ClaimStrings{r.URL.Query().Get("audience")},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(5 * time.Minute)),
		})
		tok.Header["kid"] = "k1"
		signed, err := tok.SignedString(key)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"value": signed})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "kid": "k1",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL + "/token", srv.URL + "/jwks"
}

func TestNotaryEndToEndAWorkflowIdentityTokenReplacesAStoredSecret(t *testing.T) {
	model := newFakeModel(t)
	repo := gitRepo(t, model)
	mint, jwks := mintingEndpoint(t, "repo:acme/app:ref:refs/heads/main")

	store, err := wire.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(notary.New(store, nil, false).WithOIDC(&notary.OIDC{
		Issuer: "https://issuer.test", Audience: "nitpick-notary", JWKSURL: jwks,
		Allow: []notary.OIDCRule{mustRule(t, "authoritative:repo:acme/app:*")},
	}))
	t.Cleanup(srv.Close)

	t.Setenv(EnvNotaryToken, "")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", mint+"?api-version=2")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "request-token")

	first, err := reviewJSON(t, repo, "-notary", srv.URL, "-notary-oidc-audience", "nitpick-notary")
	if err != nil {
		t.Fatalf("first review: %v", err)
	}
	if got := notaryField(t, first, "Mode"); got != "live" || notaryField(t, first, "Complete") != true {
		t.Fatalf("first notary = %v", first["notary"])
	}
	calls := model.calls.Load()
	second, err := reviewJSON(t, repo, "-notary", srv.URL, "-notary-oidc-audience", "nitpick-notary")
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if model.calls.Load() != calls || notaryField(t, second, "Mode") != "replayed" {
		t.Errorf("replay through an identity token asked the model again: %v", second["notary"])
	}

	// The wrong audience gets a token the notary refuses, and the review runs
	// on without a record rather than failing.
	wrong, err := reviewJSON(t, repo, "-notary", srv.URL, "-notary-oidc-audience", "someone-else")
	if err != nil {
		t.Fatalf("refused-token review: %v", err)
	}
	if got := notaryField(t, wrong, "Mode"); got == "replayed" || got == "live" {
		t.Errorf("a token for another audience reached the record: mode=%v", got)
	}
}

func mustRule(t *testing.T, v string) notary.OIDCRule {
	t.Helper()
	r, err := notary.ParseOIDCRule(v)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
