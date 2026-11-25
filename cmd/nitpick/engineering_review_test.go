package main

import (
	"testing"

	"github.com/nitpick-oss/nitpick/internal/config"
	"github.com/nitpick-oss/nitpick/internal/diff"
	"github.com/nitpick-oss/nitpick/internal/practices"
	"github.com/nitpick-oss/nitpick/internal/review"
	"github.com/nitpick-oss/nitpick/internal/standards"
	"github.com/nitpick-oss/nitpick/internal/vcs"
)

func TestEngineeringReviewHonorsTheAcceptedSkipMarkers(t *testing.T) {
	for _, tc := range []struct {
		name     string
		explicit bool
		markers  []string
		body     string
		wantSkip bool
	}{
		{"configured profile", false, []string{"[skip nitpick]"}, "Already reviewed. [skip nitpick]", true},
		{"explicit profile", true, []string{"[skip custom]"}, "Already reviewed. [skip custom]", true},
		{"markers disabled", false, nil, "[skip nitpick]", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Defaults()
			cfg.Review.SkipMarkers = tc.markers
			if !tc.explicit {
				cfg.Practices.Profile = "engineering"
			}
			policy := &engineeringReviewPolicy{source: fixedPolicy{cfg: cfg}, explicit: tc.explicit}
			accepted, _, err := policy.ResolvePolicy(t.Context(), vcs.Ref{}, nil, []string{config.FileName})
			if err != nil || accepted == nil {
				t.Fatalf("accepted policy: %v, %v", accepted, err)
			}
			if got := skipReason(&vcs.PullRequest{Body: tc.body}, false, accepted.Review.SkipMarkers); (got != "") != tc.wantSkip {
				t.Fatalf("skip reason = %q, want skip %v: the engineering profile must preserve the accepted operator policy", got, tc.wantSkip)
			}
		})
	}
}

func TestUnknownSelectedAnalyzerCannotProveNoApplicablePaths(t *testing.T) {
	cfg := config.Defaults()
	cfg.Linters.Enabled = []string{"golangci-lnit"}
	check := reviewAnalyzerCheck([]standards.File{{Path: "app.go", Src: []byte("package app\n")}}, &review.Report{}, cfg)
	if check.State != practices.Unavailable {
		t.Fatalf("unknown analyzer was treated as completed or inapplicable: %+v", check)
	}
	cfg.Linters.Enabled = []string{"ruff"}
	check = reviewAnalyzerCheck([]standards.File{{Path: "app.go", Src: []byte("package app\n")}}, &review.Report{}, cfg)
	if check.State != practices.NotApplicable {
		t.Fatalf("known Python analyzer over Go paths should be inapplicable: %+v", check)
	}
}

// A dependency bump edits go.mod and go.sum and nothing else. Treating its
// go.mod as a boundary change left the required design-boundaries check
// unavailable, which failed #188 with "the review did not complete" on a pull
// request that contained no Go.
func TestADependencyBumpIsNotAGoBoundaryChange(t *testing.T) {
	line := func(kind diff.LineKind, s string) diff.Line { return diff.Line{Kind: kind, Content: s} }
	manifest := func(path string, lines ...diff.Line) *diff.File {
		return &diff.File{Path: path, Kind: diff.ChangeModified, Hunks: []diff.Hunk{{Lines: lines}}}
	}
	bump := manifest("go.mod", line(diff.LineRemoved, "\tgolang.org/x/mod v0.41.0"), line(diff.LineAdded, "\tgolang.org/x/mod v0.42.0"))
	sums := manifest("go.sum", line(diff.LineAdded, "golang.org/x/mod v0.42.0 h1:abc="))

	for _, tc := range []struct {
		name  string
		files diff.Files
		want  bool
	}{
		{"bump and checksums", diff.Files{bump, sums}, false},
		{"module renamed", diff.Files{manifest("go.mod", line(diff.LineAdded, "module example.com/new"))}, true},
		{"replace added", diff.Files{manifest("go.mod", line(diff.LineAdded, "replace a => ../a"))}, true},
		{"go source", diff.Files{bump, &diff.File{Path: "app.go", Kind: diff.ChangeModified}}, true},
		{"manifest renamed away", diff.Files{{Path: "x.txt", OldPath: "sub/go.mod", Kind: diff.ChangeRenamed}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := goBoundaryChange(tc.files); got != tc.want {
				t.Fatalf("goBoundaryChange = %v, want %v", got, tc.want)
			}
		})
	}
}

// A push that moves nothing narrows the re-read set to zero files. The required
// design-boundaries check then saw an empty change, stayed unavailable, and
// failed the review of a release pull request that holds only a changelog and
// a manifest, on every run after the first.
func TestAnIncrementalRunWithNoReReadFilesStillJudgesTheWholeChange(t *testing.T) {
	unavailable := func() practices.Check {
		return practices.Check{ID: "design-boundaries", State: practices.Unavailable, Reason: "configured Go boundary rules have no Go source to inspect", Required: true}
	}
	doc := &diff.File{Path: "CHANGELOG.md", Kind: diff.ChangeModified}
	goSrc := &diff.File{Path: "app.go", Kind: diff.ChangeModified}

	for _, tc := range []struct {
		name   string
		change diff.Files
		want   practices.State
	}{
		{"whole change holds no Go", diff.Files{doc}, practices.NotApplicable},
		{"whole change holds Go", diff.Files{doc, goSrc}, practices.Unavailable},
		{"no diff was read at all", nil, practices.Unavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			check := unavailable()
			relaxBoundaries(&check, tc.change)
			if check.State != tc.want {
				t.Fatalf("state = %s, want %s", check.State, tc.want)
			}
		})
	}
}
