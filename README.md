# open-nitpick

Self-hosted, model-agnostic pull request review.

Documentation: <https://jdziat.github.io/open-nitpick/>

open-nitpick reads a pull request, reviews it with **a model you choose**, and
posts inline comments. It runs as a GitHub Action, as a CLI in any CI system, or
against your uncommitted working tree before you even open a pull request.

There is no hosted service, no per-seat pricing, and no vendor holding your
source. You bring the model (including a local one), and the review behavior is
driven by configuration and prompts you can read and change.

```bash
go install github.com/jdziat/open-nitpick/cmd/nitpick@latest

export LLM_PROVIDER=synthetic LLM_MODEL=hf:moonshotai/Kimi-K3
export SYNTHETIC_API_KEY=syn_...

nitpick review          # reviews your uncommitted changes
```

The quickstart uses [Synthetic](https://synthetic.new/?referral=KBc4DHaHWcig6zR),
the recommended route: open-weight models on a flat subscription ($30 a month
for one pack, as read from their pricing page on 2026-09-05), so a review
costs nothing per token and a busy day of pull requests does not turn into a
bill. That link carries the author's referral code, and the author receives
referral credit if you sign up through it; the plain <https://synthetic.new>
is the same service at the same price. To spend nothing at all first,
`nitpick explain-config` prints what a review would send without sending it,
and `LLM_PROVIDER=ollama` runs against a local model. OpenRouter, any
OpenAI-compatible endpoint and local models are all one config line away:
see [Configuration](#configuration).

## Why this exists

Most review bots are a hosted service wrapping one vendor's model, with a prompt
you cannot see and pricing per seat. open-nitpick inverts that:

- **Any model.** 16 providers via [llm-go-sdk][sdk], plus built-in
  `synthetic` (open-weight models on a subscription) and `openrouter` (the rest
  of the catalogue on one key) for 18 in all, plus any OpenAI-compatible
  endpoint through `base_url`, plus local models via `ollama` and `llamacpp`.
  `nitpick providers` prints the list.
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

### The whole repository

```bash
nitpick full-review                     # every file git knows about, the model on every batch
nitpick full-review internal/vcs cmd    # only these paths
nitpick full-review -budget 200000      # stop after ~200k tokens of source, and say what was left
nitpick repo-score                      # the same, plus slop, bug and security findings per thousand lines
```

`full-review` reads the working tree as one change that adds every file, so
the same engine, analyzers and prompts that review a pull request review a
repository: bugs, security findings, and whatever the installed analyzers
report (`osv-scanner` on lockfiles gives the known advisories). Both
directions of related context are on, since every file is already in the
review. The output is the review, then a remediation plan ordered most
severe first with findings that share a fix grouped and the files each
touches counted, then what was not covered: files past the budget, and
files left out for being binary, empty or oversized. The default is the
whole tree; `-budget` is for a first look, and its report says so. A tree
review also asks for the slop class, described below.

`repo-score` adds three numbers per language, each with its count and its
denominator beside it so none is read alone: slop, bug and security
findings weighted by severity (critical 8, error 4, warning 2, info 1, nit
0.5) per thousand lines reviewed. Known advisories are listed, not scored.
A threshold on the slop rate names when a repository reads as generated
and left unread; the fixture that set it is in `internal/evals`.

### Which model wrote it

```bash
nitpick identify-model src/client.ts
```

Names the model whose style a file is most similar to, from a corpus of
the same twelve programs written twice by six models, plus a human
control from the Go and Python standard libraries. The instrument is a
fingerprint (character 3-grams and token bigrams, the standard tools of
authorship attribution); it cleared the bar set before the experiment in
[docs/findings.md](docs/findings.md#which-model-wrote-it-2026-09-05-evening)
for Go, Python and TypeScript, on two task splits and across a second
generation of the corpus. Below a confidence floor it answers "unknown".
A match is a style similarity against a small corpus of six models, not
an attribution: a model outside the corpus, or a file a person has
edited, is reported as whichever of the six it is nearest to.

### From an agent session

```
nitpick mcp
```

Serves the review engine to an agent session over the Model Context
Protocol on stdio, and logs its progress to stderr as the command line
does (each batch as it starts and returns, with elapsed time, then triage,
validation and publishing): `review` (a change), `full_review` and `repo_score` (a
tree, with the remediation plan and the coverage notice), `code_smell` and
`ai_slop` (the tree review filtered to those classes), `identify_model`,
and `explain_config`. Every tool returns text and structured findings with
path, line, severity, class, rationale and suggestion; nothing is
published, and the session decides what to do with what comes back. The
plugin under [plugins/nitpick](plugins/nitpick) registers the server and
adds skills that say when to reach for each tool and how to read the
answer:

```
/plugin marketplace add jdziat/open-nitpick
/plugin install nitpick@open-nitpick
```

Or, without the skills, register the server with the client you use:

```bash
nitpick mcp install claude-code        # .mcp.json at the repository root
nitpick mcp install cursor             # .cursor/mcp.json; -user for ~/.cursor/mcp.json
nitpick mcp install opencode           # opencode.json; -user for ~/.config/opencode/
nitpick mcp install codex -user        # ~/.codex/config.toml, appended to
nitpick mcp install claude-desktop -user
nitpick mcp clients                    # the list: claude-code, claude-desktop, cursor, windsurf, vscode, opencode, gemini-cli, codex
```

Each writes the server into the client's own file, keeping what else is
there; `-print` shows the result without writing it. A project file names
the command as `nitpick`, for a teammate's PATH; a user file names this
binary's absolute path. What every one of them writes is the same server:

```json
{"mcpServers": {"nitpick": {"command": "nitpick", "args": ["mcp"]}}}
```

The binary needs a model configured as for the command line (`.nitpick.yaml`
in the repository, or `LLM_PROVIDER` and `LLM_MODEL` in the environment);
a tree tool with no `paths` reviews the whole tree with the model on every
batch, and the skills say to name paths or a budget.

### The slop class

`review.slop: true` asks the model for, and publishes, findings in a tenth
class, `slop`: code that reads as generated and left unread, where the cost
to the next reader can be named. It is nine rules a reader can check on the
line, each with the lookalike it excludes: a comment that restates the line
below it; a comment describing behaviour the code lacks; dead code beside
its replacement; a check against a condition the types exclude; an error
swallowed and carried on from; generic naming where the file has a specific
word; boilerplate repeated three times where the language has the
abstraction; prose that addresses the reader as a chat reply; a test that
asserts nothing. The rules are the prompt layer `prompt.SlopGuidance`, and
`nitpick explain-config` prints them when the switch is on.

It is off by default in a review, whatever the nitpick level, and on in
`full-review` and `repo-score`. Its corpus (`make eval-slop`) is five
planted/control pairs, and the number that matters is silence on the
controls: a human-written file with a plain comment is what the class is
measured against.

### GitHub Actions

```yaml
name: Review
on:
  pull_request:
    types: [opened, synchronize, reopened, ready_for_review]

permissions:
  contents: read
  pull-requests: write

concurrency:
  # A new push supersedes an in-flight review of the same pull request.
  group: nitpick-${{ github.event.pull_request.number }}
  cancel-in-progress: true

jobs:
  review:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0        # the reviewer needs history to diff against base
      - uses: jdziat/open-nitpick@v1
        with:
          provider: synthetic
          model: hf:moonshotai/Kimi-K3
          api-key: ${{ secrets.SYNTHETIC_API_KEY }}
          # Omit fail-on (or set it to none) until you have seen how the model
          # behaves on your codebase. A reviewer that blocks merges on its first
          # false positive is a reviewer the team switches off.
          fail-on: none
          skip-drafts: true
```

**What a run does.** The release binary for the runner is downloaded and its
checksum verified; when no release matches `version`, or the repository is a
private copy whose releases cannot be fetched, the binary is built from the
Action's own checkout. The pull request is reviewed and the review is posted as
one GitHub review with inline comments. The same review (walkthrough,
findings table, analyzer roster) is written to the job summary, and the
step sets outputs a later step can read:

| output | value |
|---|---|
| `result` | `clean`, `findings` (the gate was tripped), `skipped` (a draft), or `error` (the review could not run) |
| `findings`, `critical`, `error`, `warning` | counts of what was published |
| `files` | files reviewed |
| `withheld` | findings an earlier review had already posted |

Exit codes: `0` clean, `1` findings at or above `fail_on`, `2` the review could
not run. CI can tell "this change has problems" apart from "the reviewer
broke", and `result` says which without parsing the log.

**Analyzers on the runner.** A stock runner has none of the deterministic
analyzers installed, so the roster says "not on PATH" for every one. Set
`analyzers: auto` and the Action installs and caches, at whatever version each
tool's own installer serves that day, the analyzers for the languages the
change touches: golangci-lint for Go, ruff
and pylint for Python, shellcheck, hadolint, yamllint, actionlint and zizmor,
tflint and checkov, sqlfluff, rubocop and brakeman, cppcheck, biome, buf, and
gitleaks always. A comma-separated list installs exactly those. The installs
are not pinned: they run `go install ...@latest`, package-manager installs,
"latest" release downloads, and for actionlint, tflint and dotenv-linter the
vendor's install script fetched from its default branch, inside a job that
holds the model key and a write-scoped `GITHUB_TOKEN`. That is the same
exposure as any workflow that installs tools from upstream, and it is the
reason the roster names the tool and version it ran; pin by preinstalling
the tools you trust and listing them, or leave `analyzers` unset. This is what
the hosted reviewers do implicitly; here it is a line in the workflow.

**Try it first.** `dry-run: true` prints the review to the log and the job
summary and posts nothing. `nitpick explain-config` shows the prompt a path
would get before a token is spent.

**Events.** On `pull_request` the pull request is found from the environment.
On `workflow_dispatch` or `issue_comment`, set `pr-number`. On `push` there is
no pull request: the pushed range is reviewed and printed to the log and the
summary, nothing is posted, and `fail-on` still gates the job; `fetch-depth: 0`
is required so the range is in the checkout. Any other event fails with a
message rather than reviewing an empty tree.

**Forks.** On a `pull_request` from a fork the default `GITHUB_TOKEN` is
read-only, so the review cannot be posted. It is not lost: the run prints it
to the log, writes it to the job summary, gates on it, and emits a warning
annotation saying why it was not posted. Do **not** switch to
`pull_request_target` to get write access: that runs the workflow with your
secrets against the fork's code, and this tool's own trust model is not a
substitute for that mistake.

**Incremental review.** A push to a pull request this tool has already
reviewed is reviewed incrementally: only the files changed since the last
review are read, and a finding an earlier run already posted is withheld
rather than posted again. The review says which files it read and how many
findings it withheld. A force push that makes the earlier revision
unreachable reviews the whole change again. `review.incremental: false`
reviews the whole change on every push.

How a finding is recognised as already posted: every comment carries a
fingerprint of its path, class and title, and a review carries the revision it
read. A finding matches an earlier comment when the fingerprints agree
(which survives the line moving), or when the class and file agree and the line is
within two of where the forge now shows the earlier comment, which survives a
rewording. Deleting the bot's comment is how a reviewer says "do not post this
again"; it will not come back.

**Pinning.** `@v1` follows the latest 1.x release of the Action; the binary it
installs follows `version`, which defaults to the latest release. Pin both to
a tag for a build that never changes under you. GitHub Enterprise Server is
supported: the API URL comes from the runner; releases are fetched from
github.com.

### Any other CI

```bash
nitpick review -owner acme -repo-name widgets -pr 42
```

with `GITHUB_TOKEN` in the environment. Inside GitHub Actions the repository and
pull request number are detected automatically.

## Configuration

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

### Analyzers

Thirty-three deterministic analyzers, covering the languages the hosted reviewers
list. Two are enabled by name out of the box; twenty-four more run whenever they
are installed and the change contains files they read; the remaining seven run
only when named, and most of those also need a configuration or an explicit
grant. `nitpick linters` prints this table from
the binary.

| analyzer | covers | runs | configuration |
|---|---|---|---|
| golangci-lint | Go | enabled | open-nitpick's own config; `golangci_config` overrides |
| ruff | Python | enabled | `--isolated`; `ruff_config` overrides |
| pylint | Python (errors and warnings only) | auto | shipped rcfile |
| brakeman | Ruby on Rails (security) | auto | shipped config and an empty ignore file |
| shellcheck | shell | auto | `--norc` |
| hadolint | Dockerfile | auto | shipped config |
| yamllint | YAML | auto | shipped config |
| actionlint | GitHub Actions | auto | shipped config |
| zizmor | GitHub Actions (security) | auto | shipped config |
| gitleaks | secrets in any file | auto | shipped config; secrets are redacted from the finding |
| cppcheck | C, C++ | auto | none; inline suppressions not honoured |
| luacheck | Lua | auto | `--no-config` |
| dotenv-linter | .env files | auto | none |
| checkmake | Makefile | auto | shipped config |
| sqlfluff | SQL | auto | shipped config (ANSI); set your dialect via `configs` |
| biome | JS, TS, JSON, CSS | auto | shipped config |
| oxlint | JS, TS | auto | shipped config |
| htmlhint | HTML | auto | shipped config |
| rubocop | Ruby | auto | shipped config |
| detekt | Kotlin | auto | shipped config on the tool's defaults |
| swiftlint | Swift | auto | shipped config |
| pmd | Java | auto | the bundled quickstart ruleset |
| checkov | Terraform, Kubernetes, Helm, Compose, Dockerfile, ARM, Bicep | auto | shipped config, no downloads |
| tflint | Terraform | auto | shipped config, bundled ruleset |
| buf | Protocol Buffers | auto | shipped config |
| psscriptanalyzer | PowerShell | auto | none |
| eslint | JS, TS | opt-in | `eslint_config` required |
| semgrep | any | opt-in | `semgrep_config` required |
| stylelint | CSS, SCSS, Less | opt-in | `configs.stylelint` required; a config may be code |
| markdownlint | Markdown | opt-in | shipped config; noisy, so not auto |
| osv-scanner | lockfiles | opt-in | shipped config; queries osv.dev, so not auto; `full-review` and `repo-score` name it |
| phpstan | PHP | opt-in | `configs.phpstan` and `trusted`: loads the project's autoloader |
| clippy | Rust | opt-in | `trusted`: cargo runs build scripts and proc macros |

The rules are the ones the first four already follow. A binary is resolved
from `PATH` and refused inside the repository. Configuration is the shipped
file, written outside the repository, or the operator's `configs.<name>`,
which must resolve outside it; a tool with neither useful default nor
operator config does not run and says so. A tool that has to execute the
tree's code to analyze it is refused until named in `trusted`, because no
configuration outside the tree makes that safe on a stranger's pull request.
In-source suppression (`# noqa`, `// NOLINT`, `# rubocop:disable`) is not
closed for any of them.

**Auto-detected analyzers make no roster entry unless they run or fail.** A
tool that is not installed, or has nothing to read, is not listed, so the
block on the pull request stays short enough to be read. Naming a tool in
`enabled` is how to be told when it is missing, and how to make strict mode
fail on it.

Not covered: Vale and LanguageTool (prose), Presidio (PII), Verilator, Fortran,
Rego, Smarty, Shopify themes, Ember templates, Windows batch, CircleCI and
oasdiff.

**Analyzers do not read configuration from the branch under review.**
golangci-lint runs isolated under a config open-nitpick ships and ruff runs
`--isolated`; eslint and semgrep do not run at all until you point them at a
configuration you control, which is also why they are not in the default
`enabled` list. Adding them there without setting their config is a mistake
`mode: strict` will fail on, and that is the point; having them enabled by
default only meant strict failed on every review. The reason for all of it is the
one this project already applies to `.nitpick.yaml`: a change may not
supply the policy it is reviewed under, and analyzer configuration is policy. A
pull request adding a `.golangci.yml` with `linters: {default: none}` was
switching off the entire deterministic half of its own review, and the run
reported success.

**Policy is not a list of filenames, and drawing the line around config files
left the property open.** Policy is anything in the tree that decides what the
review reports. `--no-config` removed the attacker's configuration and our own
ability to set defaults in the same stroke, which left golangci-lint's stock
defaults in charge, and those read the tree. Measured against golangci-lint
2.8.0:

- `// Code generated by protoc-gen-go. DO NOT EDIT.` on line 1 of the file under
  review skipped the whole file, because `linters.exclusions.generated` defaults
  to `lax`. Zero findings, exit 0, nothing in `Report.Error`, and a roster line
  saying the analyzer ran, byte-identical to a clean review, under `mode:
  strict` as well as `auto`. There is no command-line flag for it; `disable` can
  only be said in a config file.
- `max-same-issues` defaults to 3 and `max-issues-per-linter` to 50. Eight
  identical `errcheck` violations arrived as three, and nothing in the JSON said
  five had been cut.
- `uniq-by-line` defaults to true, so one issue survives per line. A
  two-statement function reported four issues with it off and two with it on.

So open-nitpick ships its own golangci-lint config, embedded in the binary and
written to a temporary directory **outside** the repository under review. It sets
`exclusions.generated: disable`, and if it cannot be written outside the
repository the analyzer does not run and says so; it never falls back to the
defaults it exists to replace. The other three are passed as command-line flags
(`--max-same-issues 0`, `--max-issues-per-linter 0`, `--uniq-by-line=false`,
alongside `--path-mode abs`), which means **they apply to your `golangci_config`
too**. That is deliberately overriding you, and the reason is that none of the
four decides which rules run: they decide how much of the analyzer's own output
survives to be gated, and a finding golangci-lint dropped is one this review
cannot tell you about. Your `min_severity`, your nitpick level and your own
exclusion rules all still narrow the result, and all of them are visible.

Setting `golangci_config` replaces our file wholly: golangci-lint reads one
config, not two, so `exclusions.generated` becomes yours again. Set it to
`disable` unless you want a generated-file header in the diff to skip the file.

The cost is real and it applies to every pull request, not only the ones that
edit these files: **your `.golangci.yml` and your `[tool.ruff]` settings do not
apply.** Your enabled linter set, your exclusions, your `per-file-ignores`:
none of it. Isolated ruff in particular reports things your configuration was
suppressing, and so does golangci-lint with `exclusions.generated: disable`: a
repository that commits generated Go gets linted on it. Expect both to be
noisier rather than quieter. `golangci_config` and `ruff_config` are the way
back, and the file has to live outside the repository: a path your CI provisions,
a mounted config, a config repo checked out beside this one.

eslint's cost is not a flag, which is why it is off rather than isolated. An
eslint config is JavaScript that eslint loads and *executes*, so a config inside
the tree can be rewritten by the pull request being reviewed: arbitrary code in
CI with `GITHUB_TOKEN` and your model API key in the environment. But a config
outside the tree cannot resolve its own plugin imports, because Node resolves
them relative to the config file's own directory. Enabling eslint therefore means
provisioning a config *directory* with its own `node_modules` holding every
plugin, parser and shared config it imports, not just a file. And an external
config that imports back into the repository (`import "./eslint-rules/index.js"`)
re-opens the hole completely: the config is a loader, and substituting the loader
does not substitute what it loads.

semgrep is off for a different reason: it has no default rule set, so with no
`--config` it analyzes nothing. Point `semgrep_config` at a rule file outside the
repository, or at a registry reference (`p/...`, `r/...`), a fetch you asked for
by name. It previously ran with `--config auto`, which semgrep refuses whenever
metrics are off; that invocation had never produced a single finding, and nothing
said so.

Every review says what every analyzer did: `ran` under `isolated:
open-nitpick's own analyzer config` or `operator config <path>`, `did not run:
<reason>`, or `skipped` because the change contained no files it reads. It is
published **on the pull request**, in the summary comment beside the notice about
a substituted `.nitpick.yaml`, and repeated on stderr; the counts are in the
collapsed heading, so a reviewer who never opens the block still sees that
something did not run. The reason is that a review which quietly ran less than
you think looks exactly like a clean one, and `skipped` is separated from `did
not run` so that the line which means something is not buried among three that
never do.

`did not run` is not only about missing binaries. golangci-lint reports a failure
to load your packages *inside the same JSON it reports issues in*, and a pull
request can trigger one from the tree: a `go.work` that does not list the module,
a `//go:build ignore` on the file it wants unread. Those runs are reported as
failures, not as zero findings.

**What `mode: strict` does, exactly.** It makes an analyzer that was enabled and
applicable but did not run an *error from the analyzer set*, which is logged and
published on the pull request as `did not run: <reason>`. It does **not** change
the run's exit status: that is decided by `review.fail_on` against the findings
that were published, and an analyzer which produced no findings because it never
ran contributes nothing to it. This file used to say strict "fails the review",
which is what a reader would take to mean the job goes red. If you need a missing
analyzer to break the build, gate on the roster yourself for now.

**And every review says how many analyzer findings it discarded**, in a second
collapsed block beside the roster. An analyzer reports on whole packages while a
review comments on a diff, so findings are routinely dropped: for a file the
change does not touch, for a line it does not touch (`only_changed_lines`), or
for a line the diff does not carry at all. Those three are counted, because a
per-finding list of them is a wall of text nobody opens twice.

The fourth reason is counted *and* named individually, because it is not policy:
a finding reported **for a path that is not in this checkout**. Nothing in a
healthy tree produces one. It used to be dropped by a bare `continue` (no
counter, no log, no status), and that single line absorbed two real defects. One
was ours: with `golangci_config` set, golangci-lint's `relative-path-mode`
defaults to the *config file's* directory, so every finding arrived as
`../repo/app.go` and the opt-in path published nothing at all while reporting
that it ran. `--path-mode abs` fixes that one. The other is described in the
security section below.

The count covers every analyzer finding removed by *anchoring*: the analyzer
set's normalization and both of the engine's anchor passes. It was the first of
those alone until the anchor pass was found dropping analyzer findings at debug
level after the number had already been computed. It is still not "every analyzer
finding that did not reach the pull request": triage sits between the two anchor
passes and may merge one finding into another or drop it as noise, which is the
job it is there to do, and nothing enumerates those.

**And every review says which parts of the change the analyzers did not fully
cover**, in a third collapsed block: *Analyzed less than it ran over*.
`golangci-lint — ran` is true and gets read as "the Go analyzer looked at this
change", which is a different claim. Six things break it, each described in the
security section: a changed Go file the build excludes, a `//nolint` this change
added, a changed `.go` file with no `go.mod` above it, a file importing `"C"`
while cgo is off, a changed `.go` file this review's own ignore list withheld
from every analyzer, and a module whose `go` directive is below the toolchain
analyzing it, which switches off every check gated on a later version, most
visibly the standard library's deprecations. Files are named individually, with
a line where one decided the gap, because a count would leave nobody able to go
and look; individually up to twenty per reason, after which the rest are counted,
because `go mod vendor` is hundreds of files and a body the forge rejects is
worse than a shorter list. The last of the six is the only one that means
*reduced* coverage rather than none, which is why the block says "did not fully
cover" rather than "reported nothing about".

The ignore list is also how a change can be reviewed by *nothing*, and that case
does not reach the block above at all: with every changed file set aside there
are no batches to send, so the run ends before a model or an analyzer is asked
anything. One file under `vendor/` is enough, and vendored code is compiled into
your binary. Such a review opens with **Nothing in this change was reviewed**,
counting what was set aside and why, instead of the forge's default "found
nothing to comment on".

Analyzer severities are translated onto the five levels above, and the analyzer's
own word is kept beside the result: `HIGH` and `ERROR` both become error (they are
the same level in semgrep's scale), `MEDIUM` becomes warning, `CRITICAL` becomes
critical, and a word we cannot read (like an unrecognized string in your
`.golangci.yml`) becomes warning, the same as no severity at all.

Whether a deterministic tool should be able to fail your build at
`fail_on: critical` is your decision, not this tool's. `linters.max_severity` is
where you make it: `warning` means nothing reported by an analyzer is published or
gated above warning, however the analyzer rated it and however the reviewing model
re-rates it afterwards. It does not touch the model's own findings.

It has one hole, and you should know it rather than discover it. The ceiling
recognises an analyzer's finding by its attribution. If triage rewrites a finding
far enough that it can no longer be matched back, which happens when an analyzer
message spans several lines, as a semgrep rule with a multi-line `message:` does,
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

`max_severity` was never a defence against silencing, it only reduces, so the
two are complementary and neither substitutes for the other.

Unknown keys are rejected at load time, so a typo fails immediately instead of
being silently ignored.

### Related context

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
[docs/findings.md](docs/findings.md#callers-2026-09-05): a cheap model went
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
corpus, in [docs/findings.md](docs/findings.md#callers-2026-09-05)) is for
the operator to weigh against that. Context is not free either way: the same
definitions that let a model confirm a defect give it more to be confidently
wrong about.

### Personality and how much it nitpicks

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

### Notes addressed to the model family

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
[docs/comparison.md](docs/comparison.md#tuning-for-glm-53-flash-and-qwen38-27b-2026-09-04).

### Severities

`nit` < `info` < `warning` < `error` < `critical`. `fail_on: none` never fails
the build.

### Synthetic (recommended)

[Synthetic](https://synthetic.new/?referral=KBc4DHaHWcig6zR) hosts open-weight
models (Kimi-K3, GLM-5.3-Flash, Qwen3.8-27B and others) behind an
OpenAI-compatible endpoint on a flat subscription rather than per-token
billing: $30 a month for one pack, 500 requests per five hours, one concurrent
request per model, as read from their pricing page on 2026-09-05. Usage-based
billing is offered separately. That fits a reviewer better than metered
pricing does: the cost of a review is zero at the margin, so nothing argues
for reviewing fewer pull requests.

What the measurements say, in full: Kimi-K3 with related context ties the
shipped default, `anthropic/claude-sonnet-4.6`, on recall on both tuned
corpora ([docs/findings.md](docs/findings.md#kimi-k3-and-the-second-half-of-the-multi-file-corpus)),
and was marked down there on one column only, price per review, which is why
it is absent from the twelve-model price table below. A flat subscription
does not charge that column. GLM-5.3-Flash is the triage and iteration model
this repository's own configuration uses, and with Kimi-K3 as the expert pass
over it, noise on the tuning corpus halved at the same recall
([docs/findings.md](docs/findings.md#callers-2026-09-05)). Neither of those is
a claim that Kimi-K3 is the best reviewer measured; `qwen/qwen3.8-27b` and
`openai/gpt-5.6-luna` are, per dollar on metered pricing, and the table says
so.

The link above carries the author's referral code, and the author receives
referral credit if you sign up through it. <https://synthetic.new> without it
is the same service at the same price.

`synthetic` is a provider with a compiled-in endpoint, so a committed config
can name it and nothing else is needed:

```yaml
models:
  default:
    provider: synthetic
    model: hf:moonshotai/Kimi-K3
  triage:
    provider: synthetic
    model: hf:zai-org/GLM-5.3-Flash
    temperature: 0
```

```bash
export SYNTHETIC_API_KEY=syn_...
```

Model ids are Synthetic's `hf:<org>/<name>` form; their `syn:large:text`
aliases work too and follow whatever they currently recommend. `SYNTHETIC_API_KEY`
wins over `LLM_API_KEY` (which is how the GitHub Action's `api-key` input
arrives), and `OPENAI_API_KEY` is not accepted: it is a credential for a
different host. The endpoint is compiled into the binary rather than read from
`base_url`, and that is what lets it be a committed default: `base_url` and
`api_key_env` are stripped from a config the reviewer does not trust (see
[Trust model](#trust-model)), so the same setup written against the `openai`
provider would work only for whoever had exported
`NITPICK_TRUST_CONFIG_ENDPOINTS`.

The eval harness reaches Synthetic with a `synthetic:` prefix on the model id
(`MODELS=synthetic:hf:Qwen/Qwen3.8-27B`), which keeps the same weights on two
hosts as two rows. Cost per review is priced at Synthetic's usage-based rates,
transcribed into `internal/evals/testdata/pricing.yaml` from the vendor's
pricing page; on the subscription tier the column is what the same tokens
would cost when paying per token.

### OpenRouter

`openrouter` reaches the rest of the catalogue (the frontier closed models
among them) on one key. It is a provider in its own right, so it needs a key
and nothing else:

```yaml
models:
  default:
    provider: openrouter
    model: anthropic/claude-sonnet-4.6
```

```bash
export OPENROUTER_API_KEY=sk-or-...
```

Like `synthetic`, its endpoint is compiled into the binary, so a committed
config can name it. The eval harness reaches every model in the sweep through
it. This repository's own policy on OpenRouter is
[.nitpick.openrouter.yaml](https://github.com/jdziat/open-nitpick/blob/main/.nitpick.openrouter.yaml): the same reviewer and
triage models as `.nitpick.yaml` under their OpenRouter ids, with the policy
block kept identical, for `nitpick review -config .nitpick.openrouter.yaml`.

`LLM_API_KEY` is accepted as a fallback, which is how the GitHub Action's
`api-key` input arrives. `OPENROUTER_API_KEY` wins when both are set, so a
generic key exported for some other vendor is never the one sent here.
`OPENAI_API_KEY` is deliberately *not* accepted: it is a credential for a
different host.

What the compiled-in endpoint does **not** buy you: `provider` and `model` still
come from the config file, and for a router the model id chooses which upstream
receives the code. See [Trust model](#trust-model).

### Choosing a model by price

Twelve models were run through the shipped pipeline on all three eval corpora
(tuning, multi-file, info; 38 planted defects) with related context on. The
full table, per-corpus numbers and caveats are in
[docs/comparison.md](docs/comparison.md#twelve-models-three-corpora-the-costperformance-sweep-2026-09-04);
this is the short version. Recall is planted defects located; `$/review` is
the provider-reported spend per pull request on those corpora. Most rows are
a single run, so gaps under about 0.10 are inside the noise.

| tier | model | weighted recall | $/review | trade |
|---|---|---|---|---|
| best value overall | routed: gemma pinned, qwen3.8-27b for security and TypeScript, glm-5.3-flash router, qwen triage (`internal/evals/testdata/routes/routed.yaml`) | 0.81 | $0.005 – $0.011 | qwen's recall and near its noise at a third of the price; three models to configure |
| highest recall | ensemble: gemma pinned + glm-5.3-flash on every batch, qwen triage (`ensemble-cheap.yaml`) | 0.84 | $0.010 – $0.011 | best info-corpus recall measured; noisiest configuration in this table |
| cheapest of all | `google/gemma-4-31b-it` pinned to `deepinfra/turbo` | 0.75 | $0.0003 | needs `providers: [deepinfra/turbo]`; weak on the info corpus; best on multi-file diffs |
| cheapest without a pin | `openai/gpt-5.6-luna` | 0.76 | $0.0005 – $0.0023 | quiet on multi-file diffs (0.04 noise); weak on the info corpus |
| cheapest with no surprises | `z-ai/glm-5.3-flash` | 0.82 | $0.0017 | noisy on multi-file diffs; never lost a review |
| best quality per dollar | `qwen/qwen3.8-27b` | 0.82 | $0.017 | above the default on every corpus with lower noise |
| quietest | `x-ai/grok-4.6` | 0.74 | $0.020 | zero noise on two corpora, pays in recall |
| shipped default | `anthropic/claude-sonnet-4.6` | 0.79 | $0.021 | the only model measured on the held-out corpus |
| frontier | `openai/gpt-5.6-sol` | 0.85 | $0.027 | `z-ai/glm-5.3` edges it on recall at double the noise |
| skip | `qwen/qwen3.8-max`, `deepseek/deepseek-v4-pro-0813` | 0.63 – 0.78 | $0.013 – $0.044 | most expensive, and both dropped reviews |

Incumbent's on-demand price on the same corpora is $0.25 to $0.36 a review.

Every row was measured with `review.related_context: true`, on 2026-09-04,
which is now the default, and without the caller walk, which is not. The
multi-file corpus rerun with the walk on
([docs/findings.md](docs/findings.md#callers-2026-09-05)) cost no more per
review than before, but the sweep itself has not been repeated.

The default stays sonnet-4.6 because the sweep ran on the corpora the prompt
was tuned against; a candidate replaces it only by beating it on the held-out
corpus under the rule in [docs/measurement.md](docs/measurement.md).
`qwen/qwen3.8-27b` and `openai/gpt-5.6-luna` are the two worth that spend.

### Routing batches to different models, and ensembles

A review is a set of batches, and each batch can go to the model that
measured best for what it is. `models.routes` is tried in order; the first
match wins, and a batch no route matches goes to the review model. A match
can name languages (by file extension), a file-count range, and the kinds
of change a router assigned:

```yaml
models:
  default:
    provider: openrouter
    model: google/gemma-4-31b-it
    providers: [deepinfra/turbo]
  triage:
    model: qwen/qwen3.8-27b
  router:
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
  ensemble:
    - model: z-ai/glm-5.3-flash
```

The router is a cheap model that reads each batch's diff once and answers
with kinds from a fixed list: `security`, `concurrency`, `contract`,
`data`, `config`, `logic`, `test`, `docs`. It runs only when a route names
a kind. A router that fails does not fail the batch; the batch is reviewed
unclassified and the report says so.

`models.ensemble` names models that review every batch alongside the
chosen one. Their findings are pooled and the triage pass merges duplicates
and reranks: the same defect from two reviewers is one finding, and their
agreement is a reason to keep its level. A route's own `ensemble` replaces
the global one for the batches it matches; an empty list removes it.

Every model here overlays `default` the way a role does, so a route names
only what differs. A provider pin follows its model: a route that changes
the model starts unpinned unless it sets `providers` itself. The report
records where each batch went (`Report.Routes`), and `nitpick explain-config`
shows the prompt each reviewer gets, including its model-family layer. The
measured configurations are in `internal/evals/testdata/routes/` and their
numbers in [docs/comparison.md](docs/comparison.md).

### Pinning a router to one upstream

OpenRouter serves a model from many upstream providers and picks one per
request. When some of them stall, `providers` names the ones a review may
use, in order, with no fallback beyond them:

```yaml
models:
  default:
    provider: openrouter
    model: google/gemma-4-31b-it
    providers: [deepinfra/turbo]
```

Slugs are OpenRouter's, with an endpoint suffix where one exists. The
setting is only accepted with the `openrouter` provider. It is also a
trust decision: a pull request that edits `.nitpick.yaml` can change it,
which chooses which third party reads the code, exactly as `model` already
can. Rates differ by upstream, so the eval harness prices a pinned run only
when the pin is the endpoint the price table recorded.

### When a request never finishes

A router can hand a request to an upstream that accepts it and never
answers, and a model can generate past any sensible length on one input.
Both look the same from here: the HTTP client's timeout (`timeout`, default
10 minutes) fires while the body is still being read. The client then sends
the request again, up to `max_retries` times (default 3), with an output cap
of 16k tokens on the retries when the config set none. A hung upstream
answers under the cap. A runaway generation comes back cut, and the next
attempt samples at temperature 0.3 instead of zero to break the loop; a
review that took that path is no longer reproducible by re-running it, and
its log says so. Every retry and its outcome is one log line, so a review
that took forty minutes says why. This was built on gemma-4-31b through
OpenRouter, which lost one review in five without it and none with it; the
numbers are in [docs/comparison.md](docs/comparison.md).

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

`base_url` and `api_key_env` are only honored when the config file is trusted
(see [Trust model](#trust-model)), so set `NITPICK_TRUST_CONFIG_ENDPOINTS=1`, or
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
in CI against pull requests, and a pull request can edit any file in the repo,
including `.nitpick.yaml`. Three consequences:

- **`base_url`, `api_key_env`, `extra`, and `allow_private_endpoint` are ignored**
  by default when read from the reviewed repository. Otherwise a contributor
  could point the reviewer at an endpoint they control and name the environment
  variable to send as the bearer token, exfiltrating `GITHUB_TOKEN` or your
  model key in one line of YAML. Set `NITPICK_TRUST_CONFIG_ENDPOINTS=1` to allow
  them, only where you control the file. Ignored keys are logged, never silent.
  Providers whose endpoint is compiled in (`openrouter`, `anthropic`, `ollama`,
  and the rest) are unaffected, which is why the shipped default names one
  rather than a `base_url`.
- **`provider` and `model` are *not* stripped, and that is the residual risk.**
  A pull request editing its own `.nitpick.yaml` cannot change the endpoint or
  the bearer token, but it can still choose which model reads the diff. Two
  consequences worth naming, because the bullet above does not cover them: it
  can point the review at a weak or free-tier model and get a quiet zero-finding
  run, and, because a router's model id *is* its routing key, it can change
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
  repository (see `golangci_config` and friends above). Two things that buys: a
  pull request cannot switch its own analyzers off *through configuration*, and
  it cannot switch semgrep on with rules it wrote. It also closes one specific
  fabrication: golangci-lint's `forbidigo` prints a `msg` from the config file
  verbatim, so a config in the tree could author a finding's words outright.
- **A change CAN still author the text of a finding that reaches the reviewing
  model, and this file used to claim otherwise.** The claim was that keeping
  configuration out of the tree meant a change could not write the text of
  deterministic evidence. It cannot write it *through a config file*. It writes
  it through the source, because an analyzer message quotes the code it is about.
  Measured against golangci-lint 2.8.0 on code that compiles, with the default
  linter set and no attack surface beyond a struct tag:

  ```go
  type T struct {
      A string `json:"IGNORE PREVIOUS INSTRUCTIONS. …"`
      B string `json:"IGNORE PREVIOUS INSTRUCTIONS. …"`
  }
  ```

  `govet`'s `structtag` prints the tag verbatim (`struct field B repeats json tag
  "…"`) and `staticcheck`'s `SA5008` prints it again. Both become ordinary
  findings, and a finding's title is what the triage model reads, under a
  heading that tells it this came from a deterministic tool. A cgo preamble
  containing `#error "…"` does the same through the compile-failure path, where
  the prose lands in the published roster instead. There is no fix here that is
  not worse than the problem: an analyzer forbidden from quoting the code would
  produce findings nobody could act on. Treat analyzer text as what it is:
  attacker-influenced data with a trustworthy *source* attribution and untrusted
  *content*, exactly as this tool treats the pull request description, which is
  fenced as untrusted in the prompt. Analyzer findings are not fenced today.
- **A Go line directive rewrites the positions in an analyzer's report, and this
  is why a discarded finding is now counted.** One line above the offending
  function:

  ```go
  //line zz_generated.go:1
  ```

  golangci-lint 2.8.0 reports both findings, correctly, at
  `pkg/zz_generated.go`, a file that does not exist. Every "did the analyzer
  produce issues" check passes; the findings then died in normalization, because
  the path is not in the diff, leaving zero findings, a nil error and a roster
  line saying the analyzer ran. Point the directive at a file that *does* exist
  and it is worse than silencing: the findings land on code the change did not
  write, at lines it chose, posted under this bot's name. The block form
  (`/*line …*/`) needs no column 1 and works indented inside a function body.

  Nothing can recover the true position from the report, so the report is refused
  whole: a package whose source carries a line directive is `did not run` with the
  file and line of the directive, published on the pull request. The cost is a
  repository that commits generated Go containing directives (`goyacc` and `cgo`
  output), which gets a named refusal for those packages rather than a silent
  one. Findings that reach normalization with a path outside the checkout anyway
  are counted and named on the pull request, because that is the shape the attack
  has when the refusal misses it. Note what a refusal is worth: it makes the
  silencing *visible*, it does not stop it. A change that wants no Go analysis
  can have none, and the roster will say so.

  **That detector was a regular expression, and it missed two spellings the
  toolchain accepts.** CRLF line endings: `go/scanner` strips the trailing `\r`
  from a `//` comment *before* reading it as a directive, to match the compiler
  on files written on Windows, while the pattern anchored on `$`, which in Go's
  regexp matches only before `\n`. git stores CRLF verbatim, so no
  `.gitattributes` is needed. And a `*` in the block form's filename: a block
  comment ends at the *first* `*/`, so `/*line z*z.go:1*/` is one complete
  directive, while the pattern spelled the filename `[^*]*` to stop itself
  running past the terminator. Both were full bypasses: a real report at forged
  positions with the roster saying the analyzer ran. The detector now asks
  `go/scanner`, which is the grammar rather than a list of spellings, and is
  stricter in the other direction too: `//line z.go:1 ` with a trailing space is
  not a directive and the old pattern refused the package for it.
- **Configuration is not the only way the tree can silence an analyzer.** A
  change that breaks golangci-lint's package load (`go.work` omitting the
  module, a build constraint excluding every Go file in the directory, a
  `toolchain` directive it cannot satisfy) stops the Go analyzer as completely
  as any config file would, and none of it goes through one. golangci-lint
  reports those failures inside the same JSON envelope it reports issues in, so
  they were read as zero findings for a while; they are now failures, reported on
  the pull request. Deleting `go.mod` still stops the analyzer too:
  that one is at least visible in the diff, and it is reported as
  `did not run: no go.mod at or above the changed Go files`.

  **That reason only covers the all-or-nothing case, and a monorepo makes the
  partial one ordinary.** golangci-lint has to be run from inside a module, so a
  changed `.go` file with no `go.mod` at or above it is dropped, correctly,
  because there is nowhere to run. The roster line above appears only when
  *every* changed Go file lands there. With `backend/go.mod` present, a change
  touching `backend/app.go` and `tools/evil.go` (an identical unchecked error in
  each) published the backend finding, recorded `golangci-lint — ran`, and said
  nothing whatever about the second file. One finding and an empty coverage list
  reads as a change that was analyzed in full and was clean everywhere except
  that one line. Each such file is now named on the pull request under *Analyzed
  less than it ran over*.
- **A load failure is the loud shape of that; the quiet one needs one extra
  file.** A constraint has to empty the whole *directory* to fail the load. Put
  one unconstrained sibling next to the changed file and the package loads
  perfectly while the changed file is never analyzed. `//go:build windows`,
  `//go:build ignore`, and a plain rename of `app.go` to `app_windows.go` (no
  comment at all) each gave zero findings, exit 0, a nil error and the roster
  line `golangci-lint — ran`, in strict mode as well as auto. The rename is the
  sharp one: it reads as an ordinary platform-specific pull request, the code is
  live on another GOOS, and `go build ./...` and the tests stay green on the CI
  GOOS.

  This is **not** refused, because the report is not corrupt: it is accurate
  about the files it did cover, and a Linux runner reviewing `foo_windows.go` is
  a normal Tuesday. Instead each changed Go file the build excludes is named on
  the pull request under *Analyzed less than it ran over*, so `ran` can no longer
  be read as "the analyzer looked at this change". Exclusion is decided by
  `go/build`'s own matcher under the process's `GOOS`/`GOARCH`, which is the one
  golangci-lint inherits.
- **cgo is the same gap through a door that matcher cannot see, and it is the
  common case rather than an exotic one.** `CGO_ENABLED=0` is the default in most
  Go CI images. With it off, the go tool drops a file importing `"C"` from its
  package while an ordinary sibling keeps the package loading, measured against
  golangci-lint 2.8.0, the unchecked error in that file is reported with cgo on
  and silent with it off, exit 0 and the roster line `ran` either way.
  `go/build`'s matcher cannot answer this, because it reads build constraints and
  the filename and the deciding fact is in the import list: it matches the file
  in *both* states. This was disclosed here and in the code for a while, and a
  disclosure is not the reader of a review seeing it. The file is now read with
  `go/parser` in imports-only mode and named on the pull request like any other
  build exclusion, and only when cgo is off, because naming a file the
  analyzer had just reported on is its own defect.

  **Whether cgo is off is asked of the go tool, not of this process**, and the
  first version got that wrong. `go/build` fills in `CgoEnabled` from the
  environment alone, while `cmd/go` also reads the go env config file, what
  `go env -w CGO_ENABLED=0` writes, and the ordinary way to configure a builder
  image without exporting anything. Measured with `CGO_ENABLED` absent from the
  environment: the key in that file took the child to `CGO_ENABLED=0`, the
  finding vanished, `go/build` still said cgo was on, and the coverage list was
  empty, the same silence, through the config file instead of the variable.
- **`go.mod`'s `go` directive is policy, and it is one line the change can
  edit.** Measured against golangci-lint 2.8.0: with `go 1.24` in `go.mod` the
  review publishes `SA1019: "io/ioutil" has been deprecated`. Change that line to
  `go 1.15` and the run is byte-identical to a clean one: zero findings, roster
  `ran`, empty discard list. staticcheck reports a deprecation only for a module
  declaring the release that issued it or later, and the module's declared
  language version wins over anything the analyzer is configured with: neither
  `run.go` nor `staticcheck.checks: ["all"]` restores the check, and under
  `checks: all` staticcheck demonstrably runs (ST1000 appears) while `SA1019`
  still does not.

  It therefore cannot be closed from the configuration open-nitpick owns, and
  `go.mod` is the tree under review, so raising it is not ours to do either, so
  it is named instead: the module's `go.mod` and the line of the directive, under
  *Analyzed less than it ran over*.

  **The measure is the toolchain that loads the packages, and it used to be a
  constant floor of `go 1.21`.** The floor's argument was volume: every Go
  release deprecates something, so "below the newest" is true of nearly every
  module, and a notice that fires on ordinary code is one reviewers learn to
  collapse. What it bought was silence at the commonest directives. Measured on
  one file using `reflect.PtrTo` (deprecated in 1.22) and `cipher.NewCFBEncrypter`
  (deprecated in 1.24): `go 1.22` publishes the first, `go 1.24` publishes both,
  and `go 1.21` publishes neither while the coverage list stays empty. As an
  attack that is a diff editing `go.mod` from `go 1.25` to `go 1.21` and adding
  the file, and `go 1.21`–`1.23` are ordinary directives in live repositories.
  The sentence that had made the floor look safe was false at the floor: the
  compiler does gate language *features* on this directive: generics under
  `go 1.15` fail with `type parameter requires go1.18 or later`, reported as
  `did not run: the code did not compile`, but at `go 1.21` every feature
  through 1.21 compiles and every deprecation since is off.

  A module is now named whenever it declares less than the toolchain analyzing
  it, which is exactly the set of runs where version-gated checks were narrower
  than this one could apply.

  **What that gives up, measured, because a false coverage gap is as much a
  defect as a missed one.** The gate is staticcheck's own deprecation table, and
  that table lags the toolchain. On golangci-lint 2.8.0 and go1.25.5 over a file
  using `runtime.GOROOT` (deprecated in 1.24) and `ast.NewPackage` (deprecated in
  1.22), `go 1.24` and `go 1.25` publish an identical three findings; `go 1.23`
  publishes two. So a module one release behind is named for a reduction that is
  empty, and one release behind is where most live repositories sit, on a
  `go.mod` the change never touched. That is the floor's own argument about
  volume, pointed back at the ceiling, and it is not answered by saying the entry
  is rare, because it isn't.

  What it is answered by is the entry claiming less. It says the version gate was
  closed, not that anything was behind it, which is true whether or not the table
  has caught up. Moving the ceiling down to the newest version that gates
  something was rejected: that number can only be a constant measured against one
  analyzer release, and when the table moves past it the error turns into
  silence, which is the failure this whole list exists to prevent, and the
  reason the `go 1.21` floor above was removed.
- **Your own ignore list is a silencing channel, and it is the one that is not
  the change's doing.** Changed paths matching `review.ignore` are dropped before
  any analyzer is handed a path, and `**/vendor/**` and `**/testdata/**` are
  shipped defaults. Measured: an identical unchecked error in `app.go` and
  `vendor/token.go` published only `app.go`'s, with the roster saying the
  analyzer ran and an empty coverage list, and vendored code is compiled into
  your binary. Each such file is now named under *Analyzed less than it ran
  over*, but only when no analyzed package covered it anyway: `**/*.gen.go`
  matches the ignore list too, and a `token.gen.go` sitting beside `app.go` is
  analyzed with the rest of its directory and has its findings published, so
  naming it would report a gap that is not there.

  **The case that closes it is the one where no Go file survives at all**, and
  the first version of this fix missed it, because the coverage question was
  asked only of an analyzer that ran. A `go mod vendor` bump touches `go.mod` and
  `vendor/example.com/dep/dep.go`; the ignore list withholds the second, so
  golangci-lint is handed one path it does not read, declines to run, and the
  entire published review was a single line reading *"the change contains no
  files it analyzes"*, over a change containing a Go file with a real unchecked
  error. An analyzer that is handed nothing is now asked what it did not cover
  just as one that ran is, and the line it prints says *no files it analyzes were
  **selected for review***, which is the fact it has.
- **Code that does not compile is the same silencing, and needs no attack at
  all.** Go is analyzed a package at a time, so one file that does not build
  stops every linter for every package in that invocation. golangci-lint reports
  it as an ordinary `typecheck` issue (exit 0, nothing in `Report.Error`), and
  anchored to line 1 of a *different* file, so with `only_changed_lines` it used
  to vanish entirely and the review reported success. The sharpest version is a
  broken `_test.go`, because `go build ./...` stays green while the Go review of
  the rest of the change silently reports nothing. It is now
  `did not run: the code did not compile, so no analyzer ran over it: <file>:<line>`,
  quoting the file that failed rather than the one it was anchored to.
  Note the corollary: an ordinary work-in-progress pull request that does not
  compile gets **no Go analyzer findings at all**, and the roster says so rather
  than implying the code was clean.
- **Published reasons escape HTML, not markdown.** An analyzer's failure reason
  is quoted on the pull request, and it quotes the tree: a Go compile error
  carries source text verbatim, so `var X int = "[CLICK](https://example)"`
  reaches the roster with that string in it. Raw HTML is escaped; markdown link
  syntax is not, so a change can put a live link into a comment posted under this
  bot's name. It reaches no model (the roster is rendered, never prompted), so
  this is a phishing surface in a trusted comment, not an injection into the
  review itself. The same is true of every other untrusted string this tool
  renders, including the substituted-policy notice and the forged paths named in
  the discard block, which are by construction chosen by the change.
- **Containment covers symlinks, not hard links.** An analyzer config path is
  refused if it resolves inside the repository, following symlinks on both sides.
  A hard link (the same file under a second name outside the repository) is
  accepted, because nothing short of walking the whole tree comparing inodes can
  see one. git stores no hard links, so a pull request cannot create this;
  reaching it requires write access outside the repository, which is already a
  larger problem.
- **What that does not close: in-source suppression, and it is not per-line.**
  A pull request can suppress a deterministic finding with `//nolint`, `# noqa`,
  `# nosemgrep` or `eslint-disable`, and golangci-lint offers no way to disable
  its own: the others have `--ignore-noqa`, `--disable-nosem` and
  `--no-inline-config`, golangci-lint has nothing. This file used to describe
  that as a per-line limitation, which understates it by a whole file:
  golangci-lint expands a `//nolint` to the *declaration* it is attached to, and
  attached to the package clause it covers the entire file. Measured, a
  one-line diff whose only addition is `//nolint:all` above `package probe` took
  a file holding two pre-existing `errcheck` violations to zero findings, on
  lines the change never touched, with the roster reporting that the analyzer
  ran. `// nolint` with a space works too; a trailing `//nolint` on the `package`
  line, or one separated from it by a blank line, does not.

  The suppression happens inside golangci-lint, so it can never appear in the
  discard block: nothing was produced to discard. What is now published instead
  is the *directive*: every `//nolint` on a line **this change added** is named,
  with its file and line, under *Analyzed less than it ran over*. Pre-existing
  ones are not, because they are the repository's own policy and listing them on
  every pull request is how a notice gets collapsed and never opened again. The
  detection lexes the file, so a `//nolint` quoted in a string literal or inside
  prose is not reported. Only the Go case is detected today; the other three
  analyzers' inline configuration is still neither disabled nor counted.

Local reviews are confined to the checkout: a committed symlink pointing at
`~/.ssh/id_rsa` will not be read or sent to your model endpoint.

Diff content and pull request text reach the model as explicitly-fenced
untrusted data, and are never rendered as templates.

### Structured output

Findings are constrained to a schema. By default (`structured_output: auto`)
open-nitpick requests a JSON-Schema response format and, if the provider
*rejects* it, falls back to JSON mode with lenient parsing and one bounded
repair attempt, remembering that downgrade so it is paid for once per run
rather than once per request. Force either path with `structured_output: schema`
or `json`.

A provider that *accepts* the schema and then ignores it is handled separately
and deliberately: the response is rejected, the same request is retried on the
JSON path, and the client is **not** downgraded. Routers can hand consecutive
requests to different upstreams, so one unenforced answer is a fact about that
answer, not about the provider, and a downgrade would move every later batch of
the run onto a different strategy with nothing in the report saying so. A
response that satisfies neither path fails the batch loudly and lands in the
run's incomplete list; it is never reported as a clean review.

This is what makes small local models usable: they need the fallback, and
hard-coding the strict path would exclude them.

## How a review runs

```
diff → select and batch files → review each batch → triage → render → publish
```

- **Select** drops ignored, binary, deleted, and generated files. A review that
  quietly skipped half the diff would otherwise look identical to a clean one, so
  the exclusions are reported, with one deliberate exception and two places to
  look. *Files not reviewed* lists them all except the ones matching your ignore
  list, because `go mod vendor` is hundreds of files and those patterns are your
  own. An ignored file is still not silent where it matters: a changed `.go` file
  an analyzer would otherwise have read is named under *Analyzed less than it ran
  over*, and a change where **everything** was set aside opens by saying that
  nothing in it was reviewed.
- **Batch** groups files under a token budget, attaching whole file contents
  where they fit and a window around the changes where they do not.
- **Review** runs batches concurrently. One failed batch is logged and skipped;
  *every* batch failing is an error rather than a "no issues found".
- **Triage** merges duplicates across batches, drops unsupported findings, and
  writes the walkthrough. It may reword and merge, but it cannot invent findings
  for files nobody reported on.
- **Anchor** snaps near-miss line numbers onto real changed lines and drops
  findings that cannot be placed, so comments land where they belong. An
  *analyzer* finding dropped here is counted and published rather than discarded
  quietly; see the discard block above.

## Measurement

This project makes empirical claims about review quality, so how those numbers are
produced is part of the product.

- [docs/measurement.md](docs/measurement.md): what has to hold before a number
  out of the eval harness is worth acting on. Fifteen rules, each written
  because the harness produced a confident wrong number and something believed it.
- [docs/comparison.md](docs/comparison.md): capabilities and measured results
  against Incumbent, by language, with what each iteration changed.
- [docs/remediation.md](docs/remediation.md): every miss on the benchmark
  repository, its cause read from the pull request, and the plan.
- [docs/findings.md](docs/findings.md): what has been measured, what it
  supports, and every instrument bug found so far, in a table whose count is the
  number of rows. Several flattered one side of a comparison; one produced a
  published claim that had to be retracted.

The same documents are published at <https://jdziat.github.io/open-nitpick/>.

The short version: prefer the judge-free columns. The LLM judge runs at
temperature 0 and still scores byte-identical input anywhere from 3.66 to 3.98.

## Development

```bash
go test ./...        # no network or credentials required
go test -race ./...
make quick           # measure a prompt or analyzer change for a few cents (see below)
```

### Commits and releases

Commit subjects follow [Conventional Commits](https://www.conventionalcommits.org/):
`feat(scope): what changed`, `fix: …`, `docs: …`, `evals: …`, `prompt: …`.
CI checks every pull request's commits with `scripts/check-commits.sh`.
On each push to main, release-please keeps one pull request open with the
next version and its changelog; merging it tags the release. The release
workflow then builds the binaries, writes `checksums.txt`, and signs every
asset with Sigstore keyless signing, so a download is checkable against this
repository's workflow identity and nothing else:

```bash
cosign verify-blob --bundle nitpick_v1.2.0_linux_amd64.sigstore.json \
  --certificate-identity-regexp '^https://github.com/jdziat/open-nitpick/' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  nitpick_v1.2.0_linux_amd64
```

The `v1` tag follows every `v1.x.y` release, which is what the Action's
`@v1` pin relies on.

### Iterating cheaply

`make quick` runs the tuning corpus and the multi-file corpus, judge-free,
with related context off and on, against `z-ai/glm-5.3-flash`, about a
thirtieth of the default reviewer's price per review. It is the model to
iterate against, and the triage model this repository's own config uses;
[docs/findings.md](docs/findings.md) records how it compares as a reviewer.
`QUICK=<openrouter id>` swaps it.

### Evaluating the prompts against real models

Unit tests prove the *tooling* is correct given a scripted model. They cannot
tell you whether the *prompts* work: whether a real model, handed a real diff,
finds the bug, anchors it to the right line, and stays quiet about code that is
fine. `make eval` measures that.

```bash
echo 'OPENROUTER_API_KEY=sk-or-...' > .env    # gitignored

make eval                                     # 18-model matrix; see the cost note below
make eval MODELS=openai/gpt-4o-mini           # one model
make eval RUNS=5                              # run-to-run stability
make eval FIXTURES=go-nil-deref               # one fixture
make eval CAPTURE=testdata/responses          # save raw model output
```

It is the same `OPENROUTER_API_KEY` the default config uses, but only the eval
harness reads `.env`; `nitpick review` does not, so export it (`set -a; . ./.env;
set +a`) or keep it in your shell profile.

Each fixture is a synthetic pull request with bugs planted at known lines,
built into a real git repository and reviewed through the real engine, so the
diff parsing, batching, structured output, anchoring, and rendering are all
exercised, not just the prompt.

The report separates three things that are easy to confuse:

- **Invariants**: properties open-nitpick must uphold whatever the model does:
  every severity is a real level, every comment is placeable, and no suggestion
  is published as one-click-applicable unless it is a single line of code. A
  breach is a bug in this repository and fails the run.
- **Recall**: planted defects found. Reported per fixture; a run fails only if
  a model finds *nothing* across the whole corpus, which means the prompt or
  the plumbing is broken rather than merely weak.
- **Noise**: findings explaining no planted defect. Two fixtures
  (`clean-refactor`, `style-only`) contain no bugs at all, so every finding
  there is noise by construction.

**Five corpora.** `make eval` reads the tuning corpus. The held-out corpus is
spent once at the end of a tuning round and is selected only by naming its
fixtures. The multi-file corpus, `make benchmark-multifile`, is fourteen
changes whose defect is only visible by reading a file the change does not
touch; it measures `review.related_context` with the feature off and on,
against every hosted reviewer with a cached or collectable review:
Incumbent's CLI, and Contender's once `contender login` has been run. The
callers corpus (`FIXTURES='$(CALLERS)'`) is six changes where the file the
change breaks is an untouched caller, and the info corpus is the severity band
no reviewer had located. The last three live outside the ground-truth
registries the first two carry, and their numbers should be read with that in
mind; see `EveryFixture` in `internal/evals`.

**Cost.** The default matrix is **18 models x 16 fixtures = 288 reviews**, plus a
judge call each for `make judge-models`. That is not a cheap command. Pass
`MODELS=` to narrow it:

```bash
make eval MODELS=qwen/qwen3.7-flash            # one model, 16 reviews
```

The matrix spans providers and price tiers deliberately, including models with
and without JSON-Schema support so the structured-output fallback is exercised.

`CAPTURE` writes every raw model response to disk. Those become offline
regression fixtures, the cheapest way to keep the extractor honest without
paying for tokens on every test run.

The whole pipeline is tested against a scripted model and a stub GitHub API, so
the test suite exercises real behavior rather than mocks of its own design. Diff
position mapping is additionally cross-checked against real `git diff` output by
a second, independent implementation.

## Status

Usable, measured, and still early. The engine, the GitHub and local providers,
structured output, analyzers, related context in both directions, per-batch
routing, ensembles, and the Action work end to end; a push to a reviewed pull
request is reviewed incrementally. [docs/findings.md](docs/findings.md) is the
record of what has been measured and what it supports. Not yet done: resolving
superseded comments, `@nitpick` command handling, and a GitLab provider.

## License

Apache-2.0.
