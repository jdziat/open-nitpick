package redact

import (
	"encoding/base64"
	"strings"
	"testing"
)

// Fixture tokens are decoded at runtime so static secret scanners (gosec,
// gitleaks) do not treat this test source as a committed credential. The
// shapes still exercise Secrets.

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

func TestSecretsRemovesRawLookingSecrets(t *testing.T) {
	// Mutation: strip redaction and the raw token would remain (plan test 8).
	awsKey := fixtureAWSKey(t)
	pem := fixturePEM(t)
	pemBody := strings.SplitN(pem, "\n", 2)[1]
	if i := strings.Index(pemBody, "\n"); i > 0 {
		pemBody = pemBody[:i]
	}
	api := "api_key=ghp_" + strings.Repeat("Z", 36)
	highBytes, err := base64.StdEncoding.DecodeString("WGg3a1AybU45cVI0c1Q2dVY4d1kwekExYkMzZEU1Zkc3aEo5a0w=")
	if err != nil {
		t.Fatal(err)
	}
	high := string(highBytes)

	in := strings.Join([]string{
		"found " + awsKey + " in env",
		pem,
		api,
		"token " + high,
	}, "\n")
	out := Secrets(in)
	for _, secret := range []string{awsKey, pemBody, strings.Repeat("Z", 36), high} {
		if strings.Contains(out, secret) {
			t.Fatalf("raw secret %q still present in %q", secret, out)
		}
	}
	if !strings.Contains(out, "REDACTED") {
		t.Fatalf("expected REDACTED markers, got %q", out)
	}
}

func TestSecretsLeavesGitleaksRedactedMarkersAlone(t *testing.T) {
	in := "Secret: REDACTED was already scrubbed"
	if got := Secrets(in); got != in {
		t.Fatalf("gitleaks REDACTED marker changed: %q → %q", in, got)
	}
}

func TestSecretsScrubsSecretThatEmbedsRedactedMarker(t *testing.T) {
	// Mutation: overlapping protected spans used to skip any match that
	// merely contained the REDACTED substring, leaking the surrounding secret.
	secret := "ghp_" + strings.Repeat("A", 10) + "REDACTED" + strings.Repeat("B", 10)
	out := Secrets("leaked " + secret)
	if strings.Contains(out, "ghp_") || strings.Contains(out, strings.Repeat("B", 10)) {
		t.Fatalf("secret embedding REDACTED survived: %q", out)
	}
	if !strings.Contains(out, "REDACTED") {
		t.Fatalf("expected a REDACTED replacement, got %q", out)
	}
}

func TestSecretsScrubsGitLabPersonalAccessTokens(t *testing.T) {
	token := "glpat-" + strings.Repeat("x", 20)
	out := Secrets("auth " + token)
	if strings.Contains(out, "glpat-") || strings.Contains(out, strings.Repeat("x", 20)) {
		t.Fatalf("glpat token survived: %q", out)
	}
}

func TestSecretsScrubsLowercaseHexSecrets(t *testing.T) {
	// Mutation: drop isHexToken and a 32-char lowercase hex token clears only
	// two character classes, so looksHighEntropy returns false and the secret
	// ships in security output.
	hex := "a1b2c3d4e5f67890a1b2c3d4e5f67890"
	out := Secrets("session " + hex)
	if strings.Contains(out, hex) {
		t.Fatalf("lowercase hex secret survived: %q", out)
	}
	if !strings.Contains(out, "REDACTED") {
		t.Fatalf("expected REDACTED marker, got %q", out)
	}
}

func TestSecretsScrubsLowercaseAlphanumericSecrets(t *testing.T) {
	// Mutation: require three character classes and a lowercase+digit token
	// with non-hex letters (g/z) ships unredacted.
	raw, err := base64.StdEncoding.DecodeString("emd4a3F2bm1wbHdyeWpodDAxMjM0NTY3ODlhYmNkZWY=")
	if err != nil {
		t.Fatal(err)
	}
	token := string(raw)
	out := Secrets("key " + token)
	if strings.Contains(out, token) {
		t.Fatalf("lowercase alphanumeric secret survived: %q", out)
	}
}

func TestSecretsScrubsShortLabeledPasswords(t *testing.T) {
	// Mutation: keep the labeled capture at {16,} and password=abc12345 leaks.
	secret := "abc12345"
	out := Secrets("password=" + secret)
	if strings.Contains(out, secret) {
		t.Fatalf("short labeled password survived: %q", out)
	}
}

func TestSecretsScrubsOverlongAWSSecretSuffix(t *testing.T) {
	// Mutation: capture exactly 40 base64 chars and the suffix after an
	// over-length aws_secret value remains in security output.
	secret := strings.Repeat("A", 48) + "=="
	out := Secrets("aws_secret_access_key=" + secret)
	if strings.Contains(out, strings.Repeat("A", 8)) || strings.Contains(out, "==") {
		t.Fatalf("over-length aws secret suffix survived: %q", out)
	}
}
