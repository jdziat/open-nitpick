package gomod

import (
	"os"
	"path/filepath"
	"testing"
)

// A submodule's files are judged against its own go.mod.
//
// The Go language version is a property of the main module, and a repository
// can hold several. Judging a submodule on 1.21 against a root on 1.25 drops
// an entry that applies: a confident wrong answer, which is worse than the
// unknown the callers here are built to keep everything on.
func TestVersionsForReadsTheOwningModule(t *testing.T) {
	// The root sits inside another directory that is itself a module, so a
	// path climbing out has a real go.mod to find. Without the containment
	// check the walk reads it and answers 1.9 for a file this repository does
	// not contain.
	outside := t.TempDir()
	write(t, filepath.Join(outside, "go.mod"), "module example.com/outside\n\ngo 1.9\n")
	root := filepath.Join(outside, "repo")
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "go.mod"), "module example.com/x\n\ngo 1.25\n")
	if err := os.MkdirAll(filepath.Join(root, "tools", "deep"), 0o750); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "tools", "go.mod"), "module example.com/x/tools\n\ngo 1.21\n")

	for name, tc := range map[string]struct {
		paths []string
		want  string
	}{
		"a root file":            {[]string{"main.go"}, "1.25"},
		"a submodule file":       {[]string{"tools/run.go"}, "1.21"},
		"below the submodule":    {[]string{"tools/deep/run.go"}, "1.21"},
		"two files, one module":  {[]string{"tools/a.go", "tools/b.go"}, "1.21"},
		"two files, two modules": {[]string{"main.go", "tools/a.go"}, ""},
		"a path that climbs out": {[]string{"../elsewhere/a.go"}, ""},
		"no paths":               {nil, ""},
	} {
		t.Run(name, func(t *testing.T) {
			got := VersionsFor(root, tc.paths)
			if tc.want == "" {
				if got != nil {
					t.Errorf("VersionsFor = %v, want nothing", got)
				}
				return
			}
			if got["go"] != tc.want {
				t.Errorf("VersionsFor = %v, want go %s", got, tc.want)
			}
		})
	}

	// No checkout is no answer, rather than the process working directory.
	if got := VersionsFor("", []string{"main.go"}); got != nil {
		t.Errorf("VersionsFor with no root = %v, want nothing", got)
	}
	if got := Versions(""); got != nil {
		t.Errorf("Versions with no root = %v, want nothing", got)
	}
}

// A go.mod carrying a block comment is not a case this parser owes an answer.
//
// The modules reference: "Comments start with // and run to the end of a line.
// /* */ comments are not allowed." Such a file does not load, so the go tool
// has no reading of it either. Pinned because it reads like a parser gap and
// has been raised as one.
func TestABlockCommentIsNotAGoModComment(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "go.mod"), "/* not legal here */\nmodule example.com/x\n\ngo 1.21\n")

	// Whatever this answers, it must not be a version the file does not
	// declare, and the caller keeps every entry when the answer is nothing.
	if got := Versions(dir)["go"]; got != "1.21" && got != AssumedLanguage && got != "" {
		t.Errorf("Versions = %q, which is neither the declared version nor an abstention", got)
	}
}

// A symlink inside the checkout cannot read a go.mod outside it.
//
// The containment check compares cleaned paths, and a link is clean. Without
// resolving it, a directory in the repository pointing outward walks up into
// a foreign module and answers with its version.
func TestASymlinkCannotEscapeTheCheckout(t *testing.T) {
	outside := t.TempDir()
	write(t, filepath.Join(outside, "go.mod"), "module example.com/outside\n\ngo 1.9\n")
	if err := os.MkdirAll(filepath.Join(outside, "pkg"), 0o750); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	write(t, filepath.Join(root, "go.mod"), "module example.com/x\n\ngo 1.25\n")
	if err := os.Symlink(filepath.Join(outside, "pkg"), filepath.Join(root, "linked")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if got := VersionsFor(root, []string{"linked/a.go"}); got != nil {
		t.Errorf("VersionsFor through a symlink = %v, want nothing", got)
	}
}

// A submodule whose go.mod does not parse answers nothing, not its parent's
// version.
//
// LanguageVersion cannot say whether a file is absent or unreadable, so
// without this the walk continues upward and hands the root's version to a
// module that declares something else. Keeping every entry is the answer for a
// module nothing is known about.
func TestAnUnreadableSubmoduleDoesNotInheritItsParent(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "go.mod"), "module example.com/x\n\ngo 1.25\n")
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o750); err != nil {
		t.Fatal(err)
	}

	// A stray closing paren, which LanguageVersion refuses rather than guess
	// at. The file does not load for the go tool either.
	write(t, filepath.Join(root, "sub", "go.mod"), "module example.com/x/sub\n)\n\ngo 1.21\n")

	if got := VersionsFor(root, []string{"sub/a.go"}); got != nil {
		t.Errorf("VersionsFor = %v, want nothing rather than the root's version", got)
	}
	if got := VersionsFor(root, []string{"a.go"})["go"]; got != "1.25" {
		t.Errorf("the root still answers %q, want 1.25", got)
	}
}

// A go.mod with no module directive is not a module.
//
// LanguageVersion answers 1.16 for it, which is the go tool's assumption and
// what the linter roster's coverage note needs. A knowledge entry bounded to a
// version must not be judged against a number read out of a file the go tool
// would refuse, so retrieval abstains and keeps every entry.
func TestAFileWithNoModuleDirectiveIsNotAModule(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "go.mod"), "go 1.21\n")

	if got := Versions(dir); got != nil {
		t.Errorf("Versions = %v, want nothing", got)
	}
	// The measured fallback is untouched, because the linter roster reads it.
	if _, _, ok := LanguageVersion(filepath.Join(dir, "go.mod")); !ok {
		t.Error("LanguageVersion stopped answering; that reading is the roster's, not retrieval's")
	}

	write(t, filepath.Join(dir, "go.mod"), "module example.com/x\n\ngo 1.21\n")
	if got := Versions(dir)["go"]; got != "1.21" {
		t.Errorf("Versions = %q, want 1.21", got)
	}
}

// A parenthesis inside a quoted path is not a block.
//
// go.mod permits a quoted token, and a directory named "libs (v1)" is legal.
// Counted as a block opener it leaves the depth above zero for the rest of the
// file, so the `go` directive after it is read as block content and a module
// on 1.25 answers with the 1.16 assumption.
func TestAQuotedParenIsNotABlock(t *testing.T) {
	dir := t.TempDir()
	// One unmatched paren inside the quotes. A balanced pair cancels itself and
	// would pass with the naive count, proving nothing.
	write(t, filepath.Join(dir, "go.mod"),
		"module example.com/x\n\nreplace example.com/y => \"./libs (v1/y\"\n\ngo 1.25\n")

	if got := Versions(dir)["go"]; got != "1.25" {
		t.Errorf("Versions = %q, want 1.25: a quoted paren opened a block", got)
	}

	// A real block still counts, and a stray closer still refuses the file.
	write(t, filepath.Join(dir, "go.mod"), "module example.com/x\n\nrequire (\n\tgo 9.99\n)\n\ngo 1.22\n")
	if got := Versions(dir)["go"]; got != "1.22" {
		t.Errorf("Versions = %q, want 1.22", got)
	}
	write(t, filepath.Join(dir, "go.mod"), "module example.com/x\n)\n\ngo 1.22\n")
	if got := Versions(dir); got != nil {
		t.Errorf("Versions = %v, want an abstention on an unbalanced file", got)
	}
}
