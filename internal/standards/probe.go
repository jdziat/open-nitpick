// Package standards measures what this repository has decided for itself.
//
// internal/knowledge carries facts a model was not taught, each citing a
// source outside the tree. Nothing carried the conventions the tree arrived at
// on its own, so a reviewer that knows the language still had to guess the
// house style, and so did every coding agent opening the repository for the
// first time.
//
// A convention here is a count, never an assertion. A probe names the sites it
// has an opinion about, says which of them conform, and the share is the whole
// claim. That is what makes a rule retirable: when the code stops doing the
// thing, the number falls and the rule stops being written down, with nobody
// having to notice.
//
// The denominator is the part that goes wrong. Asking this tree whether an
// exported declaration's doc comment opens with its name returns 42% when the
// probe reads the line directly above the declaration and 98% when it walks to
// the first line of the comment block, because a multi-line comment ends on a
// line that does not repeat the name. Same question, same tree, opposite
// answers, and the wrong one would have kept this repository's strongest
// convention out of its own AGENTS.md. Every probe here owes its denominator a
// test naming what is deliberately not a site.
package standards

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"sort"
	"strings"

	"github.com/jdziat/open-nitpick/internal/config"
)

// Probe is one checkable claim about how this repository writes code.
//
// Probes are compiled in and reviewed like any other code. Configuration can
// switch one off and can move the floor, and cannot add one: a probe supplied
// at runtime is a measurement whose instrument nobody read.
type Probe struct {
	// ID is the stable identifier. It appears in reports, in AGENTS.md and in
	// the disabled list, so it never changes once shipped.
	ID string

	// Class is the review class this probe's rule belongs to, from the same
	// taxonomy knowledge entries are routed by.
	//
	// What decides which pass sees it. A style convention handed to the defect
	// pass is the dilution the generation scope exists to prevent, arriving as
	// reference material instead of as a prompt.
	Class config.Class

	// Language is the language this probe reads, as internal/bundle.Language
	// spells it. A file of any other language is not offered to it, which is
	// what keeps a Go probe from reporting 100% over a TypeScript tree it
	// never looked at.
	Language string

	// Rule is the convention as an imperative, the line AGENTS.md prints.
	// It states what to do rather than naming a topic, because an agent can
	// act on "wrap an in-scope error with %w" and cannot act on "errors".
	Rule string

	// Why is the one sentence the report prints beside a violation. What the
	// rule buys, not a restatement of the rule.
	Why string

	// sites reports every place this probe has an opinion about, conforming or
	// not. Unexported because a probe is defined in this package and read
	// through Probes, never assembled by a caller.
	sites func(s *source) []Site
}

// Site is one place a probe had an opinion about.
type Site struct {
	Path     string `json:"path"`
	Line     int    `json:"line"`
	Conforms bool   `json:"conforms"`

	// Excerpt is the source line, trimmed. It is what makes a reported
	// violation checkable without opening the file.
	Excerpt string `json:"excerpt"`
}

// source is one file, parsed at most once however many probes read it.
//
// Six probes over a tree is six parses per file if each parses for itself, and
// the parse dominates the walk.
type source struct {
	path  string
	lines []string

	fset *token.FileSet
	file *ast.File

	// parsed records that parsing was attempted, so a file that does not
	// compile is skipped once rather than retried by every probe. A tree with
	// a broken file still reports on the rest of itself.
	parsed bool

	// pkg is what the file's whole package declares, for a probe whose
	// question is not answerable from one file.
	//
	// A constructor lives beside its type far more often than in the same
	// file, so a per-file reading of "does this type have one" reported this
	// repository at 27% against a true 75%. A probe about a package has to be
	// given the package.
	pkg *packageIndex
}

// packageIndex is what one directory's files declare between them.
type packageIndex struct {
	// constructors are the names after a New prefix, so NewEngine is "Engine"
	// and a package-level New is "".
	constructors map[string]bool
}

// newSource reads one file's text without parsing it.
func newSource(path string, src []byte, pkg *packageIndex) *source {
	return &source{path: path, lines: strings.Split(string(src), "\n"), pkg: pkg}
}

// indexPackages reads what each directory declares, once, before any probe
// looks at a file.
func indexPackages(files []File) map[string]*packageIndex {
	out := map[string]*packageIndex{}
	for _, f := range files {
		if f.Src == nil || !strings.HasSuffix(f.Path, ".go") || strings.HasSuffix(f.Path, "_test.go") {
			continue
		}
		dir := path.Dir(f.Path)
		ix := out[dir]
		if ix == nil {
			ix = &packageIndex{constructors: map[string]bool{}}
			out[dir] = ix
		}
		fset := token.NewFileSet()
		parsed, err := parser.ParseFile(fset, f.Path, f.Src, 0)
		if err != nil {
			continue
		}
		for _, d := range parsed.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Recv != nil {
				continue
			}
			if name, found := strings.CutPrefix(fn.Name.Name, "New"); found {
				ix.constructors[name] = true
			}
		}
	}
	return out
}

// goFile parses the file as Go, returning nil when it does not parse.
//
// A file that does not compile is not a violation of anything. Counting it as
// one would make a branch mid-refactor look like a repository that abandoned
// its conventions.
func (s *source) goFile() (*ast.File, *token.FileSet) {
	if !s.parsed {
		s.parsed = true
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, s.path, strings.Join(s.lines, "\n"), parser.ParseComments)
		if err == nil {
			s.fset, s.file = fset, f
		}
	}
	return s.file, s.fset
}

// at returns the trimmed source line, empty when the number is out of range.
func (s *source) at(line int) string {
	if line < 1 || line > len(s.lines) {
		return ""
	}
	return strings.TrimSpace(s.lines[line-1])
}

// site builds a Site at a position, reading the excerpt from the file.
func (s *source) site(fset *token.FileSet, pos token.Pos, conforms bool) Site {
	line := fset.Position(pos).Line
	return Site{Path: s.path, Line: line, Conforms: conforms, Excerpt: s.at(line)}
}

// Probes are every probe this build knows, in report order.
//
// Ordered by how load-bearing the convention is rather than alphabetically,
// because the order is what a reader scanning the report sees first.
var Probes = []Probe{
	docCommentName,
	errorWrap,
	contextFirstArg,
	namedResultNoNakedReturn,
	testNameSentence,
	testHelperMarks,
}

// Find returns the probe with the given ID.
func Find(id string) (Probe, bool) {
	for _, p := range Probes {
		if p.ID == id {
			return p, true
		}
	}
	return Probe{}, false
}

// Languages are the languages some probe reads, sorted.
//
// A caller reports "no probes for python" from this rather than from an empty
// result, so silence over an unprobed language is distinguishable from a tree
// that conforms. See docs/measurement.md Rule 10.
func Languages() []string {
	seen := map[string]bool{}
	for _, p := range Probes {
		seen[p.Language] = true
	}
	out := make([]string, 0, len(seen))
	for l := range seen {
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}
