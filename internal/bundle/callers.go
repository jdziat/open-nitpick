package bundle

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/jdziat/open-nitpick/internal/diff"
)

// Callers: the functions in untouched files that call something the change
// redefined.
//
// related.go attaches what a changed line calls. This attaches the reverse:
// when a change rewrites an exported function or method, the defect is often
// not in the new body but in a caller nobody touched, which still assumes the
// old contract. A reviewer shown only the diff cannot see that caller, and
// the prompt forbids guessing at it, so the review stays silent. Attaching
// the first lines of each caller is the cheapest way to show it.
//
// It is bounded the same three ways as related context: to symbols the diff
// actually redefined, so a whitespace-only change attaches nothing; to the
// shared related-context budget and per-file cap; and to files the
// repository's own tree holds. It is also bounded in a fourth way that
// related context is not: finding callers means reading files that were not
// named by anything, so the walk over the tree has a ceiling of its own
// (maxCallerFiles), and a repository above it attaches whichever callers the
// first files held.
//
// Each language follows the same three steps: name the exported declarations
// whose lines the diff touched, find files whose imports resolve to the
// changed file, and cut out the enclosing function of every call site there.
// A call that cannot be traced to an import of the changed file is not a
// caller; the walk fails closed.

// maxCallerFiles caps how many candidate files one plan reads looking for
// callers. Each read is one fetch, which on a hosted provider is one request.
const maxCallerFiles = 150

// maxCallersPerSymbol caps how many call sites of one redefined symbol are
// attached. Three callers show a contract; thirty show a popular helper.
const maxCallersPerSymbol = 3

// callerSkipDirs are directory names the walk never descends into: vendored
// or generated trees, and test data, where a caller says nothing about the
// change's own contract.
var callerSkipDirs = map[string]bool{
	"vendor": true, "node_modules": true, "testdata": true, "dist": true, "build": true,
	"__pycache__": true, ".venv": true, "venv": true, "target": true,
}

// redefined is one exported declaration the diff touched: what to look for at
// a call site, and how it is called.
type redefined struct {
	// name is the declared identifier; for a Go method, the method name.
	name string
	// receiver is the receiver type of a Go method, empty for a function.
	receiver string
}

// callerWants returns a want for each untouched function that calls a symbol
// the change redefined in e.
func (c *relatedCollector) callerWants(e *Entry) []want {
	if e.File.Kind == diff.ChangeAdded {
		// A new file has no callers that predate it that the diff did not
		// also write.
		return nil
	}
	switch strings.ToLower(path.Ext(e.File.Path)) {
	case ".go":
		return c.goCallerWants(e)
	case ".py":
		return c.pyCallerWants(e)
	case ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs":
		return c.tsCallerWants(e)
	}
	return nil
}

// touchedRanges lists the new-side line ranges each hunk spans. A declaration
// that overlaps one was redefined, in the loose sense the walk wants: its doc
// comment, signature or body changed.
func touchedRanges(f *diff.File) [][2]int {
	var out [][2]int
	for _, h := range f.Hunks {
		lo, hi := 0, 0
		for _, l := range h.Lines {
			if l.Kind == diff.LineRemoved {
				continue
			}
			if l.Kind == diff.LineContext {
				// Context lines position the hunk but were not changed.
				continue
			}
			if lo == 0 || l.NewLine < lo {
				lo = l.NewLine
			}
			if l.NewLine > hi {
				hi = l.NewLine
			}
		}
		if lo == 0 {
			// A hunk of pure removals still lands somewhere: its new-side
			// start marks the line the removal sits before.
			lo, hi = h.NewStart, h.NewStart
		}
		out = append(out, [2]int{lo, hi})
	}
	return out
}

func overlaps(ranges [][2]int, lo, hi int) bool {
	for _, r := range ranges {
		if r[0] <= hi && lo <= r[1] {
			return true
		}
	}
	return false
}

// walkFiles lists files under root with one of the given extensions, in a
// breadth-first order so the shallow, central packages come before the deep
// ones, stopping at the file ceiling. Test files and the changed files
// themselves are left out.
func (c *relatedCollector) walkFiles(root string, isCandidate func(name string) bool) []string {
	var out []string
	queue := []string{strings.Trim(root, "/")}
	seen := map[string]bool{}
	for len(queue) > 0 && len(out) < maxCallerFiles {
		dir := queue[0]
		queue = queue[1:]
		if seen[dir] {
			continue
		}
		seen[dir] = true
		for _, name := range c.entries(dir) {
			if strings.HasSuffix(name, "/") {
				sub := strings.TrimSuffix(name, "/")
				if callerSkipDirs[sub] || strings.HasPrefix(sub, ".") {
					continue
				}
				queue = append(queue, path.Join(dir, sub))
				continue
			}
			if !isCandidate(name) {
				continue
			}
			p := path.Join(dir, name)
			if c.changed[p] {
				continue
			}
			out = append(out, p)
			if len(out) >= maxCallerFiles {
				break
			}
		}
	}
	return out
}

// callSites returns the 0-based line indexes in lines where re matches, in
// order, and the enclosing top-level definition of each as found by
// enclosing, deduplicated and capped at maxCallersPerSymbol.
func callSites(lines []string, re *regexp.Regexp, enclosing func(lines []string, i int) int) []int {
	var starts []int
	seen := map[int]bool{}
	for i, line := range lines {
		if !re.MatchString(line) {
			continue
		}
		s := enclosing(lines, i)
		if s < 0 || seen[s] {
			continue
		}
		seen[s] = true
		starts = append(starts, s)
		if len(starts) >= maxCallersPerSymbol {
			break
		}
	}
	return starts
}

// callerConstants finds the single-line, top-level constants of a file that
// the body at lines[start:end] names, and returns their line indexes. A
// caller that passes BATCH where the new precondition is on the page size
// tells the model nothing unless BATCH's value comes along; the definition
// is one line and sits outside the function, so it is attached beside it.
// Capped at maxCallerConstants per caller.
func callerConstants(lines []string, start, end int, def *regexp.Regexp) []int {
	body := strings.Join(lines[start:min(end, len(lines))], "\n")
	used := map[string]bool{}
	for _, m := range identWord.FindAllString(body, -1) {
		used[m] = true
	}
	var out []int
	for i, line := range lines {
		if i >= start && i < end {
			continue
		}
		m := def.FindStringSubmatch(line)
		if m == nil || !used[m[1]] {
			continue
		}
		out = append(out, i)
		if len(out) >= maxCallerConstants {
			break
		}
	}
	return out
}

// maxCallerConstants caps how many constant lines one caller brings along.
const maxCallerConstants = 3

var (
	identWord = regexp.MustCompile(`\b[A-Za-z_$][\w$]*\b`)
	// One-line top-level constant or variable, per language: the name is
	// the first group. Block members in Go (`\tNAME = ...` inside const (...))
	// are matched by the indented form.
	goConstLine = regexp.MustCompile(`^(?:(?:const|var)\s+|\t)([A-Z]\w*)\s*(?:\w+\s*)?=\s*\S`)
	pyConstLine = regexp.MustCompile(`^([A-Z][A-Z0-9_]*)\s*(?::\s*[\w\[\], ]+)?\s*=\s*\S`)
	tsConstLine = regexp.MustCompile(`^(?:export\s+)?(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*(?::[^=]+)?=\s*\S`)
)

// constantExtract returns the one line at index i with its leading comments.
func constantExtract(i int, commentPrefixes ...string) func(content, name string) (Related, bool) {
	return func(content, _ string) (Related, bool) {
		lines := splitLines(content)
		if i < 0 || i >= len(lines) {
			return Related{}, false
		}
		from := withLeadingComments(lines, i, commentPrefixes...)
		return Related{Line: from + 1, Snippet: join(lines[from : i+1])}, true
	}
}

// callerExtract cuts the definition starting at line start out of content:
// its leading comment block and its brace-delimited body.
func callerExtract(start int, commentPrefixes ...string) func(content, name string) (Related, bool) {
	return func(content, _ string) (Related, bool) {
		lines := splitLines(content)
		if start < 0 || start >= len(lines) {
			return Related{}, false
		}
		from := withLeadingComments(lines, start, commentPrefixes...)
		end := braceExtent(lines, start)
		return Related{Line: from + 1, Snippet: join(lines[from:end])}, true
	}
}

// --- Go ----------------------------------------------------------------------

// goRedefined names the exported functions and methods in e whose lines the
// diff touched, from a full parse of the head content.
func goRedefined(e *Entry) []redefined {
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, e.File.Path, e.Content, parser.ParseComments)
	if err != nil || parsed == nil {
		return nil
	}
	ranges := touchedRanges(e.File)
	var out []redefined
	for _, d := range parsed.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || !fn.Name.IsExported() {
			continue
		}
		lo := fset.Position(fn.Pos()).Line
		if fn.Doc != nil {
			lo = fset.Position(fn.Doc.Pos()).Line
		}
		hi := fset.Position(fn.End()).Line
		if !overlaps(ranges, lo, hi) {
			continue
		}
		r := redefined{name: fn.Name.Name}
		if fn.Recv != nil && len(fn.Recv.List) > 0 {
			r.receiver = goReceiverType(fn.Recv.List[0].Type)
		}
		out = append(out, r)
	}
	return out
}

func goReceiverType(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.StarExpr:
		return goReceiverType(t.X)
	case *ast.Ident:
		return t.Name
	case *ast.IndexExpr:
		return goReceiverType(t.X)
	case *ast.IndexListExpr:
		return goReceiverType(t.X)
	}
	return ""
}

// goEnclosingFunc finds the top-level func declaration line i sits in.
func goEnclosingFunc(lines []string, i int) int {
	for j := i; j >= 0; j-- {
		if strings.HasPrefix(lines[j], "func ") {
			return j
		}
		if j < i && len(lines[j]) > 0 && lines[j][0] == '}' {
			// Left the function without finding its head: i was at top level.
			return -1
		}
	}
	return -1
}

func (c *relatedCollector) goCallerWants(e *Entry) []want {
	if c.list == nil {
		return nil
	}
	syms := goRedefined(e)
	if len(syms) == 0 {
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
	rel := strings.TrimPrefix(strings.TrimPrefix(dir, mod.Dir), "/")
	importPath := mod.Path
	if rel != "" {
		importPath = mod.Path + "/" + rel
	}
	pkgName := goPackageName(importPath)

	files := c.walkFiles(mod.Dir, func(name string) bool {
		return strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")
	})

	var wants []want
	for _, file := range files {
		content, ok := c.read(file)
		if !ok {
			continue
		}
		local := goImportsAs(content, importPath, pkgName)
		samePkg := path.Dir(file) == path.Dir(e.File.Path)
		if local == "" && !samePkg {
			continue
		}
		lines := splitLines(content)
		for _, s := range syms {
			var re *regexp.Regexp
			var calls string
			switch {
			case s.receiver != "":
				// A method call binds by name alone; the file importing the
				// package (or sharing it) is the evidence that the receiver
				// is this type. Receiver-type resolution needs a type
				// checker, which a fetcher cannot run.
				re = regexp.MustCompile(`\.` + regexp.QuoteMeta(s.name) + `\s*\(`)
				calls = pkgName + "." + s.receiver + "." + s.name
			case samePkg:
				re = regexp.MustCompile(`\b` + regexp.QuoteMeta(s.name) + `\s*\(`)
				calls = s.name
			default:
				re = regexp.MustCompile(`\b` + regexp.QuoteMeta(local) + `\.` + regexp.QuoteMeta(s.name) + `\s*\(`)
				calls = pkgName + "." + s.name
			}
			for _, start := range callSites(lines, re, goEnclosingFunc) {
				wants = append(wants, want{
					file:    file,
					name:    callerName(lines[start]),
					calls:   calls,
					uses:    1,
					extract: callerExtract(start, "//"),
				})
				for _, i := range callerConstants(lines, start, braceExtent(lines, start), goConstLine) {
					wants = append(wants, want{file: file, name: callerName(lines[start]) + ":" + strings.TrimSpace(lines[i]), calls: calls, extract: constantExtract(i, "//")})
				}
			}
		}
	}
	return wants
}

// goImportsAs returns the local name a file binds importPath to, or empty
// when it does not import it.
func goImportsAs(content, importPath, pkgName string) string {
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, "", content, parser.ImportsOnly)
	if err != nil || parsed == nil {
		return ""
	}
	for _, spec := range parsed.Imports {
		if strings.Trim(spec.Path.Value, `"`) != importPath {
			continue
		}
		if spec.Name != nil {
			if spec.Name.Name == "_" || spec.Name.Name == "." {
				return ""
			}
			return spec.Name.Name
		}
		return pkgName
	}
	return ""
}

// callerName reads a display name for the definition on a line, for the
// dedupe key and the log: the identifier after the keyword, best effort.
func callerName(line string) string {
	m := regexp.MustCompile(`(?:func|def|function|class)\s+(?:\([^)]*\)\s*)?(?:async\s+)?([A-Za-z_$][\w$]*)`).FindStringSubmatch(line)
	if m != nil {
		return m[1]
	}
	m = regexp.MustCompile(`(?:const|let|var|export)\s+(?:async\s+)?(?:function\s+)?([A-Za-z_$][\w$]*)`).FindStringSubmatch(line)
	if m != nil {
		return m[1]
	}
	return strings.TrimSpace(line)
}

// --- Python --------------------------------------------------------------------

var pyTopDef = regexp.MustCompile(`^(?:async\s+)?(?:def|class)\s+([A-Za-z_]\w*)`)

// pyRedefined names the module-level defs and classes whose lines the diff
// touched. A def's extent runs to the next unindented, non-blank line.
func pyRedefined(e *Entry) []redefined {
	lines := splitLines(e.Content)
	ranges := touchedRanges(e.File)
	var out []redefined
	for i := 0; i < len(lines); i++ {
		m := pyTopDef.FindStringSubmatch(lines[i])
		if m == nil || strings.HasPrefix(m[1], "_") {
			continue
		}
		end := i + 1
		for end < len(lines) {
			t := lines[end]
			if strings.TrimSpace(t) != "" && !strings.HasPrefix(t, " ") && !strings.HasPrefix(t, "\t") {
				break
			}
			end++
		}
		// Decorators above the def belong to it.
		lo := i
		for lo > 0 && strings.HasPrefix(lines[lo-1], "@") {
			lo--
		}
		if overlaps(ranges, lo+1, end) {
			out = append(out, redefined{name: m[1]})
		}
		i = end - 1
	}
	return out
}

// pyEnclosingDef finds the module-level def or class line i sits in.
func pyEnclosingDef(lines []string, i int) int {
	for j := i; j >= 0; j-- {
		if pyTopDef.MatchString(lines[j]) {
			return j
		}
		if j < i && strings.TrimSpace(lines[j]) != "" && !strings.HasPrefix(lines[j], " ") && !strings.HasPrefix(lines[j], "\t") && !strings.HasPrefix(lines[j], "@") {
			return -1
		}
	}
	return -1
}

// pyDefEnd returns the index one past the last line of the def starting at
// start, by indentation, with trailing blank lines left out.
func pyDefEnd(lines []string, start int) int {
	end := start + 1
	for end < len(lines) && end-start < maxDefinitionLines {
		t := lines[end]
		if strings.TrimSpace(t) != "" && !strings.HasPrefix(t, " ") && !strings.HasPrefix(t, "\t") {
			break
		}
		end++
	}
	// Trailing blank lines are the gap to the next def, not the body.
	for end > start+1 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	return end
}

// pyExtract cuts a def out by indentation, since Python has no braces.
func pyExtract(start int) func(content, name string) (Related, bool) {
	return func(content, _ string) (Related, bool) {
		lines := splitLines(content)
		if start < 0 || start >= len(lines) {
			return Related{}, false
		}
		from := start
		for from > 0 && strings.HasPrefix(lines[from-1], "@") {
			from--
		}
		return Related{Line: from + 1, Snippet: join(lines[from:pyDefEnd(lines, start)])}, true
	}
}

func (c *relatedCollector) pyCallerWants(e *Entry) []want {
	syms := pyRedefined(e)
	if len(syms) == 0 {
		return nil
	}
	files := c.walkFiles("", func(name string) bool { return strings.HasSuffix(name, ".py") && !strings.HasPrefix(name, "test_") })

	var wants []want
	for _, file := range files {
		content, ok := c.read(file)
		if !ok {
			continue
		}
		// locals maps a redefined name to what this file calls it, when an
		// import binds it; module maps to the local name of the module
		// itself when it is imported whole.
		locals := map[string]string{}
		var modules []string
		for _, m := range pyFrom.FindAllStringSubmatch(content, -1) {
			if c.pyResolve(file, m[1]) != e.File.Path {
				continue
			}
			for part := range strings.SplitSeq(strings.Trim(m[2], "()"), ",") {
				part = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(part), "\\"))
				exported, local := part, part
				if a, b, ok := strings.Cut(part, " as "); ok {
					exported, local = strings.TrimSpace(a), strings.TrimSpace(b)
				}
				if exported != "" {
					locals[exported] = local
				}
			}
		}
		for _, m := range pyImport.FindAllStringSubmatch(content, -1) {
			if c.pyResolve(file, m[1]) != e.File.Path {
				continue
			}
			if m[2] != "" {
				modules = append(modules, m[2])
			} else {
				modules = append(modules, m[1])
			}
		}
		if len(locals) == 0 && len(modules) == 0 {
			continue
		}
		lines := splitLines(content)
		modName := strings.TrimSuffix(path.Base(e.File.Path), ".py")
		for _, s := range syms {
			var alts []string
			if local, ok := locals[s.name]; ok {
				alts = append(alts, `\b`+regexp.QuoteMeta(local)+`\s*\(`)
			}
			for _, mod := range modules {
				alts = append(alts, `\b`+regexp.QuoteMeta(mod)+`\.`+regexp.QuoteMeta(s.name)+`\s*\(`)
			}
			if len(alts) == 0 {
				continue
			}
			re := regexp.MustCompile(strings.Join(alts, "|"))
			for _, start := range callSites(lines, re, pyEnclosingDef) {
				wants = append(wants, want{
					file:    file,
					name:    callerName(lines[start]),
					calls:   modName + "." + s.name,
					uses:    1,
					extract: pyExtract(start),
				})
				for _, i := range callerConstants(lines, start, pyDefEnd(lines, start), pyConstLine) {
					wants = append(wants, want{file: file, name: callerName(lines[start]) + ":" + strings.TrimSpace(lines[i]), calls: modName + "." + s.name, extract: constantExtract(i, "#")})
				}
			}
		}
	}
	return wants
}

// --- TypeScript / JavaScript --------------------------------------------------

var tsTopDef = regexp.MustCompile(`^(?:export\s+)?(?:default\s+)?(?:async\s+)?(?:function\s*\*?\s*|class\s+|const\s+|let\s+|var\s+)([A-Za-z_$][\w$]*)`)

// tsRedefined names the exported top-level definitions whose lines the diff
// touched.
func tsRedefined(e *Entry) []redefined {
	lines := splitLines(e.Content)
	ranges := touchedRanges(e.File)
	exported := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^export\s+\{([^}]*)\}`).FindAllStringSubmatch(e.Content, -1) {
		for part := range strings.SplitSeq(m[1], ",") {
			name := strings.TrimSpace(part)
			if a, _, ok := strings.Cut(name, " as "); ok {
				name = strings.TrimSpace(a)
			}
			if name != "" {
				exported[name] = true
			}
		}
	}
	var out []redefined
	for i := 0; i < len(lines); i++ {
		m := tsTopDef.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		if !strings.HasPrefix(lines[i], "export") && !exported[m[1]] {
			continue
		}
		lo := withLeadingComments(lines, i, "//", "/*", "*")
		end := braceExtent(lines, i)
		if overlaps(ranges, lo+1, end) {
			out = append(out, redefined{name: m[1]})
		}
		i = end - 1
	}
	return out
}

// tsEnclosingDef finds the top-level definition line i sits in.
func tsEnclosingDef(lines []string, i int) int {
	for j := i; j >= 0; j-- {
		if tsTopDef.MatchString(lines[j]) {
			return j
		}
		if j < i && len(lines[j]) > 0 && lines[j][0] == '}' {
			return -1
		}
	}
	return -1
}

func (c *relatedCollector) tsCallerWants(e *Entry) []want {
	syms := tsRedefined(e)
	if len(syms) == 0 {
		return nil
	}
	files := c.walkFiles("", func(name string) bool {
		for _, ext := range tsExts {
			if strings.HasSuffix(name, ext) && !strings.Contains(name, ".test.") && !strings.Contains(name, ".spec.") && !strings.HasSuffix(name, ".d.ts") {
				return true
			}
		}
		return false
	})

	var wants []want
	for _, file := range files {
		content, ok := c.read(file)
		if !ok {
			continue
		}
		locals := map[string]string{}
		var namespaces []string
		consider := func(clause, spec string) {
			if c.tsResolve(file, spec) != e.File.Path {
				return
			}
			named, ns := tsBindings(clause)
			for _, b := range named {
				locals[b.exported] = b.local
			}
			if ns != "" {
				namespaces = append(namespaces, ns)
			}
		}
		for _, m := range tsImport.FindAllStringSubmatch(content, -1) {
			consider(m[1], m[2])
		}
		for _, m := range tsRequire.FindAllStringSubmatch(content, -1) {
			consider(m[1], m[2])
		}
		if len(locals) == 0 && len(namespaces) == 0 {
			continue
		}
		lines := splitLines(content)
		modName := strings.TrimSuffix(path.Base(e.File.Path), path.Ext(e.File.Path))
		for _, s := range syms {
			var alts []string
			if local, ok := locals[s.name]; ok {
				alts = append(alts, `\b`+regexp.QuoteMeta(local)+`\s*\(`)
			}
			for _, ns := range namespaces {
				alts = append(alts, `\b`+regexp.QuoteMeta(ns)+`\.`+regexp.QuoteMeta(s.name)+`\s*\(`)
			}
			if len(alts) == 0 {
				continue
			}
			re := regexp.MustCompile(strings.Join(alts, "|"))
			for _, start := range callSites(lines, re, tsEnclosingDef) {
				wants = append(wants, want{
					file:    file,
					name:    callerName(lines[start]),
					calls:   modName + "." + s.name,
					uses:    1,
					extract: callerExtract(start, "//", "/*", "*"),
				})
				for _, i := range callerConstants(lines, start, braceExtent(lines, start), tsConstLine) {
					wants = append(wants, want{file: file, name: callerName(lines[start]) + ":" + strings.TrimSpace(lines[i]), calls: modName + "." + s.name, extract: constantExtract(i, "//", "/*", "*")})
				}
			}
		}
	}
	return wants
}

// sortCallers orders caller wants by file then name so attachment is
// deterministic across runs.
func sortCallers(wants []want) {
	sort.SliceStable(wants, func(i, j int) bool {
		if wants[i].file != wants[j].file {
			return wants[i].file < wants[j].file
		}
		return wants[i].name < wants[j].name
	})
}
