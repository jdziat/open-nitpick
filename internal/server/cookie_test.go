package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestACookieThatABrowserWouldDropIsRefusedBeforeItIsSent(t *testing.T) {
	good := http.Cookie{Name: "__Host-x", Value: "v", Path: "/", Secure: true, HttpOnly: true}
	for name, mutate := range map[string]func(c *http.Cookie){
		"a __Host- cookie on a narrower path": func(c *http.Cookie) { c.Path = "/auth/saml/" },
		"a __Host- cookie with no path":       func(c *http.Cookie) { c.Path = "" },
		"a __Host- cookie not Secure":         func(c *http.Cookie) { c.Secure = false },
		"a __Host- cookie with a Domain":      func(c *http.Cookie) { c.Domain = "example.com" },
		"a __Secure- cookie not Secure":       func(c *http.Cookie) { c.Name, c.Secure, c.Path = "__Secure-x", false, "/a/" },
	} {
		c := good
		mutate(&c)
		rec := httptest.NewRecorder()
		if err := SetCookie(rec, &c); err == nil {
			t.Errorf("%s: sent", name)
		}
		if len(rec.Result().Cookies()) != 0 {
			t.Errorf("%s: a refused cookie was still written", name)
		}
	}
	rec := httptest.NewRecorder()
	if err := SetCookie(rec, &good); err != nil || len(rec.Result().Cookies()) != 1 {
		t.Fatalf("a valid __Host- cookie was refused: %v", err)
	}
	// A cookie with no prefix has no such rules: a narrow path is its whole point.
	plain := http.Cookie{Name: "nitpick_saml", Value: "v", Path: "/auth/saml/", Secure: true}
	if err := SetCookie(httptest.NewRecorder(), &plain); err != nil {
		t.Fatalf("an unprefixed cookie on a narrow path was refused: %v", err)
	}
}

// Go's cookie jar does not enforce prefixes, so a test through it passed while a
// browser would have dropped the SAML cookie. Every cookie therefore goes through
// SetCookie, and this keeps a new one from being set the old way.
func TestNoCookieIsSetWithoutGoingThroughTheChecker(t *testing.T) {
	root := filepath.Join("..", "server")
	var offenders []string
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") || filepath.Base(p) == "cookie.go" {
			return err
		}
		b, _ := os.ReadFile(p)
		if strings.Contains(string(b), "http.SetCookie(") {
			offenders = append(offenders, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) > 0 {
		t.Fatalf("these set a cookie with http.SetCookie, skipping the __Host- rules: %v", offenders)
	}
}
