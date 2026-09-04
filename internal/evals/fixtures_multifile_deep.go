package evals

import "github.com/jdziat/open-nitpick/internal/config"

// The second half of the multi-file corpus: one fixture per language for
// the resolution paths a real repository hits and a plain import-to-file
// walk misses. Each is wired into MultiFileFixtures through
// deepMultiFileFixtures, and each contract sits behind the mechanism named
// in its comment — a method rather than a type, a barrel behind an alias, a
// package re-export, a Rails constant with no require — so a reviewer that
// only follows the first hop cannot see it.

func deepMultiFileFixtures() []Fixture {
	return []Fixture{
		goCacheGetUncheckedFixture(),
		tsDurationUnitsThroughBarrelFixture(),
		pythonOverwriteThroughPackageFixture(),
		rubyMailerInTransactionFixture(),
	}
}

// goCacheGetUncheckedFixture: the contract is on a METHOD of an imported type.
// Get's doc comment says the zero value comes back on a miss and callers must
// check the boolean; the change discards it and serves the zero value.
func goCacheGetUncheckedFixture() Fixture {
	cache := `package cache

import "sync"

// Cache is a bounded, process-local map from key to rendered page.
type Cache struct {
	mu    sync.Mutex
	pages map[string]string
}

// New makes an empty Cache.
func New() *Cache { return &Cache{pages: map[string]string{}} }

// Get returns the page stored under key and whether one was present.
//
// On a miss it returns the EMPTY STRING and false. The empty string is a
// valid stored page too, so ok is the only way to tell a miss from an empty
// page: callers must check it before serving the value.
func (c *Cache) Get(key string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	page, ok := c.pages[key]
	return page, ok
}

// Put stores page under key.
func (c *Cache) Put(key, page string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pages[key] = page
}
`
	return Fixture{
		Name: "go-cache-get-unchecked",
		Base: map[string]string{
			"go.mod":                  "module example.com/site\n\ngo 1.22\n",
			"internal/cache/cache.go": cache,
			"web/pages.go": `package web

import (
	"net/http"

	"example.com/site/internal/cache"
)

// Pages serves rendered pages.
type Pages struct {
	Cache  *cache.Cache
	Render func(slug string) (string, error)
}

// Health answers a liveness probe.
func (p *Pages) Health(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}
`,
		},
		Head: map[string]string{
			"go.mod":                  "module example.com/site\n\ngo 1.22\n",
			"internal/cache/cache.go": cache,
			"web/pages.go": `package web

import (
	"io"
	"net/http"

	"example.com/site/internal/cache"
)

// Pages serves rendered pages.
type Pages struct {
	Cache  *cache.Cache
	Render func(slug string) (string, error)
}

// Health answers a liveness probe.
func (p *Pages) Health(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

// Show serves GET /pages/{slug}, rendering on a cache miss.
func (p *Pages) Show(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")

	page, _ := p.Cache.Get(slug)
	if page == "" {
		rendered, err := p.Render(slug)
		if err != nil {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		p.Cache.Put(slug, rendered)
		page = rendered
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, page)
}
`,
		},
		Defects: []Defect{{
			Path: "web/pages.go",
			Line: 24, // page, _ := p.Cache.Get(slug)
			Keywords: []string{
				"discarded ok", "discards ok", "ignores ok", "ignoring ok", "ok value", "second value",
				"boolean", "miss from", "empty page", "empty string is", "legitimately empty",
				"re-render", "rerender", "renders again", "rendered again", "every request", "cache is never hit",
				"cache never", "sentinel",
			},
			Class:        config.ClassCorrectness,
			WantSeverity: config.SeverityError,
			Why:          "Get's contract says only the boolean tells a miss from a stored empty page, and the change discards the boolean and tests the string, so an empty page is re-rendered on every request and a miss is indistinguishable from it",
		}},
	}
}

// tsDurationUnitsThroughBarrelFixture: the contract is behind a tsconfig
// path alias AND a barrel. parseDuration is documented to return
// milliseconds; the change hands the result to a function that takes seconds.
func tsDurationUnitsThroughBarrelFixture() Fixture {
	return Fixture{
		Name: "ts-duration-units-through-barrel",
		Base: map[string]string{
			"package.json":      `{ "name": "scheduler", "private": true, "type": "module" }` + "\n",
			"tsconfig.json":     `{ "compilerOptions": { "baseUrl": ".", "paths": { "@/*": ["src/*"] } } }` + "\n",
			"src/time/index.ts": "export { parseDuration } from \"./parse\";\nexport { schedule } from \"./schedule\";\n",
			"src/time/parse.ts": `/**
 * parseDuration reads "90s", "5m", "2h" and returns the duration in
 * MILLISECONDS, because every timer API in this codebase takes milliseconds.
 */
export function parseDuration(s: string): number {
  const m = /^(\d+)(s|m|h)$/.exec(s.trim());
  if (!m) throw new RangeError("not a duration: " + s);
  const n = Number(m[1]);
  return n * { s: 1000, m: 60_000, h: 3_600_000 }[m[2] as "s" | "m" | "h"];
}
`,
			"src/time/schedule.ts": `/** schedule runs fn after delayMs milliseconds and returns a cancel function. */
export function schedule(fn: () => void, delayMs: number): () => void {
  const id = setTimeout(fn, delayMs);
  return () => clearTimeout(id);
}
`,
			"src/jobs.ts": `type Job = { name: string; run: () => void };

/** register records a job to run later. */
export function register(job: Job): void {
  jobs.push(job);
}

const jobs: Job[] = [];
`,
		},
		Head: map[string]string{
			"package.json":      `{ "name": "scheduler", "private": true, "type": "module" }` + "\n",
			"tsconfig.json":     `{ "compilerOptions": { "baseUrl": ".", "paths": { "@/*": ["src/*"] } } }` + "\n",
			"src/time/index.ts": "export { parseDuration } from \"./parse\";\nexport { schedule } from \"./schedule\";\n",
			"src/time/parse.ts": `/**
 * parseDuration reads "90s", "5m", "2h" and returns the duration in
 * MILLISECONDS, because every timer API in this codebase takes milliseconds.
 */
export function parseDuration(s: string): number {
  const m = /^(\d+)(s|m|h)$/.exec(s.trim());
  if (!m) throw new RangeError("not a duration: " + s);
  const n = Number(m[1]);
  return n * { s: 1000, m: 60_000, h: 3_600_000 }[m[2] as "s" | "m" | "h"];
}
`,
			"src/time/schedule.ts": `/** schedule runs fn after delayMs milliseconds and returns a cancel function. */
export function schedule(fn: () => void, delayMs: number): () => void {
  const id = setTimeout(fn, delayMs);
  return () => clearTimeout(id);
}
`,
			"src/jobs.ts": `import { parseDuration, schedule } from "@/time";

type Job = { name: string; run: () => void };

/** register records a job to run later. */
export function register(job: Job): void {
  jobs.push(job);
}

/**
 * registerDelayed runs the job after the given delay, e.g. "5m". The delay
 * is converted to seconds for schedule().
 */
export function registerDelayed(job: Job, delay: string): () => void {
  const seconds = parseDuration(delay) / 1000;
  return schedule(job.run, seconds);
}

const jobs: Job[] = [];
`,
		},
		Defects: []Defect{{
			Path: "src/jobs.ts",
			Line: 16, // return schedule(job.run, seconds);
			Keywords: []string{
				"takes milliseconds", "expects milliseconds", "in milliseconds", "milliseconds, not", "already in milliseconds",
				"divided by 1000", "divides by 1000", "dividing by 1000", "1000 times", "thousand times",
				"too early", "too soon", "fires after", "fires early", "wrong unit", "unit mismatch", "seconds to a",
			},
			Class:        config.ClassCorrectness,
			WantSeverity: config.SeverityError,
			Why:          "schedule takes milliseconds and parseDuration already returns them, and the change divides by 1000 before calling it, so every delayed job fires a thousand times too early",
		}},
	}
}

// pythonOverwriteThroughPackageFixture: the contract is in a module the
// package's __init__ re-exports. save is documented to overwrite silently and
// to require an exists() check where the path may be taken; the change writes
// user uploads to a path derived from the filename they chose.
func pythonOverwriteThroughPackageFixture() Fixture {
	disk := `import os


def exists(path: str) -> bool:
    """exists reports whether path is already stored."""
    return os.path.exists(path)


def save(path: str, data: bytes) -> None:
    """Store data at path.

    OVERWRITES without confirmation. Where the path is chosen by anything
    other than this process — a filename a user supplied, a key another
    request may also compute — call exists() first and refuse or rename,
    because a second save to the same path silently destroys the first.
    """
    with open(path, "wb") as f:
        f.write(data)
`
	return Fixture{
		Name: "python-overwrite-through-package",
		Base: map[string]string{
			"app/__init__.py":         "",
			"app/storage/__init__.py": "from .disk import exists, save\n",
			"app/storage/disk.py":     disk,
			"app/uploads.py": `UPLOAD_DIR = "/srv/uploads"


def health():
    return {"ok": True}, 200
`,
		},
		Head: map[string]string{
			"app/__init__.py":         "",
			"app/storage/__init__.py": "from .disk import exists, save\n",
			"app/storage/disk.py":     disk,
			"app/uploads.py": `import os

from app.storage import save

UPLOAD_DIR = "/srv/uploads"


def health():
    return {"ok": True}, 200


def upload(user_id: str, filename: str, data: bytes):
    """upload stores an attachment under the user's directory.

    user_id is the caller's own id from the session, already validated as
    [a-z0-9]+ by the authentication layer.
    """
    name = os.path.basename(filename)
    if not name or name in (".", ".."):
        return {"error": "filename required"}, 400
    save(os.path.join(UPLOAD_DIR, user_id, name), data)
    return {"stored": name}, 201
`,
		},
		Defects: []Defect{{
			Path: "app/uploads.py",
			Line: 21, // save(os.path.join(UPLOAD_DIR, user_id, name), data)
			Keywords: []string{
				"overwrit", "overwrote", "clobber", "silently replace", "replaces the existing", "replaces an existing",
				"same filename", "same name", "existing file", "existing attachment", "already exists", "exists()",
				"exists check", "check exists", "destroys", "previous upload", "earlier upload",
			},
			Class:        config.ClassDataLoss,
			WantSeverity: config.SeverityCritical,
			SeverityNote: "critical under \"data loss\", with the other data-loss plant: a second upload with the " +
				"same filename destroys the first one silently, on the ordinary path, and save's own " +
				"docstring names exactly this case.",
			Why: "save overwrites silently and its docstring requires an exists check for a user-chosen path, and the change saves to a path built from the user's filename with no check, so a second upload with the same name destroys the first",
		}},
	}
}

// rubyMailerInTransactionFixture: the contract is on a Rails constant no
// require names. ReceiptMailer.deliver_later's comment says the job may run
// before the transaction commits; the change calls it inside the transaction.
func rubyMailerInTransactionFixture() Fixture {
	mailer := `# Sends order receipts.
#
# receipt(order).deliver_later ENQUEUES a job on a separate connection. The
# job can run before the transaction that enqueued it commits, and then it
# reads an Order that does not exist yet and raises RecordNotFound. Enqueue
# after commit — from an after_commit callback or outside the transaction
# block — never inside it.
class ReceiptMailer < ApplicationMailer
  def receipt(order)
    @order = order
    mail(to: order.email, subject: "Your receipt")
  end
end
`
	return Fixture{
		Name: "ruby-mailer-in-transaction",
		Base: map[string]string{
			"Gemfile":                       "source 'https://rubygems.org'\ngem 'rails'\n",
			"config/application.rb":         "module Shop\n  class Application < Rails::Application\n  end\nend\n",
			"app/mailers/receipt_mailer.rb": mailer,
			"app/models/order.rb":           "class Order < ApplicationRecord\n  has_many :line_items\nend\n",
			"app/services/checkout.rb": `# Turns a cart into an order.
class Checkout
  def initialize(cart)
    @cart = cart
  end

  def call
    Order.transaction do
      order = Order.create!(email: @cart.email, total: @cart.total)
      @cart.items.each { |item| order.line_items.create!(item.attributes) }
      order
    end
  end
end
`,
		},
		Head: map[string]string{
			"Gemfile":                       "source 'https://rubygems.org'\ngem 'rails'\n",
			"config/application.rb":         "module Shop\n  class Application < Rails::Application\n  end\nend\n",
			"app/mailers/receipt_mailer.rb": mailer,
			"app/models/order.rb":           "class Order < ApplicationRecord\n  has_many :line_items\nend\n",
			"app/services/checkout.rb": `# Turns a cart into an order and sends the receipt.
class Checkout
  def initialize(cart)
    @cart = cart
  end

  def call
    Order.transaction do
      order = Order.create!(email: @cart.email, total: @cart.total)
      @cart.items.each { |item| order.line_items.create!(item.attributes) }
      ReceiptMailer.receipt(order).deliver_later
      order
    end
  end
end
`,
		},
		Defects: []Defect{{
			Path: "app/services/checkout.rb",
			Line: 11, // ReceiptMailer.receipt(order).deliver_later
			Keywords: []string{
				"before the transaction commits", "before commit", "before the commit", "not yet committed",
				"uncommitted", "inside the transaction", "within the transaction", "in the transaction",
				"after_commit", "after commit", "recordnotfound", "record not found", "does not exist yet",
				"rolled back", "rollback", "separate connection", "another connection",
			},
			Class:        config.ClassCorrectness,
			WantSeverity: config.SeverityWarning,
			SeverityNote: "warning under \"a genuine hazard under plausible conditions\": the job usually loses the " +
				"race and the receipt is sent, and on a slow commit or a rollback it is not, which is the " +
				"hazard the mailer's comment describes.",
			Why: "the mailer's comment says deliver_later enqueues on a separate connection and must not be called inside the transaction, and the change calls it inside the transaction, so the job can run before commit and raise RecordNotFound or send a receipt for a rolled-back order",
		}},
	}
}
