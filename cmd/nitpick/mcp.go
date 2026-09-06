package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jdziat/open-nitpick/v2/internal/config"
	"github.com/jdziat/open-nitpick/v2/internal/fullreview"
	"github.com/jdziat/open-nitpick/v2/internal/review"
	"github.com/jdziat/open-nitpick/v2/internal/vcs"
)

// runMCP serves the review engine to an agent session over the Model
// Context Protocol on stdin and stdout. Every tool is a thin call into the
// same functions the commands use, so a review from a session and a review
// from the command line are the same review; what differs is the shape of
// the answer, which here is structured for a caller that will act on it.
//
// stdout is the protocol, so nothing else may write to it: the provider's
// review text goes to io.Discard, and the log to stderr.
func runMCP(ctx context.Context, args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "install":
			return runMCPInstall(args[1:], os.Stdout)
		case "clients":
			for _, c := range mcpClients {
				fmt.Printf("%-15s %s\n", c.name, c.note)
			}
			return nil
		}
	}
	var (
		repo    string
		verbose bool
	)
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	fs.StringVar(&repo, "repo", ".", "default repository root for tools that do not name one")
	fs.BoolVar(&verbose, "v", false, "verbose logging on stderr")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: nitpick mcp [flags]\n       nitpick mcp install <client> [-user] [-print]\n       nitpick mcp clients\n\nServes the review tools over the Model Context Protocol on stdio, for an agent session.\nTools: review, full_review, repo_score, code_smell, ai_slop, explain_config.\ninstall writes the server into a client's configuration (claude-code, claude-desktop, cursor, windsurf, vscode, opencode, gemini-cli, codex).\n\nFlags:")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	root, err := filepath.Abs(repo)
	if err != nil {
		return err
	}
	server := newMCPServer(root, newLogger(verbose, "text"))
	return server.Run(ctx, &mcp.StdioTransport{})
}

// mcpTools holds what every tool needs: the default repository and a log.
type mcpTools struct {
	root string
	log  *slog.Logger
}

// newMCPServer builds the server with its tools registered.
func newMCPServer(root string, log *slog.Logger) *mcp.Server {
	t := &mcpTools{root: root, log: log}
	server := mcp.NewServer(&mcp.Implementation{Name: "open-nitpick", Version: version}, nil)
	mcp.AddTool(server, &mcp.Tool{
		Name: "review",
		Description: "Review a change in a local repository with the configured model: the uncommitted working tree by default, or base..head. " +
			"Returns the walkthrough, every finding with path, line, severity, class and rationale, the analyzer roster, and whether the configured gate failed. " +
			"Costs one model review of the change. Nothing is published; the caller decides what to do with the findings.",
	}, t.review)
	mcp.AddTool(server, &mcp.Tool{
		Name: "full_review",
		Description: "Review a whole repository, or the paths given, as if every file were new: bugs, security risks, known advisories from the dependency scanner, and AI slop, " +
			"with a remediation plan ordered most severe first and a coverage notice. The default is the whole tree with the model on every batch, which is the expensive lift; " +
			"pass paths or a token budget for a first look. Optionally filter the returned findings to given classes.",
	}, t.fullReview)
	mcp.AddTool(server, &mcp.Tool{
		Name: "repo_score",
		Description: "The full review plus a scorecard: slop, bug and security findings per thousand lines, by language, weighted by severity. " +
			"For comparing a repository with itself over time, or two repositories; a single number is not a judgement.",
	}, t.repoScore)
	mcp.AddTool(server, &mcp.Tool{
		Name: "code_smell",
		Description: "Maintainability, style and slop findings for the paths given (or the whole tree): what would cost the next reader, with the remediation plan. " +
			"A full review filtered to those classes; pass paths to keep it cheap.",
	}, t.codeSmell)
	mcp.AddTool(server, &mcp.Tool{
		Name: "ai_slop",
		Description: "AI slop for the paths given (or the whole tree), with a score and fixes. Two instruments: the tells, which need no model (em dashes, en dashes as separators, arrows in prose, filler qualifiers, chat prose, comments that restate their line, doc comments longer than what they document), each with its fix; " +
			"and, unless no_model is set, the model's nine slop rules through the review engine (a docstring that describes behaviour the code does not have, a swallowed error, a tautological guard, generic names, and so on). " +
			"Returns tells by rule, the model's findings with suggestions, both per thousand lines, findings outside the slop class the review made (hidden), and recommendations ordered by count. Pass paths to keep the model's pass cheap; no_model is free.",
	}, t.aiSlop)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "explain_config",
		Description: "The resolved open-nitpick configuration for a repository: config source, models per role, validation, gating, budget, analyzers, persona, and the instructions that apply to a given path.",
	}, t.explainConfig)
	return server
}

// Finding is one finding as the tools return it.
type Finding struct {
	Path       string `json:"path"`
	Line       int    `json:"line"`
	Severity   string `json:"severity" jsonschema:"nit, info, warning, error or critical"`
	Class      string `json:"class" jsonschema:"security, correctness, concurrency, resource, data-loss, contract, tests, maintainability, style or slop"`
	Category   string `json:"category,omitempty"`
	Title      string `json:"title"`
	Rationale  string `json:"rationale"`
	Suggestion string `json:"suggestion,omitempty" jsonschema:"replacement code for the anchored lines, when the model gave one"`
	Source     string `json:"source,omitempty" jsonschema:"the analyzer rule that reported it, when an analyzer did"`
}

// Analyzer is one line of the analyzer roster.
type Analyzer struct {
	Name    string `json:"name"`
	Outcome string `json:"outcome"`
	State   string `json:"state"`
}

// Withheld is a finding a domain expert or triage set aside, with why.
type Withheld struct {
	Path   string `json:"path"`
	Line   int    `json:"line"`
	Title  string `json:"title"`
	Expert string `json:"expert"`
	Reason string `json:"reason"`
}

// ReviewOut is what review returns.
type ReviewOut struct {
	Summary   string         `json:"summary" jsonschema:"the walkthrough the triage model wrote"`
	Findings  []Finding      `json:"findings"`
	Counts    map[string]int `json:"counts" jsonschema:"findings by severity"`
	Files     int            `json:"files" jsonschema:"files reviewed"`
	Analyzers []Analyzer     `json:"analyzers,omitempty"`
	Withheld  []Withheld     `json:"withheld,omitempty"`
	Policy    string         `json:"policy,omitempty" jsonschema:"set when the change's own configuration was set aside, and why"`
	FailOn    string         `json:"fail_on" jsonschema:"the configured gate"`
	Failed    bool           `json:"failed" jsonschema:"whether a finding reached the gate"`
}

// TreeOut is what the tree tools return: the review, the grouped
// sections, the plan, and what was not covered.
type TreeOut struct {
	ReviewOut
	Sections   string    `json:"sections" jsonschema:"known advisories, security risks, bugs and slop, as text"`
	Plan       string    `json:"plan" jsonschema:"the remediation plan, most severe first, findings that share a fix grouped"`
	Covered    []string  `json:"covered" jsonschema:"files a model answered for"`
	Unreviewed []string  `json:"unreviewed,omitempty" jsonschema:"files whose review batch failed; silence about them is not a clean file"`
	Unbudgeted []string  `json:"unbudgeted,omitempty" jsonschema:"files the budget stopped before"`
	Skipped    []string  `json:"skipped,omitempty" jsonschema:"files left out, with the reason"`
	Score      string    `json:"score,omitempty" jsonschema:"the scorecard, for repo_score"`
	Hidden     []Finding `json:"hidden,omitempty" jsonschema:"findings outside the class filter; paid for, so listed rather than lost"`
}

// ReviewIn selects the change.
type ReviewIn struct {
	Repo        string `json:"repo,omitempty" jsonschema:"repository root; the server's default when omitted"`
	Base        string `json:"base,omitempty" jsonschema:"base revision; omitted reviews the uncommitted working tree"`
	Head        string `json:"head,omitempty" jsonschema:"head revision; omitted is the working tree"`
	Instruction string `json:"instruction,omitempty" jsonschema:"an extra instruction for this review only"`
	FailOn      string `json:"fail_on,omitempty" jsonschema:"override review.fail_on: nit, info, warning, error, critical or none"`
	NoLinters   bool   `json:"no_linters,omitempty" jsonschema:"skip the deterministic analyzers"`
}

// TreeIn selects the tree.
type TreeIn struct {
	Repo        string   `json:"repo,omitempty" jsonschema:"repository root; the server's default when omitted"`
	Paths       []string `json:"paths,omitempty" jsonschema:"paths under the repository root to review; the whole tree when omitted"`
	Budget      int      `json:"budget,omitempty" jsonschema:"stop after this many estimated tokens of source; 0 is the whole tree, and the answer says what was left out"`
	Instruction string   `json:"instruction,omitempty" jsonschema:"an extra instruction for this review only"`
	Classes     []string `json:"classes,omitempty" jsonschema:"return only findings in these classes"`
	NoLinters   bool     `json:"no_linters,omitempty" jsonschema:"skip the deterministic analyzers"`
}

// ExplainIn selects the repository.
type ExplainIn struct {
	Repo       string `json:"repo,omitempty" jsonschema:"repository root; the server's default when omitted"`
	ConfigPath string `json:"config_path,omitempty" jsonschema:"a .nitpick.yaml elsewhere"`
	Path       string `json:"path,omitempty" jsonschema:"show the instructions that apply to this file path"`
}

// ExplainOut is the resolved configuration as text.
type ExplainOut struct {
	Text string `json:"text"`
}

func (t *mcpTools) repoFor(repo string) string {
	if repo == "" {
		return t.root
	}
	if filepath.IsAbs(repo) {
		return repo
	}
	return filepath.Join(t.root, repo)
}

func (t *mcpTools) review(ctx context.Context, _ *mcp.CallToolRequest, in ReviewIn) (*mcp.CallToolResult, ReviewOut, error) {
	f := &reviewFlags{repo: t.repoFor(in.Repo), base: in.Base, head: in.Head, instruction: in.Instruction, noLinters: in.NoLinters, failOn: in.FailOn}
	if f.failOn != "" && !config.Severity(f.failOn).Valid() {
		return nil, ReviewOut{}, fmt.Errorf("invalid fail_on %q", f.failOn)
	}
	repo, err := filepath.Abs(f.repo)
	if err != nil {
		return nil, ReviewOut{}, err
	}
	cfg, err := loadConfig(repo, "")
	if err != nil {
		return nil, ReviewOut{}, err
	}
	if f.failOn != "" {
		cfg.Review.FailOn = config.Severity(f.failOn)
	}
	provider := vcs.NewLocal(repo, io.Discard)
	ref := vcs.Ref{Base: f.base, Head: f.head}
	report, err := newEngine(f, repo, cfg, provider, t.log).Review(ctx, ref)
	if err != nil && (!errors.Is(err, review.ErrPublish) || report == nil) {
		return nil, ReviewOut{}, err
	}
	out := reviewOut(report, gate(report, f.failOn, cfg))
	return textResult(reviewText(out)), out, nil
}

func (t *mcpTools) fullReview(ctx context.Context, _ *mcp.CallToolRequest, in TreeIn) (*mcp.CallToolResult, TreeOut, error) {
	return t.tree(ctx, in, false)
}

func (t *mcpTools) repoScore(ctx context.Context, _ *mcp.CallToolRequest, in TreeIn) (*mcp.CallToolResult, TreeOut, error) {
	return t.tree(ctx, in, true)
}

func (t *mcpTools) codeSmell(ctx context.Context, _ *mcp.CallToolRequest, in TreeIn) (*mcp.CallToolResult, TreeOut, error) {
	in.Classes = []string{string(config.ClassMaintainability), string(config.ClassStyle), string(config.ClassSlop)}
	return t.tree(ctx, in, false)
}

// SlopIn selects the tree for ai_slop.
type SlopIn struct {
	Repo        string   `json:"repo,omitempty" jsonschema:"repository root; the server's default when omitted"`
	Paths       []string `json:"paths,omitempty" jsonschema:"paths under the repository root; the whole tree when omitted"`
	Budget      int      `json:"budget,omitempty" jsonschema:"stop the model's pass after this many estimated tokens of source"`
	Instruction string   `json:"instruction,omitempty"`
	NoModel     bool     `json:"no_model,omitempty" jsonschema:"the tells only: no model call, free and fast"`
	NoLinters   bool     `json:"no_linters,omitempty"`
}

func (t *mcpTools) aiSlop(ctx context.Context, _ *mcp.CallToolRequest, in SlopIn) (*mcp.CallToolResult, SlopResult, error) {
	f := &reviewFlags{repo: t.repoFor(in.Repo), instruction: in.Instruction, noLinters: in.NoLinters}
	res, err := slopScore(ctx, f, in.Paths, in.Budget, in.NoModel, t.log)
	if err != nil {
		return nil, SlopResult{}, err
	}
	return textResult(strings.TrimSpace(res.Text())), *res, nil
}

func (t *mcpTools) tree(ctx context.Context, in TreeIn, score bool) (*mcp.CallToolResult, TreeOut, error) {
	f := &reviewFlags{repo: t.repoFor(in.Repo), instruction: in.Instruction, noLinters: in.NoLinters}
	report, tree, err := treeReview(ctx, f, in.Paths, in.Budget, t.log, io.Discard)
	if err != nil {
		return nil, TreeOut{}, err
	}
	failOn := config.SeverityNone
	if report.Policy.Config != nil {
		failOn = report.Policy.Config.Review.FailOn
	}
	out := TreeOut{
		ReviewOut:  reviewOut(report, failOn),
		Sections:   strings.TrimSpace(fullreview.Sections(report)),
		Plan:       strings.TrimSpace(fullreview.RemediationPlan(report.Findings)),
		Unbudgeted: tree.Unbudgeted,
	}
	out.Covered, out.Unreviewed = fullreview.Reviewed(report, tree)
	for _, s := range tree.Skipped {
		out.Skipped = append(out.Skipped, s.Path+": "+s.Reason)
	}
	if score {
		out.Score = strings.TrimSpace(fullreview.Score(report, tree).String())
	}
	if len(in.Classes) > 0 {
		want := map[string]bool{}
		for _, c := range in.Classes {
			want[strings.ToLower(strings.TrimSpace(c))] = true
		}
		var kept, hidden []Finding
		for _, f := range out.Findings {
			if want[f.Class] {
				kept = append(kept, f)
			} else {
				hidden = append(hidden, f)
			}
		}
		out.Hidden = hidden
		// The sections, the plan and the counts follow the filter; the
		// walkthrough does not, since it was written over everything, so
		// the text says how many findings the filter kept.
		total := len(out.Findings)
		out.Findings = kept
		out.Counts = countsOf(kept)
		filtered := *report
		filtered.Findings = nil
		for _, f := range report.Findings {
			if want[f.Class] {
				filtered.Findings = append(filtered.Findings, f)
			}
		}
		out.Sections = strings.TrimSpace(fullreview.Sections(&filtered))
		out.Plan = strings.TrimSpace(fullreview.RemediationPlan(filtered.Findings))
		out.Failed = filtered.Failed(failOn)
		out.Summary = strings.TrimSpace(out.Summary) + fmt.Sprintf("\n\nFiltered to %s: %d of %d finding(s) shown.", strings.Join(in.Classes, ", "), len(kept), total)
	}
	text := reviewText(out.ReviewOut) + "\n\n" + out.Sections + "\n\n" + out.Plan + "\n" + strings.TrimSpace(fullreview.CoverageNotice(report, tree))
	if len(out.Hidden) > 0 {
		text += fmt.Sprintf("\n\n%d finding(s) outside the filter, in hidden:", len(out.Hidden))
		for _, f := range out.Hidden {
			text += fmt.Sprintf("\n  [%s/%s] %s:%d  %s", f.Severity, f.Class, f.Path, f.Line, f.Title)
		}
	}
	if out.Score != "" {
		text += "\n\n" + out.Score
	}
	return textResult(text), out, nil
}

func (t *mcpTools) explainConfig(_ context.Context, _ *mcp.CallToolRequest, in ExplainIn) (*mcp.CallToolResult, ExplainOut, error) {
	var b strings.Builder
	if err := explainConfig(&b, t.repoFor(in.Repo), in.ConfigPath, in.Path); err != nil {
		return nil, ExplainOut{}, err
	}
	return textResult(b.String()), ExplainOut{Text: b.String()}, nil
}

// reviewOut converts a report.
func reviewOut(report *review.Report, failOn config.Severity) ReviewOut {
	out := ReviewOut{Summary: report.Summary, Files: report.Plan.Files(), FailOn: string(failOn), Failed: report.Failed(failOn)}
	for _, f := range report.Findings {
		out.Findings = append(out.Findings, Finding{
			Path: f.Path, Line: f.Line, Severity: f.Severity, Class: f.Class, Category: f.Category,
			Title: f.Title, Rationale: f.Rationale, Suggestion: f.Suggestion, Source: analyzerSource(f),
		})
	}
	out.Counts = countsOf(out.Findings)
	for _, s := range report.Linters {
		out.Analyzers = append(out.Analyzers, Analyzer{Name: s.Linter, Outcome: string(s.Outcome), State: s.State})
	}
	for _, o := range report.Overruled {
		out.Withheld = append(out.Withheld, Withheld{Path: o.Finding.Path, Line: o.Finding.Line, Title: o.Finding.Title, Expert: o.Expert, Reason: o.Reason})
	}
	if report.Policy.Replaced {
		out.Policy = fmt.Sprintf("the change edits %s, so it was reviewed under %s", report.Policy.Modified, report.Policy.Source())
	}
	return out
}

func analyzerSource(f review.Finding) string {
	if f.FromAnalyzer {
		return f.Source
	}
	return ""
}

func countsOf(findings []Finding) map[string]int {
	counts := map[string]int{}
	for _, f := range findings {
		counts[f.Severity]++
	}
	return counts
}

// reviewText renders the structured answer for a client that reads text.
func reviewText(out ReviewOut) string {
	var b strings.Builder
	if out.Summary != "" {
		b.WriteString(strings.TrimSpace(out.Summary) + "\n\n")
	}
	fmt.Fprintf(&b, "%d file(s) reviewed, %d finding(s)", out.Files, len(out.Findings))
	if len(out.Counts) > 0 {
		var parts []string
		for _, sev := range []string{"critical", "error", "warning", "info", "nit"} {
			if n := out.Counts[sev]; n > 0 {
				parts = append(parts, fmt.Sprintf("%d %s", n, sev))
			}
		}
		b.WriteString(" (" + strings.Join(parts, ", ") + ")")
	}
	fmt.Fprintf(&b, "; gate %s %s.\n", out.FailOn, map[bool]string{true: "failed", false: "passed"}[out.Failed])
	findings := append([]Finding(nil), out.Findings...)
	sort.SliceStable(findings, func(i, j int) bool {
		ri, rj := config.Severity(findings[i].Severity).Rank(), config.Severity(findings[j].Severity).Rank()
		if ri != rj {
			return ri > rj
		}
		if findings[i].Path != findings[j].Path {
			return findings[i].Path < findings[j].Path
		}
		return findings[i].Line < findings[j].Line
	})
	for _, f := range findings {
		fmt.Fprintf(&b, "\n[%s/%s] %s:%d  %s\n  %s\n", f.Severity, f.Class, f.Path, f.Line, f.Title, strings.TrimSpace(f.Rationale))
		if f.Source != "" {
			fmt.Fprintf(&b, "  reported by %s\n", f.Source)
		}
		if f.Suggestion != "" {
			fmt.Fprintf(&b, "  suggestion:\n    %s\n", strings.ReplaceAll(strings.TrimRight(f.Suggestion, "\n"), "\n", "\n    "))
		}
	}
	if len(out.Analyzers) > 0 {
		b.WriteString("\nAnalyzers:")
		for _, a := range out.Analyzers {
			fmt.Fprintf(&b, "\n  %s %s: %s", a.Name, a.Outcome, a.State)
		}
		b.WriteString("\n")
	}
	if len(out.Withheld) > 0 {
		fmt.Fprintf(&b, "\n%d finding(s) withheld:", len(out.Withheld))
		for _, w := range out.Withheld {
			fmt.Fprintf(&b, "\n  %s:%d %s (%s: %s)", w.Path, w.Line, w.Title, w.Expert, w.Reason)
		}
		b.WriteString("\n")
	}
	if out.Policy != "" {
		b.WriteString("\n" + out.Policy + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}
