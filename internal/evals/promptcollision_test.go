package evals

// Guards against one defect: the shipped prompt naming what the corpus plants.
//
// Two of them do the guarding, a mechanical keyword scan and a tripwire over
// the severity ladder, and the rest of the file is their supporting evidence:
// one test pinning the normalization the scan depends on, one pinning the
// sentence the tripwire's second question refers to, and one demonstrating the
// limit neither of them can cover.
//
// WHAT HAPPENED. review.md illustrated `info` with "Widening an exported type's
// accepted input is info. Adding a dependency for one helper function is info.",
// kotlin-widened-input and rust-crate-for-one-call stated almost verbatim,
// three lines above "These examples ... are deliberately drawn from defect
// classes you are unlikely to meet in this change; do not go looking for them."
// The prompt named two planted defects and then told the reviewer to ignore
// them. Measured on kimi-k3 over two independent three-run batteries, both
// fixtures scored 0 of 3 every time, usually with an empty findings list. The
// error rung had the same shape against timezone-boundary, and the nit rung's
// one illustration was the stated basis of five nit SeverityNotes; both were
// replaced in the same change. fixtures_info.go, fixtures_nit.go and
// timezone-boundary's own comment in fixtures.go carry the full arguments.
//
// WHY TWO GUARDS and not ONE. The two halves of that defect are not detectable
// the same way.
//
//   - The MEASUREMENT half is literal. "accepted input" is a kotlin-widened-input
//     keyword and sat character-for-character in the prompt, so a reviewer could
//     produce a crediting phrase by quoting its own instructions: matches()
//     credits a finding anchored within anchorTolerance of the plant whose
//     title, rationale or category contains a keyword as a substring, so that
//     costs full recall for a comment that noticed nothing. That half is
//     mechanical, and TestNoPlantedKeywordAppearsInTheShippedPrompt runs it.
//     Restoring the pre-fix `info` rung makes it report both "accepted input"
//     and "for one helper"; an earlier version of this comment claimed no
//     keyword was involved, and the file's own fail-first evidence refutes it.
//   - The USER-FACING half is semantic and no scan reaches it. The collision
//     that mattered was that the SENTENCE described the plant, and the same
//     sentence is a defect for a real user with no corpus anywhere: widening an
//     exported signature is among the most ordinary things a reviewer meets, so
//     "you are unlikely to meet this" was false and the prompt was suppressing
//     a legitimate finding. An illustration can do that while sharing no
//     keyword at all, api.md's `info` rung did, and scores zero hits against
//     the whole corpus. TestTheSeverityLadderIllustrationsArePinned is a
//     tripwire for that half: it forces a human to look, and it decides nothing
//     itself.
//
// Not BEHIND `//go:build eval`, and that IS THE POINT OF IT. These tests sat
// behind that tag when they were written, which made "the build goes red" false:
// the project gate runs `go test ./...` untagged, CI runs `go test -race ./...`,
// and every Makefile eval target is `-run`-filtered to a named paid battery, so
// no command anybody runs reached them. Demonstrated before the tag came off:
// replacing review.md's `nit` rung with "*Widening an exported type's accepted
// input is a nit.*", one edit that both moves a pinned illustration and plants
// a kotlin-widened-input keyword verbatim, passed `go test ./internal/evals/
// -count=1`, which is exactly what the gate runs for this package. It fails now.
//
// Nothing here needs more than the embedded templates and the in-process
// fixtures, which is why every guard of that kind in this package,
// groundtruth, claims, severity, crossjudge, incumbent, cost, rejudge,
// fixtures_dedup, is untagged too. One offline test does still carry the tag,
// score_span_test.go, and it is the same mistake at a smaller scale rather than
// a counter-example.

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/prompt"
	"github.com/jdziat/open-nitpick/internal/review"
)

// shippedPromptTexts renders the prompt text a review is generated from, keyed
// by a stable source label.
//
// The note behind it is in docs/measurement.md#shippedprompttexts.
func shippedPromptTexts(t *testing.T) map[string]string {
	t.Helper()

	out := map[string]string{}

	for _, name := range []string{prompt.NameReview, prompt.NameTriage} {
		p, err := prompt.Build(name, prompt.Options{})
		if err != nil {
			t.Fatalf("building the %s prompt: %v", name, err)
		}
		out[name+".md"] = p.String()
	}

	// The validation contract is here and the 14 per-domain expert prompts are
	// not, and the line between them is that this text is SHARED: every expert
	// call carries it whatever the finding was about, so a plant's vocabulary in
	// it is not a domain naming its domain. It costs one exception to scan,
	// measured, the whole contract collides with exactly one keyword, and it is
	// the "utc"-inside-"outcome" accident already disclosed three times below.
	out["validation-contract"] = review.ValidationContract()
	// The slop layer is in the prompt whenever review.slop is on, which the
	// slop corpus needs, so its keywords are swept against it too.
	out["slop"] = prompt.SlopGuidance()

	// Every level, not only config.GenerationLevel. A branch nothing renders
	// today ships the day that constant moves, and a guard that could only see
	// today's branch reports the collision only after something has been
	// measured against it.
	for _, level := range []config.NitpickLevel{
		config.NitpickOff, config.NitpickMinimal, config.NitpickNormal, config.NitpickPedantic,
	} {
		out["scope:"+string(level)] = prompt.ScopeText(level)
	}

	// The cross product of the voice axes, because each axis is an independent
	// switch and one of the collisions this file records lives in a single
	// branch of one of them: the hedged-confidence example is "if Close is not
	// idempotent, this double-frees", and "double" is a cross-batch-replay
	// keyword. Varying one axis at a time would have found it too; the cross
	// product is cheap and does not have to argue that it would.
	yes, no := true, false
	for _, verbosity := range []config.Verbosity{config.VerbosityTerse, config.VerbosityNormal, config.VerbosityDetailed} {
		for _, politeness := range []config.Politeness{config.PolitenessBlunt, config.PolitenessNeutral, config.PolitenessWarm} {
			for _, confidence := range []config.Confidence{config.ConfidenceDirect, config.ConfidenceHedged} {
				for _, address := range []config.Address{config.AddressImpersonal, config.AddressAuthor} {
					for _, praise := range []*bool{&yes, &no, nil} {
						p := config.DefaultPersona()
						p.Verbosity, p.Politeness = verbosity, politeness
						p.Confidence, p.Address, p.Praise = confidence, address, praise
						p = p.Resolve()

						out["persona"] += "\n" + prompt.Persona(p)
						out["stylepass"] += "\n" + prompt.StylePass(p)
					}
				}
			}
		}
	}

	return out
}

// asRendered reduces prompt text to what a reader of it reads: every
// run of whitespace collapsed to one space, and markdown's emphasis markers
// dropped.
//
// The note behind it is in docs/measurement.md#asrendered.
func asRendered(s string) string {
	s = strings.Map(func(r rune) rune {
		switch r {
		case '*', '_', '`':
			return -1
		}
		return r
	}, s)

	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

// TestThePromptScanSeesThroughEmphasis pins the normalization the scan depends
// on, at the exact granularity a maintainer's edit changes.
//
// It is here because the scan's other half, "is this keyword in this text",
// is one strings.Contains and cannot be got wrong; every miss this file has had
// was a normalization gap, first the line wrap and then the emphasis markers.
//
// EVERY CASE BELOW FAILS AGAINST A PREDECESSOR OF asRendered, checked one at a
// time rather than assumed. The wrap case fails a byte-exact scan and passes the
// whitespace-collapsing one, which is what that predecessor was added for; the
// four marker cases fail BOTH, because collapsing runs of whitespace does
// nothing to an asterisk. Measured byteExact=false, wsCollapse=false,
// asRendered=true on all four. (The sentence here used to read "pass a
// byte-exact scan and the whitespace-collapsing one and fail both", which
// contradicts itself and is wrong either way it is read.) A marker wrapping a WHOLE keyword,
// "`data loss`", is not among them on purpose: it is found by every version
// of this function, so it would sit here proving nothing.
func TestThePromptScanSeesThroughEmphasis(t *testing.T) {
	for _, c := range []struct{ keyword, prompt string }{
		{"package-level state", "introduces package-level **state** that outlives a call"},
		{"accepted input", "widening an exported type's accepted _input_"},
		{"for one helper", "adding a dependency for\n  one helper function"},
		{"data loss", "a `data` loss on a reachable path"},
		{"every row", "a backfill meant to touch **every**\n  **row**"},
	} {
		if !strings.Contains(asRendered(c.prompt), asRendered(c.keyword)) {
			t.Errorf("asRendered does not see %q in %q.\n"+
				"A reader of the prompt does, so the scan in this file is measuring the markup "+
				"instead of the prose. Widen asRendered rather than deleting the case",
				c.keyword, c.prompt)
		}
	}

	// The transform must not invent a match either: a guard that fires on
	// ordinary correct text is deleted by the first maintainer it lies to.
	// Both cases below separate DROPPING a marker from replacing it with a
	// space, which is the plausible wrong version of this function, under
	// replacement "page_size" reads as "page size" and this test goes red on a
	// prompt that never said it.
	if got := asRendered("**word**s"); got != "words" {
		t.Errorf("asRendered(%q) = %q, want %q: an emphasis marker is dropped, not spaced",
			"**word**s", got, "words")
	}

	const underscored = "the default page_size moved"
	if strings.Contains(asRendered(underscored), asRendered("page size")) {
		t.Errorf("asRendered invents a match for %q in %q", "page size", underscored)
	}
}

// promptKeywordException is one keyword that IS in the shipped prompt and stays
// there.
//
// Every entry is a collision this workflow decided not to close, with the
// reason written where the next editor finds it. The shape of the decision is
// the same each time: a prompt ILLUSTRATION is replaceable, so an illustration
// that names a plant gets replaced; a DEFINITION, a scope list, or an ordinary
// English word is the right word for its job, and changing it to dodge a
// substring would be tuning the prompt to this corpus, which is the thing the
// prompt must never be tuned to, and which would also teach a real reviewer
// nothing.
//
// The unclosed direction is recorded on each entry because it is not uniform.
// An inflating leak pays recall to a finding that noticed nothing; a
// suppressing one costs recall on a plant. This file cannot tell them apart and
// does not try.
type promptKeywordException struct {
	fixture string
	keyword string

	// sources is where the keyword appears, by shippedPromptTexts label. Every
	// label listed must still contain it, so an entry cannot rot into a claim
	// about text nobody kept.
	//
	// Whether an UNLISTED occurrence also fails depends on ordinary.
	sources []string

	// ordinary marks a keyword ordinary English supplies, where the list above
	// is a floor rather than the whole truth.
	//
	// THIS FIELD IS A REPAIR OF A MEASURED FALSE POSITIVE, and it is the kind
	// that gets a guard deleted rather than fixed. With sources exact
	// everywhere, three edits that add no keyword at all broke the build: a
	// sentence added to the pedantic scope so that all four levels say "an
	// empty findings list is a common and correct outcome" (o-UTC-ome, a
	// timezone-boundary keyword); "Check the margin again before you settle on
	// a line." in review.md's Anchoring section ("again"); and, from a
	// concurrent session that had not read this file, "A finding about data
	// loss outranks one about style." in triage.md. All three are improvements
	// to the prompt. None of them is news about the corpus: a bare adverb
	// reaching a fourth file tells a reader nothing they did not know from the
	// first three, and the build break trains the next editor to delete the
	// entry, or worse, to reword correct English to dodge a substring, which
	// is precisely the tuning this file exists to prevent.
	//
	// It stays EXACT for multi-word phrases like "existing caller", where a new
	// occurrence really is a new argument somebody has to make.
	//
	// ARGUED PER ENTRY RATHER THAN COMPUTED. The tempting metric, a keyword
	// that appears in N of the 14 expert prompts is ordinary by construction,
	// was measured and rejected: it scores "race", "injection", "secret" and
	// "sanitiz" as ordinary, and those are exactly the keywords a concurrency or
	// security plant SHOULD use, so the rule would end by demanding good
	// keywords be deleted. It is also circular the moment the same corpus is
	// used to decide what counts as ordinary and to decide what to scan.
	//
	// What this gives up, stated rather than smoothed: for an ordinary keyword
	// this test no longer reports that the leak reached one more surface. The
	// timezone entry's own note. That "utc" is absent from exactly one scope
	// branch, so the nitpick level is quietly a scoring knob, is the kind of
	// observation that is now made by hand or not at all.
	ordinary bool

	why string
}

// allowedPromptKeywords is the disclosed residual, in full.
//
// A stale entry fails as loudly as a missing one, see checkPromptExceptions,
// so this list can only shrink by deletion, never rot into a green claim about
// text nobody kept.
func allowedPromptKeywords() []promptKeywordException {
	return []promptKeywordException{{
		fixture:  "data-loss-migration",
		keyword:  "data loss",
		ordinary: true,
		sources:  []string{"persona", "review.md", "scope:minimal", "scope:normal", "scope:off", "scope:pedantic"},
		why: "\"data loss\" is the honest name of the class, in the `critical` DEFINITION and in the " +
			"scope list every level opens with. It is not an illustration and there is nothing to " +
			"replace it with: a prompt that will not say \"data loss\" cannot tell a reviewer that " +
			"losing data is critical. Direction: inflates. A finding anchored within anchorTolerance " +
			"of migrations/0007_backfill_plan.sql:8 that says \"data loss\" and nothing else is " +
			"credited with the missing WHERE, and the file is short enough that the anchor is cheap. " +
			"The closeable side is the keyword, which this workflow may not touch.",
	}, {
		fixture:  "timezone-boundary",
		keyword:  "utc",
		ordinary: true,
		sources:  []string{"persona", "review.md", "scope:minimal", "scope:normal", "scope:off", "validation-contract"},
		why: "a substring accident, not a semantic leak: \"utc\" sits inside the ordinary English word " +
			"o-UTC-ome, in \"an empty findings list is a common and correct outcome\" and its siblings. " +
			"Direction: inflates, and cheaply — any sentence containing \"outcome\" near report.go:13 " +
			"is credited with the timezone plant. Rewording correct English to dodge a three-letter " +
			"substring would teach a reviewer nothing, which is the test every prompt edit here has " +
			"to pass, so the fix belongs on the keyword list. Note the missing source: the pedantic " +
			"branch is the one scope that never says \"outcome\", so this leak's presence varies by " +
			"nitpick level and the level knob is quietly a scoring knob. That last observation was " +
			"true when it was written and is no longer asserted — `ordinary` makes sources a floor, " +
			"and the field's own comment records that this is what it gives up.",
	}, {
		fixture: "ruby-default-page-size",
		keyword: "existing caller",
		sources: []string{"scope:minimal"},
		why: "the minimal level's scope is \"defects in <core>, plus changes that break an existing " +
			"caller or published contract\", which is the plainest statement of what that level adds. " +
			"Direction: inflates, and LATENT — config.GenerationLevel is pinned to normal, so this " +
			"branch renders in no review today. It goes live the day that constant moves, which is " +
			"exactly why every level is scanned rather than only the one that ships.",
	}, {
		fixture:  "go-hardcoded-secret",
		keyword:  "secret",
		ordinary: true,
		sources:  []string{"review.md"},
		why: "the `critical` illustration is \"Writing a decrypted secret to a log that ships " +
			"off-host\", and that SCENARIO is not this plant — the plant is a literal key committed " +
			"to source. One shared domain word is left because the illustration is a good one and " +
			"the false-credit path is nearly empty: a finding anchored at client.go:12 that uses the " +
			"word \"secret\" has almost certainly noticed the credential on that line. Direction: " +
			"inflates in principle, unmeasured, and judged the weakest of the collisions here.",
	}, {
		fixture:  "go-sql-injection",
		keyword:  "placeholder",
		ordinary: true,
		sources:  []string{"review.md"},
		why: "the Suggestions section says a suggestion must be \"exact code, no placeholders\", which " +
			"is the ordinary sense of the word and not the SQL one. Direction: inflates, with low " +
			"reachability — it needs a finding at store.go:17 whose text says \"placeholder\" while " +
			"missing the interpolation, and a reviewer that reaches for that word about a SQL " +
			"statement has usually found the bug.",
	}, {
		fixture:  "csharp-client-per-request",
		keyword:  "exhaust",
		ordinary: true,
		sources:  []string{"stylepass"},
		why: "\"exhaust\" is a substring of \"this pass exists to be useful rather than exhaustive\". " +
			"Direction: inflates, and LATENT twice over — StylePass renders only when " +
			"NitpickLevel.NeedsStylePass() is true, which is pedantic only, and no eval path sets " +
			"pedantic. It would fire the day a style pass joins the battery.",
	}, {
		fixture:  "cross-batch-replay",
		keyword:  "double",
		ordinary: true,
		sources:  []string{"persona", "stylepass"},
		why: "the hedged-confidence example is \"if Close is not idempotent, this double-frees\", a " +
			"concrete illustration of naming the assumption a claim rests on. The plant's keyword is " +
			"the bare word \"double\". Direction: inflates, and NOT SCORED — cross-batch-replay is " +
			"reachable from neither Fixtures() nor HeldOutFixtures(). It is listed because its own " +
			"file says it is queued for a scored corpus, and this is one of the three things that " +
			"have to be settled first.",
	}, {
		fixture:  "cross-batch-replay",
		keyword:  "duplicate",
		ordinary: true,
		sources:  []string{"stylepass"},
		why: "triage.md's job IS deduplication (\"the raw list contains duplicates and " +
			"disagreements\") and StylePass tells the style reviewer not to duplicate the defect " +
			"pass's work. Neither can stop using the word. Direction: inflates; not scored today, " +
			"same queue as the entry above.",
	}, {
		fixture:  "cross-batch-replay",
		keyword:  "again",
		ordinary: true,
		sources:  []string{"triage.md"},
		why: "\"again\" is a bare English adverb on the plant's keyword list, and triage.md contains " +
			"it in \"Re-rank against the whole change\". A one-word keyword that ordinary prose " +
			"supplies is the fixture's own problem; direction: inflates; not scored today.",
	}}
}

// collisionAdvice orders the two available fixes by which one is more likely to
// be right for this keyword.
//
// "Fix the PROMPT, not the fixture" used to lead unconditionally, and for a
// one-word keyword that is the wrong instruction. Measured on this corpus: a
// definitional expansion of persona.go's scope list, "resource handling (a
// file descriptor, a connection, memory)", fires ten times on `memory` and
// `file descriptor`, and a one-word precision swap in review.md, "a path that
// loses an error" to "discards an error", fires on `discard`. Rewording correct
// English to dodge a bare noun teaches a reviewer nothing, which is the test
// every prompt edit here has to pass; and Defect.Keywords' own rule already
// says a keyword must not be a token a reviewer would type merely by quoting
// the change, of which typing it from ordinary English is the stronger case.
//
// The split is by word count because that is the part that can be computed.
// `memory` and `discard` are decided correctly by it; `file descriptor` and
// `already covered` are two-word phrases that ordinary prose also supplies, and
// for those this only names both doors rather than choosing.
func collisionAdvice(keyword string) string {
	const promptDoor = "If the prompt text is an illustration, replace it with a specific scenario that " +
		"teaches the same level and names nothing planted. If it is a definition or an ordinary " +
		"English word that cannot honestly be changed, add a promptKeywordException with its " +
		"direction and say why."

	const fixtureDoor = "Before touching the prompt, read the keyword: internal/evals/fixtures.go's rule " +
		"is that a keyword must not be a token a reviewer would type merely by QUOTING the change, " +
		"and a bare word ordinary English supplies fails a stronger form of it. If the prompt " +
		"sentence is right, the defect is the KEYWORD — note that changing one re-scores every " +
		"recorded Incumbent review under internal/evals/testdata, so it is a change that owns that " +
		"evidence."

	if strings.Contains(strings.TrimSpace(keyword), " ") {
		return "Two doors, and this keyword is a phrase rather than a bare word, so the prompt is the " +
			"likelier one:\n  PROMPT: " + promptDoor + "\n  FIXTURE: " + fixtureDoor
	}

	return "Two doors, and this keyword is ONE WORD, so start with the fixture:\n  FIXTURE: " +
		fixtureDoor + "\n  PROMPT: " + promptDoor
}

// TestNoPlantedKeywordAppearsInTheShippedPrompt is the mechanical half.
//
// A keyword in the prompt is a phrase the reviewer can produce by quoting its
// own instructions, and matches() will credit the plant for it without the
// reviewer having read the code, so this is the same defect Defect.Keywords
// warns about ("a keyword must not be a token a reviewer would type merely by
// QUOTING the change") with the change replaced by the prompt.
//
// dedupFixtures() is scanned alongside AllFixtures() even though nothing scores
// it, because its own file says it is queued for a scored corpus and the three
// collisions it already has are cheaper to settle now than after the wiring.
func TestNoPlantedKeywordAppearsInTheShippedPrompt(t *testing.T) {
	texts := shippedPromptTexts(t)

	renderedTexts := map[string]string{}
	for label, text := range texts {
		renderedTexts[label] = asRendered(text)
	}

	allowed := map[string]promptKeywordException{}
	for _, e := range allowedPromptKeywords() {
		allowed[e.fixture+"\x00"+strings.ToLower(e.keyword)] = e
	}

	found := map[string][]string{} // fixture\x00keyword -> sorted source labels

	// EveryFixture rather than AllFixtures: the info corpus is where the
	// review prompt's restated bar was measured, and a prompt that names one
	// of its keywords would be scored for quoting itself there too.
	corpus := append(append([]Fixture{}, EveryFixture()...), dedupFixtures()...)
	for _, f := range corpus {
		for _, d := range f.Defects {
			for _, kw := range d.Keywords {
				key := f.Name + "\x00" + strings.ToLower(kw)
				for label, text := range renderedTexts {
					if !strings.Contains(text, asRendered(kw)) {
						continue
					}
					found[key] = append(found[key], label)

					if _, ok := allowed[key]; ok {
						continue
					}
					t.Errorf("the shipped prompt (%s) contains %q, a keyword of %s (%s:%d).\n"+
						"A reviewer can now earn full credit for that plant by quoting the prompt back, "+
						"having read nothing: matches() credits a finding within anchorTolerance of the "+
						"plant whose title, rationale or category contains the keyword as a substring.\n"+
						"%s",
						label, kw, f.Name, d.Path, d.Line, collisionAdvice(kw))
				}
			}
		}
	}

	checkPromptExceptions(t, allowed, found)

	t.Logf("prompt/corpus collisions left open:\n%s", promptCollisionSummary())
}

// checkPromptExceptions runs the staleness half: an exception that no longer
// describes the tree is deleted rather than carried.
//
// Without it the list would only ever grow, and a residual nobody can reproduce
// reads to the next editor as a reason not to look.
//
// Every listed source is checked in both kinds of entry. That is the half that
// stops rot. Only the UNLISTED direction differs, and only for keywords marked
// ordinary; see promptKeywordException.ordinary for the false positives that
// bought that distinction.
func checkPromptExceptions(t *testing.T, allowed map[string]promptKeywordException, found map[string][]string) {
	t.Helper()

	for key, e := range allowed {
		got := found[key]
		if len(got) == 0 {
			t.Errorf("promptKeywordException %s/%q no longer collides with anything in the shipped "+
				"prompt. Delete the entry: a disclosed residual that cannot be reproduced is worse "+
				"than none, because the next editor trusts it", e.fixture, e.keyword)
			continue
		}

		sort.Strings(got)
		want := append([]string(nil), e.sources...)
		sort.Strings(want)

		if missing := missingFrom(want, got); len(missing) > 0 {
			t.Errorf("promptKeywordException %s/%q records sources %v and the keyword is no longer "+
				"in %v. Shorten the list: each source named there is a claim about text that still "+
				"ships.", e.fixture, e.keyword, want, missing)
		}

		if extra := missingFrom(got, want); len(extra) > 0 && !e.ordinary {
			t.Errorf("promptKeywordException %s/%q records sources %v and the keyword has also "+
				"reached %v.\n"+
				"That occurrence has not been argued for — argue it by extending sources, or reword "+
				"it. If this keyword is one ordinary English supplies anywhere, the honest answer is "+
				"the `ordinary` field rather than a longer list.",
				e.fixture, e.keyword, want, extra)
		}
	}
}

// missingFrom returns the members of want that are absent from got.
func missingFrom(want, got []string) []string {
	have := map[string]bool{}
	for _, g := range got {
		have[g] = true
	}

	var out []string
	for _, w := range want {
		if !have[w] {
			out = append(out, w)
		}
	}

	return out
}

// ladderIllustrations is the pinned text of review.md's five severity examples.
//
// IT IS A TRIPWIRE AND NOTHING MORE. It cannot tell whether a new example names
// something the corpus plants, and it cannot tell whether "you are unlikely to
// meet this in this change" is true of one, both are judgements about meaning,
// and the second is not about the corpus at all. What it does is make an edit
// to these sentences impossible to land silently: the build goes red, and a
// human has to re-affirm the questions in the failure message. It buys
// attention at the moment of the edit, which is the only moment the answer is
// cheap.
//
// THE MECHANICAL HALF WOULD HAVE CAUGHT THE ORIGINAL BUG AND THIS ONE IS STILL
// NEEDED, which is a narrower claim than the one this comment used to make.
// Restoring the pre-fix `info` rung and running
// TestNoPlantedKeywordAppearsInTheShippedPrompt reports "accepted input" and
// "for one helper", both literal keywords, so the scan was not blind to it;
// there was no scan. What no scan reaches is an illustration that
// describes a plant in words the fixture does not list, and question 2, which
// is a judgement about ordinary reviewing and would matter with no corpus in
// the repository at all.
//
// Pinned the way soleCreditors() pins keywords, and for the same reason: the
// property cannot be computed, so the list IS the assertion.
// The pinned text is each rung's WHOLE bullet, not its illustration alone. See
// ladderBullets for the two edits that landed green while only the italic was
// pinned, both of them the collision this guard exists to catch.
var ladderIllustrations = map[string]ladderIllustration{
	"critical": {class: config.ClassSecurity, text: "data loss, a security breach, or a " +
		"guaranteed production failure. *Writing a decrypted secret to a log that ships " +
		"off-host is critical. A migration that drops a column before the code that reads it " +
		"is retired is critical.*"},
	"error": {class: config.ClassCorrectness, text: "a real bug that produces incorrect " +
		"behavior on a reachable path. *Rounding a currency amount at each line rather than " +
		"once on the total is an error. A cache key that omits a field the value depends on " +
		"is an error.*"},
	"warning": {class: config.ClassConcurrency, text: "likely a bug, or a genuine hazard under " +
		"plausible conditions. *A check-then-act on a file that another process can replace " +
		"between the two steps is a warning. Retrying a non-idempotent request is a warning.*"},
	// BOTH ILLUSTRATIONS HERE STATED A WRONG ANSWER AS A FACT, which is the one
	// thing an `info` example may not do. They read "…a log line that two
	// services ARE correlated through" and "…so a rise in failures READS AS a
	// fall in traffic": the first asserts a live consumer and then rates
	// breaking it info, and the second asserts the dashboard misreports during
	// an outage. Under this package's own authoring rule, fixtures_info.go, "it
	// is discovered by asking whether the AUTHOR COULD BE WRONG. If the author
	// could be wrong, the finding is at least a warning", the author of the
	// second one is wrong, so it was illustrating `info` with a warning. The
	// second was also structurally the `error` rung's own example two lines up:
	// an artifact omitting a dimension it depends on and therefore answering
	// wrongly. Both now name a cost that falls on a later reader and assert no
	// wrong output, which is what leaves the author free to accept or reject.
	"info": {class: config.ClassMaintainability, text: "a defensible concern the author should " +
		"consciously accept or reject. *Dropping the request id from a log line, so " +
		"joining it to the rest of one request's output later has one less key, is info. " +
		"Counting a metric only on the success path, so anyone reading it later has to know " +
		"that is what it counts, is info.*"},
	"nit": {class: config.ClassTests, text: "minor and optional. *A test that asserts on an " +
		"error's exact wording rather than its type is a nit.*"},
}

// ladderIllustration is one pinned severity example and the class a finding of
// that shape would carry.
//
// THE CLASS IS A HUMAN JUDGEMENT, NOT A MEASUREMENT, no model was asked. It is
// pinned because of what it is checked against: config.GenerationLevel.
// Publishes. review.md's ladder is rendered into the STYLE pass as well as the
// defect pass (internal/review/engine.go's analyzeStyle builds prompt.NameReview
// with StylePass as its persona layer), and an illustration whose honest class
// is `style` lands in three contradictions at once. The style pass is told to
// report ONLY naming, documentation, structure, idiom and consistency, told by
// the ladder that this one is `info`, told by StylePass to mark every finding
// `nit`, and told three lines below the ladder not to go looking for it. At the
// default level it is worse than contradictory: allowedClasses drops `style`
// below pedantic, so the same finding is filtered away before a reader sees it.
//
// The rung that forced this pin illustrated `info` with a subcommand configured
// unlike its siblings, a consistency observation, the one class the ladder
// must not illustrate with, and the only one of the nine illustrations that
// named a difference and no cost.
type ladderIllustration struct {
	class config.Class
	text  string
}

// ladderBulletStart matches the opening of one severity bullet.
//
// Anchored to the start of a line, so a rung's own wrapped continuation lines
// cannot be mistaken for the next rung.
var ladderBulletStart = regexp.MustCompile("(?m)^- `(critical|error|warning|info|nit)` — ")

// ladderBullets returns each rung's ENTIRE text: its definition half, its
// illustrations, and anything written after them.
//
// PINNING ONLY THE ITALIC LET THE ORIGINAL BUG BACK IN. What was here matched
// "- `level`, [^*]*\*([^*]+)\*" and pinned the captured italic. `[^*]*` cannot
// cross an asterisk, so everything before the first italic, the whole
// definition half, was skipped without being compared, and nothing looked
// after the italic at all. Both halves of that were measured green against the
// previous guard:
//
//	appended after the italic:
//	  - `info`, … is info.* Loosening what an exported function will take is
//	    info too, as is pulling in a package for one call.
//	written into the definition half:
//	  - `info`, a defensible concern …, such as loosening an exported signature
//	    or taking on a package for one call.
//
// Each of those is the bug this whole guard was built for, the prompt naming a
// defect the corpus plants, reintroduced with the tripwire silent. The second
// is not hypothetical: the other real collision this work found, in
// experts/api.md, was written in exactly that plain-prose position.
//
// Taking the whole bullet also subsumes what the italic count was added for, and
// the count is kept anyway: it still covers an italic added to the window
// OUTSIDE any bullet, which no per-bullet comparison sees.
func ladderBullets(window string) map[string]string {
	starts := ladderBulletStart.FindAllStringSubmatchIndex(window, -1)

	out := make(map[string]string, len(starts))
	for i, m := range starts {
		end := len(window)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		// Fields collapses the line wrapping, so re-flowing a paragraph is not
		// an edit. Changing a word is.
		out[window[m[2]:m[3]]] = strings.Join(strings.Fields(window[m[1]:end]), " ")
	}
	return out
}

// italicSpans returns every italicised phrase in text, stepping over `**bold**`.
//
// IT IS HAND-WRITTEN BECAUSE THE REGEX VERSION WAS A FALSE POSITIVE, and a
// tripwire that fires on a correct edit is deleted by the second person it lies
// to. `\*([^*]+)\*` scanned over one bolded word in the severity section's
// intro line pairs the bold's trailing marker with the NEXT illustration's
// opening one; every span after it shifts by one and the count came out 4
// instead of 5. Measured: adding `**` around one word in "Assign severity by
// what you can demonstrate" made this test fail before this rewrite and leaves
// it green after. review.md already uses `**` in six places, two of them inside
// the severity section, so that edit is ordinary rather than adversarial.
//
// Bold INSIDE an illustration is not silently absorbed, the markers stay in
// the captured text, so the pin fires and a human reads the message. That is
// the right side to err on: an emphasis marker inside a pinned sentence is an
// edit to the sentence.
func italicSpans(text string) []string {
	var out []string

	for i := 0; i < len(text); {
		if text[i] != '*' {
			i++
			continue
		}

		// A bold run opens here, not an italic. Step over it whole.
		if strings.HasPrefix(text[i:], "**") {
			if j := strings.Index(text[i+2:], "**"); j >= 0 {
				i += 2 + j + 2
				continue
			}
			i += 2
			continue
		}

		// Find this span's closing marker, stepping over any bold inside it.
		closer := -1
		for j := i + 1; j < len(text); {
			if text[j] != '*' {
				j++
				continue
			}
			if strings.HasPrefix(text[j:], "**") {
				j += 2
				continue
			}
			closer = j
			break
		}

		if closer < 0 {
			break
		}

		out = append(out, strings.Join(strings.Fields(text[i+1:closer]), " "))
		i = closer + 1
	}

	return out
}

// TestTheSeverityLadderIllustrationsArePinned fails whenever review.md's
// severity examples change, and says what the editor has to check.
//
// Three of the five rungs have already been through this: `info` named
// kotlin-widened-input and rust-crate-for-one-call and measured 0 of 3 twice,
// `error` named timezone-boundary's class with its keyword `timezone` in the
// sentence, and `nit` named the class of three copy plants and was the stated
// basis of five nit SeverityNotes.
//
// THREE RUNGS STILL CARRY A DISCLOSED RESIDUAL, and they are here so the next
// editor reads them before touching this file.
//
//   - `critical` keeps "a decrypted secret", one shared word with
//     go-hardcoded-secret whose scenario differs; allowedPromptKeywords records
//     it. Direction: inflates.
//   - `warning` keeps "Retrying a non-idempotent request", which IS
//     cross-batch-replay's plant word for word. That fixture is in neither
//     scored corpus today, and its own file says wiring it in needs work this
//     rung has to be part of. Direction: inflates, unscored today.
//   - `nit` fails question 2 by the same argument that removed the example
//     before it. "A test that asserts on an error's exact wording rather than
//     its type" is an ordinary review finding, and ordinariness was the stated
//     reason "an unnecessary intermediate copy" had to go. Direction:
//     SUPPRESSES, for a real user it is one common, legitimate finding placed
//     under "do not go looking for them". For this corpus the effect is
//     unmeasured and probably nil: the only `tests`-class plant is
//     duplicate-test-case-nit, a repeated case rather than an assertion on
//     wording. It is left rather than replaced because every candidate for that
//     rung has the same problem. A nit that is NOT ordinary is not a nit, and
//     because moving it again without a measurement would be churn. What would
//     close it is the held-out battery this build already owes, not a better
//     sentence.
//
// The `info` rung's first example is a fourth, weaker one: "Dropping the
// request id from a log line that two services are correlated through" asserts
// an existing consumer and then rates removing what it reads as info, where
// this corpus's contract-break, a field an existing consumer reads, silently
// gone, is `error`. The distinction is real (a log consumer is an operator,
// not code returning a wrong answer) and the sentence does not spell it out.
// Unmeasured; direction, if any, is toward under-rating a genuine contract
// break.
func TestTheSeverityLadderIllustrationsArePinned(t *testing.T) {
	p, err := prompt.Build(prompt.NameReview, prompt.Options{})
	if err != nil {
		t.Fatalf("building the review prompt: %v", err)
	}

	// The window ends at the anti-anchoring sentence rather than at the section
	// break, because "these examples" is what that sentence governs. An italic
	// added BELOW it, in the calibration rules, is therefore unpinned, which is
	// a real gap and a small one: text outside the sentence's reach is not an
	// example the reviewer is told not to go looking for. Widening the window
	// would pin the calibration prose too, and an editor emphasising a word there
	// with `*` rather than `**` would go red for it.
	text := p.String()
	start := strings.Index(text, "## Severity")
	end := strings.Index(text, "These examples are illustrative")
	if start < 0 || end < start {
		t.Fatalf("review.md no longer has a `## Severity` section followed by the anti-anchoring " +
			"sentence, so this guard is reading the wrong text. Re-point it before editing the ladder")
	}

	got := ladderBullets(text[start:end])

	// Counted as well as compared, because comparing bullets cannot see an
	// italic that belongs to no bullet, one added to the section's intro line,
	// or below the last rung but above the anti-anchoring sentence. Those
	// positions are inside what "these examples" governs, so an illustration
	// written there is exactly as unre-affirmed as one inside a rung.
	if spans := italicSpans(text[start:end]); len(spans) != len(ladderIllustrations) {
		t.Errorf("review.md's severity section has %d italicised examples and %d are pinned:\n  %s\n\n"+
			"Every illustration in the ladder has to be pinned, including one APPENDED to a rung "+
			"that already has one. Answer the questions below for the new text, then pin it",
			len(spans), len(ladderIllustrations), strings.Join(spans, "\n  "))
	}

	for level, pinned := range ladderIllustrations {
		// An illustration whose honest class the shipping level filters away
		// teaches a level the reader can never reach. `style` is the only class
		// allowedClasses drops at config.GenerationLevel, and it is the one an
		// illustration falls into by accident: "configured unlike its siblings"
		// is a consistency observation, and consistency is style.
		if !config.GenerationLevel.Publishes(pinned.class) {
			t.Errorf("the `%s` illustration is pinned as class %q, which %q does not publish.\n"+
				"A finding of that shape is dropped by review.Filter before a reader sees it, so the "+
				"rung illustrates a level with something the default configuration cannot report. "+
				"Note the one legitimate case: the `nit` rung reaches readers through the separate "+
				"style pass, where class is forced to style anyway — if that is what changed, record "+
				"it here rather than re-wording the example.",
				level, pinned.class, config.GenerationLevel)
		}

		want := strings.Join(strings.Fields(pinned.text), " ")
		switch have, ok := got[level]; {
		case !ok:
			t.Errorf("the `%s` rung of review.md's severity ladder no longer carries an italic "+
				"example, or no longer matches ladderBulletStart. If the rung was deliberately left "+
				"unillustrated, drop it from ladderIllustrations and say why in this file", level)
		case have != want:
			t.Errorf("review.md's `%s` example changed.\n  was: %s\n  now: %s\n\n"+
				"This test decides nothing — it fires on ANY edit here, because the failure it "+
				"guards against is semantic and no scan in this package can see it. Two questions "+
				"have to be answered by hand before you update ladderIllustrations:\n"+
				"  1. Does the new example name anything this corpus plants — its scenario, its "+
				"class, or a phrase close enough that a reviewer echoing the prompt would be "+
				"credited? Walk the plants in AllFixtures(); the mechanical half of that check is "+
				"TestNoPlantedKeywordAppearsInTheShippedPrompt, and it only sees literal keywords.\n"+
				"  2. Is \"you are unlikely to meet this in this change\" TRUE of it for a real "+
				"reviewer of a real repository? Three lines below the ladder the prompt says the "+
				"examples are drawn from classes the reviewer is unlikely to meet and tells it not "+
				"to go looking. An example naming something ORDINARY turns that sentence into an "+
				"instruction to ignore a common, legitimate finding — which is the user-facing "+
				"defect this whole file exists to stop, corpus or no corpus.\n"+
				"  3. Ask question 2 again for the STYLE reviewer. internal/review/engine.go's "+
				"analyzeStyle builds the whole of review.md — this ladder and the sentence below "+
				"it — with StylePass as the persona layer, so an example in naming, documentation, "+
				"structure, idiom or consistency lands under \"do not go looking for them\" in the "+
				"one pass that exists to find exactly those, and under StylePass's \"severity nit "+
				"on every finding\", which contradicts the rung it illustrates. The class pinned "+
				"beside the text is the mechanical half of this question; it does not answer it.\n"+
				"  4. Then grep internal/evals for the sentence you removed. Comments in this "+
				"package QUOTE these illustrations to argue what a level is — fixtures_info.go and "+
				"fixtures_warning.go have each carried a quotation of a sentence that had already "+
				"been deleted from review.md. A comment keyed to prompt prose goes stale every time "+
				"the prompt is edited, and this failure is the moment it is cheap to notice.",
				level, want, have)
		}
	}

	for level := range got {
		if _, ok := ladderIllustrations[level]; !ok {
			t.Errorf("review.md's severity ladder gained an illustrated `%s` rung that "+
				"ladderIllustrations does not pin; add it, having answered the two questions above", level)
		}
	}

	if len(got) != len(ladderIllustrations) {
		t.Errorf("matched %d illustrated rungs and %d are pinned", len(got), len(ladderIllustrations))
	}
}

// TestTheLadderStillCarriesItsAntiAnchoringSentence keeps the tripwire's second
// question meaningful.
//
// The sentence below is what makes an illustration that names a plant harmful
// rather than merely redundant, and deleting it would turn every failure
// message above into advice about a rule that no longer exists. It is also the
// thing a future editor is most likely to reach for when an illustration is
// found to name a plant, and removing it, rather than the example, would
// inflate this corpus by exactly the mechanism it guards against.
func TestTheLadderStillCarriesItsAntiAnchoringSentence(t *testing.T) {
	p, err := prompt.Build(prompt.NameReview, prompt.Options{})
	if err != nil {
		t.Fatalf("building the review prompt: %v", err)
	}

	const want = "These examples are illustrative, not a checklist. They are deliberately drawn from " +
		"defect classes you are unlikely to meet in this change; do not go looking for them."

	if !strings.Contains(asRendered(p.String()), asRendered(want)) {
		t.Errorf("review.md no longer says %q.\n"+
			"That sentence is why an illustration naming a planted defect suppresses it, and why "+
			"TestTheSeverityLadderIllustrationsArePinned asks whether a new example is ORDINARY. "+
			"If it was deleted to make room for an example that names something common, the example "+
			"is the thing to change", want)
	}
}

// TestCreditIsAFunctionOfTheReviewersVocabulary is the disclosure that no scan
// in this file can be, written as a run rather than an argument.
//
// THE LIMIT IS THE TECHNIQUE'S, NOT THIS TEST'S. Credit is a substring test, so
// the score is a function of the nouns the reviewer reaches for. The two
// findings below say the same true thing about the same line and only one is
// credited. A prompt sentence can therefore raise or lower the odds of a noun,
// "ask who is allowed to do this" against "ask what the caller owns", and move
// the measured score while containing no keyword at all, which is the whole
// space TestNoPlantedKeywordAppearsInTheShippedPrompt cannot see.
//
// DIRECTION: BOTH, and that is why it stays open. Vocabulary that matches the
// keyword list inflates; vocabulary that avoids it suppresses, silently, by
// scoring a correct detection of a `critical` plant at zero. Nothing here
// distinguishes a sentence that teaches the level better from one that steers
// this corpus, and a tripwire over all of review.md would fire on every
// legitimate edit and be deleted inside a month, the same false-positive rule
// this file has already had to apply twice. What closes it is the pre-registered
// held-out battery this build already names as its open item: freeze the corpus,
// change one sentence, measure.
func TestCreditIsAFunctionOfTheReviewersVocabulary(t *testing.T) {
	var d Defect
	for _, f := range AllFixtures() {
		if f.Name == "removed-guard" {
			d = f.Defects[0]
		}
	}
	if d.Path == "" {
		t.Fatal("removed-guard is no longer in AllFixtures(), so this disclosure is reading nothing")
	}

	const rationale = "A request from an unrelated account reaches the delete path and succeeds."

	plain := review.Finding{
		Path: d.Path, Line: d.Line,
		Title:     "Nothing confirms the project belongs to the requester",
		Rationale: rationale,
	}
	canonical := plain
	canonical.Title = "The handler no longer checks ownership"

	if matches(plain, d) {
		t.Errorf("the plain wording is credited, so this disclosure no longer demonstrates "+
			"anything. Check it against %v and pick a wording that is correct and uses none of "+
			"them", d.Keywords)
	}
	if !matches(canonical, d) {
		t.Errorf("the canonical wording is NOT credited, so the pair no longer isolates "+
			"vocabulary: %q is supposed to be a keyword of removed-guard", "ownership")
	}
}

// promptCollisionSummary renders the residuals for a reader running this file
// directly, so the disclosure is reachable without reading the source.
func promptCollisionSummary() string {
	var b strings.Builder
	for _, e := range allowedPromptKeywords() {
		fmt.Fprintf(&b, "%s / %q in %s\n", e.fixture, e.keyword, strings.Join(e.sources, ", "))
	}
	return b.String()
}
