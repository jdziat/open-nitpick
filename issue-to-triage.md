# Triage notes — open-nitpick findings on llm-go-sdk PR #96

Source: `nocturnium/llm-go-sdk#96` — "Zero hardcoded per-model token limits and
share the stream watchdog".
Branch `task/pickup-the-medium-and-minor-the-2baa71`, base `main`.
Reviewer: open-nitpick GitHub Action (`github-actions[bot]`).
Head triaged here: `6b8696c467ae1d11ba8aec39e4de213b75d61386` (last of four review
rounds: `d89e086`, `cea11f2`, `6b8696c`).

The change under review, in one line each:
- Six openaicompat providers (`openai`, `zai`, `cerebras`, `deepseek`, `mistral`,
  `perplexity`) drop their static `MaxContextTokens`/`MaxOutputTokens` literals to
  `0`, so the per-model capability registry supplies the values.
- `capabilities_registry.go` gains eight `zai:*` per-model entries.
- `streaming.go` gains `StartStreamWatchdog` + `StreamPanicError`; `anthropic.go`
  and `gemini.go` use them instead of their inline copies.

---

## Verdict table

| # | Head | Path:line | Severity it filed | Verdict |
|---|------|-----------|-------------------|---------|
| 1 | 6b8696c | `pkg/providers/cerebras/cerebras.go:21` | info (earlier: error) | **FALSE POSITIVE** — no-op |
| 2 | 6b8696c | `pkg/providers/deepseek/deepseek.go:21` | info (earlier: error) | **FALSE POSITIVE** — no-op |
| 3 | 6b8696c | `pkg/providers/mistral/mistral.go:28` | info (earlier: error) | **PARTLY VALID** — silent 131072→128000 |
| 4 | 6b8696c | `capabilities_registry.go:493` | warning | **FALSE POSITIVE** — source is verifiable |
| 5 | 6b8696c | `capabilities_registry.go:500` | info | **VALID (minor)** — reasoning flag set twice |
| 6 | 6b8696c | `streaming.go:652/653` | warning | **VALID (real race)** — double `Close` still possible |
| 7 | 6b8696c | `streaming.go:657` | error (earlier head) | **FIXED** — `stopOnce` already at head |
| 8 | 6b8696c | `streaming.go:663` | info | Not a defect (reviewer itself says so) |
| 9 | earlier | `pkg/providers/zai/*` | warning | **FALSE POSITIVE** — see "anchor" note |

---

## The dominant false-positive: "zero token limits"

Appears as an **error** in the first three rounds, downgraded to **info** in the
last round (`Zeroed static limits are re-filled by the registry default`,
`fp:01ef0b4775f2d78b`). Three instances, one cause:

- `pkg/providers/cerebras/cerebras.go:21` — `fp:96f1bbce7ea7ff48`
- `pkg/providers/deepseek/deepseek.go:21` — `fp:6d72c1c5417da032`
- `pkg/providers/mistral/mistral.go:28` — `fp:8edab2389542b386`

### Why it is wrong

The claim is that `openaicompat.BaseProvider.Capabilities()` "returns the static
Capabilities struct verbatim", so zeroing the literals makes callers see
`MaxContextTokens == 0`. It does not. `pkg/openaicompat/provider.go:279`:

```go
func (p *BaseProvider) Capabilities() llms.Capabilities {
	static := p.config.Capabilities
	caps := llms.GetModelCapabilities(p.config.Provider, p.model).ToCapabilities()
	// ... boolean OR-merge ...
	if static.MaxContextTokens != 0 {
		caps.MaxContextTokens = static.MaxContextTokens
	}
	if static.MaxOutputTokens != 0 {
		caps.MaxOutputTokens = static.MaxOutputTokens
	}
	// ...
}
```

The registry is the base, and a **non-zero** static field wins. So a static `0`
is exactly the "let the registry fill this" signal — the documented merge
contract, and how the other ~11 openaicompat providers already behave (they carry
the same "model-dependent, use the registry" comment). Zeroing is a **no-op**
where the registry default equals the old literal: Cerebras 128000/8192 and
DeepSeek 128000/8192 both match their old literals exactly.

### Root cause of the false positive

The reviewer reasoned about `Capabilities()` from the diff alone. The merge
function is in `pkg/openaicompat/provider.go`, which is **not in the PR diff**, so
the model filled the gap with the pessimistic assumption ("returns the struct
verbatim"). The first-round findings even say so: *"pending unseen BaseProvider
fallback"*, *"the diff for openaicompat is not shown, so this rests on the PR
description's claim."*

### What would have fixed it

Follow the call. `Capabilities()` is reached from `BaseProvider`, and the
implementation is one grep away in the same PR checkout. A reviewer that opens
the callee (or that treats "diff does not contain the callee" as `needs-context`
rather than `error`) does not file this. Suggest: before asserting a callee's
behaviour, either read the callee in-repo or downgrade the finding to a question.

---

## The verifiability false positive: Z.AI limits

Filed twice: *"Z.AI per-model limits are asserted without a verifiable source"*
(`capabilities_registry.go:493`, `fp:3d4688f861d44d06`, `fp:0773bd64c4d6da40`).
The reviewer says the values cannot be checked because `pkg/providers/zai/models.go`
is not in the diff.

### Why it is wrong

`pkg/providers/zai/models.go` **is** in the repo and the values match it exactly:

| model | registry (`capabilities_registry.go`) | `models.go` |
|-------|----------------------------------------|-------------|
| glm-5.2 | 1000000 / 128000 | `ContextLength: 1000000`, `MaxOutput: 128000` |
| glm-5.1 / 5 / 5-turbo / 4.7 / 4.6 | 200000 / 128000 | `ContextLength: 200000`, `MaxOutput: 128000` |
| glm-4.5 / glm-4.5-air | 128000 / 96000 | `ContextLength: 128000`, `MaxOutput: 96000` |

No value was invented. Same root cause as above: the file that would verify the
claim is outside the diff, so the reviewer could not see it and flagged the gap
instead of looking it up.

---

## Genuinely open items (worth acting on)

### A. `streaming.go` watchdog double-`Close` race — **VALID**

`fp:10f5ebe00c651eba`, `fp:93c7223fd43e5c27`, `fp:b3c970f4eebe3f21`.

At head (`6b8696c`) the watchdog is:

```go
select {
case <-ctx.Done():
	select {
	case <-done:
	default:
		closeOnce.Do(func() { _ = rc.Close() })
	}
case <-done:
}
```

The inner `select` is a check-then-act, not synchronization. If `ctx.Done()` is
chosen and the `done` check takes the `default` branch, then `stop()` closes
`done` and the caller's own `Close` defer runs, the watchdog's
`closeOnce.Do(rc.Close)` and the caller's `Close` can both fire — a double close
on a body that is not idempotent for arbitrary `io.Closer` (an `http.Response`
body tolerates it; a `net.Conn` does not). `sync.Once` here guards the watchdog
against itself, not against the caller.

Fix the reviewer points at (accept): have `stop()` itself close `rc` under the
same once, and drop the watchdog's own close — then the single owner of `rc.Close`
is `stop`, which already blocks on `<-exited`. Alternatively gate both behind one
mutex-held "claimed" flag. Also update the doc comment, which currently claims no
possible double `Close`.

### B. Mistral context window 131072 → 128000 — **VALID, decide**

`fp:a3cca7e9a44cf483`. `main` had `MaxContextTokens: 131072` for Mistral; the
branch has `0`, and the registry default is 128000 (`capabilities_registry.go:424`).
Every unregistered Mistral model now reports 128000, a 3072-token reduction. Every
other migrated provider kept its previous value. Either accept 128000 as
authoritative (and say so in the commit) or add a `mistral:*` registry entry (or a
static default of 131072) to hold the line. Not a bug, but a silent behaviour
change riding inside a refactor — it should be an explicit decision.

### C. Reasoning flag set twice for Z.AI — **VALID (minor)**

`fp:e868512b6578ff84`, `fp:1157923e0e10001f`. The new `zai:*` entries set
`SupportsReasoning: true`, and `registerReasoningAndCaching` also flips it on the
`ProviderZAI` default (`capabilities_registry.go:600`). A future/unknown `glm` id
that falls through to the default reports reasoning true. The per-model entries
are now the only ones the comment justifies, so either drop the default flip or
restore the comment. Documentation/behaviour mismatch, not a demonstrated bug.

### D. `StartStreamWatchdog` stop panic — **ALREADY FIXED at head**

`fp:960e5d5c23d8c5e0` (error) and the line-657 warning. Fixed in `6b8696c`: the
stop closure is `stopOnce.Do(func() { close(done) })`, so a second call no longer
panics on `close of closed channel`. No action.

### E. Not a defect

`fp:c1585145e4fe41bf` (`streaming.go:663`, stop blocks on `<-exited`) — the
reviewer's own rationale concludes "No defect here". Filed as info; nothing to do.

---

## Suggestion for open-nitpick

One class of false positive caused almost every error in this PR: **a finding
rests on the behaviour of code outside the diff.** Both the zero-token error and
the verifiability warning name the missing callee/source in their own rationale
("returns the struct verbatim"; "the diff for openaicompat is not shown"; "that
file is not in the diff") and then file anyway.

- When a claim depends on a symbol not in the diff, resolve it in the working
  tree before filing, or emit `needs-context` instead of `error`.
- For a "value changed" claim, diff the *effect* through the call chain
  (`Capabilities()` merge here), not the literal alone — a `0` that means
  "unset" is not a value change.
- For a "source not verifiable" claim, check the cited file exists in the repo
  before flagging it; `grep` the value against it.

---

## Verbatim evidence — live `gh api` rounds (captured 2026-10-14)

Commands run from any directory (no local checkout needed):

```
gh api repos/nocturnium/llm-go-sdk/pulls/96/reviews  --paginate   # 3 reviews
gh api repos/nocturnium/llm-go-sdk/pulls/96/comments --paginate   # 16 review comments
gh api repos/nocturnium/llm-go-sdk/pulls/96                       # head/base
```

PR state at capture: `state=open`, `head=6b8696c467ae1d11ba8aec39e4de213b75d61386`,
`base=cbc591760ba801e79d19568a362b00a0d6818dd1`, `mergeable=true`, `merged=false`.

### The three review rounds, verbatim headers

```
review 5512701960  d89e086  github-actions[bot]  COMMENTED  2026-10-14T03:26:03Z
  Read 10 changed files. 5 findings: 3 error, 2 warning.

review 5512727620  cea11f2  github-actions[bot]  COMMENTED  2026-10-14T03:30:19Z
  Read 10 of 11 changed files. 1 file had nothing to review. 6 findings: 1 error, 4 warning, 1 info.

review 5512757146  6b8696c  github-actions[bot]  COMMENTED  2026-10-14T03:35:12Z
  Read 10 of 11 changed files. 1 file had nothing to review. 5 findings: 2 warning, 3 info.
```

So the error count falls 3 → 1 → **0** across the rounds: the head round carries
**no `error`-severity finding at all**.

### Head round (`6b8696c`) passes on the token-limit class — verbatim comment

```
comment 4266498464  path=pkg/providers/cerebras/cerebras.go line=21 commit=6b8696c
🔵 info · correctness
Zeroed static limits are re-filled by the registry default
`openaicompat.BaseProvider.Capabilities()` merges the registry defaults with the
static config, and the registry default for Cerebras is 128000/8192, so this change
is a no-op for Cerebras. The same holds for DeepSeek (128000/8192) and Mistral
(128000/8192), so the stated goal of removing the literals is not achieved for any
of the three.
```

The reviewer itself withdrew the error and re-filed the same lines as `info` "no-op".
The `d89e086` round labels these "Reported by the reviewer, then withheld after a
domain expert disagreed".

### Every `fp:` id cited above, verbatim from the API

```
fp:96f1bbce7ea7ff48  4266450785  pkg/providers/cerebras/cerebras.go:21  d89e086  error
fp:6d72c1c5417da032  4266450792  pkg/providers/deepseek/deepseek.go:21  d89e086  error
fp:8edab2389542b386  4266450799  pkg/providers/mistral/mistral.go:28    d89e086  error
fp:01ef0b4775f2d78b  4266498464  pkg/providers/cerebras/cerebras.go:21  6b8696c  info (no-op)
fp:3d4688f861d44d06  4266498450  capabilities_registry.go:493           6b8696c  warning
fp:0773bd64c4d6da40  4266473202  capabilities_registry.go:493           6b8696c  warning
fp:10f5ebe00c651eba  4266473224  streaming.go:653                       6b8696c  warning
fp:93c7223fd43e5c27  4266498453  streaming.go:653                       6b8696c  warning
fp:b3c970f4eebe3f21  4266450809  streaming.go:649                       d89e086  warning
fp:a3cca7e9a44cf483  4266473220  pkg/providers/mistral/mistral.go:28    6b8696c  warning
fp:e868512b6578ff84  4266473227  capabilities_registry.go:500           6b8696c  info
fp:1157923e0e10001f  4266498461  capabilities_registry.go:500           6b8696c  info
fp:960e5d5c23d8c5e0  4266473194  streaming.go:657                       cea11f2  error
fp:c1585145e4fe41bf  4266498468  streaming.go:663                       6b8696c  info
```

Every id in this file's tables resolves in the live API; the `fp:` strings are the
API's own, not reconstructed.

### Independent probe — the false positive, run against the PR head

`git archive 6b8696c467ae1d11ba8aec39e4de213b75d61386` (the live head) into a scratch
dir, then a `main.go` importing the SDK with a `replace` directive pointing at that
tree. Note: the local branch ref `task/pickup-the-medium-and-minor-the-2baa71` in the
conversation checkout resolves to the stale `d89e086`, so the probe was run against the
committed head object, not the branch name.

```
REGISTRY cerebras  llama3.1-8b          MaxContextTokens=128000 MaxOutputTokens=8192
REGISTRY deepseek  deepseek-chat        MaxContextTokens=128000 MaxOutputTokens=8192
REGISTRY mistral   mistral-large-latest MaxContextTokens=128000 MaxOutputTokens=8192
CLIENT   cerebras llama3.1-8b MaxContextTokens=128000 MaxOutputTokens=8192
CLIENT   deepseek deepseek-chat MaxContextTokens=128000 MaxOutputTokens=8192
CLIENT   mistral mistral-large-latest MaxContextTokens=128000 MaxOutputTokens=8192
```

`CLIENT` rows call the *real* `cerebras.New(...).Capabilities()` — the path a caller
takes. None reports `0`. (`GOWORK=off` was required; the parent `go.work` omits this
module.)

Baseline `main` (`cbc5917`) for Mistral reports `131072`, confirming item B's
3072-token delta is real — but that is the *decision* item, not a zero-limit bug.

The same probe against the head also confirms the Z.AI entries:

```
REGISTRY zai glm-5.2 ctx=1000000 out=128000 reasoning=true
REGISTRY zai glm-4.5 ctx=128000  out=96000  reasoning=true
```

### Watchdog at the live head — what is actually fixed

`6b8696c` is titled `fix(streaming): make watchdog stop idempotent and race-free` and
the head source reads:

```go
func StartStreamWatchdog(ctx context.Context, rc io.Closer) func() {
	done := make(chan struct{})
	exited := make(chan struct{})
	var stopOnce, closeOnce sync.Once
	go func() {
		defer close(exited)
		select {
		case <-ctx.Done():
			select {
			case <-done:
			default:
				closeOnce.Do(func() { _ = rc.Close() })
			}
		case <-done:
		}
	}()
	return func() {
		stopOnce.Do(func() { close(done) })
		<-exited
	}
}
```

```
grep -n 'stopOnce\|closeOnce' streaming.go
```

- `stopOnce` is present at head — **item D (stop panics on double call) is genuinely
  fixed**; the earlier `cea11f2` error no longer applies.
- The inner `select` is still a check-then-act: it serializes the watchdog against
  itself (`closeOnce`) but not against the caller's own `Close`, **item A stands at
  head**.
- `stop` blocks on `<-exited` unconditionally — that is item E, which the reviewer
  itself calls "No defect here".

### Z.AI "unverifiable source" warning — refuted

`pkg/providers/zai/models.go` is present at head and every registry literal matches it:

| model | registry `capabilities_registry.go` | `pkg/providers/zai/models.go` |
|---|---|---|
| glm-5.2 | 1000000 / 128000 | `ContextLength: 1000000`, `MaxOutput: 128000` |
| glm-5.1, glm-5, glm-5-turbo, glm-4.7, glm-4.6 | 200000 / 128000 | `ContextLength: 200000`, `MaxOutput: 128000` |
| glm-4.5, glm-4.5-air | 128000 / 96000 | `ContextLength: 128000`, `MaxOutput: 96000` |

```
grep -n 'ContextLength:\|MaxOutput:' pkg/providers/zai/models.go
```

No value was invented; the "source not verifiable" warning is a false positive of the
same class as the zero-limit error (the file that verifies the claim is outside the diff).
