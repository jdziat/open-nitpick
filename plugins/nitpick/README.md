# nitpick, from an agent session

This plugin puts open-nitpick's review engine inside an agent session as
MCP tools, with skills that say when to reach for each and what to do with
what comes back.

Requirements: the `nitpick` binary on PATH (`go install
github.com/jdziat/open-nitpick/cmd/nitpick@latest`, or a release asset), a
model configured the way the CLI wants it (`.nitpick.yaml` in the
repository, or `LLM_PROVIDER` and `LLM_MODEL` with the provider's key in the
environment), and git.

Install from the marketplace in this repository:

```
/plugin marketplace add jdziat/open-nitpick
/plugin install nitpick@open-nitpick
```

Or register the server directly, without the skills, with whichever
client you use:

```
nitpick mcp install claude-code     # .mcp.json at the repository root
nitpick mcp install cursor          # or opencode, vscode, gemini-cli; -user for the user-wide file
nitpick mcp install codex -user     # ~/.codex/config.toml
nitpick mcp clients                 # the full list
```

Each writes `{"mcpServers": {"nitpick": {"command": "nitpick", "args":
["mcp"]}}}` in the client's own shape, keeping what else is in the file;
`-print` shows it without writing.

## Tools

| tool | what it does | cost |
|---|---|---|
| `review` | the uncommitted change, or base..head, reviewed as a pull request would be | one review of the change |
| `full_review` | the whole tree or the paths given: bugs, security risks, known advisories, slop, a remediation plan, and what was not covered | the model on every batch; bound it with `paths` or `budget` |
| `repo_score` | `full_review` plus findings per thousand lines by language | the same |
| `code_smell` | `full_review` filtered to maintainability, style and slop | the same; pass `paths` |
| `ai_slop` | the tells (em dashes, filler, chat prose, restating and oversized comments) without a model, then the model's nine slop rules, both per thousand lines, with fixes ordered by count | free with `no_model`; otherwise the same; pass `paths` |
| `identify_model` | which of six models' styles a Go, Python or TypeScript file is nearest to, or unknown | none |
| `explain_config` | the resolved configuration for a repository | none |

Every tool returns text for reading and structured content (`findings`
with `path`, `line`, `severity`, `class`, `title`, `rationale`,
`suggestion`, `source`) for acting on. Nothing is published to a forge; the
session decides what to do with the findings.

## Skills

- `review-change`: before a commit or a pull request, or when asked to review.
- `full-review`: when asked to audit, assess, or find what is wrong with a repository or a directory.
- `code-smell`: when asked about maintainability, readability, or what would cost the next reader.
- `ai-slop`: when asked whether code reads as generated and left unread, or to clean up model-written code.
- `identify-model`: when asked which model wrote a file, with the caveat that it is a style match against six models, not an attribution.

The skills carry the rules that make the answers useful: pass paths to keep
a tree review cheap, read the coverage notice before calling anything
clean, act on the remediation plan in its order, and never present a
finding's severity as the model's own word when an analyzer reported it.
