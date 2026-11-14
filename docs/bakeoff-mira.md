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
`second_look` are, through a 12-line change to `scripts/replay_snapshot_review.py` and a one-line model alias in `scripts/run_benchmark_sample.py`
that reads them from the environment and leaves the default path untouched. The
change is kept as `tools/bakeoff/mira-replay-bakeoff.patch`.

Arms B and C give each review 900 seconds in both tools. Mira's own default is 420,
which its sentry-5 review in arm A came within 11 seconds of, and a thorough
setting with three ensemble runs would not fit.

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

## Results

Upstream scorer, Core profile, judge `anthropic/claude-sonnet-4.5`, 20 pull requests
and 62 golden comments, so one comment moves recall by 1.6 points. Every review
completed; none failed. Each row is one pass.

| arm | reviewer | pass | precision | recall | F1 | F2 | TP / FP / FN | comments | median seconds |
|---|---|---:|---:|---:|---:|---:|---|---:|---:|
| A, one model | Mira | 1 | 75.8% | 40.3% | 0.526 | 0.445 | 25 / 8 / 37 | 32 | 128 |
| A | Mira | 2 | 68.4% | 41.9% | 0.520 | 0.455 | 26 / 12 / 36 | 41 | 107 |
| A | open-nitpick | 1 | 27.6% | 54.8% | 0.368 | 0.458 | 34 / 89 / 28 | 128 | 189 |
| A | open-nitpick | 2 | 28.8% | 54.8% | 0.378 | 0.464 | 34 / 84 / 28 | 123 | 155 |
| B, mixed models | Mira (fallback, 3 ensemble runs, second look) | 1 | 76.9% | 32.3% | 0.455 | 0.365 | 20 / 6 / 42 | 33 | 229 |
| B | Mira | 2 | 87.5% | 22.6% | 0.359 | 0.265 | 14 / 2 / 48 | 16 | 205 |
| B | open-nitpick (two reviewers, qwen triage) | 1 | 20.0% | 61.3% | 0.302 | 0.434 | 38 / 152 / 24 | 199 | 177 |
| B | open-nitpick | 2 | 19.2% | 62.9% | 0.294 | 0.432 | 39 / 164 / 23 | 212 | 198 |
| C, `claude-sonnet-4.6` | Mira | 1 | 50.0% | 58.1% | 0.537 | 0.562 | 36 / 36 / 26 | 83 | 187 |
| C | open-nitpick | 1 | 34.9% | 48.4% | 0.405 | 0.449 | 30 / 56 / 32 | 86 | 22 |

The judge repeated on arm A with a fresh cache made 1,422 calls, 517 of them worded
differently, and returned the same true, false and missed counts on all 80
run-and-case pairs. Between passes of a reviewer the movement is larger: Mira's
comment count went from 32 to 41, and in arm B from 33 to 16.

What the table says. On one model, Mira leads on F1 by about 0.15 in both
passes and open-nitpick leads on recall by about 13 points; on the benchmark's
recall-weighted F2 they are level. Neither tool's mixed or thorough configuration
beat its single-model one. With a frontier model Mira's recall rises to 58% and
its F1 stays near 0.54, while open-nitpick's precision rises to 35% and its recall
falls to 48%, and open-nitpick takes 22 seconds a review to Mira's 187.

## The gap

The gap is precision. Open-nitpick matches more of the golden comments than Mira
does (46 of the 68 golden comments in all categories across its two arm A passes,
against 35, with 30 found by both, 16 only by us and 5 only by Mira) and posts about 6.3 comments a pull request to
Mira's 1.6.

- **Mira's drafts are as numerous as ours.** Its audit trail shows 90 to 100 drafted
  comments a pass. A noise filter drops 68 over two passes and a self-critique pass
  drops 44, so 39% are posted. Posted comments have median confidence 0.78 and
  dropped ones 0.665. Open-nitpick has no numeric confidence to filter on.
- **Our severity does not rank.** The judge matched 11% of our Info comments (7 of
  66), 30% of Warnings (39 of 130) and 37% of Errors (19 of 51), over both passes. A
  per-pull-request cap by severity buys precision about as fast as it loses recall:
  top 5 gives 34.6% precision at 40.4% recall (matched comments over all 68 golden
  comments, not the scorer's recall), where Mira has 68 to 76% at 40 to 42%.
- **Volume on large pull requests carries the noise.** Three pull requests with 11
  or more comments account for 100 of our 251 comments, at 17% precision.
- **Two kinds of comment match rarely:** titles saying something was removed or
  dropped (2 of 27) and comments on test files (2 of 16). Both are small shares.
- **Where Mira alone wins.** Two of the 5 golden comments only Mira found are the
  cross-file `organization_context.member` can be `None` cases in sentry-1 and
  sentry-2. The other three are small discourse issues: a percentage resize that
  fails for animated GIFs, a nil `contents` in `TopicEmbed.import`, and
  `-ms-align-items`. Mira reads other files through search and read tools.

## What was tried on our side

Offline, on the finished arm A findings, or as one-pass runs with the same model.

| change | precision | recall | F1 | F2 |
|---|---:|---:|---:|---:|
| none (arm A, two passes) | 27.6%, 28.8% | 54.8%, 54.8% | 0.368, 0.378 | 0.458, 0.464 |
| `validation.enabled: true` | 29.5% | 41.9% | 0.347 | 0.387 |
| `persona.nitpick: minimal` | 31.7% | 61.3% | 0.418 | 0.516 |
| evidence-graded critique, keep rule in code (offline) | 32.3%, 37.1% | 50.0%, 53.2% | 0.392, 0.437 | 0.451, 0.490 |
| critique, then top 3 per pull request, proven first (offline) | 47.5% | 34.6% | | |

The critique pass grades each comment against the diff around its cited lines as
proven, plausible or unsupported. It keeps proven, keeps plausible at warning
severity or worse, and drops unsupported, a rule held in code. It is Mira's design
with a paraphrased prompt. My first version of it could not see severity and
dropped every plausible comment, which looked like a recall loss; the rows above
are the corrected rule.

## Proposed fixes, in order

1. **Make `minimal` the default persona level for review, after a replicate.** It
   needs no code. One pass improved every column, and unmodified passes differ by
   0.01 F1, so the 0.04 gain is outside that. The reason is not established: the
   level filters which classes publish and I have not checked that it also changes
   the prompt. Before changing the default, repeat the run and rerun the planted
   defect corpus so a number in `docs/findings.md` does not move unseen.
2. **Replace adversarial `validation` with the evidence-graded critique.**
   Validation as shipped lost 13 points of recall for 1 to 2 of precision. The
   critique estimate gains 5 to 8 points of precision for 2 to 5 of recall. Build it as a
   stage with a code-owned keep rule, and measure it live rather than offline.
3. **Have the reviewer emit a confidence and a code citation, and post by a
   floor.** Severity does not separate matched from unmatched comments and
   confidence does in Mira's data. This changes the finding schema and the prompt,
   so it needs the planted corpus as well as this one.
4. **Do not union two reviewers for pull requests without a denoise step.** Arm B
   gained 4 to 5 matched comments and 63 to 80 false positives. `docs/comparison.md`
   already says triage cannot drop a finding, and this is the cost.
5. **Cap volume on large pull requests by confidence, not severity,** once 3 exists.
   A cap by severity cannot do it.
6. **Give the reviewer cross-file reads for the 5 Mira-only findings.** That is a
   tool loop and the largest change. Leave it until 1 to 3 are measured, because
   Mira's cost and time come from it: 187 seconds a review with Sonnet against our
   22.

Keep open-nitpick's speed: on the same frontier model it is about eight times faster.

## Limits of this result

- One judge model. A different judge could rank differently.
- Twenty pull requests, 62 golden comments, one corpus. A real defect nobody
  labelled counts as a false positive, which hurts the tool that comments more.
- Arm C is one pass for each tool. The experiments on our side are one pass each,
  and arm A shows a reviewer moving by more than the judge does.
- The critique rows are offline estimates, not a built stage.
- Mira's replay harness uses one model for every role, and its published figure
  (75% precision, 55% recall over 50 pull requests) is a different corpus and one
  pass.

