---
hide:
  - navigation
  - toc
---

<div class="np-hero" markdown>

# Pull request review you can read the source of.

<p class="np-lede">open-nitpick reviews pull requests with the model provider you choose and can post inline comments. Run it as a GitHub Action, in any CI system, from an agent, or against local changes before a pull request exists. The reviewer is self-hosted; review input is sent to the model provider you configure. Choose a local provider to keep review input on your machine.</p>

<div class="np-actions" markdown>
[Get started](docs/getting-started.md){ .md-button .md-button--primary }
[CLI reference](docs/reference/cli.md){ .md-button }
[Set up CI](docs/ci.md){ .md-button }
</div>

<p class="np-fine"><a href="docs/guide/reviewing-changes/">Review changes</a> · <a href="docs/configuration/">Configure models and policy</a> · <a href="docs/guide/agents-and-mcp/">Use from an agent</a> · <a href="docs/usage/">All workflows</a> · <a href="docs/trust-model/">Trust model</a></p>

<p class="np-fine">Read the <a href="docs/providers/">provider guidance</a> before selecting a hosted endpoint, inspect the <a href="docs/findings/">measurements</a>, or read the <a href="https://github.com/jdziat/open-nitpick">source on GitHub</a>.</p>

</div>

<div class="np-prose" markdown>

```bash
# A release binary, or `go install …/cmd/nitpick@latest` with Go 1.26.0.
v=$(gh release view --repo jdziat/open-nitpick --json tagName -q .tagName)
os=$(uname -s | tr 'A-Z' 'a-z'); arch=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
curl -fsSLo nitpick "https://github.com/jdziat/open-nitpick/releases/download/$v/nitpick_${v}_${os}_${arch}"
chmod +x nitpick && sudo mv nitpick /usr/local/bin/

export LLM_PROVIDER=openrouter
export LLM_MODEL=z-ai/glm-5.3-flash
export OPENROUTER_API_KEY=sk-or-...

nitpick review          # reviews your uncommitted changes
```

</div>

<div class="np-grid" markdown>

<div class="np-card" markdown>
<p class="np-card-title">Any model, different models per job</p>
Eighteen providers, with Synthetic and OpenRouter built in, plus any OpenAI-compatible endpoint, Ollama and llama.cpp. A cheap model triages and a strong one reviews. An expert pass can overrule either. [Configuration&nbsp;→](docs/configuration.md)
</div>

<div class="np-card" markdown>
<p class="np-card-title">Prompts you can print before you pay</p>
Path-scoped instructions live next to the code they describe. `nitpick explain-config` shows the exact prompt a file would get. [Instructions&nbsp;→](docs/configuration.md#personality-and-how-much-it-nitpicks)
</div>

<div class="np-card" markdown>
<p class="np-card-title">Context in both directions</p>
Reviews include the functions and types used by changed code. You can also enable caller context to check code that depends on your changes. [Related context&nbsp;→](docs/configuration.md#related-context)
</div>

<div class="np-card" markdown>
<p class="np-card-title">A trust model, written down</p>
The reviewer reads policy from the base revision and uses trusted linter settings. By default, pull-request configuration cannot override provider endpoints or credentials. [Trust model&nbsp;→](docs/trust-model.md)
</div>

<div class="np-card" markdown>
<p class="np-card-title">Linters as evidence, not noise</p>
Thirty-three, configured from outside the tree they read. golangci-lint and ruff ship enabled, twenty-four more run whenever they are installed, and eslint, semgrep and five others wait until you name them. Their output goes to the model for triage instead of into the pull request. [Analyzers&nbsp;→](docs/analyzers.md)
</div>

<div class="np-card" markdown>
<p class="np-card-title">Measured, mistakes included</p>
Five corpora and a judge-free harness. The findings document records the instrument bugs found along the way, including the one that forced a retraction. [Findings&nbsp;→](docs/findings.md)
</div>

</div>

<div class="np-prose" markdown>

## How a review runs

1. The change is read from GitHub or a local checkout, and the policy it is reviewed under comes from the base revision, not the branch.
2. Analyzers that are installed run against the changed lines, isolated from the tree, and their output becomes evidence.
3. Files are bundled into batches under a token budget, with related context attached: what a changed line calls by default, and who calls what the change redefined when the caller walk is switched on.
4. Each batch is reviewed by the model the route selects. A triage model merges and filters. An optional expert pass refutes.
5. The review is posted as inline comments, with a summary that lists every file not reviewed, every analyzer that did not run, and every finding that was discarded and why.

[The full walkthrough&nbsp;→](docs/how-a-review-runs.md)

<p class="np-fine" markdown>The quickstart uses OpenRouter with GLM-5.3-Flash, the reviewer this repository uses in CI. You can spend nothing first: `nitpick explain-config` prints what a review would send without sending it, and `LLM_PROVIDER=ollama` runs against a local model. [Why, and the alternatives&nbsp;→](docs/providers.md#openrouter-recommended)</p>

</div>
