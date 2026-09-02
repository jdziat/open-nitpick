package vcs

import (
	"fmt"
	"regexp"
	"strings"
)

// Markers are HTML comments appended to what this tool publishes, so a later
// run can read its own earlier output back off the pull request without a
// database. GitHub renders none of them.
//
// The bot marker alone says "this tool wrote this"; the two below say WHAT it
// wrote: which finding a comment reports, and which revision a review looked
// at. Both are needed for a push to be reviewed incrementally — the head to
// know what was already read, the fingerprints to know what was already said.

// fingerprintMarker records the finding a comment reports.
func fingerprintMarker(fingerprint, class string) string {
	if fingerprint == "" {
		return ""
	}
	return fmt.Sprintf("<!-- open-nitpick fp:%s class:%s -->", fingerprint, class)
}

// headMarker records the revision a review looked at.
func headMarker(sha string) string {
	if strings.TrimSpace(sha) == "" {
		return ""
	}
	return fmt.Sprintf("<!-- open-nitpick head:%s -->", sha)
}

var (
	fingerprintPattern = regexp.MustCompile(`<!-- open-nitpick fp:([0-9a-f]+) class:([a-z_-]*) -->`)
	headPattern        = regexp.MustCompile(`<!-- open-nitpick head:([0-9a-fA-F]+) -->`)
)

// parseFingerprint reads a comment's fingerprint marker back.
func parseFingerprint(body string) (fingerprint, class string, ok bool) {
	m := fingerprintPattern.FindStringSubmatch(body)
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

// parseHead reads a review body's head marker back.
func parseHead(body string) (string, bool) {
	m := headPattern.FindStringSubmatch(body)
	if m == nil {
		return "", false
	}
	return m[1], true
}
