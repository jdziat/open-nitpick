package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/practices"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

func runCommits(ctx context.Context, args []string, out io.Writer) error {
	var root, base, head, path, title string
	var check, asJSON bool
	fs := flag.NewFlagSet("commits", flag.ContinueOnError)
	fs.StringVar(&root, "repo", ".", "repository root")
	fs.StringVar(&base, "base", "", "required base revision; inspect commits reachable only from head")
	fs.StringVar(&head, "head", "HEAD", "head revision")
	fs.StringVar(&path, "config", "", "policy path; repository policy is read at base")
	fs.StringVar(&title, "title", "", "also validate this intended squash title")
	fs.BoolVar(&check, "check", false, "exit 1 for violations, 2 for incomplete evaluation")
	fs.BoolVar(&asJSON, "json", false, "print structured coverage and findings")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || base == "" {
		return errors.New("commits requires -base and accepts no positional arguments")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	rangeResult, err := vcs.NewLocal(root, nil).Commits(ctx, base, head)
	if err != nil {
		return err
	}
	policy, err := config.ReadPracticePolicy(ctx, root, path, rangeResult.BaseSHA)
	if err != nil {
		return err
	}
	report := practices.Report{SchemaVersion: practices.SchemaVersion, Profile: "commits", Revision: rangeResult.HeadSHA,
		PolicySource: policy.Source, PolicyDigest: policy.Digest,
		Checks: []practices.Check{practices.CommitCheck(rangeResult.Commits, policy.Practices.Commits)}}
	report.Checks[0].BaseRevision, report.Checks[0].Revision = rangeResult.BaseSHA, rangeResult.HeadSHA
	if title != "" {
		report.Checks = append(report.Checks, practices.TitleCheck(title, policy.Practices.Commits))
	}
	selected := policy.Practices
	selected.Required = []string{"commits"}
	selected.Boundaries = nil
	if title != "" {
		selected.Required = append(selected.Required, "commit-title")
	}
	practices.ApplyPolicy(&report, selected)
	practices.ApplyExceptions(&report, selected.Exceptions, nil, time.Now())
	if asJSON {
		encoder := json.NewEncoder(out)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			return err
		}
	} else if _, err := fmt.Fprint(out, report.Text()); err != nil {
		return err
	}
	if check {
		return practiceExit(report)
	}
	return nil
}

func practiceExit(report practices.Report) error {
	switch report.ExitCode() {
	case 1:
		return errFindings
	case 2:
		return errIncomplete
	default:
		return nil
	}
}
