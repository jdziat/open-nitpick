# Configuration reference

Every key `.nitpick.yaml` accepts, generated from the configuration the
binary was built with. [Configuration](configuration.md) is the same settings
argued for rather than listed; this page is the index.

Regenerate with `nitpick config-reference -o docs/configuration-reference.md`,
which `make docs` runs and CI checks. That check diffs this file against
what the generator produces now, so a key the generator reaches cannot drift
from its row. It says nothing about a key the generator never walks to: one
self-referential field kept `fallback` and the eighteen keys under it off
this page entirely, and nothing failed.

`[]` marks a list whose entries carry the keys beneath it,
`<name>` a map whose keys you choose, and `same keys as …` a block
that repeats the keys listed under the path it names. A default of
`none` means the key is unset until you set it, which is not always the
same as off: the prose page says which.

Every model block overlays `models.default`. A role, a route or an
ensemble entry sets only what differs, and a key it leaves out is served by the
default's value, so that is what the Default column carries for those rows
rather than the zero of the field's type.

Endpoint and credential keys (`base_url`, `api_key_env`, `extra`,
`allow_private_endpoint`, `api_key_keyring`, `credential_command`) are
withheld from a repository's own file unless `NITPICK_TRUST_CONFIG_ENDPOINTS=1`
is set, for every role. So is `persona.custom`. See
[Trust model](trust-model.md).

## instructions

| Key | Type | Default | What it does |
|---|---|---|---|
| `instructions[].path` | string | `none` | Path is a glob matched against each changed file's repository-relative path, in the doublestar dialect, so "**/*.go" reaches every directory. |
| `instructions[].prompt` | string | `none` | Prompt is appended to the review prompt for a matching file. |

## linters

| Key | Type | Default | What it does |
|---|---|---|---|
| `linters.auto_detect` | boolean | `true` | AutoDetect runs every catalog analyzer that is installed, isolated from the tree, and executes nothing from it, whenever the change contains files it reads, without each being named in Enabled. |
| `linters.configs` | map of string | `none` | Configs names an analyzer configuration per catalog tool, keyed by the tool's name in linters.enabled, an absolute path that must resolve outside the repository, for the four keys' reasons. |
| `linters.enabled` | list of string | `[golangci-lint ruff]` | Enabled lists runner names to consider. |
| `linters.eslint_config` | string | `none` | ESLintConfig is an absolute path to an eslint flat config outside the repository. |
| `linters.golangci_config` | string | `none` | GolangciConfig is an absolute path to a .golangci.yml outside the repository. |
| `linters.max_severity` | string | `critical` | MaxSeverity is the highest severity a finding attributed to a deterministic analyzer is published and gated at, whatever the analyzer called it. |
| `linters.mode` | string | `auto` | Mode is "auto" (run only runners detected in the repo), "strict" (error when an enabled runner is missing), or "off". |
| `linters.only_changed_lines` | boolean | `true` | OnlyChangedLines drops linter findings on lines the diff did not touch. |
| `linters.ruff_config` | string | `none` | RuffConfig is an absolute path to a ruff.toml or pyproject.toml outside the repository. |
| `linters.semgrep_config` | string | `none` | SemgrepConfig is an absolute path to a rule file outside the repository, or a registry reference (`p/...`, `r/...`). |
| `linters.timeout` | duration | `2m0s` | Timeout bounds each individual runner. |
| `linters.trusted` | list of string | `none` | Trusted names analyzers that EXECUTE the tree's own code in order to analyze it, cargo clippy runs build scripts and procedural macros, phpstan loads the project's autoloader, and that are therefore refused by default. |

## models

| Key | Type | Default | What it does |
|---|---|---|---|
| `models.default.allow_private_endpoint` | boolean | `false` | AllowPrivateEndpoint permits base_url to use plain HTTP or resolve to a loopback or private address. |
| `models.default.api_key_env` | string | `none` | APIKeyEnv names the environment variable holding the credential. |
| `models.default.api_key_keyring` | string | `none` | APIKeyKeyring names a secret in the operating system's keystore as "service/account", for example "open-nitpick/synthetic". |
| `models.default.base_url` | string | `none` | BaseURL points at an alternate endpoint. |
| `models.default.credential_command` | list of string | `none` | CredentialCommand is a command whose standard output is the credential, for a secret manager the keystore cannot reach: 1Password, AWS Secrets Manager, Vault. |
| `models.default.extra` | map of string | `none` | Extra carries provider-specific construction parameters (for example runpod's endpoint_id) straight through to the SDK. |
| `models.default.fallback` | same keys as models.default | `none` | Fallback is the model a role escalates to when this one cannot answer: a request cut at the output cap because the model looped, or structured output that never parsed. |
| `models.default.max_retries` | integer | `none` | MaxRetries bounds SDK-level retries for transient failures. |
| `models.default.max_tokens` | integer | `0` | MaxTokens caps the response. |
| `models.default.model` | string | `none` | Model is the model id as that provider spells it, which is not a name this project validates: an id the vendor does not serve fails at the call, not at load. |
| `models.default.provider` | string | `none` | Provider names the vendor or gateway the call goes to. "nitpick providers" prints the list. |
| `models.default.providers` | list of string | `none` | Providers pins a router to these upstream providers, tried in order, with no fallback beyond them. |
| `models.default.structured_output` | string | `auto` | StructuredOutput selects how findings are constrained to the schema: "auto" (default) prefers a JSON-Schema response format and falls back to JSON mode and then to prompt-carried text, "schema" forces the schema path, "json" forces JSON mode, "text" forces the text path, where the schema rides in the prompt and the reply is parsed leniently. |
| `models.default.temperature` | number | `0` | Temperature is passed through unchanged. |
| `models.default.timeout` | duration | `10m0s` | Timeout bounds one call, retries excluded. |
| `models.ensemble[].allow_private_endpoint` | boolean | `false` | AllowPrivateEndpoint permits base_url to use plain HTTP or resolve to a loopback or private address. |
| `models.ensemble[].api_key_env` | string | `none` | APIKeyEnv names the environment variable holding the credential. |
| `models.ensemble[].api_key_keyring` | string | `none` | APIKeyKeyring names a secret in the operating system's keystore as "service/account", for example "open-nitpick/synthetic". |
| `models.ensemble[].base_url` | string | `none` | BaseURL points at an alternate endpoint. |
| `models.ensemble[].credential_command` | list of string | `none` | CredentialCommand is a command whose standard output is the credential, for a secret manager the keystore cannot reach: 1Password, AWS Secrets Manager, Vault. |
| `models.ensemble[].extra` | map of string | `none` | Extra carries provider-specific construction parameters (for example runpod's endpoint_id) straight through to the SDK. |
| `models.ensemble[].fallback` | same keys as models.ensemble[] | `none` | Fallback is the model a role escalates to when this one cannot answer: a request cut at the output cap because the model looped, or structured output that never parsed. |
| `models.ensemble[].max_retries` | integer | `none` | MaxRetries bounds SDK-level retries for transient failures. |
| `models.ensemble[].max_tokens` | integer | `0` | MaxTokens caps the response. |
| `models.ensemble[].model` | string | `none` | Model is the model id as that provider spells it, which is not a name this project validates: an id the vendor does not serve fails at the call, not at load. |
| `models.ensemble[].provider` | string | `none` | Provider names the vendor or gateway the call goes to. "nitpick providers" prints the list. |
| `models.ensemble[].providers` | list of string | `none` | Providers pins a router to these upstream providers, tried in order, with no fallback beyond them. |
| `models.ensemble[].structured_output` | string | `auto` | StructuredOutput selects how findings are constrained to the schema: "auto" (default) prefers a JSON-Schema response format and falls back to JSON mode and then to prompt-carried text, "schema" forces the schema path, "json" forces JSON mode, "text" forces the text path, where the schema rides in the prompt and the reply is parsed leniently. |
| `models.ensemble[].temperature` | number | `0` | Temperature is passed through unchanged. |
| `models.ensemble[].timeout` | duration | `10m0s` | Timeout bounds one call, retries excluded. |
| `models.fix.allow_private_endpoint` | boolean | `false` | AllowPrivateEndpoint permits base_url to use plain HTTP or resolve to a loopback or private address. |
| `models.fix.api_key_env` | string | `none` | APIKeyEnv names the environment variable holding the credential. |
| `models.fix.api_key_keyring` | string | `none` | APIKeyKeyring names a secret in the operating system's keystore as "service/account", for example "open-nitpick/synthetic". |
| `models.fix.base_url` | string | `none` | BaseURL points at an alternate endpoint. |
| `models.fix.credential_command` | list of string | `none` | CredentialCommand is a command whose standard output is the credential, for a secret manager the keystore cannot reach: 1Password, AWS Secrets Manager, Vault. |
| `models.fix.extra` | map of string | `none` | Extra carries provider-specific construction parameters (for example runpod's endpoint_id) straight through to the SDK. |
| `models.fix.fallback` | same keys as models.fix | `none` | Fallback is the model a role escalates to when this one cannot answer: a request cut at the output cap because the model looped, or structured output that never parsed. |
| `models.fix.max_retries` | integer | `none` | MaxRetries bounds SDK-level retries for transient failures. |
| `models.fix.max_tokens` | integer | `0` | MaxTokens caps the response. |
| `models.fix.model` | string | `none` | Model is the model id as that provider spells it, which is not a name this project validates: an id the vendor does not serve fails at the call, not at load. |
| `models.fix.provider` | string | `none` | Provider names the vendor or gateway the call goes to. "nitpick providers" prints the list. |
| `models.fix.providers` | list of string | `none` | Providers pins a router to these upstream providers, tried in order, with no fallback beyond them. |
| `models.fix.structured_output` | string | `auto` | StructuredOutput selects how findings are constrained to the schema: "auto" (default) prefers a JSON-Schema response format and falls back to JSON mode and then to prompt-carried text, "schema" forces the schema path, "json" forces JSON mode, "text" forces the text path, where the schema rides in the prompt and the reply is parsed leniently. |
| `models.fix.temperature` | number | `0` | Temperature is passed through unchanged. |
| `models.fix.timeout` | duration | `10m0s` | Timeout bounds one call, retries excluded. |
| `models.review.allow_private_endpoint` | boolean | `false` | AllowPrivateEndpoint permits base_url to use plain HTTP or resolve to a loopback or private address. |
| `models.review.api_key_env` | string | `none` | APIKeyEnv names the environment variable holding the credential. |
| `models.review.api_key_keyring` | string | `none` | APIKeyKeyring names a secret in the operating system's keystore as "service/account", for example "open-nitpick/synthetic". |
| `models.review.base_url` | string | `none` | BaseURL points at an alternate endpoint. |
| `models.review.credential_command` | list of string | `none` | CredentialCommand is a command whose standard output is the credential, for a secret manager the keystore cannot reach: 1Password, AWS Secrets Manager, Vault. |
| `models.review.extra` | map of string | `none` | Extra carries provider-specific construction parameters (for example runpod's endpoint_id) straight through to the SDK. |
| `models.review.fallback` | same keys as models.review | `none` | Fallback is the model a role escalates to when this one cannot answer: a request cut at the output cap because the model looped, or structured output that never parsed. |
| `models.review.max_retries` | integer | `none` | MaxRetries bounds SDK-level retries for transient failures. |
| `models.review.max_tokens` | integer | `0` | MaxTokens caps the response. |
| `models.review.model` | string | `none` | Model is the model id as that provider spells it, which is not a name this project validates: an id the vendor does not serve fails at the call, not at load. |
| `models.review.provider` | string | `none` | Provider names the vendor or gateway the call goes to. "nitpick providers" prints the list. |
| `models.review.providers` | list of string | `none` | Providers pins a router to these upstream providers, tried in order, with no fallback beyond them. |
| `models.review.structured_output` | string | `auto` | StructuredOutput selects how findings are constrained to the schema: "auto" (default) prefers a JSON-Schema response format and falls back to JSON mode and then to prompt-carried text, "schema" forces the schema path, "json" forces JSON mode, "text" forces the text path, where the schema rides in the prompt and the reply is parsed leniently. |
| `models.review.temperature` | number | `0` | Temperature is passed through unchanged. |
| `models.review.timeout` | duration | `10m0s` | Timeout bounds one call, retries excluded. |
| `models.router.allow_private_endpoint` | boolean | `false` | AllowPrivateEndpoint permits base_url to use plain HTTP or resolve to a loopback or private address. |
| `models.router.api_key_env` | string | `none` | APIKeyEnv names the environment variable holding the credential. |
| `models.router.api_key_keyring` | string | `none` | APIKeyKeyring names a secret in the operating system's keystore as "service/account", for example "open-nitpick/synthetic". |
| `models.router.base_url` | string | `none` | BaseURL points at an alternate endpoint. |
| `models.router.credential_command` | list of string | `none` | CredentialCommand is a command whose standard output is the credential, for a secret manager the keystore cannot reach: 1Password, AWS Secrets Manager, Vault. |
| `models.router.extra` | map of string | `none` | Extra carries provider-specific construction parameters (for example runpod's endpoint_id) straight through to the SDK. |
| `models.router.fallback` | same keys as models.router | `none` | Fallback is the model a role escalates to when this one cannot answer: a request cut at the output cap because the model looped, or structured output that never parsed. |
| `models.router.max_retries` | integer | `none` | MaxRetries bounds SDK-level retries for transient failures. |
| `models.router.max_tokens` | integer | `0` | MaxTokens caps the response. |
| `models.router.model` | string | `none` | Model is the model id as that provider spells it, which is not a name this project validates: an id the vendor does not serve fails at the call, not at load. |
| `models.router.provider` | string | `none` | Provider names the vendor or gateway the call goes to. "nitpick providers" prints the list. |
| `models.router.providers` | list of string | `none` | Providers pins a router to these upstream providers, tried in order, with no fallback beyond them. |
| `models.router.structured_output` | string | `auto` | StructuredOutput selects how findings are constrained to the schema: "auto" (default) prefers a JSON-Schema response format and falls back to JSON mode and then to prompt-carried text, "schema" forces the schema path, "json" forces JSON mode, "text" forces the text path, where the schema rides in the prompt and the reply is parsed leniently. |
| `models.router.temperature` | number | `0` | Temperature is passed through unchanged. |
| `models.router.timeout` | duration | `10m0s` | Timeout bounds one call, retries excluded. |
| `models.routes[].ensemble[].allow_private_endpoint` | boolean | `false` | AllowPrivateEndpoint permits base_url to use plain HTTP or resolve to a loopback or private address. |
| `models.routes[].ensemble[].api_key_env` | string | `none` | APIKeyEnv names the environment variable holding the credential. |
| `models.routes[].ensemble[].api_key_keyring` | string | `none` | APIKeyKeyring names a secret in the operating system's keystore as "service/account", for example "open-nitpick/synthetic". |
| `models.routes[].ensemble[].base_url` | string | `none` | BaseURL points at an alternate endpoint. |
| `models.routes[].ensemble[].credential_command` | list of string | `none` | CredentialCommand is a command whose standard output is the credential, for a secret manager the keystore cannot reach: 1Password, AWS Secrets Manager, Vault. |
| `models.routes[].ensemble[].extra` | map of string | `none` | Extra carries provider-specific construction parameters (for example runpod's endpoint_id) straight through to the SDK. |
| `models.routes[].ensemble[].fallback` | same keys as models.routes[].ensemble[] | `none` | Fallback is the model a role escalates to when this one cannot answer: a request cut at the output cap because the model looped, or structured output that never parsed. |
| `models.routes[].ensemble[].max_retries` | integer | `none` | MaxRetries bounds SDK-level retries for transient failures. |
| `models.routes[].ensemble[].max_tokens` | integer | `0` | MaxTokens caps the response. |
| `models.routes[].ensemble[].model` | string | `none` | Model is the model id as that provider spells it, which is not a name this project validates: an id the vendor does not serve fails at the call, not at load. |
| `models.routes[].ensemble[].provider` | string | `none` | Provider names the vendor or gateway the call goes to. "nitpick providers" prints the list. |
| `models.routes[].ensemble[].providers` | list of string | `none` | Providers pins a router to these upstream providers, tried in order, with no fallback beyond them. |
| `models.routes[].ensemble[].structured_output` | string | `auto` | StructuredOutput selects how findings are constrained to the schema: "auto" (default) prefers a JSON-Schema response format and falls back to JSON mode and then to prompt-carried text, "schema" forces the schema path, "json" forces JSON mode, "text" forces the text path, where the schema rides in the prompt and the reply is parsed leniently. |
| `models.routes[].ensemble[].temperature` | number | `0` | Temperature is passed through unchanged. |
| `models.routes[].ensemble[].timeout` | duration | `10m0s` | Timeout bounds one call, retries excluded. |
| `models.routes[].match.kinds` | list of string | `none` | Kinds the router assigned the batch. |
| `models.routes[].match.languages` | list of string | `none` | Languages the batch's files are in, by the extension map in the bundle package ("go", "python", "typescript", ...). |
| `models.routes[].match.max_files` | integer | `0` | MaxFiles bounds how many files the batch may hold. |
| `models.routes[].match.min_files` | integer | `0` | MinFiles bounds how few files the batch may hold. |
| `models.routes[].name` | string | `none` | Name labels the route in logs and the report. |
| `models.routes[].review.allow_private_endpoint` | boolean | `false` | AllowPrivateEndpoint permits base_url to use plain HTTP or resolve to a loopback or private address. |
| `models.routes[].review.api_key_env` | string | `none` | APIKeyEnv names the environment variable holding the credential. |
| `models.routes[].review.api_key_keyring` | string | `none` | APIKeyKeyring names a secret in the operating system's keystore as "service/account", for example "open-nitpick/synthetic". |
| `models.routes[].review.base_url` | string | `none` | BaseURL points at an alternate endpoint. |
| `models.routes[].review.credential_command` | list of string | `none` | CredentialCommand is a command whose standard output is the credential, for a secret manager the keystore cannot reach: 1Password, AWS Secrets Manager, Vault. |
| `models.routes[].review.extra` | map of string | `none` | Extra carries provider-specific construction parameters (for example runpod's endpoint_id) straight through to the SDK. |
| `models.routes[].review.fallback` | same keys as models.routes[].review | `none` | Fallback is the model a role escalates to when this one cannot answer: a request cut at the output cap because the model looped, or structured output that never parsed. |
| `models.routes[].review.max_retries` | integer | `none` | MaxRetries bounds SDK-level retries for transient failures. |
| `models.routes[].review.max_tokens` | integer | `0` | MaxTokens caps the response. |
| `models.routes[].review.model` | string | `none` | Model is the model id as that provider spells it, which is not a name this project validates: an id the vendor does not serve fails at the call, not at load. |
| `models.routes[].review.provider` | string | `none` | Provider names the vendor or gateway the call goes to. "nitpick providers" prints the list. |
| `models.routes[].review.providers` | list of string | `none` | Providers pins a router to these upstream providers, tried in order, with no fallback beyond them. |
| `models.routes[].review.structured_output` | string | `auto` | StructuredOutput selects how findings are constrained to the schema: "auto" (default) prefers a JSON-Schema response format and falls back to JSON mode and then to prompt-carried text, "schema" forces the schema path, "json" forces JSON mode, "text" forces the text path, where the schema rides in the prompt and the reply is parsed leniently. |
| `models.routes[].review.temperature` | number | `0` | Temperature is passed through unchanged. |
| `models.routes[].review.timeout` | duration | `10m0s` | Timeout bounds one call, retries excluded. |
| `models.triage.allow_private_endpoint` | boolean | `false` | AllowPrivateEndpoint permits base_url to use plain HTTP or resolve to a loopback or private address. |
| `models.triage.api_key_env` | string | `none` | APIKeyEnv names the environment variable holding the credential. |
| `models.triage.api_key_keyring` | string | `none` | APIKeyKeyring names a secret in the operating system's keystore as "service/account", for example "open-nitpick/synthetic". |
| `models.triage.base_url` | string | `none` | BaseURL points at an alternate endpoint. |
| `models.triage.credential_command` | list of string | `none` | CredentialCommand is a command whose standard output is the credential, for a secret manager the keystore cannot reach: 1Password, AWS Secrets Manager, Vault. |
| `models.triage.extra` | map of string | `none` | Extra carries provider-specific construction parameters (for example runpod's endpoint_id) straight through to the SDK. |
| `models.triage.fallback` | same keys as models.triage | `none` | Fallback is the model a role escalates to when this one cannot answer: a request cut at the output cap because the model looped, or structured output that never parsed. |
| `models.triage.max_retries` | integer | `none` | MaxRetries bounds SDK-level retries for transient failures. |
| `models.triage.max_tokens` | integer | `0` | MaxTokens caps the response. |
| `models.triage.model` | string | `none` | Model is the model id as that provider spells it, which is not a name this project validates: an id the vendor does not serve fails at the call, not at load. |
| `models.triage.provider` | string | `none` | Provider names the vendor or gateway the call goes to. "nitpick providers" prints the list. |
| `models.triage.providers` | list of string | `none` | Providers pins a router to these upstream providers, tried in order, with no fallback beyond them. |
| `models.triage.structured_output` | string | `auto` | StructuredOutput selects how findings are constrained to the schema: "auto" (default) prefers a JSON-Schema response format and falls back to JSON mode and then to prompt-carried text, "schema" forces the schema path, "json" forces JSON mode, "text" forces the text path, where the schema rides in the prompt and the reply is parsed leniently. |
| `models.triage.temperature` | number | `0` | Temperature is passed through unchanged. |
| `models.triage.timeout` | duration | `10m0s` | Timeout bounds one call, retries excluded. |
| `models.validate.allow_private_endpoint` | boolean | `false` | AllowPrivateEndpoint permits base_url to use plain HTTP or resolve to a loopback or private address. |
| `models.validate.api_key_env` | string | `none` | APIKeyEnv names the environment variable holding the credential. |
| `models.validate.api_key_keyring` | string | `none` | APIKeyKeyring names a secret in the operating system's keystore as "service/account", for example "open-nitpick/synthetic". |
| `models.validate.base_url` | string | `none` | BaseURL points at an alternate endpoint. |
| `models.validate.credential_command` | list of string | `none` | CredentialCommand is a command whose standard output is the credential, for a secret manager the keystore cannot reach: 1Password, AWS Secrets Manager, Vault. |
| `models.validate.extra` | map of string | `none` | Extra carries provider-specific construction parameters (for example runpod's endpoint_id) straight through to the SDK. |
| `models.validate.fallback` | same keys as models.validate | `none` | Fallback is the model a role escalates to when this one cannot answer: a request cut at the output cap because the model looped, or structured output that never parsed. |
| `models.validate.max_retries` | integer | `none` | MaxRetries bounds SDK-level retries for transient failures. |
| `models.validate.max_tokens` | integer | `0` | MaxTokens caps the response. |
| `models.validate.model` | string | `none` | Model is the model id as that provider spells it, which is not a name this project validates: an id the vendor does not serve fails at the call, not at load. |
| `models.validate.provider` | string | `none` | Provider names the vendor or gateway the call goes to. "nitpick providers" prints the list. |
| `models.validate.providers` | list of string | `none` | Providers pins a router to these upstream providers, tried in order, with no fallback beyond them. |
| `models.validate.structured_output` | string | `auto` | StructuredOutput selects how findings are constrained to the schema: "auto" (default) prefers a JSON-Schema response format and falls back to JSON mode and then to prompt-carried text, "schema" forces the schema path, "json" forces JSON mode, "text" forces the text path, where the schema rides in the prompt and the reply is parsed leniently. |
| `models.validate.temperature` | number | `0` | Temperature is passed through unchanged. |
| `models.validate.timeout` | duration | `10m0s` | Timeout bounds one call, retries excluded. |

## persona

| Key | Type | Default | What it does |
|---|---|---|---|
| `persona.address` | string | `impersonal` | Address selects second person ("you dropped the error") or impersonal ("the error is dropped"). |
| `persona.confidence` | string | `direct` | Confidence controls hedging. |
| `persona.custom` | string | `none` | Persona is a free-text overlay appended last, for teams that want a house voice the built-in axes do not cover. |
| `persona.emoji` | boolean | `true` | Emoji prefixes severities with a coloured marker. |
| `persona.nitpick` | string | `normal` | Nitpick sets how far beyond outright defects the reviewer ranges. |
| `persona.politeness` | string | `neutral` | Politeness controls softening language and acknowledgement. |
| `persona.praise` | boolean | `false` | Praise permits acknowledging good work. |
| `persona.verbosity` | string | `normal` | Verbosity controls how much prose accompanies each finding. |

## review

| Key | Type | Default | What it does |
|---|---|---|---|
| `review.agent_prompt` | boolean | `false` | AgentPrompt adds a collapsed block under each published finding holding what a coding agent needs to act on it: the anchor, every secondary span, the class, the rationale as the reviewer wrote it, and the files the reviewer read for that batch. |
| `review.approve.enabled` | boolean | `false` | Enabled submits APPROVE when the review published no findings and reviewed every file it planned to. |
| `review.approve.require_analyzers` | boolean | `false` | RequireAnalyzers additionally demands that every enabled analyzer ran and covered the change, so an approval means the deterministic half happened rather than that it was absent. |
| `review.budget.completion_ratio` | number | `0.25` | CompletionRatio estimates output tokens as a fraction of prompt tokens, since what a model will write is not knowable before it writes it. |
| `review.budget.max_spend` | number | `0` | MaxSpend is the ceiling in US dollars, and zero, the default, is no ceiling and no estimation. |
| `review.budget.min_files` | integer | `0` | MinFiles is how many of the highest-ranked files are reviewed even when the estimate says they do not fit. |
| `review.budget.overhead` | number | `1` | Overhead scales the review estimate to cover the triage pass, the optional router, and validation. |
| `review.budget.prices.input` | number | `0` | Input is dollars per million prompt tokens, as you supply it. |
| `review.budget.prices.output` | number | `0` | Output is dollars per million completion tokens, on the same terms. |
| `review.budget.scope` | string | `run` | Scope says what the ceiling covers. |
| `review.concurrency` | integer | `4` | Concurrency bounds in-flight model calls. |
| `review.fail_on` | string | `none` | FailOn is the lowest severity that makes the run exit non-zero. "none" never fails the run. |
| `review.ignore` | list of string | `[**/vendor/** **/node_modules/** **/testdata/** **/dist/** **/build/** **/*.pb.go **/*.gen.go **/*_generated.go **/*.min.js **/*.map **/go.sum **/package-lock.json **/pnpm-lock.yaml **/yarn.lock **/Cargo.lock **/poetry.lock **/*.snap **/*.svg **/*.png **/*.jpg **/*.gif **/*.ico **/*.pdf]` | Ignore lists doublestar globs excluded from review. |
| `review.include_full_files` | boolean | `true` | IncludeFullFiles sends whole changed files alongside the diff when the token budget allows. |
| `review.incremental` | boolean | `true` | Incremental makes a run on a pull request this tool has reviewed before read only the files changed since that review, and withhold findings it has already posted. |
| `review.max_file_bytes` | integer | `262144` | MaxFileBytes skips files larger than this when reading full contents. |
| `review.max_files` | integer | `60` | MaxFiles caps how many changed files are reviewed in one run. |
| `review.max_files_per_request` | integer | `6` | MaxFilesPerRequest caps how many files are batched into one call. |
| `review.mention` | string | `@open-nitpick` | Mention is the handle a comment uses to talk to the reviewer: "@open-nitpick review" reviews again, "@open-nitpick resolve" closes the thread, anything else is a question answered in the thread. |
| `review.min_severity` | string | `info` | MinSeverity drops findings below this severity before publishing. |
| `review.model_notes` | boolean | `true` | ModelNotes adds the prompt layer addressed to the reviewing model's family (prompt.ModelGuidance). |
| `review.related_context` | boolean | `true` | RelatedContext attaches, beside each changed file, the definitions it imports from elsewhere in the repository and uses on a changed line, so the model can read what a called function does instead of guessing. |
| `review.related_context_callers` | boolean | `false` | RelatedContextCallers also attaches, for each exported symbol the change redefines, the untouched functions that call it, found by walking the repository's own files. |
| `review.related_context_tokens` | integer | `16000` | RelatedContextTokens bounds how much related context is attached per batch. |
| `review.resolve_superseded` | boolean | `true` | ResolveSuperseded lets an incremental run resolve its own earlier comment threads when the lines they pointed at changed and the finding did not recur, with a reply saying so. |
| `review.respond.fix.from` | list of string | `[owner member collaborator]` | From lists the associations allowed to ask. |
| `review.respond.from` | list of string | `[owner member collaborator]` | From lists the associations allowed to command the reviewer. |
| `review.respond.max_per_pull_request` | integer | `0` | MaxPerPullRequest caps how many comments the reviewer answers on one pull request, counting its own replies as the record of how many it has answered, and zero, the default, is no cap at all. |
| `review.skip_generated` | boolean | `true` | SkipGenerated drops files carrying a generated-code marker. |
| `review.skip_markers` | list of string | `[[skip review] [skip nitpick]]` | SkipMarkers are phrases that, in a pull request's title, body or head commit message, ask for no review: the run reports "skipped" and posts nothing. |
| `review.slop` | boolean | `false` | Slop asks the model for, and publishes, findings in the slop class: generated-looking code that costs a reader, defined rule by rule in the prompt layer prompt.SlopGuidance. |
| `review.summary` | boolean | `true` | Summary emits a walkthrough summary alongside inline comments. |
| `review.summary_style` | string | `receipt` | SummaryStyle chooses how the walkthrough at the top of a review is produced. |
| `review.token_budget_per_request` | integer | `60000` | TokenBudgetPerRequest bounds the context assembled for a single model call, including full file bodies. |
| `review.triage_no_new_claims` | boolean | `false` | TriageNoNewClaims restores the reviewer's own words over anything triage rewrote, so triage may select, drop, group and re-anchor findings but may not author them. |

## validation

| Key | Type | Default | What it does |
|---|---|---|---|
| `validation.classes` | list of string | `none` | Classes limits validation to these finding classes, so a team can validate security and skip style. |
| `validation.enabled` | boolean | `false` | Enabled turns the pass on. |

Anything not listed is not a key. The loader rejects an unrecognised one rather
than ignoring it, so a typo fails the run instead of silently doing nothing.
