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
