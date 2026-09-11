package bundle

import (
	"bytes"
	"context"
	"fmt"
	"path"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/jdziat/open-nitpick/internal/config"
)

// SourceLimits bounds the inventory used to discover design dependencies and callers.
type SourceLimits struct {
	Paths int
	Bytes int
}

// SourceView freezes permitted design source and retains the scope it could not read.
type SourceView struct {
	Content  map[string][]byte
	Omitted  []Skip
	Excluded []Skip
	Errors   []string
}

// CaptureDesignSources reads changed files and the repository's Go graph once.
// Blocked files, including tree-budget omissions, cannot reappear as context.
func CaptureDesignSources(ctx context.Context, cfg *config.Config, changed []string, fetch ContentFetcher, list DirLister, blocked []Skip, limits SourceLimits) SourceView {
	view := SourceView{Content: map[string][]byte{}}
	excluded := map[string]bool{}
	exclude := func(name, reason string) {
		if !excluded[name] {
			view.Excluded = append(view.Excluded, Skip{Path: name, Reason: reason})
			excluded[name] = true
		}
	}
	denied := map[string]string{}
	for _, skip := range blocked {
		denied[skip.Path] = skip.Reason
	}
	wanted := map[string]bool{}
	for _, name := range changed {
		wanted[name] = true
	}
	names := map[string]bool{}
	for _, name := range changed {
		names[name] = true
	}
	visited := 0
	dirs := []string{""}
	if list == nil {
		view.Errors = append(view.Errors, "repository listing unavailable; caller inventory is incomplete")
		dirs = nil
	}
	for len(dirs) > 0 {
		dir := dirs[0]
		dirs = dirs[1:]
		if err := ctx.Err(); err != nil {
			view.Errors = append(view.Errors, err.Error())
			break
		}
		entries, err := list(ctx, dir)
		if err != nil {
			view.Errors = append(view.Errors, fmt.Sprintf("%s: directory listing unavailable", dir))
			continue
		}
		slices.Sort(entries)
		for _, entry := range entries {
			visited++
			if limits.Paths > 0 && visited > limits.Paths {
				view.Errors = append(view.Errors, "design inventory path limit reached; callers may be missing")
				dirs = nil
				break
			}
			base := strings.TrimSuffix(entry, "/")
			if base == "" || base == "." || base == ".." || strings.ContainsAny(base, "/\\") {
				view.Errors = append(view.Errors, "repository listing contained an invalid entry")
				continue
			}
			name := path.Join(dir, base)
			if cfg.Ignored(name) || cfg.Ignored(name+"/") {
				exclude(name, ReasonIgnored)
				continue
			}
			if strings.HasSuffix(entry, "/") {
				dirs = append(dirs, name)
				continue
			}
			if wanted[name] || path.Ext(name) == ".go" || base == "go.mod" || base == "go.work" {
				names[name] = true
			}
		}
	}
	ordered := make([]string, 0, len(names))
	for name := range names {
		ordered = append(ordered, name)
	}
	slices.Sort(ordered)
	used, reads := 0, 0
	for _, name := range ordered {
		reason := denied[name]
		if path.Clean(name) != name || name == "." || strings.HasPrefix(name, "/") || strings.HasPrefix(name, "../") || strings.ContainsAny(name, "\\\x00") {
			reason = "invalid source path"
		}
		if reason == "" && limits.Paths > 0 && reads >= limits.Paths {
			reason = "design inventory path limit reached"
		}
		if reason == "" && ctx.Err() != nil {
			reason = ctx.Err().Error()
		}
		if cfg.Ignored(name) {
			exclude(name, ReasonIgnored)
			continue
		}
		if reason == "" && limits.Bytes > 0 && used >= limits.Bytes {
			reason = "design inventory byte limit reached"
		}
		if reason != "" {
			view.Omitted = append(view.Omitted, Skip{Path: name, Reason: reason})
			continue
		}
		if fetch == nil {
			view.Omitted = append(view.Omitted, Skip{Path: name, Reason: ReasonUnavailable})
			continue
		}
		reads++
		source, err := fetch(ctx, name)
		switch {
		case err != nil:
			reason = ReasonUnavailable
		case !utf8.Valid(source) || bytes.ContainsRune(source, 0):
			reason = ReasonNotText
		case cfg.Review.SkipGenerated && isGenerated(string(source)):
			exclude(name, ReasonGenerated)
			continue
		case cfg.Review.MaxFileBytes > 0 && len(source) > cfg.Review.MaxFileBytes:
			reason = ReasonTooLarge
		case limits.Bytes > 0 && len(source) > limits.Bytes-used:
			reason = "design inventory byte limit reached"
		}
		if reason != "" {
			view.Omitted = append(view.Omitted, Skip{Path: name, Reason: reason})
			continue
		}
		used += len(source)
		view.Content[name] = bytes.Clone(source)
	}
	for _, skip := range blocked {
		if !names[skip.Path] && !cfg.Ignored(skip.Path) {
			view.Omitted = append(view.Omitted, skip)
		}
	}
	return view
}
