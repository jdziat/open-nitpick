package main

import (
	"context"
	"flag"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/converse"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

func TestReviewMentionResumesAndRestartForcesFull(t *testing.T) {
	for _, command := range []string{"review", "restart-review", "re-review", "rereview"} {
		t.Run(command, func(t *testing.T) {
			kind, _, ok := converse.Command("@open-nitpick "+command, "@open-nitpick")
			if !ok {
				t.Fatal("command not recognized")
			}
			args := mentionReviewArgs(reviewFlags{repo: "checkout", configPath: "policy.yaml"}, vcs.Ref{Owner: "owner", Repo: "repo", Number: 140}, kind)
			fs := flag.NewFlagSet("review", flag.ContinueOnError)
			full := fs.Bool("full", false, "")
			repo := fs.String("repo", "", "")
			cfg := fs.String("config", "", "")
			fs.String("owner", "", "")
			fs.String("repo-name", "", "")
			fs.Int("pr", 0, "")
			if err := fs.Parse(args); err != nil {
				t.Fatal(err)
			}
			if *full != (command != "review") || *repo != "checkout" || *cfg != "policy.yaml" {
				t.Fatalf("incorrect review flags: %v", args)
			}
		})
	}
}

func TestEngineeringReviewPreservesResumeAndExplicitRestart(t *testing.T) {
	for _, full := range []bool{false, true} {
		root := t.TempDir()
		cfg := config.Defaults()
		flags := &reviewFlags{profile: "engineering", full: full}
		engine, err := newEngine(context.Background(), flags, root, cfg, vcs.NewLocal(root, nil), vcs.Ref{}, newLogger(false, "text"))
		if err != nil {
			t.Fatal(err)
		}
		policy, _, err := engine.Policy.ResolvePolicy(context.Background(), vcs.Ref{}, &vcs.PullRequest{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !engine.Resume || engine.Full != full || policy == nil || !policy.Review.Incremental {
			t.Fatalf("engineering discarded review mode: resume=%v full=%v policy=%+v", engine.Resume, engine.Full, policy)
		}
	}
}
