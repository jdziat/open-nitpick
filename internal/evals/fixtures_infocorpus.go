package evals

import "github.com/jdziat/open-nitpick/internal/config"

// InfoFixtures is the fourth corpus: ten `info` plants and two clean
// controls at the same level of subtlety.
//
// It exists because no reviewer — this one, the hosted incumbent, kimi, glm
// — has ever located the four `info` plants in the other corpora on any run,
// and the tables could not say whether that is the plants or the reviewers.
// Each plant here is a change whose consequence a senior reviewer would name
// in one sentence and want the author to decide about: not a wrong result on
// any path, but a cost — reproducibility, a lost type, a shared mutable
// value, a scan the database will make on every request — that the change
// takes on without saying so. The clean controls take a similar-looking step
// and pay no such cost, so a reviewer that objects to every change of this
// shape is scored for it.
//
// Rule 15 applies: re-runnable, not tuned on, and outside AllFixtures.
// TestInfoCorpusIsWellFormed checks what can be checked without the
// registries.
func InfoFixtures() []Fixture {
	return []Fixture{
		infoGoTimeoutHalved(),
		infoPythonPinLoosened(),
		infoTSAnyWidening(),
		infoGoContextStringKey(),
		infoJavaMutableConstant(),
		infoSQLColumnUnindexed(),
		infoBashHardcodedRegion(),
		infoPythonPrintDiagnostics(),
		infoTSMagicDuration(),
		infoGoCloseErrorOnWrite(),
		infoCleanGoNamedConstant(),
		infoCleanPythonLogging(),
	}
}

// Info reports whether a fixture belongs to the info corpus.
func Info(fixture string) bool {
	for _, f := range InfoFixtures() {
		if f.Name == fixture {
			return true
		}
	}
	return false
}

func infoGoTimeoutHalved() Fixture {
	return Fixture{
		Name: "info-go-timeout-halved",
		Base: map[string]string{"go.mod": "module example.com/svc\n\ngo 1.22\n", "internal/httpc/client.go": `package httpc

import (
	"net/http"
	"time"
)

// Default is the client every outbound call in this service shares.
var Default = &http.Client{Timeout: 30 * time.Second}
`},
		Head: map[string]string{"go.mod": "module example.com/svc\n\ngo 1.22\n", "internal/httpc/client.go": `package httpc

import (
	"net/http"
	"time"
)

// Default is the client every outbound call in this service shares.
//
// Tightened so a hung dependency fails fast.
var Default = &http.Client{Timeout: 5 * time.Second}
`},
		Defects: []Defect{{
			Path: "internal/httpc/client.go", Line: 11,
			Keywords: []string{"every outbound", "all outbound", "every caller", "shared by", "shared client", "slower upstream", "slow upstream",
				"previously succeeded", "used to succeed", "now fail", "will fail", "start failing", "report generation", "long-running", "per-call timeout", "per call"},
			Class: config.ClassContract, WantSeverity: config.SeverityInfo,
			Why: "the timeout is on the client every outbound call shares, so a slow upstream call that used to succeed within thirty seconds will now fail, and no caller was given a per-call timeout to opt out with",
		}},
	}
}

func infoPythonPinLoosened() Fixture {
	return Fixture{
		Name: "info-python-pin-loosened",
		Base: map[string]string{"requirements.txt": "requests==2.31.0\nurllib3==2.2.1\n", "app/__init__.py": ""},
		Head: map[string]string{"requirements.txt": "requests>=2.31\nurllib3==2.2.1\n", "app/__init__.py": ""},
		Defects: []Defect{{
			Path: "requirements.txt", Line: 1,
			Keywords: []string{"reproducib", "unpinned", "floating", "open-ended", "any future", "future release", "future version", "next release",
				"lockfile", "lock file", "not pinned", "no upper bound", "upper bound", "drift", "different version"},
			Class: config.ClassMaintainability, WantSeverity: config.SeverityInfo,
			Why: "the pin becomes an open-ended range, so two installs on different days get different versions and a future release can change behaviour with no change in this repository; a lockfile or an upper bound would keep it reproducible",
		}},
	}
}

func infoTSAnyWidening() Fixture {
	return Fixture{
		Name: "info-ts-any-widening",
		Base: map[string]string{"package.json": `{ "name": "audit", "private": true, "type": "module" }` + "\n", "src/audit.ts": `/** record writes one audit event. */
export function record(event: string, actor: string): void {
  events.push({ event, actor });
}

const events: { event: string; actor: string }[] = [];
`},
		Head: map[string]string{"package.json": `{ "name": "audit", "private": true, "type": "module" }` + "\n", "src/audit.ts": `/** record writes one audit event. Accepts whatever the caller has. */
export function record(event: string, actor: any): void {
  events.push({ event, actor: String(actor) });
}

const events: { event: string; actor: string }[] = [];
`},
		Defects: []Defect{{
			Path: "src/audit.ts", Line: 2,
			Keywords: []string{"type safety", "type-safety", "loses the type", "lose type", "untyped", "no longer checked", "not checked", "unknown instead",
				"use unknown", "object object", "[object", "stringified", "opts out of", "escape hatch", "compile-time"},
			Class: config.ClassMaintainability, WantSeverity: config.SeverityInfo,
			Why: "widening the parameter to any opts the call site out of type checking, so a caller passing an object is not caught at compile time and the stored actor becomes an [object Object] string; unknown with a narrowing would keep the check",
		}},
	}
}

func infoGoContextStringKey() Fixture {
	return Fixture{
		Name: "info-go-context-string-key",
		Base: map[string]string{"go.mod": "module example.com/web\n\ngo 1.22\n", "mw/user.go": `package mw

import "context"

// User is what the middleware learned about the caller.
type User struct{ ID string }
`},
		Head: map[string]string{"go.mod": "module example.com/web\n\ngo 1.22\n", "mw/user.go": `package mw

import "context"

// User is what the middleware learned about the caller.
type User struct{ ID string }

// WithUser stores the caller on the context for handlers downstream.
func WithUser(ctx context.Context, u User) context.Context {
	return context.WithValue(ctx, "user", u)
}

// FromContext reads the caller back.
func FromContext(ctx context.Context) (User, bool) {
	u, ok := ctx.Value("user").(User)
	return u, ok
}
`},
		Defects: []Defect{{
			Path: "mw/user.go", Line: 10,
			Keywords: []string{"collision", "collide", "colliding", "unexported type", "custom type", "private type", "key type", "string as a key",
				"string key", "another package", "other packages", "same key", "go vet", "staticcheck", "SA1029"},
			Class: config.ClassMaintainability, WantSeverity: config.SeverityInfo,
			Why: "a plain string is used as the context key, so any other package that stores under the same string collides with it silently; an unexported key type is the convention that prevents that, and go vet flags this",
		}},
	}
}

func infoJavaMutableConstant() Fixture {
	return Fixture{
		Name: "info-java-mutable-constant",
		Base: map[string]string{"src/main/java/com/acme/cfg/Defaults.java": `package com.acme.cfg;

/** Defaults holds settings used when none are configured. */
public final class Defaults {
    private Defaults() {}

    public static final int PAGE_SIZE = 50;
}
`},
		Head: map[string]string{"src/main/java/com/acme/cfg/Defaults.java": `package com.acme.cfg;

import java.util.Arrays;
import java.util.List;

/** Defaults holds settings used when none are configured. */
public final class Defaults {
    private Defaults() {}

    public static final int PAGE_SIZE = 50;

    /** Regions a deployment serves when none are configured. */
    public static final List<String> REGIONS = Arrays.asList("us-east-1", "eu-west-1");
}
`},
		Defects: []Defect{{
			Path: "src/main/java/com/acme/cfg/Defaults.java", Line: 13,
			Keywords: []string{"mutable", "modifiable", "unmodifiable", "immutable", "can be modified", "can be changed", "can set",
				"list.of", "shared state", "global state", "every caller sees", "fixed-size", "fixed size"},
			Class: config.ClassMaintainability, WantSeverity: config.SeverityInfo,
			Why: "Arrays.asList returns a list whose elements can be modified, and it is exposed as a public constant, so any caller can change the region every other caller sees; List.of would make it immutable",
		}},
	}
}

func infoSQLColumnUnindexed() Fixture {
	return Fixture{
		Name: "info-sql-column-unindexed",
		Base: map[string]string{"migrations/0011_orders.sql": "CREATE TABLE orders (\n    id bigserial PRIMARY KEY,\n    account_id bigint NOT NULL,\n    created_at timestamptz NOT NULL DEFAULT now()\n);\n"},
		Head: map[string]string{"migrations/0011_orders.sql": "CREATE TABLE orders (\n    id bigserial PRIMARY KEY,\n    account_id bigint NOT NULL,\n    created_at timestamptz NOT NULL DEFAULT now()\n);\n",
			"migrations/0012_order_status.sql": "-- 0012: track fulfilment; the dashboard lists orders by status.\n\nALTER TABLE orders ADD COLUMN status text NOT NULL DEFAULT 'new';\n"},
		Defects: []Defect{{
			Path: "migrations/0012_order_status.sql", Line: 3,
			Keywords: []string{"no index", "without an index", "add an index", "create index", "indexed", "sequential scan", "seq scan", "full scan",
				"table scan", "scan the whole", "scans every", "filtered by status", "filter by status", "grows with the table"},
			Class: config.ClassMaintainability, WantSeverity: config.SeverityInfo,
			Why: "the comment says the dashboard lists orders by this column and the migration adds no index on it, so that query will scan the whole table and grows with it; an index is the author's call to make now rather than after the table is large",
		}},
	}
}

func infoBashHardcodedRegion() Fixture {
	return Fixture{
		Name: "info-bash-hardcoded-region",
		Base: map[string]string{"scripts/deploy.sh": "#!/bin/sh\nset -eu\n\necho \"deploying\"\n"},
		Head: map[string]string{"scripts/deploy.sh": "#!/bin/sh\nset -eu\n\necho \"deploying\"\naws s3 sync ./dist \"s3://$BUCKET\" --region us-east-1\n"},
		Defects: []Defect{{
			Path: "scripts/deploy.sh", Line: 5,
			Keywords: []string{"hard-coded region", "hardcoded region", "hard-codes the region", "hardcodes the region", "region is fixed", "other region", "another region",
				"aws_region", "aws_default_region", "parameteri", "configurable", "environment variable", "bucket's region", "bucket lives"},
			Class: config.ClassMaintainability, WantSeverity: config.SeverityInfo,
			Why: "the region is hard-coded while the bucket comes from the environment, so a deployment to a bucket in another region needs a code change; AWS_REGION or a parameter beside BUCKET would keep the two together",
		}},
	}
}

func infoPythonPrintDiagnostics() Fixture {
	return Fixture{
		Name: "info-python-print-diagnostics",
		Base: map[string]string{"app/__init__.py": "", "app/log.py": "import logging\n\nlog = logging.getLogger(\"app\")\n", "app/sync.py": "from app.log import log\n\n\ndef sync(items):\n    for item in items:\n        log.info(\"syncing %s\", item.id)\n"},
		Head: map[string]string{"app/__init__.py": "", "app/log.py": "import logging\n\nlog = logging.getLogger(\"app\")\n", "app/sync.py": "from app.log import log\n\n\ndef sync(items):\n    for item in items:\n        log.info(\"syncing %s\", item.id)\n\n\ndef reconcile(items):\n    for item in items:\n        print(f\"reconciling {item.id}\")\n"},
		Defects: []Defect{{
			Path: "app/sync.py", Line: 11,
			Keywords: []string{"logging module", "instead of print", "rather than print", "uses print", "print statement", "stdout",
				"log level", "cannot be filtered", "can't be filtered", "bypasses", "log.info", "the logger", "structured"},
			Class: config.ClassMaintainability, WantSeverity: config.SeverityInfo,
			Why: "the sibling function two lines up logs through the module's logger and this one writes to stdout with print, so its output bypasses the log level and handlers everything else in the module respects",
		}},
	}
}

func infoTSMagicDuration() Fixture {
	return Fixture{
		Name: "info-ts-magic-duration",
		Base: map[string]string{"package.json": `{ "name": "sessions", "private": true, "type": "module" }` + "\n", "src/session.ts": `export type Session = { id: string; expiresAt: number };

/** create issues a session. */
export function create(id: string, now: number): Session {
  return { id, expiresAt: now };
}
`},
		Head: map[string]string{"package.json": `{ "name": "sessions", "private": true, "type": "module" }` + "\n", "src/session.ts": `export type Session = { id: string; expiresAt: number };

/** create issues a session that lasts a day. */
export function create(id: string, now: number): Session {
  return { id, expiresAt: now + 86400000 };
}
`},
		Defects: []Defect{{
			Path: "src/session.ts", Line: 5,
			Keywords: []string{"magic number", "magic constant", "named constant", "name the constant", "milliseconds in a day", "ms in a day", "one day in",
				"24 hours", "readab", "unexplained", "bare number", "literal", "self-document"},
			Class: config.ClassMaintainability, WantSeverity: config.SeverityInfo,
			Why: "the session length is a bare literal that only the comment explains; a named constant such as a day in milliseconds, written as 24 * 60 * 60 * 1000, would make the next change to it and the next reader's job trivial",
		}},
	}
}

func infoGoCloseErrorOnWrite() Fixture {
	return Fixture{
		Name: "info-go-close-error-on-write",
		Base: map[string]string{"go.mod": "module example.com/export\n\ngo 1.22\n", "export/write.go": `package export

import "os"

// Write stores data at path.
func Write(path string, data []byte) error {
	return os.WriteFile(path, data, 0o600)
}
`},
		Head: map[string]string{"go.mod": "module example.com/export\n\ngo 1.22\n", "export/write.go": `package export

import (
	"bufio"
	"io"
	"os"
)

// Write streams r to path.
func Write(path string, r io.Reader) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w := bufio.NewWriter(f)
	if _, err := io.Copy(w, r); err != nil {
		return err
	}
	return w.Flush()
}
`},
		Defects: []Defect{{
			Path: "export/write.go", Line: 15,
			Keywords: []string{"close error", "error from close", "close's error", "close() error", "close is ignored", "ignores the error from close", "deferred close",
				"unchecked close", "write may be lost", "writes lost", "reported at close", "surfaces on close", "enospc", "fsync"},
			Class: config.ClassMaintainability, WantSeverity: config.SeverityInfo,
			Why: "on a file being written, the error from Close is where a failed write is reported, and the deferred Close discards it, so a full disk can be reported as success; checking Close's error is the author's call on a function that writes",
		}},
	}
}

func infoCleanGoNamedConstant() Fixture {
	return Fixture{
		Name: "info-clean-go-named-constant",
		Base: map[string]string{"go.mod": "module example.com/sessions\n\ngo 1.22\n", "session/session.go": `package session

import "time"

// TTL is how long a session lasts.
const TTL = 24 * time.Hour

// Expiry returns when a session issued now expires.
func Expiry(now time.Time) time.Time { return now.Add(TTL) }
`},
		Head: map[string]string{"go.mod": "module example.com/sessions\n\ngo 1.22\n", "session/session.go": `package session

import "time"

// TTL is how long a session lasts.
const TTL = 24 * time.Hour

// RefreshWindow is how long before expiry a session may be refreshed.
const RefreshWindow = time.Hour

// Expiry returns when a session issued now expires.
func Expiry(now time.Time) time.Time { return now.Add(TTL) }

// Refreshable reports whether a session expiring at exp may be refreshed at now.
func Refreshable(now, exp time.Time) bool { return exp.Sub(now) <= RefreshWindow && now.Before(exp) }
`},
	}
}

func infoCleanPythonLogging() Fixture {
	return Fixture{
		Name: "info-clean-python-logging",
		Base: map[string]string{"app/__init__.py": "", "app/log.py": "import logging\n\nlog = logging.getLogger(\"app\")\n", "app/sync.py": "from app.log import log\n\n\ndef sync(items):\n    for item in items:\n        log.info(\"syncing %s\", item.id)\n"},
		Head: map[string]string{"app/__init__.py": "", "app/log.py": "import logging\n\nlog = logging.getLogger(\"app\")\n", "app/sync.py": "from app.log import log\n\n\ndef sync(items):\n    for item in items:\n        log.info(\"syncing %s\", item.id)\n\n\ndef reconcile(items):\n    for item in items:\n        log.info(\"reconciling %s\", item.id)\n"},
	}
}
