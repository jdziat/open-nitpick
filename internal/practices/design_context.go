package practices

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/scanner"
	"go/token"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/jdziat/open-nitpick/internal/bundle"
	"github.com/jdziat/open-nitpick/internal/standards"
)

type designSourceInfo struct {
	unit        string
	file        string
	spans       []bundle.SourceSpan
	imports     map[string]string
	selected    map[string]map[string]bool
	references  map[string]bool
	members     map[string]bool
	effects     bool
	writes      map[string]bool
	arguments   map[string]bool
	initializes bool
	function    bool
	similarity  string
	contract    *designSourceInfo
}

type designContextIndex struct {
	ctx          context.Context
	files        map[string]*designSourceInfo
	nodes        map[string]*designSourceInfo
	declarations map[string]map[string][]string
	methods      map[string]map[string][]string
	methodNames  map[string]map[string][]string
	writers      map[string]map[string][]string
	variables    map[string]map[string][]string
	mutable      map[string]map[string]bool
	unresolved   map[string]map[string]bool
	similar      map[string]map[string][]string
	errors       []string
}

func indexDesignContext(ctx context.Context, inventory DesignInventory, files []standards.File) designContextIndex {
	index := designContextIndex{ctx: ctx, files: map[string]*designSourceInfo{}, nodes: map[string]*designSourceInfo{}, declarations: map[string]map[string][]string{}, methods: map[string]map[string][]string{}, methodNames: map[string]map[string][]string{}, writers: map[string]map[string][]string{}, variables: map[string]map[string][]string{}, mutable: map[string]map[string]bool{}, unresolved: map[string]map[string]bool{}, similar: map[string]map[string][]string{}}
	owners := map[string]string{}
	for _, unit := range inventory.Units {
		index.declarations[unit.ID], index.methods[unit.ID] = map[string][]string{}, map[string][]string{}
		index.methodNames[unit.ID] = map[string][]string{}
		index.mutable[unit.ID] = map[string]bool{}
		index.similar[unit.ID] = map[string][]string{}
		index.unresolved[unit.ID] = map[string]bool{}
		for _, name := range unit.Unresolved {
			index.unresolved[unit.ID][name] = true
		}
		index.writers[unit.ID], index.variables[unit.ID] = map[string][]string{}, map[string][]string{}
		for _, name := range unit.Files {
			owners[name] = unit.ID
			index.files[name] = &designSourceInfo{unit: unit.ID, file: name}
			index.nodes[name] = index.files[name]
		}
	}
	parsed := map[string]*ast.File{}
	sourceBytes := map[string][]byte{}
	positions := token.NewFileSet()
	packageNames := map[string]string{}
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			index.errors = append(index.errors, err.Error())
			break
		}
		unit := owners[file.Path]
		if unit == "" {
			continue
		}
		f, err := parser.ParseFile(positions, file.Path, file.Src, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			index.errors = append(index.errors, file.Path+": declarations unavailable for design context")
			continue
		}
		parsed[file.Path] = f
		sourceBytes[file.Path] = file.Src
		if !strings.HasSuffix(file.Path, "_test.go") {
			packageNames[unit] = f.Name.Name
		}
	}
	for name := range owners {
		if parsed[name] == nil {
			index.errors = append(index.errors, name+": source unavailable for dependency and caller selection")
		}
	}
	for name, f := range parsed {
		if ctx.Err() != nil {
			break
		}
		unit := owners[name]
		bindings := bindDesignNames(f)
		info := &designSourceInfo{unit: unit, file: name, imports: map[string]string{}, selected: map[string]map[string]bool{}, references: map[string]bool{}}
		index.files[name] = info
		for _, spec := range f.Imports {
			imported, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				continue
			}
			alias := packageNames[imported]
			if alias == "" {
				alias = path.Base(imported)
			}
			if spec.Name != nil {
				alias = spec.Name.Name
			}
			info.imports[imported] = alias
		}

		header := &designSourceInfo{unit: unit, file: name, imports: info.imports, spans: []bundle.SourceSpan{{Start: 1, End: positions.PositionFor(f.Name.End(), false).Line}}}
		index.nodes[name] = header
		for number, declaration := range f.Decls {
			if declaration, ok := declaration.(*ast.GenDecl); ok && declaration.Tok == token.IMPORT {
				start := declaration.Pos()
				if declaration.Doc != nil {
					start = declaration.Doc.Pos()
				}
				header.spans = append(header.spans, bundle.SourceSpan{Start: positions.PositionFor(start, false).Line, End: positions.PositionFor(declaration.End(), false).Line})
				continue
			}
			key := name + ":" + strconv.Itoa(number)
			node := &designSourceInfo{unit: unit, file: name, imports: info.imports, selected: map[string]map[string]bool{}, references: map[string]bool{}}
			start := declaration.Pos()
			switch declaration := declaration.(type) {
			case *ast.FuncDecl:
				node.function = true
				if declaration.Body != nil && !strings.HasSuffix(name, "_test.go") {
					file := positions.File(declaration.Body.Pos())
					body := sourceBytes[name][file.Offset(declaration.Body.Pos()):file.Offset(declaration.Body.End())]
					node.similarity = designSimilarity(declaration, body)
					if node.similarity != "" {
						index.similar[unit][node.similarity] = append(index.similar[unit][node.similarity], key)
					}
				}
				if declaration.Doc != nil {
					start = declaration.Doc.Pos()
				}
				if declaration.Recv != nil && len(declaration.Recv.List) > 0 {
					receiver := designReceiverName(declaration.Recv.List[0].Type)
					index.methods[unit][receiver] = append(index.methods[unit][receiver], key)
					index.methodNames[unit][declaration.Name.Name] = append(index.methodNames[unit][declaration.Name.Name], key)
				} else {
					index.declarations[unit][declaration.Name.Name] = append(index.declarations[unit][declaration.Name.Name], key)
					node.initializes = declaration.Name.Name == "init"
					node.effects = node.initializes
				}
			case *ast.GenDecl:
				if declaration.Doc != nil {
					start = declaration.Doc.Pos()
				}
				for _, spec := range declaration.Specs {
					switch spec := spec.(type) {
					case *ast.TypeSpec:
						index.declarations[unit][spec.Name.Name] = append(index.declarations[unit][spec.Name.Name], key)
					case *ast.ValueSpec:
						for _, identifier := range spec.Names {
							if identifier.Name != "_" {
								index.declarations[unit][identifier.Name] = append(index.declarations[unit][identifier.Name], key)
								if declaration.Tok == token.VAR {
									index.variables[unit][identifier.Name] = append(index.variables[unit][identifier.Name], key)
									mutable := designMutableValue(spec.Type)
									for _, value := range spec.Values {
										mutable = mutable || designMutableValue(value)
									}
									index.mutable[unit][identifier.Name] = mutable
								}
							}
						}
						node.initializes = node.initializes || declaration.Tok == token.VAR && len(spec.Values) > 0
						if declaration.Tok == token.VAR {
							for _, value := range spec.Values {
								node.effects = node.effects || designInitializerCalls(value)
							}
						}
					}
				}
			}
			node.spans = []bundle.SourceSpan{{Start: positions.PositionFor(start, false).Line, End: positions.PositionFor(declaration.End(), false).Line}}
			contract := &designSourceInfo{unit: unit, file: name, imports: info.imports, selected: map[string]map[string]bool{}, references: map[string]bool{}}
			switch declaration := declaration.(type) {
			case *ast.FuncDecl:
				end := declaration.End()
				if declaration.Body != nil {
					end = declaration.Body.Lbrace
				}
				contract.spans = []bundle.SourceSpan{{Start: positions.PositionFor(start, false).Line, End: positions.PositionFor(end, false).Line}}
				collectDesignReferences(contract, declaration.Type, bindings.free)
				if declaration.Recv != nil {
					for _, field := range declaration.Recv.List {
						collectDesignReferences(contract, field.Type, bindings.free)
					}
				}
			case *ast.GenDecl:
				if declaration.Tok == token.TYPE {
					contract.spans = slices.Clone(node.spans)
					collectDesignReferences(contract, declaration, bindings.free)
				} else {
					contract.spans = []bundle.SourceSpan{{Start: positions.PositionFor(start, false).Line, End: positions.PositionFor(declaration.Pos(), false).Line}}
					for _, spec := range declaration.Specs {
						if spec, ok := spec.(*ast.ValueSpec); ok && len(spec.Names) > 0 {
							end := spec.Names[len(spec.Names)-1].End()
							if spec.Type != nil {
								end = spec.Type.End()
							}
							contract.spans = append(contract.spans, bundle.SourceSpan{Start: positions.PositionFor(spec.Pos(), false).Line, End: positions.PositionFor(end, false).Line})
							if spec.Type != nil {
								collectDesignReferences(contract, spec.Type, bindings.free)
							}
						}
					}
				}
			}
			node.contract = contract
			collectDesignReferences(node, declaration, bindings.free)
			for name := range node.writes {
				index.writers[unit][name] = append(index.writers[unit][name], key)
			}
			index.nodes[key] = node
		}
		collectDesignReferences(info, f, bindings.free)
	}
	for key, node := range index.nodes {
		for name := range node.arguments {
			if index.mutable[node.unit][name] {
				index.writers[node.unit][name] = append(index.writers[node.unit][name], key)
			}
		}
	}
	slices.Sort(index.errors)
	return index
}

func collectDesignReferences(info *designSourceInfo, root ast.Node, free func(*ast.Ident) bool) {
	info.members = map[string]bool{}
	info.writes = map[string]bool{}
	info.arguments = map[string]bool{}
	fieldQualifiers := map[*ast.SelectorExpr]bool{}

	var visit func(ast.Node) bool
	visit = func(node ast.Node) bool {
		switch node := node.(type) {
		case *ast.AssignStmt:
			for _, left := range node.Lhs {
				if name := designMutationRoot(left, free); name != "" {
					info.writes[name] = true
				}
			}
		case *ast.RangeStmt:
			if node.Tok == token.ASSIGN {
				for _, left := range []ast.Expr{node.Key, node.Value} {
					if name := designMutationRoot(left, free); name != "" {
						info.writes[name] = true
					}
				}
			}
		case *ast.IncDecStmt:
			if name := designMutationRoot(node.X, free); name != "" {
				info.writes[name] = true
			}
		case *ast.CallExpr:
			for _, arg := range node.Args {
				if name := designMutationRoot(arg, free); name != "" {
					if address, ok := arg.(*ast.UnaryExpr); ok && address.Op == token.AND {
						info.writes[name] = true
					} else {
						info.arguments[name] = true
					}
				}
			}
			if selector, ok := node.Fun.(*ast.SelectorExpr); ok {
				if name := designMutationRoot(selector.X, free); name != "" {
					info.writes[name] = true
				}
			}
		case *ast.SelectorExpr:
			packageSelector := false
			if qualifier, ok := node.X.(*ast.Ident); ok && free(qualifier) {
				for imported, alias := range info.imports {
					if qualifier.Name == alias {
						packageSelector = true
						if info.selected[imported] == nil {
							info.selected[imported] = map[string]bool{}
						}
						info.selected[imported][node.Sel.Name] = true
					}
				}
			}
			if !packageSelector && !fieldQualifiers[node] {
				info.members[node.Sel.Name] = true
			}
			qualifier := node.X
			for {
				paren, ok := qualifier.(*ast.ParenExpr)
				if !ok {
					break
				}
				qualifier = paren.X
			}
			// A function value has no selectable fields, so this is not a method value.
			if field, ok := qualifier.(*ast.SelectorExpr); ok {
				fieldQualifiers[field] = true
			}
			ast.Inspect(node.X, visit)
			return false
		case *ast.Ident:
			if free(node) {
				info.references[node.Name] = true
			}
		}
		return true
	}
	ast.Inspect(root, visit)
}

func designReceiverName(expression ast.Expr) string {
	switch expression := expression.(type) {
	case *ast.Ident:
		return expression.Name
	case *ast.StarExpr:
		return designReceiverName(expression.X)
	case *ast.IndexExpr:
		return designReceiverName(expression.X)
	case *ast.IndexListExpr:
		return designReceiverName(expression.X)
	default:
		return ""
	}
}

func (index designContextIndex) contextFor(unit DesignUnit) ([]string, []ContextSpan, []Omission) {
	selected := map[string]bool{}
	var missing []Omission
	for _, dependency := range unit.Dependencies {
		if index.declarations[dependency] == nil {
			missing = append(missing, Omission{Target: Target{Kind: UnitTarget, ID: "package:" + dependency}, Reason: "dependency package is absent from the permitted inventory"})
			continue
		}
		var seeds []string
		all := false
		for _, name := range unit.Files {
			info := index.files[name]
			if info == nil {
				continue
			}
			all = all || info.imports[dependency] == "_" || info.imports[dependency] == "."
			for symbol := range info.selected[dependency] {
				definitions := index.declarations[dependency][symbol]
				if len(definitions) == 0 {
					omission := Omission{Target: Target{Kind: UnitTarget, ID: "symbol:" + dependency + "." + symbol}, Reason: "referenced dependency declaration is unavailable"}
					if !slices.Contains(missing, omission) {
						missing = append(missing, omission)
					}
				}
				seeds = append(seeds, definitions...)
				seeds = append(seeds, index.methods[dependency][symbol]...)
			}
		}
		for name, info := range index.nodes {
			if info.unit == dependency && (all || info.initializes) {
				seeds = append(seeds, name)
			}
		}
		index.includeContext(selected, dependency, seeds, false)
	}
	callerSeeds := map[string][]string{}
	for name, info := range index.nodes {
		if info.unit == unit.ID {
			continue
		}
		alias, imports := info.imports[unit.ID]
		if imports && (alias == "_" || alias == "." || len(info.selected[unit.ID]) > 0) {
			callerSeeds[info.unit] = append(callerSeeds[info.unit], name)
		}
	}
	for caller, seeds := range callerSeeds {
		index.includeContext(selected, caller, seeds, true)
	}
	// Blank imports may have no declarations, but still establish a lifecycle edge.
	for name, info := range index.files {
		if info.unit != unit.ID && (info.imports[unit.ID] == "_" || info.imports[unit.ID] == ".") {
			selected[name] = true
		}
	}
	if err := index.ctx.Err(); err != nil {
		missing = append(missing, Omission{Target: Target{Kind: UnitTarget, ID: "package:" + unit.ID}, Reason: err.Error()})
	}
	byFile := map[string][]bundle.SourceSpan{}
	for key := range selected {
		info := index.nodes[key]
		byFile[info.file] = append(byFile[info.file], info.spans...)
	}
	var names []string
	for name := range byFile {
		names = append(names, name)
	}
	slices.Sort(names)
	var spans []ContextSpan
	for _, name := range names {
		ranges := append(slices.Clone(byFile[name]), index.nodes[name].spans...)
		slices.SortFunc(ranges, func(a, b bundle.SourceSpan) int { return a.Start - b.Start })
		var merged []bundle.SourceSpan
		for _, span := range ranges {
			if len(merged) > 0 && span.Start <= merged[len(merged)-1].End+1 {
				merged[len(merged)-1].End = max(merged[len(merged)-1].End, span.End)
			} else {
				merged = append(merged, span)
			}
		}
		for _, span := range merged {
			spans = append(spans, ContextSpan{Path: name, SourceSpan: span})
		}
	}
	slices.SortFunc(missing, func(a, b Omission) int { return strings.Compare(a.Target.ID, b.Target.ID) })
	return names, spans, missing
}

func (index designContextIndex) includeContext(selected map[string]bool, unit string, seeds []string, tests bool) {
	for key, info := range index.nodes {
		if info.unit == unit && info.initializes {
			seeds = append(seeds, key)
		}
	}
	for len(seeds) > 0 {
		if index.ctx.Err() != nil {
			return
		}
		name := seeds[0]
		seeds = seeds[1:]
		info := index.nodes[name]
		if info == nil || info.unit != unit {
			continue
		}
		if selected[name] || (!tests && strings.HasSuffix(info.file, "_test.go")) {
			continue
		}
		selected[name] = true
		for reference := range info.references {
			seeds = append(seeds, index.declarations[unit][reference]...)
			seeds = append(seeds, index.methods[unit][reference]...)
		}
	}
}

func designInitializerCalls(root ast.Node) bool {
	calls := false
	ast.Inspect(root, func(node ast.Node) bool {
		switch node.(type) {
		case *ast.FuncLit:
			return false
		case *ast.CallExpr:
			calls = true
			return false
		}
		return !calls
	})
	return calls
}

func designMutationRoot(expression ast.Expr, free func(*ast.Ident) bool) string {
	switch expression := expression.(type) {
	case *ast.Ident:
		if !free(expression) {
			return ""
		}
		return expression.Name
	case *ast.SelectorExpr:
		return designMutationRoot(expression.X, free)
	case *ast.IndexExpr:
		return designMutationRoot(expression.X, free)
	case *ast.StarExpr:
		return designMutationRoot(expression.X, free)
	case *ast.UnaryExpr:
		return designMutationRoot(expression.X, free)
	case *ast.ParenExpr:
		return designMutationRoot(expression.X, free)
	default:
		return ""
	}
}

func designMutableValue(expression ast.Expr) bool {
	switch expression := expression.(type) {
	case *ast.MapType, *ast.ArrayType, *ast.StarExpr, *ast.ChanType:
		return true
	case *ast.CompositeLit:
		return designMutableValue(expression.Type)
	case *ast.UnaryExpr:
		return expression.Op == token.AND
	case *ast.CallExpr:
		if fn, ok := expression.Fun.(*ast.Ident); ok {
			if fn.Name == "new" {
				return true
			}
			if fn.Name == "make" && len(expression.Args) > 0 {
				return designMutableValue(expression.Args[0])
			}
		}
	}
	return false
}

func designSimilarity(declaration *ast.FuncDecl, body []byte) string {
	var signature strings.Builder
	for _, fields := range []*ast.FieldList{declaration.Type.Params, declaration.Type.Results} {
		signature.WriteByte('|')
		if fields == nil {
			continue
		}
		for _, field := range fields.List {
			for i := 0; i < max(1, len(field.Names)); i++ {
				if err := printer.Fprint(&signature, token.NewFileSet(), field.Type); err != nil {
					return ""
				}
				signature.WriteByte(';')
			}
		}
	}
	var lexer scanner.Scanner
	lexer.Init(token.NewFileSet().AddFile("", -1, len(body)), body, nil, 0)
	count := 0
	for {
		_, kind, _ := lexer.Scan()
		if kind == token.EOF {
			break
		}
		count++
		signature.WriteString(kind.String())
		signature.WriteByte(' ')
	}
	if count < 12 {
		return ""
	}
	digest := sha256.Sum256([]byte(signature.String()))
	return hex.EncodeToString(digest[:])
}
