# Guide

open-nitpick reviews code with the model and evidence-gathering tools you
configure. Each review reports findings and whether its planned work completed,
so a quiet result can be distinguished from a review that did not finish.
Choose the job below; use the [CLI reference](reference/cli.md) for exact flags.

## Review a change

[Reviewing changes](guide/reviewing-changes.md) helps choose a normal, fast,
full, or improvement pass. A normal review is the pull-request workflow; a
fast review narrows feedback; full and improvement passes inspect a wider
scope.

## Put reviews in CI

The [GitHub Actions guide](ci.md) has a non-publishing first workflow, required
permissions, fork behavior, GitHub App credentials, outputs, and response
commands. The [trust model](trust-model.md) explains the boundary between a
repository, provider, GitHub, and reviewer.

## Configure the reviewer

[Configuration](configuration.md) covers models, provider settings, severity
policy, instructions, and per-path rules. [Providers](providers.md) explains
provider-specific environment settings. Run `nitpick explain-config` in a
repository to inspect the configuration a path receives.

## Use from an agent

[Agents and MCP](guide/agents-and-mcp.md) installs `nitpick mcp` into a
supported client and describes the review tools it exposes.

## All workflows

[All workflows](usage.md) is the operating manual for repository setup,
whole-tree reviews, repository standards, security scans, slop checks, and
agent sessions. It remains the place to find tasks that do not belong to a
single pull-request review.
