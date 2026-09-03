package bundle

import (
	"strings"
	"testing"
)

func TestRelatedTypeScriptFollowsAliasesAndBarrels(t *testing.T) {
	tree := fakeTree{
		"tsconfig.json": `{
  // comments and trailing commas are allowed here
  "compilerOptions": { "baseUrl": ".", "paths": { "@/*": ["src/*"], }, },
}`,
		"src/money/index.ts":   "export { toCents } from \"./convert\";\nexport * from \"./format\";\n",
		"src/money/convert.ts": "/** toCents takes DOLLARS. */\nexport function toCents(d: number): number { return Math.round(d * 100); }\n",
		"src/money/format.ts":  "/** formatCents renders cents. */\nexport function formatCents(c: number): string { return String(c); }\n",
		"src/checkout.ts":      "import { toCents, formatCents } from \"@/money\";\n\nexport const t = formatCents(toCents(1));\n",
	}
	plan := assembleRelated(t, relatedConfig(), tree, true, "src/checkout.ts")
	got := relatedNames(plan)
	want := []string{"src/money/convert.ts:toCents", "src/money/format.ts:formatCents"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("related = %v, want %v", got, want)
	}
}

func TestRelatedPythonFollowsPackageReexports(t *testing.T) {
	tree := fakeTree{
		"app/__init__.py":         "",
		"app/storage/__init__.py": "from .disk import save, exists\n",
		"app/storage/disk.py":     "def save(path, data):\n    \"\"\"Overwrites path without confirmation.\"\"\"\n    open(path, 'wb').write(data)\n\n\ndef exists(path):\n    return False\n",
		"app/upload.py":           "from app.storage import save\n\ndef handle(name, data):\n    save('/srv/' + name, data)\n",
	}
	plan := assembleRelated(t, relatedConfig(), tree, true, "app/upload.py")
	if got := relatedNames(plan); strings.Join(got, ",") != "app/storage/disk.py:save" {
		t.Fatalf("related = %v", got)
	}
	if r := plan.Batches[0].Entries[0].Related[0]; !strings.Contains(r.Snippet, "Overwrites path") {
		t.Errorf("snippet = %q", r.Snippet)
	}
}

func TestRelatedGoAttachesMethodsCalledOnImportedTypes(t *testing.T) {
	tree := fakeTree{
		"go.mod": "module example.com/app\n",
		"internal/cache/cache.go": `package cache

// Cache is a bounded map.
type Cache struct{}

// New makes a Cache.
func New() *Cache { return &Cache{} }

// Get returns the value for k and whether it was present. The zero value is
// returned on a miss, so callers must check ok before using it.
func (c *Cache) Get(k string) (string, bool) { return "", false }

// Put stores v under k.
func (c *Cache) Put(k, v string) {}
`,
		"api/handler.go": `package api

import "example.com/app/internal/cache"

func Serve(c *cache.Cache, k string) string {
	v, _ := c.Get(k)
	return v
}
`,
	}
	plan := assembleRelated(t, relatedConfig(), tree, true, "api/handler.go")
	got := relatedNames(plan)
	want := []string{"internal/cache/cache.go:Cache", "internal/cache/cache.go:Cache.Get"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("related = %v, want %v (Put is not called and is not attached)", got, want)
	}
	for _, r := range plan.Batches[0].Entries[0].Related {
		if r.Name == "Cache.Get" && !strings.Contains(r.Snippet, "callers must check ok") {
			t.Errorf("Get's doc comment missing:\n%s", r.Snippet)
		}
	}
}

func TestRelatedRubyResolvesRailsConstants(t *testing.T) {
	tree := fakeTree{
		"Gemfile":                              "gem 'rails'\n",
		"config/application.rb":                "module App; end\n",
		"app/services/billing/refund.rb":       "module Billing\n  # Issues a refund. NOT idempotent: calling it twice refunds twice.\n  class Refund\n    def self.issue(charge, cents)\n    end\n  end\nend\n",
		"app/mailers/receipt_mailer.rb":        "# Enqueues; never call inside a transaction.\nclass ReceiptMailer < ApplicationMailer\n  def deliver(order); end\nend\n",
		"app/controllers/orders_controller.rb": "class OrdersController < ApplicationController\n  def cancel\n    Billing::Refund.issue(@charge, 100)\n    ReceiptMailer.deliver(@order)\n  end\nend\n",
	}
	plan := assembleRelated(t, relatedConfig(), tree, true, "app/controllers/orders_controller.rb")
	got := relatedNames(plan)
	want := []string{"app/mailers/receipt_mailer.rb:ReceiptMailer", "app/services/billing/refund.rb:Refund"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("related = %v, want %v", got, want)
	}
	if rubyUnderscore("Billing::RefundV2") != "billing/refund_v2" || rubyUnderscore("HTMLParser") != "html_parser" {
		t.Errorf("underscore: %q %q", rubyUnderscore("Billing::RefundV2"), rubyUnderscore("HTMLParser"))
	}
}
