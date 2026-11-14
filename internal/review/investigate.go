package review

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	llms "github.com/nocturnium/llm-go-sdk/v6"

	"github.com/jdziat/open-nitpick/internal/llm"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// With review.investigate the reviewer reads the repository while it decides
// what to file, over several turns, and finishes by submitting its findings. A
// finding that depends on the receiving side of a change, or on a caller, is then
// checked by the reviewer before it is filed and not after the fact by
// review.verify. docs/remediation-mira.md, stage R8, has the measurement.

const (
	// investigateMaxTurns bounds how many times the reviewer is asked before it
	// must submit. A turn may issue several lookups.
	investigateMaxTurns = 10

	// investigateToolBytes bounds everything the lookups of one batch may return
	// in total, so a reviewer that reads freely cannot fill its own context.
	investigateToolBytes = 80_000

	// investigateCallTimeout bounds one reviewer turn. It is longer than a
	// checker's because a turn that submits findings writes them out.
	investigateCallTimeout = 4 * time.Minute

	submitFindings = "submit_findings"
)

// investigateNote is appended to the review prompt when tools are offered. It
// adds the means and the one instruction about using them, and leaves what counts
// as a finding to the prompt it extends.
func investigateNote() string {
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
lost.`, investigateMaxTurns)
}

// investigateTools are the lookups plus the one way to finish.
func investigateTools(canSearch bool, schema json.RawMessage) []llms.Tool {
	return append(lookupTools(canSearch), toolDef(submitFindings,
		"Finish. Submit every finding, or an empty list.", string(schema)))
}

// tryInvestigate reviews one batch with the repository open and reports whether
// it produced a result. A batch it could not review that way, because there is
// no checkout or the first call failed before anything was read, is logged and
// left to the ordinary review, so turning the option on can cost a review its
// lookups but never the review.
func (e *Engine) tryInvestigate(ctx context.Context, client *llm.Client, msgs []llms.Message, classes []string) (Result, bool) {
	tools := newRepoTools(e.RepoRoot, e.Provider, vcs.Ref{})
	if !tools.canSearch() {
		e.log().Warn("review.investigate needs a git checkout to search; reviewing from the diff alone")
		return Result{}, false
	}
	result, err := e.investigateBatch(ctx, client, msgs, classes, tools)
	if err != nil {
		e.log().Warn("investigation failed; reviewing from the diff alone", "error", err)
		return Result{}, false
	}
	return result, true
}

// investigation is what the loop needs to report beside the result.
type investigation struct {
	turns, lookups int
	forced         bool
	bytes          int
}

// investigateBatch runs the reviewer's tool loop for one batch.
//
// Once anything has been read, the batch is never given up: a reviewer that stops
// calling tools, runs out of turns, or whose next call fails is asked to submit
// over the transcript it has, because the lookups were paid for and the findings
// they support are in it.
func (e *Engine) investigateBatch(ctx context.Context, client *llm.Client, msgs []llms.Message, classes []string, tools *repoTools) (Result, error) {
	schema, err := findingsSchema(classes)
	if err != nil {
		return Result{}, err
	}
	msgs = slices.Clone(msgs)
	for i := range msgs {
		if msgs[i].Role == llms.RoleSystem {
			msgs[i].Content += investigateNote()
			break
		}
	}
	// The client's own options, reasoning included: this call decides what to
	// file, which is the work a reasoning model is configured for. Each
	// assistant turn carries its reasoning back, as providers that require it
	// need.
	opts := append(client.CallOptions(),
		llms.WithTools(investigateTools(tools.canSearch(), schema)), llms.WithToolChoiceAuto())

	var run investigation
	finish := func(r Result) (Result, error) {
		e.log().Info("investigated a batch", "turns", run.turns, "lookups", run.lookups,
			"forced", run.forced, "findings", len(r.Findings))
		return r, nil
	}
	for run.turns < investigateMaxTurns {
		run.turns++
		resp, err := generateBounded(ctx, client.LLM, msgs, opts, investigateCallTimeout)
		if err != nil {
			if run.lookups == 0 {
				return Result{}, err
			}
			e.log().Warn("an investigation turn failed; submitting what was read", "error", err)
			break
		}
		if len(resp.ToolCalls) == 0 {
			if strings.TrimSpace(resp.Content) != "" {
				msgs = append(msgs, llms.Message{Role: llms.RoleAssistant, Content: resp.Content, Reasoning: resp.Reasoning})
			}
			break
		}
		msgs = append(msgs, llms.Message{Role: llms.RoleAssistant, Content: resp.Content, ToolCalls: resp.ToolCalls, Reasoning: resp.Reasoning})
		for _, call := range resp.ToolCalls {
			name, args, raw := "", map[string]any{}, ""
			if call.Function != nil {
				name, raw = call.Function.Name, call.Function.Arguments
				_ = json.Unmarshal([]byte(raw), &args)
			}
			if name == submitFindings {
				r, err := parseSubmission(raw)
				if err != nil {
					return Result{}, err
				}
				return finish(r)
			}
			run.lookups++
			result, ok := runLookup(ctx, tools, name, args)
			switch {
			case !ok:
				result = "unknown tool " + name
			case run.bytes >= investigateToolBytes:
				result = "the lookup budget is spent: call " + submitFindings + " now"
			}
			// What the model is shown is what counts against the budget.
			shown := bound(result)
			run.bytes += len(shown)
			msgs = append(msgs, llms.Message{Role: llms.RoleTool, ToolCallID: call.ID, Content: shown})
		}
	}

	if run.lookups == 0 {
		// Nothing was read, so there is no investigation to rescue; an answer
		// in text here is the ordinary review's job.
		return Result{}, errors.New("the reviewer did not read anything or submit findings")
	}
	run.forced = true
	// A reasoning model can spend the whole output budget thinking and return
	// nothing. The ordinary review retries that with reasoning off; the forced
	// submission needs the same way out, or the investigation is lost to it.
	plain := append(client.NoReasoningOptions(),
		llms.WithTools(investigateTools(tools.canSearch(), schema)))
	r, err := e.forceSubmission(ctx, client, msgs, opts, plain, run.turns >= investigateMaxTurns)
	if err != nil {
		return Result{}, err
	}
	return finish(r)
}

// forcedSubmitPrompt asks for the findings over a transcript whose investigation
// is over.
const forcedSubmitPrompt = "Your investigation is over and no further lookups will be answered. " +
	"Using everything in this conversation, the diff and every file and search result you " +
	"fetched, call submit_findings now with all the findings you can defend. Do not drop a " +
	"finding only because you could not read one more file: if the evidence you have supports " +
	"it, file it with a confidence that reflects what is still uncertain."

// forceSubmission makes the model call submit_findings over the transcript. A
// reply in prose gets one more request, and a reply that is empty because
// reasoning used the whole output budget gets one more with reasoning off,
// before the investigation is given up.
func (e *Engine) forceSubmission(ctx context.Context, client *llm.Client, msgs []llms.Message, opts, noReasoning []llms.CallOption, hitCap bool) (Result, error) {
	prompt := forcedSubmitPrompt
	if hitCap {
		prompt = "You have used your turns. " + prompt
	}
	convo := append(slices.Clone(msgs), llms.Message{Role: llms.RoleUser, Content: prompt})
	forced := append(slices.Clone(opts), llms.WithToolChoiceTool(submitFindings))
	var last error
	reasoningOff, nudged := false, false
	for attempt := range 3 {
		resp, err := generateBounded(ctx, client.LLM, convo, forced, investigateCallTimeout)
		if err != nil {
			return Result{}, fmt.Errorf("forced submission: %w", err)
		}
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
