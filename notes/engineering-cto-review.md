# Engineering profile adversarial review

Work in progress, 2026-09-11. This records the first review's disposition;
it is not approval of the final implementation.

The first Nitpick pass examined 38 changed files with Synthetic Kimi-K3, GLM-5.3-Flash
triage and Kimi-K3 expert validation. It retained 48 findings, including uncertain
claims, and withheld two. The pass took about sixteen minutes. Many expert verdicts
were unresolved because their supplied context omitted the other half of the
claimed integration. Model agreement is not used as a correctness verdict.

## Confirmed and corrected

- The PR callback captured the initially loaded configuration instead of using
  `report.Policy.Config`. A guard now substitutes a stricter accepted commit
  policy and verifies that config-only engineering selection remains active.
- `-linters` was overwritten by accepted engineering settings. The combination
  now fails explicitly; an external operator config can select the analyzers.
- Configured boundaries could disappear when no check was scheduled. Policy
  application now creates the required unavailable check.
- An engineering scope adjustment falsely set the config-modified flag. A
  resolver can now return a selected policy without claiming a repository edit.
- Deterministic policy loading omitted user settings, accepted misspelled root
  blocks, and validated blank rule IDs in only one path. These cases have guards.
- Glob-only catalog inputs were absent from standards coverage. Catalog entries
  now use the same matching implementation as analyzer execution.
- Standalone commit checks omitted severity-policy and exception application.
- Empty file evidence was conflated with missing evidence for exceptions.
- Design task IDs lacked a source mapping. Tasks now name their source, purpose
  and the context available in their actual batch. Finding evidence is checked
  against examined sources and context.
- Expert uncertainty, withheld decisions and already-reported findings were
  missing from the engineering adapter. These are retained, with uncertainty
  excluded from blocking promotion.

## Claims checked against implementation

The Actions incomplete-run claim is false: `PipelineComplete` rejects practice
exit code 2 before `resultFor` evaluates findings. Its new regression exercises
both required and optional incomplete checks.

Config-only engineering selection already triggers `engineeringReviewPolicy`'s
scope callback. The separate captured-config bug above was real and is fixed.
Default practice requirements apply only after profile selection; scope selection
enables slop, full source context and expert validation. Advisory model findings
are the documented initial policy, not a missing blocking assignment.

`Local.FileContent` calls `readContained` for worktree reads. The snapshot change
therefore preserves symlink containment. An empty snapshot is a valid source
selection; the engineering report, not a low-level diff function, rejects an
assessment with no substantive examined target.

Raw analyzer findings intentionally survive partial coverage; they do not enter
the convention denominator. A completed check cannot silently attribute a finding
to an unexamined target. Expiry is intentionally exclusive at midnight UTC and is
now documented in the usage guide.

Style-only naming preferences and requests for additional comments were not
adopted where they conflict with repository guidance. Local clarity fixes included
help alignment, truthful comments, independent title-target loops and placing the
engineering report before the review signature.

## Still required

Re-run the adversarial review against the final diff and inspect its evidence.
Finish the acceptance audit in `best-practice-coverage.md`, including broader
cross-file design controls, omissions and budget behavior, reproducible model
cost/variance measurements, internal CI adoption. The shared commit-check wrapper is implemented and
its valid/invalid range controls passed. Passing the development controls does not complete that audit.

## Second adversarial pass

The second pass examined 58 files in ten batches and retained fourteen findings
(three warning, five info, six nit). It finished triage and expert validation;
this is a review result, not approval.

Confirmed corrections: the usage guide now distinguishes default no-model
standards from the opt-in engineering profile; Actions expose practice counts
separately and include practice evidence in summaries; failed snapshot checks
name omitted paths and preserve cancellation; capturing an existing snapshot
no longer discards budget-omitted source; module requirements are tidy-stable.
The commit wrapper rejects multiple two-dot separators as invalid usage.

The duplicate-check guard did pass on a pre-existing empty-assessment error.
Adding a valid-control assertion exposed that flaw; the fixture now examines a
real target before duplicating the check. The publication-order guard observes
the provider's published value, and blank-rule validation is exercised through
full configuration validation.

Three claims do not justify the proposed behavior changes. A policy revision is
not an operator-selected commit range: absent `-base`, commits must remain
unavailable. Boundary patterns explicitly use `path.Match` on full Go import
paths; they do not promise recursive globs or repository-relative matching.
The engineering policy deliberately owns the gate so advisory model findings
do not inherit the generic review threshold. MCP class-filter output now says
that filtering displayed findings does not weaken this policy gate.

Later usage metering, prompt version 2, doc generation, and these corrections
still require a fresh review and final gates. The implementation remains opt-in.


## Failure exposed by live controls

Concurrent model trials and the third adversarial pass exceeded the provider's
request limit. The overlapping adversarial pass was cancelled and is not a
completed review. The fourth pass runs separately with concurrency two.

The lifecycle trial exposed an actual coverage defect: an expert request failed,
but validation returned its original finding without recording stage failure.
The engineering adapter then reported completed coverage. Validation now retains
its finding with uncertainty and returns stage failures to the engine. A paired
scripted-provider regression checks successful validation against a provider
outage and asserts both publication and pipeline completeness.


The later MCP audit found that its verdict could include practices while its
structured output omitted the practice report. It now returns the report, renders
its evidence for text clients, and names the engineering gate. A guard exercises a
practice-only failure with no inline findings. The validation-stage, duplicate,
snapshot and full-config guards each failed under their targeted mutation.

## Full pass and final corrections

The completed fourth pass examined 74 changed files and retained eight findings.
Its error about a no-op severity verdict was already corrected in `087ae4b` and
covered by the existing no-change re-rating guard. Two additional coverage bugs
were confirmed: replaying a disk capture erased its omission records, and an
empty PR source scope was incorrectly partial. Captures now replay their original
diff and coverage, and an empty source scope is inapplicable unless a stage failed.
A deletion-only integration control still examines the selected commit. Cancelled
module-context walks now retain the cancellation reason.

The claimed commit-range substitution is not reachable through policy loading:
`ReadPracticePolicy` resolves the same requested base into `BaseRevision`; that
field is not configurable YAML. Both good-case source files are present beside
the failure-propagation manifest, and the measured good trial examined three files.
The Actions exit-2 claim is again contradicted by `PipelineComplete` and its guard.
The nil-policy callback claim has no production trigger: engine validation and
policy resolution establish a nonnil configuration before assessment.

The focused nine-file pass retained two claims. Its duplicate validation-stage
notices were consolidated, retaining each failed finding's uncertainty. Missing
source still makes selected validation incomplete; the prior ability to publish
an unvalidated finding does not establish completed validation. Configuration
documentation now states this behavior. A further four-file correction passed
Nitpick with zero findings.

Five more mutations were killed: erasing unexplained uncertainty, undercounting
an outage, losing repeated-capture omissions, rejecting an empty deletion scope,
and treating an empty scope with a failed stage as inapplicable. Source was restored
after every mutation. The final coverage corrections require their own model pass.

The final nine-file coverage-fix pass completed with zero retained findings and
one expert refutation. Its empty-scope claim was checked against the deletion-only
integration control and the engine's policy-skip path. The implemented opt-in
profile has no remaining confirmed findings from these reviews. Broader mechanism
controls, package-wide planning and internal model-gate adoption remain outside
this initial release's demonstrated coverage.

## Hosted foundation review corrections

PR #113 exposed a false-positive invalid-policy guard: its zero-value review
and linter settings were already invalid. The guard now proves its control valid
before checking rejected policy, and fails when practice validation is removed.
Standalone configuration-reference sources again work without a sibling commits
package. The accepted policy loader honors the operator's unknown-key setting,
keeps ignored keys visible in provenance, preserves cross-block YAML aliases,
and still rejects invalid known values and duplicate blocks.

Build constraints and package-pattern comments describe intended behavior:
selected Go sources contribute direct imports across platforms, and path.Match
does not give an exact package name an implicit recursive match. A build-tagged
source control pins that contract.

A stronger Actions guard exposed a narrower completion bug than the earlier
review claimed: optional practice failures still inherited the ordinary pipeline
gate. Engineering completion now follows required practice evidence; ordinary
reviews retain their stage-failure gate. Four targeted mutations failed the
policy-validation, malformed-value, supplemental-documentation, and optional
completion guards. The corrected targeted suites pass.

The subsequent Nitpick pass examined eleven changed files and returned zero
findings. Deterministic slop flagged one existing oversized engine comment,
which was shortened, and cadence in the generated configuration reference.
