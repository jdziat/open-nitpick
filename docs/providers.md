# Providers and models

Every provider below is registered in the SDK and usable as
`models.default.provider`. `nitpick providers` prints the same list from the
binary you are running, which is the answer to trust if this page has drifted.

## Every provider at a glance

The credential variable is what the provider reads when nothing else supplies a
key. `LLM_API_KEY` is a fallback for every provider that lists one, and the
keystore comes before both; see [Credentials](#credentials).

| Provider | Credential variable | `base_url` | Notes |
|---|---|---|---|
| `anthropic` | `ANTHROPIC_API_KEY` | compiled in | Claude, native API rather than an OpenAI-compatible shim |
| `azure` | `AZURE_OPENAI_API_KEY` or `AZURE_OPENAI_KEY` | **required** | `model` is the deployment name, not a model id |
| `cerebras` | `CEREBRAS_API_KEY` | `https://api.cerebras.ai/v1` | |
| `deepseek` | `DEEPSEEK_API_KEY` | `https://api.deepseek.com/v1` | |
| `featherless` | `FEATHERLESS_API_KEY` | `https://api.featherless.ai/v1` | |
| `fireworks` | `FIREWORKS_API_KEY` | `https://api.fireworks.ai/inference/v1` | |
| `gemini` | `GEMINI_API_KEY` or `GOOGLE_API_KEY` | compiled in | native API |
| `groq` | `GROQ_API_KEY` | `https://api.groq.com/openai/v1` | |
| `llamacpp` | `LLAMA_CPP_API_KEY` | `http://localhost:8080` | local; loopback allowed without `allow_private_endpoint` |
| `mistral` | `MISTRAL_API_KEY` | `https://api.mistral.ai/v1` | |
| `ollama` | `OLLAMA_API_KEY` | `http://localhost:11434/v1` | local; key usually unset |
| `openai` | `OPENAI_API_KEY` | `https://api.openai.com/v1` | |
| `openrouter` | `OPENROUTER_API_KEY` | `https://openrouter.ai/api/v1` | a router; see [Pinning a router](#pinning-a-router-to-one-upstream) |
| `perplexity` | `PERPLEXITY_API_KEY` or `PPLX_API_KEY` | `https://api.perplexity.ai` | |
| `runpod` | `RUNPOD_API_KEY` | derived | needs `extra.endpoint_id` |
| `synthetic` | `SYNTHETIC_API_KEY` | `https://api.synthetic.new/openai/v1` | flat subscription; see below |
| `togetherai` | `TOGETHER_API_KEY` | `https://api.together.xyz/v1` | |
| `zai` | `ZAI_API_KEY` | `https://api.z.ai/api/coding/paas/v4` | `extra.coding` selects the Coding API |

Model ids are the vendor's own and are not listed here: they change faster than
this page can. Take them from the provider's catalogue.

## A worked example for each

Every block below is a complete `.nitpick.yaml`. Each names only `provider` and
`model`, because everything else in this file has a working default, and the
credential comes from the keystore or the variable in the table above.

```yaml
# anthropic
models: {default: {provider: anthropic, model: claude-sonnet-4-5}}
```

```yaml
# openai
models: {default: {provider: openai, model: gpt-5}}
```

```yaml
# gemini
models: {default: {provider: gemini, model: gemini-2.5-pro}}
```

```yaml
# mistral
models: {default: {provider: mistral, model: mistral-large-latest}}
```

```yaml
# deepseek
models: {default: {provider: deepseek, model: deepseek-chat}}
```

```yaml
# groq
models: {default: {provider: groq, model: llama-3.3-70b-versatile}}
```

```yaml
# cerebras
models: {default: {provider: cerebras, model: llama-3.3-70b}}
```

```yaml
# fireworks
models: {default: {provider: fireworks, model: accounts/fireworks/models/llama-v3p1-70b-instruct}}
```

```yaml
# togetherai
models: {default: {provider: togetherai, model: meta-llama/Llama-3.3-70B-Instruct-Turbo}}
```

```yaml
# perplexity
models: {default: {provider: perplexity, model: sonar-pro}}
```

```yaml
# featherless: any model on featherless.ai, addressed by its Hugging Face path
models: {default: {provider: featherless, model: meta-llama/Meta-Llama-3.1-70B-Instruct}}
```

```yaml
# llamacpp: whatever the server was started with, so the name is yours
models: {default: {provider: llamacpp, model: local}}
```

The model ids above are examples, not recommendations, and none of them has
been measured as a reviewer here. Catalogues change faster than this file, and
every one of these providers is OpenAI-compatible, so `GET /v1/models` against
the endpoint in the table above is the current answer. The `fireworks` and
`togetherai` ids come from the SDK's own tables; `cerebras` and `perplexity`
were read from the vendors' documentation on 2026-09-07.

Three providers need more than a name.

**Azure** addresses a deployment you created, so `model` is the deployment
name and `base_url` is your resource endpoint. Neither has a default that could
be right.

```yaml
models:
  default:
    provider: azure
    model: my-gpt5-deployment      # the deployment, not a model id
    base_url: https://my-resource.openai.azure.com
    api_key_env: AZURE_OPENAI_API_KEY
```

**RunPod** serves each deployment under its own endpoint id, which the base URL
is built from, so it is required and passed through `extra`.

```yaml
models:
  default:
    provider: runpod
    model: meta-llama/Llama-3.3-70B-Instruct
    extra:
      endpoint_id: abc123xyz
```

**Z.AI** has a separate Coding API endpoint, selected with `extra.coding`.

```yaml
models:
  default:
    provider: zai
    model: glm-4.6
    extra:
      coding: "true"
```

`base_url`, `api_key_env`, `extra` and `allow_private_endpoint` are withheld
from a repository's own `.nitpick.yaml` unless
`NITPICK_TRUST_CONFIG_ENDPOINTS=1` is set. Put them in the user-level config
instead, which no pull request can edit. See
[Trust model](trust-model.md) and [Configuration](configuration.md#two-files).

## Credentials

The keystore is consulted first and needs no configuration. Store a key once:

```bash
nitpick auth set synthetic      # reads the key from standard input
nitpick auth list               # which providers have one, never what it is
nitpick auth delete synthetic
```

That writes to the operating system's keystore (Keychain on macOS, Credential
Manager on Windows, Secret Service on Linux) under the service `open-nitpick`
and the provider's name, which is where a review looks with nothing configured.
The credential is read from standard input rather than an argument so it
reaches neither the shell's history nor the process table.

Resolution order, explicit sources before implicit ones:

1. `credential_command`, a program whose standard output is the key
2. `api_key_keyring`, a named keystore secret as `service/account`
3. `api_key_env`, a named environment variable
4. the keystore under the default name above
5. nothing, leaving the provider to read its own variable from the table

A source you named failing is an error, because writing down where your key
lives says you do not want the environment used instead. A miss on step 4 falls
through in silence, so a machine with no keystore, or a Linux session with no
D-Bus, behaves as it always did.

For a secret manager the keystore cannot reach, name a command. It is a command
and its arguments rather than a shell line, so nothing in the file is expanded
by a shell:

```yaml
# ~/.config/nitpick/config.yaml, never a repository's .nitpick.yaml
models:
  default:
    provider: anthropic
    model: claude-sonnet-4-5
    credential_command: ["op", "read", "op://Private/anthropic/credential"]
```

```yaml
    credential_command:
      ["aws", "secretsmanager", "get-secret-value",
       "--secret-id", "nitpick/anthropic", "--query", "SecretString", "--output", "text"]
```

`credential_command` and `api_key_keyring` are withheld from a repository's own
config for the reason `api_key_env` is: one chooses which stored secret is read,
the other runs a program in the job holding your credentials.

## Synthetic (recommended)

[Synthetic](https://synthetic.new/?referral=KBc4DHaHWcig6zR) hosts open-weight
models (Kimi-K3, GLM-5.3-Flash, Qwen3.8-27B and others) behind an
OpenAI-compatible endpoint on a flat subscription rather than per-token
billing: $30 a month for one pack, 500 requests per five hours, one concurrent
request per model, as read from their pricing page on 2026-09-05. Usage-based
billing is offered separately. That fits a reviewer better than metered
pricing does: the cost of a review is zero at the margin, so nothing argues
for reviewing fewer pull requests.

What the measurements say, in full: Kimi-K3 with related context ties the
shipped default, `anthropic/claude-sonnet-4.6`, on recall on both tuned
corpora ([docs/findings.md](findings.md#kimi-k3-and-the-second-half-of-the-multi-file-corpus)),
and was marked down there on one column only, price per review, which is why
it is absent from the twelve-model price table below. A flat subscription
does not charge that column. GLM-5.3-Flash is the triage and iteration model
this repository's own configuration uses, and with Kimi-K3 as the expert pass
over it, noise on the tuning corpus halved at the same recall
([docs/findings.md](findings.md#callers-2026-09-05)). Neither of those is
a claim that Kimi-K3 is the best reviewer measured; `qwen/qwen3.8-27b` and
`openai/gpt-5.6-luna` are, per dollar on metered pricing, and the table says
so.

The link above carries the author's referral code, and the author receives
referral credit if you sign up through it. <https://synthetic.new> without it
is the same service at the same price.

`synthetic` is a provider with a compiled-in endpoint, so a committed config
can name it and nothing else is needed:

```yaml
models:
  default:
    provider: synthetic
    model: hf:moonshotai/Kimi-K3
  triage:
    provider: synthetic
    model: hf:zai-org/GLM-5.3-Flash
    temperature: 0
```

```bash
export SYNTHETIC_API_KEY=syn_...
```

Model ids are Synthetic's `hf:<org>/<name>` form; their `syn:large:text`
aliases work too and follow whatever they currently recommend. `SYNTHETIC_API_KEY`
wins over `LLM_API_KEY` (which is how the GitHub Action's `api-key` input
arrives), and `OPENAI_API_KEY` is not accepted: it is a credential for a
different host. The endpoint is compiled into the binary rather than read from
`base_url`, and that is what lets it be a committed default: `base_url` and
`api_key_env` are stripped from a config the reviewer does not trust (see
[Trust model](trust-model.md#trust-model)), so the same setup written against the `openai`
provider would work only for whoever had exported
`NITPICK_TRUST_CONFIG_ENDPOINTS`.

The eval harness reaches Synthetic with a `synthetic:` prefix on the model id
(`MODELS=synthetic:hf:Qwen/Qwen3.8-27B`), which keeps the same weights on two
hosts as two rows. Cost per review is priced at Synthetic's usage-based rates,
transcribed into `internal/evals/testdata/pricing.yaml` from the vendor's
pricing page; on the subscription tier the column is what the same tokens
would cost when paying per token.

## OpenRouter

`openrouter` reaches the rest of the catalogue (the frontier closed models
among them) on one key. It is a provider in its own right, so it needs a key
and nothing else:

```yaml
models:
  default:
    provider: openrouter
    model: anthropic/claude-sonnet-4.6
```

```bash
export OPENROUTER_API_KEY=sk-or-...
```

Like `synthetic`, its endpoint is compiled into the binary, so a committed
config can name it. The eval harness reaches every model in the sweep through
it. This repository's own policy on OpenRouter is
[.nitpick.openrouter.yaml](https://github.com/jdziat/open-nitpick/blob/main/.nitpick.openrouter.yaml): the same reviewer and
triage models as `.nitpick.yaml` under their OpenRouter ids, with the policy
block kept identical, for `nitpick review -config .nitpick.openrouter.yaml`.

`LLM_API_KEY` is accepted as a fallback, which is how the GitHub Action's
`api-key` input arrives. `OPENROUTER_API_KEY` wins when both are set, so a
generic key exported for some other vendor is never the one sent here.
`OPENAI_API_KEY` is deliberately *not* accepted: it is a credential for a
different host.

What the compiled-in endpoint does **not** buy you: `provider` and `model` still
come from the config file, and for a router the model id chooses which upstream
receives the code. See [Trust model](trust-model.md#trust-model).

## Choosing a model by price

Twelve models were run through the shipped pipeline on all three eval corpora
(tuning, multi-file, info; 38 planted defects) with related context on. The
full table, per-corpus numbers and caveats are in
[docs/comparison.md](comparison.md#twelve-models-three-corpora-the-costperformance-sweep-2026-09-04);
this is the short version. Recall is planted defects located; `$/review` is
the provider-reported spend per pull request on those corpora. Most rows are
a single run, so gaps under about 0.10 are inside the noise.

| tier | model | weighted recall | $/review | trade |
|---|---|---|---|---|
| best value overall | routed: gemma pinned, qwen3.8-27b for security and TypeScript, glm-5.3-flash router, qwen triage (`internal/evals/testdata/routes/routed.yaml`) | 0.81 | $0.005 – $0.011 | qwen's recall and near its noise at a third of the price; three models to configure |
| highest recall | ensemble: gemma pinned + glm-5.3-flash on every batch, qwen triage (`ensemble-cheap.yaml`) | 0.84 | $0.010 – $0.011 | best info-corpus recall measured; noisiest configuration in this table |
| cheapest of all | `google/gemma-4-31b-it` pinned to `deepinfra/turbo` | 0.75 | $0.0003 | needs `providers: [deepinfra/turbo]`; weak on the info corpus; best on multi-file diffs |
| cheapest without a pin | `openai/gpt-5.6-luna` | 0.76 | $0.0005 – $0.0023 | quiet on multi-file diffs (0.04 noise); weak on the info corpus |
| cheapest with no surprises | `z-ai/glm-5.3-flash` | 0.82 | $0.0017 | noisy on multi-file diffs; never lost a review |
| best quality per dollar | `qwen/qwen3.8-27b` | 0.82 | $0.017 | above the default on every corpus with lower noise |
| quietest | `x-ai/grok-4.6` | 0.74 | $0.020 | zero noise on two corpora, pays in recall |
| shipped default | `anthropic/claude-sonnet-4.6` | 0.79 | $0.021 | the only model measured on the held-out corpus |
| frontier | `openai/gpt-5.6-sol` | 0.85 | $0.027 | `z-ai/glm-5.3` edges it on recall at double the noise |
| skip | `qwen/qwen3.8-max`, `deepseek/deepseek-v4-pro-0813` | 0.63 – 0.78 | $0.013 – $0.044 | most expensive, and both dropped reviews |

Incumbent's on-demand price on the same corpora is $0.25 to $0.36 a review.

Every row was measured with `review.related_context: true`, on 2026-09-04,
which is now the default, and without the caller walk, which is not. The
multi-file corpus rerun with the walk on
([docs/findings.md](findings.md#callers-2026-09-05)) cost no more per
review than before, but the sweep itself has not been repeated.

The default stays sonnet-4.6 because the sweep ran on the corpora the prompt
was tuned against; a candidate replaces it only by beating it on the held-out
corpus under the rule in [docs/measurement.md](measurement.md).
`qwen/qwen3.8-27b` and `openai/gpt-5.6-luna` are the two worth that spend.

## Routing batches to different models, and ensembles

A review is a set of batches, and each batch can go to the model that
measured best for what it is. `models.routes` is tried in order; the first
match wins, and a batch no route matches goes to the review model. A match
can name languages (by file extension), a file-count range, and the kinds
of change a router assigned:

```yaml
models:
  default:
    provider: openrouter
    model: google/gemma-4-31b-it
    providers: [deepinfra/turbo]
  triage:
    model: qwen/qwen3.8-27b
  router:
    model: z-ai/glm-5.3-flash
  routes:
    - name: security
      match: {kinds: [security, concurrency]}
      review: {model: qwen/qwen3.8-27b}
    - name: typescript
      match: {languages: [typescript, javascript]}
      review: {model: qwen/qwen3.8-27b}
    - name: cross-file
      match: {min_files: 2}
      review: {model: z-ai/glm-5.3-flash}
  ensemble:
    - model: z-ai/glm-5.3-flash
```

The router is a cheap model that reads each batch's diff once and answers
with kinds from a fixed list: `security`, `concurrency`, `contract`,
`data`, `config`, `logic`, `test`, `docs`. It runs only when a route names
a kind. A router that fails does not fail the batch; the batch is reviewed
unclassified and the report says so.

`models.ensemble` names models that review every batch alongside the
chosen one. Their findings are pooled and the triage pass merges duplicates
and reranks: the same defect from two reviewers is one finding, and their
agreement is a reason to keep its level. A route's own `ensemble` replaces
the global one for the batches it matches; an empty list removes it.

Every model here overlays `default` the way a role does, so a route names
only what differs. A provider pin follows its model: a route that changes
the model starts unpinned unless it sets `providers` itself. The report
records where each batch went (`Report.Routes`), and `nitpick explain-config`
shows the prompt each reviewer gets, including its model-family layer. The
measured configurations are in `internal/evals/testdata/routes/` and their
numbers in [docs/comparison.md](comparison.md).

## Pinning a router to one upstream

OpenRouter serves a model from many upstream providers and picks one per
request. When some of them stall, `providers` names the ones a review may
use, in order, with no fallback beyond them:

```yaml
models:
  default:
    provider: openrouter
    model: google/gemma-4-31b-it
    providers: [deepinfra/turbo]
```

Slugs are OpenRouter's, with an endpoint suffix where one exists. The
setting is only accepted with the `openrouter` provider. This repository's
own OpenRouter config pins kimi-k3 to `moonshotai/mxfp4` with `fireworks`
and `together` as fallbacks: unpinned, the router's cheapest-first order
sent one review to an fp4 quantisation that stalled for ten minutes, ran
past the output cap on the retry, and answered invalid JSON on the third;
pinned, the same review returned in one attempt. `curl
https://openrouter.ai/api/v1/models/<model>/endpoints` lists a model's
upstreams with their tags and which support structured output. It is also a
trust decision: a pull request that edits `.nitpick.yaml` can change it,
which chooses which third party reads the code, exactly as `model` already
can. Rates differ by upstream, so the eval harness prices a pinned run only
when the pin is the endpoint the price table recorded.

## When a request never finishes

A router can hand a request to an upstream that accepts it and never
answers, and a model can generate past any sensible length on one input.
Both look the same from here: the HTTP client's timeout (`timeout`, default
10 minutes) fires while the body is still being read. The client then sends
the request again, up to `max_retries` times (default 3), with an output cap
of 16k tokens on the retries when the config set none. A hung upstream
answers under the cap. A runaway generation comes back cut, and the next
attempt samples at temperature 0.3 instead of zero to break the loop; a
review that took that path is no longer reproducible by re-running it, and
its log says so. Every retry and its outcome is one log line, so a review
that took forty minutes says why. This was built on gemma-4-31b through
OpenRouter, which lost one review in five without it and none with it; the
numbers are in [docs/comparison.md](comparison.md).

A provider can also refuse the shape of the answer rather than the request.
`structured_output: auto` asks for a JSON-Schema response format, drops to
JSON mode when the provider rejects that, and drops once more to carrying the
schema in the prompt and parsing the reply leniently; OpenRouter's DeepInfra
turbo endpoints land on that last path. Each downgrade is remembered for the
rest of the run, so it costs one request rather than one per batch. Naming the
path outright with `structured_output: schema`, `json` or `text` skips the
discovery for an endpoint whose answer you already know.

## Other OpenAI-compatible gateways (vLLM, LiteLLM)

Any other OpenAI-compatible endpoint works through the `openai` provider:

```yaml
models:
  default:
    provider: openai
    model: my-model
    base_url: https://gateway.example.com/v1
    api_key_env: MY_GATEWAY_KEY
```

`base_url` and `api_key_env` are only honored when the config file is trusted
(see [Trust model](trust-model.md#trust-model)), so set `NITPICK_TRUST_CONFIG_ENDPOINTS=1`, or
pass the endpoint via `LLM_BASE_URL` instead of committing it.

## Using a local model

```yaml
models:
  default:
    provider: ollama
    model: qwen2.5-coder:14b
```

Pointing a *different* provider at a private address requires two opt-ins:

```yaml
models:
  default:
    provider: openai
    model: my-model
    base_url: http://192.168.1.10:8000/v1
    api_key_env: MY_GATEWAY_KEY
    allow_private_endpoint: true
```

plus `NITPICK_TRUST_CONFIG_ENDPOINTS=1` in the environment.
