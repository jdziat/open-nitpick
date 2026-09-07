---
hide:
  - navigation
  - toc
---

<div class="np-hero" markdown>

# Pull request review you can read the source of.

<p class="np-lede">open-nitpick reads a pull request, reviews it with a model you choose, and posts inline comments. Run it as a GitHub Action, as a CLI in any CI, or against your working tree before the pull request exists. Nothing is hosted, nothing is priced per seat, and your code stays with you.</p>

<div class="np-actions" markdown>
[Get started](docs/usage.md){ .md-button .md-button--primary }
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
Eighteen providers, with Synthetic and OpenRouter built in, plus any OpenAI-compatible endpoint, Ollama and llama.cpp. A cheap model triages and a strong one reviews. An expert pass can overrule either. [Configuration →](docs/configuration.md)
</div>

<div class="np-card" markdown>
<p class="np-card-title">Prompts you can print before you pay</p>
Path-scoped instructions live next to the code they describe. `nitpick explain-config` shows the exact prompt a file would get. [Instructions →](docs/configuration.md#personality-and-how-much-it-nitpicks)
</div>

<div class="np-card" markdown>
<p class="np-card-title">Context in both directions</p>
The definitions a changed line calls are attached by default. So are the untouched callers of anything a change redefines, behind a switch of their own, since that walk reads files the change never named. [Related context →](docs/configuration.md#related-context)
</div>

<div class="np-card" markdown>
<p class="np-card-title">A trust model, written down</p>
A change cannot supply the policy it is reviewed under. Policy is read from the base revision. Analyzer configuration never comes from the tree, and endpoint keys are stripped from a config the change could have written. [Trust model →](docs/trust-model.md)
</div>

<div class="np-card" markdown>
<p class="np-card-title">Linters as evidence, not noise</p>
golangci-lint, ruff, eslint, semgrep and 29 more, detected automatically and run in isolation from the tree. Their output goes to the model for triage instead of into the pull request. [Analyzers →](docs/analyzers.md)
</div>

<div class="np-card" markdown>
<p class="np-card-title">Measured, mistakes included</p>
Five corpora and a judge-free harness. The findings document records the instrument bugs found along the way, including the one that forced a retraction. [Findings →](docs/findings.md)
</div>

</div>

<div class="np-prose" markdown>

## How a review runs

1. The change is read from GitHub or a local checkout, and the policy it is reviewed under comes from the base revision, not the branch.
2. Analyzers that are installed run against the changed lines, isolated from the tree, and their output becomes evidence.
3. Files are bundled into batches under a token budget, with related context attached: what a changed line calls by default, and who calls what the change redefined when the caller walk is switched on.
4. Each batch is reviewed by the model the route selects. A triage model merges and filters. An optional expert pass refutes.
5. The review is posted as inline comments, with a summary that lists every file not reviewed, every analyzer that did not run, and every finding that was discarded and why.

[The full walkthrough →](docs/how-a-review-runs.md)

<p class="np-fine" markdown>The quickstart uses [Synthetic](https://synthetic.new/?referral=KBc4DHaHWcig6zR), which serves open-weight models on a flat subscription. Their pricing page read $30 a month for one pack on 2026-09-05. That link carries the author's referral code and pays the author referral credit if you sign up through it. [synthetic.new](https://synthetic.new) without the code is the same service at the same price. You can spend nothing first: `nitpick explain-config` prints what a review would send without sending it, and `LLM_PROVIDER=ollama` runs against a local model. [Why, and the alternatives →](docs/providers.md#synthetic-recommended)</p>

</div>
