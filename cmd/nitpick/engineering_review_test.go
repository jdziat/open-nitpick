package main

import (
	"testing"

	"github.com/jdziat/open-nitpick/internal/diff"
)

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
