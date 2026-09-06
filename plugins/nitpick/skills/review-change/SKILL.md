---
name: review-change
description: Review a change before it is committed or opened as a pull request, with the nitpick MCP server. Use when asked to review, check, or look over a change, a diff, a branch, or the working tree, or before committing work you did in this session. Returns findings with path, line, severity and class, an analyzer roster, and whether the configured gate failed.
---

# Review a change

Call the `review` tool from the nitpick server. It reviews the uncommitted
working tree by default; pass `base` (and optionally `head`) to review a
branch against its base, for example `base: "main"`. Pass `repo` as an
absolute path when the session is not running at the repository root.

The review is the one a pull request would get: the configured model over
the diff with the deterministic analyzers as evidence, a triage pass, and
domain experts on findings in the classes the configuration validates.
Nothing is published; the findings come back to you.

## Reading the answer

- `summary` is the walkthrough. `findings` carry `path`, `line`,
  `severity` (nit, info, warning, error, critical), `class` (security,
  correctness, concurrency, resource, data-loss, contract, tests,
  maintainability, style, slop), `title`, `rationale`, and sometimes a
  `suggestion` (replacement code for the anchored lines) and a `source`
  (the analyzer rule, when an analyzer reported it).
- `failed` says whether a finding reached `fail_on`. A failed gate is what
  CI would report; fix those before anything else.
- `analyzers` says which deterministic tools ran, were skipped, or failed,
  and why. A skipped analyzer is not a clean analyzer.
- `withheld` lists findings a domain expert or triage set aside, with the
  reason. Read them: an expert's disagreement is information, not noise.
- `policy`, when set, says the change edits `.nitpick.yaml` and was
  reviewed under the base revision's configuration instead. Say so if you
  report the review to a person.

## Acting on it

1. Fix findings at or above the gate first, in severity order.
2. A `suggestion` is a candidate replacement, not a patch to apply blind:
   read the anchored lines, then apply it if it is right.
3. A finding with a `source` came from an analyzer. Its severity is
   nitpick's translation of the tool's word; do not quote it as the
   analyzer's own rating.
4. When you disagree with a finding, say why in one sentence rather than
   dropping it silently; the rationale is what a reviewer would argue with.
5. Re-run `review` after the fixes when the change was large, to catch
   what the fixes introduced. A second run costs a second review.

## When not to use it

For a whole repository or a directory that has no diff, use the
`full-review` skill. For readability alone, `code-smell`. For a question
about generated code, `ai-slop`.
