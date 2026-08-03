package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	llms "github.com/nocturnium/llm-go-sdk"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/linters"
	"github.com/jdziat/open-nitpick/internal/llm"
	"github.com/jdziat/open-nitpick/internal/prompt"
	"github.com/jdziat/open-nitpick/internal/review"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// reviewFlags holds the review command's options.
type reviewFlags struct {
	repo        string
	configPath  string
	base        string
	head        string
	instruction string
	failOn      string
	dryRun      bool
	verbose     bool
	logFormat   string
	noLinters   bool

	// Pull request selection. When unset, these are read from the GitHub
	// Actions environment.
	owner    string
	repoName string
	pr       int
}

func runReview(ctx context.Context, args []string) error {
	var f reviewFlags

	fs := flag.NewFlagSet("review", flag.ContinueOnError)
	fs.StringVar(&f.repo, "repo", ".", "repository root")
	fs.StringVar(&f.configPath, "config", "", "path to .nitpick.yaml (default: <repo>/.nitpick.yaml)")
	fs.StringVar(&f.base, "base", "", "base revision (default: review uncommitted changes)")
	fs.StringVar(&f.head, "head", "", "head revision (default: the working tree)")
	fs.StringVar(&f.instruction, "instruction", "", "extra instruction for this run only")
	fs.StringVar(&f.failOn, "fail-on", "", "override review.fail_on (nit, info, warning, error, critical, none)")
	fs.StringVar(&f.owner, "owner", "", "GitHub repository owner")
	fs.StringVar(&f.repoName, "repo-name", "", "GitHub repository name")
	fs.IntVar(&f.pr, "pr", 0, "pull request number to review and comment on")
	fs.BoolVar(&f.dryRun, "dry-run", false, "print the review instead of publishing it")
	fs.BoolVar(&f.noLinters, "no-linters", false, "skip linters even when configured")
	fs.BoolVar(&f.verbose, "v", false, "verbose logging")
	fs.StringVar(&f.logFormat, "log-format", "text", "log format: text or json")

	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: nitpick review [flags]\n\nReviews the working tree by default.\n\nFlags:")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return err
	}

	// Go's flag package stops at the first non-flag argument and leaves the
	// rest unread. Silently ignoring them means a misplaced flag — or an
	// action.yml that builds its argv wrongly — changes nothing and says
	// nothing, which is how -fail-on went missing in CI.
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q (flags must precede positional arguments)", fs.Arg(0))
	}

	repo, err := filepath.Abs(f.repo)
	if err != nil {
		return fmt.Errorf("resolve repo path: %w", err)
	}

	cfg, err := loadConfig(repo, f.configPath)
	if err != nil {
		return err
	}

	if f.failOn != "" {
		sev := config.Severity(f.failOn)
		if !sev.Valid() {
			return fmt.Errorf("invalid -fail-on %q", f.failOn)
		}
		cfg.Review.FailOn = sev
	}

	log := newLogger(f.verbose, f.logFormat)

	// Endpoint and credential keys are ignored when the config file is not
	// trusted to supply them. Saying so matters: an ignored base_url that
	// nobody mentions looks exactly like a bug.
	if len(cfg.Dropped) > 0 {
		log.Warn("ignored endpoint settings from an untrusted config file",
			"keys", strings.Join(cfg.Dropped, ", "),
			"hint", "set "+config.EnvTrustConfigEndpoints+"=1 if you control this file")
	}

	roles, err := llm.BuildRoles(cfg)
	if err != nil {
		return err
	}
	log.Info("models ready", "review", roles.Review.String(), "triage", roles.Triage.String())

	provider, ref, err := selectProvider(&f, repo)
	if err != nil {
		return err
	}
	log.Info("reviewing", "provider", provider.Name(), "ref", ref.String())

	engine := &review.Engine{
		Config:      cfg,
		Roles:       roles,
		Provider:    provider,
		Log:         log,
		Instruction: f.instruction,
	}

	if !f.noLinters && cfg.Linters.Mode != config.LinterOff {
		engine.Linters = linters.New(repo, cfg, log)
	}

	report, err := engine.Review(ctx, ref)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "\nReviewed %d file(s): %s\n", report.Plan.Files(), report.Counts)

	if report.Failed(cfg.Review.FailOn) {
		return errFindings
	}
	return nil
}

// selectProvider chooses where the review comes from and goes to.
//
// GitHub is used when a pull request is identified explicitly or by the Actions
// environment; otherwise the review runs locally and prints to stdout. Dry-run
// forces the local path so a misconfigured CI job cannot post to a real pull
// request while someone is testing.
func selectProvider(f *reviewFlags, repo string) (vcs.Provider, vcs.Ref, error) {
	local := vcs.NewLocal(repo, os.Stdout)
	localRef := vcs.Ref{Base: f.base, Head: f.head}

	ref, wantsPR, err := resolvePullRequest(f)
	if err != nil {
		return nil, vcs.Ref{}, err
	}

	// Dry-run suppresses publishing, not the source. Reviewing the working
	// tree when the user named a pull request would produce a real-looking
	// review of the wrong code.
	if f.dryRun {
		if wantsPR {
			gh, err := githubProvider()
			if err != nil {
				return nil, vcs.Ref{}, err
			}
			return &dryRunProvider{source: gh, out: os.Stdout}, ref, nil
		}
		return local, localRef, nil
	}

	if !wantsPR {
		return local, localRef, nil
	}

	gh, err := githubProvider()
	if err != nil {
		return nil, vcs.Ref{}, err
	}
	return gh, ref, nil
}

// githubProvider builds the GitHub provider from the environment.
func githubProvider() (*vcs.GitHub, error) {
	token := firstNonEmpty(os.Getenv("NITPICK_GITHUB_TOKEN"), os.Getenv("GITHUB_TOKEN"))
	if token == "" {
		return nil, errors.New("reviewing a pull request requires GITHUB_TOKEN")
	}

	return vcs.NewGitHub(vcs.GitHubOptions{
		Token:   token,
		BaseURL: os.Getenv("GITHUB_API_URL"),
	})
}

// dryRunProvider reads from a real forge but prints the review instead of
// publishing it, so --dry-run reviews the code the user actually named.
type dryRunProvider struct {
	source vcs.Provider
	out    io.Writer
}

func (d *dryRunProvider) Name() string { return d.source.Name() + " (dry run)" }

func (d *dryRunProvider) PullRequest(ctx context.Context, ref vcs.Ref) (*vcs.PullRequest, error) {
	return d.source.PullRequest(ctx, ref)
}

func (d *dryRunProvider) Diff(ctx context.Context, ref vcs.Ref) ([]byte, error) {
	return d.source.Diff(ctx, ref)
}

func (d *dryRunProvider) FileContent(ctx context.Context, ref vcs.Ref, path string) ([]byte, error) {
	return d.source.FileContent(ctx, ref, path)
}

func (d *dryRunProvider) PublishReview(_ context.Context, ref vcs.Ref, review vcs.Review) error {
	var b strings.Builder

	fmt.Fprintf(&b, "--- dry run: would publish to %s ---\n\n", ref)

	if review.Summary != "" {
		fmt.Fprintf(&b, "%s\n\n", review.Summary)
	}
	for _, c := range review.Comments {
		fmt.Fprintf(&b, "%s:%d\n", c.Path, c.Line)
		for line := range strings.SplitSeq(strings.TrimRight(c.Body, "\n"), "\n") {
			fmt.Fprintf(&b, "  %s\n", line)
		}
		b.WriteByte('\n')
	}

	_, err := io.WriteString(d.out, b.String())
	return err
}

// resolvePullRequest determines which pull request to review, from flags first
// and the Actions environment second.
//
// Partially-specified intent is an error rather than a silent fall-through to
// a local review: `-pr 42` that quietly reviews the working tree instead is
// worse than a failure, because the output looks like a real review of the
// wrong thing.
func resolvePullRequest(f *reviewFlags) (vcs.Ref, bool, error) {
	if f.pr > 0 && f.owner != "" && f.repoName != "" {
		return vcs.Ref{Owner: f.owner, Repo: f.repoName, Number: f.pr}, true, nil
	}

	env, ok := vcs.RefFromEnv(nil)
	if ok {
		if f.pr > 0 {
			env.Number = f.pr
		}
		return env, true, nil
	}

	// Nothing in the environment; decide whether the user asked for a pull
	// request review at all.
	if f.pr > 0 || f.owner != "" || f.repoName != "" {
		var missing []string
		if f.owner == "" {
			missing = append(missing, "-owner")
		}
		if f.repoName == "" {
			missing = append(missing, "-repo-name")
		}
		if f.pr <= 0 {
			missing = append(missing, "-pr")
		}
		return vcs.Ref{}, false, fmt.Errorf(
			"reviewing a pull request needs %s (or run inside GitHub Actions)", strings.Join(missing, ", "))
	}

	return vcs.Ref{}, false, nil
}

// firstNonEmpty returns the first non-empty string.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// loadConfig loads configuration from an explicit path or the repository root.
func loadConfig(repo, explicit string) (*config.Config, error) {
	if explicit != "" {
		return config.LoadFile(explicit)
	}
	return config.Load(repo)
}

func runExplainConfig(args []string) error {
	var (
		repo       string
		configPath string
		forPath    string
	)

	fs := flag.NewFlagSet("explain-config", flag.ContinueOnError)
	fs.StringVar(&repo, "repo", ".", "repository root")
	fs.StringVar(&configPath, "config", "", "path to .nitpick.yaml")
	fs.StringVar(&forPath, "path", "", "show the instructions that apply to this file path")

	if err := fs.Parse(args); err != nil {
		return err
	}

	root, err := filepath.Abs(repo)
	if err != nil {
		return err
	}

	cfg, err := loadConfig(root, configPath)
	if err != nil {
		return err
	}

	source := cfg.Source
	if source == "" {
		source = "(defaults and environment; no config file found)"
	}

	fmt.Println("Config source:", source)
	fmt.Println()

	// The keys sanitize discarded, named here as well as in the review log.
	// This command exists to answer "what does my config actually resolve to",
	// and a key that was silently thrown away is the single most surprising
	// answer it can give — an operator debugging "why is it talking to the
	// wrong endpoint" reads this, not a CI log. Printing nothing when nothing
	// was dropped is also what makes the empty case evidence rather than the
	// same output any config would produce.
	if len(cfg.Dropped) > 0 {
		fmt.Println("Ignored (untrusted config; set NITPICK_TRUST_CONFIG_ENDPOINTS=1 where you control the file):")
		for _, key := range cfg.Dropped {
			fmt.Printf("  %s\n", key)
		}
		fmt.Println()
	}

	printModel := func(role config.Role) {
		spec := cfg.Models.ResolveModel(role)
		fmt.Printf("  %-8s %s/%s", role, spec.Provider, spec.Model)
		if spec.BaseURL != "" {
			fmt.Printf(" @ %s", spec.BaseURL)
		}
		fmt.Printf("  [structured output: %s]\n", cmp(spec.StructuredOutput, config.StructuredAuto))
	}

	fmt.Println("Models:")
	printModel(config.RoleReview)
	printModel(config.RoleTriage)

	fmt.Printf("\nGating:\n  fail_on      %s\n  min_severity %s\n", cfg.Review.FailOn, cfg.Review.MinSeverity)
	fmt.Printf("\nBudget:\n  max_files              %d\n  max_files_per_request  %d\n  token_budget_per_req   %d\n  concurrency            %d\n",
		cfg.Review.MaxFiles, cfg.Review.MaxFilesPerRequest, cfg.Review.TokenBudgetPerRequest, cfg.Review.Concurrency)

	fmt.Printf("\nLinters (%s): %s\n", cfg.Linters.Mode, strings.Join(cfg.Linters.Enabled, ", "))
	fmt.Printf("\nPersona: %s\n", prompt.Describe(cfg.Persona))

	if forPath != "" {
		fmt.Printf("\nFor %s:\n", forPath)
		if cfg.Ignored(forPath) {
			fmt.Println("  ignored by review.ignore")
		}
		instructions := cfg.InstructionsFor(forPath)
		if len(instructions) == 0 {
			fmt.Println("  no path-scoped instructions match")
		}
		for _, ins := range instructions {
			fmt.Printf("  - %s\n", ins)
		}
	}

	// Printing the exact prompt is what makes prompt tuning possible without
	// spending tokens to discover what was actually sent.
	p, err := prompt.Build(prompt.NameReview, prompt.Options{
		PersonaText: prompt.Persona(cfg.Persona),
		Repository:  "(pull request title and body are inserted here at review time, fenced as untrusted)",
	})
	if err != nil {
		return err
	}
	fmt.Printf("\n===== review prompt =====\n\n%s", p.Explain())

	return nil
}

func runProviders() error {
	fmt.Println("Providers available for models.*.provider:")
	for _, name := range llms.RegisteredProviders() {
		fmt.Println("  " + name)
	}
	fmt.Println("\nSet base_url to point any OpenAI-compatible endpoint at a provider,")
	fmt.Println("or use ollama / llamacpp for local models.")
	return nil
}

// cmp returns value, or fallback when value is empty.
func cmp(value, fallback config.StructuredMode) config.StructuredMode {
	if value == "" {
		return fallback
	}
	return value
}
