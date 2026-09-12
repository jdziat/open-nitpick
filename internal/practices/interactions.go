package practices

import (
	"context"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/jdziat/open-nitpick/internal/bundle"
	"github.com/jdziat/open-nitpick/internal/standards"
)

// PlanDesignInteractions declares focused assessments with named caller obligations.
// Separate whole-source tasks retain slop coverage when design tasks use excerpts.
// Budgets are applied only after this complete intended scope is recorded.
func PlanDesignInteractions(ctx context.Context, inventory DesignInventory, files []standards.File, changed []string) DesignPlan {
	packages := PlanDesign(ctx, inventory, files, changed)
	index := indexDesignContext(ctx, inventory, files)
	plan := DesignPlan{Version: "4", Errors: slices.Clone(packages.Errors), Limitations: slices.Clone(inventory.Limitations)}
	plan.Limitations = append(plan.Limitations,
		"tasks assess named declaration and caller interactions; completion is not proof of every package-level design property",
		"change scope follows changed source declarations and two caller hops; manifest changes expand package scope; deeper callers remain outside the declared assessment",
		"coupling candidates match signature and body-token shape within a Go package; a shape match does not establish a shared policy",
		"context includes two implementation-call hops within the root package and its direct imports, state writers and package contracts; deeper bodies, transitive imports, dynamic dispatch and reflection are outside the task scope; method selection does not type-check receivers; aliases and unknown returned reference types are not tracked as state writes")
	sources := map[string][]byte{}
	for _, file := range files {
		sources[file.Path] = file.Src
	}
	changedNames := map[string]bool{}
	missingDirs := map[string]bool{}
	whole := changed == nil
	for _, name := range changed {
		changedNames[name] = true
		whole = whole || path.Base(name) == "go.mod" || path.Base(name) == "go.work"
		if _, available := sources[name]; !available && path.Ext(name) == ".go" {
			missingDirs[path.Dir(name)] = true
		}
	}
	primary := map[string]bool{}
	focusRoots := map[string]bool{}
	directCallers := map[string]bool{}
	pending := map[string]bool{}
	interactions := map[string][]DesignInteraction{}
	for _, group := range packages.Tasks {
		if !strings.HasPrefix(group.ID, "package:") {
			plan.Tasks = append(plan.Tasks, group)
			continue
		}
		unit := strings.TrimPrefix(group.ID, "package:")
		for _, omitted := range group.Omitted {
			problem := fmt.Sprintf("%s: %s", omitted.Target.ID, omitted.Reason)
			if !slices.Contains(plan.Errors, problem) {
				plan.Errors = append(plan.Errors, problem)
			}
		}
		for key, info := range index.nodes {
			alias, imports := info.imports[unit]
			unresolved := false
			for name := range info.selected[unit] {
				unresolved = unresolved || len(index.declarations[unit][name]) == 0
			}
			if imports && (alias == "_" || alias == "." || unresolved) {
				pending[info.file], focusRoots[key] = true, true
				relation := DesignInteraction{Caller: Target{Kind: FileTarget, ID: info.file, Line: info.spans[0].Start}, Callee: Target{Kind: UnitTarget, ID: "package:" + unit}}
				if !slices.Contains(interactions[info.file], relation) {
					interactions[info.file] = append(interactions[info.file], relation)
				}
			}
		}
		for _, source := range group.Sources {
			if !whole && !changedNames[source.ID] && !missingDirs[path.Dir(source.ID)] {
				continue
			}
			pending[source.ID], primary[source.ID] = true, true
			for _, caller := range index.callersOf(unit, source.ID, index.fileNodes(source.ID)) {
				node := index.nodes[caller]
				pending[node.file], focusRoots[caller], directCallers[caller] = true, true, true
				relation := DesignInteraction{Caller: Target{Kind: FileTarget, ID: node.file, Line: node.spans[0].Start}, Callee: source}
				if !slices.Contains(interactions[node.file], relation) {
					interactions[node.file] = append(interactions[node.file], relation)
				}
			}
		}
	}
	if !whole {
		for key := range directCallers {
			node := index.nodes[key]
			if primary[node.file] {
				continue
			}
			for _, caller := range index.callersOf(node.unit, node.file, []string{key}) {
				info := index.nodes[caller]
				pending[info.file], focusRoots[caller] = true, true
				relation := DesignInteraction{Caller: Target{Kind: FileTarget, ID: info.file, Line: info.spans[0].Start}, Callee: Target{Kind: FileTarget, ID: node.file}}
				if !slices.Contains(interactions[info.file], relation) {
					interactions[info.file] = append(interactions[info.file], relation)
				}
			}
		}
	}
	var names []string
	for name := range pending {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		if changed == nil || changedNames[name] {
			target := Target{Kind: FileTarget, ID: name}
			slopTask := DesignTask{ID: "slop:" + name, Package: index.files[name].unit, SlopOnly: true, Source: target, Sources: []Target{target}, Purpose: "assess semantic slop rules over the complete source; this task alone does not establish design-unit completion"}
			bindDesignSource(ctx, &slopTask, sources)
			plan.Tasks = append(plan.Tasks, slopTask)
		}
		roots := index.fileNodes(name)
		if len(roots) > 1 {
			roots = slices.DeleteFunc(roots, func(key string) bool { return key == name })
		}
		for _, root := range roots {
			if !primary[name] && !focusRoots[root] {
				continue
			}
			node := index.nodes[root]
			selected := index.interactionContext([]string{root})
			source := Target{Kind: FileTarget, ID: name}
			id := "file:" + name
			if len(roots) > 1 {
				id = "declaration:" + root
			}
			purpose := "assess the focused declaration and its declared calls using supporting implementations, state and failure paths; context beyond two call hops contains contracts without bodies; do not infer behavior from unseen bodies"
			task := index.interactionTask(id, index.files[name].unit, source, purpose, selected)
			if len(node.spans) > 0 {
				task.Focus = []ContextSpan{{Path: name, SourceSpan: node.spans[0]}}
			}
			for _, relation := range interactions[name] {
				include := false
				if relation.Caller.Line == 0 {
					dependency := strings.TrimPrefix(relation.Callee.ID, "package:")
					include = len(node.selected[dependency]) > 0 || node.imports[dependency] == "_" || node.imports[dependency] == "."
				} else {
					for _, span := range node.spans {
						include = include || relation.Caller.Line >= span.Start && relation.Caller.Line <= span.End
					}
				}
				if include {
					task.Interactions = append(task.Interactions, relation)
				}
			}
			slices.SortFunc(task.Interactions, func(a, b DesignInteraction) int {
				if d := strings.Compare(a.Callee.ID, b.Callee.ID); d != 0 {
					return d
				}
				return a.Caller.Line - b.Caller.Line
			})
			includeInteractionCallees(&task)
			bindDesignSource(ctx, &task, sources)
			plan.Tasks = append(plan.Tasks, task)
		}
	}
	if ctx.Err() != nil && !slices.Contains(plan.Errors, ctx.Err().Error()) {
		plan.Errors = append(plan.Errors, ctx.Err().Error())
	}
	slices.SortFunc(plan.Tasks, func(a, b DesignTask) int {
		if changedNames[a.Source.ID] != changedNames[b.Source.ID] {
			if changedNames[a.Source.ID] {
				return -1
			}
			return 1
		}
		return strings.Compare(a.ID, b.ID)
	})
	return plan
}

func (index designContextIndex) fileNodes(name string) []string {
	var keys []string
	for key, node := range index.nodes {
		if node.file == name {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	return keys
}

func (index designContextIndex) callersOf(unit, source string, roots []string) []string {
	names, methods := map[string]bool{}, map[string]bool{}
	for name, definitions := range index.declarations[unit] {
		for _, key := range definitions {
			if slices.Contains(roots, key) {
				names[name] = true
			}
		}
	}
	for name, definitions := range index.methodNames[unit] {
		for _, key := range definitions {
			if slices.Contains(roots, key) {
				methods[name] = true
			}
		}
	}
	var callers []string
	for key, node := range index.nodes {
		if index.ctx.Err() != nil {
			break
		}
		if node.file == source || len(node.spans) == 0 || (strings.HasSuffix(source, "_test.go") && node.unit != unit) {
			continue
		}
		references := node.selected[unit]
		if node.unit == unit {
			references = node.references
		}
		matched := false
		for name := range names {
			matched = matched || references[name]
		}
		if _, imports := node.imports[unit]; imports || node.unit == unit {
			for name := range methods {
				matched = matched || node.members[name]
			}
		}
		matched = matched || node.imports[unit] == "_" || node.imports[unit] == "."
		if matched {
			callers = append(callers, key)
		}
	}
	slices.Sort(callers)
	return callers
}

// interactionBodyDepth bounds implementation context; deeper references retain contracts.
const interactionBodyDepth = 2

type interactionVisit struct {
	key   string
	depth int
}

func (index designContextIndex) interactionContext(seeds []string) map[string]int {
	selected := map[string]int{}
	initialized := map[string]bool{}
	rootUnit := ""
	if len(seeds) > 0 {
		rootUnit = index.nodes[seeds[0]].unit
	}
	var queue []interactionVisit
	for _, key := range seeds {
		queue = append(queue, interactionVisit{key, 0})
		node := index.nodes[key]
		for name, definitions := range index.variables[node.unit] {
			if slices.Contains(definitions, key) {
				for _, writer := range index.writers[node.unit][name] {
					if !strings.HasSuffix(index.nodes[writer].file, "_test.go") {
						queue = append(queue, interactionVisit{writer, 0})
					}
				}
			}
		}
	}
	for _, key := range seeds {
		node := index.nodes[key]
		if node.similarity != "" {
			for _, peer := range index.similar[node.unit][node.similarity] {
				queue = append(queue, interactionVisit{peer, 0})
			}
		}
	}
	enqueue := func(keys []string, depth int) {
		for _, key := range keys {
			level := depth
			if index.nodes[key].function {
				level++
			}
			queue = append(queue, interactionVisit{key, level})
		}
	}
	for len(queue) > 0 {
		if index.ctx.Err() != nil {
			break
		}
		visit := queue[0]
		queue = queue[1:]
		node := index.nodes[visit.key]
		if node == nil {
			continue
		}
		if prior, seen := selected[visit.key]; seen && prior <= visit.depth {
			continue
		}
		selected[visit.key] = visit.depth
		if visit.depth <= interactionBodyDepth && !initialized[node.unit] {
			initialized[node.unit] = true
			for key, info := range index.nodes {
				if info.unit != node.unit || strings.HasSuffix(info.file, "_test.go") {
					continue
				}
				if info.effects {
					queue = append(queue, interactionVisit{key, 1})
				}
			}
		}
		if visit.depth > interactionBodyDepth && node.contract != nil {
			node = node.contract
		}
		for name := range node.references {
			enqueue(index.declarations[node.unit][name], visit.depth)
			if len(index.variables[node.unit][name]) > 0 {
				for _, writer := range index.writers[node.unit][name] {
					if !strings.HasSuffix(index.nodes[writer].file, "_test.go") {
						queue = append(queue, interactionVisit{writer, visit.depth})
					}
				}
			}
		}
		for name := range node.members {
			enqueue(index.methodNames[node.unit][name], visit.depth)
		}
		if node.unit != rootUnit {
			continue
		}
		for dependency, alias := range node.imports {
			if index.declarations[dependency] == nil {
				continue
			}
			for name := range node.selected[dependency] {
				enqueue(index.declarations[dependency][name], visit.depth)
			}
			for name := range node.members {
				enqueue(index.methodNames[dependency][name], visit.depth)
			}
			if alias == "_" || alias == "." {
				for key, info := range index.nodes {
					if info.unit == dependency && !strings.HasSuffix(info.file, "_test.go") {
						depth := interactionBodyDepth + 1
						if info.effects {
							depth = visit.depth + 1
						}
						queue = append(queue, interactionVisit{key, depth})
					}
				}
				if alias == "." {
					for name := range node.references {
						enqueue(index.declarations[dependency][name], visit.depth)
					}
				}
			}
		}
	}
	return selected
}

func (index designContextIndex) interactionTask(id, unit string, source Target, purpose string, selected map[string]int) DesignTask {
	task := DesignTask{ID: id, Package: unit, Source: source, Sources: []Target{source}, Purpose: purpose}
	byFile := map[string][]bundle.SourceSpan{}
	for key, depth := range selected {
		node := index.nodes[key]
		if depth > interactionBodyDepth && node.contract != nil {
			node = node.contract
		}
		byFile[node.file] = append(byFile[node.file], node.spans...)
	}
	var names []string
	for name := range byFile {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		if name != source.ID {
			task.Context = append(task.Context, Target{Kind: FileTarget, ID: name})
		}
		spans := append(slices.Clone(byFile[name]), index.nodes[name].spans...)
		slices.SortFunc(spans, func(a, b bundle.SourceSpan) int { return a.Start - b.Start })
		var merged []bundle.SourceSpan
		for _, span := range spans {
			if len(merged) > 0 && span.Start <= merged[len(merged)-1].End+1 {
				merged[len(merged)-1].End = max(merged[len(merged)-1].End, span.End)
			} else {
				merged = append(merged, span)
			}
		}
		for _, span := range merged {
			if name == source.ID {
				task.SourceSpans = append(task.SourceSpans, ContextSpan{Path: name, SourceSpan: span})
			} else {
				task.ContextSpans = append(task.ContextSpans, ContextSpan{Path: name, SourceSpan: span})
			}
		}
	}
	for key, depth := range selected {
		node := index.nodes[key]
		if depth > interactionBodyDepth && node.contract != nil {
			node = node.contract
		}
		if node.unit != unit {
			continue
		}
		for dependency := range node.imports {
			if index.unresolved[unit][dependency] {
				omission := Omission{Target: Target{Kind: UnitTarget, ID: "package:" + dependency}, Reason: "local import is unavailable in the permitted inventory"}
				if !slices.Contains(task.Omitted, omission) {
					task.Omitted = append(task.Omitted, omission)
				}
			}
			if index.declarations[dependency] == nil {
				continue
			}
			for symbol := range node.selected[dependency] {
				if len(index.declarations[dependency][symbol]) == 0 {
					omission := Omission{Target: Target{Kind: UnitTarget, ID: "symbol:" + dependency + "." + symbol}, Reason: "referenced local declaration is unavailable"}
					if !slices.Contains(task.Omitted, omission) {
						task.Omitted = append(task.Omitted, omission)
					}
				}
			}
		}
	}
	slices.SortFunc(task.Omitted, func(a, b Omission) int { return strings.Compare(a.Target.ID, b.Target.ID) })
	return task
}

func includeInteractionCallees(task *DesignTask) {
	for _, relation := range task.Interactions {
		if relation.Callee.Kind == FileTarget && !slices.Contains(task.Sources, relation.Callee) && !slices.Contains(task.Context, relation.Callee) {
			task.Context = append(task.Context, relation.Callee)
		}
	}
}
