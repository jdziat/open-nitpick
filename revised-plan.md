# Competitive remediation plan — revised after adversarial review

Status: proposed work, not implementation approval. This document supersedes
`docs/plans/2026-11-17-competitive-gaps.md` for discussion. Only this plan is
being revised; do not commit, stash, reset, or change the existing source work.
The user has accepted the learning shape in G3, not every feature or budget.

## 1. Evidence and corrections

This review inspected the working tree on `feat/deep-review` at HEAD
`89736ff14973522bb77842bb9d0f6b4c5d18f94a`, including uncommitted changes.
The following are implementation evidence, not claims that tests ran today:

| Area | Evidence | Consequence for this plan |
|---|---|---|
| Fix application | `cmd/nitpick/fix.go:runFix`, `internal/fix/fix.go`, `internal/vcs/propose.go` | Fixes already exist through `@open-nitpick fix` / `fix all`, with permission checks and a proposed change. There is no standalone `nitpick fix` command in the inspected CLI. Generated changes are explicitly unverified. G1 adds optional check evidence, not a new fix product. |
| Thread lifecycle | `internal/review/engine.go:superseded`, `resolveClearedForApprove`, `internal/vcs/github.go:ResolveThreads`, `internal/review/incremental_test.go` | Auto-resolution already exists. G4 audits its safety and identifies actual missing transitions; it does not replace it with a line-diff heuristic. |
| Approval | `internal/review/approve.go`, `approve_residual.go`, `internal/config/config.go:Approve` | Opt-in approvals and completeness checks already exist. G6 preserves them. A new parallel `review.verdict` configuration is not proposed. |
| Accounting | `internal/review/deep.go`, `deepselect.go`, `tools/bakeoff/deep_stats.py`, `internal/evals/cost.go` | Agent usage is recorded and parsed already. G0 checks completeness across stages, attempts, and report output instead of building a second meter. |
| Knowledge | `internal/knowledge/entry.go`, `corpus.go` | Entries are sourced and dated; the shipped corpus and indexes are embedded. Repository feedback storage and activation are new work, not an existing writable corpus. |
| Execution trust | `docs/trust-model.md`, `internal/linters` | Analyzer config isolation is not proof of a sandbox safe for arbitrary builds/tests. Running PR code needs a separate execution boundary. |
| Forge support | `README.md`, `internal/vcs/vcs.go`, GitHub and local implementations | GitHub and local are supported. GitLab is a potential adoption expansion, subject to demand and an integration environment. |

### What the measurements support

`docs/remediation-mira.md` documents an unresolved precision problem, not
"discovery is solved." Its historical aggregate matched 79 of 105 golden
comments across passes; this is not single-run recall and does not prove
that all unmatched comments are false. Sparse reference labels are a known
limitation. Do not call those historical unmatched comments a ready-made
false-positive training corpus or assume `deep_funnel.py` contains the data.

The D7c result is F1 0.172 against Mira's 0.130, difference +0.042 with
interval (-0.053, +0.136). It also exceeds the registered fallback limit
(10.1% versus 5%). That is not evidence of a win. Jev lowered F1 in the
reported comparison; preserve its opt-in status rather than generalizing
that result to every model and task.

The fresh corpus has 58 of 94 comments categorized as test, documentation,
style, or speculative, but **26**, not 58, are outside its core profile.
Changing the corpus to favor the tool's strengths is not a remedy. Report
full and prespecified profile-specific results, with their denominators.

### Competitive evidence limits

Earlier research covered eleven products, but its "10 of 11 have GitLab"
claim conflicts even with its own reports. Other feature counts, pricing
comparisons, and assertions about competitors having no provenance were not
established. They are withdrawn here, as are the Gitea-is-a-subset-of-GitLab
claim and the claim that local tools cannot support feedback or fix loops.

Useful vendor starting points are [CodeRabbit](https://docs.coderabbit.ai/),
[Greptile](https://docs.greptile.com/),
[PR-Agent](https://docs.pr-agent.ai/), and
[Cubic](https://docs.cubic.dev/). These are research pointers, not citations
proving a feature, deployment tier, or quality comparison. Before a vendor
claim drives implementation priority, retain its exact page, dated excerpt,
product tier, and limitations in a versioned evidence record. Temporary
`/tmp/competitor-*.md` reports alone are not durable evidence.

The proposed priorities rest on verified local gaps and the user's accepted
learning design. Market feature lists do not establish that execution checks
will improve precision, that a specific forge has user demand, or that a
competitor's fix loop works better.

## 2. Review standard and boundaries

A plan passes adversarial review when it has no unresolved blocker in:
source accuracy, execution safety, policy trust, evaluation independence,
publication side effects, or dependency consistency. Passing the plan does
not certify the implementation, approve spending, or establish review quality.

Work only on the plan in this task. Preserve other dirty files. For future
implementation, record the exact source/configuration snapshot and use an
owner-approved isolated worktree if necessary; do not require committing or
stashing someone else's work. Treat `RESUME.md` as potentially stale, not as
a reason to delete it. Updating it is a separate owner decision.

Use `docs/measurement.md` throughout. In particular:

- Separate development, calibration, and final evaluation data. Freeze
  policy, models, prompts, stage settings, scoring, and thresholds before
  the final evaluation. Timestamp and hash the registration and inputs.
- Evaluate paired arms on the same PR revisions and declared coverage. Count
  failures and abstentions; never silently remove them from the denominator.
- Report deterministic evidence first. Human adjudication of ambiguous
  findings is blinded to arm identity. Model judges are supporting evidence;
  disclose disagreement and uncertainty rather than voting away failures.
- For quality comparisons, use three repeated passes per arm and paired
  PR-level intervals, clustering by repository for pooled multi-repository
  claims. Replicates are not independent new PRs. Choose sample size from
  development variance and the intended effect, not an invented fixed
  "corpus resolution" such as eight percentage points.
- **Pass:** prespecified safety and quality conditions are met.
  **Fail:** a safety invariant breaks or adequate evidence shows a condition
  is not met. **Inconclusive:** incomplete, underpowered, or overlapping
  results. Fail and inconclusive both mean no promotion. Further tuning
  requires a new development cycle and a new final holdout, not a rerun
  until the same holdout looks favorable.
- Guard tests must fail under mutation (Rule 9). Empty results must assert
  why no work or findings remained (Rule 10). Zero unsafe actions is not a
  useful result if no action was eligible: exercise positive and negative
  controls and report both counts.

Exact live-evaluation sample sizes and spending ceilings remain approval
items. Until a versioned protocol supplies them, only source audits, fixture
work, and mocked experiments may proceed. New capabilities remain opt-in
until their stage gates pass; current defaults are not changed by this plan.

## 3. G0 — Audit existing accounting and freeze the baseline

**Purpose:** establish what each run costs and what actually ran before
buying more evaluations. This is an audit of existing meters and reporting.

Trace usage from agent, forced-submission, critique, verification, triage,
Jev (when enabled), and retry/fallback calls into retained artifacts. Include
model/provider/role, attempts, reported tokens, cached/reasoning-token
semantics, failed calls, elapsed time, and completeness. Do not double-count
when roles share one model. A failed call without usage is unknown, not free.
Report product, fix, execution, embedding, and judge costs separately.

**Acceptance:** scripted multi-role/retry cases reconcile with the retained
request records; missing usage and unknown prices remain explicitly unknown.
Existing logs are used to reconstruct historical results only where they
contain sufficient evidence. Never fill missing history with estimates
presented as measured cost. Tests cover shared model IDs and missing usage.

Also record the exact artifact locations and hashes needed for later audits.
If the D7c artifacts cannot be recovered, disclose that and do not promise
an offline reanalysis of them. No live rerun is authorized by this stage.

## 4. G3 — Operator-approved, repository-scoped learnings

**Chosen design:** draft knowledge entries with citations, reviewed and
accepted by an authorized operator. This is the user-approved learning shape.
It is not automatic suppression of comments or cross-repository learning.

### Signals and trust

Thread replies, dismissals, and fixes are possible evidence, not truth labels.
Resolution alone does not distinguish "fixed," "accepted risk," "false
positive," and "no longer relevant." Require an explicit rationale or ask
for operator classification. A valid finding that was fixed must not become
a rule suppressing that defect class.

Exclude the bot's own resolutions and generated replies as independent
feedback. Author replies may supply context, but neither repository
association nor a self-reported author name grants policy authority. Verify
acceptance against an authenticated operator or current forge write/admin
permission. Even a maintainer's comment remains untrusted input to drafting.
Citations establish origin, not permission to execute their instructions.

The drafting path has no shell/forge-write tools. Treat all thread text as
quoted data. Redact secrets, bound input and output, and obtain operator
consent before sending private feedback to a configured model provider.
Accept only entries that the operator has actually reviewed, not an
unreviewed batch selected by a model.

### Storage, activation, and removal

Reuse the knowledge entry vocabulary where it fits; do not mutate embedded
`internal/knowledge/corpus` or its shipped indexes at runtime. Add one
repository-scoped overlay with a versioned manifest. Proposed storage is a
tracked `.nitpick/learnings/` directory, loaded from the trusted base revision
for hosted reviews, alongside the existing trusted policy resolution path.
This path is a proposal, not an existing configuration feature.

Each entry records a stable ID, body/version hash, repository identity,
path/language applicability, source URL and source revision or excerpt hash,
evidence date, rationale, approving identity, and acceptance revision. Drafts
stay outside active policy; acceptance requires a reviewed policy change
landing in the trusted base. Never allow a candidate PR to activate its own
learning. For local reviews, resolve the same trusted policy revision; if
none is available, leave the overlay inactive with a notice rather than load
unreviewed working-tree entries.

Accepted content must not override security/trust policy, modify severity
thresholds, or grant blanket suppression. Use the existing reference-data
prompt channel, not a new system-instruction channel. Retrieval is bounded
by applicable scope and token budget. Test the interaction with the existing
embedded corpus so accepted references do not silently disappear when its
retrieval is configured differently. Malformed active entries fail visibly;
empty, disabled, or out-of-scope overlays state that reason.

Start with explicit scope filtering; do not require new embeddings for this
first version. If embeddings are added later, update retrieval caches by
content/policy revision and account for their cost. Editing, revoking, or
deleting an entry invalidates active retrieval state for subsequent reviews.
A running review is pinned to its start revision; do not silently alter its
policy mid-run. Retain a minimal audit trail without copying secret-bearing
thread bodies into permanent logs. No automatic propagation to another repo.

### Evaluation and acceptance

For repository-scoped policy, **train from earlier feedback and test on later
PRs in the same repository**. Choose the cutoff before drafting. Exclude
thread continuations, cherry-picks, near-duplicate changes, and PRs sharing
the source incident across the split. Freeze the accepted entries and their
scope before reading later PR outcomes. Retain draft-to-accepted diffs and
operator editing time so any gain is attributed to the operator-assisted
workflow, not to automatic extraction alone. Use multiple repositories for a
product-level claim, applying each repository's own overlay only to itself.
Transfer to repository B is a separate opt-in generalization study, not a
requirement for repository A's local conventions to be useful.

Compare baseline and overlay on the same later PR snapshots. Count applicable
and retrieved entries, actionable defects located, noise per PR, rejected
drafts, operator review time, cost, and failures. Include negative controls
for unrelated repositories/paths and guards against suppressing true defects.
A rule never retrieved is not evidence that it reduced noise.

**Prototype gate:** provenance, permissions, base-revision isolation,
revocation, private-data handling, and positive/negative retrieval controls
pass deterministic tests with scripted drafts. This enables a limited opt-in
pilot, not a claim of quality improvement.

**Proposed promotion gate:** the paired 95% interval for noise-per-PR change
is entirely below zero, and the lower bound for recall change is at least
-0.03 (three percentage points). Report per-repository outcomes; no interval
or insufficient applicable cases means inconclusive. The three-point margin
is a proposed tolerance, not a measured fact, and needs owner approval in the
frozen protocol. Rejection rate and operator time are reported as costs,
not assessed against an undefined "unacceptable" threshold. If the owner
wants a labor-budget gate, set it before the final run.

## 5. S0 — Execution safety prerequisite for G1 and G2

A compiler, test runner, dependency installer, build hook, or generated test
can execute attacker-controlled code. Stripping credentials from an environment
or using an ordinary worktree is not sufficient isolation. Current analyzer
handling must not be described as an established arbitrary-code sandbox.

Before any real execution, select and review one supported isolation backend.
Use a disposable environment with an immutable input snapshot and:

- No forge/model credentials, SSH agent, host home, host sockets (including
  Docker), writable host mounts, or shared writable dependency caches.
- Network disabled during execution, including metadata/private-network
  access. Dependencies must be staged separately under an operator-approved
  policy, with no repository hooks executing in the credentialed fetch job.
  Use pinned toolchains and isolated cache copies. Unsupported private
  dependencies produce unavailable checks, not weaker isolation.
- Unprivileged execution with CPU, memory, disk, process, output, and wall-time
  limits; containment for descendants, symlinks, and archive/path traversal.
  The backend's host/kernel assumptions and residual risks must be documented.
- Trusted command profiles, not arbitrary commands from findings or the PR.
  Commands, tool versions, policy, tree hash, and output hashes are retained.
  Sandbox output is untrusted data; redact secrets and never execute it in
  the credentialed publisher.
- A separate publisher verifies job identity, snapshot, policy digest, and
  current head before accepting results. Untrusted code cannot forge a
  successful control-plane result by writing a JSON file or printing a marker.

**Safety gate:** adversarial fixtures attempt credential access, network
exfiltration, host writes, cache poisoning, resource exhaustion, result
forgery, and cancellation escape. Positive controls must genuinely run.
If the boundary cannot be supported and tested, G1/G2 remain design-only;
never fall back to executing code on the credentialed review runner.

These tests establish the declared boundary, not a proof against all sandbox
escapes. An operator must approve its residual risk before the pilot.

## 6. G1 — Attach check evidence to the existing fix proposal

Keep the existing `respond` fix flow, authorization, fork restrictions,
explicit fix-model selection, and head-movement protections. Do not change
inline suggestion rules globally or add a standalone command for this stage.
Run at most one proposed patch and its selected checks per request. This is
a cost/scope choice, not a limitation requiring a cloud backend to remove.

After S0, check the original head and candidate patch under the same trusted
profile and environment. Record baseline failures separately from new failures.
Bind the result to the candidate tree and patch hashes. Before publication,
refuse a stale result if the PR head moved; reuse existing proposal guards.
Keep verification orchestration outside `internal/fix/fix.go:Apply`, which
generates file contents and has no checkout or publication responsibility.

Outcomes are `checks passed`, `checks failed`, and `unavailable/incomplete`.
Passing selected checks is not a claim that the fix is correct. The proposal
body names exactly which checks ran and what was not checked. Unchanged
baseline failures do not count as clean passes. Do not publish a failed or
incomplete patch as a checked fix; leave the existing explicitly unverified
proposal behavior behind its existing permission policy, or let an operator
require checks and decline publication. No auto-merge or self-approval is added.

**Acceptance:** lifecycle tests prove snapshot binding, genuine check execution,
no-op/skipped fixes, baseline failure reporting, cancellation, and stale-head
refusal. Mutation must make each publication guard fail its test.

**Quality evaluation:** first inventory a fix-specific corpus with runnable
preconditions, known behaviors, broken and repaired controls, and clean cases.
The old 44-PR/41-plant benchmark is not 41 executable fixtures with reference
patches. Where appropriate, extend fixtures with independently checked tests.
Judge behavior, not textual equality to a single patch; valid alternative
fixes must count. A defect-specific test must fail for the claimed reason
before repair and pass afterward. Guard tests run independently of tests
altered by the fix. Count eligible findings, attempted fixes, abstentions,
applied patches, check passes, semantic repairs, and regressions. Include
no-op and harmful-but-compiling controls. Report end-to-end defect coverage
as well as success conditional on the reviewer emitting a finding.

Use development fixes to calibrate, then freeze acceptance thresholds for
behavioral success, introduced defects, abstention, and useful coverage before
the final holdout. Those numeric thresholds are an explicit unresolved input;
no quality promotion or "reliable fixes" claim is allowed until they are set
and met. A check-reporting pilot can pass lifecycle safety without asserting
fix quality. Model calls and human adjudication are not free.

## 7. G2 — Test whether execution evidence improves finding selection

Start with an offline feasibility audit, before building extra integrations
or spending on a live comparison. Use retained drafts only if G0 locates the
artifacts. Adjudicate a stratified sample of matched and unmatched findings
blind to the proposed filter. Unmatched does not mean false. Record confirmed
true/false, uncertain, and unassessable cases, and the concrete experiment that
could confirm or refute each claim. Record sampling fractions; weight estimates
for the source population or label them sample-only when strata are oversampled.
A human approves the mapping; a model may assist but may not certify its own label.

Classify checks as existing analyzer evidence, an existing relevant test,
a new isolated reproducer, or no available test. Ordinary tests and linters
cannot refute arbitrary behavioral claims by returning clean output. Avoid
re-running the same analyzer evidence already consumed by the reviewer unless
its scope or revision differs and is recorded.

A diagnostic upper bound for suppressing only known false findings is:

`precision_ceiling = T / (T + F - R)`

where T and F are adjudicated true and false counts, and R is the subset of F
for which a valid refutation could be produced. It assumes no true finding is
lost and that every proposed refutation succeeds. It is optimistic, not a
promotion result. Unknown labels are reported separately with sensitivity
bounds. There is no fixed 30% rule and no proof from a small sample that the
whole product cannot benefit. Insufficient reach can stop a broad experiment
or motivate a separately registered narrow one, not force an architecture rewrite.

After S0, preserve read-only verification and add optional execution evidence
at its orchestration boundary. Evidence states are **supports**, **refutes**,
and **inconclusive**, each tied to an exact claim, code revision, test
preconditions, and scope. A compiler failure supports only a matching compile
claim, not every finding in the package. A passing suite, missing diagnostic,
timeout, dependency failure, or unsupported environment cannot silently
remove a finding. Refutation needs a documented claim-specific implication;
otherwise retain the finding under the existing policy and disclose limits.
New reproducer code is untrusted and receives the same sandbox restrictions.

**Evaluation:** first replay a fixed draft pool to isolate selection from
generation variance, then compare end-to-end baseline and execution-enabled
arms on a new final holdout. Declare whether the experiment targets standard
or deep review; one mode's result does not establish the other's. Keep
concurrent baseline settings identical except for this stage. Retain true
finding losses, false finding removals, inconclusive rates, execution cost,
and latency. Include clean/contradictory controls and cases where all checks
are inconclusive, so an always-silent filter cannot pass.

**Proposed broad-promotion gate:** the lower bound of the paired 95% interval
for precision gain is at least +0.10; the lower bound for recall change is at
least -0.03. These ten/three-point thresholds are proposed design targets,
not demonstrated achievable gains; approve them before the final run. Empty
output makes precision undefined, not perfect. An underpowered result is
inconclusive. A useful narrow class requires its own frozen scope and gate;
it cannot be selected after seeing the final results and declared a win.

Failure does not prove that a new generator architecture is necessary. Record
what was tested, preserve baseline behavior, and decide the next hypothesis
separately. G6 is not conditional on this experiment succeeding.

## 8. G4 — Audit the existing thread lifecycle

Do not implement duplicate auto-resolution or a commit-fingerprint lookup.
Existing fingerprints identify findings, not commits. Current resolution
uses prior review state and recurrence in a subsequent review; audit its
coverage and failure checks rather than replacing them with text matching.

Build history fixtures for genuinely fixed and still-present defects,
whitespace/rename/move-only edits, semantic refactors that preserve a bug,
force pushes, skipped files, partial reviews, withheld findings, missing
history, and forge mutations that return success without resolving a thread.
Trace the guards at call sites as well as inside `superseded`.

**Acceptance:** successful eligible resolutions and conservative non-resolutions
both occur in tests. No unsupported resolution is observed in the explicit
suite; publish its denominator, not a claim of zero production risk. Preserve
manual ownership and operator settings. Failure or lack of evidence leaves
the thread open; text changes alone do not demonstrate a fix. Idempotence
and API-confirmed resolution are required.

Reopening a closed thread is not assumed supported or desirable. Inventory
that transition separately. When a defect recurs, specify whether current
policy produces a new finding or a supported reopen; never invent an API or
silently reopen a maintainer-dismissed thread. Changes need their own scoped
proposal if the audit identifies a real gap.

## 9. G6 — Audit existing approvals; defer new merge policy

Preserve `review.approve`, residual approval policy, and `review.fail_on`.
The CLI exit gate, GitHub review event, and branch-protection rules are
separate mechanisms. The `REQUEST_CHANGES` event type alone does not prove
that the engine selects it; inventory that path before proposing new behavior.

Exercise completeness, analyzer coverage, standing/withheld findings, prior
history failures, residual-judge failure, bot-authored fixes, and head changes
against the existing guards. A verified subset of findings is not evidence
that the rest of the PR is clean. Inconclusive G2 evidence cannot be used to
bypass approval completeness requirements. Never drop unverified warnings
from the decision merely because a verified subset is empty.

**Acceptance:** existing opt-in behavior and conservative failure paths are
pinned by positive/negative tests and mutation checks. No new approval mode
or blocking review default is shipped under this plan. An expansion to merge
policy requires a separate proposal, false-approval/false-block evaluation,
and owner approval; no dependency on a G2 precision claim can replace that.

## 10. G5 — GitLab feasibility, then a bounded provider

First confirm an interested user and a disposable GitLab self-managed test
instance. Use the existing VCS interfaces to map gaps and GitHub-specific
command wiring. Define the first supported GitLab version and credential
model. Scope a vertical slice: fetch an MR's pinned revisions and complete
diff, produce an advisory review, and post safely anchored discussions.

Then add authenticated CI/event handling, target-revision policy loading,
pagination and truncation detection, stable finding identity, incremental
review, and authorized respond commands. Fork source/target identity,
force pushes, renames, concurrent head changes, duplicate events, rate limits,
and permission errors must be covered. Forge API endpoints may be operator-
configured for self-managed instances; PR content must not redirect credentials.

**Acceptance:** contract tests plus a real disposable MR demonstrate normal
and failure paths. Each unsupported capability is explicit and cannot be
reported as a complete review or an approval. Request only needed permissions;
preserve GitHub/local behavior. Fix writes and approvals on GitLab remain
out of the first slice, rather than pretending every GitHub feature has parity.

Re-estimate after the slice. Gitea and Bitbucket are separate demand decisions,
not automatic follow-ups; neither API is assumed equivalent to GitLab's.
Hosting, CI, and operator time are costs even when no model call is needed.

## 11. G7 — Optional summary in the PR body

Low priority; reuse the existing walkthrough rather than buying a new model
summary. Opt-in only. Append or update only a uniquely marked bot-owned block;
never replace author text or treat similar markers in untrusted text as
permission to delete arbitrary content. Escape markup that could terminate
the block. If markers are ambiguous, leave the body unchanged with a notice.

**Acceptance:** tests cover a populated description, duplicates, bot-block
updates, user edits, permission failures, and idempotent retries. Re-read
before updating; if the body changed, decline instead of overwriting it.
For a forge without atomic conditional updates, read-then-write still races:
keep the existing comment-only output and defer body editing unless a safe
conflict-handling mechanism is available. No new model-cost claim is made;
API calls and integration tests still cost time.

## 12. G8 and other deferred work

Ticket context needs demand, permission-scoped retrieval, injection boundaries,
and its own acceptance-criteria corpus. Defer it. Diagrams, docstrings,
dashboards, analyzer-count parity, cross-repository context, and repeated
fix loops are also deferred for scope and lack of validated need, not because
a vendor allegedly retired them or because a local tool cannot implement them.
The LSP experiments do not settle cross-repository context's value.

## 13. Proposed D8 — A new deep-review evaluation, not yet registered

D8 is a proposal here; it is not registered in `docs/remediation-mira.md` and
must not be described as an executed or approved stage. G0 supplies accounting
for D8 and G2/G3 live comparisons. A shared evaluation protocol may serve all
three, but their final holdouts cannot be reused for tuning another arm.

Before a new run, freeze a representative sampling frame independently of
reviewer outcomes. Keep clean PRs and the declared full distribution; label
profiles for stratified reporting rather than discarding inconvenient cases.
Use blinded adjudication, unknown labels, incident/duplicate separation, and
predeclared matching rules. Audit missing-label bias through blinded grading
of a sample of unmatched findings from every arm. State label uncertainty.

Compare D7c and a contemporaneous Mira baseline under fixed settings, three
passes per arm, with G0 accounting. If adopting the historical 5% forced-
submission fallback limit, record its exact numerator and denominator and
apply it symmetrically; show invalid runs but do not rank them. Historical
intervals are not a target to fit new labels to.

Register sample size, primary endpoint, minimum meaningful margin, confidence
procedure, cost limit, and fallback rule before evaluating. Those remain
owner approval items. No win or generator rewrite is automatic on the basis
of an ordering. An inconclusive run is reported as such; a generator change
would be a new hypothesis with its own budget and new evaluation data.

## 14. Order, budget, and decisions

1. G0 and source audits G4/G6 first; preserve existing behavior.
2. G3 prototype next: the user-approved learning design can be tested with
   scripted drafts before a paid temporal-holdout pilot.
3. G2 offline feasibility can run alongside G3. It does not depend on G1.
4. S0 is mandatory before G1 or G2 execute code. Both may reuse that one
   executor, but fix evidence and finding refutation are distinct workflows.
5. G5 starts only with demand and a test instance. G7 is optional and subject
   to a safe update API. G8 remains deferred.
6. D8 and any G2/G3 live evaluation wait on G0, approved protocols, datasets,
   and explicit spending authorization. No research loop runs without a cap.

The previous 33–40-day total and under-$300 promise are withdrawn. No staffing,
sandbox backend, runnable fix corpus, provider rate snapshot, sample sizes,
or number of attempts justified them. Even the earlier subtotals disagreed.
A defensible estimate follows the feasibility audits; none is a delivery promise.

For each proposed live run, budget using actual model/rate selections and
pilot usage, then report provider-reported usage afterward. Include generator,
fix, drafting, embedding (if any), checker, retries, and judge calls separately,
plus execution/CI compute and human labelling time. Missing rates or usage
remain unknown. A pause at the approved ceiling is incomplete, not clean.
Review/judge totals must not be combined into cost per production review.

**Decisions still required before implementation or spending:** overlay path
and local policy fallback, authorized acceptance workflow, numeric final
fix-quality thresholds, learning/selection tolerances and sample sizes,
isolation backend and residual risk, GitLab demand/version/instance, and
per-stage spend limits. These are explicit stop conditions, not implicit
permission to choose convenient values after seeing results.

## 15. Adversarial review record

This revision addresses blockers in the previous plan: duplicate thread and
approval features; incorrect accounting claims; unsafe execution assumptions;
confusing passing checks with proof/refutation; cross-repository testing of
repository-local learnings; post-result thresholds and unsupported ceiling
math; unsafe body updates; unsupported competitor counts and cost promises.

Review method: independent source/safety and measurement reviewers first
reported blockers, then reviewed the rewritten plan. Both returned PASS for
the proposed-plan scope. A final delta check covers the added fix-quality
controls, operator-edit provenance, and stratified-sampling caveat.

The pass is limited to planning: no remaining blocker was identified in the
specified review scope. The explicit approval items in section 14 still stop
implementation or live evaluation where stated. No implementation stage is
complete, no production safety claim is established, and no paid evaluation
was run during this review. Only `revised-plan.md` is edited; no commit is made.
