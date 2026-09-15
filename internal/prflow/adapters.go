package prflow

import (
	"go/ast"
	"go/token"
	"go/types"
	"strconv"
	"strings"
)

func boundaryKind(fn *types.Func) string {
	if fn.Pkg() == nil {
		return ""
	}
	switch fn.Pkg().Path() {
	case "os":
		switch fn.Name() {
		case "WriteFile", "Remove", "RemoveAll", "Rename", "Create", "OpenFile":
			return "filesystem"
		}
	case "os/exec":
		return "subprocess"
	case "net/http":
		return "network"
	case "database/sql":
		return "persistence"
	}
	return ""
}

func dispatchCases(d *decl) map[token.Pos]string {
	labels := map[token.Pos]string{}
	ast.Inspect(d.fn.Body, func(node ast.Node) bool {
		switchNode, ok := node.(*ast.SwitchStmt)
		if !ok {
			return true
		}
		cli := false
		if switchNode.Tag != nil {
			ast.Inspect(switchNode.Tag, func(n ast.Node) bool {
				cli = cli || isCLIArgument(d.file.info, n)
				return true
			})
		}
		if switchNode.Init != nil {
			ast.Inspect(switchNode.Init, func(n ast.Node) bool {
				cli = cli || isCLIArgument(d.file.info, n)
				return true
			})
		}
		if !cli {
			return true
		}
		for _, statement := range switchNode.Body.List {
			clause, ok := statement.(*ast.CaseClause)
			if !ok {
				continue
			}
			var commands []string
			for _, expression := range clause.List {
				if value, ok := expression.(*ast.BasicLit); ok && value.Kind == token.STRING {
					if command, err := strconv.Unquote(value.Value); err == nil {
						commands = append(commands, command)
					}
				}
			}
			if len(commands) == 0 {
				continue
			}
			for _, statement := range clause.Body {
				ast.Inspect(statement, func(n ast.Node) bool {
					if _, ok := n.(*ast.FuncLit); ok {
						return false
					}
					if call, ok := n.(*ast.CallExpr); ok {
						labels[call.Pos()] = "command: " + strings.Join(commands, ", ")
					}
					return true
				})
			}
		}
		return true
	})
	return labels
}

func isCLIArgument(info *types.Info, node ast.Node) bool {
	selector, ok := node.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	object := info.Uses[selector.Sel]
	if object == nil || object.Pkg() == nil {
		return false
	}
	return object.Pkg().Path() == "os" && object.Name() == "Args" || object.Pkg().Path() == "flag" && object.Name() == "Arg"
}
