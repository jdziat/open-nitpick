package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/linters"
	"github.com/jdziat/open-nitpick/internal/standards"
)

// RepoStandardsResult separates observed conventions from proposed enforcement.
type RepoStandardsResult struct {
	Report          standards.Report         `json:"report"`
	Recommendations []string                 `json:"recommendations"`
	Analyzers       []linters.CatalogEntry   `json:"applicable_analyzers"`
	Sources         []standards.SourceResult `json:"sources"`
}

func runRepoStandards(ctx context.Context, args []string, out io.Writer) error {
	return repoStandardsCommand(ctx, args, out, func(cfg *config.Config) standards.Source {
		return linters.NewStandardsSource(cfg, nil)
	})
}

func repoStandardsCommand(ctx context.Context, args []string, out io.Writer, source func(*config.Config) standards.Source) error {
	var repo, configPath, enabled string
	var asJSON, check, noLinters bool
	fs := flag.NewFlagSet("repo-standards", flag.ContinueOnError)
	fs.StringVar(&repo, "repo", ".", "repository root")
	fs.StringVar(&configPath, "config", "", "configuration file for the standards evidence thresholds")
	fs.StringVar(&enabled, "linters", "", "comma-separated analyzers to run (default: applicable default analyzers)")
	fs.BoolVar(&asJSON, "json", false, "print the evidence and recommendations as JSON")
	fs.BoolVar(&check, "check", false, "exit 1 for violations, 2 for checks that could not run")
	fs.BoolVar(&noLinters, "no-linters", false, "measure conventions without running analyzers")
	fs.Usage = func() {
		_, _ = fmt.Fprintln(fs.Output(), "Usage: nitpick repo-standards [flags]\n\nMeasures current conventions, recommends standards with evidence, and runs\nisolated linters across the repository. No model or credentials are required.\nNo source or configuration files are changed.\n\nFlags:")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("repo-standards accepts no positional arguments; use -repo")
	}
	if noLinters && enabled != "" {
		return errors.New("-no-linters and -linters cannot be combined")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	root, err := filepath.Abs(repo)
	if err != nil {
		return err
	}
	cfg, err := loadStandardsConfig(root, configPath)
	if err != nil {
		return err
	}
	opts := standards.Options{Floor: standards.Floor{MinShare: cfg.MinShare, MinSites: cfg.MinSites}, Disabled: cfg.Disabled}
	if err := checkDisabled(opts.Disabled); err != nil {
		return err
	}
	files, err := standards.ReadTreeContext(ctx, root, skipStandardsDir)
	if err != nil {
		return fmt.Errorf("read repository: %w", err)
	}
	paths := make([]string, 0, len(files))
	for _, f := range files {
		paths = append(paths, f.Path)
	}
	report, err := standards.MeasureContext(ctx, files, opts)
	if err != nil {
		return err
	}
	result := RepoStandardsResult{Report: report, Analyzers: linters.Suggest(paths)}
	result.Recommendations = repoStandardsRecommendations(result.Report)
	if !noLinters {
		lintCfg := config.Defaults()
		lintCfg.Linters.Enabled = nil
		lintCfg.Linters.AutoDetect = new(bool)
		lintCfg.Linters.OnlyChangedLines = false
		lintCfg.Linters.Mode = config.LinterStrict
		if enabled != "" {
			known := append(linters.Builtins(), linters.Catalog()...)
			for _, name := range strings.Split(enabled, ",") {
				name = strings.TrimSpace(name)
				if !slices.ContainsFunc(known, func(e linters.CatalogEntry) bool { return e.Name == name }) {
					return fmt.Errorf("unknown analyzer %q; run nitpick linters", name)
				}
				if !slices.Contains(lintCfg.Linters.Enabled, name) {
					lintCfg.Linters.Enabled = append(lintCfg.Linters.Enabled, name)
				}
			}
		} else {
			for _, e := range result.Analyzers {
				if e.Default {
					lintCfg.Linters.Enabled = append(lintCfg.Linters.Enabled, e.Name)
				}
			}
		}
		result.Sources = standards.Gather(ctx, root, files, []standards.Source{source(lintCfg)})
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(result); err != nil {
			return err
		}
	} else if _, err := io.WriteString(out, result.text()); err != nil {
		return err
	}
	if check {
		return result.check(noLinters)
	}
	return nil
}

func repoStandardsRecommendations(report standards.Report) []string {
	var recommendations []string
	for _, r := range report.Results {
		if r.Total == 0 {
			continue
		}
		if r.Standing == standards.StandingStandard {
			recommendations = append(recommendations, fmt.Sprintf("Enforce %s: %s (%d/%d sites; %d exceptions).", r.ID, r.Rule, r.Conforming, r.Total, len(r.Off)))
		} else {
			recommendations = append(recommendations, fmt.Sprintf("Consider %s: %s (%d/%d sites; below the evidence threshold, not an established convention). %s", r.ID, r.Rule, r.Conforming, r.Total, r.Why))
		}
	}
	return recommendations
}

func (r RepoStandardsResult) check(noLinters bool) error {
	sites := 0
	violates := false
	for _, res := range r.Report.Results {
		sites += res.Total
		if res.Standing == standards.StandingStandard && len(res.Off) > 0 {
			violates = true
		}
	}
	ran := false
	for _, s := range r.Sources {
		if !s.Coverage.Ran || s.Coverage.Why != "" || len(s.Coverage.Files) == 0 {
			return errIncomplete
		}
		ran = true
		if len(s.Observations) > 0 {
			violates = true
		}
	}
	if !noLinters && !ran || sites == 0 && !ran {
		return errIncomplete
	}
	if violates {
		return errFindings
	}
	return nil
}

func (r RepoStandardsResult) text() string {
	var b strings.Builder
	b.WriteString(r.Report.Text())
	b.WriteString("\nStandards to apply:\n")
	for _, recommendation := range r.Recommendations {
		fmt.Fprintf(&b, "- %s\n", recommendation)
	}
	if len(r.Recommendations) == 0 {
		b.WriteString("No convention has enough observed sites for a recommendation.\n")
	}
	for _, res := range r.Report.Standards() {
		for i, site := range res.Off {
			if i == 8 {
				fmt.Fprintf(&b, "  %s: %d more exceptions in -json output\n", res.ID, len(res.Off)-i)
				break
			}
			fmt.Fprintf(&b, "  %s:%d [%s] %s\n", site.Path, site.Line, res.ID, site.Excerpt)
		}
	}
	b.WriteString("\nApplicable analyzers (use -linters to select):\n")
	for _, e := range r.Analyzers {
		fmt.Fprintf(&b, "- %s: %s; %s\n", e.Name, e.Languages, e.Configuration)
	}
	if len(r.Analyzers) == 0 {
		b.WriteString("No supported analyzer matches this repository.\n")
	}
	b.WriteString("\nAnalyzer observations:\n")
	if len(r.Sources) == 0 {
		b.WriteString("Not run (-no-linters).\n")
	}
	for _, s := range r.Sources {
		fmt.Fprintf(&b, "%s: ran=%t, %d files, %d observations\n", s.Source, s.Coverage.Ran, len(s.Coverage.Files), len(s.Observations))
		if s.Coverage.Why != "" {
			fmt.Fprintf(&b, "%s\n", s.Coverage.Why)
		}
		for i, obs := range s.Observations {
			if i == 20 {
				fmt.Fprintf(&b, "%d more observations in -json output\n", len(s.Observations)-i)
				break
			}
			fmt.Fprintf(&b, "  %s:%d [%s]\n", obs.Path, obs.Line, obs.Rule)
		}
	}
	b.WriteString("\nUse nitpick repo-standards -check in CI to enforce established conventions and linter checks.\n")
	return b.String()
}
