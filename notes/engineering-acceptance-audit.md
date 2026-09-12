# Engineering-profile acceptance checkpoint

Decision: needs revision. This is a checkpoint against
[the coverage contract](best-practice-coverage.md), not CTO acceptance.
Execution candidate: `52f402a`, draft PR #119. Live evaluation uses planner 4 and
prompt `engineering-4`; production code matches the candidate, while later
cancellation-test and documentation changes do not affect that binary.

| Contract area | Current evidence | Remaining acceptance work |
| --- | --- | --- |
| Explicit scope and states | Typed targets, omissions, failed stages and policy aggregation; local gates pass | Audit final real-repository report for completeness |
| Commits | Shared validator and pinned range enumeration; 11 branch commits pass the accepted policy | Preserve commit-range evidence in the internal profile run |
| Slop | Deterministic tells and semantic assessment; full-source completion separate from design excerpts | Finish repeated controls and adjudicate misleading-comment findings |
| Design execution | Focused declarations/callers, exact excerpts, shared request IDs, cancellation evidence; 20 final mutations killed | Finish live precision and cost evaluation |
| Accepted boundaries | Bad boundary trial fails the deterministic policy; matching good boundary check passes | Distinguish unrelated slop findings in that good control |
| Incremental reuse | Engineering scope disables incremental narrowing | No cached-result speedup is being claimed |
| Language coverage | Go graph, source tasks for other languages, explicit graph limitations | Wider language graph support remains a separate change |
| Internal adoption | Repository CI requires deterministic checks and reports model-disabled coverage | Require model completion only after practical full-profile execution |
| Security and operations | Existing configured analyzer evidence remains distinct from model review | Trusted CI receipts and wider operational claims are separate work |

## Blocking acceptance gaps

The repeated seven-mechanism evaluation is unfinished. Counts of returned
findings cannot substitute for identifying the seeded defect. The first
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
