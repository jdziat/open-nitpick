package server

import (
	"context"
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
