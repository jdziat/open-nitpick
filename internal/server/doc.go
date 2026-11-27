// Package server hosts nitpick's hosted components behind one listener, one
// sign-in and one web interface.
//
// A component owns its routes, its storage and its pages. The server owns what
// is shared: authentication, sessions, the mapping from an identity to roles,
// request protection (CSRF, security headers), the UI shell and health. A
// component says which role each of its routes needs, and the server enforces
// it, so a component cannot forget to.
//
// What the server renders is untrusted by construction: a component's pages are
// html/template, and the text in them comes from code the trust model treats as
// hostile (diffs, model output). The shell never marks anything as safe HTML.
package server
