# Engineering-profile acceptance checkpoint

Decision: needs revision. This is a checkpoint against
[the coverage contract](best-practice-coverage.md), not CTO acceptance.
Execution candidate: `c8cef1e`, draft PR #119. Acknowledgement protocol candidate: `bf03e53`. The retired evaluation used planner 4 and prompt `engineering-4` with Kimi-K3
review and GLM-5.3-Flash triage. The user changed model roles after its first full
cycle; 15 completed reports are retained in
`evidence/design-v4-retired-trials.json`, including one report recovered after
the controller stopped. Its process exit and exact duration were not collected.
These are incomplete repeated trials, not acceptance evidence for the new roles.

| Contract area | Current evidence | Remaining acceptance work |
| --- | --- | --- |
| Explicit scope and states | Typed targets, omissions, failed stages and policy aggregation; local gates pass | Audit final real-repository report for completeness |
| Commits | Shared validator and pinned range enumeration; 11 branch commits pass the accepted policy | Preserve commit-range evidence in the internal profile run |
| Slop | Deterministic tells and semantic assessment; full-source completion separate from design excerpts | Version-7 protocol controls pass; finish real-source review |
| Design execution | Focused declarations/callers, exact excerpts, shared request IDs, cancellation evidence; 20 final mutations killed | Finish implementation review and measure full-repository execution |
| Accepted boundaries | Bad boundary trial fails the deterministic policy; matching good boundary check passes | Distinguish unrelated slop findings in that good control |
| Incremental reuse | Engineering scope disables incremental narrowing | No cached-result speedup is being claimed |
| Language coverage | Go graph, source tasks for other languages, explicit graph limitations | Wider language graph support remains a separate change |
| Internal adoption | Repository CI requires deterministic checks and reports model-disabled coverage | Require model completion only after practical full-profile execution |
| Security and operations | Existing configured analyzer evidence remains distinct from model review | Trusted CI receipts and wider operational claims are separate work |

## Blocking acceptance gaps

The version-5 seven-mechanism evaluation finished all 42 trials; 40 completed
their required model assessments and two were partial. The
[adjudicated results](design-v5-evaluation.md) retain acknowledgement noise,
unsupported assumptions, a reversed shipping diagnosis, and fixture ambiguities.
Counts of returned findings cannot substitute for identifying the seeded defect. The first
intended-good lifecycle trial flagged an exported cache's zero-value behavior:
the supplied caller constructs it correctly, but the public contract does not
say whether other construction is supported. Retain the disagreement and the
original fixture; do not relabel this as a clean control.

The first good boundary trial reported a storage comment that describes
persistence while its implementation returns a constant. That is separate from
its compliant import structure. Score the boundary and slop results separately.

The full repository sizing probe fits the selected scope only under enlarged
operator limits and estimates 19.7 million source tokens. It is not a completed
model assessment, a dollar cost, or evidence that those limits are practical.
The source digest and exact limits are retained in the execution evidence.

Deleted Go source currently yields unavailable-source evidence; the planner does
not yet assess its base-revision body. It must not turn that absence into a clean
change review. Dynamic dispatch and deeper callers are also outside the reported
lexical scope.

The repository still requires only conventions, linters, commits and slop tells
in `.nitpick.yaml`; CI's engineering-report step uses `-no-model`. Therefore green
CI does not establish required design/slop model completion or internal adoption.
Keep the execution PR draft while these acceptance decisions are pending.

## Follow-up from the test control

The intended-good no-panic test received an ineffective-test finding despite its
explicitly named purpose. The test expert had claimed error checks and running
code provide no protection beyond compilation, contradicting the slop exclusion.
Prompt version 5 distinguishes runtime error and no-panic contracts from missing
value coverage, in both engineering instructions and the test expert. The fixture
harness now injects a panic and runs only that no-panic test to prove it fails.
New live trials must use GLM-5.3-Flash review and Qwen3.8-27B triage. Those trials
change both policy and prompt, so they cannot establish either change's isolated
effect. Kimi-K3 remains the fix model.

## Validation response contract

Both incomplete version-5 trials selected a severity verdict without a usable
revised level. The schema describes that field as optional, while the runtime
requires it for severity changes. Clarify the conditional requirement without
forcing a rating on confirmations/refutations. The existing fail-closed behavior
must remain: an unusable expert answer retains the finding and marks the stage
incomplete. Resolve this before relying on a full internal required-model run.
