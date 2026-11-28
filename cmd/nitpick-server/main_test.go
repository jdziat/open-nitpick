package main

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nitpick-oss/nitpick/internal/database/databasetest"
	"github.com/nitpick-oss/nitpick/internal/notary"
	"github.com/nitpick-oss/nitpick/internal/wire"
)

func notaryOn(t *testing.T, limits connLimits) (addr string, token string) {
	t.Helper()
	store, err := wire.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := notary.New(store, []notary.Token{notary.NewToken("ci", notary.RoleAuthoritative, "s3cret")}, false)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := newServer(ln.Addr().String(), h, limits)
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return ln.Addr().String(), "s3cret"
}

// closedByServer reports whether the server closes conn within wait, which is
// how a connection that has been given up on looks from the client.
func closedByServer(conn net.Conn, wait time.Duration) bool {
	_ = conn.SetReadDeadline(time.Now().Add(wait))
	buf := make([]byte, 1)
	_, err := conn.Read(buf)
	if err == nil {
		return false
	}
	var ne net.Error
	return !errors.As(err, &ne) || !ne.Timeout()
}

var tight = connLimits{header: 150 * time.Millisecond, read: 400 * time.Millisecond, write: 400 * time.Millisecond, idle: 300 * time.Millisecond}

func TestAnIdleKeepAliveConnectionIsClosedByTheServer(t *testing.T) {
	// A long read limit: net/http falls back to ReadTimeout when IdleTimeout is
	// zero, so with both short a missing idle limit would go unnoticed.
	addr, _ := notaryOn(t, connLimits{header: 150 * time.Millisecond, read: time.Minute, write: time.Minute, idle: 300 * time.Millisecond})
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	// One answered request, no token needed to get a 401, then silence.
	_, _ = io.WriteString(conn, "GET /v1/records/"+strings.Repeat("a", 64)+" HTTP/1.1\r\nHost: x\r\n\r\n")
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if !closedByServer(conn, 2*time.Second) {
		t.Fatal("an idle connection was still open 2s later with a 300ms idle limit: a client that never hangs up holds a descriptor for ever")
	}
}

func TestAClientThatSendsHeadersAndStallsTheBodyIsCutOff(t *testing.T) {
	addr, token := notaryOn(t, tight)
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	// Authenticated, so the server accepts the request and waits for the body.
	_, _ = io.WriteString(conn, "PUT /v1/records/"+strings.Repeat("a", 64)+" HTTP/1.1\r\nHost: x\r\nAuthorization: Bearer "+token+"\r\nContent-Length: 100000\r\n\r\n{\"sch")
	if !closedByServer(conn, 2*time.Second) {
		t.Fatal("a stalled request body held its connection open past the read limit")
	}
}

func TestTheLimitsDoNotRefuseAnOrdinaryRequest(t *testing.T) {
	addr, token := notaryOn(t, tight)
	req, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/v1/records/"+strings.Repeat("a", 64), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (authenticated, no such record)", resp.StatusCode)
	}
}

func FuzzTokenFlagAcceptsOnlyUsableTokens(f *testing.F) {
	h := strings.Repeat("ab", 32)
	for _, s := range []string{"ci:authoritative:x", "ci:read:sha256:" + h, "a:b:c:d:e", ":::", "ci:read:sha256:", "\x00:read:x", "ci:read:" + strings.Repeat("é", 100)} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		var tokens tokenFlags
		if err := tokens.Set(s); err != nil {
			if len(tokens) != 0 {
				t.Fatalf("%q was refused but still added a token", s)
			}
			return
		}
		if len(tokens) != 1 || tokens[0].Name == "" {
			t.Fatalf("%q accepted as %+v", s, tokens)
		}
		switch tokens[0].Role {
		case notary.RoleAuthoritative, notary.RoleProvisional, notary.RoleRead:
		default:
			t.Fatalf("%q accepted with role %q", s, tokens[0].Role)
		}
	})
}

// Two server processes on one database both serve, and a record one writes the
// other reads. Run as real processes, since the point is that nothing in either
// process, a lock or a cache, stands between them.
func TestTwoServerProcessesOnOneDatabaseShareTheirRecords(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and starts the server binary")
	}
	bin := filepath.Join(t.TempDir(), "nitpick-server")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	url := databasetest.URL(t)
	free := func() string {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = ln.Close() }()
		return ln.Addr().String()
	}
	start := func(addr string) {
		c := exec.Command(bin, "-addr", addr, "-database-url", url, "-token", "ci:authoritative:s")
		var stderr bytes.Buffer
		c.Stderr = &stderr
		if err := c.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Process.Kill(); _ = c.Wait() })
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			if resp, err := http.Get("http://" + addr + "/healthz"); err == nil {
				_ = resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					return
				}
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatalf("the server on %s did not become healthy; stderr: %s", addr, stderr.String())
	}
	first, second := free(), free()
	start(first)
	start(second)

	hash := strings.Repeat("d", 64)
	body := `{"schema":` + strconv.Itoa(wire.Schema) + `,"hash":"` + hash + `","complete":true,"batches":1,"answers":{"r#0":"x"}}`
	put, _ := http.NewRequest(http.MethodPut, "http://"+first+"/v1/records/"+hash, strings.NewReader(body))
	put.Header.Set("Authorization", "Bearer s")
	resp, err := http.DefaultClient.Do(put)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		t.Fatalf("PUT to the first process = %d", resp.StatusCode)
	}
	get, _ := http.NewRequest(http.MethodGet, "http://"+second+"/v1/records/"+hash, nil)
	get.Header.Set("Authorization", "Bearer s")
	resp, err = http.DefaultClient.Do(get)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(got), hash) {
		t.Errorf("GET from the second process = %d %s", resp.StatusCode, got)
	}
}

func TestATokenFlagTakesASecretOrItsHashAndNothingElse(t *testing.T) {
	var tokens tokenFlags
	hash := strings.Repeat("ab", 32)
	for _, ok := range []string{"ci:authoritative:plain", "dev:provisional:sha256:" + hash, "v:read:sha256:" + hash} {
		if err := tokens.Set(ok); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
	if len(tokens) != 3 {
		t.Fatalf("tokens = %d, want 3", len(tokens))
	}
	for _, bad := range []string{
		"", "ci", "ci:authoritative", "ci:authoritative:", ":read:x", "ci:root:x",
		"ci:read:sha256:", "ci:read:sha256:short", "ci:read:sha256:" + strings.Repeat("z", 64),
	} {
		if err := (&tokenFlags{}).Set(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

func TestTheHealthProbeSucceedsOnlyAgainstAServerThatAnswersOK(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
	t.Cleanup(up.Close)
	if err := probe(strings.TrimPrefix(up.URL, "http://")); err != nil {
		t.Errorf("a serving server failed the probe: %v", err)
	}

	sick := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "no", http.StatusServiceUnavailable) }))
	t.Cleanup(sick.Close)
	if err := probe(strings.TrimPrefix(sick.URL, "http://")); err == nil {
		t.Error("a server answering 503 passed the probe")
	}

	down := httptest.NewServer(http.NotFoundHandler())
	addr := strings.TrimPrefix(down.URL, "http://")
	down.Close()
	if err := probe(addr); err == nil {
		t.Error("a closed port passed the probe")
	}
	if err := probe("no-port"); err == nil {
		t.Error("an address with no port passed the probe")
	}
}

func TestAPipedSecretLosesOneLineEndingAndNothingElse(t *testing.T) {
	for in, want := range map[string]string{
		"secret":         "secret",
		"secret\n":       "secret",
		"secret\r\n":     "secret",
		"secret\n\n":     "secret\n",
		"secret\r\n\r\n": "secret\r\n",
		"secret ":        "secret ",
		" secret":        " secret",
		"secret\r":       "secret\r",
		"":               "",
		"\n":             "",
		"\r\n":           "",
	} {
		if got := trimLineEnd(in); got != want {
			t.Errorf("trimLineEnd(%q) = %q, want %q", in, got, want)
		}
	}
}
