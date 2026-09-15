package prflow

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"

	"github.com/jdziat/open-nitpick/internal/diff"
)

type functionRange struct {
	start int
	end   int
}

func sourceFunctions(file SourceFile) map[string]functionRange {
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, file.Path, file.Content, 0)
	if err != nil {
		return nil
	}
	functions := map[string]functionRange{}
	for _, declaration := range parsed.Decls {
		fn, ok := declaration.(*ast.FuncDecl)
		if !ok {
			continue
		}
		name := fn.Name.Name
		if fn.Recv != nil && len(fn.Recv.List) > 0 {
			name = recvName(fn.Recv.List[0].Type) + "." + name
		}
		functions[name] = functionRange{start: fset.Position(fn.Pos()).Line, end: fset.Position(fn.End()).Line}
	}
	return functions
}

func (m *materializer) removed(req Request, changes diff.Files) {
	for _, change := range changes {
		if change == nil || change.Kind == diff.ChangeAdded || change.Kind == diff.ChangeDeleted || !containsRemoval(change) {
			continue
		}
		currentIndex := -1
		for i := range m.files {
			if m.files[i].Path == change.Path && !m.files[i].Removed {
				currentIndex = i
				break
			}
		}
		if currentIndex < 0 {
			continue
		}
		if req.Base.SHA == "" {
			addOmissionSlice(&m.omissions, "base_source_unavailable", 1)
			continue
		}
		if m.stopped || m.ctx.Err() != nil || len(m.files) >= m.options.MaxFiles || m.bytes >= m.options.MaxBytes {
			addOmissionSlice(&m.omissions, "base_source_limit", 1)
			return
		}
		name := change.OldPath
		if name == "" {
			name = change.Path
		}
		if !validPath(name) || excludedPath(name, m.options.Exclude) {
			continue
		}
		before := len(m.files)
		m.read(req.Source, req.Base, name, true)
		if len(m.files) == before {
			continue
		}
		m.files[before].comparePath = change.Path
	}
}

func mapRemovedChanges(files []SourceFile, changes diff.Files) []Omission {
	var omissions []Omission
	for i := range files {
		old := &files[i]
		if old.comparePath == "" {
			continue
		}
		var current *SourceFile
		for j := range files {
			if !files[j].Removed && files[j].Path == old.comparePath {
				current = &files[j]
				break
			}
		}
		if current == nil {
			continue
		}
		oldFunctions, currentFunctions := sourceFunctions(*old), sourceFunctions(*current)
		old.removedSymbols = map[string]bool{}
		if oldFunctions == nil || currentFunctions == nil {
			addOmissionSlice(&omissions, "base_parse_error", 1)
			continue
		}
		for _, change := range changes {
			if change == nil || change.Path != current.Path {
				continue
			}
			for symbol, span := range oldFunctions {
				if !removesRange(change, span) {
					continue
				}
				if next, exists := currentFunctions[symbol]; exists {
					current.ChangedLines = append(current.ChangedLines, next.start)
				} else {
					old.removedSymbols[symbol] = true
				}
			}
		}
	}
	for i := range files {
		sort.Ints(files[i].ChangedLines)
	}
	return omissions
}

func containsRemoval(change *diff.File) bool {
	for _, hunk := range change.Hunks {
		for _, line := range hunk.Lines {
			if line.Kind == diff.LineRemoved {
				return true
			}
		}
	}
	return false
}

func removesRange(change *diff.File, span functionRange) bool {
	for _, hunk := range change.Hunks {
		for _, line := range hunk.Lines {
			if line.Kind == diff.LineRemoved && line.OldLine >= span.start && line.OldLine <= span.end {
				return true
			}
		}
	}
	return false
}
