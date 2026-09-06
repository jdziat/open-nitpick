package evals

import (
	"bufio"
	"context"
	"fmt"
	"gopkg.in/yaml.v3"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	llms "github.com/nocturnium/llm-go-sdk/v6"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/llm"
	"github.com/jdziat/open-nitpick/internal/review"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// Environment variables controlling a run.
const (
	// EnvAPIKey holds the OpenRouter credential.
	EnvAPIKey = "OPENROUTER_API_KEY"

	// EnvModels overrides the model list (comma-separated OpenRouter ids).
	EnvModels = "NITPICK_EVAL_MODELS"

	// EnvRuns sets how many times each fixture is reviewed, for measuring
	// run-to-run stability.
	EnvRuns = "NITPICK_EVAL_RUNS"

	// EnvFixtures limits the run to named fixtures.
	EnvFixtures = "NITPICK_EVAL_FIXTURES"

	// EnvModelNotes set to "off" removes the model-family prompt layer, so a
	// run can measure the layer against its absence.
	EnvModelNotes = "NITPICK_EVAL_MODEL_NOTES"

	// EnvEngineLog names a file that receives every review's engine log at
	// WARN and above, appended. A battery discards the engine log otherwise,
	// which is how "lost 9 of 42 reviews" arrived with nothing to say why:
	// the retries, the stalls and the provider's errors all went to a
	// discarding handler.
	EnvEngineLog = "NITPICK_EVAL_ENGINE_LOG"

	// EnvCapture names a directory to write every raw model response into.
	// Those responses become offline regression fixtures.
	EnvCapture = "NITPICK_EVAL_CAPTURE"

	// EnvRelatedContext switches review.related_context and
	// review.related_context_callers on for every review in
	// the run, so the feature can be measured against the same corpus with it
	// off. Any non-empty value other than "0" or "false" enables it.
	EnvRelatedContext = "NITPICK_EVAL_RELATED_CONTEXT"

	// EnvSlop switches review.slop on for every review in the run, which the
	// slop corpus needs: its plants are in a class the default never asks
	// for. Any non-empty value other than "0" or "false" enables it.
	EnvSlop = "NITPICK_EVAL_SLOP"

	// EnvValidation switches validation (the expert pass) on for every review
	// in the run, so its effect on recall and noise can be measured.
	EnvValidation = "NITPICK_EVAL_VALIDATION"

	// EnvTimeout overrides how long a single review may take.
	//
	// The default suits a one-file fixture. It is not enough for a multi-file
	// one: collecting the seven-file ts-unbounded-memo-key against the incumbent
	// CLI exceeded four minutes and was refused -- correctly, since a truncated
	// review must never be cached, but the run then had no way to ask for more
	// time without editing this file. A corpus that now contains fixtures of very
	// different sizes needs the bound to be settable per run.
	EnvTimeout = "NITPICK_EVAL_TIMEOUT"
)

// The endpoint constant that used to live here is gone. internal/llm registers
// "openrouter" in its package init, and this package imports it, so the harness
// names that provider instead of rebuilding an equivalent from base_url. Two
// copies of the endpoint meant two owners that could drift, and the harness
// resolved its credential by the openai provider's rules rather than the ones
// production uses.

// DefaultModels is a deliberately small, cheap matrix that exercises the
// distinct code paths structured output can take.
//
// The point is not to rank models. It is to prove the tooling copes with the
// ways real providers differ:
//   - a model advertising json_schema support takes the schema path
//   - a model without it must fall back to JSON mode and still parse
//   - a reasoning model emits <think> blocks the extractor has to strip
func DefaultModels() []Model {
	return []Model{
		// Current generation. An earlier survey of this catalog printed only the
		// four cheapest models per provider, which systematically surfaced the
		// oldest and hid these — gpt-5.6-luna is CHEAPER than half the models
		// that got tested instead.
		{ID: "openai/gpt-5.6-luna", Kind: "frontier"},
		{ID: "openai/gpt-5.6-terra", Kind: "frontier"},
		{ID: "openai/gpt-5.4", Kind: "frontier"},
		{ID: "anthropic/claude-opus-5", Kind: "frontier"},
		{ID: "anthropic/claude-sonnet-4.6", Kind: "frontier"},
		{ID: "google/gemini-3.5-flash", Kind: "frontier"},
		{ID: "google/gemini-3.1-pro-preview", Kind: "frontier"},
		{ID: "deepseek/deepseek-v4-pro", Kind: "frontier"},
		{ID: "z-ai/glm-5.1", Kind: "frontier"},
		{ID: "moonshotai/kimi-k2.7-code", Kind: "coding"},

		// Carried forward as benchmarks: the best of the previous battery on
		// each axis, so the two runs are comparable.
		{ID: "z-ai/glm-5.3-flash", Kind: "value"},       // the iteration model; see Makefile `quick`
		{ID: "z-ai/glm-5.2", Kind: "incumbent"},         // best combined rank
		{ID: "minimax/minimax-m2.7", Kind: "incumbent"}, // best judged, full volume
		{ID: "qwen/qwen3.7-flash", Kind: "incumbent"},   // best value at $0.03

		// Higher-capability qwen. Only the flash tier was ever tested, because
		// the same truncated survey showed just the four cheapest of 48 qwen
		// models. These are the actual top of that family.
		{ID: "qwen/qwen3.7-max", Kind: "frontier"},
		{ID: "qwen/qwen3.7-plus", Kind: "frontier"},
		{ID: "qwen/qwen3-max-thinking", Kind: "reasoning"},
		{ID: "qwen/qwen3-coder-plus", Kind: "coding"},

		// Dropped after the previous battery, with the reason:
		//   mistralai/mistral-medium-3.1 — judged last (2.74), 7 inflated of 11
		//   kwaipilot/kat-coder-pro-v2   — precision 0.78, 5 inflated
		//   moonshotai/kimi-k3           — 74s and $3.00 for a mid-tier grade
		//   anthropic/claude-sonnet-5    — superseded by sonnet-4.6 / opus-5
		//   google/gemini-2.5-flash      — superseded by the gemini-3.x line
		//   openai/gpt-5-mini, gpt-oss-120b — superseded by the 5.6 line
		//   ~deepseek/deepseek-v4-flash-latest — superseded by v4-pro
	}
}

// Model is one endpoint under test.
type Model struct {
	ID string

	// Kind records why the model is in the matrix, so a report says what a
	// failure actually implies.
	Kind string

	// Provider is the shipped provider the model is reached through, empty
	// for OpenRouter. It is the prefix before the first ":" in an EnvModels
	// entry, so "synthetic:hf:Qwen/Qwen3.8-27B" runs Qwen through Synthetic
	// and reports under that full id, which is what keeps two hosts of the
	// same weights as two rows.
	Provider string

	// Name is the model id sent to the provider: ID with the provider prefix
	// and any routing pin removed. Empty means ID.
	Name string

	// Pin is the OpenRouter upstream the run is pinned to, from an "@slug"
	// suffix on the entry ("google/gemma-4-31b-it@deepinfra/turbo"). The
	// full id stays the row's name, so a pinned run and the default routing
	// of the same weights are two rows.
	Pin string

	// RouteFile is a YAML file whose `models:` block configures a routed or
	// ensemble review, from a "route:<path>" entry. The row is named
	// "route:<file stem>" and its cost is the sum over every model the
	// review reached for, priced each at its own rate.
	RouteFile string
}

// Composite reports whether the run speaks through several models.
func (m Model) Composite() bool { return m.RouteFile != "" }

// ParseModel reads an EnvModels entry into a Model.
func ParseModel(id string) Model {
	id = strings.TrimSpace(id)
	m := Model{ID: id, Kind: "custom"}
	if path, ok := strings.CutPrefix(id, "route:"); ok {
		m.RouteFile = path
		m.ID = "route:" + strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		return m
	}
	name := id
	if provider, rest, ok := strings.Cut(name, ":"); ok && provider == llm.ProviderSynthetic {
		m.Provider, name = provider, rest
	}
	if base, pin, ok := strings.Cut(name, "@"); ok && pin != "" {
		m.Pin, name = pin, base
	}
	if name != id {
		m.Name = name
	}
	return m
}

// PriceID is the price-table key for the model: the id without a routing
// pin, because the table records the model's rate at the endpoint it names
// and a pinned run that names a different one is priced as unknown by
// PriceTable.Price rather than at the wrong endpoint's rate.
func (m Model) PriceID() string {
	base, _, _ := strings.Cut(m.ID, "@")
	return base
}

// modelName is the id the provider sees.
func (m Model) modelName() string {
	if m.Name != "" {
		return m.Name
	}
	return m.ID
}

// provider is the shipped provider the run goes through.
func (m Model) provider() string {
	if m.Provider != "" {
		return m.Provider
	}
	return llm.ProviderOpenRouter
}

// Options configure a run.
type Options struct {
	Models   []Model
	Fixtures []Fixture
	Runs     int

	// CaptureDir, when set, receives every raw model response.
	CaptureDir string

	// Prices is the rate table this run reports cost against, resolved ONCE at
	// setup so a battery cannot be priced against two different tables — and so
	// that a bad NITPICK_EVAL_PRICES is a setup error rather than a discovery
	// made ninety minutes and a real invoice later, at the moment the report is
	// formatted. Resolving it here also fixes the rates for the whole run: a
	// table re-read per row could pick up an edit mid-battery and rank the first
	// rows against different numbers from the last.
	Prices *PriceTable

	// Timeout bounds a single review.
	Timeout time.Duration

	// Log, when set, receives the engine's log for every review in the run.
	// Nil discards it, which is right for a battery and wrong for a probe.
	Log *slog.Logger

	// Tune, when set, adjusts the configuration every review in the run is
	// built with, after the environment has been read. It is how one process
	// measures two variants of the engine against the same corpus.
	Tune func(*config.Config)

	// buildClient constructs the model client. It is unexported and nil in
	// every real run, where llm.Build is used.
	//
	// It exists so a test can drive the WHOLE harness from a scripted model.
	// Without a seam here the only way to prove that reported usage reaches
	// RunResult is to spend money at a real provider, and an assertion nobody
	// can afford to run is not a guard — which matters for exactly this field's
	// neighbours, since a cost column silently reading zero looks identical to
	// a cheap model.
	buildClient func(config.ModelSpec) (*llm.Client, error)
}

// LoadDotEnv reads KEY=VALUE pairs from path into the process environment
// without overwriting variables that are already set.
//
// The eval harness needs a credential that must never be committed, so reading
// it from an ignored .env is the least awkward option. Values already in the
// environment win, so CI can supply a secret without a file.
func LoadDotEnv(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}

		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)

		if _, exists := os.LookupEnv(key); !exists {
			if err := os.Setenv(key, value); err != nil {
				return err
			}
		}
	}

	return scanner.Err()
}

// OptionsFromEnv builds run options from the environment, applying defaults.
//
// It returns an error rather than silently falling back, because the fallback
// is the tuning corpus and the thing being named is usually the held-out one.
// NITPICK_EVAL_FIXTURES used to keep whatever it could resolve and ignore the
// rest: a single mistyped name in the six-name line the Makefile documents
// silently measured five of six, and a typo in the ONLY name ran all eight
// tuning fixtures with no warning anywhere. Neither table names its corpus, so
// the result was indistinguishable from the held-out run it claimed to be —
// a silent failure that returns exactly the wrong answer to the one question
// the held-out set exists to answer.
func OptionsFromEnv() (Options, error) {
	opts := Options{
		Models:   DefaultModels(),
		Fixtures: Fixtures(),
		Runs:     1,
		// A review's ceiling has to hold the model timeout times the retry
		// budget: a batch that stalls three times and answers on the fourth
		// has spent forty minutes and produced a review, and a ceiling that
		// cut it off at fifteen would record the loss as the model's.
		Timeout: 45 * time.Minute,
	}

	if raw := strings.TrimSpace(os.Getenv(EnvModels)); raw != "" {
		var models []Model
		for id := range strings.SplitSeq(raw, ",") {
			if id = strings.TrimSpace(id); id != "" {
				models = append(models, ParseModel(id))
			}
		}
		if len(models) > 0 {
			opts.Models = models
		}
	}

	if path := strings.TrimSpace(os.Getenv(EnvEngineLog)); path != "" {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return opts, fmt.Errorf("%s: %w", EnvEngineLog, err)
		}
		opts.Log = slog.New(slog.NewTextHandler(f, &slog.HandlerOptions{Level: slog.LevelWarn}))
	}

	if raw := strings.TrimSpace(os.Getenv(EnvRuns)); raw != "" {
		var n int
		if _, err := fmt.Sscanf(raw, "%d", &n); err == nil && n > 0 {
			opts.Runs = n
		}
	}

	if raw := strings.TrimSpace(os.Getenv(EnvFixtures)); raw != "" {
		// Resolved against BOTH corpora, not against the default list: naming a
		// held-out fixture has to select it, or the held-out set could only be
		// run by editing code. Naming nothing still yields Fixtures() alone, so
		// no tuning run picks up the held-out corpus by accident — spending it
		// takes saying its name, spelled correctly.
		known := map[string]Fixture{}
		for _, f := range EveryFixture() {
			known[f.Name] = f
		}

		var (
			kept    []Fixture
			unknown []string
			seen    = map[string]bool{}
		)
		for name := range strings.SplitSeq(raw, ",") {
			name = strings.TrimSpace(name)
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true

			f, ok := known[name]
			if !ok {
				unknown = append(unknown, name)
				continue
			}
			kept = append(kept, f)
		}

		if len(unknown) > 0 {
			names := make([]string, 0, len(known))
			for _, f := range EveryFixture() {
				names = append(names, f.Name)
			}
			return opts, fmt.Errorf("%s names %d fixture(s) that do not exist: %s (known: %s)",
				EnvFixtures, len(unknown), strings.Join(unknown, ", "), strings.Join(names, ", "))
		}

		opts.Fixtures = kept
	}

	if raw := strings.TrimSpace(os.Getenv(EnvTimeout)); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return opts, fmt.Errorf("%s=%q: %w", EnvTimeout, raw, err)
		}
		if d <= 0 {
			// Zero would disable the bound entirely rather than widen it, and a
			// review that never returns stalls the whole collection silently.
			return opts, fmt.Errorf("%s=%q must be positive", EnvTimeout, raw)
		}
		opts.Timeout = d
	}

	opts.CaptureDir = strings.TrimSpace(os.Getenv(EnvCapture))

	// Before the run, not at report time. Prices() rejects an unreadable or
	// undated override rather than falling back to the shipped table — the right
	// behaviour, but only if it happens while the operator is still watching.
	prices, err := Prices()
	if err != nil {
		return opts, err
	}
	opts.Prices = prices

	return opts, nil
}

// HeldOut reports whether a fixture belongs to the held-out corpus.
//
// Exported so a report can LABEL the corpus it measured. A held-out table and a
// tuning table were textually identical, which meant the one number the
// held-out set exists to produce could not be told apart from a training score
// after the fact — not by a reader, and not by whoever kept the artifact.
func HeldOut(fixture string) bool {
	for _, f := range HeldOutFixtures() {
		if f.Name == fixture {
			return true
		}
	}
	return false
}

// CorpusLabel names the corpus a set of fixtures came from, for a report
// header. A mixed selection is called out as mixed rather than rounded to
// whichever half is larger.
func CorpusLabel(fixtures []Fixture) string {
	var held, tuning, multi, info, callers, slop int
	for _, f := range fixtures {
		switch {
		case HeldOut(f.Name):
			held++
		case MultiFile(f.Name):
			multi++
		case Info(f.Name):
			info++
		case Caller(f.Name):
			callers++
		case Slop(f.Name):
			slop++
		default:
			tuning++
		}
	}

	others := func(except string) int {
		n := 0
		for name, count := range map[string]int{"tuning": tuning, "held": held, "multi": multi, "info": info, "callers": callers, "slop": slop} {
			if name != except {
				n += count
			}
		}
		return n
	}
	switch {
	case tuning+held+multi+info+callers+slop == 0:
		return "EMPTY (no fixtures selected)"
	case others("tuning") == 0:
		return fmt.Sprintf("TUNING corpus (%d fixture(s))", tuning)
	case others("held") == 0:
		return fmt.Sprintf("HELD-OUT corpus (%d fixture(s)) — spent once; a gain measured here is a generalization claim", held)
	case others("multi") == 0:
		return fmt.Sprintf("MULTI-FILE corpus (%d fixture(s)) — the contract is in a file the change does not touch", multi)
	case others("info") == 0:
		return fmt.Sprintf("INFO corpus (%d fixture(s)) — the band no reviewer had located", info)
	case others("callers") == 0:
		return fmt.Sprintf("CALLERS corpus (%d fixture(s)) — the change is the contract, and the file it breaks is one it does not touch", callers)
	case others("slop") == 0:
		return fmt.Sprintf("SLOP corpus (%d fixture(s)) — planted/control pairs for the slop class; needs review.slop on", slop)
	default:
		return fmt.Sprintf("MIXED corpus (%d tuning + %d HELD-OUT + %d multi-file + %d info + %d callers + %d slop fixture(s)) — not a generalization measurement", tuning, held, multi, info, callers, slop)
	}
}

// MultiFile reports whether a fixture belongs to the multi-file corpus.
func MultiFile(fixture string) bool {
	for _, f := range MultiFileFixtures() {
		if f.Name == fixture {
			return true
		}
	}
	return false
}

// buildRepo materializes a fixture as a git repository whose working tree
// contains the change under review.
//
// A real repository and a real `git diff` are used rather than a synthesized
// patch, so the eval exercises the same parsing path production does.
func buildRepo(dir string, f Fixture) error {
	write := func(files map[string]string) error {
		for path, content := range files {
			full := filepath.Join(dir, filepath.FromSlash(path))
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
				return err
			}
		}
		return nil
	}

	git := func(args ...string) error {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=eval", "GIT_AUTHOR_EMAIL=eval@example.com",
			"GIT_COMMITTER_NAME=eval", "GIT_COMMITTER_EMAIL=eval@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, out)
		}
		return nil
	}

	if err := git("init", "-q", "-b", "main"); err != nil {
		return err
	}

	// Incumbent matches a review to an organization through the repository's
	// git remote. With none it warns that "this review will use the free CLI
	// allowance, even if you're signed in" — so every fixture review was billed
	// to that allowance regardless of account tier, which is what exhausted it
	// mid-collection and turned an earlier benchmark into a measurement of the
	// allowance rather than of the reviewer. Nothing is ever pushed; the remote
	// exists only to be read. It is harmless for the model runs, which review
	// the working tree through vcs.Local and never consult a remote.
	if err := git("remote", "add", "origin", incumbentRemote()); err != nil {
		return err
	}

	if err := write(f.Extra); err != nil {
		return err
	}
	if err := write(f.Base); err != nil {
		return err
	}
	if err := git("add", "-A"); err != nil {
		return err
	}
	if err := git("commit", "-qm", "base"); err != nil {
		return err
	}

	// The head state is left uncommitted: that is the working-tree review path,
	// which is what a developer runs locally.
	if err := write(f.Head); err != nil {
		return err
	}

	// Intent-to-add, so a head file that does not exist in base shows up as an
	// addition. `git diff HEAD` does not report untracked files at all, so
	// before this a fixture whose change ADDS a file produced an empty diff:
	// the engine found nothing to review, and the fixture scored as a flawless
	// clean run no matter what the prompt said. It was the held-out SQL
	// migration that surfaced it, and a corpus bug that silently RAISES the
	// score is the worst kind. -N records no content, so the head state is
	// still uncommitted and still reviewed from the working tree.
	return git("add", "-N", ".")
}

// captureProvider records the published review instead of sending it anywhere.
type captureProvider struct {
	*vcs.Local
	review *vcs.Review
}

func (c *captureProvider) PublishReview(_ context.Context, _ vcs.Ref, r vcs.Review) error {
	c.review = &r
	return nil
}

// recordingLLM wraps a client and writes every raw response to disk, so a real
// model's output becomes an offline regression fixture.
type recordingLLM struct {
	inner llms.LLM

	mu        sync.Mutex
	responses []string
}

func (r *recordingLLM) GenerateContent(ctx context.Context, msgs []llms.Message, opts ...llms.CallOption) (*llms.Response, error) {
	resp, err := r.inner.GenerateContent(ctx, msgs, opts...)

	if resp != nil {
		r.mu.Lock()
		r.responses = append(r.responses, resp.Content)
		r.mu.Unlock()
	}

	return resp, err
}

func (r *recordingLLM) Stream(ctx context.Context, msgs []llms.Message, opts ...llms.CallOption) (<-chan llms.StreamChunk, error) {
	return r.inner.Stream(ctx, msgs, opts...)
}

func (r *recordingLLM) Provider() llms.Provider { return r.inner.Provider() }
func (r *recordingLLM) Model() string           { return r.inner.Model() }

func (r *recordingLLM) captured() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.responses...)
}

// RunResult is one review of one fixture by one model.
type RunResult struct {
	Model   string
	Fixture string
	Run     int

	// UsageByModel is the usage of each model a composite run reached for,
	// keyed by model id; nil for a single-model run.
	UsageByModel map[string]TokenUsage

	Report *review.Report
	Review *vcs.Review

	// Responses are the raw model outputs seen during the run.
	Responses []string

	// Usage is what the provider REPORTED this review spent, summed over every
	// call the run made — review, triage, any repair round-trip. It is the
	// measurement a cost figure is allowed to rest on; see TokenUsage for why
	// the estimate internal/bundle already computes is not.
	//
	// It is a LOWER bound, and TokenUsage.Failed names the two reasons. The one
	// that matters here is where the meter is installed: below.
	Usage TokenUsage

	// Scale is the severity vocabulary this run's findings are on, DECLARED
	// here because this is the adapter that produced them. See SeverityScale for
	// why it is declared rather than inferred, and ourSeverityScale for the one
	// configuration question the declaration depends on.
	//
	// IT IS SET EVEN WHEN Err IS, and that is not tidiness. commonScale
	// withdraws a row whose runs disagree, so a failed run carrying no
	// declaration drags an otherwise-declared row to n/a — meaning a provider
	// error would decide which severity cell a reader is shown. The declaration
	// is a fact about the adapter, not about whether this particular attempt
	// reached a provider.
	// TestAFailedRunStillDeclaresItsScale pins it.
	Scale SeverityScale

	Err      error
	Duration time.Duration
}

// Run reviews one fixture with one model, returning everything needed to score
// the outcome.
func Run(ctx context.Context, model Model, f Fixture, runIndex int, opts Options) RunResult {
	return RunWithPersona(ctx, model, f, runIndex, opts, config.DefaultPersona())
}

// RunWithPersona reviews a fixture with an explicit persona, which is how tone
// and nitpick-level variants are compared.
func RunWithPersona(ctx context.Context, model Model, f Fixture, runIndex int, opts Options, persona config.Persona) RunResult {
	started := time.Now()

	// The declaration is made BEFORE anything that can fail, because it depends
	// on nothing that can. ourSeverityScale reads the configuration and the
	// configuration is fully determined by evalConfig; a temp directory that
	// cannot be made says nothing about which vocabulary this adapter publishes
	// on. THE BUG THIS FIXES: it was assigned after the MkdirTemp and buildRepo
	// returns, so a run that died there carried no declaration — and the two
	// halves of one infrastructure failure then published DIFFERENT severity
	// cells. Folded with a good run, a failure carrying the declaration renders
	// SEV as the good run's own triple; a failure carrying none withdraws the
	// whole row to n/a. Which of the two a reader sees depended on where in this
	// function the provider happened to break.
	cfg := evalConfig(model)
	cfg.Persona = persona.Resolve()
	if opts.Tune != nil {
		opts.Tune(cfg)
	}

	out := RunResult{
		Model: model.ID, Fixture: f.Name, Run: runIndex,
		Scale: ourSeverityScale(cfg),
	}

	dir, err := os.MkdirTemp("", "nitpick-eval-")
	if err != nil {
		out.Err = err
		return out
	}
	defer func() { _ = os.RemoveAll(dir) }()

	if err := buildRepo(dir, f); err != nil {
		out.Err = fmt.Errorf("build fixture repo: %w", err)
		return out
	}

	build := opts.buildClient
	if build == nil {
		build = llm.Build
	}

	client, err := build(cfg.Models.Default)
	if err != nil {
		out.Err = fmt.Errorf("build model %s: %w", model.ID, err)
		return out
	}

	// Metering sits under the recorder so both see the same calls: one review
	// makes several (review, triage, and a repair round-trip when a provider
	// answers with unparseable JSON), and every one of them is billed.
	//
	// It also sits OUTSIDE the resilience wrapper llm.Build installed, because
	// that wrapper is what a real review runs behind and this harness measures
	// the shipped path. The price is one call per logical request rather than
	// one per attempt: a retried request contributes only the attempt that
	// succeeded, and the attempts a provider may have billed for before it are
	// invisible. Understated, never overstated — see TokenUsage.Failed.
	meter := MeterClient(client)

	recorder := &recordingLLM{inner: client.LLM}
	client.LLM = recorder

	// A composite run builds its own roles from the route file and meters
	// every client it reaches for, by model, so the row's cost is the sum
	// of each model's usage at that model's own rate.
	var (
		roles  = &llm.Roles{Review: client, Triage: client}
		meters = map[string]*Meter{}
		mmu    sync.Mutex
	)
	if model.Composite() {
		built, err := llm.BuildRoles(cfg)
		if err != nil {
			out.Err = fmt.Errorf("route file %s: %w", model.RouteFile, err)
			out.Duration = time.Since(started)
			return out
		}
		meterInto := func(c *llm.Client) {
			mmu.Lock()
			defer mmu.Unlock()
			m := MeterClient(c)
			meters[c.Spec.Model] = m
		}
		built.Each(meterInto)
		inner := built.Build
		built.Build = func(spec config.ModelSpec) (*llm.Client, error) {
			c, err := inner(spec)
			if err == nil {
				meterInto(c)
			}
			return c, err
		}
		roles = built
	}

	provider := &captureProvider{Local: vcs.NewLocal(dir, io.Discard)}

	engine := &review.Engine{
		Config: cfg,

		// One model in BOTH roles, which is a deliberate limit on what the
		// matrix measures: it ranks reviewers, and giving each contender a
		// different triager would confound the two. The shipped .nitpick.yaml
		// splits the roles, so no number produced here is a measurement of the
		// shipped pairing — in particular a model annotated in DefaultModels as
		// good value was ranked as a REVIEWER and has never been measured
		// triaging another model's findings.
		Roles:    roles,
		Provider: provider,
		Log:      cmpLogger(opts.Log),
	}

	runCtx := ctx
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	report, err := engine.Review(runCtx, vcs.Ref{})

	out.Report = report
	out.Review = provider.review
	out.Responses = recorder.captured()

	// Read after the review, including when it failed: a run that died at
	// triage still paid for the review calls that preceded it, and dropping
	// that spend would make an unreliable model look cheap.
	out.Usage = meter.Usage()
	if model.Composite() {
		out.UsageByModel = map[string]TokenUsage{}
		var all TokenUsage
		mmu.Lock()
		for name, m := range meters {
			u := m.Usage()
			out.UsageByModel[name] = u
			all.PerCall = append(all.PerCall, u.PerCall...)
			all.Unreported += u.Unreported
			all.Failed += u.Failed
		}
		mmu.Unlock()
		out.Usage = all
	}

	out.Err = err
	out.Duration = time.Since(started)

	return out
}

// routeFile is the shape of a route file: the config's models block.
type routeFile struct {
	Models config.Models `yaml:"models"`
}

// applyRouteFile replaces cfg's models with the file's, giving every spec
// the harness's baseline where the file is silent (temperature 0, the
// default timeout, auto structured output, openrouter) so a route file names
// only what it decides.
func applyRouteFile(cfg *config.Config, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var rf routeFile
	if err := yaml.Unmarshal(data, &rf); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	baseline := cfg.Models.Default
	fill := func(spec *config.ModelSpec) {
		if spec == nil {
			return
		}
		if spec.Provider == "" {
			spec.Provider = baseline.Provider
		}
		if spec.Temperature == nil {
			spec.Temperature = baseline.Temperature
		}
		if spec.Timeout == 0 {
			spec.Timeout = baseline.Timeout
		}
		if spec.StructuredOutput == "" {
			spec.StructuredOutput = baseline.StructuredOutput
		}
	}
	fill(&rf.Models.Default)
	if rf.Models.Default.Model == "" {
		return fmt.Errorf("%s: models.default.model is required", path)
	}
	for _, spec := range []*config.ModelSpec{rf.Models.Review, rf.Models.Triage, rf.Models.Validate, rf.Models.Router} {
		if spec != nil && spec.Provider == "" && spec.Model == "" {
			continue
		}
	}
	for i := range rf.Models.Routes {
		for j := range rf.Models.Routes[i].Ensemble {
			fill(&rf.Models.Routes[i].Ensemble[j])
		}
	}
	for i := range rf.Models.Ensemble {
		fill(&rf.Models.Ensemble[i])
	}
	cfg.Models = rf.Models
	return nil
}

// pinList is the providers list for a pin, nil for none.
func pinList(pin string) []string {
	if pin == "" {
		return nil
	}
	return []string{pin}
}

func cmpLogger(l *slog.Logger) *slog.Logger {
	if l != nil {
		return l
	}
	return slog.New(slog.DiscardHandler)
}

// evalConfig builds a configuration pointed at OpenRouter.
//
// It is constructed in Go rather than loaded from a file so the harness is not
// subject to the untrusted-config rules that (correctly) strip base_url and
// api_key_env from a repository's own .nitpick.yaml.
func evalConfig(model Model) *config.Config {
	cfg := config.Defaults()

	cfg.Models.Default = config.ModelSpec{
		// The shipped provider, not an equivalent: it carries the endpoint and
		// it resolves OPENROUTER_API_KEY then LLM_API_KEY exactly as a real
		// review does. Naming base_url + api_key_env here made `make eval` fail
		// for anyone who only exported LLM_API_KEY, while `nitpick review`
		// succeeded for them — a difference between the harness and the thing
		// it measures.
		Provider:  model.provider(),
		Model:     model.modelName(),
		Providers: pinList(model.Pin),

		// Reviews should be reproducible; run-to-run variance is measured
		// separately and deliberately, not left to the provider default.
		Temperature: floatPtr(0),
		// No max_tokens: the model's own output maximum applies, for
		// config.Defaults' reasons. The battery's own losses on
		// glm-5.3-flash were the old 8k cap and the three-minute timeout.
		Timeout: 10 * time.Minute,

		// Auto exercises the real negotiation: schema first, JSON fallback for
		// providers that reject it. That path is the point of the matrix.
		StructuredOutput: config.StructuredAuto,
	}

	if model.RouteFile != "" {
		// Errors surface when the run builds its clients; evalConfig has no
		// error return and a bad file must not be silently a plain run.
		if err := applyRouteFile(cfg, model.RouteFile); err != nil {
			cfg.Models.Default.Model = "route-file-error: " + err.Error()
		}
	}

	// Linters are deterministic and separately tested; excluding them keeps the
	// score a measurement of the prompt.
	cfg.Linters.Mode = config.LinterOff

	// On unless the run asks, matching the shipped default; the switch is
	// how the layer's own contribution gets measured.
	switch strings.ToLower(strings.TrimSpace(os.Getenv(EnvModelNotes))) {
	case "0", "false", "off":
		off := false
		cfg.Review.ModelNotes = &off
	}

	// Both directions off unless the run asks, whatever the shipped default
	// is: the harness is how the default gets decided, and the diff-only arm
	// has to stay diff-only. See docs/findings.md.
	switch strings.ToLower(strings.TrimSpace(os.Getenv(EnvRelatedContext))) {
	case "", "0", "false", "off":
		cfg.Review.RelatedContext = false
		cfg.Review.RelatedContextCallers = false
	default:
		cfg.Review.RelatedContext = true
		cfg.Review.RelatedContextCallers = true
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv(EnvValidation))) {
	case "", "0", "false", "off":
	default:
		cfg.Validation.Enabled = true
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv(EnvSlop))) {
	case "", "0", "false", "off":
	default:
		cfg.Review.Slop = true
	}

	// Report everything the model says so precision can actually be measured.
	cfg.Review.MinSeverity = config.SeverityNit
	cfg.Review.FailOn = config.SeverityNone

	return cfg
}

// ourSeverityScale declares the severity vocabulary a run under this
// configuration publishes on.
//
// It is DERIVED FROM THE CONFIGURATION RATHER THAN ASSERTED, and the one
// question it asks is the one that can make the assertion false. review.Engine
// writes our five levels for a model's own findings, but a report is the union
// of the model's findings and the analyzers', and internal/linters' mapSeverity
// folds HIGH onto our error and MEDIUM onto warning — a translation between
// vocabularies, which is the exact shape of the first retraction this package
// made. Its codomain now covers all five of our levels, so a report could carry
// an analyzer's word and our word spelled identically, and that makes the risk
// worse rather than better: an analyzer's "critical" is a rule author's
// judgement in the analyzer's own scale, not a severity written on ours, and
// linters.max_severity may have moved it after the fact. Those findings are
// absent today only
// because evalConfig turns linters off. Deriving the declaration means turning
// them back on WITHDRAWS the severity comparison instead of quietly publishing
// analyzer levels at our resolution; asserting it would have published them.
// TestTurningLintersOnWithdrawsTheSeverityComparison pins that.
func ourSeverityScale(cfg *config.Config) SeverityScale {
	if cfg == nil || cfg.Linters.Mode != config.LinterOff {
		return UndeclaredSeverityScale
	}
	return OurSeverityScale
}

func floatPtr(v float64) *float64 { return &v }

// CaptureResponses writes raw model output to disk for later offline use.
func CaptureResponses(dir, model, fixture string, run int, responses []string) error {
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	safe := strings.NewReplacer("/", "_", ":", "_").Replace(model)

	for i, body := range responses {
		name := fmt.Sprintf("%s__%s__run%d__%d.txt", safe, fixture, run, i)
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			return err
		}
	}

	return nil
}
