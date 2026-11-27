package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nitpick-oss/nitpick/internal/notary"
	"github.com/nitpick-oss/nitpick/internal/server/auth"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func writeFile(t *testing.T, dir, name, body string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	return p
}

// opts is a notary with one machine token and nothing else.
func opts(t *testing.T) *options {
	t.Helper()
	o := &options{addr: "127.0.0.1:0", dir: t.TempDir(), oidcIssuer: notary.GitHubIssuer}
	if err := o.tokens.Set("ci:authoritative:secret"); err != nil {
		t.Fatal(err)
	}
	return o
}

// A user's hash, cheap, so a test does not pay for the production cost.
const quickUser = "alice:$argon2id$v=19$m=64,t=1,p=1$c2FsdHNhbHRzYWx0c2E$c2FsdHNhbHRzYWx0c2FsdHNhbHRzYWx0c2E\n"

// With no configuration file the server is the notary and nothing else: the same
// routes, no sign-in, so no UI. This is nitpick-notary under a new name.
func TestWithNoConfigurationTheServerIsTheNotaryAlone(t *testing.T) {
	h, closeStore, err := build(context.Background(), opts(t), quiet)
	if err != nil {
		t.Fatal(err)
	}
	defer closeStore()
	ts := httptest.NewServer(h)
	defer ts.Close()

	for path, want := range map[string]int{"/healthz": 200, "/ui/": 404, "/auth/login": 404, "/v1/records/" + strings.Repeat("a", 64): 401} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("GET %s = %d, want %d", path, resp.StatusCode, want)
		}
	}
}

func TestAPasswordProviderTurnsTheUIOnForAPersonWithAUsersFile(t *testing.T) {
	dir := t.TempDir()
	users := writeFile(t, dir, "users", quickUser, 0o600)
	cfg := writeFile(t, dir, "server.yaml", "auth:\n  password:\n    users_file: "+users+"\nroles:\n  - {role: authoritative, user: alice}\n", 0o600)
	o := opts(t)
	o.config = cfg
	h, closeStore, err := build(context.Background(), o, quiet)
	if err != nil {
		t.Fatal(err)
	}
	defer closeStore()
	ts := httptest.NewServer(h)
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/auth/login")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("with a provider /auth/login = %d, want 200", resp.StatusCode)
	}
	// The UI pages are either the stub (a plain go build) or the sign-in redirect
	// (a built UI). Which one depends on whether `make ui` ran in this tree, and
	// either shows the UI is wired in; a 404 would mean it is not.
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err = noFollow.Get(ts.URL + "/ui/")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	stub := resp.StatusCode == 200 && strings.Contains(string(b), "The UI was not built")
	redirect := resp.StatusCode == http.StatusSeeOther && strings.HasPrefix(resp.Header.Get("Location"), "/auth/login")
	if !stub && !redirect {
		t.Errorf("/ui/ = %d %q, want the not-built stub or the sign-in redirect", resp.StatusCode, b)
	}
}

// Everything that can be wrong with the configuration is an error before the
// first request, and says what.
func TestStartUpRefusesWhatCouldNotWork(t *testing.T) {
	dir := t.TempDir()
	users := writeFile(t, dir, "users", quickUser, 0o600)
	badUsers := writeFile(t, dir, "bad-users", "alice:hunter2\n", 0o600)
	for name, tc := range map[string]struct {
		yaml, want string
		mutate     func(*options)
	}{
		"an unknown key":                     {"sessions: {idle: 1m}\n", "sessions", nil},
		"an unknown nested key":              {"auth:\n  password:\n    users_fiel: x\n", "users_fiel", nil},
		"not yaml":                           {"auth: [\n", "", nil},
		"an unknown component":               {"components: [reviews]\n", "not a component", nil},
		"a repeated component":               {"components: [notary, notary]\n", "twice", nil},
		"a negative timeout":                 {"session: {idle: -1m}\n", "negative", nil},
		"a role for no provider":             {"roles:\n  - {role: read, provider: oidc, group: g}\n", "not configured", nil},
		"a role with two selectors":          {"auth:\n  password: {users_file: " + users + "}\nroles:\n  - {role: read, user: a, group: g}\n", "exactly one", nil},
		"a role with no selector":            {"auth:\n  password: {users_file: " + users + "}\nroles:\n  - {role: read}\n", "exactly one", nil},
		"a missing users file":               {"auth:\n  password: {users_file: /no/such/file}\n", "no such file", nil},
		"no users file named":                {"auth:\n  password: {}\n", "users_file is required", nil},
		"a users file with a plain password": {"auth:\n  password: {users_file: " + badUsers + "}\n", "argon2id", nil},
		"saml with nothing":                  {"auth:\n  saml: {}\n", "are all required", nil},
		"oidc that cannot be reached":        {"auth:\n  oidc: {issuer: http://127.0.0.1:1, client_id: c, redirect_url: 'https://x/cb'}\n", "oidc", nil},
		"oidc and rules apart":               {"", "go together", func(o *options) { o.oidcAudience = "aud" }},
	} {
		o := opts(t)
		if tc.yaml != "" {
			o.config = writeFile(t, t.TempDir(), "server.yaml", tc.yaml, 0o600)
		}
		if tc.mutate != nil {
			tc.mutate(o)
		}
		h, closeStore, err := build(context.Background(), o, quiet)
		if err == nil {
			closeStore()
			t.Errorf("%s: start-up succeeded (%T)", name, h)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: the error is %q, want it to say %q", name, err, tc.want)
		}
	}
}

// A notary no machine can write to and no person can sign in to was not meant
// to be started.
func TestANotaryWithNoWayInIsRefused(t *testing.T) {
	o := &options{addr: "x", dir: t.TempDir(), oidcIssuer: notary.GitHubIssuer}
	if _, _, err := build(context.Background(), o, quiet); err == nil || !strings.Contains(err.Error(), "-token") {
		t.Errorf("err = %v, want the missing credentials named", err)
	}
	// Rule 10: a token alone is enough, so the refusal is the absence of any.
	if _, closeStore, err := build(context.Background(), opts(t), quiet); err != nil {
		t.Errorf("a notary with a token was refused: %v", err)
	} else {
		closeStore()
	}
}

// One server per data directory: a second is refused and says why.
func TestASecondServerOnTheSameDirectoryIsRefused(t *testing.T) {
	o := opts(t)
	_, closeStore, err := build(context.Background(), o, quiet)
	if err != nil {
		t.Fatal(err)
	}
	defer closeStore()
	o2 := opts(t)
	o2.dir = o.dir
	if _, _, err := build(context.Background(), o2, quiet); err == nil || !strings.Contains(err.Error(), "one server per data directory") {
		t.Errorf("err = %v, want the lock refused with its reason", err)
	}
}

// A failed start-up releases the directory, so a second attempt is not locked out
// by the first one's leftovers.
func TestAFailedStartUpReleasesTheDataDirectory(t *testing.T) {
	o := opts(t)
	o.config = writeFile(t, t.TempDir(), "server.yaml", "components: [notary]\nroles:\n  - {role: ' ', user: a}\n", 0o600)
	if _, _, err := build(context.Background(), o, quiet); err == nil {
		t.Fatal("a rule granting a blank role was accepted")
	}
	o.config = ""
	_, closeStore, err := build(context.Background(), o, quiet)
	if err != nil {
		t.Fatalf("the directory was left locked by the failed start-up: %v", err)
	}
	closeStore()
}

func TestTheConfigurationFileIsOptionalAndEmptyIsFine(t *testing.T) {
	if cfg, err := loadConfig(""); err != nil || len(cfg.components()) != 1 || cfg.components()[0] != "notary" {
		t.Errorf("no file = %+v, %v; want the notary by default", cfg, err)
	}
	empty := writeFile(t, t.TempDir(), "empty.yaml", "", 0o600)
	if cfg, err := loadConfig(empty); err != nil || len(cfg.components()) != 1 {
		t.Errorf("an empty file = %+v, %v", cfg, err)
	}
}

// -hash-password prints a hash a users file accepts, from standard input and not
// from an argument, so the password is not in the process list.
func TestHashPasswordProducesAHashAUsersFileAccepts(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"-hash-password"}, strings.NewReader("correct horse\n"), &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	hash := strings.TrimSpace(out.String())
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=65536,t=3,p=4$") {
		t.Fatalf("hash = %q", hash)
	}
	users, err := auth.ParseUsers(strings.NewReader("carol:" + hash + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok := users.Verify("carol", "correct horse"); !ok {
		t.Error("the printed hash does not verify the password it was made from")
	}
	if _, _, ok := users.Verify("carol", "correct horse\n"); ok {
		t.Error("the trailing newline became part of the password")
	}
	if strings.Contains(out.String()+errOut.String(), "correct horse") {
		t.Error("the password was printed")
	}
}

func TestHashStillPrintsAShaTokenForTheTokenFlag(t *testing.T) {
	var out bytes.Buffer
	if code := run([]string{"-hash"}, strings.NewReader("s3cret\n"), &out, io.Discard); code != 0 {
		t.Fatal(code)
	}
	if got := strings.TrimSpace(out.String()); got != hashPrefix+notary.HashSecret("s3cret") {
		t.Errorf("-hash = %q", got)
	}
}

func TestAnUnreadableConfigFileNamesItself(t *testing.T) {
	_, err := loadConfig(filepath.Join(t.TempDir(), "nope.yaml"))
	if err == nil || !strings.Contains(err.Error(), "read the configuration") {
		t.Errorf("err = %v", err)
	}
}

// A users file other accounts can read is allowed (it holds hashes) but said.
func TestAWorldReadableUsersFileIsWarnedAbout(t *testing.T) {
	dir := t.TempDir()
	users := writeFile(t, dir, "users", quickUser, 0o644)
	if err := os.Chmod(users, 0o644); err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	cfg := &fileConfig{}
	cfg.Auth.Password = &struct {
		UsersFile string `yaml:"users_file"`
	}{UsersFile: users}
	if _, err := cfg.providers(context.Background(), slog.New(slog.NewTextHandler(&buf, nil))); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "readable by every account") {
		t.Errorf("no warning for a 0644 users file:\n%s", buf.String())
	}
	private := writeFile(t, dir, "private", quickUser, 0o600)
	buf.Reset()
	cfg.Auth.Password.UsersFile = private
	if _, err := cfg.providers(context.Background(), slog.New(slog.NewTextHandler(&buf, nil))); err != nil || strings.Contains(buf.String(), "readable") {
		t.Errorf("a 0600 file warned or failed: %v %s", err, buf.String())
	}
}

// A role name no enabled component knows grants nothing, and a typo in one is the
// permission quietly not given. It is refused at start-up, naming the roles that
// do exist.
func TestARoleNoComponentKnowsIsRefused(t *testing.T) {
	dir := t.TempDir()
	users := writeFile(t, dir, "users", quickUser, 0o600)
	for _, role := range []string{"author" + "ative", "admin", "Authoritative", "read "} {
		o := opts(t)
		o.config = writeFile(t, t.TempDir(), "server.yaml",
			"auth:\n  password: {users_file: "+users+"}\nroles:\n  - {role: '"+role+"', user: alice}\n", 0o600)
		_, closeStore, err := build(context.Background(), o, quiet)
		if err == nil {
			closeStore()
			t.Errorf("the role %q was accepted", role)
			continue
		}
		if !strings.Contains(err.Error(), "authoritative") {
			t.Errorf("role %q: the error %q does not name the roles that exist", role, err)
		}
	}
	// Rule 10: each real role is accepted, so the refusals are the names'.
	for _, role := range []string{"authoritative", "provisional", "read"} {
		o := opts(t)
		o.config = writeFile(t, t.TempDir(), "server.yaml",
			"auth:\n  password: {users_file: "+users+"}\nroles:\n  - {role: "+role+", user: alice}\n", 0o600)
		_, closeStore, err := build(context.Background(), o, quiet)
		if err != nil {
			t.Errorf("the role %q was refused: %v", role, err)
			continue
		}
		closeStore()
	}
}

// trust_forwarded_for decides whether a client can pick its own address for the
// limiter, so it must reach the provider, and must default off.
func TestTrustForwardedForReachesThePasswordProviderAndDefaultsOff(t *testing.T) {
	users := writeFile(t, t.TempDir(), "users", quickUser, 0o600)
	for trust, yaml := range map[bool]string{
		false: "auth:\n  password: {users_file: " + users + "}\n",
		true:  "auth:\n  trust_forwarded_for: true\n  password: {users_file: " + users + "}\n",
	} {
		cfg := writeFile(t, t.TempDir(), "server.yaml", yaml, 0o600)
		loaded, err := loadConfig(cfg)
		if err != nil {
			t.Fatal(err)
		}
		providers, err := loaded.providers(context.Background(), quiet)
		if err != nil || len(providers) != 1 {
			t.Fatalf("providers = %v, %v", providers, err)
		}
		pw, ok := providers[0].(*auth.Password)
		if !ok {
			t.Fatalf("the provider is %T", providers[0])
		}
		if pw.TrustForwardedFor != trust {
			t.Errorf("trust_forwarded_for in the file is %v and the provider has %v", trust, pw.TrustForwardedFor)
		}
	}
}
