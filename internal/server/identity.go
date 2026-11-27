package server

import (
	"context"
	"slices"
)

// Identity is who a session belongs to, as the sign-in provider vouched for
// them, and what the server's role rules grant them.
type Identity struct {
	Provider string   // the provider that signed them in: "password", "oidc", "saml"
	Subject  string   // the provider's stable name for them
	Name     string   // what to show; may be empty, then Subject is shown
	Groups   []string // groups the provider reported
	Roles    []Role   // what the server's rules granted
}

// Has reports whether the identity holds a role.
func (i Identity) Has(r Role) bool { return slices.Contains(i.Roles, r) }

// Display is the name to show for the identity.
func (i Identity) Display() string {
	if i.Name != "" {
		return i.Name
	}
	return i.Subject
}

type ctxKey int

const identityKey ctxKey = iota

// IdentityFrom is the signed-in identity of a request, if it has a session.
// Handlers of routes that authenticate themselves read it to accept either a
// token or a session.
func IdentityFrom(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(identityKey).(Identity)
	return id, ok
}

func withIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, identityKey, id)
}
