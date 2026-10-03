# Guide

open-nitpick has one job: make a review result explainable. It reads the
change, runs available evidence-gathering tools, asks the configured models,
then reports findings together with coverage and skipped work. Start with the
workflow you are trying to complete; use the command reference when you need
an exact flag.

## Review a change

Use `nitpick review` for normal pull-request work. It reviews uncommitted
changes by default, or a `-base`/`-head` range when you give one. The guide
covers [normal, fast, full, and improvement reviews](guide/reviewing-changes.md),
including when incremental reuse is appropriate.

## Put reviews in CI

The [GitHub Actions guide](ci.md) configures a pull-request check, secrets,
permissions, draft handling, and response commands. The [trust model](trust-model.md)
explains why a pull request cannot change the policy used to review itself.

## Configure a repository

[Configuration](configuration.md) describes providers, models, instructions,
routes, analyzers, and review gates. [The generated configuration reference](configuration-reference.md)
lists every key. Start with `nitpick explain-config -path <file>` whenever a
configuration result surprises you.

## Use a coding agent

[Agents and MCP](guide/agents-and-mcp.md) explains the MCP server, supported
clients, and the difference between a review result and a GitHub-published
review. An agent receives structured findings but never publishes unless your
workflow gives it forge credentials.

## Understand a result

[How a review runs](how-a-review-runs.md) follows the data flow from a diff to
inline comments. [Analyzers](analyzers.md) lists deterministic evidence sources,
and [security](security.md) covers the separate security scan. Every report
names incomplete stages so “no findings” is distinguishable from “nothing ran.”
