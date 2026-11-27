// Command nitpick-server hosts nitpick's hosted components behind one listener,
// one sign-in and one web interface. The notary is the first component.
//
// It replaces nitpick-server: the flags, the NITPICK_NOTARY_TOKENS variable and
// the data directory are the same, so a deployment changes its binary name and
// nothing else. Without a -config nothing new is switched on.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/nitpick-oss/nitpick/internal/notary"
	"github.com/nitpick-oss/nitpick/internal/server"
	"github.com/nitpick-oss/nitpick/internal/server/auth"
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
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// options are the flags. They are a struct so a test can build a server from them
// without a process.
type options struct {
	addr, dir, config, oidcAudience, oidcIssuer, oidcJWKS string
	allowClear                                            bool
	tokens                                                tokenFlags
	rules                                                 oidcRules
}

// parseFlags reads the command line. The notary's flags are the ones nitpick-notary
// had, with the same names and meanings.
func parseFlags(args []string, stderr io.Writer) (*options, *flag.FlagSet, error) {
	o := &options{}
	fs := flag.NewFlagSet("nitpick-server", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&o.addr, "addr", "127.0.0.1:8484", "listen address")
	fs.StringVar(&o.dir, "data", "notary-data", "directory for records")
	fs.BoolVar(&o.allowClear, "author-clear", false, "let provisional tokens clear records")
	fs.Var(&o.tokens, "token", "name:role:secret (repeatable); the secret is hashed on load and never stored")
	fs.StringVar(&o.oidcAudience, "oidc-audience", "", "accept GitHub Actions identity tokens minted for this audience")
	fs.StringVar(&o.oidcIssuer, "oidc-issuer", notary.GitHubIssuer, "issuer the identity tokens must name")
	fs.StringVar(&o.oidcJWKS, "oidc-jwks-url", "", "where the issuer publishes its keys (default: issuer/.well-known/jwks)")
	fs.Var(&o.rules, "oidc-allow", "role:subject-pattern, first match wins, * matches any run (repeatable)")
	fs.StringVar(&o.config, "config", os.Getenv("NITPICK_SERVER_CONFIG"), "configuration file: components, sign-in providers and roles (default $NITPICK_SERVER_CONFIG)")
	return o, fs, nil
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	// The modes that do something else and exit are read before the rest.
	for _, a := range args {
		if a == "-healthcheck" || a == "--healthcheck" {
			return healthcheck(args, stderr)
		}
	}
	var mode struct{ hash, hashPassword bool }
	flagsOnly := make([]string, 0, len(args))
	for _, a := range args {
		switch a {
		case "-hash", "--hash":
			mode.hash = true
		case "-hash-password", "--hash-password":
			mode.hashPassword = true
		default:
			flagsOnly = append(flagsOnly, a)
		}
	}
	if mode.hash || mode.hashPassword {
		secret, err := io.ReadAll(io.LimitReader(stdin, 1<<20))
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "nitpick-server:", err)
			return 1
		}
		text := trimLineEnd(string(secret))
		if mode.hash {
			_, _ = fmt.Fprintln(stdout, hashPrefix+notary.HashSecret(text))
			return 0
		}
		h, err := auth.HashPassword(text)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "nitpick-server:", err)
			return 1
		}
		_, _ = fmt.Fprintln(stdout, h)
		return 0
	}

	o, fs, _ := parseFlags(flagsOnly, stderr)
	if err := fs.Parse(flagsOnly); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	// NITPICK_NOTARY_TOKENS carries whitespace-separated -token entries, so a
	// container orchestrator can supply them from a secret and not from argv.
	for _, entry := range strings.Fields(os.Getenv("NITPICK_NOTARY_TOKENS")) {
		if err := o.tokens.Set(entry); err != nil {
			_, _ = fmt.Fprintf(stderr, "nitpick-server: NITPICK_NOTARY_TOKENS: %v\n", err)
			return 2
		}
	}

	log := slog.New(slog.NewTextHandler(stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	h, closeStore, err := build(ctx, o, log)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "nitpick-server:", err)
		return 2
	}
	defer closeStore()

	srv := newServer(o.addr, h, defaultLimits)
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("nitpick-server listening", "addr", o.addr, "data", o.dir)
	select {
	case err := <-errc:
		_, _ = fmt.Fprintln(stderr, "nitpick-server:", err)
		return 1
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
		return 0
	}
}

// healthcheck is -healthcheck: probe /healthz on -addr and exit 0 if the server
// answers, for a container with no shell.
func healthcheck(args []string, stderr io.Writer) int {
	addr := "127.0.0.1:8484"
	for i, a := range args {
		if (a == "-addr" || a == "--addr") && i+1 < len(args) {
			addr = args[i+1]
		}
		if v, ok := strings.CutPrefix(a, "-addr="); ok {
			addr = v
		}
	}
	if err := probe(addr); err != nil {
		_, _ = fmt.Fprintln(stderr, "nitpick-server:", err)
		return 1
	}
	return 0
}

// build assembles the server from the options and the configuration file, and
// returns what to call to release the data directory. Every check that can fail
// before the first request does so here.
func build(ctx context.Context, o *options, log *slog.Logger) (http.Handler, func(), error) {
	cfg, err := loadConfig(o.config)
	if err != nil {
		return nil, nil, err
	}
	providers, err := cfg.providers(ctx, log)
	if err != nil {
		return nil, nil, err
	}

	if (o.oidcAudience == "") != (len(o.rules) == 0) {
		return nil, nil, errors.New("-oidc-audience and -oidc-allow go together; a token no rule matches is refused")
	}
	var components []server.Component
	closeStore := func() {}
	for _, name := range cfg.components() {
		switch name {
		case "notary":
			// A notary no machine can write to and no person can open is not a
			// notary that was meant to be started.
			if len(o.tokens) == 0 && o.oidcAudience == "" && len(providers) == 0 {
				return nil, nil, errors.New("give at least one -token or an -oidc-audience with -oidc-allow, or configure a sign-in")
			}
			store, err := wire.NewFileStore(o.dir)
			if err != nil {
				return nil, nil, err
			}
			// One server per directory: see FileStore.Lock.
			if err := store.Lock(); err != nil {
				return nil, nil, fmt.Errorf("%w (run one server per data directory)", err)
			}
			closeStore = func() { _ = store.Unlock() }
			c := notary.NewComponent(store, o.tokens, o.allowClear)
			if o.oidcAudience != "" {
				c.WithOIDC(&notary.OIDC{Issuer: o.oidcIssuer, Audience: o.oidcAudience, Allow: o.rules, JWKSURL: o.oidcJWKS})
			}
			components = append(components, c)
		}
	}

	roles, err := server.NewRoleMap(cfg.roleRules())
	if err != nil {
		closeStore()
		return nil, nil, err
	}
	srv, err := server.New(server.Config{
		Components: components, Providers: providers, Roles: roles,
		Sessions: server.SessionConfig{Idle: cfg.Session.Idle, Absolute: cfg.Session.Absolute},
		Logger:   log,
	})
	if err != nil {
		closeStore()
		return nil, nil, err
	}
	return srv, closeStore, nil
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
	read  time.Duration
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
