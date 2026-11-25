package review

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nitpick-oss/nitpick/internal/vcs"
)

func BenchmarkRepositoryLookupWindows(b *testing.B) {
	root := b.TempDir()
	if err := os.WriteFile(filepath.Join(root, "app.go"), []byte(strings.Repeat("package sample\n", 4096)), 0o644); err != nil {
		b.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			b.Fatalf("git: %v: %s", err, out)
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		tools := newRepoTools(root, nil, vcs.Ref{})
		for line := 1; line <= 12; line++ {
			got := tools.read(context.Background(), "app.go", line, line)
			if !strings.HasSuffix(got, " package sample\n") {
				b.Fatalf("read window %d = %q", line, got)
			}
		}
	}
}

func BenchmarkRepositoryRepeatedSearch(b *testing.B) {
	root := b.TempDir()
	if err := os.WriteFile(filepath.Join(root, "app.go"), []byte("package sample\n"), 0o644); err != nil {
		b.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			b.Fatalf("git: %v: %s", err, out)
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		tools := newRepoTools(root, nil, vcs.Ref{})
		for range 8 {
			if got := tools.grep(context.Background(), "package", "*.go"); got != "app.go:1:package sample" {
				b.Fatalf("search = %q", got)
			}
		}
	}
}
