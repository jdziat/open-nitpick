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

func main() {
	var tokens tokenFlags
	addr := flag.String("addr", "127.0.0.1:8484", "listen address")
	dir := flag.String("data", "notary-data", "directory for records")
	allowClear := flag.Bool("author-clear", false, "let provisional tokens clear records")
	flag.Var(&tokens, "token", "name:role:secret (repeatable)")
	flag.Parse()
	if len(tokens) == 0 {
		fmt.Fprintln(os.Stderr, "nitpick-notary: give at least one -token")
		os.Exit(2)
	}
	store, err := wire.NewFileStore(*dir)
	if err != nil {
		log.Fatal(err)
	}
	h := notary.New(store, tokens, *allowClear)
	srv := &http.Server{Addr: *addr, Handler: h, ReadHeaderTimeout: 10 * time.Second}
	log.Printf("nitpick-notary listening on %s, data in %s", *addr, *dir)
	log.Fatal(srv.ListenAndServe())
}
