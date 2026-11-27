package server

import (
	"fmt"
	"net/http"
	"strings"
)

// CheckCookie reports a cookie that a browser would reject. A cookie named with
// the __Host- prefix must be Secure, have Path=/ and no Domain, and one named
// __Secure- must be Secure (RFC 6265bis). Go's cookie jar does not enforce this,
// so a test that signs in through it passes while a real browser drops the
// cookie and every sign-in fails.
func CheckCookie(c *http.Cookie) error {
	switch {
	case strings.HasPrefix(c.Name, "__Host-"):
		if !c.Secure || c.Path != "/" || c.Domain != "" {
			return fmt.Errorf("cookie %s: a __Host- cookie must be Secure, have Path=/ and no Domain (Secure=%v Path=%q Domain=%q)", c.Name, c.Secure, c.Path, c.Domain)
		}
	case strings.HasPrefix(c.Name, "__Secure-"):
		if !c.Secure {
			return fmt.Errorf("cookie %s: a __Secure- cookie must be Secure", c.Name)
		}
	}
	return nil
}

// SetCookie sets a cookie after CheckCookie. Every cookie the server and its
// providers set goes through it; a cookie that fails is not written.
func SetCookie(w http.ResponseWriter, c *http.Cookie) error {
	if err := CheckCookie(c); err != nil {
		return err
	}
	http.SetCookie(w, c)
	return nil
}
