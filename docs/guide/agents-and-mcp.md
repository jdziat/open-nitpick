# Use open-nitpick from an agent

`nitpick mcp` serves local review tools over MCP standard input and output. It
uses the configuration and provider environment of the repository where it
runs. It does not publish pull-request comments; the agent receives the result
and decides what to do with it.

## Install a client entry

See supported clients and where each client stores a project or user entry:

```bash
nitpick mcp clients
```

Preview a project-scoped entry before writing it:

```bash
nitpick mcp install claude-code -print
```

Then write it:

```bash
nitpick mcp install claude-code
```

`-print` writes nothing. Project entries vary by client: Claude Code uses `.mcp.json`, Cursor uses
`.cursor/mcp.json`, VS Code uses `.vscode/mcp.json`, OpenCode uses
`opencode.json`, and Gemini CLI uses `.gemini/settings.json`. Claude Desktop,
Windsurf, and Codex have user entries only. Use `-user` where a client supports
a user-scoped entry. `nitpick mcp install <client> -h` is the current source of
truth for each location and limitation.

## Tools and returned data

The server exposes `review`, `full_review`, `repo_score`, `code_smell`,
`ai_slop`, `security_scan`, and `explain_config`. `review` returns a summary,
findings with path, line, severity, class, title and rationale, analyzer
outcomes, the gate result, and `complete`/`failed_stages`. Whole-tree tools add
covered, unreviewed, unbudgeted, and skipped files; `repo_score` also adds a
scorecard.

Use `review` for a changed working tree. Use `full_review`, `repo_score`, or
`code_smell` with paths to bound a broader inspection. `security_scan` reports
whether required stages completed, and `ai_slop` supports `no_model` for its
deterministic checks. Start the transport directly only when your client needs
a manual command:

```bash
nitpick mcp -repo .
```

The process reserves standard output for MCP. Use `-v` for logs on standard
error. [All workflows](../usage.md#starting-a-repository-off) explains how the
same configuration works outside an agent session.
