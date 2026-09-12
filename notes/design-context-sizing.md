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
