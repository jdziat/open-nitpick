package bundle

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/diff"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// fakeTree serves a map of paths as both a fetcher and a lister.
type fakeTree map[string]string

func (t fakeTree) fetch(_ context.Context, p string) ([]byte, error) {
	if c, ok := t[p]; ok {
		return []byte(c), nil
	}
	return nil, vcs.ErrNotFound
}

func (t fakeTree) list(_ context.Context, dir string) ([]string, error) {
	dir = strings.Trim(dir, "/")
	seen := map[string]bool{}
	var names []string
	for p := range t {
		d := path.Dir(p)
		if d == "." {
			d = ""
		}
		switch {
		case d == dir:
			names = append(names, path.Base(p))
		case dir == "" && strings.Contains(p, "/"):
			top := strings.SplitN(p, "/", 2)[0] + "/"
			if !seen[top] {
				seen[top] = true
				names = append(names, top)
			}
		case strings.HasPrefix(d, dir+"/"):
			sub := strings.SplitN(strings.TrimPrefix(d, dir+"/"), "/", 2)[0] + "/"
			if !seen[sub] {
				seen[sub] = true
				names = append(names, sub)
			}
		}
	}
	if len(names) == 0 {
		return nil, vcs.ErrNotFound
	}
	sort.Strings(names)
	return names, nil
}

// addedFile fabricates a diff adding every line of content to path.
func addedFile(p, content string) *diff.File {
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	h := diff.Hunk{NewStart: 1, NewLines: len(lines)}
	for i, l := range lines {
		h.Lines = append(h.Lines, diff.Line{Kind: diff.LineAdded, Content: l, NewLine: i + 1})
	}
	return &diff.File{Path: p, Kind: diff.ChangeAdded, Hunks: []diff.Hunk{h}}
}

func relatedConfig() *config.Config {
	cfg := config.Defaults()
	cfg.Review.RelatedContext = true
	cfg.Review.RelatedContextCallers = true
	return cfg
}

func assembleRelated(t *testing.T, cfg *config.Config, tree fakeTree, withLister bool, changed ...string) *Plan {
	t.Helper()
	var files diff.Files
	for _, p := range changed {
		files = append(files, addedFile(p, tree[p]))
	}
	var list DirLister
	if withLister {
		list = tree.list
	}
	plan, err := AssembleWith(context.Background(), cfg, files, tree.fetch, list)
	if err != nil {
		t.Fatalf("AssembleWith: %v", err)
	}
	return plan
}

func relatedNames(plan *Plan) []string {
	var out []string
	for _, b := range plan.Batches {
		for _, e := range b.Entries {
			for _, r := range e.Related {
				out = append(out, r.Path+":"+r.Name)
			}
		}
	}
	sort.Strings(out)
	return out
}

func TestRelatedGoResolvesModuleImports(t *testing.T) {
	tree := fakeTree{
		"go.mod": "module example.com/app\n\ngo 1.22\n",
		"cmd/main.go": `package main

import (
	"fmt"

	"example.com/app/internal/store"
)

func main() {
	s := store.Open("x")
	fmt.Println(s, store.DefaultTimeout)
}
`,
		"internal/store/store.go": `package store

import "time"

// DefaultTimeout bounds a query.
const DefaultTimeout = 5 * time.Second

// Open opens a store. It never returns an error: a bad path panics.
func Open(path string) *Store {
	if path == "" {
		panic("empty path")
	}
	return &Store{}
}

type Store struct{}

func (s *Store) Close() {}
`,
		"internal/store/store_test.go": "package store\n\nfunc Open() {}\n",
	}

	plan := assembleRelated(t, relatedConfig(), tree, true, "cmd/main.go")
	got := relatedNames(plan)
	want := []string{"internal/store/store.go:DefaultTimeout", "internal/store/store.go:Open"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("related = %v, want %v", got, want)
	}

	e := plan.Batches[0].Entries[0]
	var open Related
	for _, r := range e.Related {
		if r.Name == "Open" {
			open = r
		}
	}
	if !strings.Contains(open.Snippet, "panic(\"empty path\")") || !strings.Contains(open.Snippet, "// Open opens a store") {
		t.Errorf("Open's snippet lacks its body or doc comment:\n%s", open.Snippet)
	}
	if open.Line != 8 {
		t.Errorf("Open starts at line %d, want 8 (the doc comment)", open.Line)
	}
	if plan.RelatedDefinitions != 2 || len(plan.RelatedFiles) != 1 {
		t.Errorf("plan records %d definitions from %v", plan.RelatedDefinitions, plan.RelatedFiles)
	}

	rendered := Render(e)
	if !strings.Contains(rendered, "from files it does not touch") || !strings.Contains(rendered, "internal/store/store.go (from line 8)") {
		t.Errorf("rendered entry lacks the related section:\n%s", rendered)
	}
	if !strings.Contains(rendered, "     8  // Open opens a store") {
		t.Errorf("related lines are not numbered from their real line:\n%s", rendered)
	}
}

func TestRelatedGoNeedsALister(t *testing.T) {
	tree := fakeTree{
		"go.mod":     "module example.com/app\n",
		"main.go":    "package main\n\nimport \"example.com/app/lib\"\n\nfunc main() { lib.Do() }\n",
		"lib/lib.go": "package lib\n\nfunc Do() {}\n",
	}
	plan := assembleRelated(t, relatedConfig(), tree, false, "main.go")
	if n := len(relatedNames(plan)); n != 0 {
		t.Errorf("attached %d definitions with no way to list a package directory; guessing filenames is not resolution", n)
	}
}

func TestRelatedTypeScriptResolvesRelativeImports(t *testing.T) {
	tree := fakeTree{
		"src/handler.ts": `import { remember, type Entry } from "./cache";
import * as limits from "../limits.js";
import Logger from "./log/index";

export function handle(q: string) {
  const log = new Logger();
  return remember(q, () => limits.MAX_HITS);
}
`,
		"src/cache.ts": `type Entry = { value: unknown };

/**
 * remember memoizes load() under key, forever.
 */
export async function remember<T>(key: string, load: () => Promise<T>): Promise<T> {
  return load();
}
`,
		"limits.ts":        "/** MAX_HITS is the most results one search answers with. */\nexport const MAX_HITS = 50;\n",
		"src/log/index.ts": "export default class Logger {\n  info(msg: string) {}\n}\n",
	}

	plan := assembleRelated(t, relatedConfig(), tree, true, "src/handler.ts")
	got := relatedNames(plan)
	want := []string{"limits.ts:MAX_HITS", "src/cache.ts:remember", "src/log/index.ts:default"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("related = %v, want %v", got, want)
	}
	for _, r := range plan.Batches[0].Entries[0].Related {
		if r.Name == "remember" && !strings.Contains(r.Snippet, "memoizes load() under key, forever") {
			t.Errorf("remember's doc comment was not carried:\n%s", r.Snippet)
		}
	}
}

func TestRelatedTypeScriptWorksWithoutALister(t *testing.T) {
	tree := fakeTree{
		"a.ts": "import { f } from \"./b\";\nf();\n",
		"b.ts": "export function f() {}\n",
	}
	plan := assembleRelated(t, relatedConfig(), tree, false, "a.ts")
	if got := relatedNames(plan); strings.Join(got, ",") != "b.ts:f" {
		t.Errorf("related = %v; a relative import can be resolved by reading candidates", got)
	}
}

func TestRelatedPythonResolvesModules(t *testing.T) {
	tree := fakeTree{
		"app/views.py": `from .auth import check_token, User
from app.db import query
import app.util as util

def view(req):
    check_token(req)
    u = User(req.name)
    return query(util.escape(req.q)), u
`,
		"app/auth.py": `import hmac


class User:
    """A signed-in user."""

    def __init__(self, name):
        self.name = name


@lru_cache
def check_token(req):
    # NOTE: does not check expiry
    return hmac.compare_digest(req.token, "x")


def unused():
    pass
`,
		"app/db.py":   "def query(sql):\n    return run(sql)\n",
		"app/util.py": "def escape(s):\n    return s\n",
	}

	plan := assembleRelated(t, relatedConfig(), tree, true, "app/views.py")
	got := relatedNames(plan)
	want := []string{"app/auth.py:User", "app/auth.py:check_token", "app/db.py:query", "app/util.py:escape"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("related = %v, want %v", got, want)
	}
	for _, r := range plan.Batches[0].Entries[0].Related {
		switch r.Name {
		case "check_token":
			if !strings.HasPrefix(r.Snippet, "@lru_cache") || !strings.Contains(r.Snippet, "does not check expiry") || strings.Contains(r.Snippet, "def unused") {
				t.Errorf("check_token snippet is wrong:\n%s", r.Snippet)
			}
		case "User":
			if !strings.Contains(r.Snippet, "self.name = name") || strings.Contains(r.Snippet, "check_token") {
				t.Errorf("User snippet is wrong:\n%s", r.Snippet)
			}
		}
	}
}

func TestRelatedSkipsChangedFilesAndUnusedImports(t *testing.T) {
	tree := fakeTree{
		"a.ts": "import { f } from \"./b\";\nimport { g } from \"./c\";\nf();\n",
		"b.ts": "export function f() {}\n",
		"c.ts": "export function g() {}\n",
	}
	// b.ts is in the change too, so it is under review and not context.
	plan := assembleRelated(t, relatedConfig(), tree, true, "a.ts", "b.ts")
	if got := relatedNames(plan); len(got) != 0 {
		t.Errorf("related = %v; a changed file is under review, and g is imported but unused", got)
	}
}

func TestRelatedDefaultsAttachDefinitionsButNotCallersAndAreBudgeted(t *testing.T) {
	tree := fakeTree{"a.ts": "import { f } from \"./b\";\nf();\n", "b.ts": "export function f() {}\n"}
	plan := assembleRelated(t, config.Defaults(), tree, true, "a.ts")
	if got := relatedNames(plan); strings.Join(got, ",") != "b.ts:f" {
		t.Errorf("defaults attached %v, want the one definition the change imports", got)
	}

	// The caller walk is a separate switch, off by default: a redefined
	// export with a caller in the tree attaches nothing under the defaults.
	lib := "export function g(): number {\n  return 1;\n}\n"
	callerTree := fakeTree{"package.json": "{}", "src/lib.ts": lib, "src/use.ts": "import { g } from \"./lib\";\nexport function h() { return g(); }\n"}
	walk, err := AssembleWith(context.Background(), config.Defaults(), diff.Files{modifiedFile("src/lib.ts", lib, 2)}, callerTree.fetch, callerTree.list)
	if err != nil {
		t.Fatal(err)
	}
	if got := callerNames(walk); len(got) != 0 {
		t.Errorf("defaults attached callers %v; review.related_context_callers is off by default", got)
	}
	off := config.Defaults()
	off.Review.RelatedContext = false
	if plan := assembleRelated(t, off, tree, true, "a.ts"); len(relatedNames(plan)) != 0 {
		t.Errorf("related_context: false still attached %v", relatedNames(plan))
	}

	// A budget too small for the one definition attaches nothing, and the
	// entry's token count is unchanged.
	cfg := relatedConfig()
	cfg.Review.RelatedContextTokens = 1
	plan = assembleRelated(t, cfg, tree, true, "a.ts")
	if n := len(relatedNames(plan)); n != 0 {
		t.Errorf("related context attached %d definitions over a budget of one token", n)
	}
}

func TestRelatedNeverReadsOutsideTheTree(t *testing.T) {
	// Every path is handed to the fetcher; an import that climbs out of the
	// repository must not turn into a read the fetcher would refuse anyway.
	var asked []string
	tree := fakeTree{"a.ts": "import { f } from \"../../etc/passwd\";\nf();\n"}
	fetch := func(ctx context.Context, p string) ([]byte, error) {
		asked = append(asked, p)
		return tree.fetch(ctx, p)
	}
	files := diff.Files{addedFile("a.ts", tree["a.ts"])}
	if _, err := AssembleWith(context.Background(), relatedConfig(), files, fetch, nil); err != nil {
		t.Fatal(err)
	}
	for _, p := range asked {
		if strings.HasPrefix(p, "..") || strings.HasPrefix(p, "/") {
			t.Errorf("asked the fetcher for %q", p)
		}
	}
}

func TestRelatedCapsALongDefinition(t *testing.T) {
	var body strings.Builder
	body.WriteString("export function big() {\n")
	for i := range 200 {
		fmt.Fprintf(&body, "  step%d();\n", i)
	}
	body.WriteString("}\n")
	tree := fakeTree{"a.ts": "import { big } from \"./b\";\nbig();\n", "b.ts": body.String()}
	plan := assembleRelated(t, relatedConfig(), tree, true, "a.ts")
	r := plan.Batches[0].Entries[0].Related
	if len(r) != 1 {
		t.Fatalf("related = %d", len(r))
	}
	if lines := strings.Count(r[0].Snippet, "\n"); lines > maxDefinitionLines+1 || !strings.Contains(r[0].Snippet, "definition continues") {
		t.Errorf("a 200-line function was attached whole (%d lines) or without saying it was cut", lines)
	}
}
