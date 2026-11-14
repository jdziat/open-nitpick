package review

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	llms "github.com/nocturnium/llm-go-sdk/v6"

	"github.com/jdziat/open-nitpick/internal/llm"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// investigatorLLM is a reviewer that answers two ways. A request carrying the
// lookup instructions is an investigation: step N of script is the Nth
// assistant turn. Any other request is the ordinary review, answered with
// ordinary. It counts both, so a test can say which path a batch took.
type investigatorLLM struct {
	mu           sync.Mutex
	script       []llms.Response
	ordinary     string
	failTools    error
	investigated int
	reviewed     int
	toolResults  []string
}

func (r *investigatorLLM) GenerateContent(_ context.Context, msgs []llms.Message, _ ...llms.CallOption) (*llms.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(msgs) == 0 || !strings.Contains(msgs[0].Content, "## Looking things up") {
		r.reviewed++
		return &llms.Response{Content: r.ordinary}, nil
	}
	r.investigated++
	if r.failTools != nil {
		return nil, r.failTools
	}
	step := 0
	for _, m := range msgs {
		switch m.Role {
		case llms.RoleAssistant:
			step++
		case llms.RoleTool:
			r.toolResults = append(r.toolResults, m.Content)
		}
	}
	if step >= len(r.script) {
		return &llms.Response{}, nil
	}
	out := r.script[step]
	return &out, nil
}
func (r *investigatorLLM) Stream(context.Context, []llms.Message, ...llms.CallOption) (<-chan llms.StreamChunk, error) {
	return nil, errors.New("not supported")
}
func (r *investigatorLLM) Provider() llms.Provider { return "investigator" }
func (r *investigatorLLM) Model() string           { return "investigator" }

func submit(t *testing.T, fs []Finding) llms.Response {
	t.Helper()
	return llms.Response{ToolCalls: []llms.ToolCall{{
		ID: "s", Type: llms.ToolTypeFunction,
		Function: &llms.FunctionCall{Name: submitFindings, Arguments: mustJSON(t, Result{Findings: fs})},
	}}}
}

func investigateEngine(t *testing.T, reviewer *investigatorLLM, on bool, root string) *Engine {
	t.Helper()
	e, _ := verifyEngine(t, &toolLLM{}, "")
	e.Roles.Review = llm.NewClientForTest(reviewer, e.Config.Models.Default)
	e.Config.Review.Investigate = on
	e.RepoRoot = root
	return e
}

func titles(report *Report) []string {
	var out []string
	for _, f := range report.Findings {
		out = append(out, f.Title)
	}
	return out
}

// The reviewer looks a file up, is handed it, and its submitted findings are
// the batch's findings: the ordinary review is never asked.
func TestAReviewerThatInvestigatesReadsTheRepositoryAndSubmitsItsFindings(t *testing.T) {
	fs := twoFindings()
	reviewer := &investigatorLLM{
		script:   []llms.Response{lookup("app.go"), submit(t, fs[:1])},
		ordinary: mustJSON(t, Result{Findings: fs[1:]}),
	}
	e := investigateEngine(t, reviewer, true, gitRepo(t, map[string]string{"app.go": "package app\n"}))
	report, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if got := titles(report); len(got) != 1 || got[0] != fs[0].Title {
		t.Errorf("findings = %v, want only the submitted %q", got, fs[0].Title)
	}
	if reviewer.reviewed != 0 {
		t.Errorf("the ordinary review ran %d times, want none after a submission", reviewer.reviewed)
	}
	if len(reviewer.toolResults) == 0 || !strings.Contains(reviewer.toolResults[0], "package app") {
		t.Errorf("tool results = %q, want the file the reviewer asked for handed back", reviewer.toolResults)
	}
}

// Off is the default, and off means the lookup instructions are never sent.
func TestInvestigationNeverRunsUnlessAskedFor(t *testing.T) {
	fs := twoFindings()
	reviewer := &investigatorLLM{ordinary: mustJSON(t, Result{Findings: fs})}
	e := investigateEngine(t, reviewer, false, gitRepo(t, map[string]string{"app.go": "package app\n"}))
	report, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if reviewer.investigated != 0 || len(report.Findings) != 2 {
		t.Errorf("investigated %d times with %d findings, want an ordinary review of both", reviewer.investigated, len(report.Findings))
	}
}

// With no checkout there is nothing to search, so the batch is reviewed as it
// always was. The option costing a review its answer would be worse than the
// option being absent.
func TestInvestigationWithoutACheckoutFallsBackToTheOrdinaryReview(t *testing.T) {
	fs := twoFindings()
	reviewer := &investigatorLLM{ordinary: mustJSON(t, Result{Findings: fs})}
	e := investigateEngine(t, reviewer, true, "")
	report, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if reviewer.investigated != 0 || reviewer.reviewed == 0 || len(report.Findings) != 2 {
		t.Errorf("investigated %d, reviewed %d, findings %d; want the ordinary review without a checkout",
			reviewer.investigated, reviewer.reviewed, len(report.Findings))
	}
}

// A failed investigation is not a failed review: the same batch is reviewed the
// ordinary way and its findings are published.
func TestAFailedInvestigationFallsBackToTheOrdinaryReview(t *testing.T) {
	fs := twoFindings()
	reviewer := &investigatorLLM{failTools: errors.New("provider down"), ordinary: mustJSON(t, Result{Findings: fs})}
	e := investigateEngine(t, reviewer, true, gitRepo(t, map[string]string{"app.go": "package app\n"}))
	report, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if reviewer.investigated == 0 || reviewer.reviewed == 0 || len(report.Findings) != 2 {
		t.Errorf("investigated %d, reviewed %d, findings %d; want a fallback to the ordinary review",
			reviewer.investigated, reviewer.reviewed, len(report.Findings))
	}
}

// A reviewer that never stops looking is cut off at the budget plus the turns
// it has left, and the batch is then reviewed the ordinary way rather than
// running until the request timeout.
func TestAReviewerThatKeepsLookingIsCutOffAndTheBatchStillGetsReviewed(t *testing.T) {
	endless := make([]llms.Response, 60)
	for i := range endless {
		endless[i] = lookup("app.go")
	}
	fs := twoFindings()
	reviewer := &investigatorLLM{script: endless, ordinary: mustJSON(t, Result{Findings: fs})}
	e := investigateEngine(t, reviewer, true, gitRepo(t, map[string]string{"app.go": "package app\n"}))
	report, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if reviewer.investigated > investigateMaxLookups+4 {
		t.Errorf("investigated %d turns, want at most %d", reviewer.investigated, investigateMaxLookups+4)
	}
	if reviewer.reviewed == 0 || len(report.Findings) != 2 {
		t.Errorf("reviewed %d, findings %d; want the ordinary review after the cut-off", reviewer.reviewed, len(report.Findings))
	}
	spent := 0
	for _, r := range reviewer.toolResults {
		if strings.Contains(r, "call "+submitFindings+" now") {
			spent++
		}
	}
	if spent == 0 {
		t.Errorf("no lookup past the budget was told to submit; results = %q", reviewer.toolResults)
	}
}

// Submitting an empty list is the answer "nothing to report", and it must stay
// that: falling back to the ordinary review would turn silence into findings.
func TestASubmittedEmptyListIsKeptAsSilence(t *testing.T) {
	reviewer := &investigatorLLM{
		script:   []llms.Response{submit(t, nil)},
		ordinary: mustJSON(t, Result{Findings: twoFindings()}),
	}
	e := investigateEngine(t, reviewer, true, gitRepo(t, map[string]string{"app.go": "package app\n"}))
	report, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Findings) != 0 || reviewer.reviewed != 0 {
		t.Errorf("findings %d, ordinary reviews %d, want none of either after an empty submission", len(report.Findings), reviewer.reviewed)
	}
}

// A resumed run reuses a batch's cached answer only under the same
// configuration, so flipping the option never serves the other mode's result.
// The cache keys on the whole configuration; this fails if investigate ever
// drops out of it.
func TestFlippingInvestigateBetweenResumedRunsDoesNotReplayTheOtherModesAnswer(t *testing.T) {
	fs := twoFindings()
	reviewer := &investigatorLLM{
		script:   []llms.Response{submit(t, fs[:1])},
		ordinary: mustJSON(t, Result{Findings: fs}),
	}
	provider := &incrementalProvider{stubProvider: stubProvider{diff: engineDiff}, head: "abcdef"}
	e := resumeEngine(t, &scriptedLLM{fallback: `{"findings":[]}`}, provider)
	e.Roles.Review = llm.NewClientForTest(reviewer, e.Config.Models.Default)
	e.RepoRoot = gitRepo(t, map[string]string{"app.go": "package app\n"})

	if _, err := e.Review(context.Background(), vcs.Ref{}); err != nil {
		t.Fatal(err)
	}
	if reviewer.reviewed == 0 {
		t.Fatal("the first run did not review the ordinary way")
	}
	provider.prior = &vcs.PriorReview{Progress: provider.published.Progress}
	e.Config.Review.Investigate = true
	report, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if report.ReusedRequests != 0 || reviewer.investigated == 0 {
		t.Errorf("reused %d requests and investigated %d times, want a live investigation after the flip", report.ReusedRequests, reviewer.investigated)
	}
}
