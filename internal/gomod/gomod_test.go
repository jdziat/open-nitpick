package gomod

import (
	"os"
	"path/filepath"
	"testing"
)

// Versions answers from the root go.mod, and answers nothing when there is
// none.
//
// Nothing is the load-bearing case. A knowledge entry bounded to a version is
// kept when the version is unknown, so this returning a guess rather than
// nothing would silence entries on the strength of it.
func TestVersionsReadsTheRootModule(t *testing.T) {
	dir := t.TempDir()
	if got := Versions(dir); got != nil {
		t.Errorf("a directory with no go.mod reported %v, want nothing", got)
	}

	write(t, filepath.Join(dir, "go.mod"), "module example.com/x\n\ngo 1.21\n")
	got := Versions(dir)
	if len(got) != 1 || got["go"] != "1.21" {
		t.Errorf("Versions = %v, want map[go:1.21]", got)
	}

	// A module declaring no version is not a module with no answer: the go
	// tool assumes one, and so does this.
	write(t, filepath.Join(dir, "go.mod"), "module example.com/x\n")
	if got := Versions(dir)["go"]; got != AssumedLanguage {
		t.Errorf("Versions = %q for a go.mod with no directive, want %q", got, AssumedLanguage)
	}
}

// A `go` line inside a block is a block entry, not the module's directive.
//
// This is the case a second, simpler parser would get wrong, which is the
// reason there is one parser.
func TestVersionsIgnoresAGoLineInsideABlock(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "go.mod"), "module example.com/x\n\nrequire (\n\tgo 9.99\n)\n\ngo 1.22\n")
	if got := Versions(dir)["go"]; got != "1.22" {
		t.Errorf("Versions = %q, want 1.22: a require entry was read as the directive", got)
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
