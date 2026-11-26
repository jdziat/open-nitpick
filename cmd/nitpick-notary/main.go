// Command nitpick-notary serves review records.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/nitpick-oss/nitpick/internal/notary"
	"github.com/nitpick-oss/nitpick/internal/wire"
)

type tokenFlags []notary.Token

func (t *tokenFlags) String() string { return fmt.Sprint(len(*t)) }

// Set parses name:role:secret.
func (t *tokenFlags) Set(v string) error {
	p := strings.SplitN(v, ":", 3)
	if len(p) != 3 || p[0] == "" || p[2] == "" {
		return fmt.Errorf("token must be name:role:secret")
	}
	switch r := notary.Role(p[1]); r {
	case notary.RoleAuthoritative, notary.RoleProvisional, notary.RoleRead:
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
	flag.Parse()
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
	srvHandler := notary.New(store, tokens, *allowClear)
	if *audience != "" {
		srvHandler.WithOIDC(&notary.OIDC{Issuer: *issuer, Audience: *audience, Allow: rules, JWKSURL: *jwks})
	}
	h := srvHandler
	srv := &http.Server{Addr: *addr, Handler: h, ReadHeaderTimeout: 10 * time.Second}
	log.Printf("nitpick-notary listening on %s, data in %s", *addr, *dir)
	log.Fatal(srv.ListenAndServe())
}
