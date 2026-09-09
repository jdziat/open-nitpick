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
	"slices"

	"github.com/jdziat/open-nitpick/internal/config"
	"strings"

	"github.com/jdziat/open-nitpick/internal/fullreview"
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
	return runTreeReview(ctx, "full-review", args, false)
}

// runRepoScore is full-review with the scorecard after the report.
func runRepoScore(ctx context.Context, args []string) error {
	return runTreeReview(ctx, "repo-score", args, true)
}

func runTreeReview(ctx context.Context, name string, args []string, score bool) error {
	var f reviewFlags
	var budget int

	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.StringVar(&f.repo, "repo", ".", "repository root")
	fs.StringVar(&f.configPath, "config", "", "path to .nitpick.yaml (default: <repo>/.nitpick.yaml)")
	fs.StringVar(&f.instruction, "instruction", "", "extra instruction for this run only")
	fs.IntVar(&budget, "budget", 0, "stop after this many estimated tokens of source (0: the whole tree)")
	fs.BoolVar(&f.noLinters, "no-linters", false, "skip analyzers even when configured")
	fs.BoolVar(&f.verbose, "v", false, "verbose logging")
	fs.StringVar(&f.logFormat, "log-format", "text", "log format: text or json")
	fs.Usage = func() {
		extra := ""
		if score {
			extra = " and the repository score"
		}
		fmt.Fprintf(os.Stderr, "Usage: nitpick %s [flags] [path...]\n\nReviews the whole working tree, or the paths given, and prints a remediation plan%s.\nThe default is the whole tree with the model on every batch; -budget bounds it and the report says what was left out.\n\nFlags:\n", name, extra)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	log := newLogger(f.verbose, f.logFormat)
	report, tree, err := treeReview(ctx, &f, fs.Args(), budget, log, os.Stdout)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "\nReviewed %d file(s): %s\n", report.Plan.Files(), report.Counts)
	printPolicy(report)
	printLinters(report)
	printOverruled(report)
	fmt.Print(fullreview.Sections(report))
	fmt.Print(fullreview.RemediationPlan(report.Findings))
	fmt.Print(review.EscalationNotice(report))
	fmt.Print(fullreview.CoverageNotice(report, tree))
	if score {
		fmt.Print(fullreview.Score(report, tree).String())
	}

	// After the output, not instead of it. full-review and repo-score returned
	// nil unconditionally, so a tree review whose triage died exited 0 and read
	// as a finished score.
	if !report.PipelineComplete() {
		return errIncomplete
	}
	return nil
}

// treeReview reviews the working tree, or the paths given, and returns the
// report and the tree that says what was covered. The review text the
// provider publishes goes to out; the MCP server passes io.Discard, since
// its stdout is the protocol. Shared by the commands and the server so the
// overrides below are made once.
func treeReview(ctx context.Context, f *reviewFlags, paths []string, budget int, log *slog.Logger, out io.Writer) (*review.Report, *vcs.Tree, error) {
	repo, err := filepath.Abs(f.repo)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve repo path: %w", err)
	}
	cfg, err := loadConfig(repo, f.configPath)
	if err != nil {
		return nil, nil, err
	}
	if len(cfg.Dropped) > 0 {
		log.Warn("ignored endpoint settings from an untrusted config file", "keys", strings.Join(cfg.Dropped, ", "))
	}

	// A whole-tree review has already read every file, so both directions
	// of related context are on: nothing the caller walk reads is a file
	// the operator did not just ask to have reviewed. The file cap is
	// lifted for the same reason; the token budget still bounds each call.
	cfg.Review.RelatedContext = true
	cfg.Review.RelatedContextCallers = true
	cfg.Review.Slop = true
	cfg.Review.MaxFiles = 1 << 30 // the whole tree is the point; -budget bounds it
	cfg.Review.Incremental = false
	// osv-scanner is opt-in for a pull request review because it queries
	// osv.dev; a whole-tree review wants the known advisories, so it is
	// named here. Naming it is a request, not a promise: outside strict
	// mode a binary that is not installed is a skip the roster reports.
	// Under strict mode a named analyzer that is missing fails the run,
	// and that list is the operator's promise, not this command's to add to.
	enableAdvisoryScanner(cfg)

	tree := vcs.NewTree(vcs.NewLocal(repo, out), paths)
	tree.MaxBytes = cfg.Review.MaxFileBytes
	tree.Budget = budget
	ref := vcs.Ref{Head: vcs.Worktree}

	log.Info("reviewing the tree", "repo", repo, "paths", strings.Join(paths, ","), "budget", budget)
	engine, err := newEngine(ctx, f, repo, cfg, tree, log)
	if err != nil {
		return nil, nil, err
	}
	// A pull request may not supply the policy it is reviewed under, so the
	// engine normally re-reads .nitpick.yaml from the base revision when the
	// change touches it. A tree review "changes" every file, that one
	// included, and the person running it on their own checkout is the
	// policy's author; the loaded configuration, with the overrides above,
	// is the policy.
	engine.Policy = nil
	report, err := engine.Review(ctx, ref)
	if err != nil && (!errors.Is(err, review.ErrPublish) || report == nil) {
		return nil, nil, err
	}
	return report, tree, nil
}

// enableAdvisoryScanner names osv-scanner for a tree review unless the
// operator runs analyzers strictly, where a name is a promise the binary
// is present.
func enableAdvisoryScanner(cfg *config.Config) {
	if cfg.Linters.Mode == config.LinterStrict || slices.Contains(cfg.Linters.Enabled, "osv-scanner") {
		return
	}
	cfg.Linters.Enabled = append(cfg.Linters.Enabled, "osv-scanner")
}
