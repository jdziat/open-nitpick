package upgrade

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/sigstore/sigstore-go/pkg/testing/ca"
	"github.com/sigstore/sigstore-go/pkg/verify"
)

// virtualVerifier runs the real policy in Run. A virtual Sigstore's entities
// cannot be written as a bundle file, so the "bundle" the fake release serves
// is the name of the entity, and the verdict comes from SigstoreVerifier.
type virtualVerifier struct {
	real     *SigstoreVerifier
	entities map[string]verify.SignedEntity
}

func (v *virtualVerifier) Verify(bundle, digest []byte) error {
	entity, ok := v.entities[string(bundle)]
	if !ok {
		return errors.New("no such bundle")
	}
	return v.real.verifyEntity(entity, digest)
}

// The whole upgrade with the real identity policy: a signature by the release
// workflow installs, and a valid signature by anyone else does not, with the
// checksum right in both. This is the property the upgrade exists to keep.
func TestOnlyTheReleaseWorkflowsSignatureInstallsAnUpgrade(t *testing.T) {
	vs, err := ca.NewVirtualSigstore()
	if err != nil {
		t.Fatal(err)
	}
	asset := script("v2.0.0")
	digest := sha256.Sum256(asset)

	for name, tc := range map[string]struct {
		san, issuer string
		installs    bool
	}{
		"the release workflow":  {releaseSAN, ReleaseIssuer, true},
		"another workflow":      {"https://github.com/nitpick-oss/nitpick/.github/workflows/ci.yml@refs/tags/v2.0.0", ReleaseIssuer, false},
		"a fork of the project": {"https://github.com/someone/nitpick/.github/workflows/release.yml@refs/tags/v2.0.0", ReleaseIssuer, false},
		"a person":              {"someone@example.com", "https://accounts.google.com", false},
	} {
		t.Run(name, func(t *testing.T) {
			entity, err := vs.Sign(tc.san, tc.issuer, asset)
			if err != nil {
				t.Fatal(err)
			}
			rel := newFakeRelease(t)
			rel.publish("v2.0.0", asset)
			rel.setLatest("v2.0.0")
			rel.set(assetPath("v2.0.0")+".sigstore.json", []byte("entity"))
			in := newInstall(t, rel, "v1.0.0")
			in.cfg.NewVerifier = func(context.Context) (Verifier, error) {
				return &virtualVerifier{real: NewSigstoreVerifier(vs), entities: map[string]verify.SignedEntity{"entity": entity}}, nil
			}
			before := snapshot(t, in.dir)

			res, err := Run(context.Background(), in.cfg)
			if tc.installs {
				if err != nil || !res.Installed {
					t.Fatalf("the release workflow's signature was refused: %+v %v", res, err)
				}
				if got := contents(t, in.target); got != string(asset) {
					t.Error("the verified binary is not the one in place")
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "signature verification failed") {
				t.Fatalf("err = %v, want %s refused as a signer", err, name)
			}
			if after := snapshot(t, in.dir); after != before {
				t.Errorf("a refused signer changed the install directory:\nbefore:\n%s\nafter:\n%s", before, after)
			}
			// The checksum was right: the digest the signature covers is the file's.
			if sum(asset) != hexOf(digest[:]) {
				t.Fatal("test setup: the digests differ")
			}
		})
	}
}

func hexOf(b []byte) string { return fmt.Sprintf("%x", b) }
