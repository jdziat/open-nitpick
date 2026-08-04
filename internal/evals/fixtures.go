// Package evals runs open-nitpick against real models and scores the result.
//
// Unit tests prove the tooling behaves correctly given a scripted model. They
// cannot tell you whether the PROMPTS work — whether a real model, handed a
// real diff, actually finds the bug, anchors it to the right line, and stays
// quiet about code that is fine. That is what this package measures.
//
// It is deliberately not part of `go test ./...`: it costs money and needs
// network. Run it with `make eval`.
package evals

import "github.com/jdziat/open-nitpick/internal/config"

// Defect is a bug deliberately planted in a fixture, with enough information to
// recognize a finding that reports it.
type Defect struct {
	// Path and Line locate the defect in the file AFTER the change.
	Path string
	Line int

	// Keywords: a finding counts as detecting this defect when its title or
	// rationale contains at least one. They are matched case-insensitively and
	// are deliberately about the CONSEQUENCE, not the wording of a fix, so a
	// model is not penalized for phrasing.
	//
	// A keyword must not be a token a reviewer would type merely by QUOTING the
	// change. Six of them were, and each one paid full recall to a comment that
	// had noticed nothing: "created_at" is the line the diff removes, "foreach"
	// and "await" are the changed line itself, "owner" survives untouched in
	// the head, "location" sits in an unchanged doc comment, and "subprocess"
	// is both an import and the word the correct FIX uses — it credited a
	// comment about tar's exit status with finding a command injection.
	// TestKeywordsAdmitOnlyRealDetections holds the line with the actual
	// findings rather than with a list of words.
	Keywords []string

	// Class is the closest class in the shipped taxonomy.
	//
	// It exists so severity consistency can be checked mechanically: two plants
	// of the same class must agree about WantSeverity or say why they do not.
	// The security class is why. It holds five plants across two levels, and
	// which level each one gets turns on a distinction — whether the untrusted
	// source is IN the diff or only in Defect.Why, which the reviewer never
	// sees — that no reader would reconstruct from the severities alone.
	// Unwritten, that distinction is indistinguishable from drift, and a plant
	// can then be moved in whichever direction the week's numbers want.
	//
	// It is NOT ground truth for a model's class assignment and nothing scores
	// against it. capacity-hint-nit is why: the closed set has no home for an
	// allocation that is merely unnecessary, so `resource` there is the
	// least-wrong box rather than a correct answer, and grading a model against
	// it would be grading it against a gap in the taxonomy.
	//
	// It is also author-declared and unverified, so it cannot be the only guard
	// on a severity: an editor moving a plant's level can move its class to
	// match and silence the consistency check. TestPlantedSeveritiesArePinned
	// is the guard that does not depend on it.
	Class config.Class

	// WantSeverity is the severity a correct reviewer should assign — a TARGET,
	// not a floor. Rating a defect above it counts as inflation and below it as
	// understatement (see severityVerdict), because over-claiming is one of the
	// two failures the severity columns exist to tune away and "at least the
	// floor" cannot see it. Author fixtures to the level a senior reviewer would
	// actually pick, not to the lowest defensible one.
	//
	// Author it against the anchors in internal/prompt/templates/review.md,
	// which is the definition the model is actually given. A plant that
	// disagrees with the published anchor does not measure the reviewer; it
	// penalises the reviewer for obeying its instructions.
	WantSeverity config.Severity

	// SeverityNote is why this plant's WantSeverity departs from others of its
	// Class. TestSeverityIsConsistentWithinADefectClass requires one from EVERY
	// member of a class carrying more than one severity: with two plants
	// disagreeing there is no fact about which is the outlier, and letting a
	// single note excuse a whole class would reopen the hole this closes.
	//
	// It must NAME the level it is defending, in words. A note that only
	// explains a defect goes stale the moment the level moves and then reads as
	// a justification for a number it never justified — which is worse than no
	// note, because the next editor trusts it. Naming the level makes the test
	// able to catch that, and it is the only part of a note a test can check.
	SeverityNote string

	// Why documents the defect for whoever reads a failing eval report.
	Why string
}

// Fixture is a synthetic pull request: a before state, an after state, and the
// defects a good reviewer should find in the change.
type Fixture struct {
	Name string

	// Base is the committed starting state.
	Base map[string]string

	// Head replaces Base to produce the diff under review.
	Head map[string]string

	// Defects are what a competent reviewer must find. An empty list means the
	// change is genuinely fine and any finding is a false positive.
	Defects []Defect

	// Extra files exist in both states and are never changed. They give the
	// model surrounding context without appearing in the diff.
	Extra map[string]string
}

// Clean reports whether the fixture contains no planted defects.
func (f Fixture) Clean() bool { return len(f.Defects) == 0 }

// Fixtures is the corpus. Each one is small on purpose: the point is to measure
// whether the prompt finds an unambiguous bug, not to benchmark long-context
// reasoning.
func Fixtures() []Fixture {
	return []Fixture{
		goNilDerefFixture(),
		goSQLInjectionFixture(),
		goHardcodedSecretFixture(),
		pythonCommandInjectionFixture(),
		cleanRefactorFixture(),
		styleOnlyFixture(),
		multiDefectFixture(),
		subtleLogicFixture(),
	}
}

// HeldOutFixtures is a second corpus the prompt tuning never sees.
//
// Tuning a prompt against Fixtures() and then reporting a score on Fixtures()
// measures nothing: with enough iterations any prompt can be shaped to eight
// specific changes, and the number that falls out says nothing about a real
// pull request. This set is spent ONCE, at the end, to check the gain
// generalized rather than memorized.
//
// It is deliberately unreachable from Fixtures() and from the default run, so
// nothing picks it up by accident. Selecting it takes naming a fixture in
// NITPICK_EVAL_FIXTURES — an explicit act by whoever is measuring.
//
// The defect classes here are chosen to be ones the tuning corpus does NOT
// contain, because a held-out set drawn from the same distribution measures
// memorization of that distribution rather than generalization:
//
//   - contract-break     a wire-format change that silently breaks consumers
//   - data-loss-migration an UPDATE with no WHERE, in SQL
//   - ts-unawaited-async  a defect in TypeScript, so the prompt is not
//     silently tuned to Go and Python
//   - timezone-boundary   a correctness bug a careless reviewer waves through
//   - clean-sql-allowlist code that pattern-matches SQL injection and is
//     provably safe; the correct review is silence
//   - removed-guard       the defect is in the REMOVED lines, which a reviewer
//     that only reads additions cannot see
//   - retry-no-backoff    a real defect that is only worth a WARNING, so the
//     severity columns can be falsified in both directions
//
// That last one is about the instrument rather than the defect class. Without
// it every plant here is error or critical, and a prompt that learned to answer
// "at least error" to everything — the exact failure the O-INFL column exists
// to catch — cannot be caught by the corpus that is supposed to check whether
// the tuning generalized. TestHeldOutCorpusCanFalsifyInflation pins it.
func HeldOutFixtures() []Fixture {
	return []Fixture{
		contractBreakFixture(),
		dataLossMigrationFixture(),
		typescriptUnawaitedAsyncFixture(),
		timezoneBoundaryFixture(),
		cleanSQLAllowlistFixture(),
		removedGuardFixture(),
		retryNoBackoffFixture(),
	}
}

// AllFixtures is every fixture in both corpora.
//
// It exists so the NITPICK_EVAL_FIXTURES filter can name a held-out fixture
// without Fixtures() — the default run, and therefore every tuning loop —
// changing what it returns. Ground-truth tests iterate this: a corpus that is
// not validated is worse than no corpus, and "held out" is not an excuse to
// skip the check that caught four wrong line numbers in the first eight.
func AllFixtures() []Fixture {
	all := Fixtures()
	return append(all, HeldOutFixtures()...)
}

// contractBreakFixture renames the wire name of a field on a public payload.
//
// Nothing in the tuning corpus tests the contract class, and this is the shape
// that matters: the compiler is happy, the tests are happy, and every deployed
// consumer of the endpoint silently starts reading a zero value. The added
// UpdatedAt field supplies the motivation a real pull request would carry —
// "make the tags consistent" — which is exactly the reasoning a reviewer has to
// refuse.
func contractBreakFixture() Fixture {
	return Fixture{
		Name: "contract-break",
		Base: map[string]string{
			"event.go": `package api

import "time"

// Event is the payload returned by the public events endpoint.
type Event struct {
	ID        string    ` + "`json:\"id\"`" + `
	Kind      string    ` + "`json:\"kind\"`" + `
	CreatedAt time.Time ` + "`json:\"created_at\"`" + `
}

// Events returns the recent events.
func Events() []Event { return nil }
`,
		},
		Head: map[string]string{
			"event.go": `package api

import "time"

// Event is the payload returned by the public events endpoint.
type Event struct {
	ID        string    ` + "`json:\"id\"`" + `
	Kind      string    ` + "`json:\"kind\"`" + `
	CreatedAt time.Time ` + "`json:\"createdAt\"`" + `
	UpdatedAt time.Time ` + "`json:\"updatedAt\"`" + `
}

// Events returns the recent events.
func Events() []Event { return nil }
`,
		},
		Defects: []Defect{{
			Path: "event.go",
			Line: 9, // the retagged CreatedAt field
			// No "json tag", "naming" or "inconsistent": a finding that the
			// tags disagree about case has noticed the edit without noticing
			// that it breaks anyone. Detection here means naming the
			// consequence for existing consumers.
			//
			// "created_at" was on this list and defeated the whole distinction:
			// it is the REMOVED line, so every style objection to the retag
			// quotes it and scored as detection.
			Keywords: []string{"breaking", "backward", "compatib", "wire format", "existing client", "consumer", "api contract", "decode a zero", "already deployed"},
			Class:    config.ClassContract,
			// error, not critical. The demonstrated consequence is a zero
			// timestamp — "incorrect behavior on a reachable path" — not data
			// loss, not a breach, and not a failure that is guaranteed:
			// consumers keep serving, wrongly. Calibration rule 2 takes the
			// lower level. It is the only plant of its class, so no note is
			// owed and TestSeverityIsConsistentWithinADefectClass has nothing to
			// compare it against; TestPlantedSeveritiesArePinned is what keeps
			// it from moving unremarked.
			WantSeverity: config.SeverityError,
			Why:          "created_at is renamed to createdAt on a public payload, so every existing consumer silently decodes a zero timestamp",
		}},
	}
}

// dataLossMigrationFixture ships an UPDATE with no WHERE clause.
//
// The corpus has no data-loss fixture and no SQL file. The comment states the
// intent — legacy rows, the ones with a NULL plan — and the statement below it
// rewrites every row in the table, downgrading paying accounts. It is
// irreversible: the old values are gone once the transaction commits.
func dataLossMigrationFixture() Fixture {
	return Fixture{
		Name: "data-loss-migration",
		Base: map[string]string{
			"migrations/0006_add_plan.sql": `-- 0006: record which plan an account is on.

ALTER TABLE accounts ADD COLUMN plan text;
`,
		},
		Head: map[string]string{
			"migrations/0007_backfill_plan.sql": `-- 0007: give every legacy account an explicit plan.
--
-- Accounts created before the billing rewrite have a NULL plan, which the
-- dashboard renders as an empty badge.

BEGIN;

UPDATE accounts SET plan = 'free';

ALTER TABLE accounts ALTER COLUMN plan SET NOT NULL;

COMMIT;
`,
		},
		Defects: []Defect{{
			Path: "migrations/0007_backfill_plan.sql",
			Line: 8, // the unqualified UPDATE
			// Not "lock" or "index": a migration that takes a long lock is a
			// real concern and a DIFFERENT one, and crediting it here would
			// score a reviewer that never noticed the data was destroyed.
			//
			// "irreversible" went the same way and is gone: "there is no down
			// migration, so a bad deploy is irreversible" is a stock objection
			// to any migration and says nothing about the missing WHERE.
			Keywords: []string{
				"where clause", "missing where", "without a where", "unconditional",
				"data loss", "overwrite", "every account", "every row", "all rows", "destroy",
			},
			Class: config.ClassDataLoss,
			// "data loss", the critical anchor's first clause, verbatim: the old
			// plan values are gone once the transaction commits.
			WantSeverity: config.SeverityCritical,
			Why:          "the UPDATE has no WHERE, so it overwrites the plan of every account rather than only the NULL rows the comment describes",
		}},
	}
}

// typescriptUnawaitedAsyncFixture puts a defect in a third language.
//
// SEVEN of the eight tuning fixtures are Go and one is Python; a prompt tuned
// on that corpus can be Go-shaped without anyone noticing. The defect is the
// canonical JavaScript one: an async callback handed to forEach, which ignores
// the promise it returns. saveAll now resolves before a single item is stored,
// and a rejected save becomes an unhandled rejection instead of the caller's
// error.
func typescriptUnawaitedAsyncFixture() Fixture {
	return Fixture{
		Name: "ts-unawaited-async",
		Base: map[string]string{
			"src/sync.ts": `export interface Item {
  id: string;
  body: string;
}

export type Save = (item: Item) => Promise<void>;

// saveAll persists every item, and resolves once they are all stored.
export async function saveAll(items: Item[], save: Save): Promise<void> {
  for (const item of items) {
    await save(item);
  }
}
`,
		},
		Head: map[string]string{
			"src/sync.ts": `export interface Item {
  id: string;
  body: string;
}

export type Save = (item: Item) => Promise<void>;

// saveAll persists every item, and resolves once they are all stored.
export async function saveAll(items: Item[], save: Save): Promise<void> {
  items.forEach(async (item) => {
    await save(item);
  });
}
`,
		},
		Extra: map[string]string{
			"tsconfig.json": "{\n  \"compilerOptions\": {\n    \"target\": \"ES2022\",\n    \"strict\": true\n  }\n}\n",
		},
		Defects: []Defect{{
			Path: "src/sync.ts",
			Line: 10, // the forEach with an async callback
			// Not "unbounded": that the writes now run concurrently is a
			// separate objection, and a reviewer raising only it has not seen
			// that the function lies about being finished.
			//
			// "foreach", "await" and "promise" were on this list and are the
			// three most-typed tokens of the changed lines, so "prefer for...of
			// for readability" — and even "fine as written" — scored as
			// detection while the unbounded-concurrency objection the comment
			// above rejects walked straight back in through "promise".
			Keywords: []string{
				"unhandled rejection", "fire-and-forget", "not awaited", "never awaited",
				"discards the promise", "ignores the returned promise",
				"resolves before", "returns before", "does not wait",
			},
			Class:        config.ClassCorrectness,
			WantSeverity: config.SeverityError,
			Why:          "forEach discards the promise each async callback returns, so saveAll resolves before any item is stored and save's errors are never surfaced",
		}},
	}
}

// timezoneBoundaryFixture uses time.Truncate to find the start of a day.
//
// This is the fixture for the bug a careless reviewer waves through, because
// the line reads exactly like what it claims to do. Truncate rounds down since
// the zero time, which is UTC, so for any t carrying a non-UTC location the
// result is UTC midnight — mid-afternoon or the previous evening locally, and
// every daily aggregate built on it covers the wrong window.
func timezoneBoundaryFixture() Fixture {
	return Fixture{
		Name: "timezone-boundary",
		Base: map[string]string{
			"report.go": `package report

import "time"

// Day is the calendar day a timestamp falls in, in the location it carries.
func Day(t time.Time) (year int, month time.Month, day int) {
	return t.Date()
}
`,
		},
		Head: map[string]string{
			"report.go": `package report

import "time"

// Day is the calendar day a timestamp falls in, in the location it carries.
func Day(t time.Time) (year int, month time.Month, day int) {
	return t.Date()
}

// StartOfDay returns midnight at the beginning of t's calendar day. Daily
// reports use it as the lower bound of the range they aggregate.
func StartOfDay(t time.Time) time.Time {
	return t.Truncate(24 * time.Hour)
}
`,
		},
		Defects: []Defect{{
			Path: "report.go",
			Line: 13, // the Truncate call
			// Not "truncate": Truncate also strips the monotonic reading, which
			// is true, unrelated, and would let a reviewer earn credit for
			// noticing something that is not the bug. Detection requires naming
			// the zone.
			//
			// Nor "location", for the same reason it looked safe: the word is
			// already in the file's own unchanged doc comment, so "say which
			// location the result carries" — a documentation nit — matched.
			Keywords: []string{"utc", "timezone", "time zone", "local midnight", "dst", "daylight", "zone offset", "wrong day"},
			Class:    config.ClassCorrectness,
			// The error anchor's own worked example is "comparing timestamps
			// from two different timezones", which is this defect exactly.
			WantSeverity: config.SeverityError,
			Why:          "Truncate rounds relative to the zero time in UTC, so StartOfDay returns UTC midnight rather than midnight in t's location",
		}},
	}
}

// cleanSQLAllowlistFixture builds a query with Sprintf and is correct.
//
// This is the most valuable fixture in the set, because precision is where the
// benchmark loses. It pattern-matches the corpus's own SQL-injection fixture —
// fmt.Sprintf, a query string, a caller-supplied argument — but the sort key is
// resolved through a map whose values are all source literals, and the only
// caller-controlled value is passed as a parameter. ORDER BY genuinely cannot
// take a placeholder, so there is no safer shape to move to.
//
// The correct review is silence. Any injection finding here is a false
// positive, and a prompt that earns recall by flagging every Sprintf near SQL
// pays for it on exactly this change.
func cleanSQLAllowlistFixture() Fixture {
	return Fixture{
		Name: "clean-sql-allowlist",
		Base: map[string]string{
			"query.go": `package store

import "database/sql"

type Store struct{ db *sql.DB }

// ListUsers returns a page of users ordered by name.
func (s *Store) ListUsers(limit int) (*sql.Rows, error) {
	return s.db.Query("SELECT id, email FROM users ORDER BY name LIMIT ?", limit)
}
`,
		},
		Head: map[string]string{
			"query.go": `package store

import (
	"database/sql"
	"fmt"
)

type Store struct{ db *sql.DB }

// ListUsers returns a page of users ordered by name.
func (s *Store) ListUsers(limit int) (*sql.Rows, error) {
	return s.db.Query("SELECT id, email FROM users ORDER BY name LIMIT ?", limit)
}

// sortColumns is the allowlist of sort keys. A key absent from it is rejected,
// so no caller-supplied text ever reaches the query.
var sortColumns = map[string]string{
	"name":    "name",
	"email":   "email",
	"created": "created_at",
}

// ListUsersBy returns a page of users ordered by the named sort key.
func (s *Store) ListUsersBy(sortKey string, limit int) (*sql.Rows, error) {
	column, ok := sortColumns[sortKey]
	if !ok {
		return nil, fmt.Errorf("unknown sort key %q", sortKey)
	}

	// column is one of the three literals above, never caller text. ORDER BY
	// cannot take a placeholder, so the column is interpolated while the value
	// stays a parameter.
	query := fmt.Sprintf("SELECT id, email FROM users ORDER BY %s LIMIT ?", column)
	return s.db.Query(query, limit)
}
`,
		},
	}
}

// removedGuardFixture deletes an ownership check.
//
// Every defect in the tuning corpus is in an ADDED line, so a reviewer that
// reads only the + side of a diff scores well on all eight. Here the added
// lines are innocuous and the defect is what the change removed: any
// authenticated caller can now delete any project. The doc comment carries the
// plausible-sounding justification a real pull request would, and it is true as
// far as it goes — the extra read WAS latency — which is what makes the review
// a judgement rather than a lookup.
//
// That rewritten doc comment is also load-bearing, not decoration. The deleted
// check leaves the delete call itself as unchanged CONTEXT, and filterAnchors
// drops any finding with no added line within three: with the comment left
// alone the change is a pure deletion, and a perfectly correct finding on this
// line would be discarded before publication. TestEveryPlantedDefectIsReportable
// fails if that is edited away.
func removedGuardFixture() Fixture {
	return Fixture{
		Name: "removed-guard",
		Base: map[string]string{
			"project.go": `package service

import (
	"context"
	"errors"
)

// ErrForbidden is returned when a caller does not own the project.
var ErrForbidden = errors.New("forbidden")

// Store persists projects.
type Store interface {
	Project(ctx context.Context, id string) (Project, error)
	DeleteProject(ctx context.Context, id string) error
}

// Project is a user's project.
type Project struct {
	ID      string
	OwnerID string
}

// Service handles project operations.
type Service struct{ store Store }

// DeleteProject removes a project the caller owns.
func (s *Service) DeleteProject(ctx context.Context, callerID, projectID string) error {
	p, err := s.store.Project(ctx, projectID)
	if err != nil {
		return err
	}

	if p.OwnerID != callerID {
		return ErrForbidden
	}

	return s.store.DeleteProject(ctx, projectID)
}
`,
		},
		Head: map[string]string{
			"project.go": `package service

import (
	"context"
	"errors"
)

// ErrForbidden is returned when a caller does not own the project.
var ErrForbidden = errors.New("forbidden")

// Store persists projects.
type Store interface {
	Project(ctx context.Context, id string) (Project, error)
	DeleteProject(ctx context.Context, id string) error
}

// Project is a user's project.
type Project struct {
	ID      string
	OwnerID string
}

// Service handles project operations.
type Service struct{ store Store }

// DeleteProject removes a project.
//
// The extra read was pure latency: the store already returns an error for an
// unknown id.
func (s *Service) DeleteProject(ctx context.Context, callerID, projectID string) error {
	return s.store.DeleteProject(ctx, projectID)
}
`,
		},
		Defects: []Defect{{
			Path: "project.go",
			Line: 31, // the now-unguarded delete
			// Not "unused": callerID going unread is the SYMPTOM, and a finding
			// that only asks for the parameter to be dropped has proposed
			// making the hole permanent.
			//
			// "owner" is gone for the same reason: OwnerID survives untouched
			// in the head and the base doc comment names it, so both the
			// drop-the-parameter finding and a stale-doc nit matched. The
			// remaining "ownership" is a word only a reviewer reasoning about
			// the CHECK reaches for.
			Keywords:     []string{"authoriz", "access control", "ownership", "idor", "permission", "privilege", "any caller", "delete any", "anyone can"},
			Class:        config.ClassSecurity,
			WantSeverity: config.SeverityCritical,
			SeverityNote: "critical under \"a security breach\", above the two injection plants, because the " +
				"breach is visible in the diff rather than asserted about a caller: the REMOVED lines are " +
				"the ownership check itself, so the change demonstrates that any authenticated caller " +
				"now deletes any project. Calibration rule 1 confirms it rather than lowering it — the " +
				"caller ends up destroying projects it never had any access to.",
			Why: "the ownership check was deleted, so any authenticated caller can delete any project by id",
		}},
	}
}

// retryNoBackoffFixture adds a retry loop that fires attempts back to back.
//
// Every other plant in this corpus is error or critical, which leaves the
// severity measurement one-sided: understatement is observable everywhere and
// inflation almost nowhere. This one is a genuine defect that a senior reviewer
// raises at WARNING and not above — five immediate retries turn one client's
// blip into five times the load on a service that is already failing — so a
// reviewer that answers "error" to everything is visibly wrong here, on the
// corpus that decides whether the tuning generalized.
//
// The retry itself is correct: the loop count is right, the last error is
// preserved and re-raised. Only the missing delay is wrong, which is why the
// keywords name the wait and never the loop.
func retryNoBackoffFixture() Fixture {
	return Fixture{
		Name: "retry-no-backoff",
		Base: map[string]string{
			"client.py": `import urllib.request


def fetch(url):
    """Fetch a URL and return its body."""
    return urllib.request.urlopen(url).read()
`,
		},
		Head: map[string]string{
			"client.py": `import urllib.error
import urllib.request

# Transient failures are common enough that one attempt is not enough.
ATTEMPTS = 5


def fetch(url):
    """Fetch a URL and return its body, retrying transient failures."""
    last = None
    for _ in range(ATTEMPTS):
        try:
            return urllib.request.urlopen(url).read()
        except urllib.error.URLError as err:
            last = err
    raise last
`,
		},
		Extra: map[string]string{
			"pyproject.toml": "[project]\nname = \"client\"\nversion = \"0.1.0\"\n",
		},
		Defects: []Defect{{
			Path: "client.py",
			Line: 11, // the retry loop
			// None of these appear anywhere in the file, so nothing here can be
			// earned by quoting the change. The two objections this must NOT
			// credit are the broad `except urllib.error.URLError` and ATTEMPTS
			// being a literal: both are about different code and neither reaches
			// for a word about waiting.
			Keywords: []string{
				"backoff", "back off", "back-off", "sleep", "jitter",
				"thundering herd", "hammer", "delay between", "no delay", "without waiting",
			},
			// Classed by what is consumed — a struggling dependency's capacity.
			// The closed set has no "resilience", so this is the nearest box
			// rather than a snug one.
			Class:        config.ClassResource,
			WantSeverity: config.SeverityWarning,
			SeverityNote: "warning under \"a genuine hazard under plausible conditions\", between the error-level " +
				"descriptor leak and the nit-level capacity hint that share its class: the retry itself is " +
				"correct, nothing is yet wrong on a normal path, and the amplification is bounded at five.",
			Why: "the retries fire immediately one after another, so a failing dependency is hit five times per call with no backoff",
		}},
	}
}

// multiDefectFixture plants three independent defects in one change.
//
// Single-defect fixtures cannot distinguish "found the bug" from "stopped after
// the first bug", and stopping early is the failure mode a reviewer actually
// exhibits: it satisfices. This is the fixture that measures whether the prompt
// keeps looking.
func multiDefectFixture() Fixture {
	return Fixture{
		Name: "multi-defect",
		Base: map[string]string{
			"handler.go": `package api

import "net/http"

// Handler serves uploads.
type Handler struct{}

func (h *Handler) Upload(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotImplemented)
}
`,
		},
		Head: map[string]string{
			"handler.go": `package api

import (
	"fmt"
	"net/http"
	"os"
	"sync"
)

// Handler serves uploads.
type Handler struct {
	mu    sync.Mutex
	count int
}

func (h *Handler) Upload(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")

	f, err := os.Create("/var/uploads/" + name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	go func() {
		h.count++
	}()

	if _, err := f.ReadFrom(r.Body); err != nil {
		http.Error(w, fmt.Sprintf("upload failed: %v", err), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}
`,
		},
		Defects: []Defect{
			{
				Path:         "handler.go",
				Line:         19,
				Keywords:     []string{"path traversal", "traversal", "sanitiz", "arbitrary", "../", "untrusted", "user-controlled path"},
				Class:        config.ClassSecurity,
				WantSeverity: config.SeverityCritical,
				SeverityNote: "critical under \"a security breach\", above the two injection plants, and the " +
					"difference is what the diff shows. Head line 12 is r.URL.Query().Get(\"name\"), so the " +
					"untrusted source, the sink, and the path between them are all in front of the " +
					"reviewer; a crafted name writes outside /var/uploads, which is access the caller " +
					"did not have. go-sql-injection and python-command-injection show only the sink.",
				Why: "the query parameter is concatenated into a filesystem path, so ../ escapes the upload directory",
			},
			{
				Path:         "handler.go",
				Line:         25,
				Keywords:     []string{"race", "data race", "mutex", "unsynchron", "concurrent", "atomic"},
				Class:        config.ClassConcurrency,
				WantSeverity: config.SeverityError,
				Why:          "h.count is incremented from a goroutine with no synchronization despite the struct carrying a mutex",
			},
			{
				Path:         "handler.go",
				Line:         19,
				Keywords:     []string{"close", "leak", "file descriptor", "not closed", "defer"},
				Class:        config.ClassResource,
				WantSeverity: config.SeverityError,
				SeverityNote: "error under \"a real bug that produces incorrect behavior on a reachable path\": " +
					"the leak is on the NORMAL path, not a hazardous one — every successful upload loses a " +
					"descriptor, with no condition to be met and no bound on the total. That is what " +
					"separates it from retry-no-backoff, a warning because it is \"a genuine hazard under " +
					"plausible conditions\" that has not gone wrong yet, and from capacity-hint-nit, whose " +
					"one reallocation is the anchor's own nit example.",
				Why: "the created file is never closed, leaking a descriptor on every request",
			},
		},
	}
}

// subtleLogicFixture plants a one-short capacity hint.
//
// Note what is NOT wrong here: the loop bound is correct. It runs
// len(samples)-n+1 times, which is exactly the number of sliding windows. Only
// the capacity passed to make() is one short, so the final append reallocates.
//
// That distinction is the point. A reviewer that "finds an off-by-one in the
// loop" is hallucinating, and must not score as a hit — which is why the
// keywords below name the allocation, never the bound.
func subtleLogicFixture() Fixture {
	return Fixture{
		Name: "capacity-hint-nit",
		Base: map[string]string{
			"window.go": `package stats

// Window returns the last n samples.
func Window(samples []float64, n int) []float64 {
	if n >= len(samples) {
		return samples
	}
	return samples[len(samples)-n:]
}
`,
		},
		Head: map[string]string{
			"window.go": `package stats

// Window returns the last n samples.
func Window(samples []float64, n int) []float64 {
	if n >= len(samples) {
		return samples
	}
	return samples[len(samples)-n:]
}

// MovingAverage returns the average of each sliding window of width n.
func MovingAverage(samples []float64, n int) []float64 {
	if n <= 0 || len(samples) < n {
		return nil
	}

	out := make([]float64, 0, len(samples)-n)
	for i := 0; i <= len(samples)-n; i++ {
		sum := 0.0
		for j := i; j < i+n; j++ {
			sum += samples[j]
		}
		out = append(out, sum/float64(n))
	}

	return out
}
`,
		},
		Defects: []Defect{{
			Path:     "window.go",
			Line:     17,
			Keywords: []string{"capacity", "len(samples)-n+1", "reallocat"},
			// `resource` is the least-wrong box, not a correct answer: the class
			// means "leaks and unbounded growth" and this is a single bounded
			// reallocation. The closed set has no home for an allocation that is
			// merely unnecessary, which is exactly why nothing scores a model
			// against Class.
			Class:        config.ClassResource,
			WantSeverity: config.SeverityNit,
			SeverityNote: "nit under the anchor's own nit example, \"an unnecessary intermediate copy\" — a " +
				"reallocation is exactly that — while the descriptor leak it shares a class with is an error. " +
				"Nothing here produces a wrong result, so no higher clause applies.",
			Why: "the loop bound is correct, but make() reserves capacity len(samples)-n for len(samples)-n+1 appends, forcing one reallocation",
		}},
	}
}

// goNilDerefFixture plants an ignored error that makes the next line panic.
func goNilDerefFixture() Fixture {
	return Fixture{
		Name: "go-nil-deref",
		Base: map[string]string{
			"fetch.go": `package app

import "net/http"

// Fetch retrieves a URL.
func Fetch(url string) error {
	return nil
}

func _(c *http.Client) {}
`,
		},
		Head: map[string]string{
			"fetch.go": `package app

import (
	"io"
	"net/http"
)

// Fetch retrieves a URL and returns its body length.
func Fetch(url string) (int, error) {
	resp, _ := http.Get(url)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}

	return len(body), nil
}
`,
		},
		Defects: []Defect{{
			Path:     "fetch.go",
			Line:     10, // resp, _ := http.Get(url)
			Keywords: []string{"nil", "panic", "ignored error", "unchecked", "discard", "error return", "dereference"},
			Class:    config.ClassCorrectness,
			// error, not critical: the panic needs http.Get to fail first, so
			// it is not the "guaranteed production failure" the critical anchor
			// describes, and calibration rule 2 — when torn, take the lower —
			// settles what is left. A crash is not automatically critical here.
			WantSeverity: config.SeverityError,
			Why:          "http.Get's error is discarded; on failure resp is nil and the deferred Close panics",
		}},
	}
}

// goSQLInjectionFixture plants a query built by string concatenation.
func goSQLInjectionFixture() Fixture {
	return Fixture{
		Name: "go-sql-injection",
		Base: map[string]string{
			"store.go": `package store

import "database/sql"

type Store struct{ db *sql.DB }

// UserByID looks up one user.
func (s *Store) UserByID(id int) (*sql.Row, error) {
	return s.db.QueryRow("SELECT * FROM users WHERE id = ?", id), nil
}
`,
		},
		Head: map[string]string{
			"store.go": `package store

import (
	"database/sql"
	"fmt"
)

type Store struct{ db *sql.DB }

// UserByID looks up one user.
func (s *Store) UserByID(id int) (*sql.Row, error) {
	return s.db.QueryRow("SELECT * FROM users WHERE id = ?", id), nil
}

// SearchUsers finds users by name.
func (s *Store) SearchUsers(name string) (*sql.Rows, error) {
	query := fmt.Sprintf("SELECT id, email FROM users WHERE name = '%s'", name)
	return s.db.Query(query)
}
`,
		},
		Defects: []Defect{{
			Path:         "store.go",
			Line:         17, // the Sprintf query
			Keywords:     []string{"sql injection", "injection", "parameteri", "placeholder", "sanitiz", "untrusted input", "concatenat"},
			Class:        config.ClassSecurity,
			WantSeverity: config.SeverityError,
			SeverityNote: "error, below the security class's three criticals, because the diff does not show " +
				"who calls SearchUsers: Head is the bare method and there is no Base caller and no Extra. " +
				"That `name` is user-controlled is asserted by this Why field, which the reviewer never " +
				"sees, and review.md tells it \"Do not speculate about code you were not shown\". What the " +
				"change demonstrates is \"a real bug that produces incorrect behavior on a reachable path\" " +
				"— a quoted name breaks the query — and calibration rule 2, when torn take the lower, " +
				"settles the rest. multi-defect's traversal is critical because its Head contains " +
				"r.URL.Query().Get(\"name\"): the untrusted source is IN the diff there and is not here.",
			Why: "user-controlled name is interpolated into SQL instead of being passed as a parameter",
		}},
	}
}

// goHardcodedSecretFixture plants a credential in source.
func goHardcodedSecretFixture() Fixture {
	return Fixture{
		Name: "go-hardcoded-secret",
		Base: map[string]string{
			"client.go": `package client

import "os"

// APIKey returns the configured key.
func APIKey() string {
	return os.Getenv("SERVICE_API_KEY")
}
`,
		},
		Head: map[string]string{
			"client.go": `package client

import "os"

// APIKey returns the configured key.
func APIKey() string {
	if k := os.Getenv("SERVICE_API_KEY"); k != "" {
		return k
	}

	// Fall back to the shared staging key so local runs work.
	return "sk-live-9f8c2b1a7d6e5f4c3b2a1908"
}
`,
		},
		Defects: []Defect{{
			Path:         "client.go",
			Line:         12, // the literal key
			Keywords:     []string{"hardcod", "secret", "credential", "api key", "committed", "source control", "rotate"},
			Class:        config.ClassSecurity,
			WantSeverity: config.SeverityCritical,
			SeverityNote: "critical under \"a security breach\", above the two injection plants, because the " +
				"breach needs no caller at all: the ADDED line is the credential, so committing it IS " +
				"the exposure and the diff demonstrates it whole. It survives calibration rule 1 — " +
				"repository read access is not the access this key grants, so whoever reads the line " +
				"gains something they did not have. The `sk-live` prefix is the evidence; the comment " +
				"above it calling the value a staging key is the claim under review, not a finding.",
			Why: "a live-format credential is committed to source",
		}},
	}
}

// pythonCommandInjectionFixture checks the prompt is not Go-specific.
func pythonCommandInjectionFixture() Fixture {
	return Fixture{
		Name: "python-command-injection",
		Base: map[string]string{
			"tools.py": `import subprocess


def list_dir(path):
    """List a directory."""
    return subprocess.run(["ls", path], capture_output=True).stdout
`,
		},
		Head: map[string]string{
			"tools.py": `import os
import subprocess


def list_dir(path):
    """List a directory."""
    return subprocess.run(["ls", path], capture_output=True).stdout


def archive(name):
    """Archive a directory by name."""
    os.system("tar czf /backups/" + name + ".tar.gz /data/" + name)
`,
		},
		Extra: map[string]string{
			"pyproject.toml": "[project]\nname = \"tools\"\nversion = \"0.1.0\"\n",
		},
		Defects: []Defect{{
			Path: "tools.py",
			Line: 12, // the os.system call
			// Not "os.system" or "subprocess": the first is the line being
			// quoted and the second is both an import and the word the correct
			// FIX uses, so Incumbent's "Propagate archive failures" — a
			// comment about tar's ignored exit status, recommending
			// subprocess.run(check=True) — was credited with finding the
			// command injection and then graded for its severity.
			Keywords:     []string{"command injection", "injection", "shell", "untrusted", "sanitiz", "arbitrary command", "metacharacter"},
			Class:        config.ClassSecurity,
			WantSeverity: config.SeverityError,
			SeverityNote: "error, on the same reading as go-sql-injection: archive() is a bare module " +
				"function, Extra is a pyproject.toml, and nothing in the diff shows `name` arriving from " +
				"outside. The demonstrated consequence is a broken command on a reachable path, which is " +
				"the error anchor; \"a security breach\" needs an untrusted source the reviewer was not " +
				"shown. Calibration rule 2 takes the lower of the two.",
			Why: "name is concatenated into a shell command, so a crafted value runs arbitrary commands",
		}},
	}
}

// cleanRefactorFixture is a correct change. Any finding above a nit is noise,
// and noise is what gets a review bot switched off.
func cleanRefactorFixture() Fixture {
	return Fixture{
		Name: "clean-refactor",
		Base: map[string]string{
			"total.go": `package cart

// Total sums the line items.
func Total(prices []int) int {
	sum := 0
	for i := 0; i < len(prices); i++ {
		sum = sum + prices[i]
	}
	return sum
}
`,
		},
		Head: map[string]string{
			"total.go": `package cart

// Total sums the line items.
func Total(prices []int) int {
	sum := 0
	for _, price := range prices {
		sum += price
	}
	return sum
}
`,
		},
	}
}

// styleOnlyFixture is a rename with no behavior change. The prompt explicitly
// forbids commenting on naming, so findings here measure instruction-following.
func styleOnlyFixture() Fixture {
	return Fixture{
		Name: "style-only",
		Base: map[string]string{
			"greet.go": `package greet

// Greet builds a greeting.
func Greet(n string) string {
	return "hello, " + n
}
`,
		},
		Head: map[string]string{
			"greet.go": `package greet

// Greet builds a greeting.
func Greet(name string) string {
	return "hello, " + name
}
`,
		},
	}
}
