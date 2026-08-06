# open-nitpick

Self-hosted, model-agnostic pull request review.

open-nitpick reads a pull request, reviews it with **a model you choose**, and
posts inline comments. It runs as a GitHub Action, as a CLI in any CI system, or
against your uncommitted working tree before you even open a pull request.

There is no hosted service, no per-seat pricing, and no vendor holding your
source. You bring the model — including a local one — and the review behavior is
driven by configuration and prompts you can read and change.

```bash
go install github.com/jdziat/open-nitpick/cmd/nitpick@latest

export LLM_PROVIDER=openrouter LLM_MODEL=anthropic/claude-sonnet-4.6
export OPENROUTER_API_KEY=sk-or-...

nitpick review          # reviews your uncommitted changes
```

## Why this exists

Most review bots are a hosted service wrapping one vendor's model, with a prompt
you cannot see and pricing per seat. open-nitpick inverts that:

- **Any model.** 17 providers via [llm-go-sdk][sdk], plus a built-in
  `openrouter` that reaches the rest of the catalogue on one key, plus any
  OpenAI-compatible endpoint through `base_url`, plus local models via `ollama`
  and `llamacpp`.
- **Different models for different jobs.** A cheap model triages and deduplicates;
  an expensive one does the actual reviewing. That split is most of the cost
  saving available.
- **Prompts as configuration.** Path-scoped instructions live in your repository
  next to the code they describe. `nitpick explain-config` prints the exact
  prompt that will be sent, before you spend a token on it.
- **Linters as evidence.** golangci-lint, ruff, eslint, and semgrep findings are
  fed to the model for triage, not dumped raw into your pull request.
- **Runs offline.** The whole engine works against a local git checkout with no
  credentials and no network beyond the model call.

[sdk]: https://github.com/nocturnium/llm-go-sdk

## Usage

### Locally

```bash
nitpick review                          # uncommitted changes
nitpick review -base main               # working tree against main
nitpick review -base main -head feature # a committed branch
nitpick explain-config -path src/db.go  # what would be sent, and why
nitpick providers                       # available model providers
```

Local reviews print to stdout as `path:line`, which most terminals and editors
turn into a clickable link.

### GitHub Actions

```yaml
name: Review
on:
  pull_request:
    types: [opened, synchronize, reopened]

permissions:
  contents: read
  pull-requests: write

jobs:
  review:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0        # the reviewer needs history to diff against base
      - uses: jdziat/open-nitpick@v1
        with:
          provider: openrouter
          model: anthropic/claude-sonnet-4.6
          api-key: ${{ secrets.OPENROUTER_API_KEY }}
          # Omit fail-on (or set it to none) until you have seen how the model
          # behaves on your codebase. A reviewer that blocks merges on its first
          # false positive is a reviewer the team switches off.
          fail-on: none
```

Reviews are **not** deduplicated against comments already on the pull request,
so `synchronize` would re-post the same findings on every push. Until that
lands, trigger on `[opened, reopened]` only.

Exit codes: `0` clean, `1` findings at or above `fail_on`, `2` the review could
not run. CI can tell "this change has problems" apart from "the reviewer broke".

### Any other CI

```bash
nitpick review -owner acme -repo-name widgets -pr 42
```

with `GITHUB_TOKEN` in the environment. Inside GitHub Actions the repository and
pull request number are detected automatically.

## Configuration

Everything is optional — with no config file at all, `LLM_PROVIDER` and
`LLM_MODEL` are enough to run. `.nitpick.yaml` at the repository root:

```yaml
models:
  default:
    provider: openrouter
    model: anthropic/claude-sonnet-4.6
    max_tokens: 8192

  triage:                          # cheap model for merging and filtering
    provider: openrouter
    model: qwen/qwen3.7-flash
    temperature: 0

review:
  fail_on: none                    # default: advisory. Set to error/critical to gate CI.
  min_severity: info               # drop anything below this entirely
  max_files: 60
  token_budget_per_request: 60000
  include_full_files: true         # send whole files, not just hunks
  ignore: ["**/vendor/**", "**/*.pb.go"]

instructions:                      # path-scoped, and they compose
  - path: "**/*.go"
    prompt: Flag unchecked errors and goroutine leaks. Ignore formatting.
  - path: "**/migrations/**"
    prompt: Treat any non-reversible DDL as a blocking finding.

linters:
  mode: auto                       # auto | strict | off
  enabled: [golangci-lint, ruff, eslint, semgrep]
  only_changed_lines: true
  max_severity: critical           # ceiling on findings attributed to an analyzer
```

Analyzer severities are translated onto the five levels above, and the analyzer's
own word is kept beside the result: `HIGH` and `ERROR` both become error (they are
the same level in semgrep's scale), `MEDIUM` becomes warning, `CRITICAL` becomes
critical, and a word we cannot read — like an unrecognized string in your
`.golangci.yml` — becomes warning, the same as no severity at all.

Whether a deterministic tool should be able to fail your build at
`fail_on: critical` is your decision, not this tool's. `linters.max_severity` is
where you make it: `warning` means nothing reported by an analyzer is published or
gated above warning, however the analyzer rated it and however the reviewing model
re-rates it afterwards. It does not touch the model's own findings.

It has one hole, and you should know it rather than discover it. The ceiling
recognises an analyzer's finding by its attribution. If triage rewrites a finding
far enough that it can no longer be matched back — which happens when an analyzer
message spans several lines, as golangci-lint's `typecheck` output does — the
result is treated as the model's own and the ceiling does not apply to it. Titles
are flattened before triage sees them so this is rare, but "rare" is not "cannot",
and a reworded finding can be published and gated above your ceiling.

Two consequences worth knowing before you set them:

- golangci-lint's `Severity` is whatever text you put in `.golangci.yml`, and
  `severity.default` applies it to every issue including compile errors surfaced
  by `typecheck`. `severity.default: critical` therefore lets `misspell` fail a
  `fail_on: critical` build unless `max_severity` says otherwise.
- `nit` is below the default `review.min_severity` of `info`, so
  `severity.default: nit` publishes no Go analyzer findings at all.

Unknown keys are rejected at load time, so a typo fails immediately instead of
being silently ignored.

### Personality and how much it nitpicks

How a reviewer talks, and how far past outright defects it ranges, are matters
of taste that teams genuinely disagree about. Both are configuration:

```yaml
persona:
  nitpick: normal          # off | minimal | normal | pedantic
  verbosity: normal        # terse | normal | detailed
  politeness: neutral      # blunt | neutral | warm
  confidence: direct       # direct | hedged
  address: impersonal      # impersonal | author
  emoji: true
  praise: false
  custom: >-
    Reference our ADRs by number when a finding contradicts one.
```

**`nitpick`** is the setting people actually argue about. It selects which
*classes* of finding get published, where `min_severity` selects how serious
they must be — independent questions, applied independently.

It is a **post-hoc filter, not a change to the prompt**. Every review is
generated at one fixed scope and narrowed afterwards. That keeps the levels
comparable (a difference between two levels is the filter, not model variance)
and stops a wider setting from diluting the defect hunt — measured: asking one
pass to also consider style produced more findings, lower precision, and more
missed real defects. `pedantic` is the exception, since filtering can only
narrow: it adds a separate style-only pass.

| level | reports |
|---|---|
| `off` | only correctness, concurrency, security, resource handling, data loss |
| `minimal` | the above, plus contract and compatibility breakage |
| `normal` | the above, plus missing tests for risky logic and maintainability problems with a named cost |
| `pedantic` | the above, plus naming, docs, idiom, and consistency — the things every other level forbids |

Every level except `pedantic` explicitly forbids naming and style commentary,
and every level requires a stated consequence. `pedantic` still does: a nit with
no cost attached is noise even there.

**The voice axes never change what gets reported** — only how it is worded. A
blunt reviewer and a warm one find the same defects and assign the same
severities. That separation is enforced by a test, because a tone knob that
quietly became a quality knob would be the worst kind of bug: two teams would
get different reviews of the same code while believing they had only picked a
different register.

`nitpick explain-config` prints the resolved persona and the exact prompt it
produces.

### Severities

`nit` < `info` < `warning` < `error` < `critical`. `fail_on: none` never fails
the build.

### OpenRouter

`openrouter` is a provider in its own right, so it needs a key and nothing else:

```yaml
models:
  default:
    provider: openrouter
    model: anthropic/claude-sonnet-4.6
```

```bash
export OPENROUTER_API_KEY=sk-or-...
```

This is what this repository's own `.nitpick.yaml` uses. Its endpoint is
compiled into the binary rather than read from `base_url`, and that is the whole
reason it can be a committed default: `base_url` and `api_key_env` are stripped
from a config the reviewer does not trust (see [Trust model](#trust-model)), so
the same setup written against the `openai` provider would work only for whoever
had exported `NITPICK_TRUST_CONFIG_ENDPOINTS` — and would silently talk to
OpenAI for everybody else.

`LLM_API_KEY` is accepted as a fallback, which is how the GitHub Action's
`api-key` input arrives. `OPENROUTER_API_KEY` wins when both are set, so a
generic key exported for some other vendor is never the one sent here.
`OPENAI_API_KEY` is deliberately *not* accepted: it is a credential for a
different host.

What the compiled-in endpoint does **not** buy you: `provider` and `model` still
come from the config file, and for a router the model id chooses which upstream
receives the code. See [Trust model](#trust-model).

### Other OpenAI-compatible gateways (vLLM, LiteLLM)

Any other OpenAI-compatible endpoint works through the `openai` provider:

```yaml
models:
  default:
    provider: openai
    model: my-model
    base_url: https://gateway.example.com/v1
    api_key_env: MY_GATEWAY_KEY
```

`base_url` and `api_key_env` are only honored when the config file is trusted —
see [Trust model](#trust-model) — so set `NITPICK_TRUST_CONFIG_ENDPOINTS=1`, or
pass the endpoint via `LLM_BASE_URL` instead of committing it.

### Using a local model

```yaml
models:
  default:
    provider: ollama
    model: qwen2.5-coder:14b
```

Pointing a *different* provider at a private address requires two opt-ins:

```yaml
models:
  default:
    provider: openai
    model: my-model
    base_url: http://192.168.1.10:8000/v1
    api_key_env: MY_GATEWAY_KEY
    allow_private_endpoint: true
```

plus `NITPICK_TRUST_CONFIG_ENDPOINTS=1` in the environment.

## Trust model

**open-nitpick assumes the code and the config it reviews are hostile.** It runs
in CI against pull requests, and a pull request can edit any file in the repo —
including `.nitpick.yaml`. Three consequences:

- **`base_url`, `api_key_env`, `extra`, and `allow_private_endpoint` are ignored**
  by default when read from the reviewed repository. Otherwise a contributor
  could point the reviewer at an endpoint they control and name the environment
  variable to send as the bearer token — exfiltrating `GITHUB_TOKEN` or your
  model key in one line of YAML. Set `NITPICK_TRUST_CONFIG_ENDPOINTS=1` to allow
  them, only where you control the file. Ignored keys are logged, never silent.
  Providers whose endpoint is compiled in — `openrouter`, `anthropic`, `ollama`,
  and the rest — are unaffected, which is why the shipped default names one
  rather than a `base_url`.
- **`provider` and `model` are *not* stripped, and that is the residual risk.**
  A pull request editing its own `.nitpick.yaml` cannot change the endpoint or
  the bearer token, but it can still choose which model reads the diff. Two
  consequences worth naming, because the bullet above does not cover them: it
  can point the review at a weak or free-tier model and get a quiet zero-finding
  run, and — because a router's model id *is* its routing key — it can change
  which upstream inference operator receives the code under review, including
  the whole-file bodies `review.include_full_files` sends. Neither is specific
  to `openrouter`; naming a router as the default is what makes the reachable
  set a whole catalogue rather than one vendor's. Review `.nitpick.yaml` changes
  on their own merits, exactly as you would a change to a CI workflow.
- **`api_key_env` may never name a forge credential** (`GITHUB_TOKEN` and
  friends), even in a trusted config. A model provider has no business receiving
  it, and the likeliest reason to ask is exfiltration.
- **Linters run only from `PATH`, never from the repository.** A pull request can
  add an executable `node_modules/.bin/eslint`; running it would execute
  attacker code with your credentials in the environment.

Local reviews are confined to the checkout: a committed symlink pointing at
`~/.ssh/id_rsa` will not be read or sent to your model endpoint.

Diff content and pull request text reach the model as explicitly-fenced
untrusted data, and are never rendered as templates.

### Structured output

Findings are constrained to a schema. By default (`structured_output: auto`)
open-nitpick requests a JSON-Schema response format and, if the provider
*rejects* it, falls back to JSON mode with lenient parsing and one bounded
repair attempt — remembering that downgrade so it is paid for once per run
rather than once per request. Force either path with `structured_output: schema`
or `json`.

A provider that *accepts* the schema and then ignores it is handled separately
and deliberately: the response is rejected, the same request is retried on the
JSON path, and the client is **not** downgraded. Routers can hand consecutive
requests to different upstreams, so one unenforced answer is a fact about that
answer, not about the provider — and a downgrade would move every later batch of
the run onto a different strategy with nothing in the report saying so. A
response that satisfies neither path fails the batch loudly and lands in the
run's incomplete list; it is never reported as a clean review.

This is what makes small local models usable: they need the fallback, and
hard-coding the strict path would exclude them.

## How a review runs

```
diff → select and batch files → review each batch → triage → render → publish
```

- **Select** drops ignored, binary, deleted, and generated files, and reports
  every exclusion. A review that quietly skipped half the diff would otherwise
  look identical to a clean one.
- **Batch** groups files under a token budget, attaching whole file contents
  where they fit and a window around the changes where they do not.
- **Review** runs batches concurrently. One failed batch is logged and skipped;
  *every* batch failing is an error rather than a "no issues found".
- **Triage** merges duplicates across batches, drops unsupported findings, and
  writes the walkthrough. It may reword and merge, but it cannot invent findings
  for files nobody reported on.
- **Anchor** snaps near-miss line numbers onto real changed lines and drops
  findings that cannot be placed, so comments land where they belong.

## Measurement

This project makes empirical claims about review quality, so how those numbers are
produced is part of the product.

- [docs/measurement.md](docs/measurement.md) — what has to hold before a number
  out of the eval harness is worth acting on. Thirteen rules, each written
  because the harness produced a confident wrong number and something believed it.
- [docs/findings.md](docs/findings.md) — what has actually been measured, what it
  supports, and the nine instrument bugs found so far. Four of them flattered one
  side of a comparison; one produced a published claim that had to be retracted.

The short version: prefer the judge-free columns. The LLM judge runs at
temperature 0 and still scores byte-identical input anywhere from 3.66 to 3.98.

## Development

```bash
go test ./...        # no network or credentials required
go test -race ./...
```

### Evaluating the prompts against real models

Unit tests prove the *tooling* is correct given a scripted model. They cannot
tell you whether the *prompts* work — whether a real model, handed a real diff,
finds the bug, anchors it to the right line, and stays quiet about code that is
fine. `make eval` measures that.

```bash
echo 'OPENROUTER_API_KEY=sk-or-...' > .env    # gitignored

make eval                                     # 17-model matrix — see the cost note below
make eval MODELS=openai/gpt-4o-mini           # one model
make eval RUNS=5                              # run-to-run stability
make eval FIXTURES=go-nil-deref               # one fixture
make eval CAPTURE=testdata/responses          # save raw model output
```

It is the same `OPENROUTER_API_KEY` the default config uses, but only the eval
harness reads `.env` — `nitpick review` does not, so export it (`set -a; . ./.env;
set +a`) or keep it in your shell profile.

Each fixture is a synthetic pull request with bugs planted at known lines,
built into a real git repository and reviewed through the real engine — so the
diff parsing, batching, structured output, anchoring, and rendering are all
exercised, not just the prompt.

The report separates three things that are easy to confuse:

- **Invariants** — properties open-nitpick must uphold whatever the model does:
  every severity is a real level, every comment is placeable, and no suggestion
  is published as one-click-applicable unless it is a single line of code. A
  breach is a bug in this repository and fails the run.
- **Recall** — planted defects found. Reported per fixture; a run fails only if
  a model finds *nothing* across the whole corpus, which means the prompt or
  the plumbing is broken rather than merely weak.
- **Noise** — findings explaining no planted defect. Two fixtures
  (`clean-refactor`, `style-only`) contain no bugs at all, so every finding
  there is noise by construction.

**Cost.** The default matrix is **17 models x 8 fixtures = 136 reviews**, plus a
judge call each for `make judge-models`. That is not a cheap command. Pass
`MODELS=` to narrow it:

```bash
make eval MODELS=qwen/qwen3.7-flash            # one model, 8 reviews
```

The matrix spans providers and price tiers deliberately, including models with
and without JSON-Schema support so the structured-output fallback is exercised.

`CAPTURE` writes every raw model response to disk. Those become offline
regression fixtures — the cheapest way to keep the extractor honest without
paying for tokens on every test run.

The whole pipeline is tested against a scripted model and a stub GitHub API, so
the test suite exercises real behavior rather than mocks of its own design. Diff
position mapping is additionally cross-checked against real `git diff` output by
a second, independent implementation.

## Status

Early. The engine, both providers, structured output, linters, and the Action
work end to end. Not yet done: incremental re-review on push, resolving
superseded comments, `@nitpick` command handling, and a GitLab provider.

## License

Apache-2.0.
