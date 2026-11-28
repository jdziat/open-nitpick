package notary

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v4"

	"github.com/nitpick-oss/nitpick/internal/wire"
)

// FuzzAuthenticateRefusesWhatIsNotSignedByTheIssuer: no string a caller can
// send is accepted unless it is a token the issuer signed with the right
// claims. The seeds are the shapes attacks take.
func FuzzAuthenticateRefusesWhatIsNotSignedByTheIssuer(f *testing.F) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		f.Fatal(err)
	}
	jwks := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "kid": "k1",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}}})
	}))
	f.Cleanup(jwks.Close)
	v := &OIDC{Issuer: testIssuer, Audience: testAudience, JWKSURL: jwks.URL, Allow: []OIDCRule{newOIDCRule("*", RoleAuthoritative)}}

	claims := &jwt.RegisteredClaims{Issuer: testIssuer, Subject: testSubject, Audience: jwt.ClaimStrings{testAudience},
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}
	valid := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	valid.Header["kid"] = "k1"
	validStr, _ := valid.SignedString(key)
	none := jwt.NewWithClaims(jwt.SigningMethodNone, claims)
	none.Header["kid"] = "k1"
	noneStr, _ := none.SignedString(jwt.UnsafeAllowNoneSignatureType)
	hs := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	hs.Header["kid"] = "k1"
	hsStr, _ := hs.SignedString(key.N.Bytes())
	parts := strings.Split(validStr, ".")
	for _, s := range []string{
		noneStr, hsStr, parts[0] + "." + parts[1] + ".", parts[0] + "." + parts[1] + "." + parts[2][:len(parts[2])-2] + "AA",
		parts[0] + "." + parts[1], "." + parts[1] + "." + parts[2], "a.b.c", "..", "", "x", strings.Repeat("a.", 1000),
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, bearer string) {
		if tok, ok := v.Authenticate(context.Background(), bearer); ok && bearer != validStr {
			t.Fatalf("accepted %q as %+v", bearer, tok)
		}
	})
}

func FuzzParseOIDCRule(f *testing.F) {
	for _, s := range []string{"read:repo:a/b:*", "authoritative:*", ":", "read:", "x:y", "provisional:repo:acme/app:ref:refs/heads/main", "read:.*", "read:[", "read:\\", "read:*****"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		r, err := ParseOIDCRule(s)
		if err != nil {
			return
		}
		if r.re == nil || r.Pattern == "" || (r.Role != RoleAuthoritative && r.Role != RoleProvisional && r.Role != RoleRead) {
			t.Fatalf("accepted %q as an unusable rule: %+v", s, r)
		}
		// A pattern with no star matches exactly itself and nothing longer.
		if !strings.Contains(r.Pattern, "*") {
			if !r.re.MatchString(r.Pattern) {
				t.Fatalf("a literal pattern %q does not match itself", r.Pattern)
			}
			if r.re.MatchString(r.Pattern+"x") || r.re.MatchString("x"+r.Pattern) {
				t.Fatalf("a literal pattern %q matches a longer subject", r.Pattern)
			}
		}
	})
}

// FuzzPutNeverFailsWithAServerError: whatever bytes arrive as a record, the
// answer is a success or a client error, and what was stored can be read back.
func FuzzPutNeverFailsWithAServerError(f *testing.F) {
	h := strings.Repeat("a", 64)
	for _, s := range []string{
		`{"schema":4,"hash":"` + h + `","answers":{"k":"{}"},"batches":1,"complete":true}`,
		`{}`, `[]`, `null`, `"x"`, `{"hash":"` + h + `"}`, `{"answers":null}`, `{"answers":{"":""}}`,
		`{"schema":-1,"hash":"` + h + `","clears":-5,"cleared_by":[null]}`,
		`{"hash":"` + h + `","created":"not a time"}`, "\x00\x01\x02", strings.Repeat("{", 5000),
	} {
		f.Add(s)
	}
	dir := f.TempDir()
	fs, err := wire.NewFileStore(filepath.Join(dir, "data"))
	if err != nil {
		f.Fatal(err)
	}
	srv := httptest.NewServer(New(testDB(f), fs, []Token{NewToken("ci", RoleAuthoritative, "s")}, false))
	f.Cleanup(srv.Close)
	f.Fuzz(func(t *testing.T, body string) {
		req, _ := http.NewRequest(http.MethodPut, srv.URL+"/v1/records/"+h, bytes.NewReader([]byte(body)))
		req.Header.Set("Authorization", "Bearer s")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("the server dropped the connection on %q: %v", body, err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode >= 500 {
			t.Fatalf("a record body %q made the server answer %d", body, resp.StatusCode)
		}
		if resp.StatusCode == http.StatusNoContent {
			if _, _, err := fs.Get(context.Background(), h); err != nil {
				t.Fatalf("the server accepted %q and stored what cannot be read back: %v", body, err)
			}
		}
	})
}
