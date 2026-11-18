# Whole-product performance plan — after the revised plan

Status: proposed follow-on program. Begin after the handoff gate below, not
during completion of `revised-plan.md`. Writing this plan does not authorize
implementation, model spending, data collection, or broader code execution.

## Purpose

Improve open-nitpick's performance as a whole across latency, token/compute cost,
completion reliability, coverage, and finding quality—ordered so the highest-
leverage, lowest-risk gains ship first.

This is the successor to `revised-plan.md` and the companion to
`mira-win-plan.md`. It consumes `revised-plan.md` infrastructure and reuses
`mira-win-plan.md`'s `E0–E3` quality ladder for `deep-review` rather than
duplicating it or blocking `G1–G3` studies behind a whole-product baseline.

Assumptions: self-hosting, model choice, base-revision policy, explicit
coverage, advisory defaults, `STRATEGY.md` non-goals (no standalone `fix`
command, no cross-repository learning), and operator control remain binding.

## 1. Handoff: what must be true before pickup

Read `docs/remediation-implementation.md`, `STRATEGY.md`,
`docs/guide/execution-evidence.md`, `docs/execution-boundary-decision.md`,
`docs/measurement.md`, `revised-plan.md`, and `mira-win-plan.md`.

The owner accepts a handoff manifest containing:

- Exact source/build snapshot, effective configuration, supported modes, tool
  versions, retained evidence locations/hashes, and rollback reference. Preserve
  existing dirty work; do not commit, stash, or discard another worker's tree.
- Final validation evidence (race suite, vet, lint, standards, docs, review
  disposition, and safety mutation guards). Open failures are blockers, not
  performance tasks.
- Each `revised-plan.md` stage marked **accepted**, **deferred**, **failed**, or
  **inconclusive**. Unfinished `G1/G2/G3` quality studies and `D8` remain
  explicit dependencies only for claims that rely on them—they do **not** block
  Phase 1 deterministic optimizations.
- Durable storage for **newly generated** baseline/promotion artifacts.
  Historical `d7c` and remediation artifacts currently recorded under `/tmp` in
  `docs/remediation-implementation.md` are accepted as historical context with
  their known limits (`d7c` lacks per-attempt metering and is not retroactively
  reconstructed as a complete cost baseline).
- Approved execution scope: the `runc` pilot covers hand-picked trusted
  snapshots only; arbitrary contributor PR execution requires a separate
  reviewed policy and backend.

## 2. Operating modes & shared scorecard

Report each implemented CLI/MCP surface separately (per `cmd/nitpick/main.go`
and `cmd/nitpick/mcp.go`); never merge modes into one aggregate score:

| Mode / Surface | Primary focus | Notes |
|---|---|---|
| `fast-review` | Early-feedback latency (p50/p95), top-10 yield, token cost | Must preserve eligible-diff coverage; currently pays 2x prompt tokens on non-clean batches (`engine.go:2067-2083`). |
| `review` | Balanced cost per valid defect, precision, recall, wall-clock | Standard PR review; baseline independent of `deep-review`. |
| `deep-review` | Quality/cost/reliability frontier (`mira-win-plan.md` `E0–E3`) | Currently suffers 82%+ forced-submission rate and 65k-token empty thinking loops (`deep.go:46-47, 414-478`). |
| `full-review` / `repo-score` / `improve` / MCP `code_smell` | Whole-tree coverage, memory/I/O scale, budget-exhaustion disclosure | No changed-line-only denominator; report covered/skipped/unreviewed counts. |
| `security` / `slop` / `repo-standards` / `standards` / `commits` | Deterministic scanner throughput & truthful completion | `standards` and `commits` are model-free; `security` must preserve required-scanner completeness. |
| `respond` (incl. `@open-nitpick fix`) / `learn` / `knowledge-index` / `mcp` | Response latency, embedding/index cost (`knowledge-index`), permission & cancellation safety | `@open-nitpick fix` is an authorized `respond` handler (`cmd/nitpick/fix.go`), not a standalone `fix` CLI mode. |

All modes share G0's accounting and the same seven scorecard dimensions:
**Correctness**, **Coverage**, **Speed** (p50/p95 across all attempted runs),
**Cost** (cost per distinct valid defect per `docs/measurement.md` Rule 13,
alongside cost per attempted review; unknown usage stays unknown),
**Reliability** (failures/aborted runs stay in denominators),
**Actionability**, and **Safety** (hard invariants, never traded for speed).

## 3. Reorganized phasing: highest leverage first

Phases are ordered by **impact-to-risk**, separating zero-policy deterministic
wins (Phase 1) from baseline profiling (Phase 2) and model/architecture work
(Phases 3–6).

```
Handoff gate
  ├──> Phase 1: Deterministic critical-path wins (no model/policy change; ship via replay + unit/bench gates)
  └──> Phase 2: Per-mode baseline & bottleneck census (reuses E0 for deep-review)
         ├──> Phase 3: Reliability & token-waste elimination (deep-review E1, fast-review 2x call, -resume)
         ├──> Phase 4: Evidence-driven quality & routing (mira-win-plan E2/E3, existing route/ensemble tuning)
         ├──> Phase 5: Complete G1/G3 operator-value studies (unblocked independently once protocols/budgets approved)
         └──> Phase 6: Scale, multi-module linters, local models & compatibility hardening
```

### Phase 1 — Zero-policy deterministic wins (start immediately)

These changes do **not** alter prompts, models, schemas, or review policy. They
remove sequential waits and redundant I/O that the source shows, while
producing identical inputs to every LLM call. Their size is not measured yet:
the sites were found by reading the code, not by profiling. Profile the five
items on the Phase 1 fixtures first, do them in order of measured time saved,
and drop any item whose benchmark shows no gain.

1. **Overlap deterministic linters with LLM review/style passes (`internal/review/engine.go:1009-1076`)**
   - `runner.Run(ctx, files)` currently waits for `styleWG.Wait()` even though
     linters only depend on `files` and `e.Config`. Run analyzers concurrently
     with `analyze` + `analyzeStyle`, joining before triage/disclosure.
   - Today a failed `analyze` returns before any analyzer starts. The
     concurrent version must cancel the analyzers on that path and keep the
     report's order and content the same. With a local model (`ollama`,
     `llamacpp`) analyzers compete with inference for the same CPU, so measure
     that case separately and keep the serial order there if it is faster.
2. **Cache and parallelize `repoTools` across turns and agents (`internal/review/repotools.go:85-188`, `deep.go:218, 328-360`, `verify.go:178, 300-327`)**
   - Memoize `fileBytes`, `git check-ignore`, and `git grep` per review; share
     one `repoTools` cache across `general` and `security` agents in
     `deepReviewBatch` and across `verifyOne` workers; execute multi-tool turns
     (`resp.ToolCalls`) concurrently.
   - The lookup budget is order-dependent (`run.bytes >= deepToolBytes` at
     `deep.go:353`). Fetch concurrently, then apply the budget, truncation and
     message order in the original call order, so the transcript is unchanged.
     Share the cache between agents, not the per-agent byte budget.
3. **Share one memoized file fetcher across `bundle`, `applyBudget`, and `flowDiagram`; parallelize `AssembleReserving` (`engine.go:936-972`, `budget_apply.go:91`, `flow.go:39-42`, `bundle.go:295-366`, `related.go:155-172`)**
   - Eliminate re-fetching and full re-assembly when `applyBudget` trims files,
     reuse fetched content in `flowDiagram`, and bound-parallelize remote
     `FileContent`/`DirLister` calls.
4. **Deduplicate `RenderBatch` and `KnowledgeRetriever` work (`engine.go:1985, 1998, 2091`, `knowledge.go:255-299`, `route.go:180-240`)**
   - Render `RenderBatch(b)` once when `DesignTask != ""`; batch/parallelize
     `perFile` embedding queries and memoize retrieval per `(file_set, classes)`
     across ensemble, style, and split-half passes.
5. **Parallelize linter `Detect` (`linters.go:307-368`)**
   - Run `Detect` concurrently across runners.
   - Multi-module `golangci-lint` (`runners.go:478-515`) is **not** in Phase 1.
     `golangci-lint` holds a process lock and this runner does not pass
     `--allow-parallel-runners`, so concurrent module runs would wait on the
     lock or fail. Adding the flag changes how the analyzer runs and shares
     its cache; treat it as a separate proposal with its own correctness test.

**Phase 1 Gate (Track A — deterministic replay):**
- Prompt/request payloads (or canonicalized input hashes), findings, and
  coverage/disclosure reports are **identical** on scripted/deterministic
  replay; mutation and `-race` tests pass; all live permission/stale-head
  guards are re-checked before writes.
- Benchmarks on representative local/remote-mocked fixtures demonstrate measured
  wall-clock / I/O / subprocess reduction.
- **No expensive live `-0.01` PR study is required** when deterministic inputs to
  the model are unchanged.

### Phase 2 — Targeted per-mode baseline (Lightweight, no redundant ceremony)

Run a focused baseline only for the modes and strata not already covered by
`mira-win-plan.md` `E0` (which owns `deep-review` loss attribution):

- **Workloads:** fixed, authorized snapshots spanning small/medium/large PRs,
  monorepo/multi-module Go, remote vs. local checkout, clean controls,
  incremental pushes, fast + reasoning + local models, and whole-tree/security
  scans. Separate cold-start and warm-cache runs; record critical-path stage
  times, p50/p95, failures, memory, and per-role tokens/costs.
- **Censoring rule for `p95` latency:** failed, timed-out, or cancelled runs
  stay in the denominator. Report completion rate alongside `p95` computed over
  all attempted runs (with incomplete runs assigned the run timeout/deadline as
  right-censored observations), never by silently dropping failures.
- **Exit:** bottleneck rank and opportunity table by mode; select Wave 1
  model/architecture work from measured sinks (at most **one** speed/cost
  experiment and **one** quality/reliability diagnostic per wave).

### Phase 3 — Eliminate the largest token & reliability sinks

Attack the two largest measured model-token and latency sinks first:

1. **Incremental candidate capture in `deep-review` (`E1` in `mira-win-plan.md`; `deep.go:46-47, 280-294, 414-478`, `deepselect.go:253-272`, `verify.go:69-84`)**
   - Measured sink: 82%–97% forced-submission rate, up to 3x 65,536-token / 10m
     rungs on empty thinking, double-calling in `deepCritique`, and
     `generateBounded` bypassing `llm.Client`'s `retryBudgetSpent` guard.
   - Persist candidate updates during the loop and route `generateBounded`
     through `llm.Client` retry/budget accounting. Treat this primarily as a
     reliability/cost lever unless `E0` shows recoverable candidate losses.
2. **Reduce the cost of the `fast-review` verification call (`engine.go:2067-2083`)**
   - `fast-review` sends the full prompt + `RenderBatch(b)` again in a second
     sequential `llm.Extract` call for every batch with findings. The call is
     deliberate: it rejects claims that need code the diff does not show. It
     is a cost to reduce, not redundant work to delete.
   - Try the cheaper option first: the second request repeats the first as a
     prefix, so check whether provider prompt caching already discounts it and
     whether the request is shaped to hit that cache. Then test a single-pass
     variant against the 2-call path on development data. Keep the 2-call path
     unless the single pass holds precision within the frozen bound.
3. **Extend `-resume` (`reviewProgress`) beyond `analyzeBatchWith` (`progress.go:72-145`, `engine.go:2022-2046`)**
   - Checkpoint triage/verify/critique/linter outcomes, each keyed by every
     input that stage reads, with strict policy/version invalidation.
   - Do **not** drop the PR title/description from the batch key: that text is
     part of the prompt, so a hit would serve an answer to a different
     request. Do **not** restore cache hits for standing-only files: the key is
     cleared on purpose (`engine.go:2029-2041`) so an open comment gets a fresh
     check. A change to either rule is a review-policy change under Track B.

**Phase 3 Gate:** cancellation/idempotence/stale-head/fallback unit + mutation
tests pass; live runs show reduced lost work, token spend, or tail latency
under the Track B promotion contract (§6).

### Phase 4 — Quality breakthroughs & model-allocation tuning

1. **Evidence-led quality (`mira-win-plan.md` `E2–E3`):**
   - Run the cheap oracle feasibility tests before building extractors or
     broad test-execution machinery: (a) before/after producer-consumer
     contract contradictions (`E2`) and (b) claim-specific execution witnesses
     extending `internal/execution` (`E3`, gated on `S0`).
   - Do not add another confidence floor, comment cap, or generic critic.
2. **Model routing & role allocation (existing `route` / `ensemble`):**
   - Only after Phase 2 identifies role costs and an explicitly named task has
     a measured quality baseline (from Phase 3/4 or completed `G1/G3` studies),
     compare at most three frozen configurations against a single-model
     baseline at equal resource envelopes.
   - Preserve operator model choices, privacy constraints, and local models.

### Phase 5 — Complete `G1` & `G3` operator-value studies (Independent track)

Run the outstanding `revised-plan.md` human studies for `@open-nitpick fix`
(`G1`) and operator-approved repository learnings (`G3`) once their protocols
and budgets are approved (not blocked by Phases 2–4):
- **G3 Learnings:** same-repository temporal split (no cross-repo transfer,
  preserving `STRATEGY.md:66`), operator-edited drafts, measuring retrieval
  rate, noise reduction, recall preservation, and operator editing time.
- **G1 Fix checks:** runnable fix corpus measuring eligible defects -> emitted
  findings -> applied patches -> check passes -> semantic repair vs.
  regressions.

### Phase 6 — Scale, large-tree & compatibility hardening

- Profile `full-review`, `repo-score`, `security`, `slop`, `standards`, and
  `knowledge-index` on large monorepos, unsupported files, truncated outputs,
  and local models (`ollama` / `llamacpp`).
- Bound memory and `reviewProgress` snapshot eviction (`MaxProgressBytes`),
  preserve MCP progress/cancellation, and expand analyzers/forges only when a
  measured defect class or validated user demand (`G5` GitLab) justifies it.

## 4. Promotion & measurement contract (Track A vs. Track B)

Follow `docs/measurement.md` throughout:

- **Track A (deterministic / replay-identical changes, Phase 1):** requires
  bit-for-bit identical model prompts/payloads/outputs on deterministic replay,
  mutation tests on safety/cache-invalidation guards, `-race` pass, and
  reproducible wall-clock/I/O benchmarks.
- **Track B (model/policy-affecting changes, Phases 3–6):**
  - Separate development, calibration, and sealed final evaluation splits.
  - Pre-register primary endpoint, sample size (derived via power analysis from
    development variance and `docs/measurement.md` Rule 4a corpus resolution—no
    arbitrary `-0.01` paired lower bound on a 30-PR corpus), numeric regression
    bounds on **all** non-primary metrics (cost, p50/p95 latency, completion
    rate, memory—no unspecified `"material"` thresholds), and spending cap.
  - Mandatory regression guard set: explicitly name `AllFixtures` +
    `HeldOutFixtures` (with `MultiFileFixtures` reported separately per Rule 15)
    plus the `mira-win-plan.md` critical/error guard set.
  - Primary cost metric: **cost per distinct valid defect** (Rule 13), with
    cost per attempted review reported beside it; unknown usage stays unknown.
  - `mira-win-plan.md`'s clear-win gate governs any Mira superiority claim;
    local non-regression targets govern speed/cost/reliability promotions.

### Planning targets (subject to owner approval & sample-size feasibility)

| Claim | Proposed target (sized to the approved evaluation sample) |
|---|---|
| **Faster (`review` / `deep-review`)** | >=25% lower p95 latency across all attempted runs (with completion-rate & censoring disclosure), paired uncertainty supporting improvement, and non-regression within the sample-sized frozen quality/cost bounds. |
| **Faster (`fast-review`)** | >=30% lower p50/p95 time-to-first-finding and complete-review latency while preserving eligible-file coverage and within the frozen quality/cost bounds. |
| **Cheaper** | >=20% lower measured **cost per distinct valid defect** (Rule 13) and cost per attempted review, within frozen numeric latency, completion, and quality bounds. |
| **More reliable** | Statistically supported reduction in incomplete/failed runs and lost candidates under fixed resource budgets, within the frozen yield noninferiority margin (`E1`). |
| **Better quality** | Existing frozen `G1/G2/G3` gates for local features; `mira-win-plan.md` gate for competitive superiority. |

### Limits and outcomes for every Track B experiment

- Each experiment ends as **pass**, **fail**, or **inconclusive**. Fail and
  inconclusive both mean no promotion. More tuning needs a new final holdout.
- Each optimization gets one registered revision on development data, then
  passes, stops, or becomes a new separately funded hypothesis.
- Every arm and the whole program have a spend ceiling approved before the
  run. Work stops at the ceiling; an exhausted budget is an incomplete result.
- Guard tests fail under mutation (Rule 9). A path that returns empty has a
  test for why it is empty (Rule 10). This applies to every new cache: test
  the miss, the hit, and each invalidation input.
- Baseline and experiment artifacts can hold private code and thread text.
  Redact logs, set access and retention rules before collecting, and upload
  nothing by default.

## 5. Rollout, governance, and first pickup actions

- Roll out model/policy changes per mode via opt-in config -> shadow mode
  (read-only/stubbed publisher; **never** publishes comments, resolves threads,
  approves PRs, applies fixes, activates learnings, or expands execution
  authority) -> canary -> owner-approved default. Keep immediate rollback.
- **Governance:** lightweight for Track A (author + code/safety review); full
  measurement/safety/operator signoff for Track B paid studies and defaults.
- **First session after handoff:**
  1. Reconcile `docs/remediation-implementation.md` and pin the baseline
     snapshot + artifact hashes.
  2. Immediately start **Phase 1** deterministic wins (linters overlap,
     `repoTools` cache + parallel tool calls, shared file fetcher, `RenderBatch`/
     `KnowledgeRetriever` dedup)—zero model spend, verified by Track A.
  3. Run **Phase 2** targeted per-mode baseline alongside `E0` to size the
     **Phase 3** experiments (`deep-review` `E1`, `fast-review` 2x call,
     `-resume`) and register exact numeric bounds before spending on live arms.

## 6. Completion

The program is complete when each funded track has a recorded pass, fail, or
inconclusive result, every promoted claim has evidence for its stated scope,
and the operator has accepted the rollout and rollback decisions. It does not
require every hypothesis to succeed.

## 7. Signoff record

Reviewed 2026-11-18 against the working tree at HEAD `6b6b108`. The Phase 1
code sites were read in the source and match the plan. The tree has other
uncommitted work in progress, so line numbers can drift; re-check them at
pickup. Signoff covers the plan as a proposal. It does not approve spending,
the planning targets, or any default change, and no Phase 1 gain is measured.
