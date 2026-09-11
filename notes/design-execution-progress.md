# Design execution integration

Work in progress. The source capture and assembly helpers exist and have focused
race tests, but the review engine does not call them yet.

`bundle.CaptureDesignSources` freezes changed source and Go graph candidates,
records exclusions and missing scope, bounds enumeration and reads, rejects
traversal paths, and refuses blocked tree-budget source before fetching it.
Three targeted mutations fail: bypassed tree omissions, mutable source aliases,
and unbounded explicit-file reads.

`Engine.assembleDesign` connects that view to the planner and packer. A control
checks same-request package siblings and a caller, then marks a sibling as
unbudgeted and checks that its bytes do not return as context and that coverage
retains the missing scope. Inventory errors must prevent completed design
coverage even if a remaining package task can run.

Explicit engineering selection now sets the effective profile and retains the
operator's `review.max_files` limit. Its regression reproduced the previous
profile omission and unlimited-file override, then passed after correction.

Design batches now render their task metadata with source. Findings retain
engine-owned task context through triage, and experts select that context before
the path-only fallback. Successful task IDs are recorded separately from the
shared immutable plan. Scripted-provider tests cover an empty successful response
and exact context passed from reviewer to expert; three mutations fail for lost
context, substituted expert context and lost completion evidence.

Remaining integration:

- Activate assembly only after accepted policy resolution.
- Use the frozen view for all planning and execution. Empty files, intentional
  exclusions, deleted sources and unreadable files need distinct scope outcomes.
- Preserve whole tasks under spending limits. Price framing as well as rendered
  task/source content; retain intended tasks when they are dropped.
- Make the reporting adapter consume successful task IDs independently of
  other batches that happen to contain the same source paths.
- Cover triage merges and repeated paths across different tasks, including
  expert token limits; retained context must not be silently truncated.
- Keep valid context-only design findings through validation and publish them
  as summary evidence. Do not invent inline diff anchors.
- Make adapters use actual package tasks and completion evidence. Global graph
  omissions, cancellation and failed stages must remain incomplete.
- Add scripted-provider controls, then complete the remaining mechanism pairs
  and repeat the live repository assessment before the CTO acceptance review.

Before activation, check addition rendering against frozen source: ordinary
`bundle.Render` omits Content for a whole-file addition because the diff normally
contains identical bytes. A diff captured before a worktree mutation can violate
that assumption. Package requests must either reject that mismatch or render the
captured source explicitly and disclose inconsistency. Do not claim the source
digest describes bytes the model never saw.
