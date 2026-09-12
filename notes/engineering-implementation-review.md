# Engineering implementation review

GLM-5.3-Flash reviewed `c8cef1e..bf03e53`, with Qwen3.8-27B triage and isolated
golangci-lint. It read 49 changed files and returned two findings. The eval-tag
test was outside that linter build; eval-tag vet was run separately. The
[original report](evidence/implementation-v7-review.json) retains both claims.

The error claiming that the good ineffective-test fixture has no tests is false.
[`session_test.go.txt`](../internal/practices/testdata/ineffective-tests/good/session_test.go.txt)
contains them. `materializeMechanism` strips `.txt`, and the harness runs the
tests, including a mutation that injects a panic into the no-panic control.
The fixture README now links to that source.

The warning about the indirection controls was partly correct. The good fixture
already tests storage substitution, but the bad forwarding chain was not pinned.
Commit `b10f115` instruments its three forwarding returns in a temporary copy
and verifies that a public call traverses all three while preserving its result.
It also mutates the good implementation to return a constant and requires the
substitution test to fail. The [four mutation results](evidence/indirection-control-mutations.json)
cover bypassed layers, added forwarder behavior and a weakened substitution test.
The fixture source digests remain unchanged from the version-5 trials.

The [follow-up reviews](evidence/implementation-v9-review.json) assessed
`bf03e53..74d46f5` with expert validation, excluding raw `notes/evidence/**`
while retaining the written summaries. The first published zero findings but
filtered a candidate without retaining its text. It is not counted as resolved.
The second enabled every severity and pedantic publication, which also runs an
additional style pass. It published five nits and retained one expert refutation.

Two nits claim the recorder's counter is never read. Both are false:
`validation_contract_eval_test.go` reads it before and after the validator call
to prove the model ran. The suggested removal would weaken that control.
The remaining nits are addressed by sharing the engineering prompt-version
constant, aligning the validation literal with its declared field order, and
using “live triage controls” in the status line. Prompt text and emitted schema
remain unchanged.

The withheld finding names stale audit wording. The audit was updated in
`65d7cb2`, after the reviewed snapshot. Its expert's claim that no schema change
existed is incorrect: the expert saw a narrower excerpt than the full change.
Retain that context limitation rather than counting the refutation as evidence
of accuracy. These reviews do not substitute for a full internal engineering run.

The final race run exposed test setup that still consulted the operator keystore.
`6c8864a` stubs it and removes credential headers from assertion output. A fake
operator credential is seeded before the helper; removing the helper's stub
[kills the guard](evidence/credential-test-isolation-mutation.json). The
[focused review](evidence/credential-test-isolation-review.json) called that seed
dead setup, overlooking its role in testing isolation. No production credential
precedence changed. The reviewer also reported a truncated caller search.

All eight [final local gates](evidence/engineering-controls-final-gates.json)
passed at `6c8864a`. Slop retained eight tells and omitted the oversized raw trial
JSON. The first gate attempt's analyzer contention and credential-test failures
remain recorded separately from the passing rerun.
