# Remediation: closing the precision gap to Mira

**Status:** proposed, nothing built
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
- Why: severity does not separate a real finding from noise here. The judge
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
