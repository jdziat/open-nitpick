package main

import (
	"strings"
	"testing"
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
