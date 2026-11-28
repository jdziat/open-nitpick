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
	"strconv"
	"strings"
	"testing"

	"github.com/nitpick-oss/nitpick/internal/database/databasetest"
	"github.com/nitpick-oss/nitpick/internal/notary"
	"github.com/nitpick-oss/nitpick/internal/server/auth"
	"github.com/nitpick-oss/nitpick/internal/wire"
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
	o := &options{addr: "127.0.0.1:0", databaseURL: databasetest.URL(t), oidcIssuer: notary.GitHubIssuer}
	if err := o.tokens.Set("ci:authoritative:secret"); err != nil {
		t.Fatal(err)
	}
	return o
}

// A user's hash, cheap, so a test does not pay for the production cost.
const quickUser = "alice:$argon2id$v=19$m=64,t=1,p=1$c2FsdHNhbHRzYWx0c2E$c2FsdHNhbHRzYWx0c2FsdHNhbHRzYWx0c2E\n"

// With no configuration file the server is the notary and nothing else: the same
// routes, no sign-in, so no UI: what the notary command was.
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
	cfg := writeFile(t, dir, "server.yaml", "auth:\n  password:\n    users_file: "+users+"\nroles:\n  - {role: authoritative, provider: password, user: alice}\n", 0o600)
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
		"a role with two selectors":          {"auth:\n  password: {users_file: " + users + "}\nroles:\n  - {role: read, provider: password, user: a, group: g}\n", "exactly one", nil},
		"a role with no selector":            {"auth:\n  password: {users_file: " + users + "}\nroles:\n  - {role: read, provider: password}\n", "exactly one", nil},
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
	o := &options{addr: "x", databaseURL: databasetest.URL(t), oidcIssuer: notary.GitHubIssuer}
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

// A server with no database to keep its state in is refused before it listens,
// and says what to set.
func TestAServerWithNoDatabaseIsRefusedAndSaysWhatToSet(t *testing.T) {
	o := opts(t)
	o.databaseURL = ""
	_, _, err := build(context.Background(), o, quiet)
	if err == nil || !strings.Contains(err.Error(), "-database-url") || !strings.Contains(err.Error(), "NITPICK_DATABASE_URL") {
		t.Errorf("err = %v, want the refusal to name the flag and the variable", err)
	}
	o.databaseURL = "sqlite:///tmp/x.db"
	if _, _, err := build(context.Background(), o, quiet); err == nil || !strings.Contains(err.Error(), "postgres or mysql") {
		t.Errorf("a sqlite URL: err = %v, want it refused", err)
	}
}

// Any number of servers may share one database. The second does not refuse, and
// what one writes the other reads: that is what lets the chart run more than one
// replica.
func TestTwoServersOnOneDatabaseBothStartAndShareTheirRecords(t *testing.T) {
	a := opts(t)
	b := opts(t)
	b.databaseURL = a.databaseURL
	ha, closeA, err := build(context.Background(), a, quiet)
	if err != nil {
		t.Fatal(err)
	}
	defer closeA()
	hb, closeB, err := build(context.Background(), b, quiet)
	if err != nil {
		t.Fatalf("a second server on the same database was refused: %v", err)
	}
	defer closeB()
	tsA, tsB := httptest.NewServer(ha), httptest.NewServer(hb)
	defer tsA.Close()
	defer tsB.Close()

	hash := strings.Repeat("c", 64)
	body := `{"schema":` + strconv.Itoa(wire.Schema) + `,"hash":"` + hash + `","complete":true,"batches":1,"answers":{"r#0":"x"},"model":"m"}`
	put, _ := http.NewRequestWithContext(context.Background(), http.MethodPut, tsA.URL+"/v1/records/"+hash, strings.NewReader(body))
	put.Header.Set("Authorization", "Bearer secret")
	resp, err := http.DefaultClient.Do(put)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		t.Fatalf("PUT through the first server = %d", resp.StatusCode)
	}
	get, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, tsB.URL+"/v1/records/"+hash, nil)
	get.Header.Set("Authorization", "Bearer secret")
	resp, err = http.DefaultClient.Do(get)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(got), hash) {
		t.Errorf("GET through the second server = %d %s, want the record the first wrote", resp.StatusCode, got)
	}
}

// A failed start-up gives the database back: a second attempt is not left to
// contend with the first one's connections or its half-run migration.
func TestAFailedStartUpLeavesTheDatabaseUsableForTheNextAttempt(t *testing.T) {
	o := opts(t)
	dir := t.TempDir()
	users := writeFile(t, dir, "users", quickUser, 0o600)
	// The provider is configured, so config loading passes and the database is
	// opened and migrated. The role is one no component declares, which server.New
	// refuses afterwards: the failure that has to leave the database as it found it.
	o.config = writeFile(t, dir, "server.yaml", "components: [notary]\nauth:\n  password: {users_file: "+users+"}\nroles:\n  - {role: nonsense, provider: password, user: a}\n", 0o600)
	_, _, err := build(context.Background(), o, quiet)
	if err == nil || !strings.Contains(err.Error(), "no component knows the role") {
		t.Fatalf("err = %v, want the refusal that comes after the database was migrated", err)
	}
	o.config = ""
	_, closeStore, err := build(context.Background(), o, quiet)
	if err != nil {
		t.Fatalf("the database was left unusable by the failed start-up: %v", err)
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
			"auth:\n  password: {users_file: "+users+"}\nroles:\n  - {role: '"+role+"', provider: password, user: alice}\n", 0o600)
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
			"auth:\n  password: {users_file: "+users+"}\nroles:\n  - {role: "+role+", provider: password, user: alice}\n", 0o600)
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
