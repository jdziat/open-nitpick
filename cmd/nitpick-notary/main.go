// Command nitpick-notary serves review records.
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/nitpick-oss/nitpick/internal/notary"
	"github.com/nitpick-oss/nitpick/internal/wire"
)

type tokenFlags []notary.Token

func (t *tokenFlags) String() string { return fmt.Sprint(len(*t)) }

// hashPrefix marks a token whose secret field is already the hex SHA-256.
const hashPrefix = "sha256:"

// Set parses name:role:secret, or name:role:sha256:<hex> for a deployment that
// should not hold the secret at all.
func (t *tokenFlags) Set(v string) error {
	p := strings.SplitN(v, ":", 3)
	if len(p) != 3 || p[0] == "" || p[2] == "" {
		return fmt.Errorf("token must be name:role:secret")
	}
	switch r := notary.Role(p[1]); r {
	case notary.RoleAuthoritative, notary.RoleProvisional, notary.RoleRead:
		if h, ok := strings.CutPrefix(p[2], hashPrefix); ok {
			tok, err := notary.TokenFromHash(p[0], r, h)
			if err != nil {
				return err
			}
			*t = append(*t, tok)
			return nil
		}
		*t = append(*t, notary.NewToken(p[0], r, p[2]))
	default:
		return fmt.Errorf("unknown role %q: use authoritative, provisional or read", p[1])
	}
	return nil
}

type oidcRules []notary.OIDCRule

func (o *oidcRules) String() string { return fmt.Sprint(len(*o)) }

// Set parses role:subject-pattern.
func (o *oidcRules) Set(v string) error {
	r, err := notary.ParseOIDCRule(v)
	if err != nil {
		return err
	}
	*o = append(*o, r)
	return nil
}

func main() {
	var tokens tokenFlags
	var rules oidcRules
	addr := flag.String("addr", "127.0.0.1:8484", "listen address")
	dir := flag.String("data", "notary-data", "directory for records")
	allowClear := flag.Bool("author-clear", false, "let provisional tokens clear records")
	flag.Var(&tokens, "token", "name:role:secret (repeatable); the secret is hashed on load and never stored")
	audience := flag.String("oidc-audience", "", "accept GitHub Actions identity tokens minted for this audience")
	issuer := flag.String("oidc-issuer", notary.GitHubIssuer, "issuer the identity tokens must name")
	jwks := flag.String("oidc-jwks-url", "", "where the issuer publishes its keys (default: issuer/.well-known/jwks)")
	flag.Var(&rules, "oidc-allow", "role:subject-pattern, first match wins, * matches any run (repeatable)")
	healthcheck := flag.Bool("healthcheck", false, "probe /healthz on -addr and exit 0 if the server answers, for a container with no shell")
	hash := flag.Bool("hash", false, "read a secret from stdin, print the sha256:<hex> form -token accepts, and exit")
	flag.Parse()
	if *healthcheck {
		if err := probe(*addr); err != nil {
			fmt.Fprintln(os.Stderr, "nitpick-notary:", err)
			os.Exit(1)
		}
		return
	}
	if *hash {
		secret, err := io.ReadAll(os.Stdin)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(hashPrefix + notary.HashSecret(trimLineEnd(string(secret))))
		return
	}
	// NITPICK_NOTARY_TOKENS carries whitespace-separated -token entries, so a
	// container orchestrator can supply them from a secret and not from argv.
	for _, entry := range strings.Fields(os.Getenv("NITPICK_NOTARY_TOKENS")) {
		if err := tokens.Set(entry); err != nil {
			fmt.Fprintf(os.Stderr, "nitpick-notary: NITPICK_NOTARY_TOKENS: %v\n", err)
			os.Exit(2)
		}
	}
	if (*audience == "") != (len(rules) == 0) {
		fmt.Fprintln(os.Stderr, "nitpick-notary: -oidc-audience and -oidc-allow go together; a token no rule matches is refused")
		os.Exit(2)
	}
	if len(tokens) == 0 && *audience == "" {
		fmt.Fprintln(os.Stderr, "nitpick-notary: give at least one -token or an -oidc-audience with -oidc-allow")
		os.Exit(2)
	}
	store, err := wire.NewFileStore(*dir)
	if err != nil {
		log.Fatal(err)
	}
	// One server per directory: see FileStore.Lock.
	if err := store.Lock(); err != nil {
		log.Fatalf("nitpick-notary: %v (run one server per data directory)", err)
	}
	srvHandler := notary.New(store, tokens, *allowClear)
	if *audience != "" {
		srvHandler.WithOIDC(&notary.OIDC{Issuer: *issuer, Audience: *audience, Allow: rules, JWKSURL: *jwks})
	}
	h := srvHandler
	srv := newServer(*addr, h, defaultLimits)
	log.Printf("nitpick-notary listening on %s, data in %s", *addr, *dir)
	log.Fatal(srv.ListenAndServe())
}

// connLimits bound how long one connection may hold a goroutine and a file
// descriptor. Without them a client with no token can open connections, send
// one request each, and never close them: the server answers every one and then
// waits on them for ever, until it has no descriptors left for anyone else.
type connLimits struct {
	// header is the time to read a request's headers.
	header time.Duration
	// read is the time to read a whole request, body included. A record can be
	// tens of megabytes, so it is generous, and it ends a client that sends the
	// headers and stalls.
	read time.Duration
	// write is the time to answer.
	write time.Duration
	// idle is how long a kept-alive connection may wait for its next request.
	idle time.Duration
}

var defaultLimits = connLimits{header: 10 * time.Second, read: 2 * time.Minute, write: 2 * time.Minute, idle: 60 * time.Second}

// newServer is the one place the HTTP server is configured, so a test can drive
// the limits with real sockets.
func newServer(addr string, h http.Handler, l connLimits) *http.Server {
	return &http.Server{
		Addr: addr, Handler: h,
		ReadHeaderTimeout: l.header, ReadTimeout: l.read, WriteTimeout: l.write, IdleTimeout: l.idle,
		MaxHeaderBytes: 64 << 10,
	}
}

// probe asks a running server whether it is serving. A listen address of ":8484"
// has no host, and the probe runs in the same container, so it asks localhost.
func probe(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("bad -addr %q: %w", addr, err)
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + net.JoinHostPort(host, port) + "/healthz")
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health check answered %s", resp.Status)
	}
	return nil
}

// trimLineEnd drops one trailing line ending, "\n" or "\r\n", so a secret piped
// from echo on any platform hashes as the client will send it. Nothing else is
// trimmed: a space or a second newline is part of the secret.
func trimLineEnd(s string) string {
	if t, ok := strings.CutSuffix(s, "\r\n"); ok {
		return t
	}
	return strings.TrimSuffix(s, "\n")
}
