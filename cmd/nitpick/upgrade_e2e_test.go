package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// builtNitpick compiles the command with a version set the way the release
// sets it, so the test runs the process a user runs and not a function. A copy
// is made for each caller: an upgrade replaces the file it runs from, and one
// test's upgrade must not be another's starting point. The compile is done once
// per version.
func builtNitpick(t *testing.T, version string) string {
	t.Helper()
	builtMu.Lock()
	master, ok := built[version]
	if !ok {
		dir, err := os.MkdirTemp("", "nitpick-upgrade-e2e-")
		if err != nil {
			builtMu.Unlock()
			t.Fatal(err)
		}
		master = filepath.Join(dir, "nitpick")
		if runtime.GOOS == "windows" {
			master += ".exe"
		}
		out, err := exec.Command("go", "build", "-ldflags", "-X main.version="+version, "-o", master, ".").CombinedOutput()
		if err != nil {
			builtMu.Unlock()
			t.Fatalf("build: %v\n%s", err, out)
		}
		built[version] = master
	}
	builtMu.Unlock()

	data, err := os.ReadFile(master)
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), filepath.Base(master))
	if err := os.WriteFile(bin, data, 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

var (
	builtMu sync.Mutex
	built   = map[string]string{}
)

type upgradeRelease struct {
	mu   sync.Mutex
	hits []string
	srv  *httptest.Server
}

// serveRelease serves the latest checksums.txt and one version's checksums.txt
// for this platform, with an asset whose bytes do not match the checksum, so any
// install attempt is refused at the checksum and no signature is needed.
func serveRelease(t *testing.T, latest string) *upgradeRelease {
	t.Helper()
	asset := "nitpick_" + latest + "_" + runtime.GOOS + "_" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		asset += ".exe"
	}
	wrong := sha256.Sum256([]byte("not the asset"))
	listing := hex.EncodeToString(wrong[:]) + "  " + asset + "\n"
	r := &upgradeRelease{}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		r.hits = append(r.hits, req.URL.Path)
		r.mu.Unlock()
		switch {
		case strings.HasSuffix(req.URL.Path, "/checksums.txt"):
			w.Write([]byte(listing))
		case strings.HasSuffix(req.URL.Path, asset):
			w.Write([]byte("the real asset bytes, which do not match the listing"))
		default:
			http.NotFound(w, req)
		}
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *upgradeRelease) env() []string {
	return []string{"NITPICK_RELEASE_BASE=" + r.srv.URL + "/dl", "NITPICK_LATEST_BASE=" + r.srv.URL + "/latest"}
}

func (r *upgradeRelease) saw(suffix string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, h := range r.hits {
		if strings.HasSuffix(h, suffix) {
			return true
		}
	}
	return false
}

func runNitpick(t *testing.T, bin string, env []string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return stdout.String(), stderr.String(), code
}

func fileHash(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	d := sha256.Sum256(b)
	return hex.EncodeToString(d[:])
}

// The real process, end to end: -check reports a newer release and exits 0
// without touching the binary or asking for the asset.
func TestUpgradeCheckReportsANewerReleaseAndTouchesNothing(t *testing.T) {
	bin := builtNitpick(t, "v1.0.0")
	rel := serveRelease(t, "v2.0.0")
	before := fileHash(t, bin)

	stdout, stderr, code := runNitpick(t, bin, rel.env(), "upgrade", "-check")
	if code != 0 {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "v2.0.0 is available") {
		t.Errorf("stdout %q does not report v2.0.0", stdout)
	}
	if fileHash(t, bin) != before {
		t.Error("-check changed the binary")
	}
	if rel.saw("nitpick_v2.0.0_"+runtime.GOOS+"_"+runtime.GOARCH) || rel.saw(".sigstore.json") {
		t.Error("-check asked for the asset or its signature")
	}
}

func TestUpgradeOnTheLatestReleaseSaysSoAndExitsClean(t *testing.T) {
	bin := builtNitpick(t, "v2.0.0")
	rel := serveRelease(t, "v2.0.0")
	stdout, stderr, code := runNitpick(t, bin, rel.env(), "upgrade")
	if code != 0 || !strings.Contains(stdout, "up to date") {
		t.Errorf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
}

// A refused upgrade exits non-zero with the reason on stderr and the binary
// byte-identical. The listing does not match the asset, so this is the checksum
// guard in the built program.
func TestUpgradeRefusedForAChecksumMismatchExitsNonZeroAndKeepsTheBinary(t *testing.T) {
	bin := builtNitpick(t, "v1.0.0")
	rel := serveRelease(t, "v2.0.0")
	before := fileHash(t, bin)

	stdout, stderr, code := runNitpick(t, bin, rel.env(), "upgrade")
	if code == 0 {
		t.Fatalf("a refused upgrade exited 0\nstdout: %s", stdout)
	}
	if !strings.Contains(stderr, "checksum mismatch") {
		t.Errorf("stderr %q does not name the checksum mismatch", stderr)
	}
	if fileHash(t, bin) != before {
		t.Error("a refused upgrade changed the binary")
	}
	// Rule 10: it got as far as the asset, so the refusal is the checksum's.
	if !rel.saw("nitpick_v2.0.0_" + runtime.GOOS + "_" + runtime.GOARCH) {
		t.Error("the asset was never requested, so the checksum was not what refused it")
	}
	// And the version it reports is still the old one.
	if out, _, _ := runNitpick(t, bin, nil, "version"); !strings.Contains(out, "v1.0.0") {
		t.Errorf("after a refusal the binary says %q", out)
	}
}

func TestUpgradeRefusesAnUnsafeReleaseSourceFromTheEnvironment(t *testing.T) {
	bin := builtNitpick(t, "v1.0.0")
	_, stderr, code := runNitpick(t, bin, []string{"NITPICK_LATEST_BASE=http://example.com/latest"}, "upgrade", "-check")
	if code == 0 || !strings.Contains(stderr, "HTTPS") {
		t.Errorf("exit %d, stderr %q; want a plain HTTP source refused", code, stderr)
	}
}

func TestUpgradeRejectsStrayArgumentsAndUnknownFlags(t *testing.T) {
	bin := builtNitpick(t, "v1.0.0")
	if _, stderr, code := runNitpick(t, bin, nil, "upgrade", "v2.0.0"); code == 0 || !strings.Contains(stderr, "no arguments") {
		t.Errorf("a stray argument: exit %d, stderr %q", code, stderr)
	}
	if _, _, code := runNitpick(t, bin, nil, "upgrade", "-nope"); code == 0 {
		t.Error("an unknown flag exited 0")
	}
	if _, stderr, code := runNitpick(t, bin, nil, "upgrade", "-check", "-version", "v2.0.0"); code == 0 || !strings.Contains(stderr, "-check") {
		t.Errorf("-check with -version: exit %d, stderr %q", code, stderr)
	}
}

func TestUpgradeHelpExitsZeroAndListsTheFlags(t *testing.T) {
	bin := builtNitpick(t, "v1.0.0")
	stdout, stderr, code := runNitpick(t, bin, nil, "upgrade", "-h")
	if code != 0 {
		t.Fatalf("-h exited %d", code)
	}
	help := stdout + stderr
	for _, want := range []string{"-check", "-version", "Sigstore"} {
		if !strings.Contains(help, want) {
			t.Errorf("help lacks %q:\n%s", want, help)
		}
	}
}
