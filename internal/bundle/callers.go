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
// (maxCallerFiles, per plan), and a repository above it attaches whichever
// callers the first files held.
//
// Each language follows the same three steps: name the exported declarations
// whose lines the diff touched, find files whose imports resolve to the
// changed file, and cut out the enclosing function of every call site there.
// A call that cannot be traced to an import of the changed file is not a
// caller; the walk fails closed. A call site is matched in code only: a
// comment or a string that names the symbol is not a call.

// maxCallerFiles caps how many candidate files one plan fetches looking for
// callers, across every changed file and language. A candidate already read
// for an earlier changed file is not charged again: the ceiling bounds
// requests, which on a hosted provider are the cost, and a cached file costs
// none. When the ceiling stops a walk short the plan says so, so a review
// that attached no callers for a file can be read for what it is.
const maxCallerFiles = 150

// maxCallersPerSymbol caps how many call sites of one redefined symbol are
// attached across the whole walk. Three callers show a contract; thirty show
// a popular helper.
const maxCallersPerSymbol = 3

// maxCallerConstants caps how many constant lines one caller brings along.
const maxCallerConstants = 3

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
// the change redefined in e, followed by the constants those functions pass.
func (c *relatedCollector) callerWants(e *Entry) []want {
	if e.File.Kind == diff.ChangeAdded || !e.HasContent() {
		// A new file has no callers that predate it that the diff did not
		// also write. A deleted file never gets here: the assembler skips
		// it as unreviewable, and a finding about its orphaned callers
		// would have no line to sit on.
		return nil
	}
	// A rename's old path is what the callers still import, and it is
	// absent at head. The resolvers probe by existence, so it is seeded
	// with the content; it is never attached, being a changed path.
	if e.File.OldPath != "" && e.File.OldPath != e.File.Path {
		if _, ok := c.files[e.File.OldPath]; !ok {
			c.files[e.File.OldPath] = e.Content
		}
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

// redefinedSource is the text the redefined declarations are read from, the
// head content, and the new-side line ranges the diff touched in it.
func redefinedSource(e *Entry) (content string, ranges [][2]int) {
	return e.Content, touchedRanges(e.File)
}

// touchedRanges lists the new-side line ranges each hunk changed. A
// declaration that overlaps one was redefined, in the loose sense the walk
// wants: its doc comment, signature or body changed.
func touchedRanges(f *diff.File) [][2]int {
	var out [][2]int
	for _, h := range f.Hunks {
		lo, hi := 0, 0
		for _, l := range h.Lines {
			if l.Kind != diff.LineAdded {
				// Context lines position the hunk but were not changed, and
				// removed lines have no new-side number.
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

// sourcePaths are the paths a caller's import may resolve to: the file's
// current path and, for a rename, the path callers still import.
func sourcePaths(e *Entry) map[string]bool {
	out := map[string]bool{e.File.Path: true}
	if e.File.OldPath != "" {
		out[e.File.OldPath] = true
	}
	return out
}

// walkFiles lists files under root that isCandidate accepts, in a
// breadth-first order so the shallow, central packages come before the deep
// ones, stopping when the plan's ceiling is reached. The changed files
// themselves are left out. The ceiling is shared by every walk in the plan,
// so a change touching three languages does not triple it.
func (c *relatedCollector) walkFiles(root string, isCandidate func(name string) bool) []string {
	var out []string
	queue := []string{strings.Trim(root, "/")}
	seen := map[string]bool{}
	for len(queue) > 0 {
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
			if _, cached := c.files[p]; !cached && !c.missing[p] {
				if c.walked >= maxCallerFiles {
					// Something is left unlisted: this file, and whatever
					// the rest of this directory and the queue hold.
					c.truncated = true
					return out
				}
				c.walked++
			}
			out = append(out, p)
		}
	}
	return out
}

// --- Matching call sites in code, not in prose ---------------------------------

var (
	quotedSpan = regexp.MustCompile("\"(?:[^\"\\\\\\n]|\\\\.)*\"|'(?:[^'\\\\\\n]|\\\\.)*'|`[^`\\n]*`")
	slashTail  = regexp.MustCompile(`//.*$`)
	hashTail   = regexp.MustCompile(`#.*$`)
	blockSpan  = regexp.MustCompile(`/\*.*?\*/`)
)

// codeLines returns a copy of lines with string literals and comments
// blanked, so a regex for a call site cannot match a doc comment that names
// the symbol or a string that mentions it. Line-local: a multi-line block
// comment is not tracked, but a Python triple-quoted string is, whether a
// docstring or a module-level SQL template: a line with an odd number of
// fences opens or closes one, and every line inside is blank.
func codeLines(lines []string, hashComments bool) []string {
	out := make([]string, len(lines))
	inDoc := false
	for i, line := range lines {
		if hashComments {
			// Fences are counted on the line with its quoted spans and
			// comment tail blanked, so a `"""` inside a comment or a
			// single-quoted string does not open a docstring that swallows
			// the rest of the file. An opening `x = """` keeps its unpaired
			// quote and still counts one.
			probe := line
			if !inDoc {
				// Each quoted span keeps its own quote characters, so an
				// empty `''` is not rewritten to `""` and a `'''` fence
				// still counts as one.
				probe = hashTail.ReplaceAllString(quotedSpan.ReplaceAllStringFunc(line, func(q string) string {
					return q[:1] + q[len(q)-1:]
				}), "")
			}
			fences := strings.Count(probe, `"""`) + strings.Count(probe, `'''`)
			if inDoc {
				if fences%2 == 1 {
					inDoc = false
				}
				continue
			}
			if fences%2 == 1 {
				inDoc = true
				continue
			}
		}
		s := quotedSpan.ReplaceAllString(line, `""`)
		s = blockSpan.ReplaceAllString(s, "")
		if hashComments {
			s = hashTail.ReplaceAllString(s, "")
		} else {
			s = slashTail.ReplaceAllString(s, "")
		}
		out[i] = s
	}
	return out
}

// callSites returns the enclosing-definition line index of each call site in
// code where re matches, deduplicated, in order. code is the blanked copy
// from codeLines; lines is the real text the enclosing function is found in.
// A definition of name itself (another type's method of the same name, or a
// shadowing function) is not a call of it.
func callSites(code, lines []string, name string, re *regexp.Regexp, enclosing func(lines []string, i int) int) []int {
	var starts []int
	seen := map[int]bool{}
	for i, line := range code {
		if !re.MatchString(line) {
			continue
		}
		s := enclosing(lines, i)
		if s < 0 || seen[s] {
			continue
		}
		if s == i && callerName(lines[s]) == name {
			continue
		}
		seen[s] = true
		starts = append(starts, s)
	}
	return starts
}

// callRegexp matches name as a call, not preceded by a dot (which would make
// it a member of something else) and not part of a longer identifier.
func callRegexp(name string) *regexp.Regexp {
	return regexp.MustCompile(`(?:^|[^.\w$])` + regexp.QuoteMeta(name) + `\s*\(`)
}

// memberCallRegexp matches receiver.name( with the same identifier guard.
func memberCallRegexp(receiver, name string) *regexp.Regexp {
	return regexp.MustCompile(`(?:^|[^.\w$])` + regexp.QuoteMeta(receiver) + `\.` + regexp.QuoteMeta(name) + `\s*\(`)
}

// --- Constants a caller passes -------------------------------------------------

var identWord = regexp.MustCompile(`\b[A-Za-z_$][\w$]*\b`)

// callerConstants finds the single-line, top-level constants of a file that
// the body at lines[start:end] names, and returns their line indexes. A
// caller that passes BATCH where the new precondition is on the page size
// tells the model nothing unless BATCH's value comes along; the definition
// is one line and sits outside the function, so it is attached beside it.
// constAt reports the name a line defines at top level, or "".
func callerConstants(lines []string, start, end int, constAt func(i int) string) []int {
	body := strings.Join(lines[start:min(end, len(lines))], "\n")
	used := map[string]bool{}
	for _, m := range identWord.FindAllString(body, -1) {
		used[m] = true
	}
	var out []int
	for i := range lines {
		if i >= start && i < end {
			continue
		}
		if name := constAt(i); name != "" && used[name] {
			out = append(out, i)
			if len(out) >= maxCallerConstants {
				break
			}
		}
	}
	return out
}

var (
	goConstDecl  = regexp.MustCompile(`^(?:const|var)\s+([A-Z]\w*)\s*(?:\w+\s*)?=\s*\S`)
	goConstBlock = regexp.MustCompile(`^(?:const|var)\s*\(\s*$`)
	goConstItem  = regexp.MustCompile(`^\t([A-Z]\w*)\s*(?:\w+\s*)?=\s*\S`)
	pyConstLine  = regexp.MustCompile(`^([A-Z][A-Z0-9_]*)\s*(?::\s*[\w\[\], ]+)?\s*=\s*\S`)
	tsConstLine  = regexp.MustCompile(`^(?:export\s+)?(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*(?::[^=]+)?=\s*\S`)
)

// goConstAt returns a predicate naming the top-level constant or variable a
// line declares: a one-line `const X = ...`, or a member of a `const (`
// block. An indented assignment inside a function body is not one, which is
// why block membership is tracked rather than inferred from the tab.
func goConstAt(lines []string) func(i int) string {
	inBlock := make([]bool, len(lines))
	open := false
	for i, line := range lines {
		switch {
		case goConstBlock.MatchString(line):
			open = true
		case open && strings.HasPrefix(line, ")"):
			open = false
		default:
			inBlock[i] = open
		}
	}
	return func(i int) string {
		if m := goConstDecl.FindStringSubmatch(lines[i]); m != nil {
			return m[1]
		}
		if inBlock[i] {
			if m := goConstItem.FindStringSubmatch(lines[i]); m != nil {
				return m[1]
			}
		}
		return ""
	}
}

func regexpConstAt(lines []string, re *regexp.Regexp) func(i int) string {
	return func(i int) string {
		if m := re.FindStringSubmatch(lines[i]); m != nil {
			return m[1]
		}
		return ""
	}
}

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

// callerName reads a display name for the definition on a line, for the
// dedupe key and the log: the identifier the declaration binds.
func callerName(line string) string {
	if m := callerNameDef.FindStringSubmatch(line); m != nil {
		return m[1]
	}
	if m := callerNameBinding.FindStringSubmatch(line); m != nil {
		return m[1]
	}
	if m := callerNameMethod.FindStringSubmatch(line); m != nil {
		return m[1]
	}
	return strings.TrimSpace(line)
}

var (
	callerNameDef     = regexp.MustCompile(`^\s*(?:export\s+)?(?:default\s+)?(?:async\s+)?(?:function|func|def|class)\b\s*\*?\s*(?:\([^)]*\)\s*)?([A-Za-z_$][\w$]*)`)
	callerNameBinding = regexp.MustCompile(`^\s*(?:export\s+)?(?:default\s+)?(?:const|let|var)\s+(?:async\s+)?(?:function\s+)?([A-Za-z_$][\w$]*)`)
	callerNameMethod  = regexp.MustCompile(`^\s*(?:(?:public|private|protected|static|async|readonly|override|get|set)\s+)*([A-Za-z_$][\w$]*)\s*(?:<[^>]*>)?\s*(?:=\s*(?:async\s*)?)?\(`)
)

// indentOf returns the leading whitespace of a line.
func indentOf(line string) string {
	return line[:len(line)-len(strings.TrimLeft(line, " \t"))]
}

// --- Go ----------------------------------------------------------------------

// goRedefined names the exported functions and methods whose lines the diff
// touched, from a full parse of the source.
func goRedefined(filename, content string, ranges [][2]int) []redefined {
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, filename, content, parser.ParseComments)
	if err != nil || parsed == nil {
		return nil
	}
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
	source, ranges := redefinedSource(e)
	syms := goRedefined(e.File.Path, source, ranges)
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
	importPathOf := func(d string) string {
		rel := strings.TrimPrefix(strings.TrimPrefix(d, mod.Dir), "/")
		if rel == "" {
			return mod.Path
		}
		return mod.Path + "/" + rel
	}
	importPaths := map[string]bool{}
	for p := range sourcePaths(e) {
		d := path.Dir(p)
		if d == "." {
			d = ""
		}
		importPaths[importPathOf(d)] = true
	}
	pkgName := goPackageName(importPathOf(dir))

	files := c.walkFiles(mod.Dir, func(name string) bool {
		return strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")
	})

	var wants []want
	attached := map[string]int{}
	for _, file := range files {
		content, ok := c.read(file)
		if !ok {
			continue
		}
		local := goImportsAs(content, importPaths, pkgName)
		samePkg := path.Dir(file) == path.Dir(e.File.Path)
		if local == "" && !samePkg {
			continue
		}
		lines := splitLines(content)
		code := codeLines(lines, false)
		constAt := goConstAt(lines)
		for _, s := range syms {
			if attached[s.name] >= maxCallersPerSymbol {
				continue
			}
			var re *regexp.Regexp
			var calls string
			switch {
			case s.receiver != "":
				// A method call binds by name alone, so the file has to
				// name the receiver type somewhere (a field, a parameter, a
				// composite literal, a type assertion) before a .Name( in
				// it counts: otherwise every f.Close() in a file that
				// imports the package would attach as a caller of the
				// package's Close. A caller that only ever holds the value
				// from a constructor (`u := store.New()`) never names the
				// type and is missed; that is the fail-closed side.
				// Receiver resolution proper needs a type checker, which a
				// fetcher cannot run.
				typeRef := `\b` + regexp.QuoteMeta(s.receiver) + `\b`
				if !samePkg {
					typeRef = `\b` + regexp.QuoteMeta(local) + `\.` + regexp.QuoteMeta(s.receiver) + `\b`
				}
				if !regexp.MustCompile(typeRef).MatchString(strings.Join(code, "\n")) {
					continue
				}
				re = regexp.MustCompile(`\.` + regexp.QuoteMeta(s.name) + `\s*\(`)
				calls = pkgName + "." + s.receiver + "." + s.name
			case samePkg:
				re = callRegexp(s.name)
				calls = s.name
			default:
				re = memberCallRegexp(local, s.name)
				calls = pkgName + "." + s.name
			}
			for _, start := range callSites(code, lines, s.name, re, goEnclosingFunc) {
				if attached[s.name] >= maxCallersPerSymbol {
					break
				}
				attached[s.name]++
				caller := callerName(lines[start])
				wants = append(wants, want{file: file, name: caller, line: start, calls: calls, uses: 1, extract: callerExtract(start, "//")})
				for _, i := range callerConstants(lines, start, braceExtent(lines, start), constAt) {
					wants = append(wants, want{file: file, name: strings.TrimSpace(lines[i]), line: i, calls: calls, constant: true, caller: caller, callerLine: start, extract: constantExtract(i, "//")})
				}
			}
		}
	}
	return wants
}

// goImportsAs returns the local name a file binds one of importPaths to, or
// empty when it imports none of them.
func goImportsAs(content string, importPaths map[string]bool, pkgName string) string {
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, "", content, parser.ImportsOnly)
	if err != nil || parsed == nil {
		return ""
	}
	for _, spec := range parsed.Imports {
		if !importPaths[strings.Trim(spec.Path.Value, `"`)] {
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

// --- Python --------------------------------------------------------------------

var (
	pyTopDef = regexp.MustCompile(`^(?:async\s+)?(?:def|class)\s+([A-Za-z_]\w*)`)
	pyAnyDef = regexp.MustCompile(`^\s*(?:async\s+)?def\s+([A-Za-z_]\w*)`)
)

// pyRedefined names the module-level defs and classes whose lines the diff
// touched. A def's extent runs to the next unindented, non-blank line.
func pyRedefined(content string, ranges [][2]int) []redefined {
	lines := splitLines(content)
	var out []redefined
	for i := 0; i < len(lines); i++ {
		m := pyTopDef.FindStringSubmatch(lines[i])
		if m == nil || strings.HasPrefix(m[1], "_") {
			continue
		}
		end := pyBlockEnd(lines, i, "")
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

// pyBlockEnd returns the index one past the last line of the block whose
// header at start has the given indentation: lines more indented than the
// header, or blank, belong to it. Trailing blank lines are left out.
func pyBlockEnd(lines []string, start int, indent string) int {
	end := start + 1
	for end < len(lines) && end-start < maxDefinitionLines {
		t := lines[end]
		if strings.TrimSpace(t) != "" && len(indentOf(t)) <= len(indent) {
			break
		}
		end++
	}
	for end > start+1 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	return end
}

// pyEnclosingDef finds the nearest def line i sits in, at any indentation,
// so a call inside a method attaches the method rather than its class.
func pyEnclosingDef(lines []string, i int) int {
	if pyAnyDef.MatchString(lines[i]) {
		return i
	}
	indent := indentOf(lines[i])
	for j := i - 1; j >= 0; j-- {
		line := lines[j]
		if strings.TrimSpace(line) == "" {
			continue
		}
		if len(indentOf(line)) >= len(indent) {
			continue
		}
		indent = indentOf(line)
		if pyAnyDef.MatchString(line) {
			return j
		}
		if indent == "" {
			return -1
		}
	}
	return -1
}

// pyExtract cuts a def out by indentation, since Python has no braces.
func pyExtract(start int) func(content, name string) (Related, bool) {
	return func(content, _ string) (Related, bool) {
		lines := splitLines(content)
		if start < 0 || start >= len(lines) {
			return Related{}, false
		}
		indent := indentOf(lines[start])
		from := start
		for from > 0 && strings.HasPrefix(strings.TrimSpace(lines[from-1]), "@") && indentOf(lines[from-1]) == indent {
			from--
		}
		return Related{Line: from + 1, Snippet: join(lines[from:pyBlockEnd(lines, start, indent)])}, true
	}
}

func (c *relatedCollector) pyCallerWants(e *Entry) []want {
	source, ranges := redefinedSource(e)
	syms := pyRedefined(source, ranges)
	if len(syms) == 0 {
		return nil
	}
	targets := sourcePaths(e)
	files := c.walkFiles("", func(name string) bool { return strings.HasSuffix(name, ".py") && !strings.HasPrefix(name, "test_") })

	var wants []want
	attached := map[string]int{}
	modName := strings.TrimSuffix(path.Base(e.File.Path), ".py")
	for _, file := range files {
		content, ok := c.read(file)
		if !ok {
			continue
		}
		// locals maps a redefined name to what this file calls it, when an
		// import binds it; modules lists the local names of the module
		// itself when it is imported whole.
		locals := map[string]string{}
		var modules []string
		for _, m := range pyFrom.FindAllStringSubmatch(content, -1) {
			if !targets[c.pyResolve(file, m[1])] {
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
			if !targets[c.pyResolve(file, m[1])] {
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
		code := codeLines(lines, true)
		constAt := regexpConstAt(lines, pyConstLine)
		for _, s := range syms {
			if attached[s.name] >= maxCallersPerSymbol {
				continue
			}
			var alts []string
			if local, ok := locals[s.name]; ok {
				alts = append(alts, callRegexp(local).String())
			}
			for _, mod := range modules {
				alts = append(alts, memberCallRegexp(mod, s.name).String())
			}
			if len(alts) == 0 {
				continue
			}
			re := regexp.MustCompile(strings.Join(alts, "|"))
			calls := modName + "." + s.name
			for _, start := range callSites(code, lines, s.name, re, pyEnclosingDef) {
				if attached[s.name] >= maxCallersPerSymbol {
					break
				}
				attached[s.name]++
				caller := callerName(lines[start])
				wants = append(wants, want{file: file, name: caller, line: start, calls: calls, uses: 1, extract: pyExtract(start)})
				for _, i := range callerConstants(lines, start, pyBlockEnd(lines, start, indentOf(lines[start])), constAt) {
					wants = append(wants, want{file: file, name: strings.TrimSpace(lines[i]), line: i, calls: calls, constant: true, caller: caller, callerLine: start, extract: constantExtract(i, "#")})
				}
			}
		}
	}
	return wants
}

// --- TypeScript / JavaScript --------------------------------------------------

var (
	tsTopDef = regexp.MustCompile(`^(?:export\s+)?(?:default\s+)?(?:async\s+)?(?:function\s*\*?\s*|class\s+|const\s+|let\s+|var\s+)([A-Za-z_$][\w$]*)`)
	// tsMethodDef is a class member with a body: a method, an accessor, or a
	// property holding a function, at any indentation.
	// The parameter list admits one level of nested parens, so a callback
	// type (`run(cb: () => void) {`) is a method; a call whose last
	// argument is a function (`setTimeout(function () {`,
	// `it('x', function () {`) is not, because tsMethodAt rejects the
	// `function` keyword inside the header. Quoted spans are blanked
	// before matching, so a string default is fine. The arrow branch is
	// also anchored by its `=>`.
	tsMethodDef = regexp.MustCompile(`^\s+(?:(?:public|private|protected|static|async|readonly|override|get|set)\s+)*(?:\*\s*)?([A-Za-z_$][\w$]*)\s*(?:<[^>]*>)?\s*(?:\((?:[^()]|\([^()]*\))*\)\s*(?::[^{=]+)?\s*\{|=\s*(?:async\s*)?(?:\((?:[^()]|\([^()]*\))*\)|[A-Za-z_$][\w$]*)\s*(?::[^=]+)?=>)`)
)

// tsRedefined names the exported top-level definitions whose lines the diff
// touched.
func tsRedefined(content string, ranges [][2]int) []redefined {
	lines := splitLines(content)
	exported := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^export\s+\{([^}]*)\}`).FindAllStringSubmatch(content, -1) {
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

// tsKeywords are the statements whose header has the shape of a method:
// `if (x) {`. A block opened by one is not a definition.
var tsKeywords = map[string]bool{
	"if": true, "for": true, "while": true, "switch": true, "catch": true, "do": true,
	"return": true, "typeof": true, "new": true, "await": true, "else": true, "with": true,
	"function": true,
}

// tsMethodAt reports whether a line opens a class member with a body. String
// literals are emptied first, so a default like `name = "hi"` neither hides
// the method nor lets a paren inside a string count as nesting. A header
// holding the `function` keyword is a call with a callback argument, not a
// method.
func tsMethodAt(line string) bool {
	blank := quotedSpan.ReplaceAllStringFunc(line, func(q string) string { return q[:1] + q[len(q)-1:] })
	m := tsMethodDef.FindStringSubmatch(blank)
	return m != nil && !tsKeywords[m[1]] && !tsFunctionWord.MatchString(blank)
}

var tsFunctionWord = regexp.MustCompile(`\bfunction\b`)

// tsEnclosingDef finds the definition line i sits in: the nearest class
// member with a body above it at a shallower indentation, else the
// top-level definition.
func tsEnclosingDef(lines []string, i int) int {
	if tsTopDef.MatchString(lines[i]) || tsMethodAt(lines[i]) {
		return i
	}
	indent := indentOf(lines[i])
	for j := i - 1; j >= 0; j-- {
		line := lines[j]
		if strings.TrimSpace(line) == "" {
			continue
		}
		if tsTopDef.MatchString(line) {
			return j
		}
		if len(indentOf(line)) < len(indent) {
			indent = indentOf(line)
			if tsMethodAt(line) {
				return j
			}
		}
		if len(line) > 0 && line[0] == '}' {
			return -1
		}
	}
	return -1
}

func (c *relatedCollector) tsCallerWants(e *Entry) []want {
	source, ranges := redefinedSource(e)
	syms := tsRedefined(source, ranges)
	if len(syms) == 0 {
		return nil
	}
	targets := sourcePaths(e)
	files := c.walkFiles("", func(name string) bool {
		for _, ext := range tsExts {
			if strings.HasSuffix(name, ext) && !strings.Contains(name, ".test.") && !strings.Contains(name, ".spec.") && !strings.HasSuffix(name, ".d.ts") {
				return true
			}
		}
		return false
	})

	var wants []want
	attached := map[string]int{}
	modName := strings.TrimSuffix(path.Base(e.File.Path), path.Ext(e.File.Path))
	for _, file := range files {
		content, ok := c.read(file)
		if !ok {
			continue
		}
		locals := map[string]string{}
		var namespaces []string
		consider := func(clause, spec string) {
			if !targets[c.tsResolve(file, spec)] {
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
		code := codeLines(lines, false)
		constAt := regexpConstAt(lines, tsConstLine)
		for _, s := range syms {
			if attached[s.name] >= maxCallersPerSymbol {
				continue
			}
			var alts []string
			if local, ok := locals[s.name]; ok {
				alts = append(alts, callRegexp(local).String())
			}
			for _, ns := range namespaces {
				alts = append(alts, memberCallRegexp(ns, s.name).String())
			}
			if len(alts) == 0 {
				continue
			}
			re := regexp.MustCompile(strings.Join(alts, "|"))
			calls := modName + "." + s.name
			for _, start := range callSites(code, lines, s.name, re, tsEnclosingDef) {
				if attached[s.name] >= maxCallersPerSymbol {
					break
				}
				attached[s.name]++
				caller := callerName(lines[start])
				wants = append(wants, want{file: file, name: caller, line: start, calls: calls, uses: 1, extract: callerExtract(start, "//", "/*", "*")})
				for _, i := range callerConstants(lines, start, braceExtent(lines, start), constAt) {
					wants = append(wants, want{file: file, name: strings.TrimSpace(lines[i]), line: i, calls: calls, constant: true, caller: caller, callerLine: start, extract: constantExtract(i, "//", "/*", "*")})
				}
			}
		}
	}
	return wants
}

// sortCallers orders caller wants by file then name so attachment is
// deterministic across runs, with each file's constants after its callers.
func sortCallers(wants []want) {
	sort.SliceStable(wants, func(i, j int) bool {
		if wants[i].file != wants[j].file {
			return wants[i].file < wants[j].file
		}
		if wants[i].constant != wants[j].constant {
			return !wants[i].constant
		}
		if wants[i].constant {
			// File order, which the collector produced them in.
			return false
		}
		return wants[i].name < wants[j].name
	})
}
