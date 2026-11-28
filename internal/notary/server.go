// Package notary serves review records over HTTP with token roles. It is a
// component of the nitpick server: the same routes and the same machine
// authentication it always had, plus a listing and the pages of the hub that
// show what it holds.
package notary

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"sync"

	"github.com/nitpick-oss/nitpick/internal/database"
	"github.com/nitpick-oss/nitpick/internal/server"
	"github.com/nitpick-oss/nitpick/internal/wire"
)

var hashRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Role says what a token may do.
type Role string

// Roles. Authoritative tokens write the record CI trusts. Provisional tokens
// write only where no complete record exists and never replace one. Read
// tokens only read.
const (
	RoleAuthoritative Role = "authoritative"
	RoleProvisional   Role = "provisional"
	RoleRead          Role = "read"
)

// Token is one credential. The secret is held only as a hash.
type Token struct {
	Name string
	Role Role
	hash [32]byte
}

// NewToken builds a token from its plain secret.
func NewToken(name string, role Role, secret string) Token {
	return Token{Name: name, Role: role, hash: sha256.Sum256([]byte(secret))}
}

// storeFault answers a failed store call without its text. A filesystem error
// names the directory the records are kept in, which a client has no use for;
// the operator reads the detail in the server's log.
func storeFault(w http.ResponseWriter, r *http.Request, err error) {
	log.Printf("notary: %s %s: %v", r.Method, r.URL.Path, err)
	http.Error(w, "the notary could not use its store", http.StatusInternalServerError)
}

// TokenFromHash builds a token from the hex SHA-256 of its secret, so a
// deployment can be configured without the secret ever being on the server.
func TokenFromHash(name string, role Role, hexHash string) (Token, error) {
	raw, err := hex.DecodeString(hexHash)
	if err != nil || len(raw) != sha256.Size {
		return Token{}, errors.New("a token hash must be 64 hex characters")
	}
	t := Token{Name: name, Role: role}
	copy(t.hash[:], raw)
	return t, nil
}

// HashSecret returns the hex SHA-256 that TokenFromHash takes.
func HashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// Component is the notary as a component of the nitpick server.
type Component struct {
	store wire.Store
	// writes serializes PUTs. The provisional rule reads a record and then writes
	// one, and an authoritative write landing between the two would be replaced.
	writes sync.Mutex
	tokens []Token
	// AllowProvisionalClear lets provisional tokens clear a record, for
	// organisations that let a PR author re-review.
	allowProvisionalClear bool
	oidc                  *OIDC
}

// NewComponent builds the notary component. With no tokens every machine request
// is refused; people reach the listing and the pages through a session.
func NewComponent(store wire.Store, tokens []Token, allowProvisionalClear bool) *Component {
	return &Component{store: store, tokens: tokens, allowProvisionalClear: allowProvisionalClear}
}

// WithOIDC also accepts identity tokens that o verifies, beside the static
// ones. It returns c for chaining.
func (c *Component) WithOIDC(o *OIDC) *Component {
	c.oidc = o
	return c
}

// Server is the notary mounted alone in a nitpick server, for a test or an
// embedding that wants the notary's routes and nothing else. The nitpick server
// itself mounts the Component beside the others.
type Server struct {
	*Component
	handler http.Handler
}

// New mounts a notary component alone, over db for the server's own state. It
// panics only if the notary's own routes are invalid, or db lacks the tables of
// server.Models, which is a bug in the caller and not an input.
func New(db *database.DB, store wire.Store, tokens []Token, allowProvisionalClear bool) *Server {
	c := NewComponent(store, tokens, allowProvisionalClear)
	h, err := server.New(server.Config{DB: db, Components: []server.Component{c}})
	if err != nil {
		panic("notary: " + err.Error())
	}
	return &Server{Component: c, handler: h}
}

// WithOIDC is Component.WithOIDC returning the Server, so it chains from New.
func (s *Server) WithOIDC(o *OIDC) *Server {
	s.Component.WithOIDC(o)
	return s
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.handler.ServeHTTP(w, r) }

// Name implements server.Component.
func (c *Component) Name() string { return "notary" }

// Roles implements server.RoleDeclarer: the roles a token holds, which a person
// can hold too. Only authoritative opens the pages and the listing.
func (c *Component) Roles() []server.Role {
	return []server.Role{server.Role(RoleAuthoritative), server.Role(RoleProvisional), server.Role(RoleRead)}
}

// Prefixes implements server.Component.
func (c *Component) Prefixes() []string { return []string{"/v1/records/"} }

// Health implements server.Component. A store that can answer a read is healthy;
// one that cannot is not, which is what a probe has to know.
func (c *Component) Health(ctx context.Context) error {
	_, _, err := c.store.Get(ctx, strings.Repeat("0", 64))
	return err
}

// Routes implements server.Component. The record routes take their own machine
// authentication, as they always did; the listing takes a token or a session.
func (c *Component) Routes() []server.Route {
	return []server.Route{
		{Pattern: "/v1/records/{hash}", Handler: http.HandlerFunc(c.record)},
		{Pattern: "/v1/records", Handler: http.HandlerFunc(c.list)},
		{Pattern: "GET /v1/records/view", Handler: http.HandlerFunc(c.viewList)},
		{Pattern: "GET /v1/records/view/{hash}", Handler: http.HandlerFunc(c.viewRecord)},
	}
}

func (c *Component) auth(r *http.Request) (Token, bool) {
	h := r.Header.Get("Authorization")
	secret, ok := strings.CutPrefix(h, "Bearer ")
	if !ok || secret == "" {
		return Token{}, false
	}
	sum := sha256.Sum256([]byte(secret))
	var found Token
	ok = false
	for _, t := range c.tokens {
		if subtle.ConstantTimeCompare(t.hash[:], sum[:]) == 1 {
			found, ok = t, true
		}
	}
	if !ok && c.oidc != nil {
		return c.oidc.Authenticate(r.Context(), secret)
	}
	return found, ok
}

// record serves GET, PUT and DELETE on /v1/records/{hash}.
func (c *Component) record(w http.ResponseWriter, r *http.Request) {
	hash := r.PathValue("hash")
	if !hashRE.MatchString(hash) {
		http.Error(w, "record hash must be 64 hex characters", http.StatusBadRequest)
		return
	}
	tok, ok := c.auth(r)
	if !ok {
		http.Error(w, "missing or unknown token", http.StatusUnauthorized)
		return
	}
	switch r.Method {
	case http.MethodGet:
		rec, found, err := c.store.Get(r.Context(), hash)
		if err != nil {
			storeFault(w, r, err)
			return
		}
		if !found {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(rec)
	case http.MethodPut:
		c.put(w, r, tok, hash)
	case http.MethodDelete:
		if tok.Role != RoleAuthoritative && (tok.Role != RoleProvisional || !c.allowProvisionalClear) {
			http.Error(w, "this token may not clear records", http.StatusForbidden)
			return
		}
		by := r.URL.Query().Get("by")
		if by == "" {
			by = tok.Name
		}
		if err := c.store.Clear(r.Context(), hash, by); err != nil {
			storeFault(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (c *Component) put(w http.ResponseWriter, r *http.Request, tok Token, hash string) {
	if tok.Role == RoleRead {
		http.Error(w, "read-only token", http.StatusForbidden)
		return
	}
	// Unknown fields are refused. A record field that this server does not know
	// would be dropped on write, and a field that affects replay (the reasoning
	// level) must fail loudly across a version skew, not vanish.
	var rec wire.Record
	dec := json.NewDecoder(io.LimitReader(r.Body, 32<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&rec); err != nil {
		http.Error(w, "bad record: "+err.Error(), http.StatusBadRequest)
		return
	}
	if rec.Hash != hash {
		http.Error(w, "record hash does not match the URL", http.StatusBadRequest)
		return
	}
	c.writes.Lock()
	defer c.writes.Unlock()
	rec.Source = tok.Name
	if tok.Role == RoleProvisional {
		rec.Source = "provisional:" + tok.Name
		if old, found, err := c.store.Get(r.Context(), hash); err == nil && found && old.Complete {
			http.Error(w, "a complete record exists; provisional tokens do not replace it", http.StatusConflict)
			return
		}
	}
	if err := c.store.Put(r.Context(), rec); err != nil {
		storeFault(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
