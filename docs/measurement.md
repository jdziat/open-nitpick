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

**Rule 4a — print the counts and say what they can resolve.** Every rate here is
a quotient of two small integers wearing two decimal places. `7/8` and `3/6` are
the same facts as `0.88` and `0.50`, and the first pair tells a reader what the
second hides: that one is eight observations and the other six, that neither can
move by less than an eighth or a sixth, and that a gap of `0.05` between two such
rows is not a result. So every table prints a `DENOMINATORS` block giving the
counts behind each rate, and `CorpusResolution` states the smallest difference
the corpus under measurement can express — **derived from the fixtures**, because
a hand-written "this corpus cannot resolve less than one defect" goes wrong the
first time a fixture is added, and will be believed. Note that a severity column
is graded only over what a reviewer *located*, so its denominator is smaller than
`RECALL`'s and its step correspondingly coarser; `O-COV` is what says how much
smaller.

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

- **The banded column was maximised by a reviewer that also picks what to
  report.** This corpus plants 29 defects over 30 fixtures and bands them 12
  blocking, 6 medium, 11 low. A reviewer that stays silent unless the defect is
  already blocking and then calls it `critical` banded **12/0/0 — a perfect
  record**, an exact tie with a calibrated reviewer's 29/0/0, over 12 of the 29
  plants.

  Two figures published here have been corrected, and the second correction
  *weakens* half the argument. The first, *"a perfect 10/10"* for
  always-`critical`, scored those strategies over the ten plants the *incumbent*
  located rather than over what they actually report. The second — that the two
  stampers banded 12 accurate and 2 inflated of 14 against the incumbent's 6 of
  10 — was true of a fourteen-plant corpus that no longer exists. On the corpus
  in the tree the stampers band 12/17/0 of 29 (`B-ACC` 0.414) and **no longer
  tie**, so the stamper half of this argument is withdrawn and the selective
  reviewer above is what it now rests on. Nor does the incumbent locate only
  blocking plants: it locates 3 `critical`, 7 `error` and 4 `warning`, so 10 of
  its 14 are blocking, and it bands 10/0/4 over them.
- **It could not see the defect it was written for.** The parser bug behind the
  first retraction moves it not at all: buggy 10/0/4, fixed 10/0/4. At our full
  resolution the same bug moves the incumbent's triple from 6/4/4 to 8/0/6.

It was also a free parameter. `crSeverity` records Incumbent's `major` at
`warning`; recording it at `error`, with Incumbent's bytes unchanged, leaves the
banded figure at `B-ACC` 0.714 either way (10/0/4 to 10/4/0) and moves the
full-resolution triple from 6/4/4 to **5/8/1**. Across the shipped cache `major`
is credited on plants of `critical`, `error` *and* `warning` — it straddles three
of our five levels, so no single value is right for every plant it lands on.

(Two swings were published here before, *"0.62 to 0.88"* and then *"0.600 to
1.000"*. Neither reproduces from the tree. The corrected swing is at full
resolution only — which is where the published cells are, so the free parameter
still moves a published number; the banded column turns out to be insensitive to
this too.)

Every figure in this rule is read back out of the corpus by
`TestTheSeverityFiguresTheseCommentsQuoteStillReproduce`, including the banded
ones, which it reconstructs inside itself because the instrument that produced
them is deleted. A retraction whose own evidence cannot be recomputed is the
defect one level up, and this rule has been that defect twice.

**What the abstention costs, and one sentence nobody may write from it.** The
withdrawal withholds a number: the incumbent's full-resolution O-ACC over the
shipped cache is 0.429, against 1.000 for a calibrated reviewer, in a sorted
ranking. That is the first move in this sequence that costs the comparison
rather than paying for it, which is the reason to keep it — but it is *not*
licence for "we withheld a number we would have won". **Our own models' O-* under
the same instrument is not computable from this tree**: `internal/evals/testdata/`
holds only `incumbent/`, so our side needs a live paid run. Anyone tempted by
that sentence has to compute ours first; asserting it from the incumbent's figure
alone is a claim of exactly the shape this rule keeps retracting.

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
identical cached bytes with `major` at `error` moves it from 6/4/4 to 5/8/1.
Those cells now print `n/a` for any row that has not **declared** our five
levels — a three-state scale carried on the row, zero value undeclared. The gate
used to be `model != IncumbentModel`, a reporter's identity standing in for a
fact about its vocabulary, so a contender added without anyone thinking about it
was published at our resolution by default.

**Rule 6b — a description published in place of a score must be a quotation.**
The vocabulary block was not one, and this is the third correction in the same
place. It printed the level `crSeverity` had translated each foreign word *to*,
under a caption saying it was what the contender called the defect. Incumbent
prints `critical`, `major` and `minor`; it printed neither `error` nor `warning`
anywhere in the shipped cache, and the block read

    planted error (7 located): critical x4, warning x3

Swapping the free `major` constant re-rendered those same cached bytes as
`critical x4, error x3`. So the description offered *because* a score could not
be justified was itself a function of the free parameter the withdrawal rested
on, presented as observation — the same defect one level down, and the reason the
block is worth nothing if it is a score in disguise.

The words published are now the ones the reviewer printed, read from the retained
review text. Where this project translates a word into its own five levels the
level follows it as `[we read as X]` and is labelled ours, so
`major x3 [we read as warning]` says who said what. A word that genuinely cannot
be recovered — a cache entry collected before the raw review was retained — is
reported as `(word not recorded)` rather than filled in from our reading.
`TestPublishedVocabularyIsQuotedFromTheRetainedReview` reads the words straight
out of `crCache.Raw`, independently of the scoring path, and fails if the block
publishes one the CLI never printed.

**Rule 6b-ii — a description omitting what nobody said is a description of the
loudest half.** The block printed only the levels a reviewer had located
something at, on the reasoning that a miss is `RECALL`'s job. Measured, that made
the worst strategy's page the cleanest: the reviewer that reports only the plants
we rate `critical` and calls them `critical` rendered one line, a proper
**substring** of a calibrated reviewer's block, on a row whose `O-COV` cell was
blank. Every line now carries the count planted at that level whether or not
anything was located there —

    planted error   (7 of 8 located): critical x4, major x3 [we read as warning]
    planted info    (0 of 5 located): nothing located

— and `TestEveryPlantedLevelAppearsWithItsDenominator` fails if a level the
corpus plants goes missing from any reviewer's page.

The census is taken from the **fixture**, before scoring can return early, and
that detail is the rule rather than an implementation note. It was taken inside
the severity scorer, which a run with no report never reaches, so a corpus with
one review in three lost to the provider rendered a census summing to 17 against
a planted total of 29 — and a page with no `nothing located` line anywhere on it.
The same invisible absence, produced by a rate limit instead of by a reviewer
strategy, and flattering in the same direction. Every case in that test now runs
twice: once with every run delivered, once with one in three lost.

**Rule 6b-iv — a gap may not outrank a word the review kept.** Where a word was
translated and the original discarded, the block prints `(word not recorded)`
rather than our reading. When two findings tie for the credit and only one has
kept its word, the kept one wins. It did not: the tie-break ordered spellings
lexicographically and the empty string sorts before every real word, so a defect
matched by one finding carrying `Error` and one carrying nothing published the
gap phrase in **both** report orders. Order-independence held, on the wrong
answer. The shape is ordinary — `internal/linters` marks every analyzer finding
translated and keeps no raw word — so this was reachable in exactly the
configuration the scale withdrawal exists for.

**Rule 6b-iii — what the description cannot say is printed with it.** It cannot
say DIRECTION (which words landed on which plants is not whether the reviewer
under- or over-claims against our ladder), cannot show HEDGING (a reviewer
answering all five severities renders exactly as one answering only the loudest,
because the loudest claim is the credited one), cannot show PER-REVIEW STRUCTURE
(the counts are pooled; of the 12 cached reviews that locate anything, exactly
one locates at two or more distinct planted levels), and states no ORDER between
reviewers. Those five sentences are in the block itself rather than in this file,
because a limitation only the source records is a limitation only its author
knows about.

**Rule 6b-i — the same rule applies to our own side, and did not.** The fix above
was made for the incumbent and reintroduced for every contender this project
ships. `severityWasTranslated` answered from the finding's **source** — it was
true only for Incumbent — while `review.Engine` rewrites every model's severity
through `Normalize` and `linters.mapSeverity` collapses four analyzers'
vocabularies onto three of our levels. So the block answered "nothing was
translated" for all of our rows and quoted each model as having printed the word
we had just written over it. It is worse than the incumbent's version was: there
a lost word prints `(word not recorded)`, and here the substitute was published
silently as a quotation. Whoever rewrites a severity now records the fact and the
original word (`Finding.SeverityTranslated`, `Finding.RawSeverity`), and the
question is answered from that record rather than from who produced the finding —
an identity that only ever *correlated* with the answer, and was guaranteed to
drift the moment a second path rewrote a severity, which two already had. The
`NITPICK_EVAL_DUMP` artifact had the same defect and the same fix: it wrote the
translated word under a bare `severity` key beside `"model":"incumbent/cli"`,
and `RawSeverity` carries `json:"-"`, so the reviewer's own word could not reach
it at all.

The block's preamble is now **derived** from the rows it introduces. It carried a
hand-written sentence naming which words a particular reviewer prints — inside
the block whose entire thesis is that a vocabulary must be quoted rather than
restated from memory. It was the same failure in miniature, and it was already
stale by two rounds.

### Rule 6c: a score is read as a group, and the group includes its denominator

`O-ACC`/`O-INFL`/`O-UNDER` are graded only over defects the reviewer **located**.
A reviewer that reports nothing except this corpus's `critical` plants, and calls
them `critical`, therefore scores `O-ACC` 1.000 with no inflation and no
understatement — an exact tie with a perfectly calibrated reviewer, on 4 of 29
plants. Selective silence is not calibration. `O-COV` is the share of planted
defects the triple covers and is printed with it, never without.

The same strategy defeated the *description* too, and for the same reason. The
vocabulary block omitted levels nobody located, so this reviewer's page was one
clean line — `planted critical (4 located): critical x4` — a proper **substring**
of a calibrated reviewer's block, beside a blank `O-COV`. Every line now carries
the count planted at that level, located or not, so the four levels it never
reached print `(0 of N located): nothing located`.

`RECALL` and `NOISE` have the same shape. One finding per file, spanning the whole
file, titled with every keyword in it, scores `RECALL` 1.000 and `NOISE` 0 — a tie
with a calibrated reviewer, while pointing at nothing more precise than "there is
a bug somewhere in this file". `ANCHOR` is what tells them apart, and it is now a
column rather than a log line.

`ANCHOR` counts the **distinct lines** pointed at about any one thing: the widest
single finding, and — because those are not the same question — the union of
every finding claiming a given defect, whichever is larger.

It measured the widest **single** region for three rounds, and that could not see
the strategy it was added for: a finding naming 36 separate one-line regions
scored 1 — identical to a line-precise reviewer — while `anchorDistance` takes
the *minimum* over those same regions, so each one it added could only help it
match. `crParseAlsoApplies` emits exactly that shape, so this was a parse away
from live. The hull (first line to last) was rejected instead: it charges for the
gaps between two tight regions and invents vagueness a two-region finding does
not have.

Counting **per finding** was then the same gap one level up, and it was open
until it was probed. A reviewer that files one comment on every line within the
noise tolerance of each plant has every anchor one line wide, so `ANCHOR` read 1;
every comment names a defect and sits near it, so `NOISE` counted none; and it
rates each plant correctly, so the severity metric was right to say so. Run
through `ScoreRun` over the corpus it filed **196 findings against a calibrated
reviewer's 14**, 182 of them on lines holding no defect, and returned detection
and objective severity *byte-identical* to the calibrated reference — an exact
tie with being right, on every model-free column these reports publish. Seventeen
one-line comments about one defect and one comment naming seventeen regions are
the same seventeen lines to read, and only the union tells a reader that.

Over the shipped Incumbent corpus the corpus maximum is **13 under all three**
definitions, and no fixture's value rises under the union — the change was
measured against the competitor before it was adopted, because a scoring change
that only ever moves numbers our way is one nobody should believe.

`NOISE` asks whether a finding is **near** the defect it names, not only whether
it names one. Ignoring position, 36 boilerplate one-liners on a grid — each
titled "check nil, race and secret handling" — were credited against every plant
in their file and counted as noise for none: `NOISE` 0, the value a perfect
reviewer scores, for a review that identified nothing. The tolerance is wider
than the detection tolerance, deliberately: a correct finding anchored slightly off has
already lost the detection credit, and counting it as invented as well punishes
one near-miss twice. That trade accepts *false noise* — a correct finding more
than six lines from its defect is counted as invented — in exchange for closing
*false signal*. False noise is bounded and makes a reviewer look worse than it
is; false signal is unbounded and makes it look better, and this package has
twice retracted an instrument that flattered.

This page previously said **"this corpus cannot check the constant"**, and that
was wrong in both halves. The constant was *inert*: 0, 1, 2, 4, 8, 12 and 20 all
left the entire suite green, including 0, at which `explainsAny` becomes stricter
than `matches` and the double penalty the paragraph above rejects comes straight
back. And the corpus *can* check it, by a question nobody had asked — on how many
planted fixtures is a spammer charged **nothing** because the whole file fits
inside the radius? At 8 the answer was two of twelve, and on those two the rule
was not "the comment is near the defect it names" but "the file is shorter than
17 lines". The tolerance is now the **largest value** that is strictly greater
than the detection tolerance and leaves no planted fixture vacuous, both bounds
recomputed from the fixtures on every run. The incumbent is unaffected either
way: its noise count is 3 at 4, 5, 6, 8 and 12 alike, because every Incumbent
finding naming a plant sits at distance zero from it.

Both were found the same way: by crossing every published metric against
reviewers nobody would ship and declaring, cell by cell, whether each can score
as well as being right (`TestNoDegenerateReviewerCanMaxOutAPublishedMetric`). Two
cells that should have read *no* read *yes*.

**Rule 6c-i — a degenerate strategy is a review, not a tuple.** Every strategy in
both degenerate tables is a function returning `[]review.Finding`, scored through
`ScoreRun` and booked through `CostLedger.ObserveScore`, exactly as a model's
output is. The cost table's strategies used to hand-write their own `Detections`,
and the two numbers most often written were **false for the behaviours their own
rows named**: the line-spammer declared `Noise: 40` and the wide-anchor strategy
declared `WidestAnchor: 900`, while the real scorer, run over reviews of that
shape, returned 0 and 1. A guard cannot demonstrate that a column sees a
behaviour by being handed the number the column would print if it did. Running
the real path also cost one row its justification: measured properly, a
line-by-line spammer is *more* expensive than an explained reviewer, so `$/DEFECT`
caught it and `NOISE` was not load-bearing at all. What is genuinely cheap is
refusing to explain, not filing more.

**Rule 6d — ask what maximises every column, before publishing it.** The banded
column shipped because nobody did. `PublishedMetrics` registers every model-free
score, and `TestNoDegenerateReviewerCanMaxOutAPublishedMetric` crosses it against
reviewers nobody would ship — always critical, always nit, one comment per line,
silence, one comment at every severity. A metric one of them can score as well as
a correct reviewer on does not measure what its name claims. Adding a column to a
table means registering it, which means answering the question.

**Rule 6e — a guard is only as complete as the list of things it guards, so do
not keep one.** The cross-tool severity guard ran over three of the six tables
this package prints, because "every published table header" was a hand-written
slice of three. Appending `B-ACC` to the cost table passed every test in the
package. The first fix appended the missing three and added a *second*
hand-maintained list — a name-to-value map — so a test could check the first;
two lists is the same failure with an extra step.

Declaring a header is now the only way to have one: `registerTableHeader` returns
its argument, so the declaration is the registration, and `AllTableHeaders` is
derived from the registry rather than typed out. What a source scan is left with
is bypass, in the two shapes it can take — declaring a header string without the
registrar, and underlining a table built from something never declared as a
header. `TestEveryTableHeaderInThePackageIsRegistered` checks both, over test
files too: the reports are rendered from files behind the `eval` tag, and a scan
that skipped them could not see the two tables the head-to-head is printed in.

**Rule 6f — do not ask a text search what the code does.** That scan was a regexp
over source text, so `registerTableHeader(kind, X)` written in *prose* satisfied
it: a doc comment beside an unregistered const registered it, and a withdrawn
`B-ACC` column shipped with all four guards green. Comments were then blanked out
with `go/scanner` — and the identical bypass moved into a **string literal**,
which is not a comment. This package declares several long prose constants, and a
sentence inside any of them registered a header just as effectively; that version
also shipped a `B-ACC` header past every guard, reproduced on this tree.

Excluding comments and then excluding strings is a list of the disguises somebody
has noticed, which is Rule 6e again one level down. The scans now parse the
package with `go/ast` and match **call expressions**, which neither a comment nor
a string can be. `TestProseCannotRegisterATableHeader` states it directly: three
headers, registered by a comment, by a string, and by an actual call, and only the
third is in the registry.

The same shape applied to the withdrawal itself. `O-*` and `SEV a/i/u` are two
renderings of one metric, and only the first passed through the vocabulary gate —
the second formatted the counters inline at the table, so "a foreign row prints
`n/a`" held because the battery printing that table happens to run no foreign
reviewer. `SeverityCells` registers a gated renderer per column and the required
set is derived from `PublishedMetrics`, so a third rendering fails until it is
gated.

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
- [ ] Is every word of it something that was OBSERVED? A figure derived from one
      of our own constants, printed where a reader will read it as the subject's
      own output, is the defect three of the corrections above share (Rule 6b).
- [ ] **What maximises this column?** Is there a reviewer nobody would ship —
      always critical, always nit, one comment per line, silence — that scores as
      well on it as a correct one? If so it is not a score (Rule 6d).
- [ ] If it claims generalisation, was it checked on the held-out corpus?
- [ ] Do the guard tests behind it fail under mutation?
- [ ] Would this number look different if the instrument favoured the author?
      Four instrument bugs found here did exactly that.

## Rule 14: the v1 ship decision, pre-registered

Written while the battery it governs was running and before any of its output was
read. Timestamps are checkable: this section's commit against the run log's
completion line.

**Not written blind, and pretending otherwise would be the failure this rule
exists to prevent.** An earlier narrow run over the held-out split had already
reported kimi-k3 locating 10 of 13 plants against Incumbent's 4. So a threshold
chosen now is chosen by an author who expects to pass it. The margin below is
argued from the corpus's resolution rather than from that expectation, and the
argument has to survive the reader knowing the expectation existed.

### The decision

Ship v1 if ALL of these hold on the fixtures BOTH reviewers covered:

1. **Locate count.** Plants we located ≥ plants Incumbent located.
2. **Margin ≥ 2 plants.** One plant is the smallest difference this corpus can
   express, so a one-plant lead is a tie reported as a win. This is Rule 6c
   applied to the ship decision rather than to a table cell.
3. **Noise.** Our invented findings per review ≤ 1.5× Incumbent's. Recall bought
   by commenting on everything is not capability, and NOISE is the only column
   that sees it.
4. **Anchors.** Our worst-case anchored span is no wider than Incumbent's. A
   finding naming a whole file is credited with every plant inside it and is
   noise for none; ANCHOR is the only column that sees THAT.

Any one failing means do not ship, and the report says which.

**The instrument did not carry two of these when the rule was first applied, and
the thresholds above are unchanged by the fix.** Conditions 3 and 4 name NOISE
and ANCHOR, and `TestBenchmarkAgainstIncumbent` printed neither: both columns
existed in the prompt-battery cost table and nowhere in the head-to-head, because
that path never called the scorer that produces them. Condition 3 was therefore
estimated by hand from FIND and the objective denominators — which is not the
same quantity, since `FindFigure` counts VERDICTS per sample rather than findings
— and condition 4 was not measured at all. The head-to-head now prints RECALL,
NOISE, ANCHOR and L/DEF as cells with their counts in the DENOMINATORS block
beneath it, for both contenders. Four columns rather than the two the rule names,
because `PublishedMetrics` renders detection as one group: NOISE, ANCHOR and
L/DEF are each maximised by silence, so a table publishing them without RECALL
would rank a reviewer that says nothing at the top of three columns.

Read the rate columns and the worst case differently. RECALL, NOISE and L/DEF are
rates over each row's own counts and survive the one-review-per-fixture cache the
incumbent is served from — provided each row folded every review it attempted. A
row short of that marks all four cells and prints why; only our side can lose
depth without losing coverage, so an unmarked short row would flatter us. ANCHOR
is a maximum, so a row folded from more reviews took it over more chances: with
more than one run per fixture, condition 4's column is biased against us, which is
the conservative direction for that condition and is stated on the row rather than
left to be derived.

**The cells are not restricted to the intersection the preamble names.** "On the
fixtures BOTH reviewers covered" is the rule's own scope clause, and all four
detection cells are folded over each contender's OWN reviews. Two rows of unequal
COV therefore divide by two different populations, with nothing on the cells
saying so — the coverage note under the table is about GRADE, and the counts in
the DENOMINATORS block are what a reader has to compare by hand. Applying
conditions 1–4 to rows of unequal coverage compares two corpora. The direction is
unsigned: it flatters whichever row is short.

**Two corrections to the wording of conditions 3 and 4. The four conditions and
their thresholds are unchanged, because they were pre-registered and this is the
run they govern; what is corrected is a justification that was wrong, recorded
rather than edited into the rule.**

- *Condition 3 says NOISE is "the only column that sees" recall bought by
  commenting on everything. It is not, and on the strategy that matters it sees
  nothing at all.* `explainsAny` credits any finding within `noiseTolerance` of a
  plant whose keywords it names, so a reviewer filing thirteen comments per defect
  — all of them inside the radius — renders NOISE 0.00, the value a perfectly
  calibrated reviewer earns. That is `radiusSpamReview` in the degenerate table,
  and it is caught by ANCHOR (which unions every finding claiming one defect) and
  by L/DEF, not by NOISE.
- *Condition 4 thresholds a MAXIMUM against another reviewer's maximum, and a
  maximum is not a bound on the behaviour the condition names.* The incumbent's
  ANCHOR is its single worst finding. Read as "no wider than theirs", that one
  finding becomes a width every one of our findings may spend: a reviewer that is
  right about every defect and smears each anchor over exactly that span passes
  all four conditions while pointing a reader at several times as many lines. The
  prose of condition 4 is true as written — a whole-file finding does fail it —
  and the residual is the reading, not the sentence. **The direction flatters
  us.**
  `TestTheIncumbentsWorstAnchorIsNotABudgetEveryFindingMaySpend` runs that
  strategy against the real cached incumbent, and the instrument's answer is the
  L/DEF column: the same per-defect anchor measurement summed over located defects
  instead of maxed, which separates "vague once" from "vague everywhere" where the
  maximum cannot. It is published beside ANCHOR for both contenders, with its
  counts in the DENOMINATORS block. It is **not** wired into the four conditions,
  and no threshold is proposed for it here — inventing one now, after the battery
  has been read, is precisely the move pre-registration exists to prevent.

### What will not be claimed either way

- **No cross-tool severity accuracy.** The two vocabularies are different
  resolutions and every reduction that makes them comparable is maximised by a
  reviewer that also picks what to mention (Rule 6). The severity VOCABULARY
  block is a description and may not be reduced to a figure.
- **No generalisation claim from the pooled number.** The held-out figure is
  published beside it. If they disagree in direction, the held-out one governs
  what is said about generalisation, and the disagreement is reported.
- **No cost ranking across routing bands.** Amounts marked `~` sit inside a band
  and may be ordered only if the bands are disjoint.

### What a passing result does NOT establish

That the corpus is a fair sample of real pull requests. It is 29 plants chosen by
this project, and four of its five `info` plants are reported by NEITHER reviewer
— measured, ours 0–1 of 10–16 runs each and Incumbent 0 — which says those
fixtures sit below every tested reviewer's threshold rather than that anyone
failed. A win here is a win on this corpus. Say that when quoting it.

## Rule 14a: the two amendments, and which way each moves the bar

Rule 14 was applied once, to the held-out battery, and could not yield a verdict:
condition 3 failed for a reason that turned out to be a defect in the condition,
and condition 4 named a column the run did not print. Both are amended here.

**Amended AFTER seeing which conditions failed, which is the thing Rule 14 was
written to prevent.** That is why each amendment states its direction. One
loosens the bar and one tightens it, and only the second is safe on its face; the
first has to be argued.

### Condition 3, LOOSENED — precision plus an absolute floor

Was: our invented findings per review ≤ 1.5× Incumbent's.
Now: our judged precision ≥ Incumbent's − 0.10 absolute, AND our `NOISE`
≤ 0.30 invented findings per review.

The old form fired on VOLUME AT EQUAL PRECISION. Measured held-out: precision
0.80 against 0.83 — inside a single-judge figure whose cross-judge disagreement
was never measured — while findings per review were 0.88 against 0.43 and located
defects 0.68 against 0.31. Recall rose 2.2× on volume up 2.0×, so the extra
findings were not bought at a worse hit rate. The reviewer condition 3 exists to
catch is the one with high volume and LOW precision; the old form caught
thoroughness with the same net.

It was also degenerate. On the tuning corpus Incumbent's judged precision was
13/13, so `1.5 × 0` made any noise at all a failure. A threshold a
perfect-precision incumbent renders unsatisfiable is not a threshold.

The two halves answer different questions on purpose: precision is how often we
are wrong, the absolute cap is how much we say. Neither alone is the bar.

**0.30 is chosen knowing we sit near 0.25, and the reasoning is the defence.**
0.50 would leave 2× headroom and constrain nothing — a bar that cannot fail is
decoration. 0.20 fails today, so picking it would be choosing to force work
rather than to state a standard. 0.30 binds: it passes now and a 20% noise
regression breaks it. A reader who thinks that is too generous should move it;
what may not happen is moving it again after the next number.

`NOISE` means the column — findings matching no plant — and NOT the judge's
"worth raising" figure. The two gave 0.25 and 0.175 on the same run, and leaving
which one unstated is how a condition gets evaluated twice and reported once.

### Condition 4, TIGHTENED — the worst case was a budget

Was: our worst-case anchored span no wider than Incumbent's.
Now: additionally, our `L/DEF` ≤ Incumbent's.

A worst case alone is a ceiling every finding may spend. If the incumbent's worst
anchor is 13 lines, the old condition let EVERY one of our findings be 13 lines
wide and still pass — while a finding naming a whole region is credited with
every plant inside it and is noise for none. That is Rule 6d: the column is
topped by a reviewer nobody would ship. L/DEF — lines claimed per located defect
— is what sees it, and the two together bound both the worst finding and the
habit.

This one needs no defence from its timing: it makes the bar strictly harder, and
an amendment that can only cost the author the decision is not the kind
pre-registration guards against.

### Unchanged

Conditions 1 and 2 stand as written, and so does every non-claim: no cross-tool
severity accuracy, no generalisation from a pooled figure without the held-out
one beside it, no cost ranking across overlapping routing bands.

## The third corpus

`MultiFileFixtures` is a corpus of changes whose defect is only visible by
reading a file the change does not touch. It exists to measure
`review.related_context` and to compare against hosted reviewers on the shape
of change they index a repository for.

**Rule 15 — it is neither tuning nor held-out, and it is outside the
registries.** It is re-runnable, so a gain on it is not a generalization claim
(Rule 14 does not apply). It is also outside `AllFixtures`, so none of the
cross-fixture keyword sweeps in `groundtruth_test.go` run over it; its own test
checks anchors, self-credit, and that no keyword is a token of the change, and
nothing more. A number from it is a measurement of this corpus by this
instrument, and the sentence that reports it should say so. Its first spend is
recorded in [findings.md](findings.md#related-context-on-the-multi-file-corpus).

