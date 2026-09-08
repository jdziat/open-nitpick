package evals

// Comments that assert behaviour, and the rule that they have to name a test.
//
// THE DEFECT this EXISTS TO STOP, which is this package's most productive one by
// a wide margin. Seven rounds of review, and the highest-yield move against this
// tree every single time has been to read a comment and then run it: a banded
// score whose comment claimed it could see a bug it could not; a vocabulary
// block whose preamble said its words were verbatim when they were
// translations; a cross-judge legend calling a difference between two stimuli a
// confidence interval; a wiring guard whose doc said nothing has to be added
// when the next set lands, while it matched exactly one function shape. In every
// case the CODE was close to right. The prose beside it claimed more than the
// code delivered, and prose is not executable, so nothing went red.
//
// The comments here are also this codebase's best asset, they carry the
// argument, the alternatives, the failure that was prevented, so the answer is
// not to write fewer of them. It is to separate the two things a comment can do.
// Prose that ARGUES gives the reader the reasoning and lets them check it. Prose
// that ASSERTS gives them a guarantee and nothing to check it with, and that is
// the shape that has to point at something the build runs.
//
// WHAT COUNTS AS AN ASSERTION HERE, and the definition is deliberately narrow:
// the opening clause of a sentence, containing one of the idioms in
// claimTriggers, whose subject is spelled like an identifier this package
// declares. A sentence that reaches a "because" or a "so" before it reaches the
// idiom is an argument and is left alone. A sentence about a reviewer, a reader
// or a model is about the world rather than about this code, and is left alone.
//
// This narrowness is a real recall cost, not a rhetorical hedge, and it is the
// failure mode that has bitten this package twice, a scan that quietly saw less
// than its comment claimed. So the exact shapes that are and are not seen are
// written into claimShapes as sources with expected verdicts, and
// TestTheClaimScanSeesExactlyTheShapesItClaimsTo runs the scan over every one of
// them. Widening or narrowing the rule flips a row there and fails the build,
// which forces the disclosure to be corrected in the same commit as the rule.
// The disclosure is executable; it is not a paragraph anyone has to keep true by
// hand.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// claimTrigger is one idiom that turns a sentence into a guarantee.
//
// The note behind it is in docs/harness-notes.md#claimtrigger.
type claimTrigger struct {
	name    string
	pattern *regexp.Regexp
	fires   string
	quiet   string
}

// claimTriggers are the assertion idioms, taken from the defects that shipped
// rather than invented.
//
// Two of them, "is not published" and "nothing has to be added", match nothing
// in the package today, because the comments that used them have been corrected.
// They stay because the guard's job is to catch the NEXT one, and a trigger is
// retired when the idiom stops being an assertion, not when the corpus stops
// containing it. That is why liveness is asserted against fires/quiet above and
// against the package as a whole below, and never per-trigger against the
// corpus: the latter would delete a guard as a reward for fixing what it caught.
var claimTriggers = []claimTrigger{
	{
		name:    "withheld credit",
		pattern: regexp.MustCompile(`(?i)\bis not credited\b`),
		fires:   "A finding at the widening is not credited with detecting the defect.",
		quiet:   "A finding at the widening is credited with detecting the defect.",
	},
	{
		name:    "withheld publication",
		pattern: regexp.MustCompile(`(?i)\bis not published\b`),
		fires:   "The second judge's number is not published beside the first.",
		quiet:   "The second judge's number is published beside the first.",
	},
	{
		name:    "denied capability",
		pattern: regexp.MustCompile(`(?i)\bcan ?not\b`),
		fires:   "The row cannot be ordered against its neighbours.",
		quiet:   "The row can be ordered against its neighbours.",
	},
	{
		name:    "denied occurrence",
		pattern: regexp.MustCompile(`(?i)\bnever\b`),
		fires:   "The tuning corpus never sees it.",
		quiet:   "The tuning corpus sees it once.",
	},
	{
		name:    "no maintenance owed",
		pattern: regexp.MustCompile(`(?i)\bnothing has to be added\b`),
		fires:   "Nothing has to be added when the next set lands.",
		quiet:   "A registration has to be added when the next set lands.",
	},
	{
		name:    "prohibition",
		pattern: regexp.MustCompile(`(?i)\bmust not\b`),
		fires:   "It must not be printed as a number.",
		quiet:   "It may be printed as a number.",
	},
	{
		name:    "unconditional occurrence",
		pattern: regexp.MustCompile(`(?i)\balways\b`),
		fires:   "The order is always the one the header declares.",
		quiet:   "The order is usually the one the header declares.",
	},
	{
		name:    "universal quantifier",
		pattern: regexp.MustCompile(`(?i)\bevery \w+ is\b`),
		fires:   "Every strategy is priced from the same table.",
		quiet:   "Most strategies are priced from the same table.",
	},
	{
		name:    "universal denial",
		pattern: regexp.MustCompile(`(?i)\bno \w+ can\b`),
		fires:   "No caller can misread the pair.",
		quiet:   "A careless caller can misread the pair.",
	},
}

// claimReason opens a clause that ARGUES rather than asserts.
//
// The note behind it is in docs/harness-notes.md#claimreason.
var claimReason = regexp.MustCompile(`(?i)(\bbecause\b|\bso\b|\bsince\b|\bwhich\b|\bwhere\b|\bwhen\b|\bwhile\b|\bunless\b|\bif\b|\bbut\b|\band\b|\bor\b|\brather than\b|,|;|:|\(|—)`)

// claimHistory marks a sentence as a record of what HAPPENED.
//
// The note behind it is in docs/harness-notes.md#claimhistory.
var claimHistory = regexp.MustCompile(`(?i)\b(was|were|had|have been|has been|used to|did|would|could|might|previously|originally|historically|no longer|until|before|once)\b`)

// claimTestRef is how a comment cites a test. Go's own convention: the test's
// name, spelled exactly.
var claimTestRef = regexp.MustCompile(`\bTest[A-Z][A-Za-z0-9_]*`)

// claimIdent matches a word, or a dotted selector, as it appears in prose.
var claimIdent = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)?`)

// claimShape is one sentence the scan is required to reach a stated verdict on.
type claimShape struct {
	name     string
	sentence string
	// claim is what behaviouralClaim must answer. The false rows are the
	// coverage this scan does not have, stated where the build can check it:
	// widen the rule and a false row starts reporting a claim, and the test
	// fails until this table, the disclosure itself, is corrected.
	claim bool
}

// claimShapeNames is the identifier set the shape table is scanned against.
//
// A fixed set rather than the real package's, so that renaming a function
// elsewhere in this tree cannot silently turn a covered row into an uncovered
// one and take the guard's coverage with it.
var claimShapeNames = map[string]bool{
	"unknownCost":      true,
	"sameFinding":      true,
	"HeldOutFixtures":  true,
	"costStrategy":     true,
	"OrderingNotes":    true,
	"registerFixtures": true,
}

// claimShapes is the executable statement of what this scan sees.
//
// Every row is a sentence in the style this package writes, and the
// verdict beside it is what TestTheClaimScanSeesExactlyTheShapesItClaimsTo
// requires. The five false rows are the honest limits: a claim argued in a
// subordinate clause, a claim about the past, a claim with no named subject, a
// claim about the world rather than about this code, and a plain description.
// Three of those are deliberate. They are the prose worth keeping. Two of them,
// the unnamed subject and the outside subject, are genuine misses: a real
// assertion written as "A cross-stimulus figure must not be readable as a
// single-judge one" goes unseen here, and the only reason that is tolerable is
// that this row says so where a reader and the build both find it.
var claimShapes = []claimShape{
	{
		name:     "unqualified guarantee about a declared identifier",
		sentence: "unknownCost is an amount that must not be printed as a number.",
		claim:    true,
	},
	{
		name:     "absolute about a declared corpus",
		sentence: "HeldOutFixtures is a second corpus the prompt tuning never sees.",
		claim:    true,
	},
	{
		name:     "denied capability of a declared function",
		sentence: "sameFinding cannot see it.",
		claim:    true,
	},
	{
		name:     "quantifier carrying the identifier inside the idiom",
		sentence: "No costStrategy can outscore the calibrated row.",
		claim:    true,
	},
	{
		name:     "guarantee whose reason follows it",
		sentence: "OrderingNotes names the pairs a reader must not order, because their bands overlap.",
		claim:    true,
	},
	{
		name:     "consequence argued in a subordinate clause",
		sentence: "unknownCost is a sentinel, so a reader cannot sort a column of them.",
		claim:    false,
	},
	{
		name:     "record of what happened",
		sentence: "unknownCost was never printed as a number after the retraction.",
		claim:    false,
	},
	{
		name:     "assertion with no named subject",
		sentence: "A cross-stimulus figure must not be readable as a single-judge one.",
		claim:    false,
	},
	{
		name:     "assertion about the world rather than this code",
		sentence: "No reviewer can find a defect split across two requests.",
		claim:    false,
	},
	{
		name:     "description carrying no assertion idiom",
		sentence: "unknownCost carries the reason the amount is missing.",
		claim:    false,
	},
}

// commentedPackageAST parses every Go file in this package WITH its comments.
//
// A second parse beside packageAST, and the duplication is the point. That one
// blanks comments deliberately, "the comments are exactly what these scans must
// not be able to read", because a header registered in prose satisfied it once.
// This scan has the opposite requirement: prose is the only thing it looks at.
// Sharing one parse would mean one of the two guards reading input it was
// designed to be blind to.
//
// Files are read off disk, so build tags do not narrow the scan: comments in the
// `eval`-tagged files are checked by `go test ./...`, and a test declared behind
// that tag counts as existing when a comment cites it. That is the correct
// reading of "the build verifies it exists" for a package whose reports are
// rendered from tagged files.
func commentedPackageAST(t *testing.T) (map[string]*ast.File, *token.FileSet) {
	t.Helper()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}

	fset := token.NewFileSet()
	out := map[string]*ast.File{}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		parsed, err := parser.ParseFile(fset, e.Name(), nil, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parsing %s: %v", e.Name(), err)
		}
		out[e.Name()] = parsed
	}

	if len(out) == 0 {
		t.Fatal("no source files were parsed, so every scan over them passes vacuously")
	}
	return out, fset
}

// declaredNames collects what this package declares: every identifier, and the
// subset of them that are tests.
//
// Read from the AST rather than matched in source text, for the reason the
// header registry had to learn twice: a regexp over source cannot tell a
// declaration from the same words written inside a comment or a string, and both
// disguises have been used here to satisfy a guard without satisfying the
// compiler. A function declaration in the AST whose name begins with Test is a
// test the build runs; the same characters in prose are not.
func declaredNames(files map[string]*ast.File) (idents, tests map[string]bool) {
	idents, tests = map[string]bool{}, map[string]bool{}

	note := func(name string) {
		idents[name] = true
		if strings.HasPrefix(name, "Test") {
			tests[name] = true
		}
	}

	for _, file := range files {
		for _, decl := range file.Decls {
			switch node := decl.(type) {
			case *ast.FuncDecl:
				note(node.Name.Name)
			case *ast.GenDecl:
				for _, spec := range node.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						note(s.Name.Name)
					case *ast.ValueSpec:
						for _, id := range s.Names {
							note(id.Name)
						}
					}
				}
			}
		}
	}
	return idents, tests
}

// glued removes the line breaks a comment wraps at, so an identifier split
// across two lines reads as one word again.
//
// Go doc comments wrap at 80 columns and this package names its tests in whole
// sentences, so the wrap lands mid-name: three citations in this tree were in
// that state when this was written. Matching per line reports each of them as
// pointing at a test that does not exist, which is this guard's loudest error
// and would be entirely false.
func glued(text string) string { return strings.ReplaceAll(text, "\n", "") }

// codeShaped reports whether a word is spelled the way code is spelled rather
// than the way English is.
//
// The note behind it is in docs/harness-notes.md#codeshaped.
func codeShaped(word string) bool {
	for i, r := range word {
		if i > 0 && r >= 'A' && r <= 'Z' {
			return true
		}
	}
	return false
}

// namesDeclared reports whether a fragment of prose refers to something this
// package declares, spelled as code.
//
// A dotted selector counts on its trailing half, bundle.batch, Aggregate.Add,
// because the receiver is often a type from another package while the method is
// the thing the sentence is about.
func namesDeclared(fragment string, declared map[string]bool) bool {
	for _, word := range claimIdent.FindAllString(fragment, -1) {
		candidates := []string{word}
		if dot := strings.Index(word, "."); dot >= 0 {
			candidates = append(candidates, word[dot+1:])
		}
		for _, c := range candidates {
			if declared[c] && codeShaped(c) {
				return true
			}
		}
	}
	return false
}

// sentences splits a paragraph of prose on terminal punctuation.
//
// Crude on purpose: an over-eager split produces fragments, and a fragment
// carrying an idiom without its subject fails the subject test and is
// dropped. The error direction of a bad split is silence, not a false report.
func sentences(paragraph string) []string {
	var out []string
	var cur strings.Builder

	for i := range len(paragraph) {
		c := paragraph[i]
		cur.WriteByte(c)
		if c != '.' && c != '!' && c != '?' {
			continue
		}
		if i+1 >= len(paragraph) || paragraph[i+1] == ' ' || paragraph[i+1] == '\n' {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	if strings.TrimSpace(cur.String()) != "" {
		out = append(out, cur.String())
	}
	return out
}

// behaviouralClaim reports the assertion idiom a sentence makes about this
// package, or "" if the sentence is not making one.
//
// The three conditions are the whole rule, and each one is a kind of prose this
// package is right to keep: the idiom has to appear before any reason clause, or
// the sentence is an argument; the sentence must not be in the past, or it is
// history; and the subject up to and including the idiom has to name something
// declared here, or the sentence is about reviewers, readers and models rather
// than about code.
func behaviouralClaim(sentence string, declared map[string]bool) string {
	head := sentence
	if reason := claimReason.FindStringIndex(head); reason != nil {
		head = head[:reason[0]]
	}
	if claimHistory.MatchString(head) {
		return ""
	}
	for _, trigger := range claimTriggers {
		at := trigger.pattern.FindStringIndex(head)
		if at == nil {
			continue
		}
		// Up to and including the idiom: "No costStrategy can" carries its own
		// subject inside the match, and requiring the name strictly before it
		// would drop every universally quantified denial in the package.
		if namesDeclared(head[:at[1]], declared) {
			return head[at[0]:at[1]]
		}
	}
	return ""
}

// citedTests returns the tests a fragment of prose names and the package
// declares.
func citedTests(text string, tests map[string]bool) []string {
	flat := glued(text)
	var out []string
	for name := range tests {
		if strings.Contains(flat, name) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// danglingTests returns the test names a fragment cites that do not exist.
//
// Candidates are read from the WRAPPED text and resolved against the unwrapped
// one, and the split matters in both directions. Reading candidates from the
// unwrapped text loses the word boundary in front of a citation that begins a
// line, "which" and the name behind it fuse into one word that no longer looks
// like a test name at all, which silently exempted every citation wrapped that
// way. Resolving against the wrapped text loses the other end: a name broken
// mid-way arrives truncated, which is the state real citations here are in.
//
// So a citation resolves when it IS a declared test, or when it is the prefix of
// one that the unwrapped text really does spell out. A name that is neither
// points at nothing.
func danglingTests(text string, tests map[string]bool) []string {
	flat := glued(text)

	var out []string
	seen := map[string]bool{}

	for _, cited := range claimTestRef.FindAllString(text, -1) {
		if tests[cited] || seen[cited] {
			continue
		}
		resolved := false
		for name := range tests {
			if strings.HasPrefix(name, cited) && strings.Contains(flat, name) {
				resolved = true
				break
			}
		}
		if !resolved {
			seen[cited] = true
			out = append(out, cited)
		}
	}
	sort.Strings(out)
	return out
}

// TestClaimTriggersMatchTheIdiomsTheyName is the liveness check on the patterns
// themselves.
//
// A scan reporting zero violations is indistinguishable from a clean tree, and
// this package has shipped two guards that were silently matching nothing. Every
// pattern therefore has to be shown firing on the idiom it is named for and
// staying quiet on the plain statement of the same fact, so that a pattern
// mutated in either direction, into a catch-all or into a dead letter, fails
// here rather than going green over the whole package.
func TestClaimTriggersMatchTheIdiomsTheyName(t *testing.T) {
	if len(claimTriggers) == 0 {
		t.Fatal("there are no claim triggers, so the scan over this package asserts nothing")
	}

	seen := map[string]bool{}
	for _, trigger := range claimTriggers {
		if seen[trigger.name] {
			t.Errorf("two triggers are both named %q, so an error message cannot say which fired",
				trigger.name)
		}
		seen[trigger.name] = true

		if !trigger.pattern.MatchString(trigger.fires) {
			t.Errorf("the %q trigger does not match the idiom it exists for:\n  %s\n"+
				"A pattern that matches nothing reports a clean package forever",
				trigger.name, trigger.fires)
		}
		if trigger.pattern.MatchString(trigger.quiet) {
			t.Errorf("the %q trigger also matches the plain statement of the same fact:\n  %s\n"+
				"A pattern this wide flags the prose that carries the argument, and a guard that "+
				"flags good comments gets an exemption written into it",
				trigger.name, trigger.quiet)
		}
	}
}

// TestTheClaimScanSeesExactlyTheShapesItClaimsTo is the disclosure, executed.
//
// The comment at the top of this file says the scan is narrow and says which
// sentences it therefore misses. That is a claim about behaviour in a doc
// comment, which is the exact thing this file exists to distrust, so it is
// written as claimShapes and run rather than asserted. Widen behaviouralClaim
// and a false row starts reporting a claim; narrow it and a true row stops. In
// both directions the build fails until the table, which IS the disclosure, is
// brought back into line with the rule.
func TestTheClaimScanSeesExactlyTheShapesItClaimsTo(t *testing.T) {
	covered, missed := 0, 0

	for _, shape := range claimShapes {
		got := behaviouralClaim(shape.sentence, claimShapeNames)
		switch {
		case shape.claim && got == "":
			t.Errorf("the scan no longer sees %s, which claimShapes says it does:\n  %s\n"+
				"Either the rule lost coverage it is documented to have, or this row is wrong "+
				"and the file's own account of what it catches has to change with it",
				shape.name, shape.sentence)
		case !shape.claim && got != "":
			t.Errorf("the scan now reports %s as a claim (%q), which claimShapes says it does not:\n"+
				"  %s\nThis is prose that argues, records history or is not about this package's "+
				"code, and flagging it is how a guard earns the exemption list that defeats it",
				shape.name, got, shape.sentence)
		}
		if shape.claim {
			covered++
		} else {
			missed++
		}
	}

	// Both halves have to be populated. A table of nothing but true rows is a
	// rule with no stated limits, and a table of nothing but false rows is a
	// rule that catches nothing; either one makes this test pass while saying
	// nothing about the scan.
	if covered == 0 || missed == 0 {
		t.Fatalf("claimShapes has %d covered and %d uncovered shapes: a coverage statement needs "+
			"both, or it is not stating a limit", covered, missed)
	}
}

// TestEveryBehaviouralClaimInThisPackageNamesATestThatExists is the build
// failure this file was written to be.
//
// The citation has to sit in the same PARAGRAPH as the claim, not merely
// somewhere in the same comment. The wiring guard is why: its doc cited real
// tests in its opening paragraph and made an unbacked promise about future
// fixture sets four paragraphs down, and any rule scoped to the comment as a
// whole reads that as covered. A paragraph is the unit of one argument, so it is
// the unit a citation can cover.
//
// What this CANNOT check is that the named test verifies the claim rather than
// something adjacent to it. That is a judgement no scan makes, and pretending
// otherwise here would reproduce the defect on the guard itself. What it does
// buy is that every guarantee in this package's prose points at executable code
// with a name, and that following the name lands somewhere real.
func TestEveryBehaviouralClaimInThisPackageNamesATestThatExists(t *testing.T) {
	files, fset := commentedPackageAST(t)
	declared, tests := declaredNames(files)

	if len(tests) == 0 {
		t.Fatal("no test functions were discovered, so every citation in this package would " +
			"read as dangling and every claim as unbacked")
	}

	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)

	groups, scanned, found := 0, 0, 0

	for _, name := range names {
		for _, comment := range files[name].Comments {
			groups++
			text := comment.Text()
			at := fset.Position(comment.Pos()).Line

			for _, paragraph := range strings.Split(text, "\n\n") {
				cited := citedTests(paragraph, tests)

				for _, sentence := range sentences(paragraph) {
					scanned++
					trigger := behaviouralClaim(sentence, declared)
					if trigger == "" {
						continue
					}
					found++
					if len(cited) > 0 {
						continue
					}

					line := at + strings.Count(text[:strings.Index(text, sentence)+1], "\n")
					t.Errorf("%s:%d asserts behaviour (%q) and cites no test:\n  %s\n"+
						"Nothing executes this sentence, so a reader acts on it and the build "+
						"agrees with them whatever the code does. Name a test in this paragraph "+
						"that verifies it, or delete the assertion and keep the argument",
						name, line, trigger, strings.Join(strings.Fields(sentence), " "))
				}
			}
		}
	}

	// Three levels of non-vacuity, because the way this scan fails silently is by
	// finding less than it did yesterday and reporting a clean package for it.
	if groups == 0 || scanned == 0 {
		t.Fatalf("scanned %d comment groups and %d sentences: with nothing to read, this test "+
			"passes without having looked at the package", groups, scanned)
	}
	if found == 0 {
		t.Fatal("the scan found no behavioural assertion anywhere in this package. This tree has " +
			"never been in that state, and a rule that matches nothing is not evidence that the " +
			"prose is clean — check that behaviouralClaim and claimTriggers still do what " +
			"claimShapes says they do")
	}
}

// TestNoCommentInThisPackageCitesATestThatDoesNotExist covers the worse half.
//
// A claim with no citation reads as unverified, which is honest. A claim citing
// a test that is not there reads as VERIFIED, and a reader who trusts it has
// been told the opposite of the truth by a comment that looks more rigorous than
// its neighbours. That is how TestForeignSeverityWordsAreTranslatedOnPurpose sat
// in incumbent.go promising that a fourth severity translation could not be
// added silently, with nothing anywhere pinning the enumerated set.
//
// Every comment is checked, not only the ones carrying a claim: a stale citation
// beside plain prose points at the same hole.
func TestNoCommentInThisPackageCitesATestThatDoesNotExist(t *testing.T) {
	files, fset := commentedPackageAST(t)
	_, tests := declaredNames(files)

	if len(tests) == 0 {
		t.Fatal("no test functions were discovered, so this scan would report every citation " +
			"in the package as dangling")
	}

	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)

	cited := 0
	for _, name := range names {
		for _, comment := range files[name].Comments {
			text := comment.Text()
			cited += len(citedTests(text, tests))

			for _, dangling := range danglingTests(text, tests) {
				t.Errorf("%s:%d cites %s, which this package does not declare:\n%s\n"+
					"A citation is read as proof the claim beside it is checked. Pointing at a "+
					"test that is not there is worse than pointing at nothing, because it stops "+
					"the reader looking. Write the test, or correct the name",
					name, fset.Position(comment.Pos()).Line, dangling,
					fmt.Sprintf("  %.200s", strings.Join(strings.Fields(text), " ")))
			}
		}
	}

	if cited == 0 {
		t.Fatal("no comment in this package cites any test, which has not been true of this tree " +
			"since the first guard was written. The resolver is matching nothing, so every " +
			"dangling citation would go unreported")
	}
}
