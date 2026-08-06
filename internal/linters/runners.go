package linters

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/jdziat/open-nitpick/internal/config"
)

// ANALYZER CONFIGURATION IS POLICY, AND POLICY COMES FROM OUTSIDE THE CHANGE.
//
// Every analyzer here used to be invoked in the tree under review with no
// no-config flag, so each read its configuration from that tree. Three things
// followed, in increasing severity:
//
//  1. SILENCING. A pull request adding a .golangci.yml with
//     `linters: {default: none}` switched off the entire deterministic half of
//     its own review, and the run reported success. `[tool.ruff.lint] select =
//     []` in pyproject.toml did the same for Python.
//  2. ENABLEMENT. semgrep's Detect required only that .semgrep.yml or
//     .semgrepignore EXIST, so a pull request that ADDED one turned semgrep on,
//     with rules the pull request wrote, in the run reviewing it.
//  3. CODE EXECUTION. eslint.config.js is JavaScript that eslint loads and
//     EXECUTES. A pull request adding one ran arbitrary code in CI with
//     GITHUB_TOKEN and the model API key in the environment.
//
// And a fourth that is worse than silencing, because it is not an absence:
// FABRICATION. golangci-lint's forbidigo takes a `msg` from the config file and
// prints it verbatim as the finding text, so a change could author the words of
// a deterministic finding that reaches the reviewing model as evidence.
//
// resolveBinary already refused an analyzer BINARY that resolves inside the
// repository, on exactly this reasoning — while the same process handed that
// binary a CONFIG FILE the pull request wrote. The rule is now the same for
// both: a change may not supply the policy it is reviewed under, which is the
// invariant internal/config enforces for .nitpick.yaml.
//
// What that does NOT close is in-source suppression. `//nolint`, `# noqa`,
// `# nosemgrep` and `eslint-disable` are comments in the code, not
// configuration, and golangci-lint has no flag to disable its own. See the
// README's security section, which states this rather than implying otherwise.
//
// AND CONFIGURATION IS NOT THE ONLY WAY THE TREE CAN SILENCE AN ANALYZER. A
// change that breaks the package LOAD silences golangci-lint just as completely
// — go.work naming other modules, a build constraint excluding every file — and
// that route does not go through a config file at all. Those runs used to be
// read as clean, because golangci-lint reports the failure in the same JSON
// envelope it reports issues in, and this file parsed the envelope while
// throwing the failure away. See golangciLint.findings.

// analyzerConfig is one analyzer's operator-supplied configuration, resolved
// and checked for containment once, at construction.
type analyzerConfig struct {
	// Ref is what to hand the analyzer: an absolute path outside the repository
	// under review, or — semgrep only — a registry reference. Empty means the
	// operator supplied none, which is the default.
	Ref string

	// Err is why a supplied configuration was refused.
	//
	// A runner holding one must not run AT ALL, rather than fall back to its
	// isolated default. An operator who named a config and got the isolated
	// ruleset instead would read a green run as their rules passing.
	Err error
}

// state describes where this analyzer's configuration came from, in the words a
// human reads in the run's report. isolated is what to say when the operator
// supplied nothing: the analyzer's own defaults for golangci-lint and ruff, and
// "not configured" for the two that have no useful defaults.
//
// Set.Run reaches this only for an analyzer that ran, where Err is nil by
// construction — it reports Detect's reason otherwise. The refusal branch stays
// because the alternative is falling through to `isolated`, and a caller asking
// a refused runner to describe itself must not be told it ran on its defaults.
func (c analyzerConfig) state(isolated string) string {
	switch {
	case c.Err != nil:
		return "did not run: " + c.Err.Error()
	case c.Ref != "":
		return "operator config " + c.Ref
	default:
		return isolated
	}
}

// fileConfig resolves an operator-supplied analyzer config path.
func fileConfig(repoRoot, ref string) analyzerConfig {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return analyzerConfig{}
	}

	path, err := resolveConfig(ref, repoRoot)
	if err != nil {
		return analyzerConfig{Err: err}
	}
	return analyzerConfig{Ref: path}
}

// semgrepConfig resolves semgrep's config, which may also name a registry rule
// set instead of a file.
//
// A registry reference is a network fetch the operator asked for BY NAME. The
// `--config auto` this replaced was a network fetch of rules nobody chose — and
// one that never worked here anyway: semgrep refuses `auto` when metrics are
// off, so the shipped invocation exited 2 with empty stdout on every run.
func semgrepConfig(repoRoot, ref string) analyzerConfig {
	ref = strings.TrimSpace(ref)
	if config.SemgrepRegistryRef(ref) {
		return analyzerConfig{Ref: ref}
	}
	return fileConfig(repoRoot, ref)
}

// resolveConfig turns an operator's analyzer configuration path into one safe
// to hand an analyzer, refusing anything that resolves inside the repository.
//
// It mirrors resolveBinary deliberately, including EvalSymlinks on both sides so
// that a SYMLINK outside the repository cannot point back into it. The two
// answer the same question about different objects — where did this come from —
// and the bug was that only one of them was being asked.
//
// It said "a link" and meant one kind of link. A HARD link is accepted: the same
// inode under a second name outside the repository passes, because EvalSymlinks
// resolves symlinks and a hard link is not one. Nothing here can see it without
// walking the repository comparing inodes, on every run, for a case a checkout
// cannot produce — git stores no hard links, so the tree under review cannot
// create one, and anyone who can already has write access outside the
// repository. It is a residual, recorded in the README, not a property this
// function has.
func resolveConfig(path, repoRoot string) (string, error) {
	if !filepath.IsAbs(path) {
		// Relative means relative to the working directory, which is the tree
		// under review: the one place configuration may not come from.
		return "", fmt.Errorf("analyzer config %q is not an absolute path", path)
	}

	abs := filepath.Clean(path)
	if _, err := os.Stat(abs); err != nil {
		// Loud, because the quiet alternative is an analyzer that silently ran
		// with different rules than the operator believes.
		return "", fmt.Errorf("cannot read analyzer config %s: %w", abs, err)
	}

	root, err := filepath.Abs(repoRoot)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}

	if rel, err := filepath.Rel(root, abs); err == nil &&
		!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." {
		return "", fmt.Errorf(
			"analyzer config %s resolves inside the repository under review; a change may not supply "+
				"the policy it is reviewed under", abs)
	}

	return abs, nil
}

// golangciLint runs golangci-lint over the changed Go packages.
type golangciLint struct{ cfg analyzerConfig }

func (g *golangciLint) Name() string { return "golangci-lint" }

// Detect reports why golangci-lint will not run, and nil when it will.
//
// THE BUG: it required go.mod at the CHECKOUT ROOT, so in any repository whose
// module lives in a subdirectory — backend/go.mod, services/api/go.mod — the Go
// analyzer never ran, for any change, and nothing in any diff showed it. No
// attack was needed; that was simply the default state of a monorepo. Detection
// now asks the question the run actually depends on: is each changed Go file
// inside a module this process can lint. See goTargets.
//
// The residual it used to describe — a change that DELETES go.mod silences this
// arm — is still real, and is still visible in the diff. What was wrong was the
// sentence next to it claiming that a silencing which REPORTS SUCCESS could only
// come from a config file. It can come from the tree: see findings.
func (g *golangciLint) Detect(_ context.Context, repoRoot string, files []string) error {
	if g.cfg.Err != nil {
		return g.cfg.Err
	}
	if len(filterExt(files, ".go")) == 0 {
		return errNoTargets
	}
	if !available("golangci-lint") {
		return notOnPath("golangci-lint")
	}
	if len(goTargets(repoRoot, files)) == 0 {
		return errors.New("no go.mod at or above the changed Go files, so there is no module to analyze")
	}
	return nil
}

func (g *golangciLint) State() string { return g.cfg.state("isolated") }

// golangciOutput is the part of golangci-lint's JSON report this package reads.
//
// Report.Error is in it because LEAVING IT OUT WAS THE BUG. golangci-lint
// reports a failure to analyze in the same envelope it reports issues in:
// {"Issues":[],"Report":{"Error":"typechecking error: ..."}}, printed on stdout
// alongside a non-zero exit. Declaring only Issues threw that sentence away, so
// an analysis that never ran decoded to zero findings and no error — identical
// to clean code — and a change could reach that state by adding a go.work that
// does not list the module, or a `//go:build ignore` line on the file it wanted
// unread. Verified against golangci-lint 2.8.0.
//
// Report.Error is NOT the only shape that failure takes, which is why
// typecheckLinter below exists as well: a package that fails to COMPILE is
// reported as an ordinary Issue, with exit 0 and Report.Error empty.
type golangciOutput struct {
	Issues []struct {
		FromLinter string `json:"FromLinter"`
		Text       string `json:"Text"`
		Severity   string `json:"Severity"`
		Pos        struct {
			Filename string `json:"Filename"`
			Line     int    `json:"Line"`
		} `json:"Pos"`
	} `json:"Issues"`

	Report struct {
		Error string `json:"Error"`
	} `json:"Report"`
}

// Run lints the changed packages, one invocation per module.
//
// One invocation per module because golangci-lint has to be run FROM INSIDE the
// module: with cwd at the checkout root and a target in a nested module it exits
// 5 with "no go files to analyze" and an empty report. Verified against
// golangci-lint 2.8.0.
func (g *golangciLint) Run(ctx context.Context, repoRoot string, files []string) ([]Finding, error) {
	if err := g.cfg.Err; err != nil {
		return nil, err
	}

	targets := goTargets(repoRoot, files)
	if len(targets) == 0 {
		return nil, nil
	}

	// GOTOOLCHAIN=local, because go.mod is also part of the tree under review
	// and `toolchain go1.99.98` in it makes golangci-lint's package loader
	// DOWNLOAD AND RUN that toolchain — code execution from the change, by a
	// different door than the config file. With it set the same tree fails with
	// "the Go language version ... is lower than the targeted Go version", exit
	// 3 and empty stdout, which runCommand reports as an error.
	//
	// That is a property of THAT failure and not of tree-induced load failures
	// in general: go.work and a build constraint break the load too and both
	// print a populated report with a non-zero exit. findings below is what
	// catches those.
	env := analyzerEnv(map[string]string{"GOTOOLCHAIN": "local"})

	var all []Finding
	for _, t := range targets {
		// --config and --no-config are mutually exclusive: golangci-lint 2.8.0
		// refuses both with "can't combine option --config and --no-config" and
		// exits before analyzing anything.
		args := []string{"run"}
		if g.cfg.Ref == "" {
			args = append(args, "--no-config")
		} else {
			args = append(args, "--config", g.cfg.Ref)
		}
		args = append(args, "--output.json.path", "stdout", "--issues-exit-code", "0", "--")
		args = append(args, t.Dirs...)

		out, exit, err := runCommand(ctx, repoRoot, filepath.Join(repoRoot, t.Module), "golangci-lint", env, args...)
		if err != nil {
			return nil, err
		}

		found, err := g.findings(out, exit)
		if err != nil {
			// Not "keep what the other modules produced": a module whose
			// analysis failed contributes silence, and silence merged into a
			// successful report is the state this whole file exists to prevent.
			return nil, err
		}
		for i := range found {
			found[i].Path = repoPath(repoRoot, t.Module, found[i].Path)
		}
		all = append(all, found...)
	}

	return all, nil
}

// findings converts one golangci-lint report into findings.
//
// Split from Run for the reason semgrep's is: what this repository does with a
// severity is decided from the analyzer's real output, and a test that
// hand-builds the Finding has already made the decision under test.
//
// golangci-lint's Severity is not a vocabulary at all — it is whatever text is
// written in the config file's `severity` block. Under the default that file can
// only be one the OPERATOR supplied out of tree, so calling it the operator's
// text is now true; it used to be false on any fork pull request, where the
// person writing it was the contributor being reviewed. With no config at all
// golangci-lint publishes no severity, so every finding here arrives as warning
// via mapSeverity's default branch.
//
// THE BUG, AND IT WAS BEING EXPLOITED BY A ONE-FILE DIFF: a report that PARSED
// was read as a report of an analysis that HAPPENED. Adding a go.work that does
// not list the module makes golangci-lint 2.8.0 exit 7 while printing
// {"Issues":[],"Report":{"Error":"typechecking error: directory pkg is contained
// in a module that is not one of the workspace modules listed in go.work..."}}.
// runCommand tolerates a non-zero exit whenever stdout is non-empty (analyzers
// exit non-zero on findings), decodeJSON was satisfied because a payload
// arrived, and zero Issues read as clean code — so a pull request switched off
// the entire deterministic half of its own Go review by ADDING ONE FILE, and the
// run reported success in strict mode as well as auto. `//go:build ignore` on
// the changed file reproduces it exactly. There is nothing to salvage from such
// a run: 2.8.0 abandons the whole invocation, so a second, healthy package in
// the same run reports nothing either — measured, not assumed.
//
// AND THE THIRD SHAPE, WHICH THE FIRST TWO BOTH MISS AND WHICH IS THE ONE A
// CHANGE REACHES WITHOUT TRYING: a package that does not COMPILE. golangci-lint
// 2.8.0 reports that as an ordinary Issue with FromLinter "typecheck", exits
// ZERO, and leaves Report.Error EMPTY — so neither check above fires. Measured,
// on a change that adds one file:
//
//	Issues: [{FromLinter: "typecheck",
//	          Text: ": # probe/pkg\npkg/b.go:4:9: undefined: undefinedSymbol",
//	          Pos: {Filename: "pkg/a.go", Line: 1}}]
//
// Three things make that a silencing rather than a finding. The analysis is
// ABANDONED, not degraded: a second, healthy package in the same invocation
// reported nothing either, so one broken package deletes every other package's
// results. The issue is anchored to line 1 of the alphabetically FIRST file in
// the package rather than to the file that failed, so with the default
// only_changed_lines it is dropped by normalize and nothing is published at
// all. And `go build ./...` STAYS GREEN when the offending file is a _test.go,
// so the change looks healthy to everything except the review it silenced.
// End to end that was zero findings, a nil error, and status "ran: isolated" —
// byte-identical to a clean review, in strict mode as well as auto.
//
// So a typecheck issue is read as what it is: not a lint result, but
// golangci-lint reporting through the issue channel that it could not analyze
// the code. Its Text carries the real file and line, which Report.Error would
// not have given us, so that is what the status line quotes.
func (g *golangciLint) findings(out []byte, exit int) ([]Finding, error) {
	var parsed golangciOutput
	if err := decodeJSON(out, &parsed); err != nil {
		return nil, fmt.Errorf("parse golangci-lint output: %w", err)
	}

	if msg := strings.TrimSpace(parsed.Report.Error); msg != "" {
		return nil, fmt.Errorf("the analysis did not complete: %s", oneLine(msg))
	}
	for _, issue := range parsed.Issues {
		if issue.FromLinter != typecheckLinter {
			continue
		}
		// Text over Pos deliberately: Pos names a file that compiled fine.
		where := strings.TrimSpace(issue.Text)
		if where == "" {
			where = issue.Pos.Filename
		}
		return nil, fmt.Errorf(
			"the code did not compile, so no analyzer ran over it: %s", oneLine(strings.TrimPrefix(where, ":")))
	}
	if exit != 0 {
		return nil, fmt.Errorf(
			"the process exited %d and gave no reason in its report; findings do not set its exit "+
				"status here, so its results cannot be read as complete", exit)
	}

	findings := make([]Finding, 0, len(parsed.Issues))
	for _, issue := range parsed.Issues {
		findings = append(findings, Finding{
			Path:        filepath.ToSlash(issue.Pos.Filename),
			Line:        issue.Pos.Line,
			Rule:        issue.FromLinter,
			Message:     issue.Text,
			Severity:    mapSeverity(issue.Severity),
			RawSeverity: strings.TrimSpace(issue.Severity),
		})
	}
	return findings, nil
}

// typecheckLinter is the name golangci-lint puts on a compile failure it
// reports through the issue channel.
//
// It is not a linter and cannot be switched off: it is how the package loader
// says the code could not be type-checked, which is also the state in which
// every type-based linter — errcheck, staticcheck, govet, gosec — produced
// nothing. See golangciLint.findings.
const typecheckLinter = "typecheck"

// goTarget is one golangci-lint invocation.
type goTarget struct {
	// Module is the module root, relative to the repository. Empty is the
	// repository root itself.
	Module string

	// Dirs are the package directories to analyze, relative to Module and in
	// the ./x form golangci-lint expects.
	Dirs []string
}

// goTargets reduces changed Go files to the package directories to lint,
// grouped by the module that owns each one. golangci-lint works on packages,
// not files.
//
// Grouping by module is the fix for a repository whose go.mod is not at the
// checkout root. What was there before produced directories relative to the
// checkout root and left detection to a root go.mod, which meant a monorepo's Go
// code was never analyzed at all — and the report said the binary might be
// missing.
//
// Files with no go.mod above them are dropped rather than guessed at: there is
// no module to run in, and inventing one gets "no go files to analyze" with an
// empty report. Detect reports that case as a reason a reader sees.
func goTargets(repoRoot string, files []string) []goTarget {
	byModule := map[string][]string{}
	var order []string

	for _, f := range files {
		if filepath.Ext(f) != ".go" {
			continue
		}

		dir := path.Dir(filepath.ToSlash(f))
		if dir == "." {
			dir = ""
		}
		module, ok := goModuleFor(repoRoot, dir)
		if !ok {
			continue
		}

		rel := strings.TrimPrefix(strings.TrimPrefix(dir, module), "/")
		pkg := "./" + rel
		if _, seen := byModule[module]; !seen {
			order = append(order, module)
		}
		if !slices.Contains(byModule[module], pkg) {
			byModule[module] = append(byModule[module], pkg)
		}
	}

	// Sorted, so that findings from a multi-module change arrive in the same
	// order every run: map iteration order would otherwise reshuffle a review.
	slices.Sort(order)

	targets := make([]goTarget, 0, len(order))
	for _, module := range order {
		targets = append(targets, goTarget{Module: module, Dirs: byModule[module]})
	}
	return targets
}

// goModuleFor finds the module directory owning a repository-relative package
// directory, searching upward and stopping at the repository root.
func goModuleFor(repoRoot, dir string) (string, bool) {
	for d := dir; ; d = parentDir(d) {
		if exists(repoRoot, path.Join(d, "go.mod")) {
			return d, true
		}
		if d == "" {
			return "", false
		}
	}
}

// parentDir walks one directory up in a repository-relative slash path, with ""
// as the repository root.
func parentDir(d string) string {
	parent := path.Dir(d)
	if parent == "." || parent == "/" {
		return ""
	}
	return parent
}

// repoPath turns a path an analyzer printed relative to its own working
// directory back into one relative to the repository.
//
// Without it every finding from a nested module anchors to the wrong file — or
// to no file, which drops it silently in normalize.
func repoPath(repoRoot, module, reported string) string {
	if filepath.IsAbs(reported) {
		return relative(repoRoot, reported)
	}
	return path.Join(module, filepath.ToSlash(reported))
}

// ruff runs the Python linter.
type ruff struct{ cfg analyzerConfig }

func (r *ruff) Name() string { return "ruff" }

// Detect asks whether the change contains Python, not whether the checkout root
// looks like a Python project.
//
// It used to require pyproject.toml, ruff.toml, setup.py or requirements.txt AT
// THE ROOT, which had golangci-lint's monorepo defect — services/api/pyproject.
// toml meant no Python was ever linted — and bought nothing: ruff --isolated
// needs no project marker, and a change with no .py files in it returns no
// findings from this runner either way.
func (r *ruff) Detect(_ context.Context, _ string, files []string) error {
	if r.cfg.Err != nil {
		return r.cfg.Err
	}
	if len(filterExt(files, ".py", ".pyi")) == 0 {
		return errNoTargets
	}
	if !available("ruff") {
		return notOnPath("ruff")
	}
	return nil
}

func (r *ruff) State() string { return r.cfg.state("isolated") }

// ruffIssue is one entry of ruff's JSON output.
type ruffIssue struct {
	Code     string `json:"code"`
	Message  string `json:"message"`
	Filename string `json:"filename"`
	Location struct {
		Row int `json:"row"`
	} `json:"location"`
}

func (r *ruff) Run(ctx context.Context, repoRoot string, files []string) ([]Finding, error) {
	if err := r.cfg.Err; err != nil {
		return nil, err
	}

	targets := filterExt(files, ".py", ".pyi")
	if len(targets) == 0 {
		return nil, nil
	}

	// --isolated and --config are mutually exclusive: ruff 0.16.1 exits 2 with
	// "cannot be used with `--isolated`".
	args := []string{"check"}
	if r.cfg.Ref == "" {
		args = append(args, "--isolated")
	} else {
		args = append(args, "--config", r.cfg.Ref)
	}
	args = append(args, "--output-format", "json", "--no-fix", "--quiet", "--")
	args = append(args, targets...)

	// RUFF_OUTPUT_FILE redirects the report to a file, leaving stdout empty —
	// verified against ruff 0.16.1, where a run with real findings produced zero
	// bytes on stdout. RUFF_OUTPUT_FORMAT did NOT override the explicit
	// --output-format in that version; it is cleared alongside because the
	// precedence is ruff's to change and the cost of clearing it is nothing.
	env := analyzerEnv(nil, "RUFF_OUTPUT_FILE", "RUFF_OUTPUT_FORMAT")

	out, _, err := runCommand(ctx, repoRoot, repoRoot, "ruff", env, args...)
	if err != nil {
		return nil, err
	}

	var issues []ruffIssue
	if err := decodeJSON(out, &issues); err != nil {
		return nil, fmt.Errorf("parse ruff output: %w", err)
	}

	findings := make([]Finding, 0, len(issues))
	for _, issue := range issues {
		// RawSeverity stays empty because ruff publishes no severity at all:
		// the warning below is entirely this project's choice, and there is no
		// word of ruff's to quote. Downstream that reads as "not recorded",
		// which is the truth, rather than as ruff having said "warning".
		findings = append(findings, Finding{
			Path:     relative(repoRoot, issue.Filename),
			Line:     issue.Location.Row,
			Rule:     issue.Code,
			Message:  issue.Message,
			Severity: config.SeverityWarning,
		})
	}
	return findings, nil
}

// eslint runs the JavaScript and TypeScript linter.
type eslint struct{ cfg analyzerConfig }

func (e *eslint) Name() string { return "eslint" }

// Detect requires an operator-supplied config, so eslint is OFF by default.
//
// It is off rather than isolated because eslint has no usable isolated mode:
// --no-config-lookup leaves zero rules configured and therefore zero findings,
// for every repository, forever — which is the silencing this change exists to
// prevent, applied globally by our own hand. Containment that costs the whole
// analyzer is not containment, so the analyzer is switched off and SAID to be
// off, and LinterStrict still catches an operator who enabled it without
// configuring it.
//
// Base-revision resolution was rejected as the alternative: an eslint config is
// a LOADER, so an unmodified base config doing `import "./eslint-rules/index.js"`
// still executes a file the pull request wrote, and relocating that config out
// of the tree breaks its imports outright, because Node resolves them from the
// config file's own directory.
//
// Deliberately does NOT consider node_modules/.bin/eslint. That binary comes
// from the tree under review, and running it would execute attacker-supplied
// code with the review's credentials in the environment. See resolveBinary.
//
// It no longer requires package.json at the checkout root. That check had
// golangci-lint's monorepo defect — web/package.json meant eslint never ran, for
// any change, with the status line blaming a missing binary — and it decided
// nothing: the operator's config is what switches this analyzer on, and a change
// with no JavaScript in it has no targets to pass either way.
func (e *eslint) Detect(_ context.Context, _ string, files []string) error {
	if e.cfg.Err != nil {
		return e.cfg.Err
	}
	if len(filterExt(files, eslintExts...)) == 0 {
		return errNoTargets
	}
	if e.cfg.Ref == "" {
		return errors.New(eslintUnconfigured)
	}
	if !available("eslint") {
		return notOnPath("eslint")
	}
	return nil
}

func (e *eslint) State() string { return e.cfg.state(eslintUnconfigured) }

// eslintUnconfigured is one sentence in one place, because Detect, Run and the
// published status must not be able to describe this state differently.
const eslintUnconfigured = "not configured; set linters.eslint_config to a config outside the repository"

// eslintExts are the extensions eslint is given. A change containing none of
// them is not a degraded eslint run, it is a change with no JavaScript in it.
var eslintExts = []string{".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs"}

// eslintFile is one file's results in eslint's JSON output.
type eslintFile struct {
	FilePath string `json:"filePath"`
	Messages []struct {
		RuleID   string `json:"ruleId"`
		Severity int    `json:"severity"`
		Message  string `json:"message"`
		Line     int    `json:"line"`
	} `json:"messages"`
}

func (e *eslint) Run(ctx context.Context, repoRoot string, files []string) ([]Finding, error) {
	if err := e.cfg.Err; err != nil {
		return nil, err
	}
	if e.cfg.Ref == "" {
		return nil, errors.New(eslintUnconfigured)
	}

	targets := filterExt(files, eslintExts...)
	if len(targets) == 0 {
		return nil, nil
	}

	// --config alone already suppresses the tree's eslint.config.js — verified
	// against eslint 10.8.0, where a tree config whose top level printed a
	// marker did not print it under --config. --no-config-lookup is passed as
	// well because the two compose without error and the suppression is then
	// stated rather than inferred.
	args := []string{"--config", e.cfg.Ref, "--no-config-lookup", "--format", "json", "--no-color", "--"}
	args = append(args, targets...)

	out, _, err := runCommand(ctx, repoRoot, repoRoot, "eslint", nil, args...)
	if err != nil {
		return nil, err
	}

	var results []eslintFile
	if err := decodeJSON(out, &results); err != nil {
		return nil, fmt.Errorf("parse eslint output: %w", err)
	}

	var findings []Finding
	for _, file := range results {
		for _, m := range file.Messages {
			severity := config.SeverityWarning
			if m.Severity >= 2 {
				severity = config.SeverityError
			}

			findings = append(findings, Finding{
				Path:        relative(repoRoot, file.FilePath),
				Line:        m.Line,
				Rule:        m.RuleID,
				Message:     m.Message,
				Severity:    severity,
				RawSeverity: strconv.Itoa(m.Severity),
			})
		}
	}
	return findings, nil
}

// semgrep runs the multi-language static analyzer.
type semgrep struct{ cfg analyzerConfig }

func (s *semgrep) Name() string { return "semgrep" }

// Detect requires an operator-supplied config and reads NOTHING from the tree.
//
// THE BUG IT REPLACES: this used to return true when .semgrep.yml, .semgrep.yaml,
// .semgrepignore or .semgrep merely EXISTED in the head tree, so a pull request
// that added one switched semgrep on — with rules the pull request wrote — in
// the run reviewing it. That is enablement rather than silencing, and no
// severity ceiling or ignore list touches it. Keying on operator configuration
// instead means no file in the tree decides whether an analyzer runs.
//
// .semgrepignore needed no flag: explicit file targets bypass it, verified
// against semgrep 1.172.0 with the target file listed in .semgrepignore. It was
// only ever a detection trigger here, and the trigger is what is gone.
func (s *semgrep) Detect(_ context.Context, _ string, files []string) error {
	if s.cfg.Err != nil {
		return s.cfg.Err
	}
	// semgrep is multi-language and takes whatever it is given, so any changed
	// file is a target for it.
	if len(files) == 0 {
		return errNoTargets
	}
	if s.cfg.Ref == "" {
		return errors.New(semgrepUnconfigured)
	}
	if !available("semgrep") {
		return notOnPath("semgrep")
	}
	return nil
}

func (s *semgrep) State() string { return s.cfg.state(semgrepUnconfigured) }

// semgrepUnconfigured is one sentence in one place; see eslintUnconfigured.
const semgrepUnconfigured = "not configured; set linters.semgrep_config to a rule set outside the repository"

// semgrepOutput is the part of semgrep's JSON report this package reads.
//
// Errors is in it for the reason golangciOutput.Report.Error is: semgrep reports
// a failure in the same envelope as its results. A rule set it cannot parse
// gives {"results":[],"errors":[{"type":"Rule parse error",...}]} with exit 2 —
// verified against semgrep 1.172.0 — and reading only results turned that into a
// clean bill of health for an operator whose rules never ran at all.
type semgrepOutput struct {
	Results []struct {
		CheckID string `json:"check_id"`
		Path    string `json:"path"`
		Start   struct {
			Line int `json:"line"`
		} `json:"start"`
		Extra struct {
			Message  string `json:"message"`
			Severity string `json:"severity"`
		} `json:"extra"`
	} `json:"results"`

	Errors []struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"errors"`
}

func (s *semgrep) Run(ctx context.Context, repoRoot string, files []string) ([]Finding, error) {
	if err := s.cfg.Err; err != nil {
		return nil, err
	}
	if s.cfg.Ref == "" {
		return nil, errors.New(semgrepUnconfigured)
	}
	if len(files) == 0 {
		return nil, nil
	}

	// --metrics off composes with an explicit --config: verified against semgrep
	// 1.172.0, which returned results and exit 0. The documented conflict is with
	// `--config auto` specifically, and that argument is gone.
	args := []string{"--json", "--quiet", "--metrics", "off", "--config", s.cfg.Ref, "--"}
	args = append(args, files...)

	out, exit, err := runCommand(ctx, repoRoot, repoRoot, "semgrep", nil, args...)
	if err != nil {
		return nil, err
	}

	return s.findings(out, exit)
}

// findings converts one semgrep report into findings.
//
// Split from Run so that the step which decides what this repository does with
// a severity semgrep printed can be exercised from the analyzer's real output
// rather than from a hand-built Finding. A test that assembles the Finding
// itself has already made the decision under test.
//
// The exit status is what decides whether the scan happened, because semgrep
// documents 0 and 1 (findings present) as success and reserves the rest for
// failure; errors[] supplies the sentence a reader gets. Keyed the other way
// round — errors[] deciding — a benign entry would fail the whole analyzer, and
// benign entries do occur: semgrep 1.172.0 exits 0 with errors:[] on an
// unparseable Python file and on binary and Markdown targets, which is measured
// rather than assumed.
func (s *semgrep) findings(out []byte, exit int) ([]Finding, error) {
	var parsed semgrepOutput
	if err := decodeJSON(out, &parsed); err != nil {
		return nil, fmt.Errorf("parse semgrep output: %w", err)
	}

	if exit != 0 && exit != 1 {
		reason := fmt.Sprintf("exited %d", exit)
		if len(parsed.Errors) > 0 {
			reason = oneLine(parsed.Errors[0].Type + ": " + parsed.Errors[0].Message)
		}
		return nil, fmt.Errorf("the scan did not complete: %s", reason)
	}

	findings := make([]Finding, 0, len(parsed.Results))
	for _, r := range parsed.Results {
		findings = append(findings, Finding{
			Path:        filepath.ToSlash(r.Path),
			Line:        r.Start.Line,
			Rule:        r.CheckID,
			Message:     r.Extra.Message,
			Severity:    mapSeverity(r.Extra.Severity),
			RawSeverity: strings.TrimSpace(r.Extra.Severity),
		})
	}
	return findings, nil
}

// mapSeverity translates an analyzer's severity vocabulary to ours.
//
// EVERY word here is foreign, including the ones spelled like our levels. There
// is no branch for "the analyzer used our own word, so nothing needs
// translating": semgrep's documented vocabulary is LOW/MEDIUM/HIGH/CRITICAL with
// "the older levels ERROR, WARNING and INFO match HIGH, MEDIUM and LOW", so
// semgrep's ERROR *is* semgrep's HIGH — one level, two spellings, both inside
// semgrep's scale and neither one a statement about ours. Sorting the two into
// "translated" and "untranslated" branches described a distinction that does not
// exist. It is a translation table, so it DESTROYS the analyzer's word: ERROR
// and HIGH both land on error and the result cannot be un-mapped, which is why
// every caller records the original in Finding.RawSeverity and why
// review.Finding.SeverityTranslated is set for all of them.
//
// THE BUG: "CRITICAL" folded onto error, so the codomain excluded critical and
// no analyzer finding could ever BE critical. review.fail_on accepts "critical",
// so a repository configured that way got zero gating from semgrep — the only
// one of the four analyzers whose scale HAS a critical — and from any
// golangci-lint whose severity settings name one. The build went green on a
// finding the analyzer itself called critical, and nothing said so. The
// justification given was that "an unknown value becoming critical would poison
// the gate", which is an argument about UNRECOGNIZED input. An analyzer that
// printed the word critical is not an analyzer that printed something we could
// not parse, and treating the two as one case is what opened the hole.
//
// THE RULE NOW: translate for fidelity, reduce at policy time. The codomain
// covers all five levels review.fail_on accepts, so no gate threshold is
// unreachable by construction. Whether a deterministic tool may fail a build is
// the operator's question and config.Linters.MaxSeverity is where they answer
// it; answering it here answered it for every repository at once and
// permanently. That cuts both ways and the wide side is golangci-lint, whose
// Severity field is whatever text a config file's `severity` block carries:
// with `severity.default: CRITICAL` a misspell or a typecheck compile error
// arrives as a critical, and only max_severity stops it reaching a critical
// gate. That text can now only come from linters.golangci_config, a file
// outside the repository — under the default there is no config, golangci-lint
// publishes no severity at all, and every Go analyzer finding lands on the
// warning below.
//
// NIT is reachable from the literal word and nothing else. golangci-lint emits
// whatever its config says, so "nit" genuinely arrives, and folding it up to
// info would be the same unjustified reduction pointed the other way. It carries
// a cost the operator should know about, because nit is the one level BELOW the
// default review.min_severity of info: a repository whose golangci_config says
// `severity.default: nit` publishes no Go analyzer findings at all under the
// default gate. That is both configurations doing what they say, and it is why
// nothing FOREIGN is folded onto nit — LOW, INFO and NOTE are the weakest thing
// these analyzers can say and still mean "a rule fired", where nit means "take
// it or leave it", so inventing nits from an analyzer's floor would silence
// findings nobody asked to silence.
//
// AN UNREADABLE WORD is warning, the same as no word at all, because they are
// the same state: this project has no usable severity from the analyzer and
// picks one. Semgrep prints values outside its own documented four (EXPERIMENT),
// and golangci-lint prints anything. Flooring those at info instead — to match
// config.Severity.Normalize, which is where a MODEL's unrecognized word lands —
// looks symmetric and is not: a model was handed our enum and writing outside it
// is that reporter misbehaving, whereas an analyzer was never given our
// vocabulary and an unreadable word is our translation failing. Making our
// failure quieter deletes real findings under any min_severity above info, and
// it would rank "the tool said something we could not read" BELOW "the tool said
// nothing", which is incoherent.
func mapSeverity(s string) config.Severity {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "CRITICAL":
		return config.SeverityCritical
	// Synonyms within one analyzer's scale, not one foreign word and one of
	// ours: semgrep documents ERROR/WARNING/INFO as the older spellings of
	// HIGH/MEDIUM/LOW.
	case "ERROR", "HIGH":
		return config.SeverityError
	case "WARNING", "WARN", "MEDIUM":
		return config.SeverityWarning
	case "INFO", "INFORMATION", "LOW", "NOTE":
		return config.SeverityInfo
	case "NIT":
		return config.SeverityNit
	default:
		return config.SeverityWarning
	}
}

// filterExt keeps files with one of the given extensions.
func filterExt(files []string, exts ...string) []string {
	out := make([]string, 0, len(files))

	for _, f := range files {
		ext := strings.ToLower(filepath.Ext(f))
		for _, want := range exts {
			if ext == want {
				out = append(out, f)
				break
			}
		}
	}
	return out
}

// exists reports whether a path exists inside the repository.
func exists(repoRoot, name string) bool {
	_, err := os.Stat(filepath.Join(repoRoot, name))
	return err == nil
}

// relative converts an absolute analyzer path to a repository-relative one.
func relative(repoRoot, path string) string {
	if rel, err := filepath.Rel(repoRoot, path); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(path)
}

// decodeJSON decodes an analyzer's JSON payload out of its stdout, and REQUIRES
// one.
//
// Analyzers surround their JSON with human-readable noise on both sides:
// deprecation notices before it, and — golangci-lint does this — a "1 issues:"
// summary after it. A streaming decoder reads exactly the first JSON value and
// ignores whatever trails it, which a substring trim cannot do.
//
// THE BUG: a missing payload used to return nil, on the theory that an analyzer
// which found nothing says so in prose. None of these four does — golangci-lint
// prints {"Issues":[]}, ruff and eslint print [], semgrep prints its envelope —
// so the only things that reach that branch are failures. Combined with
// runCommand, which errors only when stdout is empty AND the exit was non-zero,
// an analyzer that exited 0 printing nothing became "zero findings, no error",
// indistinguishable from clean code. That is exactly the failure this whole file
// is defending against, and it was already shipped: `semgrep --config auto
// --metrics off` exits 2 with empty stdout, so the semgrep runner had never
// produced a finding. A missing payload is now an error, which Set.Run logs in
// auto mode and fails on in strict.
//
// A PRESENT PAYLOAD IS NOT A SUCCESSFUL ANALYSIS, and this function has no
// opinion about that. It is a decoder: it can tell that nothing was reported,
// never that what was reported describes a run that finished. golangci-lint and
// semgrep both report their own failures INSIDE a well-formed payload, and their
// runners check for it after decoding — see golangciLint.findings and
// semgrep.findings, which is where "a report arrived" is turned into "the
// analysis happened".
func decodeJSON(out []byte, target any) error {
	trimmed := trimToJSON(out)
	if len(trimmed) == 0 {
		return fmt.Errorf("no JSON report on stdout (got: %q)", snippet(out))
	}

	if err := json.NewDecoder(bytes.NewReader(trimmed)).Decode(target); err != nil {
		return fmt.Errorf("%w (got: %s)", err, snippet(trimmed))
	}
	return nil
}

// trimToJSON drops anything printed before the JSON payload, returning nil when
// there is no JSON at all.
func trimToJSON(out []byte) []byte {
	s := strings.TrimSpace(string(out))
	if s == "" {
		return nil
	}

	start := strings.IndexAny(s, "[{")
	if start < 0 {
		return nil
	}
	return []byte(s[start:])
}

// snippet truncates output for an error message.
func snippet(b []byte) string {
	const max = 200

	s := strings.Join(strings.Fields(string(b)), " ")
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
