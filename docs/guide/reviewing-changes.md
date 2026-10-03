# Review changes

Choose a pass by the question you need answered. The exact accepted flags vary
by command; the [CLI reference](../reference/cli.md) is the command contract.

## Choose a pass

### Review the change you are about to commit

`nitpick review` examines uncommitted changes by default, or a range with
`-base` and `-head`. It prints locally and can publish when supplied
pull-request flags and GitHub credentials.

### Get a quick first pass

`nitpick fast-review` examines eligible changed diffs and ranks at most ten
findings. It prints locally and can publish when supplied pull-request flags
and GitHub credentials.

### Assess a repository or focused directory

`nitpick full-review [paths...]` inspects the whole tree by default, or the
paths you name, and writes the broader assessment and remediation plan. It
prints locally.

`nitpick repo-score [paths...]` runs the same wider assessment and adds a
scorecard for comparing the same repository over time. It prints locally.

### Ask for improvement feedback on a pull request

`@open-nitpick improve` reviews naming, documentation, structure, idiom, and
slop after the normal review. It replies on GitHub and does not block a merge.

## Normal review

Run a normal review from a repository with uncommitted changes:

```bash
nitpick review -dry-run
```

Add `-base main -head HEAD` when you want an explicit range. `-dry-run` keeps a
GitHub-capable review from publishing. The result includes a walkthrough,
findings, coverage, and incomplete stages when planned work did not finish.
Do not read zero findings as an approval until completion is true.

## Fast review

```bash
nitpick fast-review -dry-run
```

Fast review examines eligible changed diffs and returns the ten highest-ranked
findings. Use it for an early signal; use a normal review when the merge
workflow needs its full configured assessment. `-full` bypasses incremental
narrowing for this run.

## Whole-tree review and score

```bash
nitpick full-review ./internal ./cmd
nitpick repo-score
```

The full pass reviews every eligible file in the named paths, or the repository
when paths are omitted. It can be expensive because it includes model work over
the selected tree. `repo-score` adds a scorecard; use it for comparing the same
repository over time, not as a judgment of a project.

## Ask for improvement feedback on a pull request

On a pull request, mention the reviewer in a conversation comment or inline
thread:

```text
@open-nitpick improve
```

It reviews naming, documentation, structure, idiom, and slop. An inline request
covers that file; a conversation request covers the change. It replies with a
single comment and does not create findings that block a merge.

## Incremental reviews

A GitHub review can reuse completed model work when its code, context, model,
and policy still match. New commits rerun affected work; unresolved commented
files are rechecked so their threads can be confirmed or closed. Use
`nitpick review -full` or `@open-nitpick restart-review` to inspect the whole
change again. [CI incremental review](../ci.md#incremental-review) explains the
stored progress and fallbacks.
