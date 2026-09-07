package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/converse"
	"github.com/jdziat/open-nitpick/internal/review"
	"github.com/jdziat/open-nitpick/internal/vcs"
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

// TestExplainConfigNamesThePolicySource covers the question this command exists
// to answer. "What does my config resolve to" has a second half, whether it
// resolves at all for the change being reviewed, and an operator who reads the
// ignore list here and then watches a review not honor it has been told
// something untrue by omission.
func TestExplainConfigNamesThePolicySource(t *testing.T) {
	dir := t.TempDir()
	body := "models:\n  default:\n    provider: openrouter\n    model: anthropic/claude-sonnet-4.6\n"
	if err := os.WriteFile(filepath.Join(dir, config.FileName), []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	out := captureStdout(t, func() {
		if err := runExplainConfig([]string{"-repo", dir}); err != nil {
			t.Errorf("runExplainConfig: %v", err)
		}
	})

	for _, want := range []string{
		"Policy source",
		"may not supply the policy it is reviewed under",
		config.FileName,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output never mentions %q:\n%s", want, out)
		}
	}
}

// TestGateIgnoresTheChangesOwnFailOn is the last knob the invariant has to
// cover. The engine reviews a config-editing change under a policy resolved
// elsewhere, and then the CLI decides the exit status, so reading fail_on from
// the loaded file here would hand the change back the one setting that decides
// whether CI goes red, when every other setting comes from the resolved policy.
func TestGateIgnoresTheChangesOwnFailOn(t *testing.T) {
	// The configuration as the change wrote it: never fail the build.
	supplied := config.Defaults()
	supplied.Review.FailOn = config.SeverityNone

	// The policy the engine reviewed under.
	resolved := config.Defaults()
	resolved.Review.FailOn = config.SeverityWarning

	replaced := &review.Report{Policy: review.Policy{Config: resolved, Replaced: true}}

	if got := gate(replaced, "", supplied); got != config.SeverityWarning {
		t.Errorf("gate = %q, want the resolved policy's %q", got, config.SeverityWarning)
	}

	// The operator's flag is not the change's configuration, so it still wins.
	if got := gate(replaced, string(config.SeverityCritical), supplied); got != config.SeverityCritical {
		t.Errorf("gate = %q, want the -fail-on flag to outrank the resolved policy", got)
	}

	// An ordinary review gates on the configuration it ran under, unchanged.
	ordinary := &review.Report{Policy: review.Policy{Config: supplied}}
	if got := gate(ordinary, "", supplied); got != config.SeverityNone {
		t.Errorf("gate = %q, want the repository's own %q", got, config.SeverityNone)
	}
}

// TestTheReviewEngineIsWiredAgainstTheChangesOwnPolicy is the regression test
// for the whole defense being one unpinned line of wiring.
//
// Engine.Policy and Engine.Models are both optional (an offline driver has no
// base revision to resolve against and wires its own clients), so dropping
// either from this command is a clean build and a green suite, while every
// config-editing change is reviewed under the configuration it wrote for itself
// and gated on the fail_on it chose. internal/review tests the engine's half and
// internal/config tests the resolver's; nothing tested that this command joins
// them.
func TestTheReviewEngineIsWiredAgainstTheChangesOwnPolicy(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")

	repo := t.TempDir()
	hostile := "models:\n  default:\n    provider: openai\n    model: chosen-by-the-change\n" +
		"review:\n  ignore:\n    - \"**\"\n  fail_on: none\n"
	if err := os.WriteFile(filepath.Join(repo, config.FileName), []byte(hostile), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := loadConfig(repo, "")
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}

	engine := newEngine(&reviewFlags{}, repo, cfg, vcs.NewLocal(repo, io.Discard), slog.New(slog.DiscardHandler))

	if engine.Policy == nil {
		t.Fatal("no policy resolver: a change that edits .nitpick.yaml is reviewed under its own configuration")
	}

	// The checkout has no git history, so no accepted version can be read and
	// the resolver falls back to built-in defaults, which is still a policy the
	// change did not write, and the property being asserted.
	t.Setenv(config.EnvProvider, "openai")
	t.Setenv(config.EnvModel, "gpt-4o")

	resolved, modified, err := engine.Policy.ResolvePolicy(
		context.Background(), vcs.Ref{}, nil, []string{config.FileName, "src/app.go"})
	if err != nil {
		t.Fatalf("ResolvePolicy: %v", err)
	}
	if !modified {
		t.Fatal("the resolver was wired to a root or config it cannot match; nothing would be substituted")
	}
	if resolved.Ignored("src/app.go") {
		t.Error("the change's own ignore rule survived resolution")
	}

	if engine.Models == nil {
		t.Fatal("no model factory: the change still chooses the model that reviews it")
	}
	roles, err := engine.Models(resolved)
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	if got := roles.Review.String(); strings.Contains(got, "chosen-by-the-change") {
		t.Errorf("review model = %q, built from the configuration in the change", got)
	}
}

// TestDryRunResolvesPolicyLikeTheRealRun pins the forwarding that keeps a
// preview honest. vcs.BaseRevision asks for an interface, so a wrapper without
// the method is silently treated as unable to answer: dropping this one degrades
// every config-editing change to the defaults fallback under -dry-run alone, and
// prints a preview of a review that will not be the one published.
func TestDryRunResolvesPolicyLikeTheRealRun(t *testing.T) {
	forwarding := &dryRunProvider{source: &baseNamingProvider{rev: "abc1234"}, out: io.Discard}

	rev, err := vcs.BaseRevision(context.Background(), forwarding, vcs.Ref{})
	if err != nil {
		t.Fatalf("BaseRevision: %v", err)
	}
	if rev != "abc1234" {
		t.Errorf("BaseRevision = %q, want the wrapped forge's answer", rev)
	}

	// And a source that cannot answer must still be unable to, rather
	// than have the wrapper invent something plausible: the plausible answer is
	// the head under review.
	silent := &dryRunProvider{source: &vcs.GitHub{}, out: io.Discard}
	if _, err := silent.BaseRevision(context.Background(), vcs.Ref{}); err == nil {
		t.Error("want ErrNoBaseRevision from a source that cannot name one")
	}
}

// baseNamingProvider is a forge that can name a base revision. Only BaseRevision
// is exercised; the rest satisfies vcs.Provider.
type baseNamingProvider struct {
	*vcs.Local
	rev string
}

func (b *baseNamingProvider) Name() string { return "fake" }

func (b *baseNamingProvider) BaseRevision(context.Context, vcs.Ref) (string, error) {
	return b.rev, nil
}

// TestExplainConfigDoesNotPromiseSubstitutionForAnOutsideConfig covers the
// -config escape hatch, which is the configuration the defaults-fallback failure
// actively recommends. A change under review cannot edit a file outside the
// repository, so that file always applies, and the command told an operator who
// took its own advice the exact opposite.
func TestExplainConfigDoesNotPromiseSubstitutionForAnOutsideConfig(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "outside.yaml")
	body := "models:\n  default:\n    provider: openrouter\n    model: anthropic/claude-sonnet-4.6\n"
	if err := os.WriteFile(outside, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	out := captureStdout(t, func() {
		if err := runExplainConfig([]string{"-repo", t.TempDir(), "-config", outside}); err != nil {
			t.Errorf("runExplainConfig: %v", err)
		}
	})

	if strings.Contains(out, "base revision") {
		t.Errorf("a config outside the repository is never substituted away, but the command says it is:\n%s", out)
	}
	if !strings.Contains(out, "always") {
		t.Errorf("the command never says this file applies to every review:\n%s", out)
	}
}

// TestExplainConfigNamesTheFileItLookedFor covers the silence around a missing
// config. LoadFile treats an absent file as "use built-in defaults", so a
// mistyped -config path resolves to a configuration the operator never wrote,
// and the command that exists to answer "what does my config resolve to" must
// name the file it looked for rather than report defaults as though it read one.
func TestExplainConfigNamesTheFileItLookedFor(t *testing.T) {
	t.Setenv(config.EnvProvider, "openai")
	t.Setenv(config.EnvModel, "gpt-4o")

	missing := filepath.Join(t.TempDir(), "typo-does-not-exist.yaml")

	out := captureStdout(t, func() {
		if err := runExplainConfig([]string{"-repo", t.TempDir(), "-config", missing}); err != nil {
			t.Errorf("runExplainConfig: %v", err)
		}
	})

	if !strings.Contains(out, missing) {
		t.Errorf("output never names the file that was not there:\n%s", out)
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
// question an operator asks it: what did my config resolve to, and
// what did you throw away?
//
// The discarded keys used to appear only in `nitpick review`'s log, so
// explain-config printed byte-identical "Models:" sections for a config whose
// endpoint was honored and one whose endpoint was silently stripped. That makes
// its silence worthless as evidence, the property this repository's committed
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
		// The whole point of a compiled-in provider, asserted through the CLI
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
		if !strings.Contains(out, "synthetic/") {
			t.Errorf("the default no longer resolves to synthetic:\n%s", out)
		}
	})
}

// A pull request is skipped for a draft under -skip-draft, or for a marker
// anywhere a person can put one: the title, the body, or the head commit's
// message, in any case; and for nothing else.
func TestSkipReasonReadsDraftsAndMarkers(t *testing.T) {
	markers := []string{"[skip review]", "[skip nitpick]"}
	cases := []struct {
		name  string
		pr    vcs.PullRequest
		draft bool
		want  string
	}{
		{"plain", vcs.PullRequest{Title: "feat: x"}, true, ""},
		{"draft without the flag", vcs.PullRequest{Draft: true}, false, ""},
		{"draft with the flag", vcs.PullRequest{Draft: true}, true, "Draft"},
		{"title", vcs.PullRequest{Title: "wip [Skip Review]"}, false, "[skip review]"},
		{"body", vcs.PullRequest{Body: "notes\n\n[skip nitpick]\n"}, false, "[skip nitpick]"},
		{"head commit", vcs.PullRequest{HeadMessage: "fix: y\n\n[skip review]"}, false, "[skip review]"},
		{"a similar phrase", vcs.PullRequest{Body: "please skip the review of docs"}, false, ""},
	}
	for _, tc := range cases {
		got := skipReason(&tc.pr, tc.draft, markers)
		if (tc.want == "") != (got == "") || !strings.Contains(got, tc.want) {
			t.Errorf("%s: reason = %q, want it to contain %q", tc.name, got, tc.want)
		}
	}
	if skipReason(&vcs.PullRequest{Title: "[skip review]"}, false, nil) != "" {
		t.Error("with no markers configured nothing is skipped")
	}
}

// A comment event names its pull request itself; the repository comes from
// the flags or the Actions environment, since GITHUB_REF names a branch on
// a comment event and the review resolver would give up.
func TestRefForEventUsesTheEventNumberAndTheRepositoryEnv(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "o/r")
	ref, err := refForEvent(&reviewFlags{}, &converse.Event{Number: 16})
	if err != nil || ref.Owner != "o" || ref.Repo != "r" || ref.Number != 16 {
		t.Errorf("ref = %+v, %v", ref, err)
	}
	ref, err = refForEvent(&reviewFlags{owner: "a", repoName: "b"}, &converse.Event{Number: 2})
	if err != nil || ref.Owner != "a" || ref.Repo != "b" {
		t.Errorf("flags should win: %+v, %v", ref, err)
	}
	t.Setenv("GITHUB_REPOSITORY", "")
	if _, err := refForEvent(&reviewFlags{}, &converse.Event{Number: 2}); err == nil {
		t.Error("no repository anywhere must be an error")
	}
	t.Setenv("GITHUB_REPOSITORY", "o/r")
	if _, err := refForEvent(&reviewFlags{}, &converse.Event{}); err == nil {
		t.Error("an event with no pull request must be an error")
	}
}
