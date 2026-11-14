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
// what to file. Reviewing from the diff and the bundled context alone leaves a
// finding that depends on the consuming side of a change, or on a caller, to be
// checked after the fact by review.verify; here the reviewer can look first and
// file only what it found. docs/remediation-mira.md, stage R5, has the
// measurement and says whether it earned its cost.

const (
	// investigateMaxLookups bounds how much of the repository one batch may
	// read. It is larger than the checker's budget because a reviewer looks for
	// several things, not one.
	investigateMaxLookups = 12

	// investigateCallTimeout bounds one reviewer turn. It is longer than a
	// checker's because a turn that submits findings writes them out.
	investigateCallTimeout = 4 * time.Minute

	submitFindings = "submit_findings"
)

// investigateNote is appended to the review prompt when tools are offered. It
// adds the means and one instruction about how to use them, and leaves what
// counts as a finding to the prompt it extends.
const investigateNote = `

## Looking things up

You can read files and search the repository before you file anything. Use that
to settle what the diff cannot: the definition of a function it calls, the
callers of one it changes, the code that consumes a value it produces. File a
finding only when the code you read supports it; if a lookup shows the concern is
handled elsewhere, drop it. Finish by calling submit_findings once with every
finding, or with an empty list when there is none. Do not answer in plain text.`

// investigateTools are the lookups plus the one way to finish.
func investigateTools(canSearch bool, schema json.RawMessage) []llms.Tool {
	return append(lookupTools(canSearch), toolDef(submitFindings,
		"Finish. Submit every finding, or an empty list.", string(schema)))
}

// tryInvestigate reviews one batch with the repository open and reports whether
// it produced a result. A batch it could not review that way, because there is
// no checkout or a call failed, is logged and left to the ordinary review, so
// turning the option on can cost a review its lookups but never the review.
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

// investigateBatch runs the reviewer's tool loop for one batch.
func (e *Engine) investigateBatch(ctx context.Context, client *llm.Client, msgs []llms.Message, classes []string, tools *repoTools) (Result, error) {
	schema, err := findingsSchema(classes)
	if err != nil {
		return Result{}, err
	}
	msgs = slices.Clone(msgs)
	for i := range msgs {
		if msgs[i].Role == llms.RoleSystem {
			msgs[i].Content += investigateNote
			break
		}
	}
	opts := append(client.NoReasoningOptions(),
		llms.WithTools(investigateTools(tools.canSearch(), schema)), llms.WithToolChoiceAuto())

	lookups := 0
	for range investigateMaxLookups + 4 {
		resp, err := generateBounded(ctx, client.LLM, msgs, opts, investigateCallTimeout)
		if err != nil {
			return Result{}, err
		}
		if len(resp.ToolCalls) == 0 {
			// A model that answered in text anyway may still have answered
			// correctly; one that did not has nothing to publish.
			var r Result
			if json.Unmarshal([]byte(strings.TrimSpace(resp.Content)), &r) != nil {
				return Result{}, errors.New("the reviewer ended without submitting findings")
			}
			return r, nil
		}
		msgs = append(msgs, llms.Message{Role: llms.RoleAssistant, Content: resp.Content, ToolCalls: resp.ToolCalls})
		for _, call := range resp.ToolCalls {
			name, args, raw := "", map[string]any{}, ""
			if call.Function != nil {
				name, raw = call.Function.Name, call.Function.Arguments
				_ = json.Unmarshal([]byte(raw), &args)
			}
			if name == submitFindings {
				var r Result
				if err := json.Unmarshal([]byte(raw), &r); err != nil {
					return Result{}, fmt.Errorf("the submitted findings did not parse: %w", err)
				}
				return r, nil
			}
			result, ok := runLookup(ctx, tools, name, args, &lookups, investigateMaxLookups, submitFindings)
			if !ok {
				result = "unknown tool " + name
			}
			msgs = append(msgs, llms.Message{Role: llms.RoleTool, ToolCallID: call.ID, Content: bound(result)})
		}
	}
	return Result{}, errors.New("the reviewer never submitted its findings")
}
