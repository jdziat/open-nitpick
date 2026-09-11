# Best-practice coverage in Nitpick

Status: implementation in progress, 2026-09-11, on `feat/engineering-profile`.
This document remains the acceptance contract; it does not attest that every
target below is implemented or validated. This extends the standards work
merged in PR #112.

## Outcome

Nitpick should answer two different questions: which conventions a repository
demonstrates, and which engineering practices the selected checks evaluated.
The result must call out nonconforming commits, misleading or useless code and
prose, and design problems with concrete consequences. A common pattern is not
automatically a good practice, and a successful model request is not evidence
that an architecture is sound.

Keep the current convention measurements. Add a best-practice report that names
each check, its evidence, what it examined, what it omitted, and what can fail
CI. Do not combine these into a repository quality score or a claim of complete
best-practice compliance.

## Existing pieces and gaps

| Area | Existing implementation | Missing capability |
| --- | --- | --- |
| Conventions | `internal/standards`, `repo-standards`, language linters | Explicit policy alongside inferred conventions |
| Slop | `internal/slop`, `cmd/nitpick/slop.go`, nine model rules and slop expert | Shared coverage, severity and CI policy in standards reporting |
| Commits | `scripts/check-commits.sh` | Portable validation, commit targets and range coverage |
| Design | Review engine, related context and `experts/design.md` | Dedicated assessment scope and cross-file coverage; the expert currently judges an existing claim |
| Tests and security | Review classes, analyzer adapters, eval fixtures | Evidence that distinguishes inspected code, executed checks and unassessed risks |

Reuse these instruments. The current `standards.Source` counts file observations;
it cannot represent commit subjects or justify architectural conclusions.
Do not force those into its denominator or run another independent model review
for every category.

## User experience

Proposed entry points:

```sh
# Existing behavior remains available and remains the default.
nitpick repo-standards -check

# Evaluate the engineering profile over the working tree and a commit range.
nitpick repo-standards -profile engineering -base main -check -json

# Run the same selected checks over a pull request's change and related context.
nitpick review -profile engineering -base main

# A fast, narrow interface to the same commit validator.
nitpick commits -base main -check
```

For local commands, `-base` defines the commit range. `repo-standards` still
assesses the whole selected tree; `review` assesses the change plus named
dependencies. The report states this distinction. A tree assessment without a
commit range leaves the commit check unconfigured, not passed. PR mode derives
the range from pinned forge metadata.

The engineering profile requires deterministic convention, commit and supported
language-linter checks. It also requires completion of the slop and design
assessments, initially with advisory model findings. `-no-model` remains useful
for local inspection but cannot satisfy a profile requiring those assessments.
Existing commands do not start charging for model calls merely because the
binary was upgraded.

Example output, illustrative rather than a measurement of this repository:

```text
Engineering profile: incomplete; 2 policy violations
Conventions       completed     46 applicable sites, 0 violations
Commits           completed      7 commits, 1 violation
Slop tells        completed     12 files, 1 policy violation
Slop assessment   completed     12 files, 1 advisory finding
Design assessment partial        2 of 3 planned units; budget exhausted
Security          not selected   dependency advisory scan not requested

commits.subject-format  8a14c22  "updates" lacks the required type prefix
slop.chat-reply         src/api.py:18  comment contains reply scaffolding
design.hidden-state    src/cache.go:41 + src/service.go:76
  Cache behavior depends on initialization order absent from its API contract.

Required coverage missing: design unit "cache lifecycle".
```

Each item offers a specific correction. Commit findings suggest a conforming
subject but never rewrite history. Design findings describe the smallest useful
change and its tradeoff; they do not offer an automatic multi-file refactor.

## Policy and trust

Observed conventions stay descriptive. An explicit required rule stays required
even if most of the repository violates it; it cannot retire through declining
adherence. Selecting a profile does not silently promote every observed style
into a blocking rule.

Resolve profiles, required checks, severity thresholds, exclusions and exceptions
using the existing trusted-policy mechanism. Pin base and head SHAs before
enumerating inputs. A change cannot weaken its own review by modifying its
configuration, AGENTS file, commit text, design rationale or exception list.
In tree mode, identify whether policy came from an operator file, a pinned
accepted revision or defaults. Report fallback policy explicitly.

The proposed `practices` configuration contains profile selection, per-check
completion requirements and failure thresholds. Check IDs are stable and
versioned separately from their wording. Start with typed built-in settings,
not arbitrary shell hooks or a user-defined rules language.

Exceptions identify a rule and bounded target, with reason and optional expiry.
Render them separately from conforming results. Expired exceptions stop applying.
A finding does not disappear because its text changes. Existing violations can
be grandfathered by an accepted baseline with stable evidence fingerprints;
CI checks additions and regressions while tree reports retain the debt.
Changes to targets or rule versions invalidate affected baseline entries.

Repository text remains untrusted evidence for models. Use existing secret
filtering, provider configuration and analyzer isolation. Do not run project
tests, install dependencies, or execute commit hooks to infer best practices.
Test execution and mutation runs belong in explicitly configured CI jobs.

## Coverage and findings contract

Add `internal/practices` to compose existing results and commit validation.
Keep orchestration out of `internal/standards` to avoid a dependency cycle with
the review engine. Extend `RepoStandardsResult` additively with a versioned
`practices` object; preserve existing JSON fields and command defaults.

Each check result records:

- Check ID and version, instrument (`deterministic` or `model`), required status,
  policy source/digest, source revision or worktree content digest.
- Execution state: `completed`, `partial`, `unavailable`, `failed`,
  `not_selected`, or `not_applicable`, with a reason.
- Planned, examined and omitted targets, using the appropriate unit: commits,
  files, declaration sites or architecture units. Do not sum unlike units.
- Findings, advisory signals, accepted exceptions and retained raw evidence.
  A completed check with zero targets requires an applicability explanation.
- Tool/model and prompt versions, elapsed time, usage/cost when available,
  failed stages and truncation details.

Keep execution state separate from evaluation: a completed check can find
violations. Models produce assessments, not percentages of good design.
Architecture coverage means completion of declared review tasks over available
context, not coverage of every possible design defect.

Introduce typed finding targets: file locations, commit SHA, PR title, or
architecture unit with multiple file anchors. Commit findings belong in the
summary and machine-readable report; never invent a file line to publish them.
Reuse `review.Finding` for file evidence through adapters rather than changing
its model-facing schema to accept arbitrary target objects.

Adapt analyzer findings before `standards.Observation` strips their message and
severity; keep the existing observations for convention measurements.
Keep detector provenance when deduplicating. A linter and model naming the same
defect produce one finding with both sources; a model cannot suppress a required
deterministic violation. Findings dropped by anchoring or limits remain visible
as unpublished evidence. Cross-file design findings without a changed-line
anchor go in the PR summary with links, not into an invented inline location.

For `-check`, retain exit 0 for satisfied selected policy, 1 for violations, and
2 for incomplete required evaluation or invalid invocation. Incompleteness wins
when both apply, while JSON and text retain both facts. A model timeout, failed
validation stage, absent linter or exhausted required budget cannot pass.
Nonrequired failures remain visible but do not independently fail the profile.
If no substantive selected check examined an applicable target, report no
assessment and exit 2 rather than passing an empty profile.

## Commit conventions

Implement a pure Go validator in `internal/commits`; keep Git/forge enumeration
in `internal/vcs`. Parse structured, NUL-delimited output using argument arrays.
Resolve refs to SHAs first, and handle invalid refs, missing history and shallow
clones explicitly. Never substitute HEAD-only inspection for an unavailable
range or silently fetch history.

Check commits reachable from head but not the selected base, including side
branches. Preserve this repository's allowed Conventional Commit types,
optional scope/breaking marker, nonempty subject and existing 72-character
post-colon limit as its policy, not as a universal standard. Freeze Unicode
length semantics and the treatment of `fixup!`, `squash!` and reverts in tests.
Default policy reports fixup/squash subjects until an accepted policy permits
them. Reverts follow the selected subject rules.

Detect merge commits from parent count, not a subject beginning `Merge `.
The selected policy decides whether true merges are exempt. For squash-only
projects, check the intended squash title through an explicit PR-title rule;
a title pass cannot pretend every source commit conformed. Repository settings
alone do not prove what the eventual squash subject will be.

An empty valid range is `not_applicable` with zero enumerated commits. A missing
range is incomplete when commit coverage is required. Tree scans never lint all
historical commits by default. Replace this repository's shell regex with the
shared CLI only after compatibility fixtures pin its accepted and rejected forms.

## AI slop

Report concrete defects regardless of authorship. Do not infer which model or
person wrote a file. Reuse deterministic tells and the existing nine semantic
rules, including false comments, ineffective tests, swallowed failures and
redundant scaffolding.

Tells such as punctuation, long comments and filler are signals by default.
They become blocking style rules only through explicit policy. Densities remain
diagnostic: adding clean lines must not dilute away a blocking finding. A
behavioral defect found by the slop pass keeps its consequence-based severity
and can also contribute to correctness or test evidence without double-counting.

The model must name the rule, site, consequence and relevant exclusion. Keep
legitimate controls: explanatory comments, public API documentation, defensive
checks at real boundaries, intentional recovery, and tests asserting no panic.
Generated/vendor files and quoted examples have explicit scope treatment;
excluded files are reported, and required languages cannot disappear silently.

Run slop and design within the shared engine where possible, but track which
prompts and contexts assessed each category. A generic review that
returned no slop finding does not establish that a required slop pass ran.

## Design assessment

Use a deterministic inventory to identify packages/modules, manifests, public
interfaces and available dependency edges. Start with Go package/import analysis;
for other languages, use available adapters and report unsupported graph detail.
Lexical naming support for a language does not imply architectural analysis.

Build bounded review units around changed interfaces and their callers, state
ownership, error paths, persistence boundaries and configuration. Tree mode
plans units across the selected repository; change mode prioritizes affected
units and identifies background context. Missing callers, unresolved dynamic
edges, excluded files and budget omissions stay in the report. Report the
inventory's own limits so a planner cannot claim completeness by planning less.

Assess these concrete mechanisms:

| Rule family | Evidence required | Lookalike to exclude |
| --- | --- | --- |
| Boundary violations | Forbidden import or data flow plus an accepted boundary | A preferred layering pattern with no repository contract |
| Hidden state/lifecycle | Producer, consumer and order or ownership dependency | Explicit lifecycle managed at one visible boundary |
| Coupled duplication | Two sites encoding one rule that must change together | Similar code with different reasons to change |
| Unnecessary indirection | Navigation/change cost without a current substitutability need | An interface owned by a consumer for isolation or testing |
| Failure handling | Trigger, propagation path and lost failure signal | Recovery with an explicit observable outcome |
| Contracts and migrations | Caller or stored data affected by an incompatible change | A coordinated change with verified compatibility handling |
| Ineffective tests | Behavior claimed, exercised path and ineffective assertion | A valid absence-of-panic or compile-time contract test |

Every model finding includes the mechanism, consequence, supporting locations,
smallest useful remedy, and evidence that could refute it. Retrieve relevant
callers before judging their absence. Feed claims to the existing design expert
and domain validators. Preserve uncertainty as a question or missing coverage,
not an unsupported accusation that the design is bad.

Deterministic boundary rules can gate immediately when explicitly configured.
Model design findings begin advisory. Enable blocking by rule only after
held-out positive/negative fixtures and repository trials establish acceptable
precision, with the measured counts and reviewer disagreement published.
Model agreement alone is not correctness. Do not gate on generic complexity,
interface counts or resemblance to a preferred architecture.

## Tests, security and operations

The initial profile names these coverage boundaries rather than implying they
are covered by style checks. Reuse supported security/dependency analyzers as
explicit checks, with their existing network and configuration requirements.
Missing configured scanners report unavailable. Architecture prompts inspect
error observability and lifecycle only within their named review units.

Later, accept CI evidence for test/vet/build/mutation runs bound to the exact
revision, command, environment and trusted workflow identity. An untrusted JSON
file in the pull request cannot attest that tests passed. A workflow file's
presence establishes configured intent, not execution. Local receipt ingestion
must be identified as operator-attested. No claim of operational readiness follows
from a logger, health endpoint or test file merely existing.

## Implementation sequence and acceptance

1. **Report and policy.** Add typed targets, check states, provenance, profiles
   and exit aggregation. Preserve existing CLI/JSON behavior. Tests cover every
   state, violations plus incomplete coverage, exclusions, policy tampering and
   empty applicable input. No new best-practice claim until sources are wired.
2. **Commits.** Add range enumeration and validation, CLI and PR summary output.
   Fixture Git repositories cover merge topology, shallow history, Unicode,
   empty/missing ranges, spoofed merge subjects, squash titles and config edits.
   Replace the shell validator after compatibility checks pass.
3. **Slop.** Adapt tells and semantic results into the report; add per-rule
   enforcement and shared model execution. Require bad/clean controls, no-model
   and stage-failure checks, and a regression proving added clean lines cannot
   hide an existing violation. Publish no authorship classification.
4. **Design.** Add bounded unit planning, first Go dependency/boundary adapter,
   dedicated assessment prompts and expert validation. Fixtures pair each
   defect with a legitimate design using the same surface pattern; include
   cross-file evidence, missing context, stale incremental results and budget
   exhaustion. Record model/prompt versions, repeated-run variance and cost.
5. **Adopt internally.** Run the engineering profile on this repository, fix
   findings or record bounded exceptions, and enable required completion in CI.
   Keep model design advisory until its evaluation supports a blocking policy.
   Add trusted CI receipts and wider language graph support as separate changes.

Guard tests must fail under mutation. Every zero-finding integration control
must assert the intended instrument and stage ran over the expected targets.
For example, substituting "no eligible files" for a setup error must fail the
coverage test even though both return no findings.

Incremental reuse keys include source/context digests, rule/profile versions,
model/prompt version and policy digest. A dependency or policy change invalidates
affected units, even with an unchanged primary file. Cached results identify
their original run and do not claim fresh execution. Bound tokens, wall time and
context per unit; cancellation preserves completed evidence and marks the rest
partial. Share snapshots and analyzer output within a run to avoid duplicate
cost and inconsistent reads.

Ship each stage as a separate reviewable PR. Keep new profiles opt-in until
coverage behavior and cost are measured. Rollback is profile deselection, not
removal of the existing convention checks. Update user docs, generated config
references, and findings measurements when the implementation lands.
