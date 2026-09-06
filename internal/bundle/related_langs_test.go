package bundle

import (
	"strings"
	"testing"
)

func TestRelatedRustResolvesCrateUses(t *testing.T) {
	tree := fakeTree{
		"Cargo.toml": "[package]\nname = \"app\"\n",
		"src/lib.rs": "pub mod store;\npub mod util;\n",
		"src/store/mod.rs": `/// Open a store.
///
/// Panics when the path is empty.
pub fn open(path: &str) -> Store {
    if path.is_empty() {
        panic!("empty path");
    }
    Store {}
}

pub struct Store {}

/// Default timeout, in seconds.
pub const TIMEOUT_SECS: u64 = 5;
`,
		"src/util.rs": "pub fn helper() {}\n",
		"src/main.rs": `use crate::store::{open, TIMEOUT_SECS};
use crate::util;

fn main() {
    let s = open("x");
    util::helper();
    println!("{}", TIMEOUT_SECS);
}
`,
	}
	plan := assembleRelated(t, relatedConfig(), tree, true, "src/main.rs")
	got := relatedNames(plan)
	want := []string{"src/store/mod.rs:TIMEOUT_SECS", "src/store/mod.rs:open", "src/util.rs:helper"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("related = %v, want %v", got, want)
	}
	for _, r := range plan.Batches[0].Entries[0].Related {
		if r.Name == "open" && (!strings.Contains(r.Snippet, "Panics when the path is empty") || !strings.Contains(r.Snippet, `panic!("empty path")`)) {
			t.Errorf("open's doc comment or body missing:\n%s", r.Snippet)
		}
	}
}

func TestRelatedRubyResolvesRequires(t *testing.T) {
	tree := fakeTree{
		"lib/retry.rb": `# Call the block until it returns without raising.
#
# The block MUST be idempotent: a timed-out attempt may have succeeded.
def with_retry(attempts: 3)
  yield
rescue StandardError
  retry if (attempts -= 1) > 0
  raise
end

MAX_ATTEMPTS = 3

class Gateway
  def charge(card, cents)
  end
end
`,
		"app/billing.rb": `require_relative "../lib/retry"
require "retry"

def settle(gateway, card, cents)
  with_retry { gateway.charge(card, cents) }
end
`,
	}
	plan := assembleRelated(t, relatedConfig(), tree, true, "app/billing.rb")
	got := relatedNames(plan)
	if strings.Join(got, ",") != "lib/retry.rb:with_retry" {
		t.Fatalf("related = %v; only the method the change calls is attached", got)
	}
	r := plan.Batches[0].Entries[0].Related[0]
	if !strings.Contains(r.Snippet, "MUST be idempotent") || !strings.HasSuffix(strings.TrimSpace(r.Snippet), "end") || strings.Contains(r.Snippet, "MAX_ATTEMPTS") {
		t.Errorf("with_retry snippet is wrong:\n%s", r.Snippet)
	}
}

func TestRelatedJavaResolvesImportsAndSiblings(t *testing.T) {
	tree := fakeTree{
		"src/main/java/com/acme/store/Store.java": `package com.acme.store;

/** Store talks to the primary. */
public class Store {
    /**
     * Deletes every upload matching f. An EMPTY filter matches every row.
     */
    public int deleteWhere(Filter f) {
        return 0;
    }
}
`,
		"src/main/java/com/acme/api/Helper.java": "package com.acme.api;\n\n/** A sibling. */\npublic final class Helper {\n    static void go() {}\n}\n",
		"src/main/java/com/acme/api/Admin.java": `package com.acme.api;

import com.acme.store.Store;
import java.util.List;

public class Admin {
    private final Store store;

    public Admin(Store store) { this.store = store; }

    public int cleanup() {
        Helper.go();
        return store.deleteWhere(null);
    }
}
`,
	}
	plan := assembleRelated(t, relatedConfig(), tree, true, "src/main/java/com/acme/api/Admin.java")
	got := relatedNames(plan)
	want := []string{"src/main/java/com/acme/api/Helper.java:Helper", "src/main/java/com/acme/store/Store.java:Store"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("related = %v, want %v", got, want)
	}
	for _, r := range plan.Batches[0].Entries[0].Related {
		if r.Name == "Store" && !strings.Contains(r.Snippet, "EMPTY filter matches every row") {
			t.Errorf("Store's javadoc missing:\n%s", r.Snippet)
		}
	}
}

func TestRelatedKotlinResolvesImports(t *testing.T) {
	tree := fakeTree{
		"src/main/kotlin/com/acme/money/Money.kt": `package com.acme.money

/** toCents takes DOLLARS. */
fun toCents(dollars: Double): Long = Math.round(dollars * 100)

/** A price. */
data class Price(val cents: Long)
`,
		"src/main/kotlin/com/acme/cart/Cart.kt": `package com.acme.cart

import com.acme.money.Price

class Cart {
    fun total(): Price = Price(0)
}
`,
	}
	plan := assembleRelated(t, relatedConfig(), tree, true, "src/main/kotlin/com/acme/cart/Cart.kt")
	if got := relatedNames(plan); strings.Join(got, ",") != "src/main/kotlin/com/acme/money/Money.kt:Price" {
		t.Fatalf("related = %v", got)
	}
}

func TestRelatedCResolvesQuotedIncludes(t *testing.T) {
	tree := fakeTree{
		"include/store.h": `#ifndef STORE_H
#define STORE_H

/* Query the primary. ctx_ms must be positive: 0 blocks forever. */
int store_query(int ctx_ms, const char *sql);

#define STORE_MAX 64

typedef struct store store_t;
#endif
`,
		"src/api.c": `#include <stdio.h>
#include "store.h"

int monthly(void) {
    return store_query(0, "SELECT 1") + STORE_MAX;
}
`,
	}
	plan := assembleRelated(t, relatedConfig(), tree, true, "src/api.c")
	got := relatedNames(plan)
	want := []string{"include/store.h:STORE_MAX", "include/store.h:store_query"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("related = %v, want %v", got, want)
	}
	for _, r := range plan.Batches[0].Entries[0].Related {
		if r.Name == "store_query" && !strings.Contains(r.Snippet, "0 blocks forever") {
			t.Errorf("store_query's comment missing:\n%s", r.Snippet)
		}
	}
}

func TestRustUseTreesAreWalked(t *testing.T) {
	got := strings.Split(splitTopLevel("a, b::{c, d}, e as f"), "\x00")
	if len(got) != 3 || strings.TrimSpace(got[1]) != "b::{c, d}" {
		t.Errorf("splitTopLevel = %q", got)
	}
}

// A use tree that does not close (`use crate::util::{helper;`) once walked
// the same text forever and took the review down with a stack overflow; it
// now names nothing, and the balanced use beside it still resolves.
func TestRustUnbalancedUseTreeNamesNothing(t *testing.T) {
	tree := fakeTree{
		"Cargo.toml":  "[package]\nname = \"app\"\n",
		"src/lib.rs":  "pub mod util;\n",
		"src/util.rs": "pub fn helper() {}\npub fn other() {}\n",
		"src/main.rs": "use crate::util::{helper;\nuse crate::util::other;\n\nfn main() { other(); helper(); }\n",
	}
	plan := assembleRelated(t, relatedConfig(), tree, true, "src/main.rs")
	got := relatedNames(plan)
	if len(got) != 1 || got[0] != "src/util.rs:other" {
		t.Errorf("related = %v, want only the balanced use resolved", got)
	}
}
