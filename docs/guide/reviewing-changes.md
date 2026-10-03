# Review a change

Choose the smallest pass that answers your question. All review commands accept
`-base`, `-head`, `-config`, `-instruction`, `-dry-run`, `-fail-on`, and
`-log-format` unless their [CLI reference](../reference/cli.md) says otherwise.

## Normal review

```bash
nitpick review                         # uncommitted changes
nitpick review -base origin/main -head HEAD
```

Use this for ordinary pull requests. The engine narrows later runs to work not
already completed when `review.incremental` is enabled. Add `-full` when you
need to discard that reuse for one run.

## Fast review

```bash
nitpick fast-review -base origin/main -head HEAD
```

Use this while iterating. It prioritizes the highest-value findings and returns
quickly; it is intentionally not evidence that every changed file was reviewed.
Use normal review before merging.

## Whole-repository review

```bash
nitpick full-review -repo .
nitpick repo-score -repo .
```

`full-review` reads the whole tree rather than a diff. `repo-score` adds a
scorecard and remediation plan. These runs can be expensive because every
eligible source file is in scope; use them for a baseline, release review, or
large refactor rather than every commit.

## Wider suggestions

```bash
nitpick improve -level pedantic -base origin/main -head HEAD
```

`improve` includes categories a normal review filters out. Use it to find
broader design or maintainability opportunities. Treat its suggestions as a
backlog, not a merge gate, unless you deliberately override `-fail-on`.

## Read the coverage before acting

A completed run lists files examined, files skipped and why, analyzers that ran,
and analyzers that did not. Investigate incomplete coverage before treating an
empty report as approval. See [how a review runs](../how-a-review-runs.md) for
the stages and [CLI reference](../reference/cli.md#review-commands) for flags.
