// Package flow draws how a change is reached and what it reaches.
//
// It reads Go source only, by name and without type checking, so the graph is
// an approximation: it follows calls and function values between declared
// functions, import-qualified calls into the module and out of it, and method
// calls whose name is unique in the packages in scope. Dispatch through an
// interface or a package-level table is not followed.
package flow

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/jdziat/open-nitpick/internal/diff"
	"github.com/jdziat/open-nitpick/internal/standards"
)

const (
	maxUp    = 4  // caller hops above a changed function
	maxDown  = 2  // callee hops below it
	maxNodes = 30 // nodes in one diagram
	// downShare is the part of the ceiling the callee pass may fill before the
	// caller pass starts. A function with many callers would otherwise use
	// every node on them and leave out the effects it has, which are what the
	// reader asked about.
	downShare = maxNodes / 2
)

// Diagram is the flow around the functions a change edited.
type Diagram struct {
	// Mermaid is a flowchart ready to put in a fenced block.
	Mermaid string `json:"mermaid"`
	// Changed names the edited functions the diagram is built around.
	Changed []string `json:"changed"`
	// Entries names the functions at the top with no caller in the tree.
	Entries []string `json:"entries,omitempty"`
	// Nodes counts the functions drawn.
	Nodes int `json:"nodes"`
	// Truncated is true when a limit cut the diagram short.
	Truncated bool `json:"truncated,omitempty"`
	// Locations places each drawn function at its declaration, so a reader
	// can open the code a box stands for.
	Locations []Location `json:"locations,omitempty"`
	// Calls places each arrow at the call that makes it.
	Calls []Call `json:"calls,omitempty"`
	// Limits says what the graph could not see around the drawn functions:
	// ambiguous names, reflection, generated code and files that did not parse.
	Limits []string `json:"limits,omitempty"`
}

// Location is a function declaration in the reviewed tree.
type Location struct {
	Label string `json:"label"`
	Path  string `json:"path"`
	Line  int    `json:"line"`
}

// Kind says how sure the graph is that a call exists and where it goes.
type Kind string

const (
	// Resolved is a call by plain name or through an import.
	Resolved Kind = "resolved"
	// Inferred is a method call matched by name alone, because exactly one
	// method in scope has that name. It can be a different type's method.
	Inferred Kind = "inferred"
	// Unresolved is a method call whose name matches several methods, so the
	// graph cannot say which one runs.
	Unresolved Kind = "unresolved"
)

// Call is one arrow of the diagram with the line in the caller that makes it.
type Call struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind Kind   `json:"kind"`
	Path string `json:"path"`
	Line int    `json:"line"`
	// Order is the position in a route's handler chain, counted from 1, and
	// zero for an ordinary call.
	Order int `json:"order,omitempty"`
}

type function struct {
	key, dir, name, recv string
	file                 string
	start, end           int
	calls                map[string]site
	external             map[string]site
	ambiguous            map[string]int
	reflects             bool
	// usesMiddleware is true when the body calls Use, whose middleware is
	// attached at run time and so is not drawn.
	usesMiddleware bool
	// unresolvedRoutes counts route registrations in the body whose handlers
	// could not be named, so they are missing from the graph.
	unresolvedRoutes int
	// route marks a stand-in for one registered route, whose calls are its
	// handler chain in the order they run.
	route bool
}

// site is a call the graph found: the line it is on and how sure it is.
type site struct {
	line     int
	inferred bool
	order    int
}

// call records a call to key. A call that is resolved anywhere in the
// function stays resolved, however many inferred ones also reach key.
func (f *function) call(key string, line int, inferred bool) {
	if prior, ok := f.calls[key]; ok && (!prior.inferred || inferred) {
		return
	}
	f.calls[key] = site{line: line, inferred: inferred}
}

func (f *function) label() string {
	if f.route {
		return f.name
	}
	name := f.name
	if f.recv != "" {
		name = f.recv + "." + name
	}
	if f.dir == "." {
		return name
	}
	return f.dir + "." + name
}

type graph struct {
	funcs     map[string]*function
	callers   map[string][]string
	generated map[string]bool
	unparsed  []string
}

// Build returns the diagram for the functions changed in files, or nil when no
// edited function has a caller or callee to draw.
func Build(sources []standards.File, changes diff.Files) *Diagram {
	g := parse(sources)
	changed := g.changedFunctions(changes)
	if len(changed) == 0 {
		return nil
	}
	return g.draw(changed)
}

func parse(sources []standards.File) *graph {
	g := &graph{funcs: map[string]*function{}, callers: map[string][]string{}, generated: map[string]bool{}}
	module := ""
	for _, file := range sources {
		if file.Path == "go.mod" {
			for line := range strings.SplitSeq(string(file.Src), "\n") {
				if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
					module = strings.Trim(strings.TrimSpace(rest), `"`)
					break
				}
			}
		}
	}

	fset := token.NewFileSet()
	type parsed struct {
		dir  string
		file *ast.File
	}
	var files []parsed
	plain := map[string]map[string][]string{}
	methods := map[string]map[string][]string{}
	for _, source := range sources {
		if path.Ext(source.Path) != ".go" || strings.HasSuffix(source.Path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, source.Path, source.Src, parser.SkipObjectResolution|parser.ParseComments)
		if err != nil {
			g.unparsed = append(g.unparsed, source.Path)
			continue
		}
		if ast.IsGenerated(f) {
			g.generated[source.Path] = true
		}
		dir := path.Dir(source.Path)
		files = append(files, parsed{dir, f})
		if plain[dir] == nil {
			plain[dir], methods[dir] = map[string][]string{}, map[string][]string{}
		}
		for _, declaration := range f.Decls {
			decl, ok := declaration.(*ast.FuncDecl)
			if !ok {
				continue
			}
			fn := &function{dir: dir, name: decl.Name.Name, file: source.Path,
				start: fset.Position(decl.Pos()).Line, end: fset.Position(decl.End()).Line,
				calls: map[string]site{}, external: map[string]site{}, ambiguous: map[string]int{}}
			fn.key = dir + "." + fn.name
			if decl.Recv != nil && len(decl.Recv.List) > 0 {
				fn.recv = receiverName(decl.Recv.List[0].Type)
				fn.key = dir + "." + fn.recv + "." + fn.name
				methods[dir][fn.name] = append(methods[dir][fn.name], fn.key)
			} else {
				plain[dir][fn.name] = append(plain[dir][fn.name], fn.key)
			}
			g.funcs[fn.key] = fn
		}
	}

	repoDir := func(imported string) (string, bool) {
		switch {
		case module == "":
			return "", false
		case imported == module:
			return ".", true
		case strings.HasPrefix(imported, module+"/"):
			return strings.TrimPrefix(imported, module+"/"), true
		}
		return "", false
	}

	for _, p := range files {
		aliases := map[string]string{}
		for _, spec := range p.file.Imports {
			imported, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				continue
			}
			alias := path.Base(imported)
			if spec.Name != nil {
				alias = spec.Name.Name
			}
			aliases[alias] = imported
		}
		reflects := aliases["reflect"] == "reflect" || aliases["plugin"] == "plugin"
		persistence := ""
		for _, imported := range aliases {
			if persistent(imported+".") && (persistence == "" || imported < persistence) {
				persistence = imported
			}
		}
		for _, declaration := range p.file.Decls {
			decl, ok := declaration.(*ast.FuncDecl)
			if !ok || decl.Body == nil {
				continue
			}
			fn := g.funcs[functionKey(p.dir, decl)]
			if fn == nil {
				continue
			}
			fn.reflects = reflects
			shadow := shadowed(decl)
			candidatesFor := func(name string) []string {
				candidates := slices.Clone(methods[fn.dir][name])
				for _, imported := range aliases {
					if dir, inRepo := repoDir(imported); inRepo && dir != fn.dir {
						candidates = append(candidates, methods[dir][name]...)
					}
				}
				return candidates
			}
			var resolve func(ast.Expr) (key string, inferred, ok bool)
			resolve = func(expression ast.Expr) (string, bool, bool) {
				switch e := expression.(type) {
				case *ast.ParenExpr:
					return resolve(e.X)
				case *ast.Ident:
					if keys := plain[fn.dir][e.Name]; !shadow[e.Name] && len(keys) == 1 {
						return keys[0], false, true
					}
				case *ast.SelectorExpr:
					if id, ok := e.X.(*ast.Ident); ok && !shadow[id.Name] {
						if imported, ok := aliases[id.Name]; ok {
							if dir, inRepo := repoDir(imported); inRepo {
								if keys := plain[dir][e.Sel.Name]; len(keys) == 1 {
									return keys[0], false, true
								}
							}
							return "", false, false
						}
					}
					if candidates := candidatesFor(e.Sel.Name); len(candidates) == 1 {
						return candidates[0], true, true
					}
				}
				return "", false, false
			}
			var visit func(ast.Node) bool
			visit = func(node ast.Node) bool {
				switch node := node.(type) {
				case *ast.CallExpr:
					sel, ok := node.Fun.(*ast.SelectorExpr)
					if !ok {
						break
					}
					if sel.Sel.Name == "Use" {
						fn.usesMiddleware = true
					}
					label, ok := routeLabel(sel.Sel.Name, node.Args)
					if !ok {
						break
					}
					line := fset.Position(node.Pos()).Line
					route := &function{key: fmt.Sprintf("route:%s:%d", fn.key, fset.Position(node.Pos()).Offset), dir: fn.dir, name: label,
						file: fn.file, start: line, end: fset.Position(node.End()).Line, route: true,
						calls: map[string]site{}, external: map[string]site{}, ambiguous: map[string]int{}}
					var rest []ast.Expr
					unnamed := false
					// A wrapper such as auth(list) runs the wrapper and then
					// the handler it wraps, so both join the chain, in that order.
					var chain func(ast.Expr)
					chain = func(arg ast.Expr) {
						if wrapped, ok := arg.(*ast.CallExpr); ok {
							chain(wrapped.Fun)
							for _, inner := range wrapped.Args {
								chain(inner)
							}
							return
						}
						key, inferred, ok := resolve(arg)
						if _, dup := route.calls[key]; ok && !dup {
							route.calls[key] = site{line: line, inferred: inferred, order: len(route.calls) + 1}
							return
						}
						if _, isLit := arg.(*ast.FuncLit); !isLit && !ok {
							unnamed = true
						}
						rest = append(rest, arg)
					}
					for _, arg := range node.Args[1:] {
						chain(arg)
					}
					if unnamed || len(route.calls) == 0 {
						fn.unresolvedRoutes++
					}
					if len(route.calls) == 0 {
						break
					}
					g.funcs[route.key] = route
					fn.call(route.key, line, false)
					ast.Inspect(sel.X, visit)
					for _, arg := range rest {
						ast.Inspect(arg, visit)
					}
					return false
				case *ast.SelectorExpr:
					if id, ok := node.X.(*ast.Ident); ok && !shadow[id.Name] {
						if imported, ok := aliases[id.Name]; ok {
							if dir, inRepo := repoDir(imported); inRepo {
								for _, key := range plain[dir][node.Sel.Name] {
									fn.call(key, fset.Position(node.Pos()).Line, false)
								}
							} else if effectful(imported) {
								name := imported + "." + node.Sel.Name
								if _, ok := fn.external[name]; !ok {
									fn.external[name] = site{line: fset.Position(node.Pos()).Line}
								}
							}
							return false
						}
					}
					candidates := candidatesFor(node.Sel.Name)
					switch {
					case len(candidates) == 1:
						fn.call(candidates[0], fset.Position(node.Pos()).Line, true)
					case len(candidates) > 1:
						if _, ok := fn.ambiguous[node.Sel.Name]; !ok {
							fn.ambiguous[node.Sel.Name] = fset.Position(node.Pos()).Line
						}
					case persistenceMethods[node.Sel.Name] && persistence != "":
						// No type information: a method with a database-shaped
						// name in a file that imports a database package is
						// taken to reach it, and drawn as inferred.
						name := persistence + "." + node.Sel.Name
						if _, ok := fn.external[name]; !ok {
							fn.external[name] = site{line: fset.Position(node.Pos()).Line, inferred: true}
						}
					}
					ast.Inspect(node.X, visit)
					return false
				case *ast.Ident:
					if !shadow[node.Name] {
						for _, key := range plain[fn.dir][node.Name] {
							fn.call(key, fset.Position(node.Pos()).Line, false)
						}
					}
				}
				return true
			}
			ast.Inspect(decl.Body, visit)
			delete(fn.calls, fn.key)
		}
	}

	for key, fn := range g.funcs {
		for callee := range fn.calls {
			g.callers[callee] = append(g.callers[callee], key)
		}
	}
	for _, callers := range g.callers {
		slices.Sort(callers)
	}
	return g
}

var routeNames = map[string]bool{
	"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true, "HEAD": true, "OPTIONS": true, "Any": true,
	"Get": true, "Post": true, "Put": true, "Patch": true, "Delete": true, "Head": true, "Options": true,
	"Handle": true, "HandleFunc": true,
}

// routeLabel names the route a registration call declares: a router method
// whose first argument is a path literal, followed by the handlers. The
// standard library's pattern syntax already carries the method in the path.
func routeLabel(method string, args []ast.Expr) (string, bool) {
	if !routeNames[method] || len(args) < 2 {
		return "", false
	}
	lit, ok := args[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	pattern, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	if method == "Handle" || method == "HandleFunc" {
		if !strings.HasPrefix(pattern, "/") && !strings.Contains(pattern, " /") {
			return "", false
		}
		return pattern, true
	}
	if !strings.HasPrefix(pattern, "/") {
		return "", false
	}
	return strings.ToUpper(method) + " " + pattern, true
}

// persistenceMethods are method names that read or write a database through
// the packages persistent recognises.
var persistenceMethods = map[string]bool{
	"Query": true, "QueryRow": true, "QueryContext": true, "QueryRowContext": true,
	"Exec": true, "ExecContext": true, "Begin": true, "BeginTx": true,
	"Prepare": true, "PrepareContext": true, "Commit": true, "Rollback": true,
	"Create": true, "Save": true, "First": true, "Find": true, "Updates": true,
}

// persistent reports whether a call, written as import path and name, writes to
// a database or the filesystem. These are drawn apart from other effects
// because a change that reaches one is a change to stored state.
func persistent(name string) bool {
	switch name {
	case "os.WriteFile", "os.Create", "os.OpenFile", "os.Remove", "os.RemoveAll", "os.Rename",
		"os.Mkdir", "os.MkdirAll", "os.Truncate", "os.Chmod", "io/ioutil.WriteFile":
		return true
	}
	for _, prefix := range []string{"database/sql.", "gorm.io/", "github.com/jackc/pgx", "github.com/jmoiron/sqlx",
		"go.mongodb.org/", "github.com/redis/", "github.com/go-redis/", "go.etcd.io/bbolt", "github.com/dgraph-io/badger",
		"github.com/mattn/go-sqlite3", "modernc.org/sqlite", "entgo.io/", "github.com/lib/pq"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func functionKey(dir string, decl *ast.FuncDecl) string {
	if decl.Recv != nil && len(decl.Recv.List) > 0 {
		return dir + "." + receiverName(decl.Recv.List[0].Type) + "." + decl.Name.Name
	}
	return dir + "." + decl.Name.Name
}

func receiverName(expression ast.Expr) string {
	switch expression := expression.(type) {
	case *ast.Ident:
		return expression.Name
	case *ast.StarExpr:
		return receiverName(expression.X)
	case *ast.IndexExpr:
		return receiverName(expression.X)
	case *ast.IndexListExpr:
		return receiverName(expression.X)
	}
	return ""
}

// shadowed names the identifiers a function declares itself, which stop
// meaning the package-level function of the same name.
func shadowed(decl *ast.FuncDecl) map[string]bool {
	names := map[string]bool{}
	fields := func(list *ast.FieldList) {
		if list == nil {
			return
		}
		for _, field := range list.List {
			for _, name := range field.Names {
				names[name.Name] = true
			}
		}
	}
	fields(decl.Recv)
	fields(decl.Type.Params)
	fields(decl.Type.Results)
	ast.Inspect(decl.Body, func(node ast.Node) bool {
		switch node := node.(type) {
		case *ast.AssignStmt:
			if node.Tok == token.DEFINE {
				for _, left := range node.Lhs {
					if id, ok := left.(*ast.Ident); ok {
						names[id.Name] = true
					}
				}
			}
		case *ast.ValueSpec:
			for _, name := range node.Names {
				names[name.Name] = true
			}
		case *ast.RangeStmt:
			if node.Tok == token.DEFINE {
				for _, e := range []ast.Expr{node.Key, node.Value} {
					if id, ok := e.(*ast.Ident); ok {
						names[id.Name] = true
					}
				}
			}
		case *ast.FuncLit:
			fields(node.Type.Params)
			fields(node.Type.Results)
		}
		return true
	})
	return names
}

// effectful reports whether a package outside the module is worth drawing as a
// downstream step: the standard library packages that touch the outside world,
// and anything that is not the standard library, such as a model SDK.
func effectful(imported string) bool {
	first, _, _ := strings.Cut(imported, "/")
	if strings.Contains(first, ".") {
		return true
	}
	switch imported {
	case "os", "os/exec", "io/fs", "io/ioutil", "net", "net/http", "database/sql", "syscall":
		return true
	}
	return false
}

func (g *graph) changedFunctions(changes diff.Files) []string {
	added := map[string][]int{}
	for _, file := range changes {
		if path.Ext(file.Path) == ".go" && !strings.HasSuffix(file.Path, "_test.go") {
			added[file.Path] = file.ChangedLines()
		}
	}
	var out []string
	for key, fn := range g.funcs {
		if fn.route {
			continue
		}
		for _, line := range added[fn.file] {
			if line >= fn.start && line <= fn.end {
				out = append(out, key)
				break
			}
		}
	}
	slices.Sort(out)
	return out
}

type edge struct{ from, to string }

type edgeInfo struct {
	kind  Kind
	line  int
	order int
}

func (g *graph) draw(changed []string) *Diagram {
	d := &Diagram{}
	ids := map[string]string{}
	var order []string
	classes := map[string]string{}
	limit := downShare
	add := func(key, class string) bool {
		if _, ok := ids[key]; ok {
			return true
		}
		if len(order) >= limit {
			d.Truncated = true
			return false
		}
		ids[key] = "n" + strconv.Itoa(len(order))
		order = append(order, key)
		classes[key] = class
		return true
	}
	edges := map[edge]edgeInfo{}
	infoOf := func(s site) edgeInfo {
		if s.inferred {
			return edgeInfo{Inferred, s.line, s.order}
		}
		return edgeInfo{Resolved, s.line, s.order}
	}
	classOf := func(key string) string {
		if fn := g.funcs[key]; fn != nil && fn.route {
			return "route"
		}
		return ""
	}

	for _, key := range changed {
		add(key, "changed")
		d.Changed = append(d.Changed, g.funcs[key].label())
	}

	frontier := slices.Clone(changed)
	for depth := 1; depth <= maxDown && len(frontier) > 0; depth++ {
		var next []string
		for _, caller := range frontier {
			fn := g.funcs[caller]
			callees := make([]string, 0, len(fn.calls))
			for callee := range fn.calls {
				callees = append(callees, callee)
			}
			slices.Sort(callees)
			for _, callee := range callees {
				if !add(callee, classOf(callee)) {
					continue
				}
				if _, seen := edges[edge{caller, callee}]; !seen {
					edges[edge{caller, callee}] = infoOf(fn.calls[callee])
					next = append(next, callee)
				}
			}
			for _, name := range sortedKeys(fn.external) {
				key := "ext:" + name
				class := "external"
				if persistent(name) {
					class = "persistence"
				}
				if add(key, class) {
					edges[edge{caller, key}] = infoOf(fn.external[name])
				}
			}
			for _, name := range sortedKeys(fn.ambiguous) {
				key := "amb:" + name
				if add(key, "unresolved") {
					edges[edge{caller, key}] = edgeInfo{Unresolved, fn.ambiguous[name], 0}
				}
			}
		}
		frontier = next
	}

	limit = maxNodes
	frontier = slices.Clone(changed)
	for depth := 1; depth <= maxUp && len(frontier) > 0; depth++ {
		var next []string
		for _, callee := range frontier {
			for _, caller := range g.callers[callee] {
				if !add(caller, classOf(caller)) {
					continue
				}
				if _, seen := edges[edge{caller, callee}]; seen {
					continue
				}
				edges[edge{caller, callee}] = infoOf(g.funcs[caller].calls[callee])
				next = append(next, caller)
			}
		}
		if depth == maxUp && len(next) > 0 {
			d.Truncated = true
		}
		frontier = next
	}

	// A route is drawn whole: the handler that changed is one step in a chain,
	// and the steps before and after it are what the reader came for.
	for i := 0; i < len(order); i++ {
		fn := g.funcs[order[i]]
		if fn == nil || !fn.route {
			continue
		}
		steps := make([]string, 0, len(fn.calls))
		for step := range fn.calls {
			steps = append(steps, step)
		}
		slices.SortFunc(steps, func(a, b string) int { return fn.calls[a].order - fn.calls[b].order })
		for _, step := range steps {
			if add(step, classOf(step)) {
				if _, seen := edges[edge{order[i], step}]; !seen {
					edges[edge{order[i], step}] = infoOf(fn.calls[step])
				}
			}
		}
	}

	if len(edges) == 0 {
		return nil
	}

	for _, key := range order {
		if fn := g.funcs[key]; fn != nil && classes[key] == "" && len(g.callers[key]) == 0 {
			classes[key] = "entry"
			d.Entries = append(d.Entries, fn.label())
		}
	}
	slices.Sort(d.Entries)

	label := func(key string) string {
		if name, ok := strings.CutPrefix(key, "ext:"); ok {
			return clean(name)
		}
		if name, ok := strings.CutPrefix(key, "amb:"); ok {
			return "ambiguous." + clean(name)
		}
		return clean(g.funcs[key].label())
	}
	var b strings.Builder
	b.WriteString("flowchart LR\n")
	sorted := make([]edge, 0, len(edges))
	for e := range edges {
		sorted = append(sorted, e)
	}
	slices.SortFunc(sorted, func(a, b edge) int {
		if c := strings.Compare(ids[a.from], ids[b.from]); c != 0 {
			return c
		}
		return strings.Compare(ids[a.to], ids[b.to])
	})
	for _, key := range order {
		fmt.Fprintf(&b, "  %s[\"%s\"]", ids[key], label(key))
		if class := classes[key]; class != "" {
			fmt.Fprintf(&b, ":::%s", class)
		}
		b.WriteByte('\n')
		if fn := g.funcs[key]; fn != nil {
			d.Locations = append(d.Locations, Location{Label: label(key), Path: fn.file, Line: fn.start})
		}
	}
	for _, e := range sorted {
		info := edges[e]
		arrow := "-->"
		var text []string
		if info.kind != Resolved {
			text = append(text, string(info.kind))
		}
		if info.order > 0 {
			text = append(text, strconv.Itoa(info.order))
		}
		if info.kind != Resolved {
			arrow = "-.->|" + strings.Join(text, " ") + "|"
		} else if len(text) > 0 {
			arrow = "-->|" + strings.Join(text, " ") + "|"
		}
		fmt.Fprintf(&b, "  %s %s %s\n", ids[e.from], arrow, ids[e.to])
		d.Calls = append(d.Calls, Call{From: label(e.from), To: label(e.to), Kind: info.kind, Path: g.funcs[e.from].file, Line: info.line, Order: info.order})
	}
	b.WriteString("  classDef changed fill:#fde68a,stroke:#b45309,color:#000\n")
	b.WriteString("  classDef entry fill:#bfdbfe,stroke:#1d4ed8,color:#000\n")
	b.WriteString("  classDef external fill:#e5e7eb,stroke:#6b7280,color:#000,stroke-dasharray:4 3\n")
	b.WriteString("  classDef route fill:#d9f99d,stroke:#4d7c0f,color:#000\n")
	b.WriteString("  classDef persistence fill:#fbcfe8,stroke:#be185d,color:#000\n")
	b.WriteString("  classDef unresolved fill:#fecaca,stroke:#b91c1c,color:#000,stroke-dasharray:2 2\n")
	d.Mermaid = b.String()
	d.Nodes = len(order)
	d.Limits = g.limits(order, edges)
	return d
}

// limits reports what the graph could not see around the functions it drew.
// Each is a claim about this drawing, so a file the drawing never touches
// does not appear.
func (g *graph) limits(drawn []string, edges map[edge]edgeInfo) []string {
	var generated, reflecting, middleware []string
	unresolvedRoutes := 0
	ambiguous, shown := 0, 0
	dirs := map[string]bool{}
	for _, key := range drawn {
		fn := g.funcs[key]
		if fn == nil {
			continue
		}
		if g.generated[fn.file] {
			generated = append(generated, fn.label())
		}
		if fn.reflects {
			reflecting = append(reflecting, fn.label())
		}
		unresolvedRoutes += fn.unresolvedRoutes
		if fn.usesMiddleware {
			middleware = append(middleware, fn.label())
		}
		dirs[fn.dir] = true
		for name := range fn.ambiguous {
			ambiguous++
			if _, drawn := edges[edge{key, "amb:" + name}]; drawn {
				shown++
			}
		}
	}
	var out []string
	if ambiguous > 0 {
		out = append(out, fmt.Sprintf("%d method call(s) in the drawn functions match several methods by name and are not guessed; %d of them appear as ambiguous boxes, and the rest sit outside the drawing.", ambiguous, shown))
	}
	if len(reflecting) > 0 {
		out = append(out, "Files that import reflect or plugin can call functions this graph cannot see: "+listSome(reflecting)+".")
	}
	if unresolvedRoutes > 0 {
		out = append(out, fmt.Sprintf("%d route registration(s) in the drawn functions have handlers the graph could not name, so those handlers are missing from their chains.", unresolvedRoutes))
	}
	if len(middleware) > 0 {
		out = append(out, "Middleware added with Use runs on routes at run time and is not drawn: "+listSome(middleware)+".")
	}
	if len(generated) > 0 {
		out = append(out, "Generated code is drawn like any other, but its source of truth is elsewhere: "+listSome(generated)+".")
	}
	var unparsed []string
	for _, name := range g.unparsed {
		if dirs[path.Dir(name)] {
			unparsed = append(unparsed, name)
		}
	}
	if len(unparsed) > 0 {
		out = append(out, fmt.Sprintf("%d Go file(s) beside the drawn functions did not parse and are missing from the graph: %s.", len(unparsed), listSome(unparsed)))
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func listSome(names []string) string {
	const shown = 3
	names = slices.Clone(names)
	slices.Sort(names)
	if len(names) <= shown {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(names[:shown], ", "), len(names)-shown)
}

// clean keeps a label to characters a Go identifier or a path can carry, so
// source text cannot close the quoted label or add a Mermaid directive.
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '_', r == '.', r == '/', r == '-', r == ' ', r == ':', r == '{', r == '}', r == '*':
			return r
		}
		return '_'
	}, s)
}
