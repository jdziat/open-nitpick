# Agents and MCP

The MCP server exposes review results to a coding agent without giving that
agent permission to publish GitHub comments. The caller decides what to do with
the findings.

## Install for a client

```bash
nitpick mcp install claude-code
```

The command writes the client registration at the repository root. Run
`nitpick mcp -h` for supported client names and installation locations. For a
manual registration, start the server with:

```bash
nitpick mcp
```

## Available tools

The server includes tools for a change review, whole-tree review, repository
score, code smell, prose/slop, security scan, and effective configuration.
They return text plus structured findings with a path, line, severity, class,
rationale, suggestion, and coverage data.

MCP tools do not publish review comments. Use the GitHub Action for a PR
review, or give your own integration the forge credentials and policy it needs.
The [trust model](../trust-model.md) explains the boundary.
