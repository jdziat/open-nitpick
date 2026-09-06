# Configuration

Everything is optional: with no config file at all, `LLM_PROVIDER` and
`LLM_MODEL` are enough to run. `.nitpick.yaml` at the repository root:

```yaml
models:
  default:
    provider: synthetic
    model: hf:moonshotai/Kimi-K3
    # max_tokens is unset by default: the model's own output maximum applies,
    # and reasoning tokens count against whatever you set here on most providers.
    timeout: 10m                     # default; per model call, not per review

  triage:                          # cheap model for merging and filtering
    provider: synthetic
    model: hf:zai-org/GLM-5.3-Flash
    temperature: 0

review:
  fail_on: none                    # default: advisory. Set to error/critical to gate CI.
  min_severity: info               # drop anything below this entirely
  incremental: true                # on a re-run, read only what changed since the last review
  related_context: true            # default: attach imported definitions used on changed lines (see below)
  related_context_callers: false   # default: also walk the repository for callers of what the change redefines
  slop: false                      # default: also report the slop class (see "The slop class" below)
  max_files: 60
  token_budget_per_request: 60000  # per model CALL; raise it for large-context models
  include_full_files: true         # send whole files, not just hunks
  ignore: ["**/vendor/**", "**/*.pb.go"]

instructions:                      # path-scoped, and they compose
  - path: "**/*.go"
    prompt: Flag unchecked errors and goroutine leaks. Ignore formatting.
  - path: "**/migrations/**"
    prompt: Treat any non-reversible DDL as a blocking finding.

linters:
  mode: auto                       # auto | strict | off
  enabled: [golangci-lint, ruff]   # named analyzers; strict mode fails when one is missing
  auto_detect: true                # also run any installed catalog analyzer (see the table below)
  only_changed_lines: true
  max_severity: critical           # ceiling on findings attributed to an analyzer

  # Analyzer configuration. Every one of these must resolve OUTSIDE the
  # repository under review; a path inside it is refused. Empty is the default.
  golangci_config: ""              # empty: golangci-lint runs under open-nitpick's own config
  ruff_config: ""                  # empty: ruff runs with --isolated
  eslint_config: ""                # empty: eslint does not run
  semgrep_config: ""               # empty: semgrep does not run
  configs:                         # the same, for every catalog analyzer, by name
    rubocop: /etc/nitpick/rubocop.yml
  trusted: []                      # analyzers allowed to execute the tree's code: clippy, phpstan
```

## Related context

A change is reviewed with the diff and the changed files. What the model does
not see is the function the change calls: the prompt tells it not to speculate
about code it was not shown, so a defect that turns on a callee's contract (a
helper documented as "must be called with a deadline", a converter that takes
dollars and is handed cents) is one it can only guess at.

`review.related_context`, on by default, attaches, beside each changed file,
the definitions it imports from elsewhere in the repository and uses on a
changed line: Go package-level functions, types and constants reached through the
module's own import path; TypeScript and JavaScript exports reached through a
relative import; Python module-level `def`, `class` and assignments reached
through `from x import y` or `import x`; Rust items reached through
`use crate::…` and `mod`; Ruby top-level definitions reached through
`require_relative` and `require`; Java and Kotlin classes and Kotlin top-level
functions reached through `import`, resolved from the importing file's own
package declaration; C and C++ declarations reached through a quoted
`#include`. For the four languages most changes are written in it goes one
hop further: a Go method called on a value of an imported type is attached
with its doc comment, not just the type; a TypeScript import through a
`tsconfig.json` path alias or a barrel `index.ts` is followed to the file
that defines the name; a Python name re-exported by a package `__init__.py`
is followed to its module; a Ruby constant that a Rails autoloader would
resolve is found under `app/*` and `lib/` by Zeitwerk's naming rule with no
`require` at all. Each definition is attached with its
doc comment, from its real line number, under a heading that says the file is
not under review. Nothing under `node_modules`, a module cache or outside the
checkout is ever read, and a file the change itself touches is never attached,
because the model already has it.

It also works in the other direction, behind its own switch,
`review.related_context_callers`, off by default. When a change redefines an
exported function, method or top-level export (in Go, Python, TypeScript or
JavaScript), the functions in untouched files that call it are attached too,
under a heading that tells the model to check each caller against the new
definition. This is the defect class a diff-only review cannot see by
construction: an error that stops matching a sentinel a handler compares
with `errors.Is`, a return value that changes unit, a precondition a caller
already violates. Callers are found by resolving each candidate file's imports
back to the changed file, in code rather than in comments or strings, capped
at three call sites per symbol and 150 candidate files per review, and a call
that cannot be traced to an import is not attached. A one-line constant the
caller passes comes along with it. The walk is up to 150 file fetches a
review on top of the changed files, and when that ceiling stops it short the
summary says so, so a file with no callers attached is not read as a file
with no callers. Measured on its own corpus in
[docs/findings.md](findings.md#callers-2026-09-05): a cheap model went
from finding none of the planted contract breaks to seven of eight.

It is bounded by `review.related_context_tokens` per batch, spent only from
what the request budget has left after the changed files themselves, so it can
narrow nothing the file under review would have got. The summary lists every
file attached as context; a file read by the caller walk and found to hold no
caller is not listed.

The two switches differ in what they read. Definitions come from files the
change already names through its imports, so attaching them discloses
nothing a reviewer of that diff would not open, and every price in the model
sweep was measured with them on; that direction ships on. The caller walk
reads up to 150 files the change never named and sends excerpts to the model,
which is a different consent boundary from "review my diff", so it ships off
and the measured gain (0/8 to 7/8 on its corpus, noise down on the multi-file
corpus, in [docs/findings.md](findings.md#callers-2026-09-05)) is for
the operator to weigh against that. Context is not free either way: the same
definitions that let a model confirm a defect give it more to be confidently
wrong about.

## Personality and how much it nitpicks

How a reviewer talks, and how far past outright defects it ranges, are matters
of taste that teams disagree about. Both are configuration:

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

!!! warning "`custom` is dropped unless the config is trusted"

    `persona.custom` is free text that lands in the **system** prompt, the
    highest-trust position there is. Every other string from the config file
    reaches the model as user-message data. A pull request can edit
    `.nitpick.yaml`, so a change could otherwise instruct the reviewer to stay
    quiet about itself.

    It is therefore stripped unless `NITPICK_TRUST_CONFIG_ENDPOINTS=true` is
    set in the environment, the same out-of-band switch that unlocks
    `base_url` and `api_key_env`. Stripping is reported, not silent: the
    dropped key is named in the run's log and in `nitpick explain-config`. The
    enumerated axes above are always honored, because they are bounded and
    validated. See [Trust model](trust-model.md).

**`nitpick`** is the setting people argue about. It selects which
*classes* of finding get published, where `min_severity` selects how serious
they must be: independent questions, applied independently.

It is a **post-hoc filter, not a change to the prompt**. Every review is
generated at one fixed scope and narrowed afterwards. That keeps the levels
comparable (a difference between two levels is the filter, not model variance)
and stops a wider setting from diluting the defect hunt: measured, asking one
pass to also consider style produced more findings, lower precision, and more
missed real defects. `pedantic` is the exception, since filtering can only
narrow: it adds a separate style-only pass.

| level | reports |
|---|---|
| `off` | only correctness, concurrency, security, resource handling, data loss |
| `minimal` | the above, plus contract and compatibility breakage |
| `normal` | the above, plus missing tests for risky logic and maintainability problems with a named cost |
| `pedantic` | the above, plus naming, docs, idiom, and consistency: the things every other level forbids |

Every level except `pedantic` explicitly forbids naming and style commentary,
and every level requires a stated consequence. `pedantic` still does: a nit with
no cost attached is noise even there.

**The voice axes never change what gets reported**, only how it is worded. A
blunt reviewer and a warm one find the same defects and assign the same
severities. That separation is enforced by a test, because a tone knob that
quietly became a quality knob would be the worst kind of bug: two teams would
get different reviews of the same code while believing they had only picked a
different register.

`nitpick explain-config` prints the resolved persona and the exact prompt it
produces.

## Notes addressed to the model family

The review prompt carries one more layer, chosen by the reviewing model's
name: a short list of habits to avoid, written for a model family whose eval
reviews showed the habit and kept only when measured against its absence.
Today one family has one. Qwen models are told to decide about every hunk of
the diff before reading the related-context definitions, which is where their
single-file recall went when related context was on; with the note, recall
rose on all three corpora and noise did not. A GLM note was tried and measured
out (no recall change, noise moved both ways), so GLM gets none, and so does
every other family.

The text lives in `internal/prompt/model.go`, never widens what a model is
asked to look for, and shows up in `nitpick explain-config` as the `model`
layer so it can be read without spending tokens. `review.model_notes: false`
removes it. The measurements are in
[docs/comparison.md](comparison.md#tuning-for-glm-53-flash-and-qwen38-27b-2026-09-04).

## Choosing the model per batch

One model for everything is the default. Two optional mechanisms change that,
and both overlay `models.default`, so each entry names only what differs.

**Routes** pick the reviewing model for a batch. The first route whose match
holds wins, and a batch no route matches goes to the review model.

```yaml
models:
  default:
    model: google/gemma-4-31b-it
  router:                          # optional; only routes that match on kinds need it
    model: z-ai/glm-5.3-flash
  routes:
    - name: security
      match: {kinds: [security, concurrency]}
      review: {model: qwen/qwen3.8-27b}
    - name: typescript
      match: {languages: [typescript, javascript]}
      review: {model: qwen/qwen3.8-27b}
    - name: cross-file
      match: {min_files: 2}
      review: {model: z-ai/glm-5.3-flash}
```

A match takes `languages` (the batch matches when any file is in one of them),
`kinds`, and the bounds `min_files` and `max_files`. Kinds come from the
**router**: a cheap model that reads each batch once and answers with what the
change does. Naming a kind without configuring `models.router` is a
configuration error, and routes that match only on languages or file counts
never call it.

**Ensembles** add reviewers rather than replacing one. Every model listed
reviews every batch, the findings are pooled, and triage merges and reranks
them, so a defect two models report independently becomes one finding whose
agreement is a reason to trust its level.

```yaml
models:
  ensemble:
    - model: z-ai/glm-5.3-flash
    - model: qwen/qwen3.8-27b
```

A route may carry its own `ensemble`, which replaces the global one for the
batches it matches; an empty list on a route removes the ensemble there. Cost
scales with the number of reviewers, near enough linearly. The measured
configurations, and what each costs per review, are in
[Findings](findings.md).

## The validation pass

Off by default. When on, each published finding is put to a domain expert,
which may overrule it.

```yaml
validation:
  enabled: false                   # default
  classes: [security, correctness] # empty validates every class
```

It costs one model call per published finding, on `models.validate` if set and
`models.default` otherwise, and a separate model is the point: an independent
check is worth more when it is not the same weights re-reading their own claim.
It is off because it is **unmeasured**, and what has not been measured is the
direction that matters, how many real defects an expert talks itself out of.

An unlisted class is published **without** validation, never dropped, so
narrowing `classes` can only reduce refutations. Overruled findings are not
discarded silently; they are reported with the reason.

## Severities

`nit` < `info` < `warning` < `error` < `critical`. `fail_on: none` never fails
the build.

## How findings are written

Every prompt carries a voice layer: a title of at most 12 words, a
rationale of at most 3 sentences, no em dashes, no filler, no hedging as
prose, no chat. It is in `nitpick explain-config` under `layer: voice`.
Because a prompt asks and cannot enforce, model-authored prose is scrubbed
before it is published: em dashes and en-dash separators become commas,
arrows become words, filler words and chat openers and offers of further
help are removed, and code spans and fenced blocks are left exactly as the
model wrote them. The same rules and the same scrub apply to an `@nitpick`
answer, which is capped at 120 words.

## The mention

`review.mention` (default `@nitpick`) is the handle a pull request comment
uses to talk to the reviewer; see the CI page for the workflow that
answers it.

## Resolving superseded comments

`review.resolve_superseded` (default on) lets an incremental run close its
own earlier comment threads when the lines they pointed at changed since
the earlier review and the finding did not recur, with a reply on the
thread saying so. A comment on a file the run did not re-read is left
alone, since nothing there was checked; so is every comment when the
earlier revision could not be compared (a force push). The GitHub App or
token needs Pull requests write, which posting reviews already needs.

## Skipping a pull request

`review.skip_markers` lists the phrases that, in a pull request's title,
body or head commit message, ask for no review. The default is
`["[skip review]", "[skip nitpick]"]`; the match is case-insensitive.
