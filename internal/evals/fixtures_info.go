package evals

import "github.com/jdziat/open-nitpick/internal/config"

// infoFixtures are the corpus's info-level plants.
//
// Measured over AllFixtures() before these were written, the corpus planted 4
// critical, 8 error, 6 warning, 0 info and 6 nit, 24 plants across 25
// fixtures, which is one more fixture than this sentence used to claim. Every
// other level had been given at least two plants and an argument; info had
// none, so no claim about it was falsifiable. A reviewer that never emits the
// word `info` and a reviewer that emits it perfectly scored identically, and
// the O-INFL and O-UNDR columns could not see the level at all: with nothing
// planted at info, a reviewer rating a nit as info was charged inflation and a
// reviewer rating a warning as info was charged understatement, but no reviewer
// was ever charged for MISSING info, because there was nothing there to miss.
//
// WHY THIS LEVEL RESISTED TWO PREVIOUS ATTEMPTS, and it is worth writing down
// because it is a property of the level rather than of the authors. Every other
// severity is defined by a failure: something returns the wrong answer, leaks,
// deadlocks or breaches. Authoring one is a matter of choosing the failure and
// then choosing how much has to go right for it not to happen. Info has no
// failure. The shipped anchor is "`info`, a defensible concern the author
// should consciously accept or reject", and what a published example of it has
// to do is name a cost WITHOUT naming an input that breaks.
//
// This paragraph deliberately does not quote the ladder's current examples, for
// the reason fixtures_warning.go stopped quoting them: a comment keyed to prompt
// prose goes stale every time the prompt is edited, and the property argued here
// is a property of the LEVEL. It went stale twice already. The version before
// this one quoted an example reading "so a rise in failures reads as a fall in
// traffic" and asserted in the same sentence that the ladder's examples "name no
// input that breaks", the quotation names the input, a failure, and the wrong
// output, traffic reading as falling. Both the example and the claim about it
// were wrong, and the claim was refuted by the text it quoted.
//
// THE AUTHORING TEST, which is what survives when the quotations are removed and
// is the reason both of those examples were replaced: ask whether the AUTHOR
// COULD BE WRONG. If the author could be wrong, the finding is at least a
// warning. The usual move, take a defect and turn the dial down, therefore
// does not produce an info finding at all; it produces a warning whose
// consequence has been made small. Info is the level where reasonable engineers
// split, and where the reviewable fact is that the author should have DECIDED
// rather than drifted.
//
// The test cuts both ways, and the ladder has now failed it in each direction.
// An example may not state a wrong answer as a fact, because then the author IS
// wrong and the rung is a warning. And an example may not name a mere DIFFERENCE
// either: one earlier version was "a subcommand configured on the command line
// where the tool's other subcommands read a config file", which named no cost at
// all, failed review.md's own bar ("report a finding only when you can name a
// concrete consequence"), and was besides a consistency observation,
// config.ClassStyle, which allowedClasses drops below pedantic, so a reader of
// the default configuration could never have seen the finding it illustrated.
//
// THE LADDER'S TWO INFO EXAMPLES USED TO BE TWO OF THESE PLANTS AND WERE
// REPLACED, which is why two SeverityNotes below argue from a clause rather
// than from an example. review.md illustrated `info` with "Widening an exported
// type's accepted input is info. Adding a dependency for one helper function is
// info.", kotlin-widened-input and rust-crate-for-one-call stated almost
// verbatim, three lines above "These examples ... are deliberately drawn from
// defect classes you are unlikely to meet in this change; do not go looking for
// them." So the prompt named two planted defects and then told the reviewer to
// ignore them. Measured, kimi-k3, two independent three-run batteries: both
// fixtures 0 of 3 every time, usually with an empty findings list.
//
// THE FIX WAS ON THE PROMPT AND NOT ON THESE PLANTS, and the reason is not the
// corpus. Every other rung illustrates with a SCENARIO, "Writing a decrypted
// secret to a log that ships off-host", "A check-then-act on a file that
// another process can replace between the two steps", while those two were
// CATEGORIES, and the anti-anchoring sentence's claim of rarity was therefore
// false of them for any reader: widening an exported signature and adding a
// dependency for one helper are among the most ordinary things a reviewer
// meets, so in a real repository the prompt was suppressing two legitimate
// findings. Nothing here moved: the diffs, anchors, keywords, classes and
// levels are untouched, and testdata/incumbent stays keyed to them. What could
// not stay is a note deriving its level from a sentence that no longer exists.
//
// Every plant below was tested against that question before it was written:
// name the case FOR the change, in one sentence, and refuse to plant it unless
// that sentence is one a senior engineer would say. Those sentences
// are in each fixture's doc comment, alongside the case against. A plant whose
// "for" side is a straw man is a warning with the dial turned down, and the
// previous round's gate audited for exactly that.
//
// THE PROMPT MAY NOT BE ABLE TO REACH THIS LEVEL, and that is a measurement
// these plants make rather than a reason not to author them. review.md's bar
// section says "Report a finding only when you can name a concrete consequence:
// an input that produces a wrong result, a state that deadlocks or panics, a
// request that leaks data, a path that loses an error. If you cannot describe
// how it fails, it is not a finding." No info finding can clear that bar as
// written, because no info finding fails. The severity anchors define the level
// anyway, and the normal-level scope adds the one clause that lets a reviewer
// reach it: "You may report a maintainability problem only when you can name
// what it will cost concretely". So the shipped prompt contains a genuine
// tension, and until now nothing in the corpus could see it. If every model
// misses all five of these while scoring well elsewhere, that is evidence about
// review.md's bar and not about the models, and it is the first evidence this
// tree has ever had either way. Every plant below therefore states a CONCRETE
// COST, because that clause is the only door into the level and a plant that
// does not fit through it measures nothing.
//
// FOUR CLASSES WERE UNAVAILABLE, and the reason is mechanical rather than
// editorial. TestSeverityIsConsistentWithinADefectClass requires a SeverityNote
// from EVERY member of a class that carries more than one severity.
// correctness, concurrency, contract and data-loss are each planted in
// fixtures.go at a single level with no notes at all, so an info plant in any
// of them turns green plants red in a file this change does not own,
// correctness alone would need notes on three. That is a real cost and it lands
// on the most natural home for two of these: widening an exported type's
// accepted input is contract-flavoured, and it is planted here as
// `maintainability` instead. The declared class is honest on its own terms,
// ClassContract is "a change that breaks existing callers", and nothing below
// breaks a caller, which is precisely why these are info and not error, but a
// reader should know the taxonomy was not the only pressure. Closing that needs
// notes on contract-break, in a change that owns fixtures.go.
//
// LANGUAGES. Rust, Kotlin, Ruby and PHP are all new to the corpus; one plant is
// Go. Before these, fourteen of twenty-five fixtures were Go, counted rather
// than remembered, and the previous count in this sentence, sixteen of
// twenty-four, was wrong in both figures, so a prompt tuned on this corpus
// could be Go-shaped without anyone noticing.
//
// THIS FUNCTION IS NOT A CORPUS and nothing runs it as one. The five below are
// split across Fixtures() and HeldOutFixtures(), which name each of them
// directly; what this returns is the record of what was AUTHORED at this level,
// and TestEveryAuthoredFixtureIsWiredIntoExactlyOneCorpus is what makes the two
// facts agree. Without it a fixture can be written, reviewed, merged and never
// wired into anything, passing every test in the tree while measuring nothing,
// which is the quietest way this corpus has to lose a plant.
//
// THAT IS EXACTLY WHAT HAPPENED TO THESE FIVE, and the paragraph above was true
// of them for as long as it was false. They were authored and never named by
// either accessor, so AllFixtures did not contain them and no ground-truth test
// touched one: unchecked lines, unprobed keywords, unpinned severity, in a diff
// where a plant that measures nothing looks exactly like a plant that does.
// Three defects survived that silence and were found by running the fixtures
// rather than reading them, a Kotlin head that broke every named-argument
// caller, a Rust crate cargo would not build, and a keyword the change's own doc
// comment supplied, and each is written up in the fixture it belongs to.
//
// The guard could not have caught it either: it knew the names warningFixtures
// and nitFixtures and was written as a list, so a set added after it was blind
// to it by construction. It now DISCOVERS every authored fixture out of the
// package source instead, which is the only version of that test that survives
// the next file. Where each of these five went, and why, is argued in Fixtures()
// and HeldOutFixtures().
//
// SUBSTRING KEYWORDS ARE THE WRONG INSTRUMENT FOR PART OF THIS LEVEL, and this
// is the third round to edit these lists, so it is written here rather than
// discovered a fourth time. Every other level in this corpus is defined by a
// failure, and a failure brings its own nouns: nil, injection, WHERE clause,
// symlink, socket. A reviewer that has found the defect uses them and one that
// has not cannot. Info has no failure, so the vocabulary is shared, at this
// level the finding and the objection are frequently the SAME WORDS ABOUT THE
// SAME LINE, differing in what the reviewer is asserting.
//
// Two of the five demonstrate it, and the two are not equally bad:
//
//   - php-forbidden-vs-404 is UNREACHABLE, provably. The finding's fix and the
//     error-body objection's fix are both "make the two denials the same", and
//     "the two denials should return the same response envelope" contains "the
//     two denials should return the same response" as a substring. mentionsAny
//     is strings.Contains, so any keyword crediting the first credits the second:
//     no word list separates them, whatever it contains.
//     TestTheInfoRecallThisInstrumentCannotBuy carries the proof and pins the
//     price, the corpus denies both, so a terse reviewer proposing exactly this
//     plant's fix is scored a miss.
//   - ruby-default-page-size is REACHABLE BUT FRAGILE. "Other consumers now
//     receive 100 rows" is the finding and "other consumers can still receive
//     200" is the cap objection; a keyword could separate them, but only by
//     naming TENSE. Every other keyword in this corpus names subject matter, and
//     one that names grammar is a rule about how a sentence is built rather than
//     about what it says. "other caller" and "other consumer" were removed
//     rather than qualified for that reason. The number is the seam that DOES
//     work. The cap is 200 and the new default is 100, so "rows by default"
//     separates them on subject matter, and it only reaches the sentences that
//     quote the size. The fix stated as a location, "set it at the call site
//     instead", stays uncredited: every phrase reaching it is one a reviewer
//     types about any line in any file.
//
// WHAT THIS MEANS FOR THE NUMBER. Info recall on these two plants is a LOWER
// BOUND and not a measurement: correct terse findings are uncredited by
// construction, and no keyword edit changes that. The three options that would
// , a required conjunction, a veto phrase, or judging detection at info against
// Defect.Why with the model judge, are all changes to Defect and matches(),
// argued in that test. None was made here: this round's scope was the keyword
// damage, and a scorer change to close a measurement gap belongs in a change
// that owns the scorer and can probe it in both directions.
func infoFixtures() []Fixture {
	return []Fixture{
		rustCrateForOneCallFixture(),
		kotlinWidenedInputFixture(),
		rubyDefaultPageSizeFixture(),
		phpForbiddenVsNotFoundFixture(),
		goPackageSingletonFixture(),
	}
}

// rustCrateForOneCallFixture adds a crate to format one string.
//
// This was the anchor's own second example. The ladder read "adding a
// dependency for one helper function is info" until that illustration was
// replaced, for the reason in this file's header, planted in the language
// where a manifest change is most visible. The change is small and entirely
// reasonable: the nightly report printed durations as a seconds count, someone
// on the rota misread 150 as minutes, and the fix spells them "2m 30s".
//
// THE CASE FOR IT, which is why this is not a warning with the dial turned
// down: humantime is small, widely used, and formats plural units and unit
// breaks correctly, which is exactly the kind of tedious code a team should not
// be writing itself. THE CASE AGAINST: it is one call site, the output this
// report needs is a few lines of arithmetic, and a dependency is not a local
// cost. It is in every build, every lockfile bump and whatever audit the team
// runs, forever. Both sentences are ones a senior engineer says. Neither is
// wrong, and the reviewable fact is that the author should have weighed them.
//
// The anchor is the manifest line, not the call site, because the manifest line
// is what the decision IS: the call site is fine either way, and it is the
// second half of a fix that begins by deleting the dependency.
//
// THE FALSE POSITIVE THIS INVITES is the version-specification objection:
// `humantime = "2"` accepts any 2.x, so a reviewer reaches for pinning, an exact
// version, or a lockfile. That is a real remark about supply-chain hygiene and
// it accepts the dependency, which is the opposite of this finding, so
// "version", "pin", "semver" and "supply chain" are all absent, and so is the
// bare word "dependency", which both objections type in their first sentence.
// The second is the output-format remark: humantime prints sub-second
// components, so a job that took 5.2s now renders "5s 200ms". That is an
// intended consequence of the change rather than a defect, and it shares no
// keyword here. Both were run through matches() rather than reasoned about, and
// both are in TestKeywordsAdmitOnlyRealDetections.
//
// IT DID NOT BUILD, and that is worth recording because this is the one fixture
// in either corpus whose plant is IN the manifest. Cargo.toml and src/report.rs
// were its only files, so cargo refused the manifest before compiling anything:
// "no targets specified in the manifest, either src/lib.rs, src/main.rs, a
// [lib] section, or [[bin]] section must be present". A manifest that does not
// build is not a manifest a reviewer is reading, and the finding this fixture
// scores is a judgement about a manifest. src/lib.rs supplies the missing
// target; see the note on Extra below for why it lives there.
//
// IT IS BUILT AND RUN NOW. Both states were extracted and driven under `cargo
// test` with an integration test calling render on a 150-second job: base
// answers "nightly: 150s" and head answers "nightly: 2m 30s". So the change
// does exactly the one thing its doc comment claims and nothing else, and the
// crate a reviewer is asked to judge is one that compiles.
func rustCrateForOneCallFixture() Fixture {
	return Fixture{
		Name: "rust-crate-for-one-call",
		Base: map[string]string{
			"Cargo.toml": `[package]
name = "report"
version = "0.1.0"
edition = "2021"

[dependencies]
serde = { version = "1", features = ["derive"] }
serde_json = "1"

[dev-dependencies]
insta = "1"

[profile.release]
lto = true
`,
			"src/report.rs": `use std::time::Duration;

/// One completed job, as the nightly report renders it.
pub struct Job {
    pub name: String,
    pub took: Duration,
}

/// Renders one line per job.
pub fn render(jobs: &[Job]) -> String {
    jobs.iter()
        .map(|j| format!("{}: {}s", j.name, j.took.as_secs()))
        .collect::<Vec<_>>()
        .join("\n")
}
`,
		},
		Head: map[string]string{
			// The dev-dependency and profile sections are in BOTH states and
			// exist for one reason: the plant sits at line 7, and without them
			// the file is short enough that every line of it falls within
			// noiseTolerance of the plant. A comment-on-every-line spammer is
			// then charged no noise at all, which
			// TestTheNoiseToleranceIsPinnedByTheCorpus calls vacuous and fails
			// on, correctly, the NOISE column measures nothing on a fixture
			// smaller than its own radius.
			"Cargo.toml": `[package]
name = "report"
version = "0.1.0"
edition = "2021"

[dependencies]
humantime = "2"
serde = { version = "1", features = ["derive"] }
serde_json = "1"

[dev-dependencies]
insta = "1"

[profile.release]
lto = true
`,
			"src/report.rs": `use humantime::format_duration;
use std::time::Duration;

/// One completed job, as the nightly report renders it.
pub struct Job {
    pub name: String,
    pub took: Duration,
}

/// Renders one line per job.
///
/// Durations are spelled "2m 30s" rather than as a count of seconds: the rota
/// reads this at 3am and kept taking 150 for minutes.
pub fn render(jobs: &[Job]) -> String {
    jobs.iter()
        .map(|j| format!("{}: {}", j.name, format_duration(j.took)))
        .collect::<Vec<_>>()
        .join("\n")
}
`,
		},
		Extra: map[string]string{
			// The crate root, and the reason this fixture builds at all. It is
			// byte-identical in both states and nothing about the plant needs it
			// to be READABLE. The decision is the manifest line, and the call
			// site it pays for is already in the diff, so it belongs here
			// rather than in Base and Head, and Extra's own doc is the rule:
			// unchanged files never reach the model, and buildRepo writes them
			// so the repository looks like a project. What it buys here is that
			// the project is a CRATE: with no target, cargo will not read the
			// manifest this fixture plants its defect in.
			"src/lib.rs": "pub mod report;\n",
		},
		Defects: []Defect{{
			Path: "Cargo.toml",
			Line: 7, // the added crate
			// Every keyword names either the RATIO (one call site for a whole
			// crate) or the alternative (write the few lines). Nothing here can
			// be typed by a reviewer that has accepted the dependency and is
			// asking for a tighter version, which is the objection this change
			// most invites. "a few lines" and "hand-rolled" were considered and
			// dropped: both appear in the APPROVING sentence too, "this
			// replaces a few lines of hand-rolled formatting", and the corpus
			// has already paid full recall once for admitting a phrase that a
			// finding agreeing with the change would use.
			Keywords: []string{
				"one call site", "single call site", "one use site", "used once",
				"used only once", "only used once", "used in exactly one",
				"for one helper", "for a single helper", "for one function",
				"for a single function", "whole crate", "entire crate",
				"crate for one", "crate for a single", "without the crate",
				"worth a dependency", "worth the dependency", "worth a new dependency",
				"justify the dependency", "justify a new dependency",
			},
			// maintainability is the accurate box and the scope clause that
			// admits it is the one this plant is built to fit: "You may report a
			// maintainability problem only when you can name what it will cost
			// concretely". The cost is named, a crate in every build and every
			// audit for one call.
			Class:        config.ClassMaintainability,
			WantSeverity: config.SeverityInfo,
			SeverityNote: "info under the clause \"a defensible concern the author should consciously " +
				"accept or reject\". The ladder used to illustrate that clause with this plant itself — " +
				"\"adding a dependency for one helper function is info\" — and the illustration was " +
				"replaced rather than the plant, so the level now rests on the clause alone. Not warning " +
				"— \"likely a bug, or a genuine hazard under plausible conditions\" — because there is no " +
				"condition to name: the crate compiles, formats correctly, and no input makes render " +
				"answer wrongly. Not nit: \"minor and optional\" describes a local waste that dies with " +
				"the call, and a dependency is a standing commitment that every future build and audit " +
				"pays. It is one of the three " +
				"maintainability plants, which arrived together and all sit at this level, so nothing in " +
				"the corpus disagrees with it yet.",
			Why: "a crate is added to the manifest for a single call site whose output the standard library expresses in a few lines, so every build, lockfile bump and audit carries it for one formatted string",
		}},
	}
}

// kotlinWidenedInputFixture lifts an exported function off the type it was
// written for.
//
// The anchor's own first example until that illustration was replaced, the
// ladder read "widening an exported type's accepted input is info", which is
// this plant, for the reason in this file's header. The reason it is planted in
// Kotlin rather than Go is that Kotlin's non-null types make the widening carry
// NO new failure. Widening a Go parameter from a struct to an interface makes
// nil a newly reachable input, and a reviewer reporting the nil panic would be
// reporting a real defect this fixture never meant to plant. Here summarize
// cannot be handed null, iterates nothing, and reads only members every
// implementer must provide, so the change is exactly the design decision and
// nothing else.
//
// THE CASE FOR IT: the weekly mail is the next thing on the board, it has no
// day to report, and an interface introduced now can be shaped by the caller
// that needs it rather than retrofitted around one that already shipped. THE
// CASE AGAINST: there is one implementation in this change and the second
// caller does not exist yet; `summarize` is public API, so the wider input can
// never be narrowed back, and DailyReport has grown three public properties
// that are aliases of fields it already had. Both are ordinary review
// positions. The reviewable fact is the fork, not a failure.
//
// THE FALSE POSITIVE THIS INVITES is the aliasing remark: label, gained and
// lost are second names for day, signups and cancellations, so a reviewer asks
// for one set or the other. That is a naming observation, the generation scope
// excludes naming, and its fix leaves the widened signature exactly where it
// is, so "alias", "duplicate", "two names" and "rename" are all absent.
//
// THE SECOND IS THE VISIBILITY REMARK ("make Summarizable internal"), which
// accepts the abstraction and argues about who may SEE it. This comment used to
// claim it was "admitted only through phrases that also name the widening", and
// that was false: "public api", "public surface" and "surface area" were all
// keywords, and all three match a remark that has noticed nothing about the
// parameter, "this adds public API surface area; internal would keep the public
// surface smaller" scored full recall, and survived only by an accident of
// anchor distance. The three are gone.
//
// What is left admits that remark only when it reaches for the widening or for
// the one-implementation argument, and that is deliberate rather than a residual
// leak: a reviewer that writes "there is only one implementation" has made this
// plant's case whatever fix it goes on to propose. The pure form, the one that
// argues visibility and nothing else, is the probe in
// TestKeywordsAdmitOnlyRealDetections, and it is run through matches() rather
// than described. "interface" stays absent for its own reason: it is the
// change's own most typed token.
//
// IT WAS COMPILED, and doing so is how this fixture's second, unplanted defect
// was found. Head used to rename the public parameter from `report` to `source`,
// which is not a change Kotlin lets a caller ignore: named arguments are part of
// the signature, so `summarize(report = r)` compiled against Base and failed
// against Head with "no parameter with name 'report' found" under kotlinc
// 2.0.21. That falsified two sentences of the SeverityNote below verbatim,
// "every caller that compiled before compiles now", and "this breaks none, which
// is exactly why it is not an error", and it made the fixture plant a contract
// break at info. The rename bought nothing, so the name stays: the widening is
// now the only change. Both states were recompiled with that named caller and
// run, and both print "2026-08-04: +12 / -3".
func kotlinWidenedInputFixture() Fixture {
	return Fixture{
		Name: "kotlin-widened-input",
		Base: map[string]string{
			"src/main/kotlin/com/example/report/Summary.kt": `package com.example.report

/** One day's numbers, as the nightly mail renders them. */
data class DailyReport(
    val day: String,
    val signups: Int,
    val cancellations: Int,
)

/** Renders the one-line summary the nightly mail leads with. */
fun summarize(report: DailyReport): String =
    "${report.day}: +${report.signups} / -${report.cancellations}"
`,
		},
		Head: map[string]string{
			"src/main/kotlin/com/example/report/Summary.kt": `package com.example.report

/**
 * Anything a summary line can be rendered from.
 *
 * The weekly mail is next and has no day to report, so summarize is being
 * lifted off DailyReport now rather than when that lands.
 */
interface Summarizable {
    val label: String
    val gained: Int
    val lost: Int
}

/** One day's numbers, as the nightly mail renders them. */
data class DailyReport(
    val day: String,
    val signups: Int,
    val cancellations: Int,
) : Summarizable {
    override val label: String get() = day
    override val gained: Int get() = signups
    override val lost: Int get() = cancellations
}

/** Renders the one-line summary the nightly mail leads with. */
fun summarize(report: Summarizable): String =
    "${report.label}: +${report.gained} / -${report.lost}"
`,
		},
		Extra: map[string]string{
			"build.gradle.kts": "plugins {\n    kotlin(\"jvm\") version \"2.0.0\"\n}\n",
		},
		Defects: []Defect{{
			Path: "src/main/kotlin/com/example/report/Summary.kt",
			Line: 27, // the widened signature
			// Anchored at the SIGNATURE rather than at the interface
			// declaration, because the accepted input is what the decision IS
			// and it is the line that cannot be taken back:
			// Summarizable could be deleted tomorrow, summarize's parameter
			// could not.
			//
			// "interface" is deliberately absent. It is the change's own most
			// typed token, it is in the diff twice, and the visibility objection
			// this fixture declines to credit opens with it.
			//
			// "public surface", "public api" and "surface area" were here and
			// are gone. Every one of them names VISIBILITY, which is the one
			// objection this plant's own comment says it does not credit, and a
			// remark that asks for `internal` and never mentions the parameter
			// matched all three.
			//
			// The bare stems "widen", "wider" and "widening" went with them, and
			// for a subtler version of the same reason: the visibility objection
			// is ALSO about something getting wider, so "publishing this type
			// widens what the module exposes" scored as a detection of a plant
			// about a parameter. What is left says WHAT is widened. The cost is
			// a longer list, and it is the right trade here: the level this
			// fixture measures is decided by a single sentence, so a keyword
			// that fires on the wrong sentence is the whole measurement.
			//
			// The line this list draws, stated so the next editor does not move
			// it by accident: a remark arguing only about who may SEE the type
			// is not credited; one that reaches for the widening, or for the
			// fact that nothing yet needs it, is, and is credited on purpose,
			// whatever fix it goes on to propose.
			//
			// "open set" CAME OUT, and it is the last casualty of the round that
			// removed the visibility words: a type published to the world is an
			// open set too, so "publishing this interface exposes an open set of
			// types; keep it internal" scored full recall on a plant about a
			// parameter. It is in the miss probes.
			//
			// TWO RECALL KEYWORDS WENT IN, and getting there took a wrong turn
			// worth recording. Round 7 removed sixteen keywords with a miss
			// probe each and probed the other direction nowhere, so two plain
			// statements of this finding came back MISSED: "this widens what
			// summarize accepts before anything but DailyReport needs it" and
			// "summarize's parameter is wider than anything that calls it
			// today". The first repair added "summarize accepts", "summarize's
			// parameter" and "wider than anything" under a comment claiming all
			// three "name summarize's INPUT, which is the one thing the
			// visibility objection never mentions". Two of the three did not,
			// and running them is all it took to see it:
			//
			//   - "summarize's parameter" credited "Summarizable is public with
			//     no consumer outside this module; mark it internal.
			//     summarize's parameter can stay as it is.", the pure
			//     visibility objection, mentioning the parameter only to say it
			//     is FINE, scored full recall.
			//   - "wider than anything" credited "the public surface here is
			//     wider than anything the module needed; keep Summarizable
			//     internal." It is a bare comparative and names no input at all,
			//     which is the same failure the stems "widen"/"wider" were
			//     removed for one paragraph above.
			//   - "summarize accepts" credited a locale remark: "String.format
			//     uses the default locale here; summarize accepts a Summarizable
			//     and returns a String that changes per JVM."
			//
			// What is here now is the narrowed pair, and each was chosen by
			// running the leak sentence against it rather than by reading it.
			// "what summarize accepts" requires the sentence to be ABOUT the
			// accepted input rather than merely to name the call; "parameter is
			// wider" requires the comparative to be about the PARAMETER, which
			// is what the visibility objection never says. All three leak
			// sentences are miss probes, so restoring any of the removed forms
			// fails TestKeywordsAdmitOnlyRealDetections.
			Keywords: []string{
				"only one implementation", "single implementation", "one implementer",
				"no second implementation", "second caller", "does not exist yet",
				"not exist yet", "no other caller", "no other type",
				"speculative", "premature", "yagni", "until the second",
				"widen the parameter", "widens the parameter", "widening the parameter",
				"widens its parameter", "widens summarize", "widening summarize",
				"widen the input", "widens the input", "widening the input",
				"widens the accepted", "widening the accepted", "accepted input",
				"wider type", "wider parameter", "wider input", "wider signature",
				"what summarize accepts", "parameter is wider",
				"any implementation", "cannot be narrowed", "narrow it back",
			},
			Class:        config.ClassMaintainability,
			WantSeverity: config.SeverityInfo,
			SeverityNote: "info under the clause \"a defensible concern the author should consciously " +
				"accept or reject\". The ladder used to illustrate that clause with this plant itself — " +
				"\"widening an exported type's accepted input is info\" — and the illustration was " +
				"replaced rather than the plant, so the level now rests on the clause alone. Not " +
				"warning: there is no \"genuine hazard under plausible conditions\" to name, because Kotlin's non-null types mean no new input is " +
				"reachable — every caller that compiled before compiles now and produces the same string. " +
				"Not contract either, which is the class a reader may expect: ClassContract is \"a change " +
				"that breaks existing callers\" and this breaks none, which is exactly why it is not an " +
				"error. Not nit: the accepted input of a public function is not minor and optional, because " +
				"it is the one part of this change that can never be narrowed back.",
			Why: "summarize's exported parameter is widened from DailyReport to an interface with one implementation and no second caller in the change, so the package now accepts an open set it cannot narrow again",
		}},
	}
}

// rubyDefaultPageSizeFixture quadruples a default nobody else was asked about.
//
// The seed in its plainest form: a default changed for one caller's benefit,
// with a reason stated for that caller and none offered for the rest. It is
// Ruby, which is new to the corpus, and the change is four characters wide.
//
// THE CASE FOR IT: defaults exist to serve the caller that makes the
// requests, the web client is that caller, and a default the busiest consumer
// has to override is a default that is wrong. THE CASE AGAINST: every OTHER
// consumer, the mobile client, the RSS job, the moderation queue, now
// receives and serializes four times the rows it did yesterday without asking
// for them or being mentioned in the change, and the alternative is one keyword
// argument at the one call site that wanted it. Reasonable engineers split on
// this every week.
//
// NOTHING HERE IS A DEFECT AND THAT IS DELIBERATE. The cap still applies, the
// clamp is unchanged, the offset arithmetic is unchanged, and 100 rows is not a
// size anything falls over at. The moment it were, this would be a warning
// about a named failure rather than an info about a decision.
//
// THE FALSE POSITIVE THIS INVITES is the offset-pagination remark: deep pages
// scan and discard rows, so a reviewer asks for keyset pagination. It is a real
// observation about a line this change did not touch, and it is about the same
// method, so distance does not exclude it. The second is the cap: MAX_PER_PAGE
// is 200 and a client may still ask for it, which is pre-existing and
// unchanged. Neither reaches for a word about who did not ask for the change,
// which is what every keyword here requires. The bare stem "default" is absent
// for a mechanical reason: keywords match case-insensitive substrings, and
// DEFAULT_PER_PAGE is the changed line's own identifier, so any finding quoting
// it, including both objections above, would have scored as a detection.
//
// THE CAP OBJECTION WAS CREDITED ANYWAY, which is what running it rather than
// reasoning about it found: "a client can still pass per_page: 200 and get 200
// rows per request; consider lowering the cap now that the payload is larger"
// matched three keywords at once, on a line inside anchorTolerance of the plant,
// so distance saved nothing. Both objections are now probes in
// TestKeywordsAdmitOnlyRealDetections and both come back uncredited; see the
// note on Keywords for which words went and why.
//
// IT WAS RUN. Both states were driven under ruby 3.2.3 against a stub Comment
// that records the relation it is handed. Base answers offset 0 limit 25 and
// head answers offset 0 limit 100; an explicit per_page: 10 is honoured
// unchanged by both, and per_page: 5000 is clamped to 200 by both. So the cap,
// the clamp and the offset arithmetic really are untouched, which is the claim
// the paragraph above rests on and the reason nothing here is a defect.
func rubyDefaultPageSizeFixture() Fixture {
	return Fixture{
		Name: "ruby-default-page-size",
		Base: map[string]string{
			"app/queries/comments_query.rb": `# frozen_string_literal: true

# CommentsQuery lists one article's comments, newest first.
class CommentsQuery
  MAX_PER_PAGE = 200

  DEFAULT_PER_PAGE = 25

  def initialize(article_id, per_page: DEFAULT_PER_PAGE)
    @article_id = article_id
    @per_page = per_page.clamp(1, MAX_PER_PAGE)
  end

  def call(page: 1)
    Comment.where(article_id: @article_id)
           .order(created_at: :desc)
           .offset((page - 1) * @per_page)
           .limit(@per_page)
  end
end
`,
		},
		Head: map[string]string{
			"app/queries/comments_query.rb": `# frozen_string_literal: true

# CommentsQuery lists one article's comments, newest first.
class CommentsQuery
  MAX_PER_PAGE = 200

  # The web client renders about 90 comments above the fold and was asking
  # for four pages to fill it.
  DEFAULT_PER_PAGE = 100

  def initialize(article_id, per_page: DEFAULT_PER_PAGE)
    @article_id = article_id
    @per_page = per_page.clamp(1, MAX_PER_PAGE)
  end

  def call(page: 1)
    Comment.where(article_id: @article_id)
           .order(created_at: :desc)
           .offset((page - 1) * @per_page)
           .limit(@per_page)
  end
end
`,
		},
		Defects: []Defect{{
			Path: "app/queries/comments_query.rb",
			Line: 9, // the new default
			// "the web client" and "one client" were considered and dropped:
			// the change's own comment names the web client, so a finding about
			// anything in this file can quote it, and the objection this plant
			// is about is precisely the callers the comment does NOT name.
			//
			// FIVE MORE CAME OUT, each for the same reason and each measured
			// through matches() rather than argued about. "pass per_page" and
			// "passing per_page" credited the cap objection this fixture's own
			// comment says it excludes, "a client can still pass per_page: 200"
			// is how that objection writes itself, and it accepts the new
			// default entirely. "payload" and "rows per request" credited any
			// generic remark about size, including the same one. "at the call
			// site" is a phrase a reviewer types about any line in any file.
			// "4x" went with them for a different reason: it is two characters
			// and a substring, and a corpus that admits substrings that short is
			// one keyword away from crediting a sentence about a 4xx status.
			//
			// "every caller" and "all callers" came out last and were the
			// hardest to give up, because they are how a real detection opens.
			// They also open the cap objection: "every caller can still request
			// up to MAX_PER_PAGE" is a finding that has noticed nothing and was
			// credited in full. The rule this list settles on is that a keyword
			// NAMING CALLERS must name them NEGATIVELY. The ones that do not
			// pass per_page, did not ask, and were not mentioned, because those
			// are the only callers this plant is about, and no objection that
			// accepts the new default has a reason to mention them. Singular
			// stems are used where they match both numbers.
			//
			// The rule covers the caller keywords and NOT the whole list, which
			// this sentence used to imply: "four times", "quadruple", "opt in"
			// and "rows by default" name the change's magnitude rather than any
			// caller, and they are held to the other bar instead, no objection
			// this fixture excludes reaches them, which is a probe rather than a
			// claim.
			//
			// "other caller" and "other consumer" DID NOT SURVIVE THAT RULE, and
			// they are the reason it is written here rather than assumed. Neither
			// names a caller negatively: the cap objection reaches both without
			// noticing anything, "other consumers can still request up to
			// MAX_PER_PAGE, so the cap is the real ceiling here", which is the
			// same finding "every caller" was removed for admitting, one word
			// narrower. It is in the miss probes.
			//
			// "rows by default" WENT IN as the recall half. Round 7 probed
			// sixteen removals for false credit and probed recall nowhere, and
			// this fixture's OWN PROPOSED FIX came back MISSED: "every consumer
			// of CommentsQuery now gets 100 rows by default; the web client
			// could pass per_page: 100 itself" is the alternative this plant's
			// doc comment argues for, and no keyword reached it. The bare "by
			// default" was tried first and is a leak in three directions at
			// once, all three run rather than imagined: the offset objection
			// ("by default page 50 scans 5000 rows"), a frozen-string-literal
			// nit, and the cap objection with four ordinary words appended.
			// Naming the ROWS is what turns it into a sentence about this plant.
			// No excluded objection can say it: the cap is 200 and is not a
			// default, and the offset remark counts rows SCANNED rather than
			// returned.
			//
			// "AT THE CALL SITE" WAS REPLACED BY "THE CALL SITE" AND THAT WAS
			// STRICTLY WORSE, which is written down because the reasoning that
			// produced it sounded careful: the bare form is "a phrase a reviewer
			// types about any line in any file" and the form with its article,
			// it argued, is not. Containment runs the other way. mentionsAny is
			// strings.Contains, "the call site" is a SUBSTRING of "at the call
			// site", so the replacement credited everything the removal denied
			// and more, "this allocation happens at the call site, which is
			// fine" scored full recall on this plant.
			//
			// Both are gone and NOTHING REPLACES THEM. The alternative stated as
			// "set it at the call site instead" is therefore uncredited, and
			// TestTheInfoRecallThisInstrumentCannotBuy runs that sentence and
			// records the price rather than leaving it to this paragraph. The
			// one candidate that reached it without naming a call site, "what
			// everyone gets", credits no probe in this package's table, and was
			// still rejected: it names the population POSITIVELY, which is what
			// "every caller" came out for, and one ordinary cap objection
			// reaches it ("the clamp is what everyone gets in the end, so
			// MAX_PER_PAGE is the real ceiling"). That sentence was run against
			// the candidate before it was dropped, and that test fails if the
			// candidate is ever added here.
			Keywords: []string{
				"existing caller", "unmodified caller",
				"never pass", "never passes", "does not pass", "do not pass",
				"without passing", "did not ask", "never asked", "nobody asked",
				"no one asked", "without asking", "rows by default",
				"four times", "4 times", "quadruple", "opt in", "opt into", "opt-in",
			},
			// resource is the least-wrong box, on the convention
			// capacity-hint-nit set for this class: the closed set has no home
			// for work that is merely larger than it needed to be, and nothing
			// here leaks or grows without bound. Recorded rather than chosen
			// silently, because Class is author-declared and nothing scores a
			// model against it.
			Class:        config.ClassResource,
			WantSeverity: config.SeverityInfo,
			SeverityNote: "info under \"a defensible concern the author should consciously accept or reject\": " +
				"the size of a default is a decision with two defensible answers, and this change states a " +
				"reason for one caller and none for the others. Not warning, which is where the other " +
				"resource plants in this corpus sit: \"a genuine hazard under plausible conditions\" needs a " +
				"condition, and 100 rows under an unchanged cap of 200 has none — retry-no-backoff and " +
				"ts-unbounded-memo-key both name one, a retry storm and an unbounded key space. Not nit " +
				"either: \"minor and optional\" describes work that dies with the call, and this changes what " +
				"every unmodified caller receives from now on. It is the lowest of the four levels this " +
				"class carries and the only one with no named failure at all.",
			Why: "the default page size is quadrupled for one caller's benefit, so every consumer that never passes per_page now fetches and serializes four times the rows without appearing in the change",
		}},
	}
}

// phpForbiddenVsNotFoundFixture chooses which of two correct answers to give a
// caller who may not read something.
//
// The change ADDS the membership check: before it, any signed-in viewer could
// read any project, and after it they cannot. That is the shape worth having at
// this level, because it makes the fixture unmistakably not a defect with the
// dial turned down. The change is a security improvement, and the only thing
// left to review is which of two correct denials it should send.
//
// THE CASE FOR 403: it is what the status code means, it tells a legitimate
// user who has landed on a colleague's link that they need access rather than
// that they mistyped, and support can tell the two apart. THE CASE FOR 404: a
// 403 confirms to anyone holding an id, from a shared link, a log line, a
// referrer, a support ticket, that a project with that id exists and that they
// are not on it, and hiding that costs nothing but debuggability. Serious
// products ship both: most APIs answer 403, and GitHub answers 404 for a
// private repository. That is the strongest available evidence that this is a
// decision rather than a defect, and it is why the level is info.
//
// THE FALSE POSITIVE THIS INVITES is the authorization objection: a reviewer
// that reads the added branch as missing rather than present reports an IDOR,
// and one that has not read the docblock asks who $viewerId is. Both are
// hallucinations about a check that is in the diff, so "authorization", "access
// control", "idor" and "permission" are all absent. The second is the
// error-body remark, the two responses carry different bodies, so a reviewer
// may ask for a shared error shape, which is a consistency observation whose
// fix leaves the disclosure exactly where it is.
//
// THIS FIXTURE CREDITED BOTH OF THEM, and it took running them through
// matches() to see it, which is the point. "enumerat" credited the IDOR
// hallucination in the same paragraph that declares it must not be credited:
// "an attacker can enumerate project ids" is how that finding writes itself, and
// enumeration is the attack that FOLLOWS this disclosure rather than a sign the
// disclosure was noticed. "exists" is a bare English verb and credited "No test
// exists for the non-member path", a remark about coverage, scored as a
// security detection. "hide" and "hides" credited the error-body remark, which
// asks the 403 body to "hide internal details". All four are gone and the
// probes are in TestKeywordsAdmitOnlyRealDetections. What is left either names
// the disclosure as a noun or requires the sentence to say what the response
// tells the caller.
//
// IT WAS RUN, under php 8.3, against stub Response, Project and
// ProjectRepository classes with one project the viewer is a member of, one it
// is not, and one that does not exist. Base answers 200, 200, 404: any
// signed-in viewer reads any project. Head answers 200, 403, 404, which is
// both the security improvement the change is for and, in the last two rows,
// the disclosure this plant is about, since the pair of denials is exactly what
// tells a caller holding an id which of the two it is holding.
func phpForbiddenVsNotFoundFixture() Fixture {
	return Fixture{
		Name: "php-forbidden-vs-404",
		Base: map[string]string{
			"src/Http/ProjectController.php": `<?php

declare(strict_types=1);

namespace App\Http;

/** ProjectController answers the project endpoints. */
final class ProjectController
{
    public function __construct(private ProjectRepository $projects)
    {
    }

    /** GET /projects/{id}, for the signed-in viewer. */
    public function show(string $projectId, string $viewerId): Response
    {
        $project = $this->projects->find($projectId);
        if ($project === null) {
            return new Response(404, ['error' => 'not_found']);
        }

        return new Response(200, $project->toArray());
    }
}
`,
		},
		Head: map[string]string{
			"src/Http/ProjectController.php": `<?php

declare(strict_types=1);

namespace App\Http;

/** ProjectController answers the project endpoints. */
final class ProjectController
{
    public function __construct(private ProjectRepository $projects)
    {
    }

    /** GET /projects/{id}, for the signed-in viewer. */
    public function show(string $projectId, string $viewerId): Response
    {
        $project = $this->projects->find($projectId);
        if ($project === null) {
            return new Response(404, ['error' => 'not_found']);
        }

        if (!$this->projects->isMember($projectId, $viewerId)) {
            return new Response(403, ['error' => 'forbidden']);
        }

        return new Response(200, $project->toArray());
    }
}
`,
		},
		Defects: []Defect{{
			Path: "src/Http/ProjectController.php",
			Line: 23, // the denial that answers a question it was not asked
			// "404" is absent as a bare token: the branch above returns one, so
			// any finding that describes this method can contain it. What is
			// left names what the response TELLS the caller, which is a sentence
			// only a reviewer that has noticed the disclosure writes.
			//
			// "exists" is absent for the same reason and was not, which cost
			// this plant a false detection on every finding containing an
			// ordinary English verb. It survives only in the two forms that
			// require the sentence to be ABOUT the disclosure, "whether the
			// project exists" and "that the project exists", because a reviewer
			// merely describing the branch writes "when the project exists but
			// the viewer is not a member", and that is not a finding. "enumerat",
			// "hide" and "hides" came out with it; the doc comment above says
			// which objection each one was crediting.
			//
			// "same response" CAME OUT NEXT, and it is where this instrument
			// stops. The finding says the two denials ARE distinguishable; the
			// error-body objection says they SHOULD return the same envelope.
			// Both sentences are about two responses being the same, both are
			// ordinary English, and they differ by what the reviewer is
			// ASSERTING rather than by any word either one uses, "both denials
			// should return the same response envelope" was credited in full.
			// "indistinguishable" survives on the narrower ground that asking
			// for the denials to be indistinguishable IS this plant's fix, so a
			// reviewer typing it has reached the disclosure whatever else they
			// say. See this file's header for what substring matching cannot
			// separate here.
			//
			// "identical response" was struck out in the same edit and DID NO
			// WORK, which was found by restoring it alone and re-running every
			// probe on this fixture: not one verdict changed. It was removed on
			// a reading of the sentence rather than on a run, in the round whose
			// whole thesis is that prose is not executable. The removal stands,
			// it is the same failure as "same response" one word over, but it
			// is now CHARGED: the error-body objection appears below in the
			// wording that uses it, so putting the keyword back fails
			// TestKeywordsAdmitOnlyRealDetections instead of passing silently.
			//
			// "learns the project exists" went IN, for the recall direction
			// Round 7 never probed: "a non-member now learns the project exists;
			// the 404 above would not have told them" is this finding in its
			// plainest form and no keyword reached it, because both surviving
			// "exists" forms require the word `that` or `whether`. This is those
			// two forms extended to the one that uses no connector at all, and
			// it still requires the sentence to name what the caller learns
			// about EXISTENCE.
			//
			// The looser pair tried first, "now learns" and "learns the
			// project", was withdrawn for failing exactly the rule the comment
			// claimed for it. Run through matches(), "now learns" credited an
			// N+1 remark ("the second now learns nothing the first did not
			// already have") and "learns the project" credited an input
			// validation nit ("show() learns the project id and the viewer id
			// from the router"). Both sentences say what SOMETHING learns;
			// neither says what the CALLER learns from the response, and neither
			// has noticed the disclosure. Both are miss probes now.
			Keywords: []string{
				"existence", "whether the project exists", "that the project exists",
				"reveals whether", "leaks whether", "learns whether",
				"confirms that", "confirms the project", "confirms to",
				"discloses", "disclosure", "tells the caller that", "learns that",
				"learns the project exists", "indistinguishable",
			},
			Class:        config.ClassSecurity,
			WantSeverity: config.SeverityInfo,
			SeverityNote: "info under \"a defensible concern the author should consciously accept or reject\", " +
				"and the lowest level this corpus plants in the security class by a distance. Not warning — " +
				"\"a genuine hazard under plausible conditions\" — because the check the change adds WORKS: " +
				"no project data crosses the boundary, and calibration rule 1, \"rate the demonstrated " +
				"consequence, not the worst imaginable one\", limits what is demonstrated to the fact that " +
				"an id exists. python-timing-unsafe-hmac and bash-fixed-temp-path, the class's two warnings, " +
				"each name an attacker and a mechanism; this names neither. Not nit: which denial an API " +
				"sends is a posture decision the whole product then lives with, not something minor and " +
				"optional. It is the plant that makes \"security implies at least warning\" false.",
			Why: "the new membership check denies non-members with 403, which confirms to anyone holding an id that the project exists, where the 404 above would not — both answers are defensible and the change picks one silently",
		}},
	}
}

// goPackageSingletonFixture adds a process-wide value and the helpers that read
// it.
//
// The one Go plant in this set. It is the seed in its most familiar form: a
// package-level default plus thin wrappers, so a caller can ask one question
// without being handed a *Set first.
//
// THE CASE FOR IT: this is what the standard library does, http.DefaultClient,
// log.Default, flag.CommandLine, and threading a value through four layers so
// one leaf can ask "is this flag on" is a real cost paid by everything in
// between. THE CASE AGAINST: the answer is now fixed at process start from the
// environment, so a test that wants a different set has to reach into the
// package and put it back, and two consumers in one binary cannot differ. The
// alternative is already written: Load returns a *Set and Enabled is a method
// on it, so passing one costs a parameter.
//
// Nothing here is a defect and the code is deliberately built so that nothing
// is. Load skips empty names, so an unset variable yields an empty set rather
// than a set containing "". Set is read-only once built and says so, so there
// is no race to report. Enabled cannot panic. Strip any of that and the fixture
// stops being about the decision.
//
// THE FALSE POSITIVE THIS INVITES is the configuration objection: os.Getenv
// returns "" for an unset variable, so a reviewer asks for validation or a log
// line at startup. It is a different concern with a different fix and it
// accepts the singleton, so "env", "environment", "getenv" and "features" are
// all absent. The second is a race report about the shared map, which the
// type's own doc rules out. And "thread" is absent for a mechanical reason: the
// change's own comment contains "thread a *Set through every layer", so it is a
// word a reviewer can type by quoting.
//
// "package-level" WAS NOT ABSENT, and the same comment contains it too, the
// exact failure the sentence above describes, in the same fixture, one clause
// later. See the note on Keywords. Both objections above are now probes in
// TestKeywordsAdmitOnlyRealDetections, and so is the docs nit that found it.
//
// IT WAS BUILT AND RUN. Both states were extracted as a module and driven under
// `go vet` and `go run` with FEATURES=alpha,beta. Load("alpha, beta, ,gamma")
// answers true, true, false, false in both, so the empty name really is skipped
// and no input makes Enabled wrong; head additionally answers through the
// package-level helper, which is the whole of what the change adds.
func goPackageSingletonFixture() Fixture {
	return Fixture{
		Name: "go-package-singleton",
		Base: map[string]string{
			"features/features.go": `package features

import "strings"

// Set is the features that are on for one process. It is read-only once
// built, so a Set may be shared by any number of goroutines.
type Set struct {
	on map[string]bool
}

// Load parses a comma-separated list of feature names.
func Load(list string) *Set {
	s := &Set{on: map[string]bool{}}
	for _, name := range strings.Split(list, ",") {
		name = strings.TrimSpace(name)
		if name != "" {
			s.on[name] = true
		}
	}
	return s
}

// Enabled reports whether name is on.
func (s *Set) Enabled(name string) bool { return s.on[name] }
`,
		},
		Head: map[string]string{
			"features/features.go": `package features

import (
	"os"
	"strings"
)

// Set is the features that are on for one process. It is read-only once
// built, so a Set may be shared by any number of goroutines.
type Set struct {
	on map[string]bool
}

// Load parses a comma-separated list of feature names.
func Load(list string) *Set {
	s := &Set{on: map[string]bool{}}
	for _, name := range strings.Split(list, ",") {
		name = strings.TrimSpace(name)
		if name != "" {
			s.on[name] = true
		}
	}
	return s
}

// Enabled reports whether name is on.
func (s *Set) Enabled(name string) bool { return s.on[name] }

// Default is the Set the package-level helpers read. It is built at process
// start so a caller does not have to thread a *Set through every layer.
var Default = Load(os.Getenv("FEATURES"))

// Enabled reports whether name is on in Default.
func Enabled(name string) bool { return Default.Enabled(name) }
`,
		},
		Extra: map[string]string{
			"go.mod": "module example.com/features\n\ngo 1.25\n",
		},
		Defects: []Defect{{
			Path: "features/features.go",
			Line: 31, // the process-wide value
			// Anchored at the variable rather than at the wrapper below it: the
			// wrapper is a consequence, and a reviewer that wants the parameter
			// back deletes this line first. The wrapper is three lines away,
			// inside anchorTolerance, so a finding there is still credited.
			//
			// THE BARE STEMS "package-level" AND "package level" ARE GONE, and
			// they are the reason this list needed rereading: the change's own
			// added doc comment says "the package-level helpers read", three
			// lines from the plant, so a docs nit that quoted it, "the comment
			// says Default is the Set the package-level helpers read; say which
			// variable a caller should set", was scored as having found the
			// plant. A keyword the diff supplies is a keyword the reviewer did
			// not have to earn. The compounds that replace them cannot be
			// reached by quoting: each one names the standing cost rather than
			// the location.
			//
			// THE COST OF THAT REMOVAL WAS PAID IN RECALL AND NOT MEASURED, which
			// is the half Round 7 left out. "Adding a package-level default fixes
			// the answer for the whole binary; keep returning a *Set and let
			// callers hold it" is this finding stated plainly, and it came back
			// MISSED: every compound above names the PATTERN, and a reviewer who
			// has read the diff names the SCOPE, the binary, because the scope
			// is what the added line changes. "answer for the whole" closes it
			// and requires the sentence to say what is fixed for that scope.
			//
			// THREE COMPOUNDS WERE TRIED FIRST UNDER A CLAIM THAT NONE OF THEM
			// WAS "reachable by quoting: each one names the standing cost rather
			// than the location". Running them says otherwise, and two of the
			// three were pure widening besides:
			//
			//   - "whole binary" credited the configuration objection this
			//     fixture's doc comment already excludes, wearing the scope
			//     word: "a typo in FEATURES turns a flag off for the whole
			//     binary with no error". Naming the ANSWER is what separates the
			//     finding from it. The objection is about which flags are in
			//     the set, not about there being one set.
			//   - "one binary" is ten characters and a substring. "The package
			//     function Enabled and the method Enabled differ by one binary
			//     decision at the call site" is a naming nit and was credited.
			//   - "reach into the package" is this fixture's own doc-comment
			//     phrasing, and a docs nit that quotes the diff's added comment
			//     back at it reached it in full.
			//
			// The last two were also FREE: deleting either left the whole suite
			// green, because the hit probe each was added for is credited by
			// "global variable" and by "as a parameter" anyway. A keyword that
			// no test charges for in either direction is not evidence of
			// anything, so the rule for ADDING one to this corpus is now checked
			// rather than stated: TestKeywordsAdmitOnlyRealDetections carries a
			// soleCreditors list, and a keyword on it must be the ONLY keyword
			// crediting some hit probe, deleting it then fails a test, while
			// the miss probes keep charging it in the other direction. 345 of
			// the corpus's 358 keywords predate that list and nothing enforces
			// it for them; the list is what a keyword added from here on has to
			// join.
			Keywords: []string{
				"package-level global", "package level global",
				"package-level state", "package level state",
				"global state", "global variable",
				"global mutable", "process-wide", "process wide", "one per process",
				"singleton", "hidden dependency", "implicit dependency",
				"exported mutable", "replace the shared",
				"hard to test", "harder to test", "cannot be overridden",
				"override it", "swap it out", "as a parameter", "as an argument",
				"pass the set", "passing the set", "explicit dependency",
				"answer for the whole",
			},
			Class:        config.ClassMaintainability,
			WantSeverity: config.SeverityInfo,
			SeverityNote: "info under \"a defensible concern the author should consciously accept or reject\" — " +
				"a package-level default is what several standard library packages ship, and the argument " +
				"against it is about testability and reuse rather than about anything going wrong. Not " +
				"warning: there is no \"genuine hazard under plausible conditions\", because Set is read-only " +
				"once built and says so, Load skips empty names, and no sequence of calls produces a wrong " +
				"answer. Not nit: this fixes for the life of the process how every future caller in the " +
				"binary gets its answer, which is not minor and not optional. It shares the maintainability " +
				"class with the crate and the widened signature, and all three sit at this level for the " +
				"same reason: a named cost, no named failure.",
			Why: "the package gains a process-wide Set built at init and helpers that read it, so the answer is fixed for the whole binary and a test or a second consumer that needs a different set has to reach into the package variable",
		}},
	}
}
