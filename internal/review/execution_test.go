package review

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/execution"
	"github.com/jdziat/open-nitpick/internal/vcs"
	llms "github.com/nocturnium/llm-go-sdk/v6"
)

type executionProvider struct {
	vcs.Provider
	head string
}

type incompleteExecutionProvider struct{ executionProvider }

func (p incompleteExecutionProvider) ListSnapshotDir(context.Context, vcs.Ref, string) ([]string, error) {
	return nil, errors.New("truncated or unsupported source tree")
}

func TestExecutionReceiptsSurviveThePipelineAndResetBetweenRuns(t *testing.T) {
	head := strings.Repeat("a", 40)
	findings := twoFindings()
	findings[0].Title = "The selected default build fails"
	findings[0].Rationale = "This source cannot compile under the selected profile."
	script := map[string][]llms.Response{findings[0].Title: {verdict(verdictConfirmed, "read-only confirmation")}, findings[1].Title: {verdict(verdictConfirmed, "behavior remains")}}
	e, model := verifyEngine(t, &toolLLM{script: script}, config.VerifyNotRefuted)
	model.byPrompt["Review the following changes"] = mustJSON(t, Result{Findings: findings})
	model.byPrompt["triaging findings"] = mustJSON(t, TriageResult{Summary: "s", Verdicts: verdictsFor(findings)})
	e.Provider = executionProvider{Provider: e.Provider, head: head}
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	e.Config.Execution = config.Execution{Enabled: true, Image: "sha256:" + strings.Repeat("a", 64), Runtime: "runc", TrustedCorpus: true, RiskAccepted: true, Checks: []config.ExecutionCheck{{Kind: "go_build", Package: "."}}, Claims: []config.ExecutionClaim{{FindingSHA256: execution.FindingDigest(head, findings[0].Path, findings[0].Line, findings[0].Title, findings[0].Rationale), Head: head, Path: findings[0].Path, Kind: "go_build_failure", CheckIndex: 0}}}
	report, err := e.Review(t.Context(), vcs.Ref{Head: head})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Execution.Receipts) != 1 || report.Execution.Receipts[0].Result == nil || report.Execution.Receipts[0].Result.SnapshotSHA256 == "" || len(report.Findings) != 1 {
		t.Fatalf("pipeline lost claim evidence: %+v findings=%d", report.Execution, len(report.Findings))
	}
	raw, err := json.Marshal(report.Execution)
	if err != nil || !strings.Contains(string(raw), "policy_sha256") || !strings.Contains(string(raw), "output_sha256") {
		t.Fatalf("receipt cannot be retained: %s %v", raw, err)
	}
	e.Config.Execution.Claims = nil
	report, err = e.Review(t.Context(), vcs.Ref{Head: head})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Execution.Receipts) != 0 || report.Execution.State != "enabled; no matching claim ran" {
		t.Fatalf("previous evidence leaked into a new run: %+v", report.Execution)
	}
}

func (p executionProvider) ListSnapshotDir(context.Context, vcs.Ref, string) ([]string, error) {
	return []string{"go.mod", "main.go"}, nil
}
func (p executionProvider) FileContent(context.Context, vcs.Ref, string) ([]byte, error) {
	return []byte("module example\n"), nil
}
func (p executionProvider) PullRequest(_ context.Context, ref vcs.Ref) (*vcs.PullRequest, error) {
	if ref.Head != "" {
		return &vcs.PullRequest{HeadSHA: ref.Head}, nil
	}
	return &vcs.PullRequest{HeadSHA: p.head}, nil
}

func TestExecutionRefutesOnlyAnExactRegisteredBuildFailure(t *testing.T) {
	head := strings.Repeat("a", 40)
	finding := Finding{Path: "main.go", Line: 1, Title: "Selected build fails", Rationale: "The default Go build cannot compile this source."}
	digest := execution.FindingDigest(head, finding.Path, finding.Line, finding.Title, finding.Rationale)
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	for _, test := range []struct {
		name    string
		code    string
		mutate  func(*config.ExecutionClaim)
		moved   bool
		refuted bool
	}{
		{"mapped_pass", "0", func(*config.ExecutionClaim) {}, false, true},
		{"unavailable", "125", func(*config.ExecutionClaim) {}, false, false},
		{"failed_build", "1", func(*config.ExecutionClaim) {}, false, false},
		{"changed_finding", "0", func(c *config.ExecutionClaim) { c.FindingSHA256 = strings.Repeat("0", 64) }, false, false},
		{"behavior", "0", func(c *config.ExecutionClaim) { c.Kind = "behavioral_defect" }, false, false},
		{"head_moved", "0", func(*config.ExecutionClaim) {}, true, false},
		{"incomplete_snapshot", "0", func(*config.ExecutionClaim) {}, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := os.WriteFile(filepath.Join(dir, "docker"), []byte("#!/bin/sh\nif [ \"$1\" = run ]; then exit "+test.code+"; fi\nexit 0\n"), 0700); err != nil {
				t.Fatal(err)
			}
			claim := config.ExecutionClaim{FindingSHA256: digest, Head: head, Path: finding.Path, Kind: "go_build_failure", CheckIndex: 0}
			test.mutate(&claim)
			provider := executionProvider{head: head}
			if test.moved {
				provider.head = strings.Repeat("b", 40)
			}
			var source vcs.Provider = provider
			if test.name == "incomplete_snapshot" {
				source = incompleteExecutionProvider{provider}
			}
			e := newEngine(t, &scriptedLLM{}, source, func(c *config.Config) {
				c.Execution = config.Execution{Enabled: true, Image: "sha256:" + strings.Repeat("a", 64), Runtime: "runc", TrustedCorpus: true, RiskAccepted: true, Checks: []config.ExecutionCheck{{Kind: "go_build", Package: "."}}, Claims: []config.ExecutionClaim{claim}}
			})
			e.executions = &executionRun{enabled: true}
			prior := verifyOutcome{verdict: "confirmed", reason: "repository evidence remains valid"}
			got := e.executeClaim(t.Context(), vcs.Ref{Head: head}, finding, prior)
			if (got.verdict == "refuted") != test.refuted {
				t.Fatalf("wrong execution decision: %+v", got)
			}
			if !test.refuted && (got.verdict != prior.verdict || got.reason != prior.reason) {
				t.Fatalf("execution replaced prior verification: %+v", got)
			}
			if test.name == "incomplete_snapshot" {
				receipt := e.executions.snapshot()
				if len(receipt.Receipts) != 1 || receipt.Receipts[0].Status != execution.Unavailable || receipt.Receipts[0].Result != nil {
					t.Fatalf("omitted source was not recorded as unavailable: %+v", receipt)
				}
			}
		})
	}
}
