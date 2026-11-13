# Bakeoff: open-nitpick against Mira

**Status:** registered before any review ran
**Date:** 2026-11-13
**Branch:** `evals/bakeoff-mira`

Written before results so the design cannot move toward the answer. Anything
that changes after the first review is recorded under "Deviations" with the
reason.

## Question

On real pull requests with human-labelled defects, with the model held
constant, which reviewer reports more of the labelled defects, and at what cost
in comments that match none?

## Contenders

| | version | how it is run |
|---|---|---|
| open-nitpick | v2.7.0, the release binary | the eval harness, shipped defaults |
| Mira | `miracodeai/mira` at `04d3491` | its own replay harness, `scripts/replay_snapshot_review.py`, default settings |

Mira is Apache-2.0 and self-hosted, so both tools run on this machine with no
hosted service and no terms that restrict publishing a comparison.

Both tools use `deepseek/deepseek-v4.1-flash-1017` through OpenRouter, one key.
That is the model Mira's own benchmark uses, so Mira is measured where it was
tuned. Every model role in both tools is set to it.

Settings are each tool's defaults except the model. Mira's are reasoning effort
`medium` and a 16k-token output budget. Ours leave reasoning unset and carry the
16384-token ceiling v2.7.0 adds for OpenRouter. Related-file context is off for
open-nitpick, its shipped default. These differences are part of what is being
compared and are not equalised.

## Corpus

The Martian Code Review Bench offline set at `withmartian/code-review-benchmark`
commit `e616e849755441da38f18bf3adba2c9583b03803`. Twenty of its 50 pull
requests: entries 1, 2, 4 and 5 of each of the five repositories, in the pinned
golden-file order. The rule is Mira's own twenty-PR selection
(`scripts/prepare_benchmark_inputs.py --entries 1,2,4,5`) and was fixed before
any review, so no pull request was chosen by what either tool found.

| repository | language | PRs |
|---|---|---:|
| cal.com | TypeScript | 4 |
| discourse | Ruby | 4 |
| grafana | Go | 4 |
| keycloak | Java | 4 |
| sentry | Python | 4 |

Reviewers never see the golden comments; they are read only by the scorer.

## Procedure

1. Prepare the twenty snapshots (diff, source at the head commit, metadata).
2. Run Mira on each snapshot with its replay harness.
3. Run open-nitpick on each snapshot as a fixture: the files the diff touches,
   before and after, with the pre-image rebuilt by reverse-applying the diff to
   the head tree.
4. Two independent passes of each tool, in the same session (Rule 2).
5. Score every review with the upstream Martian scorer, one judge for both
   tools, Core profile as the headline and Strict beside it.
6. Judge every review a second time into a fresh cache, to measure the judge's
   own spread (Rule 1).

## What is reported

Precision, recall and F1 as counts first: true positives, false positives and
false negatives, over all twenty pull requests and per repository, with the
number of golden comments behind each rate (Rule 4a). Wall clock per review and
model cost per review. Findings per review.

## What counts as a result

Following `docs/measurement.md`:

- An ordering is stated only if it holds in both passes and the gap exceeds the
  spread between the two judge passes on the same findings (Rules 1 and 3).
- The corpus has 20 pull requests and roughly 60 golden comments, so one
  golden comment moves recall by about 0.017 and a repository by far more. A
  difference of a few comments is not stated as a difference (Rule 4a).
- A review that failed to run is a failure, counted and printed, never a review
  with no findings (Rule 10).
- Nothing here is compared with Mira's published figure for its full 50 pull
  requests. That is a different corpus size, one pass and a different judge.

## Known limits

- The pull requests come from well-known repositories and may be in the model's
  training data. Both tools use the same model, so any leak helps both equally.
- Golden comments are human-curated and sparse on style. A tool that finds a real
  defect nobody labelled is scored as a false positive.
- The judge is a model and varies between passes. The deterministic columns of
  `internal/evals` do not exist for this corpus, because the golden comments
  carry no file or line.
- open-nitpick sees the changed files in full and the diff. Mira's harness
  serves it the whole snapshot through its index tools. That is each tool's
  shipped shape, and the cost and time columns show what each pays for it.
- Twenty pull requests is a small sample and one model is one model. A result
  here says nothing about other models.

## Deviations

None yet.
