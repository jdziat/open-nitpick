package practices

import (
	"go/parser"
	"go/token"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/standards"
	"golang.org/x/mod/modfile"
)

// DesignUnit groups package files and identifies the dependencies an assessment needs.
type DesignUnit struct {
	ID           string   `json:"id"`
	Files        []string `json:"files"`
	Dependencies []string `json:"dependencies"`
	External     []string `json:"external,omitempty"`
	Unresolved   []string `json:"unresolved,omitempty"`
}

// DesignInventory describes the structural evidence available to a model pass.
type DesignInventory struct {
	Units       []DesignUnit `json:"units"`
	Limitations []string     `json:"limitations"`
	Errors      []string     `json:"errors,omitempty"`
}

type importEdge struct {
	from, to, file string
	line           int
}

// InspectDesign inventories Go package edges without loading or executing packages.
func InspectDesign(files []standards.File, boundaries []config.PracticeBoundary) (DesignInventory, Check) {
	inventory := DesignInventory{Limitations: []string{
		"Go import graph only; dynamic calls and external dependency implementations are not resolved",
		"package inventory is not proof of complete architecture review",
	}}
	check := Check{ID: "design-boundaries", Version: "1", Instrument: Deterministic, State: Completed, Tool: "go/parser"}
	for _, boundary := range boundaries {
		for _, pattern := range append(slices.Clone(boundary.Forbid), boundary.From) {
			if _, err := path.Match(pattern, ""); err != nil {
				check.State, check.Reason = Failed, "invalid boundary pattern: "+pattern
				inventory.Errors = append(inventory.Errors, check.Reason)
				return inventory, check
			}
		}
	}
	modules := map[string]string{}
	for _, file := range files {
		if path.Base(file.Path) != "go.mod" {
			continue
		}
		modules[path.Dir(file.Path)] = ""
		module, err := modfile.Parse(file.Path, file.Src, nil)
		if err != nil || module.Module == nil {
			inventory.Errors = append(inventory.Errors, file.Path+": cannot identify Go module")
			continue
		}
		modules[path.Dir(file.Path)] = module.Module.Mod.Path
	}
	units := map[string]*DesignUnit{}
	var edges []importEdge
	for _, file := range files {
		if path.Ext(file.Path) != ".go" {
			continue
		}
		if len(boundaries) > 0 {
			check.Planned = append(check.Planned, Target{Kind: FileTarget, ID: file.Path})
		}
		root, moduleName := "", ""
		for dir, name := range modules {
			if (dir == "." || strings.HasPrefix(file.Path, dir+"/")) && len(dir) > len(root) {
				root, moduleName = dir, name
			}
		}
		if moduleName == "" {
			inventory.Errors = append(inventory.Errors, file.Path+": no module path for import analysis")
			continue
		}
		dir := path.Dir(file.Path)
		rel := dir
		if root != "." {
			rel = strings.TrimPrefix(strings.TrimPrefix(dir, root), "/")
		}
		if rel == "." {
			rel = ""
		}
		id := strings.TrimSuffix(moduleName+"/"+rel, "/")
		fset := token.NewFileSet()
		parsed, err := parser.ParseFile(fset, file.Path, file.Src, parser.ImportsOnly)
		if err != nil {
			inventory.Errors = append(inventory.Errors, file.Path+": imports could not be parsed")
			continue
		}
		if len(boundaries) > 0 {
			check.Examined = append(check.Examined, Target{Kind: FileTarget, ID: file.Path})
		}
		unit := units[id]
		if unit == nil {
			unit = &DesignUnit{ID: id}
			units[id] = unit
		}
		unit.Files = append(unit.Files, file.Path)
		for _, spec := range parsed.Imports {
			dependency, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				inventory.Errors = append(inventory.Errors, file.Path+": invalid import")
				continue
			}
			edges = append(edges, importEdge{from: id, to: dependency, file: file.Path, line: fset.Position(spec.Pos()).Line})
		}
	}
	for _, edge := range edges {
		unit := units[edge.from]
		if units[edge.to] != nil {
			unit.Dependencies = append(unit.Dependencies, edge.to)
		} else {
			local := false
			for _, module := range modules {
				if module != "" && (edge.to == module || strings.HasPrefix(edge.to, module+"/")) {
					local = true
					break
				}
			}
			if local {
				unit.Unresolved = append(unit.Unresolved, edge.to)
			} else {
				unit.External = append(unit.External, edge.to)
			}
		}
		for _, boundary := range boundaries {
			from, _ := path.Match(boundary.From, edge.from)
			if !from {
				continue
			}
			for _, forbidden := range boundary.Forbid {
				if matches, _ := path.Match(forbidden, edge.to); matches {
					check.Findings = append(check.Findings, Finding{Rule: "design.forbidden-import", Target: Target{Kind: FileTarget, ID: edge.file, Line: edge.line},
						Title: edge.from + " imports forbidden dependency " + edge.to, Rationale: boundary.Reason,
						Remedy: "remove the dependency or move the operation behind the accepted package boundary", Severity: "warning", Blocking: true, Sources: []string{"go/parser"}})
				}
			}
		}
	}
	for _, unit := range units {
		sort.Strings(unit.Files)
		sort.Strings(unit.Dependencies)
		unit.Dependencies = slices.Compact(unit.Dependencies)
		sort.Strings(unit.Unresolved)
		unit.Unresolved = slices.Compact(unit.Unresolved)
		sort.Strings(unit.External)
		unit.External = slices.Compact(unit.External)
		inventory.Units = append(inventory.Units, *unit)
	}
	slices.SortFunc(inventory.Units, func(a, b DesignUnit) int { return strings.Compare(a.ID, b.ID) })
	switch {
	case len(boundaries) == 0:
		check.State, check.Reason = NotApplicable, "no explicit dependency boundaries configured"
	case len(inventory.Errors) > 0:
		check.State, check.Reason = Partial, strings.Join(inventory.Errors, "; ")
	case len(check.Planned) == 0:
		check.State, check.Reason = Unavailable, "configured Go boundary rules have no Go source to inspect"
	}
	return inventory, check
}
