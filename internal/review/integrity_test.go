package review

import (
	"context"
	"errors"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/diff"
	"github.com/jdziat/open-nitpick/internal/llm"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

func TestSameHeadMustRetainStandingFindings(t *testing.T) {
	p := &incrementalProvider{stubProvider: stubProvider{diff: engineDiff}, head: "deadbeef", prior: &vcs.PriorReview{Head: "deadbeef", Comments: []vcs.PriorComment{{ID: 1, Path: "app.go", Line: 4, Fingerprint: "abcd", Class: "correctness"}}}}
	e := newEngine(t, &scriptedLLM{fallback: `{"findings":[]}`}, p, func(c *config.Config) { c.Review.Approve.Enabled = true })
	r, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if p.published.Event == vcs.EventApprove {
		t.Fatalf("approved with open prior finding; prior_comments=%d reviewed_files=%d", r.PriorComments, r.Plan.Files())
	}
}
func TestRepeatedFindingMustStillFailGate(t *testing.T) {
	f := Finding{Path: "app.go", Line: 4, Severity: "error", Class: "correctness", Title: "Ignored error"}
	m := &scriptedLLM{byPrompt: map[string]string{"Review the following changes": mustJSON(t, Result{Findings: []Finding{f}}), "triaging findings": mustJSON(t, TriageResult{Verdicts: verdictsFor([]Finding{f})})}}
	p := &incrementalProvider{stubProvider: stubProvider{diff: engineDiff}, head: "beef02", prior: &vcs.PriorReview{Head: "beef01", Comments: []vcs.PriorComment{{ID: 1, Path: f.Path, Line: f.Line, Fingerprint: Fingerprint(f), Class: f.Class}}}, changed: []string{"app.go"}, ok: true}
	r, err := newEngine(t, m, p, nil).Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Failed(config.SeverityError) {
		t.Fatalf("gate passed with %d re-confirmed error findings withheld", len(r.AlreadyReported))
	}
}

type adversarialFailedLinter struct{}

func (adversarialFailedLinter) Run(context.Context, diff.Files) ([]Finding, error) {
	return nil, errors.New("required analyzer failed")
}
func (adversarialFailedLinter) Statuses() []LinterStatus {
	return []LinterStatus{{Linter: "required", Outcome: LinterFailed, State: "unavailable"}}
}
func TestStrictLinterFailureMustFailPipeline(t *testing.T) {
	p := &stubProvider{diff: engineDiff}
	e := newEngine(t, &scriptedLLM{fallback: `{"findings":[]}`}, p, func(c *config.Config) { c.Linters.Mode = config.LinterStrict })
	e.Linters = func(*config.Config) LinterRunner { return adversarialFailedLinter{} }
	r, err := e.Review(context.Background(), vcs.Ref{})
	if err == nil && r.PipelineComplete() {
		t.Fatalf("strict linter failure produced complete pipeline: statuses=%+v", r.Linters)
	}
}
func TestPartialReviewMustNotCacheFailedFiles(t *testing.T) {
	cfg := config.Defaults()
	cfg.Models.Default = config.ModelSpec{Provider: "openai", Model: "gpt-4o"}
	cfg.Review.MaxFilesPerRequest = 1
	m := &partialLLM{failOn: "doomed.go", reviewed: `{"findings":[]}`, triage: `{"findings":[],"summary":""}`}
	cl := llm.NewClientForTest(m, cfg.Models.Default)
	p := &incrementalProvider{stubProvider: stubProvider{diff: twoFileDiff}, head: "deadbeef"}
	e := &Engine{Config: cfg, Roles: &llm.Roles{Review: cl, Triage: cl}, Provider: p}
	first, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if first.Complete() {
		t.Fatal("setup did not fail one batch")
	}
	if !p.published.Incomplete {
		t.Fatal("failed run published a reusable baseline")
	}
	p.prior = &vcs.PriorReview{}
	m.failOn = ""
	second, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if second.PipelineComplete() && second.Plan.Files() == 0 {
		t.Fatalf("failed file forgotten on retry: first incomplete=%v, second files=%d complete=%v", first.Incomplete, second.Plan.Files(), second.PipelineComplete())
	}
}

type snapshotProvider struct {
	incrementalProvider
	refs []vcs.Ref
}

func (p *snapshotProvider) Diff(ctx context.Context, ref vcs.Ref) ([]byte, error) {
	p.refs = append(p.refs, ref)
	return p.stubProvider.Diff(ctx, ref)
}

func (p *snapshotProvider) FileContent(ctx context.Context, ref vcs.Ref, path string) ([]byte, error) {
	p.refs = append(p.refs, ref)
	return p.stubProvider.FileContent(ctx, ref, path)
}

func TestReviewReadsFilesAtTheCapturedHead(t *testing.T) {
	p := &snapshotProvider{incrementalProvider: incrementalProvider{stubProvider: stubProvider{diff: engineDiff}, head: "deadbeef"}}
	e := newEngine(t, &scriptedLLM{fallback: `{"findings":[]}`}, p, nil)
	if _, err := e.Review(context.Background(), vcs.Ref{Owner: "o", Repo: "r", Number: 7}); err != nil {
		t.Fatal(err)
	}
	if len(p.refs) < 2 {
		t.Fatalf("did not read both diff and content: %v", p.refs)
	}
	for _, ref := range p.refs {
		if ref.Head != "deadbeef" {
			t.Fatalf("unpinned input read: %+v", ref)
		}
	}
}

func TestSkipMarkersComeFromAcceptedPolicy(t *testing.T) {
	for _, accepted := range []bool{false, true} {
		m := &scriptedLLM{fallback: `{"findings":[]}`}
		p := &incrementalProvider{stubProvider: stubProvider{diff: configEditDiff}, head: "deadbeef"}
		e := hostileEngine(t, m, p, func(c *config.Config) { c.Review.SkipMarkers = []string{"Add retry"} })
		policy := e.Policy.(*basePolicy)
		policy.cfg.Review.SkipMarkers = nil
		if accepted {
			policy.cfg.Review.SkipMarkers = []string{"Add retry"}
		}
		r, err := e.Review(context.Background(), vcs.Ref{Owner: "o", Repo: "r", Number: 7})
		if err != nil {
			t.Fatal(err)
		}
		if (r.Skipped != "") != accepted || policy.calls == 0 {
			t.Fatalf("accepted=%v skipped=%q resolutions=%d", accepted, r.Skipped, policy.calls)
		}
		if accepted && m.callCount() != 0 {
			t.Fatal("skipped review spent model calls")
		}
		if !accepted && m.callCount() == 0 {
			t.Fatal("hostile skip marker prevented review")
		}
	}
}
