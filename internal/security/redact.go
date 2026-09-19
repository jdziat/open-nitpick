package security

import (
	"math"
	"regexp"
	"sort"
	"strings"

	"github.com/jdziat/open-nitpick/internal/review"
)

const redacted = "REDACTED"

var (
	// Already-redacted placeholders from gitleaks --redact, left alone.
	alreadyRedacted = regexp.MustCompile(`REDACTED`)

	pemBlock = regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?-----END [A-Z0-9 ]*PRIVATE KEY-----`)

	awsAccessKey = regexp.MustCompile(`\b(AKIA|ASIA)[A-Z0-9]{16}\b`)

	// AWS-ish secret key material: base64 after a secret-shaped label.
	// {40,} not {40}: a longer value must be fully scrubbed, not leave a suffix.
	awsSecretLabeled = regexp.MustCompile(`(?i)(?:aws_secret_access_key|secret_access_key|aws_secret)["'=\s:]+([A-Za-z0-9/+=]{40,})`)

	apiKeyLabeled = regexp.MustCompile(`(?i)(?:api[_-]?key|access[_-]?token|auth[_-]?token|bearer|password|secret)["'=\s:]+([^\s"'\\]{8,})`)

	// Common vendor prefixes that are never ordinary prose.
	vendorKey = regexp.MustCompile(`\b(?:sk_live_|sk_test_|sk-)[A-Za-z0-9]{20,}\b|\bghp_[A-Za-z0-9]{20,}\b|\bgithub_pat_[A-Za-z0-9_]{20,}\b|\bglpat-[A-Za-z0-9_-]{20,}\b|\bxox[baprs]-[A-Za-z0-9-]{10,}\b|\bAIza[A-Za-z0-9_-]{30,}\b`)

	// High-entropy token: long mixed alphanumeric run unlikely to be prose.
	highEntropy = regexp.MustCompile(`\b[A-Za-z0-9+/=_-]{32,}\b`)
)

// RedactSecrets replaces credential-shaped and high-entropy spans with
// REDACTED. Existing gitleaks REDACTED markers are left unchanged when they
// are the entire matched span; a longer secret that merely contains that
// substring is still scrubbed.
func RedactSecrets(text string) string {
	if text == "" || text == redacted {
		return text
	}
	protected := alreadyRedacted.FindAllStringIndex(text, -1)
	var b strings.Builder
	last := 0
	for _, span := range redactSpans(text) {
		if isExactProtectedMarker(span, protected) {
			continue
		}
		if span[0] < last {
			continue
		}
		b.WriteString(text[last:span[0]])
		b.WriteString(redacted)
		last = span[1]
	}
	b.WriteString(text[last:])
	return b.String()
}

// RedactFinding redacts Title, Rationale, Suggestion, and Unresolved so secret
// material never reaches CLI, MCP, or CI output.
func RedactFinding(f *review.Finding) {
	if f == nil {
		return
	}
	f.Title = RedactSecrets(f.Title)
	f.Rationale = RedactSecrets(f.Rationale)
	f.Suggestion = RedactSecrets(f.Suggestion)
	f.Unresolved = RedactSecrets(f.Unresolved)
}

func redactSpans(text string) [][]int {
	var spans [][]int
	spans = append(spans, pemBlock.FindAllStringIndex(text, -1)...)
	spans = append(spans, awsAccessKey.FindAllStringIndex(text, -1)...)
	spans = append(spans, vendorKey.FindAllStringIndex(text, -1)...)
	spans = append(spans, captureSpans(awsSecretLabeled, text)...)
	spans = append(spans, captureSpans(apiKeyLabeled, text)...)
	for _, loc := range highEntropy.FindAllStringIndex(text, -1) {
		token := text[loc[0]:loc[1]]
		if token == redacted || !looksHighEntropy(token) {
			continue
		}
		spans = append(spans, loc)
	}
	return mergeSpans(spans)
}

func captureSpans(re *regexp.Regexp, text string) [][]int {
	var spans [][]int
	for _, m := range re.FindAllStringSubmatchIndex(text, -1) {
		if len(m) >= 4 && m[2] >= 0 {
			spans = append(spans, []int{m[2], m[3]})
		}
	}
	return spans
}

func looksHighEntropy(s string) bool {
	if len(s) < 32 {
		return false
	}
	// Pure hex clears only one or two character classes (digits + a–f), so the
	// mixed-class rule below would leave 128-bit (and longer) hex secrets in
	// findings. Treat hex as its own shape.
	if isHexToken(s) {
		return true
	}
	classes := charClasses(s)
	// Dual-class runs (e.g. lowercase + digits with g/z) are common unlabeled
	// API tokens; requiring three classes left them in security output.
	if classes >= 2 {
		return true
	}
	// Single-class lowercase alphabet soup still leaks when it is random-looking.
	return classes == 1 && shannonEntropy(s) >= 3.5
}

func charClasses(s string) int {
	var lower, upper, digit, other bool
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			lower = true
		case r >= 'A' && r <= 'Z':
			upper = true
		case r >= '0' && r <= '9':
			digit = true
		default:
			other = true
		}
	}
	n := 0
	for _, ok := range []bool{lower, upper, digit, other} {
		if ok {
			n++
		}
	}
	return n
}

func shannonEntropy(s string) float64 {
	if s == "" {
		return 0
	}
	var freq [256]int
	for i := 0; i < len(s); i++ {
		freq[s[i]]++
	}
	n := float64(len(s))
	var h float64
	for _, c := range freq {
		if c == 0 {
			continue
		}
		p := float64(c) / n
		h -= p * math.Log2(p)
	}
	return h
}

func isHexToken(s string) bool {
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f', r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}

func isExactProtectedMarker(span []int, protected [][]int) bool {
	for _, p := range protected {
		if span[0] == p[0] && span[1] == p[1] {
			return true
		}
	}
	return false
}

func mergeSpans(spans [][]int) [][]int {
	if len(spans) == 0 {
		return nil
	}
	sort.Slice(spans, func(i, j int) bool {
		if spans[i][0] != spans[j][0] {
			return spans[i][0] < spans[j][0]
		}
		return spans[i][1] < spans[j][1]
	})
	out := [][]int{{spans[0][0], spans[0][1]}}
	for _, s := range spans[1:] {
		last := out[len(out)-1]
		if s[0] <= last[1] {
			if s[1] > last[1] {
				last[1] = s[1]
			}
			continue
		}
		out = append(out, []int{s[0], s[1]})
	}
	return out
}
