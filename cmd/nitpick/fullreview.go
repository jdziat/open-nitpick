package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/review"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// runFullReview reviews a repository, or the paths named, rather than a
// change: the working tree is read as one change that adds every file, so
// the engine, the analyzers and the model see whole files and any line can
// carry a finding. The report is the review's, followed by a remediation
// plan that orders what was found by severity and groups what shares a fix,
// and by what the review did not cover, since a review of half a tree must
// not read as a review of the tree.
func runFullReview(ctx context.Context, args []string) error {
	var f reviewFlags
	var budget int

	fs := flag.NewFlagSet("full-review", flag.ContinueOnError)
	fs.StringVar(&f.repo, "repo", ".", "repository root")
	fs.StringVar(&f.configPath, "config", "", "path to .nitpick.yaml (default: <repo>/.nitpick.yaml)")
	fs.StringVar(&f.instruction, "instruction", "", "extra instruction for this run only")
	fs.IntVar(&budget, "budget", 0, "stop after this many estimated tokens of source (0: the whole tree)")
	fs.BoolVar(&f.noLinters, "no-linters", false, "skip analyzers even when configured")
	fs.BoolVar(&f.verbose, "v", false, "verbose logging")
	fs.StringVar(&f.logFormat, "log-format", "text", "log format: text or json")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: nitpick full-review [flags] [path...]\n\nReviews the whole working tree, or the paths given, and prints a remediation plan.\nThe default is the whole tree with the model on every batch; -budget bounds it and the report says what was left out.\n\nFlags:")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	repo, err := filepath.Abs(f.repo)
	if err != nil {
		return fmt.Errorf("resolve repo path: %w", err)
	}
	cfg, err := loadConfig(repo, f.configPath)
	if err != nil {
		return err
	}
	log := newLogger(f.verbose, f.logFormat)
	if len(cfg.Dropped) > 0 {
		log.Warn("ignored endpoint settings from an untrusted config file", "keys", strings.Join(cfg.Dropped, ", "))
	}

	// A whole-tree review has already read every file, so both directions
	// of related context are on: nothing the caller walk reads is a file
	// the operator did not just ask to have reviewed. The file cap is
	// lifted for the same reason; the token budget still bounds each call.
	cfg.Review.RelatedContext = true
	cfg.Review.RelatedContextCallers = true
	cfg.Review.MaxFiles = 1 << 30 // the whole tree is the point; -budget bounds it
	cfg.Review.Incremental = false

	tree := vcs.NewTree(vcs.NewLocal(repo, os.Stdout), fs.Args())
	tree.MaxBytes = cfg.Review.MaxFileBytes
	tree.Budget = budget
	ref := vcs.Ref{Head: vcs.Worktree}

	log.Info("reviewing the tree", "repo", repo, "paths", strings.Join(fs.Args(), ","), "budget", budget)
	engine := newEngine(&f, repo, cfg, tree, log)
	// A pull request may not supply the policy it is reviewed under, so the
	// engine normally re-reads .nitpick.yaml from the base revision when the
	// change touches it. A tree review "changes" every file, that one
	// included, and the person running it on their own checkout is the
	// policy's author; the loaded configuration, with the overrides above,
	// is the policy.
	engine.Policy = nil
	report, err := engine.Review(ctx, ref)
	if err != nil && !(errors.Is(err, review.ErrPublish) && report != nil) {
		return err
	}

	fmt.Fprintf(os.Stderr, "\nReviewed %d file(s): %s\n", report.Plan.Files(), report.Counts)
	printPolicy(report)
	printLinters(report)
	printOverruled(report)
	fmt.Print(remediationPlan(report.Findings))
	fmt.Print(coverageNotice(tree))
	return nil
}

// remediationPlan orders findings most severe first and groups those that
// share a title within a class, since one fix usually clears them together.
// The estimate is in files touched, which is countable; hours are not.
func remediationPlan(findings []review.Finding) string {
	if len(findings) == 0 {
		return "\nRemediation plan: nothing to remediate.\n"
	}
	type group struct {
		severity config.Severity
		class    string
		title    string
		files    map[string]bool
		first    string
		count    int
	}
	groups := map[string]*group{}
	for _, f := range findings {
		key := f.Class + "\x00" + strings.ToLower(strings.Join(strings.Fields(f.Title), " "))
		g, ok := groups[key]
		if !ok {
			g = &group{severity: config.Severity(f.Severity), class: f.Class, title: f.Title, files: map[string]bool{}, first: fmt.Sprintf("%s:%d", f.Path, f.Line)}
			groups[key] = g
		}
		if config.Severity(f.Severity).Rank() > g.severity.Rank() {
			g.severity = config.Severity(f.Severity)
		}
		g.files[f.Path] = true
		g.count++
	}
	ordered := make([]*group, 0, len(groups))
	for _, g := range groups {
		ordered = append(ordered, g)
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].severity.Rank() != ordered[j].severity.Rank() {
			return ordered[i].severity.Rank() > ordered[j].severity.Rank()
		}
		if ordered[i].class != ordered[j].class {
			return classOrder(ordered[i].class) < classOrder(ordered[j].class)
		}
		return ordered[i].first < ordered[j].first
	})

	var b strings.Builder
	b.WriteString("\nRemediation plan, most severe first; findings that share a fix are grouped:\n")
	for i, g := range ordered {
		files := make([]string, 0, len(g.files))
		for p := range g.files {
			files = append(files, p)
		}
		sort.Strings(files)
		fmt.Fprintf(&b, "%3d. [%s/%s] %s\n     %d finding(s) in %d file(s): %s\n", i+1, g.severity, g.class, g.title, g.count, len(files), strings.Join(files, ", "))
	}
	return b.String()
}

// classOrder breaks severity ties: what leaks or breaks before what reads
// badly.
func classOrder(class string) int {
	for i, c := range []string{"security", "correctness", "concurrency", "resource", "contract", "tests", "maintainability", "style"} {
		if c == class {
			return i
		}
	}
	return 99
}

// coverageNotice says what the tree review did not read, in the voice of the
// review's own notices: silence must not read as clean.
func coverageNotice(t *vcs.Tree) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\nCovered %d file(s).\n", len(t.Covered))
	if len(t.Unbudgeted) > 0 {
		fmt.Fprintf(&b, "Not reviewed, the budget ran out first (%d file(s)): %s\n", len(t.Unbudgeted), strings.Join(t.Unbudgeted, ", "))
	}
	if len(t.Skipped) > 0 {
		fmt.Fprintf(&b, "Left out (%d file(s)):\n", len(t.Skipped))
		for _, s := range t.Skipped {
			fmt.Fprintf(&b, "  %s: %s\n", s.Path, s.Reason)
		}
	}
	return b.String()
}
