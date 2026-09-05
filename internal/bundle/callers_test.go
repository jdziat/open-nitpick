package bundle

import (
	"context"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/diff"
)

// modifiedFile fabricates a diff that changed the given 1-based lines of
// content in place, so a redefinition can be positioned without spelling out
// hunks.
func modifiedFile(p, content string, changed ...int) *diff.File {
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	f := &diff.File{Path: p, Kind: diff.ChangeModified}
	for _, n := range changed {
		f.Hunks = append(f.Hunks, diff.Hunk{
			OldStart: n, OldLines: 1, NewStart: n, NewLines: 1,
			Lines: []diff.Line{
				{Kind: diff.LineRemoved, Content: "old", OldLine: n},
				{Kind: diff.LineAdded, Content: lines[n-1], NewLine: n},
			},
		})
	}
	return f
}

func assembleCallers(t *testing.T, tree fakeTree, withLister bool, changed *diff.File) *Plan {
	t.Helper()
	var list DirLister
	if withLister {
		list = tree.list
	}
	plan, err := AssembleWith(context.Background(), relatedConfig(), diff.Files{changed}, tree.fetch, list)
	if err != nil {
		t.Fatalf("AssembleWith: %v", err)
	}
	return plan
}

func callerNames(plan *Plan) []string {
	var out []string
	for _, b := range plan.Batches {
		for _, e := range b.Entries {
			for _, r := range e.Related {
				if r.Calls != "" {
					out = append(out, r.Path+":"+r.Name+" calls "+r.Calls)
				}
			}
		}
	}
	return out
}

const callersGoMod = "module example.com/app\n\ngo 1.22\n"

func TestCallersGoAttachesTheFunctionThatCallsARedefinedMethod(t *testing.T) {
	store := `package store

import "errors"

// ErrNotFound is returned by Lookup when no user has the id.
var ErrNotFound = errors.New("user not found")

// Users is an in-memory user table.
type Users struct{ names map[string]string }

// Lookup returns the display name for id, or ErrNotFound.
func (u *Users) Lookup(id string) (string, error) {
	name, ok := u.names[id]
	if !ok {
		return "", ErrNotFound
	}
	return name, nil
}

// Add stores a display name under id.
func (u *Users) Add(id, name string) { u.names[id] = name }
`
	handler := `package web

import (
	"errors"
	"net/http"

	"example.com/app/internal/store"
)

// Handler serves user lookups.
type Handler struct{ users *store.Users }

// ServeHTTP answers 404 for a missing user and 500 for anything else.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name, err := h.users.Lookup(r.URL.Query().Get("id"))
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "no such user", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "lookup failed", http.StatusInternalServerError)
		return
	}
	_, _ = w.Write([]byte(name))
}

func unrelated() {}
`
	tree := fakeTree{
		"go.mod":                  callersGoMod,
		"internal/store/users.go": store,
		"web/users.go":            handler,
		"web/users_test.go":       "package web\n\nfunc TestX(t *testing.T) { (&store.Users{}).Lookup(\"x\") }\n",
	}
	// Line 15 is the return inside Lookup.
	plan := assembleCallers(t, tree, true, modifiedFile("internal/store/users.go", store, 15))

	got := callerNames(plan)
	want := []string{"web/users.go:ServeHTTP calls store.Users.Lookup"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("callers = %v, want %v", got, want)
	}

	rendered := Render(plan.Batches[0].Entries[0])
	for _, s := range []string{
		"#### Callers of what this change redefines",
		"##### web/users.go (from line 13), calls store.Users.Lookup",
		"errors.Is(err, store.ErrNotFound)",
	} {
		if !strings.Contains(rendered, s) {
			t.Errorf("rendered prompt lacks %q:\n%s", s, rendered)
		}
	}
	if strings.Contains(rendered, "func unrelated") {
		t.Errorf("attached a function that does not call the redefined symbol")
	}
}

func TestCallersGoAttachesNothingWhenTheDiffMissesEveryExportedFunc(t *testing.T) {
	store := "package store\n\n// Lookup finds x.\nfunc Lookup() int { return 1 }\n\nvar internalNote = 1\n"
	tree := fakeTree{
		"go.mod":       callersGoMod,
		"store/s.go":   store,
		"web/users.go": "package web\n\nimport \"example.com/app/store\"\n\nfunc Serve() { store.Lookup() }\n",
		"web/other.go": "package web\n\nfunc Other() {}\n",
	}
	// Line 6 is the unexported var, outside Lookup.
	plan := assembleCallers(t, tree, true, modifiedFile("store/s.go", store, 6))
	if got := callerNames(plan); len(got) != 0 {
		t.Fatalf("callers = %v, want none", got)
	}
}

func TestCallersGoNeedsAListerAndSkipsAddedFiles(t *testing.T) {
	store := "package store\n\n// Lookup finds x.\nfunc Lookup() int { return 1 }\n"
	tree := fakeTree{
		"go.mod":       callersGoMod,
		"store/s.go":   store,
		"web/users.go": "package web\n\nimport \"example.com/app/store\"\n\nfunc Serve() { store.Lookup() }\n",
	}
	if got := callerNames(assembleCallers(t, tree, false, modifiedFile("store/s.go", store, 4))); len(got) != 0 {
		t.Errorf("without a lister, callers = %v, want none", got)
	}
	if got := callerNames(assembleCallers(t, tree, true, addedFile("store/s.go", store))); len(got) != 0 {
		t.Errorf("for an added file, callers = %v, want none", got)
	}
}

func TestCallersPythonFollowsFromImports(t *testing.T) {
	db := `MAX_PAGE = 100


def fetch_orders(conn, offset, limit):
    """Return up to limit orders."""
    if limit > MAX_PAGE:
        raise ValueError("too many")
    return conn.execute("SELECT 1").fetchall()


def unrelated():
    return 0
`
	export := `import csv

from app.db import fetch_orders


def export_orders(conn, out):
    """Write every order to out, a page at a time."""
    offset = 0
    while True:
        rows = fetch_orders(conn, offset, 500)
        if not rows:
            return
        csv.writer(out).writerows(rows)
        offset += len(rows)


def helper():
    pass
`
	tree := fakeTree{
		"app/__init__.py": "",
		"app/db.py":       db,
		"app/export.py":   export,
		"app/other.py":    "def other():\n    return fetch_orders\n",
	}
	plan := assembleCallers(t, tree, true, modifiedFile("app/db.py", db, 7))
	got := callerNames(plan)
	want := []string{"app/export.py:export_orders calls db.fetch_orders"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("callers = %v, want %v", got, want)
	}
	snippet := plan.Batches[0].Entries[0].Related[0].Snippet
	if !strings.Contains(snippet, "fetch_orders(conn, offset, 500)") || strings.Contains(snippet, "def helper") {
		t.Errorf("snippet is not the enclosing def alone:\n%s", snippet)
	}
}

func TestCallersTypeScriptFollowsRelativeImports(t *testing.T) {
	duration := `/**
 * parseDuration returns the length in seconds.
 */
export function parseDuration(text: string): number {
  return Number(text);
}
`
	http := `import { parseDuration } from "./duration";

/**
 * fetchWithTimeout fetches url, aborting after the configured duration.
 */
export async function fetchWithTimeout(url: string, timeout: string): Promise<Response> {
  const signal = AbortSignal.timeout(parseDuration(timeout));
  return fetch(url, { signal });
}

export function other() {}
`
	tree := fakeTree{
		"package.json":     "{}",
		"src/duration.ts":  duration,
		"src/http.ts":      http,
		"src/http.test.ts": "import { parseDuration } from \"./duration\";\ntest(() => parseDuration(\"1\"));\n",
	}
	plan := assembleCallers(t, tree, true, modifiedFile("src/duration.ts", duration, 5))
	got := callerNames(plan)
	want := []string{"src/http.ts:fetchWithTimeout calls duration.parseDuration"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("callers = %v, want %v", got, want)
	}
	if r := plan.Batches[0].Entries[0].Related[0]; r.Line != 3 || strings.Contains(r.Snippet, "function other") {
		t.Errorf("snippet = line %d:\n%s", r.Line, r.Snippet)
	}
}

func TestCallersWalkStopsAtTheFileCeiling(t *testing.T) {
	store := "package store\n\n// Lookup finds x.\nfunc Lookup() int { return 1 }\n"
	tree := fakeTree{"go.mod": callersGoMod, "store/s.go": store}
	for i := 0; i < maxCallerFiles+50; i++ {
		tree["pkg/f"+string(rune('a'+i%26))+strings.Repeat("x", i/26)+".go"] = "package pkg\n\nfunc F() {}\n"
	}
	tree["zzz/last.go"] = "package zzz\n\nimport \"example.com/app/store\"\n\nfunc Last() { store.Lookup() }\n"

	c := newRelatedCollector(context.Background(), diff.Files{modifiedFile("store/s.go", store, 4)}, tree.fetch, tree.list)
	files := c.walkFiles("", func(name string) bool { return strings.HasSuffix(name, ".go") })
	if len(files) != maxCallerFiles {
		t.Fatalf("walked %d files, want the ceiling %d", len(files), maxCallerFiles)
	}
}
