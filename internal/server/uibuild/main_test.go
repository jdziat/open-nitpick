package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sources(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{"app.css": "body{color:red}", "app.js": "console.log(1)"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func readManifest(t *testing.T, dst string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dst, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestABuildWritesHashedAssetsAndAManifestNamingThem(t *testing.T) {
	dst := t.TempDir()
	if err := build(sources(t), dst); err != nil {
		t.Fatal(err)
	}
	m := readManifest(t, dst)
	for _, logical := range []string{"app.css", "app.js"} {
		hashed := m[logical]
		if !isHashedAsset(hashed) {
			t.Errorf("%s was written as %q, which is not a content-hashed name", logical, hashed)
		}
		if _, err := os.Stat(filepath.Join(dst, hashed)); err != nil {
			t.Errorf("the manifest names %s, which was not written", hashed)
		}
	}
	css, _ := os.ReadFile(filepath.Join(dst, m["app.css"]))
	if string(css) != "body{color:red}" {
		t.Errorf("the css was changed in the build: %q", css)
	}
}

// The name follows the content: change the source and the name changes, which is
// what lets the server cache an asset for ever.
func TestTheHashedNameFollowsTheContent(t *testing.T) {
	src := sources(t)
	a, b := t.TempDir(), t.TempDir()
	if err := build(src, a); err != nil {
		t.Fatal(err)
	}
	if err := build(src, b); err != nil {
		t.Fatal(err)
	}
	if readManifest(t, a)["app.css"] != readManifest(t, b)["app.css"] {
		t.Error("the same source built to different names")
	}
	if err := os.WriteFile(filepath.Join(src, "app.css"), []byte("body{color:blue}"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := t.TempDir()
	if err := build(src, c); err != nil {
		t.Fatal(err)
	}
	if readManifest(t, a)["app.css"] == readManifest(t, c)["app.css"] {
		t.Error("changed content kept its name, so a cached copy would be served stale")
	}
	if readManifest(t, a)["app.js"] != readManifest(t, c)["app.js"] {
		t.Error("an unchanged file changed its name")
	}
}

// A rebuild leaves one build and no other: old hashed files go, the placeholder
// stays.
func TestARebuildRemovesTheOldBuildAndKeepsThePlaceholder(t *testing.T) {
	src, dst := sources(t), t.TempDir()
	keep := filepath.Join(dst, "README.md")
	if err := os.WriteFile(keep, []byte("placeholder"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := build(src, dst); err != nil {
		t.Fatal(err)
	}
	old := readManifest(t, dst)["app.css"]
	if err := os.WriteFile(filepath.Join(src, "app.css"), []byte("body{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := build(src, dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, old)); !os.IsNotExist(err) {
		t.Errorf("the previous build's %s was left behind", old)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Errorf("the placeholder was removed: %v", err)
	}
	entries, _ := os.ReadDir(dst)
	if len(entries) != 4 { // placeholder, manifest, one css, one js
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("the directory holds %v, want the placeholder, the manifest and one build", names)
	}
}

// A build that cannot finish leaves no manifest, so the server says the UI was
// not built and does not serve half of it.
func TestABuildThatFailsLeavesNoManifest(t *testing.T) {
	for name, prepare := range map[string]func(string){
		"a missing source": func(src string) { _ = os.Remove(filepath.Join(src, "app.js")) },
		"an empty source":  func(src string) { _ = os.WriteFile(filepath.Join(src, "app.js"), nil, 0o644) },
	} {
		src, dst := sources(t), t.TempDir()
		// There was a good build before.
		if err := build(src, dst); err != nil {
			t.Fatal(err)
		}
		prepare(src)
		if err := build(src, dst); err == nil {
			t.Errorf("%s: the build succeeded", name)
		}
		if _, err := os.Stat(filepath.Join(dst, "manifest.json")); !os.IsNotExist(err) {
			t.Errorf("%s: a failed build left a manifest, which would describe a build that is not there", name)
		}
	}
}

func TestOnlyWhatABuildWritesIsRemovedByTheNextOne(t *testing.T) {
	dst := t.TempDir()
	for _, name := range []string{"app.css", "app.js", "app.notahash.css", "app.0123456789ab.txt", "other.0123456789ab.css", "app.0123456789abc.css", "app.0123456789abcd.css"} {
		if err := os.WriteFile(filepath.Join(dst, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := build(sources(t), dst); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"app.css", "app.js", "app.notahash.css", "app.0123456789ab.txt", "other.0123456789ab.css", "app.0123456789abc.css", "app.0123456789abcd.css"} {
		if _, err := os.Stat(filepath.Join(dst, name)); err != nil {
			t.Errorf("%s is not a build output and was removed", name)
		}
	}
}

func TestABuildRefusesADirectoryItDidNotMake(t *testing.T) {
	dst := t.TempDir()
	if err := os.Mkdir(filepath.Join(dst, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := build(sources(t), dst); err == nil || !strings.Contains(err.Error(), "directory") {
		t.Errorf("err = %v, want a directory in the output refused", err)
	}
}

func TestHashedAssetNamesAreRecognisedExactly(t *testing.T) {
	for name, want := range map[string]bool{
		"app.0123456789ab.css": true, "app.0123456789ab.js": true, "app.ABCDEF012345.css": true,
		"app.css": false, "app.0123456789a.css": false, "app.0123456789abc.css": false, "app.0123456789abcd.css": false, "app.0123456789.css": false,
		"app.0123456789ab.txt": false, "app.zzzzzzzzzzzz.css": false, "other.0123456789ab.css": false, "manifest.json": false,
	} {
		if got := isHashedAsset(name); got != want {
			t.Errorf("isHashedAsset(%q) = %v, want %v", name, got, want)
		}
	}
}
