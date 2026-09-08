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
