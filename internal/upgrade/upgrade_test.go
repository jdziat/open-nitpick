package upgrade

import (
	"context"
	"os"
	"os/user"
	"runtime"
	"strings"
	"testing"
)

func run(t *testing.T, in *installed) (Result, error) {
	t.Helper()
	return Run(context.Background(), in.cfg)
}

// Already on the latest: exit clean, change nothing, say so.
func TestAlreadyOnTheLatestReleaseChangesNothing(t *testing.T) {
	rel := newFakeRelease(t)
	rel.publish("v2.0.0", script("v2.0.0"))
	rel.setLatest("v2.0.0")
	in := newInstall(t, rel, "v2.0.0")
	before := snapshot(t, in.dir)

	res, err := run(t, in)
	if err != nil {
		t.Fatal(err)
	}
	if res.Installed || res.Available {
		t.Errorf("result = %+v, want nothing installed and nothing available", res)
	}
	if !strings.Contains(in.out.String(), "up to date") {
		t.Errorf("output %q does not say it is up to date", in.out.String())
	}
	if after := snapshot(t, in.dir); after != before {
		t.Errorf("the install directory changed:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	// Rule 10: silent because it is current, not because it never looked.
	if rel.requested("/latest/checksums.txt") != 1 {
		t.Errorf("the latest release was looked up %d times, want once", rel.requested("/latest/checksums.txt"))
	}
	if rel.requested(".sigstore.json") != 0 || rel.requested("nitpick_v2.0.0_linux_amd64") != 0 {
		t.Error("an asset was downloaded for a binary that is already current")
	}
}

// A newer release replaces the binary and the new one runs.
func TestANewerReleaseReplacesTheBinaryAndTheNewOneRuns(t *testing.T) {
	rel := newFakeRelease(t)
	rel.publish("v2.0.0", script("v2.0.0"))
	rel.setLatest("v2.0.0")
	in := newInstall(t, rel, "v1.0.0")
	if got := ran(t, in.target); got != "nitpick v1.0.0" {
		t.Fatalf("before the upgrade the binary says %q", got)
	}

	res, err := run(t, in)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Installed || res.Latest != "v2.0.0" {
		t.Errorf("result = %+v, want v2.0.0 installed", res)
	}
	if got := ran(t, in.target); got != "nitpick v2.0.0" {
		t.Errorf("after the upgrade the binary says %q, want nitpick v2.0.0", got)
	}
	if info, err := os.Stat(in.target); err != nil || info.Mode().Perm()&0o111 == 0 {
		t.Errorf("the new binary is not executable: %v %v", info, err)
	}
	entries, _ := os.ReadDir(in.dir)
	if len(entries) != 1 {
		t.Errorf("the directory holds %d files after the swap, want only the binary", len(entries))
	}
	if in.verifier.calls != 1 {
		t.Errorf("the signature was verified %d times, want once", in.verifier.calls)
	}
	if !strings.Contains(in.out.String(), "releases/tag/v2.0.0") {
		t.Errorf("output %q does not name the release page", in.out.String())
	}
}

// -check reports and writes nothing: not to the install directory, not to the
// temporary one, and it asks for no asset.
func TestCheckNeverWritesAndNeverDownloadsAnAsset(t *testing.T) {
	rel := newFakeRelease(t)
	rel.publish("v2.0.0", script("v2.0.0"))
	rel.setLatest("v2.0.0")
	in := newInstall(t, rel, "v1.0.0")
	in.cfg.Check = true
	before := snapshot(t, in.dir)

	res, err := run(t, in)
	if err != nil {
		t.Fatal(err)
	}
	// Rule 10: it is silent about the install because it found a newer release
	// and was told not to take it, not because it found none.
	if !res.Available || res.Installed {
		t.Errorf("result = %+v, want a newer release available and none installed", res)
	}
	if !strings.Contains(in.out.String(), "v2.0.0 is available") {
		t.Errorf("output %q does not report the newer release", in.out.String())
	}
	if after := snapshot(t, in.dir); after != before {
		t.Errorf("-check changed the install directory:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if entries, _ := os.ReadDir(in.cfg.TempDir); len(entries) != 0 {
		t.Errorf("-check left %d entries in the temporary directory", len(entries))
	}
	if rel.requested("nitpick_v2.0.0_linux_amd64") != 0 || in.verifier.calls != 0 {
		t.Error("-check downloaded or verified an asset")
	}
}

func TestCheckSaysWhenThereIsNothingNewer(t *testing.T) {
	rel := newFakeRelease(t)
	rel.publish("v2.0.0", script("v2.0.0"))
	rel.setLatest("v2.0.0")
	in := newInstall(t, rel, "v2.0.0")
	in.cfg.Check = true
	res, err := run(t, in)
	if err != nil || res.Available {
		t.Fatalf("= %+v, %v; want nothing available", res, err)
	}
	if !strings.Contains(in.out.String(), "up to date") {
		t.Errorf("output %q", in.out.String())
	}
}

func TestCheckAndAPinnedVersionCannotBeCombined(t *testing.T) {
	rel := newFakeRelease(t)
	in := newInstall(t, rel, "v1.0.0")
	in.cfg.Check, in.cfg.Version = true, "v2.0.0"
	if _, err := run(t, in); err == nil || !strings.Contains(err.Error(), "-check") {
		t.Errorf("err = %v, want the combination refused by name", err)
	}
	if rel.requests() != 0 {
		t.Error("a refused combination still went to the network")
	}
}

// A checksum that does not match leaves the binary byte for byte as it was. The
// bundle is the genuine one for the bytes that were downloaded, so removing the
// checksum comparison lets this install, and the test goes red.
func TestAChecksumMismatchLeavesTheBinaryUntouched(t *testing.T) {
	rel := newFakeRelease(t)
	rel.publish("v2.0.0", script("v2.0.0"))
	rel.setLatest("v2.0.0")
	name, _ := assetFor("v2.0.0", platformOS, platformArch)
	rel.set("/dl/v2.0.0/checksums.txt", []byte(strings.Repeat("0", 64)+"  "+name+"\n"))
	in := newInstall(t, rel, "v1.0.0")
	before := snapshot(t, in.dir)

	_, err := run(t, in)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("err = %v, want a checksum mismatch", err)
	}
	if after := snapshot(t, in.dir); after != before {
		t.Errorf("a refused upgrade changed the install directory:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if in.verifier.calls != 0 {
		t.Error("the signature was checked on a file whose checksum had already failed")
	}
	if got := ran(t, in.target); got != "nitpick v1.0.0" {
		t.Errorf("the installed binary says %q after a refusal", got)
	}
}

// A bundle that fails leaves the binary alone even when the checksum matches.
func TestABundleThatFailsVerificationLeavesTheBinaryUntouched(t *testing.T) {
	rel := newFakeRelease(t)
	rel.publish("v2.0.0", script("v2.0.0"))
	rel.setLatest("v2.0.0")
	rel.set(assetPath("v2.0.0")+".sigstore.json", []byte("signed:"+sum([]byte("some other file"))))
	in := newInstall(t, rel, "v1.0.0")
	before := snapshot(t, in.dir)

	_, err := run(t, in)
	if err == nil || !strings.Contains(err.Error(), "signature verification failed") {
		t.Fatalf("err = %v, want a signature failure", err)
	}
	if after := snapshot(t, in.dir); after != before {
		t.Errorf("a refused upgrade changed the install directory:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	// Rule 10: the checksum was right, so the refusal is the bundle's doing.
	if in.verifier.calls != 1 {
		t.Errorf("the verifier ran %d times, want once", in.verifier.calls)
	}
}

// A missing bundle is a refusal and not a skipped check.
func TestAMissingBundleRefusesTheUpgrade(t *testing.T) {
	rel := newFakeRelease(t)
	rel.publish("v2.0.0", script("v2.0.0"))
	rel.setLatest("v2.0.0")
	rel.drop(assetPath("v2.0.0") + ".sigstore.json")
	in := newInstall(t, rel, "v1.0.0")
	before := snapshot(t, in.dir)
	if _, err := run(t, in); err == nil || !strings.Contains(err.Error(), "signature bundle") {
		t.Fatalf("err = %v, want the missing bundle named", err)
	}
	if after := snapshot(t, in.dir); after != before {
		t.Error("the install directory changed")
	}
}

func TestAReleaseThatDoesNotListTheAssetIsRefused(t *testing.T) {
	rel := newFakeRelease(t)
	rel.publish("v2.0.0", script("v2.0.0"))
	rel.setLatest("v2.0.0")
	other := strings.Repeat("a", 64) + "  nitpick_v2.0.0_darwin_arm64\n"
	rel.set("/dl/v2.0.0/checksums.txt", []byte(other))
	in := newInstall(t, rel, "v1.0.0")
	before := snapshot(t, in.dir)
	if _, err := run(t, in); err == nil || !strings.Contains(err.Error(), "does not list") {
		t.Fatalf("err = %v, want the missing listing named", err)
	}
	if after := snapshot(t, in.dir); after != before {
		t.Error("the install directory changed")
	}
}

// A checksum for a differently cased name is another file's, not this one's.
func TestAChecksumForAnotherCaseOfTheNameDoesNotStandInForTheAsset(t *testing.T) {
	asset := script("v2.0.0")
	name, _ := assetFor("v2.0.0", platformOS, platformArch)
	if _, ok := checksumFor([]byte(sum(asset)+"  "+strings.ToUpper(name)+"\n"), name); ok {
		t.Error("an upper-cased name matched the asset")
	}
	if got, ok := checksumFor([]byte(strings.ToUpper(sum(asset))+" *"+name+"\r\n"), name); !ok || got != sum(asset) {
		t.Errorf("a binary-mode, upper-case-hex, CRLF line was not read: %q %v", got, ok)
	}
}

// An unwritable install directory is refused before anything is downloaded, in
// words that name the directory.
func TestAnUnwritableInstallDirectoryIsRefusedNamingIt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a directory is not made unwritable by its mode on Windows")
	}
	if u, err := user.Current(); err == nil && u.Uid == "0" {
		t.Skip("root writes anywhere, so the directory cannot be made unwritable")
	}
	rel := newFakeRelease(t)
	rel.publish("v2.0.0", script("v2.0.0"))
	rel.setLatest("v2.0.0")
	in := newInstall(t, rel, "v1.0.0")
	if err := os.Chmod(in.dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(in.dir, 0o755) })
	before := snapshot(t, in.dir)

	_, err := run(t, in)
	if err == nil || !strings.Contains(err.Error(), in.dir) || !strings.Contains(err.Error(), "package manager") {
		t.Fatalf("err = %v, want the directory %s and the package manager named", err, in.dir)
	}
	if rel.requested("nitpick_v2.0.0_linux_amd64") != 0 {
		t.Error("the asset was downloaded into a directory that cannot be written")
	}
	if after := snapshot(t, in.dir); after != before {
		t.Error("the install directory changed")
	}
}

// A pinned version installs even when it is older: that is how a downgrade is
// asked for.
func TestPinningAnOlderVersionInstallsIt(t *testing.T) {
	rel := newFakeRelease(t)
	rel.publish("v1.5.0", script("v1.5.0"))
	rel.publish("v3.0.0", script("v3.0.0"))
	rel.setLatest("v3.0.0")
	in := newInstall(t, rel, "v3.0.0")
	in.cfg.Version = "1.5.0" // a missing v is added

	res, err := run(t, in)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Installed || res.Latest != "v1.5.0" {
		t.Errorf("result = %+v, want v1.5.0 installed", res)
	}
	if got := ran(t, in.target); got != "nitpick v1.5.0" {
		t.Errorf("the binary says %q, want the downgrade", got)
	}
	if rel.requested("/latest/checksums.txt") != 0 {
		t.Error("a pinned version still asked what the latest is")
	}
}

func TestPinningTheInstalledVersionChangesNothing(t *testing.T) {
	rel := newFakeRelease(t)
	rel.publish("v2.0.0", script("v2.0.0"))
	in := newInstall(t, rel, "v2.0.0")
	in.cfg.Version = "v2.0.0"
	before := snapshot(t, in.dir)
	res, err := run(t, in)
	if err != nil || res.Installed {
		t.Fatalf("= %+v, %v; want nothing installed", res, err)
	}
	if !strings.Contains(in.out.String(), "already installed") || rel.requests() != 0 {
		t.Errorf("output %q, %d requests", in.out.String(), rel.requests())
	}
	if snapshot(t, in.dir) != before {
		t.Error("the install directory changed")
	}
}

// A build newer than the latest release is not replaced by it.
func TestABuildNewerThanTheLatestReleaseIsNotReplaced(t *testing.T) {
	rel := newFakeRelease(t)
	rel.publish("v2.0.0", script("v2.0.0"))
	rel.setLatest("v2.0.0")
	in := newInstall(t, rel, "v2.1.0")
	before := snapshot(t, in.dir)
	res, err := run(t, in)
	if err != nil || res.Installed {
		t.Fatalf("= %+v, %v; want no downgrade without a pin", res, err)
	}
	if !strings.Contains(in.out.String(), "newer than the latest") || snapshot(t, in.dir) != before {
		t.Errorf("output %q", in.out.String())
	}
}

// A development build has no version to compare, so it is not replaced
// unless a release is asked for by name.
func TestADevelopmentBuildIsNotReplacedWithoutAPinnedVersion(t *testing.T) {
	rel := newFakeRelease(t)
	rel.publish("v2.0.0", script("v2.0.0"))
	rel.setLatest("v2.0.0")
	in := newInstall(t, rel, "dev")
	before := snapshot(t, in.dir)
	if _, err := run(t, in); err == nil || !strings.Contains(err.Error(), "-version v2.0.0") {
		t.Fatalf("err = %v, want the pin to ask for named", err)
	}
	if snapshot(t, in.dir) != before {
		t.Error("a development build was replaced")
	}

	in.cfg.Check = true
	if _, err := run(t, in); err != nil || !strings.Contains(in.out.String(), "not a release build") {
		t.Errorf("-check on a development build: err=%v output=%q", err, in.out.String())
	}

	in.cfg.Check, in.cfg.Version = false, "v2.0.0"
	if res, err := run(t, in); err != nil || !res.Installed {
		t.Errorf("a pinned release over a development build: %+v %v", res, err)
	}
}

func TestAVersionThatIsNotAReleaseVersionIsRefused(t *testing.T) {
	rel := newFakeRelease(t)
	for _, bad := range []string{"v1/../../x", "v 1", "latest", "v", "v1;rm", "../v1.0.0"} {
		in := newInstall(t, rel, "v1.0.0")
		in.cfg.Version = bad
		if _, err := run(t, in); err == nil || !strings.Contains(err.Error(), "not a release version") {
			t.Errorf("%q: err = %v, want it refused as a version", bad, err)
		}
	}
	if rel.requests() != 0 {
		t.Error("a refused version still went to the network")
	}
}

// When the swap itself fails, the staged copy is removed: a failed upgrade
// leaves the directory as it found it, not littered with half-installs.
func TestAFailedSwapLeavesNoStagedFileBehind(t *testing.T) {
	rel := newFakeRelease(t)
	rel.publish("v2.0.0", script("v2.0.0"))
	rel.setLatest("v2.0.0")
	in := newInstall(t, rel, "v1.0.0")
	// A directory where the binary should be: the file cannot be renamed over it.
	if err := os.Remove(in.target); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(in.target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(in.target+"/keep", []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := run(t, in)
	if err == nil || !strings.Contains(err.Error(), "replace") {
		t.Fatalf("err = %v, want the failed replace named", err)
	}
	entries, _ := os.ReadDir(in.dir)
	if len(entries) != 1 || entries[0].Name() != "nitpick" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("the directory holds %v after a failed swap, want only the original", names)
	}
}
