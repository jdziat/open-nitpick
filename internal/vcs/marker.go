package vcs

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Markers are HTML comments appended to what this tool publishes, so a later
// run can read its own earlier output back off the pull request without a
// database. GitHub renders none of them.
//
// The bot marker alone says "this tool wrote this"; the two below say WHAT it
// wrote: which finding a comment reports, and which revision a review looked
// at. Both are needed for a push to be reviewed incrementally, the head to
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

// spendMarker records what a review was estimated to cost, so a ceiling
// covering a whole pull request can subtract it on the next push. A CI job
// keeps nothing between runs; the pull request is where the total lives.
//
// Nothing is written below a cent's worth of nothing: a marker reading zero
// cannot be told from one left by a run whose estimate failed, and the next
// run would subtract a number nobody computed.
func spendMarker(dollars float64) string {
	if dollars <= 0 {
		return ""
	}
	return fmt.Sprintf("<!-- open-nitpick spend:%.6f -->", dollars)
}

var (
	fingerprintPattern = regexp.MustCompile(`<!-- open-nitpick fp:([0-9a-f]+) class:([a-z_-]*) -->`)
	headPattern        = regexp.MustCompile(`<!-- open-nitpick head:([0-9a-fA-F]+) -->`)
	// Six decimal places, because that is what spendMarker writes. A looser
	// pattern would read a hand-written "spend:1" out of a body somebody else
	// authored and subtract a dollar nobody spent.
	spendPattern = regexp.MustCompile(`<!-- open-nitpick spend:([0-9]+\.[0-9]{6}) -->`)
)

// parseSpend reads a review body's spend marker back.
func parseSpend(body string) (float64, bool) {
	m := spendPattern.FindStringSubmatch(body)
	if m == nil {
		return 0, false
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil || v <= 0 {
		return 0, false
	}
	return v, true
}

// FingerprintOf reads the finding identifier out of a published comment, or
// returns "" when the comment carries none.
//
// Exported because a caller acting on a comment needs the same identity the
// review gave it, and the marker's shape lives here.
func FingerprintOf(body string) string {
	fp, _, _ := parseFingerprint(body)
	return fp
}

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

// completionMarker distinguishes a reusable review from legacy or failed runs.
func completionMarker(sha string) string {
	return fmt.Sprintf("<!-- open-nitpick complete:%s -->", sha)
}

// stripMarkers keeps model-authored prose from supplying persisted review state.
func stripMarkers(body string) string {
	for _, pattern := range []*regexp.Regexp{headPattern, spendPattern, fingerprintPattern, completionPattern} {
		body = pattern.ReplaceAllString(body, "")
	}
	return body
}

var completionPattern = regexp.MustCompile(`<!-- open-nitpick complete:([0-9a-fA-F]+) -->`)
