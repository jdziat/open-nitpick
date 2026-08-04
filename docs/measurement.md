# Measurement requirements

What has to hold before a number out of `internal/evals` is worth acting on.

These are not style preferences. Every rule below was written because the harness
produced a confident wrong number and something downstream believed it. The
provenance is in [findings.md](findings.md); this file is the checklist.

## The two kinds of column, and why it matters

| | derived from | reproduces? |
|---|---|---|
| **deterministic** — `RECALL`, `NOISE`, `O-ACC`, `O-INFL`, `O-UNDER`, `SEV A/I/U` | fixture ground truth, no model | yes, bit-for-bit, given the same findings |
| **judged** — `GRADE`, `SPREAD`, `PREC`, `WORTH`, `MISSED`, `J-INFL`, `J-UNDER`, `SIGNAL` | an LLM judge | **no** |

The judge runs at `Temperature: 0` and is still not reproducible. Scoring the
*same cached Incumbent findings* — byte-identical input — across four runs gave:

| run | GRADE | MISSED |
|---|---|---|
| 1 | 3.90 | 0.25 |
| 2 | 3.66 | 0.62 |
| 3 | 3.95 | 0.12 |
| 4 | 3.98 | 0.38 |

Grade spans 0.32; `MISSED` spans **5×**. The deterministic columns were identical
in all four. Temperature is already floored, so this cannot be tuned away — only
averaged over, or avoided by preferring judge-free measurement.

**Rule 1.** Deterministic columns are the primary evidence. A judged column is
supporting evidence at best and needs a margin wider than 0.32 grade points
before it means anything.

## Comparing

**Rule 2 — within-run only.** Contenders compared against each other must appear
in the *same* run. Assembling a leaderboard from separate runs measures
scheduling as much as models.

**Rule 3 — replicate before ranking.** A single observation is not a ranking. An
ordering counts only if it holds across two independent passes *and* the gap
exceeds the larger `SPREAD`.

**Rule 4 — rates, not sums.** `GRADE` is a mean; the count columns are per-sample
rates. Never compare a raw total across contenders with different `N` — three
runs of a model that misses one defect per run shows `MISSED 3` against a single
run's `1` and looks three times worse for having been measured three times as
hard.

**Rule 5 — coverage, not sample count.** Comparability is about which *fixtures*
were judged, not how many samples exist. `COV` is the denominator that makes a
comparison legitimate; `N` is what the rates divide by.

**Rule 6 — cross-tool severity is banded.** Incumbent publishes ~3 severity
levels; open-nitpick publishes 5. Comparing them at full resolution is a category
error in whichever direction the mapping happens to round. Cross-tool comparison
uses coarse bands; our own models are still compared at full resolution, because
that comparison is between vocabularies that actually match.

Banding has a cost, and it is stated where the number appears: a model that says
`critical` where `error` was right scores identically to one that got it exactly.

## Generalising

**Rule 7 — held-out before any generalisation claim.** `Fixtures()` is the tuning
corpus. `HeldOutFixtures()` is spent once, at the end. Tuning on the fixtures you
then measure on is overfitting by construction, and the held-out set stops being
held out the moment a tuning run reads it.

Naming a held-out fixture is deliberately explicit — `FIXTURES=$(HELD_OUT)` — and
a default run never includes them.

**Rule 8 — single-file fixtures do not predict pull requests.** Every fixture is
one file, so `max_files_per_request: 6`, the 4-way concurrency and cross-batch
triage dedup have never been exercised by any measurement here. Numbers describe
the degenerate case: one file, one batch, one call.

## Trusting a test

**Rule 9 — a guard test must fail under mutation.** Break the thing it guards and
watch it go red; if it stays green it is decoration. This harness has shipped
three tests that passed against the bug they named:

- a severity-voice test that compared text from *before* the section it was
  checking
- a hallucination guard that compared keyword *literals* against a banned list
  while matching is substring-based
- a parser test written with two `+` characters instead of three, so the
  malformed-header case it claimed to cover was never exercised

**Rule 10 — silence is a result that needs proving.** Zero findings and "the
review ran and found nothing" are indistinguishable in output and completely
different in meaning. Any path that can return empty needs a test asserting it
returns empty *for the right reason*.

## Cost

**Rule 11 — measured usage, never estimated.** Estimated tokens are used for
*budgeting* in `internal/bundle`. A cost figure a purchasing decision rests on
must come from reported usage, and a model with no price entry reports **unknown**,
not zero — zero is a number a reader will act on.

**Rule 12 — judge cost is not product cost.** The judge is a measurement expense.
Folding it into cost-per-review overstates what running this tool costs a user.

**Rule 13 — cost per detected defect, not cost per review.** A cheap model that
misses half the defects is not cheap.

## Before publishing a number

- [ ] Is the claim resting on a deterministic column? If not, is the margin wider
      than the judged noise?
- [ ] Were the contenders measured in the same run?
- [ ] Has it been replicated?
- [ ] Are the count columns rates, over comparable coverage?
- [ ] If it crosses tools, is severity banded and the banding disclosed?
- [ ] If it claims generalisation, was it checked on the held-out corpus?
- [ ] Do the guard tests behind it fail under mutation?
- [ ] Would this number look different if the instrument favoured the author?
      Four instrument bugs found here did exactly that.
