package upgrade

import (
	"context"
	"strings"
	"testing"
)

const sums = "" +
	"1111111111111111111111111111111111111111111111111111111111111111  nitpick_v4.0.0_darwin_arm64\n" +
	"2222222222222222222222222222222222222222222222222222222222222222  nitpick_v4.0.0_linux_amd64\n" +
	"3333333333333333333333333333333333333333333333333333333333333333  nitpick_v4.0.0_linux_arm64\n" +
	"4444444444444444444444444444444444444444444444444444444444444444  nitpick_v4.0.0_windows_amd64.exe\n" +
	"5555555555555555555555555555555555555555555555555555555555555555  NITPICK_V9.9.9_LINUX_AMD64\n"

// The latest version is read from the line for this platform, not the first
// line and not another platform's.
func TestTheLatestVersionComesFromThisPlatformsAsset(t *testing.T) {
	rel := newFakeRelease(t)
	rel.set("/latest/checksums.txt", []byte(sums))
	for _, tc := range []struct{ goos, goarch string }{
		{"linux", "amd64"}, {"linux", "arm64"}, {"darwin", "arm64"}, {"windows", "amd64"},
	} {
		in := newInstall(t, rel, "v1.0.0")
		in.cfg.GOOS, in.cfg.GOARCH, in.cfg.Check = tc.goos, tc.goarch, true
		res, err := Run(context.Background(), in.cfg)
		if err != nil || res.Latest != "v4.0.0" {
			t.Errorf("%s/%s: latest = %q, %v; want v4.0.0", tc.goos, tc.goarch, res.Latest, err)
		}
	}
}

// Rule 10: a release with no build for this platform is refused for that
// reason, and a differently cased name is another file's.
func TestALatestReleaseWithNoBuildForThisPlatformIsRefused(t *testing.T) {
	rel := newFakeRelease(t)
	rel.set("/latest/checksums.txt", []byte(
		"1111111111111111111111111111111111111111111111111111111111111111  nitpick_v4.0.0_darwin_arm64\n"+
			"5555555555555555555555555555555555555555555555555555555555555555  NITPICK_V9.9.9_LINUX_AMD64\n"))
	in := newInstall(t, rel, "v1.0.0")
	in.cfg.Check = true
	if _, err := Run(context.Background(), in.cfg); err == nil || !strings.Contains(err.Error(), "no build for linux/amd64") {
		t.Errorf("err = %v, want the missing platform named", err)
	}
}

func TestAPlatformWithNoReleaseBuildIsRefusedBeforeTheNetwork(t *testing.T) {
	rel := newFakeRelease(t)
	for _, tc := range []struct{ goos, goarch string }{{"windows", "arm64"}, {"freebsd", "amd64"}, {"linux", "386"}} {
		in := newInstall(t, rel, "v1.0.0")
		in.cfg.GOOS, in.cfg.GOARCH = tc.goos, tc.goarch
		if _, err := Run(context.Background(), in.cfg); err == nil || !strings.Contains(err.Error(), "no release build") {
			t.Errorf("%s/%s: err = %v, want it refused as unsupported", tc.goos, tc.goarch, err)
		}
	}
	if rel.requests() != 0 {
		t.Error("an unsupported platform still went to the network")
	}
}

func TestAMissingLatestChecksumsFileIsRefused(t *testing.T) {
	rel := newFakeRelease(t)
	in := newInstall(t, rel, "v1.0.0")
	if _, err := Run(context.Background(), in.cfg); err == nil || !strings.Contains(err.Error(), "find the latest release") {
		t.Errorf("err = %v, want the lookup failure named", err)
	}
}

// Over a network that can be rewritten, the way to be served an older release
// is to be served an older release's checksums. HTTPS only, but this machine's
// own server is fine.
func TestAPlainHTTPReleaseSourceIsRefusedExceptOnThisMachine(t *testing.T) {
	// Each is refused for being a bad source, before any request: a base that
	// was wrongly accepted would fail later on the network, and an error alone
	// would not tell the two apart.
	for _, base := range []string{
		"http://example.com/dl", "ftp://example.com/dl", "example.com/dl",
		"http://127.0.0.1.example.com/dl", "http://localhost.example.com/dl",
	} {
		for _, which := range []string{"release", "latest"} {
			rel := newFakeRelease(t)
			in := newInstall(t, rel, "v1.0.0")
			if which == "release" {
				in.cfg.ReleaseBase = base
			} else {
				in.cfg.LatestBase = base
			}
			_, err := Run(context.Background(), in.cfg)
			msg := ""
			if err != nil {
				msg = err.Error()
			}
			if !strings.Contains(msg, "HTTPS") && !strings.Contains(msg, "not a release URL") {
				t.Errorf("%s base %s: err = %v, want it refused as a source", which, base, err)
			}
			if rel.requests() != 0 {
				t.Errorf("%s base %s: a refused source still went to the network", which, base)
			}
		}
	}
	rel := newFakeRelease(t)
	rel.publish("v2.0.0", script("v2.0.0"))
	rel.setLatest("v2.0.0")
	in := newInstall(t, rel, "v2.0.0")
	if _, err := Run(context.Background(), in.cfg); err != nil {
		t.Errorf("a loopback source was refused: %v", err)
	}
}

func TestAnOversizedSmallFileIsRefused(t *testing.T) {
	rel := newFakeRelease(t)
	rel.set("/latest/checksums.txt", []byte(strings.Repeat("a", maxSmall+10)))
	in := newInstall(t, rel, "v1.0.0")
	if _, err := Run(context.Background(), in.cfg); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Errorf("err = %v, want the size named", err)
	}
}

func TestAnEmptyDownloadIsRefused(t *testing.T) {
	rel := newFakeRelease(t)
	rel.publish("v2.0.0", nil)
	rel.setLatest("v2.0.0")
	rel.set(assetPath("v2.0.0"), []byte{})
	in := newInstall(t, rel, "v1.0.0")
	before := snapshot(t, in.dir)
	if _, err := Run(context.Background(), in.cfg); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Errorf("err = %v, want the empty download named", err)
	}
	if snapshot(t, in.dir) != before {
		t.Error("the install directory changed")
	}
}

func TestACancelledUpgradeStopsAndLeavesTheBinary(t *testing.T) {
	rel := newFakeRelease(t)
	rel.publish("v2.0.0", script("v2.0.0"))
	rel.setLatest("v2.0.0")
	in := newInstall(t, rel, "v1.0.0")
	before := snapshot(t, in.dir)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Run(ctx, in.cfg); err == nil {
		t.Fatal("a cancelled upgrade succeeded")
	}
	if snapshot(t, in.dir) != before {
		t.Error("the install directory changed")
	}
}

func TestAnUpgradeWithNoVerifierIsRefused(t *testing.T) {
	rel := newFakeRelease(t)
	rel.publish("v2.0.0", script("v2.0.0"))
	rel.setLatest("v2.0.0")
	in := newInstall(t, rel, "v1.0.0")
	in.cfg.NewVerifier = nil
	before := snapshot(t, in.dir)
	if _, err := Run(context.Background(), in.cfg); err == nil || !strings.Contains(err.Error(), "no signature verifier") {
		t.Errorf("err = %v, want the missing verifier named", err)
	}
	if snapshot(t, in.dir) != before {
		t.Error("the install directory changed")
	}
}

func TestTheVerifierIsBuiltWithTheContextTheUpgradeWasRunWith(t *testing.T) {
	rel := newFakeRelease(t)
	rel.publish("v2.0.0", script("v2.0.0"))
	rel.setLatest("v2.0.0")
	in := newInstall(t, rel, "v1.0.0")
	type key struct{}
	ctx := context.WithValue(context.Background(), key{}, "this command")
	var seen context.Context
	in.cfg.NewVerifier = func(c context.Context) (Verifier, error) {
		seen = c
		return in.verifier, nil
	}
	if _, err := Run(ctx, in.cfg); err != nil {
		t.Fatal(err)
	}
	if seen == nil || seen.Value(key{}) != "this command" {
		t.Fatal("NewVerifier was given a context that is not the upgrade's own, so cancelling the command cannot reach the trusted-root fetch")
	}
}
