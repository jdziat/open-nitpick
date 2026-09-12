package review

import (
	"context"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/jdziat/open-nitpick/internal/bundle"
	"github.com/jdziat/open-nitpick/internal/diff"
	"github.com/jdziat/open-nitpick/internal/practices"
	"github.com/jdziat/open-nitpick/internal/standards"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// DesignExecution retains the frozen inputs and intended tasks of a design pass.
type DesignExecution struct {
	practices.DesignPacking
	Sources  []standards.File
	Excluded []bundle.Skip
}

// assembleDesign captures one bounded source view before package planning.
func (e *Engine) assembleDesign(ctx context.Context, ref vcs.Ref, files diff.Files, reserve bundle.Reserve) DesignExecution {
	var blocked []bundle.Skip
	routine := map[string]string{}
	if tree, ok := e.Provider.(*vcs.Tree); ok {
		for _, name := range tree.Unbudgeted {
			blocked = append(blocked, bundle.Skip{Path: name, Reason: "tree review budget excluded source"})
		}
		for _, skip := range tree.Skipped {
			base := path.Base(skip.Path)
			if (skip.Reason == "binary" || skip.Reason == "empty") && base != "go.mod" && base != "go.work" {
				routine[skip.Path] = skip.Reason
			}
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
	view.Omitted = slices.DeleteFunc(view.Omitted, func(skip bundle.Skip) bool {
		if reason, ok := routine[skip.Path]; ok && reason == skip.Reason {
			view.Excluded = append(view.Excluded, skip)
			return true
		}
		return false
	})
	for _, file := range files {
		if source, ok := view.Content[file.Path]; ok && file.Kind == diff.ChangeAdded && !additionMatchesSource(file, string(source)) {
			delete(view.Content, file.Path)
			view.Omitted = append(view.Omitted, bundle.Skip{Path: file.Path, Reason: "addition diff does not match the frozen source"})
		}
	}
	names := make([]string, 0, len(view.Content))
	for name := range view.Content {
		names = append(names, name)
	}
	slices.Sort(names)
	source := make([]standards.File, 0, len(names))
	for _, name := range names {
		source = append(source, standards.File{Path: name, Src: view.Content[name]})
	}
	inventorySources := slices.Clone(source)
	for _, skip := range append(slices.Clone(view.Omitted), view.Excluded...) {
		if path.Base(skip.Path) == "go.mod" {
			// Preserve an unreadable module boundary without supplying invented source.
			inventorySources = append(inventorySources, standards.File{Path: skip.Path})
		}
	}
	inventory, _ := practices.InspectDesign(inventorySources, nil)
	if len(view.Errors) > 0 {
		// Incomplete enumeration cannot establish that nested manifests are absent.
		inventory.Units = nil
		inventory.Limitations = append(inventory.Limitations, "package identities unavailable because repository enumeration was incomplete")
	}
	inventory.Errors = append(inventory.Errors, view.Errors...)
	for _, skip := range view.Omitted {
		inventory.Errors = append(inventory.Errors, fmt.Sprintf("%s: %s", skip.Path, skip.Reason))
	}
	for _, skip := range view.Excluded {
		inventory.Limitations = append(inventory.Limitations, fmt.Sprintf("%s excluded: %s", skip.Path, skip.Reason))
	}
	inventory.Limitations = append(inventory.Limitations, "source inventory is bounded to 4096 paths and 32 MiB; only Go source has a dependency graph")
	plan := practices.PlanDesignInteractions(ctx, inventory, source, changed)
	packed := practices.PackDesign(ctx, e.Config, plan, source, files, reserve)
	packed.Plan.Skipped = append(packed.Plan.Skipped, view.Excluded...)
	return DesignExecution{DesignPacking: packed, Sources: source, Excluded: view.Excluded}
}

func additionMatchesSource(file *diff.File, source string) bool {
	var lines []string
	if source != "" {
		lines = strings.Split(strings.TrimSuffix(source, "\n"), "\n")
	}
	seen := 0
	for _, hunk := range file.Hunks {
		for _, line := range hunk.Lines {
			if line.Kind != diff.LineAdded || seen >= len(lines) || line.NewLine != seen+1 || line.Content != lines[seen] {
				return false
			}
			seen++
		}
	}
	return seen == len(lines)
}
