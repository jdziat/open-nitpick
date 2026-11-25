package review

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/nitpick-oss/nitpick/internal/config"
	"github.com/nitpick-oss/nitpick/internal/diff"
	"github.com/nitpick-oss/nitpick/internal/vcs"
)

type checkpointLinter struct {
	identity       string
	findings       []Finding
	statuses       []LinterStatus
	discarded      []LinterDiscard
	uncovered      []LinterUncovered
	calls          int
	err            error
	changeAfterRun bool
}

func (r *checkpointLinter) Run(context.Context, diff.Files) ([]Finding, error) {
	r.calls++
	if r.changeAfterRun {
		r.identity += "changed"
	}
	return r.findings, r.err
}
func (r *checkpointLinter) CheckpointKey(context.Context, diff.Files) string { return r.identity }
func (r *checkpointLinter) Statuses() []LinterStatus                         { return r.statuses }
func (r *checkpointLinter) Discarded() []LinterDiscard                       { return r.discarded }
func (r *checkpointLinter) Uncovered() []LinterUncovered                     { return r.uncovered }
func (r *checkpointLinter) RestoreCheckpoint(s []LinterStatus, d []LinterDiscard, u []LinterUncovered) {
	r.statuses, r.discarded, r.uncovered = s, d, u
}

func TestLinterCheckpointPreservesFindingsAndAllCoverageMetadata(t *testing.T) {
	for _, empty := range []bool{false, true} {
		t.Run(map[bool]string{false: "findings", true: "proved empty"}[empty], func(t *testing.T) {
			e := resumeEngine(t, &scriptedLLM{fallback: `{"findings":[]}`}, &incrementalProvider{})
			e.startProgress(&vcs.PullRequest{HeadSHA: "head"}, nil)
			files := diff.Files{&diff.File{Path: "app.go", Kind: diff.ChangeModified}}
			r := &checkpointLinter{identity: "same tool and source", statuses: []LinterStatus{{Linter: "lint", Outcome: LinterRan, State: "isolated"}}, discarded: []LinterDiscard{{Path: "other.go", Rule: "lint"}}, uncovered: []LinterUncovered{{Path: "app.go", Linter: "lint"}}}
			if !empty {
				confidence := 0.0
				r.findings = []Finding{{Path: "app.go", Line: 4, Confidence: &confidence, Source: "lint/rule", RawSeverity: "HIGH", SeverityTranslated: true, FromAnalyzer: true, Evidence: []string{"evidence"}, SummaryOnly: true, FixValidated: true, Triager: "triager", Unresolved: "verification", UnresolvedBy: "verifier", TaskContext: &TaskContext{ID: "context", Text: "source"}}}
			}
			first, err, reused := e.runLinters(context.Background(), r, files)
			if err != nil || reused || r.calls != 1 {
				t.Fatalf("first run: %v reused=%t calls=%d", err, reused, r.calls)
			}
			data, _ := e.progress.snapshot("head")
			e.startProgress(&vcs.PullRequest{HeadSHA: "head"}, &vcs.PriorReview{Progress: data})
			next := &checkpointLinter{identity: r.identity}
			got, err, reused := e.runLinters(context.Background(), next, files)
			if err != nil || !reused || next.calls != 0 || !reflect.DeepEqual(got, first) {
				t.Fatalf("checkpoint lost findings or reran: %v reused=%t calls=%d got=%+v", err, reused, next.calls, got)
			}
			if !reflect.DeepEqual(r.statuses, next.statuses) || !reflect.DeepEqual(r.discarded, next.discarded) || !reflect.DeepEqual(r.uncovered, next.uncovered) {
				t.Fatal("coverage metadata disappeared on reuse")
			}
			_, modelReuses := e.progress.snapshot("head")
			if modelReuses != 0 {
				t.Fatal("local analyzer reuse was counted as a saved model request")
			}
		})
	}
}

func TestCheckpointSizeLimitPreservesPaidResponsesBeforeAnalyzerOutput(t *testing.T) {
	e := resumeEngine(t, &scriptedLLM{}, &incrementalProvider{})
	e.startProgress(&vcs.PullRequest{HeadSHA: "head"}, nil)
	saveStage(e.progress, "large analyzer output", rankLinter, strings.Repeat("x", vcs.MaxProgressBytes*3/4))
	saveStage(e.progress, "paid response", rankAnalysis, strings.Repeat("m", vcs.MaxProgressBytes/2))
	data, _ := e.progress.snapshot("head")
	var saved progressRecord
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Results["paid response"] == nil || saved.Results["large analyzer output"] != nil {
		t.Fatal("analyzer output evicted a paid model response")
	}
}

func TestLinterCheckpointInvalidatesInputsAndRetriesIncompleteRuns(t *testing.T) {
	for _, change := range []string{"source or tool", "head", "policy", "files", "error", "failed status", "unproved skip", "no statuses", "changed during run", "malformed", "disabled", "unbound runner", "cancelled"} {
		t.Run(change, func(t *testing.T) {
			e := resumeEngine(t, &scriptedLLM{fallback: `{"findings":[]}`}, &incrementalProvider{})
			pr := &vcs.PullRequest{HeadSHA: "head"}
			e.startProgress(pr, nil)
			files := diff.Files{&diff.File{Path: "app.go"}}
			r := &checkpointLinter{identity: "identity", statuses: []LinterStatus{{Linter: "lint", Outcome: LinterRan}}}
			switch change {
			case "error":
				r.err = errors.New("failed")
			case "failed status":
				r.statuses[0].Outcome = LinterFailed
			case "unproved skip":
				r.statuses[0].Outcome = LinterSkipped
			case "no statuses":
				r.statuses = nil
			case "changed during run":
				r.changeAfterRun = true
			}
			_, _, _ = e.runLinters(context.Background(), r, files)
			data, _ := e.progress.snapshot("head")
			if change == "head" {
				pr.HeadSHA = "new head"
			}
			if change == "policy" {
				e.Config.Linters.Mode = config.LinterStrict
			}
			e.startProgress(pr, &vcs.PriorReview{Progress: data})
			if change == "malformed" {
				for key := range e.progress.prior {
					e.progress.prior[key] = json.RawMessage(`"bad checkpoint"`)
				}
			}
			if change == "disabled" {
				e.progress = nil
			}
			next := &checkpointLinter{identity: "identity", statuses: []LinterStatus{{Linter: "lint", Outcome: LinterRan}}}
			switch change {
			case "source or tool":
				next.identity = "changed"
			case "files":
				files[0].Path = "other.go"
			case "unbound runner":
				next.identity = ""
			}
			ctx := context.Background()
			if change == "cancelled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			_, err, reused := e.runLinters(ctx, next, files)
			if reused || (change != "cancelled" && next.calls != 1) {
				t.Fatalf("stale/incomplete result reused=%t calls=%d", reused, next.calls)
			}
			if change == "cancelled" && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancelled review returned %v", err)
			}
		})
	}
}

func TestResumeRestoresAnalyzerStageAlongsideModelResults(t *testing.T) {
	provider := &incrementalProvider{stubProvider: stubProvider{diff: incrementalDiff}, head: "head"}
	e := resumeEngine(t, &scriptedLLM{fallback: `{"findings":[]}`}, provider)
	calls := 0
	e.Linters = func(*config.Config) LinterRunner {
		return &countedCheckpointLinter{checkpointLinter: checkpointLinter{identity: "identity", statuses: []LinterStatus{{Linter: "lint", Outcome: LinterRan, State: "isolated"}}}, total: &calls}
	}
	first, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	provider.prior = &vcs.PriorReview{Progress: provider.published.Progress}
	second, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || second.ReusedAnalyzerStages != 1 || !reflect.DeepEqual(first.Linters, second.Linters) {
		t.Fatalf("stage not restored: calls=%d report=%+v", calls, second)
	}
}

type countedCheckpointLinter struct {
	checkpointLinter
	total *int
}

func (r *countedCheckpointLinter) Run(ctx context.Context, files diff.Files) ([]Finding, error) {
	*r.total++
	return r.checkpointLinter.Run(ctx, files)
}
