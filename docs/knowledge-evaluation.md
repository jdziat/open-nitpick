# Evaluating retrieved knowledge

The pre-registered protocol for issue #84, written before the run.

What exists today is one corpus and one result: six seeded plants and six
controls on `z-ai/glm-5.3-flash`, recall 0.75 to 1.00, noise 0.50 to 0.33 per
review ([Findings](findings.md#retrieved-knowledge-and-a-pre-registration-i-got-wrong-2026-09-08)).
Every plant has a matching corpus entry by construction, and the entries were
written by the same author as the fixtures that retrieve them. It establishes
that retrieval works. It says nothing about whether the corpus covers defects a
repository actually has, which is the only question that decides whether
`review.knowledge` should ever default on.

The registration is [`internal/evals/testdata/knowledge-eval/preregistration.yaml`](../internal/evals/testdata/knowledge-eval/preregistration.yaml)
and the corpus is `selection.yaml` beside it. Both are checked by tests in
`internal/evals/knowledge_prereg_test.go`, which is the point of them being
files: a threshold moved after a disappointing run is a diff someone can see.

## What is being asked

Does retrieval raise actionable finding recall on defects nobody wrote a corpus
entry for, without raising noise on clean changes, in repositories and languages
the corpus authors did not choose?

## The corpora

Four repositories, one per language ecosystem, chosen for exercising error
handling, parser behaviour and configuration logic:

| repository | language | what it exercises |
|---|---|---|
| prometheus/prometheus | Go | cancellation, concurrency, resource cleanup, HTTP contracts, error propagation |
| encode/httpx | Python | async behaviour, exception handling, streaming, redirects, compatibility |
| typescript-eslint/typescript-eslint | TypeScript | parser edge cases, configuration merging, AST traversal, misleading diagnostics |
| rust-lang/cargo | Rust | filesystem behaviour, cancellation, resource lifetime, platform differences |

Ten independently adjudicated bug fixes per repository, giving 40 pre-fix
snapshots, 40 repaired snapshots and 20 clean changes. Real parent-to-commit
diffs against a full checkout: a synthetic diff that re-adds every file is a
different measurement wearing this one's name.

`rust-lang/cargo` is reserved as the holdout, named here rather than after the
fact. The reservation is a whole repository rather than a sample of rows,
because a defect family leaks across a row-level split.

A row belongs in the selection only if its defect can be written in one
sentence from the fix and its own discussion, not from a review of the diff. A
refactor commit is not evidence the old code was defective, and authorship is
never the label. Style and slop get a separate manually labelled slice from the
same repositories, with counterexamples.

open-nitpick's own three historical bugs stay a diagnostic corpus, with the
selection bias stated: we already know those targets.

### The selection, frozen 2026-09-08

100 rows in `selection.yaml`: 40 pre-fix, 40 repaired, 20 clean, 25 per
repository. Classes across the fixes are 29 correctness, 4 contract, 3
concurrency, 3 resource and 1 security, each counted once and reviewed twice.

The rule, applied before any tuning: merged pull requests carrying each
repository's own bug label (`kind/bug` for Prometheus, `bug` for HTTPX and
typescript-eslint) or, where the repository labels issues rather than pull
requests, a conventional `fix(` title (Cargo), merged before 2026-09-01, in the
order the GitHub search API returned them, keeping the first ten per repository
that change source and whose defect can be written from the fix and its own
discussion. Documentation and lint-warning fixes, test-only changes, feature
removals, a reverted refactor with its revert, and one output-polish change
with no stated defect were excluded on that rule.

A pre-fix row reviews the parent commit and its repaired twin reviews the fix,
so both halves are the same pair of commits read from opposite ends.

Two limits belong in the results rather than in a footnote. The fixes were
adjudicated by one reader: independent here means chosen without reference to
the knowledge corpus, which the rule enforces, and not that a second person
checked them. And a clean change is a merged refactor with no bug label and no
later revert, which is a proxy: nobody can show a change introduced no defect,
so a finding on one of these is unsupported only in the sense that the
repository's own history never recorded it.

## The thresholds

Every one names the denominator it is read against, and a test checks that
denominator against the frozen arms. That check exists because of a recorded
failure: a threshold of *"at least 3 of 12 plants"* was registered against a
corpus that turned out to hold six, so the condition could not be evaluated as
written and had to be read proportionally afterwards, which is the reading a
pre-registration exists to make unnecessary.

| threshold | read against | bound |
|---|---|---|
| plants located, on minus off | 40 pre-fix | at least 4 |
| repaired defects reported again | 40 repaired | at most 4 |
| findings per clean review, on minus off | 20 clean | at most 0.25 |
| added seconds per review, median | 100 | at most 20 |
| added dollars per review | 100 | at most 0.01 |

Four located plants rather than two: two is the smallest difference two runs of
this size can express, so a two-plant lead is a tie reported as a win. That is
Rule 6c applied to a gain rather than to a table cell.

Two repeats per arm per retrieval setting. Spend ceiling $100 for the whole
pilot including re-runs; a run that would cross it stops and says so rather
than trimming the protocol.

## What is reported

Retrieval relevance at k, separately from finding precision and recall. Controls,
repaired-bug recurrence, localization, per-class and per-language results,
latency, cost, macro averages so a large Go corpus cannot hide a regression
elsewhere, sample sizes and uncertainty. Failures and timeouts are recorded;
an incomplete review is never scored as clean.

`Report.Knowledge` carries what retrieval did on each run, and only a run whose
status is `Retrieved()` counts as the retrieval-on arm. A run whose embedder
refused its batches reviewed without retrieval, and folding it in would measure
the control twice and call the difference an effect.

## What it cannot settle

A negative result is published. `review.knowledge` stays off by default
whatever this returns; promotion is a separate decision on this evidence, and
the pre-registration records that so a good result cannot be read as consent to
it.
