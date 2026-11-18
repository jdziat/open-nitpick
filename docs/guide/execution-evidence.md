# Selected execution evidence

Execution is disabled by default. For the hand-picked example pilot, the
operator approved Docker `runc`; gVisor is optional. This pilot reports what a
selected check did. It does not establish semantic repair or improved precision.

Build the checker from trusted source with `bash tools/execution/build.sh`.
The script uses a pinned Go toolchain image and prints the resulting content ID.
Put that `sha256:…` ID in operator configuration:

```yaml
execution:
  enabled: true
  image: sha256:REPLACE_WITH_CHECKER_CONTENT_ID
  runtime: runc
  trusted_corpus: true
  risk_accepted: true
  required_for_fix: false
  checks:
    - kind: go_test
      package: internal/http
```

Repository configuration cannot enable execution. Use the existing operator
configuration mechanism described in the [trust model](../trust-model.md).
`go_test` and `go_build` are fixed profiles; findings cannot supply commands.
The working directory is the module root. Missing dependencies, unsupported
cgo, no completed tests, timeouts, truncated output and unconfirmed cleanup
produce unavailable evidence. Dependencies are not fetched during execution.
This initial pilot accepts at most eight checks and snapshots of at most 1,024
entries, with regular files only. Larger or unsupported repositories abstain.

The existing authorized `@open-nitpick fix` workflow checks the original snapshot
and candidate under the same profile. Draft descriptions report both outcomes,
the source, patch, policy and output hashes, runtime and image. Existing
permission and stale-head publication guards still apply. With
`required_for_fix: true`, every selected baseline and candidate must pass before
a draft can be published. An already failing baseline does not become evidence
that a candidate is repaired. Fix model usage is emitted to the run log as a
JSON `fix_usage` event, including unsuccessful model attempts.

Finding verification accepts an additional `execution.claims` list only for a
registered claim that a selected default `go_build` command fails. A successful
build refutes that exact claim; it says nothing about behavior, tests or other
build tags. Use `tools/execution/map_claim.py` with the exact revision, path,
line, title and rationale to print a JSON entry for that list. `check_index`
selects a configured `go_build` profile. Changes to the finding or revision
invalidate the mapping. Failed, missing or stale evidence preserves the
previous read-only verification decision. Claims without a mapping never run.

The runtime receives no forge/model credentials or host sockets. Inputs are
read-only; caches and writable files are disposable, network access is disabled,
and each check has a 120-second wall limit, two CPUs, 2 GiB RAM, 128 processes,
1 GiB per temporary filesystem and 256 KiB output. Test binaries execute only
from the disposable temporary filesystem. `runc` shares the host kernel; the
pilot makes no claim about containing arbitrary contributor code. A different
execution scope requires a separately reviewed policy.

Run the live controls using the image content ID:

```sh
NITPICK_EXECUTION_IMAGE=sha256:REPLACE_WITH_CHECKER_CONTENT_ID \
  go test -count=1 -run TestTrustedCorpusRunsSelectedChecksInsideDocker -v ./internal/execution
```

Those controls exercise a passing test, a failing candidate, no tests, early
`TestMain` exit, missing dependencies, absent credentials and host sockets,
network denial, a read-only shared cache, and cancellation with container cleanup.
Normal unit tests also reject altered evidence, changed patches and moved heads.
These are lifecycle and execution controls, not a quality evaluation.
