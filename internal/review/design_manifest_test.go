package review

import (
	"testing"

	"github.com/jdziat/open-nitpick/internal/bundle"
	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/diff"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

func manifestDiff(line diff.Line) *diff.File {
	return &diff.File{Path: "go.mod", Kind: diff.ChangeModified, Hunks: []diff.Hunk{{NewStart: 1, NewLines: 1, Lines: []diff.Line{line}}}}
}

// A dependency bump rewrites requirements only, so it plans no design task.
// Planning it fanned out to every package in the module.
func TestDesignAssemblyKeepsDependencyBumpFromPlanningTheModule(t *testing.T) {
	cfg := config.Defaults()
	cfg.Review.MaxFilesPerRequest = 2
	tree := vcs.NewSnapshot(vcs.NewLocal(t.TempDir(), nil), map[string][]byte{
		"go.mod": []byte("module example.com/app\n\nrequire github.com/a/b v1.1.0\n"),
		"a/a.go": []byte("package a\nfunc A() {}\n"),
		"b/b.go": []byte("package b\nimport \"example.com/app/a\"\nfunc B() { a.A() }\n"),
		"c/c.go": []byte("package c\nimport \"example.com/app/b\"\nfunc C() { b.B() }\n"),
		"d/d.go": []byte("package d\nimport \"example.com/app/c\"\nfunc D() { c.C() }\n"),
	})
	engine := &Engine{Config: cfg, Provider: tree}

	bump := engine.assembleDesign(t.Context(), vcs.Ref{}, diff.Files{manifestDiff(diff.Line{Kind: diff.LineAdded, NewLine: 3, Content: "require github.com/a/b v1.1.0"})}, bundle.Reserve{})
	if len(bump.Design.Tasks) != 0 || len(bump.Plan.Skipped) != 0 {
		t.Fatalf("dependency bump planned the module: tasks=%d skipped=%+v", len(bump.Design.Tasks), bump.Plan.Skipped)
	}

	moved := engine.assembleDesign(t.Context(), vcs.Ref{}, diff.Files{manifestDiff(diff.Line{Kind: diff.LineAdded, NewLine: 3, Content: "replace example.com/a => ../a"})}, bundle.Reserve{})
	if len(moved.Design.Tasks) == 0 {
		t.Fatal("replace directive did not expand to the module's packages")
	}
}
