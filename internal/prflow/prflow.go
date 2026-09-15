// Package prflow extracts deterministic, source-linked static application flows
// from Go source snapshots. It deliberately reports unresolved calls as
// boundaries instead of guessing runtime behaviour.
package prflow

import (
	"context"
	"fmt"
	"go/ast"
	"go/build"
	"go/build/constraint"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"path"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/jdziat/open-nitpick/internal/diff"
)

// Status describes how much of a requested analysis completed.
type Status string

const (
	// StatusComplete means the declared static scope finished within limits.
	StatusComplete Status = "complete"
	// StatusPartial means limits or missing metadata reduced the scope.
	StatusPartial Status = "partial"
	// StatusNoFlow means eligible source was scanned but had no retained relationship.
	StatusNoFlow Status = "no_flow"
	// StatusSkipped means the selected language or mode had nothing to analyze.
	StatusSkipped Status = "skipped"
	// StatusUnavailable means required source could not be established.
	StatusUnavailable Status = "unavailable"
	// StatusFailed means an internal extraction contract failed.
	StatusFailed Status = "failed"
)

// Resolution describes confidence in an edge target.
type Resolution string

const (
	// ResolutionResolved identifies an exact static target.
	ResolutionResolved Resolution = "resolved"
	// ResolutionInferred identifies a justified candidate relationship.
	ResolutionInferred Resolution = "inferred"
	// ResolutionUnresolved identifies a dynamic or unavailable target.
	ResolutionUnresolved Resolution = "unresolved"
)

// ChangeState identifies the relationship of a declaration to the change.
type ChangeState string

const (
	// ChangeAdded marks a declaration introduced by the change.
	ChangeAdded ChangeState = "added"
	// ChangeModified marks a declaration touched by the change.
	ChangeModified ChangeState = "modified"
	// ChangeUnchanged marks a declaration kept only as static context.
	ChangeUnchanged ChangeState = "unchanged"
	// ChangeRemoved marks a declaration present only on the base revision.
	ChangeRemoved ChangeState = "removed"
)

// Revision identifies the immutable source snapshot being analysed.
type Revision struct {
	Repository string `json:"repository,omitempty"`
	SHA        string `json:"sha,omitempty"`
	Snapshot   string `json:"snapshot,omitempty"`
}

// Module identifies a Go module in the materialized snapshot. Dir is a
// repository-relative directory (the empty string names the repository root).
// Path is the module path declared by its go.mod. The extractor never runs the
// go command to discover this metadata.
type Module struct {
	Dir  string
	Path string
}

// SourceFile is one materialized source file in the head snapshot. Content is
// copied by Analyze and is never modified. ChangedLines may be omitted when
// Changes in Request supplies parsed diff information.
type SourceFile struct {
	Path           string
	Content        []byte
	Package        string
	ChangedLines   []int
	Removed        bool
	removedSymbols map[string]bool
	comparePath    string
}

// RevisionSource supplies immutable files when Files is not pre-materialized.
type RevisionSource interface {
	ReadFile(context.Context, Revision, string) ([]byte, error)
}

// RevisionTreeSource supplies directory entries for the same immutable
// snapshot. It is used only when unchanged context is requested.
type RevisionTreeSource interface {
	RevisionSource
	ListDir(context.Context, Revision, string) ([]string, error)
}

// Request is the complete immutable input to Analyze.
type Request struct {
	Base, Head Revision
	Files      []SourceFile
	Changes    diff.Files
	Source     RevisionSource
	// Modules is the immutable module metadata discovered while materializing
	// the snapshot. It is optional; without it, cross-package imports remain
	// boundaries because their repository identity is unknown.
	Modules []Module
	Options Options
}

// Options bounds analysis independently of rendering limits.
type Options struct {
	MaxFiles       int
	MaxBytes       int
	MaxCallerDepth int
	MaxCalleeDepth int
	MaxNodes       int
	MaxEdges       int
	MaxFlows       int
	Timeout        time.Duration
	// DepthLimitsSet preserves explicit zero caller/callee limits. When false,
	// zero values use the built-in defaults for backwards compatibility.
	DepthLimitsSet bool
	// SkipGenerated excludes checked-in generated Go files from extraction.
	SkipGenerated    bool
	IncludeUnchanged bool
	Entrypoints      []string
	// EntryPoints is accepted as an alias for callers using initialism-style
	// naming; Entrypoints takes precedence when both are supplied.
	EntryPoints []string
	// BuildTags selects files whose //go:build constraints match these tags,
	// alongside the current GOOS and GOARCH.
	BuildTags []string
	// Exclude omits repository-relative paths matched by doublestar globs.
	Exclude []string
}

func (o Options) withDefaults() Options {
	if o.MaxFiles <= 0 {
		o.MaxFiles = 2000
	}
	if o.MaxBytes <= 0 {
		o.MaxBytes = 32 << 20
	}
	if !o.DepthLimitsSet && o.MaxCallerDepth <= 0 {
		o.MaxCallerDepth = 8
	}
	if !o.DepthLimitsSet && o.MaxCalleeDepth <= 0 {
		o.MaxCalleeDepth = 4
	}
	if o.MaxNodes <= 0 {
		o.MaxNodes = 500
	}
	if o.MaxEdges <= 0 {
		o.MaxEdges = 1000
	}
	if o.MaxFlows <= 0 {
		o.MaxFlows = 3
	}
	if o.Timeout <= 0 {
		o.Timeout = 15 * time.Second
	}
	return o
}

// Location identifies source evidence. Path is repository-relative.
type Location struct {
	Path     string   `json:"path"`
	Line     int      `json:"line"`
	Revision Revision `json:"revision,omitempty"`
}

// Node is a declaration or explicit unresolved/external boundary.
type Node struct {
	ID       string      `json:"id"`
	Label    string      `json:"label"`
	Kind     string      `json:"kind"`
	Source   Location    `json:"source"`
	State    ChangeState `json:"state"`
	Changed  bool        `json:"changed"`
	Boundary bool        `json:"boundary,omitempty"`
	Reason   string      `json:"reason,omitempty"`
}

// Edge connects a caller to a callee and records the call-site evidence.
type Edge struct {
	ID         string     `json:"id"`
	From       string     `json:"from"`
	To         string     `json:"to"`
	Kind       string     `json:"kind"`
	Resolution Resolution `json:"resolution"`
	Reason     string     `json:"reason,omitempty"`
	Source     Location   `json:"source"`
}

// Omission records bounded or unavailable analysis data.
type Omission struct {
	Reason string `json:"reason"`
	Count  int    `json:"count"`
}

// Coverage describes what the extractor inspected and what it could not.
type Coverage struct {
	Files        int        `json:"files"`
	Bytes        int        `json:"bytes"`
	Declarations int        `json:"declarations"`
	Edges        int        `json:"edges"`
	Omissions    []Omission `json:"omissions,omitempty"`
}

// Flow names one bounded connected component.
type Flow struct {
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	Nodes []string `json:"nodes"`
	Edges []string `json:"edges"`
}

// Result is a versioned, deterministic extraction result.
type Result struct {
	Version       int      `json:"version"`
	Generator     string   `json:"generator,omitempty"`
	GOOS          string   `json:"goos,omitempty"`
	GOARCH        string   `json:"goarch,omitempty"`
	BuildTags     []string `json:"build_tags,omitempty"`
	OptionsDigest string   `json:"options_digest,omitempty"`
	Status        Status   `json:"status"`
	Explanation   string   `json:"explanation,omitempty"`
	Base          Revision `json:"base,omitempty"`
	Head          Revision `json:"head,omitempty"`
	Nodes         []Node   `json:"nodes,omitempty"`
	Edges         []Edge   `json:"edges,omitempty"`
	Flows         []Flow   `json:"flows,omitempty"`
	Coverage      Coverage `json:"coverage"`
}

type decl struct {
	node    Node
	obj     *types.Func
	fn      *ast.FuncDecl
	literal *ast.FuncLit
	body    *ast.BlockStmt
	file    *source
	start   int
	end     int
}
type source struct {
	file SourceFile
	ast  *ast.File
	fset *token.FileSet
	info *types.Info
	pkg  *types.Package
	// importPath is the package's module-qualified path when module metadata is
	// available, or its repository-relative directory for local relative paths.
	// missingImports records imports that could not be loaded.
	importPath     string
	missingImports map[string]bool
}
type rawEdge struct {
	from   *decl
	to     *decl
	target string
	kind   string
	res    Resolution
	reason string
	loc    Location
}

// Analyze extracts direct calls, callers and explicit unresolved boundaries.
// All ordering is stable and all limits result in diagnostics rather than
// silently dropping changed roots.
func Analyze(parent context.Context, req Request) (Result, error) {
	result, err := analyze(parent, req)
	finalizeResult(&result, req.Options.withDefaults())
	return result, err
}

func analyze(parent context.Context, req Request) (Result, error) {
	o := req.Options.withDefaults()
	ctx, cancel := context.WithTimeout(parent, o.Timeout)
	defer cancel()
	result := Result{Version: 1, Status: StatusComplete, Base: req.Base, Head: req.Head}
	files, omissions, err := materialize(ctx, req, o)
	if err != nil {
		result.Status = StatusUnavailable
		result.Explanation = err.Error()
		result.Coverage.Omissions = omissions
		return result, nil
	}
	omissions = append(omissions, mapRemovedChanges(files, req.Changes)...)
	// Module metadata is materialized under the same immutable source budget as
	// Go files. Explicit request metadata wins when both describe a module.
	discoveredModules, moduleOmissions := discoverModules(files)
	if len(discoveredModules) > 0 {
		req.Modules = append(append([]Module(nil), req.Modules...), discoveredModules...)
	}
	if result.Head.SHA == "" {
		result.Head.Snapshot = snapshotDigest(files)
	}
	result.Coverage.Omissions = omissions
	result.Coverage.Omissions = append(result.Coverage.Omissions, moduleOmissions...)
	if reducesCoverage(result.Coverage.Omissions) {
		result.Status = StatusPartial
	}
	result.Coverage.Files = len(files)
	for _, f := range files {
		result.Coverage.Bytes += len(f.Content)
	}
	if len(files) == 0 {
		switch {
		case hasOmission(result.Coverage.Omissions, "timeout"):
			result.Status = StatusUnavailable
			result.Explanation = "source analysis was cancelled before it could run"
		case hasOmission(result.Coverage.Omissions, "source_unavailable"):
			result.Status = StatusUnavailable
			result.Explanation = "selected Go source could not be read"
		case hasOmission(result.Coverage.Omissions, "max_files") || hasOmission(result.Coverage.Omissions, "max_bytes"):
			result.Status = StatusPartial
			result.Explanation = "analysis limits excluded every selected Go source file"
		default:
			result.Status = StatusSkipped
			result.Explanation = "no Go source files were selected"
		}
		return result, nil
	}

	sources, parseOmissions := parseSources(files, o)
	result.Coverage.Omissions = append(result.Coverage.Omissions, parseOmissions...)
	if reducesCoverage(parseOmissions) {
		result.Status = StatusPartial
	}
	if len(sources) == 0 {
		goSelected := false
		for _, f := range files {
			if strings.HasSuffix(f.Path, ".go") {
				goSelected = true
				break
			}
		}
		if !goSelected {
			result.Status = StatusSkipped
			result.Explanation = "no Go source files were selected"
		} else {
			result.Status = StatusUnavailable
			result.Explanation = "no selected Go file could be parsed"
		}
		return result, nil
	}
	checkOmissions := checkSources(sources, req.Modules)
	result.Coverage.Omissions = append(result.Coverage.Omissions, checkOmissions...)
	if reducesCoverage(checkOmissions) {
		result.Status = StatusPartial
	}

	decls, byObj, byName := declarations(sources)
	result.Coverage.Declarations = len(decls)
	changed, removed := changedSet(files, req.Changes)
	for i := range decls {
		if decls[i].fn != nil {
			setChange(&decls[i].node, changed[decls[i].file.file.Path], removed[decls[i].file.file.Path], decls[i].fn, decls[i].file)
			continue
		}
		setChangeRange(&decls[i].node, changed[decls[i].file.file.Path], removed[decls[i].file.file.Path], decls[i].start, decls[i].end, decls[i].file)
	}

	edges := collectEdges(decls, byObj, byName)
	result.Coverage.Edges = len(edges)
	seeds, seedErr := seeds(decls, o)
	if seedErr != nil {
		result.Status = StatusUnavailable
		result.Explanation = seedErr.Error()
		return result, nil
	}
	if len(seeds) == 0 {
		if result.Status == StatusComplete {
			result.Status = StatusNoFlow
		}
		if result.Status == StatusNoFlow {
			result.Explanation = "no changed declaration or requested entrypoint was found"
		}
		return result, nil
	}

	selected, selectedEdges, trunc := traverse(seeds, edges, o)
	if trunc > 0 {
		addOmission(&result.Coverage, "limit_reached", trunc)
		result.Status = StatusPartial
	}
	if err := ctx.Err(); err != nil {
		addOmission(&result.Coverage, "timeout", 1)
		result.Status = StatusPartial
	}
	result.Nodes = make([]Node, 0, len(selected))
	for _, d := range selected {
		result.Nodes = append(result.Nodes, d.node)
	}
	// Boundary targets are first-class nodes so unresolved and external calls
	// remain visible in the evidence table and cannot look like missing edges.
	boundarySeen := map[string]bool{}
	boundaryTrunc := 0
	keptEdges := selectedEdges[:0]
	for _, e := range selectedEdges {
		if e.to == nil {
			id := e.targetID()
			if boundarySeen[id] {
				keptEdges = append(keptEdges, e)
				continue
			}
			if len(result.Nodes) >= o.MaxNodes {
				boundaryTrunc++
				continue
			}
			boundarySeen[id] = true
			state := ChangeUnchanged
			if e.from.file.file.Removed {
				state = ChangeRemoved
			}
			result.Nodes = append(result.Nodes, Node{ID: id, Label: e.target, Kind: "boundary", Source: e.loc, State: state, Boundary: true, Reason: e.reason})
		}
		keptEdges = append(keptEdges, e)
	}
	selectedEdges = keptEdges
	if boundaryTrunc > 0 {
		addOmission(&result.Coverage, "limit_reached", boundaryTrunc)
		result.Status = StatusPartial
	}
	sort.Slice(result.Nodes, func(i, j int) bool { return result.Nodes[i].ID < result.Nodes[j].ID })
	result.Edges = make([]Edge, 0, len(selectedEdges))
	for _, e := range selectedEdges {
		result.Edges = append(result.Edges, Edge{ID: edgeID(e), From: e.from.node.ID, To: e.targetID(), Kind: e.kind, Resolution: e.res, Reason: e.reason, Source: e.loc})
	}
	sort.Slice(result.Edges, func(i, j int) bool { return result.Edges[i].ID < result.Edges[j].ID })
	for _, edge := range result.Edges {
		if edge.Resolution != ResolutionUnresolved {
			continue
		}
		reason := edge.Reason
		if reason == "" {
			reason = "unresolved_target"
		}
		addOmission(&result.Coverage, reason, 1)
		result.Status = StatusPartial
		result.Explanation = "one or more retained call targets could not be resolved"
	}
	if len(result.Edges) == 0 && result.Status == StatusComplete {
		result.Status = StatusNoFlow
		result.Explanation = "no reachable relationships were found"
	} else {
		result.Flows = buildFlows(result.Nodes, result.Edges, len(result.Nodes))
		for _, flow := range result.Flows {
			rootID := strings.TrimPrefix(flow.ID, "flow:")
			var root Node
			found := false
			for _, node := range result.Nodes {
				if node.ID == rootID {
					root, found = node, true
					break
				}
			}
			if found && !supportedCLIRoot(root, result.Edges) {
				addOmission(&result.Coverage, "root_not_found", 1)
			}
		}
		if len(result.Flows) > o.MaxFlows {
			addOmission(&result.Coverage, "max_flows", len(result.Flows)-o.MaxFlows)
			result.Flows = result.Flows[:o.MaxFlows]
			result.Status = StatusPartial
		}
	}
	if result.Status == StatusComplete {
		result.Explanation = "static source relationships resolved within the declared scope"
	}
	return result, nil
}

func hasOmission(omissions []Omission, reason string) bool {
	for _, omission := range omissions {
		if omission.Reason == reason {
			return true
		}
	}
	return false
}

func reducesCoverage(omissions []Omission) bool {
	for _, omission := range omissions {
		switch omission.Reason {
		case "build_tags":
			// A file outside the requested build constraints was not selected.
		case "root_not_found":
			// Library flows remain complete when no supported CLI root exists.
		default:
			return true
		}
	}
	return false
}

func validPath(p string) bool {
	return p != "" && !path.IsAbs(p) && path.Clean(p) == p && p != "." && p != ".." && !strings.HasPrefix(p, "../") && !strings.ContainsAny(p, "\\\r\n\x00")
}

func excludedPath(name string, patterns []string) bool {
	for _, pattern := range patterns {
		if ok, err := doublestar.Match(pattern, name); err == nil && ok {
			return true
		}
	}
	return false
}

func parseSources(files []SourceFile, o Options) ([]*source, []Omission) {
	var out []*source
	var omissions []Omission
	fset := token.NewFileSet()
	for _, sf := range files {
		if !strings.HasSuffix(sf.Path, ".go") {
			continue
		}
		selected, err := selectedByBuildTags(sf.Path, sf.Content, o.BuildTags)
		if err != nil {
			addOmissionSlice(&omissions, "build_constraint_error", 1)
			continue
		}
		if !selected {
			addOmissionSlice(&omissions, "build_tags", 1)
			continue
		}
		f, err := parser.ParseFile(fset, sf.Path, sf.Content, parser.ParseComments)
		if err != nil {
			addOmissionSlice(&omissions, "parse_error", 1)
			continue
		}
		out = append(out, &source{file: sf, ast: f, fset: fset, info: &types.Info{Types: map[ast.Expr]types.TypeAndValue{}, Defs: map[*ast.Ident]types.Object{}, Uses: map[*ast.Ident]types.Object{}, Selections: map[*ast.SelectorExpr]*types.Selection{}}})
	}
	return out, omissions
}

func selectedByBuildTags(name string, content []byte, tags []string) (bool, error) {
	if !filenameBuildMatch(name) {
		return false, nil
	}
	var expressions []constraint.Expr
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "//go:build ") || constraint.IsPlusBuild(line) {
			expr, err := constraint.Parse(line)
			if err != nil {
				return false, err
			}
			expressions = append(expressions, expr)
			continue
		}
		if line != "" && !strings.HasPrefix(line, "//") {
			break
		}
	}
	if len(expressions) == 0 {
		return true, nil
	}
	enabled := map[string]bool{"gc": true, runtime.GOOS: true, runtime.GOARCH: true}
	for _, release := range build.Default.ReleaseTags {
		enabled[release] = true
	}
	for _, tag := range tags {
		enabled[tag] = true
	}
	for _, expr := range expressions {
		if !expr.Eval(func(tag string) bool { return enabled[tag] }) {
			return false, nil
		}
	}
	return true, nil
}

// filenameBuildMatch applies the filename half of the Go build-constraint
// rules without touching the working tree. A checked-in snapshot can contain
// files for another GOOS/GOARCH; those files are outside the declared static
// scope and should not be parsed as though they were active here.
func filenameBuildMatch(name string) bool {
	base := path.Base(name)
	if !strings.HasSuffix(base, ".go") {
		return false
	}
	stem := strings.TrimSuffix(base, ".go")
	parts := strings.Split(stem, "_")
	if len(parts) < 2 {
		return true
	}
	goos := map[string]bool{
		"aix": true, "android": true, "darwin": true, "dragonfly": true,
		"freebsd": true, "hurd": true, "illumos": true, "ios": true,
		"js": true, "linux": true, "netbsd": true, "openbsd": true,
		"plan9": true, "solaris": true, "wasip1": true, "windows": true,
	}
	goarch := map[string]bool{
		"386": true, "amd64": true, "arm": true, "arm64": true,
		"loong64": true, "mips": true, "mips64": true, "mips64le": true,
		"mipsle": true, "ppc64": true, "ppc64le": true, "riscv64": true,
		"s390x": true, "wasm": true,
	}
	last := parts[len(parts)-1]
	if goarch[last] && last != runtime.GOARCH {
		return false
	}
	if goos[last] && last != runtime.GOOS {
		return false
	}
	if len(parts) >= 3 {
		second := parts[len(parts)-2]
		if goos[second] && second != runtime.GOOS {
			return false
		}
		if goarch[second] && second != runtime.GOARCH {
			return false
		}
	}
	return true
}

type packageGroup struct {
	dir        string
	name       string
	path       string
	removed    bool
	files      []*source
	pkg        *types.Package
	state      uint8
	missing    map[string]bool
	typeErrors int
}

// checkSources type-checks selected packages against one another in memory.
// importer.Default remains useful for the standard library, but it cannot see
// files in an immutable review snapshot; the snapshot importer below supplies
// those packages and records every unavailable dependency instead of allowing
// a name-based guess to masquerade as a resolved call.
func checkSources(sources []*source, modules []Module) []Omission {
	groupsByKey := map[string]*packageGroup{}
	for _, s := range sources {
		dir := path.Dir(s.file.Path)
		key := dir + "\x00" + s.ast.Name.Name + "\x00" + strconv.FormatBool(s.file.Removed)
		g := groupsByKey[key]
		if g == nil {
			g = &packageGroup{dir: dir, name: s.ast.Name.Name, path: moduleImportPath(dir, s.ast.Name.Name, modules), removed: s.file.Removed, missing: map[string]bool{}}
			groupsByKey[key] = g
		}
		g.files = append(g.files, s)
		s.importPath = g.path
		s.missingImports = g.missing
	}
	groups := make([]*packageGroup, 0, len(groupsByKey))
	for _, g := range groupsByKey {
		sort.Slice(g.files, func(i, j int) bool { return g.files[i].file.Path < g.files[j].file.Path })
		groups = append(groups, g)
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].path < groups[j].path })
	imports := newSnapshotImporter(groupsByKey, groups)
	for _, g := range groups {
		_, _ = imports.check(g)
	}
	var omissions []Omission
	for _, g := range groups {
		for _, s := range g.files {
			s.pkg = g.pkg
			if s.info == nil {
				s.info = emptyTypesInfo()
			}
		}
		if len(g.missing) > 0 {
			addOmissionSlice(&omissions, "missing_import", len(g.missing))
		}
		if g.typeErrors > 0 {
			addOmissionSlice(&omissions, "type_check_error", g.typeErrors)
		}
	}
	return omissions
}

func emptyTypesInfo() *types.Info {
	return &types.Info{Types: map[ast.Expr]types.TypeAndValue{}, Defs: map[*ast.Ident]types.Object{}, Uses: map[*ast.Ident]types.Object{}, Selections: map[*ast.SelectorExpr]*types.Selection{}}
}

func moduleImportPath(dir, pkg string, modules []Module) string {
	best := Module{}
	found := false
	for _, m := range modules {
		m.Dir = path.Clean(m.Dir)
		if m.Dir == "." {
			m.Dir = ""
		}
		if dir == m.Dir || (m.Dir != "" && strings.HasPrefix(dir, m.Dir+"/")) || (m.Dir == "" && !found) {
			if !found || len(m.Dir) > len(best.Dir) {
				best, found = m, true
			}
		}
	}
	if found && best.Path != "" {
		rel := dir
		if best.Dir != "" {
			if dir == best.Dir {
				rel = ""
			} else {
				rel = strings.TrimPrefix(dir, best.Dir+"/")
			}
		}
		if rel == "." || rel == "" {
			return best.Path
		}
		return strings.TrimSuffix(best.Path, "/") + "/" + rel
	}
	if dir == "." {
		return pkg
	}
	return dir
}

type snapshotImporter struct {
	groupsByKey map[string]*packageGroup
	byPath      map[string][]*packageGroup
	byDir       map[string][]*packageGroup
	state       map[*packageGroup]uint8
	stack       []*packageGroup
	std         types.Importer
}

func newSnapshotImporter(groupsByKey map[string]*packageGroup, groups []*packageGroup) *snapshotImporter {
	i := &snapshotImporter{groupsByKey: groupsByKey, byPath: map[string][]*packageGroup{}, byDir: map[string][]*packageGroup{}, state: map[*packageGroup]uint8{}, std: importer.Default()}
	for _, g := range groups {
		i.byPath[g.path] = append(i.byPath[g.path], g)
		i.byDir[g.dir] = append(i.byDir[g.dir], g)
	}
	return i
}

func (i *snapshotImporter) Import(importPath string) (*types.Package, error) {
	if candidates := i.byPath[importPath]; len(candidates) > 0 {
		removed := false
		if len(i.stack) > 0 {
			removed = i.stack[len(i.stack)-1].removed
		}
		for _, candidate := range candidates {
			if candidate.removed == removed {
				return i.check(candidate)
			}
		}
		if len(i.stack) > 0 {
			i.stack[len(i.stack)-1].missing[importPath] = true
		}
		return nil, fmt.Errorf("package %q is unavailable at this revision", importPath)
	}
	// Only standard-library paths may be read from the host export-data cache.
	// A dotted path that is absent from the materialized module index is an
	// unavailable dependency, even when an unrelated checkout happens to be
	// present in GOPATH or the module cache.
	first := importPath
	if slash := strings.IndexByte(first, '/'); slash >= 0 {
		first = first[:slash]
	}
	if strings.Contains(first, ".") {
		if len(i.stack) > 0 {
			i.stack[len(i.stack)-1].missing[importPath] = true
		}
		return nil, fmt.Errorf("package %q is not in the materialized snapshot", importPath)
	}
	pkg, err := i.std.Import(importPath)
	if err != nil {
		if len(i.stack) > 0 {
			i.stack[len(i.stack)-1].missing[importPath] = true
		}
		return nil, err
	}
	return pkg, nil
}

func (i *snapshotImporter) check(g *packageGroup) (*types.Package, error) {
	if g.state == 2 {
		return g.pkg, nil
	}
	if g.state == 1 {
		if g.pkg == nil {
			g.pkg = types.NewPackage(g.path, g.name)
		}
		return g.pkg, nil
	}
	g.state = 1
	g.pkg = types.NewPackage(g.path, g.name)
	files := make([]*ast.File, len(g.files))
	info := emptyTypesInfo()
	for n, s := range g.files {
		files[n] = s.ast
	}
	i.stack = append(i.stack, g)
	conf := types.Config{Importer: i, FakeImportC: true, Error: func(error) { g.typeErrors++ }}
	pkg, err := conf.Check(g.path, g.files[0].fset, files, info)
	i.stack = i.stack[:len(i.stack)-1]
	if pkg != nil {
		g.pkg = pkg
	}
	for _, s := range g.files {
		s.info = info
		s.pkg = g.pkg
	}
	g.state = 2
	return g.pkg, err
}

func declarations(sources []*source) ([]decl, map[*types.Func]*decl, map[string]*decl) {
	var out []decl
	byObj := map[*types.Func]*decl{}
	var byName map[string]*decl
	for _, s := range sources {
		for _, d := range s.ast.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			line := s.fset.Position(fd.Pos()).Line
			name := fd.Name.Name
			id := packageID(s) + "." + name
			if name == "init" {
				id += "@" + s.file.Path + ":" + strconv.Itoa(line)
			}
			if fd.Recv != nil && len(fd.Recv.List) > 0 {
				recv := recvName(fd.Recv.List[0].Type)
				if recv != "" {
					name = recv + "." + name
					id = packageID(s) + "." + name
				}
			}
			if s.file.removedSymbols != nil && !s.file.removedSymbols[name] {
				continue
			}
			if s.file.Removed {
				id += "#removed"
			}
			label := s.ast.Name.Name + "." + name
			n := decl{node: Node{ID: id, Label: label, Kind: "function", Source: Location{Path: s.file.Path, Line: line}, State: ChangeUnchanged}, fn: fd, body: fd.Body, file: s, start: line, end: s.fset.Position(fd.End()).Line}
			if obj, ok := s.info.Defs[fd.Name].(*types.Func); ok {
				n.obj = obj
				byObj[obj] = &n
			}
			out = append(out, n)
			appendClosures(&out, s, fd.Body)
		}
	}
	// Pointers in byObj must refer to final slice elements.
	byObj = map[*types.Func]*decl{}
	byName = map[string]*decl{}
	for i := range out {
		if out[i].obj != nil {
			byObj[out[i].obj] = &out[i]
		}
		byName[out[i].node.ID] = &out[i]
		byName[out[i].node.Label] = &out[i]
	}
	return out, byObj, byName
}

// appendClosures adds anonymous functions as source-linked declarations. A
// closure is its own traversal node so relationships inside it remain visible,
// while its enclosing declaration still owns the call-site edge that schedules
// it. Nested closures are walked recursively and receive stable source IDs.
func appendClosures(out *[]decl, s *source, body *ast.BlockStmt) {
	if body == nil {
		return
	}
	ast.Inspect(body, func(node ast.Node) bool {
		literal, ok := node.(*ast.FuncLit)
		if !ok {
			return true
		}
		position := s.fset.Position(literal.Pos())
		id := fmt.Sprintf("%s.<closure@%s:%d:%d>", packageID(s), s.file.Path, position.Line, position.Column)
		label := fmt.Sprintf("%s.<closure@%d:%d>", s.ast.Name.Name, position.Line, position.Column)
		*out = append(*out, decl{
			node:    Node{ID: id, Label: label, Kind: "closure", Source: Location{Path: s.file.Path, Line: position.Line}, State: ChangeUnchanged},
			body:    literal.Body,
			literal: literal,
			file:    s,
			start:   position.Line,
			end:     s.fset.Position(literal.End()).Line,
		})
		appendClosures(out, s, literal.Body)
		// The recursive call owns nested literals.
		return false
	})
}

func packageID(s *source) string {
	dir := path.Dir(s.file.Path)
	if dir == "." {
		return s.ast.Name.Name
	}
	return dir + "/" + s.ast.Name.Name
}

func recvName(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.StarExpr:
		return recvName(x.X)
	case *ast.IndexExpr:
		return recvName(x.X)
	case *ast.IndexListExpr:
		return recvName(x.X)
	}
	return ""
}

func changedSet(files []SourceFile, changes diff.Files) (map[string]map[int]bool, map[string]bool) {
	out := map[string]map[int]bool{}
	removed := map[string]bool{}
	for _, f := range files {
		if len(f.ChangedLines) > 0 {
			out[f.Path] = map[int]bool{}
			for _, n := range f.ChangedLines {
				out[f.Path][n] = true
			}
		}
	}
	for _, f := range changes {
		if f == nil {
			continue
		}
		p := f.Path
		if p == "" {
			p = f.OldPath
		}
		if out[p] == nil {
			out[p] = map[int]bool{}
		}
		for _, n := range f.ChangedLines() {
			out[p][n] = true
		}
		if f.Kind == diff.ChangeAdded {
			out[p][0] = true
		}
		if f.Kind == diff.ChangeDeleted {
			removed[p] = true
		}
	}
	return out, removed
}

func setChange(n *Node, lines map[int]bool, removed bool, fd *ast.FuncDecl, s *source) {
	setChangeRange(n, lines, removed, s.fset.Position(fd.Pos()).Line, s.fset.Position(fd.End()).Line, s)
}

func setChangeRange(n *Node, lines map[int]bool, removed bool, start, end int, s *source) {
	if removed || s.file.Removed {
		n.State = ChangeRemoved
		n.Changed = true
		return
	}
	if lines == nil {
		return
	}
	if lines[0] {
		n.State = ChangeAdded
		n.Changed = true
		return
	}
	for line := range lines {
		if line >= start && line <= end {
			n.State = ChangeModified
			n.Changed = true
			return
		}
	}
}

func collectEdges(decls []decl, byObj map[*types.Func]*decl, byName map[string]*decl) []rawEdge {
	closures := make(map[*ast.FuncLit]*decl)
	for i := range decls {
		if decls[i].literal != nil {
			closures[decls[i].literal] = &decls[i]
		}
	}
	var out []rawEdge
	for i := range decls {
		d := &decls[i]
		callKinds := make(map[token.Pos]string)
		if d.fn != nil {
			callKinds = dispatchCases(d)
		}
		ast.Inspect(d.body, func(node ast.Node) bool {
			if _, ok := node.(*ast.FuncLit); ok {
				return false
			}
			switch x := node.(type) {
			case *ast.GoStmt:
				callKinds[x.Call.Pos()] = "go"
			case *ast.DeferStmt:
				callKinds[x.Call.Pos()] = "defer"
			}
			return true
		})
		ast.Inspect(d.body, func(node ast.Node) bool {
			if _, ok := node.(*ast.FuncLit); ok {
				return false
			}
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			loc := Location{Path: d.file.file.Path, Line: d.file.fset.Position(call.Pos()).Line}
			kind, res, reason := "call", ResolutionResolved, ""
			if k := callKinds[call.Pos()]; k != "" {
				kind = k
			}
			var target *decl
			label := ""
			funExpr := unwrapCallee(call.Fun)
			switch fun := funExpr.(type) {
			case *ast.FuncLit:
				target = closures[fun]
				label = "anonymous function"
				if kind == "call" {
					kind = "closure"
				}
			case *ast.Ident:
				obj, known := d.file.info.Uses[fun]
				switch typed := obj.(type) {
				case *types.Func:
					target = byObj[typed]
				case *types.Var:
					kind, res, reason = "dynamic", ResolutionUnresolved, "dynamic_call"
				case *types.Builtin, *types.TypeName:
					// Builtins and type conversions are language operations, not
					// application relationships.
					return true
				case nil:
					if known {
						kind, res, reason = "dynamic", ResolutionUnresolved, "unresolved_target"
					}
				default:
					kind, res, reason = "dynamic", ResolutionUnresolved, "dynamic_call"
				}
				if target == nil && !known && res != ResolutionUnresolved {
					target = byName[packageID(d.file)+"."+fun.Name]
				}
				label = fun.Name
			case *ast.SelectorExpr:
				label = fun.Sel.Name
				obj, _ := d.file.info.Uses[fun.Sel].(*types.Func)
				if selection := d.file.info.Selections[fun]; selection != nil {
					obj, _ = selection.Obj().(*types.Func)
					if _, dynamic := selection.Recv().Underlying().(*types.Interface); dynamic {
						res, reason = ResolutionUnresolved, "interface_method"
					}
				}
				if obj != nil && res == ResolutionResolved {
					target = byObj[obj.Origin()]
					if target == nil {
						reason = "external_call"
						label = obj.FullName()
						if boundary := boundaryKind(obj); boundary != "" {
							kind = boundary
						}
					}
				} else if obj == nil {
					res, reason = ResolutionUnresolved, "unresolved_target"
					if qualifier, ok := fun.X.(*ast.Ident); ok {
						label = qualifier.Name + "." + label
						if packageName, ok := d.file.info.Uses[qualifier].(*types.PkgName); ok {
							if d.file.missingImports[packageName.Imported().Path()] {
								reason = "missing_import"
							}
						} else if d.file.info.Uses[qualifier] == nil {
							if importPath, imported := importedPath(d.file.ast, qualifier.Name); imported && d.file.missingImports[importPath] {
								reason = "missing_import"
							}
						}
					}
				}
			default:
				kind = "dynamic"
				res = ResolutionUnresolved
				reason = "dynamic_call"
				label = "dynamic call"
			}
			// Arguments carrying a local function are callback candidates. This
			// relationship remains inferred even when the registration function
			// itself resolves directly.
			for _, arg := range call.Args {
				if id, ok := arg.(*ast.Ident); ok {
					if obj, ok := d.file.info.Uses[id].(*types.Func); ok {
						if cb := byObj[obj]; cb != nil {
							out = append(out, rawEdge{from: d, to: cb, target: cb.node.ID, kind: "callback", res: ResolutionInferred, reason: "callback_registration", loc: loc})
						}
					}
				} else if literal, ok := unwrapCallee(arg).(*ast.FuncLit); ok {
					cb := closures[literal]
					out = append(out, rawEdge{from: d, to: cb, target: "callback literal", kind: "callback", res: ResolutionInferred, reason: "callback_literal", loc: loc})
				}
			}
			if target != nil {
				out = append(out, rawEdge{from: d, to: target, target: target.node.ID, kind: kind, res: res, reason: reason, loc: loc})
				return true
			}
			if label == "" {
				label = "unresolved call"
			}
			if res == ResolutionResolved && reason == "" {
				res, reason = ResolutionUnresolved, "unresolved_target"
			}
			out = append(out, rawEdge{from: d, target: label, kind: kind, res: res, reason: reason, loc: loc})
			return true
		})
	}
	return dedupeEdges(out)
}

func unwrapCallee(expr ast.Expr) ast.Expr {
	switch x := expr.(type) {
	case *ast.ParenExpr:
		return unwrapCallee(x.X)
	case *ast.IndexExpr:
		return unwrapCallee(x.X)
	case *ast.IndexListExpr:
		return unwrapCallee(x.X)
	default:
		return expr
	}
}

func importedPath(f *ast.File, name string) (string, bool) {
	for _, imp := range f.Imports {
		if imp.Name != nil {
			if imp.Name.Name == name {
				return strings.Trim(imp.Path.Value, "\""), true
			}
			continue
		}
		p := strings.Trim(imp.Path.Value, "\"")
		if path.Base(p) == name {
			return p, true
		}
	}
	return "", false
}

func dedupeEdges(in []rawEdge) []rawEdge {
	sort.Slice(in, func(i, j int) bool { return edgeID(in[i]) < edgeID(in[j]) })
	out := in[:0]
	seen := map[string]bool{}
	for _, e := range in {
		id := edgeID(e)
		if !seen[id] {
			seen[id] = true
			out = append(out, e)
		}
	}
	return out
}
func edgeID(e rawEdge) string {
	from := e.from.node.ID
	to := e.targetID()
	return from + "->" + to + "@" + e.loc.Path + ":" + fmt.Sprint(e.loc.Line) + ":" + e.kind
}
func (e rawEdge) targetID() string {
	if e.to != nil {
		return e.to.node.ID
	}
	return "boundary:" + e.from.node.ID + ":" + e.target + "@" + strconv.Itoa(e.loc.Line)
}

func seeds(decls []decl, o Options) ([]*decl, error) {
	var out []*decl
	entrypoints := o.Entrypoints
	if len(entrypoints) == 0 {
		entrypoints = o.EntryPoints
	}
	if len(entrypoints) > 0 {
		for _, name := range entrypoints {
			matches := 0
			for i := range decls {
				if decls[i].node.ID == name || decls[i].node.Label == name || entrypointLocation(name, &decls[i]) {
					out = append(out, &decls[i])
					matches++
				}
			}
			if matches == 0 {
				return nil, fmt.Errorf("requested entrypoint %q was not found", name)
			}
			if matches > 1 {
				return nil, fmt.Errorf("requested entrypoint %q is ambiguous", name)
			}
		}
	}
	if len(out) == 0 {
		for i := range decls {
			if decls[i].node.Changed {
				out = append(out, &decls[i])
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].node.ID < out[j].node.ID })
	return out, nil
}

func entrypointLocation(entry string, d *decl) bool {
	if d.fn == nil {
		return false
	}
	path, lineText, ok := strings.Cut(entry, ":")
	if !ok || path == "" {
		return false
	}
	line, err := strconv.Atoi(lineText)
	if err != nil || line < 1 || d.file.file.Path != path {
		return false
	}
	start := d.file.fset.Position(d.fn.Pos()).Line
	end := d.file.fset.Position(d.fn.End()).Line
	return line >= start && line <= end
}

func buildFlows(nodes []Node, edges []Edge, max int) []Flow {
	if len(nodes) == 0 {
		return nil
	}
	adj := map[string][]string{}
	for _, e := range edges {
		adj[e.From] = append(adj[e.From], e.To)
	}
	seen := map[string]bool{}
	var flows []Flow
	for _, n := range nodes {
		if seen[n.ID] {
			continue
		}
		q := []string{n.ID}
		seen[n.ID] = true
		var ns []string
		var es []string
		for len(q) > 0 {
			x := q[0]
			q = q[1:]
			ns = append(ns, x)
			for _, e := range edges {
				if e.From == x {
					es = append(es, e.ID)
					if !seen[e.To] {
						seen[e.To] = true
						q = append(q, e.To)
					}
				}
				if e.To == x && !seen[e.From] {
					seen[e.From] = true
					q = append(q, e.From)
				}
			}
		}
		sort.Strings(ns)
		sort.Strings(es)
		root := flowRoot(ns, nodes, edges)
		flows = append(flows, Flow{ID: "flow:" + root.ID, Name: root.Label, Nodes: ns, Edges: es})
		if len(flows) >= max {
			break
		}
	}
	return flows
}

func addOmission(c *Coverage, reason string, count int) {
	for i := range c.Omissions {
		if c.Omissions[i].Reason == reason {
			c.Omissions[i].Count += count
			return
		}
	}
	c.Omissions = append(c.Omissions, Omission{Reason: reason, Count: count})
	sort.Slice(c.Omissions, func(i, j int) bool { return c.Omissions[i].Reason < c.Omissions[j].Reason })
}
func addOmissionSlice(c *[]Omission, reason string, count int) {
	for i := range *c {
		if (*c)[i].Reason == reason {
			(*c)[i].Count += count
			return
		}
	}
	*c = append(*c, Omission{Reason: reason, Count: count})
	sort.Slice(*c, func(i, j int) bool { return (*c)[i].Reason < (*c)[j].Reason })
}
