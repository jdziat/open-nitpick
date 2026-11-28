# Insights: findings over time

The insights component of the [nitpick server](server.md) keeps the findings that
reviews report and answers where bugs concentrate. It shows a dashboard in the
hub, ranks files by how much and how recently they have produced findings, and
serves the same data to agents over MCP.

It holds what a review reported. It does not read your code and it does not run
reviews.

## Turn it on

```yaml
# nitpick-server.yaml
components: [notary, insights]
roles:
  - {role: insights, provider: oidc, group: engineering}
```

```sh
nitpick-server -config nitpick-server.yaml -database-url "$NITPICK_DATABASE_URL" \
  -insights-token ci:write:$CI_SECRET \
  -insights-token agent:read:$AGENT_SECRET
```

Reviews are kept in the server's database, Postgres or MySQL (see
[the notary](notary.md#where-records-are-kept) for the URL), in the table
`insights_reviews`. Any number of servers may share it: a review is kept under an
id taken from what it says, so a CI job that retries, or reaches another server,
is kept once. Reviews an earlier, file-backed server kept are copied in with
`nitpick-server import`, which counts what it copied.

| Token role | May |
|---|---|
| `write` | post reviews and read |
| `read` | read |

A signed-in person needs the `insights` role, and a sign-in alone grants nothing: the
server refuses to start the component unless a token is configured or a role rule
grants `insights`. The role reads and never writes. Secrets
can also be given as `name:role:sha256:<hex>`, or in `NITPICK_INSIGHTS_TOKENS`.

## Send a review

```sh
export NITPICK_INSIGHTS_TOKEN=...
nitpick review -insights https://nitpick.example.com
```

`-insights-repo` names the repository when it cannot be read from the pull request
or `GITHUB_REPOSITORY`. The post happens after the review is published and printed.
If it fails, `nitpick` logs a warning and the exit status stays what the review
decided: a data point that did not arrive does not fail a build that passed.

The server accepts the report that `-format json` prints, with the repository, ref
and commit it ran against. The same review sent twice is kept once, with or without a time. Finding text is
scrubbed of credential-shaped strings before it is stored, a review keeps at most
500 findings (the page says how many were dropped), and a path that leaves the
repository is refused.

## Size and retention

There is no retention yet: every review stays in the table. A request reads the
window it asks for, and the one before it for the change against the period, and
no more (`days` is at most 365), so the cost of a page follows the window and not
the history. A review with the maximum 500 findings is about 1 to 2 MB, and a
typical one is a few kilobytes. If the database cannot be read, a page is a 500
and says so; it is never an empty dashboard, and a stored review that cannot be
decoded fails the read for the same reason. To trim, delete rows from
`insights_reviews` older than you want to keep.

A write token may post up to 8 MiB per request and there is no rate limit. Give
write tokens only to CI jobs you trust. A review dated more than five minutes
ahead of the server's clock is refused with 400, since no page would show it. If
the database cannot take a post, the post fails with 500 and nothing is kept.

Text in a finding (title and rationale) is scrubbed of credential-shaped strings.
Repository, ref, commit, path and result are not, so do not put a secret there.

## The pages

The web app is at `/ui/`. Every table in it pages, sorts, searches and filters, and
the choices are in the address, so a reload or a shared link shows the same rows.

| Page | Question it answers |
|---|---|
| Overview | How many reviews and findings, up or down against the period before, by day, severity and class |
| Repositories | Which repositories are being reviewed, and which have the most findings |
| Hotspots | Which files keep producing findings, and whether that is getting worse |
| Findings | The findings themselves, searched and filtered by file, class and severity |
| Reviews | Each run, and whether it finished |

A review that did not finish is marked everywhere it appears. Its zero findings are
not a clean result, and the overview counts those reviews apart.

### How a hotspot is scored

A hotspot counts findings per review, not distinct defects. A pull request that is
pushed to five times and still has the same finding counts it five times. A file
that is reviewed often therefore ranks above an equally buggy file that is
reviewed rarely, and findings on a pull request that was fixed before merge still
count. Read the rank as "where reviews keep reporting", which tracks where bugs
are but is not a count of them.

Each finding adds a weight by severity: nit 0.25, info 0.5, warning 1, error 3,
critical 8. The weight halves for every 21 days since the review reported it. A
file's score is the sum. A file with many old findings therefore ranks below one
with a few recent severe ones. Only findings inside the period count.

Trend compares a file's findings in the period with the period before it:
up, down or the same. A file with no findings in the period is not listed.

## MCP

The server speaks MCP over streamable HTTP at `/mcp`, with the same bearer tokens:

```json
{ "mcpServers": { "nitpick-insights": {
    "type": "http", "url": "https://nitpick.example.com/mcp",
    "headers": { "Authorization": "Bearer ${NITPICK_INSIGHTS_TOKEN}" } } } }
```

| Tool | Does | Token |
|---|---|---|
| `get_overview` | Counts and the daily series for a period | read |
| `list_hotspots` | Files ranked by score, with trend | read |
| `list_findings` | Findings by file, class and severity, with the number that matched | read |
| `get_review` | One review with its findings | read |
| `submit_review` | Report a review that ran | write |

A read token is not offered `submit_review`. Each request is authenticated on its
own and the server keeps no MCP session, so what a caller may do never depends on
an earlier request.

## HTTP

| Route | Token |
|---|---|
| `POST /v1/reviews` | write |
| `GET /v1/reviews`, `GET /v1/reviews/{id}` | read |
| `GET /v1/insights/overview`, `/hotspots`, `/findings`, `/repos`, `/facets` | read |

The read routes take `days` (1 to 365, default 30) and `repo`. A bad value is a
400, not a default.

Every list route (`/v1/reviews`, `/hotspots`, `/findings`, `/repos`) pages, sorts
and searches the same way and answers in one envelope:

```json
{ "items": [], "total": 134, "page": 1, "page_size": 20, "pages": 7 }
```

| Parameter | Meaning |
|---|---|
| `page` | A whole number from 1. A page past the end is empty, with the true `total`. |
| `page_size` | 1 to 100, default 20. |
| `sort`, `order` | A field the route allows, and `asc` or `desc`. A field it does not allow is a 400. |
| `q` | Text to find, ignoring case, in the fields the route searches. At most 200 characters. |

Each route also has its own filters and sort fields:

| Route | Sort by | Filters |
|---|---|---|
| `/v1/reviews` | `at`, `repo`, `findings`, `errors`, `result` | `complete` (`true` or `false`) |
| `/v1/insights/hotspots` | `score`, `findings`, `path`, `repo`, `delta`, `worst`, `last` | `trend` (`up`, `down`, `flat`), `min_severity` |
| `/v1/insights/findings` | `at`, `severity`, `path`, `repo`, `class`, `title` | `path` (a file, or a directory ending in `/`), `class`, `min_severity` |
| `/v1/insights/repos` | `repo`, `reviews`, `findings`, `errors`, `last` | none |

`/v1/insights/facets` lists the repositories and classes that exist in the
period, so a filter menu offers only values that match something.

## What this does not have

The hub's dashboard follows [Mira](https://github.com/miracodeai/mira)'s
(Apache 2.0): totals with a period toggle, charts, a repository list and a review
history. Mira's other pages depend on features nitpick does not have, and they are
not here.

| Mira | Here |
|---|---|
| Dashboard | Overview |
| Repositories | Repositories |
| Review history and timeline | Reviews, Review |
| Contributor analytics | not here: findings are not attributed to people |
| Packages, Vulnerabilities | not here: `nitpick security` reports these per run |
| Relationships, blast radius | not here |
| Rules, Learnings | configuration and learnings live in the repository, not on the server |
| Users, Settings, model choice | configuration is a file, and sign-in is the server's |
| (none) | Hotspots, over time: findings per file, ranked, with trend |
