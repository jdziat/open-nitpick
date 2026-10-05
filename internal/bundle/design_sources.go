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

	// The reads the loop below will make, fetched ahead in order. The loop
	// stays serial because its limits (paths, bytes) are decided in order, and
	// it takes each answer from the stream, so what is kept and what is cut
	// off is what a serial read produced. The stream holds readConcurrency
	// answers at most, so a repository with a large path limit and a small
	// byte budget does not fetch the whole inventory before dropping it.
	want := designReadNames(cfg, ordered, denied, limits)
	stream := newFetchStream(ctx, want, readConcurrency, fetch)
	defer stream.stop()
	cursor := 0

	used, reads := 0, 0
	missed := false
	for _, name := range ordered {
		verdict, reason := designCandidate(cfg, name, denied)
		if verdict == candidateExclude {
			exclude(name, reason)
			continue
		}
		if reason == "" && limits.Paths > 0 && reads >= limits.Paths {
			reason = "design inventory path limit reached"
		}
		if reason == "" && ctx.Err() != nil {
			reason = ctx.Err().Error()
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
		// The stream yields candidates in ordered order. Names the loop
		// omitted never reached here, so skip past them, then take this
		// name's answer. designCandidate is the only thing that admits or
		// skips a name, so the stream cannot disagree with the loop above;
		// if it ever does, say so instead of quietly reading serially.
		for cursor < len(want) && want[cursor] != name {
			cursor++
			stream.discard()
		}
		if cursor < len(want) && want[cursor] == name {
			cursor++
			if got, ok := stream.next(); ok {
				source, err = got.content, got.err
			} else {
				source, err = nil, fmt.Errorf("design source stream closed at %s", name)
			}
		} else {
			if !missed {
				view.Errors = append(view.Errors, fmt.Sprintf("design source prefetch missed %s", name))
				missed = true
			}
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
// and returns when all have finished. No fn starts after ctx is done: the loop
// watches ctx while it waits for a slot and re-checks after taking one, so a
// cancellation is not overtaken by a slot freeing.
func inParallel(ctx context.Context, n int, fn func(i int)) {
	if n == 0 {
		return
	}
	var wg sync.WaitGroup
	slots := make(chan struct{}, readConcurrency)
spawn:
	for i := 0; i < n; i++ {
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			break spawn
		}
		if ctx.Err() != nil {
			<-slots
			break
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			fn(i)
		}()
	}
	wg.Wait()
}

// candidateVerdict is what the ordered pass and the prefetch both decide with.
type candidateVerdict int

const (
	candidateRead candidateVerdict = iota
	candidateExclude
	candidateOmit
)

// designCandidate is the one predicate for whether an ordered name is read,
// excluded outright, or omitted with a reason. CaptureDesignSources and the
// prefetch both call it, so they cannot disagree about what will be read.
func designCandidate(cfg *config.Config, name string, denied map[string]string) (candidateVerdict, string) {
	if designMetadataPath(name) {
		return candidateExclude, "repository metadata"
	}
	if cfg.Ignored(name) {
		return candidateExclude, ReasonIgnored
	}
	if invalidSourcePath(name) {
		return candidateOmit, "invalid source path"
	}
	if denied[name] != "" {
		return candidateOmit, denied[name]
	}
	return candidateRead, ""
}

// designReadNames lists, in order, the files CaptureDesignSources will try to
// read: those that pass designCandidate, up to the path limit.
func designReadNames(cfg *config.Config, ordered []string, denied map[string]string, limits SourceLimits) []string {
	var want []string
	for _, name := range ordered {
		if verdict, _ := designCandidate(cfg, name, denied); verdict != candidateRead {
			continue
		}
		if limits.Paths > 0 && len(want) >= limits.Paths {
			break
		}
		want = append(want, name)
	}
	return want
}

// fetchStream reads want concurrently but delivers it in order, a window of
// reads ahead of the caller. CaptureDesignSources decides what to keep in
// order but must not fetch the whole inventory before it knows the byte budget
// will cut it off: holding a window of bodies is a bounded cost, the whole
// inventory is not. A worker holds its answer until the caller consumes it, so
// at most window bodies exist at once. next returns answers in want order;
// discard drops one the loop omitted.
type fetchStream struct {
	ctx      context.Context
	want     []string
	fetch    ContentFetcher
	results  []fetched
	done     []chan struct{}
	consumed []chan struct{}
	todo     chan int
	stopCh   chan struct{}
	cursor   int
	stopped  bool
	once     sync.Once
}

func newFetchStream(ctx context.Context, want []string, window int, fetch ContentFetcher) *fetchStream {
	if window < 1 {
		window = 1
	}
	s := &fetchStream{ctx: ctx, want: want, fetch: fetch, stopCh: make(chan struct{})}
	if fetch == nil || len(want) == 0 {
		s.stopped = true
		return s
	}
	s.results = make([]fetched, len(want))
	s.done = make([]chan struct{}, len(want))
	s.consumed = make([]chan struct{}, len(want))
	s.todo = make(chan int, len(want))
	for i := range want {
		s.done[i] = make(chan struct{})
		s.consumed[i] = make(chan struct{})
		s.todo <- i
	}
	close(s.todo)
	for w := 0; w < window; w++ {
		go s.worker()
	}
	return s
}

// worker fetches one index at a time and does not take another until the caller
// has consumed the last, so the number of held bodies never exceeds the pool.
func (s *fetchStream) worker() {
	for {
		select {
		case <-s.stopCh:
			return
		case i, ok := <-s.todo:
			if !ok {
				return
			}
			content, err := s.fetch(s.ctx, s.want[i])
			s.results[i] = fetched{content: content, err: err}
			close(s.done[i])
			select {
			case <-s.consumed[i]:
			case <-s.stopCh:
				return
			}
		}
	}
}

func (s *fetchStream) next() (fetched, bool) {
	if s.stopped || s.cursor >= len(s.want) {
		return fetched{}, false
	}
	i := s.cursor
	s.cursor++
	select {
	case <-s.done[i]:
		got := s.results[i]
		s.results[i] = fetched{}
		close(s.consumed[i])
		return got, true
	case <-s.stopCh:
		return fetched{}, false
	}
}

// discard drops one queued answer the ordered pass will not use.
func (s *fetchStream) discard() {
	s.next()
}

func (s *fetchStream) stop() {
	s.once.Do(func() {
		s.stopped = true
		close(s.stopCh)
	})
}

// invalidSourcePath reports a name that must not be read: not canonical, or
// able to leave the repository, or carrying a separator or byte a path cannot.
func invalidSourcePath(name string) bool {
	return path.Clean(name) != name || name == "." || strings.HasPrefix(name, "/") ||
		strings.HasPrefix(name, "../") || strings.ContainsAny(name, "\\\x00")
}
