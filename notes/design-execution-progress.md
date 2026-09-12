# Design execution integration

Work in progress on `feat/package-design-execution`. Package assembly now runs
inside `Engine.Review`, after accepted policy resolution. Ordinary reviews keep
their existing assembly path. PR #118 merged as 47b39d4. The execution branch
still needs rebasing onto that squash commit, final gates and review.

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

- Rebase onto the final parent PR without losing execution or source bindings.
- Run full gates and local/hosted reviews; publish a separate execution PR.
- Complete paired mechanism fixtures and live evaluations, then audit every
  requirement in best-practice-coverage.md. The overall goal is still incomplete.
