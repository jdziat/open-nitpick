# Package-wide design execution

Work in progress. `PlanDesign` now describes intended package source, direct
imports and reverse importers before batching. Its tests cover siblings, both
edge directions, manifest changes, empty change scopes, missing content,
cancellation and dependency-sensitive source identities. Four targeted mutations
failed their guards. This planner is not yet called by the review engine.

`PackDesign` now renders a task with its complete source and context in one
request, checks the source digest again, and measures task metadata and source
together against the framing-adjusted token budget. Oversized or incomplete
tasks stay in the plan as omissions and produce no request. Five packing
mutations failed their guards: removed caller context, uncharged metadata,
accepted stale source, ignored per-request file limits and ignored total file
limits. A separate removal mutation proves that deleting a source keeps its
surviving package in the planned scope. This packer is also not wired
into engine execution yet; it deliberately does not split an oversized task.

The remaining integration must make these tasks control actual requests. Merely
renaming per-file targets after a generic review would not meet the design
contract in `best-practice-coverage.md`.

A package task needs its complete source and the intended dependency/caller
context in the same request. If file or token limits force multiple focused
requests, each must retain the required context or disclose omissions. The report
must identify which task a successful batch assessed; seeing its files in unrelated
batches is not sufficient. Cancellation and failed expert stages must preserve the
intended task set without reporting completed coverage.

The engine currently calls `bundle.AssembleReserving` after resolving accepted
policy. Engineering assembly must occur at that point and preserve its measured
framing reserve, path instructions, source exclusions and byte/token bounds.
`practices` can depend on `bundle`; the reverse would cycle through `standards`.
Keep ordinary review assembly unchanged. Explicit `-profile engineering` must
activate this path even when accepted YAML does not name the profile.

Use one frozen source view across planning and execution. PR sources must come
from the pinned head. A tree provider's budget omissions must not reappear as
unlimited context through `ListDir` or `FileContent`: its snapshot still contains
files excluded from the synthesized diff. Record excluded and unresolved graph
scope. Do not infer missing caller coverage solely from a partial inventory.

Context-only findings need summary evidence, not invented diff anchors. The
current engine filters findings by changed lines before assessment; integration
must preserve unanchored model evidence for the engineering adapter while keeping
inline publication constraints. Expert validation must receive the same task
context, and coverage must not claim it validated unseen context.

Required integration controls include a scripted provider observing same-request
package siblings and callers; successful no-findings responses with exact task
coverage; split-batch, budget, missing-source and cancelled controls; unanchored
cross-file evidence; and source/context changes invalidating task identity.
Engineering incremental reuse is currently disabled, so source digests are not
claims that a model result can be reused.
