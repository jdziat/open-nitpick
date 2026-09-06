# Development

```bash
go test ./...        # no network or credentials required
go test -race ./...
make quick           # measure a prompt or analyzer change for a few cents (see below)
```

## Commits and releases

Commit subjects follow [Conventional Commits](https://www.conventionalcommits.org/):
`feat(scope): what changed`, `fix: …`, `docs: …`, `evals: …`, `prompt: …`.
CI checks every pull request's commits with `scripts/check-commits.sh`.
On each push to main, release-please keeps one pull request open with the
next version and its changelog; merging it tags the release. The release
workflow then builds the binaries, writes `checksums.txt`, and signs every
asset with Sigstore keyless signing, so a download is checkable against this
repository's workflow identity and nothing else:

```bash
cosign verify-blob --bundle nitpick_v1.2.0_linux_amd64.sigstore.json \
  --certificate-identity-regexp '^https://github.com/jdziat/open-nitpick/' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  nitpick_v1.2.0_linux_amd64
```

The `v1` tag follows every `v1.x.y` release, which is what the Action's
`@v1` pin relies on.

## Iterating cheaply

`make quick` runs the tuning corpus and the multi-file corpus, judge-free,
with related context off and on, against `z-ai/glm-5.3-flash`, about a
thirtieth of the default reviewer's price per review. It is the model to
iterate against, and the triage model this repository's own config uses;
[docs/findings.md](findings.md) records how it compares as a reviewer.
`QUICK=<openrouter id>` swaps it.

## Evaluating the prompts against real models

Unit tests prove the *tooling* is correct given a scripted model. They cannot
tell you whether the *prompts* work: whether a real model, handed a real diff,
finds the bug, anchors it to the right line, and stays quiet about code that is
fine. `make eval` measures that.

```bash
echo 'OPENROUTER_API_KEY=sk-or-...' > .env    # gitignored

make eval                                     # 18-model matrix; see the cost note below
make eval MODELS=openai/gpt-4o-mini           # one model
make eval RUNS=5                              # run-to-run stability
make eval FIXTURES=go-nil-deref               # one fixture
make eval CAPTURE=testdata/responses          # save raw model output
```

It is the same `OPENROUTER_API_KEY` the default config uses, but only the eval
harness reads `.env`; `nitpick review` does not, so export it (`set -a; . ./.env;
set +a`) or keep it in your shell profile.

Each fixture is a synthetic pull request with bugs planted at known lines,
built into a real git repository and reviewed through the real engine, so the
diff parsing, batching, structured output, anchoring, and rendering are all
exercised, not just the prompt.

The report separates three things that are easy to confuse:

- **Invariants**: properties open-nitpick must uphold whatever the model does:
  every severity is a real level, every comment is placeable, and no suggestion
  is published as one-click-applicable unless it is a single line of code. A
  breach is a bug in this repository and fails the run.
- **Recall**: planted defects found. Reported per fixture; a run fails only if
  a model finds *nothing* across the whole corpus, which means the prompt or
  the plumbing is broken rather than merely weak.
- **Noise**: findings explaining no planted defect. Two fixtures
  (`clean-refactor`, `style-only`) contain no bugs at all, so every finding
  there is noise by construction.

**Five corpora.** `make eval` reads the tuning corpus. The held-out corpus is
spent once at the end of a tuning round and is selected only by naming its
fixtures. The multi-file corpus, `make benchmark-multifile`, is fourteen
changes whose defect is only visible by reading a file the change does not
touch; it measures `review.related_context` with the feature off and on,
against every hosted reviewer with a cached or collectable review:
Incumbent's CLI. The
callers corpus (`FIXTURES='$(CALLERS)'`) is six changes where the file the
change breaks is an untouched caller, and the info corpus is the severity band
no reviewer had located. The last three live outside the ground-truth
registries the first two carry, and their numbers should be read with that in
mind; see `EveryFixture` in `internal/evals`.

**Cost.** The default matrix is **18 models x 16 fixtures = 288 reviews**, plus a
judge call each for `make judge-models`. That is not a cheap command. Pass
`MODELS=` to narrow it:

```bash
make eval MODELS=qwen/qwen3.7-flash            # one model, 16 reviews
```

The matrix spans providers and price tiers deliberately, including models with
and without JSON-Schema support so the structured-output fallback is exercised.

`CAPTURE` writes every raw model response to disk. Those become offline
regression fixtures, the cheapest way to keep the extractor honest without
paying for tokens on every test run.

The whole pipeline is tested against a scripted model and a stub GitHub API, so
the test suite exercises real behavior rather than mocks of its own design. Diff
position mapping is additionally cross-checked against real `git diff` output by
a second, independent implementation.
