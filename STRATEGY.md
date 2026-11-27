# STRATEGY

> Updated 2026-11-17. The user approved implementation and trusted-corpus
> Docker execution. Quality promotion still requires frozen studies and budgets.

## Problem

nitpick reviews pull requests on the contributor's host with a model of
its choice. Its measured obstacle is precision at unchanged recall, and its
fix proposals are generated but unverified. Its cost accounting exists in
pieces; the gaps are completeness and retained evidence, not a missing meter.
The competitive analysis behind the first pass overstated several vendor
claims, so this strategy rests on verified local gaps and the user's accepted
learning design.

## Approach

Establish evidence before changing behavior. Audit what runs and what it costs;
audit thread resolution and approvals as they exist; prototype the accepted
learning shape and the offline feasibility of execution evidence. Only then,
behind an owner-approved execution boundary, attach check evidence to the
existing fix proposal and test whether execution evidence improves finding
selection. Expand to another forge only with a real interested user and a
disposable test instance.

## Users

Operators who self-host a pull-request reviewer and need findings they can act
on: explicit provenance, known costs, existing review and approval behavior
left intact, and policy changes that a maintainer accepts rather than a model
silently applies.

## Success metrics

- G0 reconciles every role, retry, and unknown usage in scripted cases.
- G4 and G6 pin existing lifecycle and approval behavior with positive and
  negative controls, and mutations break their guards.
- G3 proves provenance, permissions, base-revision isolation, revocation, and
  positive/negative retrieval with deterministic tests before any pilot.
- G2 shows whether a valid claim-specific refutation is available often enough
  to justify execution, with `precision_ceiling` reported as an optimistic
  bound.
- Promotions require frozen gates; inconclusive and
  underpowered results are recorded as such and are not rerun on the same
  holdout.

## Active tracks

- **G0**: audit accounting and freeze the baseline. Ready.
- **G4**: audit the thread lifecycle. Ready.
- **G6**: audit approvals; defer new merge policy. Ready.
- **G3**: operator-approved, repository-scoped learnings. Prototype after G0;
  the learning shape is accepted, budgets and features are not.
- **G2**: whether execution evidence improves finding selection. Offline
  feasibility first.
- **S0**: execution safety boundary. Required before any code execution.
- **G1**: check evidence on the existing fix proposal. After S0 and G0.
- **G5**: GitLab, only after verified demand and a test instance.
- **G7**: optional PR-body summary, low priority, needs a safe update API.
- **D8**: proposed deep-review evaluation, not registered and not approved.

## Non-goals

- No automatic suppression, cross-repository learning, or candidate PR
  activating its own policy.
- No new approval mode or blocking review default.
- No standalone fix command, auto-merge, or self-approval.
- No arbitrary-code sandbox claim; S0 selects and tests one backend.
- No feature-parity roadmap, dashboards, diagrams, repeated fix loops, or other
  deferred work.
- No withdrawn delivery or cost promises. Estimates follow the feasibility
  audits.

## Measurement

`docs/measurement.md` governs every claim. Deterministic columns are primary.
Paired arms use the same PR revisions and declared coverage. Failures,
abstentions, and unknown labels stay in the denominators they affect. Three
passes and PR-level paired intervals apply to quality comparisons; replicates
are not independent PRs. Sample size comes from development variance and the
intended effect. A safety invariant break is a fail; incomplete or overlapping
results are inconclusive; both block promotion.

## Next

Implementation now includes the accounting receipts, trusted-base learnings,
thread/approval safety guards, optional fix checks, and a registered build-claim
execution pilot. `runc` is approved for hand-picked examples; no gVisor
installation is required for that scope.

0. Re-ingest this checkout into Semanta before relying on hosted code retrieval.
   The 2026-11-17 pulse reports drift 87, stale freshness, and no embedded
   entities; audit source locally until a new pulse clears that limitation.
1. Start G0, G4, and G6 in parallel.
2. Record the overlay path, acceptance workflow, isolation backend, thresholds,
   sample sizes, GitLab demand, and spend limits as owner decisions before the
   tracks that need them.
3. After G0, prototype G3 with scripted drafts and run G2 offline feasibility.
4. Do not execute PR code until S0 passes and an operator accepts its residual
   risk.
