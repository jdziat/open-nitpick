package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v4"
)

// fakeIdP is an OpenID Connect provider with real discovery, a real JWKS and a
// token endpoint that returns correctly signed ID tokens. Each field below is a
// way to make it misbehave, so a test is one assignment away from an attack.
type fakeIdP struct {
	t    *testing.T
	srv  *httptest.Server
	key  *rsa.PrivateKey
	kid  string
	mu   sync.Mutex
	code map[string]codeGrant

	// What the next ID token says.
	subject       string
	groups        any
	name          string
	audience      string // "" means the client id
	issuer        string // "" means this server
	expires       time.Duration
	nonce         string // "" means the nonce the authorize request carried
	signWith      *rsa.PrivateKey
	alg           jwt.SigningMethod
	omitIDToken   bool
	tamperNonce   bool
	skipPKCEOK    bool // accept a token request whose verifier does not match
	tokenRequests int  // how many times the token endpoint was asked
}

type codeGrant struct {
	nonce, challenge string
}

func newFakeIdP(t *testing.T) *fakeIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &fakeIdP{t: t, key: key, kid: "k1", code: map[string]codeGrant{}, subject: "user-1", name: "Una Person", expires: time.Hour, alg: jwt.SigningMethodRS256}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": p.srv.URL, "authorization_endpoint": p.srv.URL + "/authorize", "token_endpoint": p.srv.URL + "/token",
			"jwks_uri": p.srv.URL + "/jwks", "id_token_signing_alg_values_supported": []string{"RS256"},
			"response_types_supported": []string{"code"}, "subject_types_supported": []string{"public"},
		})
	})
	mux.HandleFunc("GET /jwks", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]any{{
			"kty": "RSA", "alg": "RS256", "use": "sig", "kid": p.kid,
			"n": base64.RawURLEncoding.EncodeToString(p.key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString([]byte{1, 0, 1}),
		}}})
	})
	// authorize records the nonce and PKCE challenge and sends the browser back,
	// as an identity provider does once the person has signed in.
	mux.HandleFunc("GET /authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		code := "code-" + q.Get("state")
		p.mu.Lock()
		p.code[code] = codeGrant{nonce: q.Get("nonce"), challenge: q.Get("code_challenge")}
		p.mu.Unlock()
		back, _ := url.Parse(q.Get("redirect_uri"))
		bq := back.Query()
		bq.Set("code", code)
		bq.Set("state", q.Get("state"))
		back.RawQuery = bq.Encode()
		http.Redirect(w, r, back.String(), http.StatusFound)
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		p.mu.Lock()
		p.tokenRequests++
		grant, ok := p.code[r.PostFormValue("code")]
		delete(p.code, r.PostFormValue("code")) // a code is good once
		p.mu.Unlock()
		if !ok {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		sum := sha256.Sum256([]byte(r.PostFormValue("code_verifier")))
		if base64.RawURLEncoding.EncodeToString(sum[:]) != grant.challenge && !p.skipPKCEOK {
			http.Error(w, `{"error":"invalid_grant","error_description":"PKCE verification failed"}`, http.StatusBadRequest)
			return
		}
		resp := map[string]any{"access_token": "at", "token_type": "Bearer", "expires_in": 3600}
		if !p.omitIDToken {
			id := r.PostFormValue("client_id")
			if user, _, ok := r.BasicAuth(); ok && id == "" {
				id, _ = url.QueryUnescape(user) // oauth2 sends credentials in the Basic header
			}
			resp["id_token"] = p.idToken(id, grant.nonce)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})
	p.srv = httptest.NewTLSServer(mux)
	t.Cleanup(p.srv.Close)
	return p
}

func (p *fakeIdP) idToken(clientID, nonce string) string {
	iss := p.issuer
	if iss == "" {
		iss = p.srv.URL
	}
	aud := p.audience
	if aud == "" {
		aud = clientID
	}
	if p.nonce != "" {
		nonce = p.nonce
	}
	if p.tamperNonce {
		nonce += "x"
	}
	claims := jwt.MapClaims{
		"iss": iss, "sub": p.subject, "aud": aud, "iat": time.Now().Unix(),
		"exp": time.Now().Add(p.expires).Unix(), "nonce": nonce, "name": p.name,
	}
	if p.groups != nil {
		claims["groups"] = p.groups
	}
	tok := jwt.NewWithClaims(p.alg, claims)
	tok.Header["kid"] = p.kid
	key := p.signWith
	if key == nil {
		key = p.key
	}
	var signed string
	var err error
	if p.alg == jwt.SigningMethodNone {
		signed, err = tok.SignedString(jwt.UnsafeAllowNoneSignatureType)
	} else {
		signed, err = tok.SignedString(key)
	}
	if err != nil {
		p.t.Fatal(err)
	}
	return signed
}
