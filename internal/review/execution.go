package review

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/nitpick-oss/nitpick/internal/execution"
	"github.com/nitpick-oss/nitpick/internal/vcs"
)

// ExecutionReceipt ties an attempt and any abstention to the exact mapped claim.
type ExecutionReceipt struct {
	FindingSHA256 string            `json:"finding_sha256"`
	Kind          string            `json:"kind"`
	Status        execution.Status  `json:"status"`
	Reason        string            `json:"reason,omitempty"`
	Result        *execution.Result `json:"result,omitempty"`
}

// ExecutionSummary distinguishes no matching claims from unavailable execution.
type ExecutionSummary struct {
	State    string             `json:"state"`
	Receipts []ExecutionReceipt `json:"receipts"`
	Omitted  int                `json:"omitted"`
}

type executionRun struct {
	mu       sync.Mutex
	enabled  bool
	receipts []ExecutionReceipt
	omitted  int
}

func (r *executionRun) record(receipt ExecutionReceipt) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.receipts) >= 128 {
		r.omitted++
		return
	}
	r.receipts = append(r.receipts, receipt)
}

func (r *executionRun) snapshot() ExecutionSummary {
	if r == nil {
		return ExecutionSummary{State: "not reached", Receipts: []ExecutionReceipt{}}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	state := "enabled; no matching claim ran"
	if !r.enabled {
		state = "disabled"
	} else if len(r.receipts) > 0 {
		state = "recorded"
	}
	receipts := append([]ExecutionReceipt{}, r.receipts...)
	return ExecutionSummary{State: state, Receipts: receipts, Omitted: r.omitted}
}

// executeClaim only runs operator-mapped claims, after read-only verification.
func (e *Engine) executeClaim(ctx context.Context, ref vcs.Ref, finding Finding, prior verifyOutcome) verifyOutcome {
	settings := e.Config.Execution
	if !settings.Enabled {
		return prior
	}
	digest := execution.FindingDigest(ref.Head, finding.Path, finding.Line, finding.Title, finding.Rationale)
	for _, claim := range settings.Claims {
		if claim.FindingSHA256 != digest || claim.Head != ref.Head || claim.Path != finding.Path {
			continue
		}
		receipt := ExecutionReceipt{FindingSHA256: digest, Kind: claim.Kind, Status: execution.Unavailable, Reason: "execution did not complete"}
		defer func() { e.executions.record(receipt) }()
		if claim.CheckIndex < 0 || claim.CheckIndex >= len(settings.Checks) {
			receipt.Reason = "invalid profile mapping"
			prior.evidence += "\nExecution unavailable/incomplete: invalid profile mapping."
			return prior
		}
		runner, err := execution.NewRunner(settings.Policy())
		if err != nil {
			receipt.Reason = "backend unavailable"
			prior.evidence += "\nExecution unavailable/incomplete: backend unavailable."
			return prior
		}
		snapshot, err := execution.ReadSnapshot(ctx, e.Provider, ref)
		if err != nil {
			receipt.Reason = "snapshot unavailable"
			prior.evidence += "\nExecution unavailable/incomplete: snapshot unavailable."
			return prior
		}
		profile := settings.Checks[claim.CheckIndex]
		check := execution.Check{Kind: profile.Kind, Package: profile.Package}
		result := runner.Run(ctx, snapshot, check)
		result.Output = nil
		receipt.Result = &result
		currentRef := ref
		currentRef.Head = ""
		current, err := e.Provider.PullRequest(ctx, currentRef)
		if err != nil || current == nil || runner.VerifyReceipt(result, snapshot, check, current.HeadSHA) != nil {
			receipt.Reason = "stale or invalid evidence"
			prior.evidence += "\nExecution unavailable/incomplete: stale or invalid evidence."
			return prior
		}
		receipt.Status = result.Status
		receipt.Reason = result.Reason
		raw, _ := json.Marshal(result)
		prior.evidence += "\nSelected execution evidence: " + string(raw)
		if execution.RefutesBuildFailure(claim.Kind, finding.Path, result) {
			prior.verdict = "refuted"
			prior.failed = ""
			prior.reason = fmt.Sprintf("The selected go_build profile passed at %s, refuting this exact registered build-failure claim.", ref.Head)
		}
		return prior
	}
	return prior
}
