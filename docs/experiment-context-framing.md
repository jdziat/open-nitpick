# Experiment: context framing and reranking on the tuning corpus

What we measured, what we changed, and what a result would mean.

## Question

Two effects are confounded in the +ctx arm of the tuning corpus:

1. **Framing** — the sentence above attached definitions tells the model to
   treat them as "context only". Deepseek and the qwen models suppress a
   valid nit on cross-file-copy-nit under that framing, and the suppression
   is defensible: the Snapshot contract they read is explicitly permissive.
2. **Relevance** — the related collector attaches every definition the
   changed lines name, up to 12 per file and 80 lines per definition. It has
   no signal for whether a definition actually decides a finding. A memo key
   contract next to an unrelated helper is equally "related" to it.

This experiment separates the two.

## Arms

Four arms on the same 16-fixture tuning corpus, deepseek-v4.1-flash as the
reviewer (highest recall at 0.90 no-ctx), one run per fixture per arm:

| arm | related_context | preamble | reranking |
|---|---|---|---|
| A no-ctx | off | n/a | n/a |
| B shipped | on | "Context only. ..." | none |
| C alternate | on | "Reference material. Use it to check the change against real contracts, but do not suppress a finding the diff alone justified." | none |
| D reranked | on | alternate (from C) | attach only definitions whose snippet overlaps the diff's changed lines |

Arm C tests framing alone. Arm D tests framing + reranking. If D beats C,
relevance is doing work beyond framing.

## Measurement

Recall, noise, anchor width, and per-fixture located/plants for each arm,
reported side by side. Rule 3 applies: one run per arm is a signal, not a
result. Re-run any pair that differs by fewer than 2 plants.

## What a result would mean

- C beats B: framing suppresses valid findings, and the shipped sentence is
  too strong. Change the shipped default.
- D beats C: relevance ranking helps beyond framing. Add a relevance score
  to the related collector.
- C and D both miss cross-file-copy-nit: the plant itself is ambiguous, and
  the fixture needs redesign, not the harness.
- C and D both match B: framing was not the problem; the +ctx drop is pure
  attention dilution, and no phrasing fix will recover it.


## Results: arm C, 2 runs (confirmed)

Arm C was run twice (multifile-tuning-20260917T125438Z and 130340Z) across all
16 fixtures with deepseek-v4.1-flash as the reviewer. 32 reviews per arm.

| arm | recall | noise/rev | silent |
|---|---|---|---|
| A no-ctx (2 runs) | 0.90 | 0.22 | - |
| B shipped preamble, +ctx (2 runs) | 0.77 | 0.50 | 10/32 |
| C alternate preamble, +ctx (2 runs) | 0.83 | 0.31 | 11/32 |

C beats B by 6 recall points (0.77 to 0.83) with 0.19 fewer noise findings per
review. The recovery is not uniform: 3 fixtures flip the right way (cross-file-
copy-nit, go-package-singleton, multi-defect, ts-unbounded-memo-key each gain
0.5 to 1 located plant), 2 flip the wrong way (php-forbidden-vs-404,
sorted-for-min-nit each lose 0.5), and the rest do not move.

Rule 3: a gap of 6 recall points on 30 plant-opportunities is 2 plants across
2 runs. It is a signal, not a settled result, and the +1/-1 pattern on
different fixtures says the framing change trades recall on some plants for
recall on others rather than uniformly recovering the suppressed ones.

cross-file-copy-nit specifically: B missed it twice (0/2), C found it once
(1/2). The alternate preamble does appear to reduce the false suppression on
the fixture where the Snapshot contract made the finding debatable, but it
does not make it a certain find.

## What the first C results said (superseded for shipping)

The first two C runs recovered 6 recall points over B with less noise. That
was a signal under Rule 3, not a shipping decision. The iteration below
re-tested the same C text against other phrasings and against B in paired
sessions; the recall win did not hold.

## Arm C preamble iteration (2026-09-17)

Goal: find the best `related_context_preamble` phrasing on the tuning corpus
with deepseek-v4.1-flash. Every comparison below is within a session unless
marked otherwise. Rule 3: screen at RUNS=1, replicate contenders at RUNS=2.

### Variants

| id | text |
|---|---|
| B | shipped: "Context only. These files are not under review: judge the change by them, but do not report findings on them." |
| C0 | "Reference material. Use it to check the change against real contracts, but do not suppress a finding the diff alone justified." |
| C1 | "Attached code is evidence for judging the change, not a reason to stay quiet. Use it to confirm contracts. Report findings only on changed lines." |
| C2 | "Judge the change first from the diff. Attached definitions confirm or sharpen a finding; they do not cancel one the diff supports. Do not report findings on the attached files." |
| C3 | "These definitions are contracts the change must satisfy. A mismatch is a finding on the changed line. Attached files are not themselves under review." |
| C4 | "These definitions are contracts for judging the change. Use them to confirm findings on changed lines; do not withhold a finding because attached code looks intentional or permissive. Attached files are not under review." |

### Screen (RUNS=1)

C0–C2 parallel at T140053Z; C3 alone after a hang.

| id | +ctx R | +ctx N | no-ctx R | note |
|---|---|---|---|---|
| C0 | 0.75 | 0.44 | 0.69 | kotlin found; cross-file missed |
| C1 | 0.75 | 0.50 | 0.75 | cross-file found once |
| C2 | 0.69 | 0.38 | 0.75 | +ctx below no-ctx; drop |
| C3 | 0.81 | 0.38 | 0.81 | screen leader |

### Replicate C0 vs C3 (RUNS=2, paired)

| id | +ctx R | +ctx N | no-ctx R |
|---|---|---|---|
| C0 | **0.72** | 0.66 | 0.75 |
| C3 | 0.66 | 0.59 | 0.75 |

C3's screen lead reverses. C0 wins the pair. C3 dropped.

### Round 2 (RUNS=2): B, C1, C4

| id | +ctx R | +ctx N | no-ctx R |
|---|---|---|---|
| B | **0.75** | **0.56** | 0.72 |
| C4 | 0.75 | 0.62 | 0.78 |
| C1 | 0.69 | 0.66 | 0.72 |

C1's cross-file screen hit does not replicate (0/2). C4 ties B on recall with
worse noise. B is competitive with every alternate in this session.

### Final head-to-head: B vs C0 (RUNS=2, paired)

| id | +ctx R | +ctx N | no-ctx R |
|---|---|---|---|
| B | 0.75 | 0.56 | 0.81 |
| C0 | 0.75 | **0.47** | 0.81 |

Tied recall. C0 has lower noise by 0.09/review (~3 findings over 32 reviews).
Both sit 0.06 below their same-run no-ctx floor. cross-file-copy-nit remains
0/2 under both.

### Ranking and shipping call

1. **Best alternate among C-family texts: C0.** It beats C3 under Rule 3, and
   C1/C2/C4 do not beat it on recall.
2. **C0 does not beat shipped B on recall.** Two paired sessions put B at 0.75
   and C0 at 0.72 then 0.75. The earlier 0.83-vs-0.77 claim does not survive
   later sessions.
3. **Noise:** one head-to-head favors C0 (0.47 vs 0.56). That is a signal, not
   enough alone to change the shipped default under Rule 3.
4. **Hard floor:** cross-file-copy-nit, sorted-for-min-nit, and
   kotlin-widened-input stay mostly missed under every preamble. Framing is
   not recovering the remaining gap to no-ctx; those plants need fixture or
   collector work, not another sentence.

**Ship nothing.** Keep the shipped "Context only" sentence as the default.
Keep `review.related_context_preamble` so an operator can set C0. Do not
claim C beats B in findings docs without naming the session that produced
the number.

Further preamble variants are not justified until there is a new mechanism
hypothesis. The productive next measurement is the plants that stay dark
under every framing, starting with cross-file-copy-nit.

## cross-file-copy-nit redesign (2026-09-17)

The plant went dark under every preamble because two things stacked:

1. A justifying comment above the caller's copy ("Keep our own copy so nothing
   the store does later can change…") gave models cover to call the copy
   intentional defensive programming once Snapshot's contract was visible.
2. Keyword credit lagged the words models actually used (`re-copies`,
   `caller-owned`, `duplicates the whole`), and one Why phrase was a known
   gap (`knownWhyGaps`).

### What changed

- Snapshot already copies in Base; Head only documents ownership (same
  doc-comment move as cross-file-sort-nit) and adds the caller.
- Justifying comment removed.
- Keywords expanded; Why rewritten to phrases the list admits.
- `docs/harness-notes.md` updated.

### Measurement (deepseek-v4.1-flash, RUNS=2, this fixture only)

| arm | before redesign | after redesign+keywords |
|---|---|---|
| no-ctx | often miss / unstable on dark runs | **1.00** (2/2), noise 0.50 |
| +ctx | 0/2 on B and C H2Hs | **1.00** (2/2), noise 0.00 |

Dump `multifile-tuning-20260917T1637…` (live confirm). The plant is no longer
dark under +ctx. Remaining C-family dark plants for a later pass:
sorted-for-min-nit, kotlin-widened-input.

## Arm D: built; first run does not beat C

`review.related_context_rerank` (and `NITPICK_EVAL_RELATED_CONTEXT_RERANK`)
scores each candidate definition by how many of its snippet's identifier
tokens also appear in the change's added text, ignoring the definition's own
name (already counted by use-selection). Definitions that score zero are
dropped; the rest sort by score before the per-file cap. Callers are
unchanged and still follow definitions.

Code: `internal/bundle/related.go` (`scoreDefinitions`, `relevanceScore`).
Guards: `TestRelatedRerankPrefersSnippetOverlapOverUseCount` and
`TestRelatedRerankDropsDefinitionsWithOnlyNameOverlap`.

### Results: arm D, 1 run (multifile-tuning-20260917T134837Z)

Same harness as C: deepseek-v4.1-flash, 16 tuning fixtures, alternate
preamble, callers on with +ctx. One run.

| arm | recall | noise/rev |
|---|---|---|
| A no-ctx (2 runs, prior) | 0.90 | 0.22 |
| B shipped preamble, +ctx (2 runs, prior) | 0.77 | 0.50 |
| C alternate preamble, +ctx (2 runs, prior) | 0.83 | 0.31 |
| D alternate + rerank, +ctx (1 run) | 0.62 | 0.25 |
| D same-run no-ctx | 0.62 | 0.25 |

D does not beat C. On this run +ctx with reranking tied the same-run no-ctx
arm rather than closing the gap toward A. Per fixture under +ctx:
capacity-hint-nit and go-package-singleton located; multi-defect 2/3;
ts-unbounded-memo-key, cross-file-copy-nit, sorted-for-min-nit,
kotlin-widened-input, and php-forbidden-vs-404 missed. Relative to the
same-run no-ctx row, +ctx lost ts-unbounded-memo-key and gained
go-package-singleton.

Rule 3: a single run that fails to beat C is a negative signal, not a
settled rejection. The same-run no-ctx floor (0.62 vs historical A at 0.90)
says model variance is large enough that ranking D against C's earlier
0.83 mixes sessions. What this run does settle: reranking did not produce
the recovery the design hoped for on the first try, so a second D run is
not justified until there is a new hypothesis (for example, score-without-
dropping, or callers off so dropped definitions do not free slots for
callers).

`related_context_rerank` stays off by default.
