package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/linters"
)

// nitpick init writes a working configuration for a checkout.
//
// What it writes is the shipped defaults, spelled out and commented, plus the
// one thing a default cannot supply: a model. Spelling the defaults out is the
// point rather than an accident of generation. A reader learns which keys
// exist and what each one costs from the file in front of them, and because
// every value written is already the one in force, deleting a key changes
// nothing and the file can be trimmed to taste without a surprise.
//
// The generated file is verified before it lands: it is written to a temporary
// file, loaded through the same config.LoadFile a review uses, and only then
// renamed into place. A generator that emitted a file the loader rejects would
// hand a new user a broken repository on their first command.

// runInit writes .nitpick.yaml, and optionally a workflow, for a repository.
func runInit(args []string, out io.Writer) error {
	var (
		repo     string
		provider string
		model    string
		force    bool
		workflow bool
	)

	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.StringVar(&repo, "repo", ".", "repository root")
	fs.StringVar(&provider, "provider", "", "model provider (default: $"+config.EnvProvider+")")
	fs.StringVar(&model, "model", "", "model name (default: $"+config.EnvModel+")")
	fs.BoolVar(&force, "force", false, "overwrite an existing file")
	fs.BoolVar(&workflow, "workflow", false, "also write .github/workflows/nitpick.yml")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `Usage: nitpick init [flags]

Writes .nitpick.yaml for the repository: the shipped defaults, commented, with
the analyzers this checkout's languages call for and a model taken from the
flags or the environment. Refuses to overwrite an existing file without -force.

Flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	root, err := filepath.Abs(repo)
	if err != nil {
		return err
	}
	if info, err := os.Stat(root); err != nil {
		return fmt.Errorf("repository %s: %w", repo, err)
	} else if !info.IsDir() {
		return fmt.Errorf("repository %s is not a directory", repo)
	}

	path := filepath.Join(root, config.FileName)
	if err := refuseExisting(path, force); err != nil {
		return err
	}

	files, err := scanRepo(root)
	if err != nil {
		return fmt.Errorf("read %s: %w", root, err)
	}

	// The file being written is not evidence about the repository. Counting it
	// makes init report a different analyzer roster on a second run than on a
	// first, because the config it just wrote is itself a YAML file that
	// yamllint reads.
	matched := linters.Suggest(without(files, config.FileName))

	chosen := resolveModel(provider, model, os.Getenv)
	body := renderConfig(chosen, matched)

	if err := writeVerified(path, body, chosen); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "wrote %s\n", path)
	report(out, chosen, matched)

	if workflow {
		wf := filepath.Join(root, ".github", "workflows", "nitpick.yml")
		if err := refuseExisting(wf, force); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(wf), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(wf, []byte(renderWorkflow(chosen)), 0o644); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(out, "wrote %s (pinned to %s)\n", wf, majorTag(version))
	}

	return nil
}

// refuseExisting is the clobber guard. Overwriting a configuration someone
// tuned, because they ran a command whose name suggests it only ever creates,
// is the one failure this command must not have.
func refuseExisting(path string, force bool) error {
	if force {
		return nil
	}
	switch _, err := os.Stat(path); {
	case err == nil:
		return fmt.Errorf("%s already exists; pass -force to overwrite it", path)
	case errors.Is(err, os.ErrNotExist):
		return nil
	default:
		return err
	}
}

// chosenModel is the model the generated file names, and where it came from.
type chosenModel struct {
	Provider string
	Model    string

	// FromEnv records that the environment supplied these rather than a flag,
	// which the report says out loud: a value that arrived from a variable the
	// operator forgot they exported is the one they will not think to check.
	FromEnv bool
}

// Known reports whether a model was resolved at all.
func (c chosenModel) Known() bool { return c.Provider != "" && c.Model != "" }

// resolveModel takes the flags first and the environment second, matching the
// precedence config.applyEnv already applies to a loaded file.
func resolveModel(provider, model string, getenv func(string) string) chosenModel {
	c := chosenModel{Provider: strings.TrimSpace(provider), Model: strings.TrimSpace(model)}
	if c.Provider == "" {
		c.Provider = strings.TrimSpace(getenv(config.EnvProvider))
		c.FromEnv = c.Provider != ""
	}
	if c.Model == "" {
		c.Model = strings.TrimSpace(getenv(config.EnvModel))
		c.FromEnv = c.FromEnv || c.Model != ""
	}
	return c
}

// scanDirSkip names directories that never hold reviewable source.
//
// Skipping them is not only speed. An analyzer suggested because
// node_modules holds a TypeScript file would be suggested for code this
// repository does not write, and the reviewer already refuses to review those
// paths: config.DefaultIgnore covers the same ground.
var scanDirSkip = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, "testdata": true,
	"dist": true, "build": true, "target": true, "__pycache__": true,
	".venv": true, "venv": true, ".tox": true, ".mypy_cache": true,
	".gradle": true, ".terraform": true,
}

// scanLimit bounds the walk. A checkout large enough to reach it has long
// since shown every language it contains.
const scanLimit = 50_000

// scanRepo lists repository-relative paths, for deciding which analyzers have
// something to read.
//
// It walks the checkout rather than asking git, because init runs before there
// is anything to ask: a fresh clone, or a directory that is not a repository
// at all.
func scanRepo(root string) ([]string, error) {
	var out []string

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// An unreadable directory is not a reason to write no config. The
			// walk continues and the suggestion is made from what was legible.
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if path == root {
				return nil
			}
			// Hidden directories are skipped except .github, which is where
			// the workflow analyzers find their only targets.
			if scanDirSkip[name] || (strings.HasPrefix(name, ".") && name != ".github") {
				return fs.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		out = append(out, filepath.ToSlash(rel))
		if len(out) >= scanLimit {
			return fs.SkipAll
		}
		return nil
	})

	return out, err
}

// renderConfig builds the file.
func renderConfig(c chosenModel, matched []linters.CatalogEntry) string {
	var b strings.Builder
	p := func(format string, a ...any) { _, _ = fmt.Fprintf(&b, format, a...) }

	p(`# open-nitpick configuration. Written by "nitpick init".
#
# Every value here is already the shipped default, except the model, so
# deleting a key changes nothing and this file can be trimmed to taste. Run
# "nitpick explain-config" to print what it resolves to without spending a
# token.

`)

	// The whole block is commented out when no model is known, rather than
	// left with an empty value under it. "default:" with nothing beneath it is
	// a null, and a null here would decode OVER the built-in model defaults,
	// silently taking the timeout and temperature with it.
	if c.Known() {
		p("models:\n  default:\n    provider: %s\n    model: %s\n",
			yamlScalar(c.Provider), yamlScalar(c.Model))
		p(`    # The credential is read from the provider's own conventional variable.
    # Name a different one with api_key_env. Both it and base_url are ignored
    # when this file is read out of a pull request that could have edited them.

`)
	} else {
		p(`# No model was named and %s / %s are not set, so this block is
# commented out and a review has nothing to call. Uncomment it, or export those
# two variables; "nitpick providers" lists the provider names.
# models:
#   default:
#     provider: synthetic
#     model: hf:moonshotai/Kimi-K3

`, config.EnvProvider, config.EnvModel)
	}

	p(`review:
  # The lowest severity that makes the run exit non-zero. "none" reviews and
  # comments without ever failing a build, which is where a reviewer starts:
  # one that blocks a merge on its first false positive is one the team
  # switches off. Raise it to warning or error once you have watched it work.
  fail_on: none

  # Findings below this level are dropped before they are published.
  min_severity: info

  # How much one run reads. max_files bounds the whole change; the other two
  # bound a single model call, and lowering them costs more calls, not fewer
  # files.
  max_files: 60
  max_files_per_request: 6
  token_budget_per_request: 60000

  # Paths never reviewed. Setting this REPLACES the built-in list rather than
  # adding to it, and that list already covers vendored and generated code,
  # lockfiles, and binaries. Print it with "nitpick explain-config" and paste
  # it back before you narrow it.
  # ignore:
  #   - "**/vendor/**"

  # A spending ceiling that changes what the run does rather than stopping it
  # partway: over the ceiling, files are ranked and the plan keeps what fits.
  # The rates are yours to state, in dollars per million tokens, because a
  # ceiling computed from a price nobody checked is worse than no ceiling.
  # budget:
  #   max_spend: 1.00
  #   prices: {input: 0.20, output: 0.80}

persona:
  # How far past outright defects the reviewer ranges: off, minimal, normal,
  # pedantic.
  nitpick: normal

  # How much prose each finding carries: terse, normal, detailed.
  verbosity: normal

`)

	p("linters:\n")
	p(`  # Deterministic analyzers, run beside the model. "auto" runs what is
  # installed and has something to read, "strict" fails the run when one named
  # below is missing, "off" runs none.
  mode: auto

`)
	renderLinterRoster(&b, matched)

	return b.String()
}

// renderLinterRoster writes linters.enabled and the commented rosters beside
// it.
//
// The split is the shipped design, not a formatting choice. Naming an analyzer
// in enabled makes its absence an error under mode: strict, so only the two
// that ship enabled are named; everything the catalog auto-detects is listed
// as a comment, which tells a reader it covers their code without promising
// their CI runner has it installed.
func renderLinterRoster(b *strings.Builder, matched []linters.CatalogEntry) {
	p := func(format string, a ...any) { _, _ = fmt.Fprintf(b, format, a...) }

	var named, auto, blocked []linters.CatalogEntry
	for _, e := range matched {
		switch {
		case e.NeedsConfig || e.Trusted:
			blocked = append(blocked, e)
		case e.Default:
			named = append(named, e)
		default:
			auto = append(auto, e)
		}
	}

	if len(named) == 0 {
		p(`  # Nothing in this checkout is Go or Python, the two analyzers that ship
  # enabled, so the list is empty rather than naming a tool with nothing to
  # read.
  enabled: []
`)
	} else {
		p("  # Matched by this checkout's files.\n  enabled:\n")
		for _, e := range named {
			p("    - %-14s # %s\n", e.Name, e.Languages)
		}
	}

	if len(auto) > 0 {
		p(`
  # Also matched here. These run on their own when installed
  # (linters.auto_detect), configured from outside the tree, so they need no
  # entry above. Move one into enabled to make its absence an error under
  # mode: strict.
`)
		for _, e := range auto {
			p("  #   %-14s %s\n", e.Name, e.Languages)
		}
	}

	if len(blocked) > 0 {
		p(`
  # Matched here, and each needs something only you can supply: a
  # configuration that resolves OUTSIDE this repository, or your word that its
  # code may run. A change under review may not supply the policy it is
  # reviewed under, which is why neither can be turned on from this file
  # alone.
`)
		for _, e := range blocked {
			need := e.Configuration
			if e.Trusted {
				need = "executes tree code; add to linters.trusted"
			}
			p("  #   %-14s %s\n", e.Name, need)
		}
	}
}

// writeVerified writes the file only after the loader has accepted it.
//
// The temporary file carries a model even when the real one does not, so that
// a config whose model is still to be filled in is checked as thoroughly as
// one that is complete. Validation refuses a default model with no provider,
// and skipping the check in that case would leave the shape of every other key
// unverified in exactly the case a new user is most likely to hit.
func writeVerified(path, body string, c chosenModel) error {
	check := body
	if !c.Known() {
		// The real file leaves models commented out, so the check supplies one.
		// Validation refuses a default model with no provider, and skipping the
		// load in that case would leave every other key in the file unverified
		// in the one case a new user is most likely to hit.
		check += "\nmodels:\n  default:\n    provider: openai\n    model: placeholder\n"
	}

	dir := filepath.Dir(path)

	// The file that gets validated and the file that gets kept are the same
	// bytes whenever a model is known, so the kept one is written first and
	// renamed into place after it passes. A rename is atomic: an interruption
	// leaves the old file or the new one, never half of either, which a second
	// os.WriteFile to the destination could not promise.
	keep, err := writeTemp(dir, body)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(keep) }()

	validated := keep
	if check != body {
		// No model was named, so the kept file's models block is commented out
		// and a copy carrying one is what the loader is given.
		if validated, err = writeTemp(dir, check); err != nil {
			return err
		}
		defer func() { _ = os.Remove(validated) }()
	}

	if _, err := config.LoadFile(validated); err != nil {
		return fmt.Errorf("the generated config did not load, so nothing was written: %w", err)
	}

	if err := os.Chmod(keep, 0o644); err != nil {
		return err
	}
	return os.Rename(keep, path)
}

// without returns the paths that are not the named repository-relative file.
func without(files []string, name string) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		if f != name {
			out = append(out, f)
		}
	}
	return out
}

// writeTemp writes body to a new file beside dir and returns its path.
func writeTemp(dir, body string) (string, error) {
	f, err := os.CreateTemp(dir, ".nitpick.yaml.*")
	if err != nil {
		return "", err
	}
	if _, err := f.WriteString(body); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// report says what the file was built from, so the choices it made are
// visible without opening it.
func report(out io.Writer, c chosenModel, matched []linters.CatalogEntry) {
	switch {
	case c.Known() && c.FromEnv:
		_, _ = fmt.Fprintf(out, "  model:     %s/%s (from the environment)\n", c.Provider, c.Model)
	case c.Known():
		_, _ = fmt.Fprintf(out, "  model:     %s/%s\n", c.Provider, c.Model)
	default:
		_, _ = fmt.Fprintf(out, "  model:     none. Set models.default in the file, or export %s and %s.\n",
			config.EnvProvider, config.EnvModel)
	}

	if len(matched) == 0 {
		_, _ = fmt.Fprintln(out, "  analyzers: none matched this checkout")
		return
	}
	names := make([]string, 0, len(matched))
	for _, e := range matched {
		names = append(names, e.Name)
	}
	_, _ = fmt.Fprintf(out, "  analyzers: %s\n", strings.Join(names, ", "))
	_, _ = fmt.Fprintln(out, "\nRun \"nitpick explain-config\" to see what this resolves to.")
}

// renderWorkflow writes the Action workflow from docs/ci.md, pinned to this
// build's major version.
func renderWorkflow(c chosenModel) string {
	provider, model := c.Provider, c.Model
	if !c.Known() {
		provider, model = "synthetic", "hf:moonshotai/Kimi-K3"
	}

	return fmt.Sprintf(`name: Review
on:
  pull_request:
    types: [opened, synchronize, reopened, ready_for_review]

permissions:
  contents: read
  pull-requests: write

concurrency:
  # A new push supersedes an in-flight review of the same pull request.
  group: nitpick-${{ github.event.pull_request.number }}
  cancel-in-progress: true

jobs:
  review:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0        # the reviewer needs history to diff against base
      - uses: jdziat/open-nitpick@%s
        with:
          provider: %s
          model: %s
          # Store the key as a repository secret; never a literal here.
          api-key: ${{ secrets.NITPICK_API_KEY }}
          # Advisory until you have seen how the model behaves on this
          # codebase. Raise it to warning or error to block a merge.
          fail-on: none
          skip-drafts: true
`, majorTag(version), provider, model)
}

// majorTag is the moving tag a workflow should follow.
//
// A pin to the exact version would freeze a new repository on whichever build
// happened to write its workflow, and a pin to a branch would follow work in
// progress. The major tag is the one that carries fixes without carrying a
// breaking change. An unreleased build has no major to name, so the workflow
// gets v1, which is what the documentation shows.
func majorTag(v string) string {
	digits := strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexByte(digits, '.'); i >= 0 {
		digits = digits[:i]
	}
	if n, err := strconv.Atoi(digits); err == nil && n > 0 {
		return "v" + strconv.Itoa(n)
	}
	return "v1"
}

// yamlScalar quotes a value where YAML would otherwise read it as something
// other than a string. A model name carrying a colon is the case that matters:
// "hf:moonshotai/Kimi-K3" unquoted is a mapping key.
func yamlScalar(s string) string {
	if strings.ContainsAny(s, ":#{}[],&*?|<>=!%@`\"'\\\n ") || s == "" {
		return strconv.Quote(s)
	}
	return s
}
