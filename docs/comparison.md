# open-nitpick against Incumbent

A capability comparison and a measured one, kept apart because they are
different kinds of claim. The capability table is what each product does; the
measured tables are what each did on this project's corpora, with every rule
in [measurement.md](measurement.md) applying — above all that the corpora are
this project's own, that the incumbent's side is its CLI in plain-text mode
on a free allowance rather than the hosted product with a learned codebase,
and that Contender and Bugbot are not in the tables because neither could be
run here.

## Capabilities

| capability | Incumbent | open-nitpick |
|---|---|---|
| Runs where | hosted; self-hosted on an enterprise plan | anywhere: GitHub Action, any CI, a laptop, offline against a checkout |
| Model | theirs, undisclosed | any of 17 providers, any OpenRouter model, any OpenAI-compatible endpoint, local models; different models per role |
| Prompt | not visible | prompts are files in the repository; `nitpick explain-config` prints the exact prompt |
| Pricing | per seat | none; you pay the model provider |
| Trust model | reads `.incumbent.yaml` and analyzer configs from the branch under review | a change may not supply the policy it is reviewed under: policy from the base revision, analyzer configs never from the tree, endpoint keys stripped from untrusted config |
| Forges | GitHub, GitLab, Bitbucket, Azure DevOps | GitHub, local |
| Incremental review on push | yes | yes; fingerprints withhold findings already posted, files unchanged since the last review are not re-read, force push falls back to full |
| Static analyzers | ~50 tools, auto-selected | 33 tools, auto-detected when installed, every one isolated from the tree; see the README table |
| Repository context | indexes the repository | attaches the definitions a changed line uses: Go (types and methods), TypeScript (aliases, barrels), Python (package re-exports), Ruby (Rails autoload), Rust, Java, Kotlin, C/C++ |
| Disclosure | summary and walkthrough | every file not reviewed, every analyzer that did not run, every finding an analyzer produced and the review discarded, every part of the change an analyzer did not cover, every finding a domain expert overruled |
| Chat, `@mention` commands | yes | no |
| Learnings from human feedback | yes | no |
| Multi-line committable suggestions | yes | single-line suggestions only; multi-line fixes are described |
| Sequence diagrams, docstring generation, issue creation | yes | no |
| Measurement | vendor benchmark | a corpus, a judge-free harness, and a findings document that records its own mistakes |

## Measured

Judge-free columns (Rule 1): located plants over plants, noise findings per
review, widest anchor in lines. Incumbent's side is its cached CLI review,
one per fixture. Our side is `anthropic/claude-sonnet-4.6` with related
context on, the shipped default reviewer under this repository's own
configuration.

### By language, iteration 1 (2026-09-03)

Tuning corpus (16 fixtures, one run) and multi-file corpus (14 fixtures, two
runs), one process each, re-derived per language from the retained dumps by
`TestReportFromDump`. R is located/plants, N is noise findings over reviews,
A is the widest anchor in lines. The incumbent has one review per fixture.

| language | sonnet-4.6 + related context | incumbent/cli | lead |
|---|---|---|---|
| Go | R 18/18, N 3/18, A 2 | R 8/14, N 3/14, A 13 | recall, noise, anchor |
| Python | R 11/11, N 2/13, A 1 | R 3/7, N 2/8, A 3 | recall, noise, anchor |
| TypeScript | R 6/7, N 2/9, A 1 | R 1/4, N 1/5, A 2 | recall, anchor; noise one finding apart |
| Ruby | R 2/2, N 0/2, A 1 | R 0/1, N 0/1 | recall |
| PHP | R 1/1, N 0/1, A 1 | R 0/1, N 0/1 | recall |
| Kotlin | R 0/1, N 0/1 | R 0/1, N 0/1 | nobody locates the one `info` plant |
| **all** | **R 38/40, N 7/44, A 2** | **R 12/28, N 6/30, A 13** | recall 0.95 vs 0.43; noise 0.16 vs 0.20 per review |

The held-out corpus is not re-spent for this table. Its one retained run
(kimi-k3, 2026-08-07) had C# 3/3 vs 1/1, shell 3/3 vs 0/1, SQL 3/3 vs 1/1,
Java 0/3 vs 0/1, Rust 0/3 vs 0/1 and Ruby 0/3 vs 0/1 — the Java, Rust and
Ruby plants there are `nit` and `info` plants that no contender has located.

**What iteration 1 changed.** Reading the noise findings behind the baseline
showed three corpus artifacts hitting both reviewers (a validation endpoint
answering 201, a handler echoing database errors, an unvalidated path segment
the fixture had not said was validated) and one real reviewer fault: the same
defect reported twice a few lines apart, once as the cause and once as the
symptom. The artifacts were repaired and the triage prompt now names the
cause-and-symptom pair as one finding. Multi-file noise went from 0.36 to
0.14 per review at unchanged recall of 1.00; the incumbent's, re-collected on
the repaired fixtures, from 0.29 to 0.21.

**What the remaining noise is.** Seven findings over 44 reviews: two on the
Python clean control about the helper rather than the change, two on the Go
query handler about logging a request parameter (a line this project's own
repair added), one observing that a public function's signature changed, and
the two objections `ts-unbounded-memo-key` documents as reasonable for a
reviewer to raise. None is a hallucination. The TypeScript column is where
the incumbent's noise is lower by one finding, and that finding is one the
fixture's author calls defensible.

**Cost.** $0.019 per review for the default reviewer, $0.0018 for
glm-5.3-flash with related context (R 34/40, N 11/44). The incumbent's CLI
review is not priced.

### Iteration 2: the expert pass, measured and left off

`validation.enabled` — a second model pass in which a domain expert can
refute or re-rate each finding — has shipped disabled and unmeasured since it
was written. Measured here for the first time, sonnet-4.6 with related
context, same corpora, same day:

| corpus | without | with the expert pass | cost per review |
|---|---|---|---|
| tuning | R 0.88, N 0.19 | R 0.81, N 0.12 | $0.019 to $0.029 |
| multi-file | R 1.00, N 0.14 | R 1.00, N 0.18 | $0.019 to $0.031 |

It buys one fewer noise finding on the tuning corpus at the price of one
plant, adds noise on the multi-file corpus, and costs half again per review.
It stays off. The noise it removed was a defensible objection, and the plant
it lost was a real one.

### Where this stops

On this project's corpora the default reviewer leads the incumbent's CLI on
recall in every language that plants a defect, by two to one overall; on
anchor width everywhere; on noise per review overall and in Go and Python;
and on cost, which the incumbent does not publish per review. It does not
lead on TypeScript noise, by one finding in nine reviews, and that finding
is one the fixture's own author lists as a reasonable objection. Measurement
.md's rules say a gap inside the corpus's resolution is not a result in
either direction, so the iteration stops here rather than tuning a prompt to
suppress an objection the corpus itself calls defensible. What would move it
honestly is a larger TypeScript corpus, not a narrower reviewer.

Nothing above is a claim about the hosted product with a learned codebase,
about Contender or Bugbot, or about a corpus anyone else wrote.

### End to end, on GitHub (2026-09-03)

The corpora were materialised into a private repository
(`jdziat/nitpick-bench`, one branch and one pull request per fixture, 44 in
all) and reviewed by the shipped GitHub Action — release install, incremental
review, the job summary, the lot — with `anthropic/claude-sonnet-4.6`, related
context on, and `min_severity: nit`. `cmd/benchrepo score` reads the posted
review comments back and scores them with the harness's own scorer.

| | |
|---|---|
| pull requests reviewed | 44 of 44, every workflow run green |
| plants located | 33 of 41 |
| noise findings | 1 over 44 pull requests |
| clean fixtures with a comment | 0 of 5 |

The eight misses are the corpus's known floor: the four `info` plants nobody
locates (go-package-singleton, kotlin-widened-input, ruby-default-page-size,
rust-crate-for-one-call), two `nit` plants (cross-file-copy-nit,
sorted-for-min-nit), cross-file-sort-nit, and retry-no-backoff, where the one
noise finding landed. Every `critical`, `error` and `warning` plant in the
three corpora was located on the real pull request.

This is the number a user of the Action gets, not a harness number: it went
through GitHub's diff, the review API, the comment cap, and the fingerprint
markers. It is also a single run, on fixtures this project wrote, against no
incumbent — the private repository is where Incumbent's hosted app can be
installed for the comparison the CLI cannot give.

### Head to head with Incumbent's hosted app, on GitHub (2026-09-03)

Incumbent's GitHub App was already installed on the account, so it reviewed
the same 44 pull requests as they opened. This is the comparison the CLI
cache could not give: the hosted product, with the repository to index, its
own analyzers, and no free-allowance caveat. Both reviewers saw identical
diffs at identical times. `cmd/benchrepo score` scores each reviewer's
inline comments with the harness's scorer; a review with no inline comment
counts as located nothing, which is what it is.

| language | open-nitpick | Incumbent (hosted) |
|---|---|---|
| Go | R 16/18, N 0/19 | R 14/18, N 4/19 |
| Python | R 6/8, N 1/9 | R 7/8, N 0/9 |
| TypeScript | R 5/6, N 0/7 | R 5/6, N 1/7 |
| Ruby | R 1/2, N 0/2 | R 1/2, N 0/2 |
| Java | R 1/1, N 0/1 | R 0/1, N 0/1 |
| PHP | R 1/1, N 0/1 | R 0/1, N 0/1 |
| C#, shell, SQL | R 3/3, N 0/3 | R 3/3, N 0/3 |
| Kotlin, Rust | R 0/2 | R 0/2 |
| **all** | **R 33/41, N 1/44, 34 comments** | **R 30/41, N 5/44, 37 comments** |
| clean fixtures commented on | 0 of 5 | 0 of 5 |
| median time to first review | 96 s | 101 s |

R is located/plants, N is noise findings over pull requests.

**Read by hand, because the scorer is a keyword list.** Every difference
between the two columns was checked against the actual comments:

- `ts-unawaited-async`: Incumbent found the plant and worded it "does not
  await … can resolve before", which the keyword list scored as a miss. The
  list now admits that phrasing; the table above is after the fix. A gap
  that only costs the other side is a thumb on the scale.
- `multi-defect` (three plants): Incumbent reported all three and is
  scored 2 of 3, because it anchored the descriptor leak at the `return`
  where the close belongs (line 34) rather than at the `os.Create` (line 19),
  fifteen lines from the plant. Its three "noise" findings there are a
  partial-file-on-error remark, an unbounded-request-body remark, and that
  same leak at the other anchor — two of the three are defensible.
- `go-empty-filter-deletes-all`: its extra finding is golangci-lint's
  `errcheck` on an ignored `fmt.Fprintf`, which it ran and this run did
  not: the benchmark runner has no golangci-lint installed, so
  open-nitpick's analyzer auto-detection had nothing to run. That is a real
  difference in the hosted product's favour — it brings its analyzers.
- `go-hardcoded-secret`, `php-forbidden-vs-404`, `defensive-copy-nit`:
  Incumbent posted a walkthrough and no inline comment on all three. The
  committed `sk-live-` key is the one miss that matters.
- `python-secret-to-audit-log`, `retry-no-backoff`: Incumbent found what
  open-nitpick's single noise finding sat next to; on Python it is one
  plant ahead.

**What the hosted product does that this does not.** Every Incumbent
comment carries a category, an effort label, a proposed fix as a diff, a
committable suggestion, and a prompt for an agent; several carry the shell
script it ran to verify the finding against the repository. open-nitpick's
comment carries the severity, the class, the title, the consequence, and
which model found and triaged it. On a defect both found, both comments were
right; theirs is longer and more actionable, ours is shorter and says who
said it.

**What this establishes.** On these 44 pull requests, at the same moment, the
shipped Action located three more plants than the hosted incumbent, posted a
fifth of its noise, and answered five seconds faster at the median. It missed
the same `info` and `nit` plants everyone misses, plus one `nit`. Rule 15
applies: this corpus is this project's, and 41 plants resolve nothing finer
than one plant. What it retires is the sentence that the CLI cache was the
wrong instrument — the hosted product on the same pull requests locates 30
where the cache located 14 of 29 on an earlier corpus, and still fewer than
this reviewer.

### After the remediation plan (2026-09-04)

See [remediation.md](remediation.md) for the plan and what each step
measured. On the benchmark repository, re-laid-out so nothing tells either
reviewer it is reading a fixture, the shipped Action moved from 33 to 36 of
41 plants, recovering four of the eight misses; the four still missed are
the `info` plants, on which the new info corpus puts the default reviewer at
11–13 of 20 against 4 of 20 before the change. Noise rose from 5 to 17 over
44 pull requests, nine of them analyzer nits that `min_severity: nit`
publishes and the default `info` would not. Incumbent's app had reviewed
half of the re-opened pull requests when scored, so its column waits.
