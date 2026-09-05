package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
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
	cfg.Review.Slop = true
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
	if err != nil && (!errors.Is(err, review.ErrPublish) || report == nil) {
		return err
	}

	fmt.Fprintf(os.Stderr, "\nReviewed %d file(s): %s\n", report.Plan.Files(), report.Counts)
	printPolicy(report)
	printLinters(report)
	printOverruled(report)
	fmt.Print(fullreview.Sections(report))
	fmt.Print(fullreview.RemediationPlan(report.Findings))
	fmt.Print(fullreview.CoverageNotice(tree))
	if score {
		fmt.Print(fullreview.Score(report, tree).String())
	}
	return nil
}
