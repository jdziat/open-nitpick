package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jdziat/open-nitpick/v2/internal/config"
	"github.com/jdziat/open-nitpick/v2/internal/fullreview"
	"github.com/jdziat/open-nitpick/v2/internal/review"
	"github.com/jdziat/open-nitpick/v2/internal/slop"
	"github.com/jdziat/open-nitpick/v2/internal/vcs"
)

// runSlop scores a tree for AI slop and says how to fix it. Two instruments:
// the tells, which need no model (em dashes, filler, chat prose, comments
// that restate their line, doc comments longer than what they document),
// and the model's nine slop rules through the review engine, filtered to
// the slop class. The score is each per thousand lines.
func runSlop(ctx context.Context, args []string) error {
	var (
		f        reviewFlags
		budget   int
		noModel  bool
		asJSON   bool
		failOver int
	)
	fs := flag.NewFlagSet("slop", flag.ContinueOnError)
	fs.StringVar(&f.repo, "repo", ".", "repository root")
	fs.StringVar(&f.configPath, "config", "", "path to .nitpick.yaml (default: <repo>/.nitpick.yaml)")
	fs.StringVar(&f.instruction, "instruction", "", "extra instruction for the model's pass")
	fs.IntVar(&budget, "budget", 0, "stop the model's pass after this many estimated tokens of source (0: everything)")
	fs.BoolVar(&noModel, "no-model", false, "the tells only: no model call, no credentials")
	fs.BoolVar(&asJSON, "json", false, "print the result as JSON")
	fs.IntVar(&failOver, "fail-over", -1, "exit 1 when the tells per thousand lines exceed this (default: never)")
	fs.BoolVar(&f.noLinters, "no-linters", false, "skip analyzers in the model's pass")
	fs.BoolVar(&f.verbose, "v", false, "verbose logging")
	fs.StringVar(&f.logFormat, "log-format", "text", "log format: text or json")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: nitpick slop [flags] [path...]\n\nScores the working tree, or the paths given, for AI slop and says how to fix it:\nthe tells (em dashes, filler, chat prose, restating and oversized comments) without a model,\nand the model's nine slop rules through the review engine unless -no-model.\n\nFlags:")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	log := newLogger(f.verbose, f.logFormat)

	result, err := slopScore(ctx, &f, fs.Args(), budget, noModel, log)
	if err != nil {
		return err
	}
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(result); err != nil {
			return err
		}
	} else {
		fmt.Print(result.Text())
	}
	if failOver >= 0 && result.TellsPerKLOC > float64(failOver) {
		return errFindings
	}
	return nil
}

// SlopResult is what nitpick slop and the MCP ai_slop tool return.
type SlopResult struct {
	Files        int              `json:"files"`
	Lines        int              `json:"lines"`
	Tells        []slop.Tell      `json:"tells"`
	TellsByRule  []slop.RuleCount `json:"tells_by_rule"`
	TellsPerKLOC float64          `json:"tells_per_kloc"`
	// Findings are the model's slop findings; nil when -no-model. Hidden
	// are the findings the same review made outside the slop class: paid
	// for, so carried rather than dropped.
	Findings         []Finding `json:"findings,omitempty"`
	Hidden           []Finding `json:"hidden,omitempty" jsonschema:"findings outside the slop class the same review made"`
	ModelSlopPerKLOC float64   `json:"model_slop_per_kloc,omitempty" jsonschema:"weighted by severity, per thousand lines reviewed"`
	ModelRan         bool      `json:"model_ran"`
	Unreviewed       []string  `json:"unreviewed,omitempty"`
	Skipped          []string  `json:"skipped,omitempty"`
	Recommendations  []string  `json:"recommendations"`
	ScoreNote        string    `json:"score_note,omitempty"`
}

// slopScore runs both instruments over the tree.
func slopScore(ctx context.Context, f *reviewFlags, paths []string, budget int, noModel bool, log *slog.Logger) (*SlopResult, error) {
	repo, err := filepath.Abs(f.repo)
	if err != nil {
		return nil, err
	}
	cfg, err := loadConfig(repo, f.configPath)
	if err != nil {
		return nil, err
	}
	out := &SlopResult{}

	var tree *vcs.Tree
	var report *review.Report
	if noModel {
		tree = vcs.NewTree(vcs.NewLocal(repo, io.Discard), paths)
		tree.MaxBytes = cfg.Review.MaxFileBytes
		if _, err := tree.Diff(ctx, vcs.Ref{Head: vcs.Worktree}); err != nil {
			return nil, err
		}
	} else {
		report, tree, err = treeReview(ctx, f, paths, budget, log, io.Discard)
		if err != nil {
			return nil, err
		}
	}

	// The tells, over every covered file the configuration does not
	// ignore, read from disk: the tree read them once for the diff and
	// keeps only their line counts.
	for _, p := range tree.Covered {
		if cfg.Ignored(p) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(p)))
		if err != nil {
			// The tree read it moments ago; a file that cannot be read
			// now is recorded as left out, not dropped from the score.
			out.Skipped = append(out.Skipped, p+": unreadable for the tells: "+err.Error())
			continue
		}
		out.Files++
		out.Lines += tree.Lines[p]
		out.Tells = append(out.Tells, slop.Scan(p, string(data))...)
	}
	out.TellsByRule = slop.Summary(out.Tells)
	out.TellsPerKLOC = perKLOC(float64(len(out.Tells)), out.Lines)
	for _, s := range tree.Skipped {
		out.Skipped = append(out.Skipped, s.Path+": "+s.Reason)
	}

	if report != nil {
		out.ModelRan = true
		for _, fd := range report.Findings {
			conv := Finding{Path: fd.Path, Line: fd.Line, Severity: fd.Severity, Class: fd.Class, Title: fd.Title, Rationale: fd.Rationale, Suggestion: fd.Suggestion, Source: analyzerSource(fd)}
			if fd.Class == string(config.ClassSlop) {
				out.Findings = append(out.Findings, conv)
			} else {
				out.Hidden = append(out.Hidden, conv)
			}
		}
		card := fullreview.Score(report, tree)
		out.ModelSlopPerKLOC = card.Total.PerKLOC(card.Total.SlopWeighted)
		out.Unreviewed = card.Unreviewed
		if card.Incomplete() {
			var parts []string
			if len(card.Unreviewed) > 0 {
				parts = append(parts, fmt.Sprintf("%d file(s) whose batch failed are in no denominator", len(card.Unreviewed)))
			}
			for _, a := range card.AnalyzersFailed {
				parts = append(parts, "analyzer did not run: "+a)
			}
			out.ScoreNote = "INCOMPLETE: " + strings.Join(parts, "; ")
		}
	}
	out.Recommendations = recommendations(out)
	return out, nil
}

func perKLOC(n float64, lines int) float64 {
	if lines == 0 {
		return 0
	}
	return n / float64(lines) * 1000
}

// recommendations orders the fixes by how much each would remove, so a
// reader with an hour spends it where the count is.
func recommendations(r *SlopResult) []string {
	var out []string
	counts := append([]slop.RuleCount(nil), r.TellsByRule...)
	sort.SliceStable(counts, func(i, j int) bool { return counts[i].Count > counts[j].Count })
	for _, c := range counts {
		out = append(out, fmt.Sprintf("%s (%d): %s. Fix: %s.", c.Rule.Name, c.Count, c.Rule.What, c.Rule.Fix))
	}
	if r.ModelRan {
		byTitle := map[string]int{}
		for _, fd := range r.Findings {
			byTitle[fd.Title]++
		}
		type kv struct {
			t string
			n int
		}
		var kvs []kv
		for t, n := range byTitle {
			kvs = append(kvs, kv{t, n})
		}
		sort.Slice(kvs, func(i, j int) bool {
			if kvs[i].n != kvs[j].n {
				return kvs[i].n > kvs[j].n
			}
			return kvs[i].t < kvs[j].t
		})
		for i, k := range kvs {
			if i == 5 {
				break
			}
			out = append(out, fmt.Sprintf("model (%d): %s", k.n, k.t))
		}
	}
	return out
}

// Text renders the result for a terminal.
func (r *SlopResult) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Slop over %d file(s), %d line(s).\n", r.Files, r.Lines)
	fmt.Fprintf(&b, "\nTells (no model): %d, %.1f per thousand lines.\n", len(r.Tells), r.TellsPerKLOC)
	for _, c := range r.TellsByRule {
		fmt.Fprintf(&b, "  %-22s %4d  %s\n", c.Rule.Name, c.Count, c.Rule.What)
	}
	const perRule = 12
	shown := map[string]int{}
	for _, t := range r.Tells {
		if shown[t.Rule] == perRule {
			fmt.Fprintf(&b, "  ... more %s; -json lists every one\n", t.Rule)
		}
		shown[t.Rule]++
		if shown[t.Rule] > perRule {
			continue
		}
		fmt.Fprintf(&b, "  %s:%d  [%s]  %s\n", t.Path, t.Line, t.Rule, t.Excerpt)
	}
	if r.ModelRan {
		fmt.Fprintf(&b, "\nModel slop findings: %d, %.2f weighted per thousand lines.\n", len(r.Findings), r.ModelSlopPerKLOC)
		for _, fd := range r.Findings {
			fmt.Fprintf(&b, "  [%s] %s:%d  %s\n    %s\n", fd.Severity, fd.Path, fd.Line, fd.Title, strings.TrimSpace(fd.Rationale))
			if fd.Suggestion != "" {
				fmt.Fprintf(&b, "    suggestion:\n      %s\n", strings.ReplaceAll(strings.TrimRight(fd.Suggestion, "\n"), "\n", "\n      "))
			}
		}
		if r.ScoreNote != "" {
			fmt.Fprintf(&b, "  %s\n", r.ScoreNote)
		}
		if len(r.Hidden) > 0 {
			fmt.Fprintf(&b, "\n%d finding(s) outside the slop class from the same review:\n", len(r.Hidden))
			for _, fd := range r.Hidden {
				fmt.Fprintf(&b, "  [%s/%s] %s:%d  %s\n", fd.Severity, fd.Class, fd.Path, fd.Line, fd.Title)
			}
		}
	} else {
		b.WriteString("\nModel pass not run (-no-model); the nine slop rules need it.\n")
	}
	if len(r.Skipped) > 0 {
		fmt.Fprintf(&b, "\nLeft out (%d): %s\n", len(r.Skipped), strings.Join(r.Skipped, "; "))
	}
	b.WriteString("\nRecommendations, most to fix first:\n")
	if len(r.Recommendations) == 0 {
		b.WriteString("  nothing to fix.\n")
	}
	for i, rec := range r.Recommendations {
		fmt.Fprintf(&b, "  %d. %s\n", i+1, rec)
	}
	return b.String()
}
