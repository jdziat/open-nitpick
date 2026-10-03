# GitHub Actions and other CI

Running the reviewer on a runner rather than from a terminal: the Action and
the workflow it goes in, the inputs and permissions it needs, what an
incremental review is, what a fork changes, and the same review as a plain CLI
call in any other CI. [Usage](usage.md) is the same tool locally.

## GitHub Actions

```yaml
name: Review
on:
  pull_request:
    types: [opened, synchronize, reopened, ready_for_review]

permissions:
  contents: read
  pull-requests: write

concurrency:
  # A new push supersedes an in-flight review of the same pull request.
  group: nitpick-${{ github.event.pull_request.number }}
  cancel-in-progress: true

jobs:
  review:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0        # the reviewer needs history to diff against base
      - uses: jdziat/open-nitpick@v1
        with:
          provider: openrouter
          model: z-ai/glm-5.3-flash
          api-key: ${{ secrets.OPENROUTER_API_KEY }}
          # Omit fail-on (or set it to none) until you have seen how the model
          # behaves on your codebase. A reviewer that blocks merges on its first
          # false positive is a reviewer the team switches off.
          fail-on: none
          dry-run: true  # inspect the summary before allowing publication
          skip-drafts: true
```

The first run is intentionally non-publishing. Inspect the job summary, then
remove `dry-run: true` when the configuration and results are ready to publish.

### Talking to it

A comment on the pull request that starts with
`@open-nitpick` is answered by a second workflow on `issue_comment` and
`pull_request_review_comment` events, running the Action with
`command: respond`:

```yaml
name: Respond
on:
  issue_comment: {types: [created]}
  pull_request_review_comment: {types: [created]}
permissions: {contents: read, pull-requests: write, issues: write}
jobs:
  respond:
    if: contains(github.event.comment.body, '@open-nitpick')
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with: {fetch-depth: 0}
      - uses: jdziat/open-nitpick@v1
        with:
          command: respond
          provider: openrouter
          model: z-ai/glm-5.3-flash
          api-key: ${{ secrets.OPENROUTER_API_KEY }}
```

`@open-nitpick review` resumes the previous review, or starts one if none exists.
`@open-nitpick restart-review` reviews the whole PR afresh. The older `re-review`
and `rereview` aliases also restart.
`@open-nitpick resolve` on an inline thread resolves it with a reply naming who
asked. `@open-nitpick improve` runs the wider pass, below. Anything else is a
question, answered in the same thread by the
review model with the diff, the lines around the thread, and the thread
so far as its context; forge-authored text is fenced as untrusted, so a
comment cannot instruct the model. The comment gets an eyes reaction when
the run starts and a thumbs-up when it has answered. The handle is
`review.mention` in `.nitpick.yaml`. This repository's own is
[.github/workflows/nitpick-respond.yml](https://github.com/jdziat/open-nitpick/blob/main/.github/workflows/nitpick-respond.yml).

### Superseded comments

On a later review, an earlier inline comment whose file was rechecked and
whose finding did not recur is resolved with a reply
saying so, and the walkthrough counts them. `review.resolve_superseded:
false` leaves threads for a person to close.

### Skipping a pull request

`skip-drafts: true` leaves drafts alone until
they are marked ready. A pull request that should never be reviewed says
so with `[skip review]` (or `[skip nitpick]`) in its title, its body, or
its head commit's message; the run reports `result: skipped`, posts
nothing, and the job summary says which marker it found. The phrases are
`review.skip_markers` in `.nitpick.yaml`, so a repository can choose its
own.

### Posting as your own GitHub App

With the job token the review is posted by `github-actions[bot]`. A GitHub App
has its own identity and repository permissions. Create and install an App with
Contents read, Metadata read, and Pull requests read and write; it does not need
a webhook. Save its **installation ID** and private key as repository secrets.

The Action can mint and refresh its own installation tokens. This avoids handing
it a one-hour static token, which can expire before a long review publishes:

```yaml
      - uses: jdziat/open-nitpick@v1
        env:
          NITPICK_APP_ID: ${{ vars.NITPICK_APP_ID }}
        with:
          app-installation-id: ${{ secrets.NITPICK_APP_INSTALLATION_ID }}
          app-private-key: ${{ secrets.NITPICK_APP_PRIVATE_KEY }}
          bot-login: my-review-app[bot]
          provider: openrouter
          model: z-ai/glm-5.3-flash
          api-key: ${{ secrets.OPENROUTER_API_KEY }}
```

Set `NITPICK_APP_ID` as a repository variable and both App inputs as secrets.
The current action reads that environment variable to enable its refreshable
App credentials. `bot-login` must be the App slug followed by `[bot]`, and lets
the reviewer recognize its own earlier work. Use the default `github-token`
instead when an App identity is not needed. The job’s `permissions` still
control the default token; the App token uses the App’s installation permissions.

### Inputs

Every input the Action takes, with its default. The examples above set the
ones most runs need; the rest are here because reading `action.yml` should not
be the way to find out that the configuration file can live somewhere other
than the repository root.

| input | default | what it does |
|---|---|---|
| `bot-login` | `github-actions[bot]` | account whose earlier reviews may be reused; use `<app-slug>[bot]` with App credentials |
| `github-token` | `${{ github.token }}` | reads the pull request and publishes the review. Needs `pull-requests: write`; fork tokens cannot publish |
| `app-installation-id` | none | GitHub App installation ID. With `app-private-key` and `NITPICK_APP_ID` in the calling step environment, mints replacement tokens before expiry during a long run |
| `app-private-key` | none | GitHub App private key. Set only with `app-installation-id`, as a secret |
| `config` | `.nitpick.yaml` | configuration path relative to the workspace |
| `provider` | config | overrides `models.default.provider`; `nitpick providers` lists providers |
| `model` | config | overrides `models.default.model` |
| `api-key` | none | provider key; pass a secret, never a literal |
| `fail-on` | `review.fail_on` | lowest severity that fails: `nit`, `info`, `warning`, `error`, `critical`, or `none` |
| `instruction` | none | one extra instruction for this run |
| `pr-number` | triggering pull request | set for `workflow_dispatch` and comment events |
| `dry-run` | `false` | writes to log and job summary without publishing |
| `skip-drafts` | `false` | skips draft pull requests |
| `version` | `latest` | release tag or `latest`; falls back to building the Action checkout when its release is unavailable |
| `analyzers` | empty | catalog names to install, comma separated; `auto` selects ones for changed languages; empty uses runner tools |
| `command` | `review` | `review`, `fast-review`, or `respond` |
| `args` | none | flags passed to `review` and `fast-review` |

### What a run does

The release binary for the runner is downloaded and its
checksum verified; when no release matches `version`, or the repository is a
private copy whose releases cannot be fetched, the binary is built from the
Action's own checkout. The pull request is reviewed and the review is posted as
one GitHub review with inline comments. The same review (walkthrough,
findings table, analyzer roster) is written to the job summary, and the
step sets outputs a later step can read:

| output | value |
|---|---|
| `result` | `clean`, `findings`, `skipped`, or `error`. A completeness failure is `error`, not a clean review. |
| `findings` | number of findings published |
| `practice_findings` | practice-report findings, including advisory findings and accepted exceptions; may overlap inline findings |
| `practice_blocking` | engineering-policy violations without an accepted exception or unresolved uncertainty |
| `critical`, `error`, `warning` | findings at each severity |
| `files` | files reviewed |
| `withheld` | findings not republished because an earlier review already posted them |
| `complete` | whether the selected policy’s required assessment completed |
| `failed_stages` | comma-separated model stages that did not complete; empty when none did |


Exit codes: `0` clean, `1` findings at or above `fail_on`, `2` the review could
not run. CI can tell "this change has problems" apart from "the reviewer
broke", and `result` says which without parsing the log.

### Analyzers on the runner

A stock runner has none of the deterministic
analyzers installed, so the roster says "not on PATH" for every one. Set
`analyzers: auto` and the Action installs and caches, at whatever version each
tool's own installer serves that day, the analyzers for the languages the
change touches: golangci-lint for Go, ruff
and pylint for Python, shellcheck, hadolint, yamllint, actionlint and zizmor,
tflint and checkov, sqlfluff, rubocop and brakeman, cppcheck, biome, buf, and
gitleaks always. A comma-separated list installs exactly those. The installs
are not pinned: they run `go install ...@latest`, package-manager installs,
"latest" release downloads, and for actionlint, tflint and dotenv-linter the
vendor's install script fetched from its default branch, inside a job that
holds the model key and a write-scoped `GITHUB_TOKEN`. That is the same
exposure as any workflow that installs tools from upstream, and it is the
reason the roster names the tool and version it ran; pin by preinstalling
the tools you trust and listing them, or leave `analyzers` unset. This is what
the hosted reviewers do implicitly; here it is a line in the workflow.

### Try it first

`dry-run: true` prints the review to the log and the job
summary and posts nothing. `nitpick explain-config` shows the resolved configuration for a path without running a model.

### Events

On `pull_request` the pull request is found from the environment.
On `workflow_dispatch` or `issue_comment`, set `pr-number`. On `push` there is
no pull request: the pushed range is reviewed and printed to the log and the
summary, nothing is posted, and `fail-on` still gates the job; `fetch-depth: 0`
is required so the range is in the checkout. Any other event fails with a
message rather than reviewing an empty tree.

### Forks

On a public repository a `pull_request` from a fork is not
reviewed at all. GitHub withholds every secret except `GITHUB_TOKEN` from such
a run, so the `api-key` input arrives empty and the run exits 2 with "API key
is required". It gets as far as parsing the diff first, which is why the log
shows the files it read and then a credential error rather than a diff error.

A related rule, not the same one, is why the workflow in this repository skips
`dependabot[bot]`: those runs read from a separate Dependabot secret store, so
they start with no model credential and fail the same way.

Where a key *is* present, on a private repository or a self-hosted setup that
supplies one, the remaining limit is the token: `GITHUB_TOKEN` is read-only on
a fork run, so the review cannot be posted. It is not lost there. The run
prints it to the log, writes it to the job summary, gates on it, and emits a
warning annotation saying why it was not posted.

Do **not** switch to `pull_request_target` to get either of these back. That
runs the workflow with your secrets against the fork's code, and this tool's
own trust model is not a substitute for that mistake. There is no arrangement
that reviews an untrusted fork with a credential the fork cannot reach, and
saying so is more use than a workaround that leaks the key.

### Incremental review

A review reuses successful model requests from its latest published review when
the prompts, code and related context, model settings, and resolved policy still
match. Failed requests retry. New commits rerun affected batches; unchanged
batches keep their results. Changing one file can invalidate its whole batch or
another batch that includes it as context.

A push is also scoped before batching: only the files that changed since the
last reviewed head are read, widened to include any file still carrying an
unresolved comment from an earlier run, so that comment can still be verified
or closed. A file that neither changed nor carries a standing comment is left
alone; its earlier findings still stand. Scoping falls back to the whole
change whenever the comparison itself is unavailable: no earlier head, no
provider that can answer it, or a force push that made the earlier head
unreachable.

Analyzers, triage, validation, and approval run again over the combined results.
Earlier findings still count, and unresolved threads still prevent approval.
The summary reports how many model requests were reused. `review.incremental:
false` disables reuse; `nitpick review -full` and `@open-nitpick restart-review`
bypass it for one run.

Progress is stored in the review on GitHub, so a new Actions runner can resume.
Legacy reviews without these records start fresh. Records are bounded to fit
GitHub's review body limit; omitted results are reviewed again. A run that never
publishes a review does not save new progress.

How a finding is recognised as already posted: every comment carries a
fingerprint of its path, class and title, and a review carries the revision it
read. A finding matches an earlier comment when the fingerprints agree
(which survives the line moving), or when the class and file agree and the line is
within two of where the forge now shows the earlier comment, which survives a
rewording. Deleting the bot's comment is how a reviewer says "do not post this
again"; it will not come back.

### Pinning

`@v1` follows the latest 1.x release of the Action; the binary it
installs follows `version`, which defaults to the latest release. Pin both to
a tag for a build that never changes under you. GitHub Enterprise Server is
supported: the API URL comes from the runner; releases are fetched from
github.com.

A pinned binary and a `.nitpick.yaml` written for a newer one is the mismatch
worth knowing about: a key the pinned build does not have fails the run before
the diff is read. Set `NITPICK_IGNORE_UNKNOWN_KEYS=1` on that workflow to
ignore those keys and continue; see
[A key this nitpick does not have](configuration.md#a-key-this-nitpick-does-not-have).

## Any other CI

```bash
nitpick review -owner acme -repo-name widgets -pr 42
```

with `GITHUB_TOKEN` in the environment. Inside GitHub Actions the repository and
pull request number are detected automatically.

### Review identity and retries

The Action's `bot-login` input identifies the account whose review history may
be reused. For an App token, set it to the app slug followed by `[bot]`, for
example `${{ format('{0}[bot]', steps.app.outputs.app-slug) }}`. For the CLI,
set `NITPICK_BOT_LOGIN`; personal tokens can leave it unset to resolve their
authenticated user. A comment's HTML markers alone establish no identity.

Only completed reviews carrying the completion marker can be reused. Legacy
reviews and failed runs trigger a full review. While earlier findings remain,
the files that carry them are rechecked alongside whatever changed; confirmed
findings still affect `fail_on` even
when their comments are not posted twice. Strict analyzer failures exit 2.

On `merge_group`, the Action reviews the event's base/head SHAs as a local
range. Both commits must be available in the checkout; use `fetch-depth: 0`.
