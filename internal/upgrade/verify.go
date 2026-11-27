package upgrade

import (
	"errors"
	"fmt"

	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/verify"
)

// The identity a release is signed under: this repository's release workflow,
// run by GitHub Actions for a version tag. A signature by any other workflow,
// repository or issuer is refused, however valid it is as a signature.
const (
	ReleaseIssuer   = "https://token.actions.githubusercontent.com"
	ReleaseIdentity = `^https://github\.com/nitpick-oss/nitpick/\.github/workflows/release\.yml@refs/tags/v[0-9][A-Za-z0-9.+-]*$`
)

// Verifier decides whether a Sigstore bundle vouches for an artifact. The
// artifact is given as its SHA-256 digest, which the caller has already
// computed while downloading it.
type Verifier interface {
	Verify(bundleJSON, sha256Digest []byte) error
}

// SigstoreVerifier checks a bundle against a trusted root: the certificate
// chain to Fulcio, the signature, the transparency log entry and its
// timestamp, and the release identity.
type SigstoreVerifier struct {
	trusted root.TrustedMaterial
}

// NewSigstoreVerifier returns a verifier that trusts the given root.
func NewSigstoreVerifier(trusted root.TrustedMaterial) *SigstoreVerifier {
	return &SigstoreVerifier{trusted: trusted}
}

// FetchTrustedRoot downloads Sigstore's public-good trusted root through its
// TUF repository, which is signed and rolled forward from a root embedded in
// the library. It needs the network; the upgrade has just used it.
func FetchTrustedRoot() (root.TrustedMaterial, error) {
	tr, err := root.FetchTrustedRoot()
	if err != nil {
		return nil, fmt.Errorf("fetch the Sigstore trusted root: %w", err)
	}
	return tr, nil
}

// Verify implements Verifier.
func (v *SigstoreVerifier) Verify(bundleJSON, sha256Digest []byte) error {
	if v == nil || v.trusted == nil {
		return errors.New("no trusted root to verify against")
	}
	var b bundle.Bundle
	if err := b.UnmarshalJSON(bundleJSON); err != nil {
		return fmt.Errorf("read the signature bundle: %w", err)
	}
	return v.verifyEntity(&b, sha256Digest)
}

func (v *SigstoreVerifier) verifyEntity(entity verify.SignedEntity, sha256Digest []byte) error {
	verifier, err := verify.NewVerifier(v.trusted,
		verify.WithTransparencyLog(1), verify.WithObserverTimestamps(1))
	if err != nil {
		return fmt.Errorf("build the verifier: %w", err)
	}
	id, err := verify.NewShortCertificateIdentity(ReleaseIssuer, "", "", ReleaseIdentity)
	if err != nil {
		return fmt.Errorf("build the release identity: %w", err)
	}
	policy := verify.NewPolicy(verify.WithArtifactDigest("sha256", sha256Digest), verify.WithCertificateIdentity(id))
	if _, err := verifier.Verify(entity, policy); err != nil {
		return fmt.Errorf("signature verification failed: %w", err)
	}
	return nil
}
