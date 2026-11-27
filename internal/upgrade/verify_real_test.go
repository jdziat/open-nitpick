package upgrade

import (
	"crypto/sha256"
	"os"
	"strings"
	"testing"

	"github.com/sigstore/sigstore-go/pkg/root"
)

// The real thing, offline: the checksums.txt of v2.10.0 with the bundle its
// release workflow signed it with, against the public trusted root as it was
// exported. This is what the identity regexp has to accept, and it is the only
// test here that is not a model of Sigstore.
func realVerifier(t *testing.T) *SigstoreVerifier {
	t.Helper()
	tr, err := root.NewTrustedRootFromPath("testdata/trusted_root.json")
	if err != nil {
		t.Fatal(err)
	}
	return NewSigstoreVerifier(tr)
}

func TestARealReleaseBundleVerifiesAgainstThePublicTrustedRoot(t *testing.T) {
	sums, err := os.ReadFile("testdata/v2.10.0.checksums.txt")
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := os.ReadFile("testdata/v2.10.0.checksums.txt.sigstore.json")
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(sums)
	if err := realVerifier(t).Verify(bundle, digest[:]); err != nil {
		t.Fatalf("a real v2.10.0 bundle was refused: %v", err)
	}

	// Rule 10: the refusals below are for the reason named, so the same
	// bundle and the same verifier must have accepted the true artifact first
	// (above), and then refuse an altered one.
	altered := sha256.Sum256([]byte(strings.Replace(string(sums), "nitpick_v2.10.0_linux_amd64", "nitpick_v2.10.0_linux_amd65", 1)))
	if err := realVerifier(t).Verify(bundle, altered[:]); err == nil {
		t.Error("a bundle verified an artifact it did not sign")
	}
}

func TestABundleThatIsNotABundleIsRefused(t *testing.T) {
	digest := sha256.Sum256([]byte("x"))
	for name, junk := range map[string]string{"empty": "", "not json": "nope", "wrong shape": `{"mediaType":"x"}`} {
		if err := realVerifier(t).Verify([]byte(junk), digest[:]); err == nil {
			t.Errorf("%s: a malformed bundle verified", name)
		}
	}
}

func TestAVerifierWithNoTrustedRootRefusesEverything(t *testing.T) {
	digest := sha256.Sum256([]byte("x"))
	if err := (*SigstoreVerifier)(nil).Verify([]byte("{}"), digest[:]); err == nil {
		t.Error("a nil verifier verified")
	}
	if err := NewSigstoreVerifier(nil).Verify([]byte("{}"), digest[:]); err == nil {
		t.Error("a verifier with no root verified")
	}
}
