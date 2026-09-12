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

A follow-up review of `bf03e53..74d46f5` is pending. It enables expert validation
and excludes raw `notes/evidence/**` from model input while retaining the written
summaries. This review does not substitute for a full internal engineering run.
