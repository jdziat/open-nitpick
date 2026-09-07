package evals

import (
	"context"
	"fmt"
	"go/ast"
	"go/token"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/bundle"
	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/diff"
	"github.com/jdziat/open-nitpick/internal/review"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// TestPlantedDefectsPointAtRealLines validates the corpus itself.
//
// Every Defect.Line was hand-counted, and four of five were wrong, one pointed
// past the end of its file. Nothing failed, because the scorer's line tolerance
// silently absorbed the error. A wrong anchor corrupts every recall measurement
// taken with it, so the ground truth needs a test as much as the code does.
//
// This runs in `go test ./...`: it needs no network and no credentials.
//
// It iterates AllFixtures rather than Fixtures: the held-out corpus is spent
// once, so a wrong anchor in it would be discovered at the moment the number it
// corrupted was already being reported.
func TestPlantedDefectsPointAtRealLines(t *testing.T) {
	for _, f := range AllFixtures() {
		for _, d := range f.Defects {
			content, ok := f.Head[d.Path]
			if !ok {
				t.Errorf("%s: defect names %q, which is not in the fixture's head state", f.Name, d.Path)
				continue
			}

			lines := strings.Split(content, "\n")
			if len(lines) > 0 && lines[len(lines)-1] == "" {
				lines = lines[:len(lines)-1]
			}

			if d.Line < 1 || d.Line > len(lines) {
				t.Errorf("%s: defect at %s:%d is outside the file (%d lines): %s",
					f.Name, d.Path, d.Line, len(lines), d.Why)
			}

			// A defect with no keywords can never be matched, so it scores as
			// missed by every model forever while looking like a fixture the
			// corpus is measuring.
			if len(d.Keywords) == 0 {
				t.Errorf("%s: defect at %s:%d has no keywords, so no finding can ever match it: %s",
					f.Name, d.Path, d.Line, d.Why)
			}
		}
	}
}

// TestPlantedDefectsAreOnTheRightLine reads each Defect.Line and checks the
// text sitting on it is the defect.
//
// The previous version of this test stated the line number itself, "line 17 of
// go-sql-injection contains Sprintf", and never consulted Defect.Line. That
// tests the fixture against a second hand-written copy of the ground truth,
// which is exactly the thing being doubted: changing Defect.Line from 9 to 10
// left it green, and 10 is the wrong line. The needle is now looked up per
// DEFECT, so the only line number in the assertion is the one scoring uses.
func TestPlantedDefectsAreOnTheRightLine(t *testing.T) {
	// Each fixture maps to the substring its defect's line must contain, in the
	// order the fixture declares its defects.
	want := map[string][]string{
		"go-nil-deref":             {"http.Get"},
		"go-sql-injection":         {"Sprintf"},
		"go-hardcoded-secret":      {"sk-live"},
		"python-command-injection": {"os.system"},
		"capacity-hint-nit":        {"make("},
		"multi-defect":             {"os.Create", "go func", "os.Create"},

		// Warning and nit plants in the tuning corpus. Each needle was read back
		// out of Head by a probe that printed the line, not counted by eye: the
		// four wrong anchors this test exists for were all produced by counting.
		"ts-unbounded-memo-key":     {`remember("q:" + q`},
		"go-cancel-goroutine-leak":  {"make(chan result)"},
		"python-timing-unsafe-hmac": {"== provided"},
		"cross-file-copy-nit":       {"make([]store.Event"},
		"sorted-for-min-nit":        {"sorted(readings"},

		// Info plants, split across both corpora. Each needle was read back out
		// of Head by a probe that printed the line.
		//
		// The Kotlin needle carries the PARAMETER NAME on purpose, and it is the
		// only needle here that pins more than a location. Head used to rename
		// that parameter from report to source, which compiles and reads like
		// tidying and is a contract break in Kotlin: named arguments are part of
		// the signature, so `summarize(report = r)` stopped compiling. That is a
		// second, unplanted defect in a fixture whose whole claim is that it
		// plants exactly one, and it falsified two sentences of the fixture's own
		// SeverityNote. Renaming it again fails here.
		"kotlin-widened-input":    {"fun summarize(report: Summarizable)"},
		"php-forbidden-vs-404":    {"Response(403"},
		"go-package-singleton":    {"var Default = Load("},
		"rust-crate-for-one-call": {`humantime = "2"`},
		"ruby-default-page-size":  {"DEFAULT_PER_PAGE = 100"},

		// Held-out corpus.
		"contract-break":      {`json:"createdAt"`},
		"data-loss-migration": {"UPDATE accounts"},
		"ts-unawaited-async":  {"forEach"},
		"timezone-boundary":   {"Truncate"},
		"removed-guard":       {"s.store.DeleteProject(ctx, projectID)"},
		"retry-no-backoff":    {"for _ in range(ATTEMPTS)"},

		"csharp-client-per-request": {"new HttpClient"},
		"bash-fixed-temp-path":      {"OUT=/tmp/"},
		"cross-file-sort-nit":       {"[...listed].sort("},
		"duplicate-test-case-nit":   {"space becomes a hyphen"},
		"defensive-copy-nit":        {"new ArrayList<>(labels)"},
	}

	for _, f := range AllFixtures() {
		needles, ok := want[f.Name]
		if !ok {
			// Silence here would let a new fixture ship an unchecked anchor,
			// which is how the corpus acquired four wrong line numbers.
			if !f.Clean() {
				t.Errorf("fixture %q plants defects but has no anchor assertion; add one", f.Name)
			}
			continue
		}

		if len(needles) != len(f.Defects) {
			t.Errorf("%s: %d anchor assertions for %d defects", f.Name, len(needles), len(f.Defects))
			continue
		}

		for i, d := range f.Defects {
			content, ok := f.Head[d.Path]
			if !ok {
				t.Errorf("%s: defect names %q, which is not in the fixture's head state", f.Name, d.Path)
				continue
			}

			lines := strings.Split(content, "\n")
			if d.Line < 1 || d.Line > len(lines) {
				t.Errorf("%s: defect at %s:%d is past EOF", f.Name, d.Path, d.Line)
				continue
			}

			if !strings.Contains(lines[d.Line-1], needles[i]) {
				t.Errorf("%s: %s:%d is %q, expected the defect line to contain %q — %s",
					f.Name, d.Path, d.Line, strings.TrimSpace(lines[d.Line-1]), needles[i], d.Why)
			}
		}
	}

	for name := range want {
		found := false
		for _, f := range AllFixtures() {
			if f.Name == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("fixture %q no longer exists; update this test", name)
		}
	}
}

// TestKeywordsAdmitOnlyRealDetections runs findings through the scorer and
// asserts what it credits.
//
// The previous version of this test compared each Keyword string for EQUALITY
// against a list of banned phrases. That is a property of the fixture's
// literals, not of the scorer: mentionsAny matches keywords as case-insensitive
// SUBSTRINGS of title+rationale+category, so deleting a phrase from the keyword
// list does nothing to stop a finding CONTAINING that phrase from matching
// through some other keyword. It was green on six fixtures that each credited
// the exact objection its ban list was written to exclude, "prefer for...of
// for readability" scored full recall on the unawaited-promise plant through
// the keyword "promise", and so did "fine as written".
//
// So the cases below are findings, and the assertion is matches(). Both
// directions are pinned: without the must-match half, deleting every keyword
// would turn this test green while making the corpus unscoreable.
//
// A THIRD CHECK RIDES ALONG, and it is here rather than in its own test because
// it reads the same two lists. Being credited by SOME keyword is not evidence
// that any PARTICULAR keyword is worth carrying: ten were added to the info
// plants in one round and six survived deletion with the suite green, because a
// keyword already on the list credited the same probe. checkSoleCreditors runs
// that deletion for real. See soleCreditors for which keywords it binds and why
// it is a list rather than an invariant.
func TestKeywordsAdmitOnlyRealDetections(t *testing.T) {
	cases := declaredProbes()

	byName := map[string]Fixture{}
	for _, f := range AllFixtures() {
		byName[f.Name] = f
	}

	for name, tc := range cases {
		f, ok := byName[name]
		if !ok {
			t.Errorf("fixture %q no longer exists; update this test", name)
			continue
		}

		for _, p := range tc.hit {
			if !creditedByAnyDefect(f, p.finding) {
				t.Errorf("%s: a finding that %s is NOT credited — the keywords no longer admit a real detection: %q / %q",
					name, p.why, p.finding.Title, p.finding.Rationale)
			}
		}
		for _, p := range tc.miss {
			if creditedByAnyDefect(f, p.finding) {
				t.Errorf("%s: a finding that %s IS credited with the planted defect: %q / %q",
					name, p.why, p.finding.Title, p.finding.Rationale)
			}
		}
	}

	// Every fixture that plants something has to be exercised, or the next one
	// added inherits the silence this test was written to end.
	for _, f := range AllFixtures() {
		if !f.Clean() && cases[f.Name].hit == nil {
			t.Errorf("fixture %q plants defects but no finding is probed against it; add cases", f.Name)
		}
	}

	checkSoleCreditors(t, byName, cases)
}

// soleCreditors is the set of keywords this corpus can PROVE it needs: each
// one
// must be the only keyword crediting some hit probe, so deleting it turns a
// green test red.
//
// The note behind it is in docs/measurement.md#solecreditors.
func soleCreditors() map[string][]string {
	return map[string][]string{
		"kotlin-widened-input":   {"what summarize accepts", "parameter is wider"},
		"ruby-default-page-size": {"rows by default"},
		"php-forbidden-vs-404":   {"learns the project exists"},
		"go-package-singleton":   {"answer for the whole"},
	}
}

// checkSoleCreditors runs the deletion mutation for real: it removes one keyword
// from the defect it belongs to and asserts some hit probe stops being credited.
//
// The mutation is applied to a COPY of the Defect and scored through matches(),
// rather than reasoned about from the keyword strings, because the last round's
// redundancy was invisible to anyone reading the list, "one binary" and "whole
// binary" look independent and are not.
func checkSoleCreditors(t *testing.T, byName map[string]Fixture, cases map[string]fixtureProbes) {
	t.Helper()

	for name, keywords := range soleCreditors() {
		f, ok := byName[name]
		if !ok {
			t.Errorf("fixture %q no longer exists; update soleCreditors", name)
			continue
		}
		for _, kw := range keywords {
			held := false
			for _, d := range f.Defects {
				cut := d
				cut.Keywords = nil
				for _, k := range d.Keywords {
					if k != kw {
						cut.Keywords = append(cut.Keywords, k)
					}
				}
				if len(cut.Keywords) == len(d.Keywords) {
					continue // the keyword is not on this defect
				}
				for _, p := range cases[name].hit {
					if matches(p.finding, d) && !matches(p.finding, cut) {
						held = true
					}
				}
			}
			if !held {
				t.Errorf("%s: deleting the keyword %q loses no hit probe, so nothing in this tree "+
					"charges for it and it is pure widening of what the corpus credits. Either give it "+
					"a probe only it can credit, or delete it and drop the soleCreditors entry",
					name, kw)
			}
		}
	}
}

// probe is one finding put in front of the scorer, with the sentence that says
// what it is meant to demonstrate.
type probe struct {
	why     string
	finding review.Finding
}

// fixtureProbes is what a fixture's keywords must and must not admit.
type fixtureProbes struct {
	hit  []probe // reports the planted defect; must be credited
	miss []probe // noticed something else; must not be
}

// declaredProbes is the hand-authored half of the keyword evidence, hoisted out
// of the test that consumes it because two other tests read it as DATA.
//
// The hit list is the negative set's raw material: a finding that reports
// fixture A's plant, moved to fixture B's anchor, is review prose that says
// nothing about B, so B's keywords must not credit it. Written as a literal
// inside one test function, that material was reachable only by the assertions
// beside it, and the sixteen leaks Round 7 removed were each found by hand
// because nothing could enumerate them.
//
// Nothing here is generated. What reads it is.
func declaredProbes() map[string]fixtureProbes {
	return map[string]fixtureProbes{
		"contract-break": {
			hit: []probe{{
				"names the consequence for deployed consumers",
				review.Finding{Path: "event.go", Line: 9, Severity: "error", Category: "api",
					Title:     "Renaming this tag breaks existing clients",
					Rationale: "Consumers already deployed against created_at will silently decode a zero timestamp."},
			}},
			miss: []probe{{
				"a style objection that quotes the removed tag",
				review.Finding{Path: "event.go", Line: 9, Severity: "nit", Category: "style",
					Title:     "Inconsistent JSON tag naming",
					Rationale: "The tag was renamed from created_at to createdAt, but the rest of the package uses snake_case."},
			}},
		},

		"data-loss-migration": {
			hit: []probe{{
				"names the missing predicate and what it costs",
				review.Finding{Path: "migrations/0007_backfill_plan.sql", Line: 8, Severity: "critical", Category: "data",
					Title:     "UPDATE has no WHERE clause",
					Rationale: "This rewrites every account, not only the NULL rows, so paying plans are overwritten."},
			}},
			miss: []probe{{
				"the stock no-down-migration objection",
				review.Finding{Path: "migrations/0007_backfill_plan.sql", Line: 10, Severity: "warning", Category: "ops",
					Title:     "Migration has no down step",
					Rationale: "There is no rollback script, so a bad deploy is irreversible."},
			}},
		},

		"ts-unawaited-async": {
			hit: []probe{{
				"says the function claims to be finished when it is not",
				review.Finding{Path: "src/sync.ts", Line: 10, Severity: "error", Category: "correctness",
					Title:     "saveAll resolves before anything is stored",
					Rationale: "The callback's promise is discarded, so the function returns immediately and a failed save becomes an unhandled rejection."},
			}},
			miss: []probe{
				{"the unbounded-concurrency objection the fixture rejects",
					review.Finding{Path: "src/sync.ts", Line: 10, Severity: "warning", Category: "performance",
						Title:     "Unbounded concurrency in forEach",
						Rationale: "The saves now run in parallel rather than sequentially, which can overwhelm the backend."}},
				{"a readability preference",
					review.Finding{Path: "src/sync.ts", Line: 10, Severity: "nit", Category: "style",
						Title:     "Prefer for...of",
						Rationale: "forEach reads worse than the loop it replaced; consider reverting."}},
				{"an approving comment",
					review.Finding{Path: "src/sync.ts", Line: 10, Severity: "nit", Category: "style",
						Title: "forEach reads well here", Rationale: "Fine as written."}},
				{"a claim about the code that is false",
					review.Finding{Path: "src/sync.ts", Line: 11, Severity: "nit", Category: "performance",
						Title:     "Saves run one at a time",
						Rationale: "Each item is awaited in turn, so saveAll is serial. Consider Promise.all."}},
			},
		},

		"timezone-boundary": {
			hit: []probe{{
				"names the zone",
				review.Finding{Path: "report.go", Line: 13, Severity: "error", Category: "correctness",
					Title:     "StartOfDay returns UTC midnight",
					Rationale: "Truncate rounds from the zero time, so for a non-UTC timestamp this is the wrong instant."},
			}},
			miss: []probe{{
				"a docs nit reusing a word from the file's own comment",
				review.Finding{Path: "report.go", Line: 13, Severity: "nit", Category: "docs",
					Title:     "Document return location",
					Rationale: "Say which location the returned value carries so callers know."},
			}},
		},

		"removed-guard": {
			hit: []probe{{
				"names who can now do what",
				review.Finding{Path: "project.go", Line: 31, Severity: "critical", Category: "security",
					Title:     "Anyone can delete any project",
					Rationale: "The ownership check was removed, so an authenticated caller can delete a project it does not own."},
			}},
			miss: []probe{
				{"asks for the parameter to be dropped, making the hole permanent",
					review.Finding{Path: "project.go", Line: 30, Severity: "nit", Category: "style",
						Title:     "callerID is unused",
						Rationale: "The owner id parameter is no longer read; drop it from the signature."}},
				{"a stale-doc nit",
					review.Finding{Path: "project.go", Line: 30, Severity: "info", Category: "docs",
						Title:     "Stale doc",
						Rationale: "The comment still mentions the owner of the project."}},
			},
		},

		"retry-no-backoff": {
			hit: []probe{{
				"names the missing wait",
				review.Finding{Path: "client.py", Line: 11, Severity: "warning", Category: "resilience",
					Title:     "Retries have no backoff",
					Rationale: "Attempts fire back to back, so a struggling dependency takes five requests per call. Add a sleep with exponential backoff."},
			}},
			miss: []probe{
				{"objects to the exception breadth",
					review.Finding{Path: "client.py", Line: 14, Severity: "nit", Category: "style",
						Title:     "Catch a narrower exception",
						Rationale: "urllib.error.URLError also covers permanent failures; consider narrowing it."}},
				{"objects to the literal",
					review.Finding{Path: "client.py", Line: 11, Severity: "nit", Category: "style",
						Title:     "ATTEMPTS is a magic number",
						Rationale: "Five attempts is arbitrary; make the retry count configurable."}},
			},
		},

		"capacity-hint-nit": {
			hit: []probe{{
				"names the allocation",
				review.Finding{Path: "window.go", Line: 17, Severity: "nit", Category: "performance",
					Title:     "Capacity hint is one short",
					Rationale: "The loop appends len(samples)-n+1 times, so this forces one reallocation."},
			}},
			miss: []probe{{
				"reports a bounds bug the code does not have",
				review.Finding{Path: "window.go", Line: 18, Severity: "error", Category: "correctness",
					Title:     "Off-by-one in the loop bound",
					Rationale: "The loop runs one time too many and will index past the end of samples."},
			}},
		},

		"go-nil-deref": {
			hit: []probe{{
				"names what the discarded error costs",
				review.Finding{Path: "fetch.go", Line: 10, Severity: "error", Category: "correctness",
					Title:     "http.Get's error is discarded",
					Rationale: "On a transport failure resp is nil, so the deferred Close panics."},
			}, {
				// "nil", "panic" and "dereference" were removed from this
				// fixture's keywords because they credited prose about another
				// file entirely. Removing a keyword can silently cost recall --
				// that happened once and nothing caught it -- so each natural
				// phrasing of the real detection is pinned here.
				"says the error is unchecked without using the removed words",
				review.Finding{Path: "fetch.go", Line: 10, Severity: "error", Category: "correctness",
					Title:     "The error return is unchecked",
					Rationale: "http.Get can fail and the second value is discarded."},
			}, {
				"names the variable and the deferred call instead",
				review.Finding{Path: "fetch.go", Line: 10, Severity: "error", Category: "correctness",
					Title:     "resp may be nil here",
					Rationale: "When the request fails resp is nil and the deferred Close still runs."},
			}},
			miss: []probe{{
				"a docs request about the same function",
				review.Finding{Path: "fetch.go", Line: 9, Severity: "nit", Category: "docs",
					Title:     "Document the failure case",
					Rationale: "The comment should say what the first return value holds when the request fails."},
			}},
		},

		"go-sql-injection": {
			hit: []probe{{
				"names the interpolation and the fix",
				review.Finding{Path: "store.go", Line: 17, Severity: "error", Category: "security",
					Title:     "SQL injection in SearchUsers",
					Rationale: "name is interpolated into the statement; pass it as a placeholder instead."},
			}},
			miss: []probe{{
				"an unrelated correctness point about the same query",
				review.Finding{Path: "store.go", Line: 17, Severity: "nit", Category: "performance",
					Title:     "No LIMIT on the result set",
					Rationale: "A broad name match returns every matching row; consider paginating."},
			}},
		},

		"go-hardcoded-secret": {
			hit: []probe{{
				"names the committed credential",
				review.Finding{Path: "client.go", Line: 12, Severity: "critical", Category: "security",
					Title:     "Hardcoded API key",
					Rationale: "A live credential is committed to source; remove it and rotate the key."},
			}},
			miss: []probe{{
				"an observability suggestion about the fallback",
				review.Finding{Path: "client.go", Line: 11, Severity: "info", Category: "ops",
					Title:     "Log which value is in use",
					Rationale: "When the environment variable is empty an operator cannot tell which value the process picked."},
			}},
		},

		"python-command-injection": {
			hit: []probe{{
				// Incumbent's real finding, from the shipped cache.
				"names the shell and the injection",
				review.Finding{Path: "tools.py", Line: 10, EndLine: 12, Severity: "error", Category: "Security & Privacy",
					Title:     "Do not interpolate name into a shell command.",
					Rationale: "os.system allows shell injection when name contains shell metacharacters."},
			}},
			miss: []probe{{
				// Also Incumbent's, from the same cached review. It matched the
				// injection plant through the keyword "subprocess", the word its
				// own recommended fix uses, and was then graded for the severity
				// of a bug it never mentions.
				"is about tar's ignored exit status and recommends subprocess",
				review.Finding{Path: "tools.py", Line: 12, Severity: "warning", Category: "Stability & Availability",
					Title: "Propagate archive failures.",
					Rationale: "The function ignores the tar exit status. Missing directories, permission errors, " +
						"or a failed tar command can leave no archive while the caller receives no error. " +
						"Use subprocess.run(..., check=True) or explicitly handle the exit status."},
			}},
		},

		// The warning plants. Each miss below is the false positive its fixture's
		// own doc comment names, and each is anchored WITHIN anchorTolerance of
		// the plant wherever the objection would really be made there, otherwise
		// the exclusion could be coming from the line number and the keyword list
		// would be untested. The two exceptions are marked where they occur.
		"ts-unbounded-memo-key": {
			hit: []probe{
				{"names the key space and what the table does with it",
					review.Finding{Path: "src/search.ts", Line: 13, Severity: "warning", Category: "resource",
						Title:     "Memo table is keyed by raw search text",
						Rationale: "remember() never releases an entry, so the table gains one per distinct query and the process grows until it is killed."}},
				{"names the memory and the lifetime",
					review.Finding{Path: "src/search.ts", Line: 13, Severity: "warning", Category: "performance",
						Title:     "Do not memoize user-supplied queries",
						Rationale: "Entries here are never released, so memory is retained for every string anyone searches for."}},
			},
			miss: []probe{
				{"the staleness objection, which quotes cache.ts's own words about the process lifetime",
					review.Finding{Path: "src/search.ts", Line: 13, Severity: "warning", Category: "correctness",
						Title:     "Cached results never refresh",
						Rationale: "A document renamed after its first search keeps its old title forever, because entries are held for the lifetime of the process."}},
				{"a rate-limit objection that reaches for \"unbounded\" about the REQUEST rate",
					review.Finding{Path: "src/search.ts", Line: 13, Severity: "warning", Category: "resilience",
						Title:     "No rate limit on search",
						Rationale: "Any client can issue an unbounded number of searches, so one caller can saturate the upstream."}},
				// Anchored in the OTHER file on purpose: cache.ts is correct as
				// documented and plans.ts uses it correctly, so the defect is the
				// call site. This probe is the receipt for that judgement being
				// deliberate, it fails by PATH, not by keyword, and if a later
				// editor decides the helper is the defect this is the line that
				// tells them the current answer was chosen rather than inherited.
				{"reports the growth but blames the helper instead of the caller that broke its contract",
					review.Finding{Path: "src/cache.ts", Line: 3, Severity: "warning", Category: "resource",
						Title:     "Map grows without bound",
						Rationale: "table never evicts, so memory grows forever."}},
				// The staleness objection in the words a reviewer writes it in.
				// "grow stale" is ordinary English for it, and the bare stem
				// "grow" was in the keyword list, so this was credited with the
				// growth plant. The probe above passed only because it was
				// phrased "never refresh ... held for the lifetime".
				{"the staleness objection, saying results \"grow stale\"",
					review.Finding{Path: "src/search.ts", Line: 13, Severity: "warning", Category: "correctness",
						Title:     "Memoized search results are never invalidated",
						Rationale: "remember() holds the first answer for the life of the process, so results grow stale as documents are re-titled and there is no way to evict an entry."}},
			},
		},

		"go-cancel-goroutine-leak": {
			hit: []probe{
				{"names the channel and what blocks on it",
					review.Finding{Path: "resolve.go", Line: 24, Severity: "warning", Category: "resource",
						Title:     "Unbuffered channel leaks the goroutine",
						Rationale: "When ctx is done first nothing ever receives, so the send blocks forever and the goroutine is never reclaimed."}},
				// Anchored at the SEND, four lines below the plant and exactly on
				// anchorTolerance. A reviewer that points at the blocked send has
				// found the same defect and must not lose recall to a judgement
				// about which of two adjacent lines the fix belongs on.
				{"anchors at the send rather than the make, at the edge of the tolerance",
					review.Finding{Path: "resolve.go", Line: 28, Severity: "warning", Category: "resource",
						Title:     "Send has no receiver after a timeout",
						Rationale: "Once ResolveContext has returned on ctx.Done there is no receiver, and this goroutine is leaked."}},
			},
			miss: []probe{
				{"the interface design objection, which is about cancelling the LOOKUP",
					review.Finding{Path: "resolve.go", Line: 23, Severity: "info", Category: "design",
						Title:     "Directory.Lookup should take a context",
						Rationale: "The lookup itself is not cancelled and keeps running after ResolveContext gives up; add a context-aware method to the interface."}},
				{"an error-wrapping preference on the same function",
					review.Finding{Path: "resolve.go", Line: 26, Severity: "nit", Category: "style",
						Title:     "Wrap the context error",
						Rationale: "Returning ctx.Err() bare loses which name was being resolved."}},
			},
		},

		"python-timing-unsafe-hmac": {
			hit: []probe{{
				"names the comparison and the fix",
				review.Finding{Path: "webhook.py", Line: 15, Severity: "warning", Category: "security",
					Title:     "Signature is compared in variable time",
					Rationale: "== returns at the first differing byte, so use hmac.compare_digest instead."},
			}},
			miss: []probe{
				{"the module-level secret objection",
					review.Finding{Path: "webhook.py", Line: 15, Severity: "warning", Category: "reliability",
						Title:     "Missing WEBHOOK_SECRET crashes at import",
						Rationale: "SECRET is read at module scope, so an unset variable raises KeyError before any handler runs."}},
				{"the replay objection, which says \"timestamp\" without saying \"timing\"",
					review.Finding{Path: "webhook.py", Line: 15, Severity: "warning", Category: "security",
						Title:     "No replay protection",
						Rationale: "verify says nothing about a timestamp, so a captured request can be sent again indefinitely."}},
				{"the case-sensitivity remark, which is why bare \"compare\" is not a keyword",
					review.Finding{Path: "webhook.py", Line: 15, Severity: "nit", Category: "correctness",
						Title:     "Hex digests should be compared case-insensitively",
						Rationale: "A sender that transmits uppercase hex is rejected; normalize both sides before comparing them."}},
			},
		},

		"csharp-client-per-request": {
			hit: []probe{{
				"names the sockets and the reuse",
				review.Finding{Path: "src/Notifier.cs", Line: 17, Severity: "warning", Category: "resource",
					Title:     "A new HttpClient per call exhausts sockets",
					Rationale: "Each instance brings its own connection pool and disposing it leaves sockets in TIME_WAIT; reuse one client."},
			}},
			miss: []probe{
				{"the timeout objection, which is near-wrong here since the default was not touched",
					review.Finding{Path: "src/Notifier.cs", Line: 17, Severity: "warning", Category: "reliability",
						Title:     "No explicit timeout",
						Rationale: "Set an explicit Timeout so a hung endpoint cannot stall the alert path."}},
				{"a disposal objection the using statement already handles",
					review.Finding{Path: "src/Notifier.cs", Line: 19, Severity: "nit", Category: "error-handling",
						Title:     "EnsureSuccessStatusCode throws",
						Rationale: "A non-2xx status raises before the caller can log the body; catch it and include the message."}},
				// The three below are the same two objections written in
				// ordinary English rather than in wording that dodges the
				// keyword list. Each was CREDITED with the socket plant until
				// the bare token "port" came out, because "port" is a substring
				// of important, support and reports. The pair above passed
				// throughout: they were phrased without those words, so they
				// tested the phrasing rather than the keywords.
				{"the timeout objection, saying \"important\"",
					review.Finding{Path: "src/Notifier.cs", Line: 17, Severity: "warning", Category: "reliability",
						Title:     "No explicit timeout on the HttpClient",
						Rationale: "The default is 100 seconds. For an alert path that is far too long; it is important to set an explicit, short timeout here."}},
				{"the timeout objection, saying \"support\"",
					review.Finding{Path: "src/Notifier.cs", Line: 17, Severity: "warning", Category: "reliability",
						Title:     "Set HttpClient.Timeout explicitly",
						Rationale: "HttpClient does not support a per-request deadline here, so a hung webhook blocks the alert path."}},
				{"the disposal objection, saying \"reports\"",
					review.Finding{Path: "src/Notifier.cs", Line: 19, Severity: "nit", Category: "error-handling",
						Title:     "Response is not disposed when EnsureSuccessStatusCode throws",
						Rationale: "If the webhook reports a non-2xx status the exception propagates. It is important that the response is released on that path too."}},
			},
		},

		"bash-fixed-temp-path": {
			hit: []probe{{
				"names who else can reach the path, and the fix",
				review.Finding{Path: "scripts/release-notes.sh", Line: 8, Severity: "warning", Category: "security",
					Title:     "Fixed path in /tmp",
					Rationale: "Another user on the host can pre-create this as a symlink and redirect the write; use mktemp."},
			}},
			miss: []probe{
				{"the cleanup objection",
					review.Finding{Path: "scripts/release-notes.sh", Line: 8, Severity: "nit", Category: "maintainability",
						Title:     "Temp file is never removed",
						Rationale: "Nothing deletes the file when the script exits; add a trap to clean it up."}},
				{"an unrelated resilience suggestion on the adjacent line",
					review.Finding{Path: "scripts/release-notes.sh", Line: 9, Severity: "nit", Category: "resilience",
						Title:     "curl has no retry",
						Rationale: "A transient network failure aborts the whole script; add --retry."}},
				// The cleanup objection ARRIVING WITH ITS OWN FIX, which is
				// what a real reviewer writes. OUT=$(mktemp) plus a trap is the
				// idiomatic answer to "nothing removes the file", so the word
				// mktemp reached this plant attached to the false positive at
				// least as often as to the security finding, and it was
				// credited until the keyword came out. The probe above passed
				// only because its fix was worded "add a trap to clean it up".
				{"the cleanup objection carrying the mktemp fix",
					review.Finding{Path: "scripts/release-notes.sh", Line: 8, Severity: "nit", Category: "maintainability",
						Title:     "The temporary file is never removed",
						Rationale: "Nothing deletes the JSON after the two jq reads, so each run leaves a file behind. Use OUT=$(mktemp) and trap 'rm -f \"$OUT\"' EXIT."}},
			},
		},

		// The fixtures whose plants are rated nit.
		"cross-file-copy-nit": {
			hit: []probe{
				{"names the contract the callee already provides",
					review.Finding{Path: "report/summary.go", Line: 17, Severity: "nit", Category: "performance",
						Title:     "Snapshot already returns a copy",
						Rationale: "The slice is built under the lock and shares no backing array, so this allocates a second slice for nothing."}},
				{"names the redundancy directly",
					review.Finding{Path: "report/summary.go", Line: 17, Severity: "nit", Category: "performance",
						Title:     "Copy of a copy",
						Rationale: "The store hands this slice over exclusively; copying it again buys nothing."}},
			},
			miss: []probe{
				{"the hallucination the cross-file contract refutes",
					review.Finding{Path: "report/summary.go", Line: 17, Severity: "error", Category: "concurrency",
						Title:     "The store may append after Snapshot returns",
						Rationale: "Another goroutine calling Add could race with this read and leave the summary stale."}},
				{"a design remark about the struct",
					review.Finding{Path: "report/summary.go", Line: 18, Severity: "nit", Category: "maintainability",
						Title:     "Total duplicates len(Events)",
						Rationale: "Summary.Total is always the length of Events; compute it at render time."}},
			},
		},

		"cross-file-sort-nit": {
			hit: []probe{
				{"names the order the callee already guarantees",
					review.Finding{Path: "src/roster.ts", Line: 6, Severity: "nit", Category: "performance",
						Title:     "listMembers already returns them sorted",
						Rationale: "The array arrives in display-name order, so this orders it a second time for an identical result."}},
				{"names the redundancy directly",
					review.Finding{Path: "src/roster.ts", Line: 6, Severity: "nit", Category: "performance",
						Title:     "Redundant sort",
						Rationale: "The roster is already sorted when it arrives here; drop the re-sort."}},
			},
			miss: []probe{
				{"the in-place-mutation objection both spreads refute",
					review.Finding{Path: "src/roster.ts", Line: 6, Severity: "error", Category: "correctness",
						Title:     "sort mutates in place",
						Rationale: "Array.prototype.sort reorders the caller's array; copy before ordering to avoid corrupting the roster's storage."}},
				{"the duplicated-comparator finding, whose fix KEEPS the redundant sort",
					review.Finding{Path: "src/roster.ts", Line: 6, Severity: "nit", Category: "maintainability",
						Title:     "Inline comparator duplicates byDisplayName",
						Rationale: "members.ts exports the same comparison; import it rather than repeating the localeCompare call."}},
			},
		},

		"sorted-for-min-nit": {
			hit: []probe{{
				"names the cheaper call and what the current one copies",
				review.Finding{Path: "sensors.py", Line: 23, Severity: "nit", Category: "performance",
					Title:     "Use min() instead of ordering the whole list",
					Rationale: "sorted() copies and orders every element to read one; min(readings, key=...) walks it once."},
			}},
			miss: []probe{
				{"the empty-list objection the two lines above already handle",
					review.Finding{Path: "sensors.py", Line: 23, Severity: "error", Category: "correctness",
						Title:     "IndexError on an empty list",
						Rationale: "Indexing [0] raises when there are no readings at all."}},
				{"a sample-count suggestion, which is why the keyword carries its parenthesis",
					review.Finding{Path: "sensors.py", Line: 23, Severity: "info", Category: "design",
						Title:     "No minimum sample count",
						Rationale: "coldest answers from a single reading; require a minimum number of samples first."}},
			},
		},

		"duplicate-test-case-nit": {
			hit: []probe{{
				"names which case it repeats and what that costs",
				review.Finding{Path: "slug/slug_test.go", Line: 16, Severity: "nit", Category: "tests",
					Title:     "Duplicate table case",
					Rationale: "This is identical to the \"replaces spaces\" case above, so it runs one assertion twice and adds no coverage."},
			}},
			miss: []probe{
				{"a finding about cases that are ABSENT rather than the one repeated",
					review.Finding{Path: "slug/slug_test.go", Line: 16, Severity: "warning", Category: "tests",
						Title:     "No case covers the empty string",
						Rationale: "Slug(\"\") is never exercised; add a case for it and for unicode input."}},
				{"observes that the expectations repeat, which is why the keyword carries its preposition",
					review.Finding{Path: "slug/slug_test.go", Line: 15, Severity: "nit", Category: "tests",
						Title:     "Five cases expect the same output",
						Rationale: "Most rows are identical in their expectation, which makes a failure hard to localize."}},
				// The same observation in the two words a reviewer reaches for
				// first. "identical to" was chosen with its preposition
				// to exclude exactly this reviewer, and then "duplicate",
				// "duplicates" and "repeats" were put in the same list and let
				// it back in: both of these were credited with finding the
				// repeated CASE while talking about the repeated COLUMN, and
				// both sit inside anchorTolerance of line 16 so distance
				// excludes neither.
				{"the repeated-column objection, saying \"duplicate\"",
					review.Finding{Path: "slug/slug_test.go", Line: 14, Severity: "nit", Category: "tests",
						Title:     "Repeated expected value across the table",
						Rationale: "Five of the six cases duplicate the same expected output \"hello-world\". Consider deriving it once."}},
				{"the repeated-column objection, saying \"repeats\"",
					review.Finding{Path: "slug/slug_test.go", Line: 15, Severity: "nit", Category: "tests",
						Title:     "The table repeats one expectation",
						Rationale: "The want column repeats \"hello-world\" for nearly every row, which makes the table hard to scan."}},
			},
		},

		"defensive-copy-nit": {
			hit: []probe{{
				"names reachability rather than the copy",
				review.Finding{Path: "src/main/java/com/example/report/Labels.java", Line: 26, Severity: "nit", Category: "performance",
					Title:     "labels never escapes forIds",
					Rationale: "The list is built here and no other reference to it exists, so wrapping it directly is already immutable."},
			}},
			miss: []probe{
				{"a real but unrelated null objection shared with the whole file",
					review.Finding{Path: "src/main/java/com/example/report/Labels.java", Line: 26, Severity: "warning", Category: "correctness",
						Title:     "NPE when ids is null",
						Rationale: "forIds dereferences ids.size() with no null check."}},
				{"a tidier spelling that copies exactly as much",
					review.Finding{Path: "src/main/java/com/example/report/Labels.java", Line: 26, Severity: "nit", Category: "style",
						Title:     "Prefer List.copyOf",
						Rationale: "List.copyOf(labels) is the modern spelling and returns an unmodifiable list in one call."}},
				// The same suggestion using the ordinary phrase for a double
				// wrap. It was credited until "unnecessary copy", "redundant
				// copy", "needless copy" and "no need to copy" came out, four
				// keywords that contradicted this defect's own comment, which
				// says the list is built on reachability rather than on "copy".
				// A reviewer proposing List.copyOf has made none of the escape
				// judgement the plant is about: List.copyOf allocates the same
				// second list. The probe above passed only because it avoided
				// the word copy entirely.
				{"the same suggestion calling it a redundant copy",
					review.Finding{Path: "src/main/java/com/example/report/Labels.java", Line: 26, Severity: "nit", Category: "style",
						Title:     "Prefer List.copyOf",
						Rationale: "Collections.unmodifiableList(new ArrayList<>(labels)) is a redundant copy spelled the long way; List.copyOf(labels) returns an unmodifiable copy in one call."}},
				{"the same suggestion calling it an unnecessary copy",
					review.Finding{Path: "src/main/java/com/example/report/Labels.java", Line: 26, Severity: "nit", Category: "style",
						Title:     "Use List.copyOf instead of wrapping an ArrayList",
						Rationale: "Wrapping a fresh ArrayList in unmodifiableList is an unnecessary copy of an idiom the JDK already provides. List.copyOf does the same thing."}},
			},
		},

		// The info plants. Every miss below was CREDITED when these five were
		// written, and every one was found by running it through matches()
		// rather than by reading the keyword list, which is the only way any
		// of them could have been found, since each list had a paragraph beside
		// it asserting the opposite. The fixtures' own comments name the
		// objections; these are those objections, in the words a reviewer would
		// use.
		"kotlin-widened-input": {
			hit: []probe{
				{"names the widening and what has not arrived yet",
					review.Finding{Path: "src/main/kotlin/com/example/report/Summary.kt", Line: 27, Severity: "info", Category: "maintainability",
						Title:     "summarize accepts a wider type than anything calls it with",
						Rationale: "There is only one implementation and no second caller in this change, and a public parameter cannot be narrowed back."}},
				{"names the widening in the other common phrasing",
					review.Finding{Path: "src/main/kotlin/com/example/report/Summary.kt", Line: 27, Severity: "info", Category: "maintainability",
						Title:     "Is the abstraction needed yet?",
						Rationale: "This widens summarize's parameter to an interface for a weekly mail that has not landed."}},
				// The two below came back MISSED after Round 7's removals and are
				// the receipts for the recall half of this change. Neither says
				// anything a reviewer would have to be told: one names what
				// summarize accepts, the other names its parameter. Round 7
				// removed sixteen keywords with a miss probe each and probed the
				// recall direction nowhere, so a reviewer that found this plant
				// and said so plainly was scored a miss on both.
				{"names the widening without using a compound the list happened to carry",
					review.Finding{Path: "src/main/kotlin/com/example/report/Summary.kt", Line: 27, Severity: "info", Category: "design",
						Title:     "Wider than it needs to be",
						Rationale: "This widens what summarize accepts before anything but DailyReport needs it."}},
				{"names the parameter and that nothing calls it that way",
					review.Finding{Path: "src/main/kotlin/com/example/report/Summary.kt", Line: 27, Severity: "info", Category: "design",
						Title:     "Abstraction ahead of a caller",
						Rationale: "summarize's parameter is wider than anything that calls it today."}},
			},
			miss: []probe{
				// The objection this fixture declines to credit, in its plainest
				// form. "public api", "public surface" and "surface area" were
				// all keywords, so it scored full recall while saying nothing
				// about the parameter.
				{"the pure visibility objection",
					review.Finding{Path: "src/main/kotlin/com/example/report/Summary.kt", Line: 27, Severity: "nit", Category: "api",
						Title:     "Make Summarizable internal",
						Rationale: "This adds public API surface area for something the module uses itself; internal would keep the public surface smaller."}},
				// The same objection reaching for the verb. It is why the bare
				// stems "widen", "wider" and "widening" are gone: the public
				// surface is a thing that gets wider too.
				{"the visibility objection using the widening verb",
					review.Finding{Path: "src/main/kotlin/com/example/report/Summary.kt", Line: 27, Severity: "nit", Category: "api",
						Title:     "Keep Summarizable internal",
						Rationale: "Publishing this type widens what the module exposes; internal costs nothing here."}},
				{"the aliasing remark, which the generation scope excludes",
					review.Finding{Path: "src/main/kotlin/com/example/report/Summary.kt", Line: 21, Severity: "nit", Category: "style",
						Title:     "Two names for the same field",
						Rationale: "label, gained and lost are aliases of day, signups and cancellations; pick one set and rename the other."}},
				// The visibility objection reaching for the third word for
				// something getting bigger. "open set" survived the round that
				// removed "public surface" and "widen" for exactly this reason
				// and was credited here in full: a type published to the world is
				// an open set, and this sentence has still noticed nothing about
				// what summarize takes.
				{"the visibility objection calling the published type an open set",
					review.Finding{Path: "src/main/kotlin/com/example/report/Summary.kt", Line: 27, Severity: "nit", Category: "api",
						Title:     "Do not publish Summarizable",
						Rationale: "Publishing this interface exposes an open set of types to the world; keep it internal."}},
				// The three below charge the recall keywords added for this
				// plant, and each one was CREDITED by the first attempt at them.
				// "summarize's parameter" and "wider than anything" went in
				// under a comment claiming all the additions "name summarize's
				// INPUT, which is the one thing the visibility objection never
				// mentions"; the first two probes here are the visibility
				// objection doing exactly that. The narrowed forms, "what
				// summarize accepts" and "parameter is wider", deny all three.
				{"the visibility objection naming the parameter to say it is fine",
					review.Finding{Path: "src/main/kotlin/com/example/report/Summary.kt", Line: 27, Severity: "nit", Category: "api",
						Title:     "Mark Summarizable internal",
						Rationale: "Summarizable is public with no consumer outside this module; mark it internal. summarize's parameter can stay as it is."}},
				{"the visibility objection reaching for a bare comparative",
					review.Finding{Path: "src/main/kotlin/com/example/report/Summary.kt", Line: 27, Severity: "nit", Category: "api",
						Title:     "Narrow the published surface",
						Rationale: "The public surface here is wider than anything the module needed; keep Summarizable internal."}},
				{"a locale remark that names the call and not the input",
					review.Finding{Path: "src/main/kotlin/com/example/report/Summary.kt", Line: 27, Severity: "warning", Category: "correctness",
						Title:     "String interpolation uses the default locale",
						Rationale: "String.format uses the default locale here; summarize accepts a Summarizable and returns a String that changes per JVM."}},
			},
		},

		"php-forbidden-vs-404": {
			hit: []probe{
				{"names what the denial confirms",
					review.Finding{Path: "src/Http/ProjectController.php", Line: 23, Severity: "info", Category: "security",
						Title:     "403 tells the caller the project is there",
						Rationale: "Answering 403 confirms that a project with that id exists to anyone holding one; returning the same 404 as above would not."}},
				{"names the disclosure without quoting a status code",
					review.Finding{Path: "src/Http/ProjectController.php", Line: 23, Severity: "info", Category: "security",
						Title:     "Denial reveals existence",
						Rationale: "A non-member learns whether the project exists, because the two denials are distinguishable."}},
				// The same finding without the word `whether`, which is all it
				// took to lose it: both surviving "exists" forms require `that`
				// or `whether`, so this plain statement of the disclosure came
				// back MISSED after Round 7.
				{"says what the non-member learns, in the plainest form",
					review.Finding{Path: "src/Http/ProjectController.php", Line: 23, Severity: "info", Category: "security",
						Title:     "403 answers a question the 404 did not",
						Rationale: "A non-member now learns the project exists; the 404 above would not have told them."}},
			},
			miss: []probe{
				// "exists" was a keyword. A remark about test coverage, in a
				// fixture whose plant is an information disclosure, scored as a
				// security detection on an ordinary English verb.
				{"a missing-test remark using the bare verb",
					review.Finding{Path: "src/Http/ProjectController.php", Line: 22, Severity: "nit", Category: "tests",
						Title:     "No coverage for the new branch",
						Rationale: "No test exists for the non-member path, so the new check could be inverted and nothing would fail."}},
				// "enumerat" was a keyword, and this fixture's own comment says
				// this finding must not be credited: the check it reports as
				// missing is in the diff.
				{"the IDOR hallucination about a check that is present",
					review.Finding{Path: "src/Http/ProjectController.php", Line: 22, Severity: "critical", Category: "security",
						Title:     "Insecure direct object reference",
						Rationale: "Any signed-in caller can enumerate project ids and read projects they do not own; there is no authorization check on this endpoint."}},
				{"the same hallucination without the word enumerate",
					review.Finding{Path: "src/Http/ProjectController.php", Line: 22, Severity: "critical", Category: "security",
						Title:     "Missing ownership check",
						Rationale: "A caller can guess ids and read other people's projects; add an access control check before returning the payload."}},
				// "hide" and "hides" were keywords, and this is the remark that
				// types them: it asks the BODY to say less and leaves the
				// disclosure exactly where it is.
				{"the error-body consistency remark",
					review.Finding{Path: "src/Http/ProjectController.php", Line: 23, Severity: "nit", Category: "api",
						Title:     "Error bodies differ in shape",
						Rationale: "The 403 body should hide internal details and match the 404 above; consider a shared error envelope."}},
				// The same remark DESCRIBING the branch it is about, which is why
				// "exists" survives only in forms that require the sentence to be
				// about the disclosure.
				{"the error-body remark describing when the branch runs",
					review.Finding{Path: "src/Http/ProjectController.php", Line: 23, Severity: "nit", Category: "api",
						Title:     "Two error shapes",
						Rationale: "When the project exists but the viewer is not a member the body is {error: forbidden}, which does not match the envelope used elsewhere."}},
				// The error-body remark in the words that make this plant's
				// vocabulary problem visible. "same response" was a keyword, and
				// this sentence is the same WORDS as the finding with the
				// assertion reversed: the detection says the two denials are
				// distinguishable, this asks that they be made the same. Nothing
				// in either sentence separates them but intent.
				{"the error-body remark asking for one envelope",
					review.Finding{Path: "src/Http/ProjectController.php", Line: 23, Severity: "nit", Category: "api",
						Title:     "One error envelope",
						Rationale: "The two denials should return the same response envelope."}},
				// The same remark in the wording that charges "identical
				// response". That keyword was struck out beside "same response"
				// and, restored alone, changed no verdict on this fixture, it
				// was removed on a reading rather than on a run. The removal is
				// right and this probe is what makes it cost something: putting
				// the keyword back fails here.
				{"the error-body remark asking for one body",
					review.Finding{Path: "src/Http/ProjectController.php", Line: 23, Severity: "nit", Category: "api",
						Title:     "Denials should look alike",
						Rationale: "Both branches should send an identical response body so clients have one shape to parse."}},
				// The two below charge the recall keyword added for this plant.
				// "now learns" and "learns the project" went in under a comment
				// claiming each "requires the sentence to say what the caller
				// LEARNS"; neither does, and both of these were credited. What
				// is left, "learns the project exists", requires the sentence
				// to name what is learned about EXISTENCE, which is the rule the
				// surviving "whether"/"that" forms already follow.
				{"an N+1 remark about the second repository call",
					review.Finding{Path: "src/Http/ProjectController.php", Line: 23, Severity: "nit", Category: "performance",
						Title:     "Two repository round trips",
						Rationale: "find() and isMember() each hit the repository; the second now learns nothing the first did not already have, so pass the project through."}},
				{"an input-validation nit about the router arguments",
					review.Finding{Path: "src/Http/ProjectController.php", Line: 23, Severity: "nit", Category: "correctness",
						Title:     "Ids arrive as bare strings",
						Rationale: "show() learns the project id and the viewer id from the router with no type check beyond string."}},
			},
		},

		"go-package-singleton": {
			hit: []probe{
				{"names the global and what it costs a test",
					review.Finding{Path: "features/features.go", Line: 31, Severity: "info", Category: "maintainability",
						Title:     "Default is global state",
						Rationale: "This introduces a global variable fixed at process start, so a test that wants a different set has to reach into the package and put it back."}},
				{"asks for the parameter back",
					review.Finding{Path: "features/features.go", Line: 31, Severity: "info", Category: "maintainability",
						Title:     "Prefer passing the Set",
						Rationale: "Load already returns a *Set; passing it as a parameter costs one argument and keeps two consumers in one binary able to differ."}},
				// The finding naming the SCOPE rather than the pattern, which is
				// what a reviewer writes once "package-level" is off the list: it
				// came back MISSED, because every remaining keyword names the
				// shape of the thing and none names how far it reaches.
				{"names what the default fixes and what to do instead",
					review.Finding{Path: "features/features.go", Line: 31, Severity: "info", Category: "design",
						Title:     "One answer for the process",
						Rationale: "Adding a package-level default fixes the answer for the whole binary; keep returning a *Set and let callers hold it."}},
			},
			miss: []probe{
				// "package-level" was a keyword and is in the change's own added
				// doc comment three lines from the plant, so this nit, which
				// noticed nothing and asks for prose, scored full recall by
				// quoting the diff back at it.
				{"a docs nit quoting the change's own comment",
					review.Finding{Path: "features/features.go", Line: 30, Severity: "nit", Category: "docs",
						Title:     "Document what the package-level helpers read",
						Rationale: "The comment says Default is the Set the package-level helpers read; say which variable a caller should set instead."}},
				{"a docs nit using the phrase from its own vocabulary",
					review.Finding{Path: "features/features.go", Line: 31, Severity: "nit", Category: "docs",
						Title:     "Name the variable in the doc",
						Rationale: "The doc for this package-level variable does not say which environment variable feeds it."}},
				{"the configuration objection, which accepts the singleton",
					review.Finding{Path: "features/features.go", Line: 31, Severity: "warning", Category: "config",
						Title:     "FEATURES is read without validation",
						Rationale: "os.Getenv returns an empty string when the variable is unset, so a typo in the deployment silently yields no features. Validate it or log the parsed set at startup."}},
				{"a race the type's own doc rules out",
					review.Finding{Path: "features/features.go", Line: 31, Severity: "error", Category: "concurrency",
						Title:     "Concurrent map access",
						Rationale: "The map inside Default is shared between goroutines with no mutex, which is a data race."}},
				// The three below charge the recall keyword added for this
				// plant. "whole binary", "one binary" and "reach into the
				// package" went in under a comment claiming "none is reachable
				// by quoting: each one names the standing cost rather than the
				// location", and each of these was credited by one of them. The
				// first is the configuration objection above wearing the scope
				// word; the second is a naming nit reaching a ten-character
				// substring; the third is the docs nit that already caught
				// "package-level", quoting the diff's own added comment.
				{"the configuration objection wearing the scope word",
					review.Finding{Path: "features/features.go", Line: 31, Severity: "warning", Category: "config",
						Title:     "A typo in FEATURES fails silently",
						Rationale: "Load silently drops empty entries, so a typo in FEATURES turns a flag off for the whole binary with no error."}},
				{"a naming nit about the function and the method",
					review.Finding{Path: "features/features.go", Line: 34, Severity: "nit", Category: "style",
						Title:     "Two Enabled symbols",
						Rationale: "The package function Enabled and the method Enabled differ by one binary decision at the call site and will be confused."}},
				{"a docs nit quoting the change's own comment a second way",
					review.Finding{Path: "features/features.go", Line: 30, Severity: "nit", Category: "docs",
						Title:     "Say what a caller should set",
						Rationale: "The comment says a caller does not have to thread a *Set through every layer, but a caller that wants to reach into the package still can."}},
			},
		},

		"rust-crate-for-one-call": {
			hit: []probe{{
				"names the ratio the decision turns on",
				review.Finding{Path: "Cargo.toml", Line: 7, Severity: "info", Category: "maintainability",
					Title:     "A whole crate for one call site",
					Rationale: "humantime is used once, and every build and audit carries it from now on."},
			}},
			miss: []probe{
				{"the version-pinning objection, which accepts the dependency",
					review.Finding{Path: "Cargo.toml", Line: 7, Severity: "warning", Category: "supply-chain",
						Title:     "Unpinned dependency",
						Rationale: "humantime = \"2\" accepts any 2.x release; pin an exact version or commit the lockfile so the build is reproducible."}},
				{"the output-format remark, which reports an intended consequence",
					review.Finding{Path: "src/report.rs", Line: 14, Severity: "nit", Category: "ux",
						Title:     "Sub-second components now render",
						Rationale: "A job that took 5.2s renders as 5s 200ms, which is noisier than the old seconds count."}},
			},
		},

		"ruby-default-page-size": {
			hit: []probe{
				{"names the consumers the change does not mention",
					review.Finding{Path: "app/queries/comments_query.rb", Line: 9, Severity: "info", Category: "resource",
						Title:     "Every consumer pays for the web client's default",
						Rationale: "The mobile client and the RSS job never asked for this and now serialize four times the rows."}},
				{"names the callers that do not pass it",
					review.Finding{Path: "app/queries/comments_query.rb", Line: 9, Severity: "info", Category: "resource",
						Title:     "A larger default for everyone",
						Rationale: "Every caller that does not pass per_page now receives 100 rows instead of 25."}},
				// This fixture's OWN PROPOSED FIX, which came back MISSED. The
				// doc comment argues that the alternative is "one keyword
				// argument at the one call site that wanted it"; a reviewer that
				// proposes exactly that scores no recall once every phrase
				// naming the new default is removed, and nothing measures what
				// that removal costs. "rows by default" is what credits it,
				// and it is charged below by the offset objection wearing the
				// bare "by default" that was tried first.
				//
				// The same fix stated as a LOCATION, "setting it at the call
				// site leaves the mobile client alone", was a fourth probe
				// here, credited by "the call site". That keyword is a strict
				// superset of the "at the call site" it replaced and is gone
				// with it, so the sentence is uncredited and the probe would be
				// asserting the opposite of what the scorer does.
				// TestTheInfoRecallThisInstrumentCannotBuy runs it and records
				// the price instead.
				{"proposes the fix the fixture's own comment argues for",
					review.Finding{Path: "app/queries/comments_query.rb", Line: 9, Severity: "info", Category: "design",
						Title:     "Set this where it is needed",
						Rationale: "Every consumer of CommentsQuery now gets 100 rows by default; the web client could pass per_page: 100 itself."}},
			},
			miss: []probe{
				// "pass per_page", "rows per request" and "payload" were all
				// keywords, and this objection, which accepts the new default
				// entirely and argues about a ceiling the change did not touch,
				// matched three at once, inside anchorTolerance of the plant.
				{"the cap objection, about a line this change did not touch",
					review.Finding{Path: "app/queries/comments_query.rb", Line: 9, Severity: "warning", Category: "resource",
						Title:     "MAX_PER_PAGE is still 200",
						Rationale: "A client can still pass per_page: 200 and get 200 rows per request; consider lowering the cap now that the payload is larger."}},
				// The same objection opening the way a real detection opens,
				// which is why the bare "every caller" and "all callers" are
				// gone and the caller keywords are all negative.
				{"the cap objection phrased about every caller",
					review.Finding{Path: "app/queries/comments_query.rb", Line: 9, Severity: "warning", Category: "resource",
						Title:     "The cap is too high",
						Rationale: "Every caller can still request up to MAX_PER_PAGE, so the worst case is unchanged at 200 rows."}},
				{"the offset-pagination objection about the same method",
					review.Finding{Path: "app/queries/comments_query.rb", Line: 12, Severity: "warning", Category: "performance",
						Title:     "Offset pagination scans discarded rows",
						Rationale: "Deep pages make the database read and throw away every earlier row; keyset pagination would not."}},
				// The cap objection one word narrower than the version "every
				// caller" was removed for. "other caller" and "other consumer"
				// replaced it and neither names a caller NEGATIVELY, which is
				// the rule that list settles on: this sentence accepts the new
				// default entirely and was credited in full.
				{"the cap objection phrased about the other consumers",
					review.Finding{Path: "app/queries/comments_query.rb", Line: 9, Severity: "warning", Category: "resource",
						Title:     "The ceiling is the real problem",
						Rationale: "Other consumers can still request up to MAX_PER_PAGE, so the cap is the real ceiling here."}},
				// The two below charge the recall keyword added for this plant.
				// The bare "by default" went in first and credited the offset
				// objection, a frozen-string-literal nit and the cap objection
				// with four ordinary words appended; "rows by default" denies
				// all three, because no objection here can call 200 a default or
				// count returned rows. The second probe charges "the call site",
				// which replaced "at the call site" and credited a strict
				// superset of it, every sentence below fired on the article
				// alone.
				{"the offset objection wearing the bare stem",
					review.Finding{Path: "app/queries/comments_query.rb", Line: 12, Severity: "warning", Category: "performance",
						Title:     "Deep pages scan what they discard",
						Rationale: "OFFSET grows linearly with page number, so by default page 50 scans 5000 rows; consider keyset pagination."}},
				{"a clamp remark about where the ceiling belongs",
					review.Finding{Path: "app/queries/comments_query.rb", Line: 11, Severity: "nit", Category: "style",
						Title:     "Clamp outside the constructor",
						Rationale: "The clamp(1, MAX_PER_PAGE) expression belongs at the call site, not in the constructor."}},
			},
		},

		"multi-defect": {
			hit: []probe{
				{"names the traversal",
					review.Finding{Path: "handler.go", Line: 19, Severity: "critical", Category: "security",
						Title:     "Path traversal in the upload name",
						Rationale: "The query parameter is concatenated into a filesystem path, so ../ escapes /var/uploads."}},
				{"names the race",
					review.Finding{Path: "handler.go", Line: 25, Severity: "error", Category: "concurrency",
						Title:     "Unsynchronized increment",
						Rationale: "h.count is written from a goroutine with no lock held, which is a data race."}},
				{"names the descriptor leak",
					review.Finding{Path: "handler.go", Line: 19, Severity: "error", Category: "correctness",
						Title:     "Created file is never closed",
						Rationale: "Every request leaks a file descriptor; add a defer."}},
			},
			miss: []probe{{
				"a status-code preference",
				review.Finding{Path: "handler.go", Line: 30, Severity: "nit", Category: "api",
					Title:     "Return 201 for a successful upload",
					Rationale: "A request that stored something new should answer 201 rather than 200."},
			}},
		},
	}
}

// creditedByAnyDefect reports whether the scorer would count this finding as
// detecting any of the fixture's plants, using the scorer's own predicate
// rather than a restatement of it.
func creditedByAnyDefect(f Fixture, finding review.Finding) bool {
	for _, d := range f.Defects {
		if matches(finding, d) {
			return true
		}
	}
	return false
}

// --- the generated probe corpus ----------------------------------------------
//
// Everything below builds candidate findings out of material this repository
// already holds, so no assertion in this file depends on a person having thought
// of the sentence that breaks it. Four cross-fixture leaks were closed by hand in
// the change before this one, `nil`, `concurrent`, `capacity`, `arbitrary`,
// `close`, and the same class had recurred three times, always found by reading
// lists. Reading lists finds what the reader imagines.
//
// TWO FACTS ABOUT mentionsAny SHAPE all OF IT, and getting them wrong produces a
// generator that runs thousands of probes and cannot fail:
//
//   - IT IS strings.Contains OVER ONE HAYSTACK (title+rationale+category). So
//     credit is MONOTONE in the text: adding words to a credited finding cannot
//     uncredit it, and removing words cannot credit it. A negative probe built by
//     splitting a source sentence into fragments therefore cannot find a credit
//     the whole sentence does not already produce, splitting is worth doing to
//     ATTRIBUTE a credit (explainCredit), not to generate new ones. And a recall
//     probe built by padding Why with "please fix" is credited by construction,
//     which is why whyPhrasings only ever shortens.
//   - THE CAUSE OF A CREDIT IS always A SUBSTRING. So the minimal fragment that
//     still credits names the defective keyword exactly, and a fragment that is a
//     single ordinary word says the keyword is generic enough to be typed by a
//     reviewer who noticed nothing. That is the step the humans were doing by
//     hand after the generated test pointed at a sentence.

// generatedSource is one piece of review prose already checked into this tree,
// tagged with the fixture it was written about.
type generatedSource struct {
	fixture  string
	kind     string
	title    string
	text     string // title and rationale joined, the way mentionsAny reads them
	category string
}

// The three places this tree keeps sentences a reviewer might write.
const (
	// sourceHit is a finding the probe table declares IS a correct detection of
	// its own fixture's plant.
	sourceHit = "hit probe"
	// sourceObjection is a finding the probe table declares is a FALSE POSITIVE
	// of its own fixture: prose that noticed something else, or nothing. This is
	// the source the previous generator did not read, and every leak this change
	// reports came from it.
	sourceObjection = "declared objection"
	// sourceCache is prose the incumbent reviewer wrote, replayed from
	// testdata/incumbent. It is the half no authored probe can supply, because
	// every probe in this file was written by someone who knew which keywords
	// existed.
	sourceCache = "cached review"
)

// generatedSources is every such sentence, in a deterministic order.
//
// The order matters because failures print it: declaredProbes returns a map, and
// ranging over it directly made the same red build report its leaks in a
// different order each run.
func generatedSources() []generatedSource {
	var out []generatedSource

	add := func(fixture, kind string, f review.Finding) {
		out = append(out, generatedSource{
			fixture:  fixture,
			kind:     kind,
			title:    f.Title,
			text:     f.Title + " " + f.Rationale,
			category: f.Category,
		})
	}

	for name, p := range declaredProbes() {
		for _, h := range p.hit {
			add(name, sourceHit, h.finding)
		}
		for _, m := range p.miss {
			add(name, sourceObjection, m.finding)
		}
	}
	for _, f := range AllFixtures() {
		cached, ok := CachedIncumbent(crCacheDir, f)
		if !ok {
			continue
		}
		for _, c := range cached {
			add(f.Name, sourceCache, c)
		}
	}

	slices.SortFunc(out, func(a, b generatedSource) int {
		return strings.Compare(a.fixture+"|"+a.kind+"|"+a.title+"|"+a.text,
			b.fixture+"|"+b.kind+"|"+b.title+"|"+b.text)
	})
	return out
}

// creditsAt reports whether prose anchored ON a plant's own line would be scored
// as detecting it, which isolates the keyword list: nothing is excluded by
// distance and nothing by path.
func creditsAt(text, category string, d Defect) bool {
	return matches(review.Finding{
		Path: d.Path, Line: d.Line, Severity: "warning",
		Category: category, Rationale: text,
	}, d)
}

// firedKeywords is the subset of a defect's keywords that a haystack contains,
// using mentionsAny's own comparison.
func firedKeywords(haystack string, d Defect) []string {
	var out []string
	for _, kw := range d.Keywords {
		if strings.Contains(strings.ToLower(haystack), strings.ToLower(kw)) {
			out = append(out, kw)
		}
	}
	return out
}

// explainCredit reduces a credited text to the smallest fragment that still
// earns the credit, and names the keywords that fragment fires.
//
// This is the half the humans were doing by hand. The generated test that found
// the last four leaks reported a whole sentence, "Prevent Enabled from panicking
// when Default is nil", and a person then worked out that `nil` was the word
// doing the damage. Shrinking is mechanical, so the test does it: the answer is
// always a substring, sentences are tried before words, and a one-word answer is
// itself the verdict that the keyword is too generic to carry alone.
//
// The category is deliberately not shrunk. When a finding is credited through
// mentionsAny reading Category, no fragment of the prose explains it, and saying
// so is more useful than returning the whole sentence with no keyword named.
//
// THE JOIN IS A THIRD CASE, and the first version of this function answered it
// wrongly rather than not at all. mentionsAny concatenates title, rationale and
// category into ONE haystack, so a multi-word keyword can straddle the boundary
// between them and fire while neither half fires alone: with Keywords
// ["foo bar"], creditsAt("this ends in foo", "bar", d) is true. That fell into
// the category branch, which returned an EMPTY keyword list under a message
// positively blaming the category, an attribution step that answers wrongly is
// worse than one that answers "I do not know", because the whole point of this
// function is that a person stops re-deriving the cause by hand.
//
// It is latent rather than live: no credit in today's corpus reaches it (the
// cross-fixture loop asserts that, below). The corpus does carry multi-word
// keywords whose first or last word sits at a join, `error return`,
// `api contract`, `every row`, `where clause`, `redundant copy`, so the
// arrangement that reaches it is one cached review away.
func explainCredit(text, category string, d Defect) (fragment string, fired []string) {
	switch {
	case len(firedKeywords(text, d)) > 0:
		// The prose explains itself; shrink it below.
	case len(firedKeywords(category, d)) > 0:
		return "(the finding's category, not its prose)", firedKeywords(category, d)
	default:
		// Neither half fires alone. Either a keyword spans the join, or the
		// caller handed this function something that does not credit at all.
		if kws := firedKeywords(text+" "+category, d); len(kws) > 0 {
			return "(a keyword spanning the join mentionsAny makes between the finding's " +
				"prose and its category, so no fragment of either explains it)", kws
		}
		return "(nothing fires: this text does not credit the plant)", nil
	}

	best := text
	consider := func(frag string) {
		frag = strings.TrimSpace(frag)
		if frag == "" || len(frag) >= len(best) {
			return
		}
		if len(firedKeywords(frag, d)) > 0 {
			best = frag
		}
	}
	for _, s := range probeSentences(text) {
		consider(s)
	}
	for _, w := range strings.Fields(text) {
		consider(strings.Trim(w, `.,;:"'!?()`))
	}
	return best, firedKeywords(best, d)
}

// probeSentences splits prose into sentences.
//
// The boundary test requires a following capital because review prose is full of
// dotted identifiers, r.URL.Query(), http.Get, List.copyOf, sync.Mutex, and
// splitting on every period turns one sentence about a race into three fragments
// about nothing.
func probeSentences(text string) []string {
	var out []string
	var cur strings.Builder

	runes := []rune(text)
	for i, r := range runes {
		cur.WriteRune(r)
		if r != '.' && r != '!' && r != '?' {
			continue
		}
		if i+2 < len(runes) && (runes[i+1] == ' ' || runes[i+1] == '\n') && runes[i+2] >= 'A' && runes[i+2] <= 'Z' {
			out = append(out, strings.TrimSpace(cur.String()))
			cur.Reset()
		}
	}
	if last := strings.TrimSpace(cur.String()); last != "" {
		out = append(out, last)
	}
	return out
}

// corpusVocabulary is every ordinary word this repository uses about each
// fixture: the prose written about it, its plants' Why, and the tokens of its own
// source files.
//
// The source files are in it because the leak class is a CODE word. `nil`,
// `capacity`, `close` and `concurrent` are not words someone chose for a keyword
// list in the abstract, they are words that are in the corpus because they are in
// the corpus's code, and a reviewer quoting one change types them at another.
// Prose alone does not reach that: the only probe that shows `utc` is credited by
// the ordinary word "outcome" comes from a fixture's source text.
//
// SeverityNote IS DELIBERATELY EXCLUDED, and the measurement is the reason. Notes
// argue a plant's level by comparing it to OTHER plants by name, "every
// successful upload loses a descriptor", written in the goroutine leak's note
// about multi-defect's, so including them manufactured seven overlaps in which
// the corpus's own cross-reference was read as a reviewer's sentence. They are
// prose about the severity table, which is also why the recall direction does not
// assert them; see TestEveryPlantIsCreditedForItsOwnDescription.
func corpusVocabulary() map[string]map[string]bool {
	vocab := map[string]map[string]bool{}
	put := func(fixture, word string) {
		if len(word) < 3 {
			return
		}
		if vocab[fixture] == nil {
			vocab[fixture] = map[string]bool{}
		}
		vocab[fixture][word] = true
	}

	for _, s := range generatedSources() {
		for _, w := range proseWords(s.text) {
			put(s.fixture, w)
		}
	}
	for _, f := range AllFixtures() {
		for _, d := range f.Defects {
			for _, w := range proseWords(d.Why) {
				put(f.Name, w)
			}
		}
		for _, w := range sourceWords(f) {
			put(f.Name, w)
		}
	}
	return vocab
}

// proseWords lowercases prose and strips the punctuation that sits around a word
// in a sentence, keeping what sits INSIDE one: compare_digest, byte-by-byte and
// http.Get are single words a reviewer types.
func proseWords(text string) []string {
	var out []string
	for _, f := range strings.Fields(strings.ToLower(text)) {
		f = strings.Trim(f, `.,;:"'!?()[]{}`)
		f = strings.TrimSuffix(f, "'s")
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

// sourceWords is the identifier-ish tokens of a fixture's files, including the
// paths, which is what a reviewer quoting that change would type.
func sourceWords(f Fixture) []string {
	var out []string
	for _, files := range []map[string]string{f.Base, f.Head, f.Extra} {
		for path, body := range files {
			out = append(out, codeTokens(path+" "+body)...)
		}
	}
	return out
}

// codeTokens splits source text the way an identifier is spelled: letters,
// digits, underscore, dot and hyphen hold a token together, so http.Get,
// compare_digest and sk-live survive as one word each.
func codeTokens(text string) []string {
	var out []string
	held := func(r rune) bool {
		return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
			r == '_' || r == '.' || r == '-'
	}
	for _, tok := range strings.FieldsFunc(text, func(r rune) bool { return !held(r) }) {
		if tok = strings.ToLower(strings.Trim(tok, ".-")); tok != "" {
			out = append(out, tok)
		}
	}
	return out
}

// inertProse wraps one word in a review comment that says nothing else, so that
// a credit is attributable to the word alone.
//
// It is checked for inertness rather than assumed: TestNoBareWordFromAnother
// FixtureCreditsAPlant runs it empty against every plant first, because a single
// keyword hiding in this sentence would credit all 29 and the sweep would report
// the whole corpus as leaking.
const inertProse = "A remark on this change. It refers to %s and asserts nothing else about the code under review."

// TestReviewProseAboutAnotherFixtureIsNotCredited generates the negative set
// instead of authoring it.
//
// WHY GENERATE IT. Round 7 removed sixteen keywords that were paying full recall
// to findings which had noticed nothing, and every one was found by a person
// reading a list and imagining a sentence. That method finds what its author
// thought of, which is why the same fixtures had a paragraph beside each list
// asserting the opposite of what matches() did. The material for a negative set
// is already in this repository: a finding that reports fixture A's plant says
// nothing about fixture B, so moving it to B's anchor produces review prose that
// B's keywords must not credit. Every such sentence in this tree against every
// plant is a few thousand probes, none of which anyone has to think of. The size
// of both sets is ASSERTED below rather than quoted here: a floor fails when a
// refactor empties a source, and a figure in a comment is what nothing
// re-derives.
//
// Three sources, all checked in, all from generatedSources:
//
//   - every OTHER FIXTURE'S HIT PROBES. These are sentences already asserted to
//     be correct detections of something else, so their status at this anchor is
//     unambiguous.
//   - every OTHER FIXTURE'S DECLARED OBJECTIONS. Its miss probes: prose the
//     corpus already says reports nothing, even in the fixture it was written
//     about. This source was not being read, and every leak this change reports
//     came from it. The loop was drawing only on sentences that were correct
//     somewhere, which is the half of the material least likely to collide.
//   - THE SHIPPED INCUMBENT CACHE. Prose a real reviewer wrote, which
//     is the half authored probes cannot supply: every hit probe in this file was
//     written by someone who knew which keywords existed.
//
// SPLITTING THOSE SENTENCES INTO SENTENCES WOULD ADD nothing and is deliberately
// not done. mentionsAny is strings.Contains over one haystack, so a fragment
// credits only if the text containing it credits: a fragment probe cannot fail
// where the whole-text probe passed. Fragments are used where they do pay, by
// explainCredit, to name which sentence and which word caused a credit that has
// already been found.
//
// WHAT A FAILURE HERE MEANS, stated because it is easy to over-read. The probe
// text names the SOURCE fixture's identifiers, so no reviewer would ever write
// it at this anchor: a credit is evidence that two keyword lists share
// vocabulary, not that a reachable false credit exists. That is still the signal
// worth having, every leak Round 7 removed was a keyword generic enough to be
// typed by a sentence that had noticed something else, but this loop measures
// OVERLAP and the declared miss probes measure REACHABILITY, and neither
// substitutes for the other. TestAFixturesOwnObjectionsAreNotCreditedAtItsPlants
// is the reachability half generated the same way, and
// TestNoBareWordFromAnotherFixtureCreditsAPlant is the attribution.
//
// WHAT ITS SILENCE does not MEAN, and the first version of this comment got
// this wrong in a way worth keeping. It read the loop's output, every overlap
// lands in a fixture file this change does not own, none in fixtures_info.go,
// as evidence that the info keyword lists were clean. The loop CANNOT reach
// them, because the stems those lists carry appear only in the info fixtures'
// own probes, which it skips (`if s.fixture == f.Name { continue }`). So no
// number of probes here can produce a credit against an info plant from a keyword
// that file rewrote: zero collisions there was a property of the SOURCE CORPUS,
// not a verdict on the keywords, and eleven hand-written sentences were credited
// by them at the time. Reachability is what the declared miss probes measure.
// This loop measures overlap. Neither reads on the other's behalf.
//
// TRIPLING THE SOURCE SET did not CHANGE that, which is the same lesson arriving
// twice. Adding every fixture's declared objections took the sources from 63 to
// 142 and found thirteen new overlaps, and not one of them TARGETS an info plant
// , the info fixtures appear only as sources, where kotlin's visibility objection
// reaches contract-break's `consumer`. More material does not make a loop reach
// where its exclusion rule forbids.
//
// The overlaps it finds today are listed below rather than fixed, and the list
// says why for each. A stale entry fails as loudly as a new overlap, so the list
// cannot outlive the keywords it excuses.
func TestReviewProseAboutAnotherFixtureIsNotCredited(t *testing.T) {
	// knownOverlaps is keyed TARGET PLANT | source fixture | source kind |
	// title. Each entry says what the credit fires on, because "these two
	// collide" is not actionable and "these two collide on the bare stem
	// `leak`" is, and checkReasonFires below holds that sentence to what
	// fires, which nothing did until a mutation showed an entry staying
	// green while the stem it names stopped being the cause.
	//
	// THE KEY CARRIES THE PLANT and THE KIND because target|source|title
	// identified neither. multi-defect plants its path traversal and its
	// descriptor leak at handler.go:19 and its raced counter at handler.go:25,
	// so one location-free key absorbed credits on THREE different defects:
	// adding a `goroutines` keyword to the CRITICAL traversal plant made
	// go-package-singleton's "Concurrent map access" objection credit two plants
	// under a single entry whose reason names only the counter, and this test
	// stayed green. Kind is in it because two different sources can carry the
	// same title, a cached review and a declared objection with the same
	// opening sentence, measured twice in today's corpus, and one entry
	// covering both cannot go stale in either direction.
	//
	// They fall into two kinds, and the difference is the whole reason this is a
	// list rather than a count:
	//
	//   - SHARED MECHANISM. Two plants that are the same defect in two
	//     languages, where the sentence describing one really does describe the
	//     other. Asking the keywords to separate these would be asking them to
	//     reject a correct description of the plant, which is the failure this
	//     round is repairing in the other direction.
	//   - BARE STEM. A keyword short and ordinary enough that prose about an
	//     unrelated defect reaches it. These are defects, they are named here
	//     with the exact stem, and each needs a change that owns the fixture
	//     file it lives in.
	knownOverlaps := map[string]string{
		// Shared mechanism: interpolating an untrusted string into an
		// interpreted context. `injection` and `concatenat` name that mechanism
		// and all three plants have it, so a sentence about one names the next.
		//
		// The first two are the same opening sentence from two sources, the
		// shipped cache and the hit probe someone wrote afterwards, which one
		// title-keyed entry used to cover. They are listed separately because a
		// reword of either has to be re-derived on its own.
		"go-sql-injection|0|store.go:17|python-command-injection|cached review|Do not interpolate name into a shell command.": "shared mechanism: `injection`",
		"go-sql-injection|0|store.go:17|python-command-injection|hit probe|Do not interpolate name into a shell command.":     "shared mechanism: `injection`",
		"go-sql-injection|0|store.go:17|multi-defect|hit probe|Path traversal in the upload name":                             "shared mechanism: `concatenat`",
		"python-command-injection|0|tools.py:12|go-sql-injection|hit probe|SQL injection in SearchUsers":                      "shared mechanism: `injection`",
		"multi-defect|0|handler.go:19|python-command-injection|cached review|Do not interpolate name into a shell command.":   "shared mechanism: `../`, which the cached review types about its own traversal",

		// Shared mechanism: a resource that is acquired and not released.
		// multi-defect's descriptor leak and the goroutine leak are the same
		// sentence about different resources.
		"csharp-client-per-request|0|src/Notifier.cs:17|multi-defect|cached review|Close files and remove failed uploads.": "shared mechanism: `exhaust`",

		// Bare stems. Each is a keyword in a fixture this change does not own,
		// and each credits prose that has noticed something else entirely.
		"go-nil-deref|0|fetch.go:10|ts-unawaited-async|hit probe|saveAll resolves before anything is stored":            "bare stem `discard`: a discarded promise is not a discarded error",
		"contract-break|0|event.go:9|go-package-singleton|hit probe|Prefer passing the Set":                             "bare stem `consumer`",
		"contract-break|0|event.go:9|ruby-default-page-size|hit probe|Every consumer pays for the web client's default": "bare stem `consumer`",
		// Found BY this loop, on its first run, against a probe added in the
		// same change: the recall probe restoring ruby's proposed fix reaches
		// contract-break's `consumer` too. It is the receipt for the loop being
		// worth having, nobody would have thought to write this pair down.
		"contract-break|0|event.go:9|ruby-default-page-size|hit probe|Set this where it is needed":                                                   "bare stem `consumer`",
		"contract-break|0|event.go:9|clean-sql-allowlist|cached review|Validate and cap limit before the query.":                                     "bare stem `api contract`, reached by the cached review's \"the API contract\"",
		"data-loss-migration|0|migrations/0007_backfill_plan.sql:8|go-sql-injection|cached review|Use a parameterized query in SearchUsers.":         "bare stem `where clause`: changing a WHERE clause by injection is not a missing one, and this one is REACHABLE — an injection remark on a migration would be credited with the missing predicate",
		"data-loss-migration|0|migrations/0007_backfill_plan.sql:8|multi-defect|cached review|Reject traversal names and prevent target overwrites.": "bare stem `overwrite`",
		// One title, two sources, and under a mutation two DIFFERENT stems: the
		// cached review says "permission errors" and the objection could be
		// reworded to "ownership errors" without either entry going red, back
		// when one key covered both. checkReasonFires is the other half of that
		// repair.
		"removed-guard|0|project.go:31|python-command-injection|cached review|Propagate archive failures.":                  "bare stem `permission`: a permission ERRNO is not a permission CHECK",
		"removed-guard|0|project.go:31|python-command-injection|declared objection|Propagate archive failures.":             "bare stem `permission`, from the same sentence the corpus also declares a false positive of its own fixture",
		"go-sql-injection|0|store.go:17|clean-sql-allowlist|cached review|Validate and cap limit before the query.":         "bare stems `sql injection`/`parameteri`, reached by a sentence whose point is that parameterization is already present — the same shape as the `subprocess` leak Round 7 removed",
		"python-command-injection|0|tools.py:12|clean-sql-allowlist|cached review|Validate and cap limit before the query.": "bare stem `injection`, from a sentence about a query",

		// FOUND BY READING THE DECLARED OBJECTIONS, which this loop did not do
		// until now. Every entry below is a sentence the corpus already says
		// reports nothing, credited at another fixture's plant. Four of them are
		// the same stems the change before this one narrowed in the fixture files
		// it owned, `race`, `consumer`, `discard`, reached from a source it was
		// not looking at, which is the argument for the source rather than for the
		// stems.
		"multi-defect|1|handler.go:25|go-package-singleton|declared objection|Concurrent map access":                                             "bare stem `race`: `concurrent` was removed from this plant for exactly this sentence and `race` was left, so the same objection still credits the counter. `race` is also four letters long and matched as a substring, which TestNoOrdinaryEnglishWordCreditsAPlant is where that half is recorded",
		"multi-defect|1|handler.go:25|cross-file-copy-nit|declared objection|The store may append after Snapshot returns":                        "bare stem `race`: a different race, in a different package, on a slice this plant knows nothing about",
		"contract-break|0|event.go:9|kotlin-widened-input|declared objection|Mark Summarizable internal":                                         "bare stem `consumer`, from a visibility objection about a Kotlin interface",
		"contract-break|0|event.go:9|ruby-default-page-size|declared objection|The ceiling is the real problem":                                  "bare stem `consumer`",
		"go-nil-deref|0|fetch.go:10|ruby-default-page-size|declared objection|Deep pages scan what they discard":                                 "bare stem `discard`: rows a query discards are not an error a caller discards",
		"go-nil-deref|0|fetch.go:10|ruby-default-page-size|declared objection|Offset pagination scans discarded rows":                            "bare stem `discard`, same sentence in the reviewer's other wording",
		"go-hardcoded-secret|0|client.go:12|python-timing-unsafe-hmac|declared objection|Missing WEBHOOK_SECRET crashes at import":               "bare stem `secret`: an env var NAMED secret is not a secret committed to the repository, and this objection is about an unset variable",
		"data-loss-migration|0|migrations/0007_backfill_plan.sql:8|duplicate-test-case-nit|declared objection|The table repeats one expectation": "bare phrase `every row`, typed about the rows of a TEST TABLE",
		"removed-guard|0|project.go:31|php-forbidden-vs-404|declared objection|Insecure direct object reference":                                 "shared mechanism: the objection php declares as a false positive — there is no authorization check — is a true description of THIS plant, so `authoriz` is doing its job in both places",
		"removed-guard|0|project.go:31|php-forbidden-vs-404|declared objection|Missing ownership check":                                          "shared mechanism, as above, on `ownership`",
		"cross-file-copy-nit|0|report/summary.go:17|defensive-copy-nit|declared objection|Prefer List.copyOf":                                    "shared mechanism: `redundant copy`. Both plants ARE an unnecessary copy, one in Go and one in Java",
		"cross-file-copy-nit|0|report/summary.go:17|defensive-copy-nit|declared objection|Use List.copyOf instead of wrapping an ArrayList":      "shared mechanism: `unnecessary copy`, as above",
		// The one no reader would predict, and the reason a generator is worth
		// having at all: `error return` is matched ACROSS the word boundary in
		// "the context error Returning ctx.Err() bare". Nothing about the sentence
		// is about an unchecked return.
		"go-nil-deref|0|fetch.go:10|go-cancel-goroutine-leak|declared objection|Wrap the context error": "substring across a word boundary: `error return` inside \"error Returning\"",
	}

	sources := generatedSources()

	// A generator that silently produces nothing passes every assertion below.
	// The counts are what the loop is worth, so they are asserted rather than
	// assumed: the cache is read off disk and could go missing, and the probe
	// table is read through a function that could be emptied. The objection count
	// is here because that source produced every leak this change reports, and a
	// refactor that dropped it would look like the corpus getting cleaner.
	byKind := map[string]int{}
	for _, s := range sources {
		byKind[s.kind]++
	}
	if len(sources) < 120 {
		t.Fatalf("only %d generated sources; the negative set is not being built", len(sources))
	}
	if byKind[sourceCache] < 15 {
		t.Fatalf("only %d sources came from the Incumbent cache; the half that is not authored "+
			"against this keyword list has gone missing", byKind[sourceCache])
	}
	if byKind[sourceObjection] < 60 {
		t.Fatalf("only %d sources are declared objections; that source found every leak this "+
			"loop reports and is not being read", byKind[sourceObjection])
	}

	seen := map[string]bool{}
	probes := 0
	for _, f := range AllFixtures() {
		for i, d := range f.Defects {
			for _, s := range sources {
				// A fixture's own findings are the recall direction, which the
				// declared hit probes assert, and its own objections are
				// TestAFixturesOwnObjectionsAreNotCreditedAtItsPlants. Only prose
				// about ANOTHER change is unambiguously wrong here.
				if s.fixture == f.Name {
					continue
				}
				probes++

				// The anchor is the plant's own line, so nothing is excluded by
				// distance and the keyword list is the only thing under test.
				// Category rides along because mentionsAny reads it.
				if !creditsAt(s.text, s.category, d) {
					continue
				}

				frag, fired := explainCredit(s.text, s.category, d)
				// An unattributable credit is a broken instrument, not a clean
				// corpus: every registry entry below is a sentence someone wrote
				// after being told which keyword fired, so a credit this cannot
				// name would be triaged by hand or not at all.
				if len(fired) == 0 {
					t.Errorf("%s: the credit at %s:%d from %s's %s %q cannot be attributed to any "+
						"keyword (%s). explainCredit is the step that turns a colliding sentence into "+
						"an editable keyword, and it has no answer here",
						f.Name, d.Path, d.Line, s.fixture, s.kind, s.title, frag)
					continue
				}

				key := overlapKey(f.Name, i, d, s)
				if reason, known := knownOverlaps[key]; known {
					seen[key] = true
					// Against the whole credit, not the minimal fragment.
					// explainCredit shrinks to one cause on purpose, so a
					// sentence firing `injection`, `sql injection` and
					// `parameteri` reduces to the shortest of them, and holding
					// a reason that correctly names all three to that subset
					// would report two of its own true statements as stale.
					checkReasonFires(t, key, reason, firedKeywords(s.text+" "+s.category, d), d)
					continue
				}
				t.Errorf("%s: a %s about %s is credited with the plant at %s:%d. It fires %v on %q, "+
					"in: %q\n    registry key: %q",
					f.Name, s.kind, s.fixture, d.Path, d.Line, fired, frag, s.text, key)
			}
		}
	}

	if probes < 3500 {
		t.Errorf("only %d generated probes were run; the loop is not covering the corpus", probes)
	}

	for key, why := range knownOverlaps {
		if !seen[key] {
			t.Errorf("overlap %q no longer occurs (%s); delete the entry rather than carrying it", key, why)
		}
	}
}

// TestAFixturesOwnObjectionsAreNotCreditedAtItsPlants moves each fixture's
// declared false positives ONTO its own plant.
//
// This is the reachability question the cross-fixture loop cannot ask. A probe
// declared under `miss` is a sentence a reviewer really might write about this
// change. That is why it was written, and the corpus says it reports nothing.
// TestKeywordsAdmitOnlyRealDetections already runs it, but at the anchor its
// author chose, so an objection can pass on DISTANCE while the keyword list would
// have credited it: three of the corpus's miss probes are deliberately anchored
// in another file, and one of those is credited the moment it is moved.
//
// Moving it is not unfair. The anchor is the plant's own line, so this asks only
// what the keyword list does with the sentence, and a reviewer who wrote the same
// objection while pointing at the plant is not a rare event, anchoring is the
// thing reviewers are worst at, which is why the scorer has a tolerance at all.
//
// A credit here therefore has one of two meanings, and the registry has to say
// which: either the objection is only excluded by its anchor (and the entry says
// so, pointing at the probe's own comment), or the keyword list credits a
// sentence its own fixture says is wrong.
func TestAFixturesOwnObjectionsAreNotCreditedAtItsPlants(t *testing.T) {
	// Keyed TARGET PLANT | title, for the reason
	// TestReviewProseAboutAnotherFixtureIsNotCredited's key carries one: a
	// fixture-keyed entry excuses every plant that fixture holds, and
	// multi-defect holds three. Its reason is checked against what fires, on the
	// same argument.
	knownSelfCredits := map[string]string{
		"ts-unbounded-memo-key|0|src/search.ts:13|Map grows without bound": "excluded by PATH and the probe says so: it blames src/cache.ts, whose growth is documented and correct, " +
			"and the plant is the call site in src/search.ts. Its keywords `memory` and `grows without` do credit the sentence, " +
			"so this fixture's separation of helper from caller rests on the anchor rather than on the words",
	}

	byFixture := map[string][]generatedSource{}
	for _, s := range generatedSources() {
		if s.kind == sourceObjection {
			byFixture[s.fixture] = append(byFixture[s.fixture], s)
		}
	}

	seen := map[string]bool{}
	probes := 0
	for _, f := range AllFixtures() {
		for i, d := range f.Defects {
			for _, s := range byFixture[f.Name] {
				probes++
				if !creditsAt(s.text, s.category, d) {
					continue
				}
				key := plantKey(f.Name, i, d) + "|" + s.title
				if reason, known := knownSelfCredits[key]; known {
					seen[key] = true
					checkReasonFires(t, key, reason, firedKeywords(s.text+" "+s.category, d), d)
					continue
				}
				frag, fired := explainCredit(s.text, s.category, d)
				t.Errorf("%s: its own declared false positive is credited with the plant at %s:%d "+
					"once the anchor is the plant's line. It fires %v on %q, in: %q\n    registry key: %q",
					f.Name, d.Path, d.Line, fired, frag, s.text, key)
			}
		}
	}

	// Every fixture that plants something and declares an objection has to be
	// covered, or an emptied probe table reads as a clean corpus.
	if probes < 60 {
		t.Errorf("only %d objections were moved onto a plant; the probe table is not being read", probes)
	}

	for key, why := range knownSelfCredits {
		if !seen[key] {
			t.Errorf("self-credit %q no longer occurs (%s); delete the entry rather than carrying it", key, why)
		}
	}
}

// TestNoBareWordFromAnotherFixtureCreditsAPlant isolates the cause.
//
// Every leak the previous change closed by hand was ONE WORD, `nil`,
// `concurrent`, `capacity`, `arbitrary`, `close`, and each was found by a person
// reading a credited sentence and working out which of its words did the damage.
// This does that mechanically: one word, wrapped in prose that says nothing, is
// either credited or it is not, and if it is, the word and the keyword are both
// named in the failure. No sentence has to exist for the word to be tried.
//
// THE VOCABULARY IS MEASURED, not JUDGED, which makes the verdict checkable
// instead of a matter of taste. A word counts as generic for a plant when this
// repository already uses it about a DIFFERENT fixture, in prose written about
// that fixture, in that fixture's own Why, or in its source text. So `sk-live`
// is never tried against the secret plant (nothing else in the corpus contains
// it) and `nil` would be tried against every plant in the tree.
//
// MEASURING AGAINST THE CORPUS IS ALSO THE BLIND SPOT, and this paragraph used
// to call the measurement "the only defensible way" to decide a word is too
// generic, backwards about which half is at risk. The corpus is a few thousand
// words of Go, Python and review prose. Ordinary English is not, and it is what
// reviewers type. Run twenty-five plain words through this sweep's own wrapper
// and predicate and thirteen credit a plant; twelve of the thirteen are words
// the corpus never uses, so this loop cannot try them. `race` sits inside trace,
// embrace, grace, brace, terrace and bracelet; `dst` inside midst and amidst;
// `idor` inside corridor. Through the real scorer that is a variable-rename nit
// , "corridor is a confusing name for this local; call it path", taking FULL
// recall on removed-guard's critical authorization plant, at zero cost in
// precision.
//
// So this loop is complete over the corpus and blind outside it.
// TestNoOrdinaryEnglishWordCreditsAPlant reaches outside it and is authored, so
// it is as incomplete as its author. TestShortKeywordsAreSubstringHazards needs
// no word list at all and is a shape proxy, so it cannot argue about a long
// keyword. None of the three is the defensible way; having all three is why any
// of them is worth running.
//
// IT REACHES WORDS NO PROBED SENTENCE CONTAINS, which this paragraph used to
// deny with a proof that assumed its own premise: "every word in the vocabulary
// sits in some sentence, and mentionsAny is substring containment, so a word
// that credits is inside a sentence that credits". corpusVocabulary does not
// draw only on generatedSources. It also reads every plant's Why and every
// fixture's SOURCE TEXT, and neither is a probed sentence. Measured, this sweep
// reaches 2 keywords the sentence loop cannot: `utc`, via the word "outcome" in
// another fixture's code comment, and `placeholder`, via an identifier in
// clean-sql-allowlist's source. The first of those targets timezone-boundary,
// which the entire 3963-probe sentence loop credits zero times, so the claim
// was not merely unproven, its own headline example was the counterexample. Both
// figures are recomputed below and read back out of this comment, because the
// retracted version was a proof nothing reproduced.
func TestNoBareWordFromAnotherFixtureCreditsAPlant(t *testing.T) {
	// Keyed fixture|keyword, because the keyword is the thing that would be
	// edited and one keyword is usually reached by several inflections.
	knownBareStems := map[string]string{
		"go-sql-injection|injection":         "shared mechanism: three plants interpolate an untrusted string into an interpreted context",
		"python-command-injection|injection": "shared mechanism, as above",
		"go-sql-injection|concatenat":        "shared mechanism: the traversal plant concatenates too",
		"go-sql-injection|parameteri":        "reached by `parameterization` in the clean fixture, whose whole point is that it is ALREADY parameterized — the same shape as the `subprocess` leak Round 7 removed",
		"go-sql-injection|placeholder":       "reached by the identifier `placeholder` in clean-sql-allowlist's source, so a reviewer quoting that change types it",
		"go-nil-deref|discard":               "bare stem: a discarded promise and discarded rows both reach it",
		"go-hardcoded-secret|secret":         "bare stem: the word names the SUBJECT, not the defect, and another fixture's code declares a variable called SECRET. It is also a substring of `secretly` and `secretary`, which this loop cannot try because the corpus contains neither",
		// What the CORPUS reaches this with is the whole word "race", so what
		// this loop sees really is a topic collision. The entry used to stop
		// there, and stopping there is what makes it wrong: it told the next
		// editor the fix was to narrow the topic. `race` is also four letters
		// long and matched as a substring, so trace, embrace, grace, brace,
		// terrace and bracelet credit this counter with no race anywhere in the
		// sentence, and no amount of narrowing reaches that. It is the same
		// defect as `utc` below, which this list called categorically different
		// one screen away. See TestNoOrdinaryEnglishWordCreditsAPlant.
		"multi-defect|race":                 "bare stem: prose about a race in another package, AND a four-letter substring of ordinary words",
		"multi-defect|mutex":                "bare stem: `sync.Mutex` appears in another fixture's source, so quoting that change credits this counter",
		"contract-break|consumer":           "bare stem: every argument about who else calls a thing uses the word",
		"data-loss-migration|overwrite":     "bare stem: the traversal plant overwrites a file",
		"removed-guard|authoriz":            "shared mechanism: the php plant's declared objection is a true description of this one",
		"removed-guard|ownership":           "shared mechanism, as above",
		"removed-guard|permission":          "bare stem: a permission ERRNO is not a permission CHECK",
		"csharp-client-per-request|exhaust": "shared mechanism: a resource acquired and not released, which is what the whole word `exhaust` reaches it with here. It is separately a substring of `exhaustive`, which is ordinary review prose about test coverage and no kind of shared mechanism at all",
		// The one that cannot be found by reading a keyword list, because the
		// keyword is not a word in the sentence at all. "oUTCome" contains `utc`,
		// so an ordinary English word credits a reviewer with detecting a
		// timezone bug it never mentioned.
		"timezone-boundary|utc": "SUBSTRING INSIDE A LONGER WORD: `utc` sits in \"outcome\". This is not a shared mechanism and not a topic collision, it is the keyword being three letters long",
	}

	vocab := corpusVocabulary()

	// The wrapper has to be inert or every plant leaks and the sweep says so
	// about the corpus rather than about the wrapper.
	for _, f := range AllFixtures() {
		for _, d := range f.Defects {
			if creditsAt(fmt.Sprintf(inertProse, "it"), "", d) {
				t.Fatalf("the probe wrapper itself is credited with %s's plant at %s:%d (fires %v); "+
					"every result below would be an artifact of this sentence",
					f.Name, d.Path, d.Line, firedKeywords(fmt.Sprintf(inertProse, "it"), d))
			}
		}
	}

	words := 0
	for _, m := range vocab {
		words += len(m)
	}
	if len(vocab) < 25 || words < 2000 {
		t.Fatalf("vocabulary is %d words over %d fixtures; it is not being built", words, len(vocab))
	}

	seen := map[string]bool{}
	for _, f := range AllFixtures() {
		// One sorted list per target, so a failure names the same word every run.
		var candidates []string
		for other, m := range vocab {
			if other == f.Name {
				continue
			}
			for w := range m {
				candidates = append(candidates, w)
			}
		}
		slices.Sort(candidates)
		candidates = slices.Compact(candidates)

		for _, d := range f.Defects {
			for _, w := range candidates {
				hay := fmt.Sprintf(inertProse, w)
				if !creditsAt(hay, "", d) {
					continue
				}
				for _, kw := range firedKeywords(hay, d) {
					key := f.Name + "|" + kw
					if _, known := knownBareStems[key]; known {
						seen[key] = true
						continue
					}
					t.Errorf("%s: the single word %q credits the plant at %s:%d through the keyword %q, "+
						"and %q is a word this repository uses about %s. A keyword one ordinary word long "+
						"is typed by reviewers who noticed nothing",
						f.Name, w, d.Path, d.Line, kw, w, strings.Join(usedBy(vocab, w, f.Name), ", "))
				}
			}
		}
	}

	for key, why := range knownBareStems {
		if !seen[key] {
			t.Errorf("bare stem %q is no longer reachable by a single word (%s); delete the entry "+
				"rather than carrying it", key, why)
		}
	}

	checkVocabularyReachesBeyondSentences(t, vocab)
}

// checkVocabularyReachesBeyondSentences recomputes the two figures the paragraph
// above quotes for the claim it retracts, and reads that paragraph back out of
// the source.
//
// The retracted claim was that this sweep can find nothing the sentence loop
// can, argued as a proof. A proof about a corpus is a measurement wearing a
// disguise, so the measurement is here: how many firing keywords appear in NO
// cross-fixture generated source, and whether the sentence loop credits the
// fixture the headline example targets.
func checkVocabularyReachesBeyondSentences(t *testing.T, vocab map[string]map[string]bool) {
	t.Helper()

	sources := generatedSources()
	// Only sources the sentence loop would run against a given target:
	// it skips a fixture's own prose, so a keyword that appears only in its own
	// fixture's sources is out of that loop's reach.
	inSomeCrossFixtureSource := func(target, kw string) bool {
		for _, s := range sources {
			if s.fixture == target {
				continue
			}
			if strings.Contains(strings.ToLower(s.text+" "+s.category), strings.ToLower(kw)) {
				return true
			}
		}
		return false
	}

	beyond := map[string]bool{}
	creditedBySentences := map[string]bool{}
	for _, f := range AllFixtures() {
		for _, d := range f.Defects {
			for _, s := range sources {
				if s.fixture != f.Name && creditsAt(s.text, s.category, d) {
					creditedBySentences[f.Name] = true
				}
			}
			for other, m := range vocab {
				if other == f.Name {
					continue
				}
				for w := range m {
					hay := fmt.Sprintf(inertProse, w)
					if !creditsAt(hay, "", d) {
						continue
					}
					for _, kw := range firedKeywords(hay, d) {
						if !inSomeCrossFixtureSource(f.Name, kw) {
							beyond[f.Name+"|"+kw] = true
						}
					}
				}
			}
		}
	}

	want := fmt.Sprintf("reaches %d keywords the sentence loop cannot", len(beyond))
	src, err := os.ReadFile("groundtruth_test.go")
	if err != nil {
		t.Fatalf("reading groundtruth_test.go: %v", err)
	}
	if !strings.Contains(string(src), want) {
		t.Errorf("TestNoBareWordFromAnotherFixtureCreditsAPlant no longer says %q. The sweep now "+
			"reaches %d keywords no cross-fixture generated sentence contains (%v), and the paragraph "+
			"retracting the old proof quotes a figure it no longer has",
			want, len(beyond), slices.Sorted(maps.Keys(beyond)))
	}
	if creditedBySentences["timezone-boundary"] {
		t.Errorf("the sentence loop now credits timezone-boundary, so the paragraph above is wrong "+
			"about its own example: it says the bare-word sweep reaches a fixture the 3963-probe "+
			"loop credits zero times. Re-derive the example or the sentence (beyond=%v)",
			slices.Sorted(maps.Keys(beyond)))
	}
}

// ordinaryEnglishWords is plain English chosen for having NO review-domain
// meaning, so a credit from this list is unambiguously a SUBSTRING ACCIDENT and
// never a topical hit. Nobody typing "terrace" has noticed a data race.
//
// IT IS AUTHORED, which is the method every generator in this file exists to get
// away from, and it finds what its author thought of and nothing else. Worse,
// the honest account of how it was built is that a handful of these words were
// chosen after a run showed which keywords were short enough to hide inside one
// , trace, midst and corridor are answers, not questions. The rest is filler,
// and the filler found two things the targeted words did not: `secret` inside
// "secretly", and `exhaust` inside "exhaustive", ordinary prose about test
// coverage taking full credit for a socket-exhaustion plant.
//
// That asymmetry is the argument for TestShortKeywordsAreSubstringHazards
// beside it, which needs no list and so cannot be short of imagination, not an
// argument that this list is adequate. It is not, and a keyword six or seven
// letters long is exactly where it and the shape rule are both weakest.
func ordinaryEnglishWords() []string {
	return []string{
		"afterwards", "alignment", "amidst", "anywhere", "apparent", "appendix",
		"arguable", "awkward", "bracelet", "brace", "citation", "clumsy",
		"comment", "compromise", "conclusion", "convention", "corridor",
		"corridors", "cosmetic", "curious", "custom", "debatable", "deliberate",
		"digression", "displace", "doubtful", "draft", "elsewhere", "embrace",
		"everywhere", "evident", "exhaustive", "exhaustively", "familiar",
		"footnote", "formatting", "fragile", "furnace", "grace", "graceful",
		"grammar", "grouping", "habit", "harmless", "however", "idiom",
		"incidental", "increment", "indentation", "intentional", "introduction",
		"iteration", "meanwhile", "menace", "midst", "milestone", "minor",
		"misleading", "moreover", "nevertheless", "note", "observation",
		"obvious", "odd", "opinion", "ordering", "outcome", "outcomes",
		"overview", "palace", "paragraph", "peculiar", "pedantic", "phrasing",
		"plausible", "preference", "priority", "punctuation", "questionable",
		"readable", "reasonable", "reference", "remark", "renaming", "revision",
		"schedule", "secretary", "secretly", "sensible", "sentence", "sketch",
		"solace", "somewhere", "spelling", "stylistic", "summary", "surprising",
		"suggestion", "surface", "technique", "terminology", "terrace",
		"therefore", "timeline", "trace", "traced", "traces", "tradeoff",
		"trivial", "unclear", "unusual", "urgency", "verbose", "vocabulary",
		"whitespace", "wording", "workaround",
	}
}

// TestNoOrdinaryEnglishWordCreditsAPlant is the half the corpus sweep cannot be.
//
// TestNoBareWordFromAnotherFixtureCreditsAPlant decides "too generic" by asking
// whether this repository already uses a word about another fixture. That is
// checkable and it is blind to every ordinary word the corpus happens not to
// contain, which is most of English. The keywords that hide there are the worst
// ones: `idor` inside "corridor" hands a critical authorization plant to a
// variable-rename nit, scored through the real ScoreRun as matched=1/1 with the
// plant marked detected and nothing counted against precision.
//
// A credit here is not "these two fixtures share vocabulary". It is a keyword
// firing on a sentence that says nothing about code at all.
//
// THE SIX are RECORDED, not CLOSED, on the same argument the recall gaps are:
// changing a keyword changes which cached Incumbent findings match a plant, and
// those matches feed figures pinned as evidence elsewhere in this package. A
// change that narrows `idor` has to re-derive them in the same commit.
//
// What makes that deferral safe rather than convenient is a measurement, and it
// is a SNAPSHOT rather than an invariant. Auditing the shipped cache for these
// stems inside a longer word, title, rationale and category of every
// CachedIncumbent finding, finds none: every substring credit the cache does
// earn is a legitimate inflection (discards, parameterized, hardcode,
// metacharacters, descriptors, sockets). So no published number rests on one of
// these accidents today. Nothing here asserts that, because separating
// "secretary" from "secrets" mechanically needs a rule about English this file
// has no business inventing, which is exactly why the hazard is recorded here
// instead of being trusted to stay latent. Re-collecting the cache can spend it.
func TestNoOrdinaryEnglishWordCreditsAPlant(t *testing.T) {
	// Keyed fixture|keyword. Each reason must NAME the ordinary word, and the
	// named word is run back through the scorer, a registry whose reasons are
	// not checked against what fires is how the last one went stale.
	knownEnglishSubstrings := map[string]string{
		"multi-defect|race":                 "`race` is four letters, matched inside trace, embrace, grace, brace, terrace and bracelet. \"Log the stack trace when the upload handler fails\" scores multi-defect matched=1/3 with the raced counter marked detected",
		"timezone-boundary|dst":             "`dst` inside midst and amidst. Neither word is in the corpus, so the vocabulary sweep never tries it",
		"timezone-boundary|utc":             "`utc` inside outcome, the one case the corpus sweep also reaches — and only because a code comment in another fixture happens to use the word",
		"removed-guard|idor":                "`idor` inside corridor. A rename nit takes FULL recall on a critical authorization plant",
		"go-hardcoded-secret|secret":        "`secret` inside secretly and secretary. Six letters, so no short-keyword rule reaches it either",
		"csharp-client-per-request|exhaust": "`exhaust` inside exhaustive. Seven letters, and \"an exhaustive list of the cases\" is prose a reviewer writes about tests",
	}

	// The wrapper has to be inert here for the same reason it does in the
	// corpus sweep: a keyword hiding in this sentence would credit every plant.
	for _, f := range AllFixtures() {
		for _, d := range f.Defects {
			if creditsAt(fmt.Sprintf(inertProse, "it"), "", d) {
				t.Fatalf("the probe wrapper itself is credited with %s's plant at %s:%d (fires %v)",
					f.Name, d.Path, d.Line, firedKeywords(fmt.Sprintf(inertProse, "it"), d))
			}
		}
	}

	words := ordinaryEnglishWords()
	if len(words) < 100 {
		t.Fatalf("the English list is %d words; it is not being built", len(words))
	}

	seen := map[string]bool{}
	for _, f := range AllFixtures() {
		for _, d := range f.Defects {
			for _, w := range words {
				hay := fmt.Sprintf(inertProse, w)
				if !creditsAt(hay, "", d) {
					continue
				}
				for _, kw := range firedKeywords(hay, d) {
					key := f.Name + "|" + kw
					if reason, known := knownEnglishSubstrings[key]; known {
						seen[key] = true
						checkWitnessCredits(t, key, reason, d, kw)
						continue
					}
					t.Errorf("%s: the ordinary English word %q credits the plant at %s:%d through the "+
						"keyword %q. The word says nothing about code, so this is the keyword being a "+
						"substring of English rather than a description of a defect",
						f.Name, w, d.Path, d.Line, kw)
				}
			}
		}
	}

	for key, why := range knownEnglishSubstrings {
		if !seen[key] {
			t.Errorf("English substring %q no longer occurs (%s); delete the entry rather than "+
				"carrying it", key, why)
		}
	}
}

// checkWitnessCredits holds an entry to the word it blames.
//
// The reason names an ordinary word; this runs that word back through the same
// wrapper and predicate and requires it to credit this plant through this
// keyword. Without it the reason is prose beside a key, which is how
// "bare stem `permission`" survived the credit moving to `ownership`.
func checkWitnessCredits(t *testing.T, key, reason string, d Defect, keyword string) {
	t.Helper()

	lower := strings.ToLower(reason)
	for _, w := range ordinaryEnglishWords() {
		if !strings.Contains(lower, w) {
			continue
		}
		hay := fmt.Sprintf(inertProse, w)
		if creditsAt(hay, "", d) && slices.Contains(firedKeywords(hay, d), keyword) {
			return
		}
	}
	t.Errorf("entry %q names no ordinary word that actually credits this plant through %q. The "+
		"witness is the whole content of the entry, so one that does not reproduce is worse than "+
		"none: %s", key, keyword, reason)
}

// substringHazardLength is where TestShortKeywordsAreSubstringHazards draws
// its
// line.
//
// The note behind it is in docs/measurement.md#substringhazardlength.
const substringHazardLength = 4

// TestShortKeywordsAreSubstringHazards asks the question no list can answer.
//
// mentionsAny is strings.Contains with no word-boundary test, so a short
// alphabetic keyword matches inside any longer word that spells it. The corpus
// sweep can only try words the corpus uses and the English sweep can only try
// words its author thought of; this needs neither, because the hazard is a
// property of the KEYWORD's shape.
//
// It cannot say which English word does the damage, so each entry has to, and
// the named word is run through the scorer, the registry supplies the
// counterexample and the test proves it, rather than the other way round.
func TestShortKeywordsAreSubstringHazards(t *testing.T) {
	// Keyed fixture|keyword. The witness goes in backticks.
	knownShortKeywords := map[string]string{
		"timezone-boundary|utc": "`outcome`. Three letters, and the one this corpus found by accident",
		"timezone-boundary|dst": "`midst`. Three letters",
		"multi-defect|race":     "`trace`, as in a stack trace, which is the single most ordinary noun in Go review prose",
		"removed-guard|idor":    "`corridor`. Four letters standing for a phrase (insecure direct object reference) rather than spelling a word, which is what makes an acronym keyword this dangerous",
	}

	alphabetic := func(s string) bool {
		for _, r := range s {
			if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') {
				return false
			}
		}
		return s != ""
	}

	seen := map[string]bool{}
	for _, f := range AllFixtures() {
		for _, d := range f.Defects {
			for _, kw := range d.Keywords {
				if !alphabetic(kw) || len(kw) > substringHazardLength {
					continue
				}
				key := f.Name + "|" + strings.ToLower(kw)
				reason, known := knownShortKeywords[key]
				if !known {
					t.Errorf("%s: the keyword %q is %d alphabetic characters, so mentionsAny credits "+
						"the plant at %s:%d for any word that spells it — substring containment has no "+
						"word boundary. Name the ordinary English word that reaches it, or lengthen "+
						"the keyword", f.Name, kw, len(kw), d.Path, d.Line)
					continue
				}
				seen[key] = true

				witness := backtickedStems(reason)
				if len(witness) == 0 {
					t.Errorf("entry %q names no witness word in backticks: %s", key, reason)
					continue
				}
				w := strings.ToLower(witness[0])
				if !strings.Contains(w, strings.ToLower(kw)) {
					t.Errorf("entry %q offers the witness %q, which does not contain %q", key, w, kw)
					continue
				}
				hay := fmt.Sprintf(inertProse, w)
				if !creditsAt(hay, "", d) || !slices.Contains(firedKeywords(hay, d), kw) {
					t.Errorf("entry %q offers the witness %q, which does not credit the plant at "+
						"%s:%d through %q. An unreproduced witness is an entry that has gone stale "+
						"without saying so", key, w, d.Path, d.Line, kw)
				}
			}
		}
	}

	for key, why := range knownShortKeywords {
		if !seen[key] {
			t.Errorf("short keyword %q is gone or has grown (%s); delete the entry rather than "+
				"carrying it", key, why)
		}
	}
}

// overlapKey identifies one cross-fixture collision: which PLANT was credited,
// and which source did it.
//
// Both halves were missing. See TestReviewProseAboutAnotherFixtureIsNotCredited
// for what each one absorbed.
func overlapKey(fixture string, i int, d Defect, s generatedSource) string {
	return plantKey(fixture, i, d) + "|" + s.fixture + "|" + s.kind + "|" + s.title
}

// backtickedStems is the `quoted` fragments of a registry reason.
func backtickedStems(reason string) []string {
	var out []string
	for i, part := range strings.Split(reason, "`") {
		if i%2 == 1 && strings.TrimSpace(part) != "" {
			out = append(out, part)
		}
	}
	return out
}

// checkReasonFires holds a registry reason to the credit it excuses.
//
// The reason is the only actionable part of an entry, and until now nothing
// compared it against what fired, only the KEY was checked for
// liveness. Rewording one probe so a different stem fires left the entry green
// under a reason naming the old one: replacing "permission errors" with
// "ownership errors" in python-command-injection's objection kept
// "bare stem `permission`: a permission ERRNO is not a permission CHECK" in the
// registry while the credit had moved to `ownership`, and nothing said so. An
// editor reading that entry would go looking for a keyword that was no longer
// involved.
//
// The rule is the weakest one a stale entry cannot satisfy: every backticked
// fragment that IS one of this plant's keywords must be among the keywords that
// fired, and at least one backticked fragment must be a keyword at all. Reasons
// quote other things too, `concurrent` is named to say it was REMOVED from the
// plant, so a fragment that is not a keyword of this defect is prose about the
// history, not a claim about the firing, and is left alone.
func checkReasonFires(t *testing.T, key, reason string, fired []string, d Defect) {
	t.Helper()

	has := func(list []string, want string) bool {
		return slices.ContainsFunc(list, func(s string) bool { return strings.EqualFold(s, want) })
	}

	named := 0
	for _, q := range backtickedStems(reason) {
		if !has(d.Keywords, q) {
			continue
		}
		named++
		if !has(fired, q) {
			t.Errorf("entry %q blames the keyword %q, which this credit does not fire. It fires %v. "+
				"The reason is what the next editor acts on, so a stale one sends them to the wrong "+
				"keyword: %s", key, q, fired, reason)
		}
	}
	if named == 0 {
		t.Errorf("entry %q names no keyword of the plant it excuses, so nothing in it can be checked "+
			"against the %v this credit actually fires. Quote the stem in backticks: %s",
			key, fired, reason)
	}
}

// usedBy names the fixtures whose material contains a word, so a failure says
// where the reviewer would have got it.
func usedBy(vocab map[string]map[string]bool, word, except string) []string {
	var out []string
	for fixture, m := range vocab {
		if fixture != except && m[word] {
			out = append(out, fixture)
		}
	}
	slices.Sort(out)
	return out
}

// TestKeywordsAreNotTokensOfTheirOwnChange mechanizes the rule Defect.Keywords
// states in prose and nothing checked.
//
// That doc comment says a keyword "must not be a token a reviewer would type
// merely by QUOTING the change", and names six that were: created_at is the line
// the diff removes, foreach and await are the changed line itself. Every one was
// found by hand. The rule is mechanical. The added lines are right there in
// Head, so the corpus can be asked directly, and it answers with two keywords
// the six removals left behind.
//
// It compares against the lines the DIFF SHOWS, added to the head and removed
// from the base, and it asks whether the keyword is IN one, not whether it
// equals a token of one: quoting a line reproduces the whole line, so `sk-live`
// inside the literal "sk-live-51H..." is quoted just as surely as a bare
// identifier is.
//
// REMOVED LINES ARE IN SCOPE, and leaving them out was this test failing at its
// own first example. The paragraph above names `created_at` as one of the six,
// and `created_at` is the line contract-break DELETES: it appears nowhere in the
// head, so a rule reading Head-minus-Base could never have found it. Restoring
// `created_at` left this test green (four others caught it, none of them this
// one), and `json:"created_at"`, a verbatim token of the removed line, which
// anyone pasting the hunk types, and which no other source in the corpus
// contains, left the ENTIRE deterministic suite green. A reviewer reads both
// halves of a hunk; a rule that reads one half checks half the claim.
//
// Lines that survive untouched into the head are still out of scope. That is a
// weaker complaint (the doc comment lists `owner` as one) and it would flag most
// of the corpus, because naming the function under review is ordinary and
// useful. That exclusion is a judgement, not a consequence of the mechanism, so
// it is stated here rather than left to be inferred from which lines the helper
// happens to read.
func TestKeywordsAreNotTokensOfTheirOwnChange(t *testing.T) {
	// Keyed fixture|keyword. Each entry has to say what makes the keyword worth
	// keeping anyway, because "it is quoted" is the whole objection.
	knownQuotedTokens := map[string]string{
		"go-nil-deref|http.get": "`resp, _ := http.Get(url)` IS the defect line, so \"consider a timeout on http.Get\" is credited with finding the " +
			"discarded error. It is redundant as well as leaky — the cached detection it was presumably kept for, \"http.Get's error is discarded\", " +
			"is credited by `discard` — but removing it changes what the shipped cache matches, so it belongs to a change that re-pins those figures",
		"go-nil-deref|defer resp": "`defer resp.Body.Close()` is an added line, so a reviewer objecting to the deferred close on any grounds quotes it. " +
			"It is the phrase that replaced the bare stems `close`/`defer` removed in the change before this one, and it inherited their problem in a narrower form",
		"multi-defect|h.count": "`h.count++` IS the raced line",
		"multi-defect|mutex":   "`mu sync.Mutex` is an added line, so the struct field this plant says is unused is itself quotable. Also reachable as a bare word from another fixture's source",
		"cross-file-sort-nit|already in order": "the added COMMENT says \"that is already in order\", so a reviewer who quotes the author's own claim back is credited with " +
			"noticing that the claim makes the sort redundant. This is the shape of `location`, one of the six the doc comment lists: a keyword that lives in prose the change ships",
	}

	seen := map[string]bool{}
	for _, f := range AllFixtures() {
		shown := diffLines(f)
		for _, d := range f.Defects {
			for _, kw := range d.Keywords {
				line, quoted := quotedIn(shown, kw)
				if !quoted {
					continue
				}
				key := f.Name + "|" + strings.ToLower(kw)
				if _, known := knownQuotedTokens[key]; known {
					seen[key] = true
					continue
				}
				t.Errorf("%s: the keyword %q is inside a line this change %s (%q), so a reviewer who "+
					"quotes %s:%d without noticing anything is credited with the plant. Defect.Keywords "+
					"says a keyword must not be one", f.Name, kw, line.verb(), line.text, d.Path, d.Line)
			}
		}
	}

	for key, why := range knownQuotedTokens {
		if !seen[key] {
			t.Errorf("quoted token %q is no longer in the lines the diff shows (%s); delete the entry "+
				"rather than carrying it", key, why)
		}
	}
}

// diffLine is one line a change SHOWS in its hunks.
type diffLine struct {
	text    string
	removed bool
}

// verb says which half of the hunk the line is, because the two are different
// arguments: an added line is the code under review, and a removed one is the
// code the reviewer is being asked to compare it against.
func (l diffLine) verb() string {
	if l.removed {
		return "REMOVES"
	}
	return "ADDS"
}

// diffLines is the lines a fixture's change introduces or deletes, compared
// whole so a line that merely moved is counted as neither.
func diffLines(f Fixture) []diffLine {
	whole := func(files map[string]string) map[string]bool {
		out := map[string]bool{}
		for _, body := range files {
			for _, line := range strings.Split(body, "\n") {
				out[strings.TrimSpace(line)] = true
			}
		}
		return out
	}
	base, head := whole(f.Base), whole(f.Head)

	var out []diffLine
	for line := range head {
		if line != "" && !base[line] {
			out = append(out, diffLine{text: line})
		}
	}
	for line := range base {
		if line != "" && !head[line] {
			out = append(out, diffLine{text: line, removed: true})
		}
	}
	slices.SortFunc(out, func(a, b diffLine) int {
		if a.removed != b.removed {
			// Added first, so a keyword quotable from both halves is reported
			// against the code under review rather than against its predecessor.
			if a.removed {
				return 1
			}
			return -1
		}
		return strings.Compare(a.text, b.text)
	})
	return out
}

// quotedIn reports the first line of the diff containing a keyword, matched the
// way mentionsAny would match it against a finding that quoted that line.
func quotedIn(shown []diffLine, keyword string) (diffLine, bool) {
	for _, line := range shown {
		if strings.Contains(strings.ToLower(line.text), strings.ToLower(keyword)) {
			return line, true
		}
	}
	return diffLine{}, false
}

// knownWhyGaps is the set of plants whose own Why their own keywords do not
// admit. Each is a real recall hole, recorded with the phrase that would close it
// so the next change to that fixture file is not left to rediscover it.
//
// Note what these three have in common: the keyword list names the FIX
// (compare_digest, placeholder, "already returns a copy") and the Why names the
// DEFECT. A reviewer that reports the defect without proposing the canonical
// remedy is scored a miss on all three.
//
// It is hoisted out of the test that consumes it because
// TestNaturalPhrasingsOfAPlantStayCredited has to skip these plants: every
// shorter phrasing of a Why that is itself uncredited is uncredited too, so
// generating them would report thirteen derived gaps that are one gap each.
func knownWhyGaps() map[string]string {
	return map[string]string{
		"go-sql-injection|0|store.go:17": "the Why says `interpolated`; the list carries `concatenat` and not `interpolat`, " +
			"so it credits python-command-injection's description of ITS plant and not its own",
		"python-timing-unsafe-hmac|0|webhook.py:15": "the Why says `returns at the first differing byte`; the list carries " +
			"`byte-by-byte` and `byte by byte`, neither of which that phrase contains",
		"cross-file-copy-nit|0|report/summary.go:17": "the Why says `already returns a slice the caller owns` and `copying it " +
			"again`; the list carries `already returns its own` and `copies it again`, which miss both by a word",
	}
}

// plantKey identifies one plant.
//
// It carries the DEFECT INDEX as well as the location because multi-defect plants
// its traversal and its descriptor leak on the same line of the same file: keyed
// by location alone, an entry written to excuse one of them silently excuses the
// other, and the two are different defects with different keywords.
func plantKey(fixture string, i int, d Defect) string {
	return fmt.Sprintf("%s|%d|%s:%d", fixture, i, d.Path, d.Line)
}

// TestEveryPlantIsCreditedForItsOwnDescription is the recall half, which until
// now did not exist.
//
// Sixteen keywords were removed in one round, each with a probe proving it no
// longer credited an objection, and nothing anywhere asserted that what was left
// still credited a DETECTION. Six plain statements of a correct finding came
// back MISSED as a result, including one fixture's own proposed fix, and the
// harness would have reported a reviewer that found the plant and said so as
// having missed it. Every removal is cheap in that direction and nothing was
// charging for it.
//
// Defect.Why is the generator, because it is the corpus's own one-sentence
// statement of what a reviewer should report and it is written before the
// keywords are. If a plant's keywords cannot credit its Why, the two have
// drifted, and the Why is the half that was reviewed as ground truth.
//
// SeverityNote WAS MEASURED AS A SECOND SOURCE AND REJECTED, which is worth
// recording because it is the obvious next field to reach for. Run through
// matches() over every plant it is credited for 13, uncredited for 10, and absent
// from 6, and the first version of this paragraph said "credited for 14 and
// uncredited for 15", which folded the six plants that carry NO NOTE AT ALL into
// the evidence. An empty haystack is uncredited by construction, so 40 percent of
// the count the argument rested on was never a measurement of anything. The
// figures are recomputed below and this sentence is read back out of the source,
// on the same argument TestTheFiguresTheseCommentsQuoteStillReproduce makes: a
// corrected number is worth nothing if the next fixture makes it wrong in
// silence.
//
// The argument survives the correction, on the nine that were really measured: a
// SeverityNote argues about which ANCHOR in review.md a plant sits under and
// quotes that anchor's words, so it is prose about the severity table rather than
// about the code. Asserting it must be credited would pull keyword lists toward
// the vocabulary of review.md's severity examples, which is not where a
// reviewer's words come from.
func TestEveryPlantIsCreditedForItsOwnDescription(t *testing.T) {
	knownGaps := knownWhyGaps()

	seen := map[string]bool{}
	for _, f := range AllFixtures() {
		for i, d := range f.Defects {
			// Category is left empty on purpose. mentionsAny reads it, so
			// putting the defect's Class there would let a plant whose keywords
			// name its own class pass on the class name alone.
			at := review.Finding{
				Path: d.Path, Line: d.Line,
				Severity:  string(d.WantSeverity),
				Title:     "",
				Rationale: d.Why,
			}
			key := plantKey(f.Name, i, d)
			if matches(at, d) {
				continue
			}
			if _, known := knownGaps[key]; known {
				seen[key] = true
				continue
			}
			t.Errorf("%s: the plant's own Why is not credited by its own keywords, so a reviewer "+
				"reporting it in these words is scored a miss: %s:%d — %q",
				f.Name, d.Path, d.Line, d.Why)
		}
	}

	for key, why := range knownGaps {
		if !seen[key] {
			t.Errorf("recall gap %q is closed (%s); delete the entry rather than carrying it", key, why)
		}
	}

	checkSeverityNoteCensus(t)
}

// TestNaturalPhrasingsOfAPlantStayCredited generates the recall direction, which
// until now was one sentence per plant.
//
// THE FAILURE IT IS BUILT FOR is a keyword removal that quietly costs recall.
// That has happened: sixteen were removed in one round, each with a probe proving
// it no longer credited an objection, and six plain statements of a correct
// finding came back MISSED, nothing was charging for the other direction. The
// repair at the time was to hand-author more hit probes, which is the same
// method, and the same method finds the same things.
//
// ONLY SHORTENING CAN GENERATE A PROBE HERE. mentionsAny is strings.Contains, so
// a phrasing built by adding words to Why, "I think ...", "... please fix", is
// credited by construction and proves nothing about the keywords. What can fail
// is a reviewer who said LESS: the same finding with a clause dropped, or one
// clause of it on its own. Both families are generated from the Why, which is the
// corpus's own statement of what a reviewer should report and is written before
// the keyword list is.
//
// SAYING LESS IS NOT THE SAME AS BEING A SUBSTRING, and the two were conflated
// here. One clause on its own IS a substring of the Why. Dropping a middle
// clause is not: the survivors are rejoined with ", ", a string the Why never
// contained, so that family manufactures adjacencies the corpus never wrote,
// three of today's fifty-two phrasings, all from python-timing-unsafe-hmac. A
// keyword spanning the new seam would be credited on text no reviewer typed, and
// this test would read that as the plant staying credited, which is a recall
// credit the probe paid itself. checkNoSeamCredit is the guard, and the skip
// below rests on it rather than on the substring claim it used to assert.
//
// WHAT A FAILURE MEANS IS NOT UNIFORM, and the registry has to say which of two
// things it is, because a list that calls both "gaps" is a list of excuses:
//
//   - BY DESIGN. Most Whys are "<what the change did>, so <what goes wrong>", and
//     several fixtures deliberately refuse to credit the first half, a reviewer
//     who says only "created_at is renamed to createdAt on a public payload" has
//     noticed the edit and not that it breaks anyone, which is contract-break's
//     whole distinction. Crediting these would undo a decision, not close a hole.
//   - RECALL GAP. The clause names the DEFECT and is still not credited, so a
//     reviewer who writes it is scored a miss. Eight of the thirteen entries are
//     this, and most miss by an inflection: the corpus says "never releases an
//     entry" and the list carries "never released".
//
// THE EIGHT ARE RECORDED RATHER THAN CLOSED, deliberately. Adding a keyword
// changes which cached Incumbent findings match a plant, and those matches feed
// figures that are pinned as evidence elsewhere in this package, the shipped
// cache's severity triple among them. Closing a recall hole is worth doing and it
// is a change that has to re-derive those numbers in the same commit, not a
// side effect of building the instrument that found them.
//
// SeverityNote is NOT a source, and it is the obvious second one to reach for, so
// the refusal is measured rather than asserted. Split into sentences it runs
// 15 credited, 63 uncredited, because a note argues which anchor in review.md a
// level sits under and compares the plant to others BY NAME, so most of its
// sentences are prose about the severity table. Requiring them would pull every
// keyword list toward that table's vocabulary, which is not where a reviewer's
// words come from. checkSeverityNoteSentenceCensus recomputes both figures and
// reads this paragraph back out of the source, on the argument the whole-note
// census beside it already makes: a number in a comment that nothing reproduces
// is how the last three retracted claims survived as long as they did.
func TestNaturalPhrasingsOfAPlantStayCredited(t *testing.T) {
	// Keyed plant|phrase. Every reason must begin with one of the two markers,
	// which is itself asserted below.
	const (
		byDesign  = "by design: "
		recallGap = "recall gap: "
	)
	knownPhrasingGaps := map[string]string{
		"multi-defect|0|handler.go:19|the query parameter is concatenated into a filesystem path": byDesign +
			"the clause states the concatenation without saying anything escapes. `concatenat` is go-sql-injection's keyword, not this plant's",
		"capacity-hint-nit|0|window.go:17|the loop bound is correct": byDesign +
			"the clause says the code is RIGHT; it is the half of the Why that rules out the other reading",
		"php-forbidden-vs-404|0|src/Http/ProjectController.php:23|the new membership check denies non-members with 403": byDesign +
			"the clause describes the change. The plant is what a 403 discloses, and a reviewer who has only restated the diff has not said it",
		"contract-break|0|event.go:9|created_at is renamed to createdAt on a public payload": byDesign +
			"`created_at` was deliberately removed as a keyword because it is the line the diff REMOVES, so every style objection quotes it",
		"csharp-client-per-request|0|src/Notifier.cs:17|a new HttpClient is created and disposed per call": byDesign +
			"the clause restates the code. This plant's detection is the socket exhaustion, which the other clause names",

		"data-loss-migration|0|migrations/0007_backfill_plan.sql:8|the UPDATE has no WHERE": recallGap +
			"the terse form of the whole defect. The list carries `where clause`, `missing where` and `without a where`, and \"has no WHERE\" is none of them",
		"go-cancel-goroutine-leak|0|resolve.go:24|when ctx is done first nothing ever receives and the sending goroutine blocks for the life of the process": recallGap +
			"the list carries `never receives`, `nobody receives` and `blocks forever`; the Why says \"nothing ever receives\" and \"blocks for the life of the process\"",
		"ts-unbounded-memo-key|0|src/search.ts:13|search.ts keys a memo table that never releases an entry by trimmed request text": recallGap +
			"off by an inflection: the list carries `never released` and the Why says \"never releases\"",
		"kotlin-widened-input|0|src/main/kotlin/com/example/report/Summary.kt:27|the package now accepts an open set it cannot narrow again": recallGap +
			"the list carries `cannot be narrowed` and `narrow it back`; the Why says \"cannot narrow again\"",
		"go-package-singleton|0|features/features.go:31|the answer is fixed for the whole binary and a test or a second consumer that needs a different set has to reach into the package variable": recallGap +
			"`answer for the whole` is on the list and is a soleCreditors entry, and the Why says \"answer is fixed for the whole binary\", which does not contain it",
		"retry-no-backoff|0|client.py:11|the retries fire immediately one after another": recallGap +
			"the list names the REMEDY — `backoff`, `sleep`, `jitter`, `delay between` — and the Why names the behaviour",
		"cross-file-sort-nit|0|src/roster.ts:6|renderRoster copies and re-sorts it once per render for an identical result": recallGap +
			"the list carries `sorts them again` and `sorted twice`; the Why says \"re-sorts\", and `re-sort` is on no list",
		"rust-crate-for-one-call|0|Cargo.toml:7|every build, lockfile bump and audit carries it for one formatted string": recallGap +
			"the list is about the single CALL SITE and this clause is about the standing cost, which is the other half of why the plant is reportable at all",
	}

	for key, why := range knownPhrasingGaps {
		if !strings.HasPrefix(why, byDesign) && !strings.HasPrefix(why, recallGap) {
			t.Errorf("entry %q gives no verdict: a refusal the corpus INTENDS and a hole it has not "+
				"closed need different answers, so every reason must start %q or %q", key, byDesign, recallGap)
		}
	}

	seen := map[string]bool{}
	probes := 0
	for _, f := range AllFixtures() {
		for i, d := range f.Defects {
			whyCredited := creditsAt(d.Why, "", d)
			for _, phrasing := range whyPhrasings(d.Why) {
				// Runs for every plant, credited Why or not: a seam credit is a
				// defect in the PROBE, and a probe that pays itself is worth
				// catching wherever it happens.
				checkNoSeamCredit(t, f.Name, d, phrasing)
				if !whyCredited {
					// The whole Why is uncredited, which is
					// TestEveryPlantIsCreditedForItsOwnDescription's failure and
					// its registry; reporting the derived phrasings here would
					// turn one gap into thirteen.
					//
					// For a single clause this is a proof: a clause is a
					// substring of the Why, mentionsAny is containment, so no
					// keyword inside the Why means none inside a piece of it.
					// For the leave-one-out family it is not. Those are not
					// substrings, and skipping them is a judgement that a gap
					// derived from a gap is the same gap, backed by the seam
					// check above rather than by an argument they cannot be
					// credited.
					continue
				}
				probes++
				// Category and title are empty for the reason the whole-Why
				// probe leaves them empty: mentionsAny reads both.
				if creditsAt(phrasing, "", d) {
					continue
				}
				key := plantKey(f.Name, i, d) + "|" + phrasing
				if _, known := knownPhrasingGaps[key]; known {
					seen[key] = true
					continue
				}
				t.Errorf("%s: a reviewer who reports the plant at %s:%d in the corpus's own words, "+
					"shortened, is scored a miss: %q. The full Why is credited and this is not, so the "+
					"credit rests on the part that was dropped", f.Name, d.Path, d.Line, phrasing)
			}
		}
	}

	if probes < 40 {
		t.Errorf("only %d phrasings were generated; the recall direction is not covering the corpus", probes)
	}

	for key, why := range knownPhrasingGaps {
		if !seen[key] {
			t.Errorf("phrasing %q is no longer generated-and-uncredited (%s). Either it is credited "+
				"now, or its Why was reworded, or that plant's whole Why stopped being credited and "+
				"the gap moved to TestEveryPlantIsCreditedForItsOwnDescription. All three want the "+
				"entry re-derived rather than carried", key, why)
		}
	}

	checkSeverityNoteSentenceCensus(t)
}

// checkSeverityNoteSentenceCensus recomputes the two figures the paragraph above
// quotes for rejecting SeverityNote as a phrasing source, and reads that
// paragraph back out of the source.
func checkSeverityNoteSentenceCensus(t *testing.T) {
	t.Helper()

	credited, uncredited := 0, 0
	for _, f := range AllFixtures() {
		for _, d := range f.Defects {
			for _, s := range probeSentences(d.SeverityNote) {
				if creditsAt(s, "", d) {
					credited++
					continue
				}
				uncredited++
			}
		}
	}

	want := fmt.Sprintf("// %d credited, %d uncredited", credited, uncredited)
	src, err := os.ReadFile("groundtruth_test.go")
	if err != nil {
		t.Fatalf("reading groundtruth_test.go: %v", err)
	}
	if !strings.Contains(string(src), want) {
		t.Errorf("TestNaturalPhrasingsOfAPlantStayCredited no longer says %q. The corpus now measures "+
			"%d credited and %d uncredited SeverityNote sentences, and the paragraph rejecting that "+
			"field as a source quotes figures it no longer has", want, credited, uncredited)
	}
}

// checkNoSeamCredit catches a recall credit the PROBE earned rather than the
// corpus.
//
// whyPhrasings' leave-one-out family drops a clause and rejoins the survivors
// with ", ", which the Why never contained, so it can spell an adjacency no
// reviewer wrote. A keyword that fires only across that seam is credited on
// manufactured text, and the recall test would count it as the plant staying
// credited. The probe would be answering its own question.
//
// It is not reachable on today's corpus: the three non-substring phrasings all
// come from a plant whose whole Why is a known gap, and none of them fires
// anything. The guard is here because the argument that made it unnecessary was
// false, not because a failure was seen.
func checkNoSeamCredit(t *testing.T, fixture string, d Defect, phrasing string) {
	t.Helper()

	if strings.Contains(d.Why, phrasing) {
		return // a genuine shortening: every keyword it fires is in the Why
	}
	for _, kw := range firedKeywords(phrasing, d) {
		if strings.Contains(strings.ToLower(d.Why), strings.ToLower(kw)) {
			continue
		}
		t.Errorf("%s: the generated phrasing %q fires %q, which the plant's own Why at %s:%d does "+
			"NOT contain. whyPhrasings rejoined two clauses with a separator the corpus never wrote "+
			"and the keyword spans the seam, so this credit is the probe's, not a reviewer's: %q",
			fixture, phrasing, kw, d.Path, d.Line, d.Why)
	}
}

// whyPhrasings is the same finding said in fewer words: each clause of a Why on
// its own, and the Why with each single clause dropped.
//
// FEWER WORDS, NOT ALWAYS A SUBSTRING. A single clause is a substring of the
// Why. A leave-one-out phrasing is not. The survivors are rejoined with ", ",
// which the original may never have contained, so the family creates strings
// the corpus did not write. checkNoSeamCredit is what keeps that from being
// scored as recall.
//
// The separators are the ones this corpus's Whys are built from, a
// statement of what the change did, then what goes wrong, joined by "so", "which"
// or a semicolon. A clause shorter than three words is dropped because it is
// punctuation noise rather than a phrasing.
//
// Nothing is trimmed from the LEFT of a clause. An earlier version trimmed
// leading punctuation and turned "../ escapes the upload directory" into "/
// escapes the upload directory", which deletes the keyword and reports a recall
// gap that does not exist.
func whyPhrasings(why string) []string {
	clauses := []string{why}
	for _, sep := range []string{", so ", ", which ", ", and ", ", but ", " because ", "; "} {
		var next []string
		for _, c := range clauses {
			next = append(next, strings.Split(c, sep)...)
		}
		clauses = next
	}

	var kept []string
	for _, c := range clauses {
		if c = strings.TrimRight(strings.TrimSpace(c), ".,;"); len(strings.Fields(c)) >= 3 {
			kept = append(kept, c)
		}
	}
	if len(kept) < 2 {
		return nil // one clause: the whole-Why probe already covers it
	}

	out := append([]string(nil), kept...)
	if len(kept) > 2 {
		// With two clauses, dropping one IS the other, so the leave-one-out
		// family would be the clause family spelled twice: every failure would
		// print itself, and the probe count would say the corpus is covered
		// twice as well as it is.
		for i := range kept {
			var rest []string
			for j, c := range kept {
				if j != i {
					rest = append(rest, c)
				}
			}
			out = append(out, strings.Join(rest, ", "))
		}
	}
	return out
}

// checkSeverityNoteCensus recomputes the three figures the paragraph above
// quotes and reads that paragraph back out of the source.
//
// The number that mattered was the one nobody separated: six plants carry no
// SeverityNote, so they are uncredited because there is nothing to credit, and
// counting them as evidence about severity-table vocabulary made a 14-to-9 split
// look like 14-to-15. Recording the absent count is the whole repair. It is the
// figure that says how much of the argument is measurement.
func checkSeverityNoteCensus(t *testing.T) {
	t.Helper()

	credited, uncredited, absent := 0, 0, 0
	for _, f := range AllFixtures() {
		for _, d := range f.Defects {
			switch {
			case strings.TrimSpace(d.SeverityNote) == "":
				absent++
			case matches(review.Finding{Path: d.Path, Line: d.Line, Rationale: d.SeverityNote}, d):
				credited++
			default:
				uncredited++
			}
		}
	}

	want := fmt.Sprintf("credited for %d, uncredited for %d, and absent\n// from %d", credited, uncredited, absent)
	src, err := os.ReadFile("groundtruth_test.go")
	if err != nil {
		t.Fatalf("reading groundtruth_test.go: %v", err)
	}
	if !strings.Contains(string(src), want) {
		t.Errorf("TestEveryPlantIsCreditedForItsOwnDescription no longer says %q. The corpus now measures "+
			"%d credited, %d uncredited and %d with no SeverityNote at all; a paragraph quoting other "+
			"figures is the same defect it was written to retract", want, credited, uncredited, absent)
	}
}

// TestTheInfoRecallThisInstrumentCannotBuy records a limit of substring keywords
// rather than moving the boundary a third time.
//
// Two rounds have now edited php-forbidden-vs-404's keyword list to separate its
// finding from its false positive, and both edits moved which sentences fall on
// which side without making the boundary sharper. This test says why, in a form
// that is checked rather than asserted.
//
// THE FINDING and THE OBJECTION propose the SAME FIX. The plant is that a 403
// and a 404 are distinguishable, so the fix is to make the two denials identical;
// the excluded objection asks for the two error BODIES to share one envelope,
// whose fix is also to make the two responses look the same. What separates them
// is the reason, and a reviewer writing tersely does not always give one.
//
// For the pair below the separation is not merely hard, it is unavailable.
// mentionsAny is strings.Contains over title+rationale+category, the objection
// contains the finding as a substring, and containment is transitive: any keyword
// matching the finding matches the objection too. No word list can credit the
// first and deny the second, so this is a property of the PREDICATE and no amount
// of editing Keywords reaches it.
//
// The corpus resolves the tie by denying both, and this test pins that choice so
// it stays a choice. The price is stated in the assertion: a reviewer whose whole
// finding is "make the two denials the same" has proposed exactly this plant's
// fix and is scored a miss. Info recall on this fixture is therefore a LOWER
// BOUND, not a measurement, and any number reported from it should say so.
//
// WHAT WOULD ACTUALLY FIX IT, none of which is a keyword:
//
//   - A CONJUNCTION. Let a defect require one phrase from each of two lists,
//     here, a fix phrase AND a phrase naming what the caller learns, so "the
//     same response" credits only when the finding also says why. This is a
//     change to Defect and to matches().
//   - A VETO. Let a defect name phrases that withdraw credit, which would also
//     close ruby-default-page-size's "other consumers can still" case. Same
//     surface, opposite sign, and it makes an unmatched veto silently
//     unfalsifiable, so it needs its own probe direction.
//   - THE JUDGE. Detection at info could be scored by the model judge against
//     Defect.Why rather than by substring, keeping keywords for the levels whose
//     vocabulary is distinctive, nil, injection, WHERE clause. It costs money
//     per run and imports the judge's variance into recall, which is the column
//     the harness is most often read for.
//
// A fourth option is not on the list and should be named so it is not chosen by
// default: adding a keyword that encodes tense or modality, "now receives" as
// against "can still receive", would separate a looser version of this pair. It
// is not impossible, it is a DIFFERENT KIND of keyword: every other entry in
// this corpus names subject matter, and one that names grammar is a rule about
// how a sentence is built rather than about what it says. That is the
// boundary-moving this test exists instead of.
//
// RUBY IS THE SECOND CASE AND IT IS A CHOICE, NOT AN IMPOSSIBILITY, which is why
// it is recorded here beside the proof rather than inside it. Its finding and
// its cap objection are separable on subject matter after all. The cap is 200
// and the new default is 100, so a keyword naming the ROWS reaches one and not
// the other, and "rows by default" is that keyword. What stays out of reach is
// the same fix stated as a LOCATION: "set it at the call site instead" is this
// plant's proposed remedy, and every phrase that credits it is one a reviewer
// types about any line in any file. The second half of this test runs that
// sentence, runs the phrase that would credit it against three unrelated
// remarks, and runs the one candidate that dodged the location and was still
// rejected. Info recall on ruby is therefore a lower bound too, by one phrasing
// rather than by all of them.
func TestTheInfoRecallThisInstrumentCannotBuy(t *testing.T) {
	byName := map[string]Fixture{}
	for _, f := range AllFixtures() {
		byName[f.Name] = f
	}

	fixture, ok := byName["php-forbidden-vs-404"]
	if !ok {
		t.Fatal("php-forbidden-vs-404 no longer exists; this limit was recorded against it")
	}
	d := fixture.Defects[0]

	// The finding a terse reviewer writes once they have seen the disclosure:
	// it proposes this plant's fix and nothing else.
	detection := "The two denials should return the same response"

	// The objection is READ OUT OF THE PROBE TABLE rather than built from the
	// detection. It used to be `detection + " envelope; ..."` with a
	// strings.Contains guard underneath, which is true for every possible value
	// of detection, run with "", with unrelated prose and with a non-ASCII
	// string, the guard passed each time. An unfalsifiable check inside the test
	// whose stated purpose is to make an impossibility argument checkable is
	// this round's own defect one level up. Now the objection is a sentence the
	// corpus already asserts must not be credited, and rewording that probe
	// fails here rather than silently making the proof vacuous.
	objection := ""
	for _, p := range declaredProbes()["php-forbidden-vs-404"].miss {
		if p.finding.Title == "One error envelope" {
			objection = p.finding.Rationale
		}
	}
	if objection == "" {
		t.Fatal(`the "One error envelope" miss probe is gone, so the objection this proof is about is ` +
			`no longer asserted anywhere; restore it or retire this test`)
	}
	if !strings.Contains(objection, detection) {
		t.Fatalf("the corpus's error-body objection no longer contains the terse detection, so the "+
			"containment this test rests on is unproven:\n  detection %q\n  objection %q", detection, objection)
	}

	at := func(f Fixture, text string) review.Finding {
		p := f.Defects[0]
		return review.Finding{Path: p.Path, Line: p.Line, Severity: "info", Rationale: text}
	}

	if matches(at(fixture, detection), d) {
		t.Errorf("the terse detection is credited, which by containment means the error-body objection is too; "+
			"TestKeywordsAdmitOnlyRealDetections should be failing alongside this: %q", detection)
	}
	if matches(at(fixture, objection), d) {
		t.Errorf("the error-body objection is credited, which this fixture's doc comment says it must not be: %q", objection)
	}

	ruby, ok := byName["ruby-default-page-size"]
	if !ok {
		t.Fatal("ruby-default-page-size no longer exists; the second half of this limit was recorded against it")
	}

	// This plant's proposed remedy, stated as a place to put the argument
	// instead of as a number. It is a correct finding and it is scored a miss.
	fix := "Raising DEFAULT_PER_PAGE changes what everyone gets; setting it at the call site leaves the mobile client alone."
	if matches(at(ruby, fix), ruby.Defects[0]) {
		t.Errorf("the location phrasing of ruby's fix is credited now, so this limit has been closed; "+
			"delete this half of the test and the paragraph in fixtures_info.go that cites it: %q", fix)
	}

	// WHY it is a miss, run rather than argued. Any keyword crediting that
	// sentence through its location clause is a substring of "at the call site",
	// and these three remarks, about ordering, about an allocation, and about
	// where the clamp belongs, contain it while noticing nothing.
	for _, elsewhere := range []string{
		"Set the ordering at the call site rather than in the query object.",
		"This allocation happens at the call site, which is fine.",
		"The clamp(1, MAX_PER_PAGE) expression belongs at the call site, not in the constructor.",
	} {
		if !strings.Contains(strings.ToLower(elsewhere), "the call site") {
			t.Errorf("this remark no longer shares the location clause with ruby's fix, so it is not "+
				"evidence for the trade above: %q", elsewhere)
		}
	}

	// The one candidate that credited the fix without naming a call site. It
	// leaks nothing across the probe table and was still rejected, because the
	// cap objection reaches it in one ordinary sentence, which is the run that
	// decided it, and the reason it is here rather than in the keyword list.
	candidate := "what everyone gets"
	capObjection := "The clamp is what everyone gets in the end, so MAX_PER_PAGE is the real ceiling, not this constant."
	if !strings.Contains(strings.ToLower(fix), candidate) {
		t.Errorf("%q no longer credits ruby's fix, so it is not the candidate this paragraph rejects", candidate)
	}
	if !strings.Contains(strings.ToLower(capObjection), candidate) {
		t.Errorf("%q no longer reaches the cap objection, so the reason it was rejected is gone: %q",
			candidate, capObjection)
	}
	for _, kw := range ruby.Defects[0].Keywords {
		if strings.EqualFold(kw, candidate) {
			t.Errorf("%q is in ruby's keyword list, which credits the cap objection %q — the objection "+
				"this fixture's doc comment says it excludes", candidate, capObjection)
		}
	}
}

// TestEveryDefectDeclaresAUsableSeverity closes a silent hole in the ground
// truth.
//
// WantSeverity now drives a reported column, and its zero value is not inert:
// config.Severity("").Rank() falls through to SeverityInfo's rank, so a defect
// added without the field would silently grade every warning and above as
// INFLATED and every nit as UNDERSTATED, in the exact column the prompt is
// being tuned against, with nothing to say it happened.
func TestEveryDefectDeclaresAUsableSeverity(t *testing.T) {
	for _, f := range AllFixtures() {
		for _, d := range f.Defects {
			if !d.WantSeverity.IsFinding() {
				t.Errorf("%s: defect at %s:%d has WantSeverity %q, which is not a severity a finding can carry; "+
					"severity scoring would silently grade it against \"info\": %s",
					f.Name, d.Path, d.Line, d.WantSeverity, d.Why)
			}
		}
	}
}

// classPlant is one planted defect with the fixture carrying it, so a
// consistency failure can name both sides of a disagreement.
type classPlant struct {
	fixture string
	defect  Defect
}

// TestSeverityIsConsistentWithinADefectClass fails when two plants of the same
// class disagree about WantSeverity and neither says why.
//
// The security class is the case that motivated it. It holds three plants at
// critical and two at error, and the line between them is not visible in the
// severities: the criticals show the untrusted source, the removed guard or the
// credential IN the diff, and the two injections show only the sink, with
// reachability asserted by a Defect.Why the reviewer is never given. Unwritten,
// a reader sees a corpus answering one question two ways, and someone will
// eventually "fix" it in whichever direction the week's numbers prefer, since
// both directions look equally like tidying.
//
// A judgement about severity cannot be asserted mechanically, so this asserts
// the property that can be: that the corpus does not contradict itself in
// silence. Divergence stays legal, resource holds an error, a
// warning and a nit, but it has to be written down where the next editor
// reads it, and the note has to name the level it is defending, or moving the
// level leaves prose that argues for a number that is no longer there.
//
// The rule is deliberately strict about WHO writes it. When a class carries
// more than one severity every member of it needs a note, because with two
// plants disagreeing there is no fact about which is the outlier, and letting a
// single note excuse a class would let the next drift in through whichever
// member happened to be annotated.
//
// What it CANNOT do: a class with one plant has nothing to disagree with, so
// contract, data-loss and concurrency are exempt by construction, and Class is
// author-declared, so moving a plant's class along with its severity silences
// it. TestPlantedSeveritiesArePinned covers both.
func TestSeverityIsConsistentWithinADefectClass(t *testing.T) {
	byClass := map[config.Class][]classPlant{}

	for _, f := range AllFixtures() {
		for _, d := range f.Defects {
			class, known := d.Class.Normalize()
			if !known {
				// An unset or unrecognized class normalizes to one bucket, which
				// would silently group defects that have nothing to do with each
				// other and let this test pass by comparing the wrong things.
				t.Errorf("%s: defect at %s:%d declares class %q, which is not one of %v: %s",
					f.Name, d.Path, d.Line, d.Class, config.ClassNames(), d.Why)
				continue
			}

			byClass[class] = append(byClass[class], classPlant{fixture: f.Name, defect: d})
		}
	}

	for class, plants := range byClass {
		severities := map[config.Severity]bool{}
		for _, p := range plants {
			severities[p.defect.WantSeverity] = true
		}
		if len(severities) < 2 {
			continue
		}

		var silent, stale []string
		for _, p := range plants {
			note := strings.TrimSpace(p.defect.SeverityNote)
			where := fmt.Sprintf("%s (%s:%d)", p.fixture, p.defect.Path, p.defect.Line)

			switch {
			case note == "":
				silent = append(silent, where)
			case !strings.Contains(strings.ToLower(note), strings.ToLower(p.defect.WantSeverity.String())):
				// A note that never names its own level cannot go stale
				// visibly. Move the plant and the prose still reads as a
				// justification, for a number it no longer justifies, and the
				// next editor trusts it. Requiring the word is the only part of
				// a note a test can hold to the plant beside it.
				stale = append(stale, where)
			}
		}
		if len(silent) == 0 && len(stale) == 0 {
			continue
		}

		// One failure per class rather than per plant: the disagreement is a
		// property of the class, and reporting it once per member buries the
		// list of who has to answer for it under five copies of the question.
		if len(silent) > 0 {
			t.Errorf("class %q is planted at more than one severity (%s) with no reason given by %s. "+
				"Two plants of one class disagreeing in silence reads as drift, and the next editor "+
				"will resolve it in whichever direction the numbers prefer.",
				class, plantSeverities(plants), strings.Join(silent, ", "))
		}
		if len(stale) > 0 {
			t.Errorf("class %q: the note on %s never says which level it is defending, so it cannot be "+
				"read against the plant it sits beside; the class is planted at %s",
				class, strings.Join(stale, ", "), plantSeverities(plants))
		}
	}
}

// plantSeverities renders a class's disagreement as "go-sql-injection=critical,
// multi-defect=error", so a failure names it rather than only reporting that it
// exists. Corpus order is fixed, so the rendering is stable.
func plantSeverities(plants []classPlant) string {
	parts := make([]string, 0, len(plants))
	for _, p := range plants {
		parts = append(parts, fmt.Sprintf("%s=%s", p.fixture, p.defect.WantSeverity))
	}
	return strings.Join(parts, ", ")
}

// TestTuningCorpusCanFalsifyInflation is TestHeldOutCorpusCanFalsifyInflation's
// twin, for the corpus that shapes the prompt.
//
// The held-out set is spent once; the tuning set is what every iteration reads,
// so a one-sided severity distribution there is the more expensive of the two.
// capacity-hint-nit is the only plant in it below error, one fixture away from
// a corpus that cannot tell "rates everything at least error" from a calibrated
// reviewer.
//
// This asserts the weaker of the two properties it would like to. The corpus
// can also be degraded WITHOUT dropping that floor, by moving plants upward:
// raising two errors to critical took the tuning corpus from 6 observable
// inflations to 4 against a reviewer that answers critical to everything, and
// this test passed unchanged through it because the nit was still there.
// TestPlantedSeveritiesArePinned is what catches that; this one only keeps the
// floor from disappearing entirely.
func TestTuningCorpusCanFalsifyInflation(t *testing.T) {
	var lowest config.Severity
	for _, f := range Fixtures() {
		for _, d := range f.Defects {
			if lowest == "" || d.WantSeverity.Rank() < lowest.Rank() {
				lowest = d.WantSeverity
			}
		}
	}

	if lowest == "" {
		t.Fatal("the tuning corpus plants no defects at all")
	}
	if lowest.Rank() >= config.SeverityError.Rank() {
		t.Errorf("the lowest tuning plant is %q: the prompt is tuned against a corpus where over-claiming "+
			"cannot be observed, which is the failure the O-INFL column exists to measure", lowest)
	}
}

// TestPlantedSeveritiesArePinned makes every change to the ground truth an
// explicit one.
//
// WantSeverity is the target both severity columns are measured against, so
// editing it moves the score of every reviewer at once, including the
// incumbent it is being compared to, without touching a line of reviewer code.
// Nothing else in the tree could see such an edit. The class-consistency check
// cannot: it groups on Class, which the same editor declares, so moving a plant
// and its class together silences it. And it exempts any class with one member,
// which is three of these fourteen, contract-break, data-loss-migration and
// multi-defect's race could each be demoted to a nit with every other test
// still green.
//
// So the levels are written down twice. This table is not a second opinion
// about what the anchors say; it is a receipt, and its only job is to make a
// severity edit impossible to land without also editing the record of what the
// severity used to be, in a diff a reviewer reads.
//
// When this fails: check the new level against the anchor clause in
// internal/prompt/templates/review.md, check what it does to the corpus's
// ability to observe INFLATION (raising plants removes that ability), check what
// it does to TestNoDegenerateReviewerCanMaxOutAPublishedMetric, a corpus whose
// plants all sit at one level is one no severity metric can be falsified on, and
// that is how the withdrawn cross-tool column came to be maximised by a reviewer
// with no severity opinion at all, and then update the entry, along with the
// pinned incumbent numbers in TestIncumbentObjectiveSeverityOnTheShippedCache.
func TestPlantedSeveritiesArePinned(t *testing.T) {
	// Keyed "fixture/path:line/class". The class is in the key because two of
	// multi-defect's plants sit on the SAME line of the same file, the
	// traversal and the descriptor leak both anchor at handler.go:19, and
	// without it they would collide and this table would silently pin one of
	// them twice.
	want := map[string]config.Severity{
		"go-nil-deref/fetch.go:10/correctness":          config.SeverityError,
		"go-sql-injection/store.go:17/security":         config.SeverityError,
		"go-hardcoded-secret/client.go:12/security":     config.SeverityCritical,
		"python-command-injection/tools.py:12/security": config.SeverityError,
		"multi-defect/handler.go:19/security":           config.SeverityCritical,
		"multi-defect/handler.go:25/concurrency":        config.SeverityError,
		"multi-defect/handler.go:19/resource":           config.SeverityError,
		"capacity-hint-nit/window.go:17/resource":       config.SeverityNit,

		// The warning and nit plants that ended the skew. These are the levels
		// the corpus previously could not resolve at all, one warning and one
		// nit across fifteen fixtures, so a drift here is not one plant moving,
		// it is the bottom of the scale becoming unmeasurable again.
		"ts-unbounded-memo-key/src/search.ts:13/resource":   config.SeverityWarning,
		"go-cancel-goroutine-leak/resolve.go:24/resource":   config.SeverityWarning,
		"python-timing-unsafe-hmac/webhook.py:15/security":  config.SeverityWarning,
		"cross-file-copy-nit/report/summary.go:17/resource": config.SeverityNit,
		"sorted-for-min-nit/sensors.py:23/resource":         config.SeverityNit,

		// The info plants, which took the corpus from four resolvable levels to
		// five. Each is argued against the anchors in its own SeverityNote, and
		// each is the FIRST plant of its class at this level, so unlike the
		// levels above, nothing else in the corpus disagrees with them yet and
		// the pin is the only record of what they were.
		"kotlin-widened-input/src/main/kotlin/com/example/report/Summary.kt:27/maintainability": config.SeverityInfo,
		"php-forbidden-vs-404/src/Http/ProjectController.php:23/security":                       config.SeverityInfo,
		"go-package-singleton/features/features.go:31/maintainability":                          config.SeverityInfo,

		// Held-out corpus. Pinned on the same terms: it is spent once, so a
		// severity edit here is discovered at the moment the number it
		// corrupted is already being reported.
		"contract-break/event.go:9/contract":                                config.SeverityError,
		"data-loss-migration/migrations/0007_backfill_plan.sql:8/data-loss": config.SeverityCritical,
		"ts-unawaited-async/src/sync.ts:10/correctness":                     config.SeverityError,
		"timezone-boundary/report.go:13/correctness":                        config.SeverityError,
		"removed-guard/project.go:31/security":                              config.SeverityCritical,
		"retry-no-backoff/client.py:11/resource":                            config.SeverityWarning,

		"csharp-client-per-request/src/Notifier.cs:17/resource":                       config.SeverityWarning,
		"bash-fixed-temp-path/scripts/release-notes.sh:8/security":                    config.SeverityWarning,
		"cross-file-sort-nit/src/roster.ts:6/resource":                                config.SeverityNit,
		"duplicate-test-case-nit/slug/slug_test.go:16/tests":                          config.SeverityNit,
		"defensive-copy-nit/src/main/java/com/example/report/Labels.java:26/resource": config.SeverityNit,

		"rust-crate-for-one-call/Cargo.toml:7/maintainability":            config.SeverityInfo,
		"ruby-default-page-size/app/queries/comments_query.rb:9/resource": config.SeverityInfo,
	}

	got := map[string]config.Severity{}
	for _, f := range AllFixtures() {
		for _, d := range f.Defects {
			class, _ := d.Class.Normalize()
			key := fmt.Sprintf("%s/%s:%d/%s", f.Name, d.Path, d.Line, class)
			if _, dup := got[key]; dup {
				t.Errorf("two plants share the key %q, so this table cannot tell them apart", key)
			}
			got[key] = d.WantSeverity
		}
	}

	for key, wantSev := range want {
		gotSev, ok := got[key]
		if !ok {
			t.Errorf("%s is pinned at %q and no longer exists: a plant was moved, renamed or deleted, "+
				"and every severity number shifts with it", key, wantSev)
			continue
		}
		if gotSev != wantSev {
			t.Errorf("%s is planted %q and pinned at %q. A severity edit changes what every reviewer "+
				"scores, the incumbent included, so it does not land as a one-token change: justify it "+
				"from review.md's anchors and update the pin in the same diff",
				key, gotSev, wantSev)
		}
	}
	for key, gotSev := range got {
		if _, ok := want[key]; !ok {
			t.Errorf("%s is planted %q and is not pinned: add it here so the next edit to it is visible",
				key, gotSev)
		}
	}
}

// severityLevels is the order histograms are rendered in, loudest first, so two
// failure messages from different tests can be read against each other.
var severityLevels = []config.Severity{
	config.SeverityCritical, config.SeverityError, config.SeverityWarning,
	config.SeverityInfo, config.SeverityNit,
}

// severityHistogram counts the plants at each level in a corpus.
func severityHistogram(corpus []Fixture) map[config.Severity]int {
	h := map[config.Severity]int{}
	for _, f := range corpus {
		for _, d := range f.Defects {
			h[d.WantSeverity]++
		}
	}
	return h
}

// renderHistogram spells a histogram the way the failure messages quote it, so a
// reader sees what the corpus IS rather than only that it moved.
func renderHistogram(h map[config.Severity]int) string {
	parts := make([]string, 0, len(severityLevels))
	total := 0
	for _, level := range severityLevels {
		parts = append(parts, fmt.Sprintf("%s %d", level, h[level]))
		total += h[level]
	}
	return fmt.Sprintf("%s = %d plants", strings.Join(parts, "  "), total)
}

// TestTheSeverityDistributionStaysBalanced is the guard on the property the
// corpus was rebuilt to have.
//
// It had exactly one warning and one nit across fifteen fixtures. Two things
// follow from a shape like that, and both were live defects rather than
// theoretical ones. A reviewer that answers "critical" to everything scored
// perfectly on a severity metric, which is why one such metric has already been
// withdrawn, and ONE defect was the entire unit of resolution for every claim
// either report made about the bottom of the scale: "the reviewer calibrates
// warnings" and "the reviewer got retry-no-backoff right" were the same
// sentence, indistinguishable by construction.
//
// The methodology gate named the consequence: "A+ would require the corpus to be
// able to answer the questions the reports ask of it, and it cannot."
//
// So the distribution is asserted, and it is asserted PER CORPUS, because each
// one is reported on separately and a balanced total made of two skewed halves
// answers no question either half is asked. Three rules, each of which the old
// corpus broke:
//
//  1. A level is ABSENT or it is RESOLVED. One plant at a level cannot
//     distinguish a calibrated reviewer from a lucky one, so a level present at
//     all needs at least two. Zero is legal and is not an oversight being
//     tolerated silently, see the note on info below.
//  2. No level may hold more than half a corpus's plants, which bounds what a
//     reviewer with no severity opinion at all can score by answering that level
//     to everything.
//  3. At least three distinct levels per corpus, so the scale being measured is
//     a scale rather than a threshold.
//
// WHAT THIS TEST DOES NOT CLAIM. Balance is not calibration. Nothing here can
// check that a plant's WantSeverity is the level a senior reviewer would really
// pick. That is a judgement, argued per plant against the anchors in
// internal/prompt/templates/review.md and pinned by
// TestPlantedSeveritiesArePinned. This test is the guard against the OTHER
// failure, the one that looks like tidying: a corpus rebalanced by RELABELLING
// is worse than a skewed one, because it looks like evidence. Rule 1 is what
// stops the cheapest version of that, dialling a single plant down to fill an
// empty bucket, from ever being enough.
//
// INFO IS NO LONGER EMPTY, and the history is worth keeping because rule 1 is
// what made it end properly. This comment used to say the level was assigned to
// an author that produced nothing. Five plants were then written and wired into
// neither corpus, so for a while the level was empty in exactly the way that
// looks solved in a diff, the fixtures existed, and this test could not see
// them, because it counts what the corpora contain rather than what the package
// authors. It still counts only that; TestEveryAuthoredFixtureIsWiredIntoExactly
// OneCorpus is what makes the two agree, and it had the same blind spot until it
// was rewritten to discover rather than to list.
//
// Rule 1 is why the level could not be closed halfway: one info plant per corpus
// fails this test and two pass it, so the split had to give BOTH corpora a pair
// or give one of them none. The corpora now stand at 3 and 2.
func TestTheSeverityDistributionStaysBalanced(t *testing.T) {
	const (
		minPerPresentLevel = 2
		minDistinctLevels  = 3
	)

	for _, corpus := range []struct {
		name  string
		fx    []Fixture
		spent string
	}{
		{"tuning", Fixtures(), "every tuning iteration reads it"},
		{"held-out", HeldOutFixtures(), "it is spent once, so a skew here is discovered while the number it corrupted is being reported"},
	} {
		h := severityHistogram(corpus.fx)
		shape := renderHistogram(h)

		total, distinct := 0, 0
		for _, level := range severityLevels {
			total += h[level]
			if h[level] > 0 {
				distinct++
			}
		}
		if total == 0 {
			t.Errorf("the %s corpus plants nothing", corpus.name)
			continue
		}

		for _, level := range severityLevels {
			n := h[level]
			if n > 0 && n < minPerPresentLevel {
				t.Errorf("the %s corpus plants %s exactly %d time(s): one plant is not a "+
					"measurement of a level, it is a measurement of one fixture, and every claim "+
					"made about %s would rest on it. Author a second or author none — %s.\n  %s",
					corpus.name, level, n, level, corpus.spent, shape)
			}
			if n*2 > total {
				t.Errorf("the %s corpus plants %s %d of %d times, so a reviewer that answers %q to "+
					"everything and understands nothing scores over half of it. That is the "+
					"degenerate reviewer a withdrawn severity metric was maximised by.\n  %s",
					corpus.name, level, n, total, level, shape)
			}
		}

		if distinct < minDistinctLevels {
			t.Errorf("the %s corpus plants only %d distinct severities, so it measures a threshold "+
				"rather than a scale and cannot observe inflation and understatement in the same "+
				"run.\n  %s", corpus.name, distinct, shape)
		}
	}

	// Logged unconditionally: the histogram is the headline fact about this
	// corpus, and a reader running the suite should not have to break it to see
	// what it currently is.
	t.Logf("tuning:   %s", renderHistogram(severityHistogram(Fixtures())))
	t.Logf("held-out: %s", renderHistogram(severityHistogram(HeldOutFixtures())))
	t.Logf("both:     %s", renderHistogram(severityHistogram(AllFixtures())))
}

// TestEveryAuthoredFixtureIsWiredIntoExactlyOneCorpus closes the quietest way
// this corpus has to lose a plant.
//
// A fixture missing from both corpora is invisible in the worst way. It
// compiles, it is exercised by every ground-truth test that iterates
// AllFixtures... except that it is not IN AllFixtures, so it is exercised by
// nothing at all: no line check, no keyword probe, no severity pin. It is a
// plant that measures nothing while looking, in the diff that added it, exactly
// like a plant that does. The reverse, the same fixture in both corpora, is
// the leak TestHeldOutCorpusStaysHeldOut catches by name; this catches it for
// everything this package authors.
//
// IT MISSED FIVE, AND THE REASON IS THE POINT. It used to read
// warningFixtures() and nitFixtures(), a hand-written list of the authored sets
// that existed on the day it was written. fixtures_info.go then added five info
// plants and a sixth authored set, and this test was blind to them by
// construction: golangci-lint reported six unused functions and this test
// reported nothing, while five fixtures sat in the tree with unchecked lines and
// unprobed keywords. A guard that has to be edited to keep working is a guard
// that stops working, and the failure is silent in exactly the case it exists
// for, the case where somebody authored a fixture and forgot a step.
//
// So it DISCOVERS instead. Every zero-argument function in this package's
// non-test source that returns a Fixture is an authored fixture, and the Name it
// plants is read out of the source rather than declared here. Nothing has to be
// added to this test when the next set lands, and a fixture whose Name this scan
// cannot read is a failure rather than a silent omission, because the one thing
// a discovery-based guard must never do is discover nothing and pass.
func TestEveryAuthoredFixtureIsWiredIntoExactlyOneCorpus(t *testing.T) {
	// Fixtures deliberately in NEITHER corpus, with the reason each is out.
	//
	// An exemption is a claim, so both directions are checked below: an exempt
	// fixture that turns up in a corpus makes the reason beside it stale, and an
	// exemption naming a fixture this package no longer authors is a sentence
	// about a file nobody has. Adding a name here is how a fixture opts out of
	// being measured, which is a thing that should cost a paragraph.
	exempt := map[string]string{
		"cross-batch-replay": "its point is that ONE defect is reported TWICE, which a scored corpus " +
			"would count as one detection and one false positive against a reviewer that did exactly " +
			"the right thing; fixtures_dedup_test.go checks its lines, its batch split and its plant " +
			"instead, so it is unmeasured rather than unchecked",
	}

	authored := authoredFixtureNames(t)

	// The scan validated against runtime values.
	//
	// The authored sets are the record of INTENT, what somebody meant to write
	// at each level, and the scan is the record of FACT. This map is not what
	// drives the check above and adding a set to it is not what makes a new
	// fixture visible: a set nobody lists here is still discovered constructor
	// by constructor, which is the whole reason the test was rewritten. What it
	// buys is a check on the SCAN. If constructors ever stop matching the shape
	// buildsAFixture looks for, every loop driven by the scan quietly measures a
	// smaller corpus, and cross-batch-replay is the one fixture where nothing
	// else would notice. It is in no corpus, so the AllFixtures sweep below
	// cannot see it either.
	for set, fixtures := range map[string][]Fixture{
		"warningFixtures": warningFixtures(),
		"nitFixtures":     nitFixtures(),
		"infoFixtures":    infoFixtures(),
		"dedupFixtures":   dedupFixtures(),
	} {
		for _, f := range fixtures {
			if _, ok := authored[f.Name]; !ok {
				t.Errorf("%s() returns a fixture named %q that this scan did not discover in the "+
					"package source, so the scan is reading fewer constructors than exist and every "+
					"check driven by it is running over a subset", set, f.Name)
			}
		}
	}

	tuning := map[string]bool{}
	for _, f := range Fixtures() {
		tuning[f.Name] = true
	}
	held := map[string]bool{}
	for _, f := range HeldOutFixtures() {
		held[f.Name] = true
	}
	multi := map[string]bool{}
	for _, f := range MultiFileFixtures() {
		multi[f.Name] = true
	}
	for _, f := range InfoFixtures() {
		multi[f.Name] = true // a third re-runnable corpus, outside AllFixtures like the multi-file one
	}
	for _, f := range CallerFixtures() {
		multi[f.Name] = true // a fourth, the multi-file corpus's other direction
	}
	for _, f := range SlopFixtures() {
		multi[f.Name] = true // a fifth, planted/control pairs for the slop class
	}

	for name, where := range authored {
		reason, excused := exempt[name]

		switch {
		case tuning[name] && held[name], multi[name] && (tuning[name] || held[name]):
			t.Errorf("%s authors %q and it is in MORE THAN ONE corpus, so a fixture reported as held out "+
				"or multi-file is one the prompt is tuned on", where, name)

		case excused && (tuning[name] || held[name] || multi[name]):
			t.Errorf("%s authors %q, which is exempt from this check on the grounds that %s — and a "+
				"corpus now contains it. Either the wiring is wrong or the reason is: a fixture that "+
				"is measured does not get to keep an excuse for not being", where, name, reason)

		case excused:
			// Out of both corpora, on the record, for a reason that still holds.

		case !tuning[name] && !held[name] && !multi[name]:
			t.Errorf("%s authors %q and NO corpus contains it, so it is not in AllFixtures and no "+
				"ground-truth test touches it: its lines are unchecked, its keywords are unprobed, "+
				"its severity is unpinned, and it measures nothing. Add it to Fixtures(), "+
				"HeldOutFixtures() or MultiFileFixtures(), or exempt it here with the reason", where, name)
		}
	}

	for name, reason := range exempt {
		if _, ok := authored[name]; !ok {
			t.Errorf("this test exempts %q on the grounds that %s, and no function in this package "+
				"authors a fixture by that name; delete the exemption or fix the name", name, reason)
		}
	}

	// The other direction. A corpus fixture the scan did not find is one the
	// scan is blind to, and a blind scan passes for the same reason the old
	// list-driven version did.
	for _, f := range AllFixtures() {
		if _, ok := authored[f.Name]; !ok {
			t.Errorf("%q is in a corpus and this scan did not discover it, so the scan cannot see "+
				"whichever function builds it and would not notice that function being unwired. "+
				"Fixtures are built by a zero-argument func returning a Fixture with a literal Name", f.Name)
		}
	}
}

// authoredFixtureNames reads every fixture this package authors out of its own
// source: the constructor's file and function name, keyed by the Name the
// fixture plants.
//
// It parses rather than calls, because a Go test cannot invoke a function it
// only knows the name of, and the alternative, a written-down list of the
// authored sets, is the thing that failed. Constructors in _test.go files are
// skipped: severity_test.go and crossjudge_test.go build Fixture values for
// unit tests, and those are not corpus plants and must not be reported as
// unwired.
func authoredFixtureNames(t *testing.T) map[string]string {
	t.Helper()

	out := map[string]string{}

	for file, parsed := range packageAST(t) {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}

		for _, decl := range parsed.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || !buildsAFixture(fn) {
				continue
			}

			where := fmt.Sprintf("%s:%s", file, fn.Name.Name)

			name, ok := plantedFixtureName(fn)
			if !ok {
				t.Errorf("%s returns a Fixture and this scan cannot read a literal Name out of it, "+
					"so the fixture it builds is invisible to the wiring check — which is the exact "+
					"silence that check exists to end. Give the Fixture literal a constant Name", where)
				continue
			}

			if first, dup := out[name]; dup {
				t.Errorf("%s and %s both author a fixture named %q. Corpora are keyed by name, so "+
					"one of them is unreachable and this scan cannot say which", first, where, name)
				continue
			}
			out[name] = where
		}
	}

	if len(out) == 0 {
		t.Fatal("no fixture constructors were discovered, so every check driven by this scan passes " +
			"vacuously. Either the package moved or the shape being matched is wrong")
	}
	return out
}

// buildsAFixture reports whether a declaration is a fixture constructor: a
// plain zero-argument function returning one Fixture.
func buildsAFixture(fn *ast.FuncDecl) bool {
	if fn.Recv != nil || fn.Body == nil || fn.Type.Params.NumFields() != 0 {
		return false
	}
	if fn.Type.Results == nil || len(fn.Type.Results.List) != 1 {
		return false
	}
	ident, ok := fn.Type.Results.List[0].Type.(*ast.Ident)
	return ok && ident.Name == "Fixture"
}

// plantedFixtureName is the Name field of the Fixture literal a constructor
// builds, when it is a plain string constant.
func plantedFixtureName(fn *ast.FuncDecl) (string, bool) {
	var name string
	var found bool

	ast.Inspect(fn.Body, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		if ident, ok := lit.Type.(*ast.Ident); !ok || ident.Name != "Fixture" {
			return true
		}

		for _, el := range lit.Elts {
			kv, ok := el.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			if key, ok := kv.Key.(*ast.Ident); !ok || key.Name != "Name" {
				continue
			}
			value, ok := kv.Value.(*ast.BasicLit)
			if !ok || value.Kind != token.STRING {
				continue
			}
			unquoted, err := strconv.Unquote(value.Value)
			if err != nil {
				continue
			}
			name, found = unquoted, true
			return false
		}
		return true
	})

	return name, found
}

// TestIncumbentSeverityIsRecordedNotRewritten pins the fix for the defect this
// corpus was measured through for the whole of the first benchmark.
//
// crSeverity used to map Incumbent's "critical" onto our "error" so that its
// coarser vocabulary would not read as inflation. The effect was a CEILING: no
// Incumbent review could score ACCURATE on any of the four plants we plant at
// critical, however it worded the finding, and none could be caught INFLATING
// one either. A headline claim was published on the resulting number and had to
// be retracted. Fidelity now lives in the parser, and the vocabulary difference
// is not corrected for anywhere: the second attempt, a banded comparison, was
// retracted too. It is DESCRIBED instead, see NoCrossToolSeverityScore.
//
// A word that IS one of our levels must therefore round-trip. That is the whole
// property: a parsed severity is evidence about the reviewer, and a parser that
// edits the evidence to make a comparison come out fairly has destroyed the
// thing being compared.
func TestIncumbentSeverityIsRecordedNotRewritten(t *testing.T) {
	for _, sev := range []config.Severity{
		config.SeverityCritical, config.SeverityError, config.SeverityWarning,
		config.SeverityInfo, config.SeverityNit,
	} {
		for _, spelling := range []string{
			string(sev), strings.ToUpper(string(sev)), " " + string(sev) + " ",
		} {
			if got := crSeverity(spelling); got != sev {
				t.Errorf("crSeverity(%q) = %q, want %q. Incumbent used one of OUR OWN severity "+
					"words and the parser answered with a different one. There is nowhere else for "+
					"that correction to go: this message used to send the next reader to Band, an "+
					"instrument deleted with the retraction it belongs to. A vocabulary difference "+
					"is DESCRIBED (SeverityVocabularyBlock) and not corrected for anywhere",
					spelling, got, sev)
			}
		}
	}

	// Unrecognized input must still produce a usable severity, an unknown word
	// has to yield a finding, which is why crSeverity has a default at all.
	for _, word := range []string{"blocker", "trivial", "", "  ", "banana"} {
		if got := crSeverity(word); !got.IsFinding() {
			t.Errorf("crSeverity(%q) = %q, which no finding may carry", word, got)
		}
	}

	// The words crSeverity translates rather than degrades, enumerated.
	//
	// crSeverity's default arm is symmetric with our own models by construction
	//, it calls the same Normalize, and the guard for that only ever tested
	// words that REACH the default. These three do not: they are foreign tokens
	// given bespoke arms, so the identical word is worth a different level
	// depending on which contender emitted it, and "major" carries more than
	// half the incumbent's severity words in the shipped cache. That asymmetry
	// is deliberate and is the reason no cross-tool severity score is published.
	// Pinning the set is what stops a fourth translation being added quietly and
	// moving a published number with no new evidence.
	translated := map[string]config.Severity{
		"major":   config.SeverityWarning,
		"warn":    config.SeverityWarning,
		"nitpick": config.SeverityNit,
		"minor":   config.SeverityInfo,
	}
	for word, want := range translated {
		if got := crSeverity(word); got != want {
			t.Errorf("crSeverity(%q) = %q, want %q. Changing a translation moves published "+
				"severity numbers with the reviewer's bytes unchanged", word, got, want)
		}
		if normalized, known := config.Severity(word).Normalize(); known {
			t.Errorf("%q is a level of ours (%q), so it is not a foreign token and does not belong "+
				"in this table", word, normalized)
		}
	}

	for word := range translated {
		normalized, _ := config.Severity(word).Normalize()
		if crSeverity(word) == normalized {
			continue
		}
		t.Logf("asymmetric on purpose: %q is worth %q from %s and %q from one of ours",
			word, crSeverity(word), IncumbentModel, normalized)
	}
}

// TestIncumbentObjectiveSeverityOnTheShippedCache is the receipt for BOTH
// retractions.
//
// It recomputes the incumbent's objective severity over the reviews on disk:
//
//	                                   exact (O-*)
//	first published                acc 3  infl 0  under 4
//	after the parser fix           acc 2  infl 3  under 2
//
// Read that carefully, because the obvious summary of the parser fix is wrong.
// Recording Incumbent's criticals faithfully does not hand it points back at
// full resolution, it LOSES one there. Two calls it could not previously win
// became accurate (the hardcoded secret and multi-defect's traversal, both
// planted critical), and three that were accurate became INFLATED, because the
// same word it uses for those two is the word it used on plants of error
// (nil-deref, SQL injection, command injection). That is not a new bias; it is
// the same vocabulary mismatch pointing the other way, and it is the proof that
// no constant in crSeverity could have fixed it.
//
// A THIRD COLUMN USED TO BE PINNED HERE and is deleted: the banded triple, whose
// 5/0/2 on this corpus was quoted as "0.62". It is 0.714, and offered as the
// cross-tool result. It is withdrawn, and reconstructing it says why: a reviewer
// stamping one blocking word on every finding bands 7/1/0 here against the
// incumbent's 5/0/2, one that reports only the already-blocking plants bands
// 7/0/0, and the column moved not at all when the parser bug above was fixed.
// See NoCrossToolSeverityScore.
//
// WHAT SURVIVES IS NOT A CROSS-TOOL SCORE. These counts are pinned as EVIDENCE
// about this cache, they catch a fixture edited without re-collecting, and a
// scorer change that silently moves the incumbent, and Incumbent's own O-*
// remains a statement about vocabulary rather than about review quality, because
// its one "critical" spans two of our levels. The per-plant lines are logged on
// failure so a disagreement can be read rather than guessed at.
func TestIncumbentObjectiveSeverityOnTheShippedCache(t *testing.T) {
	// The tuning fixtures the shipped cache covers, pinned by name.
	//
	// This used to be "all of Fixtures()", and that identity broke the moment the
	// corpus grew: five warning and nit fixtures were added to end a severity
	// skew that made these very columns unreadable, and re-collecting the
	// incumbent's opinion of them is a paid, networked, rate-limited operation
	// that is not part of authoring a fixture. The guard could not tell that
	// apart from the failure it exists for, a cached fixture EDITED without
	// re-collecting, which zeroes its counts and reads as the numbers improving.
	//
	// So the set is written down instead of derived. Both directions are checked
	// below, and the second is the one that matters: a fixture in this list with
	// no cache was edited, and a tuning fixture with a cache that is NOT in this
	// list would silently move the pinned totals underneath them.
	//
	// WHAT THIS COSTS, stated because the number below is quoted elsewhere: the
	// incumbent's severity reading now covers EIGHT of the sixteen tuning
	// fixtures, not all of them, and the eight it omits are exactly the warning,
	// nit and info plants. Its 2/3/2 is therefore a statement about the corpus
	// as it stood when it was collected, which was already the honest reading
	// of it, since it was never a cross-tool score, and it is not evidence
	// about how the incumbent rates the three levels this corpus previously
	// could not resolve. Answering that takes a collection run.
	// Every tuning fixture the incumbent has a cached review for. It was the
	// original eight until the corpus was re-collected over all thirty; leaving
	// it stale would compute the totals below over a corpus that no longer
	// exists and read as the incumbent having changed, which is exactly what
	// this guard refuses.
	covered := map[string]bool{
		"go-nil-deref": true, "go-sql-injection": true, "go-hardcoded-secret": true,
		"python-command-injection": true, "clean-refactor": true, "style-only": true,
		"multi-defect": true, "capacity-hint-nit": true,
		"ts-unbounded-memo-key": true, "go-cancel-goroutine-leak": true,
		"python-timing-unsafe-hmac": true, "cross-file-copy-nit": true,
		"sorted-for-min-nit": true, "kotlin-widened-input": true,
		"php-forbidden-vs-404": true, "go-package-singleton": true,
	}

	var (
		got    SeverityScore
		cached int
		lines  []string
		usage  = SeverityUsage{}
	)

	for _, f := range Fixtures() {
		findings, ok := CachedIncumbent(crCacheDir, f)
		if ok != covered[f.Name] {
			switch {
			case covered[f.Name]:
				t.Errorf("%s is pinned as covered by %s and has no cached review matching its "+
					"current source: the fixture was edited without re-collecting, which zeroes "+
					"its counts and reads as the totals below having improved", f.Name, crCacheDir)
			default:
				t.Errorf("%s is NOT pinned as covered and has a cached review, so the totals below "+
					"are computed over a different corpus than the one they were pinned against; "+
					"add it here in the same change that re-collects", f.Name)
			}
			continue
		}
		if !ok {
			continue
		}
		cached++

		s := ScoreSeverity(f, findings)
		got.Accurate += s.Accurate
		got.Inflated += s.Inflated
		got.Understated += s.Understated
		usage.Merge(s.Usage())

		for _, c := range s.Calls {
			lines = append(lines, fmt.Sprintf("%s: planted %s, said %s -> %s (%s)",
				f.Name, c.Defect.WantSeverity, c.Finding.Sev(), c.Verdict,
				truncate(c.Finding.Title, 40)))
		}
	}

	// A cache that stopped matching would zero every count and read as the
	// numbers having improved, which is the failure this whole file guards.
	if cached != len(covered) {
		t.Fatalf("%d of the %d pinned fixtures have a cached Incumbent review matching their "+
			"current source, so this test measures a different corpus than it pins; a fixture was "+
			"edited without re-collecting %s", cached, len(covered), crCacheDir)
	}

	// Pinned over the sixteen tuning fixtures the incumbent now has reviews for.
	// It was 2/3/2 over the original eight; the corpus was re-collected, not the
	// reviewer re-run, so a change here means the corpus moved underneath it.
	want := SeverityScore{Accurate: 5, Inflated: 3, Understated: 2}

	if got.Accurate != want.Accurate || got.Inflated != want.Inflated || got.Understated != want.Understated {
		t.Errorf("exact acc/infl/under = %d/%d/%d, want %d/%d/%d. This is EVIDENCE about this cache, "+
			"not a cross-tool result: %s publishes one 'critical' spanning two of our levels, so it "+
			"cannot be accurate on both kinds of plant and its O-* is a statement about vocabulary",
			got.Accurate, got.Inflated, got.Understated,
			want.Accurate, want.Inflated, want.Understated, IncumbentModel)
	}

	// The vocabulary description, pinned on the same terms and for the same
	// reason: it is what the retracted number was replaced BY, so a change that
	// silently empties or re-shapes it removes the only thing this table now says
	// about severity across vocabularies.
	//
	// Read it as the whole argument in four numbers. On plants of ERROR the
	// incumbent prints "critical" three times and "major" twice; on plants of
	// CRITICAL it prints "critical" twice. One word covering both kinds of plant
	// is exactly the resolution difference no mapping can repair, and it is why
	// the banded reduction that "fixed" it scored a reviewer with no severity
	// opinion at all as perfect.
	//
	// THE WORDS PINNED HERE ARE INCUMBENT'S, and this table used to pin OURS.
	// It expected {error plant, warning} twice, because the block recorded what
	// crSeverity had translated "major" into. Incumbent prints no "warning"
	// anywhere in this cache; the pin asserted that a description captioned as
	// the reviewer's vocabulary reported our translation, and it would have gone
	// green on a corpus the reviewer had never seen. "major" is what it printed,
	// and what it is read as is ours and marked as ours.
	for _, tc := range []struct {
		planted config.Severity
		said    SeverityWord
		want    int
	}{
		{config.SeverityCritical, SeverityWord{"critical", config.SeverityCritical}, 2},
		{config.SeverityError, SeverityWord{"critical", config.SeverityCritical}, 3},
		{config.SeverityError, SeverityWord{"major", config.SeverityWarning}, 2},
	} {
		if n := usage[tc.planted][tc.said]; n != tc.want {
			t.Errorf("on plants of %s the incumbent printed %q (which we read as %s) %d time(s), want %d",
				tc.planted, tc.said.Said, tc.said.Recorded, n, tc.want)
		}
	}

	if t.Failed() {
		for _, l := range lines {
			t.Log(l)
		}
	}
}

// TestHeldOutCorpusCanFalsifyInflation keeps the held-out set able to answer
// the question it is spent on.
//
// The tuning is aimed at severity inflation. Every held-out plant used to be
// error or critical, which makes over-claiming almost unobservable there: a
// prompt that learned to answer "at least error" to everything scored a nearly
// perfect O-ACC on the corpus whose whole job is to catch that. A plant below
// error is what makes the column falsifiable in both directions.
func TestHeldOutCorpusCanFalsifyInflation(t *testing.T) {
	var lowest config.Severity
	for _, f := range HeldOutFixtures() {
		for _, d := range f.Defects {
			if lowest == "" || d.WantSeverity.Rank() < lowest.Rank() {
				lowest = d.WantSeverity
			}
		}
	}

	if lowest == "" {
		t.Fatal("the held-out corpus plants no defects at all")
	}
	if lowest.Rank() >= config.SeverityError.Rank() {
		t.Errorf("the lowest held-out plant is %q: a reviewer that rates everything error or critical "+
			"cannot be caught here, and that is the failure the severity columns exist to measure", lowest)
	}
}

// TestHeldOutCorpusStaysHeldOut pins the one property that makes the held-out
// set worth having.
//
// Its value is entirely that the prompt was never tuned against it. A fixture
// that leaks into Fixtures(), by being listed in both accessors, or by someone
// "consolidating" the two, is silently converted into a training example, and
// the final generalization number quietly becomes another training score. That
// is not a failure anyone would notice from a passing run, so it is asserted.
func TestHeldOutCorpusStaysHeldOut(t *testing.T) {
	tuning := map[string]bool{}
	for _, f := range Fixtures() {
		tuning[f.Name] = true
	}

	held := HeldOutFixtures()
	if len(held) == 0 {
		t.Fatal("the held-out corpus is empty; there is nothing to generalize to")
	}

	for _, f := range held {
		if tuning[f.Name] {
			t.Errorf("%s is in BOTH corpora, so the prompt is tuned on a fixture reported as held out", f.Name)
		}
	}

	if got, want := len(AllFixtures()), len(Fixtures())+len(held); got != want {
		t.Errorf("AllFixtures returned %d fixtures, expected %d; a fixture is duplicated or dropped", got, want)
	}
}

// TestHeldOutFixturesAreSelectableByName proves the corpus can be run.
//
// Both halves matter and they pull in opposite directions: naming a held-out
// fixture must select it, otherwise the set can only be spent by editing code
// , and naming nothing must not, or a tuning loop consumes the held-out corpus
// on its first iteration and no one finds out.
func TestHeldOutFixturesAreSelectableByName(t *testing.T) {
	// OptionsFromEnv reads the whole environment, so the sibling variables are
	// pinned too: a developer's exported NITPICK_EVAL_MODELS would otherwise
	// change what this test exercises.
	t.Setenv(EnvModels, "")
	t.Setenv(EnvRuns, "")
	t.Setenv(EnvCapture, "")

	t.Setenv(EnvFixtures, "")
	opts, err := OptionsFromEnv()
	if err != nil {
		t.Fatalf("unfiltered run: %v", err)
	}
	for _, f := range opts.Fixtures {
		for _, h := range HeldOutFixtures() {
			if f.Name == h.Name {
				t.Fatalf("an unfiltered run includes held-out fixture %q", h.Name)
			}
		}
	}

	// One from each corpus, to prove selecting a held-out fixture does not cost
	// access to the tuning ones.
	t.Setenv(EnvFixtures, "removed-guard,go-nil-deref")

	opts, err = OptionsFromEnv()
	if err != nil {
		t.Fatalf("selecting two real fixtures: %v", err)
	}

	var names []string
	for _, f := range opts.Fixtures {
		names = append(names, f.Name)
	}

	if len(names) != 2 {
		t.Fatalf("selected %v, expected exactly removed-guard and go-nil-deref", names)
	}
	for _, want := range []string{"removed-guard", "go-nil-deref"} {
		if !slices.Contains(names, want) {
			t.Errorf("NITPICK_EVAL_FIXTURES named %q but the run does not include it: %v", want, names)
		}
	}
}

// TestTheMakefileSpendsTheWholeHeldOutCorpus keeps the one-shot run from
// quietly becoming a partial one.
//
// The held-out corpus is spent by `make eval FIXTURES=$(HELD_OUT)`, and HELD_OUT
// is a hand-typed comma-separated line in the Makefile. Adding a fixture to
// HeldOutFixtures() does not add it there, and nothing in the tree noticed:
// five were added to the corpus in one change and the Makefile still named
// seven, so the generalization run would have measured a subset and printed a
// table saying "HELD-OUT corpus (7 fixtures)", which is true, and is not the
// claim anyone reading it would take away.
//
// This is the omission twin of TestMistypedFixtureNameIsAnError. That test
// catches a name that resolves to NOTHING, which is loud. A name that is simply
// absent resolves to nothing at all and is silent, and the corpus cannot be
// re-spent once the number is published.
func TestTheMakefileSpendsTheWholeHeldOutCorpus(t *testing.T) {
	src, err := os.ReadFile("../../Makefile")
	if err != nil {
		t.Fatalf("reading the Makefile: %v", err)
	}

	const prefix = "HELD_OUT :="
	var line string
	for _, l := range strings.Split(string(src), "\n") {
		if strings.HasPrefix(l, prefix) {
			line = strings.TrimSpace(strings.TrimPrefix(l, prefix))
			break
		}
	}
	if line == "" {
		t.Fatalf("the Makefile has no %q line, so the held-out corpus has no documented way to be "+
			"spent and this test asserts nothing", prefix)
	}

	named := map[string]bool{}
	for _, name := range strings.Split(line, ",") {
		if name = strings.TrimSpace(name); name != "" {
			named[name] = true
		}
	}

	held := map[string]bool{}
	for _, f := range HeldOutFixtures() {
		held[f.Name] = true
		if !named[f.Name] {
			t.Errorf("%s is in HeldOutFixtures and is NOT in the Makefile's HELD_OUT line, so the "+
				"one-shot run silently omits it and reports generalization over a subset", f.Name)
		}
	}
	for name := range named {
		if !held[name] {
			t.Errorf("the Makefile's HELD_OUT line names %q, which is not a held-out fixture. "+
				"OptionsFromEnv rejects a name that resolves to nothing, so this does not report a "+
				"subset — it makes the held-out run fail outright", name)
		}
	}
}

// TestMistypedFixtureNameIsAnError pins the failure mode that made the held-out
// corpus unsafe to spend.
//
// Selection used to keep whatever it could resolve and silently ignore the
// rest, so the two cases below both returned the eight TUNING fixtures, the
// exact wrong answer, with no error, no warning, and a table that does not
// name its corpus. The held-out set is a one-shot instrument selected by a
// hand-typed six-name line in the Makefile; a typo in it must stop the run.
func TestMistypedFixtureNameIsAnError(t *testing.T) {
	t.Setenv(EnvModels, "")
	t.Setenv(EnvRuns, "")
	t.Setenv(EnvCapture, "")

	for _, tc := range []struct {
		name  string
		value string
	}{
		{"every name mistyped", "removed_guard,contract_break"},
		{"the only name mistyped", "remove-guard"},
		{"one name of seven mistyped", "contract-break,data-loss-migration,ts-unawaited-async," +
			"timezone-boundary,clean-sql-allowlist,remove-guard,retry-no-backoff"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(EnvFixtures, tc.value)

			opts, err := OptionsFromEnv()
			if err == nil {
				var got []string
				for _, f := range opts.Fixtures {
					got = append(got, f.Name)
				}
				t.Fatalf("%s=%q was accepted and selected %v; a name that resolves to nothing must be an error, "+
					"or a mistyped held-out run silently reports the tuning score", EnvFixtures, tc.value, got)
			}

			// The message has to name what was wrong, or the operator retries
			// the same typo.
			if !strings.Contains(err.Error(), "remove-guard") && !strings.Contains(err.Error(), "removed_guard") {
				t.Errorf("error does not name the offending fixture: %v", err)
			}
		})
	}
}

// TestFixturesWhoseCorrectReviewIsSilenceHaveNoDefects pins the clean fixtures.
//
// A clean fixture only measures precision while it stays clean. Planting a
// defect in one, or listing a defect for a change that does not have one,
// converts the corpus's only test of restraint into a test of recall, and the
// precision number stops meaning anything. clean-sql-allowlist is the one that
// matters most: it is deliberately built to look like the SQL-injection
// fixture, and it is safe.
func TestFixturesWhoseCorrectReviewIsSilenceHaveNoDefects(t *testing.T) {
	silent := []string{"clean-refactor", "style-only", "clean-sql-allowlist"}

	byName := map[string]Fixture{}
	for _, f := range AllFixtures() {
		byName[f.Name] = f
	}

	for _, name := range silent {
		f, ok := byName[name]
		if !ok {
			t.Errorf("fixture %q no longer exists; update this test", name)
			continue
		}
		if !f.Clean() {
			t.Errorf("%s is supposed to have no defects, but plants %d: %s",
				name, len(f.Defects), f.Defects[0].Why)
		}
	}
}

// TestEveryFixtureChangesSomething catches a fixture whose head is identical to
// its base.
//
// That fixture produces an empty diff, which the engine reports as nothing to
// review, so it scores as a perfect clean run no matter what the prompt says.
// A silent free pass is the worst kind of corpus bug: it raises the score.
func TestEveryFixtureChangesSomething(t *testing.T) {
	for _, f := range AllFixtures() {
		changed := false
		for path, head := range f.Head {
			if base, ok := f.Base[path]; !ok || base != head {
				changed = true
				break
			}
		}
		if !changed {
			t.Errorf("%s: head is identical to base, so the diff is empty and the fixture measures nothing", f.Name)
		}
	}
}

// TestEveryPlantedDefectIsReportable builds each fixture into a real repository
// and checks the engine could publish a comment on the planted line.
//
// Comparing head text to base text is not enough, which the corpus proved: the
// held-out SQL migration ADDS a file, `git diff HEAD` omits untracked files, and
// the fixture produced a completely empty diff while looking correct in source.
// It would have scored as a flawless clean run for every model.
//
// The second half is the subtler trap. filterAnchors drops any finding that is
// not on an added line and has no added line within snapDistance, so a defect
// planted on a context line is invisible no matter how well a model reviews:
// its recall would read as a prompt weakness forever. removed-guard sits on
// exactly such a line. Its defect is the deleted check, and the statement left
// behind is unchanged text, so this is checked, not reasoned about.
func TestEveryPlantedDefectIsReportable(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}

	// Mirrors the engine's snapDistance. Duplicated on purpose: importing the
	// engine's value would make this test agree with a regression in it.
	const snapDistance = 3

	for _, f := range AllFixtures() {
		dir := t.TempDir()
		if err := buildRepo(dir, f); err != nil {
			t.Errorf("%s: build repo: %v", f.Name, err)
			continue
		}

		raw, err := vcs.NewLocal(dir, io.Discard).Diff(context.Background(), vcs.Ref{})
		if err != nil {
			t.Errorf("%s: diff: %v", f.Name, err)
			continue
		}

		files, err := diff.Parse(raw)
		if err != nil {
			t.Errorf("%s: parse diff: %v", f.Name, err)
			continue
		}
		if len(files) == 0 {
			t.Errorf("%s: the change produces an EMPTY diff, so there is nothing to review and the fixture scores as clean", f.Name)
			continue
		}

		for _, d := range f.Defects {
			file := files.Find(d.Path)
			if file == nil {
				t.Errorf("%s: defect is on %s, which the diff does not contain: %s", f.Name, d.Path, d.Why)
				continue
			}

			if file.IsChangedLine(d.Line) {
				continue
			}
			if _, ok := file.NearestCommentableLine(d.Line, snapDistance); !ok {
				t.Errorf("%s: %s:%d has no added line within %d, so any finding there is dropped before publication and the defect is unfindable: %s",
					f.Name, d.Path, d.Line, snapDistance, d.Why)
			}
		}
	}
}

// TestTheCorpusStillAssemblesTheWayItsCommentsClaim reads the batching back out
// of bundle.Assemble instead of trusting the sentences that describe it.
//
// Three separate comments in this package rest on one measured fact:
// ts-unbounded-memo-key changes seven files, so at the shipped
// max_files_per_request of 6 it is the only fixture that assembles into more
// than one batch. HeldOutFixtures argues its whole tuning/held-out split on
// that fact, fixtures_warning.go calls the file count load-bearing, and the
// fixture's own comment declares an invariant tighter still: the three files a
// reviewer needs in order to SEE the defect, the caller that breaks the
// contract, the helper that states it, and the control that honours it, must
// land in the SAME batch, "because a defect split across batches would be one
// no reviewer could see, and a plant nothing can find scores as a prompt
// weakness forever".
//
// Nothing read any of it back. Two one-line edits were enough to falsify the
// claims while `go test ./...` printed ok:
//
//   - Deleting the two files the fixture calls "ordinary PR filler" drops it to
//     five files and ONE batch. The corpus then has zero multi-batch coverage
//     and every comment above is false.
//   - Adding one more ordinary file whose path sorts before src/search.ts,
//     src/format.ts, say, fills the first batch with the six alphabetically
//     earliest paths and pushes search.ts into the second, ALONE with types.ts.
//     The planted defect is then structurally unfindable: the reviewer reading
//     the batch that contains it has never seen cache.ts's contract or plans.ts
//     honouring it, so every model scores a miss and the eval reports a corpus
//     bug as a prompt weakness, the exact outcome the fixture says must be
//     prevented.
//
// Both invariants turn on alphabetical position at an exact boundary, which is
// far too quiet a thing to leave to a sentence. This is the same argument
// TestTheFiguresTheseCommentsQuoteStillReproduce makes about the numbers in
// prose, applied to a number no comment could state without running the code.
func TestTheCorpusStillAssemblesTheWayItsCommentsClaim(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}

	// The files each fixture's comments say a reviewer cannot see the defect
	// without. Every one of them must share a batch with the planted line: a
	// cross-file defect whose halves arrive in different requests is not a
	// harder defect, it is an impossible one.
	together := map[string][]string{
		"ts-unbounded-memo-key": {"src/cache.ts", "src/plans.ts", "src/search.ts"},
		"cross-file-copy-nit":   {"store/store.go", "report/summary.go"},
		"cross-file-sort-nit":   {"src/members.ts", "src/roster.ts"},
	}

	multi := map[string]int{}

	for _, f := range AllFixtures() {
		dir := t.TempDir()
		if err := buildRepo(dir, f); err != nil {
			t.Errorf("%s: build repo: %v", f.Name, err)
			continue
		}

		raw, err := vcs.NewLocal(dir, io.Discard).Diff(context.Background(), vcs.Ref{})
		if err != nil {
			t.Errorf("%s: diff: %v", f.Name, err)
			continue
		}
		files, err := diff.Parse(raw)
		if err != nil {
			t.Errorf("%s: parse diff: %v", f.Name, err)
			continue
		}

		// The shipped config, through the same constructor the eval runs, so
		// this measures the batching a real review would get rather than one
		// assembled to make the assertion pass.
		cfg := evalConfig(Model{ID: "assembly-probe"})
		plan, err := bundle.Assemble(context.Background(), cfg, files,
			func(_ context.Context, path string) ([]byte, error) {
				return os.ReadFile(filepath.Join(dir, filepath.FromSlash(path)))
			})
		if err != nil {
			t.Errorf("%s: assemble: %v", f.Name, err)
			continue
		}

		batchOf := map[string]int{}
		for i, b := range plan.Batches {
			for _, p := range b.Paths() {
				batchOf[p] = i
			}
		}
		if len(plan.Batches) > 1 {
			multi[f.Name] = len(plan.Batches)
		}

		for _, group := range [][]string{together[f.Name]} {
			if len(group) == 0 {
				continue
			}
			first, ok := batchOf[group[0]]
			if !ok {
				t.Errorf("%s: %s is named as context the defect cannot be read without, but it is not in the plan at all",
					f.Name, group[0])
				continue
			}
			for _, p := range group[1:] {
				got, ok := batchOf[p]
				if !ok {
					t.Errorf("%s: %s is named as context the defect cannot be read without, but it is not in the plan at all",
						f.Name, p)
					continue
				}
				if got != first {
					t.Errorf("%s: %s is in batch %d and %s is in batch %d, so no single review request sees both. "+
						"The defect is split across requests and every reviewer scores a miss on it, which the "+
						"eval reports as a prompt weakness rather than as this. Reorder or shrink the change so "+
						"they share a batch.",
						f.Name, group[0], first+1, p, got+1)
				}
			}
		}

		// Extra is written into the repository and then never reviewed:
		// Assemble only ever fetches content for files the diff names, so an
		// unchanged file cannot reach a prompt through it. Pinned rather than
		// described, because Fixture.Extra's doc claimed the opposite for
		// nine files across seven fixtures and no test disagreed.
		for p := range f.Extra {
			if _, ok := batchOf[p]; ok {
				t.Errorf("%s: Extra file %s reached the review plan. If that is now intended, "+
					"Fixture.Extra's doc and this assertion both need rewriting — several fixtures "+
					"were authored believing it already happened", f.Name, p)
			}
		}
	}

	if len(multi) == 0 {
		t.Errorf("NO fixture assembles into more than one batch, so nothing in the corpus exercises " +
			"cross-batch assembly or the merge that follows it. HeldOutFixtures argues its split on " +
			"this property existing and fixtures_warning.go calls it load-bearing; both are now false. " +
			"Restore the file count on ts-unbounded-memo-key or author a replacement.")
	}
	t.Logf("multi-batch fixtures: %v (max_files_per_request=%d)", multi,
		evalConfig(Model{ID: "assembly-probe"}).Review.MaxFilesPerRequest)
}
