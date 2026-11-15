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
confirms and the judge does not match. A hand read of 14 of the 111 confirmed
false positives on the tuning set found most of them real-looking defects the
golden list does not contain (a migration with `force: true` that drops rows, a
hard-coded upload cap that ignores a site setting, an async `forEach` in a second
file), which suggested the benchmark was charging real findings as false positives.
That read was of checker-confirmed comments only. A random blind grade of all
unmatched held-out comments, below, found otherwise for the false positives as a
whole, so treat this paragraph as superseded by it.

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

### Is the gap reliability or volume

The same repository checker (`tools/bakeoff/verify_filter.py`, DeepSeek with
`read_file` and `grep_repo`) was run over Mira's 216 held-out comments, so both
tools are graded by one neutral party that matches neither tool's labels.

| held-out comments, 3 passes | comments | confirmed | refuted | uncertain | kept after the check: TP / FP | precision | recall | F1 |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| Mira | 216 | 200 (92.6%) | 1 | 15 | 150 / 50 | 75.0% | 47.6% | 0.583 |
| open-nitpick | 448 | 324 (72.3%) | 69 | 54 | 165 / 165 | 50.0% | 52.4% | 0.512 |

The check leaves Mira where it was (F1 0.596 before, 0.583 after, interval of the
change -0.030 to +0.001) and moves its precision by 1.5 points, an interval that
includes zero, so the checker is not what makes Mira's number look good. After it, open-nitpick is still 25 points
behind on precision (interval -33.4 to -16.6) and 4.8 ahead on recall (interval
-3.7 to +12.7).

Two effects are visible. The checker confirms 20 points fewer of open-nitpick's
comments, so Mira's generation is more reliable and not merely shorter. And among
what it confirms, open-nitpick still posts 165 comments that match no golden
comment against Mira's 50, about 55 a pass against 17. The checker reads those as
real, which is not the same as being real: the blind grade below found it too
lenient.

The checker is the same model family as both reviewers and shares its blind
spots, so this is indicative; the blind grade that follows tests it against hand
reading.

### A blind grade of the false positives

The question the gate left open was how many of each tool's unmatched comments
are real defects the golden list lacks, because that decides whether the
precision gap is quality or labelling. A random sample of the held-out unmatched
comments was drawn (30 of open-nitpick's 263 and 18 of Mira's 57, three passes
pooled, seed 20261114), shuffled so no item showed which tool wrote it, and split
across four graders, each a model with the repository checkout and the diff and
told to judge the claim against the code and not to reward confidence. A grader
returned `real`, `partly` (a genuine concern, overstated or misplaced),
`not_real` (contradicted, handled elsewhere, pre-existing or speculative) or
`unclear`. The graders are a different model family from the reviewers and the
checker, and 44 of the 48 verdicts cite a file and line.

| unmatched comments, sampled | n | real | partly | not real | unclear |
|---|---:|---:|---:|---:|---:|
| open-nitpick | 30 | 8 (27%, 95% interval 14 to 44) | 6 (20%) | 16 (53%) | 0 |
| Mira | 18 | 11 (61%, 39 to 80) | 5 (28%) | 2 (11%) | 0 |

Over half of open-nitpick's false positives are wrong by hand grade, against 11% of
Mira's, and most of Mira's are real defects the labels lack. That reverses the
earlier suggestion that our false positives were mostly unlabelled real defects.
Counting a real unmatched comment as correct, open-nitpick's precision moves from
41.3% to about 57% (50 to 67 across the sample's interval) and Mira's from 73.5% to
about 90% (84 to 95); counting `partly` as well, 69% and 97%. The gap is 32.7
points after the adjustment against 32.2 before it (28 counting `partly`), because
Mira has real unlabelled comments too. The precision gap is a quality gap.

The same sample says something about our checker. Against the hand grades it
confirmed all 8 `real` comments, 4 of 6 `partly`, and 7 of 16 `not_real`; it
refuted 6 and called 3 uncertain. Keeping only what it confirms therefore removes 9
of 16 wrong comments and 2 of 14 real or partly real ones. It filters usefully and
leaves 7 wrong comments in 19 kept. That pointed at a stronger checker, so one was
tried on the same comments.

#### A stronger checker, on the graded sample

`tools/bakeoff/verify_filter.py --model anthropic/claude-sonnet-4.6` over the 30
graded open-nitpick comments plus 20 randomly drawn matched ones as a recall
control (880,000 prompt tokens and 44,000 completion tokens, about $3 at list
price). Keeping only what each checker confirms:

| of 50 comments | DeepSeek V4 Flash | Sonnet 4.6 |
|---|---:|---:|
| wrong comments removed (of 16 `not_real`) | 9 | 6 |
| real, partly real or matched comments lost (of 34) | 4 | 1 |
| verdicts of "uncertain" | 6 | 0 |

The stronger model is not the better filter. It commits where DeepSeek hedges, so
it loses fewer good comments and removes fewer bad ones: 10 of the 16 wrong
comments are confirmed, against 7. At 4.9 lookups a comment it stops sooner than the
graders did. The wrong comments that survive are the ones that need a framework's
behaviour or a control flow read to rebut (a proxied controller property, a loop
that runs a pass on shutdown), and a checker with reasoning off and eight lookups
does not do that reading with either model. Whether one with reasoning on would is
untested; at this sample size a difference of two or three comments is noise.

Limits: 30 and 18 are small, and the intervals are wide; one family of grader read
all 48; and `partly` is a judgement. The sample, key and verdicts are in
`/tmp/bakeoff/grade`, not in the repository.

### R5. Generate with the repository open (registered 2026-11-13, before any run)

The one lever the gate leaves is the generator. Mira's reviewer reads files and
searches the repository while it decides what to file, and its prompt tells it to
look before staying silent. Ours reviews from the diff and what the bundle
carries, and a checker looks things up afterwards. R5 gives the reviewer the same
two tools in its own call (`review.investigate`), with `submit_findings` as the
way to finish, and changes nothing else: same prompt, same schema, same model.

It runs on the 20 tuning pull requests only. The 30 held-out ones were read once
and a second look is contaminated, so a result here is a reason to look for a new
corpus, not a win.

- **Ship to a fresh-corpus gate:** over three passes, paired against the same
  tuning set's three R1 passes, precision up 10 points or more, recall down 3 or
  fewer, and F1 at or above Mira's on the same 20 pull requests (0.505 and 0.524 in its
  two passes; the three R1 passes it is paired against scored 0.364, 0.425 and 0.424).
- **Kill:** any of the three fails. A precision gain with a larger recall loss is
  a cap, which the gate already measured.
- **Cost bound:** under twice the model calls of an R1 review, stated as measured
  calls, and review time reported with it.

#### R5 result: killed

Two builds, three passes each, on the 20 tuning pull requests, paired against the
same set's three R1 passes. R5a switched reasoning off for the whole generation
call, which the checker does for its lookups; R5b left the client's reasoning on
and echoed it back across tool turns, so the tools are the only change.

| arm (3 passes, 20 pull requests) | TP | FP | FN | precision | recall | F1 | median review |
|---|---:|---:|---:|---:|---:|---:|---:|
| R1, no tools | 105 | 211 | 99 | 33.2% | 51.5% | 0.404 | 146 s |
| R5a, tools, reasoning off | 119 | 338 | 85 | 26.0% | 58.3% | 0.360 | 70 s |
| R5b, tools, reasoning on | 88 | 186 | 116 | 32.1% | 43.1% | 0.368 | 218 s |
| Mira (2 passes) | 54 | 20 | 82 | 73.0% | 39.7% | 0.514 | |

| against R1 | precision | recall | F1 |
|---|---|---|---|
| R5a | -7.2 pts (-11.7 to -1.9) | +6.9 (-1.2 to +15.2) | -0.044 (-0.090 to +0.015) |
| R5b | -1.1 (-7.4 to +4.7) | -8.3 (-14.2 to -2.0) | -0.036 (-0.088 to +0.024) |

The ship condition was precision up 10 points, recall down at most 3, and F1 at
or above Mira's 0.514. Neither build comes near any of the three. R5a lowers
precision; R5b leaves it where it was and loses recall. The two differ from each
other by what the thinking does: with it off the reviewer files more (457 comments
against 274) and with it on, fewer; precision moves a little the other way
(26.0% to 32.1%, interval +0.5 to +10.2) and recall falls 15 points, which is the
trade a cap makes.

How much the reviewer looked: in R5b every one of the 102 investigated batches
used the whole 12-lookup budget, 8 turns at the median. A reviewer that looks up
something particular would stop sooner on a small change; this one reads until it
is stopped. 42 of 144 batches fell back to the ordinary review because the model
ended without submitting, so R5b is a mix of the two modes and understates what
tools alone would do. R5a fell back on 6 of 144.

Reasoning on also costs time: the median review went from 146 to 218 seconds, and
Mira's takes 76 on the held-out set. The option was removed with this result
rather than left on the books as a default-off stage nobody should enable.

### R6. A budget in the prompt (registered 2026-11-14, before it runs)

Every selector tried after generation, a cap by confidence, agreement between
passes, a repository check, ranks comments the model has already written, and
none ranks them as well as the model that wrote them chose. R6 moves the choice
before the writing: the review prompt states a budget of two findings a batch and
asks for the ones with the most severe demonstrated consequence if there are more.
The model then allocates its attention knowing it has two to spend. This is the
only change: same model, schema and context, no cap in code.

It runs on the 20 tuning pull requests, three passes, paired against the three R1
passes. It is a prompt edit, so it is measured by a build carrying the edit and
reverted if it fails.

- **Ship to a fresh-corpus gate:** precision up 10 points or more, recall down 3
  or fewer, F1 at or above Mira's 0.514 on these 20.
- **Kill:** any of the three fails. A precision gain bought with a larger recall
  loss is the cap the held-out diagnostic already measured.

#### R6 result: killed

Three passes on the 20 tuning pull requests with the budget in the prompt, paired
against the same set's three R1 passes. The reviewer posted 152 comments where R1
posted 316.

| arm (3 passes, 20 pull requests) | TP | FP | FN | precision | recall | F1 |
|---|---:|---:|---:|---:|---:|---:|
| R1 | 105 | 211 | 99 | 33.2% | 51.5% | 0.404 |
| R6, budget of two a batch | 76 | 82 | 128 | 48.1% | 37.3% | 0.420 |
| Mira (2 passes) | 54 | 20 | 82 | 73.0% | 39.7% | 0.514 |

Against R1: precision +14.9 points (+7.4 to +21.3), recall -14.2 (-24.5 to -5.1),
F1 +0.016 (-0.070 to +0.085). Per pass F1 was 0.393, 0.439 and 0.427. Against
Mira: precision -24.9 (-37.4 to -13.4), recall -2.5 (-14.8 to +9.1), F1 -0.094
(-0.204 to +0.019).

Precision cleared the ship condition and recall and F1 did not, so it is killed. It
is the cap again, reached through the prompt: the model that chooses before
writing lands where the post-hoc cap lands (at 2.5 comments a pull request the
held-out cap table gives 54% precision at 37% recall for unverified output), not
above it. The worth of this run is the comparison with Mira at matched recall:
R6 recalls 37.3% against Mira's 39.7% and posts about as many comments, and its
precision is still 25 points lower. At equal volume and equal recall, Mira's
comments are better aimed by 25 points, which is a difference in what the model
looks for and not in how many things it says. The template change was reverted.

### R7. What the prompt tells the reviewer to look for (registered 2026-11-14, before it runs)

R6 posted about as many comments as Mira, recalled about as much, and was still 25
points less precise, so the difference is in what the model goes looking for. Mira's
prompt is a set of review heuristics, and none of ours is: it names three places
defects hide (the consuming side of a value that crosses a boundary, what a refactor
silently dropped, callers and implementers that did not follow a signature change)
and a list of things not worth a finding. R7 adds both to our review prompt, in our
own words, and changes nothing else.

It runs on the 20 tuning pull requests, three passes, paired against the three R1
passes, and is reverted if it fails. The heuristics are general review practice and
none is taken from a golden comment.

- **Ship to a fresh-corpus gate:** precision up 10 points or more, recall down 3 or
  fewer, F1 at or above Mira's 0.514 on these 20.
- **Kill:** any of the three fails.

#### R7 result: killed

Three passes on the 20 tuning pull requests with the three heuristics and the
exclusion list in the review prompt, paired against the same set's R1 passes.

| arm (3 passes, 20 pull requests) | TP | FP | FN | precision | recall | F1 | comments |
|---|---:|---:|---:|---:|---:|---:|---:|
| R1 | 105 | 211 | 99 | 33.2% | 51.5% | 0.404 | 316 |
| R7, heuristics in the prompt | 92 | 192 | 112 | 32.4% | 45.1% | 0.377 | 282 |
| Mira (2 passes) | 54 | 20 | 82 | 73.0% | 39.7% | 0.514 | |

Against R1: precision -0.8 points (-4.8 to +3.2), recall -6.4 (-13.8 to 0.0), F1
-0.027 (-0.074 to +0.014). The prompt moved the reviewer's output by a small amount
and in no useful direction. So the 25 points of precision Mira has at matched
volume and recall are not in the review heuristics a prompt can state. R6 showed it
is not in how much the model says either.

One more selector, checked offline on the stored R1-build output
(`tools/bakeoff/citation_filter.py`): Mira discards a comment whose quoted code is
not in the diff, so the same rule applied to open-nitpick's `citation` field keeps a
comment only when it cites code present in the diff.

| stored output | comments | precision | recall | F1 |
|---|---:|---:|---:|---:|
| held-out, no filter | 447 | 41.3% | 58.7% | 0.485 |
| held-out, citation present and in the diff | 219 | 41.6% | 29.2% | 0.343 |
| tuning, no filter | 316 | 33.2% | 51.5% | 0.404 |
| tuning, citation present and in the diff | 135 | 32.1% | 21.1% | 0.254 |

More than half the comments carry no citation (167 of 447 on the held-out set),
because the field is optional, and the ones that have one are no more likely to
match. The rule halves recall and leaves precision where it was.

### Where Mira's results come from

Read from Mira's source (`core/engine.py`, `core/passes.py`, `core/noise_filter.py`,
`llm/prompts/templates/review.jinja2`, `config.py`) and measured by rebuilding what
each stage of its pipeline drafted and removed (`tools/bakeoff/mira_funnel.py`,
which joins the audit trail Mira stores with the drafts in its stored model calls;
the join matches the audit's draft count on every review checked).

What Mira does, in order, per chunk of at most 120,000 tokens (`config.py:66`):

1. **A reviewer that reads while it reviews** (`core/passes.py:326`). The model gets
   `read_file` and `grep_repo` beside the terminal `submit_review`, up to 10 turns, with
   its reasoning replayed on every turn. A model that stops calling tools, or runs
   out of turns, is not abandoned: the submission is forced over the whole transcript
   (`:86`). The system prompt tells it to verify a cross-file claim before filing it
   (`:172`). Over the 90 held-out reviews it made 12.7 tool calls a review.
2. **A prompt of review heuristics** (`review.jinja2`): trace the receiving side of a
   new value, audit what a rewrite dropped, check callers of a changed signature, plus
   explicit lists of what not to file and not to speculate about, and no comment quota.
3. **A noise filter in code** (`core/noise_filter.py:116`): confidence floor 0.7,
   duplicates merged, a cap of 5 on suggestions and nitpicks only.
4. **A self-critique** (`core/passes.py:942`): one batched call grades each draft
   `proven`, `plausible` or `unsupported` against the diff window around it, and a keep
   rule in code (`:921`) keeps `plausible` only at warning or above with confidence of
   0.75 or more.

What is off by default: the second look (`config.py:352`), the multi-run ensemble
(`config.py:291`). Neither ran in the bakeoff.

**The funnel.** Counts pooled over passes, held-out set (30 PRs, 3 passes) and tuning
set (20 PRs, 2 passes):

| stage | held-out precision / recall / F1 | tuning precision / recall / F1 |
|---|---|---|
| Mira, drafted (everything the model filed) | 53.9% / 63.5% / 0.583 | 50.6% / 57.4% / 0.538 |
| after the noise filter | 63.4% / 58.4% / 0.608 | 56.5% / 47.8% / 0.518 |
| posted, after the critique | 73.5% / 50.2% / 0.596 | 73.0% / 39.7% / 0.514 |
| open-nitpick, no filter | 41.3% / 58.7% / 0.485 | 33.2% / 51.5% / 0.404 |
| open-nitpick, verify on | 50.0% / 52.4% / 0.512 | |

The two filters buy about 20 points of precision with about 13 of recall and leave F1
where the drafts had it (+0.013 held-out, -0.024 tuning). They are the same trade a cap
makes. The noise filter was right to drop 65 of the 81 held-out comments it removed,
the critique 49 of 75. **Mira's lead is in the drafts**: its raw F1 is 0.098 above ours
on the held-out set and 0.134 above on the tuning set, from fewer comments (371 against
448) and more matches (200 against 185). The filters account for none of it; our own
verify stage gains slightly more F1 than Mira's two filters do.

Two things I had suspected are not it. Mira's confidence separates real from wrong no
better than ours (area under the curve 0.647 on its 463 drafts against 0.680 on our
447), so the floor at 0.7 is not a better signal than what we had. And the filters
do not inflate the benchmark number: on the tuning set they lower F1.

**Ablation.** Mira with `agentic_tools` and `security_agentic` off
(`MIRA_BAKEOFF_NO_AGENTIC`), same model, same prompt, 20 tuning PRs, two passes, no
tool calls in any audit trail:

| 20 tuning PRs | comments | precision | recall | F1 |
|---|---:|---:|---:|---:|
| Mira drafted, tools on (2 passes) | 154 | 50.6% | 57.4% | 0.538 |
| Mira drafted, tools off (2 passes) | 218 | 36.2% | 58.1% | 0.446 |
| open-nitpick R1 (3 passes) | 316 | 33.2% | 51.5% | 0.404 |
| Mira posted, tools on | 74 | 73.0% | 39.7% | 0.514 |
| Mira posted, tools off | 85 | 51.8% | 32.4% | 0.398 |

**Taking the tools away puts Mira's drafts where ours are** (36.2% precision at 58.1%
recall against our 33.2% at 51.5%) and the filters then cannot rescue it: posted F1
falls to 0.398, below our unfiltered 0.404. The tool loop is worth about 14 points of
precision at equal recall in the drafts, and it is what makes the later filters work.
The same ablation moves recall by under a point, so the tools make the model say
fewer wrong things and do not make it find more.

**Why R5 did not reproduce that.** R5 gave our reviewer the same two tools and
found no gain, so the loop is not enough by itself. What R5 lacked that Mira has:
a forced structured submission when the model stops (R5b fell back to a diff-only
review for 42 of 144 batches, Mira never discards its transcript); a turn budget
and not a lookup budget (every R5b batch exhausted its 12 lookups, so the model was
browsing, not checking); and a prompt that says what to go and check. R7 supplied that
prompt without tools and R5 supplied the tools without the prompt, so the
combination Mira runs has not been measured here. Its tuning-set ablation is
two passes of one model, so the 14 points carry the variance this plan already
documents; the direction is large enough to act on and the size is not exact.

### R8. Mira's loop, whole (registered 2026-11-14, before it runs)

The code review and the tools-off ablation say the gap is in the drafts and that the
tool loop is where Mira's precision comes from. R5 gave our reviewer tools and R7
gave it heuristics, and neither alone moved anything. R8 builds what Mira runs, as
one change, with the three things R5 lacked:

- tools, with reasoning on and echoed back across turns, as in R5b;
- a forced structured submission whenever the model stops without submitting or runs
  out of turns, over the whole transcript, so a finished investigation is never thrown
  away for a diff-only review (R5b discarded 42 of 144);
- a turn budget of 10 and a byte budget on tool output, and not a lookup budget, so
  the model decides how much to read;
- the review heuristics and the not-worth-a-finding list of R7, which say what to
  go and check.

It falls back to the ordinary review only when the first call fails before anything was
read. It runs on the 20 tuning pull requests, three passes, paired against the
three R1 passes, and is removed if it fails, as R5 was. It is off by default.

- **Ship to a fresh-corpus gate:** precision up 10 points or more, recall down 3 or
  fewer, F1 at or above Mira's 0.514 on these 20. The tools-off ablation says a
  working loop should add about 14 points of precision at unchanged recall.
- **Kill:** any of the three fails.
- **Reported with it:** forced submissions, turns and tool calls per batch, median
  review time, and calls per review against R1's.

Erratum, made after the first 13 batches had run and before any R8 score was read:
this stage first listed a fourth kill condition, that a forced submission on more
than a third of batches would mean the loop was not working. All 13 batches were
forced. Mira's own loop hit its 10-turn cap, and so was forced to submit, in 43 of
90 held-out chunks (48%), and 47 of the 90 used all 10 turns, so a loop that is
working is forced about half the time. The condition rested on a wrong assumption
and is withdrawn; the three numeric conditions are unchanged.

#### R8 result: failed, with two defects found in the build

Three passes on the 20 tuning pull requests, paired against the same set's R1 passes.

| arm (3 passes, 20 pull requests) | TP | FP | FN | precision | recall | F1 |
|---|---:|---:|---:|---:|---:|---:|
| R1 | 105 | 211 | 99 | 33.2% | 51.5% | 0.404 |
| R8, Mira's loop whole | 94 | 170 | 110 | 35.6% | 46.1% | 0.402 |
| Mira (2 passes) | 54 | 20 | 82 | 73.0% | 39.7% | 0.514 |

Against R1: precision +2.4 points (-4.3 to +9.8), recall -5.4 (-10.0 to 0.0), F1 -0.002
(-0.063 to +0.070). Per pass F1 was 0.453, 0.370 and 0.380. It fails all three ship
conditions. The reviewer was forced to submit in 108 of the 111 investigated batches, a
median of 10 turns and 20 lookups a batch, and a median review took 350 seconds against
146 for R1.

Two defects in the build, both visible in the logs and the SDK source and not in the
scores, so neither was chosen to rescue the result:

1. **The forced submission came back as prose, or empty, in 33 of 144 batches (23%),**
   which then fell back to a diff-only review. A reasoning model that spends the whole
   output budget thinking returns nothing, and `llm.Client` retries that with reasoning
   off for an ordinary review (`internal/llm/structured.go:201`); this loop called the
   SDK directly and had no such retry. Mira's provider has the equivalent
   (`_budget_exhausted_retry`).
2. **The model's reasoning was never replayed.** Our OpenRouter client is the SDK's
   OpenAI-compatible provider, whose `ConvertMessages`
   (`pkg/openaicompat/converters.go`) copies role, content and tool calls but not
   `Message.Reasoning`, although the request type has a `reasoning` field for it. So each
   turn re-thought from the tool results alone. Mira replays the reasoning of every turn
   in raw message fields (`core/passes.py:154`).

The first alone does not explain the result. On the 36 reviews where every batch was
investigated, R8 scored precision 40.8% and recall 44.3% against R1's 39.9% and 51.9% on
the same pull requests.

#### R8b: R8 with its two defects fixed (registered before it runs)

The same stage with the SDK replaying each assistant turn's reasoning on the wire and
the forced submission retried with reasoning off when it comes back empty at the
output cap. The reasoning replay is a one-line change to the SDK's converter, built
here from a patched copy of the module through an alternate `go.mod`, so neither this
repository's `go.mod` nor the SDK checkout is touched; a change to the SDK is made only
if this works. Same conditions as R8 and the same 20 pull requests, three passes, paired
against R1.

- **Ship to a fresh-corpus gate:** precision up 10 points or more, recall down 3 or
  fewer, F1 at or above Mira's 0.514.
- **Kill:** any of the three fails. This is the third build of one hypothesis, the second
  fixing a defect found in the source and not in the scores; a fail ends it.

#### R8b result: failed

Three passes on the 20 tuning pull requests, paired against the same set's R1 passes. The
loop ran as intended this time: 142 of 144 batches investigated (R8: 111), 2 fell back
(R8: 33), the reviewer submitted on its own in 22 and was forced in 120, 41 forced calls
came back empty at the output cap and every one was recovered by the retry without
reasoning, and the median review took 161 seconds (R8: 350, R1: 146).

| arm (3 passes, 20 pull requests) | TP | FP | FN | precision | recall | F1 |
|---|---:|---:|---:|---:|---:|---:|
| R1 | 105 | 211 | 99 | 33.2% | 51.5% | 0.404 |
| R8 | 94 | 170 | 110 | 35.6% | 46.1% | 0.402 |
| R8b, reasoning replayed, forced call retried | 90 | 169 | 114 | 34.7% | 44.1% | 0.389 |
| Mira (2 passes) | 54 | 20 | 82 | 73.0% | 39.7% | 0.514 |

Against R1: precision +1.5 points (-5.0 to +9.6), recall -7.4 (-14.7 to +0.6), F1 -0.015
(-0.077 to +0.059). Per pass F1 was 0.350, 0.435 and 0.385. Against R8: -0.9, -2.0 and
-0.013, all inside the interval. It fails the ship conditions, and the registration
said a fail ends this hypothesis.

**What this settles.** Giving our reviewer tools, a turn budget, a forced structured
submission that survives an empty reply, replayed reasoning and the review heuristics,
with the same model Mira runs on, does not reproduce the 14 points of precision that
switching the same things off costs Mira. The two defects the build had were real and
are fixed in this run, and fixing them moved nothing, so they were not why. What is
left is a difference in what is around the loop and not in the loop: Mira's
`grep_repo` ranks the files nearest the pull request first, its prompt carries a
language-by-language list of footguns and requires each comment to quote the code it is
about, its submission carries a summary and key issues the reviewer must reconcile with
its comments, and the review is preceded by a cross-file context pass. Which of those
does it is not known, and each would be a separate experiment on a stage that has now
failed three times.

**A defect found on the way, outside this repository.** `llm-go-sdk`'s OpenAI-compatible
`ConvertMessages` does not send an assistant turn's `Reasoning` back, which any caller
running a thinking model through a tool loop on OpenRouter would want. The change is one
guard (`tools/bakeoff/llm-go-sdk-reasoning-replay.patch`, with a test). It is not
applied here.

### Locating Mira's gain (registered 2026-11-14, before it runs)

Mira with tools off drafts at our level and with tools on drafts 14 points more
precisely, and our own tool loop in three builds did not reproduce that. So the gain is
in something that works with the tools. This switches off one of Mira's other parts at a
time, with the tools left on, on the same 20 tuning pull requests, two passes each, and
reads what each one costs the drafts. Mira's code is changed only through environment
switches in `tools/bakeoff/mira-replay-bakeoff.patch`; the default path is untouched.

| ablation | what is removed |
|---|---|
| no context | the cross-file code context placed in the prompt before the diff; the tools stay |
| no footguns | the per-language list of hazards in the prompt |
| no tool hint | the system-prompt section that tells the model to verify a cross-file claim before filing it; the tools stay |
| no heuristics | the three sections naming where defects hide: the receiving side, what a rewrite dropped, callers that did not follow |
| no exclusions | the two sections listing what not to file and what not to speculate about |

The measure is the drafted stage (everything the model filed, before the noise filter and
the critique), rebuilt with `tools/bakeoff/mira_funnel.py` and scored by the same judge:
default 50.6% precision, 57.4% recall, F1 0.538; tools off 36.2%, 58.1%, 0.446.

- **A part carries the gain** if removing it takes drafted precision down by 7 points or
  more (half the tools effect) with recall within 3 points.
- **Within the variance** otherwise. Two passes of 20 pull requests spread by about 0.03
  F1 and several points of precision (the two default passes scored F1 0.552 and 0.524),
  so a smaller shift is not read as a finding.
- **What each answer would mean.** A part that carries it is the next thing to try in
  our reviewer, singly. If none does, the gain is an interaction among them or in the
  tools themselves (their ranking and windows), and no ablation of this kind will
  find it.

#### Locating Mira's gain: result

Each part removed with the tools left on, 20 tuning pull requests, two passes, drafted stage.
The five switches were checked by rendering Mira's real prompt under each: the system
prompt is 15,262 characters by default, 11,868 without the footgun list (and no footgun
line), 12,704 without the three heuristic sections (exclusions and severity guide still
present) and 12,601 without the two exclusion sections. Every ablated run had 20 of 20
reviews, no failed chunk, and 14 to 16 tool calls a review, so the tools were in use in all.

| drafts, 2 passes pooled | comments | precision | recall | F1 | per pass F1 |
|---|---:|---:|---:|---:|---|
| default, tools on | 154 | 50.6% | 57.4% | 0.538 | 0.552, 0.524 |
| tools off | 218 | 36.2% | 58.1% | 0.446 | 0.427, 0.466 |
| no cross-file context | 169 | 49.7% | 61.8% | 0.551 | 0.539, 0.562 |
| no footgun list | 160 | 48.1% | 56.6% | 0.520 | 0.486, 0.553 |
| no tool-use hint | 166 | 50.0% | 61.0% | 0.550 | 0.555, 0.544 |
| no heuristics sections | 172 | 49.4% | 62.5% | 0.552 | 0.548, 0.556 |
| no exclusions sections | 181 | 45.3% | 60.3% | 0.517 | 0.557, 0.478 |

By the rule registered before it ran, **no part carries the gain**: none takes drafted
precision down by 7 points (the changes are -0.9, -2.5, -0.6, -1.2 and -5.3, against -14.4
for tools off). Removing the cross-file context, the tool-use hint or the heuristic
sections moves drafted precision by about a point and raises recall by 4 to 5 points, so
those parts do not help Mira's drafts at all. That agrees with R7 and R8, which gave our
reviewer the same heuristics with and without tools and moved nothing. The exclusions list
is the largest effect (-5.3 points, and the two passes scored F1 0.557 and 0.478) and is
inside the variance.

What the ablation does show is in the posted column: with any one part removed, Mira's
posted precision falls from 73.0% to 64 to 68% and posted F1 from 0.514 to between 0.437
and 0.518, although its drafts are no worse. The filters that follow, the confidence floor
and the critique, work best on the drafts the full prompt produces. Posted counts of 70 to
88 comments over two passes cannot separate that from a lucky default draw, so it is noted
and not relied on.

So the gain sits in the tools themselves, and what is left to explain is why our
loop (R8b: 34.7% precision, 44.1% recall) does not have it. The parts of Mira's loop that
no switch has touched are the tool semantics (`grep_repo` ranks the files nearest the
changed ones first and returns paths on request, `read_file` returns 12,000-byte
windows), the structured submission (each comment must carry a verbatim quote and an
agent prompt, and the submission a summary and key issues), the explicit
`reasoning_effort: medium`, and the transcript replay of every message key a provider
returns. A first attempt at this ablation ran with a broken switch, an undefined name
that failed every review chunk with a `NameError`, and its ten runs were discarded
unscored; the switches were then checked with a linter and a real review before the
ten runs above.

### Mira's tool semantics (registered 2026-11-14, before it runs)

The ablation above left the tools themselves. Reading Mira's `llm/agentic_tools.py`
against `internal/review/repotools.go` shows two differences that any loop of ours,
including the checker, would feel:

- **A capped read says where it stopped.** Mira's `read_file` fills a 12,000-byte window
  by lines and, when it cuts, reports "window capped at line N of TOTAL; call read_file
  with start_line=N+1 to continue". Ours returns up to 120 lines by default, cuts the
  text at 6,000 bytes, and appends a bare "[truncated]", so a model reading a large file
  sees roughly the first 75 lines and cannot tell where it ended.
- **A search is ranked.** Mira's `grep_repo` scans the files nearest the changed ones
  first, up to 40 files, and reports how many it scanned and how many it did not. Ours
  runs `git grep` over the whole tree and returns the first 30 hits in path order, with
  "(no matches)" when there are none and no note of coverage.

Two switches in the Mira harness make its tools behave as ours do: `MIRA_BAKEOFF_SHORT_READ`
(a 6,000-byte window, 120 lines by default, a bare "[truncated]") and
`MIRA_BAKEOFF_NO_RANK` (every file scanned in path order, the first 30 hits returned,
no coverage note). Same 20 tuning pull requests, two passes each, drafted stage.

- **The semantic matters** if removing it takes drafted precision down 7 points or more,
  the same rule as the parts above.
- **If either does,** the next build of our loop is these two tool changes and nothing
  else. If neither does, the tools' behaviour is not what separates our loop from
  Mira's, and this line of work stops.

### Tool semantics and Jev: results (unregistered probes)

Both were run as probes, not as registered stages, so they carry no ship condition.

**Tool semantics.** Mira's drafts with our 6,000-byte read window, with our unranked grep,
and with both, on the 20 tuning pull requests, two passes: F1 0.551, 0.559 and 0.571, against
0.538 with Mira's own tools. Our tool behaviour does not explain the precision gap: Mira's
drafts stay at 50 to 52% precision with our tools.

**Jev as a decision filter.** The endpoint is the one cleatus's `scripts/jev_parity.py` calls,
`https://openrouter.ai/api/alpha/decisions`, with model `~typesafe/jev-latest` (it resolves to
`typesafe/jev-1.14-20261014`). Each call costs about 0.002 cents and returns in about 0.3 s. The
probe asked one yes-or-no question per comment, with the claim and the diff window, on the 50
hand-graded comments (16 wrong, 34 good), two decisions each, `tools/bakeoff/jev_probe.py`.

| Jev probability | AUC, wrong vs good | removes wrong of 16 | loses good of 34 |
|---|---:|---:|---:|
| diff only, keep at 0.7 | 0.667 | 9 | 13 |
| diff and the checker's evidence, keep at 0.7 | 0.653 | 8 | 10 |
| diff and the checker's evidence, keep at 0.8 | 0.653 | 14 | 20 |
| DeepSeek checker, same run, confirmed only | | 10 | 5 |
| checker confirmed and Jev at least 0.7 | | 12 | 12 |

Repeats agree closely (median spread 0.01). The mean probability was about 0.72 for real
comments, 0.68 to 0.70 for partly real, 0.63 to 0.64 for wrong and 0.74 to 0.77 for matched
controls, so Jev sits near a prior and does not rank the comments. TypeSafe reports good
calibration, and the cleatus benchmark reports ECE 0.03 on policy-style questions. Calibration
is not discrimination, and neither benchmark is a code review.

Jev does not help as a filter on our findings. Its evidence-free AUC equals our own
self-reported confidence (0.68 held-out), and adding what the checker read does not improve it.
Not tested: other decisions (routing, severity), other question wording or criteria, and Jev on
whole files instead of the checker's evidence. The probe cost $0.02 in total. The probe sent
public diffs to the same provider as every other call in this work.

### Correction: tool semantics at the posted stage

The tool-semantics line above reported drafts only. At the posted stage, 20 tuning PRs,
two passes, Mira's own pipeline with our tools: default precision 73.0%, recall 39.7%, F1
0.514 (74 comments); our short read windows 75.6%, 45.6%, 0.569 (82); our unranked grep
73.8%, 43.4%, 0.546 (80); both 74.6%, 39.0%, 0.512 (71). Our tool semantics do not lower
Mira's precision at the posted stage either. The short-read arm's F1 gain over the default
is about 0.05 on two passes and is not separated from the variance the document already
records.

### Reasoning replay: Mira's loop, registered 2026-11-14 before it runs

Mira replays each assistant turn's reasoning on the next request, both the `reasoning` text
and the `reasoning_details` field (`core/passes.py`, `_REASONING_REPLAY_KEYS`). In its stored
run it sent 51 of 61 assistant turns with both. Our loop, through the SDK, replays only the
`reasoning` text (R8b), and drafts at 34.7% precision where Mira, with its tools, drafts at
50.6%. Two ablations of Mira's replay, with the same model, tools and prompt, 20 tuning PRs,
two passes each:

- **none**: no reasoning replayed (`MIRA_BAKEOFF_NO_REASONING_REPLAY`);
- **text only**: only the `reasoning` text replayed, which is what R8b sends
  (`MIRA_BAKEOFF_TEXT_REASONING_ONLY`).

Reading, against the default drafts at 50.6% precision and 57.4% recall:

- **The gap is `reasoning_details`** if text only drops drafted precision by 7 points or
  more with recall within 3 points. The next build then carries that field through the SDK.
- **The gap is not replay** if text only matches the default. The replay line of work then
  ends.
- **Neither reading** if only none drops: that would contradict R8b, and it is reported as such.

### R9: whole-PR batching (registered 2026-11-14, before it runs)

Our default batches at most 6 files, under a token budget of 60,000
(`internal/config/defaults.go:94-95`). Mira sends each pull request in one call of up to
120,000 tokens: on the 20 tuning pull requests it made one call per pull request, 20 of 20.
In R1, 12 of the 20 were split into 2 to 10 batches, and comments per review rose with the
batch count (2.8, 4.8 and 9.9 for one, two and three or more batches). Pull request size
confounds that, and Rule 8 of `docs/measurement.md` records that batching has never been
measured as a variable.

R9 is the R1 build with `review.max_files_per_request: 60` and
`review.token_budget_per_request: 120000` (`tools/bakeoff/r9-whole.yaml`). Nothing else
changes: same model, prompt, and verify stage off. It runs on the 20 tuning pull requests,
three passes, paired against the three R1 passes.

- **Ship to a fresh-corpus gate:** precision up 10 points or more, recall down 3 or fewer,
  and F1 at or above Mira's 0.514.
- **Kill:** any of the three fails. If R9 fails, the next run is the R8b tool loop with
  whole-PR batching, rebuilt from commit 2ac4bfc, registered separately before it runs.
- **Reported with it:** batches per pull request, tokens per call, pull requests whose one
  batch still exceeds 120,000 tokens, and median review time.

#### Reasoning replay: result

Mira's own loop, same model, tools and prompt, 20 tuning pull requests, two passes.

| arm | drafts: precision, recall, F1 (comments) | posted: precision, recall, F1 (comments) |
|---|---|---|
| default, every key replayed | 50.6%, 57.4%, 0.538 (154) | 73.0%, 39.7%, 0.514 (74) |
| text only (what R8b sends) | 49.4%, 58.8%, 0.537 (162) | 67.5%, 41.2%, 0.511 (83) |
| no replay | 40.3%, 70.6%, 0.513 (238) | 59.1%, 50.0%, 0.542 (115) |

The registered reading is "not replay": text only matches the default on drafts (-1.2
points of precision, +1.4 of recall), so `reasoning_details` is not the carrier. The replay
line of work ends. This also removes the R8b explanation: our loop already replays the text,
and Mira's loop with text alone drafts at 49%, against 35% for ours. Replay does shape the
balance. With no replay at all, Mira files more and is less precise. Its posted F1 of 0.542
is above the default's 0.514 on two passes, but its precision is 59%, so it fails the
registered precision condition, and the difference is inside the variance recorded above.

#### R9 result: whole-PR batching lowers recall and does not raise precision

Three passes on the 20 tuning pull requests, no verify stage, paired against R1. Whole-PR
batching took 51 of 60 reviews to one batch (the other 9 had two), and no batch exceeded
120,000 tokens; median review time was 119 seconds against 146.

| arm | precision | recall | F1 | comments |
|---|---:|---:|---:|---:|
| R1, 6-file batches | 33.2% | 51.5% | 0.404 | 316 |
| R9, whole PR | 27.8% | 43.1% | 0.338 | 316 |

R9 minus R1: precision -5.4 points (-13.3 to +2.0), recall -8.3 (-13.4 to -3.2), F1 -0.065
(-0.128 to -0.003). It fails its ship condition, and it moved the numbers down. Without tools,
seeing the whole pull request does not help our reviewer, which matches the ablation above:
whole-PR context did not help Mira's drafts either.

### R10: the tool loop with whole-PR batching (registered 2026-11-14, before it runs)

R9 tested batching without tools. The interaction is untested: R5, R5b, R8 and R8b ran the tool
loop per 6-file batch, and Mira's loop checks cross-file claims against the whole diff. R10 is
the R8b loop, built from commit 2ac4bfc, with the text reasoning replay of
`tools/bakeoff/llm-go-sdk-reasoning-replay.patch` (Mira's default replays text, and the replay
ablation showed that with no replay at all Mira files more and is less precise, so the patch
keeps the comparison fair). It runs with `review.investigate: true`,
`review.max_files_per_request: 60` and `review.token_budget_per_request: 120000`
(`tools/bakeoff/r10-whole-investigate.yaml`), three passes on the 20 tuning pull requests,
paired against R1 and R8b.

- **Ship to a fresh-corpus gate:** precision up 10 points or more against R1, recall down 3
  or fewer, and F1 at or above Mira's 0.514.
- **Kill:** any of the three fails.
- **Reported with it:** forced submissions, turns and lookups per batch, batches per review,
  median review time, and calls per review.

#### R10 result: killed by its recall and F1 conditions

Three passes on the 20 tuning pull requests, no verify stage, paired against R1 and against
Mira's two default passes. Batching: 51 of 60 reviews in one batch, 9 in two. The loop: 68
batches investigated, 58 forced to submit, 10 submitted on their own, 1 fell back to the
ordinary review. Median turns 10 and lookups 17 per batch. Median review time 137 seconds
(R1 146). Forced calls that came back empty at the output cap were retried without reasoning,
20 times.

| arm | precision | recall | F1 | comments |
|---|---:|---:|---:|---:|
| R1, 6-file batches | 33.2% | 51.5% | 0.404 | 316 |
| R8b, 6-file loop | 34.7% | 44.1% | 0.389 | 259 |
| R9, whole PR, no tools | 27.8% | 43.1% | 0.338 | 316 |
| R10, whole PR, loop | 48.6% | 43.6% | 0.460 | 183 |
| Mira, default (2 passes) | 73.0% | 39.7% | 0.514 | 74 |

R10 minus R1: precision +15.4 points (+7.6 to +22.3), recall -7.8 (-13.1 to -1.8), F1 +0.056
(+0.005 to +0.102). R10 minus Mira: precision -24.3 (-38.1 to -12.4), recall +3.9 (-6.2 to
+14.2), F1 -0.054 (-0.148 to +0.040). Per pass F1 was 0.460, and R1's three passes were
0.364, 0.425 and 0.424.

It fails two of its three conditions (recall down 7.8, F1 below 0.514), so it is killed by
its own rule. It is still the first lever in this work to move precision by an interval that
clears zero, and the R9 and R10 pair says the whole-PR context helps only when the tool loop
reads it. The gap to Mira's precision is 24 points.

### R11: triage off (registered 2026-11-14, before it runs)

The R10 build runs a model triage pass after the reviewer. It dropped 16% of the findings it
saw on the tuning set (12 to 20% across passes; R1 dropped 26 to 43%), and nothing in this
work has measured whether those drops were right. R11 is the R10 build with that model call
skipped: dedupe still runs, every other stage is unchanged. The switch is bakeoff-only,
`NITPICK_BAKEOFF_NO_TRIAGE` (`tools/bakeoff/triage-off-bakeoff.patch`). A smoke review on
calcom-1 published all 11 findings the reviewer filed.

Three passes on the 20 tuning pull requests, paired against R10.

- **Ship to a fresh-corpus gate:** precision at least 43.2% (R1 plus 10 points), recall at least
  48.5% (R1 minus 3 points), and F1 at least Mira's 0.514.
- **Kill:** any of the three fails.
- **Read:** if recall rises and the precision cost is smaller than the recall gain, triage was
  removing real findings. If precision holds, its drops were duplicates or low-value findings.

#### R11 result: killed; triage removes mostly wrong comments

Three passes on the 20 tuning pull requests, paired against R10 (triage on).

| arm | precision | recall | F1 | comments |
|---|---:|---:|---:|---:|
| R10, triage on | 48.6% | 43.6% | 0.460 | 183 |
| R11, triage off | 44.1% | 47.5% | 0.458 | 220 |
| Mira, default (2 passes) | 73.0% | 39.7% | 0.514 | 74 |

R11 minus R10: precision -4.5 points (-11.1 to +1.9), recall +3.9 (-1.1 to +9.4), F1 -0.002
(-0.044 to +0.044). R11 misses its recall gate by about one point and misses its F1 gate, so
it is killed.

Triage's own drops, from the difference: R11 has 37 more comments, of which 8 matched a golden
comment and 29 did not. Triage removes wrong comments at 78%, so it is a good filter and not
the cause of the gap.

The gap is in draft recall. Per pass on the same 20 pull requests, Mira's drafts (before its
noise filter and critique) match 39 golden comments and file 38 unmatched. Our output with
triage off matches 32 and files 41. Mira finds about 7 more real findings per pass with
three fewer wrong comments.

#### Model: Sonnet 4.6, one pass (arm C, tuning set)

Mira with Sonnet 4.6 drafts and posts at F1 0.545 (precision 52.0%, recall 57.4%, 75
comments), and our original build with Sonnet 4.6 at F1 0.390 (precision 34.9%, recall 44.1%,
86 comments). Both are one pass, scored with the same judge and the counts that
`tools/bakeoff/compare.py` uses, which differ from the Core-profile figures in the arm table
above. A stronger model does not close the gap for the original build. R10 with Sonnet 4.6 has
not been run.

### The output cap took the reasoning out of most of our reviews (found 2026-11-14)

When `max_tokens` is unset, open-nitpick sends a reasoning model on OpenRouter a cap of
16,384 output tokens (`internal/llm/client.go:444`), and reasoning counts against it.
DeepSeek V4.1 Flash spent the whole cap thinking on most of our single-call reviews. Each
of those calls came back empty and was sent again with reasoning off
(`internal/llm/structured.go:201`). Mira has the same cap and the same fallback
(`mira/llm/base.py:217`), and its loop seldom reached it. Counted from the stored logs by
`tools/bakeoff/fallbacks.py`:

| arm | review calls a pass | sent again with reasoning off, each pass |
|---|---:|---|
| R1, 6-file batches | 48 | 39, 38, 36 |
| R9, whole PR | 23 | 19, 19, 20 |
| R10, whole PR, tool loop | 23 | 7, 7, 7 (forced submissions and review calls together) |
| R11, R10 with triage off | 23 | 6, 7, 9 (counted the same way) |
| Mira, default loop | 20 reviews | 1, 0 |
| Mira, tools off | 20 reviews | 10, 12 |
| held-out, ours (R1 build) | 85 | 45, 50, 46 |
| held-out, Mira | 30 reviews | 2, 0, 1 |

Most of our reviews on both sets were written without reasoning, and nearly all of Mira's
with it. Three readings above depend on that and are reopened:

- R9's "whole-PR batching lowers recall" compared two arms that each lost the reasoning on
  about four calls in five.
- The tools-off ablation in "Locating Mira's gain" compared Mira's loop, which reasoned on
  nearly every call, with a single call that lost its reasoning on about half the pull
  requests. On that evidence "the tool loop is the source" cannot be told apart from "the
  reasoning finished".
- The held-out gap was measured with the reasoning off on more than half of our review
  calls and almost none of Mira's.

### R12 and R13: room for the reasoning to finish (registered 2026-11-14, before they run)

R13 is R1 with `models.default.max_tokens: 131072` and `models.default.timeout: 20m`
(`tools/bakeoff/r13-batch-thinking.yaml`). R12 adds R9's whole-PR batching to the same two
settings (`tools/bakeoff/r12-whole-thinking.yaml`). Both use the R1 binary, model, prompt and
triage, with verify off, three passes each on the 20 tuning pull requests. R13 against R1
measures what finished reasoning is worth. R12 against R13 is the test R9 was meant to be:
whole-PR batching with the reasoning intact. The model's output limit is 943,718 tokens and
output costs $0.50 a million, so a call that uses the whole new cap costs about 7 cents. A
smoke run of R12 on three pull requests checks the settings first and is not scored.

- **Ship to a fresh-corpus gate (each arm):** precision up 10 points or more against R1,
  recall down 3 or fewer, and F1 at or above Mira's 0.514, the conditions R9 and R10 had.
- **Kill:** any of the three fails.
- **Batching reading:** R12 minus R13 on F1, with its interval. A positive difference with an
  interval that clears zero means whole-PR batching helps once the reasoning finishes, and
  R9's reading is withdrawn.
- **Validity:** an arm with more than 5% of its review calls still sent again with reasoning
  off is reported and not ranked.
- **Reported with it:** those fallbacks for each pass, median review time, comments per
  review, and model spend for each pass.

#### R12 and R13 result: neither ranked; a single call still loses its reasoning

Three passes each on the 20 tuning pull requests, the R1 binary, scored with the counts
`tools/bakeoff/compare.py` uses. The last column is from `tools/bakeoff/fallbacks.py`.

| arm | precision | recall | F1 | comments | review calls sent again without reasoning, each pass |
|---|---:|---:|---:|---:|---|
| R1, 6-file batches, 16,384-token cap | 33.2% | 51.5% | 0.404 | 316 | 39, 38, 36 of 48 |
| R13, 6-file batches, 131,072-token cap | 32.0% | 41.7% | 0.362 | 266 | 24, 24, 19 of 48 |
| R9, whole PR, 16,384-token cap | 27.8% | 43.1% | 0.338 | 316 | 19, 19, 20 of 23 |
| R12, whole PR, 131,072-token cap | 37.4% | 37.7% | 0.376 | 206 | 14, 11, 13 of 23 |
| Mira, default (2 passes) | 73.0% | 39.7% | 0.514 | 74 | 1, 0 of 20 reviews |

With a cap eight times larger, DeepSeek V4.1 Flash still thought past it on 40% to 61% of
single calls, so both arms fail their validity condition and neither is ranked. The batching
reading cannot be taken either: R12 minus R13 is F1 +0.014 (-0.062 to +0.081). R13 minus R1
lost recall (-9.8 points, -16.2 to -3.5), so more room to think did not help a single call.
What the pair shows is that no cap tried keeps this model's reasoning in one call, and the
loop does: the deep-review smoke had no fallbacks. The batching question moves to deep
review, where every call reasons.

## Deep review (registered 2026-11-14)

`nitpick review` stays as it is. `nitpick deep-review` is a second mode that reviews the
way Mira does and costs more. The differences it closes, each read from Mira's source and
stored runs:

| | Mira | `nitpick review` | `nitpick deep-review` |
|---|---|---|---|
| Shape | a general loop and a security loop per pull request, in parallel, up to 10 tool turns each (`core/passes.py:365`) | one call per batch of 6 files or 60,000 tokens, no tools | a general agent and a security agent per batch of up to 60 files or 120,000 tokens, in parallel, up to 10 turns each with `read_file` and `grep_repo` (`internal/review/deep.go`) |
| Sampling | temperature 0.2, effort medium (`config.py:60`) | the model's own: temperature 0, effort unset | `review.deep.temperature` 0.2 and `review.deep.reasoning` medium |
| Selection | confidence floor 0.7, cap of 5 on suggestions and nitpicks (`config.py:117-122`), then a critique grading each draft proven, plausible or unsupported (`core/passes.py:930`) | model triage | the same floor, duplicate rule, cap and critique, with Mira's keep rule (`internal/review/deepselect.go`) |

The security agent extends the review prompt with a security focus
(`internal/prompt/templates/deep_security.md`), so both agents file in one shape. An agent
that stops early or runs out of turns is asked to submit what it has; one failing does not
cost the others' findings; a batch every agent failed is reviewed by the ordinary call.
Reasoning replay between turns is off. `nocturnium/llm-go-sdk` v6.15.0 drops an assistant
turn's reasoning, the loop keeps it on each turn, and the D2r result below measured sending
it back as worse than not sending it.

The loop: each stage is registered here before it runs, measured with three passes on the
20 tuning pull requests paired against R1, R10 and Mira, kept only if it meets its
conditions, and the funnel (drafted, after the filter, posted, each scored) decides the next
stage. The claim is made on the held-out set; both splits have been read, so a fresh corpus
remains the stronger test.

### D0: the mode (built 2026-11-14)

`nitpick deep-review`, the `review.deep` configuration, and per-agent accounting of turns,
lookups, forced submissions, reasoning fallbacks and tokens. `-format json` now lists
withheld findings with the stage that held them, and `tools/bakeoff/deep_funnel.py`
rebuilds the drafted and after-filter stages from one run. Every guard test was seen to fail
when its behaviour was broken. A smoke review of keycloak-4 ran both agents to their 10 turns
(about 19 lookups each, no reasoning fallback), drafted 3 findings, kept 1, in 91 seconds.

### D1: deep review at its defaults (registered 2026-11-14, before it runs)

Two arms, three passes each on the 20 tuning pull requests, DeepSeek V4.1 Flash:

- **D1:** `nitpick deep-review` at its defaults.
- **D1g:** the same with `review.deep.agents: [general]`, which measures the security agent.

Both are paired against R1, R10 and Mira's two default passes, at the posted stage and, from
the same runs, at the drafted and after-filter stages.

- **Ship to the held-out gate:** D1's F1 above Mira's 0.514.
- **Otherwise the funnel picks the next stage:** drafts below Mira's drafts (F1 0.538,
  precision 50.6%, recall 57.4%) send the next stage to the agents; drafts at or above Mira's
  with posted F1 below send it to the selection.
- **Validity:** an arm with more than 5% of its agent runs failed, or of its forced
  submissions sent again without reasoning, is reported and not ranked.
- **Reported with it:** calls, turns, lookups and tokens for each review, model spend, and
  median review time.

Amended 2026-11-14, before any D1 score was read: the first D1 build lacked Mira's
duplicate rule, so two agents filing one defect posted it twice (calcom-1 posted one async
`forEach` defect twice on each of two lines). The partial runs were deleted unscored, and D1
restarted on the build with the rule (`deepDuplicate`, `internal/review/deepselect.go`).

### D2r: D1 with the reasoning replayed (registered 2026-11-14, before it runs)

Mira replays each turn's reasoning to the next; deep review cannot until
`nocturnium/llm-go-sdk` sends it. D2r is the D1 build compiled against
`tools/bakeoff/llm-go-sdk-reasoning-replay.patch` (it applies to v6.15.0 and its test passes
there) through an alternate modfile, so the repository's `go.mod` is unchanged. Everything
else is D1's defaults. Three passes on the 20 tuning pull requests, run beside D1 and paired
against it.

- **Keep:** F1 above D1's with precision down 2 points or fewer, which would make the SDK
  change worth releasing.
- **Ship to the held-out gate:** F1 above Mira's 0.514, as for D1.
- **Validity:** D1's.

### Judge change (registered 2026-11-14, before any rescored number is read)

Every number above was judged by the upstream benchmark's judge, `anthropic/claude-sonnet-4.5`.
From here the primary judge is `openai/gpt-6.1-sol`, with `openai/gpt-6.1-terra` and
`openai/gpt-6-luna` reported beside it, on the maintainer's direction that these track human
review most closely. Only `--judge-model` changes; the scorer, its prompts and its
deduplication are the upstream ones. The published competitor rows stay under the old judge
and are not compared.

Mira is not re-run. Its stored runs, the open-nitpick runs this record compares, and the
scoring inputs are frozen outside the repository with a SHA-256 manifest and no write
permission, and every arm is rescored from those files, so a difference between judges is
the judges' alone. Sonnet-judged numbers above stay as history. A comparison is made only
between arms scored by one judge, and a ship or kill condition that names a number taken
under the old judge now means the same quantity rescored by GPT-6.1 Sol.

A two-case smoke (calcom-1, keycloak-4) ran all three. On Mira's comments, Sol and Luna
matched Sonnet's counts and Terra counted one fewer true positive on calcom-1.

#### Rescored under the new judges

Every compared run, scored again from the frozen files under GPT-6.1 Sol, Terra and Luna.
The last column is each arm's F1 minus Mira's, with the 95% interval from the paired
bootstrap over pull requests. Mira's own F1 moved by 0.012 across the three judges. The order
of the arms is the same under each, except among R10, R11 and D1, which sit within 0.014 of
each other and swap places.

Tuning set, 20 pull requests, Sol (primary):

| arm | precision | recall | F1 | TP, FP | F1 minus Mira |
|---|---:|---:|---:|---|---|
| Mira, default (2 passes) | 69.4% | 36.8% | 0.481 | 50, 22 | |
| R1, 6-file batches | 27.6% | 42.6% | 0.335 | 87, 228 | -0.146 (-0.223, -0.052) |
| R10, whole PR, tool loop | 43.5% | 39.2% | 0.412 | 80, 104 | -0.068 (-0.150, +0.019) |
| R11, R10 with triage off | 40.2% | 43.1% | 0.416 | 88, 131 | -0.065 (-0.153, +0.037) |
| D1, deep review | 56.3% | 32.8% | 0.415 | 67, 52 | -0.066 (-0.148, +0.010) |
| D1g, general agent only | 53.3% | 31.4% | 0.395 | 64, 56 | -0.086 (-0.180, +0.010) |
| D2r, D1 with reasoning replayed | 48.8% | 30.9% | 0.378 | 63, 66 | -0.102 (-0.201, -0.002) |

F1 under the other two judges: Terra has Mira 0.469, R1 0.342, R10 0.407, R11 0.421, D1 0.415,
D1g 0.395, D2r 0.366. Luna has Mira 0.478, R1 0.352, R10 0.441, R11 0.431, D1 0.430, D1g 0.410,
D2r 0.382. Held-out set, 30 pull requests, three passes each, Mira against the R1 build:

| judge | Mira precision, recall, F1 | R1 build precision, recall, F1 | F1 difference |
|---|---|---|---|
| Sol | 66.2%, 46.7%, 0.547 | 36.3%, 52.1%, 0.428 | -0.120 (-0.180, -0.062) |
| Terra | 67.9%, 47.0%, 0.555 | 38.1%, 54.6%, 0.449 | -0.106 (-0.168, -0.047) |
| Luna | 72.2%, 49.5%, 0.588 | 38.8%, 55.2%, 0.455 | -0.132 (-0.186, -0.073) |

The three arms that read the repository and filter (D1, D1g, D2r) kept the precision and lost
recall, as before. The security agent helps (D1 over D1g, 0.415 against 0.395), and replaying
reasoning hurts (D2r below D1), under every judge.

### D3m: Mira's agent prompts (registered 2026-11-14, before it runs)

D2r's drafts were 34.4% precise and recalled 47.5% (Sonnet judge), against 50.6% and 57.4% for
Mira's drafts from the same model, loop shape and selection rules. The largest difference
left is what the agents are told. Ours extends the review prompt, written for one call;
Mira's tells the agent to file only at confidence 0.7 or more, to trace a new value to the
code that consumes it, to treat removed lines as a specification, to follow a changed
signature to every caller and implementer, and not to speculate past what it can cite.

D3m is the D1 build with both agents' system prompts replaced by Mira's general and security
prompts, adapted to the findings schema (field names, severity scale, class list; Apache
License 2.0, `/tmp/bakeoff/d3m/adapt.py` makes the adaptation), built in a worktree so the
repository is unchanged unless the stage is kept. Because D1's forced submissions came back
empty at the 16,384-token cap 16% to 34% of the time, the forced submission in this build
also gets a 65,536-token cap and a 10-minute deadline; the reasoning-off retry stays as the
last resort. A two-case smoke still had 1 of 3 forced submissions run out at 65,536.

Three passes on the 20 tuning pull requests, scored by GPT-6.1 Sol, Terra and Luna, paired
against D1 and Mira under the same judge.

- **Keep, and write our own prompts to this structure:** drafted precision up 8 points or
  more over D1's drafts under Sol, with drafted recall not lower.
- **Ship to the held-out gate:** posted F1 above Mira's under Sol.
- **Validity:** D1's. The forced-submission fallbacks are reported either way.

#### D3m result: at Mira's F1, by a different route

Three passes on the 20 tuning pull requests, scored by all three judges. One review
(discourse-4, pass 1) came back incomplete: its critique was cut at the output cap and
returned no grades. It was re-run once.

| judge | D3m precision, recall, F1 | Mira F1 | D3m minus Mira, F1 | D3m minus D1, F1 |
|---|---|---:|---|---|
| Sol | 56.5%, 42.6%, 0.486 | 0.481 | +0.005 (-0.064, +0.088) | +0.071 (+0.013, +0.143) |
| Terra | 54.8%, 42.2%, 0.476 | 0.469 | +0.008 (-0.060, +0.090) | +0.062 (+0.002, +0.136) |
| Luna | 57.0%, 44.1%, 0.497 | 0.478 | +0.019 (-0.060, +0.113) | +0.067 (+0.001, +0.144) |

D3m is the first arm whose F1 is not below Mira's, and the interval says it is level with it,
not above it. It gets there with 13 points less precision (-24.1 to -2.9 under Sol, which
excludes zero) and 5.9 points more recall (-2.1 to +14.5). Against D1 the gain is recall: +9.8
points (+4.0 to +16.7), with precision unchanged (+0.2).

Drafts, Sol. D3m's drafts are 36.0% precise with 57.8% recall, D1's 34.4% and 51.0%, Mira's
44.2% and 50.7%. The registered condition for writing our own prompts to Mira's structure
was drafted precision up 8 points over D1's with recall not lower, and it fails: precision is
up 1.5 points (-5.4 to +8.1), recall up 6.9 (+0.5 to +13.8). Mira's prompts did not make the
agents more precise. The registered ship condition, posted F1 above Mira's under Sol, holds
as a point comparison (0.486 against 0.481) and not as an interval.

Two things changed from D1, the prompts and the forced-submission cap, so this result cannot
say which one carried the recall. D1c separates them. Validity: 5 of D3m's 98 forced
submissions (5.1%; 2.9%, 6.3% and 6.5% by pass) were sent again without reasoning, which is
3.6% of its agent runs, against a registered limit of 5% that the strict reading puts just
over. D1's were 13% to 26% of its agent runs.

### D1c: the repository build (registered 2026-11-14, before it runs)

D3m changed two things at once from D1: Mira's prompts and a larger forced-submission cap.
The repository build has the cap, a ladder behind it (low effort, then none), a critique
that is asked again when it is cut mid-answer, and a report that names a failed agent or a
batch reviewed without tools, all with our own prompts. D1c is that build at its defaults,
three passes on the 20 tuning pull requests, scored by Sol (primary), Terra and Luna, paired
against D1 (what the fixes change), D3m (what the prompts change) and Mira.

- **Reading:** D3m minus D1c, posted F1 under Sol, with its interval. An interval that
  excludes zero says the prompts carry part of D3m's gain, and the next stage writes our own
  prompts to D3m's structure. An interval that includes zero keeps the repository's prompts.
- **Ship to the held-out gate:** D1c's posted F1 above Mira's under Sol, as a point
  comparison like every earlier stage. The held-out run is made once, on the build that is in
  the repository, and nothing is tuned on its result.
- **Validity:** more than 5% of forced submissions sent again without reasoning, or any
  failed agent, is reported and the arm is not ranked. A review that comes back incomplete is
  re-run once and both runs are reported.

### D1t: twenty investigation turns (registered 2026-11-14, before it runs)

Most agents stop at the turn cap: 70% of D3m's agent runs (97 of 138) ended in a forced submission, and
60% of Mira's loops hit its 10-hop cap. Neither tool's misses on the tuning set are cheap
lookups. Of the 28 golden comments neither tool's drafts matched, the ones that name code
elsewhere (the delegate that `getForLogin` should call, the key a validator reads, a
queryset that cannot take a negative slice) take more reading than ten turns of a
flash-class model gives them. D1t is D1c with `review.deep.max_turns: 20`
(`/tmp/bakeoff/cfg/d1t-turns20.yaml`) and nothing else changed. Three passes on the 20
tuning pull requests, scored by Sol, Terra and Luna, paired against D1c.

- **Adopt 20 as the default:** posted F1 under Sol above D1c's by 0.03 or more.
- **Otherwise 10 stays,** and the cost of the extra turns is reported against what they bought.
- **Reported with it:** turns, lookups and tokens per review, model spend, forced
  submissions, and drafted recall against D1c's.

### D1h: high reasoning effort (registered 2026-11-14, before it runs)

Mira reviews at medium effort, as does every deep arm so far. The golden comments neither
tool matched are mostly semantic: a language rule the model has to recall, or a pattern in
code it has read. D1h is D1c with `review.deep.reasoning: high` and nothing else
(`/tmp/bakeoff/cfg/d1h-high.yaml`). Three passes on the 20 tuning pull requests, scored by
Sol, Terra and Luna, paired against D1c. A higher effort runs out of output budget more
often, so the forced-submission steps are counted.

- **Adopt high as the default:** posted F1 under Sol above D1c's by 0.03 or more.
- **Otherwise medium stays,** and the cost of the extra thinking is reported.
- **Several knobs:** D1t and D1h each change one setting. If both are adopted, the
  combination is run on the tuning set before the held-out gate, not assumed.

### L1: the repository build on GPT-6 Luna (registered 2026-11-14, before it runs)

Every arm so far is DeepSeek V4.1 Flash, the model Mira was frozen on. GPT-6 Luna costs about
the same ($0.10 and $0.50 per million tokens) and takes no temperature, which makes it the
test of the temperature handling added to the client and of whether the mode depends on one
model. L1 is the repository build at its defaults with `LLM_MODEL=openai/gpt-6-luna`,
three passes on the 20 tuning pull requests, scored by Sol, Terra and Luna.

It is descriptive. Mira has no frozen run on this model, so L1 is not ranked against it, and
no ship condition is attached. It is read against D1c, to see what the model changes, and for
two counts: reviews that completed, and agent turns that were refused for a temperature.

### D3o: our own deep base prompt (registered 2026-11-14, before it runs)

D3m's recall gain could come from Mira's prompt or from the forced-submission cap, and D1c
separates the two. D3o prepares the answer in case it is the prompt. Our review prompt is
written for a reviewer with the diff alone: report nothing it cannot demonstrate, choose the
lower level when torn, drop a finding that needs code it was not shown. A deep agent reads
the repository and is followed by a critique, so those rules ask it to be silent about what
it has tools to check. `internal/prompt/templates/deep_review.md` is written for it. It
keeps our anchoring, severity, confidence, citation and suggestion rules, which the filters
depend on, and asks the agent to file every defect it can defend, follow a change to the code
that receives it, treat the removed lines of a refactor as its specification, and check every
implementer and caller of a changed contract, with lists of what not to file and what not
to speculate about. It is our own text, written from those practices and not from Mira's.
The persona, model notes and run instruction layer over it as before.

D3o is the repository build at its commit `ea01431` with nothing else changed: three passes on
the 20 tuning pull requests, scored by Sol, Terra and Luna, paired against D1c, D3m and Mira.

- **Keep the deep prompt:** posted F1 under Sol at least D1c's. Otherwise the commit is
  reverted before the held-out gate and the deep agents go back to the ordinary base prompt.
- **Reading:** D3o minus D1c and D3o minus D3m, each with its interval, so the record shows
  whether our text does what Mira's did.
- **Ship to the held-out gate:** kept, and posted F1 above Mira's under Sol as a point
  comparison.
- **Reported with it:** drafted recall and precision against D1c's and D3m's, and how many
  agents used their time budget.

### What this means for the goal

Nothing built here, alone or stacked, beats Mira on this benchmark with this
model. The ceiling is visible in the table: precision between 41% and 50% for
every open-nitpick arm, against 73.5%, and a recall edge that disappears once
findings are filtered. The remaining levers in this plan do not reach it.

- R3 (cap by confidence) is killed. Run after the gate on the stored held-out
  passes, ranking each pull request's comments by the reviewer's confidence and
  keeping the top N, it never gets F1 past 0.517 for any N, against Mira's 0.596.
  This is a post-hoc diagnostic: N was read off the data, so it flatters the cap,
  and it still loses. The unfiltered counts (185 TP, 263 FP, 130 FN) reproduce the
  table above, which is the check that the simulation matches the scorer.

  | keep per pull request | unverified: precision / recall / F1 | offline-verified: precision / recall / F1 |
  |---|---|---|
  | all | 41.3% / 58.7% / 0.485 | 50.0% / 52.4% / 0.512 |
  | 6 | 49.0% / 52.4% / 0.506 | 54.2% / 49.5% / 0.517 |
  | 4 | 51.1% / 43.2% / 0.468 | 57.8% / 43.5% / 0.496 |
  | 3 | 54.0% / 36.8% / 0.438 | 59.4% / 38.1% / 0.464 |
  | 2 | 62.3% / 31.4% / 0.418 | 66.4% / 32.1% / 0.433 |

  Precision reaches 60% only at 2 a pull request, where recall has fallen to 32%.
  The reviewer's confidence does not rank well enough for a cap to remove noise
  faster than it removes defects. Mira posts 2.4 comments a pull request at 73.5%
  precision and 50% recall, so its selection happens before the comments exist.
- Agreement across passes is not the missing selector either. Keeping a comment
  only when another pass of the same reviewer commented within 5 lines of the same
  file gives open-nitpick F1 0.498 (unverified, up from 0.485) and 0.497
  (offline-verified, down from 0.512). It also lowers Mira's, from 0.596 to 0.577.
  `tools/bakeoff/selector_diagnostics.py` reproduces this and the cap table.
- The gap is selection, not discovery. Over the three passes open-nitpick's output
  matches 79 of the 105 golden comments and Mira's matches 62; only 5 are Mira's
  alone. Open-nitpick finds nearly everything Mira finds and posts it among 263
  false positives, and none of confidence, a repository check or agreement between
  passes separates the two well enough. Giving the reviewer the repository to read
  (R5, below) did not separate them either. What is left is a generator that
  chooses what to say before it says it, which is a new architecture and not a
  stage of this plan. Its evaluation needs a corpus neither tool has been scored
  on: the 30 held-out pull requests have now been read once.
- R4 aims at recall, and recall was never what Mira led on.
- R7 (review heuristics in the prompt) is killed above, and a verbatim-citation
  rule is no better.
- What the code review found is in "Where Mira's results come from". The combination it
  pointed at, tools with a forced submission, a turn budget, replayed reasoning and the
  heuristics together (R8, R8b), did not reproduce it.
- R6 (a budget in the prompt) is killed above; it reproduces the cap.
- R5 (the reviewer reads the repository while it files) is killed above. It was
  the generator change the plan had left, and it did not move precision.
- Mira's own agentic loop, noise filter and self-critique are a different shape
  of tool, and matching its precision means matching what produces it: fewer,
  more carefully chosen comments, not more careful filtering of many.

The benchmark scores agreement with a short list of human-labelled defects, but the
blind grade above shows that is not what explains the gap: over half of
open-nitpick's unmatched comments are wrong, against about a tenth of Mira's. The
gap is quality. A stronger checker without reasoning did not remove more of it, so
the lever is still unidentified.

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
- The benchmark's golden comments are sparse and human-curated, so a real defect
  nobody labelled counts against the tool that reports it. A blind grade of 48
  unmatched held-out comments sized it: 27% of open-nitpick's and 61% of Mira's
  were real, which moves both precisions up and leaves the gap where it was.
- The time and cost figures are estimates from the bakeoff, not measurements of
  the new stages. open-nitpick's token use per review was not recorded; Mira's
  was, at about 443,000 prompt tokens a review.
