package linters

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"go/build"
	"go/parser"
	"go/scanner"
	"go/token"
	"go/version"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/diff"
	"github.com/jdziat/open-nitpick/internal/gomod"
	"github.com/jdziat/open-nitpick/internal/review"
)

// Analyzer configuration is policy, and policy comes from outside the change.
//
// An analyzer invoked in the tree under review with no no-config flag reads its
// configuration from that tree. Three things follow, in increasing severity:
//
//  1. Silencing. A pull request adding a .golangci.yml with
//     `linters: {default: none}` switches off the entire deterministic half of
//     its own review, and the run reports success. `[tool.ruff.lint] select =
//     []` in pyproject.toml does the same for Python.
// 2. Enablement. A semgrep Detect requiring only that .semgrep.yml or
// .semgrepignore exist lets a pull request that adds one turn semgrep on,
//     with rules the pull request wrote, in the run reviewing it.
//  3. Code execution. eslint.config.js is JavaScript that eslint loads and
//     runs, so a pull request adding one runs arbitrary code in CI with
//     GITHUB_TOKEN and the model API key in the environment.
//
// A fourth is worse than silencing because it is not an absence: fabrication
// .golangci-lint's forbidigo takes a `msg` from the config file and prints it
// verbatim as the finding text, so a change can author the words of a
// deterministic finding that reaches the reviewing model as evidence.
//
// resolveBinary refuses an analyzer binary that resolves inside the repository
// on this reasoning, and the rule is the same for the config file it is handed:
// a change may not supply the policy it is reviewed under, the invariant
// internal/config enforces for .nitpick.yaml.
//
// What that does not close is in-source suppression. `//nolint`, `# noqa`,
// `# nosemgrep` and `eslint-disable` are comments in the code rather than
// configuration, and golangci-lint has no flag to disable its own. Its scope is
// a whole file, not a line: golangci-lint expands a //nolint to the declaration
// it is attached to, and attached to the package clause it covers everything,
// so a one-line diff adding `//nolint:all` above `package p` takes a file with
// pre-existing violations to zero findings on lines the change never touched.
// It cannot be prevented from outside the tree, so it is counted and published
// instead; see golangciLint.Uncovered.
//
// Configuration is not the only way the tree can silence an analyzer. A change
// that breaks the package load silences golangci-lint as completely, through
// go.work naming other modules or a build constraint excluding every file in
// the directory, and that route touches no config file. Such a run reads as
// clean unless the failure is pulled out of the JSON envelope golangci-lint
// reports issues in. See golangciLint.findings.
//
// A load failure is the loud shape of that, and there is a quiet one. The
// constraint has to empty the whole directory to fail the load; put one
// unconstrained sibling beside the changed file and the package loads while the
// changed file is never read. `//go:build windows`, `//go:build ignore` and a
// plain rename to app_windows.go each reach zero findings, exit 0, a nil error
// and the roster line "ran". Nothing there is a failure to report, so it is
// reported as a coverage gap; see golangciLint.Uncovered.
//
// Policy is not a list of filenames, and drawing the boundary around config
// files leaves the property open. Policy is anything in the tree that decides
// what the review reports, and --no-config closes one channel while opening
// another: it removes the attacker's configuration and this tool's own defaults
// in the same stroke, leaving golangci-lint's defaults in charge, and those
// read the tree. Measured against golangci-lint 2.8.0:
//
//   - the standard `Code generated ... do not edit` header on line 1 of the
//     file under review skips it entirely, because linters.exclusions.generated
//     defaults to "lax". Zero findings, exit 0, empty Report.Error, status
//     "ran(isolated)", byte-identical to a clean run, in strict mode too.
//   - max-same-issues defaults to 3 and max-issues-per-linter to 50, so a package
//     with eight identical errcheck violations reports three. The JSON does not
//     say that five were dropped.
//   - uniq-by-line defaults to true, so of four issues on one line, one is
//     published: two staticcheck findings vanished behind an ineffassign on the
//     same line in a two-statement function.
//
// None of that needs a config file, and only the first has a config-file switch
// (`linters.exclusions.generated: disable`, with no command-line equivalent in
// 2.8.0). So open-nitpick ships its own config, materialized outside the tree
// under review, and passes the rest as flags that bind an operator's config
// too. See golangciLint.Run and golangci.yml.
//
// The positions are part of the report, and the tree can rewrite those as well.
// A Go line directive above the offending function makes golangci-lint report
// real findings at a forged path; see positionsRewritten and goDirectiveLine.
// The detector reads the grammar rather than approximating it with a regular
// expression, which is a list of the spellings somebody thought of: CRLF line
// endings and a `*` in the block form's filename both walk past one.

// analyzerConfig is one analyzer's operator-supplied configuration, resolved
// and checked for containment once, at construction.
type analyzerConfig struct {
	// Ref is what to hand the analyzer: an absolute path outside the repository
	// under review, or, semgrep only, a registry reference. Empty means the
	// operator supplied none, which is the default.
	Ref string

	// Err is why a supplied configuration was refused.
	//
	// A runner holding one must not run AT all, rather than fall back to its
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
// construction, it reports Detect's reason otherwise. The refusal branch stays
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
// `--config auto` this replaced was a network fetch of rules nobody chose, and
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
// answer the same question about different objects, where did this come from,
// and the bug was that only one of them was being asked.
//
// It said "a link" and meant one kind of link. A HARD link is accepted: the same
// inode under a second name outside the repository passes, because EvalSymlinks
// resolves symlinks and a hard link is not one. Nothing here can see it without
// walking the repository comparing inodes, on every run, for a case a checkout
// cannot produce, git stores no hard links, so the tree under review cannot
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

// golangciDefaults is the golangci-lint configuration open-nitpick owns.
//
// The note behind it is in docs/runner-notes.md#golangcidefaults.
//
//go:embed golangci.yml
var golangciDefaults []byte

// golangciConventions is the ruleset a conformity measurement reads.
//
// Separate from the review's because the two ask different questions. See the
// file's own header.
//
//go:embed golangci-conventions.yml
var golangciConventions []byte

// writeGolangciDefaults materializes the review's ruleset outside the tree.
func writeGolangciDefaults(repoRoot string) (string, func(), error) {
	return writeGolangciConfig(repoRoot, golangciDefaults)
}

// WriteGolangciConventions materializes the conformity ruleset outside the
// repository and returns its path and a cleanup function.
//
// Exported because the conformity measurement lives beside this package and has
// to point the runner at a ruleset the review does not use.
func WriteGolangciConventions(repoRoot string) (string, func(), error) {
	return writeGolangciConfig(repoRoot, golangciConventions)
}

// writeGolangciConfig materializes one of our embedded rulesets and returns its
// path and a cleanup function.
//
// The file must not be written inside the repository under review, that would
// be this tool putting a config file into the tree and then reading policy out
// of the tree, which is the shape of the thing it is defending against, so the
// path is checked with resolveConfig, the same function that refuses an
// operator's config inside the repository. Deliberately the same function and
// not a second implementation of the rule: two copies of a containment check
// drift, and this one exists to prove our own file passes the test we impose on
// everybody else's.
//
// It can be defeated by an operator whose TMPDIR points inside the checkout, and
// that is precisely why the check is here rather than assumed: the answer is a
// refusal with a reason, not a config written into the tree.
func writeGolangciConfig(repoRoot string, body []byte) (string, func(), error) {
	dir, err := os.MkdirTemp("", "open-nitpick-golangci-")
	if err != nil {
		return "", nil, fmt.Errorf("create a directory for open-nitpick's analyzer config: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }

	file := filepath.Join(dir, "golangci.yml")
	if err := os.WriteFile(file, body, 0o600); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("write open-nitpick's analyzer config: %w", err)
	}

	resolved, err := resolveConfig(file, repoRoot)
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("open-nitpick's own analyzer config is not usable here: %w", err)
	}

	return resolved, cleanup, nil
}

// golangciLint runs golangci-lint over the changed Go packages.
type golangciLint struct{ cfg analyzerConfig }

func (g *golangciLint) Name() string { return "golangci-lint" }

// Detect reports why golangci-lint will not run, and nil when it will.
//
// THE BUG: it required go.mod at the CHECKOUT ROOT, so in any repository whose
// module lives in a subdirectory, backend/go.mod, services/api/go.mod, the Go
// analyzer never ran, for any change, and nothing in any diff showed it. No
// attack was needed; that was the default state of a monorepo. Detection
// now asks the question the run depends on: is each changed Go file
// inside a module this process can lint. See goTargets.
//
// The residual it used to describe, a change that DELETES go.mod silences this
// arm, is still real, and is still visible in the diff. What was wrong was the
// sentence next to it claiming that a silencing which REPORTS SUCCESS could only
// come from a config file. It can come from the tree: see findings.
func (g *golangciLint) Detect(_ context.Context, repoRoot string, files []string) error {
	if g.cfg.Err != nil {
		return g.cfg.Err
	}
	if len(filterExt(files, ".go")) == 0 {
		return errNoTargets
	}
	// The change's own shape is judged before the machine's: a change with
	// no module to analyze has that reason whether or not the binary is
	// installed, and the more specific reason is the one worth reporting.
	if len(goTargets(repoRoot, files)) == 0 {
		return errors.New("no go.mod at or above the changed Go files, so there is no module to analyze")
	}
	if !available("golangci-lint") {
		return notOnPath("golangci-lint")
	}
	return nil
}

// State says which configuration golangci-lint read. The isolated case names
// the config as well: "isolated" alone is true and incomplete, since a run
// isolated from the tree with no config of ours runs under golangci-lint's
// stock defaults, which is how a `// Code generated` line in the diff switches
// the analyzer off for that file. A reader has to tell "nothing configured
// this" from "open-nitpick configured this".
func (g *golangciLint) State() string {
	return g.cfg.state("isolated: open-nitpick's own analyzer config")
}

// golangciOutput is the part of golangci-lint's JSON report this package reads.
//
// Report.Error is in it because LEAVING IT OUT was THE BUG. golangci-lint
// reports a failure to analyze in the same envelope it reports issues in:
// {"Issues":[],"Report":{"Error":"typechecking error: ..."}}, printed on stdout
// alongside a non-zero exit. Declaring only Issues threw that sentence away, so
// an analysis that never ran decoded to zero findings and no error, identical
// to clean code, and a change could reach that state by adding a go.work that
// does not list the module, or a `//go:build ignore` line on the file it wanted
// unread. Verified against golangci-lint 2.8.0.
//
// Report.Error is not the only shape that failure takes, which is why
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

	// Before spending an analysis whose answers could not be trusted anyway.
	if reason := positionsRewritten(repoRoot, targets); reason != "" {
		return nil, errors.New(reason)
	}

	// Ours when the operator supplied none. Not "no config": --no-config leaves
	// golangci-lint's own defaults deciding what the tree can suppress, and
	// exclusions.generated is one of them.
	configRef := g.cfg.Ref
	if configRef == "" {
		file, cleanup, err := writeGolangciDefaults(repoRoot)
		if err != nil {
			// A failure to CONFIGURE the analyzer is reported as the analyzer not
			// running, never as a quieter run under whatever defaults were left:
			// falling back to --no-config here would restore the exact silencing
			// this config exists to close, at the moment nobody is watching.
			return nil, err
		}
		defer cleanup()
		configRef = file
	}

	// GOTOOLCHAIN=local, because go.mod is also part of the tree under review
	// and `toolchain go1.99.98` in it makes golangci-lint's package loader
	// DOWNLOAD and RUN that toolchain, code execution from the change, by a
	// different door than the config file. With it set the same tree fails with
	// "the Go language version ... is lower than the targeted Go version", exit
	// 3 and empty stdout, which runCommand reports as an error.
	//
	// That is a property of that failure and not of tree-induced load failures
	// in general: go.work and a build constraint break the load too and both
	// print a populated report with a non-zero exit. findings below is what
	// catches those.
	env := analyzerEnv(map[string]string{"GOTOOLCHAIN": "local"})

	var all []Finding
	for _, t := range targets {
		args := []string{"run", "--config", configRef}
		args = append(args, golangciReportArgs...)
		args = append(args, "--")
		args = append(args, t.Dirs...)

		out, exit, err := runCommand(ctx, repoRoot, filepath.Join(repoRoot, t.Module), "golangci-lint", env, args...)
		if err != nil {
			// The refusal above is deliberate and its message is not: an
			// operator reading "lower than the targeted Go version" in a
			// roster line has to know that the fix is a newer go where
			// nitpick runs, not a setting on the tree.
			if strings.Contains(err.Error(), "lower than the targeted Go version") {
				err = fmt.Errorf("%w (the go on PATH is older than go.mod asks for; GOTOOLCHAIN=local is set on purpose so the tree under review cannot choose the toolchain, so install the newer go where nitpick runs)", err)
			}
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
// The note behind it is in docs/runner-notes.md#findings.
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

// golangciReportArgs are the flags that decide how much of golangci-lint's
// report reaches this process, and how its positions are spelled.
//
// The note behind it is in docs/runner-notes.md#golangcireportargs.
var golangciReportArgs = []string{
	"--output.json.path", "stdout",
	"--issues-exit-code", "0",
	"--path-mode", "abs",
	"--max-same-issues", "0",
	"--max-issues-per-linter", "0",
	"--uniq-by-line=false",
}

// goDirectiveLine reports the 1-based line of the first Go line directive in
// src that moves a position, and 0 when nothing in the file does.
//
// IT ASKS THE TOOLCHAIN'S OWN LEXER, and that IS THE FIX RATHER THAN A DETAIL.
// What was here was a regular expression approximating the directive grammar,
// which is to say a list of the spellings somebody thought of. Two the grammar
// accepts were not on it, both honoured by go1.25.5 and both reproduced end to
// end, full report, forged positions, analyzer recorded as having run:
//
//   - CRLF LINE ENDINGS. The `//` branch anchored with `(?m)$`, which in Go's
//     regexp matches only before \n, so the \r of a CRLF file fell outside the
//     pattern. go/scanner strips exactly that \r from a // comment before
//     reading it as a directive, deliberately, to match the compiler on files
//     written on Windows. git stores CRLF verbatim, so a pull request needs no
//     .gitattributes to arrange one.
//   - A `*` IN THE BLOCK FORM'S FILENAME. The pattern spelled the filename
//     `[^*\r\n]*` to stop itself running past the closing `*/`, which also
//     forbade a character the grammar allows: a block comment ends at the FIRST
//     `*/`, so `/*line z*z.go:1*/` is one complete directive naming a file
//     called z*z.go.
//
// go/scanner supplies the grammar: `//line ` at the start of a line or
// `/*line ` anywhere, per its own `prefix`. It is also stricter in the
// direction that matters for false refusals. A trailing space in
// `//line z.go:1 ` makes it no directive, and a pattern refuses the file for
// it; `//line` inside a string literal is no comment at all, and a pattern
// cannot tell.
//
// It reports a directive only where the position it produces differs from the
// natural one, because a refusal has to be earned. `//line probe.go:4` on line
// 3 of probe.go renumbers nothing, and neither does a directive with no token
// after it.
func goDirectiveLine(name string, src []byte) int {
	fset := token.NewFileSet()
	f := fset.AddFile(name, fset.Base(), len(src))

	// A nil error handler: source that does not lex is golangci-lint's failure
	// to report, in its own words, and the scan continues past the error
	// regardless, a directive after a bad token still has to be seen.
	var s scanner.Scanner
	s.Init(f, src, nil, scanner.ScanComments)

	comment := 0
	for {
		pos, tok, _ := s.Scan()

		raw := f.PositionFor(pos, false)
		if adj := f.PositionFor(pos, true); adj.Filename != raw.Filename || adj.Line != raw.Line {
			// The directive is the comment read just before the first token
			// whose position moved: go/scanner applies the rewrite at the
			// offset FOLLOWING the comment, so the comment itself is still
			// reported at its true line.
			if comment > 0 {
				return comment
			}
			return raw.Line
		}
		if tok == token.EOF {
			return 0
		}
		if tok == token.COMMENT {
			comment = raw.Line
		}
	}
}

// positionsRewritten reports why golangci-lint's output cannot be anchored,
// scanning the packages that are about to be analyzed, and returns "" when it
// can.
//
// The note behind it is in docs/runner-notes.md#positionsrewritten.
func positionsRewritten(repoRoot string, targets []goTarget) string {
	for _, t := range targets {
		for _, d := range t.Dirs {
			dir := filepath.Join(repoRoot, filepath.FromSlash(t.Module), filepath.FromSlash(d))

			entries, err := os.ReadDir(dir)
			if err != nil {
				// Unreadable here means unreadable for golangci-lint too, which
				// reports its own failure; inventing one from a directory listing
				// would be this function guessing.
				continue
			}

			for _, e := range entries {
				if e.IsDir() || filepath.Ext(e.Name()) != ".go" {
					continue
				}

				src, err := os.ReadFile(filepath.Join(dir, e.Name()))
				if err != nil {
					continue
				}

				where := path.Join(t.Module, filepath.ToSlash(d), e.Name())

				line := goDirectiveLine(where, src)
				if line == 0 {
					continue
				}

				return fmt.Sprintf(
					"%s:%d carries a Go line directive, which rewrites the file and line golangci-lint "+
						"reports for everything after it; its findings cannot be anchored to this change, "+
						"and anchoring them anyway would comment on whichever file the directive names",
					path.Clean(where), line)
			}
		}
	}

	return ""
}

// covering is a runner that can name the parts of the change it did not
// analyze, for an analyzer that otherwise ran and reported.
//
// The note behind it is in docs/runner-notes.md#covering.
type covering interface {
	Uncovered(ctx context.Context, repoRoot string, files []string, diffs diff.Files) []review.LinterUncovered
}

// goBuildContext is the part of the go tool's configuration that decides what
// the analyzer's package loader will read.
//
// IT IS ASKED OF THE GO TOOL, and that is the whole point of the type. Both
// fields were read out of this process before, build.Default.CgoEnabled for
// cgo, a constant for the language floor, and both were wrong for the same
// reason: they answer a question about OUR process while the analysis happens in
// a child that resolves its configuration differently. cmd/go reads CGO_ENABLED
// from the environment and from the go env config file ($(go env GOENV), what
// `go env -w` writes and what a builder image ordinarily uses); go/build reads
// only os.Getenv and never that file. See cgoExcluded.
type goBuildContext struct {
	// CgoEnabled is the child's CGO_ENABLED, which decides whether a file
	// importing "C" is part of its package or dropped from it.
	CgoEnabled bool

	// Language is the language version of the toolchain that will load the
	// packages, in go/version's form ("go1.25"), or "" when it could not be
	// read. It is the ceiling on the version-gated checks any module here can
	// receive; see golangciLint.Uncovered.
	Language string
}

// goEnvironment asks the go tool for the build context the analyzer will run
// under.
//
// One invocation per review, in the environment the analyzer child is given,
// GOTOOLCHAIN=local included, so this reports the toolchain that will load the
// packages rather than one go.mod could ask to be downloaded. `go env` reads
// configuration and builds nothing, so it does not execute the tree the way
// `go list` would.
//
// A failure falls back to this process's own view, a path close to unreachable
// in a run that gets this far: golangci-lint's package loader shells out to the
// same go tool, so a missing or broken one has already produced a report
// findings() refuses, and Uncovered is asked only of an analyzer that ran.
func goEnvironment(ctx context.Context, repoRoot string) goBuildContext {
	fallback := goBuildContext{
		CgoEnabled: build.Default.CgoEnabled,
		Language:   version.Lang(runtime.Version()),
	}

	out, _, err := runCommand(ctx, repoRoot, repoRoot, "go",
		analyzerEnv(map[string]string{"GOTOOLCHAIN": "local"}), "env", "CGO_ENABLED", "GOVERSION")
	if err != nil {
		return fallback
	}

	got, ok := parseGoEnv(out)
	if !ok {
		return fallback
	}
	return got
}

// parseGoEnv reads the output of `go env CGO_ENABLED GOVERSION`.
//
// `go env` prints each named variable on its own line, in the order asked for.
// This splits by LINE rather than by field, which is not a stylistic choice:
// GOVERSION carries spaces on a development toolchain ("devel go1.26-abc123.
// .."), and a variable with no value prints an EMPTY line, so a whitespace
// split would slide the second answer into the first one's place and report the
// Go version as the cgo setting.
func parseGoEnv(out []byte) (goBuildContext, bool) {
	lines := strings.Split(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n")
	if len(lines) < 2 {
		return goBuildContext{}, false
	}

	got := goBuildContext{
		CgoEnabled: strings.TrimSpace(lines[0]) == "1",
		Language:   version.Lang(strings.TrimSpace(lines[1])),
	}
	if !version.IsValid(got.Language) {
		// A development toolchain reports a GOVERSION with no language version
		// in it. Empty rather than a guess, because the caller must be able to
		// tell "not known" from "known to be X", it names a module on a pull
		// request for the second.
		got.Language = ""
	}
	return got, true
}

// Uncovered names the parts of the change golangci-lint covered less of than
// its roster line implies: files its report says nothing about, files this
// review never offered it, the suppression directives this change added, and the
// modules whose declared language version narrowed the ruleset.
//
// The attack it closes, reproduced against golangci-lint 2.8.0: the
// package-load failures findings() catches are the loud shape of a tree
// silencing the Go analyzer, needing the constraint to empty the whole
// directory, because "build constraints exclude all Go files" fails the package
// rather than a file. Add one unconstrained sibling and the package loads while
// the changed file is never analyzed. Three spellings, all measured with a
// sibling present and one errcheck violation on twenty added lines, reach zero
// findings, exit 0 and the roster line "ran":
//
//	//go:build windows
//	//go:build ignore
//	a rename of app.go to app_windows.go, with no comment at all
//
// The last is the sharp one, needing no directive and reading as an ordinary
// platform-specific pull request: the code is live on another GOOS,
// `go build ./...` and the tests stay green on the CI GOOS, and the file gets
// no deterministic analysis while the roster reports a clean Go review.
//
// The answer is a named gap rather than a refusal, and that is deliberate.
// The report is not corrupt. It is accurate about the files it covered, so
// refusing it throws away real findings about the rest of the change, and a
// pull request touching foo_windows.go on a Linux runner is ordinary. What is
// needed is for something to say the file went unread. It uses go/build's own
// matcher rather than a constraint parser of ours, for the reason
// goDirectiveLine uses go/scanner: the name-suffix rule and the //go:build
// grammar are the toolchain's, and an approximation of them is a list of the
// cases somebody thought of.
//
// GOOS/GOARCH come from this process, which golangci-lint inherits with no
// build tags on top, so MatchFile answers the constraint question the run
// depends on. It cannot answer the cgo one: with cgo off the go tool drops a
// file importing "C" from the package and MatchFile still matches it, reading
// build constraints and filename suffixes but never the import list. A
// disclosed residual is not the reader of a review seeing it, so it is detected
// here; see cgoExcluded. Whether cgo is off is asked of the go tool rather than
// this process, see goEnvironment.
//
// A changed file with no module above it is the third shape, and the quietest,
// because nothing in this file was asked about it.
// goTargets drops such a file, correctly. There is no module to run
// golangci-lint in, and Detect turns that into a published reason only when NO
// changed Go file has a module. In a monorepo with backend/go.mod, a change
// touching backend/app.go and tools/evil.go analyzed the first and said nothing
// whatever about the second: one published finding, the roster saying the
// analyzer ran, and an empty coverage list. Partial coverage now names the part
// that was not covered.
//
// SUPPRESSION IS THE OTHER HALF, and it is here because it is the same fact
// about the same run: the analyzer read the file and was told to say nothing
// .golangci-lint has no flag that disables its own //nolint, so this cannot be
// prevented from outside the tree the way a config file can. It can only be
// counted. What made it worth counting is the SCOPE. A //nolint is not per-line:
// golangci-lint expands it to the declaration it is attached to, and attached to
// the package clause it covers the whole FILE. Measured: a one-line diff adding
// `//nolint:all` above `package probe` took a file with two pre-existing
// errcheck violations to zero findings, on lines the change never touched, with
// the roster saying the analyzer ran.
//
// Only directives the change ADDED are reported. A repository's existing nolint
// comments are its own policy and listing them on every pull request is how a
// notice gets collapsed and never opened again; a directive that arrived in this
// diff is the thing a reviewer has to weigh.
//
// A CHANGED FILE this REVIEW never OFFERED TO AN ANALYZER IS THE FOURTH, and it
// arrives from our own side of the fence rather than from the tree. reviewablePaths
// drops every path matching review.ignore before any runner is handed a path, and
// `**/vendor/**` and `**/testdata/**` are in the shipped defaults. Measured: a
// change touching app.go and vendor/token.go with an IDENTICAL errcheck violation
// in each published only app.go's, with the roster saying the analyzer ran and an
// empty coverage list, a changed Go file carrying a real violation that nothing
// looked for and nothing mentioned. Vendored code is compiled into the binary, so
// "nobody reviews vendor" is a policy about review effort, not about whether the
// code runs.
//
// It is reported only when NO ANALYZED PACKAGE COVERED THE FILE ANYWAY, because
// the ignore list decides which paths are SELECTED and golangci-lint analyzes
// whole package directories. Measured, same fixture: token.gen.go, token.pb.go
// and token_generated.go all match the default ignore list and all had their
// findings published, because app.go put their directory on the target list.
// Naming those would be a coverage gap that is not there, which is the same
// defect as missing one.
//
// And THE LAST ONE IS not ABOUT A FILE OF THE CHANGE AT all. Every entry above
// says a file went unread; a module's `go` directive says the file was read and
// part of the ruleset was not applied to it, which is a reduced analysis rather
// than an absent one. It is here because it is the same question, how much of
// this change did the analyzer cover, and it is the only answer to it
// that a reader cannot reach by opening the diff. See analyzedLanguageCeiling.
func (g *golangciLint) Uncovered(ctx context.Context, repoRoot string, files []string, diffs diff.Files) []review.LinterUncovered {
	var out []review.LinterUncovered

	// Once per review, and asked of the go tool rather than of this process:
	// both the cgo question and the language-version ceiling are decided in the
	// child, and answering them from build.Default and a constant is what two
	// separate silencings went through. See goEnvironment.
	goEnv := goEnvironment(ctx, repoRoot)

	// The modules that own at least one changed Go file, in first-seen order.
	// They are collected here rather than taken from goTargets so that the loop
	// which decides "this file had no module" and the loop which reads each
	// module's go.mod cannot disagree about which modules were involved.
	seen := map[string]bool{}
	var modules []string

	for _, f := range filterExt(files, ".go") {
		rel := filepath.ToSlash(f)
		full := filepath.Join(repoRoot, filepath.FromSlash(f))

		module, owned := goModuleFor(repoRoot, goPackageDir(rel))
		if !owned {
			out = append(out, review.LinterUncovered{
				Linter: g.Name(), Path: rel, Reason: review.UncoveredNoModule,
			})
			// Nothing below applies: a file no invocation was given cannot be
			// build-excluded from an analysis that never included it, and its
			// comments suppress nothing.
			continue
		}
		if !seen[module] {
			seen[module] = true
			modules = append(modules, module)
		}

		// (false, err) is a file we could not read, which is not the same claim
		// as a file the build excludes; golangci-lint reports its own read
		// failures and inventing one here would be a guess.
		switch match, err := build.Default.MatchFile(filepath.Dir(full), filepath.Base(full)); {
		case err != nil:
			continue
		case !match:
			out = append(out, review.LinterUncovered{
				Linter: g.Name(), Path: rel, Reason: review.UncoveredBuildExcluded,
			})
			// No suppression scan: nothing read the file, so what its comments
			// say about the analyzer is beside the point.
			continue
		}

		if cgoExcluded(full, goEnv.CgoEnabled) {
			out = append(out, review.LinterUncovered{
				Linter: g.Name(), Path: rel, Reason: review.UncoveredCgoDisabled,
			})
			continue
		}

		file := diffs.Find(rel)
		if file == nil {
			continue
		}

		src, err := os.ReadFile(full)
		if err != nil {
			continue
		}
		for _, line := range goNolintLines(rel, src) {
			if !file.IsChangedLine(line) {
				continue
			}
			out = append(out, review.LinterUncovered{
				Linter: g.Name(), Path: rel, Line: line, Reason: review.UncoveredSuppressed,
			})
		}
	}

	out = append(out, g.notSelected(repoRoot, files, diffs)...)

	// Per MODULE and not per file, because that is the scope of the fact: one
	// `go` line decides which version-gated checks ran over every package under
	// it. Reported whether or not the change touched go.mod, unlike a //nolint,
	// which is listed only when the change added it. The two are different
	// because of volume and because of visibility: a repository can hold hundreds
	// of pre-existing nolint comments, where this is one entry per module, and a
	// reader can see a //nolint by opening the file the review already points at,
	// while a `go` directive three directories up is nowhere in the diff.
	for _, module := range modules {
		mod := path.Join(module, "go.mod")

		declared, line, ok := gomod.LanguageVersion(filepath.Join(repoRoot, filepath.FromSlash(mod)))
		if !ok || !gomod.BelowAnalyzed(declared, goEnv.Language) {
			continue
		}
		out = append(out, review.LinterUncovered{
			Linter: g.Name(), Path: mod, Line: line, Reason: review.UncoveredLanguageVersion,
		})
	}

	return out
}

// notSelected names the changed Go files this review never handed to an
// analyzer, and that no analyzed package covered anyway.
//
// files is what the runner was given, reviewablePaths' output, with review.ignore
// already applied, and diffs is the whole change, so the difference between them
// is precisely what the review withheld. Two things put a path there: the ignore
// list, which is the ordinary cause, and safePaths, which withholds a path an
// analyzer would read as a flag. Neither is written by the change (a change may
// not supply the policy it is reviewed under), which is why this reads as an
// entry about the review rather than an accusation about the diff, and why the
// reason string names neither cause as though it were the only one.
//
// The directory check is what keeps the entries true. golangci-lint is given
// package directories, so an ignored file sharing one with a selected file IS
// analyzed: measured, token.gen.go beside app.go had its errcheck violation
// published. Only a file in a directory nothing analyzed went unread.
//
// Deletions and files with no added lines are skipped. There is nothing left to
// analyze in the first, and no line of the second belongs to this change, so
// silence about either is not a gap this change opened.
func (g *golangciLint) notSelected(repoRoot string, files []string, diffs diff.Files) []review.LinterUncovered {
	selected := map[string]bool{}
	for _, f := range files {
		selected[filepath.ToSlash(f)] = true
	}

	analyzed := map[string]bool{}
	for _, t := range goTargets(repoRoot, files) {
		for _, dir := range t.Dirs {
			analyzed[path.Join(t.Module, strings.TrimPrefix(dir, "./"))] = true
		}
	}

	var out []review.LinterUncovered
	for _, f := range diffs {
		rel := filepath.ToSlash(f.Path)
		switch {
		case path.Ext(rel) != ".go", selected[rel],
			f.Binary, f.Kind == diff.ChangeDeleted, len(f.ChangedLines()) == 0,
			analyzed[goPackageDir(rel)]:
			continue
		}
		out = append(out, review.LinterUncovered{
			Linter: g.Name(), Path: rel, Reason: review.UncoveredNotSelected,
		})
	}
	return out
}

// cgoExcluded reports whether the go tool drops this file from its package
// because it imports "C" while cgo is off.
//
// The note behind it is in docs/runner-notes.md#cgoexcluded.
func cgoExcluded(file string, cgoEnabled bool) bool {
	if cgoEnabled {
		return false
	}

	parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
	if err != nil {
		return false
	}
	for _, spec := range parsed.Imports {
		// Unquoted rather than compared as a literal: the spec makes an import
		// path any string literal, so `C` in back quotes is the same import and
		// a comparison against "\"C\"" would miss it.
		if p, err := strconv.Unquote(spec.Path.Value); err == nil && p == "C" {
			return true
		}
	}
	return false
}

// goNolintLines returns the lines carrying a golangci-lint nolint directive.
//
// It lexes rather than matching lines, so that a string literal or prose QUOTING
// `//nolint`, which this repository's own tests, README and this comment all do,
// is not published as a suppression somebody added.
//
// The acceptance rule is measured against golangci-lint 2.8.0 rather than
// guessed, because guessing here fails in the direction that puts a wrong
// sentence on a pull request. Its nolint processor trims leading `/` and spaces
// and requires what remains to begin with `nolint` followed by nothing, a `:`,
// or a SPACE. Suppressing: `//nolint`, `//nolint:errcheck`, `// nolint`, and,
// the one worth knowing, `// nolint is not used in this package.`, an ordinary
// English sentence at column 1 above the package clause that silently turns off
// the whole file. Not suppressing: `//nolintlint`, `//nolint-ish`,
// `//nolint,errcheck`, a TAB after the word, `//NOLINT`, and the block form,
// because trimming `/*nolint*/` leaves a `*`.
//
// THE RESIDUAL, which is an over-report and is therefore the safe direction:
// golangci-lint also validates the linter NAMES, so `//nolint:nosuchlinter` and
// a bare `//nolint:` suppress nothing and are still reported here. Checking that
// would mean shipping a copy of golangci-lint's linter registry and keeping it
// in step with the binary on PATH; naming a typo'd suppression the author
// intended to work is a better failure than missing a real one.
func goNolintLines(name string, src []byte) []int {
	fset := token.NewFileSet()
	f := fset.AddFile(name, fset.Base(), len(src))

	var s scanner.Scanner
	s.Init(f, src, nil, scanner.ScanComments)

	var out []int
	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			return out
		}
		if tok != token.COMMENT {
			continue
		}

		rest, ok := strings.CutPrefix(strings.TrimLeft(lit, "/ "), "nolint")
		if !ok || (rest != "" && rest[0] != ':' && rest[0] != ' ') {
			continue
		}
		// Unadjusted: a file carrying a line directive is refused before this
		// runs, and a line number a reviewer cannot find in the file is worse
		// than none.
		out = append(out, f.PositionFor(pos, false).Line)
	}
}

// typecheckLinter is the name golangci-lint puts on a compile failure it
// reports through the issue channel.
//
// It is not a linter and cannot be switched off: it is how the package loader
// says the code could not be type-checked, which is also the state in which
// every type-based linter, errcheck, staticcheck, govet, gosec, produced
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
// code was never analyzed at all, and the report said the binary might be
// missing.
//
// Files with no go.mod above them are dropped rather than guessed at: there is
// no module to run in, and inventing one gets "no go files to analyze" with an
// empty report.
//
// Detect does not cover that case on its own. It reports only when every
// changed Go file lands in it, under the whole-analyzer reason "no go.mod at or
// above the changed Go files". A monorepo makes the partial case ordinary, and
// the partial case is the dangerous one: with backend/go.mod present, a change
// touching backend/app.go and tools/evil.go analyzes the first, publishes its
// finding, and says nothing about the second. That is the bare-continue shape
// Set.normalize was fixed for, one directory over, and the answer is the same.
// The file is named in golangciLint.Uncovered rather than dropped.
func goTargets(repoRoot string, files []string) []goTarget {
	byModule := map[string][]string{}
	var order []string

	for _, f := range filterExt(files, ".go") {
		dir := goPackageDir(filepath.ToSlash(f))

		module, ok := goModuleFor(repoRoot, dir)
		if !ok {
			// Dropped because there is nowhere to run: inventing a module gets
			// "no go files to analyze" and an empty report. It is not dropped
			// SILENTLY, golangciLint.Uncovered asks the same question of the
			// same files and names every one that lands here, which is what was
			// missing while this was a bare continue.
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

// goPackageDir reduces a repository-relative Go file to the package directory
// holding it, with "" for the repository root.
//
// Shared by goTargets and golangciLint.Uncovered so that the two cannot disagree
// about which module owns a file. They have to answer identically: one decides
// what is analyzed and the other decides what is REPORTED as not analyzed, and a
// file both of them skip is the gap this whole mechanism exists to close.
func goPackageDir(file string) string {
	dir := path.Dir(file)
	if dir == "." {
		return ""
	}
	return dir
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
// The note behind it is in docs/runner-notes.md#repopath.
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
// THE ROOT, which had golangci-lint's monorepo defect, services/api/pyproject.
// toml meant no Python was ever linted, and bought nothing: ruff --isolated
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

	// RUFF_OUTPUT_FILE redirects the report to a file, leaving stdout empty,
	// verified against ruff 0.16.1, where a run with real findings produced zero
	// bytes on stdout. RUFF_OUTPUT_FORMAT did not override the explicit
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
// The note behind it is in docs/runner-notes.md#detect.
func (e *eslint) Detect(_ context.Context, repoRoot string, files []string) error {
	if e.cfg.Err != nil {
		return e.cfg.Err
	}
	if len(filterExt(files, eslintExts...)) == 0 {
		return errNoTargets
	}
	if e.cfg.Ref == "" {
		return errors.New(eslintUnconfigured)
	}
	// The same resolution Run uses, so a node_modules/.bin/eslint the tree
	// put on PATH is refused here, in the roster, and not first at Run.
	if _, err := resolveBinary("eslint", repoRoot); err != nil {
		return err
	}
	return nil
}

func (e *eslint) State() string { return e.cfg.state(eslintUnconfigured) }

// eslintUnconfigured is one sentence in one place, because Detect, Run and the
// published status must not be able to describe this state differently.
const eslintUnconfigured = "not configured; set linters.eslint_config to a config outside the repository"

// eslintExts are the extensions eslint is given. A change containing none of
// them is a change with no JavaScript in it, rather than a degraded run.
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

	// --config alone already suppresses the tree's eslint.config.js, verified
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

// Detect requires an operator-supplied config and reads nothing from the tree.
//
// Returning true because .semgrep.yml, .semgrep.yaml, .semgrepignore or
// .semgrep exists in the head tree lets a pull request that adds one switch
// semgrep on, with rules the pull request wrote, in the run reviewing it. That
// is enablement rather than silencing, and no severity ceiling or ignore list
// touches it. Keying on operator configuration means no file in the tree
// decides whether an analyzer runs.
//
// .semgrepignore needs no flag: explicit file targets bypass it, verified
// against semgrep 1.172.0 with the target file listed in .semgrepignore. It was
// only ever a detection trigger here.
func (s *semgrep) Detect(_ context.Context, _ string, files []string) error {
	if s.cfg.Err != nil {
		return s.cfg.Err
	}
	// semgrep is multi-language and takes whatever it is given, so an empty
	// list is the only case with nothing to analyze.
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
// gives {"results":[],"errors":[{"type":"Rule parse error",...}]} with exit 2,
// verified against semgrep 1.172.0, and reading only results turned that into a
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
// round, errors[] deciding. A benign entry would fail the whole analyzer, and
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
// The note behind it is in docs/runner-notes.md#mapseverity.
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
//
// It retries with both sides symlink-resolved because a repository root reached
// through a link, /tmp on macOS is /private/tmp, and CI checkouts are routinely
// symlinked, does not textually prefix the real path an analyzer prints. The
// first attempt would then produce "../../real/path", every finding would fail
// files.Find, and the whole analyzer's output would be discarded on a machine
// where nothing is wrong. golangci-lint is asked for absolute paths on purpose
// (see golangciReportArgs), so this is now the only step between its report and
// the diff.
func relative(repoRoot, path string) string {
	if rel, ok := relativeTo(repoRoot, path); ok {
		return rel
	}

	root, rootErr := filepath.EvalSymlinks(repoRoot)
	resolved, pathErr := filepath.EvalSymlinks(path)
	if rootErr == nil && pathErr == nil {
		if rel, ok := relativeTo(root, resolved); ok {
			return rel
		}
	}

	return filepath.ToSlash(path)
}

// relativeTo returns path relative to root, and false when it is not under it.
func relativeTo(root, path string) (string, bool) {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// decodeJSON decodes an analyzer's JSON payload out of its stdout, and
// REQUIRES
// one.
//
// The note behind it is in docs/runner-notes.md#decodejson.
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
