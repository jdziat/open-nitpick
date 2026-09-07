# Configuration

Everything is optional: with no config file at all, `LLM_PROVIDER` and
`LLM_MODEL` are enough to run. `.nitpick.yaml` at the repository root:

`nitpick init` writes one of these for you, commented, with the analyzers this
checkout's languages call for. The reference below is what to reach for when
changing a key it left at its default. `nitpick explain-config` prints what any
of it resolves to without spending a token.

## Two files

Settings you want in every checkout go in a user-level file, which each
repository's own `.nitpick.yaml` then overlays:

```
$XDG_CONFIG_HOME/nitpick/config.yaml     # or ~/.config/nitpick/config.yaml
```

Precedence runs built-in defaults, then that file, then the repository's, then
the environment for what neither said, then flags. It takes the same keys in the
same shape.

It is also the only file trusted with `base_url`, `api_key_env`, `extra`,
`allow_private_endpoint` and `persona.custom`, and it needs no
`NITPICK_TRUST_CONFIG_ENDPOINTS` to use them: you wrote it, and it sits outside
every checkout where no pull request can reach it. A repository that names any
of those keys still has them dropped, and `nitpick explain-config` names both
the file and what it supplied. See [Trust model](trust-model.md).

It is **not read on a runner**, where `CI` or `GITHUB_ACTIONS` is set, because
nobody there wrote it. `NITPICK_USER_CONFIG=/path/to/config.yaml` names one
anyway, wherever you set it; `NITPICK_NO_USER_CONFIG=1` switches it off.

## Findings a coding agent can act on

`review.agent_prompt: true` folds a block under each published finding holding
what an agent needs and a reader does not:

```yaml
review:
  agent_prompt: true
```

```
anchor:    internal/a.go:42-58
also:      internal/a.go:19
class:     correctness
severity:  warning
found by:  openrouter/qwen/qwen3.8-27b
also read: internal/b.go, internal/c.go

Guard removed that a caller depends on

b.go calls this with a nil map on the error path.
```

It is assembled from what the engine already holds, so it costs no model call,
reproduces from the same review, and cannot assert anything the review did not
establish. `also read` is the rest of the batch, which is what the reviewer had
in front of it when it wrote the finding.

That is also its limit. It does not say what would make the fix wrong, because
nothing in a finding records that, and generating it would put an unmeasured
claim beside a measured one.

The block appears on every finding, including one carrying a suggestion GitHub
can apply: the suggestion says what to type, not where else the finding reaches.

## The wider pass

`@open-nitpick improve` reviews the change again for naming, documentation,
structure, idiom and slop: the classes an ordinary review does not ask for.
On an inline thread it covers that file; on the conversation, the whole change.

It answers with one comment listing what it found, not a thread per finding,
and nothing it says has to be resolved or blocks a merge. Findings the pull
request already carries are dropped, so it does not repeat the review.

Reviews are generated at the `normal` scope whatever `persona.nitpick` says,
because asking one pass for defects and style together measured worse at both:
more findings, lower precision, more missed defects. Style therefore comes from
a second pass, which this command runs on request. Nothing about it changes
what a push produces, and it does not read or write `persona.nitpick`.

It is the widest call pattern here, a second generation pass over every batch,
so `review.budget.max_spend` bounds it the way it bounds any review, and
`review.respond.max_per_pull_request` counts its answer.

`nitpick improve` runs the same pass locally, on a checkout this tool does not
post to:

```
nitpick improve                    # the working tree
nitpick improve -base main         # a branch against its base
nitpick improve -level normal      # narrower than the default pedantic
nitpick improve -slop=false        # without the slop class
```

`-level` and `-slop` exist only on `improve`. `nitpick review` generates at
`config.GenerationLevel` whatever the configured level says, so naming a level
there would promise something that command cannot honour.

## Applying a finding

`@open-nitpick fix` on a review thread applies that finding; `@open-nitpick fix
all` applies every published finding it can. The result is a draft pull request
against the branch under review, never a write to that branch.

```yaml
models:
  fix:
    model: an-editing-model   # provider inherited from models.default

review:
  respond:
    fix:
      from: [owner, member]   # defaults to owner, member, collaborator
```

!!! warning "A fix pass has no spending ceiling"

    `review.budget.max_spend` bounds a review by dropping the lowest-ranked
    files from a packed plan. A fix pass has neither a plan nor a ranking, so
    it cannot share that machinery, and no ceiling of its own exists yet. What
    bounds a fix today is who may ask for one: the association list, and the
    write permission the forge is asked for directly.

There is no default fix model, deliberately. Every measurement in this
repository scores a reviewer on recall and noise, and neither says whether a
model can produce a change that compiles, so falling back to `models.default`
would ship an unmeasured capability under a measured model's name. Without
`models.fix` the command refuses and says why.

**Nothing is compiled, run, tested, formatted or linted before the pull request
opens.** There is no checkout: the change is written through the forge's data
API. The body says so, and the checks on the fix pull request are the only
verification, which is why the workflow requires the GitHub App token. A pull
request opened with the default `GITHUB_TOKEN` raises no workflow, so it would
arrive with no checks at all while its body says the checks are the
verification.

Four refusals, and they are separate on purpose:

- `review.respond.fix.from` may not contain `none`. Deciding that anyone may
  spend your model credit is a choice; deciding that anyone may write to your
  repository is not one, so it is refused at validation.
- The asker needs write or admin permission, which the forge is asked for
  directly. `COLLABORATOR` covers read-level access, so an association is the
  cheap half of this gate rather than the gate.
- A pull request from a fork is refused before the model call, and again at the
  write. Not in the workflow condition: an `issue_comment` payload carries no
  head repository, so the condition cannot see whether the pull request is a
  fork's.
- Only paths the pull request already changes may be written, and `.github`,
  `.nitpick.yaml` and `.git` are refused whatever the diff says.

Whole files are replaced, so a fix pull request reverts anything pushed after
the revision it was written against. The run refuses when it notices, and the
body names the revision either way.

## When a model cannot answer

`fallback` escalates to a different model rather than retrying the same one:

```yaml
models:
  default:
    provider: openrouter
    model: qwen/qwen3.8-27b
    providers: ["parasail"]
    fallback:
      model: z-ai/glm-5.3-flash
```

One failure triggers it, and it is about the model rather than the transport:
structured output that never parsed, which is what a runaway generation comes
back as once the output cap has cut it mid-JSON. A bare truncation does not: a
first attempt cut at a cap you chose never reaches the re-sampling retry, and
the fallback inherits that same cap, so it would be cut in the same place. Nor
does a timeout, a refused credential or a cancelled context, since a second
model fails those the same way and spends a second budget doing it.

Retrying the same model is the wrong move for the first of those. Runaway
generation is the model looping on the input, not the endpoint truncating
early, so the same weights on another host reproduce it. Shrinking
`token_budget_per_request` is not the lever either: a batch that looped at
37,118 tokens was under a 40,000 budget and far under a 262,144 context.

Escalation is one step. A fallback naming its own fallback is a config error,
so a misconfiguration cannot walk a batch through every model in the file.

The run says when it happened, in the log and in the published review:

```
3 file(s) were reviewed by a fallback model after the primary could not
answer: 2 batch(es) openrouter/qwen/qwen3.8-27b to openrouter/z-ai/glm-5.3-flash.
```

## Credentials

Nothing needs configuring. Store the key once and every checkout finds it:

```bash
nitpick auth set synthetic      # reads the key from standard input
nitpick auth list               # which providers have one, never what it is
nitpick auth delete synthetic
```

That writes to the operating system's keystore under the service
`open-nitpick` and the provider's name, which is where a review looks when no
key is configured anywhere. Reading it from standard input keeps it out of the
shell's history and out of the process table, which is where an environment
variable puts it.

Resolution runs explicit sources before implicit ones:

| Order | Source | On failure |
|---|---|---|
| 1 | `credential_command` | error |
| 2 | `api_key_keyring`, as `service/account` | error |
| 3 | `api_key_env` | error |
| 4 | keystore under `open-nitpick/<provider>` | falls through |
| 5 | the provider's own variable | the provider reports it |

The first three are what you wrote down for this model, so a failure in one is
reported rather than fallen past: naming where your key lives says you do not
want the environment used instead. Step 4 is a convenience, so a miss is
silent, and a machine with no keystore or a Linux session with no D-Bus behaves
as it did before.

`credential_command` is a command and its arguments, never a shell line, so no
character in a config file is interpreted by an expansion. Its standard output
is the key, trimmed; its standard error is used for the error message and never
for the key.

```yaml
# ~/.config/nitpick/config.yaml
models:
  default:
    provider: anthropic
    model: claude-sonnet-4-5
    credential_command:
      ["aws", "secretsmanager", "get-secret-value", "--secret-id", "nitpick/anthropic",
       "--query", "SecretString", "--output", "text"]
```

!!! warning "These three keys are dropped from a repository's config"

    `api_key_keyring`, `credential_command` and `api_key_env` join `base_url`,
    `extra` and `allow_private_endpoint` on the list a repository's own
    `.nitpick.yaml` may not supply. One chooses which stored secret is read and
    another runs a program in the job holding your credentials, and a pull
    request can edit that file. Put them in the user-level config above, which
    no pull request can reach. Dropped keys are named by
    `nitpick explain-config`, never silent. See [Trust model](trust-model.md).

```yaml
models:
  default:
    provider: synthetic
    model: hf:moonshotai/Kimi-K3
    # max_tokens is unset by default: the model's own output maximum applies,
    # and reasoning tokens count against whatever you set here on most providers.
    timeout: 10m                     # default; per model call, not per review

    # The model to escalate to when this one cannot answer: a request cut at
    # the output cap because the model looped, or structured output that never
    # parsed. Tried once; the batch fails if it fails too. It overlays this
    # spec, so naming a model inherits the provider and credential, and the
    # provider pin is dropped because a pin names the upstreams for one model.
    fallback:
      model: z-ai/glm-5.3-flash

    # Where the credential comes from. All three are optional and all three are
    # withheld from a repository's own file; see Credentials below.
    api_key_keyring: open-nitpick/synthetic
    api_key_env: SYNTHETIC_API_KEY
    credential_command: ["op", "read", "op://Private/synthetic/credential"]

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

## Who may make it spend

A comment event runs in the **base** repository, holding the base repository's
secrets, whoever wrote the comment. On a public repository that means every
account on the forge is one `@open-nitpick` away from your model credit, and a cap
on the size of each answer does not bound a total whose multiplier is the
number of strangers.

So the reviewer answers a closed set by default:

```yaml
review:
  respond:
    from: [owner, member, collaborator]   # the default
    max_per_pull_request: 0               # 0 is no cap
```

`contributor` is **not** in the default set. It means "has a merged commit",
which is permanent: one accepted typo fix would buy unlimited calls forever.
Add it if you want that, knowing what it grants.

A comment from outside the set is ignored without a reaction and without a
model call, and the run says who was refused and what the allowed set is. No
reaction is deliberate: acknowledging a mention tells someone probing that
something is listening.

**`max_per_pull_request`** bounds the case the list does not, a person or an
automation inside the set in a loop. The count comes from the answers already
posted on the pull request, so it survives a re-run and needs nothing
persisted. Answers carry their own marker, so published findings and the
summary do not count against it: a review that posted five findings would
otherwise exhaust a cap of five and refuse the first question anybody asked. Where the count cannot be read, the run says the cap is not being
enforced rather than answering as though it were.

**The workflow should gate too.** `nitpick respond` refuses these comments
itself, but only after a runner has started and the repository is checked out.
The shipped `.github/workflows/nitpick-respond.yml` tests
`github.event.comment.author_association` in its `if:`, so a stranger's comment
costs nothing at all. It also groups concurrency per pull request rather than
per comment: a burst of twenty comments then costs two runs instead of twenty,
at the price of dropping the questions cancelled while pending.

**Forked pull requests** are a separate matter and are already handled by the
shipped workflow, which skips them: reviewing one would need the model key
present in a run whose code the contributor controls.

## The walkthrough at the top of a review

```yaml
review:
  summary_style: receipt   # receipt | prose
```

**`receipt`**, the default, is counted from the run. It states how many changed
files were read out of how many there were, the findings by severity, which
analyzers ran, and how many files could not be reviewed.

Files it did not read are reported in two groups, because they are not the same
news. A deletion, a binary, a generated file or a change with no added lines had
nothing a comment could attach to. A file whose contents would not fetch, or one
cut off by `max_file_bytes`, `token_budget_per_request`, `max_files` or the
spending ceiling, is a hole. Pooling them made a routine review read like a
partial one. No model is asked, so
nothing in it can be invented, and two runs over the same change print the same
line.

It says "Nothing found in what was read" rather than that the change is clean,
because those are different claims and the coverage notices below it carry the
difference.

**`prose`** keeps the generated walkthrough. The triage model writes it, and it
is not shown the change: it sees the findings list and the pull request title.
Measured over six fixtures, most of the content words in what it wrote do not
appear in the diff it describes. See
[Findings](findings.md#should-triage-see-the-change).

Under `receipt` the triage prompt does not ask for a walkthrough at all, so the
output tokens are not spent on an answer nothing prints.

`review.summary: false` still suppresses the walkthrough entirely, whichever
style is set.

## What triage may and may not do

Triage merges duplicates across batches, drops findings the reviewer could not
support, and writes the walkthrough. It runs on a cheap model and it sees the
findings, not the code.

Two guards bound it. Neither is on the walkthrough, which is a separate
question tracked in [Findings](findings.md).

**A finding for a path nobody reported is dropped**, always, with a line in the
log. That guard has been there from the start.

**`review.triage_no_new_claims`** closes the gap the path check leaves. A
finding whose path *was* reported can still come back with a rewritten title
and rationale, published under the original reporter's name, because the
attribution is restored a few lines later. Turn this on and the reviewer's own
title, rationale and suggestion are restored over whatever triage returned:

```yaml
review:
  triage_no_new_claims: false   # default
```

Triage may still select, drop, group and re-anchor. It may move a finding up to
ten lines onto the changed code, which is work worth keeping and
what the anchor pass does anyway. Beyond that, or at a line no reviewer
reported, the finding is dropped as a claim about somewhere else.

Identity here is the path and the line, not the title. `Finding.Key()` includes
the normalized title, so a reworded finding does not match its own origin by
key, and matching on it would have missed the exact case this guard exists for.

Off by default until the effect on merged findings is measured: triage
sometimes rewords two findings into one, and this restores the words of
whichever original the survivor sits closest to.

## A spending ceiling

Off by default. Set one and the review stays under it by reviewing fewer files,
not by stopping partway.

```yaml
review:
  budget:
    max_spend: 1.00        # US dollars; 0, the default, is no ceiling
    scope: run             # run | pull_request
    prices:                # required with max_spend, per MILLION tokens
      input: 0.30
      output: 1.20
    completion_ratio: 0.25 # assumed output size as a fraction of the prompt
    overhead: 1.0          # scale the estimate to cover triage and validation
    min_files: 0           # review this many top files even if they do not fit
```

**`scope: pull_request` counts every run on the branch together**, so twenty
pushes cost what one review costs, where `scope: run` lets each push spend the
whole ceiling. The running total is read off the pull request itself: each
review this tool publishes carries the estimate it ran at in a hidden marker,
and a later run sums them and subtracts. That read is made whether or not
`review.incremental` is on, and every way of failing to get an answer bounds the
run as if it were the first and says so in the log. A pull request whose earlier
runs had no ceiling recorded nothing, so a ceiling added part way through starts
from zero. When earlier spend has reduced what is left, the review says which
number it is quoting.

**Rates are yours to supply.** A price is a claim about what a vendor charges
you, on your account, at your tier. The dated table in `internal/evals` is
evidence for a measurement, not a promise about anyone's bill, so a ceiling is
computed only from rates you wrote down. A `max_spend` without them is a
configuration error rather than a ceiling that silently never binds.

**What gets reviewed.** When the whole diff costs more than the ceiling, files
are ranked and the highest-ranked are reviewed until the money runs out. The
ranking is computed from the diff with no model call, and it is a priority
rather than a prediction of where the bug is:

| Signal | Effect |
|---|---|
| Changed lines | Raises the score, logarithmically. A 4,000-line mechanical change does not outrank a 12-line one. |
| Control flow per added line | Raises it. Branches, error paths, concurrency and cancellation. |
| Edits scattered across a file | Raises it. Five separate hunks are harder than one. |
| A new file | Raises it. Nothing has reviewed this code before. |
| A path that looks sensitive | Raises it. Auth, crypto, payment, migration, permission, SQL. |
| A deletion | Halves it. What matters is mostly the callers. |
| A test | Halves it. Worth reviewing, worth reviewing after the code it covers. |
| Prose or data | Halves it. Markdown, YAML, JSON, lockfiles. |

**The estimate errs high.** Output size is not knowable before the model
writes, so `completion_ratio` assumes an answer a quarter the size of the
prompt, roughly four times what a clean review produces. Over-estimating
reviews fewer files than it could have and says so; under-estimating spends
more than you allowed, which is the one direction a ceiling must not fail in.
An ensemble multiplies the estimate by the number of reviewers, and where a
route carries its own ensemble the estimate uses the largest set a batch could
land in, since which batch takes which route is not known until the router has
run.

**What the pull request says.** A trimmed review states the ceiling, both
estimates, and how many files it did not read, with the coverage notices rather
than inside the walkthrough, so turning `review.summary` off does not turn a
trimmed review into a silent one. Every dropped file also appears under *Files
not reviewed* with the ceiling as its reason. Analyzers are unaffected: they run
over the whole change, so an analyzer finding on a dropped file is still real,
and only the model's silence there means nothing.

**`min_files`** reviews that many of the top-ranked files even when the ceiling
does not pay for them, and the run reports that it expects to exceed the
ceiling. Left at zero, a diff whose cheapest file is over the ceiling is
reviewed not at all, and says so.

!!! warning "`scope: pull_request` is not yet cumulative"

    It is accepted and validated, and it currently behaves as `run`: prior
    spend on the same pull request is not recorded anywhere a later run can
    read it. The run logs that it is bounded as if it were the first. Track it
    in [#12](https://github.com/jdziat/open-nitpick/issues/12).

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
model wrote them. The same rules and the same scrub apply to an `@open-nitpick`
answer, which is capped at 120 words.

## The mention

`review.mention` (default `@open-nitpick`) is the handle a pull request comment
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
