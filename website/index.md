---
hide:
  - navigation
  - toc
---

<div class="np-hero" markdown>

# Pull request review you can read the source of.

<p class="np-lede">open-nitpick reads a pull request, reviews it with a model you choose, and posts inline comments. It runs as a GitHub Action, as a CLI in any CI, or against your working tree before the pull request exists. No hosted service, no per-seat pricing, no vendor holding your code.</p>

<div class="np-actions" markdown>
[Get started](guide.md#usage){ .md-button .md-button--primary }
[Read the measurements](docs/findings.md){ .md-button }
[Source on GitHub](https://github.com/jdziat/open-nitpick){ .md-button }
</div>

<div class="np-stat" markdown>
<div markdown><strong>5</strong><span>corpora, four re-runnable</span></div>
<div markdown><strong>18</strong><span>instrument bugs recorded</span></div>
<div markdown><strong>1</strong><span>published claim retracted</span></div>
</div>
<p class="np-fine">Counts as of 2026-09-05, from <a href="docs/findings/">the findings</a>.</p>

</div>

<div class="np-prose" markdown>

```bash
go install github.com/jdziat/open-nitpick/cmd/nitpick@latest

export LLM_PROVIDER=synthetic
export LLM_MODEL=hf:moonshotai/Kimi-K3
export SYNTHETIC_API_KEY=syn_...

nitpick review          # reviews your uncommitted changes
```

</div>

<div class="np-grid" markdown>

<div class="np-card" markdown>
<p class="np-card-title">Any model, different models per job</p>
Eighteen providers, Synthetic and OpenRouter built in, any OpenAI-compatible endpoint, Ollama and llama.cpp. A cheap model triages; a strong one reviews; an expert pass can overrule either. [Configuration →](guide.md#configuration)
</div>

<div class="np-card" markdown>
<p class="np-card-title">Prompts you can print before you pay</p>
Path-scoped instructions live next to the code they describe. `nitpick explain-config` shows the exact prompt a file would get. [Instructions →](guide.md#personality-and-how-much-it-nitpicks)
</div>

<div class="np-card" markdown>
<p class="np-card-title">Context in both directions</p>
The definitions a changed line calls, and the untouched callers of what a change redefines, attached from the repository with their real line numbers. Off by default until measured more widely. [Related context →](guide.md#related-context)
</div>

<div class="np-card" markdown>
<p class="np-card-title">A trust model, written down</p>
A change cannot supply the policy it is reviewed under. Policy comes from the base revision, analyzer configs never from the tree, endpoint keys are stripped from untrusted config. [Trust model →](guide.md#trust-model)
</div>

<div class="np-card" markdown>
<p class="np-card-title">Linters as evidence, not noise</p>
golangci-lint, ruff, eslint, semgrep and 29 more, auto-detected, isolated from the tree, fed to the model for triage rather than dumped into the pull request. [Analyzers →](guide.md#analyzers)
</div>

<div class="np-card" markdown>
<p class="np-card-title">Measured, mistakes included</p>
Five corpora, a judge-free harness, and a findings document that records its own instrument bugs, including the one that forced a retraction. [Findings →](docs/findings.md)
</div>

</div>

<div class="np-prose" markdown>

## How a review runs

1. The change is read from GitHub or a local checkout, and the policy it is reviewed under comes from the base revision, not the branch.
2. Analyzers that are installed run against the changed lines, isolated from the tree, and their output becomes evidence.
3. Files are bundled into batches under a token budget, with related context attached when it is switched on: what a changed line calls, and who calls what the change redefined.
4. Each batch is reviewed by the model the route selects. A triage model merges and filters. An optional expert pass refutes.
5. The review is posted as inline comments, with a summary that lists every file not reviewed, every analyzer that did not run, and every finding that was discarded and why.

[The full walkthrough →](guide.md#how-a-review-runs)

<p class="np-fine" markdown>The quickstart uses [Synthetic](https://synthetic.new/?referral=KBc4DHaHWcig6zR), the recommended route: open-weight models on a flat subscription, $30 a month for one pack as of 2026-09-05. That link carries the author's referral code, and the author receives referral credit if you sign up through it; [synthetic.new](https://synthetic.new) without it is the same service at the same price. To spend nothing first, `nitpick explain-config` prints what a review would send without sending it, and `LLM_PROVIDER=ollama` runs against a local model. [Why, and the alternatives →](guide.md#synthetic-recommended)</p>

</div>
