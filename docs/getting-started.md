# Get started

This path runs a review of changes in your working tree. The reviewer runs on
your machine; the configured model provider receives the review input. Choose a
local provider when source must stay on your machine, or review a hosted
provider's data policy before sending it source. [Providers](providers.md) and
the [trust model](trust-model.md) describe those boundaries.

## 1. Install the CLI

Download a binary for your platform from the
[releases page](https://github.com/jdziat/open-nitpick/releases), then put
`nitpick` on your `PATH`.

Or, with Go 1.26 or later:

```bash
go install github.com/jdziat/open-nitpick/cmd/nitpick@latest
export PATH="$(go env GOPATH)/bin:$PATH" # add this to your shell profile
nitpick version
```

If the last command cannot find `nitpick`, the binary is usually in
`$(go env GOPATH)/bin`; add that directory to `PATH` and open a new shell.

## 2. Choose a model and create configuration

For OpenRouter, set the provider, model, and its API key:

```bash
export LLM_PROVIDER=openrouter
export LLM_MODEL=z-ai/glm-5.3-flash
export OPENROUTER_API_KEY=sk-or-...
```

Then, at the root of the repository you want to review:

```bash
nitpick init
```

`init` writes `.nitpick.yaml`. Keep it in version control so a team reviews
with the same policy. Use `nitpick explain-config` to see the resolved
configuration, and [configuration](configuration.md) to change models, review
policy, or provider settings.

## 3. Review local changes

Make a small change, then run:

```bash
nitpick review -dry-run
```

The command prints a walkthrough, any findings, the files reviewed, and a
coverage result. `-dry-run` prevents GitHub publishing when credentials and a
pull request are present. A clean result is meaningful only when the coverage
result says the planned work completed; the walkthrough explains skipped or
failed stages.

| If this happens | Do this |
|---|---|
| No changes are found | Edit a tracked file, or review an explicit range with `-base` and `-head`. |
| The model cannot authenticate | Check the provider, model, and provider key; [providers](providers.md) lists the supported settings. |
| Configuration cannot load | Run `nitpick explain-config` in the repository root, then see [configuration](configuration.md). |
| A review is incomplete | Read its failed-stage and coverage information before treating zero findings as clean. See [how a review runs](how-a-review-runs.md). |

## Choose the next job

- [Review a change](guide/reviewing-changes.md) explains normal, fast, full,
  and improvement passes.
- [Put reviews in CI](ci.md) starts with a non-publishing GitHub Action run.
- [Use from an agent](guide/agents-and-mcp.md) installs the MCP server.
- [All workflows](usage.md) covers repository standards, security, slop,
  agent sessions, and whole-tree work.
- [CLI reference](reference/cli.md) documents every command and flag.
