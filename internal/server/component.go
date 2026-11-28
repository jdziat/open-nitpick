package server

import (
	"context"
	"errors"
	"net/http"
)

// Role is a permission an identity holds. Roles are plain strings so that a
// component brings its own, and the server only compares them. The notary's
// are authoritative, provisional and read.
type Role string

// Component is one hosted piece, such as the notary.
type Component interface {
	// Name identifies the component in configuration and in /healthz.
	Name() string

	// Prefixes are the URL path prefixes the component owns, each beginning and
	// ending with "/". Every route sits under one of them, so two components
	// cannot claim the same path and a route cannot be mounted somewhere the
	// component did not declare.
	Prefixes() []string

	// Routes are the component's HTTP routes.
	Routes() []Route

	Pages() []Page

	// Health reports whether the component can do its work. A nil error is
	// healthy.
	Health(ctx context.Context) error
}

// RoleDeclarer is a component that says which roles mean something to it. When
// any component does, a role rule naming a role that none of them declares is an
// error at start-up: a role nobody understands grants nothing, and a typo in a
// permission is the permission quietly not given.
type RoleDeclarer interface {
	Roles() []Role
}

// Route is one HTTP route of a component.
type Route struct {
	// Pattern is a net/http pattern, "METHOD /path", with the path under one of
	// the component's Prefixes.
	Pattern string

	// Need is the role a signed-in session must hold. The server refuses a
	// request without a session (401) and a session without the role (403)
	// before the handler runs. Empty means the component authenticates the
	// request itself, as the notary does with its machine tokens; the handler
	// may still read a session with IdentityFrom.
	Need Role

	Handler http.Handler
}

// Page is one page a component adds to the UI.
type Page struct {
	// Path is relative to /ui/<component>/. It may hold {name} wildcards. Empty
	// is the component's index page.
	Path string

	// Title is the page heading and, with Nav set, its navigation entry.
	Title string

	// Nav puts the page in the navigation.
	Nav bool

	// Need is the role a session must hold to see the page. A page always needs
	// a session; with no role named, any signed-in identity may see it.
	Need Role

	// Body is the page's html/template source. The shell parses it, so the
	// escaping contexts are the shell's and a component cannot opt out of them.
	// It is executed with a View: {{.Data}} is what Data returned, and
	// {{.Identity}} and {{.CSRF}} are the signed-in identity and the token a
	// form of the page puts in a csrf_token field.
	Body string

	// Heading, when set, names the page from its data, for a page that shows one
	// thing among many, such as a review. It replaces Title in the heading and the
	// browser title, and it is escaped like any other text. Title still names the
	// page in the navigation and in errors.
	Heading func(data any) string

	// Data builds what Body is executed with. Return ErrNotFound for a page that
	// does not exist, which is a 404. Any other error is a 500 whose text goes to
	// the log and not to the page.
	Data func(r *http.Request) (any, error)
}

// View is what a page's Body is executed with.
type View struct {
	Data     any
	Identity Identity
	CSRF     string
}

// ErrNotFound is what a page's Data returns for something that does not exist.
var ErrNotFound = errors.New("not found")
