package main

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/http"
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

func FuzzTokenFlag(f *testing.F) {
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
