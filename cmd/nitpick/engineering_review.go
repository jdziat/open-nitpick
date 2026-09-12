package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/jdziat/open-nitpick/internal/bundle"
	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/diff"
	"github.com/jdziat/open-nitpick/internal/linters"
	"github.com/jdziat/open-nitpick/internal/practices"
	"github.com/jdziat/open-nitpick/internal/review"
	"github.com/jdziat/open-nitpick/internal/standards"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

type engineeringReviewPolicy struct {
	source   review.PolicyResolver
	loaded   *config.Config
	explicit bool
	selected func()
}

func (p *engineeringReviewPolicy) ResolvePolicy(ctx context.Context, ref vcs.Ref, pr *vcs.PullRequest, changed []string) (*config.Config, bool, error) {
	cfg, modified, err := p.source.ResolvePolicy(ctx, ref, pr, changed)
	if err != nil {
		return nil, false, err
	}
	if cfg == nil {
		cfg = p.loaded
	}
	if !p.explicit && cfg.Practices.Profile != "engineering" {
		if !modified {
			return nil, false, nil
		}
		return cfg, modified, nil
	}
	scoped := *cfg
	applyEngineeringScope(&scoped)
	if p.selected != nil {
		p.selected()
	}
	return &scoped, modified, nil
}

func assessReviewPractices(ctx context.Context, root string, cfg *config.Config, ref vcs.Ref, pr *vcs.PullRequest, reviewReport *review.Report, provider vcs.Provider) *practices.Report {
	data, _ := json.Marshal(config.PracticePolicy{Practices: cfg.Practices, Standards: cfg.Standards, Review: cfg.Review, Linters: cfg.Linters})
	digest := sha256.Sum256(data)
	r := &practices.Report{SchemaVersion: practices.SchemaVersion, Profile: "engineering", Revision: reviewReport.Head, PolicySource: cfg.Policy.String(), PolicyDigest: hex.EncodeToString(digest[:]), ModelUsage: reviewReport.ModelUsage}
	var files []standards.File
	var failed []string
	reviewed := map[string]string{}
	if reviewReport.Plan != nil {
		for _, batch := range reviewReport.Plan.Batches {
			for _, entry := range batch.Entries {
				if entry.HasContent() && !entry.Truncated {
					reviewed[entry.File.Path] = entry.Content
				}
			}
		}
	}
	for index, file := range reviewReport.Files {
		if file.Kind == diff.ChangeDeleted || cfg.Ignored(file.Path) {
			continue
		}
		if ctx.Err() != nil {
			for _, remaining := range reviewReport.Files[index:] {
				if remaining.Kind != diff.ChangeDeleted && !cfg.Ignored(remaining.Path) {
					failed = append(failed, remaining.Path)
				}
			}
			break
		}
		body, err := provider.FileContent(ctx, ref, file.Path)
		if err != nil {
			failed = append(failed, file.Path)
			continue
		}
		if prior, ok := reviewed[file.Path]; ok && !bytes.Equal(body, []byte(prior)) {
			failed = append(failed, file.Path)
		}
		files = append(files, standards.File{Path: file.Path, Src: body})
	}
	if ref.Head == vcs.Worktree {
		r.Revision = sourceDigest(files)
	}
	measured := standards.Measure(files, standards.Options{Floor: standards.Floor{MinShare: cfg.Standards.MinShare, MinSites: cfg.Standards.MinSites}, Disabled: cfg.Standards.Disabled})
	r.Checks = append(r.Checks, tellCheck(files, cfg.Practices.SlopRules))
	commitCheck := practices.Check{ID: "commits", Version: "1", Instrument: practices.Deterministic, State: practices.Unavailable, Reason: "commit range unavailable; supply -base"}
	base, head := ref.Base, ref.Head
	if pr != nil && ref.Number > 0 {
		base, head = pr.BaseSHA, pr.HeadSHA
	}
	if head == vcs.Worktree {
		head = "HEAD"
	}
	accepted := *cfg
	accepted.Review.Standards = true
	measurementBase := base
	if measurementBase == "" {
		measurementBase = "HEAD"
	}
	pinnedBase, pinErr := vcs.NewLocal(root, nil).CommitSHA(ctx, measurementBase)
	if pinErr != nil {
		pinnedBase = ""
	}
	baseline, status := review.BuildStandards(ctx, &accepted, root, pinnedBase, newLogger(false, "text"))
	for i := range measured.Results {
		measured.Results[i].Standing = ""
		for _, prior := range baseline.Report().Results {
			if prior.ID == measured.Results[i].ID {
				measured.Results[i].Standing = prior.Standing
				break
			}
		}
	}
	conventions := conventionCheck(files, measured, cfg.Practices.RequiredConventions)
	if status.State != review.StandardsActive {
		conventions.State, conventions.Reason = practices.Partial, "accepted convention measurement unavailable: "+status.Reason
	}
	r.Checks = append(r.Checks, conventions)
	inventoryFiles, metadataErrors := designContextFiles(ctx, provider, ref, files)
	inventory, boundaries := practices.InspectDesign(inventoryFiles, cfg.Practices.Boundaries)
	if boundaries.State == practices.Unavailable && len(reviewReport.Files) > 0 && !goBoundaryChange(reviewReport.Files) {
		boundaries.State, boundaries.Reason = practices.NotApplicable, "change contains no Go source or module boundary changes"
	}
	inventory.Errors = append(inventory.Errors, metadataErrors...)
	if len(metadataErrors) > 0 && len(cfg.Practices.Boundaries) > 0 {
		boundaries.State, boundaries.Reason = practices.Partial, strings.Join(metadataErrors, "; ")
	}
	r.Design = &inventory
	r.Checks = append(r.Checks, boundaries)
	if base != "" {
		entries, err := vcs.NewLocal(root, nil).Commits(ctx, base, head)
		if err != nil {
			commitCheck.Reason = err.Error()
		} else {
			commitCheck = practices.CommitCheck(entries.Commits, cfg.Practices.Commits)
			commitCheck.BaseRevision, commitCheck.Revision = entries.BaseSHA, entries.HeadSHA
		}
	}
	r.Checks = append(r.Checks, commitCheck)
	if slices.Contains(cfg.Practices.Required, "commit-title") {
		if pr != nil && ref.Number > 0 {
			r.Checks = append(r.Checks, practices.TitleCheck(pr.Title, cfg.Practices.Commits))
		} else {
			r.Checks = append(r.Checks, practices.Check{ID: "commit-title", Version: "1", Instrument: practices.Deterministic, State: practices.Unavailable, Reason: "no forge PR title available"})
		}
	}
	checks := []practices.Check{{ID: "slop", Version: "1", Instrument: practices.Model, PromptVersion: "engineering-2"}, {ID: "design", Version: "1", Instrument: practices.Model, PromptVersion: "engineering-2"}}
	for i := range checks {
		for _, file := range files {
			checks[i].Planned = append(checks[i].Planned, practices.Target{Kind: practices.FileTarget, ID: file.Path})
		}
	}
	r.Checks = append(r.Checks, modelCheckResults(checks, reviewReport)...)
	r.Checks = append(r.Checks, reviewAnalyzerCheck(files, reviewReport, cfg))
	r.Checks = append(r.Checks, practices.Check{ID: "security", Version: "1", Instrument: practices.Deterministic, State: practices.NotSelected, Reason: "no separate security assessment selected"})
	practices.ApplyPolicy(r, cfg.Practices)
	if len(failed) > 0 {
		check := practices.Check{ID: "snapshot", Version: "1", Instrument: practices.Deterministic, Required: true, State: practices.Failed, Reason: "reviewed files were unreadable or changed during engineering assessment"}
		if ctx.Err() != nil {
			check.State, check.Reason = practices.Partial, ctx.Err().Error()
		}
		for _, name := range failed {
			target := practices.Target{Kind: practices.FileTarget, ID: name}
			check.Planned = append(check.Planned, target)
			check.Omitted = append(check.Omitted, practices.Omission{Target: target, Reason: check.Reason})
		}
		r.Checks = append(r.Checks, check)
	}
	practices.ApplyExceptions(r, cfg.Practices.Exceptions, files, time.Now())
	return r
}

func reviewAnalyzerCheck(files []standards.File, report *review.Report, cfg *config.Config) practices.Check {
	c := practices.Check{ID: "linters", Version: "1", Instrument: practices.Deterministic, State: practices.Completed}
	described := map[string]linters.CatalogEntry{}
	for _, entry := range append(linters.Builtins(), linters.Catalog()...) {
		described[entry.Name] = entry
	}
	selected := slices.Clone(cfg.Linters.Enabled)
	ran := map[string]bool{}
	for _, status := range report.Linters {
		selected = append(selected, status.Linter)
		ran[status.Linter] = status.Outcome == review.LinterRan
	}
	for _, file := range files {
		planned, examined := false, true
		for _, name := range selected {
			if described[name].Reads(file.Path) {
				planned = true
				examined = examined && ran[name]
			}
		}
		if !planned {
			continue
		}
		target := practices.Target{Kind: practices.FileTarget, ID: file.Path}
		c.Planned = append(c.Planned, target)
		if examined {
			c.Examined = append(c.Examined, target)
		} else {
			c.Omitted = append(c.Omitted, practices.Omission{Target: target, Reason: "an enabled analyzer did not complete"})
		}
	}
	if len(c.Omitted) > 0 {
		c.State, c.Reason = practices.Partial, "an enabled analyzer did not establish coverage"
	}
	if len(c.Planned) == 0 {
		c.State, c.Reason = practices.NotApplicable, "no configured analyzer reads these paths"
	}
	if cfg.Linters.Mode == config.LinterOff || len(selected) == 0 {
		c.State, c.Reason = practices.NotSelected, "no analyzers were selected"
	}
	for _, uncovered := range report.Uncovered {
		c.State, c.Reason = practices.Partial, "analyzer reported incomplete coverage: "+string(uncovered.Reason)
	}
	for _, finding := range report.AnalyzerFindings {
		c.Findings = append(c.Findings, practices.Finding{Rule: finding.Source, Target: practices.Target{Kind: practices.FileTarget, ID: finding.Path, Line: finding.Line}, Title: finding.Title,
			Rationale: finding.Rationale, Remedy: finding.Suggestion, Severity: finding.Severity, Blocking: true, Sources: []string{finding.Source}})
	}
	if report.Plan != nil {
		for _, skip := range report.Plan.Skipped {
			if skip.Reason != bundle.ReasonIgnored && skip.Reason != bundle.ReasonDeleted {
				c.State, c.Reason = practices.Partial, skip.Reason
			}
		}
	}
	return c
}

func designContextFiles(ctx context.Context, provider vcs.Provider, ref vcs.Ref, files []standards.File) ([]standards.File, []string) {
	out := slices.Clone(files)
	seen := map[string]bool{}
	for _, file := range files {
		seen[file.Path] = true
	}
	var problems []string
	for _, file := range files {
		if path.Ext(file.Path) != ".go" {
			continue
		}
		for dir := path.Dir(file.Path); ; dir = path.Dir(dir) {
			if err := ctx.Err(); err != nil {
				return out, append(problems, err.Error())
			}
			name := path.Join(dir, "go.mod")
			if !seen[name] {
				seen[name] = true
				body, err := provider.FileContent(ctx, ref, name)
				if ctx.Err() != nil {
					return out, append(problems, ctx.Err().Error())
				}
				if err == nil {
					out = append(out, standards.File{Path: name, Src: body})
				} else if !errors.Is(err, vcs.ErrNotFound) {
					problems = append(problems, name+": module metadata unavailable")
				}
			}
			if dir == "." {
				break
			}
		}
	}
	return out, problems
}

func goBoundaryChange(files diff.Files) bool {
	for _, file := range files {
		for _, name := range []string{file.Path, file.OldPath} {
			if path.Ext(name) == ".go" || path.Base(name) == "go.mod" || path.Base(name) == "go.work" {
				return true
			}
		}
	}
	return false
}
