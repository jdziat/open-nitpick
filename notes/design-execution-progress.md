# Design execution integration

Work in progress on `feat/package-design-execution`. Package assembly now runs
inside `Engine.Review`, after accepted policy resolution. Ordinary reviews keep
their existing assembly path. PR #118 merged as 47b39d4. The execution branch is based on that squash commit and still needs final
gates and review.

Sizing is a current acceptance blocker: the whole-package planner produced
24 tasks, 22 above 120,000 estimated tokens. The reference-selection experiment
still leaves 20 above that limit. See design-context-sizing.md. Execution remains
unpublished; the implementation's ability to report omissions does not establish
that its units are practical.

Implemented:

- Frozen bounded source capture, explicit exclusions and omissions, and rejection
  of whole-file additions whose diff differs from captured source.
- Whole-task packing and spending selection, including framing costs. Dropped
  tasks stay declared with omissions; successful task IDs are separate from the
  shared immutable plan.
- Package coverage uses both actual batches and successful task IDs. Shared
  paths alone cannot establish completion.
- Deterministic assessment reuses frozen source and module metadata. Boundary
  checks retain changed-file scope. Unknown nested module metadata cannot be
  interpreted as known absence during this assessment.
- Prompt version 3 describes per-request design tasks rather than individual
  files. Historical prompt-version measurements are unchanged.
- Valid context-only claims retain their locations and appear in the summary,
  with forge-derived links pinned to the head revision. Unsupported locations
  remain raw evidence with a failed stage.
- Experts receive exact task context. Merged claims retain both contexts; the
  engineering prompt budget rejects oversized expert requests without dropping
  the claim or silently truncating evidence.
- Design file counts exclude context-only and repeated paths. Summary receipts
  count successful tasks only, and incomplete source paths are deduplicated.

Scripted checks cover actual Engine.Review requests containing a package sibling
and caller, zero-finding task completion, and spending omissions with no model
call. Focused tests cover frozen assessment without provider reads, module
metadata ambiguity, source links, merged expert evidence and expert limits.
Full-pipeline controls now cover context-only claims through review, triage,
expert validation and summary publication, plus cancellation during review and
triage with completed task evidence preserved. Planning also refuses invented
module identities when manifests or enumeration are incomplete.
Activation and snapshot-reread mutations both fail their controls. Count, merged-context and expert-limit mutations also fail their controls.
Earlier source-capture, exact-context and completion mutations failed.

Remaining:

- Revise oversized units without losing declared source/caller obligations;
  remeasure against this repository before activation.

- Run full gates and local/hosted reviews; publish a separate execution PR.
- Complete paired mechanism fixtures and live evaluations, then audit every
  requirement in best-practice-coverage.md. The overall goal is still incomplete.

Supporting source ranges now travel through packing, source binding, exact
reviewer/expert context, anchor filtering and report validation. Omitted bytes
are absent from prompts and the binding; range metadata and supplied bytes are
bound. Snippets cannot count as whole-file coverage. Focused race tests passed
for bundle, practices, review and cmd/nitpick; focused eval-tag vet and full lint
passed. Four deliberate mutations (rendering, source binding, anchor gaps and
report gaps) each failed its behavioral guard. Local artifacts:
`/tmp/design-spans-race.log`, `/tmp/design-spans-vet.log`,
`/tmp/design-spans-lint.log`, `/tmp/design-span-mutations.json`.

The planner now produces ranges for complete referenced declarations and their
local helpers, initializers and receiver methods. Caller selection follows
referencing declarations. Header/import context and physical source positions
are preserved. A missing dependency file stays declared even when it cannot be
parsed. Focused race tests and eval-tag vet passed for practices, review and
cmd/nitpick, and full lint passed. Three mutations (adjusted line directives,
lost helper closure and whole-file reference collection) failed their guards.
Artifacts: `/tmp/design-declaration-context-race.log`,
`/tmp/design-declaration-context-vet.log`,
`/tmp/design-declaration-context-lint.log`,
`/tmp/design-declaration-mutations.json`.

The new sizing observation still has 14 of 24 tasks above 120,000 estimated
source tokens. Evidence and the next planner requirements are in
[design-context-sizing.md](design-context-sizing.md). No execution PR has been
pushed; practical interaction planning and full acceptance remain unfinished.

Local Nitpick review of 7cf118f..ce68f8f completed with two informational
findings (Kimi-K3 review, GLM-5.3-Flash triage). It used an operator config with
package execution disabled to review the implementation while package sizing
remains unresolved; this was not an engineering-profile acceptance run.
The report is in `/tmp/design-span-model-review.log`.

- The claimed lack of span-merge coverage was a false positive:
  `TestDesignEvidenceRejectsUnseenRangesAfterContextMerge` checks both retained
  spans and unseen gaps. Removing merged spans fails that test.
- The whole-file EndLine behavior is intentional: every line cited in a range
  needs source evidence. Added
  `TestDesignEvidenceValidatesEveryLineOfWholeFileRanges` for valid ranges,
  invalid end lines and the single-line default. Replacing end-line validation
  with start-line validation fails it. EndLine is not offered by the model-facing
  schema, but the finding type supports range-producing reviewers.

Both follow-up mutations failed their guards, and review race tests passed.
The full serial race suite and eval-tag vet also passed. Docs and generated
standards were rebuilt; the tracked Python cache was restored. Deterministic
slop checks found historical commentary and one filler word in touched files;
those were shortened or removed, and the recheck reported zero tells. The
repository standards check passed with zero analyzer observations after copying
range slices before appending. Commit validation passed for the seven commits
then on the branch. These checks do not close the remaining sizing, live
mechanism evaluation, internal adoption or CTO acceptance work.
