// Package upgrade replaces the running nitpick binary with a release.
//
// A release is trusted when three things agree: the asset's SHA-256 is the one
// checksums.txt lists for it, the asset's Sigstore bundle verifies, and the
// bundle was signed by this repository's release workflow. Nothing is written
// where the old binary lives until all three have passed, so a refusal leaves
// the installed binary byte for byte as it was.
package upgrade
