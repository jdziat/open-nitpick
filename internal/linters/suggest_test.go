package linters

import (
	"slices"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
)

func names(entries []CatalogEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name)
	}
	return out
}

func TestSuggestMatchesBuiltinRunnersAndCatalogTools(t *testing.T) {
	got := names(Suggest([]string{"cmd/app/main.go", "scripts/deploy.py", "Dockerfile", ".github/workflows/ci.yml"}))

	for _, want := range []string{"golangci-lint", "ruff", "hadolint", "actionlint"} {
		if !slices.Contains(got, want) {
			t.Errorf("%s has files to read here but was not suggested: %v", want, got)
		}
	}
	if !slices.IsSorted(got) {
		t.Errorf("suggestions are not in a stable order: %v", got)
	}
}

func TestSuggestReturnsNothingForACheckoutOfNothingItReads(t *testing.T) {
	if got := names(Suggest([]string{"LICENSE", "docs/logo.png"})); len(got) != 0 {
		t.Errorf("suggested %v for a checkout with no analyzable file", got)
	}
}

// semgrep's coverage is whatever rules an operator points it at, so no set of
// files in a checkout implies it. Suggesting it would put a line in every
// generated config for a tool that analyzes nothing until it is configured.
func TestSuggestNeverNamesSemgrep(t *testing.T) {
	got := names(Suggest([]string{"main.go", "app.py", "index.ts", "lib.rb", "main.rs"}))
	if slices.Contains(got, "semgrep") {
		t.Errorf("semgrep was suggested from files alone: %v", got)
	}
}

// The suggestion has to agree with what a review would actually run: an
// analyzer suggested here and skipped there would be a config that documents a
// tool the repository never sees.
func TestSuggestAgreesWithTheDetectionAReviewUses(t *testing.T) {
	files := []string{"main.go", "Dockerfile", "chart/values.yaml"}
	suggested := names(Suggest(files))

	for _, s := range catalog() {
		hasTargets := len(s.targets(files)) > 0
		if hasTargets != slices.Contains(suggested, s.name) {
			t.Errorf("%s: targets=%v but suggested=%v", s.name, hasTargets, slices.Contains(suggested, s.name))
		}
	}
}

func TestBuiltinsCoverTheRunnersThatShipEnabled(t *testing.T) {
	byName := map[string]CatalogEntry{}
	for _, e := range Builtins() {
		byName[e.Name] = e
	}

	for _, want := range config.DefaultLinters {
		e, ok := byName[want]
		if !ok {
			t.Fatalf("%s ships in config.DefaultLinters but Builtins does not describe it", want)
		}
		if !e.Default {
			t.Errorf("%s ships enabled but Builtins does not mark it Default", want)
		}
		if len(e.Exts) == 0 {
			t.Errorf("%s has no extensions, so no checkout can suggest it", want)
		}
	}

	// eslint and semgrep are described but must never be suggested outright:
	// each does nothing until an operator supplies a configuration from
	// outside the repository under review.
	for _, name := range []string{"eslint", "semgrep"} {
		if !byName[name].NeedsConfig {
			t.Errorf("%s does nothing unconfigured but is not marked NeedsConfig", name)
		}
	}
}

// The extensions in Builtins are a copy of what each runner's own Detect
// filters on, and a copy that drifts is a config that names an analyzer with
// nothing to read.
func TestBuiltinExtensionsMatchWhatTheRunnersFilterOn(t *testing.T) {
	byName := map[string]CatalogEntry{}
	for _, e := range Builtins() {
		byName[e.Name] = e
	}

	if got := byName["eslint"].Exts; !slices.Equal(got, eslintExts) {
		t.Errorf("eslint exts = %v, runner filters on %v", got, eslintExts)
	}
	for _, want := range []string{".go"} {
		if !slices.Contains(byName["golangci-lint"].Exts, want) {
			t.Errorf("golangci-lint does not claim %s", want)
		}
	}
	for _, want := range []string{".py", ".pyi"} {
		if !slices.Contains(byName["ruff"].Exts, want) {
			t.Errorf("ruff does not claim %s", want)
		}
	}
}

func TestCatalogCarriesTheInputsSuggestMatchesOn(t *testing.T) {
	var withInputs int
	for _, e := range Catalog() {
		if len(e.Exts)+len(e.Names)+len(e.Prefixes) > 0 {
			withInputs++
		}
	}
	if withInputs == 0 {
		t.Fatal("no catalog entry reports the files it reads, so Suggest can match nothing")
	}
}
