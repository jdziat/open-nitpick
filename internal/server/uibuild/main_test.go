package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// viteOutput is what `npm run build` leaves in ui/dist: files whose names already
// carry a hash of their content.
func viteOutput(t *testing.T, extra map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"app.AbC123_x.js":       "console.log(1)",
		"app.DeF456-y.css":      "body{color:red}",
		"theme.GhI789.js":       "document.documentElement.className='dark'",
		"asset.geist.Zz1.woff2": "font",
		"index.html":            "<html></html>",
	}
	for k, v := range extra {
		if v == "" {
			delete(files, k)
			continue
		}
		files[k] = v
	}
	for name, body := range files {
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

func TestABuildCopiesTheOutputAndNamesTheEntriesInTheManifest(t *testing.T) {
	dst := t.TempDir()
	if err := build(viteOutput(t, nil), dst); err != nil {
		t.Fatal(err)
	}
	m := readManifest(t, dst)
	for logical, want := range map[string]string{"app.js": "app.AbC123_x.js", "app.css": "app.DeF456-y.css", "theme.js": "theme.GhI789.js"} {
		if m[logical] != want {
			t.Errorf("manifest[%s] = %q, want %q", logical, m[logical], want)
		}
	}
	// Everything else the build made is served too, under its own name: a font the
	// style sheet names, or a chunk the app loads on demand.
	if m["asset.geist.Zz1.woff2"] != "asset.geist.Zz1.woff2" {
		t.Errorf("a font is not listed, so the server would 404 it: %v", m)
	}
	for _, name := range m {
		if _, err := os.Stat(filepath.Join(dst, name)); err != nil {
			t.Errorf("the manifest names %s, which was not written", name)
		}
	}
	if _, listed := m["index.html"]; listed {
		t.Error("index.html is Vite's page; the server renders its own and must not serve this one")
	}
}

func TestABuildNeedsExactlyOneOfEachEntry(t *testing.T) {
	for name, extra := range map[string]map[string]string{
		"no app script":     {"app.AbC123_x.js": ""},
		"no app stylesheet": {"app.DeF456-y.css": ""},
		"two app scripts":   {"app.Other99.js": "x"},
		"two stylesheets":   {"app.Other99.css": "x"},
	} {
		dst := t.TempDir()
		if err := build(viteOutput(t, extra), dst); err == nil {
			t.Errorf("%s: accepted", name)
		}
		if _, err := os.Stat(filepath.Join(dst, "manifest.json")); err == nil {
			t.Errorf("%s: a failed build left a manifest, so the server would serve half a UI", name)
		}
	}
}

func TestABuildRefusesWhatThePagePolicyWouldBlockAtRunTime(t *testing.T) {
	for name, extra := range map[string]map[string]string{
		"a data: URL in the stylesheet": {"app.DeF456-y.css": "a{background:url(data:image/png;base64,AAAA)}"},
		"a remote import":               {"app.DeF456-y.css": "@import url(https://fonts.example.com/x.css);"},
		"a remote url":                  {"app.DeF456-y.css": "a{background:url(//cdn.example.com/x.png)}"},
	} {
		if err := build(viteOutput(t, extra), t.TempDir()); err == nil {
			t.Errorf("%s: accepted, and the browser would drop it", name)
		}
	}
}

func TestABuildRefusesANameThatCouldEscapeTheDirectoryOrHideAFile(t *testing.T) {
	src := viteOutput(t, nil)
	if err := os.Mkdir(filepath.Join(src, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := build(src, t.TempDir()); err == nil || !strings.Contains(err.Error(), "flat") {
		t.Errorf("err = %v, want the refusal that says the build must be flat", err)
	}
	src = viteOutput(t, map[string]string{".hidden.js": "x"})
	if err := build(src, t.TempDir()); err == nil {
		t.Error("a dotfile was accepted")
	}
}

func TestABuildLeavesNoFileOfAnEarlierBuildAndKeepsThePlaceholders(t *testing.T) {
	dst := t.TempDir()
	for _, keep := range []string{".gitignore", "README.md"} {
		if err := os.WriteFile(filepath.Join(dst, keep), []byte("keep"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dst, "app.Old000.js"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := build(viteOutput(t, nil), dst); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dst)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if strings.Contains(strings.Join(names, " "), "Old000") {
		t.Errorf("a file of an earlier build is still there: %v", names)
	}
	for _, keep := range []string{".gitignore", "README.md"} {
		if _, err := os.Stat(filepath.Join(dst, keep)); err != nil {
			t.Errorf("%s, which keeps the directory in the repository, was removed", keep)
		}
	}
}
