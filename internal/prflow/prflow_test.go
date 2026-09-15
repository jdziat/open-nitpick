package prflow

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jdziat/open-nitpick/internal/diff"
)

func TestAnalyzeFindsChangedDirectCallAndCaller(t *testing.T) {
	src := []byte(`package main
import "os"
func main() { run() }
func run() { changed(); _ = os.WriteFile }
func changed() { helper() }
func helper() {}
`)
	res, err := Analyze(context.Background(), Request{Files: []SourceFile{{Path: "main.go", Content: src, ChangedLines: []int{5}}}, Options: Options{MaxCallerDepth: 2, MaxCalleeDepth: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusComplete {
		t.Fatalf("status = %s, want complete (%s)", res.Status, res.Explanation)
	}
	if len(res.Nodes) < 3 {
		t.Fatalf("nodes = %d, want changed declaration and neighbours: %+v", len(res.Nodes), res.Nodes)
	}
	var changed, caller bool
	for _, n := range res.Nodes {
		if n.Label == "main.changed" && n.Changed {
			changed = true
		}
		if n.Label == "main.run" {
			caller = true
		}
	}
	if !changed || !caller {
		t.Fatalf("nodes missing changed=%v caller=%v: %+v", changed, caller, res.Nodes)
	}
	var direct bool
	for _, e := range res.Edges {
		if e.From == "main.changed" && e.To == "main.helper" && e.Resolution == ResolutionResolved {
			direct = true
		}
	}
	if !direct {
		t.Fatalf("direct edge missing: %+v", res.Edges)
	}
}

func TestAnalyzeReportsDynamicCallBoundary(t *testing.T) {
	src := []byte(`package p
func Changed() { var f func(); f() }
`)
	res, err := Analyze(context.Background(), Request{Files: []SourceFile{{Path: "p.go", Content: src, ChangedLines: []int{2}}}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status == StatusUnavailable || res.Status == StatusSkipped {
		t.Fatalf("unexpected status %s", res.Status)
	}
	var found bool
	for _, n := range res.Nodes {
		if n.Boundary && strings.Contains(n.Reason, "dynamic") {
			found = true
		}
	}
	if !found {
		t.Fatalf("dynamic boundary missing: nodes=%+v edges=%+v", res.Nodes, res.Edges)
	}
}

func TestAnalyzeCountsBoundariesWithinNodeLimit(t *testing.T) {
	src := []byte(`package p
func Changed() { var f func(); f() }
`)
	result, err := Analyze(context.Background(), Request{Files: []SourceFile{{Path: "p.go", Content: src, ChangedLines: []int{2}}}, Options: Options{MaxNodes: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusPartial || len(result.Nodes) != 1 || len(result.Edges) != 0 || !hasOmission(result.Coverage.Omissions, "limit_reached") {
		t.Fatalf("boundary escaped node limit: %+v", result)
	}
}

func TestAnalyzeHonorsNodeAndEdgeLimitsAndRetainsChangedRoot(t *testing.T) {
	src := []byte(`package p
func Changed() { A(); B(); C() }
func A() {}
func B() {}
func C() {}
`)
	res, err := Analyze(context.Background(), Request{Files: []SourceFile{{Path: "p.go", Content: src, ChangedLines: []int{2}}}, Options: Options{MaxNodes: 1, MaxEdges: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusPartial {
		t.Fatalf("status = %s, want partial", res.Status)
	}
	if len(res.Nodes) != 1 || !res.Nodes[0].Changed {
		t.Fatalf("changed root was not retained: %+v", res.Nodes)
	}
	if len(res.Coverage.Omissions) == 0 {
		t.Fatal("limit omission missing")
	}
}

func TestAnalyzeNoGoFilesIsSkipped(t *testing.T) {
	res, err := Analyze(context.Background(), Request{Files: []SourceFile{{Path: "README.md", Content: []byte("text")}}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusSkipped || !strings.Contains(res.Explanation, "Go") {
		t.Fatalf("result = %+v", res)
	}
}

func TestAnalyzeLoadsChangedGoFromSource(t *testing.T) {
	provider := memorySource{files: map[string][]byte{"cmd/main.go": []byte("package main\nfunc main() { changed() }\nfunc changed() {}\n")}}
	changes := diff.Files{&diff.File{Path: "cmd/main.go", Kind: diff.ChangeModified, Hunks: []diff.Hunk{{Lines: []diff.Line{{Kind: diff.LineAdded, NewLine: 3}}}}}}
	res, err := Analyze(context.Background(), Request{Head: Revision{SHA: "abc"}, Changes: changes, Source: provider, Options: Options{Timeout: time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Nodes) == 0 {
		t.Fatalf("source-backed analysis produced no nodes: %+v", res)
	}
}

func TestAnalyzeHonorsGoBuildTags(t *testing.T) {
	src := []byte(`//go:build integration

package p
func Changed() { helper() }
func helper() {}
`)
	request := Request{Files: []SourceFile{{Path: "p.go", Content: src, ChangedLines: []int{4}}}}
	without, err := Analyze(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if without.Status != StatusUnavailable || !hasOmission(without.Coverage.Omissions, "build_tags") {
		t.Fatalf("without tag = %+v, want unavailable build-tags omission", without)
	}
	with, err := Analyze(context.Background(), Request{Files: request.Files, Options: Options{BuildTags: []string{"integration"}}})
	if err != nil {
		t.Fatal(err)
	}
	if with.Status != StatusComplete || len(with.Nodes) == 0 {
		t.Fatalf("with tag = %+v, want complete result", with)
	}
}

func TestAnalyzeKeepsSameNamedPackagesDistinct(t *testing.T) {
	files := []SourceFile{
		{Path: "one/p.go", Content: []byte("package p\nfunc Changed() { Helper() }\nfunc Helper() {}\n"), ChangedLines: []int{2}},
		{Path: "two/p.go", Content: []byte("package p\nfunc Changed() { Helper() }\nfunc Helper() {}\n"), ChangedLines: []int{2}},
	}
	result, err := Analyze(context.Background(), Request{Files: files})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, node := range result.Nodes {
		if seen[node.ID] {
			t.Fatalf("duplicate node ID %q: %+v", node.ID, result.Nodes)
		}
		seen[node.ID] = true
	}
	for _, want := range []string{"one/p.Changed", "two/p.Changed", "one/p.Helper", "two/p.Helper"} {
		if !seen[want] {
			t.Errorf("missing distinct node %q: %+v", want, result.Nodes)
		}
	}
}

func TestAnalyzeTypeChecksAllFilesInOnePackage(t *testing.T) {
	files := []SourceFile{
		{Path: "p/changed.go", Content: []byte("package p\nfunc Changed() { Helper() }\n"), ChangedLines: []int{2}},
		{Path: "p/helper.go", Content: []byte("package p\nfunc Helper() {}\n")},
	}
	result, err := Analyze(context.Background(), Request{Files: files})
	if err != nil {
		t.Fatal(err)
	}
	for _, edge := range result.Edges {
		if edge.From == "p/p.Changed" && edge.To == "p/p.Helper" && edge.Resolution == ResolutionResolved {
			return
		}
	}
	t.Fatalf("resolved cross-file edge missing: %+v", result.Edges)
}

func TestAnalyzeUsesNoFlowForAnIsolatedDeclaration(t *testing.T) {
	result, err := Analyze(context.Background(), Request{Files: []SourceFile{{Path: "p.go", Content: []byte("package p\nfunc Changed() {}\n"), ChangedLines: []int{2}}}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusNoFlow || len(result.Flows) != 0 {
		t.Fatalf("result = %+v, want no_flow without a relationship", result)
	}
}

func TestAnalyzeRejectsUnknownEntrypoint(t *testing.T) {
	result, err := Analyze(context.Background(), Request{Files: []SourceFile{{Path: "p.go", Content: []byte("package p\nfunc Known() {}\n")}}, Options: Options{Entrypoints: []string{"p.Missing"}}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusUnavailable || !strings.Contains(result.Explanation, "p.Missing") {
		t.Fatalf("result = %+v, want unavailable unknown entrypoint", result)
	}
}

func TestAnalyzeSelectsPathLineEntrypoint(t *testing.T) {
	result, err := Analyze(context.Background(), Request{Files: []SourceFile{{Path: "cmd/main.go", Content: []byte("package main\nfunc run() { helper() }\nfunc helper() {}\n")}}, Options: Options{Entrypoints: []string{"cmd/main.go:2"}}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusComplete || len(result.Edges) != 1 {
		t.Fatalf("result = %+v, want selected path:line flow", result)
	}
}

func TestAnalyzeKeepsDeletedDeclarationsAsRemoved(t *testing.T) {
	changes := diff.Files{&diff.File{Path: "gone.go", OldPath: "gone.go", Kind: diff.ChangeDeleted}}
	result, err := Analyze(context.Background(), Request{Base: Revision{SHA: "base1234"}, Changes: changes, Source: memorySource{files: map[string][]byte{"gone.go": []byte("package p\nfunc Gone() {}\n")}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Nodes) != 1 || result.Nodes[0].State != ChangeRemoved || !result.Nodes[0].Changed {
		t.Fatalf("deleted source was not represented as a removed declaration: %+v", result)
	}
}

func TestAnalyzeBoundsSourceReadsBeforeMaterializingFiles(t *testing.T) {
	reads := 0
	source := countingSource{reads: &reads}
	changes := diff.Files{
		&diff.File{Path: "a.go", Kind: diff.ChangeModified, Hunks: []diff.Hunk{{Lines: []diff.Line{{Kind: diff.LineAdded, NewLine: 2}}}}},
		&diff.File{Path: "b.go", Kind: diff.ChangeModified, Hunks: []diff.Hunk{{Lines: []diff.Line{{Kind: diff.LineAdded, NewLine: 2}}}}},
	}
	result, err := Analyze(context.Background(), Request{Changes: changes, Source: source, Options: Options{MaxFiles: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if reads != 1 || result.Status != StatusPartial || !hasOmission(result.Coverage.Omissions, "max_files") {
		t.Fatalf("reads=%d result=%+v, want one bounded source read and partial coverage", reads, result)
	}
}

func TestAnalyzeReportsPartialCoverageWhenOneSourceReadFails(t *testing.T) {
	source := failingSource{files: map[string][]byte{"a.go": []byte(`package p
func Changed() {}
`)}}
	changes := diff.Files{
		&diff.File{Path: "a.go", Kind: diff.ChangeModified, Hunks: []diff.Hunk{{Lines: []diff.Line{{Kind: diff.LineAdded, NewLine: 2}}}}},
		&diff.File{Path: "b.go", Kind: diff.ChangeModified, Hunks: []diff.Hunk{{Lines: []diff.Line{{Kind: diff.LineAdded, NewLine: 2}}}}},
	}
	result, err := Analyze(context.Background(), Request{Changes: changes, Source: source})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusPartial || !hasOmission(result.Coverage.Omissions, "source_unavailable") {
		t.Fatalf("result = %+v, want partial source coverage", result)
	}
}

func TestAnalyzeIncludesUnchangedTreeContextWhenRequested(t *testing.T) {
	source := treeSource{files: map[string][]byte{
		"cmd/main.go":   []byte("package main\nfunc main() { changed() }\n"),
		"cmd/change.go": []byte("package main\nfunc changed() {}\n"),
	}}
	changes := diff.Files{&diff.File{Path: "cmd/change.go", Kind: diff.ChangeModified, Hunks: []diff.Hunk{{Lines: []diff.Line{{Kind: diff.LineAdded, NewLine: 2}}}}}}
	result, err := Analyze(context.Background(), Request{Changes: changes, Source: source, Options: Options{IncludeUnchanged: true}})
	if err != nil {
		t.Fatal(err)
	}
	for _, edge := range result.Edges {
		if edge.From == "cmd/main.main" && edge.To == "cmd/main.changed" {
			return
		}
	}
	t.Fatalf("unchanged caller was not included: %+v", result)
}

type memorySource struct{ files map[string][]byte }

func (m memorySource) ReadFile(_ context.Context, _ Revision, p string) ([]byte, error) {
	b, ok := m.files[p]
	if !ok {
		return nil, context.Canceled
	}
	return b, nil
}

type countingSource struct{ reads *int }

func (s countingSource) ReadFile(_ context.Context, _ Revision, _ string) ([]byte, error) {
	*s.reads++
	return []byte("package p\nfunc Changed() {}\n"), nil
}

type failingSource struct{ files map[string][]byte }

func (s failingSource) ReadFile(_ context.Context, _ Revision, name string) ([]byte, error) {
	data, ok := s.files[name]
	if !ok {
		return nil, context.Canceled
	}
	return data, nil
}

type treeSource struct{ files map[string][]byte }

func (s treeSource) ReadFile(_ context.Context, _ Revision, name string) ([]byte, error) {
	data, ok := s.files[name]
	if !ok {
		return nil, context.Canceled
	}
	return data, nil
}

func (s treeSource) ListDir(_ context.Context, _ Revision, dir string) ([]string, error) {
	if dir != "" {
		dir += "/"
	}
	entries := map[string]bool{}
	for name := range s.files {
		if !strings.HasPrefix(name, dir) {
			continue
		}
		rest := strings.TrimPrefix(name, dir)
		if slash := strings.IndexByte(rest, '/'); slash >= 0 {
			rest = rest[:slash+1]
		}
		entries[rest] = true
	}
	var out []string
	for entry := range entries {
		out = append(out, entry)
	}
	sort.Strings(out)
	return out, nil
}
