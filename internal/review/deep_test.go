package review

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	llms "github.com/nocturnium/llm-go-sdk/v6"

	"github.com/nitpick-oss/nitpick/internal/config"
	"github.com/nitpick-oss/nitpick/internal/llm"
	"github.com/nitpick-oss/nitpick/internal/vcs"
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

	// refuseTemperature answers a request that carries a temperature with the
	// router's refusal, as the newest models behind require_parameters do: "all"
	// refuses every such request, "forced" only a forced submission and
	// "critique" only the critique. The last two keep the refusal from being
	// learned on an earlier call, so the call under test is the one that meets it.
	refuseTemperature string
	refused           int

	// delay is how long every call takes, for a test about wall-clock time.
	delay time.Duration

	// shown is the newest tool message each agent turn was handed, so a test
	// can say what the agent was told after a call it made.
	shown           []string
	forcedReplies   []llms.Response
	grades          *llms.Response
	critiqueReplies []llms.Response
	critiqueErr     error
	ordinary        string

	turns                       map[string]int
	forced, critiqued, reviewed int
	forcedOff                   []bool
	forcedEffort                []llms.ReasoningEffort
	forcedMaxTokens             []int
	critiqueOff                 []bool
	sampling                    []*llms.CallOptions
	offeredTools                int
	critiqueRequest             string
}

func (d *deepLLM) GenerateContent(ctx context.Context, msgs []llms.Message, opts ...llms.CallOption) (*llms.Response, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if d.delay > 0 {
		time.Sleep(d.delay)
	}
	applied := llms.ApplyOptions(opts...)
	system := ""
	if len(msgs) > 0 {
		system = msgs[0].Content
	}
	if d.refuseTemperature == "always" {
		d.refused++
		return nil, errors.New("openrouter: generate content: No endpoints found that can handle the requested parameters")
	}
	if applied.Temperature != nil {
		isCritique := strings.Contains(system, "You are grading draft review comments")
		isForced := len(msgs) > 0 && strings.Contains(msgs[len(msgs)-1].Content, "submit_findings now")
		if d.refuseTemperature == "all" || (d.refuseTemperature == "forced" && isForced) || (d.refuseTemperature == "critique" && isCritique) {
			d.refused++
			return nil, errors.New("openrouter: generate content: No endpoints found that can handle the requested parameters")
		}
	}
	if len(applied.Tools) > 0 {
		d.offeredTools++
	}
	switch {
	case strings.Contains(system, "You are grading draft review comments"):
		d.critiqued++
		d.critiqueRequest = msgs[len(msgs)-1].Content
		r := applied.Reasoning
		d.critiqueOff = append(d.critiqueOff, r != nil && r.Enabled != nil && !*r.Enabled)
		if d.critiqueErr != nil {
			return nil, d.critiqueErr
		}
		if i := d.critiqued - 1; i < len(d.critiqueReplies) {
			out := d.critiqueReplies[i]
			return &out, nil
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
		effort, maxTokens := llms.ReasoningEffort(""), 0
		if r != nil {
			effort = r.Effort
		}
		if applied.MaxTokens != nil {
			maxTokens = *applied.MaxTokens
		}
		d.forcedEffort = append(d.forcedEffort, effort)
		d.forcedMaxTokens = append(d.forcedMaxTokens, maxTokens)
		if i >= len(d.forcedReplies) {
			return &llms.Response{Content: "prose"}, nil
		}
		out := d.forcedReplies[i]
		return &out, nil
	}
	if last := msgs[len(msgs)-1]; last.Role == llms.RoleTool {
		d.shown = append(d.shown, last.Content)
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
	// These tests are about the agent loop, and deepLLM finds the security agent
	// by this tool's own focus section, which Mira's prompt does not carry.
	e.Config.Review.Deep.Prompts = config.DeepPromptsNitpick
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
// findings and not a fall back to the ordinary call. The report names the agent
// that failed, so a missing security agent is not read as a clean security
// review.
func TestADeepAgentThatFailsLeavesTheOtherAgentsFindingsAndIsNamedInTheReport(t *testing.T) {
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
	if got := report.FailedStages(); len(got) != 1 || got[0] != "deep-review security agent" {
		t.Errorf("failed stages = %v, want the security agent named", got)
	}
	if report.PipelineComplete() {
		t.Error("the review reads as complete with its security agent missing")
	}
	if notice := stageNotice(report); !strings.Contains(notice, "deep-review security agent failed") {
		t.Errorf("notice = %q, want it to say the security agent failed", notice)
	}
}

// With every agent failed there is nothing of the deep review to keep, so the
// batch is reviewed by the ordinary call, whose findings still pass the filter,
// and the report says the batch was not reviewed by the agents.
func TestWhenEveryDeepAgentFailsTheBatchIsReviewedTheOrdinaryWayAndTheReportSaysSo(t *testing.T) {
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
	want := []string{"deep-review general agent", "deep-review security agent", "deep-review"}
	if got := report.FailedStages(); !slices.Equal(got, want) {
		t.Errorf("failed stages = %v, want %v", got, want)
	}
	if notice := stageNotice(report); !strings.Contains(notice, "without tools (every agent failed)") {
		t.Errorf("notice = %q, want it to say the batch ran without tools", notice)
	}
}

// RequestTimeout bounds one request, and a deep review makes many, which
// review.deep bounds itself. Applied to the agents, it expired during their run
// and the fallback call inherited it, so a batch the ordinary call could still
// have reviewed failed instead. Every call here takes 150ms and the limit is
// 200ms: the two failed agent turns use 300ms between them, and the fallback
// call still answers.
func TestRequestTimeoutBoundsTheFallbackCallAndNotTheDeepAgentsRun(t *testing.T) {
	fs := confident(twoFindings(), 0.9)
	reviewer := &deepLLM{
		generalErr:  errors.New("upstream 503"),
		securityErr: errors.New("upstream 503"),
		grades:      gradeAll(t, "proven", "proven"),
		ordinary:    mustJSON(t, Result{Findings: fs}),
		delay:       150 * time.Millisecond,
	}
	e, _ := deepEngine(t, reviewer, appRepo(t))
	e.RequestTimeout = 200 * time.Millisecond
	report, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if reviewer.reviewed != 1 || len(report.Findings) != 2 || len(report.Incomplete) != 0 {
		t.Errorf("ordinary reviews %d, %d findings, unreviewed %v; want the fallback to review both findings' file",
			reviewer.reviewed, len(report.Findings), report.Incomplete)
	}
}

// A degraded batch is not saved for a resumed run. Saved, it would be served
// back without the agent that failed, and the resumed review would read as
// whole.
func TestADegradedDeepBatchIsNotServedFromTheCacheOnAResumedRun(t *testing.T) {
	fs := confident(twoFindings(), 0.9)
	reviewer := &deepLLM{
		general:     []llms.Response{submit(t, fs[:1])},
		securityErr: errors.New("upstream 503"),
		grades:      gradeAll(t, "proven"),
	}
	provider := &incrementalProvider{stubProvider: stubProvider{diff: engineDiff}, head: "abcdef"}
	e := resumeEngine(t, &scriptedLLM{fallback: `{"findings":[]}`}, provider)
	e.Roles.Review = llm.NewClientForTest(reviewer, e.Config.Models.Default)
	e.Config.Review.Deep.Prompts = config.DeepPromptsNitpick
	e.Deep = true
	e.RepoRoot = appRepo(t)

	first, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.FailedStages()) == 0 {
		t.Fatal("the first run did not report its failed agent")
	}
	reviewer.securityErr = nil
	reviewer.security = []llms.Response{submit(t, fs[1:])}
	provider.prior = &vcs.PriorReview{Progress: provider.published.Progress}
	second, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if second.ReusedRequests != 0 || reviewer.turns[config.DeepAgentSecurity] < 2 {
		t.Errorf("reused %d requests and asked the security agent %d times, want the degraded batch run again",
			second.ReusedRequests, reviewer.turns[config.DeepAgentSecurity])
	}
	if len(second.FailedStages()) != 0 {
		t.Errorf("failed stages = %v, want none once the agent answered", second.FailedStages())
	}
}

// With no checkout there is nothing to search, so the batch is reviewed by the
// ordinary call, and the report says a deep review was asked for and not done.
func TestDeepReviewWithoutACheckoutReviewsTheOrdinaryWayAndTheReportSaysSo(t *testing.T) {
	fs := confident(twoFindings(), 0.9)
	reviewer := &deepLLM{grades: gradeAll(t, "proven", "proven"), ordinary: mustJSON(t, Result{Findings: fs})}
	e, _ := deepEngine(t, reviewer, "")
	report, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if reviewer.reviewed != 1 || len(reviewer.turns) != 0 {
		t.Errorf("ordinary reviews %d, agent turns %v; want the ordinary review alone", reviewer.reviewed, reviewer.turns)
	}
	if got := report.FailedStages(); len(got) != 1 || got[0] != "deep-review" {
		t.Errorf("failed stages = %v, want the deep review named", got)
	}
	if notice := stageNotice(report); !strings.Contains(notice, "no git checkout to search") {
		t.Errorf("notice = %q, want the missing checkout named", notice)
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

// A forced submission that runs out of output budget steps down a ladder: it
// first gets room to finish at the configured reasoning, then low effort, then
// none, and the findings of the step that answers are kept.
func TestAForcedSubmissionThatRunsOutStepsDownToLowEffortThenNoReasoning(t *testing.T) {
	fs := confident(twoFindings(), 0.9)
	reviewer := &deepLLM{
		general:  []llms.Response{lookup("app.go")},
		security: []llms.Response{submit(t, nil)},
		forcedReplies: []llms.Response{
			{FinishReason: llms.FinishReasonLength},
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
	if len(reviewer.forcedEffort) != 3 {
		t.Fatalf("forced calls = %d, want three steps", len(reviewer.forcedEffort))
	}
	if reviewer.forcedEffort[0] != llms.ReasoningEffort(config.ReasoningMedium) || reviewer.forcedOff[0] {
		t.Errorf("first step effort %q, off %v; want the configured medium", reviewer.forcedEffort[0], reviewer.forcedOff[0])
	}
	if reviewer.forcedEffort[1] != llms.ReasoningEffort(config.ReasoningLow) || reviewer.forcedOff[1] {
		t.Errorf("second step effort %q, off %v; want low effort, still reasoning", reviewer.forcedEffort[1], reviewer.forcedOff[1])
	}
	if !reviewer.forcedOff[2] {
		t.Error("third step still reasons, want reasoning off as the last resort")
	}
	if len(report.Findings) != 1 {
		t.Errorf("findings = %v, want the last step's", titles(report))
	}
}

// The forced submission thinks over the whole transcript, so it is given more
// room than an ordinary turn before it is counted as having run out.
func TestAForcedSubmissionGetsRoomToFinishThinking(t *testing.T) {
	reviewer := &deepLLM{
		general:       []llms.Response{lookup("app.go")},
		security:      []llms.Response{submit(t, nil)},
		forcedReplies: []llms.Response{submit(t, nil)},
	}
	e, _ := deepEngine(t, reviewer, appRepo(t))
	if _, err := e.Review(context.Background(), vcs.Ref{}); err != nil {
		t.Fatal(err)
	}
	if len(reviewer.forcedMaxTokens) != 1 || reviewer.forcedMaxTokens[0] != deepForcedMaxTokens {
		t.Errorf("forced max tokens = %v, want %d", reviewer.forcedMaxTokens, deepForcedMaxTokens)
	}
}

// A forced submission cut at the cap part way through its tool call has not
// submitted: arguments cut off do not hold the findings, so it steps down too.
func TestAForcedSubmissionCutMidAnswerStepsDownTheLadder(t *testing.T) {
	fs := confident(twoFindings(), 0.9)
	cut := submit(t, fs)
	cut.FinishReason = llms.FinishReasonLength
	cut.ToolCalls[0].Function.Arguments = cut.ToolCalls[0].Function.Arguments[:20]
	reviewer := &deepLLM{
		general:       []llms.Response{lookup("app.go")},
		security:      []llms.Response{submit(t, nil)},
		forcedReplies: []llms.Response{cut, submit(t, fs[:1])},
		grades:        gradeAll(t, "proven"),
	}
	e, _ := deepEngine(t, reviewer, appRepo(t))
	report, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if len(reviewer.forcedEffort) != 2 || reviewer.forcedEffort[1] != llms.ReasoningEffort(config.ReasoningLow) {
		t.Errorf("forced efforts = %v, want a second step at low effort", reviewer.forcedEffort)
	}
	if len(report.Findings) != 1 {
		t.Errorf("findings = %v, want the second step's one finding", titles(report))
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

// A model that takes no temperature still gets a deep review: every call, the
// agents' turns, their forced submissions and the critique, is sent again
// without it, and the review reads as complete.
func TestADeepReviewCompletesOnAModelThatTakesNoTemperature(t *testing.T) {
	fs := confident(twoFindings(), 0.9)
	reviewer := &deepLLM{
		refuseTemperature: "all",
		general:           []llms.Response{lookup("app.go"), submit(t, fs[:1])},
		security:          []llms.Response{submit(t, fs[1:])},
		grades:            gradeAll(t, "proven", "proven"),
	}
	e, _ := deepEngine(t, reviewer, appRepo(t))
	report, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if got := titles(report); len(got) != 2 {
		t.Errorf("findings = %v, want one from each agent", got)
	}
	if len(report.FailedStages()) != 0 || !report.PipelineComplete() {
		t.Errorf("failed stages = %v; a model that takes no temperature must not make the review incomplete", report.FailedStages())
	}
	if reviewer.refused == 0 {
		t.Error("the model was never asked with a temperature, so the test did not exercise the refusal")
	}
	// Each of the two agents can meet the refusal once, on its first call. After
	// that the client has learned it, and a call whose options were built earlier
	// must still leave the temperature out instead of being refused again.
	if reviewer.refused > 2 {
		t.Errorf("the model refused %d requests, want at most one per agent: a refusal already learned must not be paid for again", reviewer.refused)
	}
	if reviewer.critiqued == 0 {
		t.Error("the critique did not run")
	}
}

// The forced submission is the call a loop reaches last, with options built
// before the first turn, so it meets the refusal on its own: a model that took
// every earlier turn with a temperature and refuses only this one.
func TestAForcedSubmissionIsSentAgainWithoutATemperatureTheModelRefuses(t *testing.T) {
	fs := confident(twoFindings(), 0.9)
	reviewer := &deepLLM{
		refuseTemperature: "forced",
		general:           []llms.Response{lookup("app.go"), lookup("app.go")},
		security:          []llms.Response{submit(t, nil)},
		forcedReplies:     []llms.Response{submit(t, fs[:1])},
		grades:            gradeAll(t, "proven"),
	}
	e, _ := deepEngine(t, reviewer, appRepo(t))
	e.Config.Review.Deep.MaxTurns = 2
	report, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if reviewer.refused == 0 || reviewer.forced != 1 {
		t.Fatalf("refused %d, forced submissions %d; the forced call did not meet the refusal", reviewer.refused, reviewer.forced)
	}
	if got := titles(report); len(got) != 1 || len(report.FailedStages()) != 0 {
		t.Errorf("findings %v, failed stages %v; want the forced submission's finding and a complete review", got, report.FailedStages())
	}
}

// The critique is built after the agents finish, so a model that took their
// turns with a temperature and refuses only the critique meets the refusal
// there, and its grades are still applied.
func TestTheCritiqueIsSentAgainWithoutATemperatureTheModelRefuses(t *testing.T) {
	fs := confident(twoFindings(), 0.9)
	reviewer := &deepLLM{
		refuseTemperature: "critique",
		general:           []llms.Response{submit(t, fs[:1])},
		security:          []llms.Response{submit(t, fs[1:])},
		grades:            gradeAll(t, "proven", "unsupported"),
	}
	e, _ := deepEngine(t, reviewer, appRepo(t))
	report, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if reviewer.refused == 0 {
		t.Fatal("the critique never met the refusal")
	}
	if len(report.FailedStages()) != 0 {
		t.Errorf("failed stages = %v; the critique must not fail on a refused temperature", report.FailedStages())
	}
	if got := titles(report); len(got) != 1 {
		t.Errorf("findings = %v, want the critique's grades applied: one kept, one withheld", got)
	}
}

// unparsed is a submit_findings call whose arguments are not JSON.
func unparsed() llms.Response {
	return llms.Response{ToolCalls: []llms.ToolCall{{
		ID: "bad", Type: llms.ToolTypeFunction,
		Function: &llms.FunctionCall{Name: submitFindings, Arguments: `{"findings": [{"path": "app.go", `},
	}}}
}

// An agent whose submission does not parse is told so as the answer to its call
// and sends the findings again, instead of costing the batch the agent.
func TestADeepAgentWhoseFindingsDoNotParseIsAskedToSendThemAgain(t *testing.T) {
	fs := confident(twoFindings(), 0.9)
	reviewer := &deepLLM{
		general:  []llms.Response{unparsed(), submit(t, fs[:1])},
		security: []llms.Response{submit(t, nil)},
		grades:   gradeAll(t, "proven"),
	}
	e, _ := deepEngine(t, reviewer, appRepo(t))
	report, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if got := titles(report); len(got) != 1 || got[0] != fs[0].Title {
		t.Errorf("findings = %v, want the resent submission's %q", got, fs[0].Title)
	}
	if len(report.FailedStages()) != 0 {
		t.Errorf("failed stages = %v; a submission the agent could repair must not fail it", report.FailedStages())
	}
	if len(reviewer.shown) != 1 || !strings.Contains(reviewer.shown[0], "did not parse as JSON") {
		t.Errorf("the agent was shown %q, want the parse failure as the answer to its call", reviewer.shown)
	}
}

// The forced submission treats a call that does not parse like prose: it is
// asked once more at the same step, and its answer is kept.
func TestAForcedSubmissionThatDoesNotParseIsAskedAgain(t *testing.T) {
	fs := confident(twoFindings(), 0.9)
	reviewer := &deepLLM{
		general:       []llms.Response{lookup("app.go")},
		security:      []llms.Response{submit(t, nil)},
		forcedReplies: []llms.Response{unparsed(), submit(t, fs[:1])},
		grades:        gradeAll(t, "proven"),
	}
	e, _ := deepEngine(t, reviewer, appRepo(t))
	e.Config.Review.Deep.MaxTurns = 1
	report, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if reviewer.forced != 2 {
		t.Errorf("forced calls = %d, want the unparsed one and its retry", reviewer.forced)
	}
	if got := titles(report); len(got) != 1 || len(report.FailedStages()) != 0 {
		t.Errorf("findings %v, failed stages %v; want the retry's finding and a complete review", got, report.FailedStages())
	}
}

// An agent that has used its wall-clock budget is asked to submit between turns
// instead of reading on, so a slow provider ends a review and does not hold it.
func TestADeepAgentThatUsedItsTimeBudgetIsAskedToSubmit(t *testing.T) {
	fs := confident(twoFindings(), 0.9)
	reviewer := &deepLLM{
		delay:         5 * time.Millisecond,
		general:       []llms.Response{lookup("app.go"), lookup("app.go"), submit(t, nil)},
		security:      []llms.Response{submit(t, nil)},
		forcedReplies: []llms.Response{submit(t, fs[:1])},
		grades:        gradeAll(t, "proven"),
	}
	e, _ := deepEngine(t, reviewer, appRepo(t))
	e.Config.Review.Deep.Timeout = time.Millisecond
	report, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if reviewer.turns[config.DeepAgentGeneral] != 1 || reviewer.forced != 1 {
		t.Errorf("general turns %d, forced submissions %d; want one turn and then the forced submission",
			reviewer.turns[config.DeepAgentGeneral], reviewer.forced)
	}
	if got := titles(report); len(got) != 1 || got[0] != fs[0].Title {
		t.Errorf("findings = %v, want the forced submission's %q", got, fs[0].Title)
	}
}

// A timeout of zero removes the budget: the agent reads on to its own
// submission however long each turn takes.
func TestAZeroTimeBudgetLetsADeepAgentReadOn(t *testing.T) {
	fs := confident(twoFindings(), 0.9)
	reviewer := &deepLLM{
		delay:    5 * time.Millisecond,
		general:  []llms.Response{lookup("app.go"), lookup("app.go"), submit(t, fs[:1])},
		security: []llms.Response{submit(t, nil)},
		grades:   gradeAll(t, "proven"),
	}
	e, _ := deepEngine(t, reviewer, appRepo(t))
	e.Config.Review.Deep.Timeout = 0
	if _, err := e.Review(context.Background(), vcs.Ref{}); err != nil {
		t.Fatal(err)
	}
	if reviewer.turns[config.DeepAgentGeneral] != 3 || reviewer.forced != 0 {
		t.Errorf("general turns %d, forced submissions %d; want three turns and no forced submission",
			reviewer.turns[config.DeepAgentGeneral], reviewer.forced)
	}
}

// A configured max_tokens bounds the forced submission too, on every step of
// its ladder, and not only the agent's turns.
func TestAConfiguredMaxTokensBoundsEveryStepOfTheForcedSubmission(t *testing.T) {
	fs := confident(twoFindings(), 0.9)
	reviewer := &deepLLM{
		general:  []llms.Response{lookup("app.go")},
		security: []llms.Response{submit(t, nil)},
		forcedReplies: []llms.Response{
			{FinishReason: llms.FinishReasonLength},
			{FinishReason: llms.FinishReasonLength},
			submit(t, fs[:1]),
		},
		grades: gradeAll(t, "proven"),
	}
	e, _ := deepEngine(t, reviewer, appRepo(t))
	e.Config.Review.Deep.MaxTokens = 4096
	e.Config.Review.Deep.MaxTurns = 1
	if _, err := e.Review(context.Background(), vcs.Ref{}); err != nil {
		t.Fatal(err)
	}
	if want := []int{4096, 4096, 4096}; !slices.Equal(reviewer.forcedMaxTokens, want) {
		t.Errorf("forced max tokens per step = %v, want %v", reviewer.forcedMaxTokens, want)
	}
}

// A deep call refused for a reason a missing temperature does not cure fails
// again on the retry, and the client does not learn from that to drop a
// temperature the model takes.
func TestADeepCallRefusedForAnotherParameterDoesNotTeachTheClientToDropTemperature(t *testing.T) {
	reviewer := &deepLLM{refuseTemperature: "always", ordinary: mustJSON(t, Result{})}
	e, _ := deepEngine(t, reviewer, appRepo(t))
	_, _ = e.Review(context.Background(), vcs.Ref{})
	if reviewer.refused < 2 {
		t.Fatalf("refused %d requests, want the call and its retry at least", reviewer.refused)
	}
	if e.Roles.Review.OmitsTemperature() {
		t.Error("the client learned to omit temperature from retries that failed too")
	}
}
