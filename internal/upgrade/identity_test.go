package upgrade

import (
	"crypto/sha256"
	"testing"

	"github.com/sigstore/sigstore-go/pkg/testing/ca"
	"github.com/sigstore/sigstore-go/pkg/tlog"
)

const releaseSAN = "https://github.com/nitpick-oss/nitpick/.github/workflows/release.yml@refs/tags/v9.9.9"

// signedAs signs an artifact under a certificate for the given identity, in a
// virtual Sigstore that the verifier is told to trust, and returns the verdict.
// Every failure below is a valid signature that the policy refuses.
func signedAs(t *testing.T, san, issuer string, artifact, claimed []byte) error {
	t.Helper()
	vs, err := ca.NewVirtualSigstore()
	if err != nil {
		t.Fatal(err)
	}
	entity, err := vs.Sign(san, issuer, artifact)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(claimed)
	return NewSigstoreVerifier(vs).verifyEntity(entity, digest[:])
}

// Rule 10: the accepted case, so each refusal below is the identity's doing
// and not a verifier that refuses everything.
func TestTheReleaseWorkflowsSignatureIsAccepted(t *testing.T) {
	artifact := []byte("the binary")
	if err := signedAs(t, releaseSAN, ReleaseIssuer, artifact, artifact); err != nil {
		t.Fatalf("the release workflow's own signature was refused: %v", err)
	}
}

// A signature can be perfectly valid and still not be ours. Each case is a
// certificate Sigstore would issue to somebody, which the policy must refuse.
func TestASignatureFromAnyoneButTheReleaseWorkflowIsRefused(t *testing.T) {
	artifact := []byte("the binary")
	for name, tc := range map[string]struct{ san, issuer string }{
		"another workflow in this repository":      {"https://github.com/nitpick-oss/nitpick/.github/workflows/ci.yml@refs/tags/v9.9.9", ReleaseIssuer},
		"the release workflow on a branch":         {"https://github.com/nitpick-oss/nitpick/.github/workflows/release.yml@refs/heads/main", ReleaseIssuer},
		"a repository whose name starts with ours": {"https://github.com/nitpick-oss/nitpick-fork/.github/workflows/release.yml@refs/tags/v9.9.9", ReleaseIssuer},
		"another owner":                            {"https://github.com/someone/nitpick/.github/workflows/release.yml@refs/tags/v9.9.9", ReleaseIssuer},
		"a personal account":                       {"someone@example.com", "https://accounts.google.com"},
		"our identity from another issuer":         {releaseSAN, "https://accounts.google.com"},
		"a tag that is not a version":              {"https://github.com/nitpick-oss/nitpick/.github/workflows/release.yml@refs/tags/latest", ReleaseIssuer},
		"our identity with a suffix after the tag": {releaseSAN + "/../../evil.yml@refs/heads/x", ReleaseIssuer},
		"a path that only ends like ours":          {"https://github.com/nitpick-oss/nitpick/.github/workflows/release.yml/../x.yml@refs/tags/v1", ReleaseIssuer},
	} {
		if err := signedAs(t, tc.san, tc.issuer, artifact, artifact); err == nil {
			t.Errorf("%s: a valid signature by %q was accepted", name, tc.san)
		}
	}
}

// The signature vouches for one artifact. The same release identity signing
// something else does not vouch for this file.
func TestASignatureForOtherBytesIsRefused(t *testing.T) {
	if err := signedAs(t, releaseSAN, ReleaseIssuer, []byte("the binary"), []byte("a different binary")); err == nil {
		t.Error("a signature over other bytes verified this artifact")
	}
}

// A root that does not contain the signing CA trusts nothing it signed.
func TestASignatureFromAnUntrustedAuthorityIsRefused(t *testing.T) {
	signer, err := ca.NewVirtualSigstore()
	if err != nil {
		t.Fatal(err)
	}
	other, err := ca.NewVirtualSigstore()
	if err != nil {
		t.Fatal(err)
	}
	artifact := []byte("the binary")
	entity, err := signer.Sign(releaseSAN, ReleaseIssuer, artifact)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(artifact)
	if err := NewSigstoreVerifier(signer).verifyEntity(entity, digest[:]); err != nil {
		t.Fatalf("the signer's own root refused it: %v", err)
	}
	if err := NewSigstoreVerifier(other).verifyEntity(entity, digest[:]); err == nil {
		t.Error("a root that does not hold the signing authority accepted its signature")
	}
}

// withoutLogEntries is an entity whose transparency log entries have been
// removed. Its signature, certificate and timestamp are the genuine ones.
type withoutLogEntries struct{ *ca.TestEntity }

func (withoutLogEntries) TlogEntries() ([]*tlog.Entry, error) { return nil, nil }

// A signature that was never recorded in the transparency log cannot be
// audited later, so the log entry is required and not merely tolerated.
func TestASignatureWithNoTransparencyLogEntryIsRefused(t *testing.T) {
	vs, err := ca.NewVirtualSigstore()
	if err != nil {
		t.Fatal(err)
	}
	artifact := []byte("the binary")
	entity, err := vs.Sign(releaseSAN, ReleaseIssuer, artifact)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(artifact)
	verifier := NewSigstoreVerifier(vs)
	if err := verifier.verifyEntity(entity, digest[:]); err != nil {
		t.Fatalf("the entity with its log entry was refused: %v", err)
	}
	if err := verifier.verifyEntity(withoutLogEntries{entity}, digest[:]); err == nil {
		t.Error("an entity with no transparency log entry verified")
	}
}
