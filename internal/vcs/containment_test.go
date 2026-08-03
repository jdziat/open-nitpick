package vcs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestLocalFileContentContainment is the regression test for exfiltration of
// files outside the checkout.
//
// Reviewing an untrusted branch locally (`gh pr checkout` then `nitpick
// review`) lets the branch's own diff choose which paths are read and sent to
// the model endpoint. Before containment, a committed symlink pointing at
// ~/.ssh/id_rsa put a private key in the prompt.
func TestLocalFileContentContainment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on windows")
	}

	dir := newRepo(t)

	// A secret living outside the repository, as a real one would.
	outside := t.TempDir()
	secretPath := filepath.Join(outside, "id_rsa")
	if err := os.WriteFile(secretPath, []byte("PRIVATE-KEY-MATERIAL\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Symlinks a hostile branch could legitimately commit.
	if err := os.Symlink(secretPath, filepath.Join(dir, "notes.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../", filepath.Join(dir, "docs", "up")); err != nil {
		t.Fatal(err)
	}

	local := NewLocal(dir, nil)
	ctx := context.Background()

	attacks := []struct {
		name string
		path string
	}{
		{"symlink to an absolute path outside the repo", "notes.md"},
		{"parent traversal", "../id_rsa"},
		{"deep parent traversal", "../../../../etc/passwd"},
		{"absolute path", "/etc/passwd"},
		{"traversal through a symlinked directory", "docs/up/id_rsa"},
		{"embedded traversal", "docs/../../id_rsa"},
	}

	for _, tc := range attacks {
		t.Run(tc.name, func(t *testing.T) {
			got, err := local.FileContent(ctx, Ref{}, tc.path)
			if err == nil {
				t.Fatalf("read succeeded and returned %d bytes; want it refused", len(got))
			}
			if !errors.Is(err, ErrNotFound) {
				t.Errorf("err = %v, want ErrNotFound so assembly degrades rather than aborting", err)
			}
			if strings.Contains(string(got), "PRIVATE-KEY-MATERIAL") {
				t.Fatal("secret material was returned")
			}
		})
	}
}

func TestLocalFileContentStillReadsOrdinaryFiles(t *testing.T) {
	// Containment must not break the normal path.
	dir := newRepo(t)
	write(t, dir, "pkg/sub/file.go", "package sub\n")

	local := NewLocal(dir, nil)

	got, err := local.FileContent(context.Background(), Ref{}, "pkg/sub/file.go")
	if err != nil {
		t.Fatalf("FileContent: %v", err)
	}
	if !strings.Contains(string(got), "package sub") {
		t.Errorf("content = %q", got)
	}
}

// TestLocalFileContentUnderSymlinkedRoot guards the regression the containment
// fix could plausibly introduce: repository roots that are themselves symlinks
// are routine (macOS /tmp, some CI images) and must keep working.
func TestLocalFileContentUnderSymlinkedRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on windows")
	}

	real := newRepo(t)
	write(t, real, "a.go", "package a\n")

	link := filepath.Join(t.TempDir(), "link-to-repo")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	local := NewLocal(link, nil)

	got, err := local.FileContent(context.Background(), Ref{}, "a.go")
	if err != nil {
		t.Fatalf("a symlinked repository root must still work: %v", err)
	}
	if !strings.Contains(string(got), "package a") {
		t.Errorf("content = %q", got)
	}
}
