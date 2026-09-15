package prflow

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/jdziat/open-nitpick/internal/diff"
	"github.com/jdziat/open-nitpick/internal/sourcepolicy"
	"golang.org/x/mod/modfile"
)

// ErrSourceLimit means a source refused a file larger than its read budget.
var ErrSourceLimit = errors.New("flow source exceeds byte limit")

// BoundedRevisionSource limits allocation during each source read.
type BoundedRevisionSource interface {
	RevisionSource
	ReadFileLimit(context.Context, Revision, string, int) ([]byte, error)
}

type materializer struct {
	ctx       context.Context
	options   Options
	files     []SourceFile
	omissions []Omission
	seen      map[string]bool
	bytes     int
	stopped   bool
}

func materialize(ctx context.Context, req Request, o Options) ([]SourceFile, []Omission, error) {
	m := materializer{ctx: ctx, options: o, seen: map[string]bool{}}
	provided := append([]SourceFile(nil), req.Files...)
	sort.SliceStable(provided, func(i, j int) bool { return provided[i].Path < provided[j].Path })
	providedSeen := map[string]bool{}
	for _, file := range provided {
		key := fmt.Sprintf("%s:%t", file.Path, file.Removed)
		if providedSeen[key] {
			continue
		}
		providedSeen[key] = true
		delete(m.seen, file.Path)
		if m.selectPath(file.Path) {
			m.accept(file)
		}
	}
	if req.Source != nil {
		changes := append(diff.Files(nil), req.Changes...)
		sort.SliceStable(changes, func(i, j int) bool { return changePath(changes[i]) < changePath(changes[j]) })
		for _, change := range changes {
			name := changePath(change)
			if !m.selectPath(name) {
				continue
			}
			revision := req.Head
			removed := change.Kind == diff.ChangeDeleted
			if removed {
				revision = req.Base
			}
			m.read(req.Source, revision, name, removed)
		}
		m.removed(req, changes)
		if (o.IncludeUnchanged || len(o.Entrypoints) > 0 || len(o.EntryPoints) > 0) && !m.stopped {
			if source, ok := req.Source.(RevisionTreeSource); ok {
				m.context(source, req.Head)
			} else {
				addOmissionSlice(&m.omissions, "context_unavailable", 1)
			}
		}
	}
	sort.Slice(m.files, func(i, j int) bool { return m.files[i].Path < m.files[j].Path })
	return m.files, m.omissions, nil
}

func changePath(file *diff.File) string {
	if file == nil {
		return ""
	}
	if file.Path == "" {
		return file.OldPath
	}
	return file.Path
}

func (m *materializer) selectPath(name string) bool {
	if (!strings.HasSuffix(name, ".go") && path.Base(name) != "go.mod") || excludedPath(name, m.options.Exclude) || m.seen[name] {
		return false
	}
	m.seen[name] = true
	if !validPath(name) {
		addOmissionSlice(&m.omissions, "unsafe_path", 1)
		return false
	}
	if m.ctx.Err() != nil {
		if !m.stopped {
			addOmissionSlice(&m.omissions, "timeout", 1)
		}
		m.stopped = true
		return false
	}
	if len(m.files) >= m.options.MaxFiles {
		addOmissionSlice(&m.omissions, "max_files", 1)
		return false
	}
	if m.stopped || m.bytes >= m.options.MaxBytes {
		addOmissionSlice(&m.omissions, "max_bytes", 1)
		return false
	}
	return true
}

func (m *materializer) accept(file SourceFile) {
	if len(file.Content) > m.options.MaxBytes-m.bytes {
		addOmissionSlice(&m.omissions, "max_bytes", 1)
		m.stopped = true
		return
	}
	m.bytes += len(file.Content)
	if m.options.SkipGenerated && strings.HasSuffix(file.Path, ".go") && sourcepolicy.Generated(string(file.Content)) {
		addOmissionSlice(&m.omissions, "generated_file", 1)
		return
	}
	file.Content = append([]byte(nil), file.Content...)
	file.ChangedLines = append([]int(nil), file.ChangedLines...)
	m.files = append(m.files, file)
}

func (m *materializer) read(source RevisionSource, revision Revision, name string, removed bool) {
	var content []byte
	var err error
	if bounded, ok := source.(BoundedRevisionSource); ok {
		content, err = bounded.ReadFileLimit(m.ctx, revision, name, m.options.MaxBytes-m.bytes)
	} else {
		content, err = source.ReadFile(m.ctx, revision, name)
	}
	if err != nil {
		switch {
		case errors.Is(err, ErrSourceLimit):
			addOmissionSlice(&m.omissions, "max_bytes", 1)
			m.stopped = true
		case m.ctx.Err() != nil:
			addOmissionSlice(&m.omissions, "timeout", 1)
			m.stopped = true
		default:
			addOmissionSlice(&m.omissions, "source_unavailable", 1)
		}
		return
	}
	m.accept(SourceFile{Path: name, Content: content, Removed: removed})
}

func (m *materializer) context(source RevisionTreeSource, revision Revision) {
	dirs := []string{""}
	visited := 0
	for len(dirs) > 0 {
		if m.ctx.Err() != nil {
			addOmissionSlice(&m.omissions, "timeout", 1)
			return
		}
		// Empty or non-Go directories still consume traversal work.
		if visited >= m.options.MaxFiles {
			addOmissionSlice(&m.omissions, "directory_limit", len(dirs))
			return
		}
		visited++
		dir := dirs[0]
		dirs = dirs[1:]
		entries, err := source.ListDir(m.ctx, revision, dir)
		if err != nil {
			addOmissionSlice(&m.omissions, "source_unavailable", 1)
			continue
		}
		sort.Strings(entries)
		for _, entry := range entries {
			leaf := strings.TrimSuffix(entry, "/")
			if !validPath(leaf) || strings.Contains(leaf, "/") {
				addOmissionSlice(&m.omissions, "unsafe_path", 1)
				continue
			}
			name := path.Join(dir, leaf)
			if strings.HasSuffix(entry, "/") {
				if leaf != ".git" && !excludedPath(name+"/", m.options.Exclude) {
					dirs = append(dirs, name)
				}
				continue
			}
			if m.seen[name] || (!strings.HasSuffix(name, ".go") && path.Base(name) != "go.mod") || excludedPath(name, m.options.Exclude) {
				continue
			}
			if m.selectPath(name) {
				m.read(source, revision, name, false)
			} else if m.stopped || len(m.files) >= m.options.MaxFiles || m.bytes >= m.options.MaxBytes {
				return
			}
		}
	}
}

// discoverModules uses metadata retained within the same source budget as Go
// files. Replacements and dependency downloads are never executed.
func discoverModules(files []SourceFile) ([]Module, []Omission) {
	var modules []Module
	var omissions []Omission
	for _, file := range files {
		if path.Base(file.Path) != "go.mod" {
			continue
		}
		parsed, err := modfile.ParseLax(file.Path, file.Content, nil)
		if err != nil || parsed.Module == nil || parsed.Module.Mod.Path == "" {
			addOmissionSlice(&omissions, "module_metadata_invalid", 1)
			continue
		}
		dir := path.Dir(file.Path)
		if dir == "." {
			dir = ""
		}
		modules = append(modules, Module{Dir: dir, Path: parsed.Module.Mod.Path})
	}
	return modules, omissions
}

func snapshotDigest(files []SourceFile) string {
	hash := sha256.New()
	for _, file := range files {
		if file.Removed {
			continue
		}
		_, _ = fmt.Fprintf(hash, "%d:%s:%d:", len(file.Path), file.Path, len(file.Content))
		_, _ = hash.Write(file.Content)
	}
	return fmt.Sprintf("working-tree:%x", hash.Sum(nil))
}
