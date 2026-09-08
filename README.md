<p align="center"><img src="website/assets/logo.svg" width="112" alt="open-nitpick"></p>

# open-nitpick

[![CI](https://github.com/jdziat/open-nitpick/actions/workflows/ci.yml/badge.svg)](https://github.com/jdziat/open-nitpick/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/jdziat/open-nitpick)](https://github.com/jdziat/open-nitpick/releases)
[![License: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](https://github.com/jdziat/open-nitpick/blob/main/LICENSE)

Self-hosted, model-agnostic pull request review.

Referral link: [Synthetic](https://synthetic.new/?referral=KBc4DHaHWcig6zR) is
the route this project recommends and the link pays the author referral credit.
[Below](#quick-start) says what that is worth and what the plain link is.

Documentation: <https://jdziat.github.io/open-nitpick/>

open-nitpick reads a pull request, reviews it with a model you choose, and posts
inline comments. Run it as a GitHub Action, as a CLI in any CI system, as MCP
tools inside an agent session, or against your uncommitted working tree.

Nothing is hosted here. You supply the model, which can be one running on your
own machine. The prompts and the configuration that drive the review are files
in your repository that you can read and edit.

## Quick start

```bash
# A signed binary for your platform. Releases carry linux and darwin on amd64
# and arm64, plus windows on amd64, each with a Sigstore bundle and a
# checksums file.
v=$(gh release view --repo jdziat/open-nitpick --json tagName -q .tagName)
os=$(uname -s | tr 'A-Z' 'a-z'); arch=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
curl -fsSLo nitpick "https://github.com/jdziat/open-nitpick/releases/download/$v/nitpick_${v}_${os}_${arch}"
chmod +x nitpick && sudo mv nitpick /usr/local/bin/

export LLM_PROVIDER=synthetic LLM_MODEL=hf:moonshotai/Kimi-K3
export SYNTHETIC_API_KEY=syn_...

nitpick init            # writes .nitpick.yaml for this repository
nitpick review          # reviews your uncommitted changes
```

Verifying that download is one command and
[Development](docs/development.md#releases) has it. Building from source
instead is `go install github.com/jdziat/open-nitpick/cmd/nitpick@latest`,
which needs Go 1.25.5 or newer: the `go` directive in `go.mod` is a
patch-level floor, so 1.25.4 refuses.

`init` is optional: with those two variables set, a review runs with no config
file at all. What it buys is a file that names the model, the analyzers this
checkout's languages call for, and the dozen settings most worth changing,
commented. It is a starting point, not the whole surface: [the configuration
reference](docs/configuration-reference.md) lists every key the loader accepts.
Every value written is already the one in force except two, so deleting a key
changes nothing: the model, which no default supplies, and `linters.enabled`,
which is matched to this checkout rather than copied from the default. A
Go-only tree is written `[golangci-lint]` and a Python-only one `[ruff]`, where
the shipped default is both, and under `mode: strict` that list is the one
whose absence fails a run. It refuses to overwrite an existing file without
`-force`, and `-workflow` writes the Actions workflow beside it.

The quickstart uses [Synthetic](https://synthetic.new/?referral=KBc4DHaHWcig6zR),
which serves open-weight models on a flat subscription. Their pricing page read
$30 a month for one pack on 2026-09-05, so a review costs nothing per token.
That link carries the author's referral code and pays the author referral credit
if you sign up through it. Plain <https://synthetic.new> is the same service at
the same price.

You can spend nothing first. `nitpick explain-config` prints what a review would
send without sending it, and `LLM_PROVIDER=ollama` runs against a local model.
For OpenRouter, an OpenAI-compatible endpoint or a local model, see
[Providers](docs/providers.md).

## Why this exists

Most review bots wrap one vendor's model in a hosted service, priced per seat,
with a prompt you cannot read. This one is built the other way round.

- **Any model.** 16 providers via [llm-go-sdk][sdk], plus built-in
  `synthetic` (open-weight models on a subscription) and `openrouter` (the rest
  of the catalogue on one key) for 18 in all, plus any OpenAI-compatible
  endpoint through `base_url`, plus local models via `ollama` and `llamacpp`.
  `nitpick providers` prints the list.
- **Different models for different jobs.** A cheap model triages and
  deduplicates while an expensive one reviews. Most of the available cost
  saving comes from that split.
- **Prompts as configuration.** Path-scoped instructions live in your repository
  next to the code they describe. `nitpick explain-config` prints the exact
  prompt that will be sent, before you spend a token on it.
- **Linters as evidence.** Findings from golangci-lint, ruff, eslint and semgrep
  go to the model for triage. Your pull request never receives the raw output.
- **Runs offline.** The whole engine works against a local git checkout with no
  credentials and no network beyond the model call.

[sdk]: https://github.com/nocturnium/llm-go-sdk

## Documentation

| page | what it covers |
|---|---|
| [Usage](docs/usage.md) | reviewing a change or a whole repository, the remediation plan and score, the slop class, and the MCP server for agent sessions |
| [GitHub Actions and other CI](docs/ci.md) | the Action, its inputs and permissions, incremental review, forks, and running the CLI in any other CI |
| [Configuration](docs/configuration.md) | `.nitpick.yaml`: models per role, budget, related context, personality and instructions, model-family notes, severities |
| [Configuration reference](docs/configuration-reference.md) | every key the loader accepts, with its type and shipped default, generated from the binary |
| [Analyzers](docs/analyzers.md) | the 33 deterministic tools, how they are detected, isolated and fed to the model as evidence, and what strict mode means |
| [Providers and models](docs/providers.md) | Synthetic, OpenRouter, choosing a model by price, routing and ensembles, pinning an upstream, stalls, other gateways, local models |
| [Trust model](docs/trust-model.md) | what a pull request can and cannot change about its own review, and why |
| [How a review runs](docs/how-a-review-runs.md) | the pipeline from diff to posted comments |
| [Development](docs/development.md) | tests, commits and releases, iterating cheaply, and evaluating prompts against real models |

## Measurement

This project makes empirical claims about review quality, so how those numbers are
produced is part of the product.

- [Measurement](docs/measurement.md): the fifteen rules a number out of
  the eval harness has to satisfy before it is worth acting on. Each rule was
  written after the harness produced a confident wrong number that something
  believed.
- [Against Incumbent](docs/comparison.md): capabilities and measured results
  by language, with what each iteration changed.
- [Remediation](docs/remediation.md): every miss on the benchmark
  repository, its cause read from the pull request, and the plan.
- [Findings](docs/findings.md): every measurement taken and what it
  supports. It also tables the instrument bugs found so far. Some of those bugs
  flattered one side of a comparison, and one put a claim on this page that had
  to be retracted.


Prefer the judge-free columns. The LLM judge runs at temperature 0 and still
scores byte-identical input anywhere from 3.66 to 3.98.

## Status

Early. Everything described above is built and runs.

It out-detects a hosted incumbent on both counted corpora: 33 of 41 planted
defects against 30 of 41 across 44 real pull requests, and 10 of 13 against
4 of 13 on the held-out fixtures. Noise is the open question: a later run took
detection to 36 of 41 and noise findings from 1 to 17.
[Findings](docs/findings.md) has both comparisons, what they do not
support, and the twenty instrument bugs found along the way.
[Against Incumbent](docs/comparison.md) has the costs: about two cents a
review on the default reviewer, under a fifth of a cent on the cheap one.

GitHub and a local checkout are the only forges. There is no GitLab provider.
Reviews default to advisory, so nothing blocks a merge until you set `fail_on`.

## License

Apache-2.0.
