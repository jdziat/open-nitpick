// Package config loads, merges, and validates open-nitpick configuration.
//
// Configuration comes from a .nitpick.yaml at the root of the repository being
// reviewed, overlaid onto built-in defaults so that a repository with no config
// file still produces a sensible review.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// FileName is the configuration file looked up at the repository root.
const FileName = ".nitpick.yaml"

// Config is the fully resolved open-nitpick configuration.
type Config struct {
	Models       Models        `yaml:"models"`
	Review       Review        `yaml:"review"`
	Instructions []Instruction `yaml:"instructions"`
	Linters      Linters       `yaml:"linters"`

	// Persona controls the reviewer's voice and how far past outright defects
	// it ranges.
	Persona Persona `yaml:"persona"`

	// Validation controls the expert re-check applied to findings on their way
	// to publication.
	Validation Validation `yaml:"validation"`

	// Source records where the configuration was loaded from. It is empty when
	// only built-in defaults were used.
	Source string `yaml:"-"`

	// Missing is the path a configuration file was looked for at and not found,
	// set only when Source is empty. The two are exclusive: one of them always
	// names the file this configuration is about.
	//
	// Recording an absence matters for the same reason recording the source
	// does. A change that DELETES .nitpick.yaml leaves a checkout with no
	// Source, so a review that only knows about files it read cannot tell that
	// the change replaced the repository's accepted policy with built-in
	// defaults — and a mistyped -config path silently reviews a repository under
	// settings its maintainers never chose.
	Missing string `yaml:"-"`

	// Dropped names endpoint and credential keys that were ignored because the
	// config file is not trusted to supply them. Callers should log these: a
	// silently ignored setting is very hard to diagnose.
	Dropped []string `yaml:"-"`

	// Policy records which configuration decided this review's behavior, and
	// why it is not simply the file named by Source. Like Dropped, callers
	// should surface it: a maintainer whose newly added ignore rule did nothing
	// has no other way to learn that their rule was not the one in force.
	Policy Policy `yaml:"-"`
}

// ModelSpec describes one configured model. Provider names are matched against
// the SDK's provider registry, so any provider the SDK supports is usable here
// without changes to open-nitpick.
type ModelSpec struct {
	Provider string `yaml:"provider"`
	Model    string `yaml:"model"`

	// BaseURL points at an alternate endpoint. This is what makes local models
	// (ollama, llama.cpp) and OpenAI-compatible gateways usable.
	BaseURL string `yaml:"base_url"`

	// APIKeyEnv names the environment variable holding the credential. When
	// empty the SDK provider resolves its own conventional variable.
	APIKeyEnv string `yaml:"api_key_env"`

	Temperature *float64      `yaml:"temperature"`
	MaxTokens   int           `yaml:"max_tokens"`
	Timeout     time.Duration `yaml:"timeout"`

	// StructuredOutput selects how findings are constrained to the schema:
	// "auto" (default) prefers a JSON-Schema response format and falls back to
	// JSON mode, "schema" forces the schema path, "json" forces JSON mode.
	StructuredOutput StructuredMode `yaml:"structured_output"`

	// AllowPrivateEndpoint permits base_url to use plain HTTP or resolve to a
	// loopback or private address.
	//
	// It is off by default and must be opted into deliberately. open-nitpick
	// runs in CI against pull requests, and a pull request can edit
	// .nitpick.yaml — so an endpoint pointing at an internal address turns the
	// reviewer into an SSRF vector. The ollama and llamacpp providers already
	// allow loopback themselves, so the ordinary local-model path does not
	// need this.
	AllowPrivateEndpoint bool `yaml:"allow_private_endpoint"`

	// Extra carries provider-specific construction parameters (for example
	// runpod's endpoint_id) straight through to the SDK.
	Extra map[string]string `yaml:"extra"`

	// MaxRetries bounds SDK-level retries for transient failures.
	MaxRetries *int `yaml:"max_retries"`

	// Providers pins a router to these upstream providers, tried in order,
	// with no fallback beyond them. OpenRouter only; slugs are OpenRouter's,
	// with an endpoint suffix where one exists ("deepinfra/turbo"). It is
	// what makes a model whose default routing stalls usable: the eval
	// battery measured gemma-4-31b losing one review in five across
	// OpenRouter's fifteen endpoints for it, and a pin names the one that
	// answers.
	Providers []string `yaml:"providers"`
}

// StructuredMode selects a structured-output strategy.
type StructuredMode string

// Supported structured-output strategies.
const (
	StructuredAuto   StructuredMode = "auto"
	StructuredSchema StructuredMode = "schema"
	StructuredJSON   StructuredMode = "json"
)

// Models maps review roles to model specifications. Every role falls back to
// Default when unset, which lets a minimal config name a single model while a
// tuned config uses a cheap model for triage and a strong one for review.
type Models struct {
	Default ModelSpec  `yaml:"default"`
	Review  *ModelSpec `yaml:"review"`
	Triage  *ModelSpec `yaml:"triage"`

	// Validate is the model the domain experts speak through. It is separate
	// so a deployment can have one model review and a different one check the
	// result: an independent check is worth more when it is not the same
	// weights re-reading their own claim.
	Validate *ModelSpec `yaml:"validate"`
}

// Review holds reviewer behavior and gating policy.
type Review struct {
	// MaxFiles caps how many changed files are reviewed in one run.
	MaxFiles int `yaml:"max_files"`

	// TokenBudgetPerRequest bounds the context assembled for a single model
	// call, including full file bodies.
	TokenBudgetPerRequest int `yaml:"token_budget_per_request"`

	// MaxFilesPerRequest caps how many files are batched into one call.
	MaxFilesPerRequest int `yaml:"max_files_per_request"`

	// Concurrency bounds in-flight model calls.
	Concurrency int `yaml:"concurrency"`

	// FailOn is the lowest severity that makes the run exit non-zero.
	// "none" never fails the run.
	FailOn Severity `yaml:"fail_on"`

	// MinSeverity drops findings below this severity before publishing.
	MinSeverity Severity `yaml:"min_severity"`

	// Ignore lists doublestar globs excluded from review.
	Ignore []string `yaml:"ignore"`

	// IncludeFullFiles sends whole changed files alongside the diff when the
	// token budget allows. Disabling it reviews hunks in isolation.
	IncludeFullFiles bool `yaml:"include_full_files"`

	// MaxFileBytes skips files larger than this when reading full contents.
	MaxFileBytes int `yaml:"max_file_bytes"`

	// SkipGenerated drops files carrying a generated-code marker.
	SkipGenerated bool `yaml:"skip_generated"`

	// Summary emits a walkthrough summary alongside inline comments.
	Summary bool `yaml:"summary"`

	// Incremental makes a run on a pull request this tool has reviewed before
	// read only the files changed since that review, and withhold findings it
	// has already posted. It has no effect on a first review, on a local
	// review, or when the earlier revision is no longer reachable — a force
	// push reviews the whole change again.
	Incremental bool `yaml:"incremental"`

	// RelatedContext attaches, beside each changed file, the definitions it
	// imports from elsewhere in the repository and uses on a changed line, so
	// the model can read what a called function does instead of guessing.
	RelatedContext bool `yaml:"related_context"`

	// ModelNotes adds the prompt layer addressed to the reviewing model's
	// family (prompt.ModelGuidance). On unless set to false; the switch
	// exists so the layer's contribution can be measured on its own.
	ModelNotes *bool `yaml:"model_notes"`

	// RelatedContextTokens bounds how much related context is attached per
	// batch. It is spent from the request budget, so a large value narrows
	// the window each changed file itself gets.
	RelatedContextTokens int `yaml:"related_context_tokens"`
}

// Instruction is a path-scoped prompt addition. Every instruction whose Path
// glob matches a file is appended to that file's review prompt, so instructions
// compose rather than override one another.
type Instruction struct {
	Path   string `yaml:"path"`
	Prompt string `yaml:"prompt"`
}

// Linters configures deterministic analyzers whose findings are fed to the
// model as evidence for triage.
type Linters struct {
	// Enabled lists runner names to consider.
	Enabled []string `yaml:"enabled"`

	// Mode is "auto" (run only runners detected in the repo), "strict" (error
	// when an enabled runner is missing), or "off".
	Mode LinterMode `yaml:"mode"`

	// Timeout bounds each individual runner.
	Timeout time.Duration `yaml:"timeout"`

	// OnlyChangedLines drops linter findings on lines the diff did not touch.
	OnlyChangedLines bool `yaml:"only_changed_lines"`

	// MaxSeverity is the highest severity a finding attributed to a
	// deterministic analyzer is published and gated at, whatever the analyzer
	// called it. It defaults to critical, which reduces nothing.
	//
	// It is the operator's answer to a question that used to be answered by a
	// constant. The sharpest case is not a security scanner: golangci-lint's
	// severity is arbitrary text an operator wrote in .golangci.yml, applied by
	// `severity.default` to every issue it reports including typecheck compile
	// errors, so one line there can make lll or misspell speak at the same
	// volume as an injection. `linters.max_severity: warning` is where a team
	// says a deterministic tool's opinion is worth a warning and no more, and
	// review.fail_on keeps its meaning for the model's own findings.
	//
	// The ceiling reaches the gate. review.Engine applies it after triage and
	// the expert pass, both of which may raise a severity, so the level it
	// caps is the one min_severity and fail_on read — see
	// Engine.capAnalyzerFindings, which also documents the one finding it
	// cannot describe: one triage reworded past recognition, which is published
	// with no analyzer attribution at all.
	MaxSeverity Severity `yaml:"max_severity"`

	// The four keys below name an analyzer configuration that must resolve
	// OUTSIDE the repository under review. Empty is the default and means the
	// analyzer runs isolated from the tree: golangci-lint under a config
	// open-nitpick ships, ruff under --isolated, and eslint and semgrep not at
	// all.
	//
	// "No configuration at all" is what golangci-lint used to get, and it was
	// not neutral. Its own defaults let the tree decide — a generated-file
	// header on line 1 of the file under review skipped that file entirely —
	// so open-nitpick now owns those defaults; see internal/linters/golangci.yml.
	//
	// WHY THERE IS NO "read it from the repository" OPTION. This project
	// already refuses to execute an analyzer binary that resolves inside the
	// tree under review, on the reasoning that the tree is written by the change
	// being reviewed. Analyzer configuration is the same object: it is policy,
	// and a change may not supply the policy it is reviewed under — the
	// invariant internal/config/basepolicy.go enforces for .nitpick.yaml. A
	// .golangci.yml carrying `linters: {default: none}` switches off the entire
	// deterministic half of its own review; a ruff `select = []` in
	// pyproject.toml does the same for Python; adding an eslint.config.js is
	// arbitrary JavaScript that eslint loads and EXECUTES with the review's
	// credentials in the environment.
	//
	// These keys are not scrubbed by Config.sanitize, unlike base_url and
	// persona.custom. Those name a network endpoint or free text that reaches a
	// model, both of which a change can supply outright. A value here can only
	// name a file the change cannot write, because internal/linters refuses any
	// configuration that resolves inside the repository — so the worst a merged
	// value does is point at a file the operator's own environment already has.

	// GolangciConfig is an absolute path to a .golangci.yml outside the
	// repository. Empty runs golangci-lint under open-nitpick's own config,
	// materialized outside the repository.
	GolangciConfig string `yaml:"golangci_config"`

	// RuffConfig is an absolute path to a ruff.toml or pyproject.toml outside
	// the repository. Empty runs ruff with --isolated.
	RuffConfig string `yaml:"ruff_config"`

	// ESLintConfig is an absolute path to an eslint flat config outside the
	// repository. Empty means eslint does not run.
	//
	// It is off rather than isolated because eslint has no useful isolated mode:
	// --no-config-lookup yields zero configured rules and therefore zero
	// findings, for every repository, forever — which is the silencing this
	// change exists to prevent, executed globally by our own hand. An external
	// config also has to resolve its own plugin imports, so pointing this at a
	// bare file is not enough; see the README.
	ESLintConfig string `yaml:"eslint_config"`

	// SemgrepConfig is an absolute path to a rule file outside the repository,
	// or a registry reference (`p/...`, `r/...`). Empty means semgrep does not
	// run.
	//
	// It is off rather than isolated because semgrep has no default ruleset:
	// with no --config it analyzes nothing. Keying detection on this rather than
	// on files in the tree is also what closes ENABLEMENT — semgrep used to
	// switch itself on when the tree contained .semgrep.yml or .semgrepignore,
	// so a pull request that ADDED one turned semgrep on with rules the pull
	// request wrote, in the run reviewing it.
	SemgrepConfig string `yaml:"semgrep_config"`

	// Configs names an analyzer configuration per catalog tool, keyed by the
	// tool's name in linters.enabled — an absolute path that must resolve
	// outside the repository, for the four keys' reasons. A tool with no
	// entry runs under the configuration open-nitpick ships for it, under
	// its own isolation flag, or not at all; `nitpick linters` says which.
	Configs map[string]string `yaml:"configs"`

	// AutoDetect runs every catalog analyzer that is installed, isolated from
	// the tree, and executes nothing from it, whenever the change contains
	// files it reads — without each being named in Enabled. One that is not
	// installed is skipped, silently in auto mode and in strict mode alike:
	// strict is a promise about the analyzers an operator NAMED, and naming
	// one here is how to make its absence fail the run. Defaults to on.
	AutoDetect *bool `yaml:"auto_detect"`

	// Trusted names analyzers that EXECUTE the tree's own code in order to
	// analyze it — cargo clippy runs build scripts and procedural macros,
	// phpstan loads the project's autoloader — and that are therefore
	// refused by default. Name one here only where every change reviewed
	// comes from people who could already run code in this CI job.
	Trusted []string `yaml:"trusted"`
}

// AutoDetects reports whether catalog analyzers run without being named.
func (l Linters) AutoDetects() bool { return l.AutoDetect == nil || *l.AutoDetect }

// CapSeverity reduces an analyzer-reported severity to the ceiling this
// repository lets a deterministic tool claim.
//
// It is POLICY, applied after parsing, and the split is the point. What the
// analyzer said is a fact to record; what this repository will act on is a
// decision. mapSeverity used to make the second decision by destroying the first
// — folding "CRITICAL" onto error — which took the choice away from every
// operator at once and left `fail_on: critical` gating on nothing, since no
// analyzer finding could reach the level it names.
//
// An unset or unrecognized ceiling caps nothing. Config.Validate rejects both
// before a review runs, so the only way to arrive here with one is a Config
// assembled in code; the alternative reading of an empty ceiling is Rank()'s
// unknown floor, which would silently reduce every analyzer finding to info —
// the same class of invisible severity loss this exists to end.
func (l Linters) CapSeverity(s Severity) Severity {
	ceiling := l.MaxSeverity.normalized()
	if !ceiling.IsFinding() {
		return s
	}
	if s.Rank() > ceiling.Rank() {
		return ceiling
	}
	return s
}

// LinterMode selects linter execution behavior.
type LinterMode string

// Supported linter modes.
const (
	LinterAuto   LinterMode = "auto"
	LinterStrict LinterMode = "strict"
	LinterOff    LinterMode = "off"
)

// Load reads configuration for the repository rooted at repoRoot, overlaying
// any .nitpick.yaml onto built-in defaults. A missing config file is not an
// error. The returned Config is validated.
func Load(repoRoot string) (*Config, error) {
	return LoadFile(filepath.Join(repoRoot, FileName))
}

// LoadFile loads configuration from an explicit path. A missing file yields
// validated defaults; any other read or parse failure is returned.
func LoadFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		cfg, err := defaultConfig()
		if err != nil {
			return nil, fmt.Errorf("no %s found and environment is incomplete: %w", FileName, err)
		}
		cfg.Missing = path
		return cfg, nil
	case err != nil:
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	return loadBytes(data, path)
}

// loadBytes builds a validated Config from one config file's contents.
//
// LoadFile and base-revision policy resolution both go through it so the two
// cannot drift apart. Every step here — scrubbing before anything reads the
// values, the environment fallback after, persona resolution, validation — has
// to apply to a config read out of git exactly as it applies to one read off
// disk, and a second hand-maintained copy of this sequence would eventually
// miss one.
func loadBytes(data []byte, source string) (*Config, error) {
	cfg := Defaults()

	if err := cfg.merge(data); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", source, err)
	}

	// Strip endpoint and credential keys before anything reads them. This runs
	// before applyEnv so the environment can still supply what the file may not.
	cfg.Dropped = cfg.sanitize(nil)

	cfg.applyEnv(nil)
	cfg.Persona = cfg.Persona.Resolve()
	cfg.Source = source

	// Stated even before ResolvePolicy has had a say, so that every Config can
	// answer which policy it carries. A field that is accurate only after some
	// other call has run is a field that reads as a lie the rest of the time.
	cfg.Policy = Policy{Origin: OriginCheckout, Path: source}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config %s: %w", source, err)
	}
	return cfg, nil
}

// defaultConfig returns the built-in configuration with the environment
// applied: what governs a review when no config file supplies anything.
func defaultConfig() (*Config, error) {
	cfg := Defaults()

	cfg.applyEnv(nil)
	cfg.Persona = cfg.Persona.Resolve()
	cfg.Policy = Policy{Origin: OriginCheckout}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// merge decodes YAML over the receiver. Scalar and mapping fields present in
// the document replace the default; fields absent from the document keep their
// default value. Sequences replace wholesale rather than appending, so a
// repository can narrow the default ignore list rather than only widening it.
func (c *Config) merge(data []byte) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)

	if err := dec.Decode(c); err != nil {
		// An empty document yields io.EOF and leaves defaults in place.
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	return nil
}

// ModelNotesOn reports whether the model-family prompt layer is in force.
func (r Review) ModelNotesOn() bool { return r.ModelNotes == nil || *r.ModelNotes }

// ResolveModel returns the effective spec for a role, falling back to the
// default model for any field the role does not set.
func (m Models) ResolveModel(role Role) ModelSpec {
	var override *ModelSpec
	switch role {
	case RoleReview:
		override = m.Review
	case RoleTriage:
		override = m.Triage
	case RoleValidate:
		override = m.Validate
	}
	if override == nil {
		return m.Default
	}
	return m.Default.overlay(*override)
}

// Role names a model's function within a review run.
type Role string

// Supported model roles.
const (
	// RoleReview analyzes diffs and produces findings.
	RoleReview Role = "review"
	// RoleTriage dedupes and filters findings across batches.
	RoleTriage Role = "triage"
	// RoleValidate independently checks a finding before it is published.
	RoleValidate Role = "validate"
)

// overlay returns base with every field the override sets replaced.
func (base ModelSpec) overlay(over ModelSpec) ModelSpec {
	out := base
	if over.Provider != "" {
		out.Provider = over.Provider
	}
	if over.Model != "" {
		out.Model = over.Model
	}
	if len(over.Providers) > 0 {
		out.Providers = append([]string(nil), over.Providers...)
	}
	if over.BaseURL != "" {
		out.BaseURL = over.BaseURL
	}
	if over.APIKeyEnv != "" {
		out.APIKeyEnv = over.APIKeyEnv
	}
	if over.Temperature != nil {
		out.Temperature = over.Temperature
	}
	if over.MaxTokens != 0 {
		out.MaxTokens = over.MaxTokens
	}
	if over.Timeout != 0 {
		out.Timeout = over.Timeout
	}
	if over.StructuredOutput != "" {
		out.StructuredOutput = over.StructuredOutput
	}
	if over.MaxRetries != nil {
		out.MaxRetries = over.MaxRetries
	}
	if over.AllowPrivateEndpoint {
		out.AllowPrivateEndpoint = true
	}
	if len(over.Extra) > 0 {
		out.Extra = make(map[string]string, len(base.Extra)+len(over.Extra))
		maps.Copy(out.Extra, base.Extra)
		maps.Copy(out.Extra, over.Extra)
	}
	return out
}

// InstructionsFor returns the prompts of every instruction whose glob matches
// path, in configuration order.
func (c *Config) InstructionsFor(path string) []string {
	var out []string
	for _, ins := range c.Instructions {
		if matchGlob(ins.Path, path) {
			out = append(out, ins.Prompt)
		}
	}
	return out
}

// Ignored reports whether path is excluded from review.
func (c *Config) Ignored(path string) bool {
	for _, pattern := range c.Review.Ignore {
		if matchGlob(pattern, path) {
			return true
		}
	}
	return false
}
