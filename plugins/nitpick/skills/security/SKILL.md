---
name: security
description: Scan a repository tree for security issues with required deterministic scanners and an optional model pass, using the nitpick MCP server's security_scan tool. Use when asked for a security review, vulnerability scan, secret scan, advisory check, or security posture of a tree or paths.
---

# Security scan

Call `security_scan` from the nitpick server with `paths` naming what to
check, relative to the repository root (`repo` as an absolute path when the
session is not at the root). Absolute `repo` paths are
**trusted-operator-only**.

Required deterministic scanners always run: `osv-scanner`, `gitleaks`, and
catalog-applicable tools (`golangci-lint` with gosec forced on for Go,
`zizmor`, `checkov`, `brakeman` when their matchers hit). Semgrep runs only
when `linters.semgrep_config` is set (local path or explicit `p/`/`r/`
registry ref (operator-accepted supply chain). There is no `no_linters` and
no `budget`.

Pass `no_model: true` for scanners only. The model pass, when on, is
filtered to class `security`; other classes land in `hidden`. It uses
`models.security` when configured, otherwise the review/default model.
The OpenRouter sample config pins `openai/gpt-5.6-luna` there from the
2026-09-18 security persona bake-off in `docs/findings.md`.

## Reading the answer

- `complete` means required instruments finished, not that every language
  had a SAST. Python/JS have no required SAST unless Semgrep is configured.
- `scanners` lists each required id with `ran`, `not_applicable`, or
  `failed`. Brakeman matching any `.rb` may force incompleteness on
  non-Rails repos (fail-closed).
- `failed_stages` names holes; empty findings with `complete=false` is not
  clean.
- `failed` is the `fail_on` gate (default warning). `fail_on: none` needs
  `allow_clean_with_no_gate: true`.

## Acting on it

1. If `complete` is false, install or fix the failed scanners before treating
   the tree as clean.
2. Fix findings at or above the gate first; advisories count.
3. Relay `hidden` findings rather than dropping them.
