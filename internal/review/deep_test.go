package review

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	llms "github.com/nocturnium/llm-go-sdk/v6"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/llm"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// deepLLM answers every request a deep review makes. A request under the
// critique prompt is the critique. A request whose system prompt carries the
// lookup note is an agent turn, the security agent's when the prompt also
// carries the security focus: step N of that agent's script answers its Nth
// turn, and a turn whose last message asks for submit_findings now is a forced
// submission. Anything else is the ordinary single-call review.
type deepLLM struct {
	mu sync.Mutex

	general, security       []llms.Response
	generalErr, securityErr error
	forcedReplies           []llms.Response
	grades                  *llms.Response
	critiqueErr             error
	ordinary                string

	turns                       map[string]int
	forced, critiqued, reviewed int
	forcedOff                   []bool
	sampling                    []*llms.CallOptions
	offeredTools                int
	critiqueRequest             string
}

func (d *deepLLM) GenerateContent(_ context.Context, msgs []llms.Message, opts ...llms.CallOption) (*llms.Response, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	applied := llms.ApplyOptions(opts...)
	if len(applied.Tools) > 0 {
		d.offeredTools++
	}
	system := ""
	if len(msgs) > 0 {
		system = msgs[0].Content
	}
	switch {
	case strings.Contains(system, "You are grading draft review comments"):
		d.critiqued++
		d.critiqueRequest = msgs[len(msgs)-1].Content
		if d.critiqueErr != nil {
			return nil, d.critiqueErr
		}
		if d.grades == nil {
			return &llms.Response{Content: "no grades"}, nil
		}
		out := *d.grades
		return &out, nil
	case !strings.Contains(system, "## Looking things up"):
		d.reviewed++
		return &llms.Response{Content: d.ordinary}, nil
	}
	agent, script, fail := config.DeepAgentGeneral, d.general, d.generalErr
	if strings.Contains(system, "## Your focus: security") {
		agent, script, fail = config.DeepAgentSecurity, d.security, d.securityErr
	}
	if last := msgs[len(msgs)-1]; last.Role == llms.RoleUser && strings.Contains(last.Content, "submit_findings now") {
		i := d.forced
		d.forced++
		r := applied.Reasoning
		d.forcedOff = append(d.forcedOff, r != nil && r.Enabled != nil && !*r.Enabled)
		if i >= len(d.forcedReplies) {
			return &llms.Response{Content: "prose"}, nil
		}
		out := d.forcedReplies[i]
		return &out, nil
	}
	if d.turns == nil {
		d.turns = map[string]int{}
	}
	d.turns[agent]++
	d.sampling = append(d.sampling, applied)
	if fail != nil {
		return nil, fail
	}
	step := 0
	for _, m := range msgs {
		if m.Role == llms.RoleAssistant {
			step++
		}
	}
	if step >= len(script) {
		return &llms.Response{Content: "I am done."}, nil
	}
	out := script[step]
	return &out, nil
}
func (d *deepLLM) Stream(context.Context, []llms.Message, ...llms.CallOption) (<-chan llms.StreamChunk, error) {
	return nil, errors.New("not supported")
}
func (d *deepLLM) Provider() llms.Provider { return "deep" }
func (d *deepLLM) Model() string           { return "deep" }

// confident returns fs with every finding at confidence c, so the deep
// filter's floor is not what a test about the agents measures.
func confident(fs []Finding, c float64) []Finding {
	out := make([]Finding, len(fs))
	for i, f := range fs {
		v := c
		f.Confidence = &v
		out[i] = f
	}
	return out
}

func submit(t *testing.T, fs []Finding) llms.Response {
	t.Helper()
	return llms.Response{ToolCalls: []llms.ToolCall{{
		ID: "s", Type: llms.ToolTypeFunction,
		Function: &llms.FunctionCall{Name: submitFindings, Arguments: mustJSONAny(t, Result{Findings: fs})},
	}}}
}

// gradeAll grades every finding numbered in order with one grade.
func gradeAll(t *testing.T, evidence ...string) *llms.Response {
	t.Helper()
	var grades []critiqueGrade
	for i, e := range evidence {
		grades = append(grades, critiqueGrade{Number: i, Evidence: e, Reason: "the code shows it"})
	}
	return &llms.Response{ToolCalls: []llms.ToolCall{{
		ID: "g", Type: llms.ToolTypeFunction,
		Function: &llms.FunctionCall{Name: submitGrades, Arguments: mustJSONAny(t, map[string]any{"grades": grades})},
	}}}
}

func deepEngine(t *testing.T, reviewer *deepLLM, root string) (*Engine, *scriptedLLM) {
	t.Helper()
	e, model := verifyEngine(t, &toolLLM{}, "")
	e.Roles.Review = llm.NewClientForTest(reviewer, e.Config.Models.Default)
	e.Deep = true
	e.RepoRoot = root
	return e, model
}

func appRepo(t *testing.T) string {
	t.Helper()
	return gitRepo(t, map[string]string{"app.go": "package app\n"})
}

func titles(report *Report) []string {
	var out []string
	for _, f := range report.Findings {
		out = append(out, f.Title)
	}
	return out
}

func triaged(model *scriptedLLM) bool {
	model.mu.Lock()
	defer model.mu.Unlock()
	for _, p := range model.seen {
		if strings.Contains(p, "triaging findings") {
			return true
		}
	}
	return false
}

// Each agent reads the repository and submits what it found; the review posts
// both agents' findings, and neither triage nor the ordinary call is asked.
func TestDeepReviewKeepsTheFindingsOfBothTheGeneralAndTheSecurityAgent(t *testing.T) {
	fs := confident(twoFindings(), 0.9)
	reviewer := &deepLLM{
		general:  []llms.Response{lookup("app.go"), submit(t, fs[:1])},
		security: []llms.Response{submit(t, fs[1:])},
		grades:   gradeAll(t, "proven", "proven"),
		ordinary: mustJSON(t, Result{}),
	}
	e, model := deepEngine(t, reviewer, appRepo(t))
	report, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if got := titles(report); len(got) != 2 {
		t.Fatalf("findings = %v, want one from each agent", got)
	}
	if reviewer.turns[config.DeepAgentGeneral] == 0 || reviewer.turns[config.DeepAgentSecurity] == 0 {
		t.Errorf("agent turns = %v, want both agents asked", reviewer.turns)
	}
	if reviewer.reviewed != 0 || triaged(model) {
		t.Errorf("ordinary reviews %d, triaged %v; want neither in a deep review", reviewer.reviewed, triaged(model))
	}
}

// One agent failing outright costs the review that agent, not the other's
// findings and not a fall back to the ordinary call.
func TestADeepAgentThatFailsLeavesTheOtherAgentsFindingsInTheReview(t *testing.T) {
	fs := confident(twoFindings(), 0.9)
	reviewer := &deepLLM{
		general:     []llms.Response{submit(t, fs[:1])},
		securityErr: errors.New("upstream 503"),
		grades:      gradeAll(t, "proven"),
		ordinary:    mustJSON(t, Result{Findings: fs[1:]}),
	}
	e, _ := deepEngine(t, reviewer, appRepo(t))
	report, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if got := titles(report); len(got) != 1 || got[0] != fs[0].Title {
		t.Errorf("findings = %v, want only the general agent's %q", got, fs[0].Title)
	}
	if reviewer.reviewed != 0 {
		t.Errorf("ordinary reviews = %d, want none while one agent finished", reviewer.reviewed)
	}
}

// With every agent failed there is nothing of the deep review to keep, so the
// batch is reviewed by the ordinary call, whose findings still pass the filter.
func TestWhenEveryDeepAgentFailsTheBatchIsReviewedTheOrdinaryWay(t *testing.T) {
	fs := confident(twoFindings(), 0.9)
	reviewer := &deepLLM{
		generalErr:  errors.New("upstream 503"),
		securityErr: errors.New("upstream 503"),
		grades:      gradeAll(t, "proven", "proven"),
		ordinary:    mustJSON(t, Result{Findings: fs}),
	}
	e, _ := deepEngine(t, reviewer, appRepo(t))
	report, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if reviewer.reviewed != 1 || len(report.Findings) != 2 {
		t.Errorf("ordinary reviews %d with %d findings, want one ordinary review of both", reviewer.reviewed, len(report.Findings))
	}
}

// With no checkout there is nothing to search, so the batch is reviewed by the
// ordinary call.
func TestDeepReviewWithoutACheckoutReviewsTheOrdinaryWay(t *testing.T) {
	fs := confident(twoFindings(), 0.9)
	reviewer := &deepLLM{grades: gradeAll(t, "proven", "proven"), ordinary: mustJSON(t, Result{Findings: fs})}
	e, _ := deepEngine(t, reviewer, "")
	if _, err := e.Review(context.Background(), vcs.Ref{}); err != nil {
		t.Fatal(err)
	}
	if reviewer.reviewed != 1 || len(reviewer.turns) != 0 {
		t.Errorf("ordinary reviews %d, agent turns %v; want the ordinary review alone", reviewer.reviewed, reviewer.turns)
	}
}

// An agent that keeps looking until its turns run out is asked to submit, and
// what it submits is kept.
func TestADeepAgentThatRunsOutOfTurnsIsAskedToSubmit(t *testing.T) {
	fs := confident(twoFindings(), 0.9)
	reviewer := &deepLLM{
		general:       []llms.Response{lookup("app.go"), lookup("app.go"), lookup("app.go")},
		security:      []llms.Response{submit(t, nil)},
		forcedReplies: []llms.Response{submit(t, fs[:1])},
		grades:        gradeAll(t, "proven"),
	}
	e, _ := deepEngine(t, reviewer, appRepo(t))
	e.Config.Review.Deep.MaxTurns = 2
	report, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if reviewer.turns[config.DeepAgentGeneral] != 2 || reviewer.forced != 1 {
		t.Errorf("general turns %d, forced submissions %d; want 2 turns then one forced submission", reviewer.turns[config.DeepAgentGeneral], reviewer.forced)
	}
	if got := titles(report); len(got) != 1 || got[0] != fs[0].Title {
		t.Errorf("findings = %v, want the forced submission's %q", got, fs[0].Title)
	}
}

// An agent that answers in prose without reading anything is still asked to
// submit: its answer may hold findings, and the agent is otherwise lost.
func TestADeepAgentThatAnswersInProseIsAskedToSubmit(t *testing.T) {
	fs := confident(twoFindings(), 0.9)
	reviewer := &deepLLM{
		general:       []llms.Response{{Content: "The deferred Close panics when Get fails."}},
		security:      []llms.Response{submit(t, nil)},
		forcedReplies: []llms.Response{submit(t, fs[:1])},
		grades:        gradeAll(t, "proven"),
	}
	e, _ := deepEngine(t, reviewer, appRepo(t))
	report, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if reviewer.forced != 1 || reviewer.reviewed != 0 {
		t.Errorf("forced submissions %d, ordinary reviews %d; want one forced submission and no ordinary review", reviewer.forced, reviewer.reviewed)
	}
	if got := titles(report); len(got) != 1 {
		t.Errorf("findings = %v, want the submitted finding", got)
	}
}

// A forced submission that comes back empty because reasoning used the output
// budget is asked once more, with reasoning off.
func TestAForcedSubmissionEmptyAtTheOutputCapIsAskedAgainWithoutReasoning(t *testing.T) {
	fs := confident(twoFindings(), 0.9)
	reviewer := &deepLLM{
		general:  []llms.Response{lookup("app.go")},
		security: []llms.Response{submit(t, nil)},
		forcedReplies: []llms.Response{
			{FinishReason: llms.FinishReasonLength},
			submit(t, fs[:1]),
		},
		grades: gradeAll(t, "proven"),
	}
	e, _ := deepEngine(t, reviewer, appRepo(t))
	report, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if len(reviewer.forcedOff) != 2 || reviewer.forcedOff[0] || !reviewer.forcedOff[1] {
		t.Errorf("reasoning off per forced call = %v, want on then off", reviewer.forcedOff)
	}
	if len(report.Findings) != 1 {
		t.Errorf("findings = %v, want the second attempt's", titles(report))
	}
}

// The agents sample at review.deep's temperature and reasoning level, not the
// review model's own; changing the configuration changes what is sent.
func TestDeepAgentsSampleAtTheDeepTemperatureAndReasoningLevel(t *testing.T) {
	for _, tc := range []struct {
		temperature float64
		reasoning   config.ReasoningLevel
	}{{0.2, config.ReasoningMedium}, {0.6, config.ReasoningHigh}} {
		reviewer := &deepLLM{
			general:  []llms.Response{submit(t, nil)},
			security: []llms.Response{submit(t, nil)},
		}
		e, _ := deepEngine(t, reviewer, appRepo(t))
		e.Config.Review.Deep.Temperature = tc.temperature
		e.Config.Review.Deep.Reasoning = tc.reasoning
		if _, err := e.Review(context.Background(), vcs.Ref{}); err != nil {
			t.Fatal(err)
		}
		if len(reviewer.sampling) == 0 {
			t.Fatal("no agent turn was recorded")
		}
		for _, s := range reviewer.sampling {
			if s.Temperature == nil || *s.Temperature != tc.temperature {
				t.Errorf("temperature = %v, want %v", s.Temperature, tc.temperature)
			}
			if s.Reasoning == nil || s.Reasoning.Effort != llms.ReasoningEffort(tc.reasoning) {
				t.Errorf("reasoning = %+v, want effort %q", s.Reasoning, tc.reasoning)
			}
		}
	}
}

// The ordinary review is untouched by deep review: no request it makes offers
// a tool, and none carries the lookup note.
func TestTheOrdinaryReviewNeverOffersTools(t *testing.T) {
	fs := twoFindings()
	reviewer := &deepLLM{ordinary: mustJSON(t, Result{Findings: fs})}
	e, _ := deepEngine(t, reviewer, appRepo(t))
	e.Deep = false
	report, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if reviewer.offeredTools != 0 || len(reviewer.turns) != 0 || reviewer.critiqued != 0 {
		t.Errorf("requests with tools %d, agent turns %v, critiques %d; want none", reviewer.offeredTools, reviewer.turns, reviewer.critiqued)
	}
	if len(report.Findings) != 2 {
		t.Errorf("findings = %v, want both from the ordinary review", titles(report))
	}
}

// Agents that read and file nothing produce a review with no findings and no
// failed stage: silence that was earned, not a stage that did not run.
func TestDeepAgentsThatSubmitNothingProduceASilentReviewWithNoFailedStage(t *testing.T) {
	reviewer := &deepLLM{
		general:  []llms.Response{lookup("app.go"), submit(t, nil)},
		security: []llms.Response{submit(t, nil)},
		ordinary: mustJSON(t, Result{Findings: twoFindings()}),
	}
	e, _ := deepEngine(t, reviewer, appRepo(t))
	report, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Findings) != 0 || len(report.Stages) != 0 {
		t.Errorf("findings %v, stages %v; want none of either", titles(report), report.Stages)
	}
	if reviewer.reviewed != 0 || reviewer.critiqued != 0 {
		t.Errorf("ordinary reviews %d, critiques %d; want neither for an empty submission", reviewer.reviewed, reviewer.critiqued)
	}
}

// A resumed run serves cached answers keyed on the configuration, and the
// engine's mode is not configuration: flipping deep review on must not replay
// the ordinary review's answer.
func TestFlippingDeepReviewBetweenResumedRunsDoesNotReplayTheOtherModesAnswer(t *testing.T) {
	fs := confident(twoFindings(), 0.9)
	reviewer := &deepLLM{
		general:  []llms.Response{submit(t, fs[:1])},
		security: []llms.Response{submit(t, nil)},
		grades:   gradeAll(t, "proven"),
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
	e.Deep = true
	report, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if report.ReusedRequests != 0 || reviewer.turns[config.DeepAgentGeneral] == 0 {
		t.Errorf("reused %d requests and ran the general agent %d times, want a live deep review after the flip",
			report.ReusedRequests, reviewer.turns[config.DeepAgentGeneral])
	}
}
