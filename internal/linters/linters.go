// Package linters runs deterministic analyzers over the changed files and
// normalizes their output into review findings.
//
// Linter output is evidence, not a verdict: it is merged with the model's
// findings and triaged alongside them, so the model can explain impact and
// drop noise rather than the tool dumping raw lint output into a pull request.
package linters

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
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
}

// Runner is one analyzer.
type Runner interface {
	// Name identifies the runner in configuration and logs.
	Name() string

	// Detect reports whether this analyzer is usable in the repository: its
	// binary is on PATH and the project actually looks like one it applies to.
	Detect(ctx context.Context, repoRoot string) bool

	// Run analyzes the given repository-relative files.
	Run(ctx context.Context, repoRoot string, files []string) ([]Finding, error)
}

// Set runs the configured analyzers. It implements review.LinterRunner.
type Set struct {
	repoRoot string
	cfg      *config.Config
	log      *slog.Logger
	runners  []Runner
}

// New builds the analyzer set described by the configuration.
func New(repoRoot string, cfg *config.Config, log *slog.Logger) *Set {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	available := map[string]Runner{}
	for _, r := range builtins() {
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

// builtins lists the analyzers shipped with open-nitpick.
func builtins() []Runner {
	return []Runner{
		&golangciLint{},
		&ruff{},
		&eslint{},
		&semgrep{},
	}
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
		if !r.Detect(ctx, s.repoRoot) {
			if s.cfg.Linters.Mode == config.LinterStrict {
				mu.Lock()
				errs = append(errs, fmt.Errorf("linter %s is enabled but not available", r.Name()))
				mu.Unlock()
			} else {
				s.log.Debug("linter not detected; skipping", "linter", r.Name())
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

			mu.Lock()
			defer mu.Unlock()

			if err != nil {
				s.log.Warn("linter failed", "linter", r.Name(), "error", err)
				if s.cfg.Linters.Mode == config.LinterStrict {
					errs = append(errs, fmt.Errorf("linter %s: %w", r.Name(), err))
				}
				return
			}

			s.log.Debug("linter finished", "linter", r.Name(), "findings", len(found))
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
// that cannot be anchored to the change.
func (s *Set) normalize(found []Finding, files diff.Files) []review.Finding {
	out := make([]review.Finding, 0, len(found))

	for _, f := range found {
		file := files.Find(f.Path)
		if file == nil {
			continue
		}

		// A pull request review should discuss what the pull request did.
		// Pre-existing lint debt on untouched lines is somebody else's problem
		// and reporting it is the fastest way to get the bot switched off.
		if s.cfg.Linters.OnlyChangedLines && !file.IsChangedLine(f.Line) {
			continue
		}
		if _, ok := file.Position(f.Line); !ok {
			continue
		}

		severity := f.Severity
		if !severity.Valid() || severity == config.SeverityNone {
			severity = config.SeverityWarning
		}

		out = append(out, review.Finding{
			Path:      f.Path,
			Line:      f.Line,
			Severity:  string(severity),
			Category:  "lint",
			Class:     string(classForRule(f.Rule)),
			Title:     strings.TrimSpace(f.Message),
			Rationale: fmt.Sprintf("Reported by %s.", f.Rule),
			Source:    f.Rule,
		})
	}

	return out
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

// runCommand executes an analyzer and returns stdout.
//
// Analyzers conventionally exit non-zero when they find problems, which is the
// normal case here, so a non-zero exit with usable stdout is not treated as a
// failure. Only an exit with no output at all is.
func runCommand(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	// Resolve against PATH and refuse anything inside the tree under review.
	bin, err := resolveBinary(name, dir)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir

	// Kill the process group if it ignores cancellation, so a wedged analyzer
	// cannot outlive the review.
	cmd.WaitDelay = 5 * time.Second

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	out := []byte(stdout.String())
	err = runErr

	if err != nil && len(strings.TrimSpace(stdout.String())) == 0 {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("%s: %w", name, ctxErr)
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("%s: %s", name, truncate(msg, 400))
	}

	return out, nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
