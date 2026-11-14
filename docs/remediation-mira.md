# Remediation: closing the precision gap to Mira

**Status:** gate run on 2026-11-13; the win condition was not met (see "Held-out result")
**Date:** 2026-11-13
**Evidence:** [Bakeoff: open-nitpick against Mira](bakeoff-mira.md)

The bakeoff found one gap, and it is precision. On the same model, open-nitpick
reported more of the labelled defects than Mira (recall 55% against 40 to 42%)
and posted four times as many comments, three quarters of which matched nothing
(precision 28 to 29% against 68 to 76%). This plan closes that gap in stages,
and each stage has to show its number before the next one starts.

## The target

The goal is a margin that survives a corpus neither tool was tuned on, not a
win on the 20 pull requests this analysis already looked at.

- **Corpus.** The Martian offline set has 50 pull requests. The bakeoff used 20
  (entries 1, 2, 4 and 5 of each repository). The other 30 are held out: no
  stage is tuned on them and they are scored once, at the end.
- **Win condition.** On the 30 held-out pull requests, with
  `deepseek/deepseek-v4.1-flash-1017` for every role in both tools and the same
  upstream judge, three passes each:
  - open-nitpick's F1 is at least 0.08 above Mira's in every pass, and so is its
    F2;
  - open-nitpick's precision is at least 60%;
  - open-nitpick's median review time is no worse than Mira's;
  - the planted-defect corpus in `internal/evals` loses no defect it finds today.
- **What that needs.** At today's recall of 55%, a precision of 65% gives F1 0.60
  and F2 0.57, against Mira's 0.52 and 0.45 on the tuning set. The arithmetic is
  in reach. The open question is whether precision can rise that far without
  recall falling.

Mira's figures are a moving target in one respect. Its prompt carries a list of
62 language "footguns", and one of them, negative slicing on a Django QuerySet,
restates a golden comment on sentry-1 almost word for word. That makes the
held-out split matter more, not less, and it is a reason this plan adds no rule
derived from a golden comment.

## What the variance allows

Two runs with only a class filter changed (`persona.nitpick: minimal`) scored F1
0.418 and 0.434 against 0.368 and 0.378 for the shipped passes. A filter cannot
add a finding: the prompt is generated at `normal` whatever the level
(`config.GenerationLevel`), and the filter runs after triage. Applied offline to
the shipped passes, it removes 6 and 5 comments and moves precision by under one
point. So these are four runs of one generator, and they spread across 0.07 of
F1, 53% to 61% recall and 92 to 128 comments.

So one pass of open-nitpick can move F1 by more than half the gap this plan sets
out to open, and two passes agreeing is weaker evidence than the bakeoff document
first treated it as.
Every stage below is measured with three passes per arm, compared per pull
request with a paired bootstrap, and a ship condition has to clear the 95%
interval, not the mean.

## Stages

Each stage names what it changes, what the measurement must show before it
ships, and what kills it. Ship and kill conditions are written here, before any
number exists.

### R0. Make the measurement repeatable

Changes nothing a review produces.

- `nitpick review -format json` prints findings with path, line, severity, class
  and title, and the fields R1 adds. The bakeoff runner parses the dry-run text
  today, and its first severity parse was wrong in a way that hid a result.
- `make bakeoff` runs both tools over a named split, writes the scorer's layout
  and scores it, with the tooling already in `tools/bakeoff`.
- A per-pull-request paired bootstrap over F1, precision and recall, printed
  with the counts behind every rate.

Done when a three-pass arm A rerun on the tuning split reproduces the bakeoff
numbers within the bootstrap interval.

### R1. Confidence and a citation on every finding

The largest lever, and the one with the most risk.

- The review schema gains `confidence` (0 to 1) and `evidence`: the lines of the
  shown code the claim rests on, quoted. `internal/review/schema.go`,
  `internal/review/finding.go`, `internal/prompt/templates/review.md`.
- A publish floor in code, `review.min_confidence`, applied after triage. It is
  set from the measurement below, not from Mira's 0.7.
- Why: severity separates a real finding from noise only weakly here. The judge
  matched 11% of our info comments, 30% of warnings and 37% of errors. Mira's
  drafts do separate on confidence: posted comments have median 0.78 and dropped
  ones 0.665.

Before building the floor, measure whether DeepSeek's self-reported confidence
separates matched from unmatched comments on the tuning split: the area under
the curve of confidence against the judge's verdict.

- **Ship:** area at least 0.70, and a floor exists that raises precision by 15
  points or more while recall falls by 5 or fewer, across three passes.
- **Kill:** area below 0.65. Self-reported confidence is then not a signal for
  this model and R2 carries the precision work alone.

**Result (2026-11-13): killed.** Three passes on the 20 tuning pull requests
produced 316 comments, every one carrying a confidence, 99 matched by the judge.
The area under the curve is 0.628. Confidence sits between 0.6 and 0.85 for 87% of
comments, so there is little spread to cut on: a floor of 0.8 keeps 132 comments
at 40.9% precision and loses 45% of the matched ones, and a floor of 0.9 keeps 39
comments at 51% and loses 80%. The floor, `review.min_confidence`, is not built.

The three passes scored F1 0.367, 0.430 and 0.420 and precision 29.0%, 32.3% and
37.0%, against 0.368 and 0.378 and 28 to 29% shipped. A paired bootstrap over the
20 pull requests puts the F1 difference at +0.030 with an interval of -0.044 to
+0.092, so asking for a confidence and a citation moved nothing this corpus can
resolve, in either direction.

Looked at after the gate failed, and so not pre-registered, only a lead for R3:

| signal | area under the curve |
|---|---:|
| confidence, within a pull request (490 matched-against-unmatched pairs) | 0.704 |
| severity, over all comments | 0.663 |
| confidence plus a little severity | 0.676 |
| length of the rationale | 0.519 |

A comment that quotes a citation matched 35.3% of the time (173 comments), against
26.6% for one that did not (143). Ranking within a pull request is what a per-pull-request cap needs, so R3 may use confidence as its
ranking even though R1's floor is dead. That has to be confirmed on the held-out
split before anyone relies on it. The `confidence` and `citation` fields stay in
the schema for that reason and for the JSON report; if R3 fails they are removed.

### R2. An evidence-graded critique stage

- A stage after triage, one batched call per pull request, grades each finding as
  `proven`, `plausible` or `unsupported` against the diff around its cited lines.
  The keep rule lives in code: proven keeps, plausible keeps at warning or above,
  unsupported drops.
- Why not triage: `triage.md` rule 2 already asks triage to rate a thin claim
  `nit`, and the result is info comments at 11% precision. Triage merges, ranks
  and re-rates in one call. Grading evidence is a separate job.
- Why not `validation`: as shipped it cost 13 points of recall for 1 to 2 of
  precision here, and 4 of 24 planted locations on the knowledge corpus.
- Measured offline on the shipped passes: precision rose from 27.6% to 32.3% and
  from 28.8% to 37.1%, recall fell from 54.8% to 50.0% and to 53.2%, and F1 rose
  0.024 and 0.059.

- **Ship:** precision up 5 points or more at a recall cost of 3 or fewer, across
  three live passes.
- **Kill:** recall falls more than 5 points, or precision does not move outside
  the interval.

**Result (2026-11-13): killed.** Three live passes with `review.critique` on
against the three R1 passes, which are the same build with the stage off. The
critique scored precision 35.5% against 33.2% (+2.3 points, interval -2.6 to +8.0)
and recall 42.6% against 51.5% (-8.8 points, interval -15.5 to -2.0), F1 0.388
against 0.404. Comments per pass fell from 82 to 126 down to 77 to 86. Recall
fell by more than the 5 points the kill condition allows, with the interval
clear of zero.

The offline estimate that motivated the stage (precision +5 to +8, recall -2 to
-5) compared the same findings with and without the filter. The live comparison
is between different generations, and the R1 passes alone span 48% to 65% recall,
so some of the recall difference is the model returning different findings. Both
ways of measuring agree that evidence grading buys a few points of precision at
a cost in recall and does not come near the 25 to 30 points the target needs.
The stage stays in the tree, off, until R4 shows whether a grader that can read
the repository does better; if it does not, it comes out.

### R2b. Verify each finding with repository tools (replaces R2)

Measured offline before building, on the 316 findings the three R1 passes
produced, so the comparison is exact: the same findings with and without the
filter. A grader given `read_file` and `grep_repo` over the head checkout and up
to eight tool calls submits `confirmed`, `refuted` or `uncertain` for each finding,
`tools/bakeoff/verify_filter.py`.

| keep (counts pooled over the passes) | precision | recall | F1 | F2 |
|---|---:|---:|---:|---:|
| no filter | 33.2% | 51.5% | 0.404 | 0.464 |
| confirmed only | 47.5% | 47.1% | 0.473 | 0.472 |
| confirmed and uncertain | 39.4% | 49.0% | 0.437 | 0.467 |

Against the unfiltered findings, keeping confirmed only changes precision by
+14.3 points (interval +8.3 to +20.0), recall by -4.4 (-7.9 to -1.5) and F1 by
+0.069 (+0.019 to +0.112). It graded 199 of 316 confirmed, 64 refuted and 53
uncertain, used 8.3 tool calls a finding on average, and no grading failed.
Against Mira on the same 20 pull requests the F1 gap closes from -0.14 to -0.04,
with an interval of -0.13 to +0.05 that includes zero. Precision is still 25
points behind.

The recall-weighted F2 hardly moves (0.464 to 0.472), because the filter trades
recall for precision. The win condition asks for F2 as well as F1, and Mira's F2
was 0.445 and 0.455, so this does not reach it: F2 needs recall to rise, and
nothing built so far does that.

With a cap on top (offline, ranked by the reviewer's confidence within a pull
request), 3 comments a pull request gives 56% precision at 44 comments a pass,
and 2 gives 63% at 33. Mira posts about 36 a pass at 68 to 76%.

This is the first change in this plan that moves precision by more than the
variance. The critique stage it replaces graded the same claim without letting the
grader look anything up, and gained 2 points. The difference this suggests is the
tools, and it is untested whether a grader with tools but no diff window would do
as well.

Two follow-ups on the same findings. Verifying the two-reviewer union (arm B)
left precision at 31.7% against 20.0% unverified, recall at 51.5%, F1 0.392, which
is 0.085 worse than verifying the single reviewer (interval -0.145 to -0.009): the
extra recall the second reviewer brings is mostly findings the grader also
confirms and the judge does not match. And reading 14 of the 111 confirmed false
positives, most look like real defects the benchmark's golden list does not
contain (a migration with `force: true` that drops rows, a hard-coded upload cap
that ignores a site setting, an async `forEach` in a second file). The
benchmark's precision is therefore a measure of agreement with a short list of
human-labelled issues, and a high-recall reviewer is penalised for reporting real
ones. That bounds how much precision it is honest to chase, and it is the reason
the held-out gate below judges against Mira's own score on the same labels.

### R3. Cap low-priority volume per pull request

- Warnings and above always publish. Below that, a per-pull-request cap ranked by
  R1's confidence, then R2's grade. Mira does the same with a cap of five.
- Why last among the cheap stages: a cap needs a ranking that means something.
  Ranked by severity alone, a cap of five reached 34.6% precision at 40.4%
  recall, which is what saying less costs without a better ranking.
  Three pull requests with 11 or more comments carried 40% of our comments at 17%
  precision.

- **Ship:** precision up 5 points or more over R1 and R2 at a recall cost of 2 or
  fewer.
- **Kill:** no cap clears that.

### R4. Verify plausible findings against the code they depend on

- A finding graded `plausible` because it depends on code not shown gets that
  code: the symbol it names, through the definition resolution the related
  context already does, and is graded again.
- Why: two of the five golden comments only Mira found are cross-file (a
  `member` that can be `None` in sentry-1 and sentry-2). Mira reaches them by
  reading files through tools, which is also why its reviews take 187 seconds on
  Sonnet against our 22.
- Budget: no more than 60 seconds added to the median review.

- **Ship:** recall up 3 points or more with precision held, inside the time
  budget.
- **Kill:** the time budget is breached, or recall does not move.

## What not to do

Each of these was measured in the bakeoff and lost.

- **Union two reviewers.** Arm B added `qwen3.8-27b` as a second reviewer and
  matched 4 to 5 more golden comments at a cost of 63 to 80 more false positives.
  Of 199 comments in one pass, 21 came from qwen and 2 from both models, so there
  is no agreement signal to vote on either.
- **Widen context without a denoise step.** In the same arm, with the caller walk
  on and larger batches, DeepSeek alone posted 176 comments where it posted 123
  to 128 at shipped settings. The two changes were made together, so this does
  not say which one did it.
- **Use adversarial validation for precision.** See R2.
- **Treat one pass as a result.** See the variance section.

## Held-out result

The gate ran once on the 30 held-out pull requests: three passes each of Mira,
open-nitpick as it shipped plus R1's confidence and citation fields, and that
same output kept only when the offline checker confirmed it
(`tools/bakeoff/verify_filter.py`). A fourth arm ran the Go stage
(`review.verify: confirmed`, `internal/review/verify.go`) live, three passes.
All used `deepseek/deepseek-v4.1-flash-1017` in every role and the same judge.
Counts are pooled over the three passes and over all golden categories, as in
`tools/bakeoff/compare.py`.

| arm (3 passes) | TP | FP | FN | precision | recall | F1 | F2 |
|---|---:|---:|---:|---:|---:|---:|---:|
| Mira | 158 | 57 | 157 | 73.5% | 50.2% | 0.596 | 0.536 |
| open-nitpick | 185 | 263 | 130 | 41.3% | 58.7% | 0.485 | 0.542 |
| open-nitpick, offline-verified | 165 | 165 | 150 | 50.0% | 52.4% | 0.512 | 0.519 |
| open-nitpick, Go verify, live | 149 | 161 | 166 | 48.1% | 47.3% | 0.477 | 0.475 |

Per pass, F1 for Mira was 0.586, 0.616 and 0.588; offline-verified was 0.484,
0.495 and 0.556; the live Go stage was 0.442, 0.528 and 0.458. Mira is ahead in
every pass of every arm.

Paired bootstrap over the 30 pull requests, 95% interval, each arm minus Mira:

| arm | precision | recall | F1 |
|---|---|---|---|
| open-nitpick | -32.2 pts (-41.0 to -23.3) | +8.6 (+0.4 to +16.2) | -0.111 (-0.169 to -0.052) |
| offline-verified | -23.5 (-32.7 to -14.4) | +2.2 (-5.9 to +9.9) | -0.085 (-0.146 to -0.023) |
| Go verify, live | -25.4 (-34.4 to -16.8) | -2.9 (-12.2 to +5.8) | -0.119 (-0.194 to -0.047) |

**The win condition failed on every line it set.** F1 was to be 0.08 above
Mira's in every pass; the best open-nitpick pass (0.556) is below the worst Mira pass (0.586). F2 was to be 0.08 above and
is level at best (0.542 and 0.519 against 0.536). Precision was to reach 60% and
reached 50%. Median review time was to be no worse than Mira's: Mira's median was
76 seconds a review and open-nitpick's 131 (133 with the Go stage), though all
nine passes shared one machine and one provider key, so that gap is indicative.
The planted-defect corpus was not run, because a stage that fails the first
three conditions does not ship on the strength of the fourth.

What held and what did not:

- **Verification still helps against its own baseline.** Against the unfiltered
  output it moved precision by +8.7 points (interval +4.5 to +12.7) and recall by
  -6.3 (-9.2 to -3.7), and F1 by +0.027, an interval (-0.006 to +0.054) that
  includes zero. On the tuning set the same filter moved F1 by +0.069. The held-out
  gain is smaller than the tuning one, as the variance section warned.
- **The tuning-set claim that it closes most of the F1 gap did not hold.** It
  reported a gap of -0.04; the held-out gap is -0.085.
- **The Go stage did not reproduce the offline filter.** It scored 0.035 F1
  below it (interval -0.072 to +0.001), with 5 points less recall and a
  precision no better. These are different generations, not the same findings
  filtered twice, so the difference mixes the stage with the variance between
  passes. It does not show the live stage is worse; it shows it did not do better.
- **Recall is the one axis open-nitpick led, and only unfiltered.** 58.7% against
  Mira's 50.2% (interval +0.4 to +8.6 points over three passes), bought with 263
  false positives against 57.

Two harness faults surfaced and are recorded so a later reader can discount them.

- **The judge's grouping answers.** The upstream scorer rejects a grouping that
  repeats or omits an index. Twice that stopped scoring on an answer a
  temperature-0 judge repeats unchanged, once because numbered lists inside a
  comment body read as extra candidates. `tools/bakeoff/mira-scorer-dedup-repair.patch`
  re-asks up to three times, labelling each candidate and quoting the rejected
  answer, and caches the accepted one under the original prompt. Every arm gets
  the same repair. Two grouping prompts needed it, both Mira's (pass 3, grafana-6
  and keycloak-6); no open-nitpick arm needed any. A repaired grouping can merge
  duplicate comments, which lowers a false-positive count by at most one in about
  215, so it cannot account for a gap of this size, but it is not neutral.
- **One stalled live review.** In the Go arm's third pass one checker call never
  answered and held its finding for the full five minutes, failing the stage and
  the review (exit 2). That is the same provider behaviour the `firstAttemptMaxTokens`
  work bounded for review calls, and the stage had no per-call deadline. It now
  has one (`verifyCallTimeout`, 90 seconds, one resend), with tests that fail
  without it. The review was run once more under the new binary and its result
  used; the stalled one is kept beside the runs as
  `gover-p3-sentry-9-r1.stalled`. The other 89 live reviews finished.

### What this means for the goal

Nothing built here, alone or stacked, beats Mira on this benchmark with this
model. The ceiling is visible in the table: precision between 41% and 50% for
every open-nitpick arm, against 73.5%, and a recall edge that disappears once
findings are filtered. The remaining levers in this plan do not reach it.

- R3 (cap by confidence) was measured offline at 56% precision for 3 comments a
  pull request, below 60%, and the cap loses recall the table shows is already
  gone.
- R4 aims at recall, and recall was never what Mira led on.
- Mira's own agentic loop, noise filter and self-critique are a different shape
  of tool, and matching its precision means matching what produces it: fewer,
  more carefully chosen comments, not more careful filtering of many.

Whether to keep chasing this benchmark is a product call, not a measurement.
The benchmark scores agreement with a short list of human-labelled defects, and
reading 14 of the 111 confirmed false positives found most were real defects
the list lacks. A tool that reports more real problems is penalised for it.

## Order, cost and time

| stage | depends on | estimate | model spend to measure |
|---|---|---|---|
| R0 | | 1 to 2 days | none |
| R1 | R0 | 2 to 3 days | three passes on DeepSeek, a few dollars |
| R2 | R0 | 2 days | three passes, a few dollars |
| R3 | R1, R2 | half a day | offline on stored runs |
| R4 | R2 | 3 to 5 days | three passes, a few dollars |
| final | all shipped stages | 1 day | three passes per tool on the 30 held-out pull requests, then Sonnet 4.6 at about $45 a pass |

R1 and R2 are independent and can be built in parallel. The final gate runs
Mira on the held-out split at the same time as open-nitpick, in the same session.

## Limits

- One judge model scores everything. The plan does not test a second judge, and
  a different judge could move both tools.
- The benchmark's golden comments are sparse and human-curated. A real defect
  nobody labelled counts against the tool that reports it, and precision gains
  can come partly from saying less about real problems. R0's per-comment output
  makes that checkable by hand on a sample.
- The time and cost figures are estimates from the bakeoff, not measurements of
  the new stages. open-nitpick's token use per review was not recorded; Mira's
  was, at about 443,000 prompt tokens a review.
