package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	llms "github.com/nocturnium/llm-go-sdk/v6"

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
	skipDraft   bool
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
	fs.BoolVar(&f.skipDraft, "skip-draft", false, "do nothing when the pull request is a draft")
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

	// A config file that is not there is the one outcome this command used to
	// produce no output for at all: LoadFile treats a missing file as "use
	// defaults", so a mistyped -config path reviewed the repository under
	// settings its maintainers never chose and nothing on stderr said so.
	if cfg.Missing != "" {
		msg := "no configuration file; reviewing under built-in defaults and the environment"
		if f.configPath != "" {
			log.Warn(msg, "looked_for", cfg.Missing, "hint", "-config names a file that is not there")
		} else {
			log.Info(msg, "looked_for", cfg.Missing)
		}
	}

	provider, ref, err := selectProvider(&f, repo)
	if err != nil {
		return err
	}
	log.Info("reviewing", "provider", provider.Name(), "ref", ref.String())

	actions := actionsFromEnv()

	if f.skipDraft && ref.Number > 0 {
		pr, err := provider.PullRequest(ctx, ref)
		if err != nil {
			return err
		}
		if pr.Draft {
			fmt.Fprintln(os.Stderr, "Draft pull request; not reviewed (-skip-draft).")
			actions.setOutputs(resultSkipped, nil)
			actions.writeSummary(resultSkipped, nil, nil, ref, "Draft pull request; not reviewed. Mark it ready for review to have it reviewed.")
			return nil
		}
	}

	report, err := newEngine(&f, repo, cfg, provider, log).Review(ctx, ref)
	var note string
	switch {
	case err == nil:
	case errors.Is(err, review.ErrPublish) && report != nil:
		// The review happened; only the delivery failed. Print it where the
		// log can show it and carry on to the gate, so a fork's pull request
		// is still reviewed and still gated, and the failure to post is a
		// warning rather than a broken run.
		reason := "the review could not be published"
		if errors.Is(err, vcs.ErrForbidden) {
			reason = "the token may not post reviews on this pull request (a pull request from a fork under the default GITHUB_TOKEN is read-only)"
		}
		note = reason + "; the review is below instead."
		log.Warn("could not publish the review; printing it instead", "error", err)
		if actions.active() {
			fmt.Printf("::warning::open-nitpick: %s\n", reason)
		}
		_ = (&dryRunProvider{source: provider, out: os.Stdout}).PublishReview(ctx, ref,
			review.Render(report, report.Files, cfg))
	default:
		actions.setOutputs(resultError, nil)
		return err
	}

	fmt.Fprintf(os.Stderr, "\nReviewed %d file(s): %s\n", report.Plan.Files(), report.Counts)
	printPolicy(report)
	printLinters(report)
	printOverruled(report)

	result := resultFor(report, gate(report, f.failOn, cfg))
	rendered := review.Render(report, report.Files, cfg)
	actions.setOutputs(result, report)
	actions.writeSummary(result, report, &rendered, ref, note)

	if result == resultFindings {
		return errFindings
	}
	return nil
}

// newEngine wires the review engine this command drives.
//
// It is a function rather than a literal inside runReview because the wiring IS
// the defense: an engine without Policy reviews a config-editing change under
// the configuration that change wrote, and one without Models lets that change
// pick the model that reviews it. Both are optional on the engine — an offline
// driver has no base revision to resolve against — so neither omission is a
// build error, and a test can only pin them by constructing what the command
// constructs.
func newEngine(f *reviewFlags, repo string, cfg *config.Config, provider vcs.Provider, log *slog.Logger) *review.Engine {
	engine := &review.Engine{
		Config:   cfg,
		Provider: provider,
		Log:      log,

		// A change may not supply the policy it is reviewed under. Wired here
		// rather than defaulted inside the engine because only this layer knows
		// the checkout the diff's paths are relative to and which forge can name
		// the base revision.
		Policy: &config.BasePolicy{RepoRoot: repo, Loaded: cfg, Provider: provider},

		// Built from the policy the engine resolved, never from the file on
		// disk: models.* names the model, its temperature and its token ceiling,
		// so clients built here from cfg would let a change that edits
		// .nitpick.yaml still choose what reviews it.
		Models: func(policy *config.Config) (*llm.Roles, error) { return llm.BuildRoles(policy) },

		Instruction: f.instruction,
	}

	// Built per review from the resolved policy for the same reason. The
	// analyzers read review.ignore themselves, so a change that edits
	// .nitpick.yaml would otherwise silence them on exactly the paths it named
	// even though the model no longer honors that file.
	if !f.noLinters {
		engine.Linters = func(policy *config.Config) review.LinterRunner {
			if policy.Linters.Mode == config.LinterOff {
				return nil
			}
			return linters.New(repo, policy, log)
		}
	}

	return engine
}

// gate returns the severity that decides this run's exit status.
//
// It cannot be read from the loaded configuration. When the change under review
// edits .nitpick.yaml the engine reviews under a policy resolved from a revision
// the change cannot write, and taking fail_on from the change's own file here
// would hand back the one knob that decides whether CI goes red — after every
// other knob had already been taken away.
//
// The -fail-on flag still outranks both, because it comes from the operator's
// command line rather than from the change.
func gate(report *review.Report, flag string, cfg *config.Config) config.Severity {
	if flag != "" {
		return config.Severity(flag)
	}
	if policy := report.Policy.Config; policy != nil {
		return policy.Review.FailOn
	}
	return cfg.Review.FailOn
}

// printPolicy reports that the change's own configuration was set aside.
//
// It is on stderr as well as in the published summary because the local and
// dry-run paths are where a contributor checks what their config change does,
// and a run that silently reviewed under something else looks like the config
// simply had no effect.
func printPolicy(report *review.Report) {
	if !report.Policy.Replaced {
		return
	}

	fmt.Fprintf(os.Stderr,
		"This change edits %s, so the configuration in it was not applied; reviewed under %s.\n",
		report.Policy.Modified, report.Policy.Source())

	// The Dropped reported above resolution belongs to the file that was set
	// aside. These are the keys scrubbed from the policy that actually ran, and
	// they were being discarded unread — so an operator whose base_url stopped
	// applying was told about the wrong file's keys, or about none at all.
	if policy := report.Policy.Config; policy != nil && len(policy.Dropped) > 0 {
		fmt.Fprintf(os.Stderr,
			"Ignored endpoint settings from that policy: %s (set %s=1 where you control the file).\n",
			strings.Join(policy.Dropped, ", "), config.EnvTrustConfigEndpoints)
	}
}

// printLinters reports how each deterministic analyzer was configured, and
// which of them did not run.
//
// It sits beside printPolicy because it says the same kind of thing: the review
// did not use configuration that lives in the repository. .nitpick.yaml
// substitution has been announced here since it was introduced; analyzer
// isolation announced nothing, so a run where eslint and semgrep never
// executed, and golangci-lint ignored the repository's own .golangci.yml,
// looked exactly like a run where all four were clean.
//
// It is NOT the disclosure, though it was briefly the only one. An operator
// running this locally reads stderr; the person who has to know that a review
// covered less than it looks like is reading the pull request, and
// review.linterNotice is what reaches them.
//
// Everything is printed, not only the degradations. An analyzer reported as
// having run isolated is the fact that the repository's lint settings did not
// apply, and a reader who sees a shorter list next run has no way to tell which
// line went missing.
func printLinters(report *review.Report) {
	if len(report.Linters) == 0 {
		return
	}

	for _, s := range report.Linters {
		fmt.Fprintf(os.Stderr, "Analyzer %s %s: %s.\n", s.Linter, s.Outcome, s.State)
	}
}

// printOverruled reports the findings a domain expert kept off the pull
// request.
//
// The counts line above cannot show them: they were removed before it was
// taken. Without this, a run whose only real finding an expert overruled prints
// exactly what a clean run prints, and the operator deciding whether validation
// is worth its recall cost has no way to see what it cost.
func printOverruled(report *review.Report) {
	if len(report.Overruled) == 0 {
		return
	}

	fmt.Fprintf(os.Stderr, "%d finding(s) withheld after a domain expert disagreed:\n", len(report.Overruled))
	for _, r := range report.Overruled {
		if r.Revised != "" {
			fmt.Fprintf(os.Stderr, "  %s:%d %s — %s re-rated %s → %s: %s\n",
				r.Finding.Path, r.Finding.Line, r.Finding.Title, r.Expert,
				r.Finding.Sev(), r.Revised, r.Reason)
			continue
		}
		fmt.Fprintf(os.Stderr, "  %s:%d %s — %s: %s\n",
			r.Finding.Path, r.Finding.Line, r.Finding.Title, r.Expert, r.Reason)
	}
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
			gh, err := githubProvider(repo)
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

	gh, err := githubProvider(repo)
	if err != nil {
		return nil, vcs.Ref{}, err
	}
	return gh, ref, nil
}

// githubProvider builds the GitHub provider from the environment.
func githubProvider(repo string) (*vcs.GitHub, error) {
	token := firstNonEmpty(os.Getenv("NITPICK_GITHUB_TOKEN"), os.Getenv("GITHUB_TOKEN"))
	if token == "" {
		return nil, errors.New("reviewing a pull request requires GITHUB_TOKEN")
	}

	gh, err := vcs.NewGitHub(vcs.GitHubOptions{
		Token:   token,
		BaseURL: os.Getenv("GITHUB_API_URL"),
	})
	if err != nil {
		return nil, err
	}
	// The checkout the command runs in, for a diff the API refuses as too
	// large; a directory that is not a clone of the pull request's repository
	// fails that fallback loudly rather than silently.
	if _, err := os.Stat(filepath.Join(repo, ".git")); err == nil {
		gh.Checkout = repo
	}
	return gh, nil
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

// BaseRevision forwards to the wrapped forge so a dry run resolves policy the
// way the real run will.
//
// vcs.BaseRevision asks for the interface rather than a method on Provider
// precisely so a wrapper that cannot answer is forced to say so — but this one
// can answer, and staying silent would push every config-editing change onto the
// defaults fallback under -dry-run alone. A dry run that previews a different
// review from the one that will be published is worse than no preview.
func (d *dryRunProvider) BaseRevision(ctx context.Context, ref vcs.Ref) (string, error) {
	resolver, ok := d.source.(vcs.BaseResolver)
	if !ok {
		return "", fmt.Errorf("%s: %w", d.Name(), vcs.ErrNoBaseRevision)
	}
	return resolver.BaseRevision(ctx, ref)
}

// PriorReview and ChangedSince forward for BaseRevision's reason: a dry run
// has to preview the incremental review the real run would make, not a full
// one that will never be published.
func (d *dryRunProvider) PriorReview(ctx context.Context, ref vcs.Ref) (*vcs.PriorReview, error) {
	reader, ok := d.source.(vcs.PriorReviewer)
	if !ok {
		return nil, fmt.Errorf("%s: cannot read earlier reviews", d.Name())
	}
	return reader.PriorReview(ctx, ref)
}

func (d *dryRunProvider) ChangedSince(ctx context.Context, ref vcs.Ref, since string) ([]string, bool, error) {
	differ, ok := d.source.(vcs.IncrementalDiffer)
	if !ok {
		return nil, false, nil
	}
	return differ.ChangedSince(ctx, ref, since)
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

	return explainConfig(os.Stdout, repo, configPath, forPath)
}

// explainConfig writes the resolved configuration to w. Shared by the
// command and the MCP server.
func explainConfig(w io.Writer, repo, configPath, forPath string) error {
	var b strings.Builder
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
		source = fmt.Sprintf("(defaults and environment; no config file at %s)", cfg.Missing)
	}

	b.WriteString(fmt.Sprintln("Config source:", source))

	// Where the file is and which policy applies are different questions, and
	// they have different answers for exactly one change: the one that edits
	// this file. An operator asking this command what their configuration
	// resolves to gets a misleading answer without this line — they read the
	// resolved ignore list here and then watch a review not honor it.
	//
	// Which answer is right turns on whether the change under review can reach
	// the file at all, so the resolver's own containment rule decides it rather
	// than a paragraph printed unconditionally. Stating the substitution for a
	// -config outside the repository was exactly backwards, and that file is the
	// one the defaults-fallback failure recommends.
	switch rel, inRepo := cfg.RepoRelative(root); {
	case inRepo:
		b.WriteString(fmt.Sprintf("Policy source:  this file, unless the change under review edits it.\n"+
			"                A change may not supply the policy it is reviewed under, so a change\n"+
			"                that edits %s is reviewed under the version of it at the base\n"+
			"                revision — or under built-in defaults when none can be read. Defaults\n"+
			"                name no model, so that last fallback needs %s and %s set.\n",
			rel, config.EnvProvider, config.EnvModel))
	case cfg.Source != "":
		b.WriteString(fmt.Sprintf("Policy source:  this file, always. It is outside %s, so no change under\n"+
			"                review can edit it and nothing substitutes it away.\n", root))
	}
	b.WriteString("\n")

	// The keys sanitize discarded, named here as well as in the review log.
	// This command exists to answer "what does my config actually resolve to",
	// and a key that was silently thrown away is the single most surprising
	// answer it can give — an operator debugging "why is it talking to the
	// wrong endpoint" reads this, not a CI log. Printing nothing when nothing
	// was dropped is also what makes the empty case evidence rather than the
	// same output any config would produce.
	if len(cfg.Dropped) > 0 {
		b.WriteString(fmt.Sprintln("Ignored (untrusted config; set NITPICK_TRUST_CONFIG_ENDPOINTS=1 where you control the file):"))
		for _, key := range cfg.Dropped {
			b.WriteString(fmt.Sprintf("  %s\n", key))
		}
		b.WriteString("\n")
	}

	printModel := func(role config.Role) {
		spec := cfg.Models.ResolveModel(role)
		b.WriteString(fmt.Sprintf("  %-8s %s/%s", role, spec.Provider, spec.Model))
		if spec.BaseURL != "" {
			b.WriteString(fmt.Sprintf(" @ %s", spec.BaseURL))
		}
		b.WriteString(fmt.Sprintf("  [structured output: %s]\n", cmp(spec.StructuredOutput, config.StructuredAuto)))
	}

	b.WriteString(fmt.Sprintln("Models:"))
	printModel(config.RoleReview)
	printModel(config.RoleTriage)
	printModel(config.RoleValidate)

	// Printed whether or not validation is on, because "which model would
	// check my findings" and "is checking switched on" are separate questions
	// and an operator turning it on wants the answer to the first beforehand.
	b.WriteString(fmt.Sprintf("\nValidation (a domain expert re-checks each finding before it is published):\n  enabled %t\n  classes %s\n",
		cfg.Validation.Enabled, describeClasses(cfg.Validation.Classes)))

	b.WriteString(fmt.Sprintf("\nGating:\n  fail_on      %s\n  min_severity %s\n", cfg.Review.FailOn, cfg.Review.MinSeverity))
	b.WriteString(fmt.Sprintf("\nBudget:\n  max_files              %d\n  max_files_per_request  %d\n  token_budget_per_req   %d\n  concurrency            %d\n",
		cfg.Review.MaxFiles, cfg.Review.MaxFilesPerRequest, cfg.Review.TokenBudgetPerRequest, cfg.Review.Concurrency))

	b.WriteString(fmt.Sprintf("\nLinters (%s): %s\n", cfg.Linters.Mode, strings.Join(cfg.Linters.Enabled, ", ")))
	b.WriteString(fmt.Sprintf("\nPersona: %s\n", prompt.Describe(cfg.Persona)))

	if forPath != "" {
		b.WriteString(fmt.Sprintf("\nFor %s:\n", forPath))
		if cfg.Ignored(forPath) {
			b.WriteString(fmt.Sprintln("  ignored by review.ignore"))
		}
		instructions := cfg.InstructionsFor(forPath)
		if len(instructions) == 0 {
			b.WriteString(fmt.Sprintln("  no path-scoped instructions match"))
		}
		for _, ins := range instructions {
			b.WriteString(fmt.Sprintf("  - %s\n", ins))
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
	b.WriteString(fmt.Sprintf("\n===== review prompt =====\n\n%s", p.Explain()))

	_, err = io.WriteString(w, b.String())
	return err
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

// runLinters prints the analyzer catalog: what each tool reads, whether it
// is on by default, and where its configuration comes from.
func runLinters() error {
	fmt.Println("Deterministic analyzers. Each runs only when its binary is on PATH and the change contains")
	fmt.Println("files it reads; configuration never comes from the tree under review.")
	fmt.Println("  enabled  in linters.enabled out of the box; strict mode fails when it is missing")
	fmt.Println("  auto     runs when installed (linters.auto_detect); name it in linters.enabled to make it a promise")
	fmt.Println("  opt-in   runs only when named, and configured or trusted as the last column says")
	fmt.Println()
	fmt.Printf("  %-18s %-8s %-52s %s\n", "name", "runs", "covers", "configuration")
	fixed := []linters.CatalogEntry{
		{Name: "golangci-lint", Languages: "Go", Configuration: "open-nitpick's own config; linters.golangci_config overrides", Default: true},
		{Name: "ruff", Languages: "Python", Configuration: "--isolated; linters.ruff_config overrides", Default: true},
		{Name: "eslint", Languages: "JavaScript, TypeScript (via an operator config)", Configuration: "linters.eslint_config required"},
		{Name: "semgrep", Languages: "any (rules of your choosing)", Configuration: "linters.semgrep_config required"},
	}
	for _, e := range append(fixed, linters.Catalog()...) {
		def := "opt-in"
		switch {
		case e.Default:
			def = "enabled"
		case e.Auto:
			def = "auto"
		}
		conf := e.Configuration
		if e.Trusted {
			conf += "; executes tree code: needs linters.trusted"
		}
		fmt.Printf("  %-18s %-8s %-52s %s\n", e.Name, def, truncateTo(e.Languages, 52), conf)
	}
	return nil
}

func truncateTo(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// describeClasses renders a validation class list. The empty case is spelled
// out rather than printed blank: "" and "every class" mean the same thing here
// and only one of them says so.
func describeClasses(classes []config.Class) string {
	if len(classes) == 0 {
		return "(every class)"
	}

	names := make([]string, 0, len(classes))
	for _, c := range classes {
		names = append(names, string(c))
	}
	return strings.Join(names, ", ")
}

// cmp returns value, or fallback when value is empty.
func cmp(value, fallback config.StructuredMode) config.StructuredMode {
	if value == "" {
		return fallback
	}
	return value
}
