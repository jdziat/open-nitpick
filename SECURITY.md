# Security policy

open-nitpick runs in CI, holds a model credential and a forge token, and
executes third-party analyzers against code it did not write. Its trust
model is written down in [docs/trust-model.md](docs/trust-model.md); a report
that shows that document and the code disagree is the most useful kind.

## Reporting

Use GitHub's private vulnerability reporting on this repository (Security
tab, "Report a vulnerability"). Do not open a public issue for anything that
lets a pull request reach the model key, the forge token, an endpoint it
should not, or code execution on the runner outside the analyzers listed.

Expect an acknowledgement within a week. This is a proof of concept with one
maintainer; there is no bounty and no embargo process beyond the fix landing
before the advisory is published.

## Scope

In scope: anything reachable from a pull request the tool reviews, from the
config it reads at the base revision, from analyzer output, or from model
output. Out of scope: the upstream analyzers' own bugs, the model providers,
and the exposure the trust model already states (unpinned analyzer installs
in the Action; a pull request choosing the `model` a router sends code to).
