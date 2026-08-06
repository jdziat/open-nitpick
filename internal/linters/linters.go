// Package linters runs deterministic analyzers over the changed files and
// normalizes their output into review findings.
//
// Linter output is evidence, not a verdict: it is merged with the model's
// findings and triaged alongside them, so the model can explain impact and
// drop noise rather than the tool dumping raw lint output into a pull request.
package linters

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/diff"
	"github.com/jdziat/open-nitpick/internal/review"
)

// Finding is one analyzer result, before normalization into a review finding.
type Finding struct {
	Path     string
	Line     int
	Rule     string
	Message  string
	Severity config.Severity

	// RawSeverity is the token the ANALYZER printed, whenever it printed one.
	//
	// It is recorded even when the spelling coincides with one of our level
	// names, because Severity above is still read on OUR scale: semgrep's ERROR
	// is a level in semgrep's vocabulary — its own documentation makes it a
	// synonym for HIGH — and has not thereby adopted this project's meaning.
	// The operator's ceiling can move the level afterwards anyway.
	//
	// Empty means the analyzer published no severity at all and this package
	// chose one — ruff is the case, and there the level is entirely ours.
	// Either way Severity is not a quotation, which is why every runner that
	// fills this in also sets review.Finding.SeverityTranslated downstream.
	RawSeverity string
}

// Runner is one analyzer.
type Runner interface {
	// Name identifies the runner in configuration and logs.
	Name() string

	// Detect reports why this analyzer will not run over the given changed
	// files, and nil when it will. errNoTargets means the change contains
	// nothing it reads, which is not a degradation.
	//
	// IT RETURNS THE REASON RATHER THAN A BOOL because the caller was inventing
	// one. A false used to be reported to the operator as "its binary is not on
	// PATH, or this repository has none of the files it looks for" — a guess
	// between two causes, printed where the real cause (a config refused, a
	// module the old detection could not see, no Python in a Go change) was
	// already known here and thrown away.
	Detect(ctx context.Context, repoRoot string, files []string) error

	// Run analyzes the given repository-relative files.
	Run(ctx context.Context, repoRoot string, files []string) ([]Finding, error)
}

// errNoTargets is Detect's answer when the change contains no files this
// analyzer reads.
//
// It is a distinct answer from "it could not run" because the two are different
// facts about the review and only one of them is a degradation: ruff sitting out
// a Go-only change is not a Python review that went missing, and reporting it as
// one both fails strict mode for nothing and teaches a reader to skip the block
// where real absences are announced.
var errNoTargets = errors.New("the change contains no files it analyzes")

// notOnPath is the reason an analyzer whose binary is missing did not run.
func notOnPath(name string) error {
	return fmt.Errorf("%s is not on PATH", name)
}

// stateful is a runner that can describe where its configuration came from.
//
// It is separate from Runner so that a test double is not forced to have an
// opinion about analyzer configuration, and because the string is for a human
// reading the run's report rather than for anything in this package's logic.
type stateful interface{ State() string }

// Set runs the configured analyzers. It implements review.LinterRunner.
type Set struct {
	repoRoot string
	cfg      *config.Config
	log      *slog.Logger
	runners  []Runner

	mu        sync.Mutex
	statuses  []review.LinterStatus
	discarded []review.LinterDiscard
	uncovered []review.LinterUncovered
}

// New builds the analyzer set described by the configuration.
func New(repoRoot string, cfg *config.Config, log *slog.Logger) *Set {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	available := map[string]Runner{}
	for _, r := range builtins(repoRoot, cfg) {
		available[r.Name()] = r
	}

	var selected []Runner
	for _, name := range cfg.Linters.Enabled {
		r, ok := available[strings.TrimSpace(name)]
		if !ok {
			log.Warn("unknown linter in configuration", "name", name)
			continue
		}
		selected = append(selected, r)
	}

	return &Set{repoRoot: repoRoot, cfg: cfg, log: log, runners: selected}
}

// builtins lists the analyzers shipped with open-nitpick, each holding its
// operator-supplied configuration already resolved against repoRoot.
//
// Resolution happens once, here, rather than per Run: whether a configuration is
// outside the repository is a property of the operator's setting and the
// checkout, not of the file list, and deciding it at construction is what lets
// Detect refuse a runner whose configuration was rejected.
func builtins(repoRoot string, cfg *config.Config) []Runner {
	return []Runner{
		&golangciLint{cfg: fileConfig(repoRoot, cfg.Linters.GolangciConfig)},
		&ruff{cfg: fileConfig(repoRoot, cfg.Linters.RuffConfig)},
		&eslint{cfg: fileConfig(repoRoot, cfg.Linters.ESLintConfig)},
		&semgrep{cfg: semgrepConfig(repoRoot, cfg.Linters.SemgrepConfig)},
	}
}

// Statuses reports how each configured analyzer was set up and whether it ran.
// It is populated by Run and empty before it.
//
// It exists because analyzer isolation is a DEGRADATION a reader has to be told
// about, in the same way .nitpick.yaml substitution is: under the default no
// analyzer reads the repository's own lint settings, and eslint and semgrep do
// not run at all. A review that quietly ran less than the operator believes is
// the failure mode this whole change is about, so it must not be reproduced by
// the fix.
//
// "Your linter did not run" must never be only a slog.Warn in a CI log — and for
// one release it was only a Fprintf to the same CI log, which is the letter of
// that sentence and not its point. The reader who has to know is the one reading
// the pull request, so review.Render publishes these; see linterNotice.
func (s *Set) Statuses() []review.LinterStatus {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := append([]review.LinterStatus(nil), s.statuses...)

	// By name, because the recording order is whichever goroutine finished
	// first. This ends up in a published comment, and a block that reshuffles
	// itself between runs reads as something having changed when nothing did.
	slices.SortFunc(out, func(a, b review.LinterStatus) int {
		return strings.Compare(a.Linter, b.Linter)
	})
	return out
}

// Discarded reports the findings an analyzer produced that this review did not
// publish, and why. It is populated by Run and empty before it.
//
// It exists because Set.normalize used to drop them with a bare `continue`: no
// counter, no log, no status. That single line was the sink for the line
// directive attack — golangci-lint reports real findings at a forged path, and
// they arrive here as "a path not in the diff" — and it was also where the
// opt-in analyzer config lost EVERY finding, because an operator config outside
// the repository made golangci-lint print paths relative to that config's
// directory.
//
// Neither of those looked like anything. A finding the reviewer produced and
// this tool discarded is the class this project keeps shipping, so it is counted
// and published for the same reason Plan.Skipped and Plan.Degraded are: silence
// from a review that ran less than you think is indistinguishable from silence
// from clean code.
//
// This is NOT the whole published list. review.Engine's anchor filter runs after
// normalize and drops analyzer findings of its own — it was found doing so
// silently, downstream of this fix and with the same three symptoms — so the
// engine merges its drops into the same block. See review.SortDiscards.
func (s *Set) Discarded() []review.LinterDiscard {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Sorted for the reason Statuses is: the recording order follows whichever
	// analyzer goroutine finished first, and this ends up in a published
	// comment. A block that reshuffles itself between runs reads as something
	// having changed when nothing did. The comparator lives in review because
	// review.Engine has to merge its OWN drops into the same list before it is
	// rendered, and two orderings for one published block is a bug waiting.
	out := append([]review.LinterDiscard(nil), s.discarded...)
	review.SortDiscards(out)
	return out
}

// Uncovered reports the parts of the change an analyzer ran over and said
// nothing about because the tree arranged for it not to look. It is populated by
// Run and empty before it.
//
// It exists because "the analyzer ran" and "the analyzer read the change" are
// different claims, and only the first one was being published. A build
// constraint on the changed file with an ordinary sibling beside it, or a
// //nolint on the package clause, both produce a run with zero findings, a nil
// error and a roster line saying golangci-lint ran — which is what a clean Go
// review looks like. See golangciLint.Uncovered.
func (s *Set) Uncovered() []review.LinterUncovered {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := append([]review.LinterUncovered(nil), s.uncovered...)
	slices.SortFunc(out, func(a, b review.LinterUncovered) int {
		return cmp.Or(
			cmp.Compare(a.Reason, b.Reason),
			cmp.Compare(a.Path, b.Path),
			cmp.Compare(a.Line, b.Line),
			cmp.Compare(a.Linter, b.Linter),
		)
	})
	return out
}

// record stores one analyzer's outcome for Statuses.
func (s *Set) record(linter string, outcome review.LinterOutcome, state string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.statuses = append(s.statuses, review.LinterStatus{
		Linter:  linter,
		Outcome: outcome,
		State:   state,
	})
}

// state describes a runner's configuration, for runners that have one.
func state(r Runner) string {
	if s, ok := r.(stateful); ok {
		return s.State()
	}
	return "configured"
}

// Run analyzes the changed files and returns findings as review findings.
//
// Analyzers run concurrently and independently: one that is missing, times
// out, or crashes is logged and skipped, because losing the entire review over
// a broken linter is a bad trade.
func (s *Set) Run(ctx context.Context, files diff.Files) ([]review.Finding, error) {
	if s.cfg.Linters.Mode == config.LinterOff || len(s.runners) == 0 {
		return nil, nil
	}

	paths, rejected := safePaths(reviewablePaths(s.cfg, files))
	for _, p := range rejected {
		// A path an analyzer would read as a flag. Reported rather than
		// silently dropped, since it is more likely an attack than an accident.
		s.log.Warn("skipping path that would be parsed as an analyzer flag", "path", p)
	}
	if len(paths) == 0 {
		return nil, nil
	}

	var (
		mu   sync.Mutex
		all  []Finding
		errs []error
		wg   sync.WaitGroup
	)

	for _, r := range s.runners {
		if err := r.Detect(ctx, s.repoRoot, paths); err != nil {
			// The runner's OWN reason, never a reconstruction. What was here
			// sniffed the runner's state string for "not configured" and
			// replaced everything else with a guess between a missing binary
			// and missing project files — so the commonest real cause, a
			// module the detection could not see, was reported as one of two
			// things that were not true.
			reason := oneLine(err.Error())

			if errors.Is(err, errNoTargets) {
				// Not a degradation, so it is recorded and nothing else: an
				// analyzer with nothing to read has not gone missing, and
				// failing strict mode over it would make strict unusable in
				// every repository that is not polyglot.
				s.record(r.Name(), review.LinterSkipped, reason)
				continue
			}

			s.record(r.Name(), review.LinterFailed, reason)

			if s.cfg.Linters.Mode == config.LinterStrict {
				mu.Lock()
				errs = append(errs, fmt.Errorf("linter %s is enabled but not available: %s", r.Name(), reason))
				mu.Unlock()
			} else {
				s.log.Warn("linter did not run", "linter", r.Name(), "reason", reason)
			}
			continue
		}

		wg.Add(1)
		go func(r Runner) {
			defer wg.Done()

			runCtx := ctx
			if s.cfg.Linters.Timeout > 0 {
				var cancel context.CancelFunc
				runCtx, cancel = context.WithTimeout(ctx, s.cfg.Linters.Timeout)
				defer cancel()
			}

			found, err := r.Run(runCtx, s.repoRoot, paths)

			// Asked only of a runner that RAN — an analyzer already recorded as
			// failed has told the reader more than a coverage note would — and
			// asked whether or not it found anything, because an analyzer that
			// reported nothing about a file it never read is exactly the state
			// this answers.
			//
			// Outside the lock below, because it reads files: the mutex
			// serializes the result tails of every analyzer in the run, and
			// holding it across file I/O would make each analyzer wait on the
			// last one's directory reads.
			var gaps []review.LinterUncovered
			if c, ok := r.(covering); ok && err == nil {
				gaps = c.Uncovered(s.repoRoot, paths, files)
			}

			mu.Lock()
			defer mu.Unlock()

			if err != nil {
				s.record(r.Name(), review.LinterFailed, oneLine(err.Error()))
				s.log.Warn("linter failed", "linter", r.Name(), "error", err)
				if s.cfg.Linters.Mode == config.LinterStrict {
					errs = append(errs, fmt.Errorf("linter %s: %w", r.Name(), err))
				}
				return
			}

			s.record(r.Name(), review.LinterRan, state(r))
			s.log.Debug("linter finished", "linter", r.Name(), "findings", len(found))
			s.uncover(gaps)

			for _, f := range found {
				f.Rule = prefixRule(r.Name(), f.Rule)
				all = append(all, f)
			}
		}(r)
	}

	wg.Wait()

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	return s.normalize(all, files), errors.Join(errs...)
}

// normalize converts analyzer findings into review findings, dropping those
// that cannot be anchored to the change AND RECORDING EVERY ONE IT DROPS.
//
// What was here was three bare `continue` statements. They are the correct
// behaviour — a comment cannot be published on a line the forge will not accept
// — and they were the wrong accounting: an analyzer finding entered this
// function and nothing anywhere said it had left. Two live defects hid in that
// gap, one of them an attack (a line directive forging the reported path) and
// one of them our own (an operator's config making every path unresolvable), and
// both presented as a review that ran, reported no findings, and looked clean.
//
// So every drop is now counted, named with the reason it was dropped, and
// carried out to the report. The reasons are separated because they are
// different facts: two of them are this repository's own publication policy
// working as configured, and one of them is an analyzer describing a file that
// is not in this checkout, which is not a drop at all but evidence. See
// reasonForUnknownPath.
func (s *Set) normalize(found []Finding, files diff.Files) []review.Finding {
	out := make([]review.Finding, 0, len(found))

	for _, f := range found {
		file := files.Find(f.Path)
		if file == nil {
			s.discard(f, s.reasonForUnknownPath(f.Path))
			continue
		}

		// A pull request review should discuss what the pull request did.
		// Pre-existing lint debt on untouched lines is somebody else's problem
		// and reporting it is the fastest way to get the bot switched off.
		if s.cfg.Linters.OnlyChangedLines && !file.IsChangedLine(f.Line) {
			s.discard(f, review.DiscardUnchangedLine)
			continue
		}
		if _, ok := file.Position(f.Line); !ok {
			s.discard(f, review.DiscardUnanchorable)
			continue
		}

		severity := f.Severity
		if !severity.Valid() || severity == config.SeverityNone {
			severity = config.SeverityWarning
		}

		// The operator's ceiling, applied here so that TRIAGE is not shown a
		// level the operator has already declined — a triage walkthrough calling
		// something critical while the published comment says warning would be
		// this project disagreeing with itself in one report.
		//
		// It is NOT true that no model is ever shown a declined level: this
		// comment said so and was false. The expert validation pass reads
		// Finding.Severity in validationRequest, downstream of this and of
		// triage, so with a ceiling of warning and a triage that raises to
		// critical the expert prompt reads "Claimed severity: critical". Every
		// assertion about what a model was shown inspected the triage prompt
		// only, which is why the claim survived. Narrowed to what is verified;
		// capping before the expert is a separate change with its own test.
		//
		// This application does not bind. Triage and the expert pass both run
		// downstream and both may raise, so review.Engine applies the same
		// ceiling again after them; that is the one that decides the gate. See
		// Engine.capAnalyzerFindings.
		severity = s.cfg.Linters.CapSeverity(severity)

		// EVERY analyzer finding's severity is this project's word, including
		// the ones whose spelling happens to match ours. A shared spelling is not
		// a shared scale: semgrep's ERROR is a rule author's judgement inside
		// semgrep's own four-level vocabulary — where it is a synonym for HIGH,
		// not for this project's error — and the ceiling above can move it again
		// afterwards. Marking them all is therefore correct rather than
		// conservative, and it is what stops a report captioning "semgrep said
		// error" over a word semgrep never printed. RawSeverity carries the
		// original where there was one.
		out = append(out, review.Finding{
			Path:               f.Path,
			Line:               f.Line,
			Severity:           string(severity),
			SeverityTranslated: true,
			RawSeverity:        f.RawSeverity,
			FromAnalyzer:       true,
			Category:           "lint",
			Class:              string(classForRule(f.Rule)),
			Title:              strings.TrimSpace(f.Message),
			Rationale:          fmt.Sprintf("Reported by %s.", f.Rule),
			Source:             f.Rule,
		})
	}

	return out
}

// discard records one analyzer finding this review produced and did not
// publish.
//
// It logs as well as counting. The complaint against the bare `continue` was
// three things — no counter, no log, no status — and a reader debugging a
// missing finding reaches for the log first, while the reader who never knew a
// finding existed is reached only by the status.
func (s *Set) discard(f Finding, reason review.DiscardReason) {
	s.mu.Lock()
	s.discarded = append(s.discarded, review.LinterDiscard{
		Rule:   f.Rule,
		Path:   f.Path,
		Line:   f.Line,
		Reason: reason,
	})
	s.mu.Unlock()

	s.log.Info("analyzer finding not published",
		"rule", f.Rule, "path", f.Path, "line", f.Line, "reason", reason)
}

// uncover records the parts of the change an analyzer did not cover.
//
// It logs for the reason discard does: a maintainer wondering why the Go review
// said nothing about their new _windows.go file reaches for the log first, and
// the reader who never knew the file went unread is reached only by the report.
func (s *Set) uncover(gaps []review.LinterUncovered) {
	if len(gaps) == 0 {
		return
	}

	s.mu.Lock()
	s.uncovered = append(s.uncovered, gaps...)
	s.mu.Unlock()

	for _, g := range gaps {
		s.log.Info("analyzer covered less of the change than it ran over",
			"linter", g.Linter, "path", g.Path, "line", g.Line, "reason", g.Reason)
	}
}

// reasonForUnknownPath decides which of the two "not in the diff" answers a
// path deserves, and IT IS THE ONE DECISION HERE THAT IS NOT BOOKKEEPING.
//
// A path that is not in the change is ordinary. Go is analyzed a package at a
// time, so golangci-lint routinely reports on a sibling file the change never
// touched, and dropping those is what only_changed_lines is for.
//
// A path that is not in the CHECKOUT is not ordinary and is not a lint result at
// all: the analyzer was made to describe a file that does not exist. The only
// way to reach it from a Go tree is a line directive, and the same directive
// pointed at a real file relocates a finding onto code the change did not write
// — so this is the visible half of a thing whose invisible half puts this bot's
// name on an accusation about somebody else's line.
//
// WHAT IS DONE WITH IT, AND WHY THAT AND NOT MORE. It is recorded under its own
// reason and published, rather than being turned into a finding of its own or
// used to fail the analyzer from here. Two reasons. Publishing it as a finding
// would mean anchoring it, and the only honest anchor is the file carrying the
// directive, which this function cannot see: it holds a forged path and nothing
// else. And failing the analyzer at this point would be late and partial —
// Set.Run has already recorded the analyzer as having run, and the SILENCING
// variant of the attack produces no findings for this function to inspect at
// all. The analyzer has to refuse before it reports, which is where the refusal
// now is; see positionsRewritten. This is the backstop that names it if one
// arrives anyway — from an analyzer with no such check, or along a path nobody
// has thought of yet — and a named, published count is the minimum that makes
// such a run distinguishable from a clean one.
func (s *Set) reasonForUnknownPath(reported string) review.DiscardReason {
	if reported == "" {
		return review.DiscardPathNotInCheckout
	}

	// Cleaned and containment-checked before touching the filesystem: the path
	// is analyzer output, so "../../etc/passwd" is a thing it can say, and this
	// must not become a way to ask whether an arbitrary absolute path exists.
	clean := path.Clean(filepath.ToSlash(reported))
	if path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
		return review.DiscardPathNotInCheckout
	}

	if _, err := os.Lstat(filepath.Join(s.repoRoot, filepath.FromSlash(clean))); err != nil {
		return review.DiscardPathNotInCheckout
	}
	return review.DiscardNotInChange
}

// classForRule maps an analyzer rule to a finding class.
//
// Without this every linter finding arrived classless and normalized to
// maintainability — which nitpick=off and nitpick=minimal do not publish. A
// gosec or semgrep security result would have been silently dropped and the
// build left green, which is the opposite of what a security linter is for.
func classForRule(rule string) config.Class {
	r := strings.ToLower(rule)

	switch {
	case strings.Contains(r, "gosec"), strings.Contains(r, "semgrep"),
		strings.Contains(r, "security"), strings.Contains(r, "bandit"),
		strings.Contains(r, "injection"), strings.Contains(r, "crypto"):
		return config.ClassSecurity

	case strings.Contains(r, "race"), strings.Contains(r, "concurren"),
		strings.Contains(r, "atomic"), strings.Contains(r, "sync"):
		return config.ClassConcurrency

	case strings.Contains(r, "bodyclose"), strings.Contains(r, "sqlclosecheck"),
		strings.Contains(r, "rowserr"), strings.Contains(r, "leak"),
		strings.Contains(r, "close"):
		return config.ClassResource

	case strings.Contains(r, "errcheck"), strings.Contains(r, "staticcheck"),
		strings.Contains(r, "govet"), strings.Contains(r, "vet"),
		strings.Contains(r, "nilness"), strings.Contains(r, "nilerr"),
		strings.Contains(r, "ineffassign"), strings.Contains(r, "typecheck"):
		return config.ClassCorrectness

	case strings.Contains(r, "test"):
		return config.ClassTests

	case strings.Contains(r, "lll"), strings.Contains(r, "gofmt"),
		strings.Contains(r, "goimports"), strings.Contains(r, "revive"),
		strings.Contains(r, "stylecheck"), strings.Contains(r, "misspell"),
		strings.Contains(r, "godot"), strings.Contains(r, "whitespace"):
		return config.ClassStyle
	}

	// An unrecognized linter is far more likely to be reporting a real defect
	// than a style preference, and correctness is published at every level.
	return config.ClassCorrectness
}

// reviewablePaths returns the changed, non-ignored files worth analyzing.
func reviewablePaths(cfg *config.Config, files diff.Files) []string {
	out := make([]string, 0, len(files))

	for _, f := range files {
		if f.Binary || f.Kind == diff.ChangeDeleted || cfg.Ignored(f.Path) {
			continue
		}
		if len(f.ChangedLines()) == 0 {
			continue
		}
		out = append(out, f.Path)
	}

	return out
}

// prefixRule qualifies a rule id with the analyzer that produced it, so a
// reviewer can tell where a finding came from.
func prefixRule(linter, rule string) string {
	rule = strings.TrimSpace(rule)
	if rule == "" {
		return linter
	}
	if strings.HasPrefix(rule, linter) {
		return rule
	}
	return linter + "(" + rule + ")"
}

// available reports whether an analyzer binary can be safely executed.
//
// Resolution is PATH-only and the result must not live inside the repository
// being reviewed. A pull request can add `node_modules/.bin/eslint` (git
// preserves the executable bit), and running it would execute attacker-supplied
// code with GITHUB_TOKEN and the model API key in the environment — while
// `**/node_modules/**` is in the default ignore list, so the malicious file
// would never even appear in the posted review.
func available(name string) bool {
	_, err := resolveBinary(name, "")
	return err == nil
}

// resolveBinary finds an analyzer on PATH and rejects it if it resolves inside
// repoRoot. An empty repoRoot skips the containment check.
func resolveBinary(name, repoRoot string) (string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", err
	}

	if repoRoot == "" {
		return path, nil
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	root, err := filepath.Abs(repoRoot)
	if err != nil {
		return "", err
	}
	// EvalSymlinks so a link on PATH cannot point back into the repository.
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	if resolvedRoot, err := filepath.EvalSymlinks(root); err == nil {
		root = resolvedRoot
	}

	if rel, err := filepath.Rel(root, abs); err == nil &&
		!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." {
		return "", fmt.Errorf("%s resolves to %s inside the repository under review; refusing to execute it", name, abs)
	}

	return abs, nil
}

// safePaths drops paths that an analyzer would interpret as flags rather than
// files. Every runner also passes "--", but a path beginning with a dash is
// suspicious enough on its own that it is not worth analyzing.
func safePaths(paths []string) (kept, rejected []string) {
	for _, p := range paths {
		if strings.HasPrefix(p, "-") {
			rejected = append(rejected, p)
			continue
		}
		kept = append(kept, p)
	}
	return kept, rejected
}

// analyzerEnv returns this process's environment with set applied and unset
// removed, for analyzers whose behaviour an environment variable can change.
//
// A nil result means "inherit unchanged", which is what exec does with a nil
// Cmd.Env; callers that need no adjustment pass nil directly.
func analyzerEnv(set map[string]string, unset ...string) []string {
	drop := map[string]bool{}
	for _, k := range unset {
		drop[k] = true
	}
	for k := range set {
		drop[k] = true
	}

	env := make([]string, 0, len(os.Environ())+len(set))
	for _, kv := range os.Environ() {
		name, _, ok := strings.Cut(kv, "=")
		if ok && drop[name] {
			continue
		}
		env = append(env, kv)
	}
	for k, v := range set {
		env = append(env, k+"="+v)
	}

	return env
}

// oneLine collapses text onto a single line, for a status a human reads.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// runCommand executes an analyzer and returns its stdout and exit status.
//
// Analyzers conventionally exit non-zero when they find problems, which is the
// normal case here, so a non-zero exit with usable stdout is not treated as a
// failure HERE. It is not therefore harmless, and this function is not the place
// that decides: whether a given exit code means "found something" or "did not
// run" is the analyzer's own convention, so the code is returned and the runner
// that knows reads it. golangci-lint is invoked with --issues-exit-code 0 and
// semgrep documents 0 and 1 as success, and both of them report a failed
// analysis with a non-zero exit AND a well-formed report on stdout — which used
// to arrive here as success and decode to zero findings.
//
// An exit of ZERO with no output is caught downstream by decodeJSON, which
// requires a payload.
//
// env replaces the whole environment when non-nil; see analyzerEnv.
//
// repoRoot and workDir are separate arguments because they stopped being the
// same thing: golangci-lint is invoked inside the module that owns the changed
// package, which in a monorepo is a subdirectory. Containment has to stay
// anchored to the CHECKOUT, since the whole checkout is what the change wrote —
// keyed on the working directory instead, a binary the pull request added at
// <repo>/tools would be refused for a root module and accepted for a nested one.
func runCommand(ctx context.Context, repoRoot, workDir, name string, env []string, args ...string) ([]byte, int, error) {
	// Resolve against PATH and refuse anything inside the tree under review.
	bin, err := resolveBinary(name, repoRoot)
	if err != nil {
		return nil, 0, fmt.Errorf("%s: %w", name, err)
	}

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = workDir
	cmd.Env = env

	// Kill the process group if it ignores cancellation, so a wedged analyzer
	// cannot outlive the review.
	cmd.WaitDelay = 5 * time.Second

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	out := []byte(stdout.String())

	if runErr != nil && len(strings.TrimSpace(stdout.String())) == 0 {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, 0, fmt.Errorf("%s: %w", name, ctxErr)
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = runErr.Error()
		}
		return nil, 0, fmt.Errorf("%s: %s", name, truncate(msg, 400))
	}

	return out, exitCode(runErr), nil
}

// exitCode reads a process's exit status out of the error Run returned.
//
// A failure that is not an exit at all — the process was signalled, or never
// started — reports -1 rather than 0, so that a caller reading "did it exit
// cleanly" cannot be told yes by something that never exited.
func exitCode(runErr error) int {
	if runErr == nil {
		return 0
	}

	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
