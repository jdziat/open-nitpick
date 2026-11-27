package security

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/nitpick-oss/nitpick/internal/redact"
	"github.com/nitpick-oss/nitpick/internal/review"
)

// Fixture tokens are built at runtime so static secret scanners do not treat this
// test source as a committed credential.

func fixtureAWSKey(t *testing.T) string {
	t.Helper()
	// Built at runtime (no contiguous AKIA… literal, no base64 of one) so push
	// protection and static scanners do not treat the test source as a key.
	return strings.ToUpper("akia") + strings.Repeat("9", 16)
}

func fixturePEM(t *testing.T) string {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString("LS0tLS0tQkVHSU4gUlNBIFBSSVZBVEUgS0VZLS0tLS0tCk1JSUVvd0lCQUFLQ0FRRUEwWjNWUzVKSmNkczN4Zm4veWdXeUY2UAotLS0tLUVORCBSU0EgUFJJVkFURSBLRVktLS0tLQ==")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRedactFindingScrubsMessageLikeFields(t *testing.T) {
	gh := "ghp_" + strings.Repeat("Q", 36)
	highBytes, err := base64.StdEncoding.DecodeString("WGg3a1AybU45cVI0c1Q2dVY4d1kwekExYkMzZEU1Zkc3aEo5a0w=")
	if err != nil {
		t.Fatal(err)
	}
	high := string(highBytes)
	f := &review.Finding{
		Title:      "leak " + fixtureAWSKey(t),
		Rationale:  "key " + gh + " in log",
		Suggestion: "rotate " + high,
		Unresolved: "still seeing password=" + strings.Repeat("s", 12) + "value99",
	}
	RedactFinding(f)
	for _, field := range []string{f.Title, f.Rationale, f.Suggestion, f.Unresolved} {
		if strings.Contains(field, fixtureAWSKey(t)) ||
			strings.Contains(field, strings.Repeat("Q", 36)) ||
			strings.Contains(field, high) ||
			strings.Contains(field, strings.Repeat("s", 12)+"value99") {
			t.Fatalf("finding field still holds raw secret: %q", field)
		}
	}
}

func TestRedactFindingScrubsTheCitationAndCategory(t *testing.T) {
	gh := "ghp_" + strings.Repeat("Q", 36)
	f := &review.Finding{
		Title:    "token committed",
		Category: "leaks " + gh,
		Citation: `const token = "` + gh + `"`,
	}
	RedactFinding(f)
	if strings.Contains(f.Citation, gh) {
		t.Fatalf("a secret on the cited line survived: %q", f.Citation)
	}
	if strings.Contains(f.Category, gh) {
		t.Fatalf("a secret in the category survived: %q", f.Category)
	}
	if !strings.Contains(f.Citation, "const token") {
		t.Fatalf("redaction removed the quote around the secret: %q", f.Citation)
	}
}

// security.RedactSecrets is the shared scrubber, not a second one: the same input
// gives the same output as internal/redact, so a rule added there reaches here.
func TestRedactSecretsIsTheSharedScrubber(t *testing.T) {
	for _, in := range []string{"", "REDACTED", "plain prose", "token " + fixtureAWSKey(t), fixturePEM(t), "api_key=" + strings.Repeat("x", 20)} {
		if got, want := RedactSecrets(in), redact.Secrets(in); got != want {
			t.Errorf("RedactSecrets(%q) = %q, redact.Secrets gives %q", in, got, want)
		}
	}
	if got := RedactSecrets("key " + fixtureAWSKey(t)); strings.Contains(got, "AKIA") {
		t.Errorf("the delegate left a key in: %q", got)
	}
}
