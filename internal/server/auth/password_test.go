package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
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

	"golang.org/x/crypto/argon2"

	"github.com/nitpick-oss/nitpick/internal/server"
)

// tokenIn reads the CSRF token out of a login page. A page without one fails the
// test at once, instead of slicing at a missing index and handing a wrong token on.
func tokenIn(tb testing.TB, page string) string {
	tb.Helper()
	const marker = `name="csrf_token" value="`
	start := strings.Index(page, marker)
	if start < 0 {
		tb.Fatalf("no csrf token on the page:\n%s", page)
	}
	rest := page[start+len(marker):]
	end := strings.Index(rest, `"`)
	if end < 0 {
		tb.Fatalf("an unterminated csrf token on the page:\n%s", page)
	}
	return rest[:end]
}

// quickHash is a valid argon2id hash with small parameters, so a test does not
// pay 64 MiB a time. The parser accepts any parameters inside its bounds, which
// is also how an operator's older file keeps working.
func quickHash(tb testing.TB, password string) string {
	tb.Helper()
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		tb.Fatal(err)
	}
	key := argon2.IDKey([]byte(password), salt, 1, 64, 1, 32)
	return fmt.Sprintf("$argon2id$v=19$m=64,t=1,p=1$%s$%s",
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key))
}

func usersFile(tb testing.TB, lines ...string) *Users {
	tb.Helper()
	u, err := ParseUsers(strings.NewReader(strings.Join(lines, "\n")))
	if err != nil {
		tb.Fatal(err)
	}
	return u
}

// reply is what a test needs of a response. Each helper reads and closes the body
// itself, so no test holds a response it could forget to close.
type reply struct {
	StatusCode int
	Header     http.Header
}

func replyOf(resp *http.Response) reply {
	return reply{StatusCode: resp.StatusCode, Header: resp.Header}
}

type site struct {
	t   *testing.T
	ts  *httptest.Server
	c   *http.Client
	pw  *Password
	srv *server.Server
}

func builtUI() fstest.MapFS {
	return fstest.MapFS{
		"manifest.json":        {Data: []byte(`{"app.css":"app.aaaaaaaaaaaa.css","app.js":"app.aaaaaaaaaaaa.js"}`)},
		"app.aaaaaaaaaaaa.css": {Data: []byte("x")},
		"app.aaaaaaaaaaaa.js":  {Data: []byte("x")},
	}
}

func newSite(t *testing.T, pw *Password, rules ...server.RoleRule) *site {
	t.Helper()
	roles, err := server.NewRoleMap(rules)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := server.New(server.Config{
		Components: []server.Component{echo{}},
		Providers:  []server.Provider{pw},
		Roles:      roles,
		Assets:     builtUI(),
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
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
	return &site{t: t, ts: ts, c: c, pw: pw, srv: srv}
}

// login posts the form the way a browser would: it reads the page for its
// token first.
func (s *site) login(user, password string) (reply, string) {
	s.t.Helper()
	resp, err := s.c.Get(s.ts.URL + "/auth/login")
	if err != nil {
		s.t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	token := tokenIn(s.t, string(page))
	return s.post(url.Values{"username": {user}, "password": {password}, "csrf_token": {token}, "next": {"/ui/"}})
}

func (s *site) post(form url.Values) (reply, string) {
	s.t.Helper()
	resp, err := s.c.PostForm(s.ts.URL+"/auth/password/login", form)
	if err != nil {
		s.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return replyOf(resp), string(b)
}

func (s *site) signedIn() bool {
	s.t.Helper()
	resp, err := s.c.Get(s.ts.URL + "/ui/")
	if err != nil {
		s.t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func newPassword(t *testing.T) *Password {
	t.Helper()
	return NewPassword(usersFile(t, "alice:"+quickHash(t, "correct horse")+":ops,staff", "bob:"+quickHash(t, "battery staple")), slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestACorrectPasswordSignsInAndGrantsTheRolesTheRulesGive(t *testing.T) {
	s := newSite(t, newPassword(t), server.RoleRule{Roles: []server.Role{"admin"}, Provider: "password", Group: "ops"})
	resp, body := s.login("Alice", "correct horse") // the account name is not case-sensitive
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/ui/" {
		t.Fatalf("login = %d %q to %q", resp.StatusCode, body, resp.Header.Get("Location"))
	}
	if !s.signedIn() {
		t.Error("the session from a correct login does not work")
	}
}

// A wrong password, an unknown account and a locked account are one response.
func TestEveryRefusalSaysTheSameThing(t *testing.T) {
	s := newSite(t, newPassword(t))
	_, wrongPassword := s.login("alice", "nope")
	_, unknownAccount := s.login("nobody", "nope")
	if !strings.Contains(wrongPassword, refused) || !strings.Contains(unknownAccount, refused) {
		t.Fatalf("refusals do not carry the shared text:\n%s\n%s", wrongPassword, unknownAccount)
	}
	// What differs between the pages is the CSRF token and nothing about the
	// account. Strip the token and the two bodies are the same.
	norm := func(s string) string {
		token := tokenIn(t, s)
		return strings.Replace(s, token, "TOKEN", 1)
	}
	if norm(wrongPassword) != norm(unknownAccount) {
		t.Error("an unknown account and a wrong password are answered differently, which says which names exist")
	}
	if strings.Contains(wrongPassword, "alice") || strings.Contains(unknownAccount, "nobody") {
		t.Error("the refusal names the account")
	}
	resp, _ := s.login("alice", "nope")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("a refused sign-in = %d, want 401", resp.StatusCode)
	}
}

// Rule 9: repeated failures lock the account, and removing the limiter lets the
// guesses through.
func TestRepeatedFailuresLockTheAccount(t *testing.T) {
	pw := newPassword(t)
	s := newSite(t, pw)
	for i := 0; i < 5; i++ {
		if resp, _ := s.login("alice", fmt.Sprintf("guess %d", i)); resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("guess %d = %d, want 401 while counting", i, resp.StatusCode)
		}
	}
	// Locked: even the right password is refused, with the lockout status.
	resp, body := s.login("alice", "correct horse")
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("the correct password while locked = %d, want 429", resp.StatusCode)
	}
	if !strings.Contains(body, refused) {
		t.Errorf("the lockout says something other than the shared refusal: %q", body)
	}
	if s.signedIn() {
		t.Error("a locked account signed in")
	}
	// Rule 10: another account is not locked by it, so the refusal is alice's.
	if resp, _ := s.login("bob", "battery staple"); resp.StatusCode != http.StatusSeeOther {
		t.Errorf("another account while alice is locked = %d, want a sign-in", resp.StatusCode)
	}
}

func TestAnAddressThatKeepsFailingIsLockedWhateverTheAccount(t *testing.T) {
	pw := newPassword(t)
	pw.ByAddress = &Limiter{Max: 3, Window: time.Hour, Lockout: time.Hour, Keys: 100}
	s := newSite(t, pw)
	for i := 0; i < 3; i++ {
		s.login(fmt.Sprintf("nobody%d", i), "x") // a different account each time
	}
	if resp, _ := s.login("bob", "battery staple"); resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("a correct login from a locked address = %d, want 429", resp.StatusCode)
	}
}

// A lockout ends, and a success clears the count.
func TestALockoutEndsAndASuccessClearsTheCount(t *testing.T) {
	now := time.Now()
	l := &Limiter{Max: 3, Window: 10 * time.Minute, Lockout: 20 * time.Minute, Keys: 10, now: func() time.Time { return now }}
	for i := 0; i < 3; i++ {
		l.Fail("k")
	}
	if ok, wait := l.Allowed("k"); ok || wait <= 0 {
		t.Fatalf("a key at the limit = %v %v, want locked", ok, wait)
	}
	now = now.Add(19 * time.Minute)
	if ok, _ := l.Allowed("k"); ok {
		t.Error("the lockout ended early")
	}
	now = now.Add(2 * time.Minute)
	if ok, _ := l.Allowed("k"); !ok {
		t.Error("the lockout did not end")
	}
	// Failures older than the window do not count.
	l.Fail("w")
	l.Fail("w")
	now = now.Add(11 * time.Minute)
	if l.Fail("w") {
		t.Error("failures older than the window still counted toward a lock")
	}
	// A success clears the count.
	l.Fail("s")
	l.Fail("s")
	l.Success("s")
	if l.Fail("s") {
		t.Error("a success did not clear the count")
	}
	// A correct guess does not lift a lockout that is running.
	for i := 0; i < 3; i++ {
		l.Fail("locked")
	}
	l.Success("locked")
	if ok, _ := l.Allowed("locked"); ok {
		t.Error("a success lifted a running lockout")
	}
}

func TestTheLimiterTableDoesNotGrowWithoutBound(t *testing.T) {
	l := &Limiter{Max: 5, Window: time.Hour, Lockout: time.Hour, Keys: 100}
	for i := 0; i < 5000; i++ {
		l.Fail(fmt.Sprintf("name-%d", i))
	}
	if l.Len() > 100 {
		t.Errorf("the table holds %d keys, want at most 100", l.Len())
	}
}

// The form is protected by its own CSRF token, before the account is touched.
func TestALoginPostWithoutTheFormsTokenIsRefusedBeforeCountingAFailure(t *testing.T) {
	pw := newPassword(t)
	s := newSite(t, pw)
	resp, _ := s.c.Get(s.ts.URL + "/auth/login") // gets the cookie
	_ = resp.Body.Close()
	for name, form := range map[string]url.Values{
		"no token":       {"username": {"alice"}, "password": {"nope"}},
		"a forged token": {"username": {"alice"}, "password": {"nope"}, "csrf_token": {"forged"}},
	} {
		if r, _ := s.post(form); r.StatusCode != http.StatusForbidden {
			t.Errorf("%s = %d, want 403", name, r.StatusCode)
		}
	}
	if pw.ByAccount.Len() != 0 || pw.ByAddress.Len() != 0 {
		t.Error("a request with no valid token counted as a failed sign-in")
	}
	// A token for another browser's cookie does not work: the form served to one
	// browser is no use to another.
	other := newSite(t, newPassword(t))
	r1, _ := s.c.Get(s.ts.URL + "/auth/login")
	page, _ := io.ReadAll(r1.Body)
	_ = r1.Body.Close()
	tok := tokenIn(t, string(page))
	r2, _ := other.c.Get(other.ts.URL + "/auth/login") // the other site's cookie
	_ = r2.Body.Close()
	if r, _ := other.post(url.Values{"username": {"alice"}, "password": {"correct horse"}, "csrf_token": {tok}}); r.StatusCode != http.StatusForbidden {
		t.Errorf("a token from another browser = %d, want 403", r.StatusCode)
	}
}

func TestOversizedCredentialsAreRefusedWithoutHashing(t *testing.T) {
	pw := newPassword(t)
	s := newSite(t, pw)
	if resp, _ := s.login("alice", strings.Repeat("x", 5000)); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("a huge password = %d, want 401", resp.StatusCode)
	}
	if resp, _ := s.login(strings.Repeat("a", 500), "x"); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("a huge name = %d, want 401", resp.StatusCode)
	}
	if resp, _ := s.login("", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("an empty form = %d, want 401", resp.StatusCode)
	}
	if resp, _ := s.login("  ", "x"); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("a blank name = %d, want 401", resp.StatusCode)
	}
	if resp, _ := s.login("alice", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("an empty password = %d, want 401", resp.StatusCode)
	}
	// None of these counted against an account: the account name tracked is the
	// one a real guess would carry.
	if pw.ByAccount.Len() != 0 {
		t.Errorf("malformed requests were counted as failures: %d keys", pw.ByAccount.Len())
	}
}

// X-Forwarded-For is not believed unless the operator said a proxy sets it.
func TestTheForwardedHeaderIsIgnoredUnlessTrusted(t *testing.T) {
	for _, trust := range []bool{false, true} {
		pw := newPassword(t)
		pw.TrustForwardedFor = trust
		pw.ByAddress = &Limiter{Max: 2, Window: time.Hour, Lockout: time.Hour, Keys: 100}
		s := newSite(t, pw)
		for i := 0; i < 2; i++ {
			req, _ := http.NewRequest("POST", s.ts.URL+"/auth/password/login", nil)
			_ = req
			// The client chooses a different forwarded address for each guess.
			resp, _ := s.c.Get(s.ts.URL + "/auth/login")
			page, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			tok := tokenIn(t, string(page))
			form := url.Values{"username": {"nobody"}, "password": {"x"}, "csrf_token": {tok}}
			r, _ := http.NewRequest("POST", s.ts.URL+"/auth/password/login", strings.NewReader(form.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r.Header.Set("X-Forwarded-For", fmt.Sprintf("203.0.113.%d", i+1))
			if rr, err := s.c.Do(r); err == nil {
				_ = rr.Body.Close()
			}
		}
		allowed, _ := pw.ByAddress.Allowed("address:127.0.0.1")
		if trust && !allowed {
			t.Error("trusted: the forwarded addresses were all treated as the proxy's")
		}
		if !trust && allowed {
			t.Error("untrusted: spoofed forwarded addresses let the client dodge the address limit")
		}
	}
}

func TestParsingAUsersFile(t *testing.T) {
	good := quickHash(t, "p")
	if u, err := ParseUsers(strings.NewReader("# staff\n\nalice:" + good + ":ops, staff\nBob:" + good + "\n")); err != nil || u.Len() != 2 {
		t.Fatalf("a good file = %v, %v", u, err)
	}
	for name, file := range map[string]string{
		"empty":               "",
		"only comments":       "# nothing\n",
		"a plain password":    "alice:hunter2\n",
		"bcrypt":              "alice:$2a$10$abcdefghijklmnopqrstuuABCDEFGHIJKLMNOPQRSTUVWXYZ012345\n",
		"argon2i, not id":     "alice:" + strings.Replace(good, "argon2id", "argon2i", 1) + "\n",
		"no name":             ":" + good + "\n",
		"a name with a space": "al ice:" + good + "\n",
		"no colon":            "alice\n",
		"a duplicate":         "alice:" + good + "\nALICE:" + good + "\n",
		"a short salt":        "alice:$argon2id$v=19$m=64,t=1,p=1$YQ$" + strings.Split(good, "$")[5] + "\n",
		"a short hash":        "alice:$argon2id$v=19$m=64,t=1,p=1$" + strings.Split(good, "$")[4] + "$YQ\n",
		"too much memory":     "alice:" + strings.Replace(good, "m=64", "m=9999999", 1) + "\n",
		"too many passes":     "alice:" + strings.Replace(good, "t=1", "t=999", 1) + "\n",
		"zero parallelism":    "alice:" + strings.Replace(good, "p=1", "p=0", 1) + "\n",
		"another version":     "alice:" + strings.Replace(good, "v=19", "v=16", 1) + "\n",
		"a missing parameter": "alice:" + strings.Replace(good, "m=64,", "", 1) + "\n",
	} {
		if _, err := ParseUsers(strings.NewReader(file)); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	// A refused line does not copy the secret into its error.
	_, err := ParseUsers(strings.NewReader("alice:hunter2-the-secret\n"))
	if err == nil || strings.Contains(err.Error(), "hunter2") {
		t.Errorf("the error for a plain password = %v, want it not to repeat the password", err)
	}
}

func TestVerifyingPasswordsAcceptsOnlyTheRightOne(t *testing.T) {
	u := usersFile(t, "alice:"+quickHash(t, "right")+":g1,g2")
	if name, groups, ok := u.Verify("ALICE", "right"); !ok || name != "alice" || len(groups) != 2 {
		t.Errorf("a right password = %q %v %v", name, groups, ok)
	}
	for _, c := range [][2]string{{"alice", "wrong"}, {"alice", ""}, {"alice", "right "}, {"nobody", "right"}, {"", "right"}} {
		if _, _, ok := u.Verify(c[0], c[1]); ok {
			t.Errorf("Verify(%q, %q) succeeded", c[0], c[1])
		}
	}
}

// A hash written by HashPassword, with the parameters new hashes use, verifies.
// One test pays for the full cost.
func TestAHashPasswordHashVerifies(t *testing.T) {
	if testing.Short() {
		t.Skip("argon2id at the production cost")
	}
	h, err := HashPassword("real password")
	if err != nil {
		t.Fatal(err)
	}
	u := usersFile(t, "carol:"+h)
	if _, _, ok := u.Verify("carol", "real password"); !ok {
		t.Error("a password hashed at the default cost did not verify")
	}
	if _, _, ok := u.Verify("carol", "other"); ok {
		t.Error("the wrong password verified")
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=65536,t=3,p=4$") {
		t.Errorf("hash parameters = %q, want the documented defaults", h)
	}
}

// A correct sign-in forgets the failures before it, so a person who mistyped
// four times and then got it right is not one miss from a lock.
func TestASuccessfulSignInClearsEarlierFailures(t *testing.T) {
	pw := newPassword(t)
	pw.ByAddress = &Limiter{Max: 1000, Window: time.Hour, Lockout: time.Hour, Keys: 100}
	s := newSite(t, pw)
	for i := 0; i < 4; i++ {
		s.login("alice", "wrong")
	}
	if resp, _ := s.login("alice", "correct horse"); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("the correct password after four misses = %d, want a sign-in", resp.StatusCode)
	}
	jar, _ := cookiejar.New(nil)
	s.c.Jar = jar // a fresh browser, so the login page is shown again
	for i := 0; i < 4; i++ {
		if resp, _ := s.login("alice", "wrong"); resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("miss %d after the success = %d, want 401: the earlier four should have been forgotten", i+1, resp.StatusCode)
		}
	}
}

// The destination after a sign-in is the page that was asked for.
func TestSignInReturnsToTheRequestedPage(t *testing.T) {
	s := newSite(t, newPassword(t))
	resp, err := s.c.Get(s.ts.URL + "/auth/login?next=%2Fui%2Fsomewhere")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	tok := tokenIn(t, string(page))
	if !strings.Contains(string(page), `name="next" value="/ui/somewhere"`) {
		t.Fatalf("the login page did not carry the requested page:\n%s", page)
	}
	r, _ := s.post(url.Values{"username": {"alice"}, "password": {"correct horse"}, "csrf_token": {tok}, "next": {"/ui/somewhere"}})
	if r.Header.Get("Location") != "/ui/somewhere" {
		t.Errorf("sign-in went to %q, want /ui/somewhere", r.Header.Get("Location"))
	}
}

// The groups an account is in reach the identity, and so the role rules.
func TestAnAccountsGroupsReachTheRoleRules(t *testing.T) {
	s := newSite(t, newPassword(t), server.RoleRule{Roles: []server.Role{"admin"}, Provider: "password", Group: "ops"})
	s.login("alice", "correct horse")
	id := lastIdentity(t, s)
	if !id.Has("admin") {
		t.Errorf("alice is in ops, and the roles she holds are %v", id.Roles)
	}
	// Rule 10: bob is in no group and holds nothing, so the above is the group's doing.
	other := newSite(t, newPassword(t), server.RoleRule{Roles: []server.Role{"admin"}, Provider: "password", Group: "ops"})
	other.login("bob", "battery staple")
	if got := lastIdentity(t, other); len(got.Roles) != 0 {
		t.Errorf("bob holds %v", got.Roles)
	}
}

// echo is a component whose one route says who the session is, so a test can see
// the identity a provider produced and the roles the server granted it.
type echo struct{}

func (echo) Name() string                 { return "echo" }
func (echo) Prefixes() []string           { return []string{"/v1/echo/"} }
func (echo) Pages() []server.Page         { return nil }
func (echo) Health(context.Context) error { return nil }
func (echo) Routes() []server.Route {
	return []server.Route{{Pattern: "GET /v1/echo/who", Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := server.IdentityFrom(r.Context())
		if !ok {
			http.Error(w, "none", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(id)
	})}}
}

// lastIdentity reads the signed-in identity through the echo route.
func lastIdentity(t *testing.T, s *site) server.Identity {
	t.Helper()
	resp, err := s.c.Get(s.ts.URL + "/v1/echo/who")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var id server.Identity
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the echo route = %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// With a trusted proxy, the address is the one the proxy added, which is the last
// entry. Earlier ones are what the client claimed, and a client can write them.
func TestTheForwardedAddressIsTheLastEntry(t *testing.T) {
	pw := newPassword(t)
	pw.TrustForwardedFor = true
	r, _ := http.NewRequest("POST", "https://x/auth/password/login", nil)
	r.RemoteAddr = "10.0.0.1:5555"
	r.Header.Set("X-Forwarded-For", "6.6.6.6, 203.0.113.9")
	if got := pw.address(r); got != "203.0.113.9" {
		t.Errorf("address = %q, want the proxy-added last entry", got)
	}
	r.Header.Set("X-Forwarded-For", "")
	if got := pw.address(r); got != "10.0.0.1" {
		t.Errorf("with no header the address = %q, want the connection's", got)
	}
	pw.TrustForwardedFor = false
	r.Header.Set("X-Forwarded-For", "6.6.6.6")
	if got := pw.address(r); got != "10.0.0.1" {
		t.Errorf("untrusted, the address = %q, want the connection's", got)
	}
}

// An unknown name costs what a wrong password costs: the response time does not
// say which names exist. The accounts here use a cost that makes the hash
// dominate everything else the request does.
func TestAnUnknownNameCostsWhatAWrongPasswordCosts(t *testing.T) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		t.Fatal(err)
	}
	key := argon2.IDKey([]byte("right"), salt, 3, 8192, 1, 32)
	hash := fmt.Sprintf("$argon2id$v=19$m=8192,t=3,p=1$%s$%s", base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key))
	u := usersFile(t, "alice:"+hash)

	best := func(name string) time.Duration {
		d := time.Hour
		for i := 0; i < 5; i++ {
			start := time.Now()
			u.Verify(name, "wrong")
			if e := time.Since(start); e < d {
				d = e
			}
		}
		return d
	}
	known, unknown := best("alice"), best("nobody")
	if unknown < known/2 {
		t.Errorf("an unknown name took %v against %v for a wrong password: the difference says which names exist", unknown, known)
	}
}

// tokenIn must fail the test when the page has no token, and must return exactly
// the token when it has one. A helper that slices at a missing index would hand a
// wrong token to a test that then passes or fails for a reason it did not intend.
func TestTokenInReadsTheTokenAndFailsLoudlyWithout(t *testing.T) {
	if got := tokenIn(t, `<input name="csrf_token" value="abc-123_x"><input name="next" value="/ui/">`); got != "abc-123_x" {
		t.Errorf("tokenIn = %q, want abc-123_x", got)
	}
	for name, page := range map[string]string{
		"no marker":    `<form><input name="user"></form>`,
		"unterminated": `<input name="csrf_token" value="abc`,
		"empty page":   ``,
	} {
		rec := &recordingTB{}
		func() {
			defer func() { _ = recover() }()
			tokenIn(rec, page)
		}()
		if !rec.failed {
			t.Errorf("%s: tokenIn did not fail the test", name)
		}
	}
}

// recordingTB is a testing.TB that notes whether it was told to stop, and stops by
// panicking, as Fatalf does by leaving the goroutine.
type recordingTB struct {
	testing.TB
	failed bool
}

func (r *recordingTB) Helper() {}
func (r *recordingTB) Fatalf(string, ...any) {
	r.failed = true
	panic("stop")
}
