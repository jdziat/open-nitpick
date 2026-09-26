package practices

import (
	"go/ast"
	"go/token"
)

type designBindingRange struct{ start, end token.Pos }

type designBindings struct {
	locals      map[string][]designBindingRange
	definitions map[*ast.Ident]bool
}

// bindDesignNames tracks lexical shadows without treating field keys as resolved types.
func bindDesignNames(file *ast.File) designBindings {
	bound := designBindings{locals: map[string][]designBindingRange{}, definitions: map[*ast.Ident]bool{file.Name: true}}
	type frame struct {
		node     ast.Node
		end      token.Pos
		function bool
	}
	var stack []frame
	functions := 0
	define := func(name *ast.Ident, start, end token.Pos) {
		if name == nil {
			return
		}
		bound.definitions[name] = true
		if start < end {
			bound.locals[name.Name] = append(bound.locals[name.Name], designBindingRange{start, end})
		}
	}
	fields := func(list *ast.FieldList, start, end token.Pos) {
		if list == nil {
			return
		}
		for _, field := range list.List {
			for _, name := range field.Names {
				define(name, start, end)
			}
		}
	}
	ast.Inspect(file, func(node ast.Node) bool {
		if node == nil {
			if stack[len(stack)-1].function {
				functions--
			}
			stack = stack[:len(stack)-1]
			return true
		}
		end := file.End()
		if len(stack) > 0 {
			end = stack[len(stack)-1].end
		}
		switch node.(type) {
		case *ast.BlockStmt, *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt, *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.CaseClause, *ast.CommClause, *ast.FuncDecl, *ast.FuncLit:
			end = node.End()
		}
		function := false
		switch node := node.(type) {
		case *ast.ImportSpec:
			define(node.Name, 0, 0)
		case *ast.FuncDecl:
			define(node.Name, 0, 0)
			function = true
			start := node.End()
			if node.Body != nil {
				start = node.Body.Pos()
			}
			fields(node.Recv, start, node.End())
			fields(node.Type.Params, start, node.End())
			fields(node.Type.Results, start, node.End())
			fields(node.Type.TypeParams, node.Type.Pos(), node.End())
			if node.Recv != nil {
				for _, receiver := range node.Recv.List {
					typ := receiver.Type
					if pointer, ok := typ.(*ast.StarExpr); ok {
						typ = pointer.X
					}
					var args []ast.Expr
					switch typ := typ.(type) {
					case *ast.IndexExpr:
						args = []ast.Expr{typ.Index}
					case *ast.IndexListExpr:
						args = typ.Indices
					}
					for _, arg := range args {
						if name, ok := arg.(*ast.Ident); ok {
							define(name, receiver.Type.Pos(), node.End())
						}
					}
				}
			}
		case *ast.FuncLit:
			function = true
			fields(node.Type.Params, node.Body.Pos(), node.End())
			fields(node.Type.Results, node.Body.Pos(), node.End())
		case *ast.Field:
			for _, name := range node.Names {
				define(name, 0, 0)
			}
		case *ast.FuncType:
			fields(node.Params, 0, 0)
			fields(node.Results, 0, 0)
		case *ast.TypeSpec:
			start := end
			if functions > 0 {
				start = node.Name.Pos()
			}
			define(node.Name, start, end)
			fields(node.TypeParams, node.Pos(), node.End())
		case *ast.ValueSpec:
			for _, name := range node.Names {
				start := end
				if functions > 0 {
					start = node.End()
				}
				define(name, start, end)
			}
		case *ast.AssignStmt:
			if node.Tok == token.DEFINE {
				for _, left := range node.Lhs {
					name, ok := left.(*ast.Ident)
					if !ok {
						continue
					}
					special := false
					for _, ancestor := range stack {
						if guard, ok := ancestor.node.(*ast.TypeSwitchStmt); ok && guard.Assign == node {
							special = true
							define(name, 0, 0)
							for _, statement := range guard.Body.List {
								clause := statement.(*ast.CaseClause)
								define(name, clause.Colon+1, clause.End())
							}
						}
					}
					if !special {
						define(name, node.End(), end)
					}
				}
			}
		case *ast.RangeStmt:
			if node.Tok == token.DEFINE {
				for _, value := range []ast.Expr{node.Key, node.Value} {
					if name, ok := value.(*ast.Ident); ok {
						define(name, node.Body.Pos(), node.Body.End())
					}
				}
			}
		case *ast.LabeledStmt:
			define(node.Label, 0, 0)
		case *ast.BranchStmt:
			define(node.Label, 0, 0)
		}
		if function {
			functions++
		}
		stack = append(stack, frame{node, end, function})
		return true
	})
	return bound
}

func (bound designBindings) free(name *ast.Ident) bool {
	if name == nil || name.Name == "_" || bound.definitions[name] {
		return false
	}
	for _, scope := range bound.locals[name.Name] {
		if name.Pos() >= scope.start && name.Pos() < scope.end {
			return false
		}
	}
	return true
}
