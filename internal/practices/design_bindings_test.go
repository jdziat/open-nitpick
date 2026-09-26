package practices

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

func TestDesignBindingsRespectLexicalScopeBoundaries(t *testing.T) {
	cases := []struct {
		name, source, identifier string
		free                     int
	}{
		{"initializer before short shadow", `func f() { value := value; _ = value }; var value int`, "value", 1},
		{"initializer before var shadow", `func f() { var value = value; _ = value }; var value int`, "value", 1},
		{"range expression before shadow", `func f() { for value := range value { _ = value }; _ = value }; var value []int`, "value", 2},
		{"if initializer ends with if", `func f() { if value := value; value > 0 { _ = value } else { _ = value }; _ = value }; var value int`, "value", 2},
		{"switch initializer ends with switch", `func f() { switch value := value; value {case 1: _ = value}; _ = value }; var value int`, "value", 2},
		{"type switch guard starts in each case body", `func f(x any) { switch value := x.(type) {case value: _ = value; default: _ = value}; _ = value }; type value int`, "value", 2},
		{"parameter names do not shadow signature types", `func f(value value) value { return value }; type value int`, "value", 2},
		{"later global type preserves local shadow", `func f() { value := 1; _ = value }; type value int`, "value", 0},
		{"type parameter covers signature and body", `func f[value any](x value) value { var y value; return y }`, "value", 0},
		{"generic receiver declares type parameters", `type Box[T any] struct {v T}; func (b *Box[value]) Get(x value) value { var v value; return v }`, "value", 0},
		{"labels use a separate namespace", `func f() { value: for { break value } }`, "value", 0},
		{"field names are declarations", `type T struct { value int }; func f(){ var v struct {value int}; _ = v }`, "value", 0},
		{"local type is in scope in its own definition", `func f() { type value struct { next *value }; var v value; _ = v }; type value int`, "value", 0},
		{"nested shadow stops at block end", `func f() { { value := 1; _ = value }; _ = value }; var value int`, "value", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), "scope.go", "package fixture\n"+tc.source, parser.SkipObjectResolution)
			if err != nil {
				t.Fatal(err)
			}
			bound := bindDesignNames(file)
			free := 0
			ast.Inspect(file, func(node ast.Node) bool {
				if name, ok := node.(*ast.Ident); ok && name.Name == tc.identifier && bound.free(name) {
					free++
				}
				return true
			})
			if free != tc.free {
				t.Fatalf("free %s references = %d, want %d", tc.identifier, free, tc.free)
			}
		})
	}
}
