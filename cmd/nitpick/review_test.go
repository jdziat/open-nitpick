package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
)

// TestResolvePullRequestIntent is the regression test for `-pr 42` silently
// reviewing the working tree.
//
// A partially-specified pull request used to fall through to a local review,
// which produces a real-looking review of entirely the wrong code. That is
// worse than an error, because nothing in the output says so.
func TestResolvePullRequestIntent(t *testing.T) {
	// Ensure no Actions environment leaks in.
	t.Setenv("GITHUB_REPOSITORY", "")
	t.Setenv("GITHUB_REF", "")
	t.Setenv("PR_NUMBER", "")

	cases := []struct {
		name     string
		flags    reviewFlags
		wantPR   bool
		wantErr  bool
		errNames []string
	}{
		{
			name:   "no pull request requested",
			flags:  reviewFlags{},
			wantPR: false,
		},
		{
			name:   "fully specified",
			flags:  reviewFlags{owner: "acme", repoName: "widgets", pr: 42},
			wantPR: true,
		},
		{
			name:     "pr without owner or repo",
			flags:    reviewFlags{pr: 42},
			wantErr:  true,
			errNames: []string{"-owner", "-repo-name"},
		},
		{
			name:     "owner without repo or pr",
			flags:    reviewFlags{owner: "acme"},
			wantErr:  true,
			errNames: []string{"-repo-name", "-pr"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ref, gotPR, err := resolvePullRequest(&tc.flags)

			if tc.wantErr {
				if err == nil {
					t.Fatalf("want an error; got ref=%+v wantsPR=%v", ref, gotPR)
				}
				for _, name := range tc.errNames {
					if !strings.Contains(err.Error(), name) {
						t.Errorf("error should name the missing flag %s, got: %v", name, err)
					}
				}
				return
			}

			if err != nil {
				t.Fatalf("resolvePullRequest: %v", err)
			}
			if gotPR != tc.wantPR {
				t.Errorf("wantsPR = %v, want %v", gotPR, tc.wantPR)
			}
			if tc.wantPR && (ref.Owner != "acme" || ref.Repo != "widgets" || ref.Number != 42) {
				t.Errorf("ref = %+v", ref)
			}
		})
	}
}

func TestResolvePullRequestFromActionsEnvironment(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "acme/widgets")
	t.Setenv("GITHUB_REF", "refs/pull/7/merge")
	t.Setenv("PR_NUMBER", "")

	ref, wantsPR, err := resolvePullRequest(&reviewFlags{})
	if err != nil {
		t.Fatalf("resolvePullRequest: %v", err)
	}
	if !wantsPR {
		t.Fatal("an Actions pull_request event should select the forge provider")
	}
	if ref.Owner != "acme" || ref.Repo != "widgets" || ref.Number != 7 {
		t.Errorf("ref = %+v", ref)
	}
}

// TestDryRunKeepsTheRequestedSource is the regression test for -dry-run
// silently switching what gets reviewed.
//
// Dry run must suppress publishing, not swap the source: reviewing the working
// tree when the user named PR 42 is a real-looking review of the wrong code.
func TestDryRunKeepsTheRequestedSource(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "")
	t.Setenv("GITHUB_REF", "")
	t.Setenv("PR_NUMBER", "")
	t.Setenv("GITHUB_TOKEN", "test-token")
	t.Setenv("NITPICK_GITHUB_TOKEN", "")

	f := reviewFlags{owner: "acme", repoName: "widgets", pr: 42, dryRun: true}

	provider, ref, err := selectProvider(&f, t.TempDir())
	if err != nil {
		t.Fatalf("selectProvider: %v", err)
	}

	if ref.Number != 42 {
		t.Errorf("ref = %+v, want pull request 42", ref)
	}
	if !strings.Contains(provider.Name(), "github") {
		t.Errorf("provider = %q, want the GitHub source even under dry run", provider.Name())
	}
	if !strings.Contains(provider.Name(), "dry run") {
		t.Errorf("provider = %q, want publishing suppressed", provider.Name())
	}
}

func TestDryRunWithoutPullRequestStaysLocal(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "")
	t.Setenv("GITHUB_REF", "")
	t.Setenv("PR_NUMBER", "")

	provider, _, err := selectProvider(&reviewFlags{dryRun: true}, t.TempDir())
	if err != nil {
		t.Fatalf("selectProvider: %v", err)
	}
	if provider.Name() != "local" {
		t.Errorf("provider = %q, want local", provider.Name())
	}
}

func TestPullRequestReviewRequiresAToken(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "")
	t.Setenv("GITHUB_REF", "")
	t.Setenv("PR_NUMBER", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("NITPICK_GITHUB_TOKEN", "")

	f := reviewFlags{owner: "acme", repoName: "widgets", pr: 42}

	if _, _, err := selectProvider(&f, t.TempDir()); err == nil {
		t.Fatal("want an error when no token is available")
	}
}

func TestFirstNonEmpty(t *testing.T) {
	if got := firstNonEmpty("", "  ", "b", "c"); got != "b" {
		t.Errorf("firstNonEmpty = %q, want b", got)
	}
	if got := firstNonEmpty("", ""); got != "" {
		t.Errorf("firstNonEmpty = %q, want empty", got)
	}
}

// captureStdout runs fn with os.Stdout redirected and returns what it printed.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}

	saved := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = saved }()

	done := make(chan string, 1)
	go func() {
		var buf strings.Builder
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()

	fn()

	_ = w.Close()
	return <-done
}

// TestExplainConfigNamesDiscardedKeys covers the command's answer to the
// question an operator actually asks it: what did my config resolve to, and
// what did you throw away?
//
// The discarded keys used to appear only in `nitpick review`'s log, so
// explain-config printed byte-identical "Models:" sections for a config whose
// endpoint was honored and one whose endpoint was silently stripped. That makes
// its silence worthless as evidence — the property this repository's committed
// default depends on could not be checked with the command built to check it.
func TestExplainConfigNamesDiscardedKeys(t *testing.T) {
	// Untrusted: this is the CI default and the case that strips keys.
	t.Setenv(config.EnvTrustConfigEndpoints, "")

	write := func(t *testing.T, body string) string {
		t.Helper()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, config.FileName), []byte(body), 0o644); err != nil {
			t.Fatalf("write config: %v", err)
		}
		return dir
	}

	t.Run("a config naming an endpoint says so", func(t *testing.T) {
		dir := write(t, `
models:
  default:
    provider: openrouter
    model: attacker/model
    base_url: https://attacker.example/v1
    api_key_env: OPENROUTER_API_KEY
`)
		out := captureStdout(t, func() {
			if err := runExplainConfig([]string{"-repo", dir}); err != nil {
				t.Errorf("runExplainConfig: %v", err)
			}
		})

		for _, want := range []string{"base_url", "api_key_env"} {
			if !strings.Contains(out, want) {
				t.Errorf("output never mentions the discarded %s key:\n%s", want, out)
			}
		}
		// The endpoint itself must not survive into the resolved model line.
		if strings.Contains(out, "attacker.example") {
			t.Errorf("the stripped endpoint is still shown as the resolved one:\n%s", out)
		}
	})

	t.Run("the shipped default discards nothing", func(t *testing.T) {
		// The whole point of the openrouter provider, asserted through the CLI
		// rather than through config.LoadFile: a committed default that leaned
		// on base_url would work only for whoever exported the trust variable.
		out := captureStdout(t, func() {
			if err := runExplainConfig([]string{"-repo", "../.."}); err != nil {
				t.Errorf("runExplainConfig: %v", err)
			}
		})

		if strings.Contains(out, "Ignored (untrusted config") {
			t.Errorf("this repository's own config depends on keys an untrusted config cannot supply:\n%s", out)
		}
		if !strings.Contains(out, "openrouter/") {
			t.Errorf("the default no longer resolves to openrouter:\n%s", out)
		}
	})
}
