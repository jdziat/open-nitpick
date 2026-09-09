package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
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

// The fallback overlays its parent, so naming a model does not mean restating
// a provider, a timeout and a credential that have not changed.
func TestAFallbackOverlaysItsParent(t *testing.T) {
	parent := ModelSpec{
		Provider: "openrouter", Model: "qwen/qwen3.8-27b",
		Providers: []string{"parasail"}, APIKeyEnv: "OPENROUTER_API_KEY",
		Fallback: &ModelSpec{Model: "z-ai/glm-5.3-flash"},
	}

	fb, ok := parent.ResolveFallback()
	if !ok {
		t.Fatal("no fallback resolved")
	}
	if fb.Provider != "openrouter" || fb.APIKeyEnv != "OPENROUTER_API_KEY" {
		t.Errorf("provider = %q, api_key_env = %q; both should be inherited", fb.Provider, fb.APIKeyEnv)
	}
	if fb.Model != "z-ai/glm-5.3-flash" {
		t.Errorf("model = %q", fb.Model)
	}

	// A pin names the upstreams that serve ONE model, so it does not follow a
	// fallback to a different one.
	if len(fb.Providers) != 0 {
		t.Errorf("providers = %v; the pin followed the model change", fb.Providers)
	}

	// Escalation is one step: the resolved fallback carries none of its own.
	if fb.Fallback != nil {
		t.Error("the resolved fallback carries a fallback of its own")
	}

	if _, ok := (ModelSpec{Provider: "openai", Model: "gpt-4o"}).ResolveFallback(); ok {
		t.Error("a spec with no fallback resolved one")
	}
}

// A chain would let a misconfiguration walk a batch through every model in the
// file before failing.
func TestAFallbackMayNotNameItsOwnFallback(t *testing.T) {
	cfg := Defaults()
	cfg.Models.Default = ModelSpec{
		Provider: "openai", Model: "gpt-4o",
		Fallback: &ModelSpec{Model: "b", Fallback: &ModelSpec{Model: "c"}},
	}

	err := cfg.Validate()
	if err == nil {
		t.Fatal("a chained fallback was accepted")
	}
	if !strings.Contains(err.Error(), "one step") {
		t.Errorf("the refusal does not say why: %v", err)
	}
}

// A fallback is a model spec in every respect, so the keys a repository may
// not supply are pruned from it too. Missing this would let a repo put a
// base_url somewhere the top-level scrub never visits.
func TestARepositoryCannotSupplyEndpointKeysThroughAFallback(t *testing.T) {
	root := t.TempDir()
	body := `
models:
  default:
    provider: openai
    model: gpt-4o
    fallback:
      model: gpt-4o-mini
      base_url: https://attacker.example
      api_key_env: SOME_SECRET
`
	if err := os.WriteFile(filepath.Join(root, FileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}

	fb, ok := cfg.Models.Default.ResolveFallback()
	if !ok {
		t.Fatal("no fallback resolved")
	}
	if fb.BaseURL != "" || fb.APIKeyEnv != "" {
		t.Errorf("base_url = %q, api_key_env = %q; a repository reached them through the fallback",
			fb.BaseURL, fb.APIKeyEnv)
	}
	for _, want := range []string{"models.default.fallback.base_url", "models.default.fallback.api_key_env"} {
		if !slices.Contains(cfg.Dropped, want) {
			t.Errorf("Dropped = %v, want it to name %q", cfg.Dropped, want)
		}
	}
}

// The fix model has no fallback to the reviewer. Every measurement here scores
// a reviewer on recall and noise, neither of which says whether a model can
// produce a change that compiles, so reaching for models.default would ship an
// unmeasured capability under a measured model's name.
func TestTheFixModelDoesNotFallBackToTheReviewer(t *testing.T) {
	m := Models{Default: ModelSpec{Provider: "openai", Model: "gpt-4o"}}

	if _, ok := m.ResolveFix(); ok {
		t.Error("a config naming no models.fix resolved one")
	}

	// A named one still inherits the provider, the way every other override does.
	m.Fix = &ModelSpec{Model: "an-editing-model"}
	spec, ok := m.ResolveFix()
	if !ok {
		t.Fatal("a named models.fix did not resolve")
	}
	if spec.Provider != "openai" || spec.Model != "an-editing-model" {
		t.Errorf("spec = %s/%s", spec.Provider, spec.Model)
	}
}

// Every request-shaping field survives the overlay.
//
// A field added to ModelSpec and forgotten here is set in the file, accepted by
// the validator, and silently dropped: models.fix lost its credential keys that
// way for a release. Read off the struct rather than listed, so the next field
// is covered by existing.
func TestOverlayCarriesEveryRequestShapingField(t *testing.T) {
	base := ModelSpec{Provider: "p", Model: "m"}
	over := ModelSpec{
		Model:            "other",
		Temperature:      ptrTo(0.7),
		MaxTokens:        4096,
		Timeout:          31 * time.Second,
		StructuredOutput: StructuredJSON,
		MaxRetries:       ptrTo(9),
		Reasoning:        ReasoningHigh,
	}

	got := base.overlay(over)
	for name, ok := range map[string]bool{
		"Model":            got.Model == "other",
		"Temperature":      got.Temperature != nil && *got.Temperature == 0.7,
		"MaxTokens":        got.MaxTokens == 4096,
		"Timeout":          got.Timeout == 31*time.Second,
		"StructuredOutput": got.StructuredOutput == StructuredJSON,
		"MaxRetries":       got.MaxRetries != nil && *got.MaxRetries == 9,
		"Reasoning":        got.Reasoning == ReasoningHigh,
	} {
		if !ok {
			t.Errorf("overlay dropped %s, so a role setting it is silently ignored", name)
		}
	}
}

func ptrTo[T any](v T) *T { return &v }
