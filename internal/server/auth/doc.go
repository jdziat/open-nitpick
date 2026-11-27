// Package auth holds the sign-in providers of the nitpick server: a local
// password, OAuth 2.0 / OIDC, and SAML 2.0. Each implements server.Provider and
// finishes by handing the server an identity; none of them grants a role, which
// is the server's mapping to make.
package auth
