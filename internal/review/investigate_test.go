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

// investigatorLLM is a reviewer that answers three ways. A request carrying the
// lookup instructions is an investigation turn: step N of script is the Nth
// assistant turn. A request whose last message asks for submit_findings now is a
// forced submission, answered from forcedReplies in order. Any other request is
// the ordinary review, answered with ordinary. It counts each, so a test can say
// which path a batch took.
type investigatorLLM struct {
	mu            sync.Mutex
	script        []llms.Response
	forcedReplies []llms.Response
	ordinary      string
	failFirst     error // fails the first investigation turn, before anything was read
	failAfter     error // fails every investigation turn after the first
	investigated  int
	forced        int
	reviewed      int
	toolResults   []string
	forcedOff     []bool // for each forced call, whether reasoning was switched off
}

func (r *investigatorLLM) GenerateContent(_ context.Context, msgs []llms.Message, opts ...llms.CallOption) (*llms.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(msgs) == 0 || !strings.Contains(msgs[0].Content, "## Looking things up") {
		r.reviewed++
		return &llms.Response{Content: r.ordinary}, nil
	}
	if last := msgs[len(msgs)-1]; last.Role == llms.RoleUser && strings.Contains(last.Content, "submit_findings now") {
		i := r.forced
		r.forced++
		reasoning := llms.ApplyOptions(opts...).Reasoning
		r.forcedOff = append(r.forcedOff, reasoning != nil && reasoning.Enabled != nil && !*reasoning.Enabled)
		if i >= len(r.forcedReplies) {
			return &llms.Response{Content: "prose"}, nil
		}
		out := r.forcedReplies[i]
		return &out, nil
	}
	r.investigated++
	step := 0
	var newest []string
	for _, m := range msgs {
		switch m.Role {
		case llms.RoleAssistant:
			step++
			newest = nil
		case llms.RoleTool:
			newest = append(newest, m.Content)
		}
	}
	// Only the answers to the latest turn are new; the earlier ones are replayed
	// in every request and would be counted again.
	r.toolResults = append(r.toolResults, newest...)
	if step == 0 && r.failFirst != nil {
		return nil, r.failFirst
	}
	if step > 0 && r.failAfter != nil {
		return nil, r.failAfter
	}
	if step >= len(r.script) {
		return &llms.Response{Content: "I am done."}, nil
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

func appRepo(t *testing.T) string {
	t.Helper()
	return gitRepo(t, map[string]string{"app.go": "package app\n"})
}

// The reviewer looks a file up, is handed it, and its submitted findings are the
// batch's findings: neither a forced submission nor the ordinary review is asked.
func TestAReviewerThatInvestigatesReadsTheRepositoryAndSubmitsItsFindings(t *testing.T) {
	fs := twoFindings()
	reviewer := &investigatorLLM{
		script:   []llms.Response{lookup("app.go"), submit(t, fs[:1])},
		ordinary: mustJSON(t, Result{Findings: fs[1:]}),
	}
	e := investigateEngine(t, reviewer, true, appRepo(t))
	report, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if got := titles(report); len(got) != 1 || got[0] != fs[0].Title {
		t.Errorf("findings = %v, want only the submitted %q", got, fs[0].Title)
	}
	if reviewer.reviewed != 0 || reviewer.forced != 0 {
		t.Errorf("ordinary reviews %d and forced submissions %d, want neither after a submission", reviewer.reviewed, reviewer.forced)
	}
	if len(reviewer.toolResults) == 0 || !strings.Contains(reviewer.toolResults[0], "package app") {
		t.Errorf("tool results = %q, want the file the reviewer asked for handed back", reviewer.toolResults)
	}
}

// Off is the default, and off means the lookup instructions are never sent.
func TestInvestigationNeverRunsUnlessAskedFor(t *testing.T) {
	fs := twoFindings()
	reviewer := &investigatorLLM{ordinary: mustJSON(t, Result{Findings: fs})}
	e := investigateEngine(t, reviewer, false, appRepo(t))
	report, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if reviewer.investigated != 0 || len(report.Findings) != 2 {
		t.Errorf("investigated %d times with %d findings, want an ordinary review of both", reviewer.investigated, len(report.Findings))
	}
}

// With no checkout there is nothing to search, so the batch is reviewed as it
// always was.
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

// A first call that fails before anything was read leaves nothing to rescue, so
// the batch is reviewed the ordinary way and its findings are published.
func TestAnInvestigationThatFailsBeforeReadingAnythingFallsBackToTheOrdinaryReview(t *testing.T) {
	fs := twoFindings()
	reviewer := &investigatorLLM{failFirst: errors.New("provider down"), ordinary: mustJSON(t, Result{Findings: fs})}
	e := investigateEngine(t, reviewer, true, appRepo(t))
	report, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if reviewer.investigated == 0 || reviewer.reviewed == 0 || len(report.Findings) != 2 {
		t.Errorf("investigated %d, reviewed %d, findings %d; want a fallback to the ordinary review",
			reviewer.investigated, reviewer.reviewed, len(report.Findings))
	}
}

// The guard R5b lacked. A reviewer that has read the repository and then stops
// without submitting is asked to submit over the same transcript, and what it
// submits is the batch's answer. Falling back to a diff-only review here would
// throw away the lookups the findings rest on.
func TestAReviewerThatStopsAfterLookingIsAskedToSubmitAndItsFindingsAreKept(t *testing.T) {
	fs := twoFindings()
	reviewer := &investigatorLLM{
		script:        []llms.Response{lookup("app.go")},
		forcedReplies: []llms.Response{submit(t, fs[:1])},
		ordinary:      mustJSON(t, Result{Findings: fs[1:]}),
	}
	e := investigateEngine(t, reviewer, true, appRepo(t))
	report, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if got := titles(report); len(got) != 1 || got[0] != fs[0].Title {
		t.Errorf("findings = %v, want the forced submission's %q", got, fs[0].Title)
	}
	if reviewer.forced != 1 || reviewer.reviewed != 0 {
		t.Errorf("forced submissions %d and ordinary reviews %d, want one and none", reviewer.forced, reviewer.reviewed)
	}
}

// A reviewer that never stops looking is cut off at the turn budget and asked to
// submit, rather than running until the request timeout or being discarded.
func TestAReviewerThatRunsOutOfTurnsIsAskedToSubmit(t *testing.T) {
	endless := make([]llms.Response, 40)
	for i := range endless {
		endless[i] = lookup("app.go")
	}
	fs := twoFindings()
	reviewer := &investigatorLLM{
		script:        endless,
		forcedReplies: []llms.Response{submit(t, fs[:1])},
		ordinary:      mustJSON(t, Result{Findings: fs[1:]}),
	}
	e := investigateEngine(t, reviewer, true, appRepo(t))
	report, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if reviewer.investigated != investigateMaxTurns {
		t.Errorf("investigated %d turns, want exactly the budget of %d", reviewer.investigated, investigateMaxTurns)
	}
	if reviewer.forced != 1 || reviewer.reviewed != 0 || len(report.Findings) != 1 {
		t.Errorf("forced %d, ordinary %d, findings %d; want one forced submission and its finding", reviewer.forced, reviewer.reviewed, len(report.Findings))
	}
}

// A turn that fails after the reviewer has read something still ends in a
// submission over what was read.
func TestAFailedTurnAfterLookingStillSubmitsWhatWasRead(t *testing.T) {
	fs := twoFindings()
	reviewer := &investigatorLLM{
		script:        []llms.Response{lookup("app.go")},
		failAfter:     errors.New("provider down"),
		forcedReplies: []llms.Response{submit(t, fs[:1])},
		ordinary:      mustJSON(t, Result{Findings: fs[1:]}),
	}
	e := investigateEngine(t, reviewer, true, appRepo(t))
	report, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if reviewer.forced != 1 || reviewer.reviewed != 0 || len(report.Findings) != 1 {
		t.Errorf("forced %d, ordinary %d, findings %d; want a forced submission after the failed turn", reviewer.forced, reviewer.reviewed, len(report.Findings))
	}
}

// A forced submission that comes back as prose is asked once more.
func TestAForcedSubmissionThatComesBackAsProseIsAskedAgain(t *testing.T) {
	fs := twoFindings()
	reviewer := &investigatorLLM{
		script:        []llms.Response{lookup("app.go")},
		forcedReplies: []llms.Response{{Content: "Here is what I found."}, submit(t, fs[:1])},
		ordinary:      mustJSON(t, Result{Findings: fs[1:]}),
	}
	e := investigateEngine(t, reviewer, true, appRepo(t))
	report, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if reviewer.forced != 2 || reviewer.reviewed != 0 || len(report.Findings) != 1 {
		t.Errorf("forced %d, ordinary %d, findings %d; want a second request that is answered", reviewer.forced, reviewer.reviewed, len(report.Findings))
	}
}

// A reasoning model can spend the whole output budget thinking and answer nothing.
// The forced submission is then asked again with reasoning off, as the ordinary
// review does, and the findings it yields are kept.
func TestAForcedSubmissionThatIsEmptyAtTheOutputCapIsAskedAgainWithoutReasoning(t *testing.T) {
	fs := twoFindings()
	reviewer := &investigatorLLM{
		script:        []llms.Response{lookup("app.go")},
		forcedReplies: []llms.Response{{FinishReason: llms.FinishReasonLength}, submit(t, fs[:1])},
		ordinary:      mustJSON(t, Result{Findings: fs[1:]}),
	}
	e := investigateEngine(t, reviewer, true, appRepo(t))
	report, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if reviewer.forced != 2 || reviewer.reviewed != 0 || len(report.Findings) != 1 {
		t.Fatalf("forced %d, ordinary %d, findings %d; want a second forced request that is answered", reviewer.forced, reviewer.reviewed, len(report.Findings))
	}
	if len(reviewer.forcedOff) != 2 || reviewer.forcedOff[0] || !reviewer.forcedOff[1] {
		t.Errorf("reasoning switched off per forced call = %v, want on for the first and off for the retry", reviewer.forcedOff)
	}
}

// An empty reply at the cap is retried without reasoning once. A second one is the
// model saying it cannot answer, and the batch is reviewed the ordinary way.
func TestAForcedSubmissionThatIsEmptyAtTheCapTwiceFallsBackToTheOrdinaryReview(t *testing.T) {
	fs := twoFindings()
	empty := llms.Response{FinishReason: llms.FinishReasonLength}
	reviewer := &investigatorLLM{
		script:        []llms.Response{lookup("app.go")},
		forcedReplies: []llms.Response{empty, empty, empty},
		ordinary:      mustJSON(t, Result{Findings: fs}),
	}
	e := investigateEngine(t, reviewer, true, appRepo(t))
	report, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if reviewer.forced != 2 || reviewer.reviewed == 0 || len(report.Findings) != 2 {
		t.Errorf("forced %d, ordinary %d, findings %d; want two forced requests and then the ordinary review", reviewer.forced, reviewer.reviewed, len(report.Findings))
	}
}

// If the model will not submit even when forced, the batch is reviewed the
// ordinary way and not left without an answer.
func TestAForcedSubmissionThatNeverComesFallsBackToTheOrdinaryReview(t *testing.T) {
	fs := twoFindings()
	reviewer := &investigatorLLM{
		script:   []llms.Response{lookup("app.go")},
		ordinary: mustJSON(t, Result{Findings: fs}),
	}
	e := investigateEngine(t, reviewer, true, appRepo(t))
	report, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if reviewer.reviewed == 0 || len(report.Findings) != 2 {
		t.Errorf("ordinary reviews %d, findings %d; want the ordinary review once forcing failed", reviewer.reviewed, len(report.Findings))
	}
}

// Submitting an empty list is the answer "nothing to report", and it must stay
// that: falling back to the ordinary review would turn silence into findings.
func TestASubmittedEmptyListIsKeptAsSilence(t *testing.T) {
	reviewer := &investigatorLLM{
		script:   []llms.Response{lookup("app.go"), submit(t, nil)},
		ordinary: mustJSON(t, Result{Findings: twoFindings()}),
	}
	e := investigateEngine(t, reviewer, true, appRepo(t))
	report, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Findings) != 0 || reviewer.reviewed != 0 || reviewer.forced != 0 {
		t.Errorf("findings %d, ordinary reviews %d, forced %d, want none of them after an empty submission", len(report.Findings), reviewer.reviewed, reviewer.forced)
	}
}

// Reading is bounded by bytes shown, not by a count of lookups: once the budget is
// spent a lookup is answered with an instruction to submit.
func TestOnceTheToolOutputBudgetIsSpentLookupsAreRefused(t *testing.T) {
	line := strings.Repeat("x", 120)
	big := strings.Repeat(line+"\n", 150) // a window larger than one tool result may show
	// Four lookups in a turn, as a reviewer reading several files at once would
	// make, so that the byte budget and not the turn budget is what ends it.
	reads := make([]llms.Response, 0, 12)
	for range 12 {
		reads = append(reads, llms.Response{ToolCalls: []llms.ToolCall{
			lookup("big.go").ToolCalls[0], lookup("big.go").ToolCalls[0],
			lookup("big.go").ToolCalls[0], lookup("big.go").ToolCalls[0],
		}})
	}
	fs := twoFindings()
	reviewer := &investigatorLLM{
		script:        reads,
		forcedReplies: []llms.Response{submit(t, fs[:1])},
		ordinary:      mustJSON(t, Result{Findings: fs[1:]}),
	}
	e := investigateEngine(t, reviewer, true, gitRepo(t, map[string]string{"big.go": big, "app.go": "package app\n"}))
	if _, err := e.Review(context.Background(), vcs.Ref{}); err != nil {
		t.Fatal(err)
	}
	spent := 0
	shownBytes := 0
	for _, r := range reviewer.toolResults {
		if strings.Contains(r, "the lookup budget is spent") {
			spent++
		} else {
			shownBytes += len(r)
		}
	}
	if spent == 0 {
		t.Fatalf("no lookup was refused after %d bytes were shown across %d results", shownBytes, len(reviewer.toolResults))
	}
	if shownBytes > investigateToolBytes+toolResultBytes {
		t.Errorf("showed %d bytes, want the budget of %d plus at most one more result", shownBytes, investigateToolBytes)
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
	e.RepoRoot = appRepo(t)

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
