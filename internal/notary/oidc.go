package notary

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v4"
)

// GitHubIssuer is the issuer of GitHub Actions OIDC tokens.
const GitHubIssuer = "https://token.actions.githubusercontent.com"

// OIDCRule grants a role to the workflows whose subject matches Pattern.
type OIDCRule struct {
	// Pattern is matched against the whole subject claim. "*" matches any run
	// of characters, "/" and ":" included, since a subject such as
	// repo:acme/app:ref:refs/heads/main has both.
	Pattern string
	Role    Role
	re      *regexp.Regexp
}

// ParseOIDCRule reads role:pattern, as the -oidc-allow flag gives it. The
// role comes first because a pattern contains colons and a role never does.
func ParseOIDCRule(v string) (OIDCRule, error) {
	role, pattern, ok := strings.Cut(v, ":")
	if !ok || pattern == "" {
		return OIDCRule{}, errors.New("oidc rule must be role:subject-pattern")
	}
	switch r := Role(role); r {
	case RoleAuthoritative, RoleProvisional, RoleRead:
		return newOIDCRule(pattern, r), nil
	default:
		return OIDCRule{}, fmt.Errorf("unknown role %q: use authoritative, provisional or read", role)
	}
}

func newOIDCRule(pattern string, role Role) OIDCRule {
	quoted := regexp.QuoteMeta(pattern)
	re := regexp.MustCompile("^" + strings.ReplaceAll(quoted, `\*`, ".*") + "$")
	return OIDCRule{Pattern: pattern, Role: role, re: re}
}

// OIDC lets a CI run authenticate with the identity token its platform issues
// instead of a stored secret.
type OIDC struct {
	// Issuer must equal the token's iss claim.
	Issuer string
	// Audience must be among the token's aud claims. The token is minted for
	// this notary and for no other relying party.
	Audience string
	// Allow is checked in order, and the first match decides the role. A
	// subject no rule matches is refused, so an empty list admits nobody.
	Allow []OIDCRule
	// JWKSURL is where the issuer publishes its keys. Empty means
	// Issuer + /.well-known/jwks.
	JWKSURL string
	// Client fetches the key set. Nil uses a client with a timeout.
	Client *http.Client

	mu      sync.Mutex
	keys    map[string]*rsa.PublicKey
	fetched time.Time
}

// keyRefresh bounds how often an unknown key id triggers a fetch, so a flood
// of tokens naming random key ids cannot turn into a flood of requests to the
// issuer.
const keyRefresh = time.Minute

func (o *OIDC) jwksURL() string {
	if o.JWKSURL != "" {
		return o.JWKSURL
	}
	return strings.TrimRight(o.Issuer, "/") + "/.well-known/jwks"
}

func (o *OIDC) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if k, ok := o.keys[kid]; ok {
		return k, nil
	}
	if !o.fetched.IsZero() && time.Since(o.fetched) < keyRefresh {
		return nil, fmt.Errorf("no key %q", kid)
	}
	keys, err := o.fetch(ctx)
	// Stamped before the error check: a failing issuer must not be asked again
	// by every token that names a key we do not have. Not when the caller gave
	// up: a cancelled request says nothing about the issuer, and stamping it
	// would refuse every workflow token for a minute.
	if err == nil || ctx.Err() == nil {
		o.fetched = time.Now()
	}
	if err != nil {
		return nil, err
	}
	o.keys = keys
	if k, ok := keys[kid]; ok {
		return k, nil
	}
	return nil, fmt.Errorf("no key %q", kid)
}

func (o *OIDC) fetch(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.jwksURL(), nil)
	if err != nil {
		return nil, err
	}
	c := o.Client
	if c == nil {
		c = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch signing keys: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch signing keys: %s", resp.Status)
	}
	var set struct {
		Keys []struct {
			Kty, Kid, N, E string
		} `json:"keys"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&set); err != nil {
		return nil, fmt.Errorf("decode signing keys: %w", err)
	}
	keys := map[string]*rsa.PublicKey{}
	for _, k := range set.Keys {
		if k.Kty != "RSA" {
			continue
		}
		n, errN := base64.RawURLEncoding.DecodeString(k.N)
		e, errE := base64.RawURLEncoding.DecodeString(k.E)
		if errN != nil || errE != nil || len(e) == 0 || len(e) > 4 {
			continue
		}
		exp := 0
		for _, b := range e {
			exp = exp<<8 | int(b)
		}
		keys[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: exp}
	}
	return keys, nil
}

// Authenticate verifies a bearer token and returns the identity it grants.
// Every refusal is the same to the caller, a 401: which check failed is
// logged by nobody and learned by nobody.
func (o *OIDC) Authenticate(ctx context.Context, bearer string) (Token, bool) {
	if o == nil || o.Issuer == "" || o.Audience == "" || bearer == "" {
		return Token{}, false
	}
	claims := &jwt.RegisteredClaims{}
	_, err := jwt.ParseWithClaims(bearer, claims, func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		if kid == "" {
			return nil, errors.New("token names no key")
		}
		return o.key(ctx, kid)
	},
		// RS256 only. A verifier that takes the algorithm from the token can be
		// handed "none", or an HMAC keyed with the public key it publishes.
		jwt.WithValidMethods([]string{"RS256"}),
	)
	// Required claims are checked by hand: the library treats an absent
	// expiry as valid, and its issuer and audience checks are opt-in.
	if err != nil || claims.ExpiresAt == nil || claims.Subject == "" ||
		!claims.VerifyIssuer(o.Issuer, true) || !claims.VerifyAudience(o.Audience, true) {
		return Token{}, false
	}
	for _, rule := range o.Allow {
		if rule.re != nil && rule.re.MatchString(claims.Subject) {
			return Token{Name: "oidc:" + claims.Subject, Role: rule.Role}, true
		}
	}
	return Token{}, false
}
