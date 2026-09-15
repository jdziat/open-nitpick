package prflow

import (
	"context"
	"fmt"
	"testing"

	"github.com/jdziat/open-nitpick/internal/diff"
)

func TestAnalyzeRetainsRemovedFunctionFromModifiedFile(t *testing.T) {
	source := changedRevisionSource{
		base: "package p\nfunc Gone() { helper() }\nfunc Stay() {}\nfunc helper() {}\n",
		head: "package p\nfunc Stay() {}\nfunc helper() {}\n",
	}
	changes := diff.Files{&diff.File{Path: "p.go", Kind: diff.ChangeModified, Hunks: []diff.Hunk{{Lines: []diff.Line{{Kind: diff.LineRemoved, OldLine: 2}}}}}}
	result, err := Analyze(context.Background(), Request{Base: Revision{SHA: "base"}, Head: Revision{SHA: "head"}, Source: source, Changes: changes})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, node := range result.Nodes {
		if node.Label == "p.Gone" {
			found = node.State == ChangeRemoved && node.Changed && node.Source.Line == 2
		}
		if node.Label == "p.Stay" && node.Changed {
			t.Fatalf("unchanged function after a removal was marked changed: %+v", node)
		}
	}
	if !found {
		t.Fatalf("removed function missing from base-side evidence: %+v", result)
	}
}

func TestAnalyzeMapsDeletedStatementToSurvivingFunction(t *testing.T) {
	source := changedRevisionSource{
		base: "package p\nfunc Changed() {\n helper()\n helper()\n}\nfunc helper() {}\n",
		head: "package p\nfunc Changed() {\n helper()\n}\nfunc helper() {}\n",
	}
	changes := diff.Files{&diff.File{Path: "p.go", Kind: diff.ChangeModified, Hunks: []diff.Hunk{{Lines: []diff.Line{{Kind: diff.LineRemoved, OldLine: 4}}}}}}
	result, err := Analyze(context.Background(), Request{Base: Revision{SHA: "base"}, Head: Revision{SHA: "head"}, Source: source, Changes: changes})
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range result.Nodes {
		if node.Label == "p.Changed" && node.State == ChangeModified && node.Changed {
			return
		}
	}
	t.Fatalf("deletion-only function edit was not seeded: %+v", result)
}

func TestAnalyzeMarksNewFileDeclarationsAsAdded(t *testing.T) {
	result, err := Analyze(context.Background(), Request{
		Files:   []SourceFile{{Path: "p.go", Content: []byte("package p\nfunc New() {}\n")}},
		Changes: diff.Files{&diff.File{Path: "p.go", Kind: diff.ChangeAdded, Hunks: []diff.Hunk{{Lines: []diff.Line{{Kind: diff.LineAdded, NewLine: 2}}}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Nodes) != 1 || result.Nodes[0].State != ChangeAdded {
		t.Fatalf("new file declaration state is not added: %+v", result)
	}
}

type changedRevisionSource struct{ base, head string }

func (s changedRevisionSource) ReadFile(_ context.Context, revision Revision, _ string) ([]byte, error) {
	switch revision.SHA {
	case "base":
		return []byte(s.base), nil
	case "head":
		return []byte(s.head), nil
	default:
		return nil, fmt.Errorf("unexpected revision %q", revision.SHA)
	}
}
