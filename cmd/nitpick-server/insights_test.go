package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nitpick-oss/nitpick/internal/notary"
)

func insightsOpts(t *testing.T) *options {
	t.Helper()
	o := &options{addr: "127.0.0.1:0", dir: t.TempDir(), oidcIssuer: notary.GitHubIssuer}
	for _, tok := range []string{"ci:write:wsecret", "agent:read:rsecret"} {
		if err := o.insightsTokens.Set(tok); err != nil {
			t.Fatal(err)
		}
	}
	o.config = writeFile(t, o.dir, "server.yaml", "components: [insights]\n", 0o600)
	return o
}

func status(t *testing.T, method, url, token, body string) int {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), method, url, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

func TestInsightsAloneAcceptsAReviewFromAWriteTokenAndKeepsItOnDisk(t *testing.T) {
	o := insightsOpts(t)
	h, closeStore, err := build(context.Background(), o, quiet)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h)
	defer ts.Close()
	const sub = `{"repo":"acme/api","report":{"result":"clean","complete":true,"findings":[]}}`
	if got := status(t, "POST", ts.URL+"/v1/reviews", "wsecret", sub); got != 201 {
		t.Fatalf("post = %d", got)
	}
	if got := status(t, "POST", ts.URL+"/v1/reviews", "rsecret", sub); got != 403 {
		t.Fatalf("a read token posted: %d", got)
	}
	if got := status(t, "GET", ts.URL+"/v1/records/"+strings.Repeat("a", 64), "wsecret", ""); got != 404 {
		t.Fatalf("the notary is not configured, so its routes must not exist: %d", got)
	}
	ts.Close()
	closeStore()
	b, err := os.ReadFile(filepath.Join(o.dir, "insights", "reviews.jsonl"))
	if err != nil || !strings.Contains(string(b), `"repo":"acme/api"`) {
		t.Fatalf("the review is not on disk: %v %q", err, b)
	}
}

func TestInsightsAndTheNotaryRunSideBySideInOneServer(t *testing.T) {
	o := insightsOpts(t)
	if err := o.tokens.Set("ci:authoritative:nsecret"); err != nil {
		t.Fatal(err)
	}
	o.config = writeFile(t, o.dir, "both.yaml", "components: [notary, insights]\n", 0o600)
	h, closeStore, err := build(context.Background(), o, quiet)
	if err != nil {
		t.Fatal(err)
	}
	defer closeStore()
	ts := httptest.NewServer(h)
	defer ts.Close()
	if got := status(t, "GET", ts.URL+"/v1/records", "nsecret", ""); got != 200 {
		t.Errorf("notary list = %d", got)
	}
	if got := status(t, "GET", ts.URL+"/v1/insights/overview", "rsecret", ""); got != 200 {
		t.Errorf("insights overview = %d", got)
	}
	// Tokens of one component open nothing of the other.
	if got := status(t, "GET", ts.URL+"/v1/records", "wsecret", ""); got != 401 {
		t.Errorf("an insights token listed notary records: %d", got)
	}
	if got := status(t, "GET", ts.URL+"/v1/insights/overview", "nsecret", ""); got != 401 {
		t.Errorf("a notary token read insights: %d", got)
	}
}

func TestInsightsWithNoWayInIsRefusedAtStartUp(t *testing.T) {
	o := &options{addr: "x", dir: t.TempDir(), oidcIssuer: notary.GitHubIssuer}
	o.config = writeFile(t, o.dir, "server.yaml", "components: [insights]\n", 0o600)
	if _, _, err := build(context.Background(), o, quiet); err == nil || !strings.Contains(err.Error(), "-insights-token") {
		t.Fatalf("err = %v, want the missing credentials named", err)
	}
}

func TestAnInsightsTokenFlagTakesWriteOrReadAndNothingElse(t *testing.T) {
	var toks insightsTokens
	hash := strings.Repeat("ab", 32)
	for _, ok := range []string{"ci:write:plain", "a:read:sha256:" + hash} {
		if err := toks.Set(ok); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "ci", "ci:write", "ci:write:", ":read:x", "ci:authoritative:x", "ci:admin:x", "ci:read:sha256:short"} {
		if err := (&insightsTokens{}).Set(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

func TestAnInsightsServerDoesNotLeaveItsDataDirectoryLockedAfterAFailedStartUp(t *testing.T) {
	o := insightsOpts(t)
	if err := o.tokens.Set("ci:authoritative:nsecret"); err != nil {
		t.Fatal(err)
	}
	o.config = writeFile(t, o.dir, "both.yaml", "components: [notary, insights]\nroles:\n  - {role: nonsense, user: bob}\n", 0o600)
	if _, _, err := build(context.Background(), o, quiet); err == nil {
		t.Fatal("a role no component declares was accepted")
	}
	o.config = writeFile(t, o.dir, "ok.yaml", "components: [notary, insights]\n", 0o600)
	_, closeStore, err := build(context.Background(), o, quiet)
	if err != nil {
		t.Fatalf("the failed start-up left the directory locked: %v", err)
	}
	closeStore()
}

// A build that fails after the insights store opened must give its directory back,
// whichever step failed, or the next start in the same process is refused.
func TestABuildThatFailsAfterInsightsOpenedReleasesItsDirectory(t *testing.T) {
	t.Run("notary has no way in", func(t *testing.T) {
		o := insightsOpts(t)
		o.config = writeFile(t, o.dir, "both.yaml", "components: [insights, notary]\n", 0o600)
		if _, _, err := build(context.Background(), o, quiet); err == nil || !strings.Contains(err.Error(), "-token") {
			t.Fatalf("err = %v, want the notary's missing credentials", err)
		}
		o.config = writeFile(t, o.dir, "ok.yaml", "components: [insights]\n", 0o600)
		_, closeStore, err := build(context.Background(), o, quiet)
		if err != nil {
			t.Fatalf("the failed build left the insights directory locked: %v", err)
		}
		closeStore()
	})
	t.Run("notary directory already in use", func(t *testing.T) {
		o := insightsOpts(t)
		if err := o.tokens.Set("ci:authoritative:nsecret"); err != nil {
			t.Fatal(err)
		}
		o.config = writeFile(t, o.dir, "n.yaml", "components: [notary]\n", 0o600)
		_, holdNotary, err := build(context.Background(), o, quiet)
		if err != nil {
			t.Fatal(err)
		}
		defer holdNotary()
		o.config = writeFile(t, o.dir, "both.yaml", "components: [insights, notary]\n", 0o600)
		if _, _, err := build(context.Background(), o, quiet); err == nil {
			t.Fatal("a second notary on a held directory was accepted")
		}
		o.config = writeFile(t, o.dir, "ins.yaml", "components: [insights]\n", 0o600)
		_, closeStore, err := build(context.Background(), o, quiet)
		if err != nil {
			t.Fatalf("the failed build left the insights directory locked: %v", err)
		}
		closeStore()
	})
}

func TestInsightsWithASignInButNoRoleThatGrantsItIsRefusedAtStartUp(t *testing.T) {
	for name, tc := range map[string]struct {
		roles   string
		wantErr bool
	}{
		"a sign-in and no rule": {"", true},
		// The notary runs beside insights, so authoritative is a role the server
		// knows and the rule is valid. It grants nothing on insights.
		"a rule for the notary's role": {"roles:\n  - {role: authoritative, user: alice}\n", true},
		"a rule that grants insights":  {"roles:\n  - {role: insights, user: alice}\n", false},
		"a rule for a group":           {"roles:\n  - {role: insights, group: eng}\n", false},
	} {
		o := &options{addr: "x", dir: t.TempDir(), oidcIssuer: notary.GitHubIssuer}
		if err := o.tokens.Set("ci:authoritative:nsecret"); err != nil {
			t.Fatal(err)
		}
		users := writeFile(t, o.dir, "users", quickUser, 0o600)
		o.config = writeFile(t, o.dir, "server.yaml", "components: [notary, insights]\nauth:\n  password:\n    users_file: "+users+"\n"+tc.roles, 0o600)
		_, closeStore, err := build(context.Background(), o, quiet)
		switch {
		case tc.wantErr && (err == nil || !strings.Contains(err.Error(), "grants the insights role")):
			t.Errorf("%s: err = %v, want the refusal that names the insights role", name, err)
		case !tc.wantErr && err != nil:
			t.Errorf("%s: refused: %v", name, err)
		}
		if err == nil {
			closeStore()
		}
	}
}
