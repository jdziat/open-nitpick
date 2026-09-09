// Package gomod reads what a module's go.mod declares.
//
// One reader, because two would disagree. The linter roster uses the declared
// language version to say which version-gated checks did not apply to a
// module, and knowledge retrieval uses it to drop an entry whose claim is
// about a version this repository does not target. A second parser with its
// own handling of block directives and comments would give those two answers
// that differ on the same file.
package gomod

import (
	"os"
	"path/filepath"
	"strings"

	"go/version"
)

// AssumedLanguage is what the go tool assumes for a module whose go.mod
// carries no `go` directive at all.
//
// The note behind it is in docs/runner-notes.md#assumedlanguage.
const AssumedLanguage = "1.16"

// BelowAnalyzed reports whether a module's declared Go language version is
// below the toolchain analyzing it, so that version-gated checks this run
// could have applied were not applied to it.
//
// The note behind it is in docs/runner-notes.md#belowanalyzed.
func BelowAnalyzed(declared, ceiling string) bool {
	v := "go" + declared
	if !version.IsValid(v) || !version.IsValid(ceiling) {
		return false
	}
	return version.Compare(version.Lang(v), version.Lang(ceiling)) < 0
}

// LanguageVersion reads the Go language version a go.mod declares, with
// the 1-based line of the `go` directive.
//
// The note behind it is in docs/runner-notes.md#languageversion.
func LanguageVersion(modFile string) (declared string, line int, ok bool) {
	src, err := os.ReadFile(modFile)
	if err != nil {
		return "", 0, false
	}

	// Parenthesis depth, so that a `go` line inside require/exclude/replace/
	// retract/godebug/tool is read as what it is, a block entry, and not as the
	// module's directive. Indentation cannot stand in for this: go.mod permits a
	// top-level directive to be indented, which is why the scan below uses Fields
	// in the first place.
	depth := 0

	for i, raw := range strings.Split(string(src), "\n") {
		// Line comments only, which is every comment go.mod has: the modules
		// reference says "Comments start with // and run to the end of a line.
		// /* */ comments are not allowed." A file carrying one does not load,
		// so there is nothing here to be right about.
		if comment := strings.Index(raw, "//"); comment >= 0 {
			raw = raw[:comment]
		}

		// Fields rather than a split on " ": it absorbs leading indentation,
		// which go.mod permits, and the trailing \r of a file written on Windows.
		if fields := strings.Fields(raw); depth == 0 && len(fields) >= 2 && fields[0] == "go" {
			return fields[1], i + 1, true
		}

		// After the check and not before it: `require (` opens the block on the
		// line that names it, and the directive itself never carries a paren, so
		// no top-level `go` is ever hidden by its own line.
		depth += strings.Count(raw, "(") - strings.Count(raw, ")")
		if depth < 0 {
			// Unbalanced. The file does not load either, and guessing which of
			// the two readings the author meant is how a misread becomes a
			// number the ceiling comparison trusts.
			return "", 0, false
		}
	}

	return AssumedLanguage, 0, true
}

// Versions reports what a repository's root module declares, keyed by the
// names a knowledge entry's `applies:` line uses.
//
// The root go.mod only, so an entry is judged against the version the
// repository as a whole claims. An unreadable or absent one returns nothing,
// which AppliesTo reads as "unknown".
func Versions(repoRoot string) map[string]string {
	if repoRoot == "" {
		// Not the process working directory. Joining "" with "go.mod" names a
		// relative path, so a caller with no checkout would judge entries
		// against whatever module the binary happens to be run from.
		return nil
	}
	declared, _, ok := LanguageVersion(filepath.Join(repoRoot, "go.mod"))
	if !ok {
		return nil
	}
	return map[string]string{"go": declared}
}

// VersionsFor reports what the module owning these paths declares.
//
// The module owning them, not the repository's root, because the Go language
// version is a property of the main module and a repository can hold several.
// A submodule on 1.21 under a root on 1.25 has the timer behaviour its own
// go.mod declares, and judging its files against the root's answer would drop
// an entry that applies: a confident wrong answer, which is worse than the
// unknown this package's callers are built to keep everything on.
//
// Paths belonging to different modules return nothing, for the same reason. So
// does a path that escapes repoRoot, which is not a path this repository
// serves.
func VersionsFor(repoRoot string, paths []string) map[string]string {
	if repoRoot == "" || len(paths) == 0 {
		return nil
	}

	var found string
	for i, p := range paths {
		mod, ok := moduleDir(repoRoot, p)
		if !ok {
			return nil
		}
		if i > 0 && mod != found {
			return nil
		}
		found = mod
	}
	return Versions(found)
}

// moduleDir walks up from a repository-relative path to the nearest directory
// holding a go.mod, stopping at repoRoot.
//
// The root itself counts, so a single-module repository answers the way it
// always did. A path with no go.mod anywhere above it is not in a module, and
// reporting one for it would be an invention.
func moduleDir(repoRoot, rel string) (string, bool) {
	root := filepath.Clean(repoRoot)
	dir := filepath.Dir(filepath.Join(root, filepath.FromSlash(rel)))

	// Refuse anything that climbed out of the repository rather than searching
	// upward from it: a "../" in a diff path must not read a go.mod the
	// repository does not contain.
	if dir != root && !strings.HasPrefix(dir, root+string(filepath.Separator)) {
		return "", false
	}

	for {
		if _, _, ok := LanguageVersion(filepath.Join(dir, "go.mod")); ok {
			return dir, true
		}
		if dir == root {
			return "", false
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}
