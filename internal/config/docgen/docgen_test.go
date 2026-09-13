package docgen_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/config/docgen"
)

// TestRowsNameTheValuesTheyAccept guards the half of the reference that a
// field's own comment cannot supply.
//
// Ten keys printed a default and never the alternatives: `persona.confidence`
// gave `direct` and left `hedged` findable only by setting a bad value and
// reading the loader's rejection. The values live on the type's constants, so
// the fix reads them from there, and the two keys that take fewer than their
// type declares answer for themselves.
func TestRowsNameTheValuesTheyAccept(t *testing.T) {
	docs, values, err := docgen.ReadDocs("..")
	if err != nil {
		t.Fatalf("ReadDocs: %v", err)
	}
	rows := map[string]string{}
	for _, f := range docgen.Walk(config.Defaults(), docs, values) {
		rows[f.Path] = f.Doc
	}

	for path, want := range map[string]string{
		"persona.confidence":   "One of: direct, hedged.",
		"persona.nitpick":      "One of: off, minimal, normal, pedantic.",
		"persona.politeness":   "One of: blunt, neutral, warm.",
		"persona.verbosity":    "One of: terse, normal, detailed.",
		"persona.address":      "One of: impersonal, author.",
		"review.budget.scope":  "One of: run, pull_request.",
		"review.summary_style": "One of: receipt, prose.",
		"review.fail_on":       "One of: none, nit, info, warning, error, critical.",
		"review.min_severity":  "One of: nit, info, warning, error, critical.",
		"linters.max_severity": "One of: nit, info, warning, error, critical.",
	} {
		if got, ok := rows[path]; !ok {
			t.Errorf("%s: no row", path)
		} else if !strings.HasSuffix(got, want) {
			t.Errorf("%s ends %q, want it to end %q", path, got, want)
		}
	}

	// none is a gate threshold and not a level a finding carries. validate
	// rejects it on both of these, so a row offering it would document an
	// error.
	for _, path := range []string{"review.min_severity", "linters.max_severity"} {
		if strings.Contains(rows[path], "none,") {
			t.Errorf("%s offers none: %q", path, rows[path])
		}
	}

	// A comment that already spells its values out does not get them twice.
	for _, path := range []string{"linters.mode", "models.default.structured_output"} {
		if strings.Contains(rows[path], "One of:") {
			t.Errorf("%s repeats values its sentence already names: %q", path, rows[path])
		}
	}
}

func TestReadDocsIncludesBuildVariantsAndSkipsTestFiles(t *testing.T) {
	root := t.TempDir()
	for name, source := range map[string]string{
		"variant.go":     "//go:build never_enabled\n\npackage sample\ntype Config struct {\n// Value selects a value.\nValue string\n}\n",
		"broken_test.go": "invalid Go",
		"notes.txt":      "invalid Go",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	docs, _, err := docgen.ReadDocs(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(docs["Config.Value"], "Value selects a value.") {
		t.Fatalf("missing field documentation: %v", docs)
	}
	if err := os.WriteFile(filepath.Join(root, "broken.go"), []byte("invalid Go"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := docgen.ReadDocs(root); err == nil {
		t.Fatal("malformed source returned a successful scan")
	}
}
