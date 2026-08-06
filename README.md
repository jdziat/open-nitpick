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
  enabled: [golangci-lint, ruff]   # add eslint/semgrep once you have configured them
  only_changed_lines: true
  max_severity: critical           # ceiling on findings attributed to an analyzer

  # Analyzer configuration. Every one of these must resolve OUTSIDE the
  # repository under review; a path inside it is refused. Empty is the default.
  golangci_config: ""              # empty: golangci-lint runs with --no-config
  ruff_config: ""                  # empty: ruff runs with --isolated
  eslint_config: ""                # empty: eslint does not run
  semgrep_config: ""               # empty: semgrep does not run
```

**Analyzers do not read configuration from the branch under review.**
golangci-lint and ruff run isolated, with their own defaults; eslint and semgrep
do not run at all until you point them at a configuration you control — which is
also why they are not in the default `enabled` list. Adding them there without
setting their config is a mistake `mode: strict` will fail on, and that is the
point; having them enabled by default only meant strict failed on every review.
The reason for all of it is the one this project already applies to
`.nitpick.yaml`: a change may not
supply the policy it is reviewed under, and analyzer configuration is policy. A
pull request adding a `.golangci.yml` with `linters: {default: none}` was
switching off the entire deterministic half of its own review, and the run
reported success.

The cost is real and it applies to every pull request, not only the ones that
edit these files: **your `.golangci.yml` and your `[tool.ruff]` settings do not
apply.** Your enabled linter set, your exclusions, your `per-file-ignores` —
none of it. Isolated ruff in particular reports things your configuration was
suppressing, so expect it to be noisier rather than quieter. `golangci_config`
and `ruff_config` are the way back, and the file has to live outside the
repository: a path your CI provisions, a mounted config, a config repo checked
out beside this one.

eslint's cost is not a flag, which is why it is off rather than isolated. An
eslint config is JavaScript that eslint loads and *executes*, so a config inside
the tree can be rewritten by the pull request being reviewed — arbitrary code in
CI with `GITHUB_TOKEN` and your model API key in the environment. But a config
outside the tree cannot resolve its own plugin imports, because Node resolves
them relative to the config file's own directory. Enabling eslint therefore means
provisioning a config *directory* with its own `node_modules` holding every
plugin, parser and shared config it imports — not just a file. And an external
config that imports back into the repository (`import "./eslint-rules/index.js"`)
re-opens the hole completely: the config is a loader, and substituting the loader
does not substitute what it loads.

semgrep is off for a different reason: it has no default rule set, so with no
`--config` it analyzes nothing. Point `semgrep_config` at a rule file outside the
repository, or at a registry reference (`p/...`, `r/...`) — a fetch you asked for
by name. It previously ran with `--config auto`, which semgrep refuses whenever
metrics are off; that invocation had never produced a single finding, and nothing
said so.

Every review says what every analyzer did — `ran` under `isolated` or
`operator config <path>`, `did not run: <reason>`, or `skipped` because the change
contained no files it reads. It is published **on the pull request**, in the
summary comment beside the notice about a substituted `.nitpick.yaml`, and
repeated on stderr; the counts are in the collapsed heading, so a reviewer who
never opens the block still sees that something did not run. The reason is that a
review which quietly ran less than you think looks exactly like a clean one — and
`skipped` is separated from `did not run` so that the line which means something
is not buried among three that never do.

`did not run` is not only about missing binaries. golangci-lint reports a failure
to load your packages *inside the same JSON it reports issues in*, and a pull
request can trigger one from the tree — a `go.work` that does not list the module,
a `//go:build ignore` on the file it wants unread. Those runs are reported as
failures, not as zero findings, and under `mode: strict` they fail the review.

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
message spans several lines, as a semgrep rule with a multi-line `message:` does —
the result is treated as the model's own and the ceiling does not apply to it. Titles
are flattened before triage sees them so this is rare, but "rare" is not "cannot",
and a reworded finding can be published and gated above your ceiling.

Two consequences worth knowing before you set them. Both describe
`golangci_config` only: with no config golangci-lint publishes no severity at
all, so under the default every Go analyzer finding arrives as `warning` and the
ceiling has nothing to reduce.

- golangci-lint's `Severity` is whatever text your `golangci_config` puts there,
  and `severity.default` applies it to every issue. `severity.default: critical`
  therefore lets `misspell` fail a `fail_on: critical` build unless
  `max_severity` says otherwise.
- `nit` is below the default `review.min_severity` of `info`, so
  `severity.default: nit` publishes no Go analyzer findings at all.

`max_severity` was never a defence against silencing — it only reduces — so the
two are complementary and neither substitutes for the other.

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
- **Linters run only from `PATH`, never from the repository, and neither does
  their configuration.** A pull request can add an executable
  `node_modules/.bin/eslint`; running it would execute attacker code with your
  credentials in the environment. The same reasoning covers the config file the
  binary is handed, so analyzer configuration comes only from outside the
  repository (see `golangci_config` and friends above). Three things that buys:
  a pull request cannot switch its own analyzers off *through configuration*, it
  cannot switch semgrep on with rules it wrote, and it cannot author the text of
  a finding that reaches the reviewing model as deterministic evidence —
  golangci-lint's `forbidigo` prints a `msg` from the config file verbatim, so a
  config in the tree could address the reviewing model directly through a channel
  labelled as tool output.
- **Configuration is not the only way the tree can silence an analyzer.** A
  change that breaks golangci-lint's package load — `go.work` omitting the
  module, a build constraint excluding the changed file, a `toolchain` directive
  it cannot satisfy — stops the Go analyzer as completely as any config file
  would, and none of it goes through one. golangci-lint reports those failures
  inside the same JSON envelope it reports issues in, so they were read as zero
  findings for a while; they are now failures, reported on the pull request and
  fatal under `mode: strict`. Deleting `go.mod` still stops the analyzer too —
  that one is at least visible in the diff, and it is reported as
  `did not run: no go.mod at or above the changed Go files`.
- **Code that does not compile is the same silencing, and needs no attack at
  all.** Go is analyzed a package at a time, so one file that does not build
  stops every linter for every package in that invocation. golangci-lint reports
  it as an ordinary `typecheck` issue — exit 0, nothing in `Report.Error`, and
  anchored to line 1 of a *different* file — so with `only_changed_lines` it used
  to vanish entirely and the review reported success. The sharpest version is a
  broken `_test.go`, because `go build ./...` stays green while the Go review of
  the rest of the change silently reports nothing. It is now
  `did not run: the code did not compile, so no analyzer ran over it: <file>:<line>`,
  quoting the file that actually failed rather than the one it was anchored to.
  Note the corollary: an ordinary work-in-progress pull request that does not
  compile gets **no Go analyzer findings at all**, and the roster says so rather
  than implying the code was clean.
- **Published reasons escape HTML, not markdown.** An analyzer's failure reason
  is quoted on the pull request, and it quotes the tree: a Go compile error
  carries source text verbatim, so `var X int = "[CLICK](https://example)"`
  reaches the roster with that string in it. Raw HTML is escaped; markdown link
  syntax is not, so a change can put a live link into a comment posted under this
  bot's name. It reaches no model — the roster is rendered, never prompted — so
  this is a phishing surface in a trusted comment, not an injection into the
  review itself. The same is true of every other untrusted string this tool
  renders, including the substituted-policy notice.
- **Containment covers symlinks, not hard links.** An analyzer config path is
  refused if it resolves inside the repository, following symlinks on both sides.
  A hard link — the same file under a second name outside the repository — is
  accepted, because nothing short of walking the whole tree comparing inodes can
  see one. git stores no hard links, so a pull request cannot create this;
  reaching it requires write access outside the repository, which is already a
  larger problem.
- **What that does not close: in-source suppression.** A pull request can still
  suppress a deterministic finding on its own lines with `//nolint`, `# noqa`,
  `# nosemgrep` or `eslint-disable`, and golangci-lint offers no way to disable
  its own — the others have `--ignore-noqa`, `--disable-nosem` and
  `--no-inline-config`, golangci-lint has nothing. With the default
  `only_changed_lines: true` the change only needs the comment on the lines it
  touched. Suppression comments added by a diff are worth reviewing on their own
  merits, exactly as this file says about `.nitpick.yaml` changes.

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
