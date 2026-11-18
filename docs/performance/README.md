# Performance follow-on evidence

The program follows `performance-follow-on-plan.md`. The registration fixes workload identities and spending ceilings before new model calls or concurrency changes. Passing an isolated control does not establish an improvement in end-to-end review latency or quality.

The six retained self-review receipts report **$43.37 as a lower bound**, with 285 attempts whose costs were not reported. Those runs used evolving source. They supply a cost/recovery census, not independent quality observations or a paired speed comparison. This predates the newly approved $100 ceiling.

- [Registration](registration.json) records the gates, ordering and allocation of that $100.
- [Historical receipt census](p0-census.json) records per-run costs, unknowns, stage failures and source-artifact hashes. Recreate it with `python3 tools/bakeoff/review_census.py REPORTS... --output OUTPUT`.
- [Closeout](closeout.json) records the reviewed code snapshot, gate results,
  spending, the operator's warnings-only pass rule and unfinished experiments.

| Performance cohort | Frozen public change | Files |
|---|---|---:|
| routine | [PR 200](https://github.com/jdziat/open-nitpick/pull/200) | 2 |
| noisy-docs | [PR 210](https://github.com/jdziat/open-nitpick/pull/210) | 22 |
| cross-file | [PR 197](https://github.com/jdziat/open-nitpick/pull/197) | 7 |
| provider-retry | [PR 215](https://github.com/jdziat/open-nitpick/pull/215) | 6 |
| large-change | [PR 213](https://github.com/jdziat/open-nitpick/pull/213) | 40 |

These cases were selected from PR metadata without running a detector. Their merge-parent and merge-commit hashes fix the compared change. They are performance inputs, not untouched quality holdouts. Fresh quality splits and independent adjudications must be registered separately.

The Phase 1 experiments made no provider calls. [Local experiment results](phase1-results.json)
retain all twenty samples and the ordered-plan hashes for each frozen workload.
Receiver-method indexing reduced median preparation of the 40-file case from
5.26 s to 115 ms with unchanged payloads. Initial whole-process peak RSS was
57,816 KiB before and 58,160 KiB after; it did not establish a memory reduction.
The initial routine-case timing regression did not recur in the isolated
twenty-run control. These are preparation measurements.

The lookup experiment reads twelve windows of a tracked file and repeats eight
identical searches, using a fresh cache per sample. Across twenty runs, median
time fell from 51.8 ms to 4.59 ms for the windows and from 136 ms to 17.7 ms for
the searches. Allocations and process peak RSS also fell. Nine mutation guards
cover cancellation, transient errors, lookup ordering, budgets, oversized
source and review-head changes. The CTO approved this change; its package race
suite passed. Oversized source remains uncached so Jev receives the same text.

Engineering preparation has a separate profile. Replacing repeated whole-file
line splits with byte ranges reduced preparation by 6.7–27.5% across the five
cases, with three runs per case. The 40-file median fell from 13.57 s to 10.23 s;
allocations fell from 13.03 GB to 7.65 GB per iteration. All plan hashes, task
counts, batches, skips and errors match. Whole-process peak RSS was 344,092 KiB
before and 346,404 KiB after, so this does not establish a peak-memory gain.
Five mutation guards, equivalence fuzzing and the dependent race suites passed.
The CTO approved this change.

Parallel analyzer detection was rejected: its sub-millisecond gains were mixed,
and the large case slowed from 401 µs to 617 µs. A source-read memo also remains
outside production. It reduced repeated reads but gave mixed latency and
regressed local in-memory cases, including with simulated 2 ms provider waits.
Both experiments preserve their raw observations in the results.

Overlapping real analyzer execution with preparation also regressed all five
medians (0.4–9.2%); production scheduling stays serial. These runs did not
simulate network model latency.

C8 reuses each task batch's rendering for validation. Its corrected 20-run
proof includes ordered task contexts and every engine-owned finding field.
The original hashes omitted those fields and are superseded; changing context
text now fails the guard. Median prompt preparation improved in all five cases.

C9 overlaps up to four main source reads and preserves all five frozen plans
and fetch counts. Large Git-backed assembly falls from about 838 to 612 ms;
process peak RSS rises from 62,896 to 69,320 KiB. The smallest worktree case
regresses by 0.14 ms; the other four improve. Eight mutation guards cover
selection, generated-file refill, source assignment, errors, worker bounds and
cancellation/draining. Related reads can coexist with the four main reads.
Final repository gates and CTO review still apply. These measurements exclude
provider latency and do not establish total-review speedups.

[The mode scorecard](mode-scorecard.json) inventories the CLI commands and all
seven MCP tools. It keeps shared-engine preparation, scripted transport
controls and the single live compatibility sample separate. Per-mode live
latency and role-cost baselines remain incomplete.

[Local transport controls](phase2-local-transports.json) run the real binary through 16 CLI entries and the 7 MCP tools against scripted loopback responses, 20 samples where the entry completes. They record the model, reasoning and thinking options each entry sends (review and full-review send low reasoning, deep-review medium, fast-review disables it), plus startup time. The responses are scripted and spend nothing, so the timings measure transport only, never review speed or provider cost. Three entries are incomplete, with the reason recorded beside each.

[Historical selection replay](phase2-replay.json) is an exploratory diagnostic
on the inspected 20-PR and 30-PR corpora. Its optimistic cluster
labels omit unlabelled-only groups. It does not satisfy the plan's per-mode
baseline or justify a policy change.

The large scripted review profile covers ten iterations (268 ms per review).
Prompt rendering accounts for 2.5% of CPU; no dedup prototype is promoted.
Flow-diagram preparation accounts for 27%, and total bundle assembly for 24%.
Those CPU fractions are not wall-time savings. That earlier profile did not
measure analyzer/model overlap or parallel full-file prefetch; C7 and C9 above
record the later experiments. C8 measures task-batch rendering reuse separately.
Per-file knowledge lookup still needs a knowledge fixture, so Phase 1 remains
partial.

Phase 3 item 1 (incremental candidate capture) has a census of 324 retained
finished agents. Forced submission completed a parseable result for 247,
including 10 with zero findings. The logs report no failed agents or whole-batch
fallbacks. The 21 cut calls represent at most 12.8% of completion-token volume;
that ratio does not bound dollar savings because prompt costs also matter.
Recoverable candidate loss is not demonstrated. The pilot remains unrun; see
[the census](phase3-e1-census.json).

For item 2, fast-review verification repeats the first request as a prefix, with
the same schema. Cache discounts and the single-pass experiment remain
unmeasured. The two-call path stays.

For item 3, triage, critique and repository-verification responses can be saved
for reuse. Verification keys include the head, backend access and effective
model options. Every original file read and search is rechecked before a hit;
changed evidence triggers a fresh model check. Invalid verdicts, failed lookups
and empty triage responses are not saved. Standing-only comment rechecks stay
fresh. Critique replay, corrupted-cache controls and 16 mutation guards passed, and
the CTO approved this component; see [the receipt](phase3-resume.json).

Eligible analyzer sets now save their findings, provenance and coverage
metadata as a separate opt-in checkpoint. Failed or unbound inputs run fresh,
and local output cannot displace paid model answers. The CTO approved this
component after 27 mutation guards were detected. In 20 real Ruff repeats,
neither the analyzer nor the scripted model ran again. This is a negative
timing result: median 34.51 ms without resume versus 73.91 ms for checkpoint
reuse. Identity checking costs more than this small analysis. These in-process
controls establish repeat-work avoidance; measured live savings and independent
CLI/MCP baselines remain open, so item 3 is incomplete.

Phase 2 still needs per-mode live latency, completion, coverage and role-cost
baselines. Incremental candidate capture, provider prefix-cache discounts,
routing comparisons and model-backed large-tree studies remain unrun. The
compatibility sample and model-label panel below do not close those studies.
Human adjudications, temporal operator feedback and editing-time observations
are absent. No quality, precision, reliable-fix or whole-review speedup claim
follows from these data.

Recreate the frozen inputs and run the local experiments without provider calls:

```sh
python3 tools/bakeoff/freeze_workloads.py --registration docs/performance/registration.json --directory /tmp/nitpick-frozen
NITPICK_PERFORMANCE_WORKLOADS=/tmp/nitpick-frozen/benchmarks.json go test ./internal/bundle -run '^$' -bench FrozenPublicWorkloadAssembly -benchtime=20x
go test ./internal/review -run '^$' -bench 'Repository(LookupWindows|RepeatedSearch)' -benchtime=1x -count=20 -benchmem
```

The first [live CLI pilot](phase2-live-pilot.json) stopped on two HTTP 400
responses: GLM requires reasoning while that registered profile disabled it.
There was no usable model output or reported cost. The $1 reservation remains
held for those unpriced attempts; the other pilot sample was not started.
This rejection time is not a latency baseline. No automatic retry is allowed.

A single compatibility sample then ran the CLI review at the existing low-reasoning
configuration: 43.5 s, 3 model calls, 0.0077 USD reported, none unpriced. A replay of
the same corpus with all IP sockets denied made no model calls, wrote no cache entries
and returned the identical finding. It shows the repeat is free; it is an N=1
observation, not a latency, quality or p95 baseline. Fast-review was not run because
this model rejects disabled reasoning. That sample's reported spend is
0.0077 USD; total recorded spend, including labels and closeout reviews, is
in [the closeout](closeout.json).

[Large-tree smoke](phase6-large-tree-smoke.json) ran the two model-free commands once on the Go standard library (7,711 Go files, 1.9 million lines): standards in 4.2 s and 218 MB peak, slop -no-model in 17.1 s and 401 MB peak, both exiting 0. It is one run per command on a shared host with no spend. It shows the commands scale to that size; it is not a baseline, and the model-backed modes were not run at this scale.

[LLM-generated labels](phase4-llm-labels.json) supplement the packet for the 15 retained examples, as the operator approved; the human slots remain blank. Two prompts of deepseek-flash read each finding without the code. Across 30 labels: 10 valid, 2 invalid, 18 uncertain, and the two passes agree on 5 of 15. This is one model family checking itself, not ground truth: it supports no precision, recall or quality claim, and the examples are hand-picked rather than a fresh holdout. The 0.028 USD accounting total includes a 0.005 USD reserve for one interrupted request with unknown billing. The operator-timing and temporal-feedback CSVs measure human editing time and later maintainer feedback, so a model cannot fill them and they stay empty.

[A six-provider label panel](phase4-llm-label-panel.json) (Anthropic, Google, OpenAI, Mistral, Qwen, DeepSeek) labelled the same 15 findings from text alone. A strict majority exists for 12 of 15: 7 valid, 1 invalid, 4 uncertain. Mistral returned only 7 labels and Qwen 13, because failed calls hit the token cap and were not retried. The models share training data and see no code, so a majority is a consistency check, not ground truth, and supports no precision, recall or quality claim. Total spend on labelling was 0.30 USD.
