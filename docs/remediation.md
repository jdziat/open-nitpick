# Remediation plan: the misses on the benchmark repository

Written against the first end-to-end run on `jdziat/nitpick-bench`
(2026-09-03): 44 pull requests, 33 of 41 plants located, one noise finding,
beside Incumbent's hosted app at 30 of 41 and five. Every miss was read from
the pull request it happened on — the walkthrough this tool posted, the
inline comments, and the analyzer roster — and each workstream below names
the miss it answers, what the evidence says caused it, the change, how the
change is measured before it ships, and what a pass looks like. The rules in
[measurement.md](measurement.md) apply throughout.

## What the misses actually are

Reading the posted reviews, the eight misses fall into four causes, and the
one this project would have guessed — the model did not see the defect — is
the least of them.

| miss | what the review said | cause |
|---|---|---|
| cross-file-copy-nit | "The single reported item — that Build's copy … is redundant — was dropped: it is an info-level nit with no concrete defect" | **triage dropped a correct finding** |
| cross-file-sort-nit | "No findings survived triage: the reported double sort is a harmless redundancy" | **triage dropped a correct finding** |
| go-package-singleton | "the single reported concern about testability of the global was speculative" | **triage dropped a correct finding** |
| ruby-default-page-size | walkthrough: "callers relying on the previous default will see the new page size" — no finding | **the reviewer saw it and did not file it** |
| kotlin-widened-input | walkthrough names the risk, "raised no concerns on that front" | **the reviewer saw it and did not file it** |
| retry-no-backoff | walkthrough: "with no delay between attempts" — the one finding filed was about HTTPError | **the reviewer saw it and filed something else** |
| rust-crate-for-one-call | walkthrough: "adds a new fixture … for validating tooling behavior" | **the benchmark told the model it was reading a test fixture** |
| sorted-for-min-nit | "adds a fixture that determines the coldest sensor reading" — no finding | **same, plus a nit the reviewer did not rate worth filing** |

Three of eight were found by the reviewer and thrown away by triage. Three
were noticed in prose and never became findings. Two were reviewed as test
scaffolding because the repository put them under `fixtures/` with a pull
request titled `fixture: …` and a body that said "eval corpus". Only one,
arguably, is a model that did not see the defect.

Beside the misses, the run showed two things the hosted incumbent does that
this tool did not on that runner: it ran golangci-lint (its one Go noise
finding was an `errcheck`; ours reported "golangci-lint is not on PATH" on
every Go pull request), and every one of its comments carried a proposed fix
as a diff and a committable suggestion.

## Workstreams, in the order to do them

### 0. Stop the benchmark lying to both reviewers

**Answers:** rust-crate-for-one-call, sorted-for-min-nit, and an unknown
share of every other result, on both sides. Incumbent's retry comment
literally says "If this is only a fixture, keep it out of production request
paths."

**Change.** `cmd/benchrepo` lays fixtures out under a neutral root
(`services/<name>/`), titles each pull request as the change would be titled
in a real repository (`Add roster rendering`, `Retry transient failures`),
and writes a body of one sentence in the author's voice with no mention of
fixtures, corpora or benchmarks. Fixture names that say what is planted
(`retry-no-backoff`) never reach the branch name either: branches are
`change/<n>`. The mapping from pull request to fixture lives in the scorer,
read from a file `benchrepo prs` writes locally.

**Measure.** Re-run all 44 with both reviewers. This is the new baseline;
nothing else in this plan is measured against the old one.

**Pass.** No walkthrough on either side describes a change as a fixture,
test scaffold or benchmark. Effort: half a day.

### 1. Triage may not drop a finding for being small

**Answers:** cross-file-copy-nit, cross-file-sort-nit, go-package-singleton
— three of eight, and the class of every future `nit` and `info` plant.

**Cause.** [triage.md](../internal/prompt/templates/triage.md) rule 2 ends
"When in doubt, drop it: a false positive costs more than a missed nit."
The triage model took it as written. The nitpick level is a post-hoc filter
on published findings precisely so that generation and triage never have to
decide what is worth the reader's time; triage deciding it anyway makes
`persona.nitpick: pedantic` a setting that publishes nothing extra.

**Change.**
- Rule 2 becomes: drop a finding only when its rationale names no
  consequence, or asserts something about code that was not shown. A finding
  whose consequence is small is re-rated, never dropped; the nitpick filter
  decides whether the reader sees it. The "false positive costs more than a
  missed nit" sentence goes.
- Triage's walkthrough may no longer explain a drop, because there will be
  no drop of that kind to explain; a dropped finding is recorded in
  `Report.Overruled` with the triage model as the expert, so it lands in the
  "reported, then withheld" block and is visible on the pull request — the
  same disclosure the domain-expert pass already gets.
- A unit test scripts a triage model that returns fewer findings than it was
  given without an overrule reason, and asserts the engine restores them.

**Measure.** `make quick` before and after on the tuning corpus (three of
its plants are the affected class), then sonnet on both corpora. The number
to watch is the `nit` and `info` bands of the located table and NOISE.

**Pass.** Located `nit` plants rise; noise per review does not rise by more
than the corpus resolution. Effort: one day including measurement.

### 2. What the walkthrough noticed becomes a finding

**Answers:** ruby-default-page-size, kotlin-widened-input, retry-no-backoff.

**Cause.** The reviewer wrote the consequence into its prose and did not file
it, twice for `info` plants and once for a `warning`. [review.md](../internal/prompt/templates/review.md)
sets the bar as "an input that produces a wrong result, a state that
deadlocks or panics, a request that leaks data, a path that loses an error",
and a changed default page size, a widened input type, or a retry loop with
no delay produces none of those on the happy path. The `info` anchor
("a defensible concern the author should consciously accept or reject") is
defined but the bar above it reads as a gate that `info` cannot pass.

**Change.**
- The bar in review.md is restated per level: `critical`/`error` need the
  demonstrable failure; `warning` needs the plausible condition; `info` and
  `nit` need a named cost the author would want to decide about — a caller
  that will see different behaviour, a dependency for one call, a resource
  bounded by nothing in the change. The examples are kept away from anything
  planted (`TestNoPlantedKeywordAppearsInTheShippedPrompt` guards this).
- A closing instruction: anything the reviewer would write into a summary as
  a consequence of the change is a finding, at the level the consequence
  earns; a summary is not a place to park an observation.
- For retry-no-backoff specifically, the "Independent defects" section gains
  one sentence: a change with two defects gets two findings, and the second
  is not displaced by the first being more interesting.

**Measure.** Tuning corpus with `make quick` for the `info` band and noise;
then held-out is NOT re-spent — this is exactly the prompt change Rule 14
was written for, and the held-out corpus has been spent twice already. A
third corpus of `info` plants is authored first (see workstream 6) and the
change is measured on that.

**Pass.** `info` located rises on the new corpus with noise inside
resolution. Effort: two days including the corpus.

### 3. The Action brings its analyzers

**Answers:** "golangci-lint is not on PATH" on every Go pull request and
"ruff is not on PATH" on every Python one; the one thing the hosted product
did on this run that this tool could not.

**Change.**
- An `analyzers` input on the Action: a list of catalog names, or `auto`,
  which installs the analyzers whose languages appear in the pull request's
  diff. Installation is per tool in the composite (`setup-go` +
  `go install` for golangci-lint, `pipx` for ruff and yamllint and sqlfluff,
  `apt` for shellcheck and cppcheck, release binaries for hadolint, gitleaks,
  actionlint, zizmor, tflint, checkov), pinned to versions the catalog names,
  cached with `actions/cache` keyed on the version.
- The roster line changes from "did not run: not on PATH" to "not installed;
  set `analyzers` to install it", so the reader learns the remedy.
- Default stays off: a workflow that installed nine toolchains without being
  asked is a slow workflow nobody asked for.

**Measure.** The benchmark run's roster: every Go and Python pull request
shows golangci-lint and ruff as ran. The `errcheck` finding Incumbent posted
appears in ours.

**Pass.** Analyzer coverage on the benchmark equals the hosted product's for
the languages in it. Effort: two days.

### 4. Proposed fixes and committable suggestions

**Answers:** nothing in the located table; the comment-for-comment
comparison, where every Incumbent finding carries a diff and a committable
suggestion and ours carries a sentence.

**Change.**
- The finding schema gains `fix`: a replacement for an inclusive line range
  `[line, end_line]` within the same file, with the model told to leave it
  out unless the replacement is exact and complete. Single-line stays as it
  is.
- The engine validates every `fix` before it is rendered: the range is
  inside one hunk of the file's diff (GitHub rejects a multi-line suggestion
  that is not), the replacement is not empty, and it differs from the lines
  it replaces. A fix that fails validation is dropped from the comment and
  the finding is published without it; nothing corrupts a file.
- Rendering uses GitHub's `start_line`/`line` pair with a `suggestion` fence,
  which is the committable form; the local renderer prints it as a diff.
- The eval harness's invariant that "no suggestion is published as
  one-click-applicable unless it is a single line" becomes "unless it
  passes the validation above", and a fixture with a planted multi-line fix
  is added so the invariant is exercised.

**Measure.** Judged, by necessity — whether a fix is right is not a keyword
question — but with the judge's known instability priced in: two judges, and
only the per-finding "is this fix correct" question, which was the stable
one (98% agreement on "is this claim true").

**Pass.** Fixes are correct in ≥ 90% of judged cases; a fix is never
rendered as committable when validation fails. Effort: three days.

### 5. Anchor tolerance is a scoring question, not a reviewer one

**Answers:** nothing of ours; Incumbent was scored 2 of 3 on multi-defect
for anchoring the descriptor leak at the `return` rather than the
`os.Create`. Recorded here because a plan that only fixes our side is a
plan for the number rather than the comparison.

**Change.** The scorer's anchor tolerance stays at four lines — a finding
fifteen lines away IS elsewhere — but the benchmark report gains a column,
"located out of tolerance", that counts findings whose keywords match a
plant in the same file beyond the tolerance, for both sides, so a near miss
is visible rather than folded into noise. Effort: an hour.

### 6. A corpus for the band nobody finds

**Answers:** the four `info` plants that no reviewer — ours, the hosted
incumbent, kimi, glm — has ever located on any run, and the inability to
tell whether that is the plants or the reviewers.

**Change.** Ten `info` plants, each a change whose consequence a senior
reviewer would name in one sentence and want the author to decide about,
in the languages the corpora already cover, authored to the restated bar in
workstream 2 and validated by the multi-file corpus's own ground-truth test
(they join that corpus's registry, not `AllFixtures`). Two are clean controls
at the same level of subtlety.

**Measure.** Workstream 2's prompt change is measured here first.

**Pass.** Either reviewers locate a majority of them — in which case the
band was findable and the existing four are re-examined — or they do not,
and the four are retired from every table with that written down. Effort:
two days.

## Order and gates

| step | depends on | gate before the next |
|---|---|---|
| 0 benchmark layout | — | both reviewers re-run; new baseline recorded in comparison.md |
| 1 triage keeps small findings | 0 | `make quick` and sonnet on both corpora; nit band up, noise inside resolution |
| 5 out-of-tolerance column | — | report reproduces from a dump |
| 3 Action installs analyzers | — | benchmark roster shows analyzers ran |
| 6 info corpus | — | corpus test green; both reviewers scored on it |
| 2 walkthrough observations become findings | 1, 6 | measured on 6; not on held-out |
| 4 committable fixes | 1 | judged fix correctness ≥ 0.90; validation test green |

Steps 5, 3 and 6 have no dependencies and run beside 1. Nothing here
re-spends the held-out corpus, and nothing here is tuned against the
benchmark repository: it is re-run to confirm, not iterated against.

## What this plan does not do

It does not chase the incumbent's remaining advantages that are product
surface rather than review quality — chat commands, learnings, forges other
than GitHub — and it does not add a judge to the benchmark scorer. It also
does not promise that the four `info` plants will be found: workstream 6 is
written so that "nobody can find these" is an acceptable answer, recorded,
rather than a number left on the page.

## Status, 2026-09-03 evening

Every workstream is implemented. What each did when measured, on the
corpora named in its gate, sonnet-4.6 with related context unless said
otherwise; one run per table, Rule 15 throughout.

| step | done | measured |
|---|---|---|
| 0 benchmark layout | yes — `services/`, engineer-written titles, opaque branches, answer key on our side | re-run: ours 33/41 again; Incumbent's app throttled at 8 of 44 reviews after the earlier batch, so its column waits |
| 1 triage keeps small findings | yes, on the second attempt (below) | tuning 0.88 → 0.88; multi-file 1.00 → 1.00 |
| 2 noticed consequences become findings | yes | info corpus 0.20 → 0.55–0.65 (4 → 11–13 of 20) |
| 3 Action installs analyzers | yes — `analyzers: auto` | not yet exercised on the benchmark repository |
| 4 committable multi-line fixes | yes, validated against the diff | judged pass not run |
| 5 near-miss column | yes | in every table above as NEAR |
| 6 info corpus | yes — ten plants, two controls | see 2 |

**Workstream 1 took two attempts, and the first was a regression.** The
first version let triage list a dropped finding with a reason. Triage then
dropped a correct milliseconds-versus-seconds finding as "the rationale
contradicts itself" and a correct redundant copy as naming "no concrete cost
beyond a future reader" — which is the cost. sonnet's multi-file recall fell
from 1.00 to 0.88 and tuning from 0.88 to 0.75 in one run. A reason channel
is a rationalisation channel. The second version lets triage merge duplicates
(naming the survivor) and re-rate, and never drop; everything else it leaves
out is restored. Recall returned to 1.00 and 0.88.

**What that costs.** Findings triage used to remove as speculative are now
published as `nit` when the prompt is obeyed and as `warning` when it is not.
On the multi-file corpus sonnet's noise per review went from 0.14 to 0.29
at the shipped `min_severity: info` (0.36 counting nits); on the tuning
corpus it did not move. Five of the ten extra findings are guesses about
unshown code the third prompt revision rates `nit`; the rest are secondary
observations on the fixtures. The plan's gate — noise inside the corpus
resolution — is not met on multi-file, and that is recorded rather than
tuned away.

**The info band was the reviewer, not the plants.** glm-5.3-flash located
14 of 20 on the info corpus before and after; sonnet went from 4 to 11–13.
sonnet's triage had been deleting what its reviewer found. Incumbent's CLI
locates 4 of 10 with no noise. The four original `info` plants stay in their
tables: two runs of sonnet on the tuning corpus since the change locate
`go-package-singleton` in one run and not the other.

**Not re-spent:** the held-out corpus. **Not run:** the judged pass for
workstream 4, and the benchmark re-run under the new code, which is next.

## The benchmark re-run under the remediated code (2026-09-04)

All 44 pull requests re-reviewed by the Action at the new `v1`, whole review
each, `analyzers: auto`, `min_severity: nit`; each reviewer's latest review
scored, so earlier reviews on the same pull request do not count.

| | before (2026-09-03) | after |
|---|---|---|
| plants located | 33 of 41 | **36 of 41** |
| of the eight misses, recovered | — | cross-file-copy-nit, cross-file-sort-nit, sorted-for-min-nit, retry-no-backoff |
| still missed | — | go-package-singleton, kotlin-widened-input, ruby-default-page-size, rust-crate-for-one-call |
| noise findings over 44 | 5 | 17 |
| inline comments | 38 | 53 |

Three of the four recovered misses are the triage drops workstream 1
answered; the fourth is retry-no-backoff, which workstream 2 answered. The
four still missed are the original `info` plants, on which the info corpus
now says the reviewer is capable (sonnet 11–13 of 20) and these four
particular plants stay hard: two runs of the tuning corpus since the change
located `go-package-singleton` once.

**The noise is mostly the analyzers, and it is honest noise.** Of the twelve
extra noise findings, nine are analyzer output that `min_severity: nit`
publishes: biome's "template literals are preferred" on three TypeScript
lines, sqlfluff's capitalisation rule and an "unparsable SQL" on a valid
migration, golangci-lint's `errcheck` on an ignored `Fprintf`. The hosted
incumbent posted the same `errcheck`. Two of those are now fixed at the
source — sqlfluff's parse failures are configuration and are no longer
findings, and biome's shipped config drops its style group — and none of the
nine would be published under the shipped `min_severity: info`. The other
three are the reviewer's: two `warning`s about unshown code that the third
prompt revision rates `nit`, and one on the Ruby mailer fixture.

**The incumbent's number is its first full run.** Incumbent's app reviewed
all 44 once before its plan throttled it and reached about half of the
re-opened ones. Its first run — 30 of 41, 5 noise — is the comparison
figure; see comparison.md.
