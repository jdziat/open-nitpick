package upgrade

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
)

// A release server that keeps what it was asked for, so a test can say "the
// asset was never requested" and mean it.
type fakeRelease struct {
	t     *testing.T
	mu    sync.Mutex
	files map[string][]byte // URL path to body
	hits  []string
	srv   *httptest.Server
}

func newFakeRelease(t *testing.T) *fakeRelease {
	t.Helper()
	r := &fakeRelease{t: t, files: map[string][]byte{}}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		r.hits = append(r.hits, req.URL.Path)
		body, ok := r.files[req.URL.Path]
		r.mu.Unlock()
		if !ok {
			http.NotFound(w, req)
			return
		}
		w.Write(body)
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *fakeRelease) releaseBase() string { return r.srv.URL + "/dl" }
func (r *fakeRelease) latestBase() string  { return r.srv.URL + "/latest" }

// requested reports how many requests reached a path that ends with suffix.
func (r *fakeRelease) requested(suffix string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, h := range r.hits {
		if strings.HasSuffix(h, suffix) {
			n++
		}
	}
	return n
}

func (r *fakeRelease) requests() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.hits)
}

func sum(b []byte) string {
	d := sha256.Sum256(b)
	return hex.EncodeToString(d[:])
}

// script is a binary the tests can run: it prints what `nitpick version` does.
func script(version string) []byte {
	return []byte("#!/bin/sh\necho \"nitpick " + version + "\"\n")
}

const platformOS, platformArch = "linux", "amd64"

// publish serves a release of this platform's asset under version, with a
// checksums.txt that lists it and a bundle the fake verifier accepts.
func (r *fakeRelease) publish(version string, asset []byte) {
	r.t.Helper()
	name, err := assetFor(version, platformOS, platformArch)
	if err != nil {
		r.t.Fatal(err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.files["/dl/"+version+"/"+name] = asset
	r.files["/dl/"+version+"/checksums.txt"] = []byte(sum(asset) + "  " + name + "\n")
	r.files["/dl/"+version+"/"+name+".sigstore.json"] = []byte("signed:" + sum(asset))
}

// setLatest points the "latest release" lookup at a version's checksums.
func (r *fakeRelease) setLatest(version string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.files["/latest/checksums.txt"] = r.files["/dl/"+version+"/checksums.txt"]
}

func (r *fakeRelease) set(path string, body []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.files[path] = body
}

func (r *fakeRelease) drop(path string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.files, path)
}

func assetPath(version string) string {
	name, _ := assetFor(version, platformOS, platformArch)
	return "/dl/" + version + "/" + name
}

// fakeVerifier accepts a bundle only if it vouches for these exact bytes, as a
// real one does. Without that a fake would pass a bundle that signs something
// else.
type fakeVerifier struct{ calls int }

func (v *fakeVerifier) Verify(bundle, digest []byte) error {
	v.calls++
	if string(bundle) != "signed:"+hex.EncodeToString(digest) {
		return errors.New("signature verification failed: the bundle does not vouch for this file")
	}
	return nil
}

// install lays down a "running binary" and returns a config that upgrades it.
type installed struct {
	dir, target string
	cfg         Config
	out         *strings.Builder
	verifier    *fakeVerifier
}

func newInstall(t *testing.T, rel *fakeRelease, current string) *installed {
	t.Helper()
	dir := t.TempDir()
	target := filepath.Join(dir, "nitpick")
	if err := os.WriteFile(target, script(current), 0o755); err != nil {
		t.Fatal(err)
	}
	in := &installed{dir: dir, target: target, out: &strings.Builder{}, verifier: &fakeVerifier{}}
	in.cfg = Config{
		ReleaseBase: rel.releaseBase(),
		LatestBase:  rel.latestBase(),
		NewVerifier: func(context.Context) (Verifier, error) { return in.verifier, nil },
		GOOS:        platformOS,
		GOARCH:      platformArch,
		Current:     current,
		Target:      target,
		Out:         in.out,
		TempDir:     t.TempDir(),
	}
	return in
}

// snapshot is every file in dir with its content hash and permissions, for
// comparing a directory before and after. "Untouched" means this is equal.
func snapshot(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		body, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, fmt.Sprintf("%s %v %s", e.Name(), info.Mode(), sum(body)))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// ran executes the installed binary and returns what it printed.
func ran(t *testing.T, path string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake binaries are shell scripts")
	}
	out, err := execOutput(path)
	if err != nil {
		t.Fatalf("run %s: %v", path, err)
	}
	return strings.TrimSpace(out)
}

func execOutput(path string) (string, error) {
	cmd := newCommand(path)
	b, err := cmd.Output()
	return string(b), err
}
