package server

import (
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

// A plain go build embeds only a placeholder. The server compiles, starts, and
// its UI pages say the UI was not built. This is the shipped binary of a
// developer who did not run make ui.
func TestABinaryWithoutTheBuiltUIServesTheStubPage(t *testing.T) {
	if _, err := fs.Stat(uiDist, "ui_dist/manifest.json"); err == nil {
		t.Skip("the UI was built into this tree (make ui), so the embedded files are not the placeholder")
	}
	e := newEnv(t, Config{
		Components: []Component{&stubComponent{name: "stub"}},
		Providers:  []Provider{fakeProvider{id: "fake"}},
		// No Assets: what is embedded in this test binary, which is the placeholder.
	})
	for _, path := range []string{"/ui/", "/ui/stub/", "/ui/stub/admin"} {
		resp, body := e.do("GET", path, nil, nil)
		if resp.StatusCode != 200 || !strings.Contains(body, "The UI was not built") {
			t.Errorf("%s = %d %q, want the stub page", path, resp.StatusCode, body)
		}
		if strings.Contains(body, "stylesheet") || strings.Contains(body, "<script") {
			t.Errorf("%s references an asset that does not exist: %q", path, body)
		}
	}
	// Rule 10: the stub is the UI being unbuilt. With the UI built the same URL
	// is the login redirect, so the stub above is not a page that is always shown.
	built := standard(t)
	if resp, _ := built.do("GET", "/ui/", nil, nil); resp.StatusCode != 303 {
		t.Errorf("a built UI at /ui/ = %d, want the login redirect", resp.StatusCode)
	}
	// The rest of the server works with no UI: components and health.
	if resp, body := e.do("GET", "/v1/stub/open", nil, nil); resp.StatusCode != 200 || body != "open" {
		t.Errorf("a component route in an unbuilt binary = %d %q", resp.StatusCode, body)
	}
}

// A manifest that is half there is not a built UI. Each is the stub, not a page
// with a link to a missing file.
func TestAManifestThatIsBrokenMeansTheUIWasNotBuilt(t *testing.T) {
	for name, fsys := range map[string]fstest.MapFS{
		"no manifest":               {},
		"not json":                  {"manifest.json": {Data: []byte("nope")}},
		"a file the manifest lacks": {"manifest.json": {Data: []byte(`{"app.css":"app.aaaaaaaaaaaa.css","app.js":"app.aaaaaaaaaaaa.js"}`)}},
		"only the style sheet":      {"manifest.json": {Data: []byte(`{"app.css":"app.a.css"}`)}, "app.a.css": {Data: []byte("x")}},
		"a name with a path":        {"manifest.json": {Data: []byte(`{"app.css":"../x.css","app.js":"app.j.js"}`)}, "app.j.js": {Data: []byte("x")}},
		"a name in a subdirectory":  {"manifest.json": {Data: []byte(`{"app.css":"sub/app.css","app.js":"app.j.js"}`)}, "sub/app.css": {Data: []byte("x")}, "app.j.js": {Data: []byte("x")}},
		"a name with a backslash":   {"manifest.json": {Data: []byte(`{"app.css":"a\\b.css","app.js":"app.j.js"}`)}, "a\\b.css": {Data: []byte("x")}, "app.j.js": {Data: []byte("x")}},
		"the manifest as an asset":  {"manifest.json": {Data: []byte(`{"app.css":"manifest.json","app.js":"app.j.js"}`)}, "app.j.js": {Data: []byte("x")}},
	} {
		e := newEnv(t, Config{
			Components: []Component{&stubComponent{name: "stub"}},
			Providers:  []Provider{fakeProvider{id: "fake"}},
			Assets:     fsys,
		})
		if _, body := e.do("GET", "/ui/", nil, nil); !strings.Contains(body, "The UI was not built") {
			t.Errorf("%s: /ui/ = %q, want the stub", name, body)
		}
	}
}

// Assets are served by their hashed name, only if the manifest lists them, with
// the headers a content-hashed file wants.
func TestAssetsAreServedOnlyByTheNamesTheManifestLists(t *testing.T) {
	e := standard(t)
	resp, body := e.do("GET", "/ui/assets/app.0123456789ab.css", nil, nil)
	if resp.StatusCode != 200 || body != "body{}" {
		t.Fatalf("a listed asset = %d %q", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/css") {
		t.Errorf("content type = %q", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("cache control = %q, want a long immutable lifetime for a hashed name", cc)
	}
	for _, name := range []string{"manifest.json", "app.css", "..%2fmanifest.json", "%2e%2e%2fmanifest.json", "nothing.js"} {
		if resp, _ := e.do("GET", "/ui/assets/"+name, nil, nil); resp.StatusCode != 404 {
			t.Errorf("/ui/assets/%s = %d, want 404", name, resp.StatusCode)
		}
	}
	// A page links to the hashed names, which is what makes the cache safe.
	e.signIn("alice")
	_, page := e.do("GET", "/ui/", nil, nil)
	if !strings.Contains(page, `href="/ui/assets/app.0123456789ab.css"`) || !strings.Contains(page, `src="/ui/assets/app.0123456789ab.js"`) {
		t.Errorf("the page does not link the built assets:\n%s", page)
	}
	if strings.Contains(page, "<script>") || strings.Contains(page, "style=") {
		t.Error("the page has inline script or style, which the policy would refuse")
	}
}
