package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jdziat/open-nitpick/internal/practices"
)

func TestCommitCommandReportsSubjectsAndKnownRangeCoverage(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init", "-q", "-b", "main")
	git(t, root, "commit", "--allow-empty", "-qm", "feat: initial")
	git(t, root, "checkout", "-qb", "feature")
	git(t, root, "commit", "--allow-empty", "-qm", "updates")
	var output bytes.Buffer
	err := runCommits(context.Background(), []string{"-repo", root, "-base", "main", "-json", "-check"}, &output)
	if !errors.Is(err, errFindings) {
		t.Fatalf("violation: %v %s", err, output.String())
	}
	var report practices.Report
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Checks) != 1 || len(report.Checks[0].Examined) != 1 || report.Checks[0].Findings[0].Rationale != "updates" {
		t.Fatalf("missing evidence: %+v", report)
	}
	git(t, root, "commit", "--amend", "--allow-empty", "-qm", "fix: preserve coverage")
	output.Reset()
	err = runCommits(context.Background(), []string{"-repo", root, "-base", "main", "-json", "-check"}, &output)
	if err != nil {
		t.Fatalf("clean control: %v %s", err, output.String())
	}
	if err := json.Unmarshal(output.Bytes(), &report); err != nil || len(report.Checks[0].Examined) != 1 || len(report.Checks[0].Findings) != 0 {
		t.Fatalf("false clean: %+v %v", report, err)
	}
}

func TestCommitCommandCannotBeWeakenedByItsOwnConfigChange(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init", "-q", "-b", "main")
	write(t, root, ".nitpick.yaml", "practices: {commits: {types: [fix]}}")
	git(t, root, "add", ".")
	git(t, root, "commit", "-qm", "fix: initial")
	git(t, root, "checkout", "-qb", "feature")
	write(t, root, ".nitpick.yaml", "practices: {commits: {types: [feat]}}")
	git(t, root, "add", ".")
	git(t, root, "commit", "-qm", "feat: disable policy")
	var output bytes.Buffer
	err := runCommits(context.Background(), []string{"-repo", root, "-base", "main", "-check"}, &output)
	if !errors.Is(err, errFindings) {
		t.Fatalf("self-supplied policy passed: %v %s", err, output.String())
	}
}

func TestCommitCommandRequiresScopeAndKeepsSquashTitleSeparate(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init", "-q", "-b", "main")
	git(t, root, "commit", "--allow-empty", "-qm", "feat: initial")
	var output bytes.Buffer
	if err := runCommits(context.Background(), []string{"-repo", root}, &output); err == nil {
		t.Fatal("missing range accepted")
	}
	if err := runCommits(context.Background(), []string{"-repo", root, "-base", "main", "-check"}, &output); !errors.Is(err, errIncomplete) {
		t.Fatalf("empty assessment: %v", err)
	}
	output.Reset()
	err := runCommits(context.Background(), []string{"-repo", root, "-base", "main", "-title", "updates", "-check", "-json"}, &output)
	if !errors.Is(err, errFindings) {
		t.Fatalf("invalid squash title passed: %v %s", err, output.String())
	}
	var report practices.Report
	if err := json.Unmarshal(output.Bytes(), &report); err != nil || len(report.Checks) != 2 || report.Checks[1].Findings[0].Target.Kind != practices.TitleTarget {
		t.Fatalf("title claimed commit coverage: %+v %v", report, err)
	}
	output.Reset()
	err = runCommits(context.Background(), []string{"-repo", root, "-base", "main", "-title", "fix: preserve the title contract", "-check", "-json"}, &output)
	if err != nil {
		t.Fatalf("valid title failed: %v %s", err, output.String())
	}
	if err := json.Unmarshal(output.Bytes(), &report); err != nil || len(report.Checks) != 2 || report.Checks[0].State != practices.NotApplicable || len(report.Checks[0].Examined) != 0 || report.Checks[1].State != practices.Completed || len(report.Checks[1].Examined) != 1 || len(report.Checks[1].Findings) != 0 {
		t.Fatalf("valid title lost scope evidence: %+v %v", report, err)
	}
}
