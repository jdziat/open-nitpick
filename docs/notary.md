# Notary

A notary keeps the model answers of a review, so the same inputs replay them
with no model call, and a review that was interrupted asks only for what is
missing. It is a component of the [nitpick server](server.md), `nitpick-server`,
and a client built into `nitpick review`, `fast-review` and `deep-review`.

## Moving from nitpick-notary

`nitpick-server` replaces the `nitpick-notary` command, and the notary is now a
component of it. Nothing about records changes: the routes, the roles, the machine
authentication, the record format and its hashing, and the data directory are the
same, and a data directory written by `nitpick-notary` is read by `nitpick-server`
unchanged (a test holds a fixture from the last release of the old command to
that). A deployment changes its binary name and nothing else.

| Before | Now |
| --- | --- |
| `nitpick-notary -addr … -data … -token …` | `nitpick-server -addr … -data … -token …` |
| `NITPICK_NOTARY_TOKENS` | the same variable |
| image `ghcr.io/nitpick-oss/nitpick-notary` | `ghcr.io/nitpick-oss/nitpick-server` |
| chart `oci://ghcr.io/nitpick-oss/charts/nitpick-notary` | `oci://ghcr.io/nitpick-oss/charts/nitpick-server` |
| helm values | the same values, plus `config.*` for sign-in |

The old image and chart stay on the registry and get no new versions; there is no
pointer release under the old name. What is new is `GET /v1/records`, a paginated
listing for the `authoritative` role, and the notary pages of the hub, described in
[the server](server.md#the-notary-pages).

## What a record is

A record is filed under a SHA-256 hash of everything that decides what the
review says: the tool version, the provider and model, the system prompt, the
resolved policy, the mode flags (`deep`, `fast`, skip-triage, instruction) and
the packed diff of every batch. Change any of them and the hash changes, so a
different review is never answered from the record of another. The hash is
length-prefixed field by field, so two different splits of the same bytes do
not collide, and batches are sorted so the hash does not depend on the order
they were packed in.

The record holds one answer per model request (the n-th identical request has
its own answer, so `review.votes` keeps its votes apart), keyed by a hash of the request,
across every stage: batch review, triage, critique and the rest. That one
mechanism is both replay (every request answered, so no model is called) and
resume (some answered, so the rest are asked live).

Requests are keyed by provider, model and messages, not by endpoint or
credential, so a record made in CI replays on a developer machine.

The reasoning level is stored with the record and left out of the hash, so a
replay answers with what the recorded level produced whatever the configuration
says today. A partial record made at another level is not resumed, because its
answers would sit beside live ones from a different level.

With a usable notary the checkpoint that a review stores on its pull request is
not used: the notary is the resume record, and every model request goes through
it. Otherwise an answer served from the checkpoint would be missing from a record
marked complete.

A record is complete only when the review it belongs to finished with no
failed batch or stage. A review that lost one leaves the record partial, which
is what lets the next run resume instead of replaying a hole. An answer is kept
only if it finished (not truncated, not empty) and, when JSON was requested,
only if it is JSON: otherwise a reply that failed to parse would be replayed
forever.

## Running one

```sh
nitpick-server -addr 127.0.0.1:8484 -data /var/lib/notary \
  -token ci:authoritative:$CI_SECRET \
  -token dev:provisional:$DEV_SECRET \
  -token viewer:read:$VIEW_SECRET
```

| Role | May |
| --- | --- |
| `authoritative` | Write the record CI trusts. May replace any record. |
| `provisional` | Write only where no complete record exists. Never replaces a complete one. |
| `read` | Read only. A read token replays; it records nothing. |

`-author-clear` lets provisional tokens clear a record (`-fresh`); authoritative
tokens always may. Secrets are hashed when the server loads them and compared
in constant time. Only the hash is held, and no secret is written to disk or
to the log.

### GitHub Actions identity instead of a stored secret

```sh
nitpick-server -oidc-audience nitpick-notary \
  -oidc-allow 'authoritative:repo:acme/app:ref:refs/heads/main' \
  -oidc-allow 'provisional:repo:acme/app:*'
```

A workflow token is accepted when it is signed RS256 by the issuer's published
key, names `-oidc-issuer` (default GitHub's), carries the audience, has not
expired, and its subject matches an `-oidc-allow` rule. The first matching rule
decides the role. `*` matches any run of characters, `/` and `:` included. A
subject no rule matches is refused, so an empty list admits nobody, and the
two flags must be given together. Static tokens keep working beside it.

## Deploying one

Tokens for a deployment are given as hashes, so a configuration file or a
Kubernetes Secret never holds a usable credential. Hash a secret with the server
itself:

```sh
printf %s "$SECRET" | nitpick-server -hash      # sha256:<64 hex>
```

`-token name:role:sha256:<hex>` and the `NITPICK_NOTARY_TOKENS` environment
variable (whitespace-separated entries, for a container) take that form. The
hash is not a bearer token: presenting it is refused. A plain secret must not
start with `sha256:`, since that prefix means the rest is a hash; one that does
is refused at startup unless the rest is 64 hex characters. `GET /healthz` answers
`ok` with no token, followed by a line for each component, and serves no record, and `nitpick-server -healthcheck` probes
it, for an image with no shell.

### Docker

```sh
cd deploy
cp .env.example .env            # put token hashes in it
docker compose up -d --build
curl -s localhost:8484/healthz
```

The image holds the server and the hub UI, runs as an unprivileged user on a read-only root filesystem,
and keeps records on the `notary-data` volume. The compose file binds loopback
only.

### Kubernetes

```sh
helm install nitpick oci://ghcr.io/nitpick-oss/charts/nitpick-server \
  --set 'tokens[0].name=ci' --set 'tokens[0].role=authoritative' --set 'tokens[0].hash=<64 hex>'
```

The chart refuses a hash that is not 64 lowercase hex characters (a secret put
there by mistake), an unknown role, and a configuration with no way to
authenticate. It runs one replica on a `ReadWriteOnce` volume and replaces the pod
rather than rolling it, because the record files are owned by one process. The
volume claim is kept when the release is deleted, and installing again under the
same release name picks it up with its records. Set `existingSecret` to supply
the `tokens` entry from a Secret you manage, and `oidc.*` for GitHub Actions
identity tokens. The image and chart are published to `ghcr.io` when a release
tag is pushed by the `Server image` workflow.

## Limits

One server may run on a data directory: it takes an exclusive lock, and a second
exits with a message saying so, because the rule that a partial record never
replaces a complete one is kept by a lock inside the process. The server closes
a connection that is idle for 60 seconds or that takes two minutes to send a
request, which bounds an outage from connections held open and does not stop a
client that reconnects: put a proxy in front of it, or limit connections per
address.

A record file that cannot be decoded is set aside in `history/` by the next
write or clear, and reading it is an error. A store error is logged on the
server and the client is told only that the store could not be used.

A review gives the notary up after three failed writes in a row, and is then
unofficial. The `review.jev` decision model is recorded with the rest.

The hash covers the whole resolved configuration, so `-fail-on`,
`review.concurrency` and a model's timeout or retries change it, and a record
made under one setting is not replayed under another. Each request is also keyed
by its own content, so a changed prompt is always asked again.


## Using one

```sh
export NITPICK_NOTARY_TOKEN=...            # or -notary-oidc-audience in Actions
nitpick review -base origin/main -notary https://notary.example.com
nitpick review -base origin/main -notary https://notary.example.com -fresh
```

`-fresh` clears the record for these inputs and reviews again. The cleared
record is kept as history, with who cleared it (`-clear-by`) and a count.

For a job minting its own token, give the job `permissions: id-token: write`
and pass `-notary-oidc-audience`. The token is fetched for each request, since a
review can outlast it.

## Unofficial reviews

`-require-notary` labels a review UNOFFICIAL, and does not post it, unless a
notary kept its record. That covers no `-notary` at all, a notary that cannot
be reached, a token the notary refuses, and one that refuses every write. The
findings still print and the gate still applies: unofficial describes the
record, not the result. A replay of a record someone else wrote is official
whoever reads it.

A review whose plan packs no batch has no model answer to record. With a notary
configured it posts. With none at all, `-require-notary` withholds it, an
approval included.

`-fresh`, `-clear-by` and `-notary-oidc-audience` need `-notary`, and are
refused without it rather than ignored.

Without `-require-notary` a failure of the notary never stops a review. It is
logged, the review carries on without a record, and the report says so.

## Telling an empty replay from nothing having run

`-format json` prints a `notary` object:

| Field | Meaning |
| --- | --- |
| `Mode` | `live`, `resumed`, `replayed` or `unreachable`. Empty only when no notary was configured. |
| `Replayed`, `Live`, `Recorded` | Requests answered from the record, requests that reached a model, and writes the notary accepted. |
| `Complete` | Whether the record was complete at the end of the run. |
| `Hash` | The record's hash. |
| `Err` | Why the notary could not be used or written, when it could not. |

A clean review that replayed has `Mode: replayed` and a nonzero `Replayed`. A
review that never reached the notary has an empty `Mode`. Both report zero
findings.

## Trust

Whoever can write an authoritative record decides what a replay says, so give
that role to the job that runs on the protected branch and nothing else. A
record is a cache of model answers, not a signed attestation: the notary does
not prove a model said it, only that a holder of the role stored it.
