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

**Rule 6 — there is no cross-tool severity score. WITHDRAWN, twice.** Incumbent
publishes ~3 severity levels; open-nitpick publishes 5. Comparing them at full
resolution is a category error in whichever direction the mapping happens to
round — that was the first attempt, correcting for it in the parser, and it was
retracted. The second attempt corrected for it at comparison time, coarsening
both vocabularies into bands and publishing `B-ACC`. It is also retracted, for two
measured reasons:

- **The banded column was maximised by the worst production behaviour.** This
  corpus bands 12 plants blocking, 1 medium, 1 low, and every plant Incumbent
  locates is blocking. A reviewer that stamps one blocking word on every finding —
  always `critical`, or always `error` — banded 12 accurate and 2 inflated of 14,
  against the incumbent's 6 of 10. A reviewer that also picks *what to report* —
  stay silent unless the defect is already blocking, then call it `critical` —
  banded **12 of 12, a perfect record**.

  The figure first published here, *"a perfect 10/10"* for always-`critical`, was
  wrong and is corrected above. It scored those strategies over the ten plants the
  *incumbent* located rather than over what they actually report, which credited
  them with a denominator they had not earned. The conclusion survives; the number
  did not, and a retraction argued from an unreproducible measurement repeats the
  defect it is retracting. The live receipt is now the selective-reporting row in
  `degenerateReviewers()`, scored on every run.
- **It could not see the defect it was written for.** The parser bug behind the
  first retraction moves it not at all: buggy 6/0/4, fixed 6/0/4.

It was also a free parameter. `crSeverity` records Incumbent's `major` at
`warning`; recording it at `error`, with Incumbent's bytes unchanged, moves the
banded figure from 0.600 to **1.000** over all fixtures (6/0/4 to 10/0/0) and from
0.714 to 1.000 over the tuning set. Every `major` this corpus credits sits on a
plant we rate blocking, so the corpus contains no evidence for either placement.

(The figure first published here, *"0.62 to 0.88"*, reproduces from nothing in the
tree and is corrected above. The real swing is larger and ends at a perfect
record — the correction strengthens the argument, which is precisely why it had to
be recomputed rather than reused.)

**Do not re-tune the boundaries.** That would be the fourth attempt, and each of
the first three moved a number toward the author's side on no new evidence. What
is published instead is a DESCRIPTION: the severity vocabulary block, which says
which words each reviewer used against which planted levels and leaves the reader
to judge. Our own models keep full-resolution `O-*` scoring against each other,
which is a comparison between vocabularies that actually match.

**Withdrawing the banded spelling was not enough, and that too is corrected.**
The full-resolution `O-INFL`/`O-UNDER`/`O-ACC` triple was left printed on
Incumbent's row, in the same columns of the same sorted ranking as our models,
with a note underneath telling the reader not to compare them — the mitigation
this very rule records as insufficient. The surviving triple is *more* sensitive
to the free `major` constant than the banded one it replaced: re-parsing the
identical cached bytes with `major` at `error` moves it from 2/4/4 to 5/4/1.
Those cells now print `n/a` for any row that does not publish our five levels.

### Rule 7: a score is read as a group, and the group includes its denominator

`O-ACC`/`O-INFL`/`O-UNDER` are graded only over defects the reviewer **located**.
A reviewer that reports nothing except this corpus's `critical` plants, and calls
them `critical`, therefore scores `O-ACC` 1.000 with no inflation and no
understatement — an exact tie with a perfectly calibrated reviewer, on 4 of 14
plants. Selective silence is not calibration. `O-COV` is the share of planted
defects the triple covers and is printed with it, never without.

`RECALL` and `NOISE` have the same shape. One finding per file, spanning the whole
file, titled with every keyword in it, scores `RECALL` 1.000 and `NOISE` 0 — a tie
with a calibrated reviewer, while pointing at nothing more precise than "there is
a bug somewhere in this file". `ANCHOR`, the widest region any finding claimed, is
what tells them apart, and it is now a column rather than a log line.

Both were found the same way: by crossing every published metric against
reviewers nobody would ship and declaring, cell by cell, whether each can score
as well as being right (`TestNoDegenerateReviewerCanMaxOutAPublishedMetric`). Two
cells that should have read *no* read *yes*.

**Rule 6b — ask what maximises every column, before publishing it.** The banded
column shipped because nobody did. `PublishedMetrics` registers every model-free
score, and `TestNoDegenerateReviewerCanMaxOutAPublishedMetric` crosses it against
reviewers nobody would ship — always critical, always nit, one comment per line,
silence, one comment at every severity. A metric one of them can score as well as
a correct reviewer on does not measure what its name claims. Adding a column to a
table means registering it, which means answering the question.

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
- [ ] If it crosses tools, is it a SEVERITY score? There is no such number here;
      quote the vocabulary description instead (Rule 6).
- [ ] **What maximises this column?** Is there a reviewer nobody would ship —
      always critical, always nit, one comment per line, silence — that scores as
      well on it as a correct one? If so it is not a score (Rule 6b).
- [ ] If it claims generalisation, was it checked on the held-out corpus?
- [ ] Do the guard tests behind it fail under mutation?
- [ ] Would this number look different if the instrument favoured the author?
      Four instrument bugs found here did exactly that.
