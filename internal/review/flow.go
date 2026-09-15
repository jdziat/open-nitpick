package review

import (
	"context"
	"errors"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/diff"
	"github.com/jdziat/open-nitpick/internal/prflow"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// flow runs the deterministic flow pass over the complete policy
// filtered diff. It is deliberately independent of model review completion.
func (e *Engine) flow(ctx context.Context, ref vcs.Ref, pr *vcs.PullRequest, files diff.Files, cfg *config.Config) (*prflow.Result, string, string) {
	if cfg == nil || (cfg.Flow.Mode == config.FlowOff || cfg.Flow.Mode == "") {
		return nil, "", ""
	}
	ctx, cancel := context.WithTimeout(ctx, cfg.Flow.Timeout)
	defer cancel()
	goChanges := make(diff.Files, 0, len(files))
	for _, f := range files {
		p := f.Path
		if p == "" {
			p = f.OldPath
		}
		if !strings.HasSuffix(strings.ToLower(p), ".go") || cfg.Ignored(p) || excludedFlowPath(p, cfg.Flow.Exclude) {
			continue
		}
		goChanges = append(goChanges, f)
	}
	if cfg.Flow.Mode == config.FlowAuto && len(goChanges) == 0 {
		result := prflow.Result{Version: 1, Status: prflow.StatusSkipped, Explanation: "no eligible changed Go files"}
		return &result, renderFlowMarkdown(result, "", ""), renderChangedFlowMarkdown(result, "", "")
	}
	source := prflow.RevisionSource(providerFlowSource{provider: e.Provider, ref: ref})
	if lister, ok := e.Provider.(vcs.DirLister); ok {
		source = providerTreeFlowSource{providerFlowSource: providerFlowSource{provider: e.Provider, ref: ref}, lister: lister}
	}
	head := prflow.Revision{Repository: pr.HeadRepo, SHA: pr.HeadSHA}
	if ref.Head == "" || ref.Head == vcs.Worktree {
		head = prflow.Revision{}
	}
	baseSHA := pr.BaseSHA
	if baseSHA == "" {
		if resolved, err := vcs.BaseRevision(ctx, e.Provider, ref); err == nil {
			baseSHA = resolved
		}
	}
	analyze := prflow.Analyze
	if e.FlowWorker != "" {
		analyze = func(ctx context.Context, request prflow.Request) (prflow.Result, error) {
			return prflow.AnalyzeIsolated(ctx, request, e.FlowWorker)
		}
	}
	result, err := analyze(ctx, prflow.Request{
		Base:    prflow.Revision{Repository: pr.BaseRepo, SHA: baseSHA},
		Head:    head,
		Changes: goChanges,
		Source:  source,
		Options: prflow.Options{DepthLimitsSet: true, SkipGenerated: cfg.Review.SkipGenerated, MaxFiles: cfg.Flow.MaxFiles, MaxBytes: cfg.Flow.MaxBytes, MaxCallerDepth: cfg.Flow.MaxDepthCallers, MaxCalleeDepth: cfg.Flow.MaxDepthCallees, MaxNodes: cfg.Flow.MaxNodes, MaxEdges: cfg.Flow.MaxEdges, MaxFlows: cfg.Flow.MaxFlows, Timeout: cfg.Flow.Timeout, IncludeUnchanged: cfg.Flow.IncludeUnchanged, Entrypoints: cfg.Flow.Entrypoints, BuildTags: cfg.Flow.BuildTags, Exclude: append(append([]string(nil), cfg.Flow.Exclude...), cfg.Review.Ignore...)},
	})
	if err != nil {
		e.log().Warn("flow analysis failed", "error", err)
		result = prflow.Result{Version: 1, Status: prflow.StatusFailed, Explanation: "flow analysis failed", Base: prflow.Revision{SHA: baseSHA}, Head: head}
	}
	headBase, baseBase := "", ""
	if pr.HeadSHA != "" {
		if p, err := vcs.SourceBase(ctx, e.Provider, ref, pr.HeadSHA); err == nil {
			headBase = p
		}
	}
	if baseSHA != "" {
		if p, err := vcs.SourceBase(ctx, e.Provider, ref, baseSHA); err == nil {
			baseBase = p
		}
	}
	if ref.Number > 0 && headBase == "" {
		result.Coverage.Omissions = append(result.Coverage.Omissions, prflow.Omission{Reason: "source_links_unavailable", Count: 1})
		if result.Status == prflow.StatusComplete || result.Status == prflow.StatusNoFlow {
			result.Status = prflow.StatusPartial
		}
	}
	if ref.Number > 0 && hasRemovedFlowNode(result.Nodes) && baseBase == "" {
		result.Coverage.Omissions = append(result.Coverage.Omissions, prflow.Omission{Reason: "base_source_links_unavailable", Count: 1})
		if result.Status == prflow.StatusComplete || result.Status == prflow.StatusNoFlow {
			result.Status = prflow.StatusPartial
		}
	}
	return &result, renderFlowMarkdown(result, headBase, baseBase), renderChangedFlowMarkdown(result, headBase, baseBase)
}

type providerFlowSource struct {
	provider vcs.Provider
	ref      vcs.Ref
}

func (s providerFlowSource) ReadFile(ctx context.Context, revision prflow.Revision, name string) ([]byte, error) {
	return s.provider.FileContent(ctx, s.refAt(revision), name)
}

func (s providerFlowSource) ReadFileLimit(ctx context.Context, revision prflow.Revision, name string, limit int) ([]byte, error) {
	if bounded, ok := s.provider.(vcs.LimitedFileProvider); ok {
		data, err := bounded.FileContentLimit(ctx, s.refAt(revision), name, limit)
		if errors.Is(err, vcs.ErrSourceLimit) {
			return nil, prflow.ErrSourceLimit
		}
		return data, err
	}
	return s.ReadFile(ctx, revision, name)
}

func (s providerFlowSource) refAt(revision prflow.Revision) vcs.Ref {
	ref := s.ref
	if owner, repo, ok := strings.Cut(revision.Repository, "/"); ok && owner != "" && repo != "" {
		ref.Owner, ref.Repo = owner, repo
	}
	if revision.SHA == "" {
		return ref.At(vcs.Worktree)
	}
	return ref.At(revision.SHA)
}

type providerTreeFlowSource struct {
	providerFlowSource
	lister vcs.DirLister
}

func (s providerTreeFlowSource) ListDir(ctx context.Context, revision prflow.Revision, dir string) ([]string, error) {
	return s.lister.ListDir(ctx, s.refAt(revision), dir)
}

func excludedFlowPath(name string, patterns []string) bool {
	for _, p := range patterns {
		if ok, err := doublestar.Match(p, name); err == nil && ok {
			return true
		}
	}
	return false
}

func hasRemovedFlowNode(nodes []prflow.Node) bool {
	for _, node := range nodes {
		if node.State == prflow.ChangeRemoved {
			return true
		}
	}
	return false
}

func renderFlowMarkdown(r prflow.Result, headBase, baseBase string) string {
	if markdown, err := r.MarkdownSources(headBase, baseBase); err == nil {
		return markdown
	}
	fallback := failedFlowRender(r)
	markdown, _ := fallback.Markdown("")
	return markdown
}

func renderChangedFlowMarkdown(r prflow.Result, headBase, baseBase string) string {
	if markdown, err := r.ChangedMarkdownSources(headBase, baseBase); err == nil {
		return markdown
	}
	fallback := failedFlowRender(r)
	markdown, _ := fallback.ChangedMarkdownSources("", "")
	return markdown
}

func failedFlowRender(r prflow.Result) prflow.Result {
	return prflow.Result{Version: 1, Status: prflow.StatusFailed, Explanation: "flow evidence could not be rendered safely", Base: r.Base, Head: r.Head, Coverage: r.Coverage}
}
