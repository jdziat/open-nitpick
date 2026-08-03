package evals

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	llms "github.com/nocturnium/llm-go-sdk"

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

	// EnvCapture names a directory to write every raw model response into.
	// Those responses become offline regression fixtures.
	EnvCapture = "NITPICK_EVAL_CAPTURE"
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
}

// Options configure a run.
type Options struct {
	Models   []Model
	Fixtures []Fixture
	Runs     int

	// CaptureDir, when set, receives every raw model response.
	CaptureDir string

	// Timeout bounds a single review.
	Timeout time.Duration
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
		Timeout:  4 * time.Minute,
	}

	if raw := strings.TrimSpace(os.Getenv(EnvModels)); raw != "" {
		var models []Model
		for id := range strings.SplitSeq(raw, ",") {
			if id = strings.TrimSpace(id); id != "" {
				models = append(models, Model{ID: id, Kind: "custom"})
			}
		}
		if len(models) > 0 {
			opts.Models = models
		}
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
		for _, f := range AllFixtures() {
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
			for _, f := range AllFixtures() {
				names = append(names, f.Name)
			}
			return opts, fmt.Errorf("%s names %d fixture(s) that do not exist: %s (known: %s)",
				EnvFixtures, len(unknown), strings.Join(unknown, ", "), strings.Join(names, ", "))
		}

		opts.Fixtures = kept
	}

	opts.CaptureDir = strings.TrimSpace(os.Getenv(EnvCapture))

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
	var held, tuning int
	for _, f := range fixtures {
		if HeldOut(f.Name) {
			held++
			continue
		}
		tuning++
	}

	switch {
	case held == 0 && tuning == 0:
		return "EMPTY (no fixtures selected)"
	case held == 0:
		return fmt.Sprintf("TUNING corpus (%d fixture(s))", tuning)
	case tuning == 0:
		return fmt.Sprintf("HELD-OUT corpus (%d fixture(s)) — spent once; a gain measured here is a generalization claim", held)
	default:
		return fmt.Sprintf("MIXED corpus (%d tuning + %d HELD-OUT fixture(s)) — not a generalization measurement", tuning, held)
	}
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

	Report *review.Report
	Review *vcs.Review

	// Responses are the raw model outputs seen during the run.
	Responses []string

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
	out := RunResult{Model: model.ID, Fixture: f.Name, Run: runIndex}

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

	cfg := evalConfig(model)
	cfg.Persona = persona.Resolve()

	client, err := llm.Build(cfg.Models.Default)
	if err != nil {
		out.Err = fmt.Errorf("build model %s: %w", model.ID, err)
		return out
	}

	recorder := &recordingLLM{inner: client.LLM}
	client.LLM = recorder

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
		Roles:    &llm.Roles{Review: client, Triage: client},
		Provider: provider,
		Log:      slog.New(slog.DiscardHandler),
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
	out.Err = err
	out.Duration = time.Since(started)

	return out
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
		Provider: llm.ProviderOpenRouter,
		Model:    model.ID,

		// Reviews should be reproducible; run-to-run variance is measured
		// separately and deliberately, not left to the provider default.
		Temperature: floatPtr(0),
		MaxTokens:   8192,
		Timeout:     3 * time.Minute,

		// Auto exercises the real negotiation: schema first, JSON fallback for
		// providers that reject it. That path is the point of the matrix.
		StructuredOutput: config.StructuredAuto,
	}

	// Linters are deterministic and separately tested; excluding them keeps the
	// score a measurement of the prompt.
	cfg.Linters.Mode = config.LinterOff

	// Report everything the model says so precision can actually be measured.
	cfg.Review.MinSeverity = config.SeverityNit
	cfg.Review.FailOn = config.SeverityNone

	return cfg
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
