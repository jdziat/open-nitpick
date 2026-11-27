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
  - {role: insights, group: engineering}
```

```sh
nitpick-server -config nitpick-server.yaml -data /var/lib/nitpick \
  -insights-token ci:write:$CI_SECRET \
  -insights-token agent:read:$AGENT_SECRET
```

Reviews are kept in `<data>/insights/reviews.jsonl`. The server holds an index of
them in memory and rebuilds it at start-up. A line that cannot be read, such as
the tail a crash left, is skipped and logged with its count, and the server
starts.

| Token role | May |
|---|---|
| `write` | post reviews and read |
| `read` | read |

A signed-in person needs the `insights` role. It reads and never writes. Secrets
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
and commit it ran against. The same review sent twice is kept once. Finding text is
scrubbed of credential-shaped strings before it is stored, a review keeps at most
500 findings (the page says how many were dropped), and a path that leaves the
repository is refused.

## The pages

| Page | Question it answers |
|---|---|
| Overview | How many reviews and findings, up or down against the period before, by day, severity and class |
| Repositories | Which repositories are being reviewed, and which have the most findings |
| Hotspots | Which files keep producing findings, and whether that is getting worse |
| Findings | The findings themselves, filtered by file, class and severity |
| Reviews | Each run, and whether it finished |

A review that did not finish is marked everywhere it appears. Its zero findings are
not a clean result, and the overview counts those reviews apart.

### How a hotspot is scored

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
| `GET /v1/insights/overview`, `/hotspots`, `/findings` | read |

The read routes take `days` (1 to 365, default 30) and `repo`. `findings` also
takes `path` (a file, or a directory ending in `/`), `class`, `min_severity` and
`limit`. A bad value is a 400, not a default.

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
