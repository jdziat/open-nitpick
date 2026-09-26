package practices

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"slices"
	"sort"
	"strings"

	"github.com/jdziat/open-nitpick/internal/bundle"
	"github.com/jdziat/open-nitpick/internal/standards"
)

// DesignPlan records package assessments before model batching can omit context.
type DesignPlan struct {
	Version     string       `json:"version"`
	Tasks       []DesignTask `json:"tasks"`
	Limitations []string     `json:"limitations"`
	Errors      []string     `json:"errors,omitempty"`
}

// PlanDesign groups package sources with their direct dependencies and callers.
// A nil changed slice selects the tree; an empty nonnil slice selects no tasks.
// The caller must supply an inventory of the full permitted source scope.
func PlanDesign(ctx context.Context, inventory DesignInventory, files []standards.File, changed []string) DesignPlan {
	plan := DesignPlan{Version: "2", Limitations: slices.Clone(inventory.Limitations), Errors: slices.Clone(inventory.Errors)}
	plan.Limitations = append(plan.Limitations, "package context follows direct Go imports; dynamic calls and transitive effects are not resolved")
	sources := make(map[string][]byte, len(files))
	for _, file := range files {
		sources[file.Path] = file.Src
	}
	contextIndex := indexDesignContext(ctx, inventory, files)
	plan.Errors = append(plan.Errors, contextIndex.errors...)
	plan.Limitations = append(plan.Limitations, "dependency context follows referenced declarations and local helpers; caller context follows declarations referencing the primary package; selection does not type-check dynamic dispatch")
	members := map[string]bool{}
	for _, unit := range inventory.Units {
		for _, name := range unit.Files {
			members[name] = true
		}
	}
	selected := map[string]bool{}
	missingGoDirs := map[string]bool{}
	manifestsChanged := false
	for _, name := range changed {
		selected[name] = true
		if _, present := sources[name]; !present && path.Ext(name) == ".go" {
			missingGoDirs[path.Dir(name)] = true
		}
		if path.Base(name) == "go.mod" || path.Base(name) == "go.work" {
			manifestsChanged = true
		}
	}
	for _, unit := range inventory.Units {
		included := changed == nil || manifestsChanged
		for _, name := range unit.Files {
			included = included || selected[name] || missingGoDirs[path.Dir(name)]
		}
		if !included || len(unit.Files) == 0 {
			continue
		}
		names := slices.Clone(unit.Files)
		sort.Strings(names)
		task := DesignTask{ID: "package:" + unit.ID, Source: Target{Kind: FileTarget, ID: names[0]}, Purpose: "assess package contracts, state ownership, coupled rules and failure propagation using dependency and caller source"}
		own := map[string]bool{}
		for _, name := range names {
			own[name] = true
			task.Sources = append(task.Sources, Target{Kind: FileTarget, ID: name})
		}
		contextNames, spans, omissions := contextIndex.contextFor(unit)
		for _, span := range spans {
			if !own[span.Path] {
				task.ContextSpans = append(task.ContextSpans, span)
			}
		}
		task.Omitted = append(task.Omitted, omissions...)
		for _, name := range contextNames {
			if !own[name] {
				task.Context = append(task.Context, Target{Kind: FileTarget, ID: name})
			}
		}
		unresolved := slices.Clone(unit.Unresolved)
		sort.Strings(unresolved)
		for _, id := range slices.Compact(unresolved) {
			task.Omitted = append(task.Omitted, Omission{Target: Target{Kind: UnitTarget, ID: "package:" + id}, Reason: "local import could not be resolved in the permitted inventory"})
		}
		bindDesignSource(ctx, &task, sources)
		plan.Tasks = append(plan.Tasks, task)
	}
	for _, file := range files {
		if members[file.Path] || (changed != nil && !selected[file.Path]) {
			continue
		}
		target := Target{Kind: FileTarget, ID: file.Path}
		task := DesignTask{ID: "source:" + file.Path, Source: target, Sources: []Target{target}, Purpose: "assess source contracts and failure propagation; no package graph is available for this source"}
		bindDesignSource(ctx, &task, sources)
		plan.Tasks = append(plan.Tasks, task)
	}
	for name := range selected {
		if _, present := sources[name]; present || members[name] {
			continue
		}
		target := Target{Kind: FileTarget, ID: name}
		task := DesignTask{ID: "source:" + name, Source: target, Sources: []Target{target}, Purpose: "assess the removed or unavailable source and its affected contracts"}
		bindDesignSource(ctx, &task, sources)
		plan.Tasks = append(plan.Tasks, task)
	}
	if ctx.Err() != nil && !slices.Contains(plan.Errors, ctx.Err().Error()) {
		plan.Errors = append(plan.Errors, ctx.Err().Error())
	}
	slices.SortFunc(plan.Tasks, func(a, b DesignTask) int { return strings.Compare(a.ID, b.ID) })
	return plan
}

func bindDesignSource(ctx context.Context, task *DesignTask, sources map[string][]byte) {
	task.SourceDigest = ""
	ranges, err := task.contextRanges()
	if err != nil {
		task.Omitted = append(task.Omitted, Omission{Target: Target{Kind: UnitTarget, ID: task.ID}, Reason: err.Error()})
		return
	}
	targets := append(slices.Clone(task.Sources), task.Context...)
	for _, target := range targets {
		if _, ok := sources[target.ID]; !ok {
			task.Omitted = append(task.Omitted, Omission{Target: target, Reason: "planned source content is unavailable"})
		}
	}
	metadata := *task
	metadata.SourceDigest = ""
	encoded, _ := json.Marshal(metadata)
	hash := sha256.New()
	_, _ = hash.Write(encoded)
	for index, target := range targets {
		if err := ctx.Err(); err != nil {
			for _, remaining := range targets[index:] {
				if !slices.ContainsFunc(task.Omitted, func(o Omission) bool { return o.Target == remaining }) {
					task.Omitted = append(task.Omitted, Omission{Target: remaining, Reason: err.Error()})
				}
			}
			return
		}
		source, available := sources[target.ID]
		if spans := ranges[target.ID]; len(spans) > 0 {
			selected, err := bundle.SelectSourceSpans(string(source), spans)
			if err != nil || !available {
				task.Omitted = append(task.Omitted, Omission{Target: target, Reason: "planned context spans are unavailable"})
				return
			}
			source = []byte(selected)
		}
		_, _ = fmt.Fprintf(hash, "%d:%s:%t:%d:", len(target.ID), target.ID, available, len(source))
		_, _ = hash.Write(source)
	}
	task.SourceDigest = hex.EncodeToString(hash.Sum(nil))
}
