package review

import (
	"context"
	"fmt"
	"slices"

	"github.com/jdziat/open-nitpick/internal/bundle"
	"github.com/jdziat/open-nitpick/internal/diff"
	"github.com/jdziat/open-nitpick/internal/practices"
	"github.com/jdziat/open-nitpick/internal/standards"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// assembleDesign captures one bounded source view before package planning.
func (e *Engine) assembleDesign(ctx context.Context, ref vcs.Ref, files diff.Files, reserve bundle.Reserve) practices.DesignPacking {
	var blocked []bundle.Skip
	if tree, ok := e.Provider.(*vcs.Tree); ok {
		for _, name := range tree.Unbudgeted {
			blocked = append(blocked, bundle.Skip{Path: name, Reason: "tree review budget excluded source"})
		}
		for _, skip := range tree.Skipped {
			blocked = append(blocked, bundle.Skip{Path: skip.Path, Reason: skip.Reason})
		}
	}
	changed := make([]string, 0, len(files))
	for _, file := range files {
		if !e.Config.Ignored(file.Path) && !file.Binary {
			changed = append(changed, file.Path)
		}
	}
	fetch := func(ctx context.Context, name string) ([]byte, error) { return e.Provider.FileContent(ctx, ref, name) }
	view := bundle.CaptureDesignSources(ctx, e.Config, changed, fetch, bundle.ListerFrom(e.Provider, ref), blocked, bundle.SourceLimits{Paths: 4096, Bytes: 32 << 20})
	names := make([]string, 0, len(view.Content))
	for name := range view.Content {
		names = append(names, name)
	}
	slices.Sort(names)
	source := make([]standards.File, 0, len(names))
	for _, name := range names {
		source = append(source, standards.File{Path: name, Src: view.Content[name]})
	}
	inventory, _ := practices.InspectDesign(source, nil)
	inventory.Errors = append(inventory.Errors, view.Errors...)
	for _, skip := range view.Omitted {
		inventory.Errors = append(inventory.Errors, fmt.Sprintf("%s: %s", skip.Path, skip.Reason))
	}
	for _, skip := range view.Excluded {
		inventory.Limitations = append(inventory.Limitations, fmt.Sprintf("%s excluded: %s", skip.Path, skip.Reason))
	}
	inventory.Limitations = append(inventory.Limitations, "source inventory is bounded to 4096 paths and 32 MiB; only Go source has a dependency graph")
	plan := practices.PlanDesign(ctx, inventory, source, changed)
	packed := practices.PackDesign(ctx, e.Config, plan, source, files, reserve)
	packed.Plan.Skipped = append(packed.Plan.Skipped, view.Excluded...)
	return packed
}
