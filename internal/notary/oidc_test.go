package notary

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v4"

	"github.com/nitpick-oss/nitpick/internal/wire"
)

const (
	testIssuer   = "https://issuer.test"
	testAudience = "nitpick-notary"
	testSubject  = "repo:acme/app:ref:refs/heads/main"
)

type idp struct {
	key  *rsa.PrivateKey
	srv  *httptest.Server
	hits int
}

func newIDP(t *testing.T) *idp {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &idp{key: key}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		p.hits++
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "kid": "k1",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}}})
	}))
	t.Cleanup(p.srv.Close)
	return p
}

func (p *idp) verifier(rules ...OIDCRule) *OIDC {
	return &OIDC{Issuer: testIssuer, Audience: testAudience, Allow: rules, JWKSURL: p.srv.URL}
}

func (p *idp) token(t *testing.T, mutate func(*jwt.RegisteredClaims)) string {
	t.Helper()
	c := &jwt.RegisteredClaims{
		Issuer: testIssuer, Subject: testSubject, Audience: jwt.ClaimStrings{testAudience},
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(5 * time.Minute)),
	}
	if mutate != nil {
		mutate(c)
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, c)
	tok.Header["kid"] = "k1"
	s, err := tok.SignedString(p.key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestAWorkflowTokenForAnAllowedSubjectGetsTheRuleRole(t *testing.T) {
	p := newIDP(t)
	v := p.verifier(newOIDCRule("repo:acme/*:ref:refs/heads/main", RoleAuthoritative))
	tok, ok := v.Authenticate(context.Background(), p.token(t, nil))
	if !ok || tok.Role != RoleAuthoritative || tok.Name != "oidc:"+testSubject {
		t.Fatalf("got %+v ok=%v", tok, ok)
	}
}

func TestATokenIsRefusedUnlessEveryRequiredClaimHolds(t *testing.T) {
	p := newIDP(t)
	rule := newOIDCRule("*", RoleAuthoritative)
	cases := map[string]func(*jwt.RegisteredClaims){
		"another audience": func(c *jwt.RegisteredClaims) { c.Audience = jwt.ClaimStrings{"someone-else"} },
		"no audience":      func(c *jwt.RegisteredClaims) { c.Audience = nil },
		"another issuer":   func(c *jwt.RegisteredClaims) { c.Issuer = "https://evil.test" },
		"expired":          func(c *jwt.RegisteredClaims) { c.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-time.Minute)) },
		"no expiry":        func(c *jwt.RegisteredClaims) { c.ExpiresAt = nil },
		"no subject":       func(c *jwt.RegisteredClaims) { c.Subject = "" },
		"not yet valid":    func(c *jwt.RegisteredClaims) { c.NotBefore = jwt.NewNumericDate(time.Now().Add(time.Hour)) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			if tok, ok := p.verifier(rule).Authenticate(context.Background(), p.token(t, mutate)); ok {
				t.Fatalf("accepted: %+v", tok)
			}
		})
	}
}

func TestASubjectNoRuleMatchesIsRefused(t *testing.T) {
	p := newIDP(t)
	v := p.verifier(newOIDCRule("repo:acme/other:*", RoleAuthoritative))
	if tok, ok := v.Authenticate(context.Background(), p.token(t, nil)); ok {
		t.Fatalf("accepted a subject no rule names: %+v", tok)
	}
	if _, ok := p.verifier().Authenticate(context.Background(), p.token(t, nil)); ok {
		t.Fatal("an empty allow list must admit nobody")
	}
}

func TestTheFirstMatchingRuleDecidesTheRole(t *testing.T) {
	p := newIDP(t)
	v := p.verifier(
		newOIDCRule("repo:acme/app:ref:refs/heads/main", RoleAuthoritative),
		newOIDCRule("repo:acme/*", RoleProvisional),
	)
	tok, _ := v.Authenticate(context.Background(), p.token(t, nil))
	if tok.Role != RoleAuthoritative {
		t.Errorf("role = %q, want authoritative", tok.Role)
	}
	tok, _ = v.Authenticate(context.Background(), p.token(t, func(c *jwt.RegisteredClaims) { c.Subject = "repo:acme/app:pull_request" }))
	if tok.Role != RoleProvisional {
		t.Errorf("role = %q, want provisional", tok.Role)
	}
}

func TestARuleDoesNotMatchAMereSubstringOfTheSubject(t *testing.T) {
	p := newIDP(t)
	v := p.verifier(newOIDCRule("repo:acme/app", RoleAuthoritative))
	if tok, ok := v.Authenticate(context.Background(), p.token(t, nil)); ok {
		t.Fatalf("a prefix of the subject granted %q", tok.Role)
	}
}

func TestATokenSignedByAnotherKeyIsRefused(t *testing.T) {
	p := newIDP(t)
	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	forged := jwt.NewWithClaims(jwt.SigningMethodRS256, &jwt.RegisteredClaims{
		Issuer: testIssuer, Subject: testSubject, Audience: jwt.ClaimStrings{testAudience},
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
	})
	forged.Header["kid"] = "k1"
	s, _ := forged.SignedString(other)
	if _, ok := p.verifier(newOIDCRule("*", RoleAuthoritative)).Authenticate(context.Background(), s); ok {
		t.Fatal("accepted a token the issuer did not sign")
	}
}

func TestTheAlgorithmIsNotTakenFromTheToken(t *testing.T) {
	p := newIDP(t)
	v := p.verifier(newOIDCRule("*", RoleAuthoritative))
	claims := &jwt.RegisteredClaims{
		Issuer: testIssuer, Subject: testSubject, Audience: jwt.ClaimStrings{testAudience},
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
	}
	none := jwt.NewWithClaims(jwt.SigningMethodNone, claims)
	none.Header["kid"] = "k1"
	unsigned, _ := none.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if _, ok := v.Authenticate(context.Background(), unsigned); ok {
		t.Error("accepted alg none")
	}
	// HMAC keyed with the public modulus, the classic confusion attack.
	hs := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	hs.Header["kid"] = "k1"
	mac, _ := hs.SignedString(p.key.N.Bytes())
	if _, ok := v.Authenticate(context.Background(), mac); ok {
		t.Error("accepted an HMAC token")
	}
}

func TestOnlyRS256IsAcceptedEvenWhenAnotherRSAVariantIsSignedByTheRealKey(t *testing.T) {
	p := newIDP(t)
	v := p.verifier(newOIDCRule("*", RoleAuthoritative))
	claims := &jwt.RegisteredClaims{
		Issuer: testIssuer, Subject: testSubject, Audience: jwt.ClaimStrings{testAudience},
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
	}
	if _, ok := v.Authenticate(context.Background(), p.token(t, nil)); !ok {
		t.Fatal("the RS256 control token was refused, so the refusal below proves nothing")
	}
	// The pin is the only thing that refuses this: the key is the real one.
	rs512 := jwt.NewWithClaims(jwt.SigningMethodRS512, claims)
	rs512.Header["kid"] = "k1"
	s, _ := rs512.SignedString(p.key)
	if _, ok := v.Authenticate(context.Background(), s); ok {
		t.Error("accepted RS512: the algorithm is being read from the token")
	}
}

func TestUnknownKeyIdsDoNotFloodTheIssuer(t *testing.T) {
	p := newIDP(t)
	v := p.verifier(newOIDCRule("*", RoleAuthoritative))
	for range 20 {
		tok := jwt.NewWithClaims(jwt.SigningMethodRS256, &jwt.RegisteredClaims{})
		tok.Header["kid"] = "nope"
		s, _ := tok.SignedString(p.key)
		v.Authenticate(context.Background(), s)
	}
	if p.hits > 1 {
		t.Errorf("the issuer was asked for its keys %d times by 20 bad tokens", p.hits)
	}
}

func TestAServerWithOIDCStillTakesStaticTokensAndRoutesWorkflowTokensByRole(t *testing.T) {
	p := newIDP(t)
	fs, err := wire.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(New(fs, []Token{NewToken("ci", RoleAuthoritative, "ci-secret")}, false).
		WithOIDC(p.verifier(newOIDCRule("repo:acme/app:*", RoleRead))))
	t.Cleanup(srv.Close)
	ctx := context.Background()

	static, _ := wire.NewHTTPStore(srv.URL, "ci-secret")
	if err := static.Put(ctx, rec(true)); err != nil {
		t.Fatalf("static token write: %v", err)
	}

	oidc, _ := wire.NewHTTPStore(srv.URL, p.token(t, nil))
	if _, found, err := oidc.Get(ctx, hash); err != nil || !found {
		t.Fatalf("workflow token read: found=%v err=%v", found, err)
	}
	if err := oidc.Put(ctx, rec(false)); err == nil {
		t.Fatal("a workflow token mapped to the read role wrote a record")
	}

	bad, _ := wire.NewHTTPStore(srv.URL, p.token(t, func(c *jwt.RegisteredClaims) { c.Audience = jwt.ClaimStrings{"x"} }))
	if _, _, err := bad.Get(ctx, hash); err == nil {
		t.Fatal("a token for another audience read a record")
	}
}

func TestParseOIDCRuleSplitsTheRoleFromAPatternThatHasColons(t *testing.T) {
	r, err := ParseOIDCRule("authoritative:repo:acme/app:ref:refs/heads/main")
	if err != nil || r.Role != RoleAuthoritative || r.Pattern != "repo:acme/app:ref:refs/heads/main" {
		t.Fatalf("got %+v err=%v", r, err)
	}
	for _, bad := range []string{"", "authoritative", "authoritative:", "root:repo:x", ":repo:x"} {
		if _, err := ParseOIDCRule(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

func TestATokenHoldsOnlyTheHashOfItsSecret(t *testing.T) {
	const secret = "s3cret-value-that-must-not-be-kept"
	tok := NewToken("ci", RoleAuthoritative, secret)
	for _, shown := range []string{fmt.Sprintf("%v", tok), fmt.Sprintf("%+v", tok), fmt.Sprintf("%#v", tok)} {
		if strings.Contains(shown, secret) {
			t.Fatalf("the secret is reachable from the token: %s", shown)
		}
	}
	sum := sha256.Sum256([]byte(secret))
	if tok.hash != sum {
		t.Error("the token does not hold the SHA-256 of its secret")
	}
}
