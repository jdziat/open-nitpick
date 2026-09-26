# Full-review comparison: llm-go-sdk (2026-09-16)

Blind head-to-head of four configs against llm-go-sdk at HEAD (40be1de).
All four reviews ran simultaneously from 21:51:01 PDT. 525 Go/Markdown
files, 462 Go files.

## Summary

| config | reviewer | triage | total | error+warn | wall time |
|---|---|---|---|---|---|
| baseline | glm-5.3 (synthetic) | qwen3.8-27b | 55 | 36 | 13 min |
| config-A | deepseek-v4.1-flash | laguna-s-2.1 | 62 | 26 | 12 min |
| config-B | laguna-s-2.1 | luna flex | 42 | 24 | 12 min |
| config-C | luna flex | glm-5.3-flash | 48 | 28 | 22 min |

## Overlap

Only 2 of 207 unique findings appear in more than one config. Pairwise
overlap is near zero across all four pairs. Each config sees a nearly
entirely distinct slice of the codebase.

Shared findings: 'CreateMessageStream mutates the caller MessagesRequest'
(baseline + config-C) and 't.Fatalf called from server handler goroutine'
(config-A + config-B).

This low overlap means no single config captures the full picture. It also
means each config's findings are real -- they are not four versions of the
same list. A production setup benefits from at least two configs in rotation.

## What each config found that the others did not

**Baseline (glm-5.3 + qwen3.8-27b):** SSRF hostname validation allows DNS
rebinding. llamacpp blocks indefinitely on unreachable server. Anthropic
ToolChoiceNone wire error. Reasoning API parameter mismatch. Strongest on
contract violations. 36 high-signal findings, also the most noise.

**Config-A (deepseek + laguna):** Both critical correctness errors: Responses
API reasoning option uses the wrong parameter name; non-string response_format
silently ignored in transcription. Strongest test-quality coverage. 62 total
findings (highest), 26 high-signal.

**Config-B (laguna + luna flex):** Division by zero returns fabricated result.
Sonnet 5 pricing error. Canceled requests dropped from Results silently.
Cleanest output: 42 total, 24 high-signal, least repetition when read
in sequence. Luna triage cut 72 raw findings to 42, showing real triage value.

**Config-C (luna flex + glm triage):** 6 concurrency findings no other config
caught: first-fetch blocking all callers, goroutine sync in test helpers,
channel send after consumer exits. Found responsesResponseError is never
called. Slowest (22 min) because glm triage stalls 30-120s per batch.

## Recommendation

Default config for llm-go-sdk: **config-B** (laguna reviewer, luna flex triage).
Best signal-to-noise, 12 min wall time, luna triage is clean and cheap.

Supplementary: **config-C** (luna reviewer, glm triage) for the concurrency
cluster laguna misses. Acceptable on-demand or weekly rather than every PR.

The baseline (glm+qwen27b) is outperformed on cleanliness by config-B and on
recall by configs A and C. Its triage model (qwen3.8-27b) was not measured
on this corpus; the output reflects that -- findings are sometimes redundant.

## Config-C wall time note

The 22-minute runtime is almost entirely glm triage stalls, not review time.
The luna review leg finished in 8 minutes. Replacing glm triage with laguna
would bring config-C to approximately 12 minutes with some loss on the
concurrency-finding cluster laguna is known to miss.

## Post-merge check (2026-09-17)

After a3d3d56 pinned config-B under `models.default` only, a user-level
`models.review: deepseek/deepseek-v4.1-flash` still won the review role.
That run failed 55 of 88 batches on invalid JSON and left 342 of 526 files
unreviewed. Pinning `models.review` to laguna in `.nitpick.yaml` (and luna
in `.nitpick-concurrency.yaml`) closed the hole.

Rerun with laguna: 2 failed batches. Both published error findings were
false positives:

- `internal/geminiapi/client.go:415` — `WrapError(operation, err)` takes the
  formatted string as the operation name and wraps `llms.ErrInvalidParameters`;
  `errors.Is` matches.
- `pkg/providers/runpod/models.go:179` — `return &m` under Go 1.25, where the
  range variable is per-iteration, so each pointer is distinct.

The two failed batches (12 files) returned
`response was not valid JSON after one repair attempt: model returned empty
content`. Laguna lists `include_reasoning` on OpenRouter; under an inherited
`max_tokens: 8192` the model can spend the whole budget on thinking and answer
with `finish_reason=length` and empty content. open-nitpick now retries that
shape with reasoning off and a raised output floor; the repo config pins
`reasoning: off` and `max_tokens: 16384` on the review role so the overlay
cannot reintroduce the tight cap.
