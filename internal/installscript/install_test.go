package installscript

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const testVersion = "v9.9.9"

func mustContain(t *testing.T, out, want string) {
	t.Helper()
	if !strings.Contains(out, want) {
		t.Errorf("output does not contain %q:\n%s", want, out)
	}
}

func TestInstallsTheLatestReleaseAndRunsIt(t *testing.T) {
	rel := newFakeRelease(t)
	built := rel.publish(t, testVersion)
	rel.setLatest(testVersion)

	res := run(t, opts{rel: rel})
	if res.code != 0 {
		t.Fatalf("exit %d:\n%s", res.code, res.out())
	}
	got := readFile(t, filepath.Join(res.dir, "nitpick"))
	if !bytes.Equal(got, built[target{"linux", "amd64"}]) {
		t.Error("the installed file is not the linux/amd64 binary")
	}
	info, err := os.Stat(filepath.Join(res.dir, "nitpick"))
	if err != nil || info.Mode().Perm()&0o111 == 0 {
		t.Errorf("the installed file is not executable: %v %v", info, err)
	}
	mustContain(t, res.stdout, "Installing nitpick "+testVersion+" (linux/amd64) to "+res.dir)
	mustContain(t, res.stdout, "Installed: nitpick "+testVersion)
	if names := installed(t, res.dir); len(names) != 1 {
		t.Errorf("the install directory holds %v, want only nitpick", names)
	}
}

func TestAPinnedVersionSkipsTheLatestLookup(t *testing.T) {
	rel := newFakeRelease(t)
	rel.publish(t, "v9.8.0")
	rel.publish(t, testVersion)
	rel.setLatest(testVersion)

	for _, pin := range []string{"v9.8.0", "9.8.0"} {
		res := run(t, opts{rel: rel, version: pin})
		if res.code != 0 {
			t.Fatalf("VERSION=%s: exit %d:\n%s", pin, res.code, res.out())
		}
		mustContain(t, res.stdout, "Installed: nitpick v9.8.0")
	}
	for _, p := range rel.requests() {
		if p == "/latest" {
			t.Error("a pinned install still asked which release is the latest")
		}
	}
}

func TestEveryReleaseTargetIsEitherInstalledOrSentToTheWindowsScript(t *testing.T) {
	rel := newFakeRelease(t)
	built := rel.publish(t, testVersion)
	rel.setLatest(testVersion)

	uname := map[target][2]string{
		{"linux", "amd64"}:  {"Linux", "x86_64"},
		{"linux", "arm64"}:  {"Linux", "aarch64"},
		{"darwin", "amd64"}: {"Darwin", "x86_64"},
		{"darwin", "arm64"}: {"Darwin", "arm64"},
	}
	for _, tg := range releaseTargets(t) {
		t.Run(tg.String(), func(t *testing.T) {
			if tg.os == "windows" {
				res := run(t, opts{rel: rel, sysname: "MINGW64_NT-10.0", machine: "x86_64"})
				if res.code == 0 {
					t.Fatal("the shell script accepted a windows machine")
				}
				mustContain(t, res.stderr, "install.ps1")
				return
			}
			u, ok := uname[tg]
			if !ok {
				t.Fatalf("the Makefile now builds %s and install.sh does not know it: add it to the script and to this test", tg)
			}
			res := run(t, opts{rel: rel, sysname: u[0], machine: u[1]})
			if res.code != 0 {
				t.Fatalf("exit %d:\n%s", res.code, res.out())
			}
			if !bytes.Equal(readFile(t, filepath.Join(res.dir, "nitpick")), built[tg]) {
				t.Errorf("installed a different binary than the one built for %s", tg)
			}
			mustContain(t, res.stdout, "("+tg.os+"/"+tg.arch+")")
		})
	}
}

func TestTheReleaseWorkflowStillNamesBundlesTheWayTheScriptReadsThem(t *testing.T) {
	wf := readRepoFile(t, ".github/workflows/release.yml")
	if !strings.Contains(wf, `"$f.sigstore.json"`) {
		t.Fatal("release.yml no longer writes <asset>.sigstore.json: update install.sh and install.ps1")
	}
	if !strings.Contains(wf, "^https://github.com/nitpick-oss/nitpick/") || !strings.Contains(wf, "https://token.actions.githubusercontent.com") {
		t.Fatal("release.yml's documented signing identity changed: update the verification in the install scripts")
	}
	sh := readRepoFile(t, "website/install.sh")
	for _, want := range []string{`$asset.sigstore.json`, `--certificate-identity-regexp "^https://github.com/$repo/"`, `https://token.actions.githubusercontent.com`} {
		if !strings.Contains(sh, want) {
			t.Errorf("install.sh does not contain %q", want)
		}
	}
}

func TestATamperedAssetIsRefusedAndNothingIsInstalled(t *testing.T) {
	rel := newFakeRelease(t)
	rel.publish(t, testVersion)
	rel.setLatest(testVersion)
	// Same name, different bytes: the checksums.txt still describes the original.
	rel.put(testVersion, assetName(t, testVersion, target{"linux", "amd64"}), []byte("#!/bin/sh\necho owned\n"))

	res := run(t, opts{rel: rel})
	if res.code == 0 {
		t.Fatalf("a tampered asset was installed:\n%s", res.out())
	}
	mustContain(t, res.stderr, "checksum mismatch")
	if names := installed(t, res.dir); len(names) != 0 {
		t.Errorf("a refused install left %v behind", names)
	}
}

func TestATamperedChecksumsFileIsRefusedAndNothingIsInstalled(t *testing.T) {
	rel := newFakeRelease(t)
	rel.publish(t, testVersion)
	rel.setLatest(testVersion)
	name := assetName(t, testVersion, target{"linux", "amd64"})
	rel.put(testVersion, "checksums.txt", []byte(strings.Repeat("0", 64)+"  "+name+"\n"))

	res := run(t, opts{rel: rel})
	if res.code == 0 {
		t.Fatal("an asset that does not match checksums.txt was installed")
	}
	mustContain(t, res.stderr, "checksum mismatch")
	if names := installed(t, res.dir); len(names) != 0 {
		t.Errorf("a refused install left %v behind", names)
	}
}

func TestAChecksumsFileThatDoesNotListTheAssetIsRefused(t *testing.T) {
	rel := newFakeRelease(t)
	rel.publish(t, testVersion)
	rel.setLatest(testVersion)
	name := assetName(t, testVersion, target{"linux", "amd64"})
	good := sum(append(fakeBinary(testVersion), []byte("# linux/amd64\n")...))
	// Listed under the names of other files, none of them this asset: a prefix,
	// a suffix, and a different platform.
	rel.put(testVersion, "checksums.txt", []byte(
		good+"  "+name+".sigstore.json\n"+
			good+"  x"+name+"\n"+
			good+"  "+strings.Replace(name, "amd64", "arm64", 1)+"\n"))

	res := run(t, opts{rel: rel})
	if res.code == 0 {
		t.Fatal("an asset with no checksum of its own was installed")
	}
	mustContain(t, res.stderr, "does not list "+name)
	if names := installed(t, res.dir); len(names) != 0 {
		t.Errorf("a refused install left %v behind", names)
	}
}

func TestARefusedInstallLeavesAnOlderBinaryExactlyAsItWas(t *testing.T) {
	rel := newFakeRelease(t)
	rel.publish(t, testVersion)
	rel.setLatest(testVersion)
	rel.put(testVersion, assetName(t, testVersion, target{"linux", "amd64"}), []byte("tampered"))

	dir := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	old := []byte("#!/bin/sh\necho nitpick v1.0.0\n")
	write(t, filepath.Join(dir, "nitpick"), string(old))

	res := run(t, opts{rel: rel, dir: dir})
	if res.code == 0 {
		t.Fatal("a tampered asset replaced the installed binary")
	}
	if !bytes.Equal(readFile(t, filepath.Join(dir, "nitpick")), old) {
		t.Error("the existing binary changed although the install was refused")
	}
	if names := installed(t, dir); len(names) != 1 {
		t.Errorf("the directory holds %v, want only the old binary", names)
	}
}

func TestAnInstallReplacesAnOlderBinaryAndLeavesNoTemporaryFile(t *testing.T) {
	rel := newFakeRelease(t)
	built := rel.publish(t, testVersion)
	rel.setLatest(testVersion)
	dir := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "nitpick"), "#!/bin/sh\necho nitpick v1.0.0\n")

	res := run(t, opts{rel: rel, dir: dir})
	if res.code != 0 {
		t.Fatalf("exit %d:\n%s", res.code, res.out())
	}
	if !bytes.Equal(readFile(t, filepath.Join(dir, "nitpick")), built[target{"linux", "amd64"}]) {
		t.Error("the older binary was not replaced")
	}
	if names := installed(t, dir); len(names) != 1 {
		t.Errorf("the directory holds %v, want only nitpick", names)
	}
}

func TestUnsupportedPlatformsAreRefusedBeforeAnyDownload(t *testing.T) {
	cases := []struct {
		name, sys, machine, want string
	}{
		{"freebsd", "FreeBSD", "amd64", "unsupported operating system"},
		{"solaris", "SunOS", "x86_64", "unsupported operating system"},
		{"linux on riscv", "Linux", "riscv64", "unsupported architecture"},
		{"linux on 32-bit x86", "Linux", "i686", "unsupported architecture"},
		{"darwin on powerpc", "Darwin", "Power Macintosh", "unsupported architecture"},
		{"windows under a unix shell", "MINGW64_NT-10.0", "x86_64", "install.ps1"},
		{"cygwin", "CYGWIN_NT-10.0", "x86_64", "install.ps1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rel := newFakeRelease(t)
			rel.publish(t, testVersion)
			rel.setLatest(testVersion)
			res := run(t, opts{rel: rel, sysname: tc.sys, machine: tc.machine})
			if res.code == 0 {
				t.Fatalf("%s/%s was accepted:\n%s", tc.sys, tc.machine, res.out())
			}
			mustContain(t, res.stderr, tc.want)
			if reqs := rel.requests(); len(reqs) != 0 {
				t.Errorf("an unsupported platform still made requests: %v", reqs)
			}
			if names := installed(t, res.dir); len(names) != 0 {
				t.Errorf("an unsupported platform left %v behind", names)
			}
		})
	}
}

// okCosign records how it was called and accepts everything.
const okCosign = "echo \"$@\" > \"$COSIGN_ARGS\"\nexit 0\n"

func TestCosignOnThePathVerifiesTheSignatureAgainstTheReleaseIdentity(t *testing.T) {
	rel := newFakeRelease(t)
	rel.publish(t, testVersion)
	rel.setLatest(testVersion)
	argsFile := filepath.Join(t.TempDir(), "args")

	res := run(t, opts{rel: rel, fake: map[string]string{"cosign": okCosign}, extraEnv: []string{"COSIGN_ARGS=" + argsFile}})
	if res.code != 0 {
		t.Fatalf("exit %d:\n%s", res.code, res.out())
	}
	mustContain(t, res.stdout, "Sigstore signature")
	args := string(readFile(t, argsFile))
	for _, want := range []string{"verify-blob", "--bundle", ".sigstore.json", "--certificate-identity-regexp", "^https://github.com/nitpick-oss/nitpick/", "--certificate-oidc-issuer", "https://token.actions.githubusercontent.com"} {
		if !strings.Contains(args, want) {
			t.Errorf("cosign was called without %q: %s", want, args)
		}
	}
	if strings.Contains(res.stdout, "checksum only") {
		t.Error("the output says only the checksum was checked although cosign ran")
	}
}

func TestABadSignatureStopsTheInstallEvenWhenTheChecksumMatches(t *testing.T) {
	rel := newFakeRelease(t)
	rel.publish(t, testVersion)
	rel.setLatest(testVersion)

	res := run(t, opts{rel: rel, fake: map[string]string{"cosign": "echo 'no matching signature' >&2\nexit 1\n"}})
	if res.code == 0 {
		t.Fatalf("an asset whose signature failed was installed:\n%s", res.out())
	}
	mustContain(t, res.stderr, "signature verification failed")
	mustContain(t, res.stderr, "no matching signature")
	if names := installed(t, res.dir); len(names) != 0 {
		t.Errorf("a refused install left %v behind", names)
	}
}

func TestMissingSignatureBundleStopsTheInstallWhenCosignIsPresent(t *testing.T) {
	rel := newFakeRelease(t)
	rel.publish(t, testVersion)
	rel.setLatest(testVersion)
	name := assetName(t, testVersion, target{"linux", "amd64"})
	rel.drop(testVersion, name+".sigstore.json")

	res := run(t, opts{rel: rel, fake: map[string]string{"cosign": "exit 0\n"}})
	if res.code == 0 {
		t.Fatal("an unsigned release was installed although cosign was present")
	}
	mustContain(t, res.stderr, "signature bundle")
}

func TestWithoutCosignTheOutputSaysOnlyTheChecksumWasChecked(t *testing.T) {
	rel := newFakeRelease(t)
	rel.publish(t, testVersion)
	rel.setLatest(testVersion)

	res := run(t, opts{rel: rel})
	if res.code != 0 {
		t.Fatalf("exit %d:\n%s", res.code, res.out())
	}
	mustContain(t, res.stdout, "Verified the checksum only")
	if strings.Contains(res.stdout, "Verified the checksum and the Sigstore") {
		t.Error("the output claims a signature check that did not run")
	}
}

func TestWgetIsUsedWhenCurlIsMissing(t *testing.T) {
	rel := newFakeRelease(t)
	rel.publish(t, testVersion)
	rel.setLatest(testVersion)
	tools := append(append([]string(nil), baseTools...), "wget", checksumTool(t))

	res := run(t, opts{rel: rel, tools: tools})
	if res.code != 0 {
		t.Fatalf("exit %d:\n%s", res.code, res.out())
	}
	mustContain(t, res.stdout, "Installed: nitpick "+testVersion)
}

func TestNeitherCurlNorWgetIsAnErrorBeforeAnyWork(t *testing.T) {
	rel := newFakeRelease(t)
	rel.publish(t, testVersion)
	rel.setLatest(testVersion)
	tools := append(append([]string(nil), baseTools...), checksumTool(t))

	res := run(t, opts{rel: rel, tools: tools})
	if res.code == 0 {
		t.Fatal("the script ran with no way to download")
	}
	mustContain(t, res.stderr, "curl or wget")
	if names := installed(t, res.dir); len(names) != 0 {
		t.Errorf("left %v behind", names)
	}
}

func TestNoChecksumProgramIsAnErrorAndNotASkippedCheck(t *testing.T) {
	rel := newFakeRelease(t)
	rel.publish(t, testVersion)
	rel.setLatest(testVersion)
	tools := append(append([]string(nil), baseTools...), "curl")

	res := run(t, opts{rel: rel, tools: tools})
	if res.code == 0 {
		t.Fatal("the script installed with nothing to check the download with")
	}
	mustContain(t, res.stderr, "sha256sum or shasum")
	if reqs := rel.requests(); len(reqs) != 0 {
		t.Errorf("downloaded %v before finding it could not check them", reqs)
	}
}

func TestShasumAloneIsEnoughToCheckTheDownload(t *testing.T) {
	rel := newFakeRelease(t)
	rel.publish(t, testVersion)
	rel.setLatest(testVersion)
	real := checksumTool(t)
	tools := append(append([]string(nil), baseTools...), "curl", real)
	// A machine with shasum and not sha256sum: shasum -a 256 is sha256sum.
	fake := map[string]string{}
	if real == "sha256sum" {
		tools = append(append([]string(nil), baseTools...), "curl")
		path, err := exec.LookPath("sha256sum")
		if err != nil {
			t.Skip("sha256sum missing")
		}
		fake["shasum"] = "[ \"$1\" = -a ] && [ \"$2\" = 256 ] || exit 2\nshift 2\nexec " + path + " \"$@\"\n"
	}
	res := run(t, opts{rel: rel, tools: tools, fake: fake})
	if res.code != 0 {
		t.Fatalf("exit %d:\n%s", res.code, res.out())
	}
	mustContain(t, res.stdout, "Installed: nitpick "+testVersion)
}

func TestAnInstallDirectoryThatCannotBeWrittenIsNamedInTheError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write anywhere, so no directory is unwritable")
	}
	rel := newFakeRelease(t)
	rel.publish(t, testVersion)
	rel.setLatest(testVersion)
	dir := filepath.Join(t.TempDir(), "locked")
	if err := os.MkdirAll(dir, 0o500); err != nil { //nolint:gosec // read-only on purpose
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) }) //nolint:gosec // restored so the test directory can be removed

	// A fake sudo that records any use: the script must never reach for it.
	sudoLog := filepath.Join(t.TempDir(), "sudo-used")
	res := run(t, opts{rel: rel, dir: dir, fake: map[string]string{"sudo": "echo \"$@\" >> " + sudoLog + "\nexit 0\n"}})
	if res.code == 0 {
		t.Fatalf("installed into a directory that cannot be written:\n%s", res.out())
	}
	mustContain(t, res.stderr, dir)
	// Not just any failure: the one that tells the user what to do instead.
	mustContain(t, res.stderr, "INSTALL_DIR")
	mustContain(t, res.stderr, "never uses sudo")
	if _, err := os.Stat(sudoLog); err == nil {
		t.Errorf("the script ran sudo: %s", readFile(t, sudoLog))
	}
}

func TestAnInstallDirectoryThatCannotBeCreatedIsNamedInTheError(t *testing.T) {
	rel := newFakeRelease(t)
	rel.publish(t, testVersion)
	rel.setLatest(testVersion)
	blocker := filepath.Join(t.TempDir(), "file")
	write(t, blocker, "not a directory")
	dir := filepath.Join(blocker, "bin")

	res := run(t, opts{rel: rel, dir: dir})
	if res.code == 0 {
		t.Fatal("installed under a path that is a file")
	}
	mustContain(t, res.stderr, dir)
}

func TestTheScriptNeverCallsSudo(t *testing.T) {
	sh := readRepoFile(t, "website/install.sh")
	callsSudo := regexp.MustCompile(`(^|[;&|(]|\$\()\s*sudo\s`)
	for i, line := range strings.Split(sh, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if callsSudo.MatchString(line) {
			t.Errorf("line %d runs sudo: %s", i+1, line)
		}
	}
}

func TestAPathHintAppearsOnlyWhenTheDirectoryIsNotOnThePath(t *testing.T) {
	rel := newFakeRelease(t)
	rel.publish(t, testVersion)
	rel.setLatest(testVersion)

	off := run(t, opts{rel: rel})
	if off.code != 0 {
		t.Fatalf("exit %d:\n%s", off.code, off.out())
	}
	mustContain(t, off.stdout, "Add "+off.dir+" to your PATH")

	dir := filepath.Join(t.TempDir(), "bin")
	on := run(t, opts{rel: rel, dir: dir, path: []string{dir}})
	if on.code != 0 {
		t.Fatalf("exit %d:\n%s", on.code, on.out())
	}
	if strings.Contains(on.stdout, "to your PATH") {
		t.Errorf("a directory already on the PATH still got a hint:\n%s", on.stdout)
	}
}

func TestAVersionThatIsNotAReleaseVersionIsRefusedBeforeAnyRequest(t *testing.T) {
	for _, bad := range []string{"latest", "../../etc/passwd", "v1.2.3/../../x", "v1.2.3;id", "v1 2", "$(id)", "v1.2.3?x=1", "vv"} {
		t.Run(bad, func(t *testing.T) {
			rel := newFakeRelease(t)
			rel.publish(t, testVersion)
			rel.setLatest(testVersion)
			res := run(t, opts{rel: rel, version: bad})
			if res.code == 0 {
				t.Fatalf("VERSION=%q was accepted:\n%s", bad, res.out())
			}
			mustContain(t, res.stderr, "not a release version")
			if reqs := rel.requests(); len(reqs) != 0 {
				t.Errorf("made requests for %q: %v", bad, reqs)
			}
		})
	}
}

func TestALatestLookupThatFailsExplainsHowToPinAVersion(t *testing.T) {
	rel := newFakeRelease(t) // nothing published, no latest set: the endpoint answers 404

	res := run(t, opts{rel: rel})
	if res.code == 0 {
		t.Fatal("installed with no release to install")
	}
	mustContain(t, res.stderr, "VERSION=vX.Y.Z")
}

func TestALatestAnswerWithNoTagIsAnErrorNotAnEmptyVersion(t *testing.T) {
	rel := newFakeRelease(t)
	rel.put("x", "y", []byte(`{"name":"a release with no tag_name key"}`))

	res := run(t, opts{rel: rel, extraEnv: []string{"NITPICK_LATEST_URL=" + rel.srv.URL + "/dl/x/y"}})
	if res.code == 0 {
		t.Fatal("an answer with no tag was accepted")
	}
	mustContain(t, res.stderr, "tag_name")
}

func TestTheScriptRunsWhenItIsPipedToTheShell(t *testing.T) {
	rel := newFakeRelease(t)
	rel.publish(t, testVersion)
	rel.setLatest(testVersion)

	res := run(t, opts{rel: rel, stdin: string(readFile(t, filepath.Join(repoRoot(t), "website", "install.sh")))})
	if res.code != 0 {
		t.Fatalf("exit %d:\n%s", res.code, res.out())
	}
	mustContain(t, res.stdout, "Installed: nitpick "+testVersion)
}

func TestATruncatedDownloadOfTheScriptInstallsNothing(t *testing.T) {
	full := string(readFile(t, filepath.Join(repoRoot(t), "website", "install.sh")))
	lines := strings.SplitAfter(full, "\n")
	for _, keep := range []int{3, len(lines) / 4, len(lines) / 2, len(lines) - len(lines)/8, len(lines) - 3, len(lines) - 2} {
		t.Run(fmt.Sprintf("first %d lines", keep), func(t *testing.T) {
			rel := newFakeRelease(t)
			rel.publish(t, testVersion)
			rel.setLatest(testVersion)
			res := run(t, opts{rel: rel, stdin: strings.Join(lines[:keep], "")})
			if reqs := rel.requests(); len(reqs) != 0 {
				t.Errorf("a script cut off after %d lines downloaded %v", keep, reqs)
			}
			if names := installed(t, res.dir); len(names) != 0 {
				t.Errorf("a script cut off after %d lines installed %v", keep, names)
			}
		})
	}
}

func TestShellcheckPassesOnTheInstallScript(t *testing.T) {
	sc, err := exec.LookPath("shellcheck")
	if err != nil {
		t.Skip("shellcheck is not installed; CI runs it")
	}
	out, err := exec.Command(sc, "-s", "sh", filepath.Join(repoRoot(t), "website", "install.sh")).CombinedOutput()
	if err != nil {
		t.Fatalf("shellcheck:\n%s", out)
	}
}
