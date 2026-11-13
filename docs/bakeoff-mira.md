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
| open-nitpick | v2.7.0, the release binary | `nitpick review -dry-run -no-linters` in a git checkout of each snapshot, shipped defaults |
| Mira | `miracodeai/mira` at `04d3491` | its own replay harness, `scripts/replay_snapshot_review.py`, default settings |

Mira is Apache-2.0 and self-hosted, so both tools run on this machine with no
hosted service and no terms that restrict publishing a comparison.

Both tools use `deepseek/deepseek-v4.1-flash-1017` through OpenRouter, one key.
That is the model Mira's own benchmark uses, so Mira is measured where it was
tuned. Every model role in both tools is set to it.

Settings are each tool's defaults except the model. Mira's are reasoning effort
`medium` and a 16k-token output budget. Ours leave reasoning unset and carry the
16384-token ceiling v2.7.0 adds for OpenRouter, with the model set through
`LLM_MODEL` and no repository or user configuration. These differences are part of what is being
compared and are not equalised.

## Arms

Each arm is both tools run in the same session on the same twenty pull requests,
two passes each. Arm A is the registered design above, one model for every role.
Arms B and C give each tool a stronger configuration.

| arm | open-nitpick | Mira |
|---|---|---|
| A, one model | `deepseek/deepseek-v4.1-flash-1017` for every role, shipped defaults | the same model for every role, default settings |
| B, mixed models | `tools/bakeoff/nitpick-mix.yaml`: DeepSeek reviews, qwen3.8-27b is a second reviewer on every batch, the triage model and the fallback; related-file context and the caller walk on | DeepSeek, `fallback_model` qwen3.8-27b, `ensemble_runs` 3 and `second_look` on, Mira's own opt-in "thorough" settings |
| C, frontier model | `anthropic/claude-sonnet-4.6` for every role | the same model, Mira's default |

Mira's replay harness uses one model for every role, so a review model separate
from a triage model is not reachable there. `fallback_model`, `ensemble_runs` and
`second_look` are, through a 12-line change to `scripts/replay_snapshot_review.py`
that reads them from the environment and leaves the default path untouched. The
change is kept as `tools/bakeoff/mira-replay-bakeoff.patch`.

Arm C costs real money. Mira's reviews average about 443,000 prompt tokens each,
which at Sonnet rates is roughly $1.80 a review, so about $35 for one pass over
twenty pull requests, and open-nitpick uses fewer tokens. Arm C runs one pass of
each tool, and only after the cost is confirmed.

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
3. Run open-nitpick on each snapshot as a checkout: the snapshot's head tree is
   the working tree, with a base commit made by reverse-applying the diff and a
   head commit that applies it again, and `nitpick review -base HEAD~1 -head
   HEAD -dry-run -no-linters` reviews the pull request. Related-file context is on,
   as shipped. Analyzers are off because the other reviewer runs none.
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
- Each tool gets the whole head tree and reads what it chooses to: Mira through
  its index tools, open-nitpick through its related-file context. That is each
  tool's shipped shape, and the time column shows what each pays for it.
- open-nitpick prints findings as dry-run text, and `tools/bakeoff/run_nitpick.py`
  parses them. The raw output is kept beside each result so the parse can be
  audited.
- Token cost is not collected for open-nitpick, because the CLI does not report
  it; Mira's harness does. Time is.
- Twenty pull requests is a small sample and one model is one model. A result
  here says nothing about other models.

## Deviations

- Arms B and C were added after arm A's first pass had been read and graded. They
  exist because the first result raised the question of each tool's best
  configuration, not because of which tool led, and arm A stays in the report
  whatever B and C show.
- Before any result was read, the open-nitpick arm changed from the eval harness
  on a fixture of the changed files to the shipped CLI on a full checkout. The
  fixture gave it less than the other reviewer sees and left its related-file
  context unable to run. Mira's first pass had started and its results were
  unread; open-nitpick had not run.
