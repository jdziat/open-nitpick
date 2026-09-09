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
	"strings"
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
	// defaults, and a mistyped -config path silently reviews a repository under
	// settings its maintainers never chose.
	Missing string `yaml:"-"`

	// Dropped names endpoint and credential keys that were ignored because the
	// config file is not trusted to supply them. Callers should log these: a
	// silently ignored setting is very hard to diagnose.
	Dropped []string `yaml:"-"`

	// User is the user-level configuration file this one was overlaid onto,
	// empty when none applied. See internal/config/user.go.
	User string `yaml:"-"`

	// UserKeys are the settings the user-level file supplied, as dotted paths,
	// and UserOverridden are those of them the repository's own file then
	// replaced.
	//
	// Both are reported for the same reason Dropped is. A user-level file is
	// read from outside the checkout, so a value arriving from one is the
	// hardest kind to account for when a review does something unexpected, and
	// "which of my settings did this repository overrule" has no other answer.
	UserKeys       []string `yaml:"-"`
	UserOverridden []string `yaml:"-"`

	// Policy records which configuration decided this review's behavior, and
	// why it is not the file named by Source. Like Dropped, callers
	// should surface it: a maintainer whose newly added ignore rule did nothing
	// has no other way to learn that their rule was not the one in force.
	Policy Policy `yaml:"-"`
}

// ModelSpec describes one configured model. Provider names are matched against
// the SDK's provider registry, so any provider the SDK supports is usable here
// without changes to open-nitpick.
type ModelSpec struct {
	// Provider names the vendor or gateway the call goes to. "nitpick
	// providers" prints the list.
	Provider string `yaml:"provider"`

	// Model is the model id as that provider spells it, which is not a name
	// this project validates: an id the vendor does not serve fails at the
	// call, not at load.
	Model string `yaml:"model"`

	// BaseURL points at an alternate endpoint. This is what makes local models
	// (ollama, llama.cpp) and OpenAI-compatible gateways usable.
	BaseURL string `yaml:"base_url"`

	// APIKeyEnv names the environment variable holding the credential. When
	// empty the SDK provider resolves its own conventional variable.
	APIKeyEnv string `yaml:"api_key_env"`

	// APIKeyKeyring names a secret in the operating system's keystore as
	// "service/account", for example "open-nitpick/synthetic". Empty still
	// consults the keystore under a default name; see internal/llm/credential.go.
	APIKeyKeyring string `yaml:"api_key_keyring"`

	// CredentialCommand is a command whose standard output is the credential,
	// for a secret manager the keystore cannot reach: 1Password, AWS Secrets
	// Manager, Vault.
	//
	// It is a command and its arguments, not a shell line, and it is run
	// without a shell. A string split on spaces would make quoting decide
	// whether an argument containing one is an argument or two, and a shell
	// would make every character in this field a program.
	CredentialCommand []string `yaml:"credential_command"`

	// Temperature is passed through unchanged. Unset leaves the role's default,
	// which is 0 for every role here: a review that varies between runs on the
	// same diff is one nobody can hold to a measurement.
	Temperature *float64 `yaml:"temperature"`

	// MaxTokens caps the response. Zero lets the provider decide, which is the
	// shipped behaviour, and a cap too low truncates a finding rather than
	// dropping it.
	MaxTokens int `yaml:"max_tokens"`

	// Timeout bounds one call, retries excluded.
	Timeout time.Duration `yaml:"timeout"`

	// StructuredOutput selects how findings are constrained to the schema:
	// "auto" (default) prefers a JSON-Schema response format and falls back to
	// JSON mode and then to prompt-carried text, "schema" forces the schema
	// path, "json" forces JSON mode, "text" forces the text path, where the
	// schema rides in the prompt and the reply is parsed leniently.
	StructuredOutput StructuredMode `yaml:"structured_output"`

	// AllowPrivateEndpoint permits base_url to use plain HTTP or resolve to a
	// loopback or private address.
	//
	// It is off by default and must be opted into deliberately. open-nitpick
	// runs in CI against pull requests, and a pull request can edit.
	// nitpick.yaml, so an endpoint pointing at an internal address turns the
	// reviewer into an SSRF vector. The ollama and llamacpp providers already
	// allow loopback themselves, so the ordinary local-model path does not
	// need this.
	AllowPrivateEndpoint bool `yaml:"allow_private_endpoint"`

	// Extra carries provider-specific construction parameters (for example
	// runpod's endpoint_id) straight through to the SDK.
	Extra map[string]string `yaml:"extra"`

	// MaxRetries bounds two loops, not one, and they multiply.
	//
	// It is the SDK's retry count for a transient failure (a 429, a 5xx, a
	// dropped connection) and also the local stall loop's budget for a request
	// that returns nothing. One structured call can take several attempts
	// through the schema, JSON and repair paths, so the ceiling on provider
	// requests for a single extraction is the product of the three, not the
	// largest of them. Raising this past its default of 3 raises that ceiling
	// faster than it looks.
	MaxRetries *int `yaml:"max_retries"`

	// Fallback is the model a role escalates to when this one cannot answer:
	// a request cut at the output cap because the model looped, or structured
	// output that never parsed. It is tried once, and the batch fails if it
	// fails too.
	//
	// Escalating beats retrying the same model again. Runaway generation is
	// the model looping on the input rather than the endpoint truncating
	// early, so the same weights on another host reproduce it, and a third
	// attempt against a model that has already looped twice buys minutes for
	// the same answer. See issue #49.
	//
	// It overlays its parent, so a fallback naming only a model inherits the
	// provider, timeout and credential of the spec it hangs from. The provider
	// pin is the exception: overlay drops it when the model changes, because a
	// pin names the upstreams that serve ONE model.
	Fallback *ModelSpec `yaml:"fallback"`

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
	// StructuredText sends no response_format at all: the schema rides in
	// the prompt and the reply is parsed leniently. It is where auto lands
	// when a provider rejects json_object too, which OpenRouter's DeepInfra
	// turbo endpoints do, and it can be chosen outright for one.
	StructuredText StructuredMode = "text"
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

	// Router classifies each batch of files by what the change does, a
	// security-sensitive edit, a cross-file contract change, a migration,
	// so a route can match on that rather than only on which languages the
	// files are in. It is a cheap model reading the diff once per batch and
	// answering with a list of kinds. Optional: routes that name no kinds
	// never call it.
	Router *ModelSpec `yaml:"router"`

	// Fix is the model that edits code when a maintainer asks for a finding to
	// be applied. It has no default, deliberately: the model that reviews well
	// is not the one that edits well, every measurement in this repository
	// scores a reviewer on recall and noise, and neither says whether a model
	// can produce a change that compiles. Falling back to models.default would
	// ship an unmeasured capability under a measured model's name.
	Fix *ModelSpec `yaml:"fix"`

	// Embed is the model that turns text into vectors for knowledge
	// retrieval. It has no default and no fallback to Default, because an
	// embedding model is not a chat model and naming the reviewer here would
	// fail at the first request rather than at load.
	//
	// Not every provider serves embeddings, and one that does may serve a
	// different set of models for it than it does for chat, which is why this
	// names a provider rather than inheriting the reviewer's.
	Embed *ModelSpec `yaml:"embed"`

	// Routes choose the reviewing model per batch. The first route whose
	// match holds wins; a batch no route matches is reviewed by the review
	// model. Every model here overlays Default the way a role does, so a
	// route can name only what differs.
	Routes []Route `yaml:"routes"`

	// Ensemble names models that review every batch alongside the chosen
	// one. Their findings are pooled and the triage pass merges and reranks
	// them; a defect two reviewers report independently is one finding, and
	// its agreement is a reason to trust the level. A route's own ensemble
	// replaces this one for the batches it matches.
	Ensemble []ModelSpec `yaml:"ensemble"`
}

// Route sends the batches its match describes to a model.
type Route struct {
	// Name labels the route in logs and the report. Optional.
	Name string `yaml:"name"`

	Match RouteMatch `yaml:"match"`

	// Review is the model that reviews matching batches, overlaid on
	// Default. Nil keeps the review model and only changes the ensemble.
	Review *ModelSpec `yaml:"review"`

	// Ensemble replaces Models.Ensemble for matching batches. An empty list
	// on a route that sets it removes the ensemble for those batches.
	Ensemble []ModelSpec `yaml:"ensemble"`
}

// RouteMatch is the condition a batch has to meet. Every field that is set
// must hold; an unset field holds for every batch.
type RouteMatch struct {
	// Languages the batch's files are in, by the extension map in the
	// bundle package ("go", "python", "typescript", ...). The batch matches
	// when any of its files is in one of them.
	Languages []string `yaml:"languages"`

	// Kinds the router assigned the batch. The batch matches when it has any
	// of them. Naming a kind requires Models.Router.
	Kinds []string `yaml:"kinds"`

	// MinFiles bounds how few files the batch may hold. Zero is unset.
	MinFiles int `yaml:"min_files"`

	// MaxFiles bounds how many files the batch may hold. Zero is unset.
	MaxFiles int `yaml:"max_files"`
}

// The kinds a router may assign. They name what a change does, which is what
// the eval corpora vary and what the measured strengths differ on: the
// multi-file corpus is contract changes, the tuning corpus is largely
// security and logic, the info corpus is judgement calls.
const (
	KindSecurity    = "security"    // auth, secrets, injection, crypto, permissions
	KindConcurrency = "concurrency" // goroutines, locks, shared state, async
	KindContract    = "contract"    // a signature, type, API or file another file depends on
	KindData        = "data"        // migrations, schemas, serialization, persistence
	KindConfig      = "config"      // CI, build, infra, dependency manifests
	KindLogic       = "logic"       // ordinary control flow in one place
	KindTest        = "test"        // test code only
	KindDocs        = "docs"        // documentation only
)

// Kinds lists every kind a router may assign, in the order the prompt
// presents them.
func Kinds() []string {
	return []string{KindSecurity, KindConcurrency, KindContract, KindData, KindConfig, KindLogic, KindTest, KindDocs}
}

// Matches reports whether a batch with these languages, kinds and file count
// meets the match.
func (m RouteMatch) Matches(languages, kinds []string, files int) bool {
	if m.MinFiles > 0 && files < m.MinFiles {
		return false
	}
	if m.MaxFiles > 0 && files > m.MaxFiles {
		return false
	}
	if len(m.Languages) > 0 && !intersects(m.Languages, languages) {
		return false
	}
	if len(m.Kinds) > 0 && !intersects(m.Kinds, kinds) {
		return false
	}
	return true
}

// NeedsRouter reports whether any route matches on kinds.
func (m Models) NeedsRouter() bool {
	for _, r := range m.Routes {
		if len(r.Match.Kinds) > 0 {
			return true
		}
	}
	return false
}

// ResolveRoute returns the review spec a route uses.
func (m Models) ResolveRoute(r Route) ModelSpec {
	base := m.ResolveModel(RoleReview)
	if r.Review == nil {
		return base
	}
	return base.overlay(*r.Review)
}

// ResolveEnsemble returns the ensemble specs for a route (or the global
// ensemble when the route sets none), each overlaid on Default.
func (m Models) ResolveEnsemble(r *Route) []ModelSpec {
	specs := m.Ensemble
	if r != nil && r.Ensemble != nil {
		specs = r.Ensemble
	}
	out := make([]ModelSpec, 0, len(specs))
	for _, s := range specs {
		out = append(out, m.Default.overlay(s))
	}
	return out
}

// ResolveEmbed returns the embedding model, and false when none is named.
//
// Like ResolveFix and unlike ResolveModel, it does not fall back to Default.
// Overlaying a chat model's spec would produce a configuration that looks
// complete and fails at the first embedding request.
func (m Models) ResolveEmbed() (ModelSpec, bool) {
	if m.Embed == nil {
		return ModelSpec{}, false
	}
	return *m.Embed, true
}

// ResolveFix returns the model that edits code, and false when none is named.
//
// Unlike a role, this has no fallback to the default model. A caller with no
// fix model refuses rather than reaching for the reviewer.
func (m Models) ResolveFix() (ModelSpec, bool) {
	if m.Fix == nil {
		return ModelSpec{}, false
	}
	return m.Default.overlay(*m.Fix), true
}

// ResolveRouter returns the router spec, overlaid on Default, and whether
// one is configured.
func (m Models) ResolveRouter() (ModelSpec, bool) {
	if m.Router == nil {
		return ModelSpec{}, false
	}
	return m.Default.overlay(*m.Router), true
}

// Key identifies a spec for client caching: the fields that change which
// endpoint or weights answer, and nothing that only shapes the request.
func (s ModelSpec) Key() string {
	return strings.Join(append([]string{s.Provider, s.Model, s.BaseURL}, s.Providers...), "|")
}

func intersects(a, b []string) bool {
	for _, x := range a {
		for _, y := range b {
			if strings.EqualFold(strings.TrimSpace(x), strings.TrimSpace(y)) {
				return true
			}
		}
	}
	return false
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

	// Mention is the handle a comment uses to talk to the reviewer:
	// "@open-nitpick review" reviews again, "@open-nitpick resolve" closes the
	// thread, anything else is a question answered in the thread.
	Mention string `yaml:"mention"`

	// SkipMarkers are phrases that, in a pull request's title, body or head
	// commit message, ask for no review: the run reports "skipped" and
	// posts nothing. Matched case-insensitively.
	SkipMarkers []string `yaml:"skip_markers"`

	// Incremental makes a run on a pull request this tool has reviewed before
	// read only the files changed since that review, and withhold findings it
	// has already posted. It has no effect on a first review, on a local
	// review, or when the earlier revision is no longer reachable, a force
	// push reviews the whole change again.
	Incremental bool `yaml:"incremental"`

	// ResolveSuperseded lets an incremental run resolve its own earlier
	// comment threads when the lines they pointed at changed and the
	// finding did not recur, with a reply saying so. Off, and old threads
	// stay open for a person to close.
	ResolveSuperseded bool `yaml:"resolve_superseded"`

	// RelatedContext attaches, beside each changed file, the definitions it
	// imports from elsewhere in the repository and uses on a changed line, so
	// the model can read what a called function does instead of guessing. It
	// reads only files the change itself points at through its imports.
	RelatedContext bool `yaml:"related_context"`

	// RelatedContextCallers also attaches, for each exported symbol the
	// change redefines, the untouched functions that call it, found by
	// walking the repository's own files. That walk reads up to 150 files
	// nobody named and sends excerpts of them to the model, which is a
	// different consent boundary from "review my diff"; it is therefore a
	// separate switch, and off unless asked for. It does nothing unless
	// RelatedContext is on.
	RelatedContextCallers bool `yaml:"related_context_callers"`

	// Knowledge attaches entries from the shipped corpus that the change
	// resembles: antipatterns and standard-library contracts a model may not
	// carry. It needs models.embed, and does nothing without it.
	//
	// Off by default, and it should stay off until a measurement says
	// otherwise. Reference material beside a diff is a reason for a model to
	// report the reference, and a reviewer that invents defects out of a style
	// guide is worse than one that misses them.
	Knowledge bool `yaml:"knowledge"`

	// KnowledgeIndex names an index file to retrieve from, instead of the one
	// this build ships for the configured embedding model.
	//
	// The escape hatch that keeps the embedding model configuration rather
	// than a property of the binary: an operator whose provider is not one of
	// the shipped ones runs `nitpick knowledge-index` and names the result
	// here. It is checked against the corpus and the model like any other, so
	// naming a file buys no exemption from either.
	KnowledgeIndex string `yaml:"knowledge_index"`

	// ModelNotes adds the prompt layer addressed to the reviewing model's
	// family (prompt.ModelGuidance). On unless set to false; the switch
	// exists so the layer's contribution can be measured on its own.
	ModelNotes *bool `yaml:"model_notes"`

	// AgentPrompt adds a collapsed block under each published finding holding
	// what a coding agent needs to act on it: the anchor, every secondary
	// span, the class, the rationale as the reviewer wrote it, and the files
	// the reviewer read for that batch.
	//
	// It is assembled from fields the engine already holds rather than
	// generated, so it costs no model call and cannot assert anything the
	// review did not establish. The findings it helps most are the ones a
	// one-line suggestion cannot express, which are the ones a reader
	// otherwise translates into a change by hand.
	AgentPrompt bool `yaml:"agent_prompt"`

	// Slop asks the model for, and publishes, findings in the slop class:
	// generated-looking code that costs a reader, defined rule by rule in
	// the prompt layer prompt.SlopGuidance. Off by default in a review; a
	// whole-tree review turns it on. Independent of the nitpick level.
	Slop bool `yaml:"slop"`

	// RelatedContextTokens bounds how much related context is attached per
	// batch. It is spent from the request budget, so a large value narrows
	// the window each changed file itself gets.
	RelatedContextTokens int `yaml:"related_context_tokens"`

	// SummaryStyle chooses how the walkthrough at the top of a review is
	// produced. See internal/review/receipt.go.
	SummaryStyle SummaryStyle `yaml:"summary_style"`

	// TriageNoNewClaims restores the reviewer's own words over anything triage
	// rewrote, so triage may select, drop, group and re-anchor findings but may
	// not author them. See internal/review/noclaims.go.
	TriageNoNewClaims bool `yaml:"triage_no_new_claims"`

	// Approve lets a review that found nothing be submitted as an approval
	// rather than a comment. See Approve.
	Approve Approve `yaml:"approve"`

	// Respond bounds who may make the reviewer spend money by mentioning it.
	// See spend.go.
	Respond Respond `yaml:"respond"`

	// Budget bounds what one review may spend. Off by default; see budget.go.
	Budget Budget `yaml:"budget"`
}

// Approve decides whether a review that found nothing is submitted as an
// approval instead of a comment.
//
// Off by default. A reviewer that can approve is one whose approval a branch
// rule may come to require, and turning that on for somebody is their decision
// rather than this tool's.
//
// A GitHub App cannot approve a pull request it opened itself, so a fix branch
// this tool published is never approved by it whatever this says.
type Approve struct {
	// Enabled submits APPROVE when the review published no findings and
	// reviewed every file it planned to. Report.Complete() is the second half
	// and is not optional: approving a run whose batches partly failed is the
	// silence this package exists to refuse, wearing a green check.
	Enabled bool `yaml:"enabled"`

	// RequireAnalyzers additionally demands that every enabled analyzer ran
	// and covered the change, so an approval means the deterministic half
	// happened rather than that it was absent.
	//
	// Off by default: the model finding nothing is what a clean review
	// reports, and an operator who wants the analyzers counted says so. With
	// it on, an analyzer recorded as skipped or failed, or any entry in the
	// coverage list, holds the review at a comment.
	RequireAnalyzers bool `yaml:"require_analyzers"`
}

// Instruction is a path-scoped prompt addition. Every instruction whose Path
// glob matches a file is appended to that file's review prompt, so instructions
// compose rather than override one another.
type Instruction struct {
	// Path is a glob matched against each changed file's repository-relative
	// path, in the doublestar dialect, so "**/*.go" reaches every directory.
	Path string `yaml:"path"`

	// Prompt is appended to the review prompt for a matching file. It is
	// repository text and is fenced as untrusted before the model reads it.
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
	// It is the operator's answer to a question a constant cannot answer. The
	// sharpest case is golangci-lint rather than a security scanner: its
	// severity is arbitrary text an operator wrote in .golangci.yml, applied by
	// `severity.default` to every issue it reports including typecheck compile
	// errors, so one line there can make lll or misspell speak at the same
	// volume as an injection. `linters.max_severity: warning` is where a team
	// says a deterministic tool's opinion is worth a warning and no more, and
	// review.fail_on keeps its meaning for the model's own findings.
	//
	// The ceiling reaches the gate. review.Engine applies it after triage and
	// the expert pass, both of which may raise a severity, so the level it
	// caps is the one min_severity and fail_on read, see
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
	// not neutral. Its own defaults let the tree decide, a generated-file
	// header on line 1 of the file under review skipped that file entirely,
	// so open-nitpick now owns those defaults; see internal/linters/golangci.yml.
	//
	// WHY THERE IS NO "read it from the repository" OPTION. This project
	// already refuses to execute an analyzer binary that resolves inside the
	// tree under review, on the reasoning that the tree is written by the change
	// being reviewed. Analyzer configuration is the same object: it is policy,
	// and a change may not supply the policy it is reviewed under, the
	// invariant internal/config/basepolicy.go enforces for .nitpick.yaml. A.
	// golangci.yml carrying `linters: {default: none}` switches off the entire
	// deterministic half of its own review; a ruff `select = []` in
	// pyproject.toml does the same for Python; adding an eslint.config.js is
	// arbitrary JavaScript that eslint loads and EXECUTES with the review's
	// credentials in the environment.
	//
	// These keys are not scrubbed by Config.sanitize, unlike base_url and
	// persona.custom. Those name a network endpoint or free text that reaches a
	// model, both of which a change can supply outright. A value here can only
	// name a file the change cannot write, because internal/linters refuses any
	// configuration that resolves inside the repository, so the worst a merged
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
	// findings, for every repository, forever, which is the silencing this
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
	// on files in the tree is also what closes ENABLEMENT, semgrep used to
	// switch itself on when the tree contained .semgrep.yml or .semgrepignore,
	// so a pull request that ADDED one turned semgrep on with rules the pull
	// request wrote, in the run reviewing it.
	SemgrepConfig string `yaml:"semgrep_config"`

	// Configs names an analyzer configuration per catalog tool, keyed by the
	// tool's name in linters.enabled, an absolute path that must resolve
	// outside the repository, for the four keys' reasons. A tool with no
	// entry runs under the configuration open-nitpick ships for it, under
	// its own isolation flag, or not at all; `nitpick linters` says which.
	Configs map[string]string `yaml:"configs"`

	// AutoDetect runs the catalog analyzers marked auto, each installed,
	// isolated from the tree, and executing nothing from it, whenever the
	// change contains files it reads and without being named in Enabled;
	// `nitpick linters` says which those are. It is not every catalog
	// analyzer: the ones marked opt-in there are reached by naming and by
	// nothing else, so this key never turns them on. One that is not
	// installed is skipped, silently in auto mode and in strict mode alike:
	// strict is a promise about the analyzers an operator NAMED, and naming
	// one here is how to make its absence fail the run. Defaults to on.
	AutoDetect *bool `yaml:"auto_detect"`

	// Trusted names analyzers that EXECUTE the tree's own code in order to
	// analyze it, cargo clippy runs build scripts and procedural macros,
	// phpstan loads the project's autoloader, and that are therefore
	// refused by default. Name one here only where every change reviewed
	// comes from people who could already run code in this CI job.
	Trusted []string `yaml:"trusted"`
}

// AutoDetects reports whether catalog analyzers run without being named.
func (l Linters) AutoDetects() bool { return l.AutoDetect == nil || *l.AutoDetect }

// CapSeverity reduces an analyzer-reported severity to the ceiling this
// repository lets a deterministic tool claim. It is policy applied after
// parsing: what the analyzer said is a fact to
// record, what this repository acts on is a decision. Mapping "CRITICAL" onto
// error at parse time folds the two, taking the choice from every operator and
// leaving `fail_on: critical` gating on nothing. An unset or unrecognized
// ceiling caps nothing; Config.Validate rejects both, so the only way here
// with one is a Config assembled in code. Reading an empty ceiling as Rank()'s
// unknown floor would reduce every analyzer finding to info, the severity loss
// this exists to end.
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
// cannot drift apart. Every step here, scrubbing before anything reads the
// values, the environment fallback after, persona resolution, validation, has
// to apply to a config read out of git exactly as it applies to one read off
// disk, and a second hand-maintained copy of this sequence would eventually
// miss one.
func loadBytes(data []byte, source string) (*Config, error) {
	cfg := Defaults()

	userPath, userData, err := userDocument(nil)
	if err != nil {
		return nil, err
	}

	// The user-level file goes on first and the repository's over it, with the
	// keys the repository may not supply deleted from its document before the
	// merge rather than scrubbed from the struct after. See internal/config/user.go:
	// scrubbing afterwards would clear the user's own endpoint along with the
	// repository's, because by then nothing records which file supplied it.
	//
	// This runs before applyEnv so the environment can still supply what
	// neither file said.
	dropped, userKeys, overridden, err := cfg.overlay(userData, data, trustEndpointKeys(nil))
	if err != nil {
		return nil, fmt.Errorf("parse config %s: %w", source, err)
	}
	cfg.Dropped = dropped
	cfg.User, cfg.UserKeys, cfg.UserOverridden = userPath, userKeys, overridden

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

	// The user-level file applies here too, and that is deliberate. It lives
	// outside every checkout, so no change under review can reach it: the
	// reason the repository's own file is withheld does not touch it, and an
	// operator whose endpoint is configured once should not lose it because a
	// pull request edited a file somewhere else.
	userPath, userData, err := userDocument(nil)
	if err != nil {
		return nil, err
	}
	if len(userData) > 0 {
		if err := cfg.merge(userData); err != nil {
			return nil, fmt.Errorf("parse user config %s: %w", userPath, err)
		}
		node, _ := documentNode(userData)
		cfg.User, cfg.UserKeys = userPath, keyPaths(node)
	}

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

// ResolveFallback returns the effective spec for this model's fallback, or
// false when it has none.
//
// The fallback overlays its parent for the same reason a role overlays the
// default: naming a model should not mean restating a provider, a timeout and
// a credential that have not changed.
func (s ModelSpec) ResolveFallback() (ModelSpec, bool) {
	if s.Fallback == nil {
		return ModelSpec{}, false
	}

	// The parent's own fallback is not inherited: escalation is one step, and
	// a chain would let a misconfiguration walk a batch through every model in
	// the file before failing.
	parent := s
	parent.Fallback = nil

	out := parent.overlay(*s.Fallback)
	out.Fallback = nil
	return out, true
}

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
	// A pin names the upstreams that serve ONE model. It follows the model:
	// an override that changes the model starts with no pin, and one that
	// keeps the model keeps the pin unless it sets its own. Inherited across
	// models it sent qwen to an endpoint that only serves gemma, and the
	// router answered "no endpoints found" for every batch.
	switch {
	case over.Providers != nil:
		out.Providers = append([]string(nil), over.Providers...)
	case over.Model != "" && over.Model != base.Model:
		out.Providers = nil
	}
	if over.BaseURL != "" {
		out.BaseURL = over.BaseURL
	}
	// The three credential sources move as a group. They are alternatives
	// rather than layers, so an override that names any one of them replaces
	// all three: a role that says its key is in a keystore entry must not
	// inherit the default's credential_command and have it win, which is what
	// copying them independently produced.
	if over.APIKeyEnv != "" || over.APIKeyKeyring != "" || len(over.CredentialCommand) > 0 {
		out.APIKeyEnv = over.APIKeyEnv
		out.APIKeyKeyring = over.APIKeyKeyring
		out.CredentialCommand = append([]string(nil), over.CredentialCommand...)
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
