package evals

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/diff"
	"github.com/jdziat/open-nitpick/internal/review"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// TestPlantedDefectsPointAtRealLines validates the corpus itself.
//
// Every Defect.Line was hand-counted, and four of five were wrong — one pointed
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
// The previous version of this test stated the line number itself — "line 17 of
// go-sql-injection contains Sprintf" — and never consulted Defect.Line. That
// tests the fixture against a second hand-written copy of the ground truth,
// which is exactly the thing being doubted: changing Defect.Line from 9 to 10
// left it green, and 10 is the wrong line. The needle is now looked up per
// DEFECT, so the only line number in the assertion is the one scoring uses.
func TestPlantedDefectsAreOnTheRightLine(t *testing.T) {
	// fixture -> the substring each defect's line must contain, in the order
	// the fixture declares its defects.
	want := map[string][]string{
		"go-nil-deref":             {"http.Get"},
		"go-sql-injection":         {"Sprintf"},
		"go-hardcoded-secret":      {"sk-live"},
		"python-command-injection": {"os.system"},
		"capacity-hint-nit":        {"make("},
		"multi-defect":             {"os.Create", "go func", "os.Create"},

		// Held-out corpus.
		"contract-break":      {`json:"createdAt"`},
		"data-loss-migration": {"UPDATE accounts"},
		"ts-unawaited-async":  {"forEach"},
		"timezone-boundary":   {"Truncate"},
		"removed-guard":       {"s.store.DeleteProject(ctx, projectID)"},
		"retry-no-backoff":    {"for _ in range(ATTEMPTS)"},
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
// the exact objection its ban list was written to exclude — "prefer for...of
// for readability" scored full recall on the unawaited-promise plant through
// the keyword "promise", and so did "fine as written".
//
// So the cases below are findings, and the assertion is matches(). Both
// directions are pinned: without the must-match half, deleting every keyword
// would turn this test green while making the corpus unscoreable.
func TestKeywordsAdmitOnlyRealDetections(t *testing.T) {
	type probe struct {
		why     string
		finding review.Finding
	}

	cases := map[string]struct {
		hit  []probe // reports the planted defect; must be credited
		miss []probe // noticed something else; must not be
	}{
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
				// injection plant through the keyword "subprocess" — the word its
				// own recommended fix uses — and was then graded for the severity
				// of a bug it never mentions.
				"is about tar's ignored exit status and recommends subprocess",
				review.Finding{Path: "tools.py", Line: 12, Severity: "warning", Category: "Stability & Availability",
					Title: "Propagate archive failures.",
					Rationale: "The function ignores the tar exit status. Missing directories, permission errors, " +
						"or a failed tar command can leave no archive while the caller receives no error. " +
						"Use subprocess.run(..., check=True) or explicitly handle the exit status."},
			}},
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

// TestEveryDefectDeclaresAUsableSeverity closes a silent hole in the ground
// truth.
//
// WantSeverity now drives a reported column, and its zero value is not inert:
// config.Severity("").Rank() falls through to SeverityInfo's rank, so a defect
// added without the field would silently grade every warning and above as
// INFLATED and every nit as UNDERSTATED — in the exact column the prompt is
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
// a reader sees a corpus answering one question two ways — and someone will
// eventually "fix" it in whichever direction the week's numbers prefer, since
// both directions look equally like tidying.
//
// A judgement about severity cannot be asserted mechanically, so this asserts
// the property that can be: that the corpus does not contradict itself in
// silence. Divergence stays legal — resource genuinely holds an error, a
// warning and a nit — but it has to be written down where the next editor
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
				// justification — for a number it no longer justifies — and the
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
// twin, for the corpus that actually shapes the prompt.
//
// The held-out set is spent once; the tuning set is what every iteration reads,
// so a one-sided severity distribution there is the more expensive of the two.
// capacity-hint-nit is the only plant in it below error — one fixture away from
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
// editing it moves the score of every reviewer at once — including the
// incumbent it is being compared to — without touching a line of reviewer code.
// Nothing else in the tree could see such an edit. The class-consistency check
// cannot: it groups on Class, which the same editor declares, so moving a plant
// and its class together silences it. And it exempts any class with one member,
// which is three of these fourteen — contract-break, data-loss-migration and
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
// ability to observe INFLATION (raising plants removes that ability), check
// what it does to a reviewer whose vocabulary cannot reach the new level — see
// TestIncumbentCannotExpressCritical — and then update the entry.
func TestPlantedSeveritiesArePinned(t *testing.T) {
	// Keyed "fixture/path:line/class". The class is in the key because two of
	// multi-defect's plants sit on the SAME line of the same file — the
	// traversal and the descriptor leak both anchor at handler.go:19 — and
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

		// Held-out corpus. Pinned on the same terms: it is spent once, so a
		// severity edit here is discovered at the moment the number it
		// corrupted is already being reported.
		"contract-break/event.go:9/contract":                                config.SeverityError,
		"data-loss-migration/migrations/0007_backfill_plan.sql:8/data-loss": config.SeverityCritical,
		"ts-unawaited-async/src/sync.ts:10/correctness":                     config.SeverityError,
		"timezone-boundary/report.go:13/correctness":                        config.SeverityError,
		"removed-guard/project.go:31/security":                              config.SeverityCritical,
		"retry-no-backoff/client.py:11/resource":                            config.SeverityWarning,
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

// TestIncumbentCannotExpressCritical pins the ceiling the objective severity
// columns are read through.
//
// crSeverity has no branch that returns critical — Incumbent publishes
// critical/warning/info with no separate error tier, so its critical is mapped
// onto ours deliberately. The consequence is easy to forget and expensive: no
// Incumbent review can ever score ACCURATE on a plant we planted critical, and
// none can ever be caught INFLATING one. Raising a plant to critical therefore
// moves the head-to-head numbers with no change whatever in Incumbent's
// output, and it happened: two plants were raised, O-ACC and O-UNDER each moved
// by two against the incumbent, and nothing objected.
//
// So the count is pinned rather than merely documented. It is the number
// ScoreSeverity's own comment states, and the two must not drift apart.
func TestIncumbentCannotExpressCritical(t *testing.T) {
	for _, word := range []string{"critical", "CRITICAL", " Critical ", "blocker", "major", "warning", "info", "nit", ""} {
		if got := crSeverity(word); got == config.SeverityCritical {
			t.Fatalf("crSeverity(%q) = %q: the ceiling this test exists to describe has moved, and "+
				"score.go's account of the objective severity columns is now wrong", word, got)
		}
	}

	// Graded calls on plants Incumbent's vocabulary cannot reach. These are
	// UNDERSTATEMENTS BY CONSTRUCTION: no output could have scored otherwise.
	const wantForced = 2

	var (
		forced []string
		cached int
	)
	for _, f := range Fixtures() {
		findings, ok := CachedIncumbent(crCacheDir, f)
		if !ok {
			continue
		}
		cached++
		for _, c := range ScoreSeverity(f, findings).Calls {
			if c.Defect.WantSeverity == config.SeverityCritical {
				forced = append(forced, fmt.Sprintf("%s (%s)", f.Name, c.Finding.Title))
			}
		}
	}

	// A cache that stopped matching would drop `forced` to zero and read as the
	// ceiling having gone away, which is the opposite of what happened.
	if cached == 0 {
		t.Fatalf("no cached Incumbent review in %s matches the current corpus, so this test measures "+
			"nothing; a fixture's source was edited without re-collecting", crCacheDir)
	}

	if len(forced) != wantForced {
		t.Errorf("%d of the tuning corpus's graded Incumbent calls sit on a plant its vocabulary "+
			"cannot reach, and %d are accounted for: %s.\n"+
			"Each one is an O-UNDER the incumbent could not have avoided and an O-INFL it could not "+
			"have committed, so the column is that much less a statement about review quality. "+
			"If a plant was raised to critical, say why the anchor requires it AND update the count "+
			"here and the account in ScoreSeverity's doc comment",
			len(forced), wantForced, strings.Join(forced, ", "))
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
// that leaks into Fixtures() — by being listed in both accessors, or by someone
// "consolidating" the two — is silently converted into a training example, and
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

// TestHeldOutFixturesAreSelectableByName proves the corpus can actually be run.
//
// Both halves matter and they pull in opposite directions: naming a held-out
// fixture must select it — otherwise the set can only be spent by editing code
// — and naming nothing must not, or a tuning loop consumes the held-out corpus
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

// TestMistypedFixtureNameIsAnError pins the failure mode that made the held-out
// corpus unsafe to spend.
//
// Selection used to keep whatever it could resolve and silently ignore the
// rest, so the two cases below both returned the eight TUNING fixtures — the
// exact wrong answer — with no error, no warning, and a table that does not
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
// defect in one — or listing a defect for a change that does not have one —
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
// review — so it scores as a perfect clean run no matter what the prompt says.
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
// and checks the engine could actually publish a comment on the planted line.
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
// exactly such a line — its defect is the deleted check, and the statement left
// behind is unchanged text — so this is checked, not reasoned about.
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
