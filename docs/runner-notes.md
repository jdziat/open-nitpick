---
# The harness and runner notes are 209,313 of the 589,395 characters of the
# published search index, 35.5%, and they are commentary on Go declarations, so
# they repeat operator vocabulary at length without answering an operator's
# question. A document boost was tried here and removed. At 0.3 it moved the
# runner notes off the first screen and left the harness notes at rank 1 for
# "severity", a 562px result block that is 29.2% of the rendered list, and 27.8%
# for "fail_on", because the word runs the length of the page. Weighting a page
# that is not an answer only changes how far down the wrong answer sits, so
# these two leave the answer set instead. Nothing is hidden: both stay in the
# nav under Internals and stay linked from every declaration that points at
# them. Measured 2026-09-07 against the built index.
search:
  exclude: true
---

# Notes from the runners

Every note below was a doc comment in `internal/linters/runners.go`. Each records
an attack, a measurement against a named analyzer version, or a reason a flag is
passed the way it is; the declaration keeps its summary and points here.

## golangciDefaults

`internal/linters/runners.go`

Embedded rather than read from disk so that it travels with the binary: an
operator who installed `nitpick` with `go install` has no checkout of this
repository to read it out of, and a config the run cannot find would fail
every Go analysis. See golangci.yml for what is in it and why.

## findings

`internal/linters/runners.go`

Split from Run for the reason semgrep's is: what this repository does with a
severity is decided from the analyzer's real output, and a test that
hand-builds the Finding has already made the decision under test.

golangci-lint's Severity is not a vocabulary. It is whatever text is written
in the config file's `severity` block, and under the default that file can
only be one the operator supplied out of tree, which is what makes calling it
the operator's text true: read from the tree it would be the contributor's
on any fork pull request. With no config at all golangci-lint publishes no
severity, so every finding here arrives as warning via mapSeverity's default.

A report that parsed must not be read as an analysis that happened, and a
one-file diff is enough to exploit the difference. Adding a go.work that does
not list the module makes golangci-lint 2.8.0 exit 7 while printing
{"Issues":[],"Report":{"Error":"typechecking error: directory pkg is contained
in a module that is not one of the workspace modules listed in go.work..."}}.
runCommand tolerates a non-zero exit whenever stdout is non-empty, since
analyzers exit non-zero on findings, decodeJSON is satisfied by the payload,
and zero Issues reads as clean code, so a pull request switches off the
entire deterministic half of its own Go review by adding one file and the run
reports success in strict mode as well as auto. `//go:build ignore` on the
changed file reproduces it. There is nothing to salvage: 2.8.0 abandons the
whole invocation, so a second, healthy package reports nothing either.

The third shape is the one a change reaches without trying, and the first two
miss it: a package that does not compile. golangci-lint 2.8.0 reports that as
an ordinary Issue with FromLinter "typecheck", exits zero, and leaves
Report.Error empty, so neither check above fires. Measured, on a change that
adds one file:

    Issues: [{FromLinter: "typecheck",
              Text: ": # probe/pkg\npkg/b.go:4:9: undefined: undefinedSymbol",
              Pos: {Filename: "pkg/a.go", Line: 1}}]

Three things make that a silencing rather than a finding. The analysis is
abandoned rather than degraded, so one broken package deletes every other
package's results in the same invocation. The issue anchors to line 1 of the
alphabetically first file in the package rather than the file that failed, so
the default only_changed_lines has normalize drop it and nothing is published
at all. And `go build ./...` stays green when the offending file is a
_test.go, so the change looks healthy to everything except the review it
silenced. End to end that is zero findings, a nil error and status "ran:
isolated", byte-identical to a clean review, in strict mode as well as auto.

So a typecheck issue is read as golangci-lint reporting through the issue
channel that it could not analyze the code. Its Text carries the real file
and line, which Report.Error would not give, so the status line quotes that.

## golangciReportArgs

`internal/linters/runners.go`

They are flags rather than lines in golangci.yml so that they bind an
operator's linters.golangci_config as well as ours. Overriding the operator
is deliberate: none of the four chooses which rules run, they decide how much
of the analyzer's own output survives to be gated, and a finding produced and
then discarded may not vanish silently. Each was measured against
golangci-lint 2.8.0.

- --path-mode abs. run.relative-path-mode defaults to `cfg`, paths relative
to the config file's directory, so an operator config outside the
repository makes every issue arrive as "../repo/app.go". files.Find
misses all of them and Set.normalize drops the lot without a word, so the
operator who configured their own rules to get their review back gets an
analyzer that ran, reported and published nothing. Absolute paths cannot
be re-based, and repoPath already relativizes them.
- --max-same-issues 0 and --max-issues-per-linter 0. The defaults are 3 and
50, and the truncation is invisible: eight identical errcheck violations
arrive as three, with nothing in the JSON saying five were cut, so a
change can push a real finding out of the report with decoys.
- --uniq-by-line=false. The default keeps one issue per line. A
two-statement function reported four issues with it off and two with it
on; the pair that disappeared were staticcheck's, hidden behind an
ineffassign on the same line.

--issues-exit-code 0 is separate in kind: it makes a non-zero exit mean "the
analysis failed" rather than "there were findings", which is what lets
golangciLint.findings treat any non-zero exit as a failure.

## positionsRewritten

`internal/linters/runners.go`

The attack, reproduced against golangci-lint 2.8.0 and go1.25.5, is one line
above the offending function:

    //line zz_generated.go:1

golangci-lint reports both findings, correctly, at pkg/zz_generated.go, a
file that does not exist. Every "did the analyzer produce issues" check
passes, and they then die in Set.normalize because the path is not in the
diff. Point the directive at a real file the change also touched and it is
worse than silencing: the findings land on that file, at lines the attacker
chose, and this bot posts them under its own name against code the change
did not write.

There is nothing to salvage. A line directive rewrites the file and the line
of every position after it, golangci-lint's JSON carries only the rewritten
position, and the original is not recoverable from the report. So the report
is refused whole, which is this package's existing rule for analyzer output
that describes a run we cannot read, Report.Error and typecheck take the same
exit, and the run says "did not run" with a reason instead of publishing a
quieter or a relocated review.

A refusal is worth less than it looks. It makes the silencing visible
without preventing it: a change that wants no Go analysis can still have
none, and mode: strict does not close that either, since strict makes this an
error from Set.Run that review.Engine logs and publishes as "did not run",
and review.fail_on decides the exit status against what was published.
Visible is the property on offer.

It scans every .go file in the analyzed directories rather than only the
changed ones, because a directive anywhere in the package can move a finding
onto the changed file, and because that is the set golangci-lint parsed.
Legitimately generated code carrying directives, goyacc and cgo output
committed to the tree, trips it, at the cost of a named refusal rather than a
silent one.

It quotes where the directive is rather than what it says. The filename
inside a directive is written by the change, this reason is published on the
pull request, and published reasons escape HTML but not markdown; a file and
line locates it without giving the change a second place to render a link.

## covering

`internal/linters/runners.go`

It is separate from Runner for the reason stateful is: a test double must not
be forced to have an opinion about build constraints, and only the Go runner
has anything to say here today. It is asked only of a runner that ran, since
a skipped or failed analyzer says so in the roster and a coverage note
underneath repeats what the reader was told.

## cgoExcluded

`internal/linters/runners.go`

THE RESIDUAL IT CLOSES, reproduced against golangci-lint 2.8.0 and go1.25.5.
With CGO_ENABLED=0, a file importing "C" and one ordinary sibling beside it:
zero findings, exit 0, a nil error, the roster line "ran", and, before this,
an empty coverage list. It is the same shape as the build-constraint gap and
it was reachable without writing anything unusual, because CGO_ENABLED=0 is
the default in most Go CI images: the errcheck violation in the cgo file is
reported with cgo on and silent with it off, measured both ways.

It parses rather than asking build.Default.MatchFile, because MatchFile CANNOT
answer this: it reads build constraints and the filename, and the fact that
decides a cgo file's fate is in the import list. Measured, MatchFile returns
true for this file with CGO_ENABLED=0 and with it set to 1.

ImportsOnly stops at the import block, so the cost is bounded by the top of
the file rather than by its size, and a body that does not parse, cgo files
carry C in a comment, not in Go, cannot make this lie. A file that does not
parse AT all returns false: golangci-lint reports its own parse failures
through typecheck, findings() turns that into a refusal, and a second opinion
invented here would be a guess about a run that already failed loudly.
cgoEnabled is the CHILD'S, read from the go tool by goEnvironment, and the
argument exists because reading it from this process was a second silencing of
exactly the file this function was written for. build.Default.CgoEnabled is
filled in at go/build's package init from os.Getenv alone; cmd/go resolves
CGO_ENABLED from the environment and from the go env config file at
$(go env GOENV), which is what `go env -w CGO_ENABLED=0` writes and the
ordinary way to configure a builder image without exporting variables.
Measured against golangci-lint 2.8.0, CGO_ENABLED absent from the process
environment: with the key in that file the child reported CGO_ENABLED=0, the
cgo file's errcheck violation vanished, build.Default.CgoEnabled was still
true so this returned false, and the coverage list was empty, the state the
function exists to prevent, reached through the config file instead of the
variable. It was the same mistake as the MatchFile one diagnosed above: the
detector keyed to a proxy for the child's build context rather than to the
child's build context.

## goAssumedLanguageVersion

`internal/linters/runners.go`

Measured rather than taken from the documentation: a go.mod reading only
`module probe` reports the same nothing as `go 1.16` on a file using
strings.Title, io/ioutil and rand.Seed, where `go 1.20` reports all three. A
missing directive is therefore a declared version like any other and is
reported like one, with line 0 because there is no line to send a reader to.

## belowAnalyzedLanguage

`internal/linters/runners.go`

WHAT THE `go` DIRECTIVE does, measured against golangci-lint 2.8.0 and go1.25.5 on
a file importing io/ioutil. With `go 1.24` in go.mod the review publishes
SA1019, `"io/ioutil" has been deprecated`. Change that one line to `go 1.15`
and the finding is gone: zero findings, the roster saying golangci-lint ran,
an empty discard list and an empty coverage list, byte-identical to a clean
review, from one line in a file the change can edit.

IT IS not CLOSABLE FROM THE CONFIG WE OWN, which is why it is named rather
than fixed. Both routes were tried against 2.8.0: `run.go: "1.25"` does not
restore the finding, and neither does `linters.settings.staticcheck.checks:
["all"]`, with `checks: all` staticcheck demonstrably runs, because ST1000
appears, and SA1019 still does not. The module's declared language version
wins over anything the analyzer is configured with, and we may not edit go.mod
to raise it: it is the tree under review.

A constant floor of 1.21 is the wrong shape here. The argument for one is
volume: every Go release deprecates something, so "below the newest" is true
of nearly every module, and a notice firing on ordinary code is one reviewers
learn to collapse. What it buys is silence at the modal directive. Measured
on one file using reflect.PtrTo (deprecated in 1.22) and
cipher.NewCFBEncrypter (deprecated in 1.24), sweeping the directive: `go
1.22` publishes the first, `go 1.24` publishes both, and `go 1.21` and `go
1.21.4` publish neither while the coverage list stays empty, the review
byte-identical to a clean one. As an attack it is a diff editing go.mod from
`go 1.25` to `go 1.21` and adding the file, and go 1.21 to 1.23 are the
ordinary directives in live repositories, so a floor is silent in the common
case at a cost disclosed only in a paragraph like this one.

The sentence that makes a floor look safe is false: "the compiler gates
language features on this same directive, so lowering it below what the code
uses fails rather than going quiet". True at `go 1.15`, where generics
produce `type parameter requires go1.18 or later` and findings() turns that
into "the code did not compile". Nothing at the floor: at `go 1.21` every
language feature through 1.21 compiles, generics included, and every
deprecation issued since is off. A bound that holds only far below the
threshold does not bound the threshold.

The ceiling gives something up in the other direction, and a false coverage
gap is as much a defect as a missed one. The gate staticcheck applies is its
table of deprecations, not the toolchain's standard library, so a module
declaring one release behind a toolchain whose deprecations the analyzer does
not yet know about is named for a reduction that is currently empty. That is
the same over-report goNolintLines accepts for `//nolint:nosuchlinter`, in the
same direction: the entry claims the ruleset was narrower than this run could
apply, which is exactly the state of the run, and the alternative was measured
to hide real findings. A module that keeps its directive current, this
repository's own does, is named on no review at all.

The comparison is go/version's, which is the toolchain's own, for the reason
goDirectiveLine is go/scanner's: an ordering hand-written over "1.9" and
"1.10" is a list of the cases somebody thought of. Lang() first because a
go.mod may carry a full release (`go 1.21.4`) and it is the language version
that gates the checks.

EITHER SIDE UNREADABLE REPORTS FALSE, say nothing rather than guess, in both
directions. A go.mod version go/version cannot read does not load either, and
golangci-lint reports that failure itself through the channel findings()
already refuses on. An unreadable ceiling is a development toolchain, whose
GOVERSION carries no language version at all; naming every module in the
checkout because we could not read our own toolchain would be a wall of
coverage gaps invented out of an unanswered question.

## moduleLanguageVersion

`internal/linters/runners.go`

It reads the file rather than running `go list -m`, because go.mod is part of
the tree under review and asking the go tool about it is another way to
execute what the change wrote, the same reasoning that put GOTOOLCHAIN=local
on every golangci-lint invocation.

The accepted grammar is go.mod's, which is small and is why this is hand-read
rather than given to a parser: the file is line-oriented, `//` is its only
comment form, and the `go` directive is a top-level line `go <version>`. The
first one wins, matching the go tool, which rejects a second with "repeated go
statement".

TOP-LEVEL IS ENFORCED HERE and USED not TO BE. The comment that stood here
said the directive "cannot appear inside a parenthesized block. Nothing else
can be mistaken for it", and offered as the reason that `go` is a reserved
module path, so no require line begins with that word. The premise is about
what the go tool ACCEPTS; this function runs before anything has accepted
anything, over a file the change is free to write. Measured, the go.mod

    module probe

    require (
    go 1.99
    )

    go 1.25

returned ("1.99", 4, true). The block entry read as the directive, and the
real one on line 7 never reached. Reserved-ness is checked by the loader, and
the loader's answer arrives after this.

It was not exploitable at the time it was found, because that go.mod does not
load and the review says so in the loader's own words. That is a property of
the surrounding run rather than of this function, and it is not one to leave
load-bearing: the same misread with a version the loader tolerates would put
an attacker-chosen number where the ceiling comparison reads it. Depth is
counted instead, which costs two lines and removes the class rather than the
instance.

A go.mod that cannot be read reports false: goModuleFor found the file, so a
read failure here means something is wrong with the checkout, and every
analyzer in the run is about to say so in its own words.

## repoPath

`internal/linters/runners.go`

Without it every finding from a nested module anchors to the wrong file, or
to no file, which is a discard.

golangci-lint is asked for absolute paths (--path-mode abs), so its findings
take the first branch and never the module join. The relative branch is kept
because it is the correct handling if a future version ignores that flag, and
because the alternative, assuming absolute, would turn a regression there
into every Go finding being discarded, which is the shape of failure this
package keeps having to fix.

## Detect

`internal/linters/runners.go`

It is off rather than isolated because eslint has no usable isolated mode:
--no-config-lookup leaves zero rules configured and therefore zero findings,
for every repository, forever, which is the silencing this change exists to
prevent, applied globally by our own hand. Containment that costs the whole
analyzer is not containment, so the analyzer is switched off and SAID to be
off, and LinterStrict still catches an operator who enabled it without
configuring it.

Base-revision resolution was rejected as the alternative: an eslint config is
a LOADER, so an unmodified base config doing `import "./eslint-rules/index.js"`
still executes a file the pull request wrote, and relocating that config out
of the tree breaks its imports outright, because Node resolves them from the
config file's own directory.

Deliberately does not consider node_modules/.bin/eslint. That binary comes
from the tree under review, and running it would execute attacker-supplied
code with the review's credentials in the environment. See resolveBinary.

It no longer requires package.json at the checkout root. That check had
golangci-lint's monorepo defect, web/package.json meant eslint never ran, for
any change, with the status line blaming a missing binary, and it decided
nothing: the operator's config is what switches this analyzer on, and a change
with no JavaScript in it has no targets to pass either way.

## mapSeverity

`internal/linters/runners.go`

Every word here is foreign, including the ones spelled like our levels. There
is no branch for "the analyzer used our own word, so nothing needs
translating": semgrep's documented vocabulary is LOW/MEDIUM/HIGH/CRITICAL with
"the older levels ERROR, WARNING and INFO match HIGH, MEDIUM and LOW", so
semgrep's ERROR *is* semgrep's HIGH, one level, two spellings, both inside
semgrep's scale and neither one a statement about ours. Sorting the two into
"translated" and "untranslated" branches described a distinction that does not
exist. It is a translation table, so it DESTROYS the analyzer's word: ERROR
and HIGH both land on error and the result cannot be un-mapped, which is why
every caller records the original in Finding.RawSeverity and why
review.Finding.SeverityTranslated is set for all of them.

THE BUG: "CRITICAL" folded onto error, so the codomain excluded critical and
no analyzer finding could ever BE critical. review.fail_on accepts "critical",
so a repository configured that way got zero gating from semgrep, the only
one of the four analyzers whose scale HAS a critical, and from any
golangci-lint whose severity settings name one. The build went green on a
finding the analyzer itself called critical, and nothing said so. The
justification given was that "an unknown value becoming critical would poison
the gate", which is an argument about UNRECOGNIZED input. An analyzer that
printed the word critical is not an analyzer that printed something we could
not parse, and treating the two as one case is what opened the hole.

THE RULE NOW: translate for fidelity, reduce at policy time. The codomain
covers all five levels review.fail_on accepts, so no gate threshold is
unreachable by construction. Whether a deterministic tool may fail a build is
the operator's question and config.Linters.MaxSeverity is where they answer
it; answering it here answered it for every repository at once and
permanently. That cuts both ways and the wide side is golangci-lint, whose
Severity field is whatever text a config file's `severity` block carries:
with `severity.default: CRITICAL` a misspell or a typecheck compile error
arrives as a critical, and only max_severity stops it reaching a critical
gate. That text can now only come from linters.golangci_config, a file
outside the repository, under the default there is no config, golangci-lint
publishes no severity at all, and every Go analyzer finding lands on the
warning below.

NIT is reachable from the literal word and nothing else. golangci-lint emits
whatever its config says, so "nit" arrives, and folding it up to
info would be the same unjustified reduction pointed the other way. It carries
a cost the operator should know about, because nit is the one level BELOW the
default review.min_severity of info: a repository whose golangci_config says
`severity.default: nit` publishes no Go analyzer findings at all under the
default gate. That is both configurations doing what they say, and it is why
nothing FOREIGN is folded onto nit, LOW, INFO and NOTE are the weakest thing
these analyzers can say and still mean "a rule fired", where nit means "take
it or leave it", so inventing nits from an analyzer's floor would silence
findings nobody asked to silence.

AN UNREADABLE WORD is warning, the same as no word at all, because they are
the same state: this project has no usable severity from the analyzer and
picks one. Semgrep prints values outside its own documented four (EXPERIMENT),
and golangci-lint prints anything. Flooring those at info instead, to match
config.Severity.Normalize, which is where a MODEL's unrecognized word lands,
looks symmetric and is not: a model was handed our enum and writing outside it
is that reporter misbehaving, whereas an analyzer was never given our
vocabulary and an unreadable word is our translation failing. Making our
failure quieter deletes real findings under any min_severity above info, and
it would rank "the tool said something we could not read" BELOW "the tool said
nothing", which is incoherent.

## decodeJSON

`internal/linters/runners.go`

Analyzers surround their JSON with human-readable noise on both sides:
deprecation notices before it, and, golangci-lint does this, a "1 issues:"
summary after it. A streaming decoder reads exactly the first JSON value and
ignores whatever trails it, which a substring trim cannot do.

THE BUG: a missing payload used to return nil, on the theory that an analyzer
which found nothing says so in prose. None of these four does, golangci-lint
prints {"Issues":[]}, ruff and eslint print [], semgrep prints its envelope,
so the only things that reach that branch are failures. Combined with
runCommand, which errors only when stdout is empty and the exit was non-zero,
an analyzer that exited 0 printing nothing became "zero findings, no error",
indistinguishable from clean code. That is exactly the failure this whole file
is defending against, and it was already shipped: `semgrep --config auto
--metrics off` exits 2 with empty stdout, so the semgrep runner had never
produced a finding. A missing payload is now an error, which Set.Run logs in
auto mode and returns in strict.

A PRESENT PAYLOAD IS not A SUCCESSFUL ANALYSIS, and this function has no
opinion about that. It is a decoder: it can tell that nothing was reported,
never that what was reported describes a run that finished. golangci-lint and
semgrep both report their own failures INSIDE a well-formed payload, and their
runners check for it after decoding, see golangciLint.findings and
semgrep.findings, which is where "a report arrived" is turned into "the
analysis happened".
