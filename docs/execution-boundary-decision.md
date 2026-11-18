# Execution boundary decision

**Decided:** 2026-11-17, by the operator.

The question was whether S0 needs gVisor to validate execution evidence. It does not. The
checks run only on hand-picked snapshots the operator chose and read, so the threat gVisor
answers (an unknown contributor's code reaching the credentialed runner) is absent.

`internal/execution` therefore accepts two runtimes:

- `runsc` (gVisor), the default, for any snapshot the operator has not vetted.
- `runc`, only when the policy sets `trusted_corpus: true`. It shares the host kernel.

Every result records the runtime it ran under, and a result produced under `runc` does not
verify under a `runsc` policy, so a trusted-corpus run cannot be presented as sandboxed.
The other containment flags (no network, read-only root, dropped capabilities, resource
limits, pinned image) apply to both runtimes.

This decision covers feasibility and fix-evidence work on the trusted corpus. It does not
cover running contributor pull requests. That needs `runsc` or another reviewed backend,
and the plan's rule against falling back to the credentialed runner still holds.
