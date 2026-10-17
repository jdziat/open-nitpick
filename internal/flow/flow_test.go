package flow

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/diff"
	"github.com/jdziat/open-nitpick/internal/standards"
)

func src(name, body string) standards.File {
	return standards.File{Path: name, Src: []byte(body)}
}

// edit reports lines [from, to] of name as added.
func edit(name string, from, to int) diff.Files {
	var lines []diff.Line
	for n := from; n <= to; n++ {
		lines = append(lines, diff.Line{Kind: diff.LineAdded, NewLine: n})
	}
	return diff.Files{{Path: name, Kind: diff.ChangeModified, Hunks: []diff.Hunk{{Lines: lines}}}}
}

func chain() []standards.File {
	return []standards.File{
		src("go.mod", "module example.com/app\n\ngo 1.22\n"),
		src("cmd/app/main.go", "package main\n\nimport \"example.com/app/internal/run\"\n\nfunc main() { run.Dispatch() }\n"),
		src("internal/run/run.go", "package run\n\nimport \"example.com/app/internal/analyze\"\n\nfunc Dispatch() { analyze.Check() }\n"),
		src("internal/analyze/analyze.go", "package analyze\n\nimport \"os\"\n\nfunc Check() { write() }\n\nfunc write() {\n\tos.WriteFile(\"x\", nil, 0o600)\n}\n\nfunc unrelated() {}\n"),
	}
}

func TestBuildShowsTheCommandThatReachesAChangedFunction(t *testing.T) {
	d := Build(chain(), edit("internal/analyze/analyze.go", 5, 5))
	if d == nil {
		t.Fatal("no diagram for a function with callers and callees")
	}
	if len(d.Changed) != 1 || d.Changed[0] != "internal/analyze.Check" {
		t.Fatalf("changed = %v", d.Changed)
	}
	if len(d.Entries) != 1 || d.Entries[0] != "cmd/app.main" {
		t.Fatalf("entries = %v, want the main function that starts the chain", d.Entries)
	}
	for _, want := range []string{"flowchart LR", "internal/run.Dispatch", "internal/analyze.write", "os.WriteFile", ":::changed", ":::entry", ":::persistence"} {
		if !strings.Contains(d.Mermaid, want) {
			t.Errorf("diagram lacks %q:\n%s", want, d.Mermaid)
		}
	}
}

func TestBuildDrawsNothingForAnEditOutsideAnyConnectedFunction(t *testing.T) {
	if d := Build(chain(), edit("internal/analyze/analyze.go", 10, 10)); d != nil {
		t.Fatalf("an unconnected function drew a diagram:\n%s", d.Mermaid)
	}
	if d := Build(chain(), edit("internal/analyze/analyze.go", 1, 3)); d != nil {
		t.Fatalf("an edit outside every function drew a diagram:\n%s", d.Mermaid)
	}
	if d := Build(chain(), edit("README.md", 1, 3)); d != nil {
		t.Fatalf("a non-Go edit drew a diagram:\n%s", d.Mermaid)
	}
}

func TestBuildKeepsSourceTextOutOfTheMermaidSyntax(t *testing.T) {
	files := []standards.File{
		src("go.mod", "module example.com/app\n"),
		src("a\"]-->evil[\"x/a.go", "package a\n\nfunc Top() { Leaf() }\n\nfunc Leaf() {}\n"),
	}
	d := Build(files, edit("a\"]-->evil[\"x/a.go", 5, 5))
	if d == nil {
		t.Fatal("no diagram")
	}
	for _, line := range strings.Split(d.Mermaid, "\n") {
		if strings.Contains(line, "[\"") && strings.Count(line, "\"") != 2 {
			t.Errorf("a label escaped its quotes: %s", line)
		}
		if strings.Contains(line, "evil") && strings.Contains(line, "-->") && strings.Contains(line, "evil[") {
			t.Errorf("source text added an edge: %s", line)
		}
	}
}

func TestBuildStopsAtTheNodeCeilingAndSaysSo(t *testing.T) {
	var b strings.Builder
	b.WriteString("package p\n\nfunc Leaf() {}\n")
	for i := range maxNodes + 10 {
		fmt.Fprintf(&b, "\nfunc Caller%d() { Leaf() }\n", i)
	}
	files := []standards.File{src("go.mod", "module example.com/app\n"), src("p/p.go", b.String())}
	d := Build(files, edit("p/p.go", 3, 3))
	if d == nil {
		t.Fatal("no diagram")
	}
	if d.Nodes != maxNodes || !d.Truncated {
		t.Fatalf("nodes = %d truncated = %v, want %d and true", d.Nodes, d.Truncated, maxNodes)
	}
}

func TestBuildIgnoresACallThroughAShadowingParameter(t *testing.T) {
	files := []standards.File{
		src("go.mod", "module example.com/app\n"),
		src("p/p.go", "package p\n\nfunc Leaf() {}\n\nfunc Top(Leaf func()) { Leaf() }\n"),
	}
	if d := Build(files, edit("p/p.go", 3, 3)); d != nil {
		t.Fatalf("a parameter named Leaf was drawn as a call to the function Leaf:\n%s", d.Mermaid)
	}
}

func hasCall(d *Diagram, from, to string, kind Kind) (Call, bool) {
	for _, c := range d.Calls {
		if c.From == from && c.To == to && c.Kind == kind {
			return c, true
		}
	}
	return Call{}, false
}

func TestBuildLocatesEachDrawnFunctionAndCall(t *testing.T) {
	d := Build(chain(), edit("internal/analyze/analyze.go", 5, 5))
	if d == nil {
		t.Fatal("no diagram")
	}
	call, ok := hasCall(d, "internal/run.Dispatch", "internal/analyze.Check", Resolved)
	if !ok || call.Path != "internal/run/run.go" || call.Line != 5 {
		t.Fatalf("call = %+v ok=%v, want the Dispatch call on line 5 of run.go", call, ok)
	}
	var found bool
	for _, l := range d.Locations {
		if l.Label == "internal/analyze.write" && l.Path == "internal/analyze/analyze.go" && l.Line == 7 {
			found = true
		}
	}
	if !found {
		t.Fatalf("locations = %+v, want write declared at analyze.go:7", d.Locations)
	}
}

func methods(extra string) []standards.File {
	return []standards.File{
		src("go.mod", "module example.com/app\n"),
		src("p/p.go", "package p\n\ntype A struct{}\n\nfunc (A) Save() {}\n\nfunc Top(a A) { a.Save() }\n"+extra),
	}
}

func TestBuildMarksAMethodMatchedByNameAsInferred(t *testing.T) {
	d := Build(methods(""), edit("p/p.go", 5, 5))
	if d == nil {
		t.Fatal("no diagram")
	}
	if _, ok := hasCall(d, "p.Top", "p.A.Save", Inferred); !ok {
		t.Fatalf("calls = %+v, want an inferred Top to A.Save", d.Calls)
	}
	if !strings.Contains(d.Mermaid, "-.->|inferred|") {
		t.Fatalf("inferred call drawn as a solid arrow:\n%s", d.Mermaid)
	}
}

func TestBuildDrawsAnAmbiguousMethodAsUnresolvedNotGuessed(t *testing.T) {
	d := Build(methods("\ntype B struct{}\n\nfunc (B) Save() {}\n"), edit("p/p.go", 7, 7))
	if d == nil {
		t.Fatal("no diagram")
	}
	if _, ok := hasCall(d, "p.Top", "p.A.Save", Inferred); ok {
		t.Fatalf("an ambiguous method was resolved to one candidate: %+v", d.Calls)
	}
	if _, ok := hasCall(d, "p.Top", "ambiguous.Save", Unresolved); !ok {
		t.Fatalf("calls = %+v, want an unresolved Top to ambiguous.Save", d.Calls)
	}
	if !strings.Contains(strings.Join(d.Limits, " "), "match several methods") {
		t.Fatalf("limits = %v, want the ambiguity reported", d.Limits)
	}
}

func TestBuildReportsReflectionGeneratedCodeAndUnparsedFiles(t *testing.T) {
	files := []standards.File{
		src("go.mod", "module example.com/app\n"),
		src("p/p.go", "package p\n\nimport \"reflect\"\n\nfunc Top() { Leaf(); _ = reflect.TypeOf(1) }\n\nfunc Leaf() {}\n"),
		src("p/gen.go", "// Code generated by tool. DO NOT EDIT.\n\npackage p\n\nfunc Gen() { Leaf() }\n"),
		src("p/broken.go", "package p\n\nfunc {"),
	}
	d := Build(files, edit("p/p.go", 7, 7))
	if d == nil {
		t.Fatal("no diagram")
	}
	limits := strings.Join(d.Limits, "\n")
	for _, want := range []string{"reflect or plugin", "p.Top", "Generated code", "p.Gen", "did not parse", "p/broken.go"} {
		if !strings.Contains(limits, want) {
			t.Errorf("limits lack %q:\n%s", want, limits)
		}
	}
}

func TestBuildReportsNoLimitsForAPlainGraph(t *testing.T) {
	d := Build(chain(), edit("internal/analyze/analyze.go", 5, 5))
	if d == nil || len(d.Limits) != 0 {
		t.Fatalf("a graph with nothing hidden reported limits: %+v", d)
	}
}

func web() []standards.File {
	return []standards.File{
		src("go.mod", "module example.com/web\n"),
		src("cmd/web/main.go", "package main\n\nimport (\n\t\"net/http\"\n\n\t\"example.com/web/internal/api\"\n)\n\nfunc main() {\n\tmux := http.NewServeMux()\n\tapi.Register(mux)\n}\n"),
		src("internal/api/api.go", "package api\n\nimport (\n\t\"database/sql\"\n\t\"net/http\"\n)\n\nfunc Register(mux *http.ServeMux) {\n\tmux.Use(logging)\n\tmux.HandleFunc(\"GET /users\", auth, list)\n}\n\nfunc auth(next http.HandlerFunc) http.HandlerFunc { return next }\n\nfunc logging() {}\n\nfunc list(w http.ResponseWriter, r *http.Request) {\n\tvar db *sql.DB\n\tdb.Query(\"select 1\")\n}\n"),
	}
}

func TestBuildDrawsARouteWithItsHandlersInOrderAndTheDatabaseItReaches(t *testing.T) {
	d := Build(web(), edit("internal/api/api.go", 20, 20))
	if d == nil {
		t.Fatal("no diagram")
	}
	for _, want := range []string{"GET /users", ":::route", "|1|", ":::persistence", "database/sql.Query"} {
		if !strings.Contains(d.Mermaid, want) {
			t.Errorf("diagram lacks %q:\n%s", want, d.Mermaid)
		}
	}
	var chain []int
	for _, c := range d.Calls {
		if c.From == "GET /users" {
			chain = append(chain, c.Order)
		}
	}
	slices.Sort(chain)
	if !slices.Equal(chain, []int{1, 2}) {
		t.Fatalf("route handler orders = %v, want 1 and 2", chain)
	}
}

func TestBuildNumbersEveryHandlerInARoutesChain(t *testing.T) {
	d := Build(web(), edit("internal/api/api.go", 13, 13))
	if d == nil {
		t.Fatal("no diagram")
	}
	orders := map[string]int{}
	for _, c := range d.Calls {
		if c.From == "GET /users" {
			orders[c.To] = c.Order
		}
	}
	if orders["internal/api.auth"] != 1 || orders["internal/api.list"] != 2 {
		t.Fatalf("handler order = %v, want auth then list", orders)
	}
}

func TestBuildSaysThatMiddlewareAddedWithUseIsNotDrawn(t *testing.T) {
	d := Build(web(), edit("internal/api/api.go", 13, 13))
	if d == nil || !strings.Contains(strings.Join(d.Limits, " "), "Middleware added with Use") {
		t.Fatalf("limits = %+v, want the Use limit", d)
	}
}

func TestBuildDoesNotTakeAPlainMethodCallForARoute(t *testing.T) {
	files := []standards.File{
		src("go.mod", "module example.com/web\n"),
		src("p/p.go", "package p\n\nfunc Top(c Client) { c.Get(\"x\", Leaf) }\n\nfunc Leaf() {}\n"),
	}
	d := Build(files, edit("p/p.go", 5, 5))
	if d != nil && strings.Contains(d.Mermaid, ":::route") {
		t.Fatalf("a client call with a string argument was drawn as a route:\n%s", d.Mermaid)
	}
}

func TestBuildTreatsADatabaseShapedMethodAsInferred(t *testing.T) {
	d := Build(web(), edit("internal/api/api.go", 20, 20))
	if d == nil {
		t.Fatal("no diagram")
	}
	if _, ok := hasCall(d, "internal/api.list", "database/sql.Query", Inferred); !ok {
		t.Fatalf("calls = %+v, want an inferred list to database/sql.Query", d.Calls)
	}
}

func TestBuildOnlyTreatsRouterVerbsAsRouteRegistrations(t *testing.T) {
	files := []standards.File{
		src("go.mod", "module example.com/web\n"),
		src("p/p.go", "package p\n\nfunc Top(r Router) { r.Mount(\"/x\", Leaf) }\n\nfunc Leaf() {}\n"),
	}
	d := Build(files, edit("p/p.go", 5, 5))
	if d != nil && strings.Contains(d.Mermaid, ":::route") {
		t.Fatalf("a method that is not a router verb was drawn as a route:\n%s", d.Mermaid)
	}
}

func TestBuildLeavesOutUnparsedFilesInPackagesItDidNotDraw(t *testing.T) {
	files := []standards.File{
		src("go.mod", "module example.com/app\n"),
		src("p/p.go", "package p\n\nfunc Top() { Leaf() }\n\nfunc Leaf() {}\n"),
		src("elsewhere/broken.go", "package q\n\nfunc {"),
	}
	d := Build(files, edit("p/p.go", 5, 5))
	if d == nil {
		t.Fatal("no diagram")
	}
	if strings.Contains(strings.Join(d.Limits, " "), "did not parse") {
		t.Fatalf("a file in a package the drawing never touches was reported: %v", d.Limits)
	}
}

func TestBuildCountsAmbiguousCallsButClaimsOnlyTheOnesItDrew(t *testing.T) {
	var b strings.Builder
	b.WriteString("package p\n\ntype A struct{}\n\nfunc (A) Save() {}\n\ntype B struct{}\n\nfunc (B) Save() {}\n\nfunc Top(a A) {\n")
	for i := range maxNodes + 5 {
		fmt.Fprintf(&b, "\tLeaf%d()\n\ta.Save()\n", i)
	}
	b.WriteString("}\n")
	for i := range maxNodes + 5 {
		fmt.Fprintf(&b, "\nfunc Leaf%d() {}\n", i)
	}
	files := []standards.File{src("go.mod", "module example.com/app\n"), src("p/p.go", b.String())}
	d := Build(files, edit("p/p.go", 11, 11))
	if d == nil {
		t.Fatal("no diagram")
	}
	if _, drew := hasCall(d, "p.Top", "ambiguous.Save", Unresolved); drew {
		t.Skip("the node ceiling did not cut the ambiguous call; the test cannot tell")
	}
	limits := strings.Join(d.Limits, " ")
	if !strings.Contains(limits, "match several methods") || !strings.Contains(limits, "0 of them appear") {
		t.Fatalf("an ambiguous call that was cut from the diagram must be counted but not claimed as drawn: %v", d.Limits)
	}
}

// The registering function is the edit; the route it declares is not a
// function and must not be reported as one, or lose its own colour.
func TestBuildKeepsARouteOutOfTheEditedFunctions(t *testing.T) {
	d := Build(web(), edit("internal/api/api.go", 10, 10))
	if d == nil {
		t.Fatal("no diagram")
	}
	if !slices.Equal(d.Changed, []string{"internal/api.Register"}) {
		t.Fatalf("changed = %v, want only the registering function", d.Changed)
	}
	if strings.Contains(d.Mermaid, "GET /users\"]:::changed") || !strings.Contains(d.Mermaid, "GET /users\"]:::route") {
		t.Fatalf("the route box lost its class:\n%s", d.Mermaid)
	}
}

func TestBuildCannotLetARoutePathCloseTheFence(t *testing.T) {
	files := []standards.File{
		src("go.mod", "module example.com/web\n"),
		src("p/p.go", "package p\n\nfunc Top(r Router) { r.Get(\"/a\u0060\u0060\u0060b\", Leaf) }\n\nfunc Leaf() {}\n"),
	}
	d := Build(files, edit("p/p.go", 5, 5))
	if d == nil {
		t.Fatal("no diagram")
	}
	if strings.Contains(d.Mermaid, "\u0060") {
		t.Fatalf("a backtick from a route path reached the diagram:\n%s", d.Mermaid)
	}
}

func TestBuildPutsAWrapperAndTheHandlerItWrapsIntoTheChain(t *testing.T) {
	files := []standards.File{
		src("go.mod", "module example.com/web\n"),
		src("p/p.go", "package p\n\nfunc Register(r Router) { r.Get(\"/x\", auth(list)) }\n\nfunc auth(h func()) func() { return h }\n\nfunc list() {}\n"),
	}
	d := Build(files, edit("p/p.go", 7, 7))
	if d == nil {
		t.Fatal("no diagram")
	}
	orders := map[string]int{}
	for _, c := range d.Calls {
		if c.From == "GET /x" {
			orders[c.To] = c.Order
		}
	}
	if orders["p.auth"] != 1 || orders["p.list"] != 2 {
		t.Fatalf("chain = %v, want auth then the handler it wraps", orders)
	}
}

func TestBuildReportsARouteWhoseHandlersItCouldNotName(t *testing.T) {
	files := []standards.File{
		src("go.mod", "module example.com/web\n"),
		src("p/p.go", "package p\n\nfunc Register(r Router, h func()) { r.Get(\"/x\", h); Leaf() }\n\nfunc Leaf() {}\n"),
	}
	d := Build(files, edit("p/p.go", 3, 3))
	if d == nil {
		t.Fatal("no diagram")
	}
	if !strings.Contains(strings.Join(d.Limits, " "), "could not name") {
		t.Fatalf("limits = %v, want the unnamed handler reported", d.Limits)
	}
}

// A function with more callers than the diagram has room for must still show
// the effect it has: the callee pass runs first and keeps its share.
func TestBuildKeepsADownstreamEffectWhenCallersFillTheDiagram(t *testing.T) {
	var b strings.Builder
	b.WriteString("package p\n\nimport \"os\"\n\nfunc Target() { os.WriteFile(\"x\", nil, 0o600) }\n")
	for i := range maxNodes * 2 {
		fmt.Fprintf(&b, "\nfunc Caller%d() { Target() }\n", i)
	}
	files := []standards.File{src("go.mod", "module example.com/app\n"), src("p/p.go", b.String())}
	d := Build(files, edit("p/p.go", 5, 5))
	if d == nil {
		t.Fatal("no diagram")
	}
	if !strings.Contains(d.Mermaid, "os.WriteFile") || !strings.Contains(d.Mermaid, ":::persistence") {
		t.Fatalf("callers crowded out the file write:\n%s", d.Mermaid)
	}
	if d.Nodes != maxNodes || !d.Truncated {
		t.Fatalf("nodes = %d truncated = %v, want the full ceiling and a truncation notice", d.Nodes, d.Truncated)
	}
}

// The reverse: a function that calls more than the diagram holds must still
// show who reaches it, because "which command gets here" is the first question.
func TestBuildKeepsACallerWhenCalleesFillTheDiagram(t *testing.T) {
	var b strings.Builder
	b.WriteString("package p\n\nfunc Entry() { Target() }\n\nfunc Target() {\n")
	for i := range maxNodes * 2 {
		fmt.Fprintf(&b, "\tLeaf%d()\n", i)
	}
	b.WriteString("}\n")
	for i := range maxNodes * 2 {
		fmt.Fprintf(&b, "\nfunc Leaf%d() {}\n", i)
	}
	files := []standards.File{src("go.mod", "module example.com/app\n"), src("p/p.go", b.String())}
	d := Build(files, edit("p/p.go", 5, 5))
	if d == nil {
		t.Fatal("no diagram")
	}
	if !slices.Equal(d.Entries, []string{"p.Entry"}) {
		t.Fatalf("entries = %v, want p.Entry kept despite %d callees", d.Entries, maxNodes*2)
	}
}
