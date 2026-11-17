# Execution Boundary Implementation - Adopted Decisions

**Adopted:** 2026-11-17
**Status:** Plan committed to Git, gating work proceeds

## Chosen Options

### 1. Execution Boundary Scope: Option A (Full with gVisor)

- Docker + gVisor runtime validation
- Network isolation (disabled during execution)
- No host sockets, writable mounts, shared caches
- Adversarial fixtures for security tests
- Positive controls must genuinely run

Dependency: S0 required before G1/G2 execution

### 2. Zero-Precision Enforcement: Option C (Noise Injection Test)

- Sample synthesis with injected errors/noise
- Validate invalidation in automated tests

Moderate effort

### 3. Boundary Phasing: Option A (Development -> Review -> Production)

Sequence:
1. S0: Safety gate with adversarial fixtures
2. A: Dev builds only
3. Review: G1 check evidence begins
4. Production: G2 feasibility pilot

Dependency: S0 passes before any G1/G2 execution

### 4. Learnings Storage: Option A (.nitpick/learnings/ overlay)

- Tracked under .nitpick
- Loaded alongside embedded corpus
- Manifest + revision pinning
- Operator review required

### 5. Fix Quality: Option A (Conservative)

- Narrow start from development data
- Behavioral success, defects, coverage metrics
- One calibration pass, one holdout validation

### 6. Documentation: Option B (Enterprise-Ready)

- Enforcement policy in STRATEGY.md
- Measurement regime in docs/measurement.md
- Sandboxing in CI build gates

