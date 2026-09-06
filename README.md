<p align="center"><img src="website/assets/logo.svg" width="112" alt="open-nitpick"></p>

# open-nitpick

[![CI](https://github.com/jdziat/open-nitpick/actions/workflows/ci.yml/badge.svg)](https://github.com/jdziat/open-nitpick/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/jdziat/open-nitpick)](https://github.com/jdziat/open-nitpick/releases)
[![Go Reference](https://pkg.go.dev/badge/github.com/jdziat/open-nitpick.svg)](https://pkg.go.dev/github.com/jdziat/open-nitpick)
[![License: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](https://github.com/jdziat/open-nitpick/blob/main/LICENSE)
[![Recommended route: Synthetic](https://img.shields.io/badge/recommended_route-Synthetic-7c3aed)](https://synthetic.new/?referral=KBc4DHaHWcig6zR)

Self-hosted, model-agnostic pull request review.

Documentation: <https://jdziat.github.io/open-nitpick/>

open-nitpick reads a pull request, reviews it with **a model you choose**, and
posts inline comments. It runs as a GitHub Action, as a CLI in any CI system,
as MCP tools inside an agent session, or against your uncommitted working
tree before you open a pull request. There is no hosted service, no per-seat
pricing, and no vendor holding your source: you bring the model (including a
local one), and the review behavior is driven by configuration and prompts
you can read and change.

## Quick start

```bash
go install github.com/jdziat/open-nitpick/cmd/nitpick@latest

export LLM_PROVIDER=synthetic LLM_MODEL=hf:moonshotai/Kimi-K3
export SYNTHETIC_API_KEY=syn_...

nitpick review          # reviews your uncommitted changes
```

The quickstart and the badge above use [Synthetic](https://synthetic.new/?referral=KBc4DHaHWcig6zR),
the recommended route: open-weight models on a flat subscription ($30 a month
for one pack, as read from their pricing page on 2026-09-05), so a review
costs nothing per token. That link carries the author's referral code, and the
author receives referral credit if you sign up through it; the plain
<https://synthetic.new> is the same service at the same price. To spend
nothing at all first, `nitpick explain-config` prints what a review would send
without sending it, and `LLM_PROVIDER=ollama` runs against a local model.
OpenRouter, any OpenAI-compatible endpoint and local models are one config
line away: see [Providers](docs/providers.md).

## Why this exists

Most review bots are a hosted service wrapping one vendor's model, with a prompt
you cannot see and pricing per seat. open-nitpick inverts that:

- **Any model.** 16 providers via [llm-go-sdk][sdk], plus built-in
  `synthetic` (open-weight models on a subscription) and `openrouter` (the rest
  of the catalogue on one key) for 18 in all, plus any OpenAI-compatible
  endpoint through `base_url`, plus local models via `ollama` and `llamacpp`.
  `nitpick providers` prints the list.
- **Different models for different jobs.** A cheap model triages and deduplicates;
  an expensive one does the actual reviewing. That split is most of the cost
  saving available.
- **Prompts as configuration.** Path-scoped instructions live in your repository
  next to the code they describe. `nitpick explain-config` prints the exact
  prompt that will be sent, before you spend a token on it.
- **Linters as evidence.** golangci-lint, ruff, eslint, and semgrep findings are
  fed to the model for triage, not dumped raw into your pull request.
- **Runs offline.** The whole engine works against a local git checkout with no
  credentials and no network beyond the model call.

[sdk]: https://github.com/nocturnium/llm-go-sdk

## Documentation

| page | what it covers |
|---|---|
| [Usage](docs/usage.md) | reviewing a change or a whole repository, the remediation plan and score, the slop class, and the MCP server for agent sessions |
| [GitHub Actions and other CI](docs/ci.md) | the Action, its inputs and permissions, incremental review, forks, and running the CLI in any other CI |
| [Configuration](docs/configuration.md) | `.nitpick.yaml`: models per role, budget, related context, personality and instructions, model-family notes, severities |
| [Analyzers](docs/analyzers.md) | the 33 deterministic tools, how they are detected, isolated and fed to the model as evidence, and what strict mode means |
| [Providers and models](docs/providers.md) | Synthetic, OpenRouter, choosing a model by price, routing and ensembles, pinning an upstream, stalls, other gateways, local models |
| [Trust model](docs/trust-model.md) | what a pull request can and cannot change about its own review, and why |
| [How a review runs](docs/how-a-review-runs.md) | the pipeline from diff to posted comments |
| [Development](docs/development.md) | tests, commits and releases, iterating cheaply, and evaluating prompts against real models |

## Measurement

This project makes empirical claims about review quality, so how those numbers are
produced is part of the product.

- [docs/measurement.md](docs/measurement.md): what has to hold before a number
  out of the eval harness is worth acting on. Fifteen rules, each written
  because the harness produced a confident wrong number and something believed it.
- [docs/comparison.md](docs/comparison.md): capabilities and measured results
  against Incumbent, by language, with what each iteration changed.
- [docs/remediation.md](docs/remediation.md): every miss on the benchmark
  repository, its cause read from the pull request, and the plan.
- [docs/findings.md](docs/findings.md): what has been measured, what it
  supports, and every instrument bug found so far, in a table whose count is the
  number of rows. Several flattered one side of a comparison; one produced a
  published claim that had to be retracted.


The short version: prefer the judge-free columns. The LLM judge runs at
temperature 0 and still scores byte-identical input anywhere from 3.66 to 3.98.

## Status

Usable, measured, and still early. The engine, the GitHub and local providers,
structured output, analyzers, related context in both directions, per-batch
routing, ensembles, and the Action work end to end; a push to a reviewed pull
request is reviewed incrementally. [docs/findings.md](docs/findings.md) is the
record of what has been measured and what it supports. Not yet done: a GitLab
provider.

## License

Apache-2.0.
