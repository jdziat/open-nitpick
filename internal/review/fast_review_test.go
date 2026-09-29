package review

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/diff"
	"github.com/jdziat/open-nitpick/internal/llm"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

func TestFastReviewLimitsFindingsAndDisclosesWhatItOmitted(t *testing.T) {
	findings := make([]Finding, 0, 12)
	for i := 0; i < 12; i++ {
		findings = append(findings, Finding{Path: "one.go", Line: 2, Severity: "warning", Class: "correctness", Title: fmt.Sprintf("finding %02d", i), Rationale: "The changed value is wrong."})
	}
	model := &scriptedLLM{fallback: mustJSON(t, Result{Findings: findings})}
	cfg := config.Defaults()
	cfg.Models.Default = config.ModelSpec{Provider: "openai", Model: "test"}
	cfg.Review.IncludeFullFiles = false
	cfg.Review.RelatedContext = false
	client := llm.NewClientForTest(model, cfg.Models.Default)
	provider := &stubProvider{diff: "diff --git a/one.go b/one.go\n--- a/one.go\n+++ b/one.go\n@@ -0,0 +1,2 @@\n+package one\n+var Value = 1\n"}
	engine := &Engine{Config: cfg, Roles: &llm.Roles{Review: client, Triage: client}, Provider: provider, FastLimit: 10, SkipTriage: true}

	report, err := engine.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Findings) != 10 || report.OmittedFindings != 2 {
		t.Fatalf("findings=%d omitted=%d, want 10 and 2", len(report.Findings), report.OmittedFindings)
	}
	if calls := model.callCount(); calls != 1 {
		t.Fatalf("model calls=%d, want one review request and no triage", calls)
	}
	if provider.published == nil || !strings.Contains(provider.published.Summary, "Fast review") || !strings.Contains(provider.published.Summary, "2 lower-ranked") {
		t.Fatalf("fast-review receipt missing: %+v", provider.published)
	}
}

func TestFastReviewMakesAFileLimitIncomplete(t *testing.T) {
	var raw strings.Builder
	for i := 0; i < 11; i++ {
		fmt.Fprintf(&raw, "diff --git a/f%02d.go b/f%02d.go\n--- a/f%02d.go\n+++ b/f%02d.go\n@@ -0,0 +1,2 @@\n+package fast\n+var V%d = %d\n", i, i, i, i, i, i)
	}
	model := &scriptedLLM{fallback: `{"findings":[]}`}
	cfg := config.Defaults()
	cfg.Models.Default = config.ModelSpec{Provider: "openai", Model: "test"}
	cfg.Review.MaxFiles = 10
	cfg.Review.MaxFilesPerRequest = 1
	cfg.Review.Concurrency = 10
	cfg.Review.IncludeFullFiles = false
	cfg.Review.RelatedContext = false
	client := llm.NewClientForTest(model, cfg.Models.Default)
	provider := &stubProvider{diff: raw.String()}
	engine := &Engine{Config: cfg, Roles: &llm.Roles{Review: client, Triage: client}, Provider: provider, SkipTriage: true}

	report, err := engine.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if report.ReusableCoverage() {
		t.Fatal("a fast review with an omitted changed file claimed reusable coverage")
	}
	if provider.published == nil || !strings.Contains(provider.published.Summary, "Read 10 of 11 changed files") || !strings.Contains(provider.published.Summary, "exceeded review.max_files") {
		t.Fatalf("file-limit receipt missing: %+v", provider.published)
	}
}

func TestFastReviewRanksFilesBeforeApplyingItsLimit(t *testing.T) {
	plain := file("a_plain.go", 2, 0)
	risky := file("internal/auth/session.go", 2, 2)
	ordered := rankFiles([]*diff.File{plain, risky})
	if got := ordered[0].Path; got != risky.Path {
		t.Fatalf("first ranked path = %q, want %q", got, risky.Path)
	}
}

func TestFastReviewNeverProvidesReusableCoverageOrClearsThreads(t *testing.T) {
	provider := &resolvingProvider{incrementalProvider: incrementalProvider{
		stubProvider: stubProvider{diff: engineDiff},
		head:         "beef02",
		prior: &vcs.PriorReview{Head: "beef01", Comments: []vcs.PriorComment{
			{ID: 9, Path: "app.go", Line: 4, Fingerprint: "prior", Class: "correctness"},
		}},
	}}
	model := &scriptedLLM{fallback: `{"findings":[]}`}
	cfg := config.Defaults()
	cfg.Models.Default = config.ModelSpec{Provider: "openai", Model: "test"}
	cfg.Review.IncludeFullFiles = false
	cfg.Review.RelatedContext = false
	cfg.Review.Approve.Enabled = true
	client := llm.NewClientForTest(model, cfg.Models.Default)
	engine := &Engine{Config: cfg, Roles: &llm.Roles{Review: client, Triage: client}, Provider: provider, SkipTriage: true}

	report, err := engine.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if report.ReusableCoverage() {
		t.Fatal("fast review must not provide an incremental baseline")
	}
	if len(provider.resolved) != 0 {
		t.Fatalf("fast review resolved standing threads: %v", provider.resolved)
	}
	if provider.published == nil || provider.published.Event != vcs.EventComment {
		t.Fatalf("fast review event = %+v, want COMMENT", provider.published)
	}
}

func TestFastReviewKeepsDuplicateDiffEntries(t *testing.T) {
	file := file("same.go", 2, 1)
	ordered := rankFiles([]*diff.File{file, file})
	if len(ordered) != 2 || ordered[0] != file || ordered[1] != file {
		t.Fatalf("ranked files = %#v, want both duplicate entries", ordered)
	}
}
