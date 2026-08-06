package linters

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
	name      string
	detected  bool
	detectErr error
	findings  []Finding
	err       error
	ran       bool
}

func (f *fakeRunner) Name() string { return f.name }

func (f *fakeRunner) Detect(context.Context, string, []string) error {
	if f.detected {
		return nil
	}
	if f.detectErr != nil {
		return f.detectErr
	}
	return errors.New("scripted as undetected")
}

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
// mapSeverity folds foreign vocabularies onto our levels — "HIGH" and "ERROR"
// both land on error — and ruff publishes no severity at all, so a linter
// finding's level is our reading rather than the analyzer's own claim. It holds
// even where the spelling coincides: semgrep documents ERROR as the older
// spelling of HIGH, so it is a word inside SEMGREP'S scale, and
// linters.max_severity can reduce the result afterwards. Nothing recorded that,
// and internal/evals answered "was this translated?" from the reporter's
// identity, so a report captioned as each contender's severity vocabulary would
// quote semgrep as having printed a word semgrep never used.
func TestALinterFindingNeverClaimsOurSeverityAsItsOwn(t *testing.T) {
	cfg := baseConfig()

	set := New(".", cfg, nil)
	set.runners = []Runner{&fakeRunner{name: "semgrep", detected: true, findings: []Finding{
		{Path: "app.go", Line: 1, Rule: "go.lang.security.audit.weak-rand",
			Message: "weak rng", Severity: mapSeverity("HIGH"), RawSeverity: "HIGH"},
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
		t.Errorf("RawSeverity = %q, want %q: mapSeverity folds HIGH and ERROR onto one level, so "+
			"the analyzer's word cannot be recovered from ours", got[0].RawSeverity, "HIGH")
	}
	if got[1].RawSeverity != "" {
		t.Errorf("RawSeverity = %q for an analyzer that published no severity; there is no word to "+
			"quote and inventing one is the substitution this field exists to stop", got[1].RawSeverity)
	}
}

// TestAnUnusableSeverityFromARunnerBecomesWarning covers normalize's last-ditch
// guard: a RUNNER handing over a value that is not a level at all, which is our
// own bug rather than a vocabulary we failed to read.
//
// It lands on the same level mapSeverity gives an unreadable analyzer word and
// ruff's runner gives no word at all, because all three are the same state —
// this project has no usable severity and picks one.
func TestAnUnusableSeverityFromARunnerBecomesWarning(t *testing.T) {
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

// TestMapSeverity pins the whole table, including the entry that was wrong.
//
// Every level in config's vocabulary appears in the codomain here. That is the
// property the old table failed: critical was unreachable, so `fail_on: critical`
// gated on nothing an analyzer could ever produce.
func TestMapSeverity(t *testing.T) {
	cases := map[string]config.Severity{
		// Analyzer vocabularies, folded onto ours. Semgrep documents
		// ERROR/WARNING/INFO as the older spellings of HIGH/MEDIUM/LOW, so the
		// pairs below are synonyms inside one scale rather than a foreign word
		// and one of ours — and the fold is why RawSeverity exists.
		"HIGH":     config.SeverityError,
		"high":     config.SeverityError,
		"ERROR":    config.SeverityError,
		"MEDIUM":   config.SeverityWarning,
		"WARNING":  config.SeverityWarning,
		"WARN":     config.SeverityWarning,
		"LOW":      config.SeverityInfo,
		"info":     config.SeverityInfo,
		"CRITICAL": config.SeverityCritical,
		"critical": config.SeverityCritical,

		// Spellings only some of these tools use.
		"NOTE":        config.SeverityInfo,
		"INFORMATION": config.SeverityInfo,

		// Reachable only from the literal word, which golangci-lint's operator
		// text can supply. Nothing foreign folds up or down onto it.
		"nit": config.SeverityNit,

		// The three states in which this project has no usable severity from
		// the analyzer and picks one. "none" belongs here: it is a gate
		// threshold, not a level a finding can carry, so as an analyzer's word
		// it is unreadable like any other.
		"":        config.SeverityWarning,
		"weird":   config.SeverityWarning,
		"blocker": config.SeverityWarning,
		"none":    config.SeverityWarning,
	}
	for in, want := range cases {
		if got := mapSeverity(in); got != want {
			t.Errorf("mapSeverity(%q) = %q, want %q", in, got, want)
		}
	}

	seen := map[config.Severity]bool{}
	for in := range cases {
		seen[mapSeverity(in)] = true
	}
	for _, level := range []config.Severity{
		config.SeverityNit, config.SeverityInfo, config.SeverityWarning,
		config.SeverityError, config.SeverityCritical,
	} {
		if !seen[level] {
			t.Errorf("no analyzer word maps to %q. A level outside this codomain is a level "+
				"review.fail_on accepts and no analyzer finding can ever reach", level)
		}
	}
}

// TestAnUnreadableAnalyzerWordIsRankedWhereSilenceIs pins the floor against the
// only other case that means the same thing, so moving one without the other
// fails.
//
// THE BUG IT REPLACES: this floor was briefly aligned with
// config.Severity.Normalize's info instead — the level an unrecognized word from
// a MODEL gets. The symmetry is false. A model is handed our enum and writing
// outside it is that reporter misbehaving; an analyzer was never given our
// vocabulary, so an unreadable word is our translation failing, and quietening
// our own failure deletes real findings under any review.min_severity above
// info. It also ranked "the tool said something we could not read" below "the
// tool said nothing", which is incoherent — those are the same state, and this
// test is what says so.
func TestAnUnreadableAnalyzerWordIsRankedWhereSilenceIs(t *testing.T) {
	silence := mapSeverity("")

	for _, word := range []string{"P1", "blocker", "major", "trivial", "S3", "EXPERIMENT"} {
		if config.Severity(word).Valid() {
			t.Fatalf("%q is a level this project recognizes; it does not test the unreadable floor", word)
		}

		if got := mapSeverity(word); got != silence {
			t.Errorf("an analyzer saying %q lands on %q while one that published no severity at "+
				"all lands on %q. Both mean we have no usable severity, so both are this "+
				"project's choice and it is the same choice", word, got, silence)
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

// TestDecodeJSONRequiresAPayload pins the assertion that an analyzer actually
// reported.
//
// THE BUG IT REPLACES: this test used to assert the opposite — that output with
// no JSON in it is "the analyzer found nothing and said so in prose", and
// therefore not an error. None of the four analyzers behaves that way:
// golangci-lint prints {"Issues":[]}, ruff and eslint print [], semgrep prints
// its envelope. So the only things reaching that branch were failures, and
// combined with runCommand — which errors only when stdout is empty AND the exit
// was non-zero — an analyzer that exited 0 printing nothing was indistinguishable
// from clean code. `semgrep --config auto --metrics off` had been shipping in
// exactly that state.
func TestDecodeJSONRequiresAPayload(t *testing.T) {
	var parsed golangciOutput

	for _, out := range []string{"", "   ", "no issues found"} {
		err := decodeJSON([]byte(out), &parsed)
		if err == nil {
			t.Errorf("decodeJSON(%q) returned no error. An analyzer that printed no report has "+
				"not reported zero findings; it has not reported", out)
			continue
		}
		if !strings.Contains(err.Error(), "no JSON report") {
			t.Errorf("error should say the report is missing, got: %v", err)
		}
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

func TestGoTargetsDeduplicatesWithinAModule(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module probe\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := goTargets(repo, []string{"a/b/x.go", "a/b/y.go", "c/z.go", "readme.md"})
	if len(got) != 1 {
		t.Fatalf("targets = %+v, want one invocation for the single module", got)
	}
	if got[0].Module != "" {
		t.Errorf("module = %q, want the repository root", got[0].Module)
	}
	if len(got[0].Dirs) != 2 {
		t.Errorf("dirs = %v, want 2 unique package directories", got[0].Dirs)
	}
}

func TestFilterExt(t *testing.T) {
	got := filterExt([]string{"a.py", "b.go", "c.PY"}, ".py")
	if len(got) != 2 {
		t.Errorf("filterExt = %v, want the two Python files (case-insensitive)", got)
	}
}
