package practices

import (
	"testing"

	"github.com/jdziat/open-nitpick/internal/diff"
)

func modFile(kind diff.ChangeKind, path string, lines ...diff.Line) *diff.File {
	return &diff.File{Path: path, Kind: kind, Hunks: []diff.Hunk{{Lines: lines}}}
}

func TestManifestChangesPackagesOnlyWhenPackageEdgesCanMove(t *testing.T) {
	added := func(s string) diff.Line { return diff.Line{Kind: diff.LineAdded, Content: s} }
	removed := func(s string) diff.Line { return diff.Line{Kind: diff.LineRemoved, Content: s} }
	context := diff.Line{Kind: diff.LineContext, Content: "module example.com/app"}
	for _, tc := range []struct {
		name string
		file *diff.File
		want bool
	}{
		{"nil", nil, false},
		{"source file", modFile(diff.ChangeModified, "app.go", added("x")), false},
		{"dependency bump", modFile(diff.ChangeModified, "go.mod", context, removed("\tgithub.com/a/b v1.0.0"), added("\tgithub.com/a/b v1.1.0")), false},
		{"added require", modFile(diff.ChangeModified, "go.mod", added("require github.com/a/b v1.0.0")), false},
		{"go directive", modFile(diff.ChangeModified, "go.mod", removed("go 1.24"), added("go 1.25")), false},
		{"module renamed", modFile(diff.ChangeModified, "go.mod", removed("module example.com/old"), added("module example.com/new")), true},
		{"replace added", modFile(diff.ChangeModified, "go.mod", added("replace example.com/a => ../a")), true},
		{"replace block entry", modFile(diff.ChangeModified, "go.mod", added("\texample.com/a => ../a")), true},
		{"nested manifest added", modFile(diff.ChangeAdded, "nested/go.mod", added("module example.com/inner")), true},
		{"manifest deleted", modFile(diff.ChangeDeleted, "go.mod", removed("module example.com/app")), true},
		{"no hunks", &diff.File{Path: "go.mod", Kind: diff.ChangeModified}, true},
		{"workspace", modFile(diff.ChangeModified, "go.work", added("use ./b")), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ManifestChangesPackages(tc.file); got != tc.want {
				t.Fatalf("ManifestChangesPackages=%v, want %v", got, tc.want)
			}
		})
	}
}
