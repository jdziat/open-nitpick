package evals

import (
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jdziat/open-nitpick/internal/review"
)

// TestASecondarySpanSurvivesTheDump is what makes a retained run sufficient to
// answer Rule 14's conditions 3 and 4 without re-spending the corpus.
//
// NOISE and ANCHOR are pure functions of (findings, fixture): explainsAny,
// anchorDistance, anchoredLines and defectAnchoredLines consult no judge, no
// model and no network. All four read review.Finding.AlsoAt, and the incumbent
// is precisely the reviewer that fills it, crParseAlsoApplies populates it from
// an "Also applies to" line. A round trip that drops the secondary spans
// understates ANCHOR and can overstate NOISE, because a finding whose only near
// span lived in AlsoAt is reclassified as invented; and a reader has no way to
// tell a dropped span from a finding that never had one.
//
// The assertion is on the SCORE rather than on the field, because the field is
// only interesting for what it changes: it is scored through ScoreDetection,
// which is the same function the published cells are rendered from.
func TestASecondarySpanSurvivesTheDump(t *testing.T) {
	fixture := plantedFixtureForDump(t)
	defect := fixture.Defects[0]

	// One finding whose PRIMARY anchor is far from the plant and whose secondary
	// span sits on it, the shape the incumbent's cache carries, and the
	// shape in which the two readings disagree.
	findings := []review.Finding{{
		Path:      defect.Path,
		Line:      defect.Line + 400,
		EndLine:   defect.Line + 402,
		AlsoAt:    []review.LineSpan{{Line: defect.Line, EndLine: defect.Line + 6}},
		Severity:  string(defect.WantSeverity),
		Class:     string(defect.Class),
		Title:     strings.Join(defect.Keywords, " "),
		Rationale: "recorded so the round trip has something to lose",
	}}

	live := ScoreDetection(fixture, findings)

	path := filepath.Join(t.TempDir(), "findings.jsonl")
	dump, err := NewDump(path)
	if err != nil {
		t.Fatalf("open dump: %v", err)
	}
	if err := dump.Record(DumpSample{Model: "probe", Run: 1, Fixture: fixture, Findings: findings}); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := dump.Close(); err != nil {
		t.Fatalf("close dump: %v", err)
	}

	records, err := ReadDump(path)
	if err != nil {
		t.Fatalf("read dump: %v", err)
	}
	if len(records) == 0 {
		t.Fatal("the dump recorded nothing, so this test would pass on an empty round trip")
	}

	var rebuilt []review.Finding
	for _, r := range records {
		rebuilt = append(rebuilt, findingFromRecord(r))
	}

	replayed := ScoreDetection(fixture, rebuilt)

	if live.WidestAnchor != replayed.WidestAnchor {
		t.Errorf("ANCHOR reads %d lines live and %d off the retained dump. The two columns Rule 14 "+
			"was unable to evaluate are re-derivable from a dump only if the dump carries what they "+
			"read, and a run retained in a format that loses secondary spans still costs a re-spend "+
			"of the held-out corpus", live.WidestAnchor, replayed.WidestAnchor)
	}
	if live.Noise() != replayed.Noise() {
		t.Errorf("NOISE counts %d invented finding(s) live and %d off the retained dump. A finding "+
			"whose only near span was secondary comes back as invented, so the replay reports the "+
			"reviewer as noisier than the table did", live.Noise(), replayed.Noise())
	}
}

// plantedFixtureForDump is a fixture that plants at least one defect, for the
// round-trip assertions that need a plant to score against.
func plantedFixtureForDump(t *testing.T) Fixture {
	t.Helper()

	for _, f := range AllFixtures() {
		if len(f.Defects) > 0 {
			return f
		}
	}

	t.Fatal("no fixture in the corpus plants a defect, so a detection round trip would compare two zeroes")
	return Fixture{}
}

// TestARetainedRunRefusesToOverwriteAnEarlierOne.
//
// Retention is only worth having if it does not destroy what it retained last
// time. NewDump opens with os.Create, which truncates, and its comment gives the
// reason that has to stay true: records are keyed by (model, fixture, run),
// every experiment reuses those keys, and GroupDump refuses a concatenated file
// outright, so appending is not the alternative. A default path therefore has
// to be a NAME nobody else took, and the name has to be enforced rather than
// assumed, because the file it would land on may be the only copy of a held-out
// run that cannot be re-collected.
func TestARetainedRunRefusesToOverwriteAnEarlierOne(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 8, 7, 14, 22, 33, 0, time.UTC)
	corpus := HeldOutFixtures()

	first, path, err := openRunDumpAt(dir, "benchmark", corpus, at, 4242)
	if err != nil {
		t.Fatalf("open the first run's dump: %v", err)
	}
	if err := first.Record(DumpSample{
		Model: "first-run", Run: 1,
		Fixture:  plantedFixtureForDump(t),
		Findings: []review.Finding{{Path: "a.go", Line: 1, Title: "the evidence of a paid run"}},
	}); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close the first run's dump: %v", err)
	}

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the first run's dump: %v", err)
	}
	if len(before) == 0 {
		t.Fatal("the first run retained nothing, so a clobber would destroy nothing and this test " +
			"would pass on an empty file")
	}

	// The same battery, the same corpus, the same second, the same process: the
	// worst case the name alone does not separate.
	second, _, err := openRunDumpAt(dir, "benchmark", corpus, at, 4242)
	if err == nil {
		_ = second.Close()
		t.Error("a second retained run opened over the first one's file without complaint. The " +
			"held-out corpus is spent once, so the file this would have truncated may be the only " +
			"record of a run nobody can repeat")
	}

	after, rerr := os.ReadFile(path)
	if rerr != nil {
		t.Fatalf("the first run's dump is no longer readable after a second run opened: %v", rerr)
	}
	if string(after) != string(before) {
		t.Errorf("the first run's retained evidence changed when a second run opened: %d bytes "+
			"became %d", len(before), len(after))
	}
}

// TestARetainedRunIsNamedForTheCorpusItSpent.
//
// A directory of retained runs is read months later, and the one distinction
// that matters when reading it is the one the Makefile's truncation warning is
// about: a held-out dump carries defect_why, which is the planted defect's own
// prose, and the documented workflow is to read a dump and edit the prompt. The
// per-record held_out flag already carries it in the data; the name carries it
// to somebody running ls.
func TestARetainedRunIsNamedForTheCorpusItSpent(t *testing.T) {
	at := time.Date(2026, 8, 7, 14, 22, 33, 0, time.UTC)

	held := HeldOutFixtures()
	tuning := Fixtures()
	if len(held) == 0 || len(tuning) == 0 {
		t.Fatal("one half of the corpus is empty, so the two names below cannot differ")
	}

	for _, tc := range []struct {
		what     string
		fixtures []Fixture
		want     string
	}{
		{"the held-out corpus", held, "heldout"},
		{"the tuning corpus", tuning, "tuning"},
		{"a mixed selection", append(append([]Fixture{}, held[0]), tuning[0]), "mixed"},
	} {
		got := runDumpName("benchmark", tc.fixtures, at, 4242)
		if !strings.Contains(got, tc.want) {
			t.Errorf("a run over %s is retained as %q, which does not say %q; a reader of the "+
				"directory cannot tell a spent held-out run from a tuning one", tc.what, got, tc.want)
		}
	}

	// And two runs of one battery over one corpus land on two files, so the
	// refusal above is the residual rather than the common case.
	a := runDumpName("benchmark", held, at, 4242)
	b := runDumpName("benchmark", held, at.Add(time.Second), 4242)
	if a == b {
		t.Errorf("two runs a second apart are both retained as %q, so the second one's only "+
			"protection is the refusal to open", a)
	}
}

// TestAPartialDumpIsRefusedAsARejudgeInput.
//
// Re-judging a file a battery is still filling measures whatever reached disk
// first. Guarding it by comparing NITPICK_EVAL_DUMP with the re-judge
// input, which covers the operator who exported both, and covers nothing once a
// battery resolves its own path, because then NITPICK_EVAL_DUMP is empty and the
// comparison reads "" and skips. Dump.Close renames a retained run out of its
// in-progress suffix, so the completeness answer is in the name; both branches
// are checked here because closing the second one is what the first one's
// absence was hiding.
func TestAPartialDumpIsRefusedAsARejudgeInput(t *testing.T) {
	dir := t.TempDir()

	dump, path, err := openRunDumpAt(dir, "benchmark", HeldOutFixtures(), time.Now().UTC(), os.Getpid())
	if err != nil {
		t.Fatalf("open a retained run: %v", err)
	}

	inProgress := path + dumpPartialSuffix
	if _, err := os.Stat(inProgress); err != nil {
		t.Fatalf("a run being written is not under %q: %v", dumpPartialSuffix, err)
	}

	// EnvDump is empty, which is exactly the state the old check could not see.
	if why := RejudgeInputProblem(inProgress, ""); why == "" {
		t.Error("a dump that is still being written is accepted as a re-judge input while the " +
			"environment variable the guard reads is unset — which is every retained run, since a " +
			"battery that resolves its own path sets nothing")
	}

	if err := dump.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("a finished run is not under its final name: %v", err)
	}
	if why := RejudgeInputProblem(path, ""); why != "" {
		t.Errorf("a finished run is refused as a re-judge input: %s", why)
	}

	// The operator's collision is still refused, which is the branch this
	// function inherited.
	if why := RejudgeInputProblem(path, path); why == "" {
		t.Error("re-judging the very file NITPICK_EVAL_DUMP names is accepted; the writer truncates " +
			"on open, so that reads a file being emptied")
	}
}

// TestEveryJudgedBatteryRetainsItsFindingsWithoutBeingAsked.
//
// The held-out corpus is spent once and both batteries that produced the Rule 14
// evidence ran with NITPICK_EVAL_DUMP unset, so OpenDump returned nil, every
// dump.Record call was a no-op, and the finding lists that sat in RAM for the
// whole paid run were written nowhere. Re-deriving the two missing columns then
// requires re-running, and every re-run is another look at a set that is
// supposed to be looked at once.
//
// IT COVERS every BATTERY that PRINTS THE JUDGED TABLE, not the head-to-head
// alone, and that is the correction rather than a generalisation for its own
// sake. Retention was fixed on the benchmark and TestJudgeModels was described
// as a tuning axis over a re-reviewable corpus, which it is not. It prints the
// same reportJudgedModels table, and `make judge-models FIXTURES=$(HELD_OUT)`
// points it at the spent-once corpus, which is the original incident exactly.
// Deriving the list from the table means a third battery inherits the
// requirement along with the renderer.
//
// This is a source assertion because both batteries are behind the `eval` build
// tag and reach a paid provider, so nothing in the default build can call them.
// The package already reads those files off disk for exactly this reason: a
// guard compiled only under that tag cannot run in `go test ./...`.
func TestEveryJudgedBatteryRetainsItsFindingsWithoutBeingAsked(t *testing.T) {
	reporting := batteriesCalling(t, "reportJudgedModels")
	if len(reporting) == 0 {
		t.Fatal("no function calls reportJudgedModels, so this scan is looking for a table nobody " +
			"prints and would pass on any tree")
	}

	for _, battery := range judgedBatteries() {
		if !reporting[battery] {
			t.Errorf("%s is listed as a judged battery and does not print the judged table; one of "+
				"the two lists is stale and this guard is checking the wrong functions", battery)
		}
	}
	for battery := range reporting {
		if !slices.Contains(judgedBatteries(), battery) {
			t.Errorf("%s prints the judged table and is not in judgedBatteries(), so the guards "+
				"keyed on that list do not see it", battery)
		}
	}

	for battery := range reporting {
		t.Run(battery, func(t *testing.T) {
			body := batteryBody(t, battery)

			called := map[string]bool{}
			ast.Inspect(body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if fn, ok := call.Fun.(*ast.Ident); ok {
					called[fn.Name] = true
				}
				return true
			})

			if !called["OpenRunDump"] {
				t.Errorf("%s does not open its dump through OpenRunDump, so with %s unset it retains "+
					"nothing. The evidence for the two columns Rule 14 could not be read was in "+
					"memory for the whole of a paid held-out run and was written nowhere",
					battery, EnvDump)
			}
			if called["OpenDump"] {
				t.Errorf("%s still opens its dump through OpenDump, which returns nil when %s is "+
					"unset. A run that retains its findings only when an operator remembers a "+
					"variable retains nothing on the run that mattered", battery, EnvDump)
			}
		})
	}
}

// batteriesCalling returns the tune_test.go top-level functions that call a
// named function, so a guard can be keyed on what a battery does rather than on
// a list somebody has to remember to extend.
func batteriesCalling(t *testing.T, name string) map[string]bool {
	t.Helper()

	const file = "tune_test.go"

	files := packageAST(t)
	parsed, ok := files[file]
	if !ok {
		t.Fatalf("%s is not in the package any more; this scan is checking a file that does not "+
			"exist and would pass on any tree", file)
	}

	out := map[string]bool{}
	for _, decl := range parsed.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil || !strings.HasPrefix(fn.Name.Name, "Test") {
			continue
		}
		if len(calledAt(fn.Body, "", name)) > 0 {
			out[fn.Name.Name] = true
		}
	}
	return out
}

// TestATruncatedTailCostsTheTailAndNotTheFile.
//
// Retention makes this file the only record of a corpus that is spent once, and
// the decoder discarded every record it had already read the moment one was
// malformed. A short write is the ordinary way to get one, ENOSPC on a
// ninety-minute run, whose error Record returns and the battery demotes to a
// single line in its notes while continuing to append after the mangled record.
// Refusing the file is still right; refusing it while THROWING AWAY the eighty
// good records above the bad one is not, and it turns a lost tail into a lost
// run.
func TestATruncatedTailCostsTheTailAndNotTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "findings.jsonl")
	dump, err := NewDump(path)
	if err != nil {
		t.Fatalf("open dump: %v", err)
	}
	fixture := plantedFixtureForDump(t)
	for i := range 5 {
		if err := dump.Record(DumpSample{
			Model: "probe", Run: i + 1, Fixture: fixture,
			Findings: []review.Finding{{Path: "a.go", Line: 1, Title: "the evidence of a paid run"}},
		}); err != nil {
			t.Fatalf("record: %v", err)
		}
	}
	if err := dump.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	whole, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(whole), "\n"), "\n")
	if len(lines) != 5 {
		t.Fatalf("the probe wrote %d record(s), not the 5 this test truncates", len(lines))
	}

	// The last record cut in half, which is what a process killed mid-encode
	// leaves behind.
	cut := strings.Join(lines[:4], "\n") + "\n" + lines[4][:len(lines[4])/2]
	if err := os.WriteFile(path, []byte(cut), 0o600); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	records, err := ReadDump(path)
	if err == nil {
		t.Error("a dump whose last record is half-written decoded without error, so a corrupted " +
			"tail would be read as the end of a complete run")
	}
	if len(records) != 4 {
		t.Errorf("a dump with four intact records and a truncated fifth decoded %d of them. The "+
			"file is the only copy of a corpus that is spent once, so a bad byte at the end must "+
			"cost the tail and not the run", len(records))
	}
}

// TestAFinishedRunIsNotRenamedOverAnother.
//
// openRunDumpAt refuses both collision shapes at OPEN, and a benchmark runs for
// tens of minutes between opening and closing. Nothing in this package reaches
// the window, so this is a latent hazard rather than an observed one, but the
// argument the open-time refusal is built on ("the file it would land on may be
// the only copy of a held-out run") is not weaker at close time, and os.Rename
// is silent.
func TestAFinishedRunIsNotRenamedOverAnother(t *testing.T) {
	dir := t.TempDir()

	dump, path, err := openRunDumpAt(dir, "benchmark", HeldOutFixtures(), time.Now().UTC(), os.Getpid())
	if err != nil {
		t.Fatalf("open a retained run: %v", err)
	}

	// Another writer lands on the final name while this run is still going.
	const earlier = "{\"model\":\"an earlier run nobody can repeat\"}\n"
	if err := os.WriteFile(path, []byte(earlier), 0o600); err != nil {
		t.Fatalf("plant an earlier run: %v", err)
	}

	if err := dump.Close(); err == nil {
		t.Error("closing a retained run renamed it over a file that appeared at its final name " +
			"while it was writing, and said nothing")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the earlier run is gone: %v", err)
	}
	if string(after) != earlier {
		t.Errorf("the earlier run's %d bytes were replaced by this run's %d", len(earlier), len(after))
	}
}

// judgedBatteries are the batteries that review, judge and print a scored table,
// named here so a guard over them fails when a third arrives without being
// wired the same way.
func judgedBatteries() []string {
	return []string{"TestBenchmarkAgainstIncumbent", "TestJudgeModels"}
}

// batteryBody returns one battery's function body from the package source.
func batteryBody(t *testing.T, name string) *ast.BlockStmt {
	t.Helper()

	const file = "tune_test.go"

	files := packageAST(t)
	parsed, ok := files[file]
	if !ok {
		t.Fatalf("%s is not in the package any more; this scan is checking a file that does not "+
			"exist and would pass on any tree", file)
	}

	for _, decl := range parsed.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == name {
			return fn.Body
		}
	}

	t.Fatalf("%s no longer declares %s", file, name)
	return nil
}

// calledAt returns the source positions of every call to pkg.method inside a
// node, or to a bare function when recv is empty.
func calledAt(n ast.Node, recv, method string) []token.Pos {
	var out []token.Pos
	ast.Inspect(n, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fn := call.Fun.(type) {
		case *ast.SelectorExpr:
			id, ok := fn.X.(*ast.Ident)
			if ok && id.Name == recv && fn.Sel.Name == method {
				out = append(out, call.Pos())
			}
		case *ast.Ident:
			if recv == "" && fn.Name == method {
				out = append(out, call.Pos())
			}
		}
		return true
	})
	return out
}

// TestEveryPaidReviewIsRetainedWhateverTheJudgeSays.
//
// RETENTION was GATED ON THE JUDGE. A review whose judge call failed returned
// before dump.Record, so its findings, already paid for, a model call on our
// side and a rate-limited free-tier `incumbent review` on the incumbent's,
// were discarded. RECALL, NOISE, ANCHOR and L/DEF need no judge, which is the entire
// argument for retaining a run at all; the arithmetic needed none and the
// RECORDING did. Worse on the incumbent's side, where the benchmark's live
// collection path does not write the cache: the only `os.WriteFile` into the
// cache directory is in the collector, so one judge hiccup on an uncached
// fixture destroyed a review drawn from an allowance nobody can re-spend on
// demand.
//
// THE PROPERTY, and it is narrower than "every return records": a review that
// reached the judge is written down whatever the judge answers. Returns before
// the judge call are exempt and must be. Those are the paths where the review
// itself failed and there is no finding list, and Dump.Record writes a
// `silent: true` line for an empty one, which would record "this reviewer said
// nothing" about a review that never ran.
//
// A source assertion because both batteries are behind the `eval` build tag and
// reach a paid provider, so nothing in the default build can call them.
func TestEveryPaidReviewIsRetainedWhateverTheJudgeSays(t *testing.T) {
	for _, battery := range judgedBatteries() {
		t.Run(battery, func(t *testing.T) {
			body := batteryBody(t, battery)

			for _, lit := range judgingClosures(body) {
				judged := calledAt(lit, "judge", "Judge")
				if len(judged) == 0 {
					continue
				}
				recorded := calledAt(lit, "dump", "Record")
				if len(recorded) == 0 {
					t.Fatalf("%s judges a review in a closure that never calls dump.Record, so every "+
						"paid review it handles is discarded", battery)
				}

				first := recorded[0]
				for _, ret := range returnsIn(lit) {
					if ret < judged[0] || ret > first {
						continue
					}
					t.Errorf("%s returns at offset %d — after asking the judge and before recording "+
						"anything — so a review the judge could not grade is thrown away. It has "+
						"already been paid for and its model-free columns need no judge to be "+
						"recomputed from it", battery, ret)
				}
			}
		})
	}
}

// judgingClosures returns the function literals in a battery that call the
// judge, which is where a review is held between being paid for and being
// written down.
func judgingClosures(body *ast.BlockStmt) []*ast.FuncLit {
	return closuresCalling(body, "judge", "Judge")
}

// closuresCalling returns the function literals inside a battery that call
// recv.method.
func closuresCalling(body *ast.BlockStmt, recv, method string) []*ast.FuncLit {
	var out []*ast.FuncLit
	ast.Inspect(body, func(n ast.Node) bool {
		lit, ok := n.(*ast.FuncLit)
		if !ok {
			return true
		}
		if len(calledAt(lit, recv, method)) > 0 {
			out = append(out, lit)
		}
		return true
	})
	return out
}

// TestEveryJudgedBatteryStatesWhatItAttempted is what keeps
// Aggregate.ShortFixtures out of its own blind spot.
//
// ShortFixtures is silent for a row that never called Attempted, so a battery
// that folds reviews without stating how many it tried publishes unmarked rates
// over an unknown sample. TestEveryJudgedBatteryStatesWhatItAttempted is the
// thing that makes that unreachable; it is not detectable from the row, which is
// why it is checked here rather than at render time.
//
// The position matters as much as the call. Attempted must sit ABOVE every
// return in the fold site: it is the one counter that has to run on the paths
// where nothing else does, and a call placed beside AddSeverity would count
// exactly the reviews that arrived and report every row as whole.
//
// A source assertion because both batteries are behind the `eval` build tag and
// reach a paid provider.
func TestEveryJudgedBatteryStatesWhatItAttempted(t *testing.T) {
	for _, battery := range judgedBatteries() {
		t.Run(battery, func(t *testing.T) {
			body := batteryBody(t, battery)

			folds := closuresCalling(body, "agg", "AddSeverity")
			if len(folds) == 0 {
				t.Fatalf("%s folds no severity score through a closure, so this scan cannot find its "+
					"fold site and would pass on any wiring", battery)
			}

			for _, lit := range folds {
				stated := calledAt(lit, "agg", "Attempted")
				if len(stated) == 0 {
					t.Errorf("%s folds reviews into a row that never states how many it attempted. "+
						"Every rate on that row is then a mean over the reviews that survived, and "+
						"the ones that did not are a subset the run did not choose at random",
						battery)
					continue
				}
				for _, ret := range returnsIn(lit) {
					if ret < stated[0] {
						t.Errorf("%s can return at offset %d before calling Attempted at %d. A lost "+
							"review that returns above the counter is a review the row does not know "+
							"it tried", battery, ret, stated[0])
					}
				}
			}
		})
	}
}

// returnsIn returns the position of every return statement directly inside a
// closure, excluding those in closures nested within it, a deferred recorder's
// own body is not a path out of the function that registered it.
func returnsIn(lit *ast.FuncLit) []token.Pos {
	var out []token.Pos
	ast.Inspect(lit, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncLit:
			// Descend into the literal under test and into nothing nested in it.
			return n == lit
		case *ast.ReturnStmt:
			out = append(out, n.Pos())
		}
		return true
	})
	return out
}
