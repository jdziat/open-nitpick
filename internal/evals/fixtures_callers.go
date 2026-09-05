package evals

import "github.com/jdziat/open-nitpick/internal/config"

// CallerFixtures is the callers corpus: the change IS the contract, and the file it breaks is
// one the change does not touch.
//
// The multi-file corpus measures one direction of cross-file reasoning: a
// change USES a contract that lives in an unchanged file, and related context
// attaches that contract so the reviewer can read it. This corpus measures
// the other direction. A change alters what a function returns, raises, or
// promises, its own file is self-consistent afterwards — the doc comment is
// updated, nothing in the diff contradicts itself — and the defect exists only
// because an untouched file still calls it the old way.
//
// No reviewer shown the diff alone can find these. The diff is a plausible,
// motivated change; the evidence is a call site the diff never mentions. That
// is the point: this corpus exists to measure what a caller-aware collector
// buys, one that finds the untouched files which import the changed symbol
// and attaches how they use it. Before that collector exists the honest
// number here is the floor, and it is measured first so the gain has
// something to be measured against.
//
// Every fixture here inverts the multi-file corpus's structural rule, and
// TestCallersCorpusIsWellFormed holds the inverted rule: the changed file is
// imported by a file that is byte-identical in Base and Head, and every plant
// sits on a line the change added IN THE CONTRACT FILE, because that is the
// line a reviewer comments on — "this breaks web/users.go" belongs on the
// line that breaks it.
//
// Keywords credit only a finding that has SEEN the caller: its file, its
// function, or a detail that exists nowhere else — the body length the upload
// handler compares against, the page of 500 the exporter asks for. Nothing
// about consequences. Three floor runs showed why: a reviewer that reasons
// well from the diff alone writes "callers passing this to setTimeout now run
// 1000x too short" and "any caller comparing against a byte limit", and one
// said outright that the diff showed no callers and it was assuming some.
// Those are good comments and they are not what this corpus measures; the
// clean pair is what keeps them from being free.
//
// Two of the six are clean. Each is the control for a planted fixture beside
// it: the same files, the same caller, and a change to the same function that
// keeps its contract. A reviewer that flags "this might break callers" on
// every signature change has not looked at the callers, and the clean pair
// is what catches it.
func CallerFixtures() []Fixture {
	return []Fixture{
		goErrorIdentityChangedFixture(),
		goCleanWrappedSentinelFixture(),
		goReturnUnitsChangedFixture(),
		pythonPreconditionAddedFixture(),
		pythonCleanPreconditionSatisfiedFixture(),
		tsReturnUnitsChangedFixture(),
	}
}

// Caller reports whether a fixture is in the callers corpus.
func Caller(fixture string) bool {
	for _, f := range CallerFixtures() {
		if f.Name == fixture {
			return true
		}
	}
	return false
}

// callersGoMod is the module every Go fixture in this corpus lives in.
const callersGoMod = "module example.com/svc\n\ngo 1.22\n"

// goUsersHandler is the untouched caller shared by the sentinel pair. It
// branches on store.ErrNotFound with errors.Is to answer 404 rather than 500.
const goUsersHandler = `package web

import (
	"errors"
	"net/http"

	"example.com/svc/internal/store"
)

// Handler serves GET /users/{id}.
type Handler struct {
	Store *store.Users
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name, err := h.Store.Lookup(r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "no such user", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	_, _ = w.Write([]byte(name))
}
`

// goUsersStoreBase is the sentinel pair's starting state: Lookup returns the
// bare sentinel.
const goUsersStoreBase = `package store

import "errors"

// ErrNotFound is returned by Lookup when no user has the id.
var ErrNotFound = errors.New("user not found")

// Users is an in-memory user table.
type Users struct {
	names map[string]string
}

// New makes an empty table.
func New() *Users { return &Users{names: map[string]string{}} }

// Add stores a display name under id.
func (u *Users) Add(id, name string) { u.names[id] = name }

// Lookup returns the display name for id, or ErrNotFound.
func (u *Users) Lookup(id string) (string, error) {
	name, ok := u.names[id]
	if !ok {
		return "", ErrNotFound
	}
	return name, nil
}
`

// goErrorIdentityChangedFixture: an error loses its identity. The change puts
// the id in the message, which is a reasonable thing to want, and builds a
// fresh error to do it. Nothing in the diff is wrong on its face: the doc
// comment is updated to match, the sentinel is still declared, the package
// still compiles. The untouched handler in web/users.go compares with
// errors.Is and now answers 500 for a missing user.
func goErrorIdentityChangedFixture() Fixture {
	return Fixture{
		Name: "go-error-identity-changed",
		Base: map[string]string{
			"go.mod":                  callersGoMod,
			"internal/store/users.go": goUsersStoreBase,
			"web/users.go":            goUsersHandler,
		},
		Head: map[string]string{
			"go.mod": callersGoMod,
			"internal/store/users.go": `package store

import (
	"errors"
	"fmt"
)

// ErrNotFound is returned by Lookup when no user has the id.
var ErrNotFound = errors.New("user not found")

// Users is an in-memory user table.
type Users struct {
	names map[string]string
}

// New makes an empty table.
func New() *Users { return &Users{names: map[string]string{}} }

// Add stores a display name under id.
func (u *Users) Add(id, name string) { u.names[id] = name }

// Lookup returns the display name for id, or an error naming the id when
// there is none.
func (u *Users) Lookup(id string) (string, error) {
	name, ok := u.names[id]
	if !ok {
		return "", fmt.Errorf("user %q: no such user", id)
	}
	return name, nil
}
`,
			"web/users.go": goUsersHandler,
		},
		Defects: []Defect{{
			Path: "internal/store/users.go",
			Line: 27, // return "", fmt.Errorf("user %q: no such user", id)
			Keywords: []string{
				"web/users.go", "users.go", "servehttp", "http handler", "the handler",
			},
			Class:        config.ClassContract,
			WantSeverity: config.SeverityError,
			Why: "the untouched handler in web/users.go branches on Lookup's sentinel with errors.Is, and " +
				"the new error neither is the sentinel nor wraps it, so a missing user now gets 500 where " +
				"it answered 404",
		}},
	}
}

// goCleanWrappedSentinelFixture is the control for go-error-identity-changed:
// the same files, the same caller, the same motivation, and the change wraps
// the sentinel with %w so errors.Is still matches. Any finding here is noise,
// and "this may break callers that compare the error" is the finding a
// reviewer who did not read the caller — or did not read the %w — writes.
func goCleanWrappedSentinelFixture() Fixture {
	return Fixture{
		Name: "go-clean-wrapped-sentinel",
		Base: map[string]string{
			"go.mod":                  callersGoMod,
			"internal/store/users.go": goUsersStoreBase,
			"web/users.go":            goUsersHandler,
		},
		Head: map[string]string{
			"go.mod": callersGoMod,
			"internal/store/users.go": `package store

import (
	"errors"
	"fmt"
)

// ErrNotFound is returned by Lookup when no user has the id.
var ErrNotFound = errors.New("user not found")

// Users is an in-memory user table.
type Users struct {
	names map[string]string
}

// New makes an empty table.
func New() *Users { return &Users{names: map[string]string{}} }

// Add stores a display name under id.
func (u *Users) Add(id, name string) { u.names[id] = name }

// Lookup returns the display name for id, or an error wrapping ErrNotFound
// that names the id.
func (u *Users) Lookup(id string) (string, error) {
	name, ok := u.names[id]
	if !ok {
		return "", fmt.Errorf("user %q: %w", id, ErrNotFound)
	}
	return name, nil
}
`,
			"web/users.go": goUsersHandler,
		},
	}
}

// goReturnUnitsChangedFixture: a return value changes unit. Remaining used to
// answer in bytes and now answers in whole megabytes, because that is what the
// dashboard prints. The doc comment says so. The untouched upload handler in
// web/upload.go still compares it against len(body), so a tenant with ten
// megabytes left is refused any upload over ten bytes.
func goReturnUnitsChangedFixture() Fixture {
	upload := `package web

import (
	"io"
	"net/http"

	"example.com/svc/internal/quota"
)

// Upload serves PUT /blobs, charging each body against the tenant's quota.
type Upload struct {
	Quota *quota.Quota
	Store func(body []byte) error
}

func (h *Upload) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<20))
	if err != nil {
		http.Error(w, "body too large", http.StatusRequestEntityTooLarge)
		return
	}
	if h.Quota.Remaining() < int64(len(body)) {
		http.Error(w, "quota exceeded", http.StatusRequestEntityTooLarge)
		return
	}
	if err := h.Store(body); err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	h.Quota.Record(int64(len(body)))
	w.WriteHeader(http.StatusCreated)
}
`
	return Fixture{
		Name: "go-return-units-changed",
		Base: map[string]string{
			"go.mod": callersGoMod,
			"internal/quota/quota.go": `package quota

// Quota tracks how much of a byte allowance a tenant has used.
type Quota struct {
	limit int64
	used  int64
}

// New makes a quota of limit bytes.
func New(limit int64) *Quota { return &Quota{limit: limit} }

// Record charges n bytes against the quota.
func (q *Quota) Record(n int64) { q.used += n }

// Remaining returns how many bytes the tenant may still upload.
func (q *Quota) Remaining() int64 {
	return q.limit - q.used
}
`,
			"web/upload.go": upload,
		},
		Head: map[string]string{
			"go.mod": callersGoMod,
			"internal/quota/quota.go": `package quota

// Quota tracks how much of a byte allowance a tenant has used.
type Quota struct {
	limit int64
	used  int64
}

// New makes a quota of limit bytes.
func New(limit int64) *Quota { return &Quota{limit: limit} }

// Record charges n bytes against the quota.
func (q *Quota) Record(n int64) { q.used += n }

// Remaining returns how many whole megabytes the tenant may still upload,
// which is the figure the dashboard prints.
func (q *Quota) Remaining() int64 {
	return (q.limit - q.used) / (1 << 20)
}
`,
			"web/upload.go": upload,
		},
		Defects: []Defect{{
			Path: "internal/quota/quota.go",
			Line: 18, // return (q.limit - q.used) / (1 << 20)
			Keywords: []string{
				"upload.go", "len(body)", "against the body", "body length", "body size", "rejects every", "reject every", "refuses every",
			},
			Class:        config.ClassContract,
			WantSeverity: config.SeverityError,
			Why: "Remaining now counts megabytes and the untouched handler in web/upload.go still " +
				"compares it against len(body) in bytes, so almost every upload is refused as over quota",
		}},
	}
}

// pyOrdersExport is the untouched caller shared by the precondition pair. It
// pages through orders 500 at a time.
const pyOrdersExport = `import csv

from app.db import fetch_orders

BATCH = 500


def export_orders(conn, out):
    """export_orders writes every order to out as CSV, one page at a time."""
    writer = csv.writer(out)
    writer.writerow(["id", "total_cents", "placed_at"])
    offset = 0
    while True:
        rows = fetch_orders(conn, offset, BATCH)
        if not rows:
            return
        writer.writerows(rows)
        offset += len(rows)
`

// pyOrdersDBBase is the precondition pair's starting state.
const pyOrdersDBBase = `MAX_PAGE = 100


def fetch_orders(conn, offset, limit):
    """Return up to limit orders starting at offset, oldest first."""
    cur = conn.execute(
        "SELECT id, total_cents, placed_at FROM orders ORDER BY placed_at LIMIT ? OFFSET ?",
        (limit, offset),
    )
    return cur.fetchall()
`

// pythonPreconditionAddedFixture: a parameter gains a precondition. MAX_PAGE
// was declared and never enforced; the change enforces it, with a docstring
// to match. The untouched exporter in app/export.py asks for 500 at a time
// and now raises on its first page.
func pythonPreconditionAddedFixture() Fixture {
	return Fixture{
		Name: "python-precondition-added",
		Base: map[string]string{
			"app/__init__.py": "",
			"app/db.py":       pyOrdersDBBase,
			"app/export.py":   pyOrdersExport,
		},
		Head: map[string]string{
			"app/__init__.py": "",
			"app/db.py": `MAX_PAGE = 100


def fetch_orders(conn, offset, limit):
    """Return up to limit orders starting at offset, oldest first.

    Raises ValueError when limit exceeds MAX_PAGE, so a caller cannot pull
    the whole table into memory by accident.
    """
    if limit > MAX_PAGE:
        raise ValueError(f"limit {limit} exceeds MAX_PAGE ({MAX_PAGE})")
    cur = conn.execute(
        "SELECT id, total_cents, placed_at FROM orders ORDER BY placed_at LIMIT ? OFFSET ?",
        (limit, offset),
    )
    return cur.fetchall()
`,
			"app/export.py": pyOrdersExport,
		},
		Defects: []Defect{{
			Path: "app/db.py",
			Line: 11, // raise ValueError(f"limit {limit} exceeds MAX_PAGE ({MAX_PAGE})")
			Keywords: []string{
				"export.py", "export_orders", "first page", "every export", "breaks the export", "break the export", "passes 500", "page of 500", "batch of 500",
			},
			Class:        config.ClassContract,
			WantSeverity: config.SeverityError,
			Why: "the untouched exporter in app/export.py calls fetch_orders with a page of 500, so the " +
				"new precondition makes every export raise on its first page",
		}},
	}
}

// pythonCleanPreconditionSatisfiedFixture is the control for
// python-precondition-added: the same files, the same caller, and a
// precondition the caller already satisfies. A page of 500 is positive. Any
// finding here is noise.
func pythonCleanPreconditionSatisfiedFixture() Fixture {
	return Fixture{
		Name: "python-clean-precondition-satisfied",
		Base: map[string]string{
			"app/__init__.py": "",
			"app/db.py":       pyOrdersDBBase,
			"app/export.py":   pyOrdersExport,
		},
		Head: map[string]string{
			"app/__init__.py": "",
			"app/db.py": `MAX_PAGE = 100


def fetch_orders(conn, offset, limit):
    """Return up to limit orders starting at offset, oldest first.

    Raises ValueError when limit is not positive, which SQLite would
    otherwise read as "no limit" and return the whole table.
    """
    if limit <= 0:
        raise ValueError(f"limit must be positive, got {limit}")
    cur = conn.execute(
        "SELECT id, total_cents, placed_at FROM orders ORDER BY placed_at LIMIT ? OFFSET ?",
        (limit, offset),
    )
    return cur.fetchall()
`,
			"app/export.py": pyOrdersExport,
		},
	}
}

// tsReturnUnitsChangedFixture: a return value changes unit. parseDuration
// answered in milliseconds and now answers in seconds, because the config
// file's examples are written that way. The untouched fetchWithTimeout in
// src/http.ts hands the result to AbortSignal.timeout, which takes
// milliseconds, so every request is aborted a thousand times sooner.
func tsReturnUnitsChangedFixture() Fixture {
	http := `import { parseDuration } from "./duration";

/**
 * fetchWithTimeout fetches url, aborting after the configured duration
 * ("30s", "1500ms").
 */
export async function fetchWithTimeout(url: string, timeout: string): Promise<Response> {
  const signal = AbortSignal.timeout(parseDuration(timeout));
  return fetch(url, { signal });
}
`
	return Fixture{
		Name: "ts-return-units-changed",
		Base: map[string]string{
			"package.json": `{ "name": "client", "private": true, "type": "module" }` + "\n",
			"src/duration.ts": `/**
 * parseDuration reads "1500ms", "2s" or "3m" and returns the length in
 * milliseconds.
 */
export function parseDuration(text: string): number {
  const m = /^(\d+)(ms|s|m)$/.exec(text.trim());
  if (!m) throw new Error(` + "`bad duration: ${text}`" + `);
  const n = Number(m[1]);
  switch (m[2]) {
    case "ms":
      return n;
    case "s":
      return n * 1000;
    default:
      return n * 60_000;
  }
}
`,
			"src/http.ts": http,
		},
		Head: map[string]string{
			"package.json": `{ "name": "client", "private": true, "type": "module" }` + "\n",
			"src/duration.ts": `/**
 * parseDuration reads "1500ms", "2s" or "3m" and returns the length in
 * seconds, the unit the config file's own examples are written in.
 */
export function parseDuration(text: string): number {
  const m = /^(\d+)(ms|s|m)$/.exec(text.trim());
  if (!m) throw new Error(` + "`bad duration: ${text}`" + `);
  const n = Number(m[1]);
  switch (m[2]) {
    case "ms":
      return n / 1000;
    case "s":
      return n;
    default:
      return n * 60;
  }
}
`,
			"src/http.ts": http,
		},
		Defects: []Defect{{
			Path: "src/duration.ts",
			Line: 13, // return n;  (the "s" case, now unscaled)
			Keywords: []string{
				"http.ts", "fetchwithtimeout", "abortsignal", "abortsignal.timeout", "every request",
			},
			Class:        config.ClassContract,
			WantSeverity: config.SeverityError,
			Why: "parseDuration now returns seconds and the untouched fetchWithTimeout in src/http.ts " +
				"hands the result to AbortSignal.timeout, which takes milliseconds, so every request is " +
				"aborted a thousand times sooner than configured",
		}},
	}
}
