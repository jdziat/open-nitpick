package server

import "net/http"

// Provider is one way to sign in: a password, an OIDC identity provider, a SAML
// one. The server knows only this interface, so adding a vendor does not touch
// it.
type Provider interface {
	// ID is a short name used in URLs and in role rules: "password", "oidc".
	ID() string

	// Label is what the login page calls the provider.
	Label() string

	// Form says how the login page offers the provider.
	Form() LoginForm

	// Mount adds the provider's routes. They are served under /auth/<ID>/ and
	// are expected to finish with Host.SignIn.
	Mount(mux *http.ServeMux, host Host)

	// CrossSitePosts are the exact paths that a browser reaches by a POST that
	// another site started, such as a SAML assertion consumer. The server does
	// not ask those for a CSRF token, because the identity provider cannot have
	// one; the provider binds the request to a login it started instead.
	CrossSitePosts() []string
}

// LoginForm is how a provider appears on the login page.
type LoginForm int

const (
	// FormCredentials is a username and password form posting to /auth/<ID>/login.
	FormCredentials LoginForm = iota
	// FormRedirect is a link to /auth/<ID>/login that starts a redirect.
	FormRedirect
)

// Host is what the server gives a provider.
type Host interface {
	// SignIn finishes a sign-in: it grants the identity its roles, starts a new
	// session, and sends the browser on to next, or to the UI home when next is
	// empty or not a local path.
	SignIn(w http.ResponseWriter, r *http.Request, id Identity, next string)

	// CSRF is the protector for the provider's own forms.
	CSRF() *CSRF

	// LoginFailed answers a failed sign-in with the login page and a message. The
	// message is shown to the person, so it names no account and no internal.
	LoginFailed(w http.ResponseWriter, r *http.Request, status int, message string)
}
