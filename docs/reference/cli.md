---
title: CLI reference
description: Every open-nitpick command, its flags, and what it changes.
---

# CLI reference

`nitpick` reviews a Git repository from the directory you run it in. Run
`nitpick <command> -h` for the version installed on your machine; that help is
the source of truth when a newer release adds an option.

Commands that review a change exit non-zero when they find a result at or above
their configured failure level. An operational failure (bad revision, missing
credential, unavailable required tool) is also non-zero. Use `-dry-run` with
`review` or `fast-review` before connecting a run to a pull request.

## Choose a command

| You want to… | Start with |
| --- | --- |
| Review the change you are about to commit | [`review`](#review) |
| Get a quick first pass on a pull request | [`fast-review`](#fast-review) |
| Assess a whole repository or a focused directory | [`full-review`](#full-review) or [`repo-score`](#repo-score) |
| Enforce conventions, commits, security, or prose in CI | [`repo-standards`](#repo-standards), [`commits`](#commits), [`security`](#security), or [`slop`](#slop) |
| Configure a provider, editor integration, or a new repository | [`auth`](#auth), [`mcp`](#mcp), or [`init`](#init) |
| Inspect configuration or generated reference material | [`explain-config`](#explain-config) or [`config-reference`](#config-reference) |

## `review`

```sh
nitpick review -base origin/main
nitpick review -pr 42 -owner acme -repo-name service
```

The normal change review. With no `-base`, it reviews uncommitted changes; with
`-pr`, it fetches the pull request and can publish its findings to GitHub.

| Flag | Meaning |
| --- | --- |
| `-repo` | Repository root (default `.`). |
| `-config` | Configuration file; default is `<repo>/.nitpick.yaml`. |
| `-base`, `-head` | Revisions that bound the diff. `-head` defaults to the working tree. |
| `-profile` | Opt in to an engineering assessment profile. |
| `-instruction` | One extra instruction for this invocation. |
| `-fail-on` | Override `review.fail_on`: `nit`, `info`, `warning`, `error`, `critical`, or `none`. |
| `-owner`, `-repo-name`, `-pr` | GitHub repository and pull request to review. |
| `-dry-run` | Print the review instead of publishing it. |
| `-skip-draft` | Exit without work for a draft pull request. |
| `-no-linters` | Skip configured linters. |
| `-full` | Review the whole change, even when a prior run would narrow it incrementally. |
| `-log-format` | Log format: `text` (default) or `json`. |
| `-v` | Print verbose logs. |

## `fast-review`

```sh
nitpick fast-review -base origin/main -dry-run
```

A bounded first pass for changed code. It reports at most ten findings; it does
not limit the number of files reviewed. It intentionally omits some full-review
stages, so do not use it as an approval decision. It accepts the same flags as
[`review`](#review) except `-profile`: fast review rejects the engineering
assessment profile because that assessment needs the full review path.

## `full-review`

```sh
nitpick full-review
nitpick full-review ./internal/review
```

Reviews the complete tree, or only the positional paths supplied, and writes a
remediation plan. It is for codebase health work, rather than a pull-request
comment.

| Flag | Meaning |
| --- | --- |
| `-repo`, `-config` | Repository root and configuration path. |
| `-budget` | Stop the model pass after this many estimated source tokens; `0` reviews the whole tree. The report names omitted work. |
| `-instruction` | Extra instruction for this run. |
| `-no-linters` | Do not run configured linters. |
| `-log-format` | Log format: `text` (default) or `json`. |
| `-v` | Print verbose logs. |

## `repo-score`

```sh
nitpick repo-score ./cmd
```

Runs the full-tree assessment and adds slop, bug, and security findings per
thousand lines, grouped by language. Positional paths and flags are identical
to [`full-review`](#full-review).

## `repo-standards`

```sh
nitpick repo-standards -check
nitpick repo-standards -profile engineering -base origin/main -json
```

Measures repository conventions and, by default, runs applicable analyzers to
enforce them. `-check` is suitable for CI. The default command needs neither a
model nor credentials. Set `-profile engineering` for the engineering
assessment; `-base`, `-budget`, and `-no-model` are valid only with that profile.

| Flag | Meaning |
| --- | --- |
| `-repo`, `-config` | Repository root and standards-evidence configuration. |
| `-base` | Comparison base for the engineering profile; requires `-profile engineering`. |
| `-check` | Exit 1 for violations or 2 when a required check could not run. |
| `-budget` | Estimated source-token ceiling for engineering assessments; requires `-profile engineering`. |
| `-linters` | Comma-separated analyzer names; defaults to applicable analyzers. It cannot override the engineering profile or combine with `-no-linters`. |
| `-no-linters` | Measure conventions without analyzers; cannot combine with `-linters`. |
| `-no-model` | Omit engineering model assessments and report coverage as missing; requires `-profile engineering`. |
| `-profile` | Opt into the `engineering` assessment profile. |
| `-json` | Emit the evidence and recommendations as JSON. |

## `commits`

```sh
nitpick commits -base origin/main -head HEAD -check
```

Checks commit subjects against the repository policy, including the title passed
with `-title`. Use `-check` for a failing CI gate and `-json` for machine output.

| Flag | Meaning |
| --- | --- |
| `-repo`, `-config` | Repository root and policy path. |
| `-base` | Required base revision. |
| `-head` | Revision to inspect (default `HEAD`). |
| `-title` | Also validate this proposed squash title. |
| `-check` | Return failure for invalid subjects. |
| `-json` | Print JSON. |

## `standards`

```sh
nitpick standards -base origin/main
nitpick standards -agents AGENTS.md
```

Counts conventions the repository demonstrates. No model is called and no
credentials are read. With `-base`, it also scores changed lines against the
standards measured at that revision; `-agents` writes the measured block to the
named agent-instructions file. `-agents` cannot be combined with `-base` or
`-json`.

| Flag | Meaning |
| --- | --- |
| `-repo` | Repository root (default `.`). |
| `-config` | Configuration file; default is `<repo>/.nitpick.yaml`. |
| `-base` | Revision at which to measure standards and score changed lines. |
| `-agents` | Write measured standards into this file; cannot combine with `-base` or `-json`. |
| `-json` | Print JSON; cannot combine with `-agents`. |

## `improve`

```sh
nitpick improve -base origin/main -dry-run
```

Runs a wider, pedantic review over the working tree by default and prints its
result; it does not publish GitHub findings. Use `@open-nitpick improve` in a
pull-request comment for the conversational version. It shares the revision,
configuration, instruction, linter, failure, logging, and narrowing controls
from [`review`](#review), but rejects pull-request publishing coordinates. It
also accepts the following local-only controls.

| Extra flag | Meaning |
| --- | --- |
| `-level` | Review level: `minimal`, `normal`, or `pedantic` (the default). |
| `-slop` | Include slop findings (default `true`). Set `-slop=false` to leave them out. |

## `slop`

```sh
nitpick slop -no-model docs/
```

Checks positional paths, or the repository if none are given, for mechanical
and model-driven writing tells. `-budget` caps the model pass in estimated
source tokens (`0` means all source); `-fail-over` exits 1 above the configured
tells-per-thousand-lines threshold. `-no-model` runs only deterministic checks
and does not read credentials.

| Flag | Meaning |
| --- | --- |
| `-repo` | Repository root (default `.`). |
| `-config` | Configuration file; default is `<repo>/.nitpick.yaml`. |
| `-budget` | Stop the model pass after this many estimated source tokens; `0` means all source. |
| `-fail-over` | Exit 1 when deterministic tells per thousand lines exceed this number; `-1` (default) never gates. |
| `-instruction` | Extra instruction for the model pass. |
| `-json` | Print the score and findings as JSON. |
| `-log-format` | Log format: `text` (default) or `json`. |
| `-no-linters` | Skip analyzers during the model pass. |
| `-no-model` | Run deterministic tells only; no credential is read. |
| `-v` | Print verbose logs. |

## `security`

```sh
nitpick security -no-model internal/review
```

Runs the security roster over positional paths or the repository. Required
scanner availability is part of the result. It rejects budget and linter-skip
controls because required scanners cannot be skipped. `-no-model` skips
only the optional model pass.

| Flag | Meaning |
| --- | --- |
| `-repo` | Repository root (default `.`). |
| `-config` | Configuration file; default is `<repo>/.nitpick.yaml`. |
| `-allow-clean-with-no-gate` | Required loud waiver when `-fail-on none` is used; it does not waive incomplete scanners. |
| `-fail-on` | Override `security.fail_on`: `nit`, `info`, `warning`, `error`, `critical`, or `none`. `none` requires `-allow-clean-with-no-gate`. |
| `-instruction` | Extra instruction for the optional model pass. |
| `-json` | Print the result as JSON. |
| `-log-format` | Log format: `text` (default) or `json`. |
| `-no-model` | Use deterministic scanners only. |
| `-v` | Print verbose logs. |

## `respond`

```sh
nitpick respond -event "$GITHUB_EVENT_PATH" -event-name issue_comment
```

Handles a GitHub Actions comment event. `@open-nitpick review` resumes a review;
`restart-review` starts a fresh one; `resolve` resolves the thread; any other
mention is answered in that thread. It needs GitHub event data and credentials,
and is intended for `issue_comment` and `pull_request_review_comment` workflows.

| Flag | Meaning |
| --- | --- |
| `-config` | Configuration file. |
| `-event`, `-event-name` | Event payload and event name; defaults come from GitHub Actions. |
| `-mention` | Reviewer mention to recognize. |
| `-owner`, `-repo`, `-repo-name` | GitHub and checkout coordinates. |
| `-log-format` | Log format: `text` (default) or `json`. |
| `-v` | Print verbose logs. |

## `mcp`

```sh
nitpick mcp
nitpick mcp install cursor -command nitpick
nitpick mcp clients
```

Starts the Model Context Protocol server over standard input and output. Its
flags are `-repo`, `-v`.

### `mcp install`

Adds the MCP server to the named required client configuration. `-command`
sets the executable (default `nitpick`); `-repo` chooses the project
configuration; `-user` selects a user-level configuration; `-print` prints the
change rather than writing it. `nitpick mcp clients` lists supported clients and
their configuration locations.

### `mcp clients`

Lists supported MCP clients and their configuration locations. It takes no flags.

## `auth`

```sh
printf '%s\n' "$OPENROUTER_API_KEY" | nitpick auth set openrouter
nitpick auth list
nitpick auth delete openrouter
```

Stores provider credentials in the operating system keystore. `set` reads one
credential from standard input, so a shell history or process list does not
receive it. `list` prints provider names only; `delete` removes that provider's
stored credential. `set` and `delete` take the provider as a positional
argument; `list` takes none. These subcommands have no flags.

Use `nitpick auth -h` for help; the subcommands take positional arguments and
do not support their own help flag.

### `auth set`

Reads a credential from standard input and stores it for the positional provider.

### `auth delete`

Removes the credential for the positional provider.

### `auth list`

Lists providers with a stored credential.

## `init`

```sh
nitpick init -provider openrouter -model z-ai/glm-5.3-flash
```

Writes a starter `.nitpick.yaml`; it refuses to overwrite an existing file
without `-force`. Add `-workflow` to also write `.github/workflows/nitpick.yml`.
`-repo` sets the target root, and `-provider` and `-model` select the starter
model (or their corresponding environment defaults).

## `explain-config`

```sh
nitpick explain-config -path internal/review/engine.go
```

Shows the resolved configuration, the model assigned to each role, and the
review prompt that would be sent. `-repo` chooses the repository and `-config`
chooses the configuration file. `-path` is a file path: it also shows the
path-scoped instructions that apply to that file.

## `providers`

```sh
nitpick providers
```

Lists model providers accepted by `models.*.provider`, including local and
OpenAI-compatible choices. It takes no flags; it does not show stored
credentials or validate provider endpoints. Use `nitpick auth list` to see
which providers have a stored credential.

## `linters`

```sh
nitpick linters
```

Prints the deterministic analyzer catalog, including file coverage, activation
mode, and configuration requirements. It takes no flags and does not run the
tools.

## `knowledge-index`

```sh
nitpick knowledge-index
```

Builds the local knowledge index. It needs `models.embed` configuration and its
provider credential. `-config` selects the configuration, `-provider` and
`-model` override the embedding model, and `-o` writes the index to that exact
output path. Without `-o`, `-d` selects the directory in which the command
writes a model-named JSON file; it defaults to `internal/knowledge/indexes`.

## `config-reference`

```sh
nitpick config-reference -o docs/configuration-reference.md
```

Generates configuration reference Markdown. `-o` is the output file and `-src`
chooses the configuration source used to build it. `make docs` writes
`docs/configuration-reference.md`; use that path for a checked-in regeneration.

## `version`

```sh
nitpick version
```

Prints the installed version. It takes no flags.
