package review

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	llms "github.com/nocturnium/llm-go-sdk/v6"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/llm"
	"github.com/jdziat/open-nitpick/internal/prompt"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// nitpick deep-review reads each batch with several agents at once. Each agent
// reads files and searches the repository while it decides what to file, then
// submits its findings, so a claim that depends on code outside the diff is
// checked before it is filed. One call over a whole large pull request does not
// work with a reasoning model: on the R12 smoke, DeepSeek V4.1 Flash thought past
// 131,072 tokens on 115,000-token prompts and answered nothing. Spread over loop
// turns, each call stays short enough for the reasoning to finish.
// docs/remediation-mira.md, "Deep review", has the design.

const (
	// deepToolBytes bounds what the lookups of one agent may return in total,
	// so an agent that reads freely cannot fill its own context.
	deepToolBytes = 80_000

	// deepCallTimeout bounds one agent turn. A turn that submits findings
	// writes them all out, after a reasoning model has thought.
	deepCallTimeout = 4 * time.Minute

	submitFindings = "submit_findings"
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
// The security agent extends that prompt instead of replacing it, so both
// agents file findings in the same shape.
func (e *Engine) deepAgents(system string) ([]deepAgent, error) {
	deep := e.Config.Review.Deep
	agents := make([]deepAgent, 0, len(deep.Agents))
	for _, name := range deep.Agents {
		text := system
		if name == config.DeepAgentSecurity {
			focus, err := prompt.DeepSecurity()
			if err != nil {
				return nil, err
			}
			text = strings.TrimRight(system, "\n") + "\n\n" + focus
		}
		agents = append(agents, deepAgent{name: name, system: text + deepToolNote(deep.MaxTurns)})
	}
	return agents, nil
}

// deepReviewBatch reviews one batch with every configured agent in parallel
// and reports whether any of them produced a result. One agent failing does
// not cost the batch the others' findings. A batch no agent could review, for
// want of a checkout to search or because every agent failed, is left to the
// ordinary single call, and the log says which.
func (e *Engine) deepReviewBatch(ctx context.Context, client *llm.Client, msgs []llms.Message, classes []string) (Result, bool) {
	if !newRepoTools(e.RepoRoot, e.Provider, vcs.Ref{}).canSearch() {
		e.log().Warn("deep-review needs a git checkout to search; reviewing from the diff alone")
		return Result{}, false
	}
	at := slices.IndexFunc(msgs, func(m llms.Message) bool { return m.Role == llms.RoleSystem })
	if at < 0 {
		e.log().Warn("deep-review found no system prompt to extend; reviewing from the diff alone")
		return Result{}, false
	}
	agents, err := e.deepAgents(msgs[at].Content)
	if err != nil {
		e.log().Warn("deep-review agents could not be built; reviewing from the diff alone", "error", err)
		return Result{}, false
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
			tools := newRepoTools(e.RepoRoot, e.Provider, vcs.Ref{})
			results[i], errs[i] = e.runDeepAgent(ctx, client, agent.name, convo, classes, tools)
		}()
	}
	wg.Wait()

	var merged Result
	finished := 0
	for i, agent := range agents {
		if errs[i] != nil {
			e.log().Warn("a deep-review agent failed; keeping the other agents' findings", "agent", agent.name, "error", errs[i])
			continue
		}
		finished++
		merged.Findings = append(merged.Findings, results[i].Findings...)
	}
	if finished == 0 {
		e.log().Warn("every deep-review agent failed; reviewing the batch from the diff alone", "agents", len(agents))
		return Result{}, false
	}
	return merged, true
}

// agentRun is what one agent's loop reports beside its findings.
type agentRun struct {
	turns, lookups, bytes int
	forced                bool
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
	plain := append(client.CallOptionsWith(&temperature, config.ReasoningOff), offered)

	var run agentRun
	finish := func(r Result) (Result, error) {
		e.log().Info("deep-review agent finished", "agent", name, "turns", run.turns, "lookups", run.lookups,
			"forced", run.forced, "reasoning_off", run.reasoningOff, "findings", len(r.Findings),
			"prompt_tokens", run.usage.PromptTokens, "completion_tokens", run.usage.CompletionTokens,
			"reasoning_tokens", run.usage.ReasoningTokens)
		return r, nil
	}
	for run.turns < deep.MaxTurns {
		run.turns++
		resp, err := generateBounded(ctx, client.LLM, msgs, opts, deepCallTimeout)
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
		for _, call := range resp.ToolCalls {
			fn, args, raw := "", map[string]any{}, ""
			if call.Function != nil {
				fn, raw = call.Function.Name, call.Function.Arguments
				_ = json.Unmarshal([]byte(raw), &args)
			}
			if fn == submitFindings {
				r, err := parseSubmission(raw)
				if err != nil {
					return Result{}, err
				}
				return finish(r)
			}
			run.lookups++
			result, ok := runLookup(ctx, tools, fn, args)
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
	r, err := e.forceSubmission(ctx, client, msgs, opts, plain, run.turns >= deep.MaxTurns, &run)
	if err != nil {
		return Result{}, err
	}
	return finish(r)
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

// forceSubmission makes the model call submit_findings over the transcript. A
// reply in prose gets one more request, and a reply that is empty because
// reasoning used the whole output budget gets one more with reasoning off,
// before the agent is given up.
func (e *Engine) forceSubmission(ctx context.Context, client *llm.Client, msgs []llms.Message, opts, noReasoning []llms.CallOption, outOfTurns bool, run *agentRun) (Result, error) {
	text := forcedSubmitPrompt
	if outOfTurns {
		text = "You have used your turns. " + text
	}
	convo := append(slices.Clone(msgs), llms.Message{Role: llms.RoleUser, Content: text})
	forced := append(slices.Clone(opts), llms.WithToolChoiceTool(submitFindings))
	var last error
	reasoningOff, nudged := false, false
	for attempt := range 3 {
		resp, err := generateBounded(ctx, client.LLM, convo, forced, deepCallTimeout)
		if err != nil {
			return Result{}, fmt.Errorf("forced submission: %w", err)
		}
		run.add(resp.Usage)
		for _, call := range resp.ToolCalls {
			if call.Function != nil && call.Function.Name == submitFindings {
				return parseSubmission(call.Function.Arguments)
			}
		}
		last = errors.New("the forced submission came back as prose")
		e.log().Warn("a forced submission made no tool call",
			"attempt", attempt+1, "finish", resp.FinishReason, "content_bytes", len(resp.Content))
		switch {
		case emptyAtOutputCap(resp) && !reasoningOff:
			// Thinking used the budget. Ask again without it, once.
			reasoningOff = true
			run.reasoningOff++
			forced = append(slices.Clone(noReasoning), llms.WithToolChoiceTool(submitFindings))
		case !emptyAtOutputCap(resp) && !nudged:
			nudged = true
			convo = append(convo,
				llms.Message{Role: llms.RoleAssistant, Content: resp.Content, Reasoning: resp.Reasoning},
				llms.Message{Role: llms.RoleUser, Content: "That was prose, not a tool call. Call " + submitFindings + " now with the findings you have."})
		default:
			return Result{}, last
		}
	}
	return Result{}, last
}

// emptyAtOutputCap reports a reply cut at the output limit with nothing in it.
func emptyAtOutputCap(resp *llms.Response) bool {
	return resp.FinishReason == llms.FinishReasonLength && strings.TrimSpace(resp.Content) == ""
}

// parseSubmission reads the arguments of a submit_findings call.
func parseSubmission(raw string) (Result, error) {
	var r Result
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		return Result{}, fmt.Errorf("the submitted findings did not parse: %w", err)
	}
	return r, nil
}
