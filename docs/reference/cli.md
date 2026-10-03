# CLI reference

Run `nitpick <command> -h` for the installed binary's exact flag defaults. This
page explains when to use each command, the inputs it reads, and what it can
change. Commands never need a model unless they run a model pass; commands that
do need one resolve credentials from the selected provider.

## Common conventions

`-repo` selects a checkout. `-config` selects a `.nitpick.yaml`; configuration
from the base revision is used to review a change that edits its own policy.
`-base` and `-head` select a committed range; without them, review commands use
the working tree. `-log-format json` makes progress logs machine-readable.
`-fail-on` changes the severity that returns a nonzero status for that one run.

## Review commands

### `review`

```bash
nitpick review [ -base <revision> -head <revision> ]
```

Reviews a change and, when running with GitHub credentials in a PR workflow,
publishes its result. Use `-dry-run` to print instead. `-full` disables
incremental narrowing; `-no-linters` skips configured analyzers; `-instruction`
adds one run-specific instruction. See [Review a change](../guide/reviewing-changes.md).

### `fast-review`

```bash
nitpick fast-review [ -base <revision> -head <revision> ]
```

Runs the low-latency review profile and returns the highest-value findings. Its
`-limit` controls the maximum findings, not a file limit. It accepts `-full`,
`-dry-run`, `-instruction`, `-fail-on`, `-config`, and `-log-format`.

### `improve`

```bash
nitpick improve -level pedantic [ -base <revision> -head <revision> ]
```

Runs a wider suggestion pass. `-level` is `minimal`, `normal`, or `pedantic`.
It accepts the normal review flags, including `-full` and `-no-linters`.

### `full-review`

```bash
nitpick full-review [ -repo <directory> ]
```

Reviews the whole working tree. It accepts `-config`, `-fail-on`, `-instruction`,
`-no-linters`, `-log-format`, and `-v`. It is not a replacement for a PR review:
it has no diff anchors and can be much larger.

### `repo-score`

```bash
nitpick repo-score [ -repo <directory> ]
```

Runs a whole-tree review and adds a scorecard and remediation plan. It accepts
the same flags as `full-review`.

## Repository quality commands

### `commits`

```bash
nitpick commits [ -base <revision> -head <revision> ]
```

Checks commit messages in a range. `-title` checks an intended squash title;
`-json` writes structured findings. It reads Git history but never calls a model.

### `repo-standards`

```bash
nitpick repo-standards -check
```

Measures demonstrated conventions and installed analyzer coverage. `-check`
returns nonzero when a required analyzer did not run. Use `-linters` to select
analyzers, `-json` for structured results, and `-v` for commands and skips.

### `standards`

```bash
nitpick standards [ -base <revision> ]
```

Measures conventions in the repository; with `-base`, scores the change against
standards measured from that revision. `-agents AGENTS.md` writes the measured
rules to an agent-instructions file. No model or credential is used.

### `security`

```bash
nitpick security [ -repo <directory> ]
```

Runs security scanners and an optional security model pass. `-no-model` keeps
only deterministic scanners. `-fail-on`, `-instruction`, `-json`, `-config`,
`-log-format`, and `-v` control the result. Read [Security policy](../security.md)
before treating it as a release gate.

### `slop`

```bash
nitpick slop [paths...]
```

Checks prose for the project’s named writing tells. With no paths, it considers
the working tree; `-no-model` runs deterministic rules only. Use `-json` for
automation and `-fail-on` to choose the gate threshold.

## Configuration and discovery

### `init`

```bash
nitpick init -provider openrouter -model z-ai/glm-5.3-flash
```

Writes a starter configuration. Use `-force` only when replacing an existing
file. It does not make a model request.

### `explain-config`

```bash
nitpick explain-config -path internal/example.go
```

Prints the effective models, review policy, analyzers, instructions, and prompt
for a path. `-base` resolves policy at a revision; `-json` supports tooling.

### `config-reference`

```bash
nitpick config-reference
```

Prints the generated configuration key reference. The rendered equivalent is
[Configuration reference](../configuration-reference.md).

### `providers` and `linters`

```bash
nitpick providers
nitpick linters
```

List built-in providers and available analyzer integrations. Both accept `-json`.
Use `providers` to discover credential environment variables and `linters` to
see language/tool matching.

### `knowledge-index`

```bash
nitpick knowledge-index [ -repo <directory> ]
```

Embeds the configured knowledge corpus and writes the retrieval index. It needs
`models.embed` and that provider's credential. Use `-dry-run` to list intended
inputs, `-force` to rebuild, `-json` for automation, and `-v` for progress.

## Integrations

### `respond`

```bash
nitpick respond -event "$GITHUB_EVENT_PATH"
```

Runs in a GitHub Actions comment event. It recognizes `@open-nitpick review`,
`restart-review`, and `resolve`; other mentions become a question answered in
the thread. It accepts `-config`, `-event`, `-repo`, `-log-format`, and `-v`.

### `mcp` and `mcp install`

```bash
nitpick mcp
nitpick mcp install claude-code
```

`mcp` starts the Model Context Protocol server on standard input/output.
`mcp install <client>` writes a supported client registration. Use `-h` on each
form for client names and paths; see [Agents and MCP](../guide/agents-and-mcp.md).

### `auth`

```bash
nitpick auth set openrouter
nitpick auth list
nitpick auth delete openrouter
```

Stores, lists, or removes local provider credentials. `set` reads from standard
input when needed; do not commit credentials into `.nitpick.yaml`. `list` shows
which providers have credentials, never their values.

## Utility commands

### `version`

```bash
nitpick version
```

Prints the binary version and build metadata. Use it in bug reports and CI
receipts.

### Help

```bash
nitpick -h
nitpick <command> -h
```

Help is the authoritative synopsis for the installed version. If this page and
help disagree, treat help as correct and report the documentation mismatch.
