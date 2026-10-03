# Get started

Run a useful review before you configure anything else. open-nitpick reads the
current repository, reviews the change you name, and reports what it did not
review so an empty result never masquerades as a clean review.

## Install

Use a release binary, or install the current command with Go:

```bash
go install github.com/jdziat/open-nitpick/cmd/nitpick@latest
nitpick version
```

Choose a provider and model before a model-backed review. The project uses
OpenRouter with GLM-5.3-Flash for review and Qwen3.8-27B for triage:

```bash
export LLM_PROVIDER=openrouter
export LLM_MODEL=z-ai/glm-5.3-flash
export OPENROUTER_API_KEY=sk-or-...
```

See [providers and models](providers.md) for local models, other gateways,
routing, pricing, and credential names.

## Run your first review

From a repository with uncommitted changes:

```bash
nitpick review
```

For a committed branch, give the comparison explicitly:

```bash
nitpick review -base origin/main -head HEAD
```

The command prints findings and coverage. Read both: a file can be skipped
because it is generated, ignored by policy, too large, or outside the chosen
change. `nitpick review -dry-run` shows the result without publishing it when
you run with GitHub credentials.

## Add a project configuration

`nitpick init` writes a starting `.nitpick.yaml` without making a network call:

```bash
nitpick init -provider openrouter -model z-ai/glm-5.3-flash
nitpick explain-config -path internal/example.go
```

`explain-config` is the quickest way to confirm which model, policy, and
path-scoped instructions a file receives. Commit the configuration only after
you are comfortable with which provider reads your source.

## Choose the next path

- [Review a change](guide/reviewing-changes.md) explains normal, fast, full,
  and improvement passes.
- [Run in GitHub Actions](ci.md) installs a pull-request check.
- [Configure models and policy](configuration.md) describes what a committed
  configuration controls.
- [Use from an agent](guide/agents-and-mcp.md) installs the MCP server.
- [CLI reference](reference/cli.md) documents every command and flag.
