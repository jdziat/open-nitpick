package evals

import "github.com/jdziat/open-nitpick/internal/config"

// SlopFixtures is the slop corpus: five rules of the slop class, each as a
// pair. The planted fixture adds code that matches the rule as written; its
// control adds the lookalike the rule excludes, so a reviewer that fires on
// the shape rather than the rule is caught. Keywords name details that exist
// only in the fixture, never the rule's own words, which the prompt carries.
// Rule 15 of docs/measurement.md applies.
func SlopFixtures() []Fixture {
	return []Fixture{
		slopRestatingComments(), slopCleanWhyComments(),
		slopSwallowedException(), slopCleanLoggedAndReraised(),
		slopChatProse(), slopCleanDocComment(),
		slopTypeExcludedCheck(), slopCleanRealGuard(),
		slopTestAssertsNothing(), slopCleanTestAsserts(),
	}
}

// Slop reports whether a fixture is in the slop corpus.
func Slop(fixture string) bool {
	for _, f := range SlopFixtures() {
		if f.Name == fixture {
			return true
		}
	}
	return false
}

const slopGoMod = "module example.com/app\n\ngo 1.22\n"

const slopInvoiceBase = `package billing

// Invoice is one bill for one tenant.
type Invoice struct {
	Tenant string
	Cents  int64
	Paid   bool
}
`

func slopRestatingComments() Fixture {
	return Fixture{
		Name: "go-slop-restating-comments",
		Base: map[string]string{"go.mod": slopGoMod, "billing/invoice.go": slopInvoiceBase},
		Head: map[string]string{"go.mod": slopGoMod, "billing/invoice.go": slopInvoiceBase + `
// Total returns the total.
func Total(invoices []Invoice) int64 {
	// Initialize the total to zero.
	var total int64
	// Loop over the invoices.
	for _, inv := range invoices {
		// Add the cents to the total.
		total += inv.Cents
	}
	// Return the total.
	return total
}
`},
		Defects: []Defect{{
			Path: "billing/invoice.go", Line: 13, // // Initialize the total to zero.
			Keywords:     []string{"Initialize the total", "Loop over the invoices", "Add the cents", "Return the total", "every comment in Total", "each comment"},
			Class:        config.ClassSlop,
			WantSeverity: config.SeverityNit,
			Why:          "every comment in Total restates the line beneath it",
		}},
	}
}

func slopCleanWhyComments() Fixture {
	return Fixture{
		Name: "go-clean-why-comments",
		Base: map[string]string{"go.mod": slopGoMod, "billing/invoice.go": slopInvoiceBase},
		Head: map[string]string{"go.mod": slopGoMod, "billing/invoice.go": slopInvoiceBase + `
// Total sums the invoices in cents. It ignores Paid on purpose: the dashboard
// shows what was billed, and the ledger, not this function, tracks payment.
func Total(invoices []Invoice) int64 {
	var total int64
	for _, inv := range invoices {
		// Cents is signed because a credit note is a negative invoice.
		total += inv.Cents
	}
	return total
}
`},
	}
}

const slopOrdersBase = `import logging

log = logging.getLogger(__name__)


class GatewayError(Exception):
    pass


def charge(gateway, order):
    return gateway.charge(order.card, order.cents)
`

func slopSwallowedException() Fixture {
	return Fixture{
		Name: "python-slop-swallowed-exception",
		Base: map[string]string{"app/orders.py": slopOrdersBase},
		Head: map[string]string{"app/orders.py": slopOrdersBase + `

def charge_all(gateway, orders):
    receipts = []
    for order in orders:
        try:
            receipts.append(charge(gateway, order))
        except Exception:
            pass
    return receipts
`},
		Defects: []Defect{{
			Path: "app/orders.py", Line: 20, // except Exception:
			Keywords:     []string{"charge_all", "bare except", "failed charge", "receipts", "the batch continues", "order is skipped", "no receipt"},
			Class:        config.ClassSlop,
			WantSeverity: config.SeverityWarning,
			Why:          "a failed charge is dropped without a trace and the loop carries on",
		}},
	}
}

func slopCleanLoggedAndReraised() Fixture {
	return Fixture{
		Name: "python-clean-logged-and-reraised",
		Base: map[string]string{"app/orders.py": slopOrdersBase},
		Head: map[string]string{"app/orders.py": slopOrdersBase + `

def charge_all(gateway, orders):
    receipts = []
    for order in orders:
        try:
            receipts.append(charge(gateway, order))
        except GatewayError:
            log.exception("charge failed for order %s; stopping the batch", order.id)
            raise
    return receipts
`},
	}
}

const slopDurationBase = `export function parseDuration(text: string): number {
  const m = /^(\d+)(ms|s|m)$/.exec(text.trim());
  if (!m) throw new Error(` + "`bad duration: ${text}`" + `);
  const n = Number(m[1]);
  return m[2] === "ms" ? n : m[2] === "s" ? n * 1000 : n * 60_000;
}
`

func slopChatProse() Fixture {
	return Fixture{
		Name: "ts-slop-chat-prose",
		Base: map[string]string{"package.json": `{ "name": "client", "private": true }` + "\n", "src/duration.ts": slopDurationBase},
		Head: map[string]string{"package.json": `{ "name": "client", "private": true }` + "\n", "src/duration.ts": slopDurationBase + `
// Sure! Here's a helper function that formats a duration in milliseconds.
// Note that this function will handle all the edge cases for you.
// I hope this helps!
export function formatDuration(ms: number): string {
  if (ms < 1000) return ` + "`${ms}ms`" + `;
  if (ms < 60_000) return ` + "`${ms / 1000}s`" + `;
  return ` + "`${ms / 60_000}m`" + `;
}
`},
		Defects: []Defect{{
			Path: "src/duration.ts", Line: 8, // // Sure! Here's a helper function
			Keywords:     []string{"Here's a helper", "handle all the edge cases", "Note that this function", "formatDuration", "pasted"},
			Class:        config.ClassSlop,
			WantSeverity: config.SeverityNit,
			Why:          "the comment is a chat reply pasted into the file",
		}},
	}
}

func slopCleanDocComment() Fixture {
	return Fixture{
		Name: "ts-clean-doc-comment",
		Base: map[string]string{"package.json": `{ "name": "client", "private": true }` + "\n", "src/duration.ts": slopDurationBase},
		Head: map[string]string{"package.json": `{ "name": "client", "private": true }` + "\n", "src/duration.ts": slopDurationBase + `
/**
 * formatDuration renders milliseconds in the largest unit that keeps a whole
 * number below 60, which is what the settings page shows. You get "1500ms",
 * not "1.5s": the page never prints fractions.
 */
export function formatDuration(ms: number): string {
  if (ms < 1000 || ms % 1000 !== 0) return ` + "`${ms}ms`" + `;
  if (ms < 60_000 || ms % 60_000 !== 0) return ` + "`${ms / 1000}s`" + `;
  return ` + "`${ms / 60_000}m`" + `;
}
`},
	}
}

const slopPageBase = `package page

// Size is a page size in rows, always positive once built.
type Size struct{ rows int }

// NewSize clamps rows to [1, 500].
func NewSize(rows int) Size {
	if rows < 1 {
		rows = 1
	}
	if rows > 500 {
		rows = 500
	}
	return Size{rows: rows}
}
`

func slopTypeExcludedCheck() Fixture {
	return Fixture{
		Name: "go-slop-type-excluded-check",
		Base: map[string]string{"go.mod": slopGoMod, "page/size.go": slopPageBase},
		Head: map[string]string{"go.mod": slopGoMod, "page/size.go": slopPageBase + `
// Offset returns the first row of page n.
func (s Size) Offset(n int) int {
	if s.rows <= 0 || s.rows > 500 {
		return 0
	}
	if n != 0 || n == 0 {
		return (n - 1) * s.rows
	}
	return 0
}
`},
		Defects: []Defect{{
			Path: "page/size.go", Line: 22, // if n != 0 || n == 0 {
			Keywords:     []string{"n != 0 || n == 0", "n == 0", "already clamps", "NewSize", "rows check", "s.rows <= 0"},
			Class:        config.ClassSlop,
			WantSeverity: config.SeverityNit,
			Why:          "the condition is always true, and the rows check guards a value NewSize already clamps",
		}},
	}
}

func slopCleanRealGuard() Fixture {
	return Fixture{
		Name: "go-clean-real-guard",
		Base: map[string]string{"go.mod": slopGoMod, "page/size.go": slopPageBase},
		Head: map[string]string{"go.mod": slopGoMod, "page/size.go": slopPageBase + `
// Offset returns the first row of page n, counting from 1. A page before
// the first is the first: callers pass the number from a query string.
func (s Size) Offset(n int) int {
	if n < 1 {
		return 0
	}
	return (n - 1) * s.rows
}
`},
	}
}

const slopSlugBase = `def slug(s):
    return "-".join(s.lower().split())
`

func slopTestAssertsNothing() Fixture {
	return Fixture{
		Name: "python-slop-test-asserts-nothing",
		Base: map[string]string{"app/slug.py": slopSlugBase, "app/__init__.py": ""},
		Head: map[string]string{"app/slug.py": slopSlugBase, "app/__init__.py": "", "tests/test_slug.py": `from app.slug import slug


def test_slug():
    slug("Hello World")
    assert True


def test_slug_runs_on_empty():
    result = slug("")
    result
`},
		Defects: []Defect{{
			Path: "tests/test_slug.py", Line: 6, // assert True
			Keywords:     []string{"test_slug", "test_slug_runs_on_empty", "slug(\"Hello World\")", "never checks", "unused result", "whatever slug returns"},
			Class:        config.ClassSlop,
			WantSeverity: config.SeverityWarning,
			Why:          "neither test checks a result; both pass whatever slug returns",
		}},
	}
}

func slopCleanTestAsserts() Fixture {
	return Fixture{
		Name: "python-clean-test-asserts",
		Base: map[string]string{"app/slug.py": slopSlugBase, "app/__init__.py": ""},
		Head: map[string]string{"app/slug.py": slopSlugBase, "app/__init__.py": "", "tests/test_slug.py": `from app.slug import slug


def test_slug_joins_words_with_hyphens():
    assert slug("Hello World") == "hello-world"


def test_slug_of_empty_is_empty():
    assert slug("") == ""
`},
	}
}
