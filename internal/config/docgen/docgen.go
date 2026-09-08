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
	"regexp"
	"sort"
	"strconv"
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

// Enums maps a named string type to the values its constants declare, in the
// order the source declares them.
//
// A key whose type is one of these accepts those values and nothing else, and
// the reference used to say so for two of the twenty-one: `linters.mode` and
// the nine `structured_output` rows, whose field comments happen to spell the
// values out, against ten keys that named none of them. `persona.confidence`
// printed "Confidence controls hedging." beside a default of `direct` and left
// `hedged` findable only by setting a bad value and reading the rejection. The
// values are on the type, never on the field, so the field comment was the
// wrong place to look for them.
type Enums map[string][]string

// ReadDocs parses every non-test file in dir and collects struct field
// comments and the constants of every named string type.
//
// A field with a comment above it and one beside it has both, in that order,
// which is how the configuration is written today.
func ReadDocs(dir string) (Docs, Enums, error) {
	set := token.NewFileSet()
	pkgs, err := parser.ParseDir(set, dir, func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.ParseComments)
	if err != nil {
		return nil, nil, fmt.Errorf("docgen: parse %s: %w", dir, err)
	}

	out := Docs{}
	// Constants are collected for every type and filtered afterwards, because a
	// const block and the `type X string` it belongs to need not be in the same
	// file and are not, for Severity.
	named := map[string]bool{}
	values := Enums{}
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			ast.Inspect(file, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.TypeSpec:
					if id, ok := n.Type.(*ast.Ident); ok && id.Name == "string" {
						named[n.Name.Name] = true
						return true
					}
					st, ok := n.Type.(*ast.StructType)
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
								out[n.Name.Name+"."+name.Name] = s
							}
						}
					}
				case *ast.ValueSpec:
					id, ok := n.Type.(*ast.Ident)
					if !ok || len(n.Values) != 1 {
						return true
					}
					lit, ok := n.Values[0].(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						return true
					}
					v, err := strconv.Unquote(lit.Value)
					if err != nil || v == "" {
						return true
					}
					values[id.Name] = appendOnce(values[id.Name], v)
				}
				return true
			})
		}
	}
	for name := range values {
		if !named[name] {
			delete(values, name)
		}
	}
	return out, values, nil
}

// appendOnce keeps the declaration order and drops a repeat.
func appendOnce(list []string, v string) []string {
	for _, have := range list {
		if have == v {
			return list
		}
	}
	return append(list, v)
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
	end := sentenceEnd(doc, 0)
	// The Default column carries a value and the description has to say what
	// that value does, which for a handful of keys is the SECOND sentence. Nine
	// keys lost it: every models.*.max_tokens row printed "MaxTokens caps the
	// response." beside a default of 0, which reads as a cap of zero tokens,
	// where the source's next sentence is "Zero lets the provider decide, which
	// is the shipped behaviour"; the route bounds printed "bounds how many files
	// the batch may hold" beside 0, where the source says "Zero is unset." A
	// sentence opening on Zero, Empty or Unset is qualifying the default rather
	// than continuing the argument, so it is kept with the first. The intro on
	// the generated page covers `none` in prose and had nothing to say about 0.
	if rest := strings.TrimLeft(doc[end:], " "); rest != "" {
		for _, qualifier := range []string{"Zero ", "Empty ", "Unset "} {
			if strings.HasPrefix(rest, qualifier) {
				end = sentenceEnd(doc, end)
				break
			}
		}
	}
	doc = strings.TrimSpace(doc[:end])
	if !strings.HasSuffix(doc, ".") {
		doc += "."
	}
	return doc
}

// sentenceEnd reports the index just past the sentence that starts at from, or
// the end of doc when it does not end before that.
//
// A period inside a key name, a version or an ellipsis does not end a sentence,
// so the split looks for one followed by a space and a capital.
func sentenceEnd(doc string, from int) int {
	for i := from; i < len(doc)-2; i++ {
		if doc[i] == '.' && doc[i+1] == ' ' && doc[i+2] >= 'A' && doc[i+2] <= 'Z' {
			return i + 1
		}
	}
	return len(doc)
}

// Walk reports every yaml-tagged field reachable from cfg, with the value
// found in it as the default.
//
// cfg is the defaults, not a zero value: a reference whose default column is
// empty everywhere describes a configuration nobody ships.
func Walk(cfg any, docs Docs, values Enums) []Field {
	var out []Field
	walk(reflect.ValueOf(cfg), "", docs, values, &out, map[reflect.Type]bool{}, map[reflect.Type]reflect.Value{})
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// walk emits one Field per yaml key under v.
//
// inherit carries the block a sibling named `default` holds, keyed by its
// type. A block of that type which sets nothing is not off: ModelSpec.overlay
// fills each unset field from it, and every resolver in internal/config
// (ResolveModel, ResolveRoute, ResolveEnsemble, ResolveFix, ResolveRouter)
// goes through it. Printing the struct zero instead gave models.review a
// timeout of 0s where the run uses 10m and a structured_output of none where
// it uses auto, across eight roles.
func walk(v reflect.Value, prefix string, docs Docs, values Enums, out *[]Field, seen map[reflect.Type]bool, inherit map[reflect.Type]reflect.Value) {
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
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("yaml"), ",")
		if name != "default" {
			continue
		}
		d := deref(v.Field(i))
		if d.Kind() != reflect.Struct || isLeaf(d.Type()) {
			continue
		}
		if _, ok := inherit[d.Type()]; !ok {
			inherit[d.Type()] = d
			defer delete(inherit, d.Type())
		}
	}

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
			// A field whose type is one of its own ancestors: models.<role>.fallback
			// is a ModelSpec inside a ModelSpec. Walking it does not terminate, and
			// the guard above used to return here having emitted nothing, so the key
			// and the eighteen under it were absent from a page whose footer says
			// anything not listed is not a key. One row, pointing at the block whose
			// keys it repeats.
			if seen[inner.Type()] {
				*out = append(*out, Field{
					Path: path,
					Type: "same keys as " + prefix,
					Doc:  docs[t.Name()+"."+f.Name],
				})
				continue
			}
			walk(inner, path, docs, values, out, seen, inherit)
			continue
		}
		if fv.Kind() == reflect.Slice && fv.Type().Elem().Kind() == reflect.Struct {
			walk(reflect.New(fv.Type().Elem()).Elem(), path+"[]", docs, values, out, seen, inherit)
			continue
		}
		if fv.Kind() == reflect.Map && fv.Type().Elem().Kind() == reflect.Struct {
			walk(reflect.New(fv.Type().Elem()).Elem(), path+".<name>", docs, values, out, seen, inherit)
			continue
		}

		def := format(fv)
		// An unset field takes the sibling `default` block's value, so that is
		// the default this key has. Inside the default block itself the two
		// values are the same one and this changes nothing.
		if base, ok := inherit[t]; ok && fv.IsZero() {
			def = format(base.Field(i))
		}
		if resolved, ok := resolvedDefault(v, f.Name); ok {
			def = resolved
		}
		*out = append(*out, Field{
			Path:    path,
			Type:    yamlType(f.Type),
			Default: def,
			Doc:     withValues(docs[t.Name()+"."+f.Name], accepted(v, f, values)),
		})
	}
}

// resolvedDefault returns what a resolver method on the parent type gives for
// this field, and false when the type has none.
//
// Several defaults are not in the struct. A *bool that means on when unset, a
// list whose empty value stands for a package-level default, a ratio the
// budget substitutes where it is read: reflection over the field alone finds
// the zero value and the reference printed `none` for two settings that ship
// on and `0` for two that ship at 0.25 and 1. A reference that says a switch
// is off when it is on is worse than no reference. The resolver is where the
// shipped value lives, so the reference asks it, by the three shapes this
// package spells one in.
func resolvedDefault(parent reflect.Value, field string) (string, bool) {
	for _, name := range []string{"Effective" + field, field + "On", field + "s"} {
		m := parent.MethodByName(name)
		if !m.IsValid() {
			continue
		}
		// A resolver takes nothing and answers with one value. Allows(assoc)
		// and Matches(languages, kinds, files) are the same shape of name and
		// are not resolvers.
		if mt := m.Type(); mt.NumIn() != 0 || mt.NumOut() != 1 {
			continue
		}
		return format(m.Call(nil)[0]), true
	}
	return "", false
}

// accepted reports the values this key takes, and nil where it is not an
// enumeration.
//
// The type's constants are the answer for every key but two. Severity declares
// six and `review.min_severity` and `linters.max_severity` take five: validate
// rejects `none` on both, since none is a gate threshold rather than a level a
// finding carries, and a reference offering it would be documenting an error.
// A method on the PARENT named <Field>Values is asked first so the key that
// takes fewer than its type declares answers for itself, in the source, beside
// the check that enforces it. That is the same shape resolvedDefault uses for
// a default that is not in the struct.
func accepted(parent reflect.Value, f reflect.StructField, values Enums) []string {
	if m := parent.MethodByName(f.Name + "Values"); m.IsValid() {
		mt := m.Type()
		if mt.NumIn() == 0 && mt.NumOut() == 1 && mt.Out(0) == reflect.TypeOf([]string(nil)) {
			return m.Call(nil)[0].Interface().([]string)
		}
	}
	t := f.Type
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.String {
		return nil
	}
	return values[t.Name()]
}

// withValues appends the accepted values to a description that does not
// already carry them, in the words the loader rejects a bad one with.
//
// Eleven field comments spell their own values out, `linters.mode` and the
// nine `structured_output` rows among them, and a row that named "auto",
// "strict" and "off" in a sentence and then again in a list would be saying it
// twice. The test is the sentence the row prints, not the whole comment: the
// Confidence comment names both values in a second sentence the table cell
// never shows.
func withValues(doc string, values []string) string {
	if len(values) == 0 || doc == "" {
		return doc
	}
	for _, v := range values {
		if !wordIn(doc, v) {
			return strings.TrimSuffix(doc, " ") + " One of: " + strings.Join(values, ", ") + "."
		}
	}
	return doc
}

// wordIn reports whether doc uses v as a word rather than inside a longer one,
// so "off" in "offset" is not a mention of the value off.
func wordIn(doc, v string) bool {
	re, err := regexp.Compile(`(^|[^\w-])` + regexp.QuoteMeta(v) + `($|[^\w-])`)
	if err != nil {
		return false
	}
	return re.MatchString(doc)
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
