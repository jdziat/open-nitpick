package review

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/nitpick-oss/nitpick/internal/diff"
)

// LinterCheckpointer binds reusable analyzer output to its complete inputs.
// An empty key requires a fresh run. Implementations must include tool and
// configuration versions, dependency inputs and repository state in the key.
type LinterCheckpointer interface {
	LinterRunner
	LinterStatusReporter
	LinterDiscardReporter
	LinterUncoveredReporter
	CheckpointKey(context.Context, diff.Files) string
	RestoreCheckpoint([]LinterStatus, []LinterDiscard, []LinterUncovered)
}

const linterCheckpointVersion = 1
const rankLinter = rankVerify + 1

type linterCheckpointRecord struct {
	Version   int
	Findings  []checkpointFinding
	Statuses  []LinterStatus
	Discarded []LinterDiscard
	Uncovered []LinterUncovered
}

// Finding's model JSON omits engine-owned fields; a checkpoint needs both.
type checkpointFinding struct {
	Finding
	TaskContext        *TaskContext
	SummaryOnly        bool
	SeverityTranslated bool
	RawSeverity        string
	FixValidated       bool
	Source             string
	Triager            string
	Evidence           []string
	Unresolved         string
	UnresolvedBy       string
	FromAnalyzer       bool
}

func checkpointFindings(findings []Finding) []checkpointFinding {
	if findings == nil {
		return nil
	}
	out := make([]checkpointFinding, len(findings))
	for i, f := range findings {
		out[i] = checkpointFinding{Finding: f, TaskContext: f.TaskContext, SummaryOnly: f.SummaryOnly, SeverityTranslated: f.SeverityTranslated, RawSeverity: f.RawSeverity, FixValidated: f.FixValidated, Source: f.Source, Triager: f.Triager, Evidence: f.Evidence, Unresolved: f.Unresolved, UnresolvedBy: f.UnresolvedBy, FromAnalyzer: f.FromAnalyzer}
	}
	return out
}

func (r linterCheckpointRecord) findings() []Finding {
	if r.Findings == nil {
		return nil
	}
	out := make([]Finding, len(r.Findings))
	for i, saved := range r.Findings {
		f := saved.Finding
		f.TaskContext, f.SummaryOnly = saved.TaskContext, saved.SummaryOnly
		f.SeverityTranslated, f.RawSeverity = saved.SeverityTranslated, saved.RawSeverity
		f.FixValidated, f.Source, f.Triager = saved.FixValidated, saved.Source, saved.Triager
		f.Evidence, f.Unresolved, f.UnresolvedBy, f.FromAnalyzer = saved.Evidence, saved.Unresolved, saved.UnresolvedBy, saved.FromAnalyzer
		out[i] = f
	}
	return out
}

func (r linterCheckpointRecord) complete() bool {
	if r.Version != linterCheckpointVersion || len(r.Statuses) == 0 {
		return false
	}
	for _, s := range r.Statuses {
		if s.Outcome != LinterRan && (s.Outcome != LinterSkipped || !s.NoTargets) {
			return false
		}
	}
	return true
}

func (e *Engine) linterStageKey(identity string, files diff.Files) string {
	if e.progress == nil || identity == "" {
		return ""
	}
	data, err := json.Marshal([]any{linterCheckpointVersion, e.progress.now, e.progress.policy, identity, files})
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (e *Engine) runLinters(ctx context.Context, runner LinterRunner, files diff.Files) ([]Finding, error, bool) {
	if err := ctx.Err(); err != nil {
		return nil, err, false
	}
	checkpoint, eligible := runner.(LinterCheckpointer)
	if !eligible || e.progress == nil {
		findings, err := runner.Run(ctx, files)
		return findings, err, false
	}
	identity := checkpoint.CheckpointKey(ctx, files)
	key := e.linterStageKey(identity, files)
	if key != "" {
		e.progress.mu.Lock()
		prior := e.progress.prior[key]
		e.progress.mu.Unlock()
		var saved linterCheckpointRecord
		if ctx.Err() == nil && json.Unmarshal(prior, &saved) == nil && saved.complete() {
			checkpoint.RestoreCheckpoint(saved.Statuses, saved.Discarded, saved.Uncovered)
			// Local tool reuse is separate from the count of saved model requests.
			e.progress.mu.Lock()
			e.progress.current[key] = prior
			e.progress.setRank(key, rankLinter)
			e.progress.mu.Unlock()
			return saved.findings(), nil, true
		}
	}
	findings, err := runner.Run(ctx, files)
	if err != nil || key == "" || ctx.Err() != nil {
		return findings, err, false
	}
	saved := linterCheckpointRecord{Version: linterCheckpointVersion, Findings: checkpointFindings(findings), Statuses: checkpoint.Statuses(), Discarded: checkpoint.Discarded(), Uncovered: checkpoint.Uncovered()}
	if saved.complete() && checkpoint.CheckpointKey(ctx, files) == identity {
		saveStage(e.progress, key, rankLinter, saved)
	}
	return findings, err, false
}
