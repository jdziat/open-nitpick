package bundle

import (
	"bytes"
	"context"
	"fmt"
	"path"
	"slices"
	"strings"
	"sync"
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
//
// fetch and list are called from several goroutines at once, so they must be
// safe for that. Which files are kept, and in what order, does not depend on
// the order the answers arrive in.
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
		if err := ctx.Err(); err != nil {
			view.Errors = append(view.Errors, err.Error())
			break
		}
		// One directory level at a time, listed together. A forge answers each
		// listing with a request, and a serial walk of a repository with a
		// hundred directories spent a minute before the first model call. The
		// level is then read in queue order, so what the path limit cuts off
		// and the order callers are found in are what the serial walk gave.
		level := dirs
		dirs = nil
		listed := make([]listing, len(level))
		inParallel(ctx, len(level), func(i int) {
			listed[i].entries, listed[i].err = list(ctx, level[i])
		})
		for i, dir := range level {
			if err := ctx.Err(); err != nil {
				view.Errors = append(view.Errors, err.Error())
				dirs = nil
				break
			}
			entries, err := listed[i].entries, listed[i].err
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
				if designMetadataPath(name) {
					exclude(name, "repository metadata")
					continue
				}
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
			if limits.Paths > 0 && visited > limits.Paths {
				break
			}
		}
	}
	ordered := make([]string, 0, len(names))
	for name := range names {
		ordered = append(ordered, name)
	}
	slices.Sort(ordered)

	// The reads the loop below will make, fetched together first. The loop
	// stays serial because its limits (paths, bytes) are decided in order, and
	// it takes each answer from here, so what is kept and what is cut off is
	// what a serial read produced. Only the byte limit cannot be known ahead,
	// so a repository that hits it fetches a few files it then drops.
	prefetched := prefetchSources(ctx, cfg, ordered, denied, fetch, limits)

	used, reads := 0, 0
	for _, name := range ordered {
		if designMetadataPath(name) {
			exclude(name, "repository metadata")
			continue
		}
		reason := denied[name]
		if invalidSourcePath(name) {
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
		var (
			source []byte
			err    error
		)
		if got, ok := prefetched[name]; ok {
			source, err = got.content, got.err
		} else {
			source, err = fetch(ctx, name)
		}
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

func designMetadataPath(name string) bool {
	for _, component := range strings.Split(name, "/") {
		switch strings.ToLower(component) {
		case ".git", ".hg", ".svn":
			return true
		}
	}
	return false
}

// listing is one directory's answer, held until its level has been listed.
type listing struct {
	entries []string
	err     error
}

// fetched is one file's answer, held until the ordered pass reaches it.
type fetched struct {
	content []byte
	err     error
}

// readConcurrency bounds requests in flight against a forge. Enough that a
// hundred reads do not queue behind one another, few enough that the forge's
// abuse detection, which retryOnAbuse backs off from, stays quiet.
const readConcurrency = 8

// inParallel runs fn for each index below n, at most readConcurrency at once,
// and returns when all have finished. fn is not started once ctx is done.
func inParallel(ctx context.Context, n int, fn func(i int)) {
	if n == 0 {
		return
	}
	var wg sync.WaitGroup
	slots := make(chan struct{}, readConcurrency)
	for i := 0; i < n; i++ {
		if ctx.Err() != nil {
			break
		}
		slots <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			fn(i)
		}()
	}
	wg.Wait()
}

// prefetchSources reads, concurrently, the files CaptureDesignSources will
// ask for in order: those that pass its path checks, up to the path limit.
func prefetchSources(ctx context.Context, cfg *config.Config, ordered []string, denied map[string]string, fetch ContentFetcher, limits SourceLimits) map[string]fetched {
	if fetch == nil {
		return nil
	}
	var want []string
	for _, name := range ordered {
		if designMetadataPath(name) || denied[name] != "" || cfg.Ignored(name) {
			continue
		}
		if invalidSourcePath(name) {
			continue
		}
		if limits.Paths > 0 && len(want) >= limits.Paths {
			break
		}
		want = append(want, name)
	}
	got := make([]fetched, len(want))
	inParallel(ctx, len(want), func(i int) {
		got[i].content, got[i].err = fetch(ctx, want[i])
	})
	out := make(map[string]fetched, len(want))
	for i, name := range want {
		out[name] = got[i]
	}
	return out
}

// invalidSourcePath reports a name that must not be read: not canonical, or
// able to leave the repository, or carrying a separator or byte a path cannot.
func invalidSourcePath(name string) bool {
	return path.Clean(name) != name || name == "." || strings.HasPrefix(name, "/") ||
		strings.HasPrefix(name, "../") || strings.ContainsAny(name, "\\\x00")
}
