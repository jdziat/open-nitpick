package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeConfig writes a .nitpick.yaml into a temp repo root and returns the root.
func writeConfig(t *testing.T, body string) string {
	t.Helper()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, FileName), []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return root
}

func TestLoadMergesOntoDefaults(t *testing.T) {
	root := writeConfig(t, `
models:
  default:
    provider: anthropic
    model: claude-sonnet-4-20250514
review:
  max_files: 10
`)

	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Review.MaxFiles != 10 {
		t.Errorf("MaxFiles = %d, want 10 (explicit value)", cfg.Review.MaxFiles)
	}
	// Untouched fields must retain their defaults rather than zeroing out.
	if cfg.Review.Concurrency != Defaults().Review.Concurrency {
		t.Errorf("Concurrency = %d, want default %d", cfg.Review.Concurrency, Defaults().Review.Concurrency)
	}
	// Advisory by default: the reviewer does not block merges unless asked to.
	if cfg.Review.FailOn != SeverityNone {
		t.Errorf("FailOn = %q, want default none", cfg.Review.FailOn)
	}
	if len(cfg.Review.Ignore) == 0 {
		t.Error("default ignore list was dropped")
	}
	if cfg.Source == "" {
		t.Error("Source should record the config path")
	}
}

func TestLoadMissingFileUsesEnv(t *testing.T) {
	root := t.TempDir()

	t.Setenv(EnvProvider, "ollama")
	t.Setenv(EnvModel, "qwen2.5-coder")

	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("Load with env: %v", err)
	}
	if cfg.Models.Default.Provider != "ollama" || cfg.Models.Default.Model != "qwen2.5-coder" {
		t.Errorf("env fallback not applied: %+v", cfg.Models.Default)
	}
	if cfg.Source != "" {
		t.Errorf("Source = %q, want empty when no file was read", cfg.Source)
	}
}

func TestLoadMissingFileAndEnvFails(t *testing.T) {
	t.Setenv(EnvProvider, "")
	t.Setenv(EnvModel, "")

	if _, err := Load(t.TempDir()); err == nil {
		t.Fatal("want error when neither config file nor environment supplies a model")
	}
}

func TestConfigFileWinsOverEnv(t *testing.T) {
	t.Setenv(EnvProvider, "ollama")
	t.Setenv(EnvModel, "llama3")

	root := writeConfig(t, `
models:
  default:
    provider: anthropic
    model: claude-sonnet-4-20250514
`)

	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Models.Default.Provider != "anthropic" {
		t.Errorf("Provider = %q, want the config file to win over env", cfg.Models.Default.Provider)
	}
}

func TestUnknownFieldIsRejected(t *testing.T) {
	root := writeConfig(t, `
models:
  default: {provider: openai, model: gpt-4o}
review:
  max_fils: 10
`)

	_, err := Load(root)
	if err == nil {
		t.Fatal("want error for a misspelled key")
	}
	if !strings.Contains(err.Error(), "max_fils") {
		t.Errorf("error should name the offending key, got: %v", err)
	}
}

func TestInvalidSeverityIsRejectedAtLoad(t *testing.T) {
	root := writeConfig(t, `
models:
  default: {provider: openai, model: gpt-4o}
review:
  fail_on: blocker
`)

	_, err := Load(root)
	if err == nil {
		t.Fatal("want error for an unknown severity")
	}
	if !strings.Contains(err.Error(), "blocker") {
		t.Errorf("error should name the bad value, got: %v", err)
	}
}

func TestResolveModelInheritsFromDefault(t *testing.T) {
	temp := 0.1
	models := Models{
		Default: ModelSpec{
			Provider:         "anthropic",
			Model:            "claude-sonnet-4-20250514",
			MaxTokens:        8192,
			StructuredOutput: StructuredAuto,
		},
		Triage: &ModelSpec{
			Provider:    "openai",
			Model:       "gpt-4o-mini",
			Temperature: &temp,
		},
	}

	review := models.ResolveModel(RoleReview)
	if review.Provider != "anthropic" || review.MaxTokens != 8192 {
		t.Errorf("review role should fall back to default entirely, got %+v", review)
	}

	triage := models.ResolveModel(RoleTriage)
	if triage.Provider != "openai" || triage.Model != "gpt-4o-mini" {
		t.Errorf("triage override not applied: %+v", triage)
	}
	// Inherited because the override does not set it.
	if triage.MaxTokens != 8192 {
		t.Errorf("triage MaxTokens = %d, want inherited 8192", triage.MaxTokens)
	}
	if triage.Temperature == nil || *triage.Temperature != 0.1 {
		t.Errorf("triage Temperature not applied: %+v", triage.Temperature)
	}
	if triage.StructuredOutput != StructuredAuto {
		t.Errorf("triage StructuredOutput = %q, want inherited auto", triage.StructuredOutput)
	}
}

func TestResolveModelDoesNotMutateDefault(t *testing.T) {
	models := Models{
		Default: ModelSpec{Provider: "anthropic", Model: "a", Extra: map[string]string{"k": "v"}},
		Triage:  &ModelSpec{Extra: map[string]string{"k2": "v2"}},
	}

	_ = models.ResolveModel(RoleTriage)

	if len(models.Default.Extra) != 1 {
		t.Errorf("default Extra was mutated by overlay: %v", models.Default.Extra)
	}
}

func TestValidateReportsAllProblemsAtOnce(t *testing.T) {
	cfg := Defaults()
	cfg.Models.Default.Provider = ""
	cfg.Models.Default.Model = ""
	cfg.Review.Concurrency = 0

	err := cfg.Validate()
	if err == nil {
		t.Fatal("want validation error")
	}
	for _, want := range []string{"provider is required", "model is required", "concurrency"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got: %v", want, err)
		}
	}
}

func TestValidateRejectsMinSeverityNone(t *testing.T) {
	cfg := Defaults()
	cfg.Models.Default = ModelSpec{Provider: "openai", Model: "gpt-4o"}
	cfg.Review.MinSeverity = SeverityNone

	if err := cfg.Validate(); err == nil {
		t.Fatal("min_severity: none discards everything and should be rejected")
	}
}

func TestValidateAllowsFailOnNone(t *testing.T) {
	cfg := Defaults()
	cfg.Models.Default = ModelSpec{Provider: "openai", Model: "gpt-4o"}
	cfg.Review.FailOn = SeverityNone

	if err := cfg.Validate(); err != nil {
		t.Fatalf("fail_on: none is a valid never-fail policy: %v", err)
	}
}

func TestSeverityOrdering(t *testing.T) {
	if !SeverityError.AtLeast(SeverityWarning) {
		t.Error("error should outrank warning")
	}
	if SeverityNit.AtLeast(SeverityError) {
		t.Error("nit should not satisfy an error threshold")
	}
	// fail_on: none must never trigger.
	for _, s := range []Severity{SeverityNit, SeverityInfo, SeverityWarning, SeverityError, SeverityCritical} {
		if s.AtLeast(SeverityNone) {
			t.Errorf("%s should not satisfy the none threshold", s)
		}
	}
}

func TestSeverityUnknownDegradesToInfo(t *testing.T) {
	// A model returning an unexpected severity should produce a non-gating
	// finding rather than failing the build or vanishing.
	unknown := Severity("spicy")
	if unknown.Rank() != SeverityInfo.Rank() {
		t.Errorf("unknown severity rank = %d, want info rank %d", unknown.Rank(), SeverityInfo.Rank())
	}
	if unknown.AtLeast(SeverityError) {
		t.Error("unknown severity should not satisfy an error gate")
	}
}

func TestInstructionsForMatchingPaths(t *testing.T) {
	cfg := Defaults()
	cfg.Instructions = []Instruction{
		{Path: "**/*.go", Prompt: "go rules"},
		{Path: "internal/db/**", Prompt: "db rules"},
		{Path: "**/*.py", Prompt: "python rules"},
	}

	got := cfg.InstructionsFor("internal/db/query.go")
	want := []string{"go rules", "db rules"}
	if len(got) != len(want) {
		t.Fatalf("InstructionsFor = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("instruction[%d] = %q, want %q (config order)", i, got[i], want[i])
		}
	}
}

func TestIgnoreDefaults(t *testing.T) {
	cfg := Defaults()

	ignored := []string{
		"vendor/github.com/x/y.go",
		"web/node_modules/pkg/index.js",
		"api/service.pb.go",
		"go.sum",
		"internal/testdata/fixture.json",
	}
	for _, p := range ignored {
		if !cfg.Ignored(p) {
			t.Errorf("%q should be ignored by default", p)
		}
	}

	reviewed := []string{"internal/review/engine.go", "cmd/nitpick/main.go", "src/app.ts"}
	for _, p := range reviewed {
		if cfg.Ignored(p) {
			t.Errorf("%q should not be ignored", p)
		}
	}
}

func TestMatchGlobConveniences(t *testing.T) {
	cases := []struct {
		pattern string
		path    string
		want    bool
	}{
		// A separator-free pattern matches at any depth.
		{"*.go", "internal/config/config.go", true},
		{"*.go", "main.go", true},
		{"*.go", "main.rs", false},
		// A trailing slash means "everything under here".
		{"internal/", "internal/config/config.go", true},
		{"internal/", "cmd/main.go", false},
		// Doublestar spans separators; a single star does not.
		{"**/*.go", "a/b/c.go", true},
		{"a/*/c.go", "a/b/c.go", true},
		{"a/*/c.go", "a/b/d/c.go", false},
		// Leading ./ is normalized away.
		{"internal/**", "./internal/x.go", true},
		{"", "anything", false},
	}

	for _, tc := range cases {
		if got := matchGlob(tc.pattern, tc.path); got != tc.want {
			t.Errorf("matchGlob(%q, %q) = %v, want %v", tc.pattern, tc.path, got, tc.want)
		}
	}
}

func TestEmptyConfigFileKeepsDefaults(t *testing.T) {
	t.Setenv(EnvProvider, "openai")
	t.Setenv(EnvModel, "gpt-4o")

	root := writeConfig(t, "")

	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("empty config file should be valid: %v", err)
	}
	if cfg.Review.MaxFiles != Defaults().Review.MaxFiles {
		t.Error("empty document should leave defaults intact")
	}
}

func TestSkipMarkersDefaultToTheTwoPhrases(t *testing.T) {
	cfg := Defaults()
	if got := strings.Join(cfg.Review.SkipMarkers, ","); got != "[skip review],[skip nitpick]" {
		t.Errorf("skip_markers = %s", got)
	}
}
