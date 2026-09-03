package evals

import "github.com/jdziat/open-nitpick/internal/config"

// The multi-file corpus: changes whose defect is only visible by reading a file
// the change does not touch.
//
// Every fixture in the other two corpora is judged from the diff and the files
// it names. That is also what a hosted reviewer with the whole repository
// indexed is supposed to be better at, and nothing here had ever measured it:
// the one cross-file plant in the tuning corpus (ts-unbounded-memo-key) puts
// the contract in a file the change ADDS, so the reviewer is shown it. Here the
// contract sits in a file that is byte-identical in Base and Head. A reviewer
// that reads only the diff cannot see it and must either guess or stay quiet;
// a reviewer that follows the import can read the sentence that makes the
// change wrong.
//
// So this corpus answers two questions the others cannot:
//
//   - whether review.related_context — attaching the definitions a changed
//     line uses from files the change does not touch — finds defects a
//     diff-only review misses, and what it costs in precision on the two clean
//     fixtures, which honour their helpers' contracts exactly;
//   - how this reviewer compares with hosted ones that index the repository,
//     on the changes those products are built for.
//
// It is a THIRD corpus rather than more held-out fixtures because it will be
// re-run: the feature it measures is new and will be tuned, and a corpus that
// is spent once cannot answer "did that change help?" twice. It is not a
// tuning corpus either — the prompt is not tuned on it — but nothing here
// should be read as a generalization claim.
//
// It is also outside AllFixtures, and therefore outside the ground-truth suite
// that sweeps every plant's keywords against every other fixture's recorded
// prose. TestMultiFileCorpusIsWellFormed checks what can be checked without
// those registries: that every plant is on an added line, that its Why is
// credited by its own keywords, that no keyword is a token of the change
// itself, and that the contract really is in a file the change does not
// touch. See EveryFixture for what that leaves unchecked.
//
// Every helper's contract is written the way a maintainer writes one: a doc
// comment on the definition, in the language's own convention, stating what a
// caller must do. None is hidden in a README or a test. The plants are
// realistic in the sense that matters — each is a change a competent engineer
// makes when they have not read the callee — and unrealistic in the sense that
// every corpus is: the repository is ten files, not ten thousand.
func MultiFileFixtures() []Fixture {
	fixtures := []Fixture{
		goQueryWithoutDeadlineFixture(),
		goEmptySlugPathFixture(),
		goEmptyFilterDeletesAllFixture(),
		tsMoneyUnitsFixture(),
		tsClientPerRequestFixture(),
		tsCleanContractFixture(),
		pythonRetryNonIdempotentFixture(),
		pythonExpiredTokenAcceptedFixture(),
		pythonSecretToAuditLogFixture(),
		pythonCleanContractFixture(),
	}
	return append(fixtures, deepMultiFileFixtures()...)
}

// goQueryWithoutDeadlineFixture: the store's Query documents that ctx must
// carry a deadline because the driver has no read timeout; the change passes
// context.Background() from a request handler.
func goQueryWithoutDeadlineFixture() Fixture {
	store := `package store

import "context"

// Row is one result row, already read from the wire: the result set is
// fully materialised before Query returns, so there is nothing to close.
type Row struct {
	Month string
	Total int64
}

// Store is a connection to the primary.
type Store struct{}

// Query runs q against the primary and returns every row it matched.
//
// ctx must carry a deadline. The driver has no read timeout of its own, so a
// call made with a context that never expires blocks for as long as the
// connection stays half-open after the primary drops it — in practice
// indefinitely — and the goroutine that made the call is never freed.
// Handlers should pass the request's own context or wrap it with a timeout.
func (s *Store) Query(ctx context.Context, q string, args ...any) ([]Row, error) {
	return nil, nil
}
`
	return Fixture{
		Name: "go-query-without-deadline",
		Base: map[string]string{
			"go.mod":                  "module example.com/reports\n\ngo 1.22\n",
			"internal/store/store.go": store,
			"api/report.go": `package api

import (
	"net/http"

	"example.com/reports/internal/store"
)

// Handler serves report endpoints.
type Handler struct {
	Store *store.Store
}

// Ping answers a liveness probe.
func (h *Handler) Ping(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}
`,
		},
		Head: map[string]string{
			"go.mod":                  "module example.com/reports\n\ngo 1.22\n",
			"internal/store/store.go": store,
			"api/report.go": `package api

import (
	"context"
	"encoding/json"
	"net/http"

	"example.com/reports/internal/store"
)

// Handler serves report endpoints.
type Handler struct {
	Store *store.Store
}

// Ping answers a liveness probe.
func (h *Handler) Ping(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

// Monthly renders the monthly totals for the account in the query string.
func (h *Handler) Monthly(w http.ResponseWriter, r *http.Request) {
	account := r.URL.Query().Get("account")
	if account == "" {
		http.Error(w, "account is required", http.StatusBadRequest)
		return
	}

	rows, err := h.Store.Query(context.Background(),
		"SELECT month, total FROM monthly_totals WHERE account = $1", account)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(rows); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
`,
		},
		Defects: []Defect{{
			Path: "api/report.go",
			Line: 28, // rows, err := h.Store.Query(context.Background(),
			Keywords: []string{
				"deadline", "no timeout", "without a timeout", "never expires", "never times out",
				"blocks forever", "block forever", "blocks indefinitely", "indefinitely",
				"never returns", "never freed", "goroutine leak", "leaked goroutine",
				"r.context", "request context", "request's context",
			},
			Class:        config.ClassResource,
			WantSeverity: config.SeverityWarning,
			SeverityNote: "warning under \"a genuine hazard under plausible conditions\", with the other " +
				"hazards in this class: every request on a healthy connection is answered correctly, " +
				"and the goroutine is lost only when the primary drops a connection mid-query, which " +
				"is plausible on any bad afternoon and absent on a good one.",
			Why: "Query's contract requires a deadline and the handler passes a context that never expires, so a dropped connection blocks the request indefinitely and its goroutine is never freed",
		}},
	}
}

// goEmptySlugPathFixture: Slug documents that it returns "" for a title with
// no letters or digits and that callers must check before using it as a path
// segment; the change builds a post URL from it unchecked.
func goEmptySlugPathFixture() Fixture {
	text := `package text

import (
	"strings"
	"unicode"
)

// Slug reduces title to lower-case letters, digits and hyphens.
//
// It returns "" for a title containing no letters or digits at all — "???",
// "…", a string of emoji — and callers using the result as a path segment
// must check for that first: a post whose slug is empty is published at the
// bare collection URL, and every such post lands on the same page.
func Slug(title string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(title) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = false
			b.WriteRune(r)
		default:
			dash = true
		}
	}
	return b.String()
}
`
	return Fixture{
		Name: "go-empty-slug-path",
		Base: map[string]string{
			"go.mod":       "module example.com/blog\n\ngo 1.22\n",
			"text/slug.go": text,
			"posts/publish.go": `package posts

// Post is a published article.
type Post struct {
	ID    int
	Title string
	URL   string
}

// Publish records a post and returns it with its public URL.
func Publish(id int, title string) Post {
	return Post{ID: id, Title: title, URL: "/posts/" + itoa(id)}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var out []byte
	for n > 0 {
		out = append([]byte{byte('0' + n%10)}, out...)
		n /= 10
	}
	return string(out)
}
`,
		},
		Head: map[string]string{
			"go.mod":       "module example.com/blog\n\ngo 1.22\n",
			"text/slug.go": text,
			"posts/publish.go": `package posts

import "example.com/blog/text"

// Post is a published article.
type Post struct {
	ID    int
	Title string
	URL   string
}

// Publish records a post and returns it with its public URL.
//
// URLs are now readable: /posts/how-we-ship rather than /posts/1932. The id
// is kept on the Post for lookups.
func Publish(id int, title string) Post {
	return Post{ID: id, Title: title, URL: "/posts/" + text.Slug(title)}
}
`,
		},
		Defects: []Defect{{
			Path: "posts/publish.go",
			Line: 17, // return Post{ID: id, Title: title, URL: "/posts/" + text.Slug(title)}
			Keywords: []string{
				"empty slug", "empty string", "returns \"\"", "returns an empty",
				"no letters", "same url", "same path", "collide", "collision", "collisions",
				"bare collection", "/posts/ with nothing", "duplicate url", "not unique", "uniqueness",
			},
			Class:        config.ClassCorrectness,
			WantSeverity: config.SeverityError,
			Why:          "Slug returns an empty string for a title with no letters or digits, and the URL is built from it unchecked, so such posts collide at /posts/",
		}},
	}
}

// goEmptyFilterDeletesAllFixture: DeleteWhere documents that an empty Filter
// matches every row; the change builds a Filter from optional query
// parameters and passes it straight through.
func goEmptyFilterDeletesAllFixture() Fixture {
	store := `package store

import (
	"context"
	"time"
)

// Filter selects uploads. A zero field is not a constraint.
type Filter struct {
	Owner  string
	Before time.Time
}

// Store holds uploads.
type Store struct{}

// DeleteWhere removes every upload matching f and returns how many.
//
// An EMPTY Filter matches every row: with no Owner and a zero Before there is
// no WHERE clause at all. Callers that build a Filter from optional input must
// refuse an empty one themselves — this function cannot tell "delete
// everything" from "the caller forgot to constrain it", and it does the first.
func (s *Store) DeleteWhere(ctx context.Context, f Filter) (int, error) {
	return 0, nil
}
`
	return Fixture{
		Name: "go-empty-filter-deletes-all",
		Base: map[string]string{
			"go.mod":                  "module example.com/uploads\n\ngo 1.22\n",
			"internal/store/store.go": store,
			"api/admin.go": `package api

import (
	"net/http"

	"example.com/uploads/internal/store"
)

// Admin serves operator endpoints.
type Admin struct {
	Store *store.Store
}

// Health answers a liveness probe.
func (a *Admin) Health(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}
`,
		},
		Head: map[string]string{
			"go.mod":                  "module example.com/uploads\n\ngo 1.22\n",
			"internal/store/store.go": store,
			"api/admin.go": `package api

import (
	"fmt"
	"net/http"
	"time"

	"example.com/uploads/internal/store"
)

// Admin serves operator endpoints.
type Admin struct {
	Store *store.Store
}

// Health answers a liveness probe.
func (a *Admin) Health(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

// Cleanup deletes uploads matching the optional owner and before parameters.
//
//	DELETE /admin/uploads?owner=alice&before=2024-01-01
func (a *Admin) Cleanup(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	var before time.Time
	if raw := q.Get("before"); raw != "" {
		t, err := time.Parse("2006-01-02", raw)
		if err != nil {
			http.Error(w, "before: expected YYYY-MM-DD", http.StatusBadRequest)
			return
		}
		before = t
	}

	n, err := a.Store.DeleteWhere(r.Context(), store.Filter{Owner: q.Get("owner"), Before: before})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	fmt.Fprintf(w, "deleted %d upload(s)\n", n)
}
`,
		},
		Defects: []Defect{{
			Path: "api/admin.go",
			Line: 37, // n, err := a.Store.DeleteWhere(r.Context(), store.Filter{...})
			Keywords: []string{
				"every row", "all rows", "every upload", "all uploads", "empty filter", "zero filter",
				"entire table", "whole table", "everything", "wipes", "wipes the",
				"deletes all", "delete all", "unfiltered", "no constraint", "unconstrained",
				"both parameters", "neither parameter", "no parameters", "without parameters",
			},
			Class:        config.ClassDataLoss,
			WantSeverity: config.SeverityCritical,
			Why:          "DeleteWhere treats an empty Filter as matching every row, and the handler passes one straight from two optional parameters, so a request with neither deletes every upload",
		}},
	}
}

// tsMoneyUnitsFixture: toCents documents that its argument is in dollars; the
// change passes a value that is already in cents.
func tsMoneyUnitsFixture() Fixture {
	money := `/**
 * toCents converts a DOLLAR amount to an integer number of cents.
 *
 * The argument is in dollars — 19.99, not 1999 — and is multiplied by 100 and
 * rounded, because binary floating point cannot hold most cent values exactly.
 * Values that are already in cents must not be passed through this function.
 */
export function toCents(dollars: number): number {
  return Math.round(dollars * 100);
}

/** formatCents renders an integer cent amount as "$12.34". */
export function formatCents(cents: number): string {
  return "$" + (cents / 100).toFixed(2);
}
`
	return Fixture{
		Name: "ts-money-units",
		Base: map[string]string{
			"package.json": `{ "name": "checkout", "private": true, "type": "module" }` + "\n",
			"src/money.ts": money,
			"src/checkout.ts": `import { formatCents } from "./money";

/** LineItem is one row of a cart. priceCents is an integer number of cents. */
export interface LineItem {
  sku: string;
  priceCents: number;
  quantity: number;
}

/** describe renders a cart line for the receipt. */
export function describe(item: LineItem): string {
  return item.sku + " x" + item.quantity + " " + formatCents(item.priceCents);
}
`,
		},
		Head: map[string]string{
			"package.json": `{ "name": "checkout", "private": true, "type": "module" }` + "\n",
			"src/money.ts": money,
			"src/checkout.ts": `import { formatCents, toCents } from "./money";

/** LineItem is one row of a cart. priceCents is an integer number of cents. */
export interface LineItem {
  sku: string;
  priceCents: number;
  quantity: number;
}

/** describe renders a cart line for the receipt. */
export function describe(item: LineItem): string {
  return item.sku + " x" + item.quantity + " " + formatCents(item.priceCents);
}

/** total is the amount to charge for the cart, in cents. */
export function total(items: LineItem[]): number {
  let sum = 0;
  for (const item of items) {
    sum += toCents(item.priceCents) * item.quantity;
  }
  return sum;
}
`,
		},
		Defects: []Defect{{
			Path: "src/checkout.ts",
			Line: 19, // sum += toCents(item.priceCents) * item.quantity;
			Keywords: []string{
				"already in cents", "already cents", "already an integer", "in dollars", "expects dollars",
				"dollar amount", "takes dollars", "100 times", "hundred times", "hundredfold",
				"multiplied by 100", "multiplies by 100", "times 100", "double conversion", "overcharg",
				"wrong unit", "unit mismatch",
			},
			Class:        config.ClassCorrectness,
			WantSeverity: config.SeverityError,
			Why:          "toCents takes a dollar amount and multiplies by 100, and the change hands it a value already in cents, so every cart is charged a hundred times its price",
		}},
	}
}

// tsClientPerRequestFixture: createClient documents that it opens a connection
// pool and must be created once per process; the change creates one per
// request. The same hazard as csharp-client-per-request, with the contract in
// a file the change does not touch.
func tsClientPerRequestFixture() Fixture {
	client := `/** Client talks to the pricing service. */
export interface Client {
  price(sku: string): Promise<number>;
  close(): Promise<void>;
}

/**
 * createClient opens a pool of eight keep-alive connections to the pricing
 * service and returns a Client over them.
 *
 * It is expensive and long-lived: create ONE per process at startup and share
 * it. A client per call opens eight sockets per call and closes none of them
 * until the process exits, which exhausts the file descriptor table under
 * load.
 */
export function createClient(baseUrl: string): Client {
  return {
    async price(sku: string): Promise<number> {
      return 0;
    },
    async close(): Promise<void> {},
  };
}
`
	return Fixture{
		Name: "ts-client-per-request",
		Base: map[string]string{
			"package.json":  `{ "name": "quotes", "private": true, "type": "module" }` + "\n",
			"src/client.ts": client,
			"src/quote.ts": `type Request = { params: { sku: string } };
type Response = { json: (body: unknown) => void };

/** health answers a liveness probe. */
export async function health(_req: Request, res: Response): Promise<void> {
  res.json({ ok: true });
}
`,
		},
		Head: map[string]string{
			"package.json":  `{ "name": "quotes", "private": true, "type": "module" }` + "\n",
			"src/client.ts": client,
			"src/quote.ts": `import { createClient } from "./client";

type Request = { params: { sku: string } };
type Response = { json: (body: unknown) => void };

const PRICING_URL = process.env.PRICING_URL ?? "http://pricing:8080";

/** health answers a liveness probe. */
export async function health(_req: Request, res: Response): Promise<void> {
  res.json({ ok: true });
}

/** quote answers GET /quote/:sku with the current price. */
export async function quote(req: Request, res: Response): Promise<void> {
  const client = createClient(PRICING_URL);
  const price = await client.price(req.params.sku);
  res.json({ sku: req.params.sku, price });
}
`,
		},
		Defects: []Defect{{
			Path: "src/quote.ts",
			Line: 15, // const client = createClient(PRICING_URL);
			Keywords: []string{
				"per request", "per call", "every request", "each request", "on every call", "each call",
				"once per process", "at startup", "module scope", "module level", "shared instance",
				"singleton", "reuse", "reused", "never closed", "not closed", "without closing",
				"sockets", "file descriptor", "descriptors", "exhausts", "exhaustion", "connection pool", "pool per",
			},
			Class:        config.ClassResource,
			WantSeverity: config.SeverityWarning,
			SeverityNote: "warning under \"a genuine hazard under plausible conditions\", the same level as " +
				"csharp-client-per-request, whose defect this is in another language: every quote is " +
				"answered correctly and the descriptors run out only under sustained load.",
			Why: "createClient opens a pool that must be created once per process, and the handler creates one per request without closing it, so descriptors are exhausted under load",
		}},
	}
}

// tsCleanContractFixture honours a helper's contract exactly: parseAmount
// documents that it throws on malformed input, and the change catches that
// and answers 400. Any finding here is noise, and a reviewer shown the
// helper's contract has been shown the reason the change is right.
func tsCleanContractFixture() Fixture {
	parse := `/**
 * parseAmount reads a decimal string such as "12.50" as an integer number of
 * cents.
 *
 * It throws a RangeError on anything that is not a non-negative decimal with
 * at most two fractional digits. Callers handling user input must catch it.
 */
export function parseAmount(s: string): number {
  const m = /^(\d+)(?:\.(\d{1,2}))?$/.exec(s.trim());
  if (!m) {
    throw new RangeError("not an amount: " + s);
  }
  const whole = Number(m[1]);
  const frac = m[2] ? Number(m[2].padEnd(2, "0")) : 0;
  return whole * 100 + frac;
}
`
	return Fixture{
		Name: "ts-clean-contract",
		Base: map[string]string{
			"package.json": `{ "name": "tips", "private": true, "type": "module" }` + "\n",
			"src/parse.ts": parse,
			"src/tip.ts": `type Request = { body: { amount?: string } };
type Response = { status: (code: number) => Response; json: (body: unknown) => void };

/** health answers a liveness probe. */
export function health(_req: Request, res: Response): void {
  res.json({ ok: true });
}
`,
		},
		Head: map[string]string{
			"package.json": `{ "name": "tips", "private": true, "type": "module" }` + "\n",
			"src/parse.ts": parse,
			"src/tip.ts": `import { parseAmount } from "./parse";

type Request = { body: { amount?: string } };
type Response = { status: (code: number) => Response; json: (body: unknown) => void };

/** health answers a liveness probe. */
export function health(_req: Request, res: Response): void {
  res.json({ ok: true });
}

/**
 * tip validates the tip amount in the request body and answers with it in
 * cents. Storing it is the payment step's job, which runs after this check.
 */
export function tip(req: Request, res: Response): void {
  let cents: number;
  try {
    cents = parseAmount(req.body.amount ?? "");
  } catch (err) {
    if (err instanceof RangeError) {
      res.status(400).json({ error: "amount must be a decimal such as 12.50" });
      return;
    }
    throw err;
  }
  res.status(201).json({ cents });
}
`,
		},
	}
}

// pythonRetryNonIdempotentFixture: with_retry documents that the wrapped call
// must be idempotent because a timed-out attempt may have succeeded; the
// change wraps a card charge in it.
func pythonRetryNonIdempotentFixture() Fixture {
	retry := `import time


def with_retry(fn, attempts=3, delay=0.5):
    """Call fn until it returns without raising, at most attempts times.

    fn MUST be idempotent. A timeout is retried like any other error, and a
    timed-out request may already have been processed by the other side —
    so anything fn does that is not safe to do twice (creating an order,
    charging a card, sending a message) will happen more than once.
    """
    last = None
    for i in range(attempts):
        try:
            return fn()
        except Exception as exc:  # noqa: BLE001 - deliberately broad
            last = exc
            if i + 1 < attempts:
                time.sleep(delay * (i + 1))
    raise last
`
	return Fixture{
		Name: "python-retry-nonidempotent",
		Base: map[string]string{
			"app/__init__.py": "",
			"app/retry.py":    retry,
			"app/gateway.py": `class Gateway:
    """Gateway is the card processor's HTTP client."""

    def charge(self, card_token, amount_cents):
        """charge captures amount_cents from the card and returns a receipt id."""
        raise NotImplementedError

    def balance(self, account):
        """balance reads an account's available balance."""
        raise NotImplementedError
`,
			"app/billing.py": `from app.gateway import Gateway


def settle(gateway: Gateway, card_token: str, amount_cents: int) -> str:
    """settle charges the card and returns the receipt id."""
    return gateway.charge(card_token, amount_cents)
`,
		},
		Head: map[string]string{
			"app/__init__.py": "",
			"app/retry.py":    retry,
			"app/gateway.py": `class Gateway:
    """Gateway is the card processor's HTTP client."""

    def charge(self, card_token, amount_cents):
        """charge captures amount_cents from the card and returns a receipt id."""
        raise NotImplementedError

    def balance(self, account):
        """balance reads an account's available balance."""
        raise NotImplementedError
`,
			"app/billing.py": `from app.gateway import Gateway
from app.retry import with_retry


def settle(gateway: Gateway, card_token: str, amount_cents: int) -> str:
    """settle charges the card and returns the receipt id.

    The processor times out a few times a day; rather than fail the order,
    try again.
    """
    return with_retry(lambda: gateway.charge(card_token, amount_cents))
`,
		},
		Defects: []Defect{{
			Path: "app/billing.py",
			Line: 11, // return with_retry(lambda: gateway.charge(card_token, amount_cents))
			Keywords: []string{
				"idempoten", "not idempotent", "non-idempotent", "charged twice", "charge twice", "double charge",
				"double-charge", "charged again", "duplicate charge", "more than once", "multiple times",
				"already succeeded", "already been processed", "already processed", "may have succeeded",
			},
			Class:        config.ClassCorrectness,
			WantSeverity: config.SeverityError,
			Why:          "with_retry requires an idempotent call and a card charge is not one, so a timed-out charge that succeeded server-side is charged again",
		}},
	}
}

// pythonExpiredTokenAcceptedFixture: verify documents that it checks the
// signature only and that callers must check expiry; the change trusts the
// claims after a None check.
func pythonExpiredTokenAcceptedFixture() Fixture {
	tokens := `import hmac
import json
import time


SECRET = b"rotate-me"


def verify(token: str):
    """Check the token's signature and return its claims, or None if forged.

    This checks the SIGNATURE ONLY. It deliberately returns the claims of an
    expired token — the refresh endpoint needs them — so every other caller
    must call is_expired(claims) before trusting the result.
    """
    try:
        body, sig = token.rsplit(".", 1)
    except ValueError:
        return None
    want = hmac.new(SECRET, body.encode(), "sha256").hexdigest()
    if not hmac.compare_digest(want, sig):
        return None
    return json.loads(body)


def is_expired(claims, now=None) -> bool:
    """is_expired reports whether the claims' exp is in the past."""
    now = time.time() if now is None else now
    return claims.get("exp", 0) <= now
`
	return Fixture{
		Name: "python-expired-token-accepted",
		Base: map[string]string{
			"app/__init__.py": "",
			"app/tokens.py":   tokens,
			"app/api.py": `def health():
    return {"ok": True}, 200
`,
		},
		Head: map[string]string{
			"app/__init__.py": "",
			"app/tokens.py":   tokens,
			"app/api.py": `from app.tokens import verify


def health():
    return {"ok": True}, 200


def current_user(headers):
    """current_user resolves the caller from the Authorization header.

    Returns (user_id, None) on success and (None, error_response) otherwise.
    """
    auth = headers.get("Authorization", "")
    if not auth.startswith("Bearer "):
        return None, ({"error": "missing token"}, 401)
    claims = verify(auth[len("Bearer "):])
    if claims is None:
        return None, ({"error": "invalid token"}, 401)
    return claims["sub"], None
`,
		},
		Defects: []Defect{{
			Path: "app/api.py",
			Line: 19, // return claims["sub"], None
			Keywords: []string{
				"expired", "expiry", "expiration", "is_expired", "exp claim", "signature only",
				"only the signature", "never expire", "never expires", "indefinitely", "revoked",
				"stale token", "old token",
			},
			Class:        config.ClassSecurity,
			WantSeverity: config.SeverityError,
			SeverityNote: "error, below the security class's criticals, because what is gained is access " +
				"the holder once had: an expired token was issued to its bearer, so calibration rule 1 " +
				"— \"if exploiting it requires an attacker who already has the access it would grant, " +
				"it is not critical\" — applies, and \"a real bug that produces incorrect behavior on a " +
				"reachable path\" is exactly what accepting a token past its exp is.",
			Why: "verify checks the signature only and documents that callers must check expiry, and the change trusts the claims after a None check, so an expired token is accepted indefinitely",
		}},
	}
}

// pythonSecretToAuditLogFixture: audit documents that its fields are shipped
// verbatim to an external SIEM and must never carry credentials; the change
// logs the API key it just minted.
func pythonSecretToAuditLogFixture() Fixture {
	audit := `import json
import sys


def audit(event: str, **fields) -> None:
    """Record a security-relevant event.

    Every field is serialised VERBATIM and shipped off-host to the SIEM, where
    it is retained for seven years and readable by the whole security
    organisation. Never pass a credential, token or key as a field — pass an
    identifier for it (a key id, a fingerprint, the last four characters).
    """
    sys.stdout.write(json.dumps({"event": event, **fields}) + "\n")
`
	return Fixture{
		Name: "python-secret-to-audit-log",
		Base: map[string]string{
			"app/__init__.py": "",
			"app/audit.py":    audit,
			"app/keys.py": `import secrets


def mint(user_id: str) -> tuple[str, str]:
    """mint creates an API key for user_id and returns (key_id, key).

    The key is shown to the user once and stored only as a hash.
    """
    key_id = "k_" + secrets.token_hex(4)
    key = "sk_" + secrets.token_urlsafe(32)
    return key_id, key
`,
		},
		Head: map[string]string{
			"app/__init__.py": "",
			"app/audit.py":    audit,
			"app/keys.py": `import secrets

from app.audit import audit


def mint(user_id: str) -> tuple[str, str]:
    """mint creates an API key for user_id and returns (key_id, key).

    The key is shown to the user once and stored only as a hash.
    """
    key_id = "k_" + secrets.token_hex(4)
    key = "sk_" + secrets.token_urlsafe(32)
    audit("api_key.minted", user=user_id, key_id=key_id, key=key)
    return key_id, key
`,
		},
		Defects: []Defect{{
			Path: "app/keys.py",
			Line: 13, // audit("api_key.minted", user=user_id, key_id=key_id, key=key)
			Keywords: []string{
				"off-host", "off host", "shipped", "ships the", "external", "retained",
				"plaintext", "plain text", "cleartext", "clear text", "logs the key", "logged", "logging the",
				"in the log", "into the log", "to the log", "secret in", "leaks the", "exposes the",
				"key id", "key_id only", "fingerprint", "last four",
			},
			Class:        config.ClassSecurity,
			WantSeverity: config.SeverityCritical,
			SeverityNote: "critical under \"a security breach\", and the review prompt's own example — " +
				"\"writing a decrypted secret to a log that ships off-host is critical\" — is this " +
				"change: audit documents that fields are shipped off-host and retained, and the change " +
				"passes the newly minted key itself. Above python-expired-token-accepted because the " +
				"key confers access its readers never had.",
			Why: "audit ships every field off-host to the SIEM and documents that credentials must never be passed, and the change passes the plaintext key itself, so every minted key is retained in the log",
		}},
	}
}

// pythonCleanContractFixture uses with_retry the way its contract allows: on
// a balance read, which is idempotent. It is the control for
// python-retry-nonidempotent — a reviewer that flags every retry has not read
// the contract, and a reviewer that flags a GET wrapped in it is guessing.
func pythonCleanContractFixture() Fixture {
	retry := `import time


def with_retry(fn, attempts=3, delay=0.5):
    """Call fn until it returns without raising, at most attempts times.

    fn MUST be idempotent. A timeout is retried like any other error, and a
    timed-out request may already have been processed by the other side —
    so anything fn does that is not safe to do twice (creating an order,
    charging a card, sending a message) will happen more than once.
    """
    last = None
    for i in range(attempts):
        try:
            return fn()
        except Exception as exc:  # noqa: BLE001 - deliberately broad
            last = exc
            if i + 1 < attempts:
                time.sleep(delay * (i + 1))
    raise last
`
	gateway := `class Gateway:
    """Gateway is the card processor's HTTP client."""

    def charge(self, card_token, amount_cents):
        """charge captures amount_cents from the card and returns a receipt id."""
        raise NotImplementedError

    def balance(self, account):
        """balance reads an account's available balance in cents (a GET)."""
        raise NotImplementedError
`
	return Fixture{
		Name: "python-clean-contract",
		Base: map[string]string{
			"app/__init__.py": "",
			"app/retry.py":    retry,
			"app/gateway.py":  gateway,
			"app/ledger.py": `from app.gateway import Gateway


def available(gateway: Gateway, account: str) -> int:
    """available reports the account's balance in cents."""
    return gateway.balance(account)
`,
		},
		Head: map[string]string{
			"app/__init__.py": "",
			"app/retry.py":    retry,
			"app/gateway.py":  gateway,
			"app/ledger.py": `from app.gateway import Gateway
from app.retry import with_retry


def available(gateway: Gateway, account: str) -> int:
    """available reports the account's balance in cents.

    The processor times out a few times a day; a balance read is safe to
    repeat, so try again rather than fail the page.
    """
    return with_retry(lambda: gateway.balance(account))
`,
		},
	}
}
