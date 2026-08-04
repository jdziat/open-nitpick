package evals

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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

		// Warning and nit plants in the tuning corpus. Each needle was read back
		// out of Head by a probe that printed the line, not counted by eye: the
		// four wrong anchors this test exists for were all produced by counting.
		"ts-unbounded-memo-key":     {`remember("q:" + q`},
		"go-cancel-goroutine-leak":  {"make(chan result)"},
		"python-timing-unsafe-hmac": {"== provided"},
		"cross-file-copy-nit":       {"make([]store.Event"},
		"sorted-for-min-nit":        {"sorted(readings"},

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

		// The warning plants. Each miss below is the false positive its fixture's
		// own doc comment names, and each is anchored WITHIN anchorTolerance of
		// the plant wherever the objection would really be made there — otherwise
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
				// deliberate — it fails by PATH, not by keyword, and if a later
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
				// least as often as to the security finding — and it was
				// credited until the keyword came out. The probe above passed
				// only because its fix was worded "add a trap to clean it up".
				{"the cleanup objection carrying the mktemp fix",
					review.Finding{Path: "scripts/release-notes.sh", Line: 8, Severity: "nit", Category: "maintainability",
						Title:     "The temporary file is never removed",
						Rationale: "Nothing deletes the JSON after the two jq reads, so each run leaves a file behind. Use OUT=$(mktemp) and trap 'rm -f \"$OUT\"' EXIT."}},
			},
		},

		// The nit plants.
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
				// The same observation in the two words a reviewer actually
				// reaches for. "identical to" was chosen with its preposition
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
				// copy", "needless copy" and "no need to copy" came out — four
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
// ability to observe INFLATION (raising plants removes that ability), check what
// it does to TestNoDegenerateReviewerCanMaxOutAPublishedMetric — a corpus whose
// plants all sit at one level is one no severity metric can be falsified on, and
// that is how the withdrawn cross-tool column came to be maximised by a reviewer
// with no severity opinion at all — and then update the entry, along with the
// pinned incumbent numbers in TestIncumbentObjectiveSeverityOnTheShippedCache.
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

		// The warning and nit plants that ended the skew. These are the levels
		// the corpus previously could not resolve at all — one warning and one
		// nit across fifteen fixtures — so a drift here is not one plant moving,
		// it is the bottom of the scale becoming unmeasurable again.
		"ts-unbounded-memo-key/src/search.ts:13/resource":   config.SeverityWarning,
		"go-cancel-goroutine-leak/resolve.go:24/resource":   config.SeverityWarning,
		"python-timing-unsafe-hmac/webhook.py:15/security":  config.SeverityWarning,
		"cross-file-copy-nit/report/summary.go:17/resource": config.SeverityNit,
		"sorted-for-min-nit/sensors.py:23/resource":         config.SeverityNit,

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
// perfectly on a severity metric — which is why one such metric has already been
// withdrawn — and ONE defect was the entire unit of resolution for every claim
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
//     tolerated silently — see the note on info below.
//  2. No level may hold more than half a corpus's plants, which bounds what a
//     reviewer with no severity opinion at all can score by answering that level
//     to everything.
//  3. At least three distinct levels per corpus, so the scale being measured is
//     a scale rather than a threshold.
//
// WHAT THIS TEST DOES NOT CLAIM. Balance is not calibration. Nothing here can
// check that a plant's WantSeverity is the level a senior reviewer would really
// pick — that is a judgement, argued per plant against the anchors in
// internal/prompt/templates/review.md and pinned by
// TestPlantedSeveritiesArePinned. This test is the guard against the OTHER
// failure, the one that looks like tidying: a corpus rebalanced by RELABELLING
// is worse than a skewed one, because it looks like evidence. Rule 1 is what
// stops the cheapest version of that — dialling a single plant down to fill an
// empty bucket — from ever being enough.
//
// INFO IS EMPTY, and deliberately visible in every message this prints. Three
// levels were authored to fix the skew; the info level was assigned to an author
// that produced nothing, so the corpus still cannot resolve a single claim about
// it. Rule 1 means the next person to work on this cannot close the gap halfway:
// one info plant fails this test, and two pass it.
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
// warningFixtures and nitFixtures are the record of what was AUTHORED at each
// level. Neither is a corpus: Fixtures() and HeldOutFixtures() name the
// individual constructors, because the five warning plants and the five nit
// plants are each split across both. That split is deliberate and is argued in
// HeldOutFixtures — but it means the authored set and the measured set are two
// different lists, and nothing else in the tree compares them.
//
// A fixture missing from both corpora is invisible in the worst way. It
// compiles, it is exercised by every ground-truth test that iterates
// AllFixtures... except that it is not IN AllFixtures, so it is exercised by
// nothing at all: no line check, no keyword probe, no severity pin. It is a
// plant that measures nothing while looking, in the diff that added it, exactly
// like a plant that does. The reverse — the same fixture in both corpora — is
// the leak TestHeldOutCorpusStaysHeldOut catches by name; this catches it by
// count for the authored sets.
func TestEveryAuthoredFixtureIsWiredIntoExactlyOneCorpus(t *testing.T) {
	authored := map[string][]Fixture{
		"warningFixtures": warningFixtures(),
		"nitFixtures":     nitFixtures(),
	}

	tuning := map[string]bool{}
	for _, f := range Fixtures() {
		tuning[f.Name] = true
	}
	held := map[string]bool{}
	for _, f := range HeldOutFixtures() {
		held[f.Name] = true
	}

	for _, set := range []string{"warningFixtures", "nitFixtures"} {
		for _, f := range authored[set] {
			switch {
			case tuning[f.Name] && held[f.Name]:
				t.Errorf("%s authored %q and it is in BOTH corpora, so a fixture reported as held "+
					"out is one the prompt is tuned on", set, f.Name)
			case !tuning[f.Name] && !held[f.Name]:
				t.Errorf("%s authored %q and NO corpus contains it, so it is not in AllFixtures and "+
					"no ground-truth test touches it: its lines are unchecked, its keywords are "+
					"unprobed, its severity is unpinned, and it measures nothing. Add it to "+
					"Fixtures() or HeldOutFixtures()", set, f.Name)
			}
		}
	}
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
// retracted too. It is DESCRIBED instead — see NoCrossToolSeverityScore.
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

	// Unrecognized input must still produce a usable severity — an unknown word
	// has to yield a finding, which is why crSeverity has a default at all.
	for _, word := range []string{"blocker", "trivial", "", "  ", "banana"} {
		if got := crSeverity(word); !got.IsFinding() {
			t.Errorf("crSeverity(%q) = %q, which no finding may carry", word, got)
		}
	}

	// The words crSeverity translates rather than degrades, enumerated.
	//
	// crSeverity's default arm is symmetric with our own models by construction
	// — it calls the same Normalize — and the guard for that only ever tested
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
// full resolution — it LOSES one there. Two calls it could not previously win
// became accurate (the hardcoded secret and multi-defect's traversal, both
// planted critical), and three that were accurate became INFLATED, because the
// same word it uses for those two is the word it used on plants of error
// (nil-deref, SQL injection, command injection). That is not a new bias; it is
// the same vocabulary mismatch pointing the other way, and it is the proof that
// no constant in crSeverity could have fixed it.
//
// A THIRD COLUMN USED TO BE PINNED HERE and is deleted: the banded triple, whose
// 5/0/2 on this corpus was quoted as "0.62" — it is 0.714 — and offered as the
// cross-tool result. It is withdrawn, and reconstructing it says why: a reviewer
// stamping one blocking word on every finding bands 7/1/0 here against the
// incumbent's 5/0/2, one that reports only the already-blocking plants bands
// 7/0/0, and the column moved not at all when the parser bug above was fixed.
// See NoCrossToolSeverityScore.
//
// WHAT SURVIVES IS NOT A CROSS-TOOL SCORE. These counts are pinned as EVIDENCE
// about this cache — they catch a fixture edited without re-collecting, and a
// scorer change that silently moves the incumbent — and Incumbent's own O-*
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
	// apart from the failure it exists for — a cached fixture EDITED without
	// re-collecting, which zeroes its counts and reads as the numbers improving.
	//
	// So the set is written down instead of derived. Both directions are checked
	// below, and the second is the one that matters: a fixture in this list with
	// no cache was edited, and a tuning fixture with a cache that is NOT in this
	// list would silently move the pinned totals underneath them.
	//
	// WHAT THIS COSTS, stated because the number below is quoted elsewhere: the
	// incumbent's severity reading now covers EIGHT of the thirteen tuning
	// fixtures, not all of them, and the five it omits are exactly the warning
	// and nit plants. Its 2/3/2 is therefore a statement about the corpus as it
	// stood when it was collected — which was already the honest reading of it,
	// since it was never a cross-tool score — and it is not evidence about how
	// the incumbent rates the two levels this corpus previously could not
	// resolve. Answering that takes a collection run.
	covered := map[string]bool{
		"go-nil-deref": true, "go-sql-injection": true, "go-hardcoded-secret": true,
		"python-command-injection": true, "clean-refactor": true, "style-only": true,
		"multi-defect": true, "capacity-hint-nit": true,
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

	want := SeverityScore{Accurate: 2, Inflated: 3, Understated: 2}

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
	// is exactly the resolution difference no mapping can repair — and it is why
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

// TestTheMakefileSpendsTheWholeHeldOutCorpus keeps the one-shot run from
// quietly becoming a partial one.
//
// The held-out corpus is spent by `make eval FIXTURES=$(HELD_OUT)`, and HELD_OUT
// is a hand-typed comma-separated line in the Makefile. Adding a fixture to
// HeldOutFixtures() does not add it there, and nothing in the tree noticed:
// five were added to the corpus in one change and the Makefile still named
// seven, so the generalization run would have measured a subset and printed a
// table saying "HELD-OUT corpus (7 fixtures)" — which is true, and is not the
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

// TestTheCorpusStillAssemblesTheWayItsCommentsClaim reads the batching back out
// of bundle.Assemble instead of trusting the sentences that describe it.
//
// Three separate comments in this package rest on one measured fact:
// ts-unbounded-memo-key changes seven files, so at the shipped
// max_files_per_request of 6 it is the only fixture that assembles into more
// than one batch. HeldOutFixtures argues its whole tuning/held-out split on
// that fact, fixtures_warning.go calls the file count load-bearing, and the
// fixture's own comment declares an invariant tighter still: the three files a
// reviewer needs in order to SEE the defect — the caller that breaks the
// contract, the helper that states it, and the control that honours it — must
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
//   - Adding one more ordinary file whose path sorts before src/search.ts —
//     src/format.ts, say — fills the first batch with the six alphabetically
//     earliest paths and pushes search.ts into the second, ALONE with types.ts.
//     The planted defect is then structurally unfindable: the reviewer reading
//     the batch that contains it has never seen cache.ts's contract or plans.ts
//     honouring it, so every model scores a miss and the eval reports a corpus
//     bug as a prompt weakness — the exact outcome the fixture says must be
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
