package practices

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/jdziat/open-nitpick/internal/standards"
)

type designSourceInfo struct {
	unit        string
	imports     map[string]string
	selected    map[string]map[string]bool
	references  map[string]bool
	initializes bool
}

type designContextIndex struct {
	ctx          context.Context
	files        map[string]*designSourceInfo
	declarations map[string]map[string][]string
	methods      map[string]map[string][]string
	errors       []string
}

func indexDesignContext(ctx context.Context, inventory DesignInventory, files []standards.File) designContextIndex {
	index := designContextIndex{ctx: ctx, files: map[string]*designSourceInfo{}, declarations: map[string]map[string][]string{}, methods: map[string]map[string][]string{}}
	owners := map[string]string{}
	for _, unit := range inventory.Units {
		index.declarations[unit.ID], index.methods[unit.ID] = map[string][]string{}, map[string][]string{}
		for _, name := range unit.Files {
			owners[name] = unit.ID
			index.files[name] = &designSourceInfo{unit: unit.ID}
		}
	}
	parsed := map[string]*ast.File{}
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
		f, err := parser.ParseFile(token.NewFileSet(), file.Path, file.Src, 0)
		if err != nil {
			index.errors = append(index.errors, file.Path+": declarations unavailable for design context")
			continue
		}
		parsed[file.Path] = f
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
		info := &designSourceInfo{unit: unit, imports: map[string]string{}, selected: map[string]map[string]bool{}, references: map[string]bool{}}
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
		for _, declaration := range f.Decls {
			switch declaration := declaration.(type) {
			case *ast.FuncDecl:
				if declaration.Recv != nil && len(declaration.Recv.List) > 0 {
					receiver := designReceiverName(declaration.Recv.List[0].Type)
					index.methods[unit][receiver] = append(index.methods[unit][receiver], name)
				} else {
					index.declarations[unit][declaration.Name.Name] = append(index.declarations[unit][declaration.Name.Name], name)
					info.initializes = info.initializes || declaration.Name.Name == "init"
				}
			case *ast.GenDecl:
				for _, spec := range declaration.Specs {
					switch spec := spec.(type) {
					case *ast.TypeSpec:
						index.declarations[unit][spec.Name.Name] = append(index.declarations[unit][spec.Name.Name], name)
					case *ast.ValueSpec:
						for _, identifier := range spec.Names {
							if identifier.Name != "_" {
								index.declarations[unit][identifier.Name] = append(index.declarations[unit][identifier.Name], name)
							}
						}
						info.initializes = info.initializes || declaration.Tok == token.VAR && len(spec.Values) > 0
					}
				}
			}
		}
		var visit func(ast.Node) bool
		visit = func(node ast.Node) bool {
			switch node := node.(type) {
			case *ast.SelectorExpr:
				if qualifier, ok := node.X.(*ast.Ident); ok && (qualifier.Obj == nil || qualifier.Obj.Kind == ast.Pkg) {
					for imported, alias := range info.imports {
						if qualifier.Name == alias {
							if info.selected[imported] == nil {
								info.selected[imported] = map[string]bool{}
							}
							info.selected[imported][node.Sel.Name] = true
						}
					}
				}
				ast.Inspect(node.X, visit)
				return false
			case *ast.Ident:
				info.references[node.Name] = true
			}
			return true
		}
		ast.Inspect(f, visit)
	}
	slices.Sort(index.errors)
	return index
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

func (index designContextIndex) contextFor(unit DesignUnit) ([]string, []Omission) {
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
		for name, info := range index.files {
			if info.unit == dependency && (all || info.initializes) {
				seeds = append(seeds, name)
			}
		}
		index.includeContext(selected, dependency, seeds, false)
	}
	for name, info := range index.files {
		if info.unit != unit.ID {
			if _, imports := info.imports[unit.ID]; imports {
				index.includeContext(selected, info.unit, []string{name}, true)
			}
		}
	}
	if err := index.ctx.Err(); err != nil {
		missing = append(missing, Omission{Target: Target{Kind: UnitTarget, ID: "package:" + unit.ID}, Reason: err.Error()})
	}
	var names []string
	for name := range selected {
		names = append(names, name)
	}
	slices.Sort(names)
	slices.SortFunc(missing, func(a, b Omission) int { return strings.Compare(a.Target.ID, b.Target.ID) })
	return names, missing
}

func (index designContextIndex) includeContext(selected map[string]bool, unit string, seeds []string, tests bool) {
	for len(seeds) > 0 {
		if index.ctx.Err() != nil {
			return
		}
		name := seeds[0]
		seeds = seeds[1:]
		if selected[name] || (!tests && strings.HasSuffix(name, "_test.go")) {
			continue
		}
		info := index.files[name]
		if info == nil || info.unit != unit {
			continue
		}
		selected[name] = true
		for reference := range info.references {
			seeds = append(seeds, index.declarations[unit][reference]...)
			seeds = append(seeds, index.methods[unit][reference]...)
		}
	}
}
