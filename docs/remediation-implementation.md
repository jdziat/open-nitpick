# Competitive remediation implementation

Source: `revised-plan.md` at the repository root, original proposal blob
`a2b71eb0ed77b047d8d657805f116f0fd59a1ef7`. The user has requested the full
implementation, CTO signoff, and a complete `nitpick review` with no warnings
or errors. This record separates implemented work from evidence still needed.

## Current work

| Stage | Implementation and evidence | Remaining gate |
| --- | --- | --- |
| G0 accounting | Provider-attempt observation inside retries; attribution; review JSON including fatal errors; dynamic fallback meters; fix usage receipts; separate Jev/embedding records; hashed artifacts; offline reports that retain unknown usage/prices | Final full-suite and review evidence |
| G4 thread lifecycle | Reviewed-path and API-confirmed resolution guards; rename, move, recurrence, permission denial and partial mutation tests | Final full-suite and mutation evidence |
| G6 approvals | Unread standing threads, stale heads, incomplete coverage and failed history prevent approval; existing publication semantics retained | Final full-suite and review evidence |
| G3 learnings | Individual draft/accept commands; provider consent; redaction; tool-free drafting; authenticated acceptance; trusted-base provenance; hashes; scope/budget controls; revocation; prompt/JSON integration | Human temporal study, frozen quality thresholds and labor/spend budget; no quality claim |
| G2 offline feasibility | Retained-candidate inventory, seeded stratified sampling, blinded human view, sampling weights, unknown-label sensitivity, optimistic precision ceiling | Human adjudication, explicit scope and runnable mappings |
| S0 execution boundary | Actual runc controls pass for selected tests, broken candidates, no-tests/early-exit, missing dependencies, credential/socket/network denial, immutable cache and timeout cleanup ([guide](guide/execution-evidence.md)) | A different execution scope needs a separately reviewed policy; no arbitrary-contributor containment claim |
| G1 fix check evidence | Existing authorized respond/fix flow now supports optional baseline/candidate comparisons, source/patch/policy/output hashes, stale-head checks, and required-check refusal | Human fix-quality corpus, frozen numeric quality thresholds and funded evaluation; no reliable-fixes claim |
| G2 execution arm | Opt-in exact finding/revision/profile mappings can refute only a selected default Go-build failure; unavailable or unrelated results preserve the previous verifier decision | Human adjudication, fixed-draft replay, fresh-PR protocol and funded evaluation; no precision-gain claim |
| G5 GitLab | Demand and disposable-instance details requested | Provider and wiring, contract fixtures, disposable-instance integration proof |
| G7 PR-body summary | Existing walkthrough comments retained | Body editing deferred: no verified conditional-update contract that preserves concurrent author edits |
| D8 evaluation | Unregistered | Frozen sampling/labels/protocol, rates, sample size, explicit spend cap and contemporaneous arms |

G8 and the other explicitly deferred capabilities remain deferred as stated in
the source plan. The user identified CTO signoff as the local agent persona;
its initial audit found accounting defects that are now fixed. Final signoff
awaits the final diff and validation. No quality promotion is implied.

## Accounting evidence

The historical artifacts exist at `/tmp/bakeoff/fresh/runs/d7c-p1`, `d7c-p2`,
and `d7c-p3`. Each holds 30 completed reviews, but none has per-attempt metering.
There are 202 posted and 125 retained withheld candidates across the passes.
The logs contain partial agent/critique token totals. They cannot establish
complete tokens or cost per review. `deep_stats.py` now names that limitation
and requires explicit rates to print even a partial cost estimate.

```sh
python3 tools/bakeoff/usage_report.py /path/to/run --output accounting.json
python3 tools/bakeoff/execution_feasibility.py sample \
  --runs /path/to/run --scores /path/to/scores.json \
  --per-stratum 5 --seed 20261117 --output /new/sample-directory
python3 tools/bakeoff/execution_feasibility.py report \
  --sample /new/sample-directory/sample-manifest.json \
  --adjudications /new/sample-directory/human-adjudication.json
```

The sample created during development has 15 candidates, five from each
reference-matching stratum, with all 15 labels unknown. This is a tooling
exercise, not an evaluation sample-size decision. The report returns an
undefined adjudicated ceiling and requests human adjudication. It makes no
precision-gain claim. Keep arm/source metadata separate from the human view.

## Learning workflow

Configure `review.learnings.enabled: true` in operator configuration. For
local reviews also set `review.learnings.repository`. The default is disabled;
repository-provided configuration cannot enable or redirect it. Accepted
entries live in `.nitpick/learnings/manifest.json` at the trusted base.
Missing base disables retrieval with a notice. Malformed active entries fail
the review. The complete rendered reference section has a token limit.

Use `nitpick learn draft` and `nitpick learn accept` as documented in the
[CLI reference](reference/cli.md#learn). The scripted-entry path exercises the
workflow without a model call. Acceptance prints a manifest for a reviewed
policy change; it neither publishes nor activates it on the candidate branch.
Fixed/no-longer-relevant feedback remains history and is excluded from active
retrieval. JSON rejects duplicate keys and unknown fields.

## Validation status

Focused Go tests and eight Python accounting/feasibility tests pass. Mutation
checks have killed restorations of the two outdated-thread bypasses, moving
observation outside retries, omitting trusted-base checks, omitting repository
identity checks, omitting content hashes, and omitting prompt integration.
The full race suite, vet, generated-document checks, standards check, and
nitpick review must be assessed on the final change before completion.

ESLint matches the version pinned in CI (`eslint@10.8.0`). Analyzer-using
tests and standalone linter checks are serialized when they share the
same golangci-lint process lock.

The preexisting dirty Jev/Mira work was preserved. A local baseline patch,
untracked hashes, mutation outputs, accounting inventory, and feasibility
sample are under `/tmp/open-nitpick-remediation-4wtbfqf6`. These are local
development evidence, not published GitHub evidence links. No commit or push
has been made.

## Lifecycle evidence

Execution uses complete Git tree enumeration and refuses symlinks, submodules
and truncated trees. JSON retains bounded receipts keyed by the exact finding
digest, including unavailable attempts; disabled execution and no matching
claims have separate states.

Rename/move controls preserve an old thread when its path was not reviewed;
they do not claim automatic reattachment.

| Scenario | Direct guard |
|---|---|
| Rename or file move | `TestRenamedAndMovedPathsDoNotCloseUnreadThreads` |
| Force push | `TestIncrementalFallsBackToFullReviewAfterForcePush` |
| Reopened thread | `TestIncrementalDoesNotSuppressReopenedDismissedFinding` |
| Recurring or withheld finding | `TestRecurringAndWithheldFindingsKeepTheirExistingThreads` |
| Partial review or split file coverage | `TestIncrementalDoesNotResolveThreadsOnIncompleteFiles`, `TestAPartlyFailedRunIsNotApproved` |
| Incomplete or legacy history | `TestIncompleteAndLegacyRunsCannotEstablishCoverage` |
| Permission denial or partial mutation | `TestDeniedAndPartialThreadMutationsCannotPermitApproval` |
| Forge accepts but does not confirm resolution | `TestResolveThreadsTrustsOnlyConfirmedResolutions` |
| Stale summary/head | `TestPublishedApprovalPinsAndChecksReviewedCommit` |
| Failed prior-history read | `TestCleanApproveHeldWhenResidualPriorReadFails` |
| Outdated thread on an unread/skipped path | `TestOutdatedThreadsRequireReviewedPathsBeforeResolution` |

These guards run in the ordinary suite. The execution controls also require
the explicitly selected Docker image shown in the guide.
