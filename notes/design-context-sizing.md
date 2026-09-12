# Package context sizing

The initial package planner is not ready to activate internally. A deterministic
probe of the execution tree found 24 tasks; 22 exceeded both six files and the
repository's 120,000-token source budget. The largest rendered task was 1,412,390
estimated tokens across 339 files. This was source/task text before prompt framing,
not an actual paid model request. Raw results: local development artifact
`/tmp/engineering-repo-design-sizing.json`.

The planner copied every file from each dependency and caller package. That
includes dependency tests and unrelated caller files. In this repository, test
packages embed large evaluation fixtures, so one ordinary package change pulls
most of the repository into its context.

Keep each primary package's source complete. Select dependency context from the
exported declarations its source references, and caller context from files that
actually import the primary package. Include local declaration dependencies and
receiver methods so these selections do not strand a helper or lifecycle method
in an unseen sibling. Blank and dot imports conservatively need the dependency's
non-test source. Type resolution and dynamic dispatch remain stated limitations.
Every admitted source stays bound to the task digest; omitted required context
continues to make the task incomplete. File/token limits remain operator policy.

Remeasure before selecting an internal policy or claiming this resolves sizing.
Even relevant complete package source can exceed a configured budget. Do not call
such a task complete or fall back to isolated generic file reviews under a package
coverage claim.

The first selector experiment still produced 24 tasks, of which 20 exceeded
120,000 estimated source tokens; the largest was 1,250,466 tokens across 258 files.
All tasks retained bound context and the focused controls passed, but this does
not resolve practical sizing. This second probe includes the selector's added
source files, so these are separate scope observations, not a controlled before/
after benchmark. Artifact: `/tmp/engineering-repo-selected-context-sizing.json`.

A remaining cause is genuine fan-out from widely used packages: selecting an
entire primary package makes every API in it relevant, and whole caller files
include large test fixtures unrelated to a particular contract. The next design
must use declared, bounded interactions or source spans while preserving the
context needed for each claim. It must not mark an oversized package assessed
because pieces appeared in unrelated successful requests. Activation remains
unpublished pending that work and a fresh adversarial review.

Declaration-span selection produced 24 rendered tasks, 14 above 120,000
estimated source tokens. The largest was 983,215 tokens across 261 files.
This is another scope observation with changed implementation source, not a
controlled comparison with the earlier probes. The source hashes, task bindings,
limits and per-task sizes are retained in
[evidence/design-declaration-span-sizing.json](evidence/design-declaration-span-sizing.json).
No paid model requests were made by this probe. It excludes prompt framing.

The selector now retains complete referenced declarations, local helpers,
initializers and receiver methods, with original physical source lines. Primary
packages remain whole. Whole-package primary scope and automatic expansion to
all receiver methods still produce broad dependency and caller context. The
current package unit therefore remains unsuitable for internal activation.

The next planner must declare bounded interactions before admission:

- Keep the inventory of packages, declarations and available edges independent
  of budgets. Assign each planned obligation to a named unit so a budget cannot
  improve apparent coverage by removing work from the denominator.
- Separate package identity from request identity. A successful request covers
  its named interaction; package completion requires every required interaction.
- Select the primary source and relevant state, invoked methods, helper closure,
  dependencies and caller obligations for that interaction. A reference to a
  receiver type alone must not expand every method into every request. Unknown
  dispatch remains an explicit inventory limitation.
- If callers need separate requests, retain the primary contract and required
  state/dependency evidence in each request. Do not infer cross-file findings
  from fragments supplied to unrelated requests.
- Preserve full-source slop coverage separately. Design spans cannot count as
  a completed file assessment merely because some declarations were supplied.
- Keep an indivisible oversized interaction omitted, with its cause. Add a
  fixture where splitting would strand the failure or lifecycle evidence, and
  require it to stay incomplete rather than pass as unrelated small tasks.

Remeasure before choosing internal policy limits or enabling required model
completion. Existing transport, mutation and selection checks validate the
mechanism; they do not establish practical or effective repository assessment.
