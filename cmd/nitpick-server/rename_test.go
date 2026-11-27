package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The command is called nitpick-server. The name it replaced survives in two
// places on purpose, the changelog (history) and the migration note in
// docs/notary.md, and a deploy file, a doc or a message that still tells a person
// to run the old name sends them to a command that does not exist.
func TestNoFileStillNamesTheCommandThisOneReplaced(t *testing.T) {
	const old = "nitpick-notary"
	allowed := map[string]bool{"CHANGELOG.md": true, filepath.Join("docs", "notary.md"): true}
	root := filepath.Join("..", "..")
	skipDir := map[string]bool{".git": true, "public": true, ".website": true, "node_modules": true, "testdata": true, "bin": true}

	scanned, found := 0, 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDir[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		info, err := d.Info()
		if err != nil || info.Size() > 2<<20 || allowed[rel] || rel == filepath.Join("cmd", "nitpick-server", "rename_test.go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil || strings.IndexByte(string(b[:min(len(b), 512)]), 0) >= 0 {
			return nil // unreadable or binary
		}
		scanned++
		if strings.Contains(string(b), old) {
			found++
			t.Errorf("%s still names %s", rel, old)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Rule 10: the walk read the tree. A walk that found nothing because it read
	// nothing is the same silence.
	if scanned < 200 {
		t.Fatalf("scanned only %d files; the walk is not reading the repository", scanned)
	}
	_ = found
}

// The allowed migration note really is there and really names the old command, so
// the exemption above is not for a file that no longer says anything.
func TestTheMigrationNoteNamesTheOldCommandAndTheNewOne(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "notary.md"))
	if err != nil {
		t.Fatal(err)
	}
	// Prose wraps, so the note is compared with its whitespace collapsed.
	text := strings.Join(strings.Fields(string(b)), " ")
	for _, want := range []string{"## Moving from nitpick-notary", "`nitpick-server -addr", "ghcr.io/nitpick-oss/nitpick-server", "there is no pointer release"} {
		if !strings.Contains(text, want) {
			t.Errorf("docs/notary.md lacks %q", want)
		}
	}
}
