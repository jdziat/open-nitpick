package server

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// The sources in the repository, built by the build step, are accepted by the
// server's own loader and served: this is the whole path from `make ui` to a
// page with a working style sheet, and it is the only test that runs the shipped
// CSS and JS through it.
func TestTheShippedSourcesBuildIntoAUIThatTheServerServes(t *testing.T) {
	dist := t.TempDir()
	cmd := exec.Command("go", "run", "./uibuild", "-src", "ui_src", "-dst", dist)
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
	js := between(t, page, `src="/ui/assets/`, `"`)
	for name, want := range map[string]string{css: "text/css", js: "text/javascript"} {
		resp, body := e.do("GET", "/ui/assets/"+name, nil, nil)
		if resp.StatusCode != 200 || len(body) < 50 {
			t.Errorf("%s = %d (%d bytes)", name, resp.StatusCode, len(body))
		}
		ct := resp.Header.Get("Content-Type")
		javascript := want == "text/javascript" && strings.Contains(ct, "javascript")
		if !strings.HasPrefix(ct, want) && !javascript {
			t.Errorf("%s served as %q, want %s", name, ct, want)
		}
	}
	// The script obeys the policy the server sends: no inline code, no eval.
	_, script := e.do("GET", "/ui/assets/"+js, nil, nil)
	for _, banned := range []string{"eval(", "new Function", "innerHTML", "document.write", "fetch(", "XMLHttpRequest"} {
		if strings.Contains(script, banned) {
			t.Errorf("the script uses %s, which the page's policy and its purpose do not need", banned)
		}
	}
}
