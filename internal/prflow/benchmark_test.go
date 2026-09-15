package prflow

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/diff"
)

// BenchmarkAnalyzeChangedGraphs measures the complete extractor path on small
// and medium multi-file packages. It intentionally uses source bytes supplied
// by the caller so the result covers parsing, type checking, edge collection,
// and bounded traversal without involving a VCS or model provider.
func BenchmarkAnalyzeChangedGraphs(b *testing.B) {
	for _, files := range []int{8, 64} {
		b.Run(fmt.Sprintf("files=%d", files), func(b *testing.B) {
			input := benchmarkGraphInput(files)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				result, err := Analyze(context.Background(), input)
				if err != nil {
					b.Fatal(err)
				}
				if result.Status == StatusUnavailable || len(result.Nodes) == 0 {
					b.Fatalf("unexpected benchmark result: status=%s nodes=%d explanation=%q", result.Status, len(result.Nodes), result.Explanation)
				}
			}
		})
	}
}

// BenchmarkAnalyzeMaterializationBounds measures source-backed analysis while
// exercising the file and byte caps used by the CLI. The source is deliberately
// larger than the configured budget; callers can inspect -benchmem results
// alongside the materialization tests when tuning the defaults.
func BenchmarkAnalyzeMaterializationBounds(b *testing.B) {
	const fileCount = 96
	source := newBenchmarkRevisionSource(fileCount)
	changes := make(diff.Files, 0, fileCount)
	for i := 0; i < fileCount; i++ {
		name := fmt.Sprintf("pkg/file%03d.go", i)
		changes = append(changes, &diff.File{
			Path:  name,
			Kind:  diff.ChangeModified,
			Hunks: []diff.Hunk{{Lines: []diff.Line{{Kind: diff.LineAdded, NewLine: 3}}}},
		})
	}
	request := Request{
		Changes: changes,
		Source:  source,
		Options: Options{MaxFiles: 32, MaxBytes: 32 << 10, MaxNodes: 500, MaxEdges: 1000},
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result, err := Analyze(context.Background(), request)
		if err != nil {
			b.Fatal(err)
		}
		if result.Status == StatusUnavailable || result.Coverage.Files > request.Options.MaxFiles || result.Coverage.Bytes > request.Options.MaxBytes {
			b.Fatalf("materialization exceeded bounds: status=%s files=%d bytes=%d", result.Status, result.Coverage.Files, result.Coverage.Bytes)
		}
	}
}

// BenchmarkAnalyzeMissingDependency exercises the failure-tolerant type-check
// path on a small multi-module-shaped tree. The extractor must remain bounded
// and return a structured result when an import is unavailable; this benchmark
// does not treat the current result status as a quality claim. A qualification
// run should pair it with a coverage assertion that names the missing import.
func BenchmarkAnalyzeMissingDependency(b *testing.B) {
	input := Request{
		Files: []SourceFile{
			{Path: "app/cmd/main.go", Content: []byte("package main\nfunc main() { Changed() }\nfunc Changed() { missing.Run() }\n"), ChangedLines: []int{3}},
			{Path: "lib/helper.go", Content: []byte("package lib\nfunc Helper() {}\n")},
			{Path: "app/go.mod", Content: []byte("module example.local/app\n")},
			{Path: "lib/go.mod", Content: []byte("module example.local/lib\n")},
		},
		Options: Options{MaxFiles: 8, MaxBytes: 8 << 10, MaxNodes: 64, MaxEdges: 128},
	}
	// Keep the import unresolved without reaching out to a module proxy. The
	// package remains parseable, so this isolates missing metadata from parser
	// cost and network behavior.
	input.Files[0].Content = []byte("package main\nimport \"example.invalid/missing\"\nfunc main() { Changed() }\nfunc Changed() { missing.Run() }\n")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result, err := Analyze(context.Background(), input)
		if err != nil || result.Status == StatusUnavailable {
			b.Fatalf("unexpected missing-dependency result: status=%s err=%v explanation=%q", result.Status, err, result.Explanation)
		}
	}
}

func benchmarkGraphInput(fileCount int) Request {
	files := make([]SourceFile, 0, fileCount)
	for i := 0; i < fileCount; i++ {
		name := fmt.Sprintf("pkg/file%03d.go", i)
		fn := fmt.Sprintf("F%03d", i)
		body := fmt.Sprintf("package p\nfunc %s() {\n", fn)
		if i+1 < fileCount {
			body += fmt.Sprintf("F%03d()\n", i+1)
		}
		body += "}\n"
		changed := []int(nil)
		if i == 0 {
			changed = []int{2}
		}
		files = append(files, SourceFile{Path: name, Content: []byte(body), ChangedLines: changed})
	}
	return Request{Files: files, Options: Options{MaxCallerDepth: 8, MaxCalleeDepth: 4, MaxNodes: 500, MaxEdges: 1000}}
}

type benchmarkRevisionSource struct {
	files map[string][]byte
}

func newBenchmarkRevisionSource(fileCount int) benchmarkRevisionSource {
	files := make(map[string][]byte, fileCount)
	for i := 0; i < fileCount; i++ {
		name := fmt.Sprintf("pkg/file%03d.go", i)
		files[name] = []byte("package p\n// " + strings.Repeat("x", 2048) + "\nfunc Changed() {}\n")
	}
	return benchmarkRevisionSource{files: files}
}

func (s benchmarkRevisionSource) ReadFile(_ context.Context, _ Revision, name string) ([]byte, error) {
	return s.files[name], nil
}

func (s benchmarkRevisionSource) ReadFileLimit(_ context.Context, _ Revision, name string, limit int) ([]byte, error) {
	data := s.files[name]
	if len(data) > limit {
		return nil, ErrSourceLimit
	}
	if data == nil {
		return nil, errors.New("benchmark source file not found")
	}
	return data, nil
}
