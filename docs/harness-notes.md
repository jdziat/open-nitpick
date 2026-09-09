---
# The harness and runner notes are 209,313 of the 589,395 characters of the
# published search index, 35.5%, and they are commentary on Go declarations, so
# they repeat operator vocabulary at length without answering an operator's
# question. A document boost was tried here and removed. At 0.3 it moved the
# runner notes off the first screen and left the harness notes at rank 1 for
# "severity", a 562px result block that is 29.2% of the rendered list, and 27.8%
# for "fail_on", because the word runs the length of the page. Weighting a page
# that is not an answer only changes how far down the wrong answer sits, so
# these two leave the answer set instead. Nothing is hidden: both stay in the
# nav under Internals and stay linked from every declaration that points at
# them. Measured 2026-09-07 against the built index.
search:
  exclude: true
---

# Notes from the harness

Every note below was a doc comment in `internal/evals`, long enough to bury the
declaration it sat on. Each records a measurement, a retraction, or a shape the
code is built around; the declaration keeps its summary and points here.

## claimTrigger

`internal/evals/claims_test.go`

fires and quiet are the pattern's own test. A regexp that has been widened
into a catch-all, or narrowed into something that matches nothing, is a scan
that reports zero violations and looks exactly like a clean tree, so every
pattern has to be shown matching the idiom it names and not matching the plain
statement of the same fact. TestClaimTriggersMatchTheIdiomsTheyName runs both
halves.

## claimReason

`internal/evals/claims_test.go`

Reaching one of these before the assertion idiom is what separates the two
kinds of prose. A bare "X cannot see the verdict" hands the reader a
guarantee; "the verdict is not part of the finding, so X cannot see it" hands
them the reasoning and invites them to check it against the code. The second
needs no citation because it has already given its evidence. Both forms are
carried as executable rows in claimShapes, spelled with a real identifier,
which is why the illustration here is not.

## claimHistory

`internal/evals/claims_test.go`

History is the most valuable prose in this package and the least dangerous: a
reader cannot act on "the banded column shipped green" as though it were a
guarantee about the tree in front of them. Only auxiliaries and explicit
time-markers are listed, never ordinary past participles, "printed",
"declared" and "scored" all appear in live present-tense assertions here, and
matching them would silently retire the guard over most of the package.

## codeShaped

`internal/evals/claims_test.go`

The discriminator is a capital after the first letter: unknownCost,
HeldOutFixtures, sameFinding. A single-capital word, Fixtures, Defect, Score
, is not accepted even when the package declares it, because at the start of a
sentence it is indistinguishable from an ordinary capitalized noun, and the
direction to be wrong in is the one that leaves good prose alone. That costs
recall on doc comments whose subject is a one-word exported name, and
claimShapes does not pretend otherwise.

## staleAfterDays

`internal/evals/cost.go`

It is a display flag and it changes no number: every cost in the table is
computed from the recorded rate whatever its age. It exists because "captured
47 days ago" is a fact a reader has to convert, and "STALE" is one they
cannot skim past. A month is the horizon over which this catalog has moved,
the gemini and qwen lines both re-tiering inside one, so it is the
point at which the right action is to recapture rather than to trust.

## referenceCorpus

`internal/evals/cost.go`

The set is a set, not a count. Keyed on the count, which is what this was,
two models that each completed two of four fixtures and FAILED DIFFERENT ONES
both report Covered=2 against a peer coverage of 2, both pass as comparable,
and their $/DEFECT figures describe disjoint corpora. That is the same "priced
on an easier subset" error the coverage check was added to catch, wearing a
number that matches, and it is reachable by ordinary flakiness rather than by
anything adversarial: one timeout each on different fixtures produces it.

The depth is per fixture and is the MAXIMUM any row reached there, rather than
one chosen row's profile. What it defends against is a row assembled from the
runs that went well: being priced on a fixture ONCE satisfies the set, so a
reviewer whose barren runs happen not to report usage is priced only on the
runs that found something. The comparison a reader makes is between rows, so
the standard is what a peer achieved on that fixture, no synthetic
number, and no row is asked for work none of its peers managed.

Ties on set size are broken on the sorted fixture list rather than on map
order, so two runs of the same battery choose the same reference and print the
same table. Where two rows hold equal-sized different sets, at most one of them
can contain the reference, so the pair is never declared mutually comparable.
The caller holds the lock.

## ShallowSampleWarning

`internal/evals/cost.go`

ONE WORDING FOR TWO TABLES. The cost ledger reached this reading first and got
it right. The shortfall is a sample the row did not choose rather than merely
a smaller one, while the judged report grew the identical failure and printed
nothing at all about it, so one loss was described two ways by two tables seven
hundred lines apart. The parts that differ between the two are
arguments, not a second sentence.

standard is what the row fell short OF, and the two callers differ here for a
reason: the cost table compares against its PEERS, because a row priced on
fewer runs than another row is the shape that reading is maxed out by; the
judged table compares against the row's OWN attempted depth, because the
incumbent is served at depth one by design and a peer-max test would mark it
short on every run with RUNS>1.

dropped names what the missing runs have in common, which is the whole content
of the warning: a random shortfall costs precision and a selected one costs the
result. readings names the figures a reader must not rank.

## Recall

`internal/evals/cost.go`

It is a cost method, not a duplicate of the score table's column, because it
is the other half of the only cost reading this file publishes: it is
computed over exactly the reviews the dollar amount was computed over, so the
pair describes one set of runs. Reading the score table's recall, which
includes reviews that reported no usage and are therefore absent from the
cost, beside this row's $/DEFECT would pair a numerator and a denominator
drawn from different corpora, which is the error this whole file exists to
avoid making with dollars.

## PerDefect

`internal/evals/cost.go`

This is the number a default is chosen on, and it is the reason cost per
review is not: a model at a fifth of the price that finds half as much is not
cheaper, and only this ratio says so. When nothing was detected it is
UNDEFINED, a division by zero, not an infinite cost, and not the free lunch
a zero would read as.

It is also the number with a degenerate maximum, which is what Comparable
defends. The cheapest possible cost per defect is bought by failing every run
that is expensive or hard and completing only the cheap ones the model finds
easy: the surviving runs are priced correctly, the ratio is computed
correctly, and the answer is a lie about the corpus. Coverage is the only
thing in the row that can see it.

## DescriptiveCostColumns

`internal/evals/cost.go`

The distinction is the whole mechanism: a column here is never asked what
maximises it, so putting a score here by mistake is how one escapes the
degenerate-strategy table. COST and TOKENS are the clearest case. A row is
not better for having spent less in total than a row that reviewed a
different number of fixtures, and COV is not an achievement but the
precondition under which the ratios may be compared at all.

## CostTableHeader

`internal/evals/cost.go`

It is a package-level const, beside the other published table headers, so a
guard running in the DEFAULT build can check that every column in it is
declared. The reports themselves are behind the `eval` tag, where no
ordinary `go test ./...` would reach them. That is the mechanism that would
have stopped the withdrawn banded severity columns being added to a header
and a legend with nothing anywhere asking what maximised them.

PRICED is the number of reviews that fed the cost, and COV the number of
distinct fixtures behind them. Both are printed beside REVIEWS rather than
hidden: when PRICED and REVIEWS disagree every cost cell on the row describes
a subset of the run, and when COV disagrees between rows the rows describe
different corpora. FAILED is calls that errored, whose charge, if the
provider raised one, is not visible from here at all, which makes the cost on
that row a lower bound. RECALL is printed immediately left of $/DEFECT and not
at the far end of the row, because the two are one reading and a reader who
has to look for the second half will not.
SPREAD is the width of the model's routing band, printed beside the amounts
rather than only in a footnote: it is the difference between a figure that is
exact and one that could be several times either way, and a reader comparing
two rows needs it in the same glance as the dollars.

NOISE and ANCHOR sit between RECALL and $/DEFECT because the five are one
reading and those two are the ones a reader would not think to want. RECALL
was printed here without either for a round, score.go's AllTableHeaders
records the gap and hands it to this track, and each of them is the only
column that sees one of the two cheap ways to buy recall.
The RECALL field is 11 wide and not 8: its cell is "14/14 1.00", which is ten
characters, and at 8 it pushed every column to its right out of line on every
row in the table.

## costFixture

`internal/evals/cost_test.go`

It was a synthetic tuple, a name, a planted count and a prompt size, and the
strategies returned hand-written Detections beside it. That made this whole
block a test of CostRow's arithmetic over numbers a person typed, and the two
numbers most often typed were FALSE for the behaviours their own rows named:
the line-spammer declared `Noise: 40` and the wide-anchor strategy declared
`WidestAnchor: 900`, while the real scorer, run over reviews of that shape,
returned 0 and 1. A guard cannot demonstrate that a column sees a behaviour by
being handed the number the column would print if it did.

So the corpus is the corpus, and every strategy publishes findings that go
through ScoreRun. What remains declared is the USAGE, and that is not the same
kind of thing: token usage is what a provider REPORTS about a call, it is not
derivable from the findings, and the ledger's job is precisely to combine a
reported usage with an observed detection. Faking the reported half is
modelling a provider; faking the observed half was faking the answer.

## costPrices

`internal/evals/cost_test.go`

This COMMENT USED TO CLAIM it "prices the two models every strategy is run
as", and that was FALSE when it was written. The "a model nobody priced"
strategy sets costStrategy.model to test/unpriced precisely so that it is
absent from here, being unpriced IS that row's behaviour, and it is the one
strategy in the table whose whole argument depends on a missing entry. Read as
a guarantee of complete coverage, the old sentence said no such row could
exist, in a file where one does. Two of the three ids the strategies run as
are below; the third is deliberately missing, and
TestAModelWithNoPriceEntryReportsUnknownAndNotZero pins the reading its
absence has to produce.

Deliberately a literal rather than the shipped table: a degeneracy guard that
moves when somebody recaptures a rate is a guard nobody will trust.

## calibratedCostRun

`internal/evals/cost_test.go`

Its completion cost is per finding because that is the term every strategy
below moves: a reviewer is billed for what it wrote, and "say forty empty
things instead of three explained ones" is a claim about output tokens that
has to be expressible or the NOISE column has nothing to be load-bearing
against.

## explainedTokens

`internal/evals/cost_test.go`

explainedTokens is what one EXPLAINED finding costs to write, and spamTokens
what one empty one costs. The gap between them is the whole content of the line-spammer row: a comment
carrying a reason is many times the output of a comment carrying none, which
is why saying everything is cheaper than saying three useful things. Ten to
one is conservative. A real rationale runs longer than ten times a one-line
nit, and the row is only claiming the sign of the difference.

## observeCostRun

`internal/evals/cost_test.go`

Going through ObserveScore rather than calling Observe with assembled
Detections is the point of the rewrite twice over. It is what makes the
noise and anchor counts in this file the scorer's answer rather than a
person's, and it is what gives ObserveScore a caller reachable from
`go test ./...`: it had none, no test, and three separate mutations of it left
the suite green, an inert function that the cost table's whole premise rested
on.

## shownList

`internal/evals/crossjudge_test.go`

The tests carry it because a cross-judge delta is only a confidence interval
when both sides were built over the same lists, so an aggregate that does not
say what it was shown cannot be paired with one that does. Two aggregates
built from the same lists disagree; two built from different ones do not
disagree at all, and the difference between those two sentences is what
JudgedFigure now has a fourth state for.

## gradeLadder

`internal/evals/crossjudge_test.go`

One end is pinned and the other climbs an ASCENDING ladder, which makes both
quantities monotone in the seed by construction. Two grades picked
independently is what the first draft did, and it produced two different means
with an identical spread, a SPREAD cell reading "+0.00" that this helper was
written to make impossible.

## OpenRunDump

`internal/evals/dump.go`

THE RUN IT EXISTS FOR HAS ALREADY HAPPENED. The held-out battery that produced
the Rule 14 evidence ran with EnvDump unset, so OpenDump returned nil, every
Record call was a no-op, and the finding lists sat in memory for the whole of
a paid run and were written nowhere. Two of Rule 14's four conditions then
needed a re-run to evaluate, against a corpus whose own label says it is
spent once. Retention is not a diagnostic convenience here; it is what makes
the next held-out spend the last one required for a model-free column, because
RECALL, NOISE, ANCHOR and L/DEF are pure functions of (findings, fixture) and
need no judge and no network to recompute.

THE RECORDING IS not GATED ON THE JUDGE, and saying so is load-bearing rather
than decorative: the arithmetic needing no judge is worth nothing if the write
happens after a judge call that can fail. It did, and a review whose judge call
errored was discarded, findings already paid for, and on the incumbent's side
drawn from a rate-limited allowance the benchmark's live path does not even
cache. TestEveryPaidReviewIsRetainedWhateverTheJudgeSays holds the write above
every return that follows the judge.

It does not change OpenDump. That function's nil-on-unset contract is shared
by the remaining callers and pinned by TestDumpDisabledCostsNothing, and the
nil no-op is what lets every call site drop a record unconditionally; a
default resolved inside it would also leave EnvDump empty, which the re-judge
path's collision check reads. Batteries that want retention ask for it here.

Which batteries those are is derived rather than listed. Saying the tuning
axes stay on OpenDump because "their corpus can be reviewed again" is true of
TestTunePersona, the one battery that remains there, and false of
TestJudgeModels, which prints the same judged table the head-to-head does and
which `make judge-models FIXTURES=$(HELD_OUT)` points at the spent-once
corpus: a class of caller named where a property of one was meant.
TestEveryJudgedBatteryRetainsItsFindingsWithoutBeingAsked derives the list
from the table, so anything calling reportJudgedModels must open through here.

battery names the caller, so a directory of retained runs says which produced
each file.

## HeldOutFixtures

`internal/evals/fixtures.go`

Tuning a prompt against Fixtures() and then reporting a score on Fixtures()
measures nothing: with enough iterations any prompt can be shaped to eight
specific changes, and the number that falls out says nothing about a real
pull request. This set is spent ONCE, at the end, to check the gain
generalized rather than memorized.

It is deliberately unreachable from Fixtures() and from the default run, so
nothing picks it up by accident. Selecting it takes naming a fixture in
NITPICK_EVAL_FIXTURES, an explicit act by whoever is measuring.

The defect classes here are chosen to be ones the tuning corpus does not
contain, because a held-out set drawn from the same distribution measures
memorization of that distribution rather than generalization:

- contract-break     a wire-format change that silently breaks consumers
- data-loss-migration an UPDATE with no WHERE, in SQL
- ts-unawaited-async  a defect in TypeScript, so the prompt is not
silently tuned to Go and Python
- timezone-boundary   a correctness bug a careless reviewer waves through
- clean-sql-allowlist code that pattern-matches SQL injection and is
provably safe; the correct review is silence
- removed-guard       the defect is in the REMOVED lines, which a reviewer
that only reads additions cannot see
- retry-no-backoff    a real defect that is only worth a WARNING, so the
severity columns can be falsified in both directions

That last one is about the instrument rather than the defect class. Without
it every plant here is error or critical, and a prompt that learned to answer
"at least error" to everything, the exact failure the O-INFL column exists
to catch, cannot be caught by the corpus that is supposed to check whether
the tuning generalized. TestHeldOutCorpusCanFalsifyInflation pins it.

THE SECOND HALF OF THE SEVERITY REBALANCE lands here, and the split was made
on one question: what would a prompt tuned on Fixtures() have to GENERALIZE
to, rather than what is left over. Held-out fixtures chosen as leftovers make
the set thin and the generalization claim weak, which is the state this list
was in, seven fixtures, six plants, one of them below error.

- csharp-client-per-request  C#, a language NEITHER corpus contained
- bash-fixed-temp-path       shell, likewise, and a security warning
- defensive-copy-nit         Java, likewise
- cross-file-sort-nit        cross-file reasoning in a different language
from the tuning corpus's cross-file plant
- duplicate-test-case-nit    the `tests` class, which appears NOWHERE in
Fixtures(), and the only nit in either corpus that costs coverage rather
than an allocation

The three new languages are the load-bearing ones and they are here on
purpose. A language planted in Fixtures() can be tuned for, and a prompt that
was tuned until it worked on C# has demonstrated nothing about the next
language it meets; the same prompt working on C# it was never shown is the
only version of that claim worth publishing. The cost is the one every
held-out set pays and is worth stating: if the reviewer is bad at shell, this
corpus finds out once, at the end, and the finding cannot be acted on without
authoring a replacement.

`tests` is here for a related but WEAKER reason than this comment first
claimed, and the difference matters. It said the class is one "NitpickNormal's
scope includes", which is not true of this plant: that scope asks for "missing
tests where new branching logic is risky", and duplicate-test-case-nit
is the opposite, a REDUNDANT case, in a change whose slug.go is byte-identical
between base and head, so there is no new branching logic for a missing-test
finding to attach to. A reviewer reading the tests clause literally stays
silent and is scored a miss, which is the "a corpus that penalizes obedience
measures nothing" trap fixtures_nit.go uses to rule out style nits.

It stays because it is still reachable, through the maintainability clause of
the same scope. A duplicated case has a concrete cost a reviewer can name.
So it measures how far the prompt generalizes past the examples it was given,
which is worth measuring; it does not measure coverage of an instructed
class, and no claim resting on that reading should be made from it.

WHAT DELIBERATELY did not COME HERE: ts-unbounded-memo-key, the only fixture
that assembles into more than one batch. Batching is a property of the
assembly and prompt this project keeps changing, and a one-shot corpus cannot
answer whether a change to it helped. The first measurement would also be
the last. It is in Fixtures() so it can be measured repeatedly, and this list
therefore still tests no multi-batch behaviour at all.

## EveryFixture

`internal/evals/fixtures.go`

The multi-file corpus is deliberately not in AllFixtures. The ground-truth
suite that iterates AllFixtures carries hand-maintained registries for every
plant, anchor assertions, hit and miss probes, severity pins, and
cross-fixture prose sweeps, and the multi-file corpus is validated by its
own, narrower test (TestMultiFileCorpusIsWellFormed) instead. That is a
weaker guarantee, and a claim about that corpus should be read with it in
mind: its keywords have not been swept against every other fixture's
recorded prose.

## CallerFixtures

`internal/evals/fixtures_callers.go`

The multi-file corpus measures one direction of cross-file reasoning: a
change USES a contract that lives in an unchanged file, and related context
attaches that contract so the reviewer can read it. This corpus measures
the other direction. A change alters what a function returns, raises, or
promises, its own file is self-consistent afterwards. The doc comment is
updated, nothing in the diff contradicts itself, and the defect exists only
because an untouched file still calls it the old way.

No reviewer shown the diff alone can find these. The diff is a plausible,
motivated change; the evidence is a call site the diff never mentions. That
is the point: this corpus exists to measure what a caller-aware collector
buys, one that finds the untouched files which import the changed symbol
and attaches how they use it. Before that collector exists the honest
number here is the floor, and it is measured first so the gain has
something to be measured against.

Every fixture here inverts the multi-file corpus's structural rule, and
TestCallersCorpusIsWellFormed holds the inverted rule: the changed file is
imported by a file that is byte-identical in Base and Head, and every plant
sits on a line the change added IN THE CONTRACT FILE, because that is the
line a reviewer comments on, "this breaks web/users.go" belongs on the
line that breaks it.

Keywords credit only a finding that has SEEN the caller: its file, its
function, or a detail that exists nowhere else, the body length the upload
handler compares against, the page of 500 the exporter asks for. Nothing
about consequences. Three floor runs showed why: a reviewer that reasons
well from the diff alone writes "callers passing this to setTimeout now run
1000x too short" and "any caller comparing against a byte limit", and one
said outright that the diff showed no callers and it was assuming some.
Those are good comments and they are not what this corpus measures; the
clean pair is what keeps them from being free.

Two of the six are clean. Each is the control for a planted fixture beside
it: the same files, the same caller, and a change to the same function that
keeps its contract. A reviewer that flags "this might break callers" on
every signature change has not looked at the callers, and the clean pair
is what catches it.

## dedupFixtures

`internal/evals/fixtures_dedup.go`

WHAT was wrong. ts-unbounded-memo-key made this project's first multi-batch
review happen at all, seven files at the shipped max_files_per_request of 6
, but it was authored to keep the plant and the files needed to see it in the
Same batch, because a defect split across requests is one no reviewer can
find and a plant nothing can find scores as a prompt weakness forever. That
is the right call for a scored plant, and its cost is that the second batch
has nothing to say: only one batch ever reports the defect, so dedupe(),
which triage() runs over the combined findings, has never had two reports of
one defect to collapse. The engine's cross-batch merge, README calls it a
headline capability, has therefore never merged anything under any
measurement or any test. fixtures_warning.go says so in its own words: "a
fixture that makes it do so is still owed". This is that fixture.

WHY both BATCHES REPORT IT. The defect is one bug with two faces, and each
batch holds a complete, independently reportable face of it:

- platform/retry/retry.go:16 (batch 2) is the CAUSE. Replayable used to
admit only the read methods; it now admits everything except PATCH. A
reviewer holding that file alone can name the consequence without seeing
any caller: Do re-sends a POST the gateway may already have applied.
- billing/charge.go:20 (batch 1) is the EFFECT. Capture hands a payment
capture to that retry helper as a POST, with nothing on the request that
lets the gateway recognise a repeat. A reviewer holding that file alone
can name the consequence without seeing the helper: a capture that is
retried captures twice. "Retrying a non-idempotent request" is the review
prompt's own worked example of a warning, so this is not a defect a
reviewer has to be clever to see from either end.

Neither half needs the other to be reportable, which is exactly the shape
ts-unbounded-memo-key deliberately does not have: there, the defect is
invisible from either file alone, so splitting it across batches would erase
it. Here, splitting it across batches DUPLICATES it. That inversion is the
whole fixture.

WHY both NAME THE same LINE. The two reports collapse only if they land on
the same path and line, because Finding.Key is path, line and normalized
title. review.md tells a reviewer to "anchor to the line where the problem
is, not where its effect surfaces", and for this defect that line is
retry.go:16, the predicate that declares a POST replayable. The effect side
reaches the same line from the other end: charge.go's own import names the
helper, the helper is part of this same change, and filterAnchors validates a
finding's path against the whole change rather than against the batch that
produced it, which is what lets a finding from batch 1 anchor there at all.

THE RESIDUAL, STATED RATHER THAN HIDDEN: review.md also says "path must
exactly match one of the file paths given below", and a reviewer that obeys
that literally anchors in its own batch, charge.go:20 from batch 1,
retry.go:16 from batch 2. Those are two keys, and dedupe cannot collapse
them; only the triage model can. TestOneDefectAnchoredTwiceIsNotDeduped pins
that boundary so nobody reads the test above as a claim the engine merges
every cross-batch duplicate. It merges the ones that agree about where the
problem is.

DELIBERATELY IN NEITHER CORPUS, and this is the one thing about this file
that has to be read before it is copied. Fixtures() and HeldOutFixtures() do
not name it, so AllFixtures() does not contain it and NO ground-truth test in
groundtruth_test.go touches it, which is precisely the failure
TestEveryAuthoredFixtureIsWiredIntoExactlyOneCorpus exists to catch for
warningFixtures and nitFixtures. It is not an oversight here and the checks
are not skipped: fixtures_dedup_test.go re-derives this fixture's line
numbers out of Head by counting, checks the plant sits on a line the engine
could publish a comment on, and checks the batch split, rather than trusting
the paragraphs above. It stays out of the scored corpora because a fixture
whose point is that ONE defect gets reported TWICE would be scored as one
detection and one false positive by a reviewer that did exactly the right
thing, which would make the noise column read a correct review as a sloppy
one.

IF IT IS EVER WIRED IN, two things have to move with it, and neither is free:
score.go would need a notion of a defect with more than one acceptable
anchor, and this plant is correctness at warning while every correctness
plant in fixtures.go is error with no SeverityNote, so
TestSeverityIsConsistentWithinADefectClass would turn three green plants red
in a file this change does not own. SeverityNote below is written for that
day; it changes nothing today.

IT was RUN. Both states were extracted to a temp module and built, vetted,
gofmt-ed and executed under `go test`. Head reproduces the plant, a gateway
that applies a capture and then reports a timeout receives the same capture
body twice for one order, and base does not, because base refuses to replay
a POST at all. The filler files are exercised by the same run rather than
eyeballed, which is what would have caught the second, unplanted,
user-reachable defect ts-unbounded-memo-key shipped with: a Ledger that
handed out its internal slice, or a Level whose String fell through, are both
findings a reviewer would report and neither is planted.

## crossBatchReplayFixture

`internal/evals/fixtures_dedup.go`

THE BATCH SPLIT IS LOAD-BEARING and IT IS ALPHABETICAL. git orders a diff by
path, bundle.batch fills a request with up to max_files_per_request entries in
that order, and exactly six of the eight changed paths sort before platform/:
three under billing/, three under internal/. So batch 1 is billing/charge.go
plus five files with nothing wrong with them, and batch 2 is
platform/retry/retry.go plus platform/version/version.go. REMOVING one of
those six is what breaks it: five paths before platform/ leaves room for
retry.go in the first request, both halves arrive together, one reviewer sees
the whole defect and reports it once, and the fixture silently stops testing
anything while every test here still compiles. Adding one is survivable but
not free, the split moves, and whichever filler lands beside retry.go is the
file a reviewer of batch 2 has to ignore. TestTheDedupFixtureSplitsTheDefect
reads the split back out of bundle.Assemble rather than trusting this
paragraph, for the reason the corpus already learned once: two one-line edits
were enough to falsify the same claim about ts-unbounded-memo-key while
`go test ./...` printed ok.

THE FILLER IS not PADDING and IT IS not DECORATION. Six files carry no
defect, five holding batch-1 slots and version.go riding along in batch 2,
and every one of them is a change a reviewer should wave through: a method
that reports whether a customer left an address, a total over entries the
ledger was already copying out defensively, a fixed clock for tests, a level
comparison, a prefix trim, a version bump. A filler file with
a defect in it would be a false positive charged to every reviewer that
reported it, and a filler file with no added lines at all would be dropped by
bundle for having nothing to comment on, which would shrink the change back
to one batch.

WHAT THE FIXTURE IS CAREFUL not TO INVITE. The head's predicate excludes
PATCH and admits GET, HEAD, PUT and DELETE, all four of which are
idempotent, so there is exactly one thing wrong with it: POST. An earlier
draft excluded DELETE instead, which is idempotent, and that hands a reviewer
a second true remark, "your exclusion is backwards", in a fixture whose
whole premise is that there is one defect to report twice. Do itself is
byte-identical in both states for the same reason: a retry loop that is new
code invites remarks about backoff and jitter, and the corpus already plants
retry-no-backoff as a separate warning in the held-out set.

## infoFixtures

`internal/evals/fixtures_info.go`

Measured over AllFixtures() before these were written, the corpus planted 4
critical, 8 error, 6 warning, 0 info and 6 nit, 24 plants across 25
fixtures. Every other level carried at least two plants and an argument;
info carried none, so no claim about it was falsifiable. A reviewer that never emits the
word `info` and a reviewer that emits it perfectly scored identically, and
the O-INFL and O-UNDR columns could not see the level at all: with nothing
planted at info, a reviewer rating a nit as info was charged inflation and a
reviewer rating a warning as info was charged understatement, but no reviewer
was ever charged for MISSING info, because there was nothing there to miss.

WHY this LEVEL RESISTED TWO PREVIOUS ATTEMPTS, and it is worth writing down
because it is a property of the level rather than of the authors. Every other
severity is defined by a failure: something returns the wrong answer, leaks,
deadlocks or breaches. Authoring one is a matter of choosing the failure and
then choosing how much has to go right for it not to happen. Info has no
failure. The shipped anchor is "`info`, a defensible concern the author
should consciously accept or reject", and what a published example of it has
to do is name a cost WITHOUT naming an input that breaks.

This paragraph deliberately does not quote the ladder's current examples, for
the reason fixtures_warning.go stopped quoting them: a comment keyed to prompt
prose goes stale every time the prompt is edited, and the property argued here
is a property of the LEVEL. It went stale twice already. The version before
this one quoted an example reading "so a rise in failures reads as a fall in
traffic" and asserted in the same sentence that the ladder's examples "name no
input that breaks", the quotation names the input, a failure, and the wrong
output, traffic reading as falling. Both the example and the claim about it
were wrong, and the claim was refuted by the text it quoted.

THE AUTHORING TEST, which is what survives when the quotations are removed and
is the reason both of those examples were replaced: ask whether the AUTHOR
COULD BE wrong. If the author could be wrong, the finding is at least a
warning. The usual move, take a defect and turn the dial down, therefore
does not produce an info finding at all; it produces a warning whose
consequence has been made small. Info is the level where reasonable engineers
split, and where the reviewable fact is that the author should have DECIDED
rather than drifted.

The test cuts both ways, and the ladder has now failed it in each direction.
An example may not state a wrong answer as a fact, because then the author IS
wrong and the rung is a warning. And an example may not name a mere DIFFERENCE
either: one earlier version was "a subcommand configured on the command line
where the tool's other subcommands read a config file", which named no cost at
all, failed review.md's own bar ("report a finding only when you can name a
concrete consequence"), and was besides a consistency observation,
config.ClassStyle, which allowedClasses drops below pedantic, so a reader of
the default configuration could never have seen the finding it illustrated.

The ladder's two info examples were two of these plants and have been
replaced, which is why two SeverityNotes below argue from a clause rather
than from an example. review.md illustrated `info` with "Widening an exported
type's accepted input is info. Adding a dependency for one helper function is
info.", kotlin-widened-input and rust-crate-for-one-call stated almost
verbatim, three lines above "These examples ... are deliberately drawn from
defect classes you are unlikely to meet in this change; do not go looking for
them." So the prompt named two planted defects and then told the reviewer to
ignore them. Measured, kimi-k3, two independent three-run batteries: both
fixtures 0 of 3 every time, usually with an empty findings list.

THE FIX was ON THE PROMPT and not ON THESE PLANTS, and the reason is not the
corpus. Every other rung illustrates with a SCENARIO, "Writing a decrypted
secret to a log that ships off-host", "A check-then-act on a file that
another process can replace between the two steps", while those two were
CATEGORIES, and the anti-anchoring sentence's claim of rarity was therefore
false of them for any reader: widening an exported signature and adding a
dependency for one helper are among the most ordinary things a reviewer
meets, so in a real repository the prompt was suppressing two legitimate
findings. Nothing here moved: the diffs, anchors, keywords, classes and
levels are untouched, and testdata/incumbent stays keyed to them. What could
not stay is a note deriving its level from a sentence that no longer exists.

Every plant below was tested against that question before it was written:
name the case FOR the change, in one sentence, and refuse to plant it unless
that sentence is one a senior engineer would say. Those sentences
are in each fixture's doc comment, alongside the case against. A plant whose
"for" side is a straw man is a warning with the dial turned down, and the
previous round's gate audited for exactly that.

THE PROMPT MAY not BE ABLE TO REACH this LEVEL, and that is a measurement
these plants make rather than a reason not to author them. review.md's bar
section says "Report a finding only when you can name a concrete consequence:
an input that produces a wrong result, a state that deadlocks or panics, a
request that leaks data, a path that loses an error. If you cannot describe
how it fails, it is not a finding." No info finding can clear that bar as
written, because no info finding fails. The severity anchors define the level
anyway, and the normal-level scope adds the one clause that lets a reviewer
reach it: "You may report a maintainability problem only when you can name
what it will cost concretely". So the shipped prompt contains a genuine
tension, and until now nothing in the corpus could see it. If every model
misses all five of these while scoring well elsewhere, that is evidence about
review.md's bar and not about the models, and it is the first evidence this
tree has ever had either way. Every plant below therefore states a CONCRETE
COST, because that clause is the only door into the level and a plant that
does not fit through it measures nothing.

FOUR CLASSES were UNAVAILABLE, and the reason is mechanical rather than
editorial. TestSeverityIsConsistentWithinADefectClass requires a SeverityNote
from every member of a class that carries more than one severity.
correctness, concurrency, contract and data-loss are each planted in
fixtures.go at a single level with no notes at all, so an info plant in any
of them turns green plants red in a file this change does not own,
correctness alone would need notes on three. That is a real cost and it lands
on the most natural home for two of these: widening an exported type's
accepted input is contract-flavoured, and it is planted here as
`maintainability` instead. The declared class is honest on its own terms,
ClassContract is "a change that breaks existing callers", and nothing below
breaks a caller, which is precisely why these are info and not error, but a
reader should know the taxonomy was not the only pressure. Closing that needs
notes on contract-break, in a change that owns fixtures.go.

LANGUAGES. Rust, Kotlin, Ruby and PHP are all new to the corpus; one plant is
Go. Before these, fourteen of twenty-five fixtures were Go, counted rather
than remembered, and the previous count in this sentence, sixteen of
twenty-four, was wrong in both figures, so a prompt tuned on this corpus
could be Go-shaped without anyone noticing.

This FUNCTION IS not A CORPUS and nothing runs it as one. The five below are
split across Fixtures() and HeldOutFixtures(), which name each of them
directly; what this returns is the record of what was AUTHORED at this level,
and TestEveryAuthoredFixtureIsWiredIntoExactlyOneCorpus is what makes the two
facts agree. Without it a fixture can be written, reviewed, merged and never
wired into anything, passing every test in the tree while measuring nothing,
which is the quietest way this corpus has to lose a plant.

That IS EXACTLY WHAT HAPPENED TO THESE FIVE, and the paragraph above was true
of them for as long as it was false. They were authored and never named by
either accessor, so AllFixtures did not contain them and no ground-truth test
touched one: unchecked lines, unprobed keywords, unpinned severity, in a diff
where a plant that measures nothing looks exactly like a plant that does.
Three defects survived that silence and were found by running the fixtures
rather than reading them, a Kotlin head that broke every named-argument
caller, a Rust crate cargo would not build, and a keyword the change's own doc
comment supplied, and each is written up in the fixture it belongs to.

The guard could not have caught it either: it knew the names warningFixtures
and nitFixtures and was written as a list, so a set added after it was blind
to it by construction. It now DISCOVERS every authored fixture out of the
package source instead, which is the only version of that test that survives
the next file. Where each of these five went, and why, is argued in Fixtures()
and HeldOutFixtures().

SUBSTRING KEYWORDS are THE wrong INSTRUMENT FOR PART OF this LEVEL, and this
is the third round to edit these lists, so it is written here rather than
discovered a fourth time. Every other level in this corpus is defined by a
failure, and a failure brings its own nouns: nil, injection, WHERE clause,
symlink, socket. A reviewer that has found the defect uses them and one that
has not cannot. Info has no failure, so the vocabulary is shared, at this
level the finding and the objection are frequently the same WORDS ABOUT THE
Same LINE, differing in what the reviewer is asserting.

Two of the five demonstrate it, and the two are not equally bad:

- php-forbidden-vs-404 is UNREACHABLE, provably. The finding's fix and the
error-body objection's fix are both "make the two denials the same", and
"the two denials should return the same response envelope" contains "the
two denials should return the same response" as a substring. mentionsAny
is strings.Contains, so any keyword crediting the first credits the second:
no word list separates them, whatever it contains.
TestTheInfoRecallThisInstrumentCannotBuy carries the proof and pins the
price, the corpus denies both, so a terse reviewer proposing exactly this
plant's fix is scored a miss.
- ruby-default-page-size is REACHABLE but FRAGILE. "Other consumers now
receive 100 rows" is the finding and "other consumers can still receive
200" is the cap objection; a keyword could separate them, but only by
naming TENSE. Every other keyword in this corpus names subject matter, and
one that names grammar is a rule about how a sentence is built rather than
about what it says. "other caller" and "other consumer" were removed
rather than qualified for that reason. The number is the seam that does
work. The cap is 200 and the new default is 100, so "rows by default"
separates them on subject matter, and it only reaches the sentences that
quote the size. The fix stated as a location, "set it at the call site
instead", stays uncredited: every phrase reaching it is one a reviewer
types about any line in any file.

WHAT this MEANS FOR THE NUMBER. Info recall on these two plants is a LOWER
BOUND and not a measurement: correct terse findings are uncredited by
construction, and no keyword edit changes that. The three options that would
, a required conjunction, a veto phrase, or judging detection at info against
Defect.Why with the model judge, are all changes to Defect and matches(),
argued in that test. None was made here: this round's scope was the keyword
damage, and a scorer change to close a measurement gap belongs in a change
that owns the scorer and can probe it in both directions.

## rustCrateForOneCallFixture

`internal/evals/fixtures_info.go`

This was the anchor's own second example. The ladder read "adding a
dependency for one helper function is info" until that illustration was
replaced, for the reason in this file's header, planted in the language
where a manifest change is most visible. The change is small and entirely
reasonable: the nightly report printed durations as a seconds count, someone
on the rota misread 150 as minutes, and the fix spells them "2m 30s".

THE CASE FOR IT, which is why this is not a warning with the dial turned
down: humantime is small, widely used, and formats plural units and unit
breaks correctly, which is exactly the kind of tedious code a team should not
be writing itself. THE CASE AGAINST: it is one call site, the output this
report needs is a few lines of arithmetic, and a dependency is not a local
cost. It is in every build, every lockfile bump and whatever audit the team
runs, forever. Both sentences are ones a senior engineer says. Neither is
wrong, and the reviewable fact is that the author should have weighed them.

The anchor is the manifest line, not the call site, because the manifest line
is what the decision IS: the call site is fine either way, and it is the
second half of a fix that begins by deleting the dependency.

THE FALSE POSITIVE this INVITES is the version-specification objection:
`humantime = "2"` accepts any 2.x, so a reviewer reaches for pinning, an exact
version, or a lockfile. That is a real remark about supply-chain hygiene and
it accepts the dependency, which is the opposite of this finding, so
"version", "pin", "semver" and "supply chain" are all absent, and so is the
bare word "dependency", which both objections type in their first sentence.
The second is the output-format remark: humantime prints sub-second
components, so a job that took 5.2s now renders "5s 200ms". That is an
intended consequence of the change rather than a defect, and it shares no
keyword here. Both were run through matches() rather than reasoned about, and
both are in TestKeywordsAdmitOnlyRealDetections.

IT did not BUILD, and that is worth recording because this is the one fixture
in either corpus whose plant is IN the manifest. Cargo.toml and src/report.rs
were its only files, so cargo refused the manifest before compiling anything:
"no targets specified in the manifest, either src/lib.rs, src/main.rs, a
[lib] section, or [[bin]] section must be present". A manifest that does not
build is not a manifest a reviewer is reading, and the finding this fixture
scores is a judgement about a manifest. src/lib.rs supplies the missing
target; see the note on Extra below for why it lives there.

IT IS BUILT and RUN NOW. Both states were extracted and driven under `cargo
test` with an integration test calling render on a 150-second job: base
answers "nightly: 150s" and head answers "nightly: 2m 30s". So the change
does exactly the one thing its doc comment claims and nothing else, and the
crate a reviewer is asked to judge is one that compiles.

## kotlinWidenedInputFixture

`internal/evals/fixtures_info.go`

The anchor's own first example until that illustration was replaced, the
ladder read "widening an exported type's accepted input is info", which is
this plant, for the reason in this file's header. The reason it is planted in
Kotlin rather than Go is that Kotlin's non-null types make the widening carry
NO new failure. Widening a Go parameter from a struct to an interface makes
nil a newly reachable input, and a reviewer reporting the nil panic would be
reporting a real defect this fixture never meant to plant. Here summarize
cannot be handed null, iterates nothing, and reads only members every
implementer must provide, so the change is exactly the design decision and
nothing else.

THE CASE FOR IT: the weekly mail is the next thing on the board, it has no
day to report, and an interface introduced now can be shaped by the caller
that needs it rather than retrofitted around one that already shipped. THE
CASE AGAINST: there is one implementation in this change and the second
caller does not exist yet; `summarize` is public API, so the wider input can
never be narrowed back, and DailyReport has grown three public properties
that are aliases of fields it already had. Both are ordinary review
positions. The reviewable fact is the fork, not a failure.

THE FALSE POSITIVE this INVITES is the aliasing remark: label, gained and
lost are second names for day, signups and cancellations, so a reviewer asks
for one set or the other. That is a naming observation, the generation scope
excludes naming, and its fix leaves the widened signature exactly where it
is, so "alias", "duplicate", "two names" and "rename" are all absent.

THE SECOND IS THE VISIBILITY REMARK ("make Summarizable internal"), which
accepts the abstraction and argues about who may SEE it. This comment used to
claim it was "admitted only through phrases that also name the widening", and
that was false: "public api", "public surface" and "surface area" were all
keywords, and all three match a remark that has noticed nothing about the
parameter, "this adds public API surface area; internal would keep the public
surface smaller" scored full recall, and survived only by an accident of
anchor distance. The three are gone.

What is left admits that remark only when it reaches for the widening or for
the one-implementation argument, and that is deliberate rather than a residual
leak: a reviewer that writes "there is only one implementation" has made this
plant's case whatever fix it goes on to propose. The pure form, the one that
argues visibility and nothing else, is the probe in
TestKeywordsAdmitOnlyRealDetections, and it is run through matches() rather
than described. "interface" stays absent for its own reason: it is the
change's own most typed token.

IT was COMPILED, and doing so is how this fixture's second, unplanted defect
was found. Head used to rename the public parameter from `report` to `source`,
which is not a change Kotlin lets a caller ignore: named arguments are part of
the signature, so `summarize(report = r)` compiled against Base and failed
against Head with "no parameter with name 'report' found" under kotlinc
2.0.21. That falsified two sentences of the SeverityNote below verbatim,
"every caller that compiled before compiles now", and "this breaks none, which
is exactly why it is not an error", and it made the fixture plant a contract
break at info. The rename bought nothing, so the name stays: the widening is
now the only change. Both states were recompiled with that named caller and
run, and both print "2026-08-04: +12 / -3".

## phpForbiddenVsNotFoundFixture

`internal/evals/fixtures_info.go`

The change ADDS the membership check: before it, any signed-in viewer could
read any project, and after it they cannot. That is the shape worth having at
this level, because it makes the fixture unmistakably not a defect with the
dial turned down. The change is a security improvement, and the only thing
left to review is which of two correct denials it should send.

THE CASE FOR 403: it is what the status code means, it tells a legitimate
user who has landed on a colleague's link that they need access rather than
that they mistyped, and support can tell the two apart. THE CASE FOR 404: a
403 confirms to anyone holding an id, from a shared link, a log line, a
referrer, a support ticket, that a project with that id exists and that they
are not on it, and hiding that costs nothing but debuggability. Serious
products ship both: most APIs answer 403, and GitHub answers 404 for a
private repository. That is the strongest available evidence that this is a
decision rather than a defect, and it is why the level is info.

THE FALSE POSITIVE this INVITES is the authorization objection: a reviewer
that reads the added branch as missing rather than present reports an IDOR,
and one that has not read the docblock asks who $viewerId is. Both are
hallucinations about a check that is in the diff, so "authorization", "access
control", "idor" and "permission" are all absent. The second is the
error-body remark, the two responses carry different bodies, so a reviewer
may ask for a shared error shape, which is a consistency observation whose
fix leaves the disclosure exactly where it is.

This FIXTURE CREDITED both OF THEM, and it took running them through
matches() to see it, which is the point. "enumerat" credited the IDOR
hallucination in the same paragraph that declares it must not be credited:
"an attacker can enumerate project ids" is how that finding writes itself, and
enumeration is the attack that FOLLOWS this disclosure rather than a sign the
disclosure was noticed. "exists" is a bare English verb and credited "No test
exists for the non-member path", a remark about coverage, scored as a
security detection. "hide" and "hides" credited the error-body remark, which
asks the 403 body to "hide internal details". All four are gone and the
probes are in TestKeywordsAdmitOnlyRealDetections. What is left either names
the disclosure as a noun or requires the sentence to say what the response
tells the caller.

IT was RUN, under php 8.3, against stub Response, Project and
ProjectRepository classes with one project the viewer is a member of, one it
is not, and one that does not exist. Base answers 200, 200, 404: any
signed-in viewer reads any project. Head answers 200, 403, 404, which is
both the security improvement the change is for and, in the last two rows,
the disclosure this plant is about, since the pair of denials is exactly what
tells a caller holding an id which of the two it is holding.

## goPackageSingletonFixture

`internal/evals/fixtures_info.go`

The one Go plant in this set. It is the seed in its most familiar form: a
package-level default plus thin wrappers, so a caller can ask one question
without being handed a *Set first.

THE CASE FOR IT: this is what the standard library does, http.DefaultClient,
log.Default, flag.CommandLine, and threading a value through four layers so
one leaf can ask "is this flag on" is a real cost paid by everything in
between. THE CASE AGAINST: the answer is now fixed at process start from the
environment, so a test that wants a different set has to reach into the
package and put it back, and two consumers in one binary cannot differ. The
alternative is already written: Load returns a *Set and Enabled is a method
on it, so passing one costs a parameter.

Nothing here is a defect and the code is deliberately built so that nothing
is. Load skips empty names, so an unset variable yields an empty set rather
than a set containing "". Set is read-only once built and says so, so there
is no race to report. Enabled cannot panic. Strip any of that and the fixture
stops being about the decision.

THE FALSE POSITIVE this INVITES is the configuration objection: os.Getenv
returns "" for an unset variable, so a reviewer asks for validation or a log
line at startup. It is a different concern with a different fix and it
accepts the singleton, so "env", "environment", "getenv" and "features" are
all absent. The second is a race report about the shared map, which the
type's own doc rules out. And "thread" is absent for a mechanical reason: the
change's own comment contains "thread a *Set through every layer", so it is a
word a reviewer can type by quoting.

"package-level" was not ABSENT, and the same comment contains it too, the
exact failure the sentence above describes, in the same fixture, one clause
later. See the note on Keywords. Both objections above are now probes in
TestKeywordsAdmitOnlyRealDetections, and so is the docs nit that found it.

IT was BUILT and RUN. Both states were extracted as a module and driven under
`go vet` and `go run` with FEATURES=alpha,beta. Load("alpha, beta, ,gamma")
answers true, true, false, false in both, so the empty name really is skipped
and no input makes Enabled wrong; head additionally answers through the
package-level helper, which is the whole of what the change adds.

## InfoFixtures

`internal/evals/fixtures_infocorpus.go`

It exists because no reviewer, this one, the hosted incumbent, kimi, glm
, has ever located the four `info` plants in the other corpora on any run,
and the tables could not say whether that is the plants or the reviewers.
Each plant here is a change whose consequence a senior reviewer would name
in one sentence and want the author to decide about: not a wrong result on
any path, but a cost, reproducibility, a lost type, a shared mutable
value, a scan the database will make on every request, that the change
takes on without saying so. The clean controls take a similar-looking step
and pay no such cost, so a reviewer that objects to every change of this
shape is scored for it.

Rule 15 applies: re-runnable, not tuned on, and outside AllFixtures.
TestInfoCorpusIsWellFormed checks what can be checked without the
registries.

## MultiFileFixtures

`internal/evals/fixtures_multifile.go`

Every fixture in the other two corpora is judged from the diff and the files
it names. That is also what a hosted reviewer with the whole repository
indexed is supposed to be better at, and nothing here had ever measured it:
the one cross-file plant in the tuning corpus (ts-unbounded-memo-key) puts
the contract in a file the change ADDS, so the reviewer is shown it. Here the
contract sits in a file that is byte-identical in Base and Head. A reviewer
that reads only the diff cannot see it and must either guess or stay quiet;
a reviewer that follows the import can read the sentence that makes the
change wrong.

So this corpus answers two questions the others cannot:

- whether review.related_context, attaching the definitions a changed
line uses from files the change does not touch, finds defects a
diff-only review misses, and what it costs in precision on the two clean
fixtures, which honour their helpers' contracts exactly;
- how this reviewer compares with hosted ones that index the repository,
on the changes those products are built for.

It is a THIRD corpus rather than more held-out fixtures because it will be
re-run: the feature it measures is new and will be tuned, and a corpus that
is spent once cannot answer "did that change help?" twice. It is not a
tuning corpus either. The prompt is not tuned on it, but nothing here
should be read as a generalization claim.

It is also outside AllFixtures, and therefore outside the ground-truth suite
that sweeps every plant's keywords against every other fixture's recorded
prose. TestMultiFileCorpusIsWellFormed checks what can be checked without
those registries: that every plant is on an added line, that its Why is
credited by its own keywords, that no keyword is a token of the change
itself, and that the contract really is in a file the change does not
touch. See EveryFixture for what that leaves unchecked.

Every helper's contract is written the way a maintainer writes one: a doc
comment on the definition, in the language's own convention, stating what a
caller must do. None is hidden in a README or a test. The plants are
realistic in the sense that matters, each is a change a competent engineer
makes when they have not read the callee, and unrealistic in the sense that
every corpus is: the repository is ten files, not ten thousand.

## nitFixtures

`internal/evals/fixtures_nit.go`

Measured over AllFixtures() before these were written, the corpus planted 4
critical, 8 error, 1 warning, 0 info and 1 nit across 15 fixtures. A severity
distribution that shape cannot support a severity claim: "answer critical to
everything" scores perfectly against it, and at the bottom of the scale ONE
defect was the unit of resolution for every statement the reports made about
nits. A single plant cannot distinguish a reviewer that calibrates from a
reviewer that got one fixture right.

The correction that would have been worthless is relabelling. Taking an
existing plant and dialling its severity down to fill this bucket produces a
corpus that looks like evidence and is not: the plant's Why still describes a
descriptor leak, and the number beside it now says nit. Nothing here was
moved. Every defect below is newly authored and is one a senior reviewer
would rate nit on its own terms, against the anchor the model is given:
"`nit`, minor and optional."

The anchor's illustration changed under these plants, and every note below
was rewritten in the same commit rather than left quoting it. The rung used
to read "`nit`, minor and optional. *An unnecessary intermediate copy is a
nit.*", and five plants, cross-file-copy-nit, cross-file-sort-nit,
sorted-for-min-nit, defensive-copy-nit and capacity-hint-nit in fixtures.go,
derived their level from that one sentence. It was a CATEGORY where every
other rung illustrates with a scenario, and three lines below it review.md
says the examples are "drawn from defect classes you are unlikely to meet in
this change; do not go looking for them", which is plainly false of an
unnecessary copy for any reviewer of any repository, and named the class of
three of the plants below. So the illustration was replaced and the plants
were not: their level now rests on "minor and optional", which is the clause
the sentence only ever illustrated.

TWO THINGS are UNMEASURED HERE and are not CLAIMED. No battery was run at
this rung under either wording, so nothing here says what the old sentence
did to nit recall. Its measurable half was also weaker than the info pair's:
the info line contained two crediting keywords verbatim ("accepted input",
"for one helper") while the nit line contained none, cross-file-copy-nit's
keyword is "unnecessary copy" and the intervening word "intermediate" breaks
the substring, so the argument for replacing it was the user-facing one
above, not a leak.

Two constraints shaped what could be planted here, and both are
worth writing down because they eliminate most of what the word "nit"
normally means:

- Style is not generated. config.GenerationLevel is NitpickNormal, whose
scope tells the reviewer "Do not report: naming preferences, documentation
wording, formatting, import order". A naming or doc-comment nit planted
here would be a plant the reviewer is instructed not to report, and a
corpus that penalizes obedience measures nothing. So every plant below is
in a class the reviewer is asked for and carries a runtime cost.
- The same scope says "or anything a formatter or linter already enforces".
That rules out the pattern-matchable nits, gosimple's S1025, clippy's
needless_collect, rubocop-performance's Detect, because a reviewer that
stays silent on those is obeying, not missing. What is left, and what
these use, is waste that only becomes visible from a CONTRACT: what
another function already guarantees, what a table already covers, what a
local variable can and cannot reach. No linter can see any of it.

Each plant states its cost, because the shipped prompt says so outright at
the one level that invites nits at all: "A nit with no stated cost is noise
even here". "Minor" is not licence for vagueness. A nit a reviewer cannot act
on in one sentence should not have been written.

Two of the five are multi-file, which nothing else in the corpus is, and in
both the defect is invisible from either file alone: the call site looks
prudent, and only the callee's contract, changed by the same pull request,
so it is in the diff, shows that it is buying nothing. That is the property
worth having. Be precise about what it does not buy: two files fit in one
batch under the default MaxFilesPerRequest of 6, so these exercise
cross-file REASONING inside a single request and leave the 6-file cap, the
4-way concurrency and cross-batch triage dedup as untested as they were. That
gap is now closed elsewhere: ts-unbounded-memo-key, authored at warning,
changes seven files and is the first fixture in the corpus to assemble into
two batches. Cross-batch DEDUP is reached but runs trivially, and it is not
owed by either file: it cannot be authored. bundle.batch appends each entry
to exactly one Batch, so no path is ever in two batches, and review.dedupe
keys on path:line:title, two batches therefore cannot collide by
construction. The only way one could is a reviewer inventing an anchor inside
a file it was never shown, which is a model failure and not something a
fixture can force. Recording this so the next reader does not spend an
afternoon trying to write the fixture that closes it.

This FUNCTION IS not A CORPUS and nothing runs it as one. The five below are
split across Fixtures() and HeldOutFixtures(), which name each of them
directly; what this returns is the record of what was AUTHORED at this level,
and TestEveryAuthoredFixtureIsWiredIntoExactlyOneCorpus is what makes the two
facts agree. Without it a fixture can be written, reviewed, merged and never
wired into anything, passing every test in the tree while measuring nothing,
which is the quietest way this corpus has to lose a plant.

## redundantSnapshotCopyNitFixture

`internal/evals/fixtures_nit.go`

This is the shape the corpus has never had. Read report/summary.go alone and
the copy is the careful thing to do rather than merely defensible: a caller
holding a slice another goroutine can append to is a real bug, and the
comment above the copy says exactly that. Read store/store.go, changed by
the same pull request, so it is in the diff, and Snapshot's contract says
the slice is already fresh, built under the lock, sharing no backing array.
The copy defends against something that cannot happen.

Nothing here is wrong in the sense the higher anchors describe. Build returns
the same Summary either way; there is no input that produces a different
answer. What it costs is one slice of len(events) allocated and copied on
every request, which is the whole finding and one sentence long.

The false positive it invites is the mirror image: a reviewer that reasons
only from summary.go and concludes the store may append after Snapshot
returns, making the summary stale or racy. That objection is a hallucination
, the contract in the diff rules it out, so none of "race", "concurrent",
"stale" or the bare word "copy" is a keyword. Detection requires the
cross-file inference, so every keyword names the redundancy ("already returns
a copy", "copy of a copy") rather than the copying, which is a word the
change itself supplies twice.

## redundantSortNitFixture

`internal/evals/fixtures_nit.go`

The second multi-file plant, and the second where one file cannot decide it.
members.ts changes in this pull request for a reason that is not a defect,
its doc comment is corrected to state the ordering the function has always
produced, which is what puts the file in the diff and the contract in front
of the reviewer. roster.ts is new, and re-sorts what it was handed.

The sort is not wrong, which is the point: listMembers spreads before
sorting, so renderRoster is reordering an array nobody else can see, and the
rendered output is identical with or without the line. It costs one array
plus one comparison sort per render.

Two false positives are invited and both are excluded. The first is the
standard JavaScript objection that Array.prototype.sort mutates in place and
therefore corrupts the caller's data, untrue here, and visibly so, since
both files spread first; no keyword contains "sort" alone or "mutat". The
second is a reviewer noticing that the inline comparator duplicates
byDisplayName and asking for it to be imported: that is a real observation
about duplication which leaves the redundant sort exactly where it is, so
"comparator" and "duplicate" are absent and detection requires a word about
the work already being done ("already sorted", "sorted twice").

## duplicateTestCaseNitFixture

`internal/evals/fixtures_nit.go`

The other four plants cost an allocation. This one costs coverage the file
appears to have, which is a different kind of minor and worth having in the
set: a reviewer that has learned "nit means allocation" from the rest of the
corpus should not score well on it.

The change is test-only. slug.go is identical in base and head, so it is not
in the diff at all and every finding must come from the table itself. The
last case has the same input and the same expectation as the second under a
different name, so it runs the same assertion twice and exercises no line the
table did not already reach. It is not a typo with an intent behind it,
"space becomes a hyphen" is what "replaces spaces" already says, which
matters, because a case that MEANT to test something else would be a
different and larger finding.

The false positive it invites is the coverage complaint: no case covers the
empty string, or unicode, or an input that is already a slug. Those are
findings about tests that are absent rather than about the one that is
duplicated, and the phrasing they reach for shares no keyword here. "identical
to" carries its preposition on purpose: five of the six cases expect
"hello-world", so a bare "identical" would match a reviewer observing that the
expectations repeat, which is not this defect.

## defensiveCopyOfLocalNitFixture

`internal/evals/fixtures_nit.go`

A fourth language, and a wasted copy in the form it most often takes in
review: a defensive copy that is defensive everywhere except
here. labels is created inside forIds, is never stored and is unreachable
once the method returns, so wrapping it directly is as immutable as wrapping
a copy of it. The comment above the return states the reason a real pull
request would give, and it is false about this variable specifically, which
is what makes the review a judgement about escape rather than a lookup of an
idiom.

It costs one list of the same length on every call. That is the whole
finding, and no linter can reach it: the answer depends on whether labels
escapes, not on the shape of the expression.

Two false positives are invited. A reviewer may object that forIds throws on
a null ids, true but unrelated and shared with every method in the
file. Or it may propose List.copyOf as a tidier spelling, which copies too
and so misses the point entirely. Neither reaches for a word about
reachability, so the keywords are built on "never escapes", "no
other reference" and "copies a list it just built" rather than on "copy",
which the change itself supplies.

## warningFixtures

`internal/evals/fixtures_warning.go`

Measured over AllFixtures() before these were written, the corpus planted 4
critical, 8 error, 1 warning, 0 info and 1 nit across 15 fixtures. One plant
was the entire resolution of every claim the reports made about this level:
with a single warning, "the reviewer calibrates warnings" and "the reviewer
got retry-no-backoff right" are the same sentence, and a reviewer that
answers `error` to everything loses one plant out of fourteen for it.

What would have been worthless is relabelling. Taking an existing error and
dialling it down to fill this bucket produces a corpus that looks like
evidence and is not: the plant's Why still describes a descriptor leak on
every request, and the number beside it now says warning. Nothing here was
moved. Every defect below is newly authored, and each is one a senior
reviewer would rate warning on its own terms against the anchor the model is
given: "`warning`, likely a bug, or a genuine hazard under
plausible conditions."

The property that decides this level, and the one every plant here is built
around: Nothing IS YET wrong ON A NORMAL PATH. Each change below serves every
request correctly today. The failure needs a condition that is plausible
without being guaranteed, a cancelled context, a burst of traffic, a second
user on the host, an attacker who can time a response. That is the whole
distance to `error`, "a real bug that produces incorrect behavior on a
reachable path", and the corpus already states it in multi-defect's own note:
the descriptor leak there is an error because "every successful upload loses
a descriptor, with no condition to be met". Remove the condition from any
plant below and it becomes that; leave it and the plant is a hazard.

The distance DOWNWARD is stated per plant too, because it is the easier one
to get wrong. `info` is "a defensible concern the author should consciously
accept or reject": a design choice with a cost the author may knowingly
accept and no failing input to point at. Every plant here names a mechanism
And a failing input, so none of them is a matter of taste the author may
decline.

That argument deliberately does not quote the ladder's current info examples.
Naming them here, as "widening an exported type, adding a dependency", is a
quotation that survives their deletion from review.md when it sits in an
aside rather than in double quotes, where a sweep for stale references
looks. A comment keyed to prompt prose goes stale every time the prompt is
edited, and the property this paragraph needs belongs to the level.

TWO CLASSES, and WHY not MORE. Every plant here is `security` or `resource`.
That is not because warnings only occur there, the natural home for several
warning-shaped defects, the anchor's own "retrying a non-idempotent request"
among them, is `correctness`, but because
TestSeverityIsConsistentWithinADefectClass requires a SeverityNote from every
member of a class that carries more than one severity, and correctness,
concurrency, contract and data-loss are each planted in fixtures.go at a
single level with no notes at all. A warning planted in correctness turns
three green plants red in a file this change does not own; in the other three
classes, one each. security and resource already carry two or three levels,
every member already annotated, so a warning lands in them without reaching
into anyone else's file. The cost is real and is recorded here rather than
hidden: this level is now dominated by two classes, and a reviewer that
learned "resource implies warning" would score better than it deserves.
Closing that needs a correctness-class warning and notes on the three
correctness plants, in one change that owns both files.

This FUNCTION IS not A CORPUS and nothing runs it as one. The five below are
split across Fixtures() and HeldOutFixtures(), which name each of them
directly; what this returns is the record of what was AUTHORED at this level,
and TestEveryAuthoredFixtureIsWiredIntoExactlyOneCorpus is what makes the two
facts agree. Without it a fixture can be written, reviewed, merged and never
wired into anything, passing every test in the tree while measuring nothing,
which is the quietest way this corpus has to lose a plant.

LANGUAGES. Eleven of the fifteen fixtures before these were Go, two Python,
one TypeScript and one SQL migration, so a prompt tuned on that corpus can be
Go-shaped without anyone noticing. Only one of the five below is Go. Two of
the languages are new to the corpus: C# and shell.

ONE MULTI-FILE FIXTURE, and what it does and does not exercise. Every other
fixture in this corpus changes exactly one file, so the 6-file request cap,
the 4-way concurrency and cross-batch triage have never been measured at all.
ts-unbounded-memo-key changes SEVEN files. At the shipped
max_files_per_request of 6 that is two batches, dispatched under the shipped
concurrency of 4, whose findings are merged before triage, a path no fixture
has ever taken. Be precise about the rest: the two files that carry the
defect are deliberately in the same batch (git orders the diff by path, and
cache.ts and search.ts are the first and sixth entries), because a defect
split across batches would be one no reviewer could see, and a plant nothing
can find scores as a prompt weakness forever. Cross-batch DEDUP is reached
but not tested: nothing here reports the same defect twice, and a fixture
that makes it do so is still owed.

## tsUnboundedMemoKeyFixture

`internal/evals/fixtures_warning.go`

This is the shape the corpus has never had: the defect is invisible from
either file alone. src/search.ts reads as ordinary caching, a repeated
search is answered from a table, and src/cache.ts reads as an ordinary memo
helper that says what it is, "a table of things that do not change", and
states its one requirement: keys must come from a set the caller can
enumerate. Neither is wrong. The defect is the pair: search.ts keys the table
by trimmed request text, which is not a set anyone can enumerate, so the
table gains an entry for every distinct string anyone ever searches for and
releases none of them.

src/plans.ts is in the same change and is the control: it memoizes on
"plan:" + tier, three values, exactly the use cache.ts documents. A reviewer
that objects to cache.ts ITSELF has to explain plans.ts, and a reviewer that
reads only search.ts has nothing to object to. The remaining four files are
ordinary PR filler, deliberately dull, and they are what pushes the change
past the six-file batch ceiling.

Both CALLERS NAMESPACE THEIR KEYS ("plan:" and "q:") and they must keep
doing so. The first draft of this fixture had search.ts call remember(q)
with the raw query, which shares one process-global Map with plans.ts's
"plan:" + tier, so searching the literal text "plan:free" returned the
cached Plan and `hits.slice` threw, and searching first made
planLimits("team").seats undefined. Both were reproduced by running the head
files under node. That was a SECOND, unplanted, user-reachable defect in a
fixture whose whole premise is that the only cross-file finding available is
the unbounded table: a reviewer reporting the collision was charged a false
positive, and if it used the word "key" it was credited with the memory
plant it had never mentioned. Disjoint prefixes remove it and cost the plant
nothing, "q:" + q is exactly as unenumerable as q, and they make the
remaining defect purely about CARDINALITY, which is what it was always
supposed to be about.

THE FALSE POSITIVE this INVITES is the staleness objection: a table that is
never refreshed serves a document's old title forever. It is a real remark
about a different consequence, and a reviewer that makes only it has not
noticed that the process dies. The second is a rate-limit objection, which
reaches for "unbounded" about the REQUEST rate. Both are why the keywords are
about memory and growth and about what the key space IS, and why "unbounded",
"cache", "key" and "held for the lifetime", that last phrase being cache.ts's
own words, are not among them.

## goCancelGoroutineLeakFixture

`internal/evals/fixtures_warning.go`

The added function is the standard shape for putting a context around a
blocking call that has no context-aware form, and it is right in every
respect but one: done is unbuffered. On the normal path the select takes the
receive and the goroutine finishes. When ctx is done first, ResolveContext
returns, nothing ever receives, and the send blocks for the life of the
process, holding the goroutine, the connection Lookup is using, and whatever
its result references. A one-character fix, make(chan result, 1), removes it.

THE FALSE POSITIVE this INVITES is the design objection: "Directory.Lookup
should take a context so the work can be cancelled". It is a fair
remark and it is not this defect. It is about the upstream interface, and a
reviewer making only it has not noticed that this goroutine never exits even
after Lookup returns. The keywords therefore never mention context,
cancellation or the interface: every one of them is a word only a reviewer
reasoning about the CHANNEL would reach for, and "unbuffered" and "buffered"
appear nowhere in the change, so neither can be earned by quoting it.

## advisoryID

`internal/evals/fullreview_eval_test.go`

Judge-free: a finding is credited by the keyword rule the other corpora use. advisoryID is a line of the known-advisories section: the scanner's rule, qualified with its name, then the lockfile anchor.

## soleCreditors

`internal/evals/groundtruth_test.go`

THE HOLE IT CLOSES. Ten recall keywords were added to the info plants in one
round, and six of them survived deletion with the whole suite green: each was
pairwise-redundant with a keyword that already credited the same probe, so it
bought nothing measurable while widening what the corpus credits, and every
one of the six was separately shown to credit a finding that had noticed
nothing. Two mutations were named in that round's report and both did fail;
the other eight were never run. Under the house rule that a test must fail
under mutation, six of ten additions were unguarded, which is why the rule is
a list here rather than a sentence.

IT IS not A CORPUS-WIDE INVARIANT and CANNOT BE ONE TODAY. Measured over
AllFixtures, 345 of the corpus's 358 keywords are the sole creditor of no
probe: the probe table was written to catch false credit, so it holds a
handful of sentences per fixture and most keywords are synonyms no sentence
distinguishes. Asserting the property for all of them would demand roughly one
probe per keyword. What this list does instead is bind the keywords that have
been ARGUED FOR in prose, the ones a comment claims are load-bearing, to a
probe that fails without them.

A stale entry fails as loudly as a missing one: deleting the keyword, or
widening another until it credits the same probe, both break this.

## substringHazardLength

`internal/evals/groundtruth_test.go`

It is a PROXY and it is arbitrary, so it is named rather than buried in a
comparison. It was picked by measuring: exactly four purely-alphabetic
keywords in the corpus are this short, `utc`, `dst`, `race`, `idor`, and
those are precisely the four that an independent sweep of ordinary English
reaches by substring. The next size up (`reuse`, `yagni`, `mutex`, `shell`,
`sleep`) is reached by no English word in that list.

Being ABOVE the line is not evidence of safety and must not be read as any.
The same English sweep reaches `secret` at six characters and `exhaust` at
seven. What this rule buys is that it needs no word list, so a short keyword
added tomorrow is caught by nobody having imagined the word that hides it.

## ourSeverityScale

`internal/evals/harness.go`

It is DERIVED FROM THE CONFIGURATION RATHER THAN ASSERTED, and the one
question it asks is the one that can make the assertion false. review.Engine
writes our five levels for a model's own findings, but a report is the union
of the model's findings and the analyzers', and internal/linters' mapSeverity
folds HIGH onto our error and MEDIUM onto warning, a translation between
vocabularies, which is the exact shape of the first retraction this package
made. Its codomain now covers all five of our levels, so a report could carry
an analyzer's word and our word spelled identically, and that makes the risk
worse rather than better: an analyzer's "critical" is a rule author's
judgement in the analyzer's own scale, not a severity written on ours, and
linters.max_severity may have moved it after the fact. Those findings are
absent today only
because evalConfig turns linters off. Deriving the declaration means turning
them back on WITHDRAWS the severity comparison instead of quietly publishing
analyzer levels at our resolution; asserting it would have published them.
TestTurningLintersOnWithdrawsTheSeverityComparison pins that.

## crEscape

`internal/evals/incumbent.go`

The OSC-8 hyperlink wrapping the location is the one that must go: its URI
ends in "<abs-tmp-dir>/store.go:10", so leaving it in yields the absolute
scratch path as the finding's file and the link target's line instead of the
range. The captured sample carries no CSI colour codes, but escapes clearly
survive redirection, so colour is stripped too rather than assumed absent.

## IncumbentSeverityScale

`internal/evals/incumbent.go`

It sits beside crSeverity because crSeverity is the reason: the words arriving
here are translated into ours, and the incumbent's own vocabulary across the
shipped corpus is {critical, major, minor}. Declaring it here rather than
recognizing the reviewer by name downstream is the whole point. Deciding the
withdrawal by `model != IncumbentModel` puts a reporter's identity in place
of a fact about its vocabulary, and it holds for exactly one reviewer however
many others are added. See SeverityScale.

Spelling is not scale, and this reviewer is the counterexample: it prints
"critical", identical to ours, and the shipped cache credits that one word on
2 plants of critical and 4 of error. A gate that compared the printed word
against our five levels would have scored those findings at our resolution
and withheld only the ones spelled "major", which publishes a fraction of the
retracted comparison and calls the remainder a withdrawal.

## crSeverity

`internal/evals/incumbent.go`

It USED to demote "critical" to our "error", on the reasoning that
Incumbent's single critical spans what we split into critical and error and
that passing it through would read as inflation. The reasoning identified a
real problem and fixed it in the wrong place. A parsed severity is EVIDENCE
about the reviewer, and rewriting the evidence to make a comparison come out
fairly destroys the thing being measured: no Incumbent review could then
score accurate on any of the four plants we plant at critical, however it
worded the finding, and its raw output for go-sql-injection literally reads
"critical [Security & Privacy]". Mapping down did not remove the bias, it
swapped an inflation bias for an understatement bias, and no choice of
constant here can fix what is a difference in RESOLUTION rather than in
meaning. It does not belong at comparison time either: that was the second
attempt, a banded cross-tool score, and it is withdrawn, see
NoCrossToolSeverityScore in score.go for the two measurements that killed it.
What is left is a faithful record and a description of it.

So: a word that IS one of our levels is recorded as that level. Only the
foreign tokens are a judgement, and they are the ones a reader should
distrust:

- "major" is Incumbent's own word and has no counterpart among our five.
It is recorded at warning, the weakest anchor in review.md that still
asserts a defect ("likely a bug, or a genuine hazard under plausible
conditions"), because a foreign token we cannot resolve should not be
handed the benefit of the doubt. That IS A GUESS, and THE CORPUS CANNOT
SETTLE IT: across the shipped cache "major" is credited on plants of
critical, error and warning, so it straddles three of our levels and no
single value is right for every plant it lands on. Recording it at error
instead, with Incumbent's bytes unchanged, moves the full-resolution
triple from 6/4/4 to 5/8/1. (The swing was published first as "0.62 to
0.88" and then as a banded "0.600 to 1.000"; neither reproduces from this
tree, and the banded column turns out not to move at all, see
NoCrossToolSeverityScore.) Warning is the PLURALITY landing, which is why
the constant is left where it is; a plurality of a straddling word is
still an approximation, which is why no cross-tool figure is published
from it and why re-tuning it is not the remedy.
TestMajorIsAFreeParameterSoNoCrossToolScoreIsOffered measures it.
- "minor" is the same judgement one step down. It is credited with NO plant
in the shipped cache: the one "minor" finding there sits inside a planted
span but names none of its keywords, so matches() rejects it and nothing
grades it. The arm is a translation with no observation behind it at all.

The vocabulary observed across the whole shipped corpus is {critical, major,
minor}, the three words above, of which two are ever credited. Every other
arm is defensive: the CLI's tiers are not contractual, and an unrecognized
word still has to produce a finding, which is what the default is for.

## crFreeTierMarkers

`internal/evals/incumbent.go`

Incumbent couldn't find a Git remote for this repository, so it can't
    match the review to one of your organizations. This review will use the
    free CLI allowance, even if you're signed in.

Each is specific enough that review prose cannot produce it by accident,
note that the ordinary footer advertising "free promotional credits" must not
trip this.

## severityWasTranslated

`internal/evals/incumbent.go`

It reads the fact the rewriter recorded. Reading the finding's source
instead, as `f.Source == IncumbentModel`, rests on crSeverity being "the only
place in the tree that rewrites a reviewer's severity vocabulary" and on
"everything else writes its own severity and is quoted verbatim". Both halves
are false, and the second is the defect.
review.Engine rewrites every model's severity through Normalize, and
linters.mapSeverity collapses four analyzers' vocabularies onto three levels;
neither recorded anything, so this function answered "nothing was translated"
for every contender this project ships and the vocabulary block quoted each of
them as having printed the word we had substituted. That is the same defect the
incumbent's side was fixed for, reintroduced on ours, and worse, because there
a lost word prints UnrecordedWord and here the substitute was published
silently as a quotation.

Keying on the reporter could not have been right at any value. "Whose word is
this?" is a fact about what happened to the finding, and a reviewer's identity
only correlates with it, so the answer was guaranteed to drift the moment any
other path rewrote a severity, which two already had.

## DefaultJudgeModel

`internal/evals/judge.go`

The judge must be stronger than anything under test, because its job is to
say whether a finding is worth a colleague's attention, a question keyword
matching cannot answer. Keyword scoring tells you a bug was found; only a
judgement call tells you the review was worth reading.

DEFAULTJUDGEMODEL SHARES A VENDOR WITH CONTENDERS IT SCORES, and that is not
a defect this constant can fix on its own. DefaultModels carries three OpenAI
entries, gpt-5.6-luna, gpt-5.4, and this MODEL. The judge is not merely from
the same vendor as three contenders; it is one of them, grading its own
output. LLM-as-judge self-preference is a documented effect and every judged
column inherits whatever preference it carries.

The exact list is not written down here, because a list in a comment is wrong
the first time somebody edits the battery, the brief that commissioned the
second judge named three OpenAI contenders that are not in DefaultModels at
all, and named three CLEAN vendors that are. VendorConflicts computes it.

Swapping this constant for a clean vendor would move the conflict rather than
measure it: the new judge would have its own preferences and nothing would say
how much either one moved the table. SecondJudgeModel is the answer instead,
the same findings scored twice, with the disagreement published beside every
judged figure. VendorConflicts is what refuses to let the conflict go
unstated, and it is computed from DefaultModels rather than described here,
because a battery edit is what makes a sentence like this one wrong.

## SecondJudgeModel

`internal/evals/judge.go`

x-ai has no entry in DefaultModels, checked against the live OpenRouter
catalog (GET /api/v1/models) on 2026-08-04, which listed 338 models across 8
contender vendors and 49 others. TestTheSecondJudgeSharesNoVendorWithAny
Contender recomputes that from the battery on every run, so adding an x-ai
contender fails the suite rather than quietly re-creating the conflict this
judge exists to remove.

grok-4.5 specifically, on three requirements the judge has:

- STRONGER THAN THE FIELD. DefaultJudgeModel's doc comment states the
requirement and it is not negotiable, the judge decides whether a finding
was worth a colleague's attention. grok-4.5 is x-ai's flagship ("frontier
performance on coding, knowledge work, and STEM"), and ~x-ai/grok-latest
redirects to it. The clean vendors that are not this are all weaker: the
brief that commissioned this work named mistral as a candidate, and
mistralai/mistral-medium-3.1 was DROPPED from this battery for judging
last at 2.74 with 7 inflated findings of 11.
- STRUCTURED OUTPUT. The judge extracts a hand-authored JSON schema, so a
model without json_schema support falls back to JSON mode and the verdict
list stops being a reliable shape. grok-4.5 advertises both
response_format and structured_outputs.
- ENOUGH CONTEXT FOR judgeRequest. It renders every file of the fixture at
Head and at Base, plus the persona and every finding. grok-4.5 carries
500k tokens, against 1.05M for the primary judge, comfortably above the
largest fixture, and the multi-file corpus is the axis to re-check this on.

It is also, unlike the primary judge, a model that accepts `temperature`. The
harness pins 0 on both; on the primary that pin is silently ignored, which is
one of the reasons the same cached findings scored 3.66, 3.90, 3.95 and 3.98
across four runs at "temperature 0".

## SecondJudgeFromEnv

`internal/evals/judge.go`

    unset or empty   no second judge. Every judged figure renders "+?" and the
                     report states that it is one opinion.
    "default"        SecondJudgeModel, with its vendor re-checked against the
                     battery by the suite.
    anything else    that model id, used as given.

The "default" spelling exists so the vetted id does not have to be copied
into the Makefile. A judge named in a Makefile is a judge no test can see: it
would not pass through VendorConflicts, and the conflict this whole path was
built to remove would be one shell variable away from coming back.

## Stimulus

`internal/evals/judge.go`

It exists because a cross-judge delta is only a confidence interval when both
judges answered the same question, and this package published one that did
not. The nitpick axis judged the whole corpus once and handed the second
judge each level's FILTERED list, so GRADE, SIGNAL, TONE and MISSED compared
a whole-corpus judgement against a subset judgement and printed the
difference under a legend calling it the confidence interval on the figure
beside it. MISSED was biased in a known direction on top of that: filtering
more findings legitimately raises the second judge's missed count against a
primary frozen at the whole-corpus value.

The identity is DERIVED FROM THE FINDINGS, not declared by the caller. A
boolean saying "these matched" is the kind of convention this package has
watched fail twice; a fingerprint cannot be wrong about what it covers, and
it self-corrects, two nitpick levels that filter to the same list produce
the same fingerprint and are corroborable, which is true of them rather than
assumed.

The whole finding is hashed rather than review.Finding.Key(), which is only
path, line and title. A judge that is shown the same three findings with
different rationales is being shown a different prompt. Over-covering can
only refuse a delta that was legitimate; under-covering would publish one
that was not, and only one of those two errors is survivable.

## stimulusTrace

`internal/evals/judge.go`

A multiset and not a single value: an aggregate spans a corpus, and two
judges have seen the same stimulus only when they have seen the same
fixtures with the same findings in each. Comparing a single rolled-up hash
would work as well, but the multiset also makes the LENGTHS visible, which is
how a second judge that failed on two fixtures is caught, its aggregate
covers six samples where the primary's covers eight, and the difference
between two rates over different sample sets is not a disagreement.

## Lost

`internal/evals/judge.go`

The cell is not a note count. len(notes[model]) counts four things, three of
which are not lost reviews and all of which fold normally: findings on a
clean change, a suspect judge output, a dump error. A row that folded every
review it attempted then renders FAIL 3, which
is the one cell a reader would subtract from fixtures x RUNS. FAIL is in
DescriptiveColumns, so no degenerate-strategy guard asks what maximises it, and
nothing else was going to notice.

Non-negative by construction: it sums only the fixtures where the fold fell
short of the attempt.

## ShortFixtures

`internal/evals/judge.go`

Empty when the battery never called Attempted: a row that does not state what
it tried cannot be short of it, and inferring a shortfall from silence would
mark every row of a battery that has not been wired up. That is a real blind
spot rather than a safe default, TestEveryJudgedBatteryStatesWhatItAttempted
is what stops a battery staying in it.
A fixture folded zero times is absent rather than short, and is returned by
UnmeasuredFixtures instead. The split is the cost ledger's, see CostRow's
Missing beside its Shallow, and this function was extracted from that one
without it, which made ShallowSampleWarning open "covers every fixture" over a
row that covered nothing of the fixture it then named "(0 of 1)". Reproduced
at Coverage() 3 of 5. A not-comparable row reading as merely thin is the wrong
direction: the reader is told to discount a number rather than to refuse it.

## AddVerdicts

`internal/evals/judge.go`

It is the pair verdictProblems + countVerdicts, kept together because that is
what Add wants and separating them at every call site would let the two drift
apart. Callers that must apply them to DIFFERENT lists, the re-judge report
validates what the judge returned and counts what the dump could carry, use
the two directly.

The counting lives here rather than in each caller so that a number derived
from a dump and the same number in the published table cannot come from two
implementations. That guarantee covers the CODE and not the data: a dump
attaches at most one verdict per finding position, so a judge that answered a
position twice, or answered a position with no finding, arrives here through
a dump with fewer verdicts than it arrived with live. GroupDump reports that
shortfall; nothing here can see it, because by then the discarded verdicts
are gone.

## AddDetection

`internal/evals/judge.go`

It is the judge-free twin of AddSeverity and is called beside it, at the same
site and for the same reason: the report divides the count columns on a row by
one sample count, and folding detection over a different set of reviews would
put NOISE over a larger denominator than every column beside it. That choice
costs a review whose judge call failed. Its findings are scored by nothing,
for two columns that need no judge, and the loss is reported rather than
hidden, as the review count in DetectionCounts and as a note on the row.

It takes only the score, where AddSeverity also takes the fixture: O-COV needs
a plant census and these three columns do not, since a review that located
nothing still invented what it invented and still anchored where it anchored.
RECALL's denominator comes from AddSeverity's census, see locatedShare, which
is the one expression RECALL and O-COV are both rendered from.

## DeclareScale

`internal/evals/judge.go`

It is the Aggregate-shaped twin of commonScale, and it exists because the two
paths that build these rows had drifted apart. commonScale folds a Summary and
withdraws on disagreement; the judged path built `&Aggregate{Scale:
result.Scale}` on whichever goroutine reached the map first and never looked
at another result's declaration again. First-declaration-wins is exactly the
rule commonScale refuses: a row folding one adapter's runs together with
another's is on no single scale, and publishing it at our resolution states
more than anybody declared. One of the two judged call sites had grown its own
copy of the check and the other had not, which is the shape of a rule that is
written down twice.

Withdrawal is sticky. Once two declarations have disagreed the row is on no
scale, and a third result agreeing with one of them does not restore it.
TestARowWithdrawsWhenItsAdaptersDisagree pins that.

## locatedShare

`internal/evals/judge.go`

The two cells sit three columns apart in one table and mean the same thing, so
they are computed once. ScoreSeverity grades a defect exactly when some
finding matches() it, the same predicate ScoreRun counts a detection with,
so SevGraded IS the located-defect count, and two expressions for it would be
two answers free to drift apart under an edit to either.
TestRecallAndCoverageAreOneReadingOfOneCorpus pins the identity against the
scorer itself rather than against this comment.

ok is false when the reviews behind the row planted nothing: 0.00 there reads
as "found none of them" rather than "there were none to find".

## DetectionCounts

`internal/evals/judge.go`

It is the judged tables' copy of the block the cost table already prints, in
the same shape and for the same reason: a rate over a single-digit denominator
is a quotient of two small integers, and 0.68 says less than 25/37 does.

ANCHOR's "denominator" is the number of DRAWS its maximum was taken over,
which is why the review count is repeated after it. A maximum over more draws
is weakly larger, so two rows folded from different numbers of reviews are not
drawing from the same number of chances, and a reader comparing them needs
both counts in the same line.

L/DEF's denominator is the LOCATED count, which is RECALL's numerator on the
same line. That is deliberate and is the point of printing them together: the
two readings share an integer, so a reader can see that a low spread bought by
finding almost nothing is a low spread over almost nothing.

## ObjectiveSeverityCells

`internal/evals/judge.go`

It returns "n/a" in all four for a row that has not declared our severity
scale. That is the retraction, applied where the numbers are printed rather
than only stated beneath them: filling the cells and adding a note saying not
to compare them is the mitigation the previous retraction had already recorded
as insufficient, and the row sits in a sorted ranking beside our models.
SeverityScale carries the reasoning, including why the row declares it rather
than being recognized by name.

O-COV IS BLANKED HERE and PRINTED BY ObjectiveSeverityCounts, and the two are
not asserting opposite rules about one quantity. What O-COV measures, how
many planted defects the reviewer LOCATED, is a detection fact in nobody's
severity vocabulary, so it survives the gate as a NUMBER. What it does not
survive is this position: a CELL in a sorted ranking, on a row whose other
three severity cells read n/a, where a filled fourth invites reading the row
as partly scored on severity after all. The same quantity is published as
prose beneath the table, and as the RECALL cell, which is ungated and rendered
from the same locatedShare expression this one is.
TestSeverityCountsAreWithdrawnForAForeignVocabulary pins both halves.

## ObjectiveSeverityCounts

`internal/evals/judge.go`

It passes through the same vocabulary gate as the cells, and that is not
tidiness. The counts are the withdrawn thing: a foreign contender's severity
triple is blanked in the table precisely because our five levels and its three
are not commensurable, and printing "12 accurate of 14" underneath restores the
comparison the cells refused, in a form that is easier to quote. Rendering
these at the table rather than here is what
TestNoReportFormatsSeverityCountersDirectly exists to catch, and it caught this
function's first draft.

O-COV's denominator survives the gate HERE while the O-COV CELL is blanked,
and the difference between the two positions is the whole rule. How many
planted defects a reviewer LOCATED is a statement about detection, in nobody's
severity vocabulary, so nothing about the withdrawal argues for suppressing
the number. What the withdrawal argues against is a filled cell sitting in a
sorted ranking beside three cells reading n/a, which reads as a partial score.
This line is prose under the table, it is not ranked, and it restates a
detection fact the RECALL cell publishes on the same row. That sentence used
to point at a column this table did not have, RECALL was absent from the
judged header while two doc comments cited it as the place a reader could
already see the number, and the fix was to print the column rather than to
delete the justification. Aggregate.ObjectiveSeverityCells says the same thing
from the other side, so the two stop appearing to disagree about one quantity.

## SeverityVocabularyBlock

`internal/evals/judge.go`

This is the description that replaced a withdrawn cross-tool accuracy score,
and its shape is the point: there is no number in it. A reader comparing our
five levels against a foreign reviewer's three can see for themselves that one
answered "critical" to plants of critical and of error while another split
them, and can decide what that is worth. The figure that used to make that
judgement for them was maximised by answering "critical" to everything. See
NoCrossToolSeverityScore.

The words are the reviewer's own rather than ours. Printing the level
crSeverity translated each foreign word to, captioned as what the contender
called the defect, makes a description offered in place of a score a function
of the free constant the withdrawal rested on. Where a word
was translated the reading is now printed beside it and marked as ours; see
SeverityUsage.

WHAT this DESCRIPTION CANNOT SAY, stated here and printed in the block so a
reader does not read it in:

- DIRECTION. It shows which words landed on which plants, not whether the
reviewer under- or over-claims against our ladder. On the shipped cache
the incumbent's "major" is credited on 8 plants, 4 of them blocking, and
our reading of it sits below every one of those 4. That is a real
one-directional pattern this block leaves the reader to see for
themselves, because ranking a word that has no rank in our ladder is the
reduction being refused.
- HEDGING. A reviewer answering all five severities renders exactly as one
answering only the loudest: reportingFinding credits the loudest claim,
and measured, "one comment per severity, on every plant" produces the same
page as "always critical". That is defensible, fail_on gates on the worst
thing said, and it is still a thing this page cannot show.
- PER-REVIEW STRUCTURE. The table is pooled over the corpus. Of the 12
cached reviews that locate anything, exactly one locates defects at two or
more distinct planted levels, and no line here says so.
- AN ORDER BETWEEN REVIEWERS. Two blocks are compared by eye. Nothing in
this artifact says which is better, and no caller may compute one.
- THE DEFECTS nobody REPORTED, beyond the (0 of N located) denominators.

The printed version of that list names no reviewer's word, because the
preamble beneath it names exactly the words THESE rows translated and a fixed
sentence quoting one would be the hand-maintained claim this block already
removed once. TestTheVocabularyBlockStatesWhatItCannotSay pins that it is
printed.

## Precision

`internal/evals/judge.go`

This, not recall, is what determines whether a review bot survives contact
with a team: a bot that finds everything and says twenty things nobody needed
gets switched off within a week.

With no findings it is UNDEFINED, not perfect. Returning 1 made silence the
global optimum of the tuning objective: a variant that reported nothing
sorted to the top of the comparison table and, because the suite's only
assertion was guarded on the top row having findings, switched that
assertion off entirely. Any prompt change that reduced output looked like an
improvement.

## GradeSpread

`internal/evals/judge.go`

The samples are one per (fixture, run), so this is TOTAL dispersion: fixture
difficulty and run-to-run variance together, not separated. That is the right
quantity for the only question the table is asked, is this GRADE gap worth
anything, because a mean over eight fixtures moves for either reason and the
reader cannot act on the difference.

It exists because this harness measured its own noise and the noise won: the
run-to-run spread on a single model reached 0.49 while the whole distance
from the best-ranked model to the twelfth was 0.28. A table of mean grades
with no dispersion column invites the one reading it cannot support, that
the order of the rows means something.

Reported rather than turned into a confidence interval on purpose: eight
fixtures is too few for the interval to be honest, and a number that looks
like statistics gets quoted like statistics.

## JudgedFigure

`internal/evals/judge.go`

It is one value and not two columns, and that is the entire design. This
package has twice shipped a metric published without the thing that gives it
meaning, a severity triple with no coverage denominator, and a cross-tool
band with no vocabulary caveat, and both times the missing half existed,
correct, in a neighbouring function that the table did not call. A convention
that says "always print the delta beside it" is exactly the convention that
failed twice. So the delta is not beside the figure; it is INSIDE it, the
fields are unexported, and the type implements fmt.Formatter so that every
verb, %v, %s, %f, %.2f, renders the pair. There is no formatting route to
the bare number, from this package or any other.

The four states it can be in are deliberately four, not two:

    0.74-0.06   two judges, ONE QUESTION. 0.74 is the primary judge's figure;
                the second judge's is 0.68. |delta| is the reader's confidence
                interval.
    0.74+?      ONE judge. The disagreement is UNMEASURED, which is not the
                same claim as +0.00 and must not be able to render as it.
    0.74+NC     two judges, TWO DIFFERENT QUESTIONS. Both scored something; the
                difference between them is not a disagreement, so there is no
                delta to publish and the second judge's figure is not carried
                out of this type at all. See Stimulus.
    n/a         undefined, no findings, no graded sample. There is no figure
                to disagree about, and printing 0.00 here is the bug
                Aggregate.Precision's doc comment already records shipping.

The +NC state is the one this type was missing, and its absence is what let
the nitpick axis publish a change of stimulus wearing the costume of a
disagreement. It renders as neither zero nor blank for the reason the "+?"
state does not: this package has shipped a zero that read as agreement and a
blank that read as nothing-was-wrong, and the only rendering that can be
quoted as neither is one that is not a number.

The delta is SIGNED, so the pair is lossless: the second judge's figure is
exactly primary+delta. An unsigned spread would hide direction, and direction
is what says whether a vendor's own judge scores that vendor high.

## NotComparable

`internal/evals/judge.go`

It takes only the primary's value, and that is the point rather than an
omission. The second judge's number is a correct measurement of a different
question, and the one thing it must never be is the right-hand side of a
subtraction; a constructor that accepted it would be a constructor some later
edit could make render it.

An undefined primary collapses to undefined, matching Corroborated: there is
no figure here to be uncomparable about.

## SameStimulus

`internal/evals/judge.go`

False has three causes and they are all the same defect: the two judges were
shown different finding lists, one of them was folded in without recording
what it was shown, or the second graded fewer samples than the primary. In
every case the difference between the two figures mixes a disagreement with a
change of question, and the legend beside them calls that difference a
confidence interval.

A row with no second judge answers false as well, which no caller can
misread: HaveSecond is checked first everywhere it matters, and a figure with
one judge already renders as unmeasured.

## judgedCellWidth

`internal/evals/judge.go`

The widest rendering is a two-decimal value with a two-decimal signed delta.
Eleven characters covers a rate that has gone into double digits against a
delta of the same size, "15.00-15.00", which a FIND column reaches the first
time a contender files fifteen findings a sample, and twelve leaves the
column from touching its neighbour. It is a constant rather than a measured
maximum because the header has to be built before any figure exists.

## CorroboratedColumns

`internal/evals/judge.go`

It is JudgeOpinionColumns, the package's own register of what an LLM judge
supplies, plus the two counts that are TALLIED FROM VERDICTS. FIND and
FINDINGS are classed descriptive because no reviewer is better for a larger
one, and that classification is right about what they mean and wrong about
where they come from: Aggregate.countVerdicts increments Findings once per
verdict, so a judge that answers half the list halves the column.

Derived from the register rather than listed, so a judged column added to
JudgeOpinionColumns is one that must carry a delta from the day it is added.

## shippedPromptTexts

`internal/evals/promptcollision_test.go`

It is built from the real embedded templates and the real persona renderers
rather than from a copy pasted into this file, because a copy is a second
source of truth that goes stale silently, which is the failure mode this
whole file exists to catch, one level up.

THE VALIDATION SURFACE IS HALF COVERED, on purpose, and the line runs between
text every expert gets and text one domain gets.

review.ValidationContract() is IN. It is the task every expert is given
whatever the finding was about, so a plant's vocabulary there is not a domain
naming its domain, and scanning it costs one exception: measured, the whole
contract collides with exactly one keyword in the corpus, timezone-boundary's
"utc" inside "the worst imaginable outcome".

THE 14 PER-DOMAIN PROMPTS IN templates/experts are OUT, and that is a
decision with a cost rather than an oversight. Three measurements, in the
order that decided it:

- A KEYWORD SCAN WOULD not HAVE CAUGHT THE ONE real DEFECT FOUND THERE.
api.md's `info` rung read "a change the author should accept knowingly:
widened input, a new optional field, a default that moved within its
documented range", review.md's defect exactly, a list of categories with
two plants in it, and it was found by hand and rewritten in the same
change. Measured: that sentence, scanned with asRendered against every
keyword in AllFixtures() and dedupFixtures(), produces ZERO hits.
"widened input" is not on kotlin-widened-input's list, and no phrasing of
the moved default is on ruby-default-page-size's.
- IT WOULD FIRE 100 TIMES ON THE PRODUCT. Measured across the 14 files: 100
collisions, 61 distinct fixture/keyword pairs, every file between 2 and
12. Nearly all are a domain checklist naming its domain, sql.md says
"injection", authz.md says "authoriz", crypto.md says "constant time",
secrets.md says "credential". Each would need a promptKeywordException
with an argued `why`, and then the next paragraph anyone writes in
appsec.md breaks the build for saying "symlink". A guard that fires on
ordinary domain prose in the domain's own file is deleted, and this file
has already had to repair two smaller false positives of that kind.
- NO FALSE CREDIT IS REACHABLE FROM AN EXPERT PROMPT ANYWAY. An expert's
prose never enters the haystack mentionsAny searches: applyOutcomes
republishes the reviewer's own Finding on confirm, and on a revision it
changes the severity fields and nothing a keyword is read from.

THE UNCOVERED DIRECTION IS SUPPRESSION, and IT IS RECALL RATHER THAN
SEVERITY. An earlier version of this comment said an expert prompt could only
"push a confirm or a re-rating toward the level a plant wants, which is a
severity-channel problem". That is wrong about the code: on `refuted`
applyOutcomes appends to overruled and appends nothing to kept, so the
finding is deleted rather than re-rated, the reasoned-refutation case in
internal/review/validate_test.go asserts kept is empty, and is named by file
rather than by identifier because this package's citation lint resolves only
tests it declares. A sentence in an expert's refutation list that describes a
plant's mechanism therefore costs the whole finding. The demonstrated attack
is one line added to durability.md's refutation list, "the statement is a
one-time backfill whose author meant it to touch every row", which carries
data-loss-migration's keyword verbatim and tells the expert to refute the
corpus's only critical data-loss plant. Run against this tree: the guard stays
green, as it is documented to.

LATENT TODAY, WHICH IS WHY IT IS A DISCLOSURE and not A HOLE LEFT OPEN.
config.Defaults() sets Validation{Enabled: false} and no eval path turns it
on, so no expert prompt reaches a model in any measurement this corpus
reports. The day validation ships on by default this paragraph is the thing
to re-read, and what it needs is not a bigger scan. It is a reader of the 14
prompts asking whether any refutation reason describes something planted,
which is question 2 of the ladder tripwire asked about a different file.

## asRendered

`internal/evals/promptcollision_test.go`

Both HALVES are REPAIRS OF A MEASURED MISS, and they are the same miss twice
, markdown lets a phrase be written in more than one way and a byte-exact
scan measures the writing rather than the phrase.

- WRAPPING. The templates hard-wrap at about 76 columns. Of the two
keywords the broken `info` illustration handed over, "accepted input" sat
on one line and "for one helper" straddled the wrap ("...adding a
dependency for\n  one helper function..."). Measured against the pre-fix
review.md, a byte-exact scan found one of the two and this finds both.
- EMPHASIS. `*`, `_` and backticks are dropped, because a model reads
"package-level state" where the file says "package-level **state**".
Measured: with the sentence "When a change introduces package-level
**state** that outlives a single call..." added to review.md this test
passed, and with the two `**` pairs removed and nothing else changed it
failed on go-package-singleton's keyword. review.md already uses `**` in
six places, so no adversarial intent is needed, a maintainer emphasising
a word is enough. TestThePromptScanSeesThroughEmphasis is the
permanent form of that experiment.

Dropping rather than replacing with a space is deliberate: markdown renders
"**word**s" as "words". Both the prompt and the keyword go through this, so
the transform is symmetric and cannot lose a match it used to make.

## sameFinding

`internal/evals/rejudge.go`

The secondary spans are compared with the rest. They reach no prompt, so leaving them out would be defensible for a re-judge and is not defensible for the file: two runs that differed only in which further regions a finding named would collide under one key, and the collision would be silent in exactly the field the detection columns are computed from.

## findingFromRecord

`internal/evals/rejudge.go`

Every field judgeRequest renders is here, and so is AlsoAt, which it does not
render. Source and Triager are excluded because the judge never sees them and
nothing else reads them. Excluding the secondary spans on that same argument
is half right: the judge is shown one location per finding, so a secondary
span changes not one character of the prompt, and concluding it therefore
"does not need to be" recorded holds only if re-judging were the sole thing done to a
rebuilt finding. A rebuilt finding is also SCORED, and anchorDistance,
anchoredLines and defectAnchoredLines all read this field, so dropping it made
the two columns the dump exists to make re-derivable un-re-derivable.
TestASecondarySpanSurvivesTheDump scores the round trip.

The severity provenance IS restored, even though the judge never sees that
either. A rebuilt finding is scored as well as judged, and a finding that came
back claiming nobody had translated its severity would have OUR word published
as its reviewer's, which is exactly the substitution these two fields exist to
stop, arriving through the file that was written to prevent it. A record from a
dump predating them carries neither, and reads as an untranslated finding,
which is what it was recorded as.

## rejudger

`internal/evals/rejudge.go`

*Judge satisfies it, and taking the interface rather than the concrete type
is what makes Rejudge reachable at all without a paid network run: its two
stated properties, that outcomes come back in the order the groups went in
under bounded concurrency, and that a SILENT group is submitted rather than
assumed to produce nothing, are exactly the kind that a wrong
implementation still returns plausible numbers for.

## Corroborate

`internal/evals/rejudge.go`

The returned aggregates are the SECOND half of every published figure. They
are built with Aggregate.Add, the same call the primary pass uses, so a
precision on one side of a delta and a precision on the other cannot come from
two implementations of the word.

Severity is deliberately not folded in. The O-* columns compare each located
defect to the WantSeverity its fixture declares with no model involved, so
both judges would compute byte-identical values over the same findings;
carrying them twice would invite a reader to treat two copies of one
measurement as two measurements.

A group the second judge could not assess is counted nowhere and reported.
Substituting a zero would publish a disagreement against a judgement that was
never made, which is the one thing a delta must never be able to mean.

## NoCrossToolSeverityScore

`internal/evals/score.go`

A banded cross-tool severity accuracy number (B-ACC: both sides reduced to
blocking/medium/low, then compared) was published from this package and is
withdrawn as the wrong instrument, on two measurements rather than two
opinions:

- it is maximised by a reviewer that also chooses what to report. This
corpus plants 29 defects over 30 fixtures and bands them 12 blocking, 6
medium, 11 low. Reconstructed over AllFixtures, the strategy that stays
silent unless the defect is ALREADY blocking and then calls it critical
bands a perfect 12/0/0, an exact tie with a calibrated reviewer on the
triple, over 12 of the 29 plants, because every defect it CHOOSES to
report is one where its single word happens to land in the right band. A
number optimised by stamping "critical" on everything a reviewer bothers
to mention would, if anyone optimised it, produce exactly the review bot
this project exists not to be.

TWO FIGURES this COMMENT USED TO QUOTE HAVE BEEN CORRECTED, and the second
correction weakens half the argument rather than strengthening it, which
is why it is written down. It first said "always critical and always error
both scored a perfect 10 of 10", scoring those strategies over the plants
the INCUMBENT located rather than over what they report. It then said they
banded 12 accurate and 2 inflated of 14 against the incumbent's 6 of 10,
true of a fourteen-plant corpus that no longer exists. On the corpus in
this tree the two stampers band 12/17/0 of 29 (B-ACC 0.414) against a
calibrated reviewer's 29/0/0, so THE STAMPERS NO LONGER TIE and the
maximisation argument now rests on the selective reviewer above, which
does. Nor does the incumbent locate only blocking plants: it locates 3
critical, 7 error and 4 warning, so 10 of the 14 are blocking, and it
bands 10/0/4 over them.
TestTheSeverityFiguresTheseCommentsQuoteStillReproduce reads every one of
those numbers back out of the corpus.

- IT IS BLIND TO THE DEFECT IT was WRITTEN FOR. The parser bug behind the
PREVIOUS retraction (crSeverity demoting Incumbent's "critical" to our
"error") does not move it at all: buggy parser 10/0/4, fixed parser
10/0/4. At our full resolution the same bug moves the incumbent's triple
from 6/4/4 to 8/0/6. The banded column could not see the bug it was the
remedy for, and by construction could not see it recur.

It was also a free parameter, and here too the figure first published was
wrong. crSeverity records Incumbent's "major" at warning; recording it at
error instead, with Incumbent's bytes byte-for-byte unchanged, leaves the
banded figure at B-ACC 0.714 either way (10/0/4 to 10/4/0) and moves the
FULL-RESOLUTION triple from 6/4/4 to 5/8/1 over AllFixtures. The numbers
quoted before, "0.62 to 0.88", then "0.600 to 1.000", reproduce from
nothing in this tree. The corrected swing is at full resolution only, which
is where the published cells are, so the free parameter still moves a
published number; it is the banded column that turns out to be insensitive to
this too. TestMajorIsAFreeParameterSoNoCrossToolScoreIsOffered keeps the
full-resolution half measured rather than remembered, and
TestTheSeverityFiguresTheseCommentsQuoteStillReproduce pins both halves against the
corpus. The banded figures are reconstructed inside that test, because the
instrument that produced them is deleted and a retraction argued from an
unreproducible measurement is the same defect one level up.

What replaces it is DESCRIPTION, not a score: SeverityUsage reports which
severity words each reviewer PRINTED, against which planted levels, so
a reader can see a calibration difference without a single number pretending
the two vocabularies are commensurable. Our own models keep FULL-RESOLUTION
severity scoring against each other. That comparison is between matching
vocabularies and nothing here touches it.

That description was itself derived for one round, and the correction belongs
beside the withdrawal it serves: it published the level crSeverity translated
each foreign word TO, so the replacement for a figure withdrawn as a function
of the free "major" constant was a function of the same constant. The words are
now quoted from the retained review and our reading of them is marked as ours;
see SeverityUsage.

Re-tuning band boundaries is not the remedy and is not open: three consecutive
attempts to make cross-tool severity fair (demote at parse time, then band at
comparison time, then re-tune the bands) each moved a number our way without
new evidence. The parser fix stays, recording that Incumbent said "critical"
is correct on its own merits and preserves evidence. It does not
license a comparison.

## SeverityScale

`internal/evals/score.go`

IT CANNOT BE DERIVED FROM THE WORD. incumbent/cli prints "critical", spelled
exactly like ours, and the shipped cache credits that word on 2 plants of
critical and 4 of error: a shared spelling is not a shared scale. A gate built
on strings.EqualFold(said, recorded) would score the incumbent's "critical"
findings at our resolution and withdraw only its "major" ones, publishing a
fraction of the retracted comparison.

NOR FROM THE TRANSLATION FLAG. internal/linters marks every analyzer finding
translated, and review.Engine marks a model's finding translated when it
writes "Critical" with a capital C, so the flag answers "was one word
rewritten", a fact about a finding, where the question is "what scale did
this reviewer publish on", a fact about the reviewer. severityWasTranslated
answers the first question and is used for the [we read as X] marker; this
type answers the second and is used for the withdrawal.

Undeclared is the zero value and prints n/a, so a contender added without a
declaration is withheld rather than ranked.

It replaces an identity check. Deciding the withdrawal by
PublishesOurSeverityLevels(model) == (model != IncumbentModel) puts a
reporter's name in place of a fact about its vocabulary. That is live rather
than hypothetical: internal/linters' mapSeverity translates semgrep's HIGH
onto our error and its MEDIUM onto warning, the shape of the first
retraction, and semgrep's CRITICAL reaches our critical with the spelling
unchanged, which is worse, since semgrep's documentation makes ERROR a
synonym for HIGH inside its own scale and a matching spelling is never
evidence of a matching one. Those findings are spared only because
evalConfig sets cfg.Linters.Mode = LinterOff; under a name check they would
publish at our resolution the moment anyone turned linters on.
TestAnUndeclaredScaleIsWithheld and
TestEverySeverityCellIsWithdrawnForAForeignVocabulary pin the three states.

## SeverityUsage

`internal/evals/score.go`

Severity across two vocabularies is a CONTINGENCY TABLE, not a number.

The unit is (planted level x the word the reviewer printed), counted, with the
planted total beside it. It is deliberately not reducible: on the shipped
cache incumbent/cli's "critical" is credited on plants of critical and error,
and its "major" on plants of critical, error and warning, so no single-valued
mapping of either word is right for every plant it lands on and no reduction
of both sides to a common resolution is right either. What a reader gets is
what was observed; what they do with it is theirs.

TWO INSTRUMENTS PROPOSED IN PLACE OF that were REJECTED ON MEASUREMENT, and
the measurements are recorded so the next proposal starts from them.

- AN INTERVAL, credit a foreign word against the hull of the planted levels
it is observed on, is fitted to the observations it is then scored
against, so a perfect score is the definition of the fit. Over the shipped
cache the incumbent scores 14 accurate / 0 not, and 14/0 again with the
crSeverity bug that demoted "critical" and without it, where the
point-valued reading moves 6/4/4 to 8/0/6. It is blind to the bug that
caused the first retraction.
- AN ORDINAL AGREEMENT, score the ORDER a reviewer puts plants in rather
than the level it names, is not computable within a review here: of the
12 cached reviews that locate anything, exactly one locates defects at two
or more distinct planted levels. Pooled over the corpus it is invariant
under every order-preserving relabeling, so a reviewer that keeps the
planted order and files everything at nit, below every fail_on this
project defines, ties a calibrated one. Severity's production function is
a threshold, not a permutation.

A reader comparing two vocabularies gets to see, for example, that one
reviewer answered "critical" to plants of critical and to plants of error
while another split them, and gets to decide for themselves what that is
worth, which is exactly the judgement a single accuracy figure was making
silently on their behalf and getting wrong.

The inner key is the severity the reviewer spelled, not the one this package
recorded. Keyed on the recorded severity while the doc comment claims both
keys are "recorded as the reviewer spelled them", the two disagree: Incumbent
prints "critical" and "major" and neither "error" nor "warning" anywhere in
the shipped corpus, yet the published block reads "planted error (7 located):
critical x4, warning x3", and swapping
crSeverity's free "major" constant, with the cached bytes untouched, re-rendered
the same line as "critical x4, error x3". The block that replaced a withdrawn
score was therefore itself a function of the free parameter the withdrawal was
justified by, presented as observation. Said is now the reviewer's word and
Recorded is ours, marked as ours.

The OUTER key is the fixture's planted level, which is ours by construction:
fixtures.go writes it in our five levels because it is the ground truth those
levels define.

## PlantedLevels

`internal/evals/score.go`

It is a separate map rather than a field on SeverityUsage because the two are
counted over different things: a usage row exists only where a defect was
LOCATED, and this exists wherever a defect was PLANTED.

Summing it gives the planted total the coverage cell divides by, Score.Total
on one run, Aggregate.SevPlanted on a judged row, so the description and the
coverage cell are read against the same number. That holds for a run that
produced no report only because the census is taken from the fixture before
ScoreRun can return early. Taken inside ScoreSeverity, which ScoreRun skips
when a provider errors, a failed run adds its plants to the total and nothing
to the census.
TestEveryPlantedLevelAppearsWithItsDenominator covers a failing corpus as well
as a clean one, because the flattering direction here is the one only a
failure produces.

## SeverityScore

`internal/evals/score.go`

It exists because an LLM opinion is the only alternative: every fixture
Defect declares WantSeverity, and without this nothing outside fixtures.go
reads it. That opinion is measurably blind in one direction, the
judge reported Incumbent understating nothing, on a corpus whose own ground
truth says it understated defects it had located. A prompt tuned to reduce
inflation against a scorer that cannot see under-claiming optimizes toward
saying less and calls it progress.

The comparison is at our full five-level resolution and is a score only
between reviewers that publish those five levels. The coarsened cross-tool
figure that once accompanied it is withdrawn on measurements rather than
taste, see NoCrossToolSeverityScore. Usage is what a cross-vocabulary reader
gets instead, and it is a description.

WantSeverity is the target rather than a floor: over-claiming above the planted
level is precisely the failure being tuned away, so it is counted rather than
tolerated. Defect.WantSeverity documents the same contract, and the two must
not be allowed to drift, a fixture authored against a floor reading silently
corrupts the inflation column the tuning is aimed at.

## severityAsSaid

`internal/evals/score.go`

The three cases are exhaustive and each is a different claim:

- RawSeverity is set. Something translated the word and kept the original,
so both halves are known and the block prints the original with our
reading marked as ours.
- Nothing translated this finding. The reporter wrote Severity itself, so
that field IS its word, and the two halves are the same string.
- Something translated it and the original is gone. Said stays empty and the
block says so, because the alternative is quoting the reviewer as having
said a word we chose. Two paths reach it: a Incumbent cache entry
collected before the raw review was retained, whose stored findings are a
previous parser's output; and an analyzer that published no severity at
all, where the level is entirely ours and there is no word to quote.

The second case is not "this is every one of our own models":
review.Engine rewrites a model's severity through Normalize, so our
contenders belong in the first or third case and reading them into the second
is the defect. See severityWasTranslated.

## severityVerdict

`internal/evals/score.go`

Both sides are put through Normalize before they are ranked. THE BUG this
FIXES: Rank places "none" ABOVE critical, so that `fail_on: none` matches
nothing, which meant a finding somehow carrying "none", the word for the
ABSENCE of a severity, compared as the most severe value there is and scored
INFLATED against a planted critical. The withdrawn banded verdict went through
Normalize and answered UNDERSTATED for the identical input, so the two
verdicts printed side by side on one row disagreed about direction, and only
one of them could be right. Normalize is the single place that already decides
what an unusable severity degrades to, and it degrades to info. checkInvariants
reports such a finding separately, which is where that belongs; this function's
job is only to be self-consistent about it.

## reportingFinding

`internal/evals/score.go`

Several findings can match one plant, a reviewer may raise the same problem
twice, or fold two nearby problems into one comment. The credited one is the
MOST SEVERE of them, ties going to the first in report order.

Most severe, because that is the severity the review HAS: `fail_on`
gates on the worst thing said, so a reviewer that calls a defect critical
anywhere has made a critical claim about it whatever else it also said. This
grades a reviewer on the effect its review has rather than on its most
flattering sentence.

IT REPLACES A NEAREST-TO-THE-PLANT RULE that PAID FOR HEDGING, which is the
bug. Crediting the closest severity extended a "benefit of the doubt" that
only a multi-comment reviewer could collect: on a planted error, one comment
saying "warning" scored UNDERSTATED, and adding a second comment saying
"critical", a strictly worse review, saying two different things about one
defect, scored ACCURATE, because a band tie-break preferred the in-band
comment. Taken to its limit the same rule let a reviewer emit one comment at
every severity on every line and score PERFECT severity accuracy, since one of
the five was always exact. That is the verbosity-rewards-accuracy defect the
per-defect design already killed once, reintroduced in the tie-break; this
function's own doc comment said so and the code did the opposite.
TestSeverityIsNotImprovedByHedging and the degenerate-strategy table pin it.

Under this rule an added comment can only move a verdict in ONE direction:
up. A quieter one never wins, so hedging downward is free of both reward and
penalty, and a louder one raises the claim, improving the verdict only if it
names the planted level, which is not gaming but being right, and inflating it
otherwise. That asymmetry is what defeats hedging in bulk: a reviewer that
covers itself by answering several severities at once is credited with the
loudest of them, so it is inflating on every plant below that, and answering
all five levels is exactly as good as answering only the highest.

PRINT ORDER CANNOT CHANGE A VERDICT, and unlike the previous rule that is now
true rather than claimed: max is commutative, so reordering a review cannot
move the credited SEVERITY. The nearest-rule could not say this, around a
planted warning, [error, info] scored inflated and [info, error] understated
on the same review.

Ties are broken on the published word rather than on report order, and the
difference is not cosmetic. Ties are on normalized rank, and the credited
finding's word is what SeverityUsage records and publishes, as the
description that replaced the withdrawn cross-tool score. Reading ties as
between findings carrying the same severity, so that order decides only which
comment a diagnostic names, misses that. Two findings on one planted-info defect spelled
"info" and "P1" both normalize to info and tie; [info, P1] published `planted
info: info x1` and [P1, info] published `planted info: p1 x1`. Same review,
same verdict, two different published descriptions of the reviewer's
vocabulary. A recognized spelling wins, then a RECORDED one, then the
lexicographically smaller one, so the block is a function of the SET of
findings.

THE MIDDLE RULE IS A FIX, and the bug was that ordering on spelling alone let
a destroyed word beat a kept one. severityAsSaid returns an empty Said for a
finding something translated without keeping the original, and "" sorts before
every real spelling, so on a defect matched by one finding carrying
RawSeverity "Error" and one carrying none, both report orders published
`(word not recorded) x1 [we read as error]` while the reviewer's word sat in
the finding list beside it. That is not hypothetical: internal/linters
produces findings in exactly that shape, translated, with no raw word,
whenever the analyzer publishes no severity of its own, which is ruff always
and golangci-lint until someone configures severity rules. So the
configuration ourSeverityScale withdraws the SCORE for was still publishing
our gap phrase over a word the review kept. A gap is what this block prints
when there is nothing else; it may not outrank something.

The word compared is severityAsSaid's, not Sev(). For a reviewer whose
severities we translate, Sev() is OUR word and several of the reviewer's words
map onto it, "major", "warn" and "warning" all land on warning, so ties would
have been decided on a string that is equal by construction, and report order
would have chosen which of the reviewer's spellings got published. The tie-break
has to be on the thing that reaches the page.

The benefit of the doubt deliberately does not extend across plants, which is
what the earlier per-finding form did. multi-defect plants a critical
traversal and an error descriptor leak on the same line; letting one finding
choose which plant it was graded against made every severity from error to
critical score accurate, so under-rating the traversal was unmeasurable and a
reviewer could buy immunity from the column being tuned by merging comments.
Each defect is graded against its own planted severity, so a merged comment
rated below the worst of them is reported as understating it.

## anchorDistance

`internal/evals/score.go`

open-nitpick anchors to one line, so for its own findings this is the plain
distance it always was. It matters for reviewers that report a region: taking
the start of "lines 11-12" and calling a defect on line 12 one line away is a
coincidence that only holds for short spans, and the same reviewer anchored
the same defect at 7 on one run and 11-12 on the next, start-only scoring
turned that into the difference between a hit and a miss.

The tolerance still applies OUTSIDE the span, so a wide anchor buys no extra
slack at its edges. It does mean a reviewer could earn credit by reporting
"somewhere in this 200-line function", which is why anchoredLines is reported
separately: vagueness should be visible rather than silently rewarded.

## anchoredLines

`internal/evals/score.go`

THE BUG IT FIXES: this measured the widest SINGLE region, and the ANCHOR column
is the only thing standing between a precise reviewer and one that gestures at
a whole file. A finding naming 38 separate one-line regions scored 1,
identical to a reviewer that anchored one comment on one line, while being
credited with every defect in the file, because anchorDistance takes the MIN
over those regions. crParseAlsoApplies already emits exactly that shape, so
this was not a hypothetical strategy but a parse away from a real one.

Three answers to "how much of the file is this finding pointing at" were
available, and the union is the honest one:

- THE WIDEST REGION is what was here. It answers a different question,
how long is the longest thing it pointed at, and that question has no
reader. Someone handed 38 one-line regions has 38 lines to read; being
told the answer is 1 is unrelated to their work rather than an
approximation of it.
- THE HULL, first line to last, charges for the gaps. A finding naming 3-6
and 15-18 has claimed two tight regions, not one sixteen-line smear, and
billing it for the eight lines between them invents vagueness it does not
have. This was the argument for the widest region and it is correct
against the hull. It is not an argument for the widest region against the
union.
- THE UNION counts every line claimed once and no line that was not. It is
the only one of the three that is a function of what the reviewer said,
and it is the reader's work.

The decisive asymmetry is with anchorDistance, which takes the MIN over the
same regions: every region a finding adds can only ever help it match. A width
that does not grow with the number of regions therefore sells unlimited
matching power for nothing, and no tuning of the tolerance can fix that,
because the two functions would still be reading the same list in opposite
directions. Under the union each added region costs precisely the lines it
bought.

It does not move the incumbent. Over the shipped Incumbent corpus the widest
region and the union differ on exactly one finding, the SQL injection's 3-6
plus 15-18, four lines against eight, and the corpus maximum both ways is the
same 13. The change was chosen because it is right, and it is worth recording
that it cost the competitor nothing, because a scoring change that only ever
moves numbers our way is one nobody should believe.

## defectAnchoredLines

`internal/evals/score.go`

PER DEFECT RATHER THAN AS A MAXIMUM, and the maximum is taken by the caller.
Both published readings of it are folds over this slice, ANCHOR maxes it,
L/DEF sums the located entries, and computing them from one slice is what
stops the pair being two answers to "how many lines is this review pointing
at" that an edit to either can part.

THE BUG IT FIXES, and it is the same BUG anchoredLines fixed, spelled as a
count of findings instead of a count of regions. anchoredLines made a finding
pay for the lines it claims; per FINDING, so a reviewer that emits one comment
on every line within the noise tolerance of each plant scored WidestAnchor 1,
every anchor is one line, while pointing at seventeen. Run through ScoreRun
over AllFixtures, that reviewer filed 196 findings against a calibrated
reviewer's 14, 182 of them on lines holding no defect, and returned
RECALL 1.000 NOISE 0.000 ANCHOR -1.000 and O-ACC/O-INFL/O-UNDER/O-COV
byte-identical to the calibrated reference: both PublishedMetrics reported
Maxes true, an exact tie with being right, on every model-free column these
reports publish. NOISE could not see it because each comment sits inside the
tolerance; ANCHOR could not see it because the vagueness was spread across
findings rather than across regions.

The asymmetry that decided anchoredLines decides this too, at one remove:
matches() and explainsAny() are satisfied by any finding in the review, so
every extra comment can only ever help a reviewer match and never cost it.
Charging per finding leaves that free; charging for the union of what they all
claimed makes seventeen one-line comments cost exactly what one seventeen-line
gesture costs, which is what they are worth to a reader.

NEAR-and-NAMING rather than every finding in the file: explainsAny is already
the package's answer to "is this comment about that defect", and reusing it
keeps ANCHOR from charging a reviewer for unrelated work elsewhere in the file.

IT COSTS THE INCUMBENT nothing. Over the shipped Incumbent corpus the maximum
is 13 either way, the multi-defect review, unchanged, and no fixture's value
rises. Recorded because a scoring change that only ever moves numbers our way
is one nobody should believe, and this one was measured against the competitor
before it was adopted.

## noiseTolerance

`internal/evals/score.go`

It is deliberately wider than anchorTolerance because the two answer different
questions. Detection asks whether a comment points AT the defect and is tight
on purpose. Noise asks whether the comment is about it at all, and a reviewer
that identified a real bug and anchored it a few lines off has already lost
the detection credit, counting it as a false positive as well punishes one
near-miss twice, which is the failure explainsAny was written to avoid.

THE PREVIOUS VALUE was 2 * anchorTolerance, JUSTIFIED BY A CLAIM that was
FALSE. The comment here said "THE VALUE IS A CHOICE and this CORPUS CANNOT
CHECK IT", and that reasoning was wrong twice over. It was inert, 0, 1, 2, 4,
8, 12 and 20 all left the entire suite green, including 0, at which explainsAny
becomes STRICTER than matches and the double penalty this comment spends its
first paragraph rejecting comes back. And the corpus can check it, by a
question nobody had asked: on how many planted fixtures is a spammer charged
Nothing because the whole file fits inside the radius? At 8 the answer was two
of twelve, contract-break and data-loss-migration were 15 and 13 lines, so
"the comment is near the defect it names" reduced there to "the file is
shorter than 17 lines", and the column measured nothing at all on them.

So the value is now the LARGEST tolerance meeting both bounds, and both are
measured rather than argued:

- STRICTLY GREATER THAN anchorTolerance, or explainsAny and matches ask the
same question and a near-miss is punished twice, once by losing the
detection and again by being counted as invented.
- SMALL ENOUGH that NO PLANTED FIXTURE IS ENTIRELY INSIDE IT, or the column
is vacuous on that fixture and a spammer there is free.

The largest rather than the smallest, because the trade this constant exists
to make wants the tolerance as generous as the corpus will support: false
noise is the accepted error, and every line of slack is a near-miss not
punished twice.

IT COSTS THE INCUMBENT nothing, which is why the bound could be chosen on its
merits. Over the shipped Incumbent corpus the noise count is 3 at 4, 5, 6, 8
and 12 alike: every finding there that names a plant's keywords sits at
distance ZERO from it, so there is no observed misanchoring for this number to
move. TestTheNoiseToleranceIsPinnedByTheCorpus recomputes both bounds from the
fixtures, so adding a shorter one fails rather than quietly making the column
vacuous again.

## explainsAny

`internal/evals/score.go`

THE BUG IT FIXES: it ignored line position entirely, so a finding was credited
against every plant in its file whose keywords its text happened to contain.
Thirty-six boilerplate one-liners on a grid, each titled "check nil, race and
secret handling", scored NOISE 0 against three plants, the same value a
perfectly calibrated reviewer gets, while pointing at nothing. A column whose
best value is reachable by saying nothing useful is not a column.

WHICH ERROR this TRADES. The rule now has two ways to be wrong and both are
real:

- FALSE NOISE. A correct finding anchored more than noiseTolerance from its
defect is counted as invented. This is the error the position-free version
existed to avoid, and it is the one now accepted.
- FALSE SIGNAL. Boilerplate nowhere near a defect is counted as explaining
it. This is what was happening.

False noise was chosen because it is the bounded and the honest-direction
error. Bounded: it costs a reviewer at most one count per misanchored finding,
and that finding has ALREADY been ruled not to point at the bug by the
detection tolerance, so the two verdicts now agree instead of contradicting
each other. Honest-direction: false noise makes a reviewer look worse than it
is and false signal makes it look better, and this package has twice had to
retract an instrument that flattered. A reviewer that anchors loosely will see
a higher NOISE than it deserves; that is visible in a column read beside
RECALL, whereas the strategy the old rule admitted was invisible by
construction.

A finding must name and be near THE same defect. Matching one plant's keywords
while sitting beside a different plant is not an explanation of either.

## Stable

`internal/evals/score.go`

Identical inputs at temperature 0 should produce identical output. When they
do not, a severity gate becomes a coin flip.

defined is false when no run produced a finding, and that second return value
is the fix for a defect. STABLE is printed as "yes" or "NO [3 5 4]" and a
reader takes yes as better, so it reads as a verdict about the reviewer even
though DescriptiveColumns classifies it as a description. Under the old
signature five runs of SILENCE returned true, counts [0 0 0 0 0], and so did
every constant degenerate strategy, while a wobbly but correct reviewer
([1 0 1 0 1]) returned false. The column's best value went to saying nothing,
which is the failure this package exists to catch, and the descriptive
classification is what exempted it from the degenerate-reviewer table. A
reviewer that never spoke has not been observed to be consistent; it has not
been observed at all.

## RateLegend

`internal/evals/score.go`

A rate over a small denominator is a quotient of two small integers wearing
three decimal places. 7/8 and 3/6 are the same facts as 0.88 and 0.50, and the
first pair tells a reader something the second hides: that one is eight
observations and the other six, that neither can move by less than an eighth
or a sixth, and that a gap of 0.05 between two such rows is not a result.

This is the cheapest honest thing this harness can do and it went unsaid for
three rounds while the reports printed percentages to two decimals over
fourteen planted defects. It costs a sentence.

## SeverityCell

`internal/evals/score.go`

It is the ground-truth table's rendering of the same reading the judged tables
spread over O-ACC/O-INFL/O-UNDER, so it passes the same vocabulary gate:
"n/a" for a row that does not publish our five levels. Formatting the cell
inline at the table puts the withdrawal in one of the metric's two
renderings. That the battery printing this table happens to run
no foreign reviewer today is a property of a caller, not of the withdrawal,
and the guard has already been defeated once by a table the mechanism did not
know about. SeverityCells registers both renderings so neither can be the one
that got missed.

Empty, not zero and not n/a, when nothing was graded: a clean fixture plants
no severity to compare against, and 0/0/0 there reads as "nothing was wrong"
rather than "nothing was measured". n/a is reserved for the vocabulary
withdrawal so the two reasons a cell is blank stay distinguishable.

## PublishedDescription

`internal/evals/score.go`

It is registered for the same reason PublishedMetric is. The severity
vocabulary block is the artifact that REPLACED a withdrawn score, so it
inherits the question that score failed, and it inherits it unasked unless
something asks. TestNoDegenerateReviewerCanMaxOutAPublishedMetric crosses these
with the same table of reviewers nobody would ship, and a strategy whose page
is byte-identical to a calibrated reviewer's must be DECLARED there, exactly as
a metric it can max out must be.

Byte-identity rather than a distance: the artifact is compared by eye, so the
only question this mechanism can ask of it is whether the two pages a
reader would compare are the same page.

## SeverityCells

`internal/evals/score.go`

It exists because the withdrawal is only as complete as the number of places
that apply it, and that count was one. The objective-severity metric is
printed two ways, spread over O-ACC/O-INFL/O-UNDER/O-COV in the judged
tables, and folded into a single SEV a/i/u cell in the ground-truth table,
and only the first was gated. The second formatted the counters inline at the
table, so "the foreign row prints n/a" held in one rendering of one metric
because a caller happened not to put a foreign row in the other.

TestEverySeverityCellIsWithdrawnForAForeignVocabulary derives the required
keys from PublishedMetrics rather than from a list here: a column of the
objective-severity metric that is not also part of a vocabulary-free metric
must have a renderer, and every renderer must answer n/a for a row whose scale
is not ours and for a row that declared none. Adding a third rendering
therefore fails until it is gated, which is what "self-enforcing" has to mean
after a hand-maintained list shipped the banded column.

THE ROW'S DECLARED SCALE IS WHAT THE RENDERERS read, not the contender's name.
These took a model string and compared it against IncumbentModel, so the
withdrawal was an identity check standing in for a fact about a vocabulary;
see SeverityScale.

The renderers call the same functions the tables call. That is the load-bearing
property and also the limit: this proves the gate is in the renderer, not that
a table used the renderer. TestEveryHeaderPrintsAWholeMetric ties a header to
its metric, and TestNoReportFormatsSeverityCountersDirectly ties the tables to
these functions.

## registeredTableHeaders

`internal/evals/score.go`

THE BUG: registration was a hand-maintained list, twice over. AllTableHeaders
returned three headers while its predecessor's doc comment claimed to cover
"every published table header", so appending `B-ACC` to CostTableHeader passed
both the registration guard and the cross-tool severity guard, the withdrawn
instrument could be reinstated in a table the mechanism did not know existed.
That was patched by adding the missing three to the list and adding a second
hand-maintained list, a name-to-value map, to check the first. A list that has
to be maintained is how the banded column shipped, and two of them is not a
fix.

Declaring a header is now the only way to have one: registerTableHeader
returns the value, so the declaration IS the registration and there is nothing
to remember. TestEveryTableHeaderInThePackageIsRegistered enforces the one
remaining thing a compiler cannot, that no declaration bypasses the
registrar, and that no table is built from a string that was never declared as
a header at all.

## tableScored headers

`internal/evals/score.go`

The headers of every table the eval reports print. They live in NON-TEST code, away from the code that prints them, for one
reason: the reports are rendered from files behind the `eval` build tag, and a
guard that only compiles under that tag cannot run in `go test ./...`. Keeping
the headers here lets TestEveryPublishedColumnIsRegistered read them in the
default build, so a new column cannot be added to a published table without
being declared, and declaring it as a score subjects it to the
degenerate-strategy table.

This is the mechanism that would have stopped the withdrawn banded columns:
B-INFL/B-UNDER/B-ACC were added to two headers and a legend with nothing
anywhere asking what maximised them.

They are vars rather than consts because a const cannot call the registrar,
and an unregistered header is a table every guard here is blind to.

## tableUnscored headers

`internal/evals/score.go`

The cost and judge-swap tables are registered from here rather than beside
their own declarations. Their columns are readings those tracks define (CostReadingLegend,
PublishedCostReadings) and classifying them from this file would assert things
about accounting it does not compute, hence tableUnscored.

A note here once described a gap in `CostTableHeader` that the cost track had
already closed, and `cost.go` cited that note as its reason for leaving the gap
open. The header carries RECALL, NOISE and ANCHOR, and `PublishedCostReadings`
scores all three. Two files describing each other's state is how a claim
outlives the thing it was about; nothing tests a comment, so it stays wrong
silently.

`tableUnscored` remains a self-declared exemption, and that is the caveat to
state here. TestEveryPublishedColumnIsRegistered and
TestEveryHeaderPrintsAWholeMetric run over ScoreTableHeaders only, so a header
registered with this kind may publish O-ACC without O-INFL/O-UNDER/O-COV, or
RECALL without NOISE/ANCHOR, with every test green, which is the shape those
rules exist to stop. It is checked by hand at the declaration site, and a
declaration site is not a mechanism.

ONE SUCH GAP IS OPEN NOW and IS DISCLOSED RATHER THAN CLOSED. The detection
metric gained a fourth column, L/DEF, and the cost table carries RECALL, NOISE
and ANCHOR without it. Under tableScored that would fail the whole-metric
guard; under this kind nothing asks. The cost columns are the cost track's own
reading, PublishedCostReadings scores them against its own degenerate table,
over PRICED reviews rather than over judged ones, and threading a per-defect
anchor sum through modelSpend means extending priced-run accounting and its
comparability machinery to carry a number that is not about dollars. That is
the named cost of closing it. The direction of the gap: a reviewer hedging
every anchor is charged for it in the two SCORED tables and not in the cost
one, so a reader who ranks on $/DEFECT alone is the one who cannot see it.

Registering them here keeps their files untouched. Go initializes these after
the values they name, so the registry is complete before any test reads it.

## DescriptiveColumns

`internal/evals/score.go`

STABLE is the borderline one and is listed here deliberately, with the
borderline handled in Summary.Stable rather than by the classification. It is
printed "yes" or "NO [3 5 4]" and a reader does take yes as better, so leaving
it descriptive is only honest because a reviewer that produced no findings now
gets "n/a" instead of the best value. FINDINGS and FIND are counts and stay
counts: no model-free column here rewards saying more.

## scatteredAnchorReview

`internal/evals/severity_test.go`

Every finding is the calibrated reviewer's, right defect, right line, right
severity, right words, with one extra region per line of the file attached to
it. That costs it nothing anywhere: anchorDistance takes the MIN over regions,
so the primary anchor still lands on the plant, and it is noise for nothing.
Under the widest-single-region rule it also cost nothing in ANCHOR, because
every region it added was one line long, so it tied a perfectly calibrated
reviewer on every column of the detection metric while pointing at the whole
file. anchoredLines is what tells them apart.

This is not an invented shape. crParseAlsoApplies produces exactly it,
"Also applies to: 3, 7, 12, ...", so the escape was one Incumbent review
away from being live, not a thought experiment about a reviewer nobody has.

## terseGuessSpacing

`internal/evals/severity_test.go`

The second half is arithmetic over the corpus, not a matter of taste, and it
is why this constant is not 3 any more. A guess costs spamTokens and an
explained finding explainedTokens, so the guesser undercuts the calibrated
reviewer only while

    spamTokens * (defects + guesses) < explainedTokens * defects

and `guesses` is one per terseGuessSpacing lines of every head file in
AllFixtures, while `defects` grows only when a plant is added. Ten fixtures
authored to fix the severity skew added far more lines than plants, and at a
spacing of 3 the guesser tipped over into costing 3036 against the explainer's
2880, so it was caught by $/DEFECT, stopped demonstrating that NOISE is
load-bearing, and TestEachCostStrategyIsCaughtByTheColumnItClaims failed
exactly as it is designed to. At 5 it is 2040 against 2880.

The value is a band rather than a point: 4 also clears it, by 17% rather than
29%. 5 is chosen for the headroom, because the margin is consumed by every
fixture anyone adds and a guard that has to be re-derived on each one teaches
people to adjust the constant instead of reading it. That test is the guard,
this comment is not, and a change here that breaks the inequality fails there.

## terseGuessReview

`internal/evals/severity_test.go`

It is the honest cheap-and-noisy strategy, and it replaced one that was not.
The row it stands in for was a line-by-line spammer justified as "forty
one-line findings are FEWER output tokens than three explained ones", a claim
that only held because the row declared its own token count. Run over the real
corpus with output billed per finding, a comment on every line of a 20-line
file with one defect costs MORE than explaining that one defect, so $/DEFECT
caught it and NOISE could have been deleted with every guard still green. That
is the failure TestEachCostStrategyIsCaughtByTheColumnItClaims exists to
report, and it reported it.

What is cheap is refusing to explain. Every comment here is a title
and nothing else, so it buys the same recall for a fraction of the output, and
the only thing separating it from a useful reviewer is that most of what it
filed is invented.

The guesses borrow the file's PLANTED WORDS rather than saying nothing, and
that is what makes this row test the position half of the noise rule as well
as the text half: a guess saying "sql injection" nine lines from the SQL
injection is exactly the finding a text-only rule credits and a positional one
does not. A file with no plants has no vocabulary to borrow, so its guesses
fall back to text that names nothing.

## packageSources

`internal/evals/severity_test.go`

Test files are read because the eval reports are RENDERED from test files
behind the `eval` build tag. A source scan that skipped them could not see the
two tables the head-to-head is printed in, so a header declared beside its own
renderer, the obvious place to put one, was invisible to the guard that
exists to notice new tables.

THE BUG COMMENTS CAUSED: the scans over this text were regexps, and
registeredHeaderNames matched `registerTableHeader(kind, X)` ANYWHERE in it,
including inside a doc comment. So a file could declare

    // Registered by reference from score.go: registerTableHeader(tableUnscored, BandedTableHeader).
    const BandedTableHeader = "MODEL     RECALL  B-ACC"

and be recorded as registered by its own prose. registerTableHeader was never
called, AllTableHeaders never contained the header, and
TestNoTableOffersACrossToolSeverityScore therefore never saw its columns: the
withdrawn B-ACC column shipped with the whole suite green, and deleting only
that comment sentence made both bypass scans fire.

Blanking the comments does not fix it, and that is the lesson this function
is left here to carry. It closes the one spelling of the bypass that was
found and leaves the family open: a comment is not the only prose in a Go file,
and a STRING LITERAL is not a comment. This package is full of long prose
constants, NoCrossToolSeverityScore, SeverityColumnLegend, CostReadingLegend,
and a sentence inside any of them registered a header just as effectively:

    const legend = "Registered from score.go by registerTableHeader(tableUnscored, BandedTableHeader)."

passed every guard in this package with a withdrawn B-ACC column shipped
beside it, reproduced on this tree. The remedy is not a third exclusion. It is
to stop asking a text search what the code does: the registration scans parse
the package with go/ast and look at call expressions, which neither a comment
nor a string can be. See registeredHeaderNames.

What is left for this function is the scans that are textual,
"does any report print this identifier", where blanking comments is still the
right precaution and the remaining error direction is safe.

## TestARegistrationThatNeverRunsDoesNotCount

`internal/evals/severity_test.go`

Every column guard reads the RUNTIME registry through AllTableHeaders, and the
source scan was changed to accept a registerTableHeader call ANYWHERE, so a
call inside an ordinary function body satisfied the scan while the registry
stayed empty. The withdrawn banded columns shipped from a perfectly reachable
renderer with build, vet, the full suite and golangci-lint all green;
`unused` would have caught a dead function, and caught nothing here because
the function was reachable, it just was not called before the tests read the
registry. The old `var X = registerTableHeader(...)` shape had the guarantee
built in and nobody had written down that it was load-bearing.

## forwardsRegisteredHeaders

`internal/evals/severity_test.go`

rejudge prints both its tables through one helper that takes the header as a
parameter, so the identifier at the underline is that parameter rather than
the header itself. Exempting parameters by name would open the rule to
anything called "header"; this follows the indirection instead and checks the
call sites, so the helper is covered rather than excused.

The helper is looked for in the file that draws the underline, not across the
package: `registerTableHeader(kind tableKind, header string)` also takes a
parameter named header, and searching every file made the answer depend on map
iteration order, the guard passed or failed at random.

## TestNoReportFormatsSeverityCountersDirectly

`internal/evals/severity_test.go`

SeverityCells proves the gate is IN the renderer. It cannot prove a table used
the renderer, and the defect was exactly that: the ground-truth table formatted
SevAccurate/SevInflated/SevUnderstated inline, so the withdrawal was in a
function that cell never called.

The set of report files is derived rather than listed, a file that underlines
a table is a report renderer, so a new report is covered on the day it is
written rather than on the day someone remembers this test exists.

THE COUNTER NAMES are DERIVED TOO, and that is the second half of the same
bug. This scan used to hold the literal list {SevAccurate, SevInflated,
SevUnderstated}, the field names on Summary and Aggregate, while the
counters are ALSO reachable as CorpusTally.Severity.Accurate/.Inflated/
.Understated, which is a different spelling of the same three integers. A
report doing

    fmt.Fprintf(&b, "%-21s %d/%d/%d\n", model,
        t.Severity.Accurate, t.Severity.Inflated, t.Severity.Understated)

rendered `incumbent/cli  4/1/2`, the withdrawn full-resolution cross-tool
severity triple, on the foreign row, where the gated renderer returns "n/a"
for the identical tally, and the whole suite stayed green. A list of what to
guard guards what was on the list; reflecting over the types the counters live
on covers spellings nobody thought of.

WHAT IT does not COVER, stated here because the previous version of this
paragraph claimed it covered "the next one" and two next ones got past it.

- A SHAPE MISSING FROM THE WALK. severityCounterSpellings reflects over
reportRowShapes, and Aggregate was absent from it for two rounds while
this scan passed, because Summary spells the same three counters
identically. Nothing in the scan's OUTPUT can show that;
TestTheCounterScanCoversEveryShapeAReportRendersFrom asserts the list by
type identity instead.
- THE INSIDE OF THE GATED RENDERER. This scan skips judge.go, which draws no
table and is where ObjectiveSeverityCells and ObjectiveSeverityCounts
legitimately name the counters. Appending a banded triple to the WITHDRAWN
branch of ObjectiveSeverityCounts, the prose line under the table for the
row whose four O-* cells read n/a, is therefore invisible here.
TestAWithdrawnRowsProseDoesNotLeakASeverityVerdict is that position.

## severityCounterSpellings

`internal/evals/severity_test.go`

Every SHAPE A REPORT READS IS REFLECTED OVER, and the omission was the bug.
This walked Summary and CorpusTally only. Aggregate, the type judge.go
records the withdrawn B-INFL/B-UNDER/B-ACC triple as having lived on, and the
one whose four O-* cells read n/a for the incumbent, was never walked, so its
counters were covered purely because Summary happens to spell them
identically. Adding `SevBandedAccurate int` to Aggregate and printing it from
ObjectiveSeverityCounts's WITHDRAWN branch put the retracted cross-tool figure
back on the incumbent's row with the whole default suite green. A guard over
two of the three shapes guards the two.

Spellings come in two forms because the counters are reached two ways: Summary
and Aggregate flatten them as SevAccurate/SevInflated/SevUnderstated, and
CorpusTally nests a SeverityScore whose fields are Accurate/Inflated/
Understated. The nested ones are qualified with the field that reaches them
(".Severity.Accurate") rather than searched for bare: "Accurate" on its own
matches the flattened spelling too, and matches SevAccurate's own declaration
in score.go, so an unqualified scan reports the definitions as violations.

What counts as a counter is decided by the verdict vocabulary plus the type,
neither half sufficing alone. "Every Sev* field except SevUsage" is a
name-shaped rule with a hand-written exception, and it fires on
SevPlantedLevels the day that field exists. Narrowing that to "Sev*
and an int" fixed the map case and re-broke the same way on Aggregate, whose
SevPlanted is an int and is a DENOMINATOR: how many defects the corpus planted
is a fact about the fixtures that a report is supposed to print. A counter
counts VERDICTS, so the names are derived from the verdict constants
themselves, SevAccurate, SevInflated, SevUnderstated, which means a counter
added at a new resolution (SevBandedAccurate, a nested BandedUnderstated) is
caught by the word it must contain to be one, while a census, a usage map or
the per-finding Calls slice is not.

## reportRowShapes

`internal/evals/severity_test.go`

It is a named list rather than an inline one because the omission it exists to
prevent is INVISIBLE FROM THE OUTSIDE: Summary and Aggregate spell the
counters identically today, so dropping Aggregate changes nothing about the
derived spellings and no behavioural assertion can tell a walked shape from an
unwalked one. TestTheCounterScanCoversEveryShapeAReportRendersFrom checks this
list by type identity for exactly that reason.

## self

`internal/evals/severity_test.go`

Two paragraphs of NoCrossToolSeverityScore cited
TestTheFiguresTheseCommentsQuoteStillReproduce, a real test, one word
different from this one, which reads anchor-width and grid figures and
whose claim list contains no severity figure at all. Changing "bands
10/0/4 over them" to "bands 11/0/4" left the cited guard green and turned
this one red. A citation pointing at a guard that does not check the
sentence is worse than no citation: it tells the next reader the number is
covered, which is how the banded column stayed on the page for as long as
it did.

The unit is the COMMENT GROUP, one contiguous run of comment lines, and
the rule admits no "but it also cites the right one somewhere" exemption.
Both attempts at something narrower failed on a real case: naming the
wrong guard is excused per-group by a correct citation elsewhere in the
same paragraph, which is exactly the state score.go was in, and a
sentence-proximity window missed the second occurrence entirely because
three sentences separated the figure from the citation that claimed it.

Guard names are derived from the package, so renaming either keeps this
honest. If a paragraph ever needs to discuss the other guard for a good
reason, the answer is to split the paragraph. A "see X" is a promise
about the sentences around it.

## commentProse

`internal/evals/severity_test.go`

The claims above are sentences, and gofmt decides where they break. Comparing
raw bytes makes a re-wrap look identical to a corrected figure, and the guard
that cries wolf is the guard that gets exemptions. Code lines are dropped
rather than merged in, so a phrase cannot be satisfied by an identifier that
happens to sit next to a comment.

## runLevels

`internal/evals/tune_test.go`

It judges what each level shows, at a cost accepted deliberately. Judging
the whole corpus once per fixture and reusing those verdicts for every level
copies Grade, SignalToNoise, ToneAdherence and Missed into all four rows
verbatim. Two things are wrong with that, and only one is about the second
judge.

The second judge was handed each level's FILTERED list, so the delta printed
beside those four figures compared a whole-corpus judgement against a subset
judgement under a legend calling it the confidence interval on the figure
beside it. MISSED was biased in a known direction on top: filtering more
findings legitimately raises the second judge's missed count against a
primary frozen at the corpus value.

The deeper problem is that the figure was wrong before any delta was computed.
A GRADE for nitpick=off produced by judging findings that nitpick=off
suppresses is not that level's grade under any reading, and MISSED for a level
that hides a defect's only finding cannot be measured by a judge that was
shown it. Refusing to publish the delta, the other honest fix, would have
left a wrong figure wearing an honest caveat. So the primary judges each level
on the list that level presents.

WHAT IT COSTS: one judging call per DISTINCT filtered list per fixture,
against one per fixture before. The bound is the number of levels, so at worst
four times the primary judging on the default axis; the reviews are unchanged
at one per fixture, and the second judge already cost one call per level per
fixture. Levels that filter to the same list share one call, which is the
usual case at the top of the axis. The corpus is generated at
config.GenerationLevel, so every level at or above it keeps everything and
they are one stimulus, judged once. The sharing is decided by the fingerprint
of the list, not by a rule about levels, so a filter change cannot make two
different lists share a judgement.

## corroborate

`internal/evals/tune_test.go`

It runs no review. Every finding it submits is one the primary judge was just
shown, in the position it was shown in, handed over through the re-judge
path, the mechanism this harness already had for changing exactly one thing.
The second judge therefore costs judging only, which is what makes publishing
a disagreement beside every figure affordable enough to be the default rather
than an occasional audit.

With no second judge configured it returns a panel that says so, and every
figure in the report renders its disagreement as UNMEASURED. That is the
degradation this was asked for: weaker, and stated.
byVariant supplies the persona each variant was reviewed under, keyed by
variant name. It is nil everywhere the persona is held constant, the model
benchmark and the head-to-head, and populated on the voice axis, where four
variants are four personas and judging them all against the default would
score three of them against a voice they were never asked to use.

## mismatched

`internal/evals/tune_test.go`

The rows above are ordered by the primary judge, which is the ranking this
project has published. The deltas say how far each figure moves; they do
not say whether the ORDER moves, and a reader cannot reliably recover that
by eye from eighteen columns. So it is computed and printed: the second
judge's own order, and how many contenders sit in a different place in it.

It is a count and a list, not a correlation coefficient. Eight fixtures
cannot support a statistic, and a number that looks like statistics gets
quoted like statistics, the same reasoning GradeSpread records for not
becoming a confidence interval.

It is also a COMPARISON, and so it is subject to the same rule the deltas
are: two orders built from different stimuli do not disagree, they answer
different questions, and "3 of 6 contenders sit in a different position"
would read as judge disagreement while measuring the change of question.
So a contender whose two judges did not score the same finding lists
suppresses the line entirely, and the report says which contenders and
why. Printing the order for the rest would be worse than printing none:
an order over a subset of the rows is not the order of the table.

## counts

`internal/evals/tune_test.go`

The columns are rates because contenders are measured different numbers of
times and raw sums are not comparable across that, but a rate hides its
own resolution, and these denominators are single digits. PREC 0.74 and
PREC 0.67 read as a difference until you are told they are 17/23 and 2/3,
at which point the second is one comment away from 1.00 and the comparison
is not one. The methodology gate called this the cheapest high-value item
available to this harness and it is: the numbers were already here, and
nothing printed them.

Below rather than inside the row because the columns are fixed-width and a
count pair outgrows its cell the moment a contender files a hundred
findings, at which point the table silently misaligns, which is a defect
this file has already had once.

Rendered by CrossJudged.Denominators, which prints both judges' counts,
rather than formatted here. Formatting them here is what would put this
report back in possession of the raw judged counters, the state that
makes half a result printable, and it would also have published one
judge's denominators under a table of two judges' figures.

## judged

`internal/evals/tune_test.go`

It is deferred rather than written out on both branches so that no future
branch can be added without it, and registered after `defer mu.Unlock()` so it
runs while the lock is still held: it appends to notes. It is registered after
the review-error return above, which is
deliberate and is the one path that must not record: there are no
findings there, and Record writes a `silent: true` line for an empty
list, a review that never ran would go into the file as a reviewer
that said nothing. TestEveryPaidReviewIsRetainedWhateverTheJudgeSays
draws the line in exactly that place.

## corroborateVariants

`internal/evals/tune_test.go`

The notes matter as much as the aggregates. A second judge that failed on two
fixtures produces a delta over fewer samples than the figure beside it, and
dropping the note that says so leaves a row whose N cell reads "8/6" with
nothing anywhere explaining the six. Both axes report per variant, and
Corroborate keys by contenderLabel, so the notes are translated back the same
way the aggregates are looked up.

## evidence ordering

`internal/review/evidence.go`

`mine` answers true in two cases, for different reasons, and neither is a claim
about which entry produced the finding.

An empty `Hit.Path` is the batch query, which retrieves once for a whole batch
of files. That is the default (`review.knowledge_query: batch`, what shipped
and what was measured), and under it every finding in a batch carries the same
entries, so the ordering does nothing at all.

A matching path is the per-file query. There `knowledge.Merge` has already
deduplicated by entry id keeping the highest-scoring hit, so `Hit.Path` names
the file that retrieved the entry most strongly rather than a file that
retrieved it. An entry that `a.go` pulled in sorts last for an `a.go`
finding when `b.go` scored higher on it.

What the field is for is stated at `Finding.Evidence`: the reviewer read these
entries when it wrote this finding. The ordering is a preference, not evidence
of attribution, and no consumer treats it as more than that.

## naming a citation

`internal/review/validate.go`

`namesSomething` gates the demotion, and the demotion is the strong step: it
publishes a finding stamped as having had its verdict rest on a source the
expert never saw. That is a claim about the expert, in a place a reader cannot
check it, so it is worth making only where the expert did claim something.

One token, not among the entries it was shown, is that case. A sentence is not.
A model asked for an id and answering "no specific entry" was answering in the
wrong form, not inventing a source, and reading it as one demotes a sound
refutation and publishes a false line about the expert.

The earlier rule tried to decide whether a string means nothing, as a list of
nullish phrasings. Three rounds of review found a form the list was missing:
the bracketed id the prompt itself asks for, an id spelled with spaces, and
then ordinary prose. Whether an arbitrary sentence means nothing is not
decidable from the string; whether it is one token is. The list survives as a
backstop for the single words, `none` and `n/a` among them, where the shape
test alone would say a source was claimed.
