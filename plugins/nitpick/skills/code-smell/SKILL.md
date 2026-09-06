---
name: code-smell
description: Find what would cost the next reader in a directory or file, maintainability, style and slop findings with a remediation plan, using the nitpick MCP server. Use when asked about code smell, readability, maintainability, technical debt, cleanup, refactoring candidates, or whether code is well written.
---

# Code smell

Call `code_smell` from the nitpick server with `paths` naming what to
look at, relative to the repository root (`repo` as an absolute path when
the session is not at the root). It is a full review filtered to the
maintainability, style and slop classes, so it costs what a full review
of those paths costs; name the paths that matter rather than the tree.

## What comes back

- `findings` in three classes. `maintainability` is what makes the code
  hard to change: duplicated logic, a function doing three things, a
  dependency in the wrong direction, a name that lies. `style` is what
  the project's own conventions or its formatter would say, when the
  model can see a convention being broken. `slop` is code that reads as
  generated and left unread, under rules named in the `ai-slop` skill.
- `plan` orders them and groups the ones that share a fix, with the files
  each touches, which is the estimate.
- `covered` and `skipped` say what was and was not read.

## Acting on it

1. Group by the plan, not by file: one fix often clears several findings.
2. Style findings are worth a look at the configuration before a fix. If
   the project has a formatter or a linter that disagrees with the
   finding, the tool wins and the finding is noise; say so.
3. A maintainability finding is a judgement, and the rationale is the
   argument. Quote it when you propose the change so the reader can
   disagree with the reason, not the conclusion.
4. Do not turn a smell review into a rewrite. The plan's first items are
   the ones worth a change; the rest are worth a sentence.

## When not to use it

For bugs and security, `full-review` unfiltered. For a change, not a
tree, `review-change`. For generated code alone, `ai-slop`.
