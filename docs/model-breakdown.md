# Model breakdown, measured on open-nitpick

What the models this project can reach do on its own corpora.
Dated 2026-09-16.

Everything here is measured on the 16-fixture tuning corpus with one run
per fixture unless a table says otherwise. The 2-way table (glm-5.3-flash
against gpt-5.6-luna) carries 2 runs per fixture and is the more stable
read; the 4-way table (glm, luna, astra and grok-4.6) carries 1 run and
one contender lost a review. Related context is off unless a row carries
`+ctx`. Judged figures come from `google/gemini-3.1-pro-preview`
corroborated by `x-ai/grok-4.5`; every judged number carries the second
judge's delta where both judges scored the same finding list.

Rule 1 of [Measurement](measurement.md) applies: the counted columns
(RECALL, NOISE, ANCHOR) are the evidence and the judged columns (WORTH,
MISSED, severity vocabulary) are supporting evidence at best. This
document is not the cross-vendor leaderboard at
`/home/jdziat/Code/jdziat/mega-smart-router/docs/model-breakdown.md`,
which measures the same models against a different instrument on a
different corpus. Where the two disagree, this one wins for open-nitpick:
it measures our prompt, our triage pass and our severity vocabulary,
which is what ships.

## Tuning corpus, 2 runs per fixture

The stable read, from `multifile-tuning-20260917T002432Z`:

| contender | R | N | A | $/REV | $/LOC |
|---|---|---|---|---|---|
| z-ai/glm-5.3-flash | **0.81** | 0.25 | 1 | $0.0016 | $0.0020 |
| z-ai/glm-5.3-flash +ctx | 0.75 | 0.25 | 1 | $0.0014 | $0.0018 |
| openai/gpt-5.6-luna (flex) | 0.69 | 0.25 | 1 | $0.0004 | $0.0006 |
| openai/gpt-5.6-luna +ctx | 0.69 | 0.25 | 1 | $0.0004 | $0.0006 |

R = located/plants. N = noise findings per review. A = widest anchor in
lines. $/REV is the provider-reported spend per review; $/LOC divides it
by the plants it located. glm-5.3-flash has no flex tier; luna is run
with `service_tier: flex`, which halves its $0.0009 standard rate.

## Tuning corpus, 4 models, 1 run per fixture

`multifile-tuning-20260917T005316Z`. Astra lost one review
(`go-package-singleton`) to a batch failure, which prices its row as
unknown.

| contender | R | N | A | $/REV |
|---|---|---|---|---|
| z-ai/glm-5.3-flash | **0.88** | 0.25 | 1 | $0.0015 |
| z-ai/glm-5.3-flash +ctx | 0.81 | 0.19 | 1 | $0.0011 |
| openai/gpt-6-astra +ctx | 0.80 | **0.07** | 1 | n/a (1 lost) |
| x-ai/grok-4.6 | 0.75 | **0.00** | 1 | $0.0184 |
| openai/gpt-5.6-luna +ctx | 0.75 | 0.12 | 1 | $0.0005 |
| openai/gpt-6-astra | 0.69 | 0.19 | 2 | n/a (1 lost) |
| openai/gpt-5.6-luna (flex) | 0.69 | 0.25 | 1 | $0.0005 |
| x-ai/grok-4.6 +ctx | 0.56 | 0.06 | 1 | $0.0180 |

grok-4.6 is the only contender whose recall drops when related context
turns on: 0.75 without, 0.56 with. It is also the only one at zero
noise. Neither signal reproduces in the 2-run table above, which has no
grok row; treat the 0.56 as one observation until it is re-run.

## Judged quality, 4 models

`/tmp/four-model-judged2.jsonl`, judged by
`google/gemini-3.1-pro-preview`, corroborated by `x-ai/grok-4.5`. The
primary judge's ranking and the second judge's agree that
`glm-5.3-flash` leads on recall and that `grok-4.6` and `gpt-6-astra`
tie on precision; the second judge ranks `astra` above `luna`, where
the primary ties them.

| contender | findings | verdicts | worth | sev_acc | class_ok | plants |
|---|---|---|---|---|---|---|
| z-ai/glm-5.3-flash | 44 | 44 | 18/44 | 17/23 | 15/16 | 12/13 |
| openai/gpt-5.6-luna | 30 | 30 | 16/30 | 14/21 | 16/16 | 13/13 |
| openai/gpt-6-astra | 27 | 27 | 14/27 | 11/21 | 15/15 | 10/10 |
| x-ai/grok-4.6 | 17 | 17 | 11/17 | 10/18 | 11/11 | 11/11 |

`plants` is the plants the review located; the count after the slash is
the plants that fixture's ground truth declares. The `worth` denominator
is every verdict the judge returned, including ones it scored `real`
but `not worth raising`.

The judged run's smaller plant counts than the counted run's are the
corpus being split across more contenders: 4 models over 16 fixtures
gives each contender 16 observations, which is one fixture per
contender's resolution step, and no gap smaller than one plant is a
result (Rule 4a).

## What each is good at

**z-ai/glm-5.3-flash** is the recall leader, noisier than anyone else,
one misclass. Locates 14/16 plants in the 4-way run and 13/16 in the
2-run table, both higher than every other contender. It is also the
only one that locates the `php-forbidden-vs-404` plant, and the only
one that finds all three `multi-defect` plants on the no-ctx arm.
It invents more findings per review than anything else here and
misclassifies 2 of what it finds. No flex tier; prices at standard.

**openai/gpt-5.6-luna (flex)** is the cheapest, clean and average-recall option.
Perfect precision (16/16 worth raising) on the primary judge and 16/16
class accuracy across both runs, at $0.0004 per review with flex. Its
recall is concentrated at the higher severities: it locates nothing at
`info` and one fewer `nit` than glm-5.3-flash. Flex never falls back
to a standard endpoint, so a capacity failure is reported rather than
silently billed at the higher rate.

**openai/gpt-6-astra** gets its recall with related context on.
0.69 without, 0.80 with, the second-highest in the battery. Its noise
is the lowest of any non-grok contender with context on (0.07), and
both judges score its class accuracy at 100%. Its severity calibration
is weaker than grok-4.6's, at 11/21 accurate on the primary judge, and
the second judge scores it as having MISSED more defects than anyone
else. One lost review in this run; its $/REV is unknown here but the
cross-vendor leaderboard puts it at $0.04 per call at standard, $0.02
under flex.

**x-ai/grok-4.6** is the precision leader and the recall laggard.
Zero noise, perfect severity, perfect class across both judges, in
every row it ran. Also the smallest recall (0.75 without context, 0.56
with, the only contender whose recall drops when context turns on).
At $0.0184 per review it is 12× glm-5.3-flash and 37× luna, which makes
it a reviewer you pay for the review you trust, not the review that
finds the most.

## Per-role pairing

What these numbers argue for, read against what each role
does:

| role | best on this corpus | why |
|---|---|---|
| review | glm-5.3-flash | best recall (0.88), cheapest of the two leaders, finds plants nothing else finds |
| triage / validate | openai/gpt-5.6-luna on flex | perfect precision and class accuracy at a quarter of glm's price |
| fix | glm-5.3-flash, pinned to `[z-ai]`, temperature 0 | deterministic edits at the lowest cost; no flex tier exists for this model |
| expensive review | openai/gpt-6-astra +ctx | second-best recall (0.80), lowest noise of any non-grok contender |

grok-4.6 is the strongest triage filter in the battery: zero noise,
perfect severity, perfect class. It is the weakest reviewer. A pairing
that uses it for triage pays $0.018 per call for the cleanest filter
here, which is roughly 36× what luna costs for 6 points more noise and
a 4-point edge on severity accuracy. The pairing is defensible when
precision matters more than cost; it is not defensible as a default.

The second battery adds two candidates worth noting. deepseek/deepseek-v4.1-flash
at $0.0010/call reached 1.00 recall with 0.19 noise in one run, which would make it
the strongest reviewer at the lowest cost if the result replicates. poolside/laguna-s-2.1
at $0.0005/call reached 0.87 recall with the cleanest noise of the battery (0.12),
putting it ahead of luna on recall at roughly the same price. Both are 1-run
observations; the second run was still collecting at publication time.

## Cross-reference: what the vendor leaderboard got right and wrong

The benchmark at
`/home/jdziat/Code/jdziat/mega-smart-router/docs/model-breakdown.md`
(2026-09-16) scores the same models on a different corpus, a different
prompt and a different judge.

What it got right: the headline GRADE being within one judge's spread
of glm-5.3-flash (10.00 vs 9.93) tracks with this battery's judged
table. The value-index ranking that puts luna above glm-5.3-flash on
cost-per-unit-quality holds here at $0.0004 vs $0.0015 per review.

What it could not see, because its corpus does not measure it: the
recall gap between glm-5.3-flash and luna is concentrated at the
lowest-severity plants. The `nit`-band plants (`cross-file-copy-nit`,
`sorted-for-min-nit`, `capacity-hint-nit`) are where glm-5.3-flash
pulls ahead and luna does not follow. A corpus that scores by aggregate
quality does not see that trade.

What it overstates: grok-4.6's 10.00 overall score. That tracks with
its precision here, but its recall on this corpus is below glm-5.3-flash
and below astra+ctx. A raw score comparison hides the fact that grok
finds fewer defects for 12× the cost, and that its recall drops further
when related context turns on, a signal this project measures and the
cross-vendor benchmark does not.


## Tuning corpus, 4 new models, 1 run per fixture

`multifile-tuning-20260917T020755Z`. This run tested models from the
cross-vendor leaderboard that were not in the earlier battery: two qwen
variants, poolside/laguna-s-2.1, and deepseek/deepseek-v4.1-flash. Each
model reviewed all 16 tuning fixtures with and without related context.
The 15 planted defects span 13 fixtures; three fixtures (clean-refactor,
style-only, kotlin-widened-input) are intentionally clean. One run per
fixture; treat the per-row figures as preliminary until replicated.

| contender | R | N | R+ctx | N+ctx |
|---|---|---|---|---|
| deepseek/deepseek-v4.1-flash | **1.00** | 0.19 | 0.87 | 0.31 |
| qwen/qwen3.8-flash | 0.93 | 0.75 | 0.80 | 0.31 |
| poolside/laguna-s-2.1 | 0.80 | 0.12 | 0.87 | 0.19 |
| qwen/qwen3.8-max-0902 | 0.80 | 0.06 | 0.60 | 0.75 |

R = located/plants (no context), N = noise findings per review.
+ctx columns are the same model with related context turned on.
$/REV is not available in this dump; see the cross-vendor leaderboard
for pricing: deepseek $0.0010, laguna $0.0005, qwen3.8-flash $0.0009,
qwen3.8-max-0902 $0.0120 at standard (no flex tier for any of these).

Surprising results on this run that diverge from the cross-vendor leaderboard:

**deepseek/deepseek-v4.1-flash** hits 1.00 recall at 0.19 noise per review
without context, the highest recall of any model ever run on this corpus.
Its +ctx arm drops to 0.87, continuing the pattern (first seen with grok-4.6)
where context can reduce rather than improve recall. The cross-vendor leaderboard
ranks it as a strong value model; that holds here: it is the cheapest path to
perfect recall on one run. Needs a second run before trusting the 1.00.

**poolside/laguna-s-2.1** scores the cleanest noise of any model in this
battery (0.12 per review, lower than luna's 0.25 in the 2-run table).
Its recall improves with context (0.80 to 0.87), unlike most models here.
The cross-vendor leaderboard scores it at 9.14 and puts it at the top of
the value-index table. That is consistent with what this run shows for
code review work specifically.

**qwen/qwen3.8-flash** reaches 0.93 recall but with 0.75 noise per review,
the highest noise of any model in this battery. Every fixture except
sorted-for-min-nit found its plant; it also injected extra findings on
11 of 16 fixtures. The cross-vendor leaderboard rates it "poor value,
skip" (5.86 overall), which reflects tasks beyond code review: on this
corpus its recall competes with glm-5.3-flash, but its noise is 3x higher.
With a cheap triage pass downstream to filter noise it is usable; without
one, it is not the choice.

**qwen/qwen3.8-max-0902** has low noise without context (0.06 per review,
comparable to grok-4.6's zero), but its +ctx arm collapses: recall drops
from 0.80 to 0.60 and noise triples. It is also the most expensive model
in this battery at $0.0120/call with no flex option, 12x laguna for worse
recall. Nothing here recommends it for this project.

## Per-fixture findings, new models

For each fixture with a planted defect, what each model found on the no-context arm:

| fixture | deepseek-v4.1-flash | laguna-s-2.1 | qwen3.8-flash | qwen3.8-max |
|---|---|---|---|---|
| capacity-hint-nit | ok | ok | ok | ok |
| cross-file-copy-nit | ok | ok | ok | miss |
| go-cancel-goroutine-leak | ok | ok | ok | ok |
| go-hardcoded-secret | ok | ok | ok | ok |
| go-nil-deref | ok | ok | ok | ok |
| go-package-singleton | ok | miss | ok | ok |
| go-sql-injection | ok | ok | ok | ok |
| multi-defect (3 plants) | 3/3 | 3/3 | 3/3 | 3/3 |
| php-forbidden-vs-404 | ok | ok | ok | miss |
| python-command-injection | ok | ok | ok | ok |
| python-timing-unsafe-hmac | ok | ok | ok | ok |
| sorted-for-min-nit | ok | ok | miss | ok |
| ts-unbounded-memo-key | ok | miss | ok | ok |

deepseek and qwen3.8-flash are the only models to locate all 13 fixture-level
plants on the no-context arm. laguna-s-2.1 missed go-package-singleton and
ts-unbounded-memo-key. qwen3.8-max missed cross-file-copy-nit and php-forbidden-vs-404.

## What is not yet measured (updated)

The second run of the new-model battery was still in progress at publication;
qwen3.8-max-0902 in particular has slow response times. The 1-run figures above
are from multifile-tuning-20260917T020755Z. Rule 3 says replicate before ranking;
the deepseek 1.00 recall and qwen3.8-max +ctx collapse are one observation each.
Neither astra nor glm-5.3-flash nor grok-4.6 appear in this run, so direct
comparison with the earlier battery requires noting both are 1-run tables.


## Tuning corpus, 4 new models, 2 runs per fixture (confirmed)

`multifile-tuning-20260917T020755Z` and `multifile-tuning-20260917T032749Z`,
combined. 32 fixture-reviews per row (16 fixtures × 2 runs). The 15 planted
defects across 13 fixtures give 30 plant-opportunities per arm (15 × 2 runs).

| contender | R | N | R+ctx | N+ctx |
|---|---|---|---|---|
| deepseek/deepseek-v4.1-flash | **0.90** | 0.22 | 0.77 | 0.50 |
| poolside/laguna-s-2.1 | 0.87 | **0.09** | 0.83 | 0.16 |
| qwen/qwen3.8-flash | 0.87 | 0.75 | 0.80 | 0.25 |
| qwen/qwen3.8-max-0902 | 0.80 | 0.25 | 0.63 | 0.66 |

R = recall no-context, N = noise/review no-context. +ctx columns: same
model with related context on. All 32 reviews per arm.

**What stabilized vs the 1-run table:**

deepseek's 1.00 recall was one-run luck; the 2-run figure is 0.90, still
the recall leader. The context collapse is confirmed: +ctx drops recall
from 0.90 to 0.77 and doubles noise. Turn related context off for deepseek.

laguna-s-2.1 stabilizes at 0.87 recall and 0.09 noise per review — the
cleanest noise floor of any model ever run on this corpus. Context helps
slightly (0.87 → 0.83) but the no-ctx arm is already strong enough to use
without it.

qwen3.8-flash holds its 0.87 recall but confirms the noise penalty: 0.75
per review without context. With context noise drops to 0.25 and recall
to 0.80, which is a better trade only if you have a triage pass. Without
one, the 0.75 noise floor makes it unsuitable as a standalone reviewer.

qwen3.8-max-0902 confirms the +ctx collapse: recall drops from 0.80 to
0.63 and noise spikes to 0.66 with context on. The no-ctx arm is serviceable
(0.80/0.25) but at $0.0120/call it costs 13× laguna for the same recall.
Nothing here recommends it.

## Per-role pairing (updated after 2-run confirmation)

| role | recommended | alternative | why |
|---|---|---|---|
| review | deepseek/deepseek-v4.1-flash | laguna-s-2.1 | deepseek leads recall (0.90); laguna leads noise (0.09) |
| triage / validate | poolside/laguna-s-2.1 | openai/gpt-5.6-luna flex | laguna at $0.0005 beats luna on recall; luna beats it on noise |
| fix | z-ai/glm-5.3-flash, temp 0 | deepseek-v4.1-flash | glm is the established fix model; deepseek untested on edits |
| expensive review | openai/gpt-6-astra +ctx | deepseek-v4.1-flash | astra still holds 0.80 recall with lowest noise when +ctx helps |

Turn related context OFF for deepseek; it consistently hurts recall on this
corpus. Turn it ON for laguna; it gives a small noise improvement with no
recall cost.


## Why +ctx hurts recall: what the data actually shows

The +ctx recall drops are not harness failures or context-length truncation.
Every "silent" +ctx record has silent=True in the dump, meaning the model
returned {"findings":[]} -- a deliberate empty response, not an error. The
fixtures are tiny (under 500 tokens of content); no model is hitting a budget
limit.

**cross-file-copy-nit** is the clearest case. The plant is a redundant copy in
report/summary.go: Snapshot() already returns a fresh slice the caller owns,
so the second make/copy is unnecessary. Without context, models can only
infer this from the function name. With context on, store/store.go is attached
and the Snapshot doc comment explicitly says "The returned slice is a fresh one
the caller owns." Deepseek, qwen3.8-flash, and qwen3.8-max all read that
contract and conclude the copy in summary.go is intentional defensive
programming -- and they are not entirely wrong. The +ctx arm is making the
plant harder to find correctly, not easier.

This is a real evaluation problem: the fixture was designed to test cross-file
contract reading, but with the contract explicitly visible, the correct answer
is debatable. The plant is the least defensible finding in the corpus (nit,
"optional"), and the models that suppress it under +ctx are applying valid
reviewer judgment.

**What this means for the config recommendations:**

Turn +ctx off for deepseek-v4.1-flash in production. Its +ctx arm is
consistently worse on this corpus (0.90 to 0.77 recall), and the cause is
reasoning into valid-but-wrong suppressions on fixtures where the full contract
is visible. The no-ctx arm at 0.90 recall is the right operating point.

laguna-s-2.1 is less affected (+ctx gives 0.83 vs 0.87 without). Its
ts-unbounded-memo-key +ctx miss returns findings but not the specific plant --
more likely genuine attention dilution than false suppression.

qwen3.8-max-0902 shows the same context-suppression on cross-file-copy-nit,
and additionally collapses on multi-defect +ctx (1/3 plants instead of 3/3).
That regression is harder to explain as correct suppression and more plausibly
a reasoning attention issue with longer context.
