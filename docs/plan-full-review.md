# Plan: full-review, model identification, repo-score

Branch `feature/full-review`, opened 2026-09-05 from main at fc8311e. Three
commands, in the order they should be built, each with the acceptance it
must meet before the next starts. Nothing here is implemented yet.

## 1. `nitpick full-review [path...]`

Status 2026-09-05: the command exists (`cmd/nitpick/fullreview.go`,
`internal/vcs/tree.go`). It reviews the tree as one all-additions change
through the unchanged engine, with `-budget`, path arguments, the
remediation plan and the coverage notice. Smoke-run on `internal/diff`
with Kimi-K3 on Synthetic. The output now has the grouped sections
(known advisories from the scanner, listed not judged, with any the
review set aside; security risks; bugs), the remediation plan and the
coverage notice, and the tree describes itself to the summarizer as a
review of a repository, not a change. The fixture repository
(`internal/evals/fixtures_fullreview.go`: a planted bug, a planted secret,
a dependency with a known advisory, a file of slop, a clean control) and
its acceptance test (`make eval-fullreview`, `TestFullReviewFixture`)
exist. First run, Kimi-K3 on Synthetic, 2026-09-05: both plants located,
the control silent, the secret first in the remediation plan, and the
advisory named by the model from `go.mod` even without osv-scanner
installed; the advisories section itself is unverified until the scanner
is installed on the machine that runs the test. One run, same-day
fixture, Rule 15 applies. Section 1 is done; the report helpers live in
`internal/fullreview` so the test and the command share them.

Review a whole repository, or the paths given, rather than a change. The
engine already reviews batches of files with analyzers as evidence and
related context attached; a full review is that engine run over the tree
with no diff, and a report that groups by what was found.

**Output sections**, each empty when nothing was found and saying so:

- Bugs: correctness findings, as today, anchored to `path:line`.
- Security risks: the security class as today, plus every `semgrep`,
  `gitleaks`, `zizmor` and `bandit` finding the roster produces, triaged.
- CVEs: `osv-scanner` over every manifest the tree holds, with the
  affected package, version, advisory id and the fixed version, not
  triaged by the model (a CVE is deterministic evidence).
- AI slop: findings in a new class `slop`, defined below, so a reader can
  switch it off with `min_severity` or `ignore` like any other class.
- Remediation plan: an ordered list, most severe first, grouping findings
  that share a fix, each with the files it touches and an estimate in
  files changed rather than hours.

**Scope control**: `--paths`, the existing `review.ignore` globs, and a
`--budget` in tokens that stops the walk and says how much of the tree it
covered, in the same voice the caller-walk notice uses: silence must not
read as clean.

**Acceptance**: a fixture repository under `internal/evals/testdata/full/`
with planted bugs, one planted secret, one dependency with a known
advisory, and one file of planted slop; the report finds each, the clean
files produce nothing, and the remediation plan orders the secret first.
Judge-free, with the keyword rule the callers corpus uses.

## 2. The `slop` class

Status 2026-09-05: built. `config.ClassSlop`, the `review.slop` switch
(`internal/config/config.go`), the prompt layer `prompt.SlopGuidance`
with the nine rules and their exclusions, a dedicated expert
(`experts/slop.md`), the gate that publishes the class by the switch
alone, and the corpus `SlopFixtures` (five planted/control pairs,
`make eval-slop`). The measurement is in `docs/findings.md` once the
first run is recorded.

"AI slop" has to be defined before it can be scored, and defined as things
a reader can check, not as a feeling. Working list, each a finding with a
line:

- A comment that restates the line below it.
- A docstring or comment that describes behaviour the code does not have.
- Dead code left beside its replacement; an unused import or variable a
  linter already flags is evidence, not a second finding.
- Defensive checks against conditions the types exclude.
- A try/except or recover that swallows the error and continues.
- Generic naming (`data`, `result`, `helper`, `utils2`) where the file's
  own vocabulary has a specific word.
- Boilerplate repeated three or more times where the language has the
  abstraction (a loop, a helper, a generic).
- Prose in the code that addresses the reader as a chat reply ("Sure!
  Here's", "Note that", "This function will").
- A test that asserts nothing, or only that the code ran.

Each of these is a prompt rule with a positive and a negative example, and
each gets a fixture pair like the callers corpus: one planted, one clean
control that looks similar. The class ships off by default until the
controls are silent.

## 3. `nitpick repo-score`

Status 2026-09-05: built (`internal/fullreview/score.go`, the command in
`cmd/nitpick/fullreview.go`). Three numbers per language with their
denominators; `SlopThreshold` 2.0 weighted findings per thousand lines;
acceptance `TestRepoScoreFixture` under `make eval-fullreview`. First
run, Kimi-K3 on Synthetic: with the slop file 57 weighted slop findings
per thousand lines (5 findings over 70 lines), without it 0.00, both
sides of the threshold; the full-review acceptance passed in the same
run with the slop class on.

A number from `full-review` with the `slop` class on: slop findings per
thousand lines, weighted by severity, reported with the count and the
denominator beside it so the number is never read alone. Plus the same
for bugs and security, so a repository is three numbers, not one. Rule 1
of `docs/measurement.md` applies: the score is judge-free, computed from
findings the keyword rule can credit, and it is reported per language.

**Acceptance**: the fixture repository scores above a threshold with the
planted slop and below it with the slop files removed; a known-clean
repository (this one, at a tagged commit) scores below it.

## 4. `nitpick identify-model`

Status 2026-09-05: the experiment exists (`internal/modelid`, corpus under
`internal/evals/testdata/modelid` from `cmd/modelid-corpus`): six models
from the sweep, twelve tasks, three languages, plus a human control from
the Go and Python standard libraries (TypeScript has no offline human
source here, and says so). `TestModelIdentificationExperiment` prints the
confusion matrices for two task splits and the verdict against
`GoMargin` 0.15 over the majority baseline. The command is built only
on a go.

Which model wrote a file. This is the one command whose premise needs a
measurement before code: it is not known that current models leave a
stylistic signature that survives a human edit, and a confident wrong
attribution is worse than none.

**Step 1, the experiment, before any product code**: a corpus of files
generated by each candidate model (the twelve from the sweep, plus a
human-written control set from this repository's history before any model
was used), same prompts, same languages. A classifier over cheap features
(comment density and phrasing, naming, error-handling shape, import order,
line length distribution, the slop rules above as features) trained on
half and measured on the other half, per language. Publish the confusion
matrix in `docs/findings.md`.

**Go/no-go**: ship only if held-out accuracy beats the majority class by
a margin that survives Rule 15, and then only as "most similar to", with
the confidence shown and "unknown" as the default answer. If the
experiment fails, the finding is published and the command is not built.

## Decisions, taken 2026-09-05

1. `slop` is a finding class in pull request reviews too, **off by default**
   in the review; it may be switched on once it is built and its controls
   are seen to be silent. It is always on inside `full-review` and the score.
2. `full-review` supports a small `--budget`, but the default is the big
   lift: the whole tree, the model on every batch, analyzers as evidence.
   A small budget runs what it can and the report says what it did not
   cover, in the caller-walk notice's voice.
3. The model-identification corpus spend is approved. Step 4's experiment
   may start once 1 through 3 are built.
4. The repository stays private until this plan's steps are done.
5. Related context was split on main (`related_context` on,
   `related_context_callers` off) before this branch starts; `full-review`
   turns both on by default, since a whole-tree review has already read
   every file.

## Order

1 (engine and report), then 2 (the class and its corpus), then 3 (the
score, which is 1 + 2 with a denominator), then 4's experiment, then 4's
command if the experiment passes.
