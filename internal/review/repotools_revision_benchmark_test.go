package review

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/vcs"
)

func pinnedBenchmarkTree(b *testing.B, content string) (string, vcs.Ref) {
	b.Helper()
	root := b.TempDir()
	if err := os.WriteFile(filepath.Join(root, "app.go"), []byte(content), 0o644); err != nil {
		b.Fatal(err)
	}
	cmd := exec.Command("git", "init", "-q", root)
	if out, err := cmd.CombinedOutput(); err != nil {
		b.Fatalf("git init: %v: %s", err, out)
	}
	return root, vcs.Ref{Head: commitLookupTree(b, root)}
}

func BenchmarkPinnedRepositoryLookupWindows(b *testing.B) {
	root, ref := pinnedBenchmarkTree(b, strings.Repeat("package sample\n", 4096))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		tools := newRepoTools(root, nil, ref)
		for line := 1; line <= 12; line++ {
			got := tools.read(b.Context(), "app.go", line, line)
			if !strings.HasSuffix(got, " package sample\n") {
				b.Fatalf("read window %d = %q", line, got)
			}
		}
	}
}

func BenchmarkPinnedRepositoryRepeatedSearch(b *testing.B) {
	root, ref := pinnedBenchmarkTree(b, "package sample\n")
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		tools := newRepoTools(root, nil, ref)
		for range 8 {
			if got := tools.grep(b.Context(), "package", "*.go"); got != "app.go:1:package sample" {
				b.Fatalf("search = %q", got)
			}
		}
	}
}
