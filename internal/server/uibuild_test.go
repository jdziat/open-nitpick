package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The whole path from the Vite build to a page the server serves: uibuild copies
// the output and writes the manifest, the server's loader accepts it, and every
// file the shell names is served. Only a built UI/dist/ exists after `npm run
// build`, so the test is skipped when it has not been run.
func TestTheBuiltUIGoesThroughUIBuildAndIsServedByTheServer(t *testing.T) {
	src := filepath.Join("..", "..", "ui", "dist")
	if _, err := os.Stat(src); err != nil {
		t.Skip("ui/dist does not exist; run `npm run build` in ui/ to exercise this path")
	}
	dist := t.TempDir()
	cmd := exec.Command("go", "run", "./uibuild", "-src", src, "-dst", dist)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the build failed: %v\n%s", err, out)
	}
	e := newEnv(t, Config{
		Components: []Component{&stubComponent{name: "stub"}},
		Providers:  []Provider{fakeProvider{id: "fake"}},
		Assets:     os.DirFS(dist),
	})
	e.signIn("alice")
	_, page := e.do("GET", "/ui/", nil, nil)
	css := between(t, page, `href="/ui/assets/`, `"`)
	js := between(t, page, `type="module" src="/ui/assets/`, `"`)
	for name, want := range map[string]string{css: "text/css", js: "javascript"} {
		resp, body := e.do("GET", "/ui/assets/"+name, nil, nil)
		if resp.StatusCode != 200 || len(body) < 50 {
			t.Errorf("%s = %d (%d bytes)", name, resp.StatusCode, len(body))
		}
		if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, want) {
			t.Errorf("%s served as %q, want %s", name, ct, want)
		}
	}
	// The font the style sheet names is in the manifest, or the browser gets a 404
	// and falls back to a system font with nothing to say why.
	_, sheet := e.do("GET", "/ui/assets/"+css, nil, nil)
	for _, ref := range regexpFindAll(`url\(([^)]+\.woff2)\)`, sheet) {
		name := ref[strings.LastIndex(ref, "/")+1:]
		if resp, _ := e.do("GET", "/ui/assets/"+name, nil, nil); resp.StatusCode != 200 {
			t.Errorf("the style sheet names %s, which the server answers %d", name, resp.StatusCode)
		}
	}
}
