package bundle

import (
	"context"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"path"
	"regexp"
	"sort"
	"strings"

	llms "github.com/nocturnium/llm-go-sdk/v6"

	"github.com/jdziat/open-nitpick/internal/diff"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// Related context: the definitions a changed file imports from elsewhere in
// the repository and uses on a changed line.
//
// The prompt tells the model not to speculate about code it was not shown,
// and a review that obeys that rule stays quiet about every defect whose
// nature turns on what a called function does. Attaching the callee is the
// cheapest way to show it. It is bounded three ways: to definitions actually
// named on a changed line, so an import list is not an invitation to dump a
// package; to a token budget of its own, so it cannot crowd out the file
// under review; and to what the repository's own files define, so nothing
// under node_modules or a module cache is ever read.
//
// Three languages are resolved. Each has a hand-written reader rather than a
// parser, because the question is narrow — where is NAME defined, and what do
// its first lines say — and a reader that gets that wrong attaches the wrong
// snippet, which costs nothing worse than a model reading one definition it
// did not need. What it must never do is read a file outside the checkout,
// and every path here is resolved relative to the repository root and handed
// to the same fetcher the diff's own files come through.

// Related is one definition attached from a file the change did not touch.
type Related struct {
	// Path is the file the definition lives in.
	Path string

	// Name is the identifier defined.
	Name string

	// Line is the 1-based line Snippet starts on in Path.
	Line int

	// Snippet is the definition's own lines, un-numbered.
	Snippet string

	// Calls names the redefined symbol this snippet calls, when the snippet
	// is a caller attached by callers.go rather than a definition the
	// change uses. Empty for a definition.
	Calls string

	// Constant marks a one-line constant a caller passes, and Caller names
	// that caller. It is attached only when the caller was, and rendered
	// apart from it, because a constant calls nothing.
	Constant bool
	Caller   string
}

// DirLister names the entries of a directory at the reviewed revision, with a
// trailing slash on subdirectories. Nil means directories cannot be listed,
// which rules out Go — a package is a directory — and makes the other two
// probe for files by name instead.
type DirLister func(ctx context.Context, dir string) ([]string, error)

// maxDefinitionLines caps one attached definition. Past this a snippet is cut
// and says so; a model reading a 400-line function was not going to find the
// changed line's contract in it anyway.
const maxDefinitionLines = 80

// maxRelatedPerFile caps how many definitions one changed file attaches, so a
// file that touches forty helpers attaches the forty most-used rather than
// everything the budget can hold.
const maxRelatedPerFile = 12

// relatedCollector resolves imports and extracts definitions for one plan,
// caching every read so two files importing the same helper read it once.
type relatedCollector struct {
	ctx   context.Context
	fetch ContentFetcher
	list  DirLister

	// changed is every path in the diff. Those are under review already and
	// are never attached as context: the model has them, and a definition
	// from a changed file would be shown at whichever revision the fetcher
	// answers for, which is not necessarily the one in the diff.
	changed map[string]bool

	files   map[string]string
	missing map[string]bool
	dirs    map[string][]string
	goMods  map[string]goModule

	// attached records definitions already given to an earlier entry, so a
	// plan attaches each once.
	attached map[string]bool

	// walked counts the candidate files the caller walk has charged to this
	// plan, against maxCallerFiles, and truncated records that the ceiling
	// stopped a walk with candidates unlisted. See callers.go.
	walked    int
	truncated bool

	// maxBytes refuses a file larger than this after fetching it, so the
	// caller walk cannot scan a generated bundle that escaped
	// callerSkipDirs. It applies to every read the collector makes,
	// definitions included, which the review's max_file_bytes already
	// refuses for the files under review. Zero means no limit.
	maxBytes int
}

// goModule is one go.mod's answer: the module path and the directory it sits
// in, or empty when no go.mod exists above a file.
type goModule struct {
	Path string
	Dir  string
}

func newRelatedCollector(ctx context.Context, files diff.Files, fetch ContentFetcher, list DirLister) *relatedCollector {
	changed := make(map[string]bool, len(files))
	for _, f := range files {
		changed[f.Path] = true
		if f.OldPath != "" {
			changed[f.OldPath] = true
		}
	}
	return &relatedCollector{
		ctx: ctx, fetch: fetch, list: list, changed: changed,
		files: map[string]string{}, missing: map[string]bool{},
		dirs: map[string][]string{}, goMods: map[string]goModule{},
		attached: map[string]bool{},
	}
}

// read fetches a file once.
func (c *relatedCollector) read(p string) (string, bool) {
	if content, ok := c.files[p]; ok {
		return content, true
	}
	if c.missing[p] || c.fetch == nil {
		return "", false
	}
	data, err := c.fetch(c.ctx, p)
	if err == nil && c.maxBytes > 0 && len(data) > c.maxBytes {
		err = errors.New("larger than max_file_bytes")
	}
	if err != nil || !isText(data) {
		c.missing[p] = true
		return "", false
	}
	c.files[p] = string(data)
	return c.files[p], true
}

// isText refuses NUL-bearing content, which is the same test fetchContent
// applies to files under review.
func isText(data []byte) bool {
	for _, b := range data {
		if b == 0 {
			return false
		}
	}
	return true
}

// entries lists a directory once, or answers nil when it cannot.
func (c *relatedCollector) entries(dir string) []string {
	dir = strings.Trim(dir, "/")
	if dir == "." {
		dir = ""
	}
	if names, ok := c.dirs[dir]; ok {
		return names
	}
	if c.list == nil {
		c.dirs[dir] = nil
		return nil
	}
	names, err := c.list(c.ctx, dir)
	if err != nil {
		names = nil
	}
	c.dirs[dir] = names
	return names
}

// exists reports whether a file exists, by listing its directory when that is
// possible and by reading it when it is not.
func (c *relatedCollector) exists(p string) bool {
	if _, ok := c.files[p]; ok {
		return true
	}
	if c.list != nil {
		for _, name := range c.entries(path.Dir(p)) {
			if name == path.Base(p) {
				return true
			}
		}
		return false
	}
	_, ok := c.read(p)
	return ok
}

// collect attaches related definitions to e, spending at most budget tokens
// as estimated by est. It returns the tokens spent.
func (c *relatedCollector) collect(e *Entry, budget int, est *llms.TokenEstimator) int {
	if budget <= 0 {
		return 0
	}

	if !e.HasContent() || e.File.Kind == diff.ChangeDeleted {
		return 0
	}
	wants := c.definitionWants(e)

	// Most-used first, so the cap keeps the definitions the change leans on.
	sort.SliceStable(wants, func(i, j int) bool {
		if wants[i].uses != wants[j].uses {
			return wants[i].uses > wants[j].uses
		}
		if wants[i].file != wants[j].file {
			return wants[i].file < wants[j].file
		}
		return wants[i].name < wants[j].name
	})

	// Callers come after every definition the change uses: the callee is
	// what the changed line means, the caller is what it breaks, and when
	// the budget holds only one the first is the one to keep. The walk is
	// skipped when nothing it finds could be attached.
	fresh := 0
	for _, w := range wants {
		if !c.attached[w.file+"\x00"+w.name+"\x00"+w.calls] {
			fresh++
		}
	}
	if len(e.Related)+fresh < maxRelatedPerFile && budget >= minCallerBudget {
		callers := c.callerWants(e)
		sortCallers(callers)
		wants = append(wants, callers...)
	}
	if len(wants) == 0 {
		return 0
	}

	return c.attach(e, wants, budget, est)
}

// minCallerBudget is the fewest tokens worth walking the tree for: one
// short caller. Below it the walk would fetch files and attach nothing.
const minCallerBudget = 200

// definitionWants resolves the definitions the changed lines of e use.
func (c *relatedCollector) definitionWants(e *Entry) []want {
	var wants []want
	switch strings.ToLower(path.Ext(e.File.Path)) {
	case ".go":
		wants = c.goWants(e)
	case ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs":
		wants = c.tsWants(e)
	case ".py":
		wants = c.pyWants(e)
	case ".rs":
		wants = c.rustWants(e)
	case ".rb":
		wants = c.rubyWants(e)
	case ".java":
		wants = c.jvmWants(e, ".java")
	case ".kt", ".kts":
		wants = c.jvmWants(e, ".kt")
	case ".c", ".cc", ".cpp", ".cxx", ".h", ".hh", ".hpp", ".hxx":
		wants = c.cWants(e)
	default:
		return nil
	}
	return wants
}

// attach reads and renders wants into e.Related in order, within budget.
func (c *relatedCollector) attach(e *Entry, wants []want, budget int, est *llms.TokenEstimator) int {
	spent := 0
	// callers attached for this entry, by file and name, so a constant is
	// never attached without the caller it belongs to.
	callers := map[string]bool{}
	for _, w := range wants {
		if len(e.Related) >= maxRelatedPerFile {
			break
		}
		key := w.file + "\x00" + w.name + "\x00" + w.calls
		if c.attached[key] {
			continue
		}
		if w.constant && !callers[w.file+"\x00"+w.caller] {
			continue
		}
		content, ok := c.read(w.file)
		if !ok {
			continue
		}
		def, ok := w.extract(content, w.name)
		if !ok {
			continue
		}
		def.Path = w.file
		def.Name = w.name
		def.Calls = w.calls
		def.Constant = w.constant
		def.Caller = w.caller

		cost := est.EstimateTokens(renderRelated(def))
		if spent+cost > budget {
			continue
		}
		spent += cost
		c.attached[key] = true
		if w.calls != "" && !w.constant {
			callers[w.file+"\x00"+w.name] = true
		}
		e.Related = append(e.Related, def)
	}
	return spent
}

// want is one definition a changed file may need: where to look, what to look
// for, how to read it out, and how often the change names it.
type want struct {
	file    string
	name    string
	uses    int
	extract func(content, name string) (Related, bool)

	// calls is set on a caller want: the redefined symbol the snippet calls.
	calls string

	// constant marks a one-line constant a caller passes, and caller names
	// that caller.
	constant bool
	caller   string
}

// addedText joins the lines the change added, which is where a used name has
// to appear for its definition to be worth attaching. Removed lines are not
// consulted: a helper the change stopped calling is not context for the
// change.
//
// Import statements are left out. An added import names the binding by
// definition, and counting it would attach every newly imported name whether
// or not the change calls it.
func addedText(f *diff.File) string {
	var b strings.Builder
	for _, h := range f.Hunks {
		for _, l := range h.Lines {
			if l.Kind != diff.LineAdded || importLine.MatchString(l.Content) {
				continue
			}
			b.WriteString(l.Content)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// importLine matches the import forms of the three resolved languages, plus a
// re-export, which names a binding without using it.
var importLine = regexp.MustCompile(`^\s*(?:import\b|from\s+[\w.]+\s+import\b|export\s+(?:\*|\{[^}]*\})\s+from\b)`)

// countUses counts whole-word occurrences of ident in text.
func countUses(text, ident string) int {
	if ident == "" {
		return 0
	}
	re := regexp.MustCompile(`\b` + regexp.QuoteMeta(ident) + `\b`)
	return len(re.FindAllStringIndex(text, -1))
}

// memberUses counts the distinct members of ns used as ns.Member in text,
// keyed by member.
func memberUses(text, ns string) map[string]int {
	re := regexp.MustCompile(`\b` + regexp.QuoteMeta(ns) + `\.([A-Za-z_]\w*)`)
	out := map[string]int{}
	for _, m := range re.FindAllStringSubmatch(text, -1) {
		out[m[1]]++
	}
	return out
}

// --- Go ----------------------------------------------------------------------

// goModuleFor finds the go.mod governing a file, walking up from its
// directory. Cached per directory, and a miss is cached too.
func (c *relatedCollector) goModuleFor(dir string) goModule {
	dir = strings.Trim(dir, "/")
	if m, ok := c.goMods[dir]; ok {
		return m
	}

	var found goModule
	for d := dir; ; {
		modPath := "go.mod"
		if d != "" && d != "." {
			modPath = d + "/go.mod"
		}
		if content, ok := c.read(modPath); ok {
			if m := goModulePath(content); m != "" {
				found = goModule{Path: m, Dir: d}
				break
			}
		}
		if d == "" || d == "." {
			break
		}
		parent := path.Dir(d)
		if parent == "." {
			parent = ""
		}
		d = parent
	}
	c.goMods[dir] = found
	return found
}

var goModuleLine = regexp.MustCompile(`(?m)^module\s+(\S+)`)

func goModulePath(gomod string) string {
	m := goModuleLine.FindStringSubmatch(gomod)
	if m == nil {
		return ""
	}
	return strings.Trim(m[1], `"`)
}

// goWants reads the file's imports with go/parser, keeps those inside the
// module, and returns a want for every exported name the change uses through
// each of them.
func (c *relatedCollector) goWants(e *Entry) []want {
	if c.list == nil {
		// A Go package is a directory of files, and without a listing there
		// is no way to know which file defines a name short of guessing.
		return nil
	}

	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, e.File.Path, e.Content, parser.ImportsOnly)
	if err != nil || parsed == nil {
		return nil
	}

	dir := path.Dir(e.File.Path)
	if dir == "." {
		dir = ""
	}
	mod := c.goModuleFor(dir)
	if mod.Path == "" {
		return nil
	}

	added := addedText(e.File)
	var wants []want
	for _, spec := range parsed.Imports {
		imp := strings.Trim(spec.Path.Value, `"`)
		if imp != mod.Path && !strings.HasPrefix(imp, mod.Path+"/") {
			continue
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(imp, mod.Path), "/")
		pkgDir := path.Join(mod.Dir, rel)
		if pkgDir == "." {
			pkgDir = ""
		}

		local := goPackageName(imp)
		if spec.Name != nil {
			switch spec.Name.Name {
			case "_", ".":
				continue
			default:
				local = spec.Name.Name
			}
		}

		uses := memberUses(added, local)
		if len(uses) == 0 {
			continue
		}

		var goFiles []string
		for _, name := range c.entries(pkgDir) {
			if strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") {
				goFiles = append(goFiles, path.Join(pkgDir, name))
			}
		}

		for name, n := range uses {
			for _, file := range goFiles {
				if c.changed[file] {
					continue
				}
				wants = append(wants, want{file: file, name: name, uses: n, extract: goDefinition})
			}
		}

		// Methods called on values of this package's types. The struct
		// declaration is rarely where the contract is written; the method's
		// doc comment is.
		var unchanged []string
		for _, file := range goFiles {
			if !c.changed[file] {
				unchanged = append(unchanged, file)
			}
		}
		for _, typeName := range c.goTypeNames(unchanged) {
			wants = append(wants, c.goMethodWants(added, unchanged, typeName)...)
		}
	}
	return wants
}

// goTypeNames lists the types the given package files declare.
func (c *relatedCollector) goTypeNames(files []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, file := range files {
		content, ok := c.read(file)
		if !ok {
			continue
		}
		for _, m := range regexp.MustCompile(`(?m)^type\s+([A-Z]\w*)\b`).FindAllStringSubmatch(content, -1) {
			if !seen[m[1]] {
				seen[m[1]] = true
				out = append(out, m[1])
			}
		}
	}
	return out
}

// goPackageName guesses a package's name from its import path: the last
// element, or the one before a major-version suffix.
func goPackageName(imp string) string {
	parts := strings.Split(imp, "/")
	last := parts[len(parts)-1]
	if len(parts) > 1 && regexp.MustCompile(`^v\d+$`).MatchString(last) {
		last = parts[len(parts)-2]
	}
	return strings.ReplaceAll(strings.TrimPrefix(last, "go-"), "-", "")
}

// goDefinition finds a package-level definition of name.
func goDefinition(content, name string) (Related, bool) {
	lines := splitLines(content)
	q := regexp.QuoteMeta(name)
	top := regexp.MustCompile(`^(?:func|type|var|const)\s+` + q + `\b`)
	inBlock := regexp.MustCompile(`^\s+` + q + `\b`)
	blockOpen := regexp.MustCompile(`^(?:var|const)\s*\($`)

	block := false
	for i, line := range lines {
		switch {
		case blockOpen.MatchString(line):
			block = true
			continue
		case block && strings.HasPrefix(line, ")"):
			block = false
			continue
		case block && inBlock.MatchString(line):
			start := withLeadingComments(lines, i, "//")
			return Related{Line: start + 1, Snippet: join(lines[start : i+1])}, true
		case !block && top.MatchString(line):
			start := withLeadingComments(lines, i, "//")
			end := braceExtent(lines, i)
			return Related{Line: start + 1, Snippet: join(lines[start:end])}, true
		}
	}
	return Related{}, false
}

// --- TypeScript / JavaScript --------------------------------------------------

var (
	tsImport  = regexp.MustCompile(`(?m)^\s*import\s+(?:type\s+)?([^'";]+?)\s+from\s+['"]([^'"]+)['"]`)
	tsRequire = regexp.MustCompile(`(?m)^\s*(?:const|let|var)\s+([^=]+?)\s*=\s*require\(\s*['"]([^'"]+)['"]\s*\)`)
	tsNamed   = regexp.MustCompile(`\{([^}]*)\}`)
	tsStar    = regexp.MustCompile(`\*\s+as\s+([A-Za-z_$][\w$]*)`)
	tsExts    = []string{".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs"}
)

// tsBinding is one imported name: what the source exports it as, and what the
// importing file calls it.
type tsBinding struct {
	exported, local string
}

// tsBindings reads the names an import clause binds.
func tsBindings(clause string) (named []tsBinding, ns string) {
	clause = strings.TrimSpace(clause)
	if m := tsStar.FindStringSubmatch(clause); m != nil {
		ns = m[1]
	}
	if m := tsNamed.FindStringSubmatch(clause); m != nil {
		for part := range strings.SplitSeq(m[1], ",") {
			part = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(part), "type "))
			if part == "" {
				continue
			}
			exported, local := part, part
			if a, b, ok := strings.Cut(part, " as "); ok {
				exported, local = strings.TrimSpace(a), strings.TrimSpace(b)
			}
			named = append(named, tsBinding{exported: exported, local: local})
		}
		clause = tsNamed.ReplaceAllString(clause, "")
	}
	// Whatever is left before a comma is the default import.
	if def := strings.TrimSpace(strings.Split(tsStar.ReplaceAllString(clause, ""), ",")[0]); def != "" &&
		regexp.MustCompile(`^[A-Za-z_$][\w$]*$`).MatchString(def) {
		named = append(named, tsBinding{exported: "default", local: def})
	}
	return named, ns
}

// tsResolve turns a relative import specifier into a repository path, or "".
func (c *relatedCollector) tsResolve(from, spec string) string {
	if strings.HasPrefix(spec, "./") || strings.HasPrefix(spec, "../") {
		base := path.Join(path.Dir(from), spec)
		base = strings.TrimPrefix(base, "./")
		// An import that climbs out of the repository names nothing this
		// review may read. The fetcher would refuse it too; not asking is
		// the point.
		if base == ".." || strings.HasPrefix(base, "../") || strings.HasPrefix(base, "/") {
			return ""
		}
		return c.tsResolveBase(base)
	}
	// A bare specifier is a package unless the nearest tsconfig maps it.
	for _, base := range c.tsConfigFor(from).tsAliasCandidates(spec) {
		if file := c.tsResolveBase(base); file != "" {
			return file
		}
	}
	return ""
}

func (c *relatedCollector) tsWants(e *Entry) []want {
	added := addedText(e.File)
	var wants []want

	consider := func(clause, spec string) {
		target := c.tsResolve(e.File.Path, spec)
		if target == "" || c.changed[target] {
			return
		}
		named, ns := tsBindings(clause)
		for _, b := range named {
			if n := countUses(added, b.local); n > 0 {
				// A barrel that re-exports the name is followed to the file
				// that defines it.
				file := c.tsDefiningFile(target, b.exported)
				if file == "" {
					file = target
				}
				if c.changed[file] {
					continue
				}
				wants = append(wants, want{file: file, name: b.exported, uses: n, extract: tsDefinition})
			}
		}
		if ns != "" {
			for name, n := range memberUses(added, ns) {
				wants = append(wants, want{file: target, name: name, uses: n, extract: tsDefinition})
			}
		}
	}

	for _, m := range tsImport.FindAllStringSubmatch(e.Content, -1) {
		consider(m[1], m[2])
	}
	for _, m := range tsRequire.FindAllStringSubmatch(e.Content, -1) {
		consider(m[1], m[2])
	}
	return wants
}

// tsDefinition finds a top-level definition of name, exported or not.
func tsDefinition(content, name string) (Related, bool) {
	lines := splitLines(content)
	var re *regexp.Regexp
	if name == "default" {
		re = regexp.MustCompile(`^export\s+default\b`)
	} else {
		q := regexp.QuoteMeta(name)
		re = regexp.MustCompile(`^(?:export\s+)?(?:default\s+)?(?:declare\s+)?(?:abstract\s+)?(?:async\s+)?` +
			`(?:function\s*\*?|class|const|let|var|interface|type|enum|namespace)\s+` + q + `\b`)
	}

	for i, line := range lines {
		if !re.MatchString(line) {
			continue
		}
		start := withLeadingComments(lines, i, "/**", "*", "*/", "//")
		end := braceExtent(lines, i)
		if end == i+1 && !strings.HasSuffix(strings.TrimSpace(line), ";") && !strings.Contains(line, "{") {
			// A multi-line type alias or expression: read on to the
			// terminator.
			for end < len(lines) && end-i < maxDefinitionLines {
				t := strings.TrimSpace(lines[end])
				end++
				if strings.HasSuffix(t, ";") || t == "" {
					break
				}
			}
		}
		return Related{Line: start + 1, Snippet: join(lines[start:end])}, true
	}
	return Related{}, false
}

// --- Python ------------------------------------------------------------------

var (
	pyFrom   = regexp.MustCompile(`(?m)^\s*from\s+([\w.]+)\s+import\s+(\(?[^\n]*)`)
	pyImport = regexp.MustCompile(`(?m)^\s*import\s+([\w.]+)(?:\s+as\s+(\w+))?\s*$`)
)

// pyResolve turns a module reference into a repository path, or "".
func (c *relatedCollector) pyResolve(from, module string) string {
	dots := 0
	for dots < len(module) && module[dots] == '.' {
		dots++
	}
	rest := strings.ReplaceAll(module[dots:], ".", "/")

	var roots []string
	dir := path.Dir(from)
	if dir == "." {
		dir = ""
	}
	if dots > 0 {
		base := dir
		for range dots - 1 {
			if base == "" {
				return ""
			}
			base = path.Dir(base)
			if base == "." {
				base = ""
			}
		}
		roots = []string{base}
	} else {
		// An absolute import is resolved from the repository root and from
		// each ancestor of the importing file, which covers src/ layouts and
		// packages run from their own directory.
		roots = append(roots, "")
		for d := dir; d != ""; {
			roots = append(roots, d)
			d = path.Dir(d)
			if d == "." {
				d = ""
			}
		}
	}

	for _, root := range roots {
		if rest == "" {
			if p := path.Join(root, "__init__.py"); c.exists(p) {
				return p
			}
			continue
		}
		if p := path.Join(root, rest+".py"); c.exists(p) {
			return p
		}
		if p := path.Join(root, rest, "__init__.py"); c.exists(p) {
			return p
		}
	}
	return ""
}

func (c *relatedCollector) pyWants(e *Entry) []want {
	added := addedText(e.File)
	var wants []want

	for _, m := range pyFrom.FindAllStringSubmatch(e.Content, -1) {
		target := c.pyResolve(e.File.Path, m[1])
		if target == "" || c.changed[target] {
			continue
		}
		names := m[2]
		if strings.HasPrefix(names, "(") {
			// A parenthesised list may continue on later lines; take up to
			// the closing parenthesis.
			idx := strings.Index(e.Content, names)
			if idx >= 0 {
				if end := strings.Index(e.Content[idx:], ")"); end >= 0 {
					names = e.Content[idx : idx+end]
				}
			}
			names = strings.Trim(names, "()")
		}
		for part := range strings.SplitSeq(names, ",") {
			part = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(part), "\\"))
			if part == "" || part == "*" {
				continue
			}
			exported, local := part, part
			if a, b, ok := strings.Cut(part, " as "); ok {
				exported, local = strings.TrimSpace(a), strings.TrimSpace(b)
			}
			if n := countUses(added, local); n > 0 {
				// A package __init__ that re-exports the name is followed
				// to the module that defines it.
				file := c.pyDefiningFile(target, exported)
				if file == "" {
					file = target
				}
				if c.changed[file] {
					continue
				}
				wants = append(wants, want{file: file, name: exported, uses: n, extract: pyDefinition})
			}
		}
	}

	for _, m := range pyImport.FindAllStringSubmatch(e.Content, -1) {
		target := c.pyResolve(e.File.Path, m[1])
		if target == "" || c.changed[target] {
			continue
		}
		local := m[2]
		if local == "" {
			local = m[1]
		}
		for name, n := range memberUses(added, local) {
			wants = append(wants, want{file: target, name: name, uses: n, extract: pyDefinition})
		}
	}
	return wants
}

// pyDefinition finds a module-level def, class or assignment of name.
func pyDefinition(content, name string) (Related, bool) {
	lines := splitLines(content)
	q := regexp.QuoteMeta(name)
	re := regexp.MustCompile(`^(?:async\s+)?(?:def|class)\s+` + q + `\b|^` + q + `\s*(?::|=)`)

	for i, line := range lines {
		if !re.MatchString(line) {
			continue
		}
		start := withLeadingComments(lines, i, "@")
		end := i + 1
		for end < len(lines) && end-start < maxDefinitionLines {
			t := lines[end]
			if strings.TrimSpace(t) != "" && !strings.HasPrefix(t, " ") && !strings.HasPrefix(t, "\t") {
				break
			}
			end++
		}
		// Trailing blank lines belong to whatever follows.
		for end > i+1 && strings.TrimSpace(lines[end-1]) == "" {
			end--
		}
		snippet := join(lines[start:end])
		if end-start >= maxDefinitionLines {
			snippet += "\n# … definition continues"
		}
		return Related{Line: start + 1, Snippet: snippet}, true
	}
	return Related{}, false
}

// --- Shared readers -----------------------------------------------------------

// withLeadingComments walks back from line i over contiguous lines beginning
// with one of the prefixes, so a definition carries its doc comment.
func withLeadingComments(lines []string, i int, prefixes ...string) int {
	start := i
	for start > 0 {
		t := strings.TrimSpace(lines[start-1])
		matched := false
		for _, p := range prefixes {
			if strings.HasPrefix(t, p) {
				matched = true
				break
			}
		}
		if !matched {
			break
		}
		start--
	}
	return start
}

// braceExtent returns the index one past the line that closes the brace block
// opened on line i, or i+1 when the line opens none, capped at
// maxDefinitionLines with a marker appended by the caller's join.
func braceExtent(lines []string, i int) int {
	depth := 0
	opened := false
	for j := i; j < len(lines); j++ {
		for _, r := range lines[j] {
			switch r {
			case '{':
				depth++
				opened = true
			case '}':
				depth--
			}
		}
		if opened && depth <= 0 {
			return j + 1
		}
		if !opened && j > i {
			return i + 1
		}
		if j-i+1 >= maxDefinitionLines {
			return j + 1
		}
	}
	return len(lines)
}

func join(lines []string) string {
	s := strings.Join(lines, "\n")
	if len(lines) >= maxDefinitionLines {
		s += "\n… definition continues"
	}
	return s
}

// renderRelated formats one attached definition for the prompt.
func renderRelated(r Related) string {
	var b strings.Builder
	switch {
	case r.Constant:
		fmt.Fprintf(&b, "##### %s (line %d), a constant %s passes\n\n```\n", promptSafe(r.Path), r.Line, promptSafe(r.Caller))
	case r.Calls != "":
		fmt.Fprintf(&b, "##### %s (from line %d), calls %s\n\n```\n", promptSafe(r.Path), r.Line, promptSafe(r.Calls))
	default:
		fmt.Fprintf(&b, "##### %s (from line %d)\n\n```\n", promptSafe(r.Path), r.Line)
	}
	for i, line := range strings.Split(r.Snippet, "\n") {
		fmt.Fprintf(&b, "%6d  %s\n", r.Line+i, line)
	}
	b.WriteString("```\n")
	return b.String()
}

// ListerFrom adapts a provider that can list directories into a DirLister, or
// returns nil when it cannot.
func ListerFrom(p vcs.Provider, ref vcs.Ref) DirLister {
	l, ok := p.(vcs.DirLister)
	if !ok {
		return nil
	}
	return func(ctx context.Context, dir string) ([]string, error) {
		names, err := l.ListDir(ctx, ref, dir)
		if errors.Is(err, vcs.ErrNotFound) {
			return nil, err
		}
		return names, err
	}
}
