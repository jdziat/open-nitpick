package review

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	llms "github.com/nocturnium/llm-go-sdk/v6"

	"github.com/nitpick-oss/nitpick/internal/config"
	"github.com/nitpick-oss/nitpick/internal/llm"
	"github.com/nitpick-oss/nitpick/internal/prompt"
	"github.com/nitpick-oss/nitpick/internal/vcs"
)

// nitpick deep-review reads each batch with several agents at once. Each agent
// reads files and searches the repository while it decides what to file, then
// submits its findings, so a claim that depends on code outside the diff is
// checked before it is filed. One call over a whole large pull request does not
// work with a reasoning model: on the R12 smoke, DeepSeek V4.1 Flash thought past
// 131,072 tokens on 115,000-token prompts and answered nothing. Spread over loop
// turns, each call stays short enough for the reasoning to finish.

const (
	// deepToolBytes bounds what the lookups of one agent may return in total,
	// so an agent that reads freely cannot fill its own context.
	deepToolBytes = 80_000

	// deepCallTimeout bounds one agent turn. A turn that submits findings
	// writes them all out, after a reasoning model has thought.
	deepCallTimeout = 4 * time.Minute

	submitFindings = "submit_findings"

	// deepForcedMaxTokens and deepForcedTimeout give the forced submission room
	// to think over the whole transcript. At the 16,384-token cap the client
	// otherwise sends, D1's forced submissions ran out 16% to 34% of the time;
	// replayed, the same request finished between 16k and 65k.
	deepForcedMaxTokens = 65536
	deepForcedTimeout   = 10 * time.Minute
)

// deepAgent is one reviewer in a deep review: a name for the logs and the
// system prompt it reviews under.
type deepAgent struct {
	name   string
	system string
}

// deepToolNote is appended to every agent's system prompt. It adds the means
// and the one instruction about using them, and leaves what counts as a finding
// to the prompt it extends.
func deepToolNote(maxTurns int) string {
	return fmt.Sprintf(`

## Looking things up

You can read files and search the repository before you file anything:
read_file(path, start_line, end_line) and grep_repo(pattern, path_glob). Use them
to check a claim that depends on code outside the diff before you file it: whether
the function it calls behaves that way, whether every caller and implementer
followed the change, whether the code that receives a new value handles it. Fetch
what you need rather than browsing, and skip them when the diff settles the
question. You have at most %d turns; when they run out you will be asked to file
what you have. Finish by calling submit_findings once with every finding you can
defend, or with an empty list when there is none. A finding written in prose is
lost.`, maxTurns)
}

// deepAgents builds the configured agents over the review's system prompt.
// Under the default prompts the security agent extends that prompt instead of
// replacing it, so both agents file findings in the same shape. Under
// review.deep.prompts: mira each agent has its own prompt, built over the same
// layers.
func (e *Engine) deepAgents(client *llm.Client, system string) ([]deepAgent, error) {
	deep := e.Config.Review.Deep
	agents := make([]deepAgent, 0, len(deep.Agents))
	for _, name := range deep.Agents {
		text, err := e.deepAgentPrompt(client, name, system)
		if err != nil {
			return nil, err
		}
		agents = append(agents, deepAgent{name: name, system: text + deepToolNote(deep.MaxTurns)})
	}
	return agents, nil
}

// deepAgentPrompt is the system prompt one agent reviews under, before the
// tool note is appended.
func (e *Engine) deepAgentPrompt(client *llm.Client, agent, system string) (string, error) {
	security := agent == config.DeepAgentSecurity
	if e.Config.Review.Deep.Prompts == config.DeepPromptsMira {
		base, err := prompt.DeepMiraGeneral()
		if security {
			base, err = prompt.DeepMiraSecurity()
		}
		if err != nil {
			return "", err
		}
		return e.reviewPromptBase(client, base)
	}
	if !security {
		return system, nil
	}
	focus, err := prompt.DeepSecurity()
	if err != nil {
		return "", err
	}
	return strings.TrimRight(system, "\n") + "\n\n" + focus, nil
}

// deepOutcome says how a batch's deep review went, beyond its findings.
type deepOutcome struct {
	// reviewed is true when at least one agent produced a result. When it is
	// false the batch has no result yet and the ordinary single call runs.
	reviewed bool

	// degraded is true when the batch's result is not the whole deep review:
	// an agent failed, or no agent ran. A degraded result is reported and is
	// not saved for a resumed run, which would otherwise serve it back without
	// the agent that failed.
	degraded bool
}

// deepNotes collects what went wrong in one deep review, so the report names it.
// Silence from a security agent that never answered is not silence from one
// that found nothing, and the two must not read alike.
type deepNotes struct {
	mu       sync.Mutex
	failures map[string]string
	fellBack map[string]bool
}

func newDeepNotes() *deepNotes {
	return &deepNotes{failures: map[string]string{}, fellBack: map[string]bool{}}
}

// agentFailed records that an agent did not finish a batch. The first reason
// seen for an agent is the one reported.
func (n *deepNotes) agentFailed(agent string, err error) {
	if n == nil {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if _, ok := n.failures[agent]; !ok {
		n.failures[agent] = errorKind(err)
	}
}

// ranWithoutTools records that a batch was reviewed in one call without tools.
func (n *deepNotes) ranWithoutTools(why string) {
	if n == nil {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	n.fellBack[why] = true
}

// stages turns the notes into stage statuses, in a fixed order so a report does
// not change between runs of the same review.
func (n *deepNotes) stages() []StageStatus {
	if n == nil {
		return nil
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	var out []StageStatus
	for _, agent := range slices.Sorted(maps.Keys(n.failures)) {
		out = append(out, StageStatus{Stage: "deep-review " + agent + " agent", Reason: n.failures[agent]})
	}
	for _, why := range slices.Sorted(maps.Keys(n.fellBack)) {
		out = append(out, StageStatus{Stage: "deep-review", Reason: why})
	}
	return out
}

// deepReviewBatch reviews one batch with every configured agent in parallel.
// One agent failing does not cost the batch the others' findings. A batch no
// agent could review, for want of a checkout to search or because every agent
// failed, is left to the ordinary single call. Either way the report says so.
func (e *Engine) deepReviewBatch(ctx context.Context, client *llm.Client, msgs []llms.Message, classes []string) (Result, deepOutcome) {
	tools := e.repositoryTools(vcs.Ref{})
	if !tools.canSearch() {
		e.log().Warn("deep-review needs a git checkout to search; reviewing from the diff alone")
		e.deepNotes.ranWithoutTools("no git checkout to search")
		return Result{}, deepOutcome{degraded: true}
	}
	at := slices.IndexFunc(msgs, func(m llms.Message) bool { return m.Role == llms.RoleSystem })
	if at < 0 {
		e.log().Warn("deep-review found no system prompt to extend; reviewing from the diff alone")
		e.deepNotes.ranWithoutTools("no system prompt")
		return Result{}, deepOutcome{degraded: true}
	}
	agents, err := e.deepAgents(client, msgs[at].Content)
	if err != nil {
		e.log().Warn("deep-review agents could not be built; reviewing from the diff alone", "error", err)
		e.deepNotes.ranWithoutTools("agents could not be built")
		return Result{}, deepOutcome{degraded: true}
	}

	results := make([]Result, len(agents))
	errs := make([]error, len(agents))
	var wg sync.WaitGroup
	for i, agent := range agents {
		wg.Add(1)
		go func() {
			defer wg.Done()
			convo := slices.Clone(msgs)
			convo[at].Content = agent.system
			results[i], errs[i] = e.runDeepAgent(ctx, client, agent.name, convo, classes, tools)
		}()
	}
	wg.Wait()

	var merged Result
	finished := 0
	for i, agent := range agents {
		if errs[i] != nil {
			e.log().Warn("a deep-review agent failed; keeping the other agents' findings", "agent", agent.name, "error", errs[i])
			e.deepNotes.agentFailed(agent.name, errs[i])
			continue
		}
		finished++
		merged.Findings = append(merged.Findings, results[i].Findings...)
	}
	if finished == 0 {
		e.log().Warn("every deep-review agent failed; reviewing the batch from the diff alone", "agents", len(agents))
		e.deepNotes.ranWithoutTools("every agent failed")
		return Result{}, deepOutcome{degraded: true}
	}
	return merged, deepOutcome{reviewed: true, degraded: finished < len(agents)}
}

// agentRun is what one agent's loop reports beside its findings.
type agentRun struct {
	turns, lookups, bytes int
	forced                bool
	reasoningLow          int
	reasoningOff          int
	usage                 llms.Usage
}

func (r *agentRun) add(u llms.Usage) {
	r.usage.PromptTokens += u.PromptTokens
	r.usage.CompletionTokens += u.CompletionTokens
	r.usage.ReasoningTokens += u.ReasoningTokens
}

// runDeepAgent runs one agent's tool loop over a batch.
//
// An agent that stops calling tools, runs out of turns, or whose later turn
// fails is asked to submit over the transcript it has, because what it read is
// in that transcript. Only a first turn that fails outright fails the agent.
func (e *Engine) runDeepAgent(ctx context.Context, client *llm.Client, name string, msgs []llms.Message, classes []string, tools *repoTools) (Result, error) {
	ctx = llm.WithCallRole(ctx, "deep_agent")
	schema, err := findingsSchema(classes)
	if err != nil {
		return Result{}, err
	}
	deep := e.Config.Review.Deep
	temperature := deep.Temperature
	offered := llms.WithTools(deepTools(tools.canSearch(), schema))
	opts := client.CallOptionsWith(&temperature, deep.Reasoning)
	if deep.MaxTokens > 0 {
		opts = append(opts, llms.WithMaxTokens(deep.MaxTokens))
	}
	opts = append(opts, offered, llms.WithToolChoiceAuto())
	// The forced submission's ladder: the configured reasoning with room to
	// finish, then low effort, then none. Each step down is taken only when the
	// one before ran out of output budget.
	submit := llms.WithToolChoiceTool(submitFindings)
	forcedCap := deepForcedMaxTokens
	if deep.MaxTokens > 0 {
		forcedCap = deep.MaxTokens
	}
	room := llms.WithMaxTokens(forcedCap)
	noThinking := append(client.CallOptionsWith(&temperature, config.ReasoningOff), offered, submit)
	if deep.MaxTokens > 0 {
		noThinking = append(noThinking, room)
	}
	ladder := []forcedRung{
		{opts: append(slices.Clone(opts), submit, room)},
		{opts: append(client.CallOptionsWith(&temperature, config.ReasoningLow), offered, submit, room), low: true},
		{opts: noThinking, off: true},
	}

	var run agentRun
	finish := func(r Result) (Result, error) {
		e.log().Info("deep-review agent finished", "agent", name, "turns", run.turns, "lookups", run.lookups,
			"forced", run.forced, "reasoning_low", run.reasoningLow, "reasoning_off", run.reasoningOff, "findings", len(r.Findings),
			"prompt_tokens", run.usage.PromptTokens, "completion_tokens", run.usage.CompletionTokens,
			"reasoning_tokens", run.usage.ReasoningTokens)
		return r, nil
	}
	started := time.Now()
	for run.turns < deep.MaxTurns {
		if run.turns > 0 && deep.Timeout > 0 && time.Since(started) >= deep.Timeout {
			e.log().Warn("a deep-review agent used its time budget; asking it to submit what it has",
				"agent", name, "budget", deep.Timeout, "turns", run.turns)
			break
		}
		run.turns++
		resp, err := e.generateDeep(ctx, client, msgs, opts, deepCallTimeout)
		if err != nil {
			if run.turns == 1 {
				return Result{}, err
			}
			e.log().Warn("a deep-review turn failed; asking the agent to submit what it has", "agent", name, "error", err)
			break
		}
		run.add(resp.Usage)
		if len(resp.ToolCalls) == 0 {
			if strings.TrimSpace(resp.Content) != "" {
				msgs = append(msgs, llms.Message{Role: llms.RoleAssistant, Content: resp.Content, Reasoning: resp.Reasoning})
			}
			break
		}
		msgs = append(msgs, llms.Message{Role: llms.RoleAssistant, Content: resp.Content, ToolCalls: resp.ToolCalls, Reasoning: resp.Reasoning})
		answers := lookupTurn(ctx, tools, resp.ToolCalls, len(resp.ToolCalls))
		for i, call := range resp.ToolCalls {
			fn, args, raw := "", map[string]any{}, ""
			if call.Function != nil {
				fn, raw = call.Function.Name, call.Function.Arguments
				_ = json.Unmarshal([]byte(raw), &args)
			}
			if fn == submitFindings {
				r, err := parseSubmission(raw)
				if err == nil {
					return finish(r)
				}
				// A tool call needs an answer or the next request is invalid, and
				// the answer that lets the agent repair its submission is the
				// error. Failing here would cost the batch the whole agent over
				// one malformed call.
				e.log().Warn("an agent's findings did not parse; asking it to send them again", "agent", name, "error", err)
				msgs = append(msgs, llms.Message{Role: llms.RoleTool, ToolCallID: call.ID,
					Content: "The findings did not parse as JSON (" + err.Error() + "). Call " + submitFindings + " again with valid JSON."})
				continue
			}
			run.lookups++
			var result string
			ok := i < len(answers)
			if ok {
				result = answers[i]
			} else {
				result, ok = runLookup(ctx, tools, fn, args)
			}
			switch {
			case !ok:
				result = "unknown tool " + fn
			case run.bytes >= deepToolBytes:
				result = "the lookup budget is spent: call " + submitFindings + " now"
			}
			// What the model is shown is what counts against the budget.
			shown := bound(result)
			run.bytes += len(shown)
			msgs = append(msgs, llms.Message{Role: llms.RoleTool, ToolCallID: call.ID, Content: shown})
		}
	}

	run.forced = true
	r, err := e.forceSubmission(ctx, client, msgs, ladder, run.turns >= deep.MaxTurns, &run)
	if err != nil {
		return Result{}, err
	}
	return finish(r)
}

// generateDeep is generateBounded for a deep-review call. It adds the one thing
// the client's own retry layer does and a bare SDK call does not: when the model
// takes no temperature, the call is sent again without it. The client remembers
// the refusal, so later calls leave the temperature out from the start.
func (e *Engine) generateDeep(ctx context.Context, client *llm.Client, msgs []llms.Message, opts []llms.CallOption, timeout time.Duration) (*llms.Response, error) {
	if client.OmitsTemperature() {
		opts = llm.WithoutTemperature(opts)
	}
	resp, err := generateBounded(ctx, client.LLM, msgs, opts, timeout)
	if client.RejectsTemperature(err, opts) {
		e.log().Warn("the model accepts no temperature; sending again without it", "model", client.String())
		resp, err = generateBounded(ctx, client.LLM, msgs, llm.WithoutTemperature(opts), timeout)
		if err == nil {
			client.NoteNoTemperature()
		}
	}
	return resp, err
}

// deepTools are the lookups plus the one way to finish.
func deepTools(canSearch bool, schema json.RawMessage) []llms.Tool {
	return append(lookupTools(canSearch), toolDef(submitFindings,
		"Finish. Submit every finding, or an empty list.", string(schema)))
}

// forcedSubmitPrompt asks for the findings over a transcript whose
// investigation is over.
const forcedSubmitPrompt = "Your investigation is over and no further lookups will be answered. " +
	"Using everything in this conversation, the diff and every file and search result you " +
	"fetched, call submit_findings now with all the findings you can defend. Do not drop a " +
	"finding only because you could not read one more file: if the evidence you have supports " +
	"it, file it with a confidence that reflects what is still uncertain."

// forcedRung is one step of the forced submission's ladder.
type forcedRung struct {
	opts     []llms.CallOption
	low, off bool
}

// forceSubmission makes the model call submit_findings over the transcript. A
// reply cut at the output cap, empty or mid-answer, steps down the ladder; a
// reply in prose gets one more request at the same step. The agent is given up
// only when the ladder runs out or the call itself fails.
func (e *Engine) forceSubmission(ctx context.Context, client *llm.Client, msgs []llms.Message, ladder []forcedRung, outOfTurns bool, run *agentRun) (Result, error) {
	ctx = llm.WithCallRole(ctx, "deep_forced_submission")
	text := forcedSubmitPrompt
	if outOfTurns {
		text = "You have used your turns. " + text
	}
	convo := append(slices.Clone(msgs), llms.Message{Role: llms.RoleUser, Content: text})
	last := errors.New("the forced submission never made a tool call")
	nudged := false
	for step := 0; step < len(ladder); {
		rung := ladder[step]
		resp, err := e.generateDeep(ctx, client, convo, rung.opts, deepForcedTimeout)
		if err != nil {
			return Result{}, fmt.Errorf("forced submission: %w", err)
		}
		run.add(resp.Usage)
		var unparsed error
		for _, call := range resp.ToolCalls {
			if call.Function != nil && call.Function.Name == submitFindings && resp.FinishReason != llms.FinishReasonLength {
				r, err := parseSubmission(call.Function.Arguments)
				if err == nil {
					return r, nil
				}
				unparsed = err
				break
			}
		}
		e.log().Warn("a forced submission made no usable tool call",
			"step", step+1, "finish", resp.FinishReason, "content_bytes", len(resp.Content), "unparsed", unparsed != nil)
		switch {
		case resp.FinishReason == llms.FinishReasonLength:
			last = errors.New("the forced submission ran out of output budget")
			step++
			if step < len(ladder) {
				if ladder[step].low {
					run.reasoningLow++
				}
				if ladder[step].off {
					run.reasoningOff++
				}
			}
		case !nudged:
			nudged = true
			last = errors.New("the forced submission came back as prose")
			ask := "That was prose, not a tool call. Call " + submitFindings + " now with the findings you have."
			said := resp.Content
			if unparsed != nil {
				last = unparsed
				ask = "The findings did not parse as JSON (" + unparsed.Error() + "). Call " + submitFindings + " now with valid JSON."
				if strings.TrimSpace(said) == "" {
					said = "I called " + submitFindings + "."
				}
			}
			convo = append(convo,
				llms.Message{Role: llms.RoleAssistant, Content: said, Reasoning: resp.Reasoning},
				llms.Message{Role: llms.RoleUser, Content: ask})
		default:
			if unparsed != nil {
				return Result{}, unparsed
			}
			return Result{}, errors.New("the forced submission came back as prose")
		}
	}
	return Result{}, last
}

// parseSubmission reads the arguments of a submit_findings call.
func parseSubmission(raw string) (Result, error) {
	var r Result
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		return Result{}, fmt.Errorf("the submitted findings did not parse: %w", err)
	}
	return r, nil
}
