# A path to beating Mira clearly

Status: proposed experiment program, not a claim of superiority or permission
to spend. This is a focused companion to `revised-plan.md`; it does not replace
that plan's execution safety, feedback trust, or approval constraints.
No implementation, live evaluation, or model training was performed to write it.
Source basis: the inspected working tree at HEAD
`6b6b108bb6db0a7e9a896c449c89ae9d08a2402a`, including the execution and learnings
work in progress. These are architectural footholds, not features validated
by this design review. Pin a fresh exact build before any experiment.

## Thesis

Do not try to win by adding another confidence threshold or copying more of
Mira's prompt. Test whether open-nitpick can investigate more real defects per
unit of reviewer budget, retain the ones it can substantiate, and publish them
without the runtime and selection losses documented in the current deep build.

The proposed advantage is **short, targeted investigations over before/after
behavior, with persistent evidence and explicit coverage**. This is a hypothesis,
not an established technique shown to beat Mira here. First prove the mechanism
on development data, then test a frozen candidate against Mira on new repositories.

## What the record changes about the strategy

Sources: `docs/bakeoff-mira.md`, `docs/remediation-mira.md`,
`internal/review/deep.go`, `deepselect.go`, and `docs/measurement.md`.

1. The old standard-review result and the newer deep result differ. The R1
   unmatched-comment sample was graded real in 8/30 cases; the later D1c deep
   sample was real in 20/30, compared with Mira's 11/18 in that second sample.
   These were small, unregistered model-graded samples with wide intervals,
   not human-adjudicated proof of parity. They do show why "all extra comments
   are noise" is an unsafe premise for more filtering.
2. On the tuning-set funnel, D1c drafted 33.3 labelled true findings per pass
   versus Mira's 34.5. D1c's initial filter withheld 27% of its true drafts
   versus Mira's 13%. That is a measurable candidate-retention problem, not
   evidence that merely generating more drafts will win.
3. Across the logged D7/D7c agent runs, 82% ended with forced submission.
   Failed forced attempts consumed about 65k tokens with no content. Reasoning
   step-downs correlated with long transcripts. This points to an experiment
   on work-unit size and result capture, not proof that context length alone
   caused every failure. Prose conversion, required tool calls, and per-turn
   token caps were already tried without improvement.
4. Current source also contains `internal/execution` and
   `internal/review/execution.go`: immutable snapshots, fixed check profiles,
   receipts, and operator-mapped claims. `RefutesBuildFailure` is deliberately
   narrow: a passed selected Go build can refute that exact mapped build-failure
   claim, not arbitrary behavior. Reuse and audit this boundary; do not propose
   an unrelated shell tool or assume the new source is already safety-certified.
5. Confidence floors, arbitrary comment budgets, more turns, more reasoning,
   stronger generic checking, and LSP caller enrichment have failed or failed
   to transfer in the reported settings. New work must name a different
   mechanism and a falsification test, rather than rename one of those stages.
6. D7c's +0.042 F1 point estimate has an interval crossing zero and exceeds
   its fallback validity limit. It is a useful development baseline, not a win.

Discovery is not "solved," and the next architecture is not guaranteed to
solve precision. The aim is to make the remaining uncertainty testable.

## Define "handily" before the run

Proposed targets for one frozen final candidate, compared contemporaneously
against a pinned Mira release/configuration:

| Dimension | Proposed clear-win gate |
|---|---|
| Overall quality | F1_U point gain at least 0.10; paired 95% lower bound at least +0.05. U is the common adjudicated inventory defined below. |
| Independent recall | Recall_R gain at least +0.10 absolute, with paired lower bound above zero. R is frozen before tool output, so the candidate cannot manufacture this gate by adding its own discoveries to the labels. |
| Precision | Actionable claim precision P_U at least 0.75 and paired lower bound for its difference at least -0.02. Recall cannot be bought with unbounded wrong claims. |
| Recall-weighted quality | F2_U gain at least 0.08 in each pass, retaining the earlier program's recall-weighted ambition but with explicitly new labels. |
| Critical/error regressions | No independently confirmed newly missed critical/error case in the mandatory guard set. Report per-class results and uncertainty, not a universal zero-miss claim. |
| Operational validity | No hidden failures or silent scope reduction. The historical reasoning-off/forced-submission rate must remain at most 5% where that protocol applies, and no more than 5% of attempted PR reviews may suffer any unintended reasoning downgrade or incomplete planned unit. Both tools must meet the registered rule. |
| Resource value | Same predeclared per-PR cost/time ceilings; candidate mean measured product cost per attempted PR no higher than Mira's, median latency no worse, and p95 latency at most 10% higher. Report uncertainty and cost per distinct located defect. |

Report PR-, work-unit-, and call-level degradation denominators, including
zero-eligible cases. Removing forced submission does not turn missing results
into successful reviews. A planned matched-model reasoning setting is not a
fallback; an unintended lower-effort retry is. Define this mapping for both
architectures before the run, and disclose any change from the historical
protocol rather than claiming its 5% condition was met under a new denominator.

These margins are proposed design choices, not derived performance promises.
The original registered target required F1 and F2 +0.08 in every pass,
precision at least 60%, median time no worse, and no lost guard plants. This
proposal strengthens the F1 uncertainty and absolute precision requirements
but changes its pass-level treatment. It is not a retrospective amendment:
the earlier gate remains the recorded target unless the owner approves this
new one, and no result here may be called a pass of an unchanged old protocol.
An illustrative target, not a prediction: precision 0.75 and recall 0.65 gives
F1 about 0.70; precision 0.73 and recall 0.50 gives about 0.59. That is the
kind of separation sought, not a few hundredths on one reused corpus.

Require a positive F1 difference in each of the three passes, not only in
its pooled result. Also cap the increase in incorrect-or-redundant comments
at 0.10 per attempted PR relative to Mira. These are proposed acceptance
constants; latency, noise and cost must include failed attempts. Exact cost
enforcement uses conservative per-call reservations when usage arrives only
afterward; actual overruns remain visible resource-envelope failures.

Measure clean-PR comment rate and review completeness alongside the primary
metrics. Empty output does not have perfect precision; an always-silent arm
fails coverage and recall. Unknown cost cannot support an efficiency win.
A quality gain at higher spend is useful but fails this clear-win value gate.

### Separate three comparisons

1. **Orchestration:** same reviewer model/provider for every reasoning role,
   same declared reasoning/temperature settings where supported, same available
   base/head source and information policy, same resource envelope, both without
   extra analyzer/feedback context. Verify actual API arguments, not just model
   environment variables. Batching, tools, context selection, call allocation,
   and prompts remain the architectures under test. Unmatchable model settings
   make it a same-model product comparison, not isolated orchestration.
2. **Product:** each tool's frozen supported configuration within the same
   envelope, with the same underlying repository/history/dependency inputs
   available. Tooling may exploit them differently. This asks which product
   a user should run; a better model mix belongs here, not in an orchestration claim.
3. **Cost-quality curve:** a few budget points selected on development data.
   A win at twice the cost is reported as such, not as a free quality gain.

Both tools get a comparable development tuning allowance. Do not compare a
heavily tuned open-nitpick to an intentionally weak Mira configuration. Pin
versions and provider routing; record upstream changes during the experiment.
Old results are context, never the new run's opponent.

## Proposed review architecture

### 1. Describe the change before inventing defects

Build a bounded map of the changed behavior from base and head snapshots:
changed function/interface, before/after condition, relevant callers/consumers,
tests or documented contracts, and source spans. Reuse current diff/context
collection; do not require a new global graph or LSP rollout.

Group related changes into small investigation units. File count alone is not
the unit: a helper and its consumer may be in different files. Keep a compact
PR-wide view and explicitly schedule cross-unit interaction checks. Every
eligible changed file is assigned, skipped with a reason, or marked uncovered.
A narrow unit is not permission to lose a cross-file defect.

This stage is not a hard "low risk, skip it" classifier. It organizes work;
it cannot silently suppress review of apparently simple changes.

### 2. Persist candidate evidence during investigation

Replace dependence on one large final `submit_findings` response with
incremental, host-validated candidate updates. Persist:

- the specific changed behavior and claimed consequence;
- trigger or supported input condition, including public API contracts when
  no concrete in-repository caller exists;
- exact base/head spans, consulted definitions/tests, and their revision;
- counterevidence already checked, unresolved assumptions, and next lookup;
- status: hypothesis, supported, refuted, or still unresolved.

Source-span validation proves where a claim came from, not that it is true.
Model-authored status is not a ground-truth label. Partial updates survive
later model/format/time failures; they are not automatically published.
Use a bounded final serialization over retained records, not a renewed full-PR
reasoning pass. If evidence is missing, mark the result incomplete rather than
silently converting lost submissions into clean reviews.

Retain references and raw source slices outside the conversation. A summary
must never be the sole source of truth after a context reset. Re-fetch a pinned
span when the next investigator needs it; record failed or truncated retrieval.

### 3. Spend turns answering a concrete uncertainty

Dispatch each local investigation with one question and enough surrounding
control flow to answer it. Examples of general tasks, not benchmark plants:

- Does the alleged violation already exist at base, or is it introduced here?
- Does a callee's actual merge/default/error contract contradict the claim?
- Is the failing path reachable under the documented input contract?
- Is another part of this PR responsible for handling the changed behavior?

The investigator reads base/head and the relevant contract before producing
its conclusion. It can widen scope or return unresolved; it cannot fabricate
an absent caller or treat a missing grep hit as proof. Absence of a known caller
also cannot suppress a valid public-contract defect.

This differs from R5's general tool loop and a generic "is this finding real?"
checker: task decomposition, context lifecycle, and persisted intermediate
results change. Merely adding these questions to the old prompt is not this
experiment. Keep a control arm that does exactly that, to test the distinction.

### 4. Select using claim evidence, not self-reported confidence

Start the new selector in shadow mode on a fixed candidate pool. Do not add
another aggregate confidence floor. Deduplicate by underlying defect while
preserving independent consequences when they are actually different defects.

A claim-specific contradiction can support withholding a draft. A confirmed
failure path can support publishing it. Missing context or inconclusive checks
remain unresolved, with an explicit publication-policy decision recorded.
During the first comparison, do not change how unresolved findings are treated
relative to baseline; otherwise an apparent gain may just be a new suppression
rule. A stricter abstention policy needs a separate recall-loss ablation.

Measure true candidates retained and wrong candidates removed at each stage.
Do not optimize solely for agreement with sparse old golden comments. Keep the
normal report's provenance, completeness, and withheld-finding disclosure.

## A finite experiment ladder

### E0 — Instrument and label the losses

Use the existing meters and `deep_stats.py`; audit coverage as G0 specifies.
Locate retained artifacts before promising replays. On development PRs,
attribute losses to discovery, serialization/runtime, selection, anchoring,
and publication. Blind-adjudicate a sample of matched and unmatched comments
from both tools; do not label every unmatched comment false.

Produce an attainable-gain estimate: if every recoverable candidate in a stage
were rescued without extra noise, could that stage supply the proposed margin?
Report overlap between loss causes; do not add their upper bounds as if they
were independent. An oracle estimate is a ceiling, not evidence of achievement.

Also test the explicitly untried cheaper reasoning setting on development
traffic as a bounded diagnostic. Match that setting on both tools for an
orchestration claim; changing it only on ours is a product-setting experiment.
If it simply trades recall for speed, reject it as a quality solution. Lower
compute cannot itself be claimed to beat Mira.

**Stop:** if labels, artifacts, or accounting cannot support a paired comparison,
repair that measurement before more architecture experiments.

### E1 — Remove terminal-submission fragility

This is primarily a reliability/cost hypothesis. Empty reasoning before any
lookup or candidate update cannot be rescued by persisting candidates. E0
must show recoverable losses before this becomes a claimed quality lever.

Keep the existing discovery prompt and selection policy. Change only result
capture to incremental candidate updates plus bounded serialization. Test
restart, duplicate update, stale-span, cancellation, malformed submission,
and early-stop cases with deterministic clients before any live pilot.

Then compare on development PRs at fixed resource limits. The desired mechanism
is fewer lost candidates and wasted forced-submission tokens, not a shorter
transcript that happens to discover fewer defects. Count all attempts and
recovered candidates; normalizing a fallback must not hide its cost.

**Advance:** capture integrity holds, lost/submission-failure work decreases,
and actionable finding yield is not reduced beyond the prespecified development
noninferiority margin. Freeze that margin before running the pilot.
**Stop:** if loss is mostly earlier reasoning failure, not final serialization,
do not keep extending the capture feature in pursuit of a quality gain.

### E2 — Discover producer/consumer contradictions, then investigate narrowly

Before building the planner, run an oracle-evidence feasibility test. On a
held-aside development set of real behavior changes and matched safe controls,
an engineer specifies the exact before/after contract facts and consumer path
conditions an ideal extractor would supply. Do not include the defect verdict
in the evidence. Compare baseline, prompt-only questions, and oracle evidence
at equal budgets. If even accurate evidence cannot rescue defects without
inventing them on safe controls, stop before building more retrieval machinery.
This diagnostic establishes a mechanism, not natural prevalence or a Mira win.

If it works, automate one checkable property in one language first. For example,
use typed Go producer summaries to detect a changed return-value/error relation,
then inspect unchanged consumers whose guards relied on the old relation. The
output is a specific feasible contradiction: changed return path, consumer
guard/use, supported input, source revisions, and uncertain edges. A valid
signature must be preserved so this is not LS2's near-ceiling compile/signature
case. Safe controls include guarded consumers, unreachable paths, and deliberate
contract changes with callers migrated.

First check whether an existing analyzer already supplies this evidence.
Name-based call graphs and LSP references alone are not proof of feasible
behavior. Interface/alias uncertainty must remain explicit. Typed constraints
need not prove project intent; the investigator still checks whether the
claimed invariant was actually required.

Measure natural opportunity on a representative development sample. A narrow
Go-only check cannot support a multilingual clear-win claim if its prevalence
and incremental catches cannot supply the required overall margin. Do not expand
to several languages merely because it succeeds on hand-picked plants.

Using E1's artifact handling only if it passed its gate, compare the existing
whole-batch loop with these evidence-backed contract units plus explicit
cross-unit coverage. Otherwise compare against the unchanged capture path;
E2 does not require a failed E1 to be retained. The bounded context is useful
only if it carries better evidence, not just a narrower version of the old prompt.

Add a prompt-only control: identical questions in the old loop. Hold total
resource ceilings fixed; local agents do not each inherit a full PR budget.

**Advance:** fewer unsupported/pre-existing/contract-misread claims, no material
loss of cross-file defects, and an improvement over both baseline and the
prompt-only control that exceeds development uncertainty.
**Stop:** if benefit is explained only by extra calls or by dropping hard work,
reject the architecture claim. Report a separately measured cost trade if useful.

### E3 — Claim-specific counterevidence, not another generic critic

Replay identical drafts through baseline selection and the new evidence tasks.
Before a live run, test the selector on labelled positive/negative controls:
real public-contract violations, wrong callee assumptions, pre-existing bugs,
partial evidence, and unrelated test/linter success.

Require an independently inspectable evidence artifact, not just a second
model's verdict. First test human-prepared correct evidence on real/safe
contrasts to establish an oracle ceiling, then measure automated retrieval
against it. Wrong summaries and missing path conditions are extraction errors,
not invitations to tune the critic harder.

For executable claims, extend the existing fixed-profile execution path rather
than add arbitrary shell access. A proposed witness must assert a requirement
from an existing test, documented contract, or supported consumer assumption.
Run it on pinned base and head and, where valid, a narrow revert/repair control.
Require path coverage and assertion-sensitivity controls; repeat flaky cases.
Base pass/head failure is not a defect if the behavior change is intentional.
Failure on both revisions is not evidence of a new regression. No-witness
findings remain eligible for read-only investigation, not automatically discarded.
This execution branch cannot begin until the S0 safety requirements are audited
against the current backend and approved. An execution result is evidence of
only the scoped claim it actually tests.

Only after a selection benefit appears, run end-to-end reviews with the same
rule. This guards against a fixed-draft win disappearing when generation changes,
as earlier offline floor estimates did.

**Advance:** improved retained-true/removed-false tradeoff at the same resource
envelope, followed by an end-to-end improvement. Register numerical development
criteria before that experiment; publish all results, not only the best rerun.
**Stop:** if evidence tasks repeat the generic checker's errors, do not respond
by adding another critic or a stronger checker without a new causal hypothesis.

### E4 — One product-level advantage, only if needed

Choose one based on measured residual misses; do not stack all of them:

- **Analyzer evidence:** target actionable defect classes where reliable
  diagnostics add information not already supplied. Deduplicate and check
  changed-line attribution; do not win by flooding style comments.
- **Accepted repository learnings:** apply the G3 design to recurring false
  assumptions and local contracts, using only earlier accepted feedback.
  Useful for established repos; not a promised cold-start benchmark gain.
- **Targeted reproduction:** only for classes with a concrete testable claim,
  after S0's execution safety gate. Passing unrelated tests is inconclusive.
- **Model allocation:** use a stronger model for a measured hard task, with
  a development-fixed routing policy and a contemporaneous Mira comparison
  under the same cost envelope. More expensive checking already failed in
  one reported setting; model size is not the mechanism by itself.

This stage is a product comparison, not proof that the same-model architecture
won. A new model or signal needs its own development test and cannot be chosen
by inspecting final-test failures.

## Evaluation that can establish a win

### Development and final data

Keep Martian and all previously inspected F7/D-series data as development or
historical diagnostics. They cannot be restored to unseen status by a new seed.

Build a new sampling frame before running either tool. Include clean PRs,
small and large changes, multiple languages/repositories, and changed behavior
that requires cross-file context. Preserve the intended user's distribution;
report strata rather than choosing only those favorable to the candidate.
Split by repository and incident where practical. Exclude near-duplicates,
cherry-picks, and overlapping incident histories across development and final.

Use a small development pilot to estimate variance and labeling effort. Select
final sample size by power simulation for the declared margin with repository/
PR clustering. Three passes on 30 PRs are not 90 independent PRs. If the required
sample exceeds the budget, report that the program cannot establish a decisive
win at that budget; do not lower the target after seeing the final result.

### Ground truth and blinded adjudication

Define two explicit inventories:

- **R:** independently established actionable defects, frozen before tool
  outputs are visible. Use verified bug-fix history, relevant checks, and
  human inspection of pinned base/head revisions. Public comments are leads,
  not ground truth. Later commits are available to labelers, never reviewers.
- **U:** R plus every novel valid defect found in the pooled outputs of both
  tools and all registered passes, adjudicated blind to tool identity. U is
  frozen before arm scores are exposed, and shared by all arms and passes.

Two independent human reviewers assess claims with a fixed correctness and
changed-code-relevance rubric; disagreements receive adjudication. Audit both
matches to R and novel findings, not just a favorable sample of one tool's
unmatched output. Retain unresolved cases and source provenance. Independently
inspect a prespecified sample of PRs where neither tool reported anything;
report shared omissions rather than treating those PRs as proven clean.

Deduplicate only to save labeling labor. Restore every emitted claim for
scoring. Split multi-claim comments by a frozen atomic-claim rubric; additional
unsupported assertions cannot hide behind one correct claim. Within each PR
and pass, match each valid emitted claim to at most one defect and each defect
to at most one true positive. Repeated claims are redundant false positives,
not extra credit. Publish raw comments, atomic claims, duplicate counts, and
anchor widths so neither verbosity nor broad anchors can game the metric.

For the primary F1/F2 gate, compute P_U, Recall_U, F1_U, and F2_U using the
same common inventory U and match rules. Compute Recall_R separately against
R for the independent-recall gate. **Never combine precision credit for new
U defects with R-only recall denominators into a hybrid F1.** Report the
number and distribution of U-minus-R discoveries separately. U-based recall
is ascertainment-limited: even pooled and human-checked outputs do not reveal
all real-world defects. Both R and U results must be published.

Unknown labels remain unknown. Report adverse/best-case sensitivity bounds
under the registered treatment; if plausible unknown assignments reverse the
win, the outcome is inconclusive. Model judges may assist triage or provide
sensitivity analyses, not silently replace the primary human labels. If only
model grading is affordable, label the comparison as model-judged rather than
claiming human-validated superiority. Keep synthetic planted controls separate
from the natural-PR headline.

### Final comparison and outcomes

Freeze one candidate, opponent configuration, budgets, retrieval policy,
publication thresholds, matching rules, and the primary gate before unsealing
the final corpus. Run paired, interleaved reviews with three passes per arm;
record service outages, retries, coverage, timeouts, and spending. Every selected
PR is an attempted review in every pass, including failures and budget exhaustion.
An empty failed review contributes no true positives: all of that PR's U
defects remain false negatives for U metrics, and all R defects remain misses
for Recall_R. Each defect has one scoring opportunity per PR per pass. The
failed review has no perfect-precision value. Incomplete emitted claims
still count if actually published by the arm's declared policy. Print TP/FP/FN,
coverage and failure counts per arm and pass and pooled. Do not score only
successful reviews or use old Mira outputs as the new opponent.

Apply the clear-win gate to the declared primary comparison. Report the other
comparison tracks and per-repository/language results even when they disagree.
Use a prespecified repository/PR clustered bootstrap; fixed-corpus claims and
cross-repository claims need appropriately scoped intervals. Report all attempted
runs, not just a favorable pass. Publish artifacts and configuration hashes.

- **Pass:** every registered primary gate is met; scope the claim to that
  population, model/configuration, and budget.
- **Competitive but not decisive:** gains exist but fail the clear-win margin.
- **Inconclusive:** uncertainty, label quality, operational validity, or sample
  size prevents the claim.
- **Fail:** meaningful regression or no improvement under adequate evidence.

A second independently sampled cohort, with the same frozen candidate and
prespecified gate, is required before calling the result a durable general
advantage. The full frozen gate must pass separately on each cohort; pooling
them cannot rescue a failed cohort. Size and budget both before the first
final run. Failure on the first does not authorize tuning on the second;
it ends that final test.

## Scope and stopping discipline

No forge expansion, dashboard, diagrams, new approval behavior, or auto-fix
product work belongs on this critical path. They may be useful products but
cannot establish a review-quality win.

Bound development to the three architectural experiments E1–E3 and at most
one evidence-led E4 choice. Each gets one initial comparison and at most one
registered revision on development data. E0 diagnostics, corpus building,
labeling, and all evaluations also need explicit budgets. Stop at the approved
spend ceiling; missing data is not a successful review.

If the ladder yields no defensible quality gain, report it and stop. The next
option might be a learned reviewer/ranker trained on a separate evidence-rich
corpus, but that needs enough labels, licensing/privacy review, held-out
repositories, and a new budget. It is not the next automatic filter iteration.

## First deliverables and decisions

1. Corrected loss report separating the old R1 noise problem from deep-review
   candidate loss and sparse-label effects, with raw counts and uncertainty.
2. Exact paired-baseline manifest and a cost/coverage audit of retained runs.
3. E1 interface/fixture design and an E2 unit-boundary design with cross-unit
   coverage controls; no production default changes.
4. Registered development gates and a sampling/adjudication protocol, including
   the numerical E1–E3 thresholds, before any paid pilot.

Owner decisions: primary comparison track; approval of the proposed 0.10 F1,
0.05 lower-bound, 0.10 recall and 0.02 precision-tolerance targets; desired
repository population; access to independent adjudicators; wall-time/cost
ceilings; and total program budget. No prices or delivery dates are promised
without those inputs. Reuse G0 for measured accounting and S0 for any execution.
