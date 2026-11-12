package vcs

import (
	"slices"
	"testing"
)

func TestCodeownersLastMatchingRuleWinsAndUnownedMeansNil(t *testing.T) {
	co := ParseCodeowners([]byte(`
# comment
*                 @everyone
/docs/            @docs-team
*.go              @gophers @org/core   # trailing comment
/internal/secret/ @security
/internal/secret/public.txt
!negated          @nobody
`))
	cases := []struct {
		path string
		want []string
	}{
		{"README.md", []string{"@everyone"}},
		{"docs/a/b.md", []string{"@docs-team"}},
		{"cmd/x/main.go", []string{"@gophers", "@org/core"}},
		{"internal/secret/key.go", []string{"@security"}},
		// A later rule with no owners un-owns the path on purpose.
		{"internal/secret/public.txt", nil},
		{"docs", []string{"@everyone"}},
	}
	for _, c := range cases {
		if got := co.OwnersOf(c.path); !slices.Equal(got, c.want) {
			t.Errorf("OwnersOf(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestCodeownersPatternAnchoringAndGlobs(t *testing.T) {
	co := ParseCodeowners([]byte(`
build/logs/       @logs
/top.txt          @root
apps/**/config.yml @cfg
src/*.js          @js
`))
	cases := []struct {
		path string
		want []string
	}{
		{"build/logs/x.log", []string{"@logs"}},
		{"deep/build/logs/x.log", nil},
		{"top.txt", []string{"@root"}},
		{"sub/top.txt", nil},
		{"apps/a/b/config.yml", []string{"@cfg"}},
		{"apps/config.yml", []string{"@cfg"}},
		{"src/a.js", []string{"@js"}},
		{"src/nested/a.js", nil},
	}
	for _, c := range cases {
		if got := co.OwnersOf(c.path); !slices.Equal(got, c.want) {
			t.Errorf("OwnersOf(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestCodeownersEmptyFileHasNoUsableRule(t *testing.T) {
	if !ParseCodeowners([]byte("# only a comment\n\n")).Empty() {
		t.Error("a file of comments reported rules")
	}
	if !(*Codeowners)(nil).Empty() {
		t.Error("a nil file reported rules")
	}
}

// The examples GitHub's documentation gives for a wildcard in the last segment.
func TestCodeownersWildcardInTheLastSegmentStaysAtThatDepth(t *testing.T) {
	co := ParseCodeowners([]byte("docs/* docs@example.com @docs\napps/ @octocat\n"))
	if got := co.OwnersOf("docs/getting-started.md"); len(got) != 2 {
		t.Errorf("docs/getting-started.md owners = %v, want the docs/* rule", got)
	}
	if got := co.OwnersOf("docs/build-app/troubleshooting.md"); got != nil {
		t.Errorf("docs/build-app/troubleshooting.md owners = %v, want none: docs/* does not reach nested files", got)
	}
	if got := co.OwnersOf("apps/a/b.go"); len(got) != 1 {
		t.Errorf("apps/a/b.go owners = %v, want @octocat", got)
	}
}
