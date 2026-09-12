package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/jdziat/open-nitpick/internal/bundle"
	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/practices"
	"github.com/jdziat/open-nitpick/internal/review"
	"github.com/jdziat/open-nitpick/internal/slop"
	"github.com/jdziat/open-nitpick/internal/standards"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

func assessEngineering(ctx context.Context, root, configPath, base string, noModel bool, budget int, policy config.PracticePolicy, files []standards.File, measured RepoStandardsResult) practices.Report {
	r := practices.Report{SchemaVersion: practices.SchemaVersion, Profile: "engineering", Revision: sourceDigest(files), PolicySource: policy.Source, PolicyDigest: policy.Digest}
	inventory, boundaries := practices.InspectDesign(files, policy.Practices.Boundaries)
	r.Design = &inventory
	conventions := conventionCheck(files, measured.Report, policy.Practices.RequiredConventions)
	r.Checks = append(r.Checks, conventions, analyzerEvidenceCheck(measured.Sources, measured.analyzerFindings), tellCheck(files, policy.Practices.SlopRules))
	r.Checks = append(r.Checks, boundaries)
	commitCheck := practices.Check{ID: "commits", Version: "1", Instrument: practices.Deterministic, State: practices.Unavailable, Reason: "no commit range selected; supply -base"}
	if base != "" {
		commitBase := base
		if policy.BaseRevision != "" {
			commitBase = policy.BaseRevision
		}
		committed, err := vcs.NewLocal(root, nil).Commits(ctx, commitBase, "HEAD")
		if err != nil {
			commitCheck.Reason = err.Error()
		} else {
			commitCheck = practices.CommitCheck(committed.Commits, policy.Practices.Commits)
			commitCheck.BaseRevision, commitCheck.Revision = committed.BaseSHA, committed.HeadSHA
		}
	}
	r.Checks = append(r.Checks, commitCheck)
	if budget == 0 {
		budget = policy.Practices.Budget
	}
	modelBase := base
	if policy.BaseRevision != "" {
		modelBase = policy.BaseRevision
	}
	modelChecks, usage := engineeringModelChecks(ctx, root, configPath, modelBase, noModel, budget, files)
	r.ModelUsage = usage
	r.Checks = append(r.Checks, modelChecks...)
	r.Checks = append(r.Checks, practices.Check{ID: "security", Version: "1", Instrument: practices.Deterministic,
		State: practices.NotSelected, Reason: "no separate security assessment selected; analyzer findings remain under linters"})
	practices.ApplyPolicy(&r, policy.Practices)
	practices.ApplyExceptions(&r, policy.Practices.Exceptions, files, time.Now())
	return r
}

func sourceDigest(files []standards.File) string {
	hash := sha256.New()
	for _, f := range files {
		_, _ = fmt.Fprintf(hash, "%d:%s:%d:", len(f.Path), f.Path, len(f.Src))
		_, _ = hash.Write(f.Src)
	}
	return "worktree:sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func conventionCheck(files []standards.File, report standards.Report, required []string) practices.Check {
	c := practices.Check{ID: "conventions", Version: "1", Instrument: practices.Deterministic, State: practices.Completed, Tool: "nitpick.standards"}
	languages := map[string]bool{}
	for _, result := range report.Results {
		languages[result.Language] = true
		if result.Standing != standards.StandingStandard && !slices.Contains(required, result.ID) {
			continue
		}
		for _, off := range result.Off {
			c.Findings = append(c.Findings, practices.Finding{Rule: result.ID, Title: result.Rule,
				Target:    practices.Target{Kind: practices.FileTarget, ID: off.Path, Line: off.Line},
				Rationale: off.Excerpt, Severity: "warning", Blocking: true, Sources: []string{"nitpick.standards"}})
		}
	}
	for _, file := range files {
		if languages[bundle.Language(file.Path)] {
			target := practices.Target{Kind: practices.FileTarget, ID: file.Path}
			c.Planned = append(c.Planned, target)
			c.Examined = append(c.Examined, target)
		}
	}
	if len(c.Planned) == 0 {
		c.State, c.Reason = practices.NotApplicable, "no files in a language with enabled convention probes"
	}
	if len(report.Unmeasured) > 0 {
		c.State, c.Reason = practices.Partial, strings.Join(report.Unmeasured, "; ")
	}
	for _, id := range required {
		if !slices.ContainsFunc(report.Results, func(r standards.Result) bool { return r.ID == id }) {
			c.State, c.Reason = practices.Failed, "required convention was unknown or disabled: "+id
		}
	}
	return c
}

func analyzerCheck(sources []standards.SourceResult) practices.Check {
	c := practices.Check{ID: "linters", Version: "1", Instrument: practices.Deterministic, State: practices.Completed}
	seen := map[string]bool{}
	for _, source := range sources {
		if !source.Coverage.Ran || source.Coverage.Why != "" {
			c.State = practices.Partial
			c.Reason += source.Source + ": " + source.Coverage.Why + "; "
		}
		for name := range source.Coverage.Files {
			seen[name] = true
		}
		for _, observation := range source.Observations {
			c.Findings = append(c.Findings, practices.Finding{Rule: observation.Rule, Title: "analyzer reported " + observation.Rule,
				Target:  practices.Target{Kind: practices.FileTarget, ID: observation.Path, Line: observation.Line},
				Sources: []string{source.Source}, Severity: "warning", Blocking: true})
		}
	}
	var names []string
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		target := practices.Target{Kind: practices.FileTarget, ID: name}
		c.Planned = append(c.Planned, target)
		c.Examined = append(c.Examined, target)
	}
	if len(sources) == 0 {
		c.State, c.Reason = practices.NotSelected, "analyzers were disabled"
	} else if len(names) == 0 {
		c.State, c.Reason = practices.Unavailable, "no analyzer established file coverage; "+c.Reason
	}
	return c
}

func tellCheck(files []standards.File, blocking []string) practices.Check {
	c := practices.Check{ID: "slop-tells", Version: "1", Instrument: practices.Deterministic, State: practices.Completed, Tool: "nitpick.slop"}
	for _, file := range files {
		target := practices.Target{Kind: practices.FileTarget, ID: file.Path}
		c.Planned = append(c.Planned, target)
		c.Examined = append(c.Examined, target)
		for _, tell := range slop.Scan(file.Path, string(file.Src)) {
			c.Findings = append(c.Findings, practices.Finding{Rule: "slop." + tell.Rule, Target: practices.Target{Kind: practices.FileTarget, ID: tell.Path, Line: tell.Line},
				Title: tell.Rule, Rationale: tell.Excerpt, Remedy: tell.Fix, Sources: []string{"nitpick.slop"}, Severity: "nit", Blocking: slices.Contains(blocking, tell.Rule)})
		}
	}
	if len(files) == 0 {
		c.State, c.Reason = practices.NotApplicable, "no text files in the selected tree"
	}
	for _, id := range blocking {
		if !slices.ContainsFunc(slop.Rules, func(r slop.Rule) bool { return r.Name == id }) {
			c.State, c.Reason = practices.Failed, "unknown slop rule: "+id
		}
	}
	return c
}

func analyzerEvidenceCheck(sources []standards.SourceResult, findings []review.Finding) practices.Check {
	c := analyzerCheck(sources)
	if findings == nil {
		return c
	}
	c.Findings = nil
	for _, f := range findings {
		c.Findings = append(c.Findings, practices.Finding{Rule: f.Source,
			Target: practices.Target{Kind: practices.FileTarget, ID: f.Path, Line: f.Line},
			Title:  f.Title, Rationale: f.Rationale, Remedy: f.Suggestion,
			Severity: f.Severity, Blocking: true, Sources: []string{f.Source}})
	}
	return c
}
