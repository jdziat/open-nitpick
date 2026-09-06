---
name: full-review
description: Review a whole repository, or the directories given, as if every file were new, with a remediation plan and a coverage notice, using the nitpick MCP server. Use when asked to audit, assess, inspect, or find what is wrong with a codebase, a package, or a directory that has no diff to review, or to plan a cleanup. Also covers repo_score for a per-thousand-lines scorecard.
---

# Review a whole tree

Call `full_review` from the nitpick server with `paths` naming the
directories or files to review, relative to the repository root, and
`repo` as an absolute path when the session is not at the root. With no
`paths` it reviews the whole tree with the model on every batch, which is
the expensive lift: on a large repository, name the paths that matter or
pass `budget` (estimated tokens of source) for a first look. Either way
the answer says what it did not cover.

For a scorecard (slop, bug and security findings per thousand lines, by
language, weighted by severity) call `repo_score` instead; it is the same
review plus the numbers. A score compares a repository with itself over
time or with another repository; a single number is not a judgement.

## Reading the answer

- `sections` groups what was found by what a reader does about it: known
  advisories from the dependency scanner (deterministic, listed not
  judged), security risks, bugs, and AI slop. Each section says when it
  is empty.
- `plan` is the remediation plan, most severe first, findings that share
  a fix grouped, each with the files it touches. A security finding
  graded warning or above sorts with the errors: a committed credential is
  an incident before a crash is a bug.
- `covered`, `unbudgeted` and `skipped` are the coverage notice. Silence
  about a file that is in `unbudgeted` or `skipped` is not a clean file.
  Read this before saying anything is clean.
- `findings` carry the same fields as `review`; `classes` on the call
  filters them (and the plan) to the classes named.

## Acting on it

1. Work the plan in its order. The first items are the ones a person
   would want fixed before anything else.
2. Known advisories name a package, a version and an advisory id; the fix
   is a version bump, and the plan counts files, not hours.
3. When the notice says files were left out, either review them in a
   second call with `paths` naming them, or say they were not reviewed.
4. On a large repository, prefer several calls over several directories
   to one call over everything: each answer is smaller, and a failure in
   one does not lose the others.

## When not to use it

For a change with a diff, `review-change` is cheaper and reviews what
changed. For readability alone, `code-smell`; for generated code,
`ai-slop`; both are this review filtered, so they cost the same and the
same advice about `paths` applies.
