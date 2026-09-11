# Usage

Running the reviewer from a terminal: over uncommitted changes, over a branch,
over a whole repository, from an agent session over MCP, and over prose alone.
[GitHub Actions and other CI](ci.md) is the same tool on a runner.

Start anywhere in it. `nitpick init` comes first here because a repository is
usually set up once, but it is optional: a review runs with no config file at
all as long as `LLM_PROVIDER` and `LLM_MODEL` are set, and every command below
works that way. All of them assume `nitpick` is on your PATH; the
[Quick start](../README.md#quick-start) has the one-line install, and
[Providers and models](providers.md) covers what to set those two variables to.

## Starting a repository off

```bash
nitpick init                            # writes .nitpick.yaml here
nitpick init -provider synthetic -model hf:moonshotai/Kimi-K3
nitpick init -workflow                  # also .github/workflows/nitpick.yml
nitpick init -force                     # overwrite an existing file
```

The file it writes names the model, the analyzers this checkout calls for, and
the settings most worth changing first: the gate, the severity floor, the three
budget bounds, the ignore list, the persona axes, and the analyzer mode. Every
value written is already the one in force except two, so deleting a key changes
nothing and the file can be trimmed to taste. It is not the whole surface:
[Configuration reference](configuration-reference.md) lists every key the
loader accepts.

The two exceptions are the model, which no default supplies and which comes
from `-provider` and `-model` or from `LLM_PROVIDER` and `LLM_MODEL`, and
`linters.enabled`, which is matched to this checkout rather than copied from
the default. A Go-only tree is written `[golangci-lint]` and a Python-only one
`[ruff]`, where the shipped default is both, and under `mode: strict` that list
is the one whose absence fails a run.

The analyzers are the part hardest to get right by hand, and
[Analyzers](analyzers.md) is the catalog they come from. `init` matches this
checkout against the same targets a review detects on, names the ones that ship
enabled in `linters.enabled`, and lists the rest as comments under three
headings: those the catalog runs on its own when installed, those that run only
once you name them, and those that need a configuration from outside the
repository or your word that their code may run. Nothing is enabled that
would fail a run under `mode: strict` on a runner that lacks it.

Nothing is written until the generated file has been parsed, so a first command
cannot leave a repository with a file the loader cannot read. That is a weaker
guarantee than it sounds and the difference is worth stating: with no model
named anywhere the `models` block is written commented out, and a configuration
naming no model is one `nitpick review` and `nitpick explain-config` both
refuse with `models.default: model is required`. `init` says as much on the
line reporting `model: none`, and the file it wrote is not yet runnable.

## Locally

```bash
nitpick review                          # uncommitted changes
nitpick review -base main               # working tree against main
nitpick review -base main -head feature # a committed branch
nitpick explain-config -path src/db.go  # what would be sent, and why
nitpick providers                       # available model providers
```

Local reviews print to stdout as `path:line`, which most terminals and editors
turn into a clickable link.

## Evaluate repository standards

```bash
nitpick repo-standards                       # measured conventions, proposals, and lint evidence
nitpick repo-standards -json                  # all counts and violation locations
nitpick repo-standards -check                 # CI: 0 passed, 1 violations, 2 incomplete checks
nitpick repo-standards -linters ruff,pylint    # explicitly select analyzers
nitpick repo-standards -no-linters            # convention probes only
```

The default `repo-standards` profile evaluates the current working tree without a model.
Selecting `-profile engineering` enables model assessments and sends selected
source to the configured provider unless `-no-model` is supplied. It reuses
`standards`' evidence thresholds to distinguish established conventions from
proposals, lists the exceptions, and suggests analyzers for the languages it
finds. A linter's silence is not evidence that a particular convention is
universal; the report keeps linter observations separate from probe counts.

Applicable default analyzers run across whole files: golangci-lint for Go,
Ruff for Python, ESLint for JavaScript, PMD for Java, and RuboCop for Ruby.
Select additional tools with `-linters`; `nitpick linters` lists their
configuration requirements. Tools must already be installed.
Analyzers use nitpick's isolated rules, including its Go convention ruleset,
and do not execute repository-supplied linter configurations. The standards
command supplies an ESLint config using built-in rules; ordinary reviews still
require an operator ESLint config. The command does
not install tools or change source, linter configuration, or `AGENTS.md`.

`-check` fails for exceptions to established conventions or any linter
observation. An unavailable analyzer exits 2; an empty or entirely unmeasured
tree also exits 2. With `-no-linters`, only the convention probes gate the run.
Without `-check`, the report is advisory and still discloses unavailable checks.
In the default profile, `-repo` selects a root; `-config` reads only the `standards` thresholds and
disabled probes, leaving model configuration and credentials unused.

This repository runs `go run ./cmd/nitpick repo-standards -check` in CI after
ordinary lint, using the Go toolchain in `go.mod`, golangci-lint pinned in
`tools/go.mod`, and ESLint 10.8.0 for the shipped JavaScript configuration.
Run the same command before pushing.

The probes now measure these additional conventions:

| Language | Probe | Sites counted | Linter rules |
|---|---|---|---|
| Python | `python-function-snake-case` | Named functions and methods, including async and special methods | Ruff N802 |
| Python | `python-class-pascal-case` | Class declarations; leading private underscores allowed | Ruff N801 |
| JavaScript | `javascript-class-pascal-case` | Named class declarations and expressions | ESLint class-name selectors |
| JavaScript | `javascript-strict-equality` | `==`, `!=`, `===`, and `!==` operators | ESLint eqeqeq |
| Java | `java-type-pascal-case` | Classes, interfaces, annotation types, enums, and records | PMD ClassNamingConventions |
| Java | `java-package-lowercase` | Package declarations | PMD PackageCase |
| Ruby | `ruby-method-snake-case` | Explicit method definitions; operator methods excluded | RuboCop Naming/MethodName |
| Ruby | `ruby-type-pascal-case` | Class and module declarations, including qualified names | RuboCop Naming/ClassAndModuleCamelCase |

These are lexical checks, not full language parsers. Comments and literal text
are excluded; JavaScript template and JSX expressions are code. Ruby predicate,
bang, and setter suffixes are allowed. Ruby heredoc bodies are excluded,
including interpolation inside those bodies. `Gemfile`, `Rakefile`, `.gemspec`,
and `.rake` files are recognized as Ruby; `.pyi` is Python, and `.mjs`, `.cjs`,
and `.jsx` are JavaScript. TypeScript has no convention probes yet.

Lexer failures appear in `unmeasured` in JSON, make `-check` exit 2, and prevent
`agents` from writing an incomplete measurement. Java Unicode escapes and
multiple heredoc openers on one Ruby line are not supported by these probes.
Unterminated Ruby `<<` and `<<-` arguments can resemble append expressions and
escape lexical detection; RuboCop reports their syntax errors. Probe-only
measurements do not establish that source files parse successfully.
Linters provide syntax checks and broader rule coverage; their observations
remain separate from the probe denominators.

The evidence is measured in the current tree. For conventions fixed at a base
revision when evaluating a pull request, use `nitpick standards -base main`.

## The conventions this repository demonstrates

```bash
nitpick standards                   # what the tree does, as counts
nitpick standards -base main        # and how this change sits against them
nitpick standards -json
```

No model is called and no credentials are read. Each probe names the sites it
has an opinion about and reports how many conform, so a convention is a count
rather than an assertion, and a rule the code stops following stops being
reported without anybody having to notice.

A rule is only written down at or above `standards.min_share` over
`standards.min_sites` places. Below either it is reported as `contested`, which
is a proposal rather than a standard: a convention half the tree ignores is not
one to measure an author against, and `-base` scores a change against the
standards only.

Those standards are measured at the base revision, over the file list that
revision holds rather than the one on disk. A change cannot supply the
convention it is scored against, and cannot remove the evidence against one by
deleting or renaming the files that carry it. Only lines the change touched are
scored, so code that predates a convention is never counted against whoever
edits near it.

Setting `review.standards: true` hands the same measured conventions to the
reviewer as reference material, beside the retrieved knowledge entries and
after the diff. Only rules that cleared the floor are offered, each with the
share behind it, and each goes to the pass its class belongs to: a style
convention never reaches the defect pass. The prompt says a departure is worth
raising where it costs a reader or breaks something, not on the strength of the
section alone.

It is off by default, needs a local checkout for git to read the base revision
in, and needs a base revision to read. Without either the run says which and
reviews without it, and the report records that rather than leaving an absent
section to be read as a repository with no conventions.

A language no probe reads is named in the report rather than left out of it.
Six probes read Go today and nothing reads anything else, so a TypeScript tree
reports that it was not measured instead of reporting that it passed.
A change that only removes code is still reviewed when the diff includes a
surviving line immediately beside the removal. Findings can anchor to that
line: deleting a guard can break code that did not itself change. Whole-file
deletions and removals with no surviving diff context are still skipped;
inline findings currently require a new-file line number.

## The whole repository

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


## From an agent session

```
nitpick mcp
```

Serves the review engine to an agent session over the Model Context
Protocol on stdio, and logs its progress to stderr as the command line
does (each batch as it starts and returns, with elapsed time, then triage,
validation and publishing): `review` (a change), `full_review` and `repo_score` (a
tree, with the remediation plan and the coverage notice), `code_smell` and
`ai_slop` (the tree review filtered to those classes), and `explain_config`. Every tool returns text and structured findings with
path, line, severity, class, rationale and suggestion; nothing is
published, and the session decides what to do with what comes back. The
plugin under [plugins/nitpick](https://github.com/jdziat/open-nitpick/tree/main/plugins/nitpick) registers the server and
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

## AI slop only

```bash
nitpick slop                       # the whole tree
nitpick slop docs README.md        # the paths given
nitpick slop -no-model             # the tells only: free, no credentials
nitpick slop -json                 # every tell and finding, for a script
```

Two instruments, each scored per thousand lines, with the fixes ordered by how
much each would remove. The tells need no model and run first.

| tell | what it catches |
|---|---|
| `em-dash`, `en-dash-separator` | a dash doing a comma's work |
| `arrow-in-prose` | `->` or an arrow standing in for a word |
| `filler-qualifier` | `genuinely`, `honestly`, `actually`, `simply` |
| `chat-prose` | a comment written as a chat reply |
| `restating-comment` | a comment that repeats the line below it |
| `oversized-doc-comment` | a doc comment longer than what it documents |
| `triplet-rhythm` | three adjectives of praise in a row |
| `antithesis` | "not a nicety, it is a correctness matter" |
| `shouting-emphasis` | capitals doing a sentence's work: NOT, MUST, WHOLE |
| `changelog-comment` | a comment narrating what the code used to do |
| `prose-cadence` | a file written in one rhythm, measured over the file |

Prose files are scanned whole. Source files are scanned in their comments only,
so a string literal is never a tell. Then,
unless `-no-model`, the model's nine slop rules run through the review
engine over the same paths, filtered to the slop class, with the
suggestions the model gave; findings it made outside that class are
listed rather than dropped, since they were paid for. `-fail-over N`
exits 1 when the tells exceed N per thousand lines, for a CI gate on
prose. The MCP tool `ai_slop` returns the same result, with `no_model`
for the free pass.

## The slop class

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


## Engineering practice coverage

The opt-in engineering profile combines convention checks, analyzers, commit
subjects, deterministic prose tells, and a shared model assessment of slop and
design mechanisms:

```sh
nitpick repo-standards -profile engineering -base main -check
nitpick repo-standards -profile engineering -base main -check -json
nitpick review -profile engineering -base main
nitpick commits -base main -check
```

`repo-standards` examines the selected working tree; `review` examines changed
files with available related context. `-base` also selects the accepted repository
policy and the commit range. An external `-config` file is operator policy.
Repository configuration edits cannot weaken their own checks.

Each check reports what it planned, examined and omitted. Exit 0 means selected
policy was satisfied, 1 means blocking findings, and 2 means incomplete required
coverage or no substantive assessment. Incompleteness takes precedence and keeps
the findings. A valid empty commit range is inapplicable; `commits -check` alone
returns 2 because it assessed no commits. `-title` adds a separate intended squash
title check, without claiming that the source commits conformed.

The engineering tree scan runs models. `-no-model` provides
deterministic results but leaves required slop and design assessments unavailable.
`-budget` limits estimated source tokens for the tree model pass; omitted targets
remain visible. It is not a monetary spending limit.

The initial policy requires completion of conventions, linters, commits, slop
tells, slop assessment and design assessment. Commit and analyzer violations
block by default. Model findings and prose tells begin advisory. Accepted policy
can require specific convention rules and prose tells independently of their
frequency:

```yaml
practices:
  profile: engineering
  required: [conventions, linters, commits, slop-tells, slop, design]
  required_conventions: [go-doc-comment-name]
  slop_rules: [chat-prose]
  boundaries:
    - from: example.com/app/api
      forbid: [example.com/app/db]
      reason: API handlers use the service contract.
```

Go import boundaries inspect direct imports in every selected Go source, including
files behind build constraints. They use full module import paths and `path.Match`
patterns: `*` does not cross `/`, and an exact package does not imply its subpackages.
Exclude scratch sources through accepted ignore policy when they are outside the
intended boundary. Configured
boundaries require a completed check. Missing or malformed module metadata is
reported rather than treated as an empty import graph. The inventory distinguishes
available local packages, unresolved local imports and external dependencies.

Design coverage means completion of declared source review tasks over the context
provided. Tasks currently follow individual source files and their available related
definitions; they are not a package-wide lifecycle or migration planner. It does not certify an architecture. The inventory currently supports
Go imports; it does not resolve dynamic calls or external implementations. The
shared model pass assesses lifecycle, contracts, failure handling, duplication
and tests, retaining the finding's original class. A completed pass can miss a
defect. Separate security scans and executed test/build evidence are not claimed
by this profile. The published controls exercise lifecycle and lost-failure cases;
duplication, indirection, migration compatibility and ineffective-test prompts do
not yet have equivalent held-out controls.

Exceptions require an accepted rule, exact target, evidence fingerprint and
reason, with optional expiry. Expiry takes effect at 00:00 UTC on the named
date. They remain visible in the report. File evidence
changes invalidate the fingerprint; exceptions cannot attest that a skipped
instrument ran. AI slop findings describe defects in code or prose and never
classify authorship.

### This repository's engineering policy

Open-nitpick requires its convention, analyzer, commit, and deterministic slop
checks to complete. Its six established Go conventions remain required even
if later violations would lower them below the inference threshold. Direct
imports of `os/exec` and `net/http` are forbidden in the commit validator and
convention probes; this is a source import constraint, not a sandbox guarantee.

CI reports deterministic engineering coverage over the accepted commit range;
the existing standards and commit jobs remain gates. The commit job validates
both branch subjects and the proposed squash title, and reruns when the PR title
is edited without rerunning the build and test workflows.
Pull request reviews build the current source and select the engineering profile
from accepted policy. Model slop/design checks run but are optional and advisory:
provider failures stay visible, and a passing policy gate does not mean these
assessments completed. The paired controls in `docs/findings.md` do not justify
blocking merges on model judgments. Deterministic prose tells also remain
advisory while their existing findings are assessed.
