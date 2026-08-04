package linters

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/diff"
)

// changedDiff adds lines 1..3 of app.go, so only those lines are commentable.
const changedDiff = `diff --git a/app.go b/app.go
--- a/app.go
+++ b/app.go
@@ -0,0 +1,3 @@
+package app
+
+func F() {}
`

func parse(t *testing.T, d string) diff.Files {
	t.Helper()

	files, err := diff.Parse([]byte(d))
	if err != nil {
		t.Fatalf("parse diff: %v", err)
	}
	return files
}

// fakeRunner is a scripted analyzer.
type fakeRunner struct {
	name     string
	detected bool
	findings []Finding
	err      error
	ran      bool
}

func (f *fakeRunner) Name() string                        { return f.name }
func (f *fakeRunner) Detect(context.Context, string) bool { return f.detected }
func (f *fakeRunner) Run(context.Context, string, []string) ([]Finding, error) {
	f.ran = true
	return f.findings, f.err
}

func baseConfig() *config.Config {
	cfg := config.Defaults()
	cfg.Models.Default = config.ModelSpec{Provider: "openai", Model: "gpt-4o"}
	return cfg
}

func TestFindingsOnUnchangedLinesAreDropped(t *testing.T) {
	// Pre-existing lint debt on untouched lines is somebody else's problem;
	// reporting it is the fastest way to get the bot switched off.
	cfg := baseConfig()

	runner := &fakeRunner{name: "fake", detected: true, findings: []Finding{
		{Path: "app.go", Line: 2, Message: "on a changed line", Severity: config.SeverityWarning},
		{Path: "app.go", Line: 99, Message: "on an untouched line", Severity: config.SeverityError},
	}}

	set := New(".", cfg, nil)
	set.runners = []Runner{runner}

	got, err := set.Run(context.Background(), parse(t, changedDiff))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("findings = %+v, want only the one on a changed line", got)
	}
	if got[0].Line != 2 {
		t.Errorf("line = %d, want 2", got[0].Line)
	}
}

func TestOnlyChangedLinesIsConfigurable(t *testing.T) {
	cfg := baseConfig()
	cfg.Linters.OnlyChangedLines = false

	set := New(".", cfg, nil)
	set.runners = []Runner{&fakeRunner{name: "fake", detected: true, findings: []Finding{
		{Path: "app.go", Line: 1, Message: "changed", Severity: config.SeverityWarning},
	}}}

	got, err := set.Run(context.Background(), parse(t, changedDiff))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("findings = %d, want 1", len(got))
	}
}

func TestUndetectedRunnerIsSkippedInAutoMode(t *testing.T) {
	cfg := baseConfig()
	cfg.Linters.Mode = config.LinterAuto

	runner := &fakeRunner{name: "fake", detected: false}

	set := New(".", cfg, nil)
	set.runners = []Runner{runner}

	got, err := set.Run(context.Background(), parse(t, changedDiff))
	if err != nil {
		t.Fatalf("auto mode should not fail on a missing linter: %v", err)
	}
	if runner.ran {
		t.Error("an undetected runner should not be executed")
	}
	if len(got) != 0 {
		t.Errorf("findings = %+v, want none", got)
	}
}

func TestUndetectedRunnerFailsInStrictMode(t *testing.T) {
	cfg := baseConfig()
	cfg.Linters.Mode = config.LinterStrict

	set := New(".", cfg, nil)
	set.runners = []Runner{&fakeRunner{name: "fake", detected: false}}

	_, err := set.Run(context.Background(), parse(t, changedDiff))
	if err == nil {
		t.Fatal("strict mode should report a missing linter")
	}
	if !strings.Contains(err.Error(), "fake") {
		t.Errorf("error should name the linter, got: %v", err)
	}
}

func TestFailingRunnerDoesNotFailTheReviewInAutoMode(t *testing.T) {
	// A broken linter must not cost the user the whole review.
	cfg := baseConfig()

	set := New(".", cfg, nil)
	set.runners = []Runner{
		&fakeRunner{name: "broken", detected: true, err: errors.New("exploded")},
		&fakeRunner{name: "working", detected: true, findings: []Finding{
			{Path: "app.go", Line: 1, Message: "real finding", Severity: config.SeverityWarning},
		}},
	}

	got, err := set.Run(context.Background(), parse(t, changedDiff))
	if err != nil {
		t.Fatalf("a failing linter should not fail the run: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("findings = %+v, want the working linter's output", got)
	}
}

func TestLintersOffRunsNothing(t *testing.T) {
	cfg := baseConfig()
	cfg.Linters.Mode = config.LinterOff

	runner := &fakeRunner{name: "fake", detected: true}

	set := New(".", cfg, nil)
	set.runners = []Runner{runner}

	got, err := set.Run(context.Background(), parse(t, changedDiff))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if runner.ran || len(got) != 0 {
		t.Error("mode off should run nothing")
	}
}

func TestFindingsAreAttributedToTheirLinter(t *testing.T) {
	cfg := baseConfig()

	set := New(".", cfg, nil)
	set.runners = []Runner{&fakeRunner{name: "golangci-lint", detected: true, findings: []Finding{
		{Path: "app.go", Line: 1, Rule: "errcheck", Message: "unchecked error", Severity: config.SeverityError},
	}}}

	got, err := set.Run(context.Background(), parse(t, changedDiff))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("findings = %d, want 1", len(got))
	}
	// A reviewer needs to know a finding came from a tool, not a model.
	if !strings.Contains(got[0].Rationale, "golangci-lint") {
		t.Errorf("rationale should attribute the linter: %q", got[0].Rationale)
	}
	if got[0].Category != "lint" {
		t.Errorf("category = %q, want lint", got[0].Category)
	}
}

// TestALinterFindingNeverClaimsOurSeverityAsItsOwn pins the provenance of a
// severity nobody but this package chose.
//
// mapSeverity collapses four analyzers' vocabularies onto three of our levels —
// "HIGH", "ERROR" and "CRITICAL" all land on error — and ruff publishes no
// severity at all, so the level on a linter finding is never the analyzer's own
// spelling. Nothing recorded that, and internal/evals answered "was this
// translated?" from the reporter's identity, so a report captioned as each
// contender's severity vocabulary would quote gosec as having printed a word
// gosec never used.
func TestALinterFindingNeverClaimsOurSeverityAsItsOwn(t *testing.T) {
	cfg := baseConfig()

	set := New(".", cfg, nil)
	set.runners = []Runner{&fakeRunner{name: "gosec", detected: true, findings: []Finding{
		{Path: "app.go", Line: 1, Rule: "G404", Message: "weak rng",
			Severity: mapSeverity("HIGH"), RawSeverity: "HIGH"},
		// ruff's shape: no severity published, so this package chose one and
		// there is no word to quote.
		{Path: "app.go", Line: 2, Rule: "E501", Message: "line too long",
			Severity: config.SeverityWarning},
	}}}

	got, err := set.Run(context.Background(), parse(t, changedDiff))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("findings = %d, want 2", len(got))
	}

	for _, f := range got {
		if !f.SeverityTranslated {
			t.Errorf("%s:%d is published at %q and does not record that the level is ours. Every "+
				"path through this package either maps the analyzer's word or invents one",
				f.Path, f.Line, f.Severity)
		}
	}

	if got[0].RawSeverity != "HIGH" {
		t.Errorf("RawSeverity = %q, want %q: mapSeverity folds HIGH, ERROR and CRITICAL onto one "+
			"level, so the analyzer's word cannot be recovered from ours", got[0].RawSeverity, "HIGH")
	}
	if got[1].RawSeverity != "" {
		t.Errorf("RawSeverity = %q for an analyzer that published no severity; there is no word to "+
			"quote and inventing one is the substitution this field exists to stop", got[1].RawSeverity)
	}
}

func TestUnknownSeverityBecomesWarning(t *testing.T) {
	// An unrecognized analyzer severity must not be able to trip the gate.
	cfg := baseConfig()

	set := New(".", cfg, nil)
	set.runners = []Runner{&fakeRunner{name: "fake", detected: true, findings: []Finding{
		{Path: "app.go", Line: 1, Message: "x", Severity: config.Severity("bizarre")},
	}}}

	got, err := set.Run(context.Background(), parse(t, changedDiff))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got[0].Severity != string(config.SeverityWarning) {
		t.Errorf("severity = %q, want warning", got[0].Severity)
	}
}

func TestIgnoredAndDeletedFilesAreNotLinted(t *testing.T) {
	const d = `diff --git a/vendor/x.go b/vendor/x.go
--- a/vendor/x.go
+++ b/vendor/x.go
@@ -0,0 +1,1 @@
+package x
diff --git a/gone.go b/gone.go
deleted file mode 100644
--- a/gone.go
+++ /dev/null
@@ -1,1 +0,0 @@
-package gone
`
	paths := reviewablePaths(baseConfig(), parse(t, d))
	if len(paths) != 0 {
		t.Errorf("paths = %v, want none (vendored and deleted files)", paths)
	}
}

func TestMapSeverity(t *testing.T) {
	cases := map[string]config.Severity{
		"ERROR":   config.SeverityError,
		"high":    config.SeverityError,
		"WARNING": config.SeverityWarning,
		"info":    config.SeverityInfo,
		"":        config.SeverityWarning,
		"weird":   config.SeverityWarning,
	}
	for in, want := range cases {
		if got := mapSeverity(in); got != want {
			t.Errorf("mapSeverity(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPrefixRule(t *testing.T) {
	cases := []struct{ linter, rule, want string }{
		{"golangci-lint", "errcheck", "golangci-lint(errcheck)"},
		{"ruff", "", "ruff"},
		{"eslint", "eslint/no-unused", "eslint/no-unused"},
	}
	for _, tc := range cases {
		if got := prefixRule(tc.linter, tc.rule); got != tc.want {
			t.Errorf("prefixRule(%q, %q) = %q, want %q", tc.linter, tc.rule, got, tc.want)
		}
	}
}

func TestDecodeJSONIgnoresSurroundingNoise(t *testing.T) {
	// Analyzers wrap their JSON in human-readable output on both sides.
	// golangci-lint in particular appends a "1 issues:" summary after it, which
	// a prefix trim alone does not handle.
	cases := []struct {
		name string
		out  string
	}{
		{"clean", `{"Issues":[{"Text":"x"}]}`},
		{"leading notice", "level=warning config is deprecated\n" + `{"Issues":[{"Text":"x"}]}`},
		{"trailing summary", `{"Issues":[{"Text":"x"}]}` + "\n1 issues:\n* errcheck: 1\n"},
		{"both", "notice\n" + `{"Issues":[{"Text":"x"}]}` + "\n1 issues:\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var parsed golangciOutput
			if err := decodeJSON([]byte(tc.out), &parsed); err != nil {
				t.Fatalf("decodeJSON: %v", err)
			}
			if len(parsed.Issues) != 1 || parsed.Issues[0].Text != "x" {
				t.Errorf("parsed = %+v", parsed.Issues)
			}
		})
	}
}

func TestDecodeJSONWithNoPayload(t *testing.T) {
	// An analyzer that printed only prose found nothing; that is not an error.
	var parsed golangciOutput

	if err := decodeJSON([]byte("   "), &parsed); err != nil {
		t.Errorf("empty output should not error: %v", err)
	}
	if err := decodeJSON([]byte("no issues found"), &parsed); err != nil {
		t.Errorf("prose-only output should not error: %v", err)
	}
	if len(parsed.Issues) != 0 {
		t.Errorf("issues = %+v, want none", parsed.Issues)
	}
}

func TestDecodeJSONReportsMalformedPayload(t *testing.T) {
	var parsed golangciOutput

	err := decodeJSON([]byte(`{"Issues": [ broken`), &parsed)
	if err == nil {
		t.Fatal("want an error for malformed JSON")
	}
	// The error must show what was actually received, or diagnosing a new
	// analyzer version means guessing.
	if !strings.Contains(err.Error(), "Issues") {
		t.Errorf("error should quote the output, got: %v", err)
	}
}

func TestGoPackageDirsDeduplicates(t *testing.T) {
	got := goPackageDirs([]string{"a/b/x.go", "a/b/y.go", "c/z.go", "readme.md"})
	if len(got) != 2 {
		t.Fatalf("dirs = %v, want 2 unique package directories", got)
	}
}

func TestFilterExt(t *testing.T) {
	got := filterExt([]string{"a.py", "b.go", "c.PY"}, ".py")
	if len(got) != 2 {
		t.Errorf("filterExt = %v, want the two Python files (case-insensitive)", got)
	}
}
