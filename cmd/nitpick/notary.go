package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/nitpick-oss/nitpick/internal/review"
	"github.com/nitpick-oss/nitpick/internal/wire"
)

// EnvNotaryToken carries the notary credential, so it never appears in argv or
// a process listing.
const EnvNotaryToken = "NITPICK_NOTARY_TOKEN"

// notaryFlags are the client side of the notary: where it is, who is asking,
// and whether to ignore what it already holds.
type notaryFlags struct {
	url          string
	oidcAudience string
	clearBy      string
	fresh        bool
	require      bool
}

func (n *notaryFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&n.url, "notary", "", "notary URL: replay a recorded review for the same inputs and record this one (token in "+EnvNotaryToken+")")
	fs.StringVar(&n.oidcAudience, "notary-oidc-audience", "", "authenticate to the notary with a GitHub Actions identity token for this audience instead of "+EnvNotaryToken)
	fs.BoolVar(&n.fresh, "fresh", false, "clear the notary's record for these inputs and review again")
	fs.StringVar(&n.clearBy, "clear-by", "", "name kept in the record's history when -fresh clears it (default: the token's name)")
	fs.BoolVar(&n.require, "require-notary", false, "label a review UNOFFICIAL and do not post it unless a notary kept its record")
}

// apply installs the notary on the engine. No -notary is no notary, and -fresh
// without one is refused, since it would otherwise do nothing and say nothing.
func (n *notaryFlags) apply(engine *review.Engine) error {
	if n.url == "" {
		if n.fresh {
			return errors.New("-fresh clears a notary record and needs -notary")
		}
		engine.RequireNotary = n.require
		return nil
	}
	var (
		store *wire.HTTPStore
		err   error
	)
	switch token := strings.TrimSpace(os.Getenv(EnvNotaryToken)); {
	case token != "":
		store, err = wire.NewHTTPStore(n.url, token)
	case n.oidcAudience != "":
		store, err = wire.NewHTTPStoreWithSource(n.url, wire.GitHubOIDCToken(n.oidcAudience))
	default:
		return fmt.Errorf("-notary needs a credential: set %s or pass -notary-oidc-audience", EnvNotaryToken)
	}
	if err != nil {
		return err
	}
	engine.Notary = store
	engine.Fresh = n.fresh
	engine.NotarySource = n.clearBy
	engine.RequireNotary = n.require
	return nil
}
