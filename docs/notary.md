# Notary

A notary keeps the model answers of a review, so the same inputs replay them
with no model call, and a review that was interrupted asks only for what is
missing. It is a server, `nitpick-notary`, and a client built into
`nitpick review`, `fast-review` and `deep-review`.

## What a record is

A record is filed under a SHA-256 hash of everything that decides what the
review says: the tool version, the provider and model, the system prompt, the
resolved policy, the mode flags (`deep`, `fast`, skip-triage, instruction) and
the packed diff of every batch. Change any of them and the hash changes, so a
different review is never answered from the record of another. The hash is
length-prefixed field by field, so two different splits of the same bytes do
not collide, and batches are sorted so the hash does not depend on the order
they were packed in.

The record holds one answer per model request, keyed by a hash of the request,
across every stage: batch review, triage, critique and the rest. That one
mechanism is both replay (every request answered, so no model is called) and
resume (some answered, so the rest are asked live).

The reasoning level is stored with the record and left out of the hash, so a
replay answers with what the recorded level produced whatever the configuration
says today. A partial record made at another level is not resumed, because its
answers would sit beside live ones from a different level.

A record is complete only when the review it belongs to finished with no
failed batch or stage. A review that lost one leaves the record partial, which
is what lets the next run resume instead of replaying a hole. An answer is kept
only if it finished (not truncated, not empty) and, when JSON was requested,
only if it is JSON: otherwise a reply that failed to parse would be replayed
forever.

## Running one

```sh
nitpick-notary -addr 127.0.0.1:8484 -data /var/lib/notary \
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
nitpick-notary -oidc-audience nitpick-notary \
  -oidc-allow 'authoritative:repo:acme/app:ref:refs/heads/main' \
  -oidc-allow 'provisional:repo:acme/app:*'
```

A workflow token is accepted when it is signed RS256 by the issuer's published
key, names `-oidc-issuer` (default GitHub's), carries the audience, has not
expired, and its subject matches an `-oidc-allow` rule. The first matching rule
decides the role. `*` matches any run of characters, `/` and `:` included. A
subject no rule matches is refused, so an empty list admits nobody, and the
two flags must be given together. Static tokens keep working beside it.

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
