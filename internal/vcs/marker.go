package vcs

import (
	"encoding/base64"
	"encoding/json"
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

// severityMarker records the severity of the finding a comment reports, so a
// later run can tell whether a thread it did not re-read still stands at a
// level that matters.
//
// A marker of its own rather than a field in the fingerprint marker: a
// release that predates it parses that marker with an exact pattern, and a
// workflow pinned to such a release would stop recognising every new thread.
func severityMarker(severity string) string {
	if severity == "" {
		return ""
	}
	return fmt.Sprintf("<!-- open-nitpick sev:%s -->", severity)
}

// roundMarker records that a review came from a run whose pipeline finished,
// which is what makes it a round of the series. It is separate from the
// completion marker because that one also needs every file covered, and a pull
// request with one oversized file would then never count a round.
const roundMarker = "<!-- open-nitpick round -->"

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
	severityPattern    = regexp.MustCompile(`<!-- open-nitpick sev:([a-z]+) -->`)
	roundPattern       = regexp.MustCompile(regexp.QuoteMeta(roundMarker))
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
//
// The last marker is the one read. Markers are appended after the comment's
// text, so anything earlier in the body is text that survived stripping, and
// reading it first would let that text speak for the comment.
func parseFingerprint(body string) (fingerprint, class string, ok bool) {
	m := lastMatch(fingerprintPattern, body)
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

// parseSeverity reads a comment's severity marker back, or "" for a comment
// published before comments carried one.
func parseSeverity(body string) string {
	if m := lastMatch(severityPattern, body); m != nil {
		return m[1]
	}
	return ""
}

func lastMatch(re *regexp.Regexp, body string) []string {
	all := re.FindAllStringSubmatch(body, -1)
	if len(all) == 0 {
		return nil
	}
	return all[len(all)-1]
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
//
// It repeats until nothing changes, because one pass can assemble a marker:
// removing one nested inside another leaves the outer one whole.
func stripMarkers(body string) string {
	patterns := []*regexp.Regexp{headPattern, spendPattern, fingerprintPattern, severityPattern,
		roundPattern, completionPattern, progressPattern}
	for {
		before := body
		for _, pattern := range patterns {
			body = pattern.ReplaceAllString(body, "")
		}
		if body == before {
			return body
		}
	}
}

var completionPattern = regexp.MustCompile(`<!-- open-nitpick complete:([0-9a-fA-F]+) -->`)

// MaxProgressBytes bounds persisted results before base64 encoding.
const MaxProgressBytes = 24000

var progressPattern = regexp.MustCompile(`<!-- open-nitpick progress:([A-Za-z0-9+/=]+) -->`)

func progressMarker(data json.RawMessage) string {
	if len(data) == 0 || len(data) > MaxProgressBytes || !json.Valid(data) {
		return ""
	}
	return "<!-- open-nitpick progress:" + base64.StdEncoding.EncodeToString(data) + " -->"
}

func parseProgress(body string) json.RawMessage {
	matches := progressPattern.FindAllStringSubmatch(body, 2)
	if len(matches) != 1 || len(matches[0][1]) > base64.StdEncoding.EncodedLen(MaxProgressBytes) {
		return nil
	}
	data, err := base64.StdEncoding.DecodeString(matches[0][1])
	if err != nil || !json.Valid(data) {
		return nil
	}
	return data
}
