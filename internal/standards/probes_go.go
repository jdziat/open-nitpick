package standards

import (
	"go/ast"
	"go/token"
	"strconv"
	"strings"

	"github.com/jdziat/open-nitpick/internal/config"
)

// docCommentName: an exported declaration carries a doc comment opening with
// its own name.
//
// Sites are exported top-level functions, methods and types. An undocumented
// one is a site that does not conform. It is not an absence. "How many
// exported declarations are documented" and "how many of the documented ones
// open correctly" are different rules with different denominators. This is the
// first of them.
//
// Grouped const and var declarations are deliberately not sites. The doc
// belongs to the block there, and one comment covering eight names cannot be
// judged against any single one of them.
//
// Neither is anything in a _test.go file. A test function is exported and
// godoc does not document it, so counting it asks whether the repository
// writes doc comments on its tests, which nobody intends and no reader wants.
// Including them read 1221/2000, 61%, contested. Excluding them reads
// 686/723, 95%, a standard, and the second number is the one about the rule.
//
// Nor is a method on an unexported receiver. `func (d *dryRunProvider) Name()`
// is an exported identifier that godoc renders nowhere, because the type it
// hangs off is not in the package's API. These are interface adapters, Go
// documents the interface rather than the adapter, and every one of the 37
// violations this probe reported on its own repository was one of them: 37
// false positives and no true ones. Corrected, this tree reads 683/683.
var docCommentName = Probe{
	ID:       "go-doc-comment-name",
	Class:    config.ClassStyle,
	Language: "go",
	Rule:     "Open an exported declaration's doc comment with the declaration's own name.",
	Why:      "godoc renders the comment as the entry for that name, and a reader greps for it.",
	sites: func(s *source) []Site {
		f, fset := s.goFile()
		if f == nil || strings.HasSuffix(s.path, "_test.go") {
			return nil
		}
		var out []Site
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if !d.Name.IsExported() || !exportedReceiver(d) {
					continue
				}
				out = append(out, s.site(fset, d.Pos(), opensWith(d.Doc, d.Name.Name)))
			case *ast.GenDecl:
				if d.Tok != token.TYPE {
					continue
				}
				for _, spec := range d.Specs {
					ts, ok := spec.(*ast.TypeSpec)
					if !ok || !ts.Name.IsExported() {
						continue
					}
					// A single-spec block carries its doc on the block, which
					// is the ordinary spelling of `type T struct{}`. A grouped
					// block carries it on each spec.
					doc := ts.Doc
					if doc == nil && len(d.Specs) == 1 {
						doc = d.Doc
					}
					out = append(out, s.site(fset, ts.Pos(), opensWith(doc, ts.Name.Name)))
				}
			}
		}
		return out
	},
}

// opensWith reports whether the comment group's FIRST line names the symbol.
//
// The first line, not the last. A doc comment is usually several lines and
// only the first repeats the name, so reading the line directly above the
// declaration answers a different question and answers it wrong: 42% of this
// tree against 98%, measured the day this package was written.
func opensWith(doc *ast.CommentGroup, name string) bool {
	if doc == nil || len(doc.List) == 0 {
		return false
	}
	// The first line that is prose. Directives are not prose and do not open
	// the sentence: `//go:build` and friends sit above the comment that does.
	text := ""
	for _, c := range doc.List {
		for _, t := range commentLines(c.Text) {
			if strings.HasPrefix(t, "go:") || t == "" {
				continue
			}
			text = t
			break
		}
		if text != "" {
			break
		}
	}
	rest, ok := strings.CutPrefix(text, name)
	if !ok {
		return false
	}
	// "Parse reads" opens with Parse; "Parser is" does not open with Parse.
	return rest == "" || !isIdentByte(rest[0])
}

// commentLines strips a comment's markers and returns its lines, trimmed.
//
// Both spellings, because `/* Alpha does a thing. */` is a legal doc comment
// and reading it as one token left every declaration documented that way
// counted as a violation, deflating the share on any tree that prefers the
// block form.
func commentLines(raw string) []string {
	if after, ok := strings.CutPrefix(raw, "/*"); ok {
		raw = strings.TrimSuffix(after, "*/")
		lines := strings.Split(raw, "\n")
		for i, l := range lines {
			lines[i] = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(l), "*"))
		}
		return lines
	}
	return []string{strings.TrimSpace(strings.TrimPrefix(raw, "//"))}
}

func isIdentByte(b byte) bool {
	return b == '_' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
}

// errorWrap: an error carried into fmt.Errorf is wrapped with %w.
//
// A site is a call to fmt.Errorf that has an error in hand. There is no type
// information here, so the test is the argument's name, and what the name test
// admits is the whole denominator. fmt.Errorf("no such user: %s", name) is not
// a site: there is no cause there to lose.
//
// Read the count as a share of the calls this naming can see, which is less
// than every call carrying an error. Accepted: a bare `err` or `somethingErr`,
// a selector ending the same way such as `r.Err`, and an `Error()` call. Not
// accepted: an error in a variable named `e` or `cause`, one pulled out of a
// slice, or one returned inline by a function whose name says nothing. A
// forgotten wrap hides in those spellings, and this probe does not count them
// either way.
//
// The first version accepted only a bare identifier. It read 200/200 on this
// repository, which is what a probe reports when the population it can see is
// idiomatic by construction: a bare `err` almost only appears in
// `if err != nil { return fmt.Errorf("...: %w", err) }`.
//
// The cost in the other direction is a `wantErr` holding a bool, which this
// counts and should not. A name is not a type.
var errorWrap = Probe{
	ID:       "go-error-wrap",
	Class:    config.ClassCorrectness,
	Language: "go",
	Rule:     "Wrap an error you are formatting into a new one with %w, not %v or %s.",
	Why:      "errors.Is and errors.As walk the wrapped chain; %v flattens the cause to text.",
	sites: func(s *source) []Site {
		f, fset := s.goFile()
		if f == nil {
			return nil
		}
		var out []Site
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || !isSelector(call.Fun, "fmt", "Errorf") || len(call.Args) < 2 {
				return true
			}
			if !carriesAnError(call.Args[1:]) {
				return true
			}
			format, ok := stringLit(call.Args[0])
			if !ok {
				// A format string built elsewhere cannot be read here, so this
				// probe has no opinion rather than a guess.
				return true
			}
			out = append(out, s.site(fset, call.Pos(), strings.Contains(format, "%w")))
			return true
		})
		return out
	},
}

// carriesAnError reports whether one of the arguments is spelled the way Go
// spells an error value.
func carriesAnError(args []ast.Expr) bool {
	for _, a := range args {
		if errorish(a) {
			return true
		}
	}
	return false
}

// errorish reads an expression's trailing name.
func errorish(e ast.Expr) bool {
	switch e := e.(type) {
	case *ast.Ident:
		return errorName(e.Name)
	case *ast.SelectorExpr:
		return errorName(e.Sel.Name)
	case *ast.CallExpr:
		// `err.Error()` and `x.Err()`, where the cause is being flattened to a
		// string on the way in.
		sel, ok := e.Fun.(*ast.SelectorExpr)
		return ok && errorName(sel.Sel.Name)
	}
	return false
}

func errorName(n string) bool {
	return n == "err" || n == "Err" || n == "Error" ||
		strings.HasSuffix(n, "Err") || strings.HasSuffix(n, "Error")
}

// contextFirstArg: a context.Context is the function's first parameter.
//
// Sites are functions and methods that take a context at all. A function
// without one is not a violation, so it is not counted, and the share is about
// placement rather than about how much of the tree is context-aware.
//
// Position only. The rule said "named ctx" for a while and nothing here read
// the name, so the count endorsed a convention it had never checked and
// AGENTS.md published it with a real-looking denominator. Whether the
// parameter is called ctx is a second claim and belongs to a second probe with
// its own count.
var contextFirstArg = Probe{
	ID:       "go-ctx-first-arg",
	Class:    config.ClassContract,
	Language: "go",
	Rule:     "Put context.Context first in the parameter list.",
	Why:      "callers pass it positionally, and a context anywhere else is a context somebody forgets.",
	sites: func(s *source) []Site {
		f, fset := s.goFile()
		if f == nil {
			return nil
		}
		var out []Site
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Type.Params == nil {
				continue
			}
			// By field rather than by parameter, which reads the same and is
			// the smaller thing to be right about: a context inside the first
			// field means the first parameter is a context, whatever the
			// field's other names are, and a context in any later field is not
			// first under either reading.
			at := -1
			for i, f := range fn.Type.Params.List {
				if isSelector(f.Type, "context", "Context") {
					at = i
					break
				}
			}
			if at < 0 {
				continue
			}
			out = append(out, s.site(fset, fn.Pos(), at == 0))
		}
		return out
	},
}

// namedResultNoNakedReturn: a function with named results returns its values
// explicitly.
//
// Sites are functions whose results are named. Nowhere else can a naked return
// occur. Naming results for documentation is common, and is not the thing
// measured here.
var namedResultNoNakedReturn = Probe{
	ID:       "go-no-naked-return",
	Class:    config.ClassStyle,
	Language: "go",
	Rule:     "Return named results explicitly; do not use a bare return.",
	Why:      "a bare return makes the reader scroll to the signature to learn what was returned.",
	sites: func(s *source) []Site {
		f, fset := s.goFile()
		if f == nil {
			return nil
		}
		var out []Site
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil || !hasNamedResults(fn.Type) {
				continue
			}
			naked := false
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				// A nested function literal has its own results and its own
				// naked returns, so it is not this function's site.
				if _, ok := n.(*ast.FuncLit); ok {
					return false
				}
				if r, ok := n.(*ast.ReturnStmt); ok && len(r.Results) == 0 {
					naked = true
				}
				return true
			})
			out = append(out, s.site(fset, fn.Pos(), !naked))
		}
		return out
	},
}

func hasNamedResults(t *ast.FuncType) bool {
	if t.Results == nil {
		return false
	}
	for _, f := range t.Results.List {
		if len(f.Names) > 0 {
			return true
		}
	}
	return false
}

// testNameSentence: a test's name says what behaviour it pins.
//
// Sites are test functions. Conformance is at least three words after the Test
// prefix, which is the shortest name that can carry a subject and a claim:
// TestParseRejectsEmptyInput does, TestParse does not.
//
// A word count is a coarse instrument for a rule about meaning, and it is here
// because the alternative is a model deciding what a good name is on every
// run. If this repository's share sits under the floor, the rule does not get
// written down, which is the floor doing its job rather than the probe failing.
var testNameSentence = Probe{
	ID:       "go-test-name-sentence",
	Class:    config.ClassTests,
	Language: "go",
	Rule:     "Name a test after the behaviour it pins, in at least three words.",
	Why:      "the name is what a failing run prints, and it should say what broke.",
	sites: func(s *source) []Site {
		f, fset := s.goFile()
		if f == nil || !strings.HasSuffix(s.path, "_test.go") {
			return nil
		}
		var out []Site
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !strings.HasPrefix(fn.Name.Name, "Test") {
				continue
			}
			// TestMain is the fixture entry point the testing package names,
			// not a test whose name anybody chose.
			if fn.Name.Name == "TestMain" {
				continue
			}
			out = append(out, s.site(fset, fn.Pos(), words(fn.Name.Name[len("Test"):]) >= 3))
		}
		return out
	},
}

// words counts the CamelCase words in an identifier.
//
// A run of capitals is one word. HTTPServer is two, not nine. A capital starts
// a word when the character before it is not a capital, or when the character
// after it is lower-case. That second case is the tail of an acronym.
func words(s string) int {
	r := []rune(s)
	n := 0
	for i, c := range r {
		if !upper(c) {
			continue
		}
		prevUpper := i > 0 && upper(r[i-1])
		nextLower := i+1 < len(r) && lower(r[i+1])
		if !prevUpper || nextLower {
			n++
		}
	}
	if n == 0 && s != "" {
		n = 1
	}
	return n
}

func upper(r rune) bool { return r >= 'A' && r <= 'Z' }
func lower(r rune) bool { return r >= 'a' && r <= 'z' }

// testHelperMarks: a test helper calls t.Helper.
//
// Sites are functions in a test file that take *testing.T and are not
// themselves tests. Without the call, every failure the helper reports points
// at the helper's own line, and the test that called it is the thing the
// reader needs.
var testHelperMarks = Probe{
	ID:       "go-test-helper-marks",
	Class:    config.ClassTests,
	Language: "go",
	Rule:     "Call t.Helper() at the top of a test helper.",
	Why:      "without it a failure is reported at the helper's line, not the caller's.",
	sites: func(s *source) []Site {
		f, fset := s.goFile()
		if f == nil || !strings.HasSuffix(s.path, "_test.go") {
			return nil
		}
		var out []Site
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil || fn.Recv != nil {
				continue
			}
			if strings.HasPrefix(fn.Name.Name, "Test") || strings.HasPrefix(fn.Name.Name, "Benchmark") ||
				strings.HasPrefix(fn.Name.Name, "Fuzz") || strings.HasPrefix(fn.Name.Name, "Example") {
				continue
			}
			name, ok := testingParam(fn.Type.Params)
			if !ok {
				continue
			}
			out = append(out, s.site(fset, fn.Pos(), callsHelper(fn.Body, name)))
		}
		return out
	},
}

// testingParam returns the name a *testing.T or *testing.B parameter is bound
// to. A blank or missing name means the helper cannot call Helper at all, so
// it is not a site.
func testingParam(fl *ast.FieldList) (string, bool) {
	if fl == nil {
		return "", false
	}
	for _, f := range fl.List {
		// *testing.T, *testing.B, and the bare testing.TB interface, which is
		// not a StarExpr and was silently missing from the denominator. A
		// helper written against TB is usually the more disciplined one, so
		// dropping them biased the share downward.
		t := f.Type
		if star, ok := t.(*ast.StarExpr); ok {
			t = star.X
		}
		if !isSelector(t, "testing", "T") && !isSelector(t, "testing", "B") &&
			!isSelector(t, "testing", "TB") {
			continue
		}
		if len(f.Names) == 0 || f.Names[0].Name == "_" {
			return "", false
		}
		return f.Names[0].Name, true
	}
	return "", false
}

func callsHelper(body *ast.BlockStmt, recv string) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if ok && isSelector(call.Fun, recv, "Helper") {
			found = true
		}
		return !found
	})
	return found
}

// isSelector reports whether the expression is exactly pkg.Name.
func isSelector(e ast.Expr, pkg, name string) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == pkg
}

// stringLit returns the value of a plain string literal.
func stringLit(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	v, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return v, true
}

// exportedReceiver reports whether a declaration's receiver, if it has one, is
// a type godoc will render.
//
// A method on an unexported type is not API however exported its own name is,
// and the doc-comment rule is about what godoc renders. Handles `T`, `*T` and
// the generic forms `T[U]` and `*T[U, V]`.
func exportedReceiver(d *ast.FuncDecl) bool {
	if d.Recv == nil || len(d.Recv.List) == 0 {
		return true
	}
	t := d.Recv.List[0].Type
	if star, ok := t.(*ast.StarExpr); ok {
		t = star.X
	}
	if idx, ok := t.(*ast.IndexExpr); ok {
		t = idx.X
	}
	if idx, ok := t.(*ast.IndexListExpr); ok {
		t = idx.X
	}
	id, ok := t.(*ast.Ident)
	return ok && id.IsExported()
}
