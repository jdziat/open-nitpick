package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// A second server on the same data directory must refuse to start. Run as a
// real process: the lock is the operating system's, not a variable.
func TestASecondServerOnTheSameDirectoryRefusesToStart(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and starts the server binary")
	}
	bin := filepath.Join(t.TempDir(), "nitpick-notary")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	data := t.TempDir()
	start := func(addr string) *exec.Cmd {
		c := exec.Command(bin, "-addr", addr, "-data", data, "-token", "ci:authoritative:s")
		var stderr bytes.Buffer
		c.Stderr = &stderr
		return c
	}
	first := start("127.0.0.1:0")
	if err := first.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Process.Kill(); _ = first.Wait() }()
	time.Sleep(700 * time.Millisecond)

	// Bounded: a server that wrongly starts would serve for ever, and a guard
	// test that hangs when its guard is broken is a poor way to find out.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	second := exec.CommandContext(ctx, bin, "-addr", "127.0.0.1:0", "-data", data, "-token", "ci:authoritative:s")
	var buf bytes.Buffer
	second.Stderr = &buf
	err := second.Run()
	out := buf.String()
	if ctx.Err() != nil {
		t.Fatal("the second server was still running after 5s: it started on a directory another server holds")
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() == 0 {
		t.Fatalf("the second server started on a held directory: err=%v output=%q", err, out)
	}
	if !strings.Contains(out, "in use") {
		t.Errorf("the refusal does not say why: %q", out)
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
