// Package docgen renders the configuration reference from the configuration
// itself.
//
// The reference is generated rather than written because a hand-kept table of
// every key drifts, silently, in the direction of the code being ahead. Six
// keys were already missing from the prose when this was added, and nothing
// failed to say so. Here a key that exists is a row, and a row that exists is
// a key.
//
// Two sources are read. Reflection over the zero and default values supplies
// the path, the type and the shipped default; the package's own source supplies
// each field's doc comment, because a struct tag carries no prose and a
// reference without prose is a list of names.
package docgen

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"reflect"
	"sort"
	"strings"
)

// Field is one configuration key as the reference reports it.
type Field struct {
	Path    string // dotted yaml path, e.g. review.concurrency
	Type    string // the shape a value takes, in the words a writer of yaml uses
	Default string // the shipped value, or empty when there is none
	Doc     string // the first sentence of the declaration's doc comment
}

// Docs maps "TypeName.FieldName" to the field's doc comment, read from the Go
// source of the package the configuration lives in.
type Docs map[string]string

// ReadDocs parses every non-test file in dir and collects struct field
// comments.
//
// A field with a comment above it and one beside it has both, in that order,
// which is how the configuration is written today.
func ReadDocs(dir string) (Docs, error) {
	set := token.NewFileSet()
	pkgs, err := parser.ParseDir(set, dir, func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("docgen: parse %s: %w", dir, err)
	}

	out := Docs{}
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			ast.Inspect(file, func(n ast.Node) bool {
				ts, ok := n.(*ast.TypeSpec)
				if !ok {
					return true
				}
				st, ok := ts.Type.(*ast.StructType)
				if !ok {
					return true
				}
				for _, f := range st.Fields.List {
					text := f.Doc.Text()
					if text == "" {
						text = f.Comment.Text()
					}
					for _, name := range f.Names {
						if s := firstSentence(text); s != "" {
							out[ts.Name.Name+"."+name.Name] = s
						}
					}
				}
				return true
			})
		}
	}
	return out, nil
}

// firstSentence trims a doc comment to the one sentence a table cell can hold.
//
// The comments in this package run to paragraphs, and deliberately: the
// reasoning belongs beside the code. A reference wants the claim without the
// argument, and the argument is one click away in the prose.
func firstSentence(doc string) string {
	doc = strings.TrimSpace(strings.ReplaceAll(doc, "\n", " "))
	for strings.Contains(doc, "  ") {
		doc = strings.ReplaceAll(doc, "  ", " ")
	}
	if doc == "" {
		return ""
	}
	// A period inside a key name, a version or an ellipsis does not end a
	// sentence, so the split looks for one followed by a space and a capital.
	for i := 0; i < len(doc)-2; i++ {
		if doc[i] == '.' && doc[i+1] == ' ' && doc[i+2] >= 'A' && doc[i+2] <= 'Z' {
			return doc[:i+1]
		}
	}
	if !strings.HasSuffix(doc, ".") {
		doc += "."
	}
	return doc
}

// Walk reports every yaml-tagged field reachable from cfg, with the value
// found in it as the default.
//
// cfg is the defaults, not a zero value: a reference whose default column is
// empty everywhere describes a configuration nobody ships.
func Walk(cfg any, docs Docs) []Field {
	var out []Field
	walk(reflect.ValueOf(cfg), "", docs, &out, map[reflect.Type]bool{})
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func walk(v reflect.Value, prefix string, docs Docs, out *[]Field, seen map[reflect.Type]bool) {
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			v = reflect.New(v.Type().Elem())
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return
	}
	t := v.Type()
	// A configuration that refers to its own type (a fallback model naming a
	// model) would otherwise walk forever.
	if seen[t] {
		return
	}
	seen[t] = true
	defer delete(seen, t)

	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("yaml")
		name, _, _ := strings.Cut(tag, ",")
		if name == "" || name == "-" || !f.IsExported() {
			continue
		}
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}
		fv := v.Field(i)

		if inner := deref(fv); inner.Kind() == reflect.Struct && !isLeaf(inner.Type()) {
			walk(inner, path, docs, out, seen)
			continue
		}
		if fv.Kind() == reflect.Slice && fv.Type().Elem().Kind() == reflect.Struct {
			walk(reflect.New(fv.Type().Elem()).Elem(), path+"[]", docs, out, seen)
			continue
		}
		if fv.Kind() == reflect.Map && fv.Type().Elem().Kind() == reflect.Struct {
			walk(reflect.New(fv.Type().Elem()).Elem(), path+".<name>", docs, out, seen)
			continue
		}

		*out = append(*out, Field{
			Path:    path,
			Type:    yamlType(f.Type),
			Default: format(fv),
			Doc:     docs[t.Name()+"."+f.Name],
		})
	}
}

func deref(v reflect.Value) reflect.Value {
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return reflect.New(v.Type().Elem()).Elem()
		}
		v = v.Elem()
	}
	return v
}

// isLeaf reports types that are structs to Go and a single value to yaml.
func isLeaf(t reflect.Type) bool {
	return t.String() == "time.Duration" || t.NumField() == 0
}

// yamlType names a type the way someone writing yaml would, not the way Go
// spells it.
func yamlType(t reflect.Type) string {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Bool:
		return "boolean"
	case reflect.String:
		return "string"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if t.String() == "time.Duration" {
			return "duration"
		}
		return "integer"
	case reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Slice:
		return "list of " + yamlType(t.Elem())
	case reflect.Map:
		return "map of " + yamlType(t.Elem())
	default:
		return t.Kind().String()
	}
}

func format(v reflect.Value) string {
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return ""
		}
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.Slice, reflect.Map:
		if v.Len() == 0 {
			return ""
		}
		return fmt.Sprintf("%v", v.Interface())
	case reflect.String:
		if v.String() == "" {
			return ""
		}
		return v.String()
	default:
		if v.Type().String() == "time.Duration" {
			return fmt.Sprintf("%v", v.Interface())
		}
		return fmt.Sprintf("%v", v.Interface())
	}
}
