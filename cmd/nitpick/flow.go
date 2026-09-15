package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/diff"
	"github.com/jdziat/open-nitpick/internal/flowhtml"
	"github.com/jdziat/open-nitpick/internal/prflow"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// flowStringList accepts a repeatable command-line option without making a
// caller encode a comma-separated list (paths and entrypoint names may contain
// commas).
type flowStringList []string

func (s *flowStringList) String() string { return strings.Join(*s, ",") }
func (s *flowStringList) Set(v string) error {
	if strings.TrimSpace(v) == "" {
		return errors.New("value must not be empty")
	}
	*s = append(*s, v)
	return nil
}

// runFlow extracts a deterministic source-level application flow and writes
// Markdown or versioned JSON. It never publishes to a forge or calls a model.
func runFlow(ctx context.Context, args []string) error {
	var (
		repo       string
		configPath string
		base       string
		head       string
		mode       string
		format     string
		output     string
		maxFiles   int
		maxBytes   int
		callers    int
		callees    int
		maxNodes   int
		maxEdges   int
		maxFlows   int
		timeout    time.Duration
		unchanged  bool
		withSource bool
		open       bool
		visible    int
		entry      flowStringList
		exclude    flowStringList
		buildTags  flowStringList
	)
	fs := flag.NewFlagSet("flow", flag.ContinueOnError)
	fs.StringVar(&repo, "repo", ".", "repository root")
	fs.StringVar(&configPath, "config", "", "path to .nitpick.yaml (default: <repo>/.nitpick.yaml)")
	fs.StringVar(&base, "base", "", "base revision (default: HEAD for the working tree)")
	fs.StringVar(&head, "head", "", "head revision (default: the working tree)")
	fs.StringVar(&mode, "mode", "", "flow mode override: auto or on (off is an error)")
	fs.StringVar(&format, "format", "markdown", "output format: markdown, json, or html")
	fs.StringVar(&output, "output", "-", "output path, or - for stdout")
	fs.IntVar(&maxFiles, "max-files", 0, "maximum Go files to inspect (default: config)")
	fs.IntVar(&maxBytes, "max-bytes", 0, "maximum source bytes to inspect (default: config)")
	fs.IntVar(&callers, "max-depth-callers", 0, "maximum caller traversal depth (default: config)")
	fs.IntVar(&callees, "max-depth-callees", 0, "maximum callee traversal depth (default: config)")
	fs.IntVar(&maxNodes, "max-nodes", 0, "maximum nodes (default: config)")
	fs.IntVar(&maxEdges, "max-edges", 0, "maximum edges (default: config)")
	fs.IntVar(&maxFlows, "max-flows", 0, "maximum connected flows (default: config)")
	fs.DurationVar(&timeout, "timeout", 0, "analysis timeout (default: config)")
	fs.BoolVar(&unchanged, "include-unchanged", false, "include unchanged context around changed declarations")
	fs.BoolVar(&withSource, "source", false, "embed source snippets in HTML output (the document then contains repository source)")
	fs.BoolVar(&open, "open", false, "open HTML output in the default browser")
	fs.IntVar(&visible, "max-visible", 0, "maximum nodes one HTML diagram draws before it shows the change and its immediate neighbours (default: 24)")
	fs.Var(&entry, "entry", "entrypoint package.Func or path/to/file.go:line (repeatable)")
	fs.Var(&exclude, "exclude", "path glob to omit (repeatable)")
	fs.Var(&buildTags, "build-tag", "additional Go build tag (repeatable)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: nitpick flow [flags]\n\nExtracts source-linked application flows from changed Go files. The result is static source relationships, not a runtime trace or proof of complete coverage. It never publishes or calls a model.\n\nWith -format html it writes a self-contained browsable document: a call graph you can click through, with callers, callees, and source for each declaration.\n\nFlags:")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if format != "markdown" && format != "json" && format != "html" {
		return fmt.Errorf("invalid -format %q (want markdown, json, or html)", format)
	}
	if withSource && format != "html" {
		return errors.New("-source applies to -format html")
	}
	if open && format != "html" {
		return errors.New("-open applies to -format html")
	}
	if visible < 0 {
		return errors.New("-max-visible must not be negative")
	}
	if visible != 0 && format != "html" {
		return errors.New("-max-visible applies to -format html")
	}
	if open && (output == "" || output == "-") {
		return errors.New("-open needs -output naming a file")
	}
	if mode != "" && mode != string(config.FlowAuto) && mode != string(config.FlowOn) {
		return fmt.Errorf("invalid -mode %q (want auto or on; off is an error)", mode)
	}
	root, err := filepath.Abs(repo)
	if err != nil {
		return fmt.Errorf("resolve repo path: %w", err)
	}
	cfg, err := loadFlowConfig(root, configPath)
	if err != nil {
		return err
	}
	selectedMode := cfg.Flow.Mode
	if mode != "" {
		selectedMode = config.FlowMode(mode)
	}
	if selectedMode == "" {
		selectedMode = config.FlowOff
	}
	if selectedMode == config.FlowOff {
		return errors.New("flow mode is off; pass -mode auto or -mode on, or set flow.mode in .nitpick.yaml")
	}

	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "max-files":
			cfg.Flow.MaxFiles = maxFiles
		case "max-bytes":
			cfg.Flow.MaxBytes = maxBytes
		case "max-depth-callers":
			cfg.Flow.MaxDepthCallers = callers
		case "max-depth-callees":
			cfg.Flow.MaxDepthCallees = callees
		case "max-nodes":
			cfg.Flow.MaxNodes = maxNodes
		case "max-edges":
			cfg.Flow.MaxEdges = maxEdges
		case "max-flows":
			cfg.Flow.MaxFlows = maxFlows
		case "timeout":
			cfg.Flow.Timeout = timeout
		case "include-unchanged":
			cfg.Flow.IncludeUnchanged = unchanged
		}
	})
	if len(entry) > 0 {
		cfg.Flow.Entrypoints = append([]string(nil), entry...)
	}
	if len(exclude) > 0 {
		cfg.Flow.Exclude = append([]string(nil), exclude...)
	}
	if len(buildTags) > 0 {
		cfg.Flow.BuildTags = append([]string(nil), buildTags...)
	}
	if err := cfg.Flow.Validate(); err != nil {
		return err
	}

	worker, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate flow worker: %w", err)
	}
	result, markdown, err := analyzeFlow(ctx, root, base, head, cfg, selectedMode, worker)
	if err != nil {
		return err
	}
	var data []byte
	switch format {
	case "json":
		data, err = json.Marshal(result)
		if err == nil {
			data = append(data, '\n')
		}
	case "html":
		data, err = flowDocument(ctx, root, result, withSource, visible)
	default:
		data = []byte(markdown)
	}
	if err != nil {
		return err
	}
	if err := writeFlowOutput(output, data); err != nil {
		return err
	}
	if open {
		return openInBrowser(output)
	}
	return nil
}

func loadFlowConfig(repo, explicit string) (*config.Config, error) {
	if explicit != "" {
		if _, err := os.Stat(explicit); err != nil {
			return nil, fmt.Errorf("read config: %w", err)
		}
		return config.LoadFlowFile(explicit)
	}
	return config.LoadFlowFile(filepath.Join(repo, config.FileName))
}

// flowSource adapts the local VCS snapshot reader to prflow's immutable source
// interface. A blank SHA means the current working tree, matching vcs.Worktree.
type flowSource struct{ local *vcs.Local }

func (s flowSource) ReadFile(ctx context.Context, rev prflow.Revision, name string) ([]byte, error) {
	ref := vcs.Ref{Head: rev.SHA}
	if rev.SHA == "" {
		ref.Head = vcs.Worktree
	}
	return s.local.FileContent(ctx, ref, name)
}

func (s flowSource) ReadFileLimit(ctx context.Context, rev prflow.Revision, name string, limit int) ([]byte, error) {
	ref := vcs.Ref{Head: rev.SHA}
	if rev.SHA == "" {
		ref.Head = vcs.Worktree
	}
	data, err := s.local.FileContentLimit(ctx, ref, name, limit)
	if errors.Is(err, vcs.ErrSourceLimit) {
		return nil, prflow.ErrSourceLimit
	}
	return data, err
}

func (s flowSource) ListDir(ctx context.Context, rev prflow.Revision, dir string) ([]string, error) {
	ref := vcs.Ref{Head: rev.SHA}
	if rev.SHA == "" {
		ref.Head = vcs.Worktree
	}
	return s.local.ListDir(ctx, ref, dir)
}

func analyzeFlow(ctx context.Context, root, base, head string, policy *config.Config, mode config.FlowMode, worker string) (prflow.Result, string, error) {
	cfg := policy.Flow
	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()
	local := vcs.NewLocal(root, io.Discard)
	ref := vcs.Ref{Base: base, Head: head}
	raw, err := local.Diff(ctx, ref)
	if err != nil {
		return prflow.Result{}, "", fmt.Errorf("read diff: %w", err)
	}
	changes, err := diff.Parse(raw)
	if err != nil {
		return prflow.Result{}, "", fmt.Errorf("parse diff: %w", err)
	}

	// auto mode is intentionally quiet about non-Go changes, but returning a
	// structured skipped result keeps an empty scan distinguishable from no_flow.
	goChanges := make(diff.Files, 0, len(changes))
	for _, c := range changes {
		p := c.Path
		if p == "" {
			p = c.OldPath
		}
		if !strings.HasSuffix(strings.ToLower(p), ".go") || policy.Ignored(p) || excludedFlowPath(p, cfg.Exclude) {
			continue
		}
		goChanges = append(goChanges, c)
	}
	if mode == config.FlowAuto && len(goChanges) == 0 {
		result := prflow.Result{Version: 1, Status: prflow.StatusSkipped, Explanation: "no eligible changed Go files"}
		return result, flowMarkdown(result), nil
	}

	headSHA := head
	if headSHA != "" {
		if pr, err := local.PullRequest(ctx, ref); err == nil {
			headSHA = pr.HeadSHA
		}
	}
	baseSHA, _ := local.BaseRevision(ctx, ref)
	opts := prflow.Options{
		MaxFiles:         cfg.MaxFiles,
		MaxBytes:         cfg.MaxBytes,
		MaxCallerDepth:   cfg.MaxDepthCallers,
		DepthLimitsSet:   true,
		MaxCalleeDepth:   cfg.MaxDepthCallees,
		MaxNodes:         cfg.MaxNodes,
		MaxEdges:         cfg.MaxEdges,
		MaxFlows:         cfg.MaxFlows,
		Timeout:          cfg.Timeout,
		IncludeUnchanged: cfg.IncludeUnchanged,
		SkipGenerated:    policy.Review.SkipGenerated,
		Entrypoints:      append([]string(nil), cfg.Entrypoints...),
		BuildTags:        append([]string(nil), cfg.BuildTags...),
		Exclude:          append(append([]string(nil), cfg.Exclude...), policy.Review.Ignore...),
	}
	analyze := prflow.Analyze
	if worker != "" {
		analyze = func(ctx context.Context, request prflow.Request) (prflow.Result, error) {
			return prflow.AnalyzeIsolated(ctx, request, worker)
		}
	}
	result, err := analyze(ctx, prflow.Request{
		Base:    prflow.Revision{SHA: baseSHA},
		Head:    prflow.Revision{SHA: headSHA},
		Changes: goChanges,
		Source:  flowSource{local: local},
		Options: opts,
	})
	if err != nil {
		return prflow.Result{}, "", err
	}
	if len(cfg.Entrypoints) > 0 && result.Status == prflow.StatusUnavailable {
		return result, "", fmt.Errorf("flow entrypoint unavailable: %s", result.Explanation)
	}
	return result, flowMarkdown(result), nil
}

func excludedFlowPath(name string, patterns []string) bool {
	for _, p := range patterns {
		if ok, err := doublestar.Match(p, name); err == nil && ok {
			return true
		}
	}
	return false
}

func flowMarkdown(r prflow.Result) string {
	markdown, err := r.Markdown("")
	if err == nil {
		return markdown
	}
	return fmt.Sprintf("## Application flows (static analysis)\n\nStatus: **%s** — flow output could not be rendered safely.\n", r.Status)
}

// flowDocument renders the browsable HTML view. Snippets are embedded only on
// request: the flow result itself is links-only, so a document that carries
// repository source is an explicit choice and says so in its header.
func flowDocument(ctx context.Context, root string, result prflow.Result, withSource bool, visible int) ([]byte, error) {
	title := "Application flows"
	if name := filepath.Base(root); name != "" && name != "." && name != string(filepath.Separator) {
		title += " — " + name
	}
	var reader flowhtml.SourceReader
	if withSource {
		reader = flowSource{local: vcs.NewLocal(root, io.Discard)}
	}
	data, err := flowhtml.Render(ctx, result, reader, flowhtml.Options{
		Title:           title,
		IncludeSource:   withSource,
		MaxVisibleNodes: visible,
	})
	if err != nil {
		return nil, fmt.Errorf("render flow document: %w", err)
	}
	return data, nil
}

// openInBrowser hands the written file to the desktop opener. A missing opener
// is reported rather than ignored, because the caller asked for it explicitly.
func openInBrowser(name string) error {
	absolute, err := filepath.Abs(name)
	if err != nil {
		return fmt.Errorf("resolve output path: %w", err)
	}
	opener, args := browserOpener(absolute)
	if opener == "" {
		return fmt.Errorf("no browser opener on this platform; open %s manually", absolute)
	}
	path, err := exec.LookPath(opener)
	if err != nil {
		return fmt.Errorf("locate %s: %w", opener, err)
	}
	cmd := exec.Command(path, args...)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("open %s: %w", absolute, err)
	}
	// The browser outlives this process; reaping it here would close the tab.
	return cmd.Process.Release()
}

func browserOpener(target string) (string, []string) {
	switch runtime.GOOS {
	case "darwin":
		return "open", []string{target}
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler", target}
	case "linux", "freebsd", "openbsd", "netbsd":
		return "xdg-open", []string{target}
	}
	return "", nil
}

func writeFlowOutput(name string, data []byte) error {
	if name == "" || name == "-" {
		_, err := os.Stdout.Write(data)
		return err
	}
	dir := filepath.Dir(name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".nitpick-flow-*")
	if err != nil {
		return fmt.Errorf("create output: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}()
	if err := tmp.Chmod(0o644); err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, name); err != nil {
		return fmt.Errorf("replace output: %w", err)
	}
	return nil
}
