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

To register the server with every client found on this machine at once, run
`nitpick setup` (`nitpick setup -dry-run` lists the files first).

`-print` writes nothing. Project entries vary by client: Claude Code uses `.mcp.json`, Cursor uses
`.cursor/mcp.json`, VS Code uses `.vscode/mcp.json`, OpenCode uses
`opencode.json`, and Gemini CLI uses `.gemini/settings.json`. Claude Desktop,
Windsurf, and Codex have user entries only. Use `-user` where a client supports
a user-scoped entry. `nitpick mcp install <client> -h` is the current source of
truth for each location and limitation.

Reinstalling updates Nitpick's command and arguments while preserving custom
server settings, including environment variables, timeouts, and disabled state.
Other servers and settings are kept.

For Codex, installation adds `tool_timeout_sec = 3600` when the Nitpick entry has
no explicit timeout. Reinstalling keeps your configured value and TOML comments
outside the values it updates. Restart the client to load the new settings.
The client timeout limits the whole tool call; Nitpick's model timeout limits
each model request, and a review can make several requests. Increase the client
timeout for longer reviews, or run `nitpick review -dry-run` in a tracked local
process to avoid the MCP wait limit.

A `review` call has a total server deadline of 30 minutes. Set
`timeout_seconds` to 1–86400 to change it (0 uses the default), and keep the
client timeout longer than that deadline. Clients that supply an MCP progress
token receive stage messages and a heartbeat every 30 seconds, including elapsed
time. A heartbeat says the request is still pending; it does not prove model
progress. Clients without a progress token still receive the result or error.
Cancellation and the total deadline propagate to model requests and credential
commands. A deadline error is a failed review, not a clean result.

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
