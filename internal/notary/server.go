// Package notary serves review records over HTTP with token roles.
package notary

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"

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

// Server is the notary HTTP handler.
type Server struct {
	store  wire.Store
	tokens []Token
	// AllowProvisionalClear lets provisional tokens clear a record, for
	// organisations that let a PR author re-review.
	allowProvisionalClear bool
}

// New builds a server. With no tokens every request is refused.
func New(store wire.Store, tokens []Token, allowProvisionalClear bool) *Server {
	return &Server{store: store, tokens: tokens, allowProvisionalClear: allowProvisionalClear}
}

func (s *Server) auth(r *http.Request) (Token, bool) {
	h := r.Header.Get("Authorization")
	secret, ok := strings.CutPrefix(h, "Bearer ")
	if !ok || secret == "" {
		return Token{}, false
	}
	sum := sha256.Sum256([]byte(secret))
	var found Token
	ok = false
	for _, t := range s.tokens {
		if subtle.ConstantTimeCompare(t.hash[:], sum[:]) == 1 {
			found, ok = t, true
		}
	}
	return found, ok
}

// ServeHTTP implements http.Handler. Routes: GET, PUT and DELETE on
// /v1/records/{hash}.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	hash, ok := strings.CutPrefix(r.URL.Path, "/v1/records/")
	if !ok || hash == "" || strings.Contains(hash, "/") {
		http.NotFound(w, r)
		return
	}
	if !hashRE.MatchString(hash) {
		http.Error(w, "record hash must be 64 hex characters", http.StatusBadRequest)
		return
	}
	tok, ok := s.auth(r)
	if !ok {
		http.Error(w, "missing or unknown token", http.StatusUnauthorized)
		return
	}
	switch r.Method {
	case http.MethodGet:
		rec, found, err := s.store.Get(r.Context(), hash)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if !found {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(rec)
	case http.MethodPut:
		s.put(w, r, tok, hash)
	case http.MethodDelete:
		if tok.Role != RoleAuthoritative && !(tok.Role == RoleProvisional && s.allowProvisionalClear) {
			http.Error(w, "this token may not clear records", http.StatusForbidden)
			return
		}
		by := r.URL.Query().Get("by")
		if by == "" {
			by = tok.Name
		}
		if err := s.store.Clear(r.Context(), hash, by); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) put(w http.ResponseWriter, r *http.Request, tok Token, hash string) {
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
	rec.Source = tok.Name
	if tok.Role == RoleProvisional {
		rec.Source = "provisional:" + tok.Name
		if old, found, err := s.store.Get(r.Context(), hash); err == nil && found && old.Complete {
			http.Error(w, "a complete record exists; provisional tokens do not replace it", http.StatusConflict)
			return
		}
	}
	if err := s.store.Put(r.Context(), rec); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
