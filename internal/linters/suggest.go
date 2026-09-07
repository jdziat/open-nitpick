package linters

import (
	"path/filepath"
	"sort"
	"strings"
)

// Builtins describes the four hand-written runners, which are not in catalog()
// because each has its own Runner implementation.
//
// The table lives here rather than in the command that prints it so that
// `nitpick linters` and `nitpick init` cannot describe the same analyzer
// differently. The extensions are the ones each runner's own Detect filters
// on; a change to one has to change the other.
func Builtins() []CatalogEntry {
	return []CatalogEntry{
		{
			Name: "golangci-lint", Languages: "Go", Default: true,
			Configuration: "open-nitpick's own config; linters.golangci_config overrides",
			Exts:          []string{".go"},
		},
		{
			Name: "ruff", Languages: "Python", Default: true,
			Configuration: "--isolated; linters.ruff_config overrides",
			Exts:          []string{".py", ".pyi"},
		},
		{
			Name: "eslint", Languages: "JavaScript, TypeScript (via an operator config)",
			Configuration: "linters.eslint_config required", NeedsConfig: true,
			Exts: append([]string(nil), eslintExts...),
		},
		{
			Name: "semgrep", Languages: "any (rules of your choosing)",
			Configuration: "linters.semgrep_config required", NeedsConfig: true,
			// No extensions: semgrep's coverage is whatever its rules say, so
			// no set of files in a checkout implies it. It is listed for the
			// roster and never suggested by Suggest.
		},
	}
}

// Suggest names the analyzers that have something to read in a checkout
// holding these repository-relative paths.
//
// It is what `nitpick init` writes a config from, and it answers the question
// a person is least likely to get right by hand: which of thirty-three
// analyzers this repository's languages call for. Matching is the same
// matching a review does, so an analyzer suggested here is one that would
// actually have targets.
//
// An entry with no inputs at all is never suggested. semgrep is the case: its
// coverage is its rules, so a checkout cannot imply it.
func Suggest(files []string) []CatalogEntry {
	var out []CatalogEntry

	for _, e := range Builtins() {
		if matchesAny(e, files) {
			out = append(out, e)
		}
	}

	described := map[string]CatalogEntry{}
	for _, e := range Catalog() {
		described[e.Name] = e
	}
	for _, s := range catalog() {
		if len(s.targets(files)) > 0 {
			out = append(out, described[s.name])
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// matchesAny reports whether any path is an input to this entry.
func matchesAny(e CatalogEntry, files []string) bool {
	if len(e.Exts) == 0 && len(e.Names) == 0 && len(e.Prefixes) == 0 {
		return false
	}
	for _, f := range files {
		base := filepath.Base(filepath.ToSlash(f))
		ext := strings.ToLower(filepath.Ext(base))
		for _, x := range e.Exts {
			if ext == x {
				return true
			}
		}
		for _, n := range e.Names {
			if base == n {
				return true
			}
		}
		for _, p := range e.Prefixes {
			if strings.HasPrefix(base, p) {
				return true
			}
		}
	}
	return false
}
