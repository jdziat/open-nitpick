package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
)

// initRepo builds a checkout holding one file of each named kind.
func initRepo(t *testing.T, names ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, n := range names {
		full := filepath.Join(root, filepath.FromSlash(n))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func runInitIn(t *testing.T, args ...string) (string, string) {
	t.Helper()
	var out bytes.Buffer
	if err := runInit(args, &out); err != nil {
		t.Fatalf("nitpick init %v: %v\n%s", args, err, out.String())
	}
	return out.String(), ""
}

// The round trip issue #14 asks for: what init writes must load, validate, and
// resolve to what the same repository resolves to with no file at all and the
// model supplied by the environment.
//
// Equality is the assertion rather than "it loads", because a generator that
// spells out defaults is only safe while the values it spells are the defaults.
// A default changed in config.Defaults and not here would otherwise ship a file
// that quietly pins the old value on every repository initialized after it.
func TestInitWritesAConfigThatResolvesToTheShippedDefaults(t *testing.T) {
	// Go and Python both present, so linters.enabled is the full default pair
	// and the comparison is not confounded by the narrowing in the test below.
	root := initRepo(t, "main.go", "go.mod", "app.py")

	runInitIn(t, "-repo", root, "-provider", "synthetic", "-model", "hf:moonshotai/Kimi-K3")

	written, err := config.LoadFile(filepath.Join(root, config.FileName))
	if err != nil {
		t.Fatalf("the generated config did not load: %v", err)
	}

	t.Setenv(config.EnvProvider, "synthetic")
	t.Setenv(config.EnvModel, "hf:moonshotai/Kimi-K3")
	t.Setenv(config.EnvBaseURL, "")
	defaults, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatalf("defaults: %v", err)
	}

	// Where the config came from is the one thing that must differ.
	for _, c := range []*config.Config{written, defaults} {
		c.Source, c.Missing, c.Policy, c.Dropped = "", "", config.Policy{}, nil
	}

	if !reflect.DeepEqual(written, defaults) {
		t.Errorf("the written config does not resolve to the defaults\nwritten:  %+v\ndefaults: %+v", written, defaults)
	}
}

func TestInitNamesOnlyTheDefaultAnalyzersTheCheckoutHasFilesFor(t *testing.T) {
	root := initRepo(t, "main.go", "go.mod")
	runInitIn(t, "-repo", root, "-provider", "openai", "-model", "gpt-4o")

	cfg, err := config.LoadFile(filepath.Join(root, config.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Linters.Enabled; !reflect.DeepEqual(got, []string{"golangci-lint"}) {
		t.Errorf("a Go-only checkout enabled %v; ruff has nothing to read", got)
	}
}

func TestInitLeavesEnabledEmptyWhenNoDefaultAnalyzerApplies(t *testing.T) {
	root := initRepo(t, "index.ts", "README.md")
	runInitIn(t, "-repo", root, "-provider", "openai", "-model", "gpt-4o")

	cfg, err := config.LoadFile(filepath.Join(root, config.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Linters.Enabled) != 0 {
		t.Errorf("enabled = %v; neither Go nor Python is present", cfg.Linters.Enabled)
	}
}

// An analyzer that runs itself belongs in a comment, not in enabled. Putting
// it in enabled would make its absence an error under mode: strict, which is a
// promise about the operator's runner that init has no standing to make.
func TestInitCommentsTheAutoDetectedAnalyzersRatherThanEnablingThem(t *testing.T) {
	root := initRepo(t, "main.go", "go.mod", "Dockerfile", ".github/workflows/ci.yml")
	runInitIn(t, "-repo", root, "-provider", "openai", "-model", "gpt-4o")

	body := read(t, filepath.Join(root, config.FileName))
	for _, name := range []string{"hadolint", "actionlint"} {
		if !strings.Contains(body, "#   "+name) {
			t.Errorf("%s was matched by this checkout but is not named in the file:\n%s", name, body)
		}
	}

	cfg, err := config.LoadFile(filepath.Join(root, config.FileName))
	if err != nil {
		t.Fatal(err)
	}
	for _, got := range cfg.Linters.Enabled {
		if got == "hadolint" || got == "actionlint" {
			t.Errorf("%s was enabled; auto-detected analyzers must stay comments", got)
		}
	}
}

// An analyzer that needs an operator's configuration, or an operator's word
// that its code may run, is listed with what it needs instead of silently
// omitted.
func TestInitSaysWhatAnAnalyzerNeedsBeforeItCanRun(t *testing.T) {
	root := initRepo(t, "index.ts", "src/lib.rs", "Cargo.toml")
	runInitIn(t, "-repo", root, "-provider", "openai", "-model", "gpt-4o")

	body := read(t, filepath.Join(root, config.FileName))
	if !strings.Contains(body, "eslint") || !strings.Contains(body, "linters.eslint_config required") {
		t.Errorf("eslint matched but the file does not say it needs a config:\n%s", body)
	}
	if !strings.Contains(body, "clippy") || !strings.Contains(body, "linters.trusted") {
		t.Errorf("clippy matched but the file does not say it executes tree code:\n%s", body)
	}
}

func TestInitRefusesToOverwriteWithoutForce(t *testing.T) {
	root := initRepo(t, "main.go", "go.mod")
	path := filepath.Join(root, config.FileName)
	if err := os.WriteFile(path, []byte("# hand written\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	err := runInit([]string{"-repo", root, "-provider", "openai", "-model", "gpt-4o"}, &out)
	if err == nil {
		t.Fatal("init overwrote an existing config")
	}
	if !strings.Contains(err.Error(), "-force") {
		t.Errorf("the refusal does not name the flag that would allow it: %v", err)
	}
	if got := read(t, path); got != "# hand written\n" {
		t.Errorf("the existing file was modified: %q", got)
	}

	runInitIn(t, "-repo", root, "-provider", "openai", "-model", "gpt-4o", "-force")
	if got := read(t, path); strings.Contains(got, "hand written") {
		t.Error("-force did not overwrite")
	}
}

func TestInitTakesTheModelFromTheEnvironmentAndSaysSo(t *testing.T) {
	t.Setenv(config.EnvProvider, "ollama")
	t.Setenv(config.EnvModel, "qwen3:32b")
	root := initRepo(t, "main.go", "go.mod")

	out, _ := runInitIn(t, "-repo", root)

	if !strings.Contains(out, "from the environment") {
		t.Errorf("init did not say where the model came from: %s", out)
	}
	cfg, err := config.LoadFile(filepath.Join(root, config.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Models.Default.Provider != "ollama" || cfg.Models.Default.Model != "qwen3:32b" {
		t.Errorf("model = %s/%s", cfg.Models.Default.Provider, cfg.Models.Default.Model)
	}

	// A model name carrying a colon is a mapping key unquoted, so the file has
	// to quote it or the loader reads something else entirely.
	if !strings.Contains(read(t, filepath.Join(root, config.FileName)), `"qwen3:32b"`) {
		t.Error("a model name containing a colon was written unquoted")
	}
}

// With nothing to name a model, the file still has to be a valid config: every
// other key it wrote is verified, and the models block is left commented rather
// than written empty. An empty one would decode a null over the built-in model
// defaults and take the timeout and temperature with it.
func TestInitWithNoModelWritesALoadableFileAndSaysWhatIsMissing(t *testing.T) {
	t.Setenv(config.EnvProvider, "")
	t.Setenv(config.EnvModel, "")
	root := initRepo(t, "main.go", "go.mod")

	out, _ := runInitIn(t, "-repo", root)
	if !strings.Contains(out, config.EnvProvider) {
		t.Errorf("init did not say how to supply a model: %s", out)
	}

	body := read(t, filepath.Join(root, config.FileName))
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "models:") {
			t.Fatalf("the models block was written live with no model to put in it:\n%s", body)
		}
	}

	t.Setenv(config.EnvProvider, "openai")
	t.Setenv(config.EnvModel, "gpt-4o")
	cfg, err := config.LoadFile(filepath.Join(root, config.FileName))
	if err != nil {
		t.Fatalf("the file is not loadable once the environment supplies a model: %v", err)
	}
	if cfg.Models.Default.Timeout != config.Defaults().Models.Default.Timeout {
		t.Errorf("timeout = %s; the commented block must not overwrite the built-in defaults", cfg.Models.Default.Timeout)
	}
}

func TestInitWritesAWorkflowPinnedToAMajorTag(t *testing.T) {
	root := initRepo(t, "main.go", "go.mod")
	runInitIn(t, "-repo", root, "-provider", "synthetic", "-model", "hf:moonshotai/Kimi-K3", "-workflow")

	wf := read(t, filepath.Join(root, ".github", "workflows", "nitpick.yml"))
	if !strings.Contains(wf, "jdziat/open-nitpick@"+majorTag(version)) {
		t.Errorf("workflow is not pinned to the major tag:\n%s", wf)
	}
	for _, want := range []string{"fetch-depth: 0", "provider: synthetic", "fail-on: none", "secrets."} {
		if !strings.Contains(wf, want) {
			t.Errorf("workflow is missing %q:\n%s", want, wf)
		}
	}

	var out bytes.Buffer
	if err := runInit([]string{"-repo", root, "-provider", "synthetic", "-model", "m", "-workflow"}, &out); err == nil {
		t.Error("the workflow was overwritten without -force")
	}
}

func TestMajorTagFollowsTheMajorVersionAndFallsBackToV1(t *testing.T) {
	for in, want := range map[string]string{
		"v1.4.2": "v1", "2.0.0": "v2", "v10.1.0": "v10",
		"dev": "v1", "": "v1", "v0.9.1": "v1",
	} {
		if got := majorTag(in); got != want {
			t.Errorf("majorTag(%q) = %q, want %q", in, got, want)
		}
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// The generated file is renamed into place, so nothing is left beside it and a
// second run with the same inputs writes the same file.
//
// This does not exercise a validation failure. The generator has no input that
// produces a config the loader rejects, so there is nothing to inject from out
// here; what a failure would leave behind is covered by the rename itself.
func TestInitLeavesNoTemporaryFileAndIsIdempotent(t *testing.T) {
	root := initRepo(t, "main.go", "go.mod")
	runInitIn(t, "-repo", root, "-provider", "openai", "-model", "gpt-4o")

	before := read(t, filepath.Join(root, config.FileName))
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".nitpick.yaml.") {
			t.Errorf("a temporary file was left behind: %s", e.Name())
		}
	}

	// A second run under -force rewrites it, and the content is the same
	// because the inputs are: the rename replaced rather than appended.
	runInitIn(t, "-repo", root, "-provider", "openai", "-model", "gpt-4o", "-force")
	if after := read(t, filepath.Join(root, config.FileName)); after != before {
		t.Error("rewriting the file with the same inputs produced different content")
	}
}
