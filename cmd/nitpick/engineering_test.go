package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/bundle"
	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/diff"
	"github.com/jdziat/open-nitpick/internal/practices"
	"github.com/jdziat/open-nitpick/internal/review"
	"github.com/jdziat/open-nitpick/internal/standards"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

func TestEngineeringProfileDoesNotPassWhenRequiredModelsAreDisabled(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init", "-q", "-b", "main")
	write(t, root, "app.py", "class Client: pass\n")
	git(t, root, "add", ".")
	git(t, root, "commit", "-qm", "feat: initial")
	git(t, root, "checkout", "-qb", "feature")
	git(t, root, "commit", "--allow-empty", "-qm", "fix: coverage")
	var output bytes.Buffer
	err := repoStandardsCommand(context.Background(), []string{"-repo", root, "-profile", "engineering", "-base", "main", "-no-model", "-json", "-check"}, &output,
		func(*config.Config) standards.Source { return &repoStandardsSource{} })
	if !errors.Is(err, errIncomplete) {
		t.Fatalf("disabled models passed: %v %s", err, output.String())
	}
	var result RepoStandardsResult
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Practices == nil || result.Practices.PolicyDigest == "" {
		t.Fatal("missing engineering evidence")
	}
	seen := map[string]practices.Check{}
	for _, check := range result.Practices.Checks {
		seen[check.ID] = check
	}
	if seen["commits"].State != practices.Completed || len(seen["commits"].Examined) != 1 || seen["linters"].State != practices.Completed {
		t.Fatalf("deterministic evidence lost: %+v", seen)
	}
	if seen["slop"].State != practices.Unavailable || !seen["slop"].Required || seen["design"].State != practices.Unavailable {
		t.Fatalf("model omissions hidden: %+v", seen)
	}
}

func TestModelCoverageRequiresSuccessfulBatchesAndValidationStages(t *testing.T) {
	target := practices.Target{Kind: practices.FileTarget, ID: "a.go"}
	checks := func() []practices.Check {
		return []practices.Check{{ID: "slop", Planned: []practices.Target{target}}, {ID: "design", Planned: []practices.Target{target}}}
	}
	r := &review.Report{Plan: &bundle.Plan{Batches: []bundle.Batch{{Entries: []bundle.Entry{{File: &diff.File{Path: "a.go"}, Content: "package a"}}}}}}
	result := modelCheckResults(checks(), r)
	if result[0].State != practices.Completed || len(result[0].Examined) != 1 || len(result[0].Findings) != 0 {
		t.Fatalf("clean control: %+v", result)
	}
	r.Stages = []review.StageStatus{{Stage: "validation", Reason: "provider unavailable"}}
	result = modelCheckResults(checks(), r)
	if result[1].State != practices.Partial || len(result[1].FailedStages) != 1 {
		t.Fatal("validation failure passed")
	}
	r.Stages = nil
	r.Incomplete = []string{"a.go"}
	result = modelCheckResults(checks(), r)
	if len(result[0].Examined) != 0 || len(result[0].Omitted) != 1 {
		t.Fatal("failed batch counted as reviewed")
	}
	r.Incomplete = nil
	r.Plan.Batches[0].Entries[0].Truncated = true
	result = modelCheckResults(checks(), r)
	if result[1].State != practices.Partial {
		t.Fatal("windowed context reported as full design coverage")
	}
}

func TestExplicitSlopRulesCannotBeDilutedByCleanSource(t *testing.T) {
	files := []standards.File{{Path: "a.py", Src: []byte("# Sure! Here is the implementation\npass\n")}}
	check := tellCheck(files, []string{"chat-prose"})
	if len(check.Findings) == 0 || !check.Findings[0].Blocking {
		t.Fatalf("missing policy violation: %+v", check)
	}
	files = append(files, standards.File{Path: "clean.py", Src: bytes.Repeat([]byte("value = 1\n"), 10000)})
	check = tellCheck(files, []string{"chat-prose"})
	if len(check.Findings) == 0 || !check.Findings[0].Blocking {
		t.Fatal("clean lines diluted a blocking finding")
	}
	if tellCheck(files, []string{"chat-proze"}).State != practices.Failed {
		t.Fatal("misspelled required rule accepted")
	}
}

func TestEngineeringSnapshotReadsProseAndListsAcceptedExclusions(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init", "-q")
	write(t, root, "README.md", "Sure! Here is the implementation.\n")
	write(t, root, "generated.md", "ignored prose\n")
	git(t, root, "add", ".")
	git(t, root, "commit", "-qm", "docs: initial")
	policy := config.PracticePolicy{Practices: config.DefaultPractices(), Review: config.Defaults().Review}
	policy.Practices.Ignore = []string{"generated.md"}
	files, excluded, omitted, err := readEngineeringTree(context.Background(), root, policy)
	if err != nil || len(files) != 1 || len(files[0].Src) == 0 || len(excluded) != 1 || len(omitted) != 0 {
		t.Fatalf("snapshot=%v excluded=%v omitted=%v err=%v", files, excluded, omitted, err)
	}
	check := tellCheck(files, []string{"chat-prose"})
	if len(check.Findings) == 0 || check.Findings[0].Target.ID != "README.md" {
		t.Fatalf("prose went unread: %+v", check)
	}
}

func TestEngineeringAnalyzerEvidencePreservesMessagesAndSeverity(t *testing.T) {
	finding := review.Finding{Path: "a.go", Line: 4, Source: "govet.printf", Title: "format expects an integer", Rationale: "the argument is a string", Suggestion: "use %s", Severity: "error"}
	check := analyzerEvidenceCheck([]standards.SourceResult{{Source: "linters", Coverage: standards.Coverage{Ran: true, Files: map[string]int{"a.go": 10}}}}, []review.Finding{finding})
	if check.State != practices.Completed || len(check.Examined) != 1 || len(check.Findings) != 1 {
		t.Fatalf("lost executed analyzer evidence: %+v", check)
	}
	got := check.Findings[0]
	if got.Title != finding.Title || got.Rationale != finding.Rationale || got.Remedy != finding.Suggestion || got.Severity != "error" || !got.Blocking {
		t.Fatalf("analyzer diagnostic reduced to a rule name: %+v", got)
	}
}

func TestEngineeringAnalyzersUseAcceptedSettings(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init", "-q", "-b", "main")
	write(t, root, "a.py", "value = 1\n")
	write(t, root, ".nitpick.yaml", "linters:\n  mode: off\n  enabled: [ruff]\n")
	git(t, root, "add", ".")
	git(t, root, "commit", "-qm", "feat: initial")
	write(t, root, ".nitpick.yaml", "linters:\n  mode: strict\n  enabled: [eslint]\n")
	var output bytes.Buffer
	called := false
	err := repoStandardsCommand(context.Background(), []string{"-repo", root, "-profile", "engineering", "-base", "main", "-no-model", "-json"}, &output,
		func(cfg *config.Config) standards.Source {
			called = true
			if cfg.Linters.Mode != config.LinterOff || len(cfg.Linters.Enabled) != 1 || cfg.Linters.Enabled[0] != "ruff" {
				t.Fatalf("accepted analyzer policy replaced: %+v", cfg.Linters)
			}
			return &repoStandardsSource{}
		})
	if err != nil || !called {
		t.Fatalf("analyzer policy not exercised: %v", err)
	}
}

func TestReviewConventionsCannotBeRedefinedByTheChange(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init", "-q", "-b", "main")
	write(t, root, "go.mod", "module example.com/app\ngo 1.25\n")
	write(t, root, "api.go", "package app\n// Run starts the application.\nfunc Run() {}\n")
	git(t, root, "add", ".")
	git(t, root, "commit", "-qm", "feat: initial")
	changed := "package app\n// Starts the application.\nfunc Run() {}\n"
	write(t, root, "api.go", changed)
	cfg := config.Defaults()
	cfg.Standards.MinSites, cfg.Standards.MinShare = 1, 1
	file := &diff.File{Path: "api.go"}
	r := &review.Report{Head: "HEAD", Files: diff.Files{file}, Plan: &bundle.Plan{Batches: []bundle.Batch{{Entries: []bundle.Entry{{File: file, Content: changed}}}}}}
	result := assessReviewPractices(context.Background(), root, cfg, vcs.Ref{Base: "main"}, nil, r, vcs.NewLocal(root, nil))
	for _, check := range result.Checks {
		if check.ID == "conventions" {
			if check.State != practices.Completed || len(check.Examined) != 1 || len(check.Findings) != 1 || check.Findings[0].Rule != "go-doc-comment-name" {
				t.Fatalf("change voted away accepted convention: %+v", check)
			}
			return
		}
	}
	t.Fatal("convention check was not executed")
}

func TestReviewBoundariesReadUnchangedModuleMetadata(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init", "-q", "-b", "main")
	write(t, root, "go.mod", "module example.com/app\ngo 1.25\n")
	body := "package api\nimport _ \"example.com/app/db\"\n"
	if err := os.MkdirAll(filepath.Join(root, "api"), 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, root, "api/api.go", body)
	git(t, root, "add", ".")
	git(t, root, "commit", "-qm", "feat: initial")
	cfg := config.Defaults()
	cfg.Practices.Boundaries = []config.PracticeBoundary{{From: "example.com/app/api", Forbid: []string{"example.com/app/db"}, Reason: "API must use service"}}
	r := &review.Report{Head: "HEAD", Files: diff.Files{&diff.File{Path: "api/api.go"}}}
	result := assessReviewPractices(context.Background(), root, cfg, vcs.Ref{Base: "main"}, nil, r, vcs.NewLocal(root, nil))
	for _, check := range result.Checks {
		if check.ID == "design-boundaries" {
			if check.State != practices.Completed || len(check.Examined) != 1 || len(check.Findings) != 1 || !check.Required {
				t.Fatalf("boundary without changed go.mod was skipped: %+v", check)
			}
			return
		}
	}
	t.Fatal("boundary check was not executed")
}

func TestSharedModelEvidencePreservesRoutesAndStandingFindings(t *testing.T) {
	file := &diff.File{Path: "a.go"}
	r := &review.Report{
		Plan:            &bundle.Plan{Batches: []bundle.Batch{{Entries: []bundle.Entry{{File: file, Content: "package a"}}}}},
		Routes:          []review.RouteDecision{{Files: []string{"a.go"}, Reviewer: "primary", Ensemble: []string{"second"}}},
		Escalated:       []review.Escalation{{Files: []string{"a.go"}, From: "primary", To: "fallback"}},
		AlreadyReported: []review.Finding{{Path: "a.go", Line: 1, Class: "correctness", Title: "standing defect"}},
		Findings:        []review.Finding{{Path: "a.go", Line: 1, Class: "correctness", Title: "analyzer defect", FromAnalyzer: true}},
	}
	checks := modelCheckResults([]practices.Check{{ID: "design", Planned: []practices.Target{{Kind: practices.FileTarget, ID: "a.go"}}}}, r)
	got := checks[0]
	if got.State != practices.Completed || len(got.Examined) != 1 || len(got.Findings) != 1 || got.Findings[0].Title != "standing defect" {
		t.Fatalf("standing finding lost or analyzer mislabeled as model evidence: %+v", got)
	}
	if len(got.ModelRuns) != 1 || got.ModelRuns[0].Primary != "primary" || got.ModelRuns[0].Fallback != "fallback" || len(got.ModelRuns[0].Ensemble) != 1 {
		t.Fatalf("actual routed model identity lost: %+v", got.ModelRuns)
	}
}

func TestReviewSnapshotRejectsContentChangedAfterModelAssessment(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init", "-q", "-b", "main")
	write(t, root, "a.py", "value = 1\n")
	git(t, root, "add", ".")
	git(t, root, "commit", "-qm", "feat: initial")
	file := &diff.File{Path: "a.py"}
	r := &review.Report{Head: "HEAD", Files: diff.Files{file}, Plan: &bundle.Plan{Batches: []bundle.Batch{{Entries: []bundle.Entry{{File: file, Content: "value = 1\n"}}}}}}
	assess := func() *practices.Report {
		return assessReviewPractices(context.Background(), root, config.Defaults(), vcs.Ref{Base: "main"}, nil, r, vcs.NewLocal(root, nil))
	}
	for _, check := range assess().Checks {
		if check.ID == "snapshot" && check.State == practices.Failed {
			t.Fatal("unchanged source incorrectly marked stale")
		}
	}
	write(t, root, "a.py", "value = 2\n")
	for _, check := range assess().Checks {
		if check.ID == "snapshot" && check.State == practices.Failed && check.Required {
			if len(check.Omitted) != 1 || check.Omitted[0].Target.ID != "a.py" {
				t.Fatalf("source drift omitted failing paths: %+v", check)
			}
			return
		}
	}
	t.Fatal("model assessment was attributed to different source bytes")
}

func TestModelAdapterRetainsUncertaintyAndExpertDecisions(t *testing.T) {
	r := &review.Report{
		Plan:      &bundle.Plan{Batches: []bundle.Batch{{Entries: []bundle.Entry{{File: &diff.File{Path: "a.go"}, Content: "package a"}}}}},
		Findings:  []review.Finding{{Path: "a.go", Line: 1, Class: "correctness", Title: "uncertain claim", Unresolved: "external contract unavailable", Severity: "error"}},
		Overruled: []review.Overruled{{Finding: review.Finding{Path: "a.go", Line: 1, Class: "maintainability", Title: "refuted claim"}, Expert: "design", Reason: "caller explicitly owns lifecycle"}},
	}
	checks := modelCheckResults([]practices.Check{{ID: "design", Planned: []practices.Target{{Kind: practices.FileTarget, ID: "a.go"}}}}, r)
	got := checks[0]
	if len(got.Findings) != 1 || got.Findings[0].Uncertainty != "external contract unavailable" || len(got.Decisions) != 1 || got.Decisions[0].Reason != "caller explicitly owns lifecycle" {
		t.Fatalf("expert uncertainty or refutation disappeared: %+v", got)
	}
	report := &practices.Report{Checks: checks}
	practices.ApplyPolicy(report, config.Practices{FailOn: map[string]config.Severity{"design": config.SeverityWarning}})
	if report.Checks[0].Findings[0].Blocking {
		t.Fatal("unresolved claim promoted to policy violation")
	}
}

func TestEngineeringCallbackUsesTheResolvedReviewPolicy(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init", "-q", "-b", "main")
	git(t, root, "commit", "--allow-empty", "-qm", "feat: initial")
	git(t, root, "checkout", "-qb", "feature")
	git(t, root, "commit", "--allow-empty", "-qm", "feat: changed policy")
	loaded := config.Defaults()
	loaded.Practices.Commits.Types = []string{"feat"}
	provider := vcs.NewLocal(root, nil)
	engine, err := newEngine(context.Background(), &reviewFlags{}, root, loaded, provider, vcs.Ref{Base: "main"}, newLogger(false, "text"))
	if err != nil {
		t.Fatal(err)
	}
	accepted := config.Defaults()
	accepted.Practices.Profile = "engineering"
	accepted.Practices.Commits.Types = []string{"fix"}
	r := &review.Report{Head: "HEAD", Policy: review.Policy{Config: accepted, Replaced: true}}
	result := engine.AssessPractices(context.Background(), vcs.Ref{Base: "main"}, nil, r)
	if result == nil {
		t.Fatal("change disabled the profile selected by accepted policy")
	}
	for _, check := range result.Checks {
		if check.ID == "commits" {
			if check.State != practices.Completed || len(check.Findings) != 1 || check.Findings[0].Rule != "commits.type" {
				t.Fatalf("callback used the change's commit policy: %+v", check)
			}
			return
		}
	}
	t.Fatal("commit policy was not assessed")
}

func TestActionsCannotReportIncompletePracticesAsClean(t *testing.T) {
	target := practices.Target{Kind: practices.FileTarget, ID: "a.go"}
	r := &review.Report{Stages: []review.StageStatus{{Stage: "triage", Reason: "provider unavailable"}}, Incomplete: []string{"a.go"}, Practices: &practices.Report{SchemaVersion: practices.SchemaVersion, Profile: "engineering", Revision: "head", PolicySource: "operator", PolicyDigest: "digest", Checks: []practices.Check{
		{ID: "conventions", Version: "1", Instrument: practices.Deterministic, State: practices.Completed, Planned: []practices.Target{target}, Examined: []practices.Target{target}},
		{ID: "design", Version: "1", Instrument: practices.Model, State: practices.Partial, Required: true, Reason: "budget"},
	}}}
	if resultFor(r, config.SeverityNone) != resultError {
		t.Fatal("required incomplete assessment reported clean in Actions")
	}
	r.Practices.Checks[1].Required = false
	if resultFor(r, config.SeverityNone) != resultClean {
		t.Fatal("optional incomplete assessment blocked completed required evidence")
	}
	r.Practices = nil
	if resultFor(r, config.SeverityNone) != resultError {
		t.Fatal("ordinary review lost its required-stage failure")
	}
}

func TestEngineeringProfileRejectsIgnoredLinterOverride(t *testing.T) {
	var out bytes.Buffer
	err := repoStandardsCommand(context.Background(), []string{"-profile", "engineering", "-linters", "ruff"}, &out, nil)
	if err == nil || !strings.Contains(err.Error(), "-linters cannot override") {
		t.Fatalf("operator selection silently discarded: %v", err)
	}
}

func TestEngineeringDesignScopeKeepsStyleSignalsSeparate(t *testing.T) {
	cfg := config.Defaults()
	cfg.Persona.Nitpick = config.NitpickOff
	applyEngineeringScope(cfg)
	if cfg.Persona.Nitpick != config.NitpickNormal || !cfg.Review.Slop || !cfg.Validation.Enabled {
		t.Fatalf("engineering scope did not select semantic checks: %+v", cfg.Review)
	}
	file := &diff.File{Path: "a.go"}
	r := &review.Report{Plan: &bundle.Plan{Batches: []bundle.Batch{{Entries: []bundle.Entry{{File: file, Content: "package a"}}}}}, Findings: []review.Finding{
		{Path: "a.go", Line: 1, Class: "style", Title: "rename a constructor"},
		{Path: "a.go", Line: 1, Class: "maintainability", Title: "state has conflicting owners"},
	}}
	checks := modelCheckResults([]practices.Check{{ID: "design", Planned: []practices.Target{{Kind: practices.FileTarget, ID: "a.go"}}}}, r)
	got := checks[0]
	if got.State != practices.Completed || len(got.Examined) != 1 || len(got.Findings) != 1 || got.Findings[0].Rule != "model.maintainability" || len(got.Signals) != 1 || got.Signals[0].Rule != "model.style" {
		t.Fatalf("style inflated design findings or disappeared: %+v", got)
	}
}

func TestMCPPracticeGateRetainsItsEvidenceAndPolicyName(t *testing.T) {
	target := practices.Target{Kind: practices.CommitTarget, ID: "commit-sha"}
	assessment := &practices.Report{SchemaVersion: practices.SchemaVersion, Profile: "engineering", Revision: "head", PolicySource: "operator", PolicyDigest: "digest", Checks: []practices.Check{{ID: "commits", Version: "1", Instrument: practices.Deterministic, State: practices.Completed, Planned: []practices.Target{target}, Examined: []practices.Target{target}, Findings: []practices.Finding{{Rule: "subject", Target: target, Title: "invalid subject", Blocking: true}}}}}
	out := reviewOut(&review.Report{Plan: &bundle.Plan{}, Practices: assessment}, config.SeverityNone)
	if out.Practices != assessment || out.FailOn != "engineering" || !out.Failed || !out.Complete || !strings.Contains(reviewText(out), "invalid subject") {
		t.Fatalf("practice verdict lost its evidence: %+v", out)
	}
}

func TestEmptyModelScopeIsInapplicableUnlessAStageFailed(t *testing.T) {
	for _, failed := range []bool{false, true} {
		report := &review.Report{Plan: &bundle.Plan{}}
		if failed {
			report.Stages = []review.StageStatus{{Stage: "triage", Reason: "provider unavailable"}}
		}
		checks := modelCheckResults([]practices.Check{{ID: "slop"}, {ID: "design"}}, report)
		for _, check := range checks {
			if failed && check.State != practices.Partial || !failed && check.State != practices.NotApplicable || len(check.Examined) != 0 || check.Reason == "" {
				t.Fatalf("empty scope conflated with failed execution: %+v", check)
			}
		}
	}
}

func TestDeletedOnlyReviewAssessesCommitsWithoutInventingModelWork(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init", "-q", "-b", "main")
	write(t, root, "go.mod", "module example.com/deleted\n\ngo 1.25\n")
	write(t, root, "a.go", "package deleted\n")
	git(t, root, "add", ".")
	git(t, root, "commit", "-qm", "feat: add source")
	git(t, root, "checkout", "-qb", "candidate")
	git(t, root, "rm", "a.go")
	git(t, root, "commit", "-qm", "refactor: remove unused source")
	report := &review.Report{Head: "HEAD", Files: diff.Files{&diff.File{Path: "a.go", Kind: diff.ChangeDeleted}}, Plan: &bundle.Plan{}}
	result := assessReviewPractices(t.Context(), root, config.Defaults(), vcs.Ref{Base: "main", Head: "HEAD"}, nil, report, vcs.NewLocal(root, nil))
	checks := map[string]practices.Check{}
	for _, check := range result.Checks {
		checks[check.ID] = check
	}
	if checks["commits"].State != practices.Completed || len(checks["commits"].Examined) != 1 || checks["slop"].State != practices.NotApplicable || checks["design"].State != practices.NotApplicable || len(checks["design"].Examined) != 0 {
		t.Fatalf("deleted-only scope was fabricated: %+v", checks)
	}
	if result.ExitCode() != 0 {
		t.Fatalf("valid empty source scope failed: %v", result.Problems())
	}
}

func TestFileDesignAdapterBindsSourceAndRelatedContext(t *testing.T) {
	file := &diff.File{Path: "a.py"}
	entry := bundle.Entry{File: file, Content: "answer = 42\n", Related: []bundle.Related{{Path: "b.py", Line: 1, Snippet: "limit = 42\n"}}}
	report := &review.Report{Plan: &bundle.Plan{Batches: []bundle.Batch{{Entries: []bundle.Entry{entry, {File: &diff.File{Path: "peer.py"}, Content: "peer = 42\n"}}}}}}
	checks := func() []practices.Check {
		return []practices.Check{{ID: "design", Planned: []practices.Target{{Kind: practices.FileTarget, ID: "a.py"}}}}
	}
	original := modelCheckResults(checks(), report)[0]
	if len(original.Tasks) != 1 || len(original.Tasks[0].Sources) != 1 || len(original.Tasks[0].SourceDigest) != 64 {
		t.Fatalf("file adapter lost source binding: %+v", original)
	}
	report.Plan.Batches[0].Entries[0].Related[0].Snippet = "limit = 43\n"
	changed := modelCheckResults(checks(), report)[0]
	if changed.Tasks[0].SourceDigest == original.Tasks[0].SourceDigest {
		t.Fatal("related source changed without changing task binding")
	}
	report.Plan.Batches[0].Entries[0].Related[0].Snippet = "limit = 42\n"
	report.Plan.Batches[0].Entries[1].Content = "peer = 43\n"
	peer := modelCheckResults(checks(), report)[0]
	if peer.Tasks[0].SourceDigest == original.Tasks[0].SourceDigest {
		t.Fatal("batch source changed without changing task binding")
	}
}

func TestReviewBoundaryApplicabilityKeepsMissingGoInputsIncomplete(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init", "-q", "-b", "main")
	write(t, root, "go.mod", "module example.com/app\ngo 1.25\n")
	write(t, root, "api.go", "package app\n")
	write(t, root, "README.md", "Project documentation.\n")
	git(t, root, "add", ".")
	git(t, root, "commit", "-qm", "feat: initial")
	cfg := config.Defaults()
	cfg.Practices.Boundaries = []config.PracticeBoundary{{From: "*", Forbid: []string{"net/http"}, Reason: "no HTTP dependencies"}}
	for _, tc := range []struct {
		name         string
		file         *diff.File
		inapplicable bool
	}{
		{"empty diff", nil, false},
		{"documentation", &diff.File{Path: "README.md"}, true},
		{"configuration", &diff.File{Path: ".nitpick.yaml"}, true},
		{"missing source", &diff.File{Path: "missing.go"}, false},
		{"deleted source", &diff.File{Path: "api.go", Kind: diff.ChangeDeleted}, false},
		{"renamed source", &diff.File{Path: "api.txt", OldPath: "api.go", Kind: diff.ChangeRenamed}, false},
		{"module", &diff.File{Path: "go.mod"}, false},
		{"workspace", &diff.File{Path: "go.work"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report := &review.Report{Head: "HEAD", Plan: &bundle.Plan{}}
			if tc.file != nil {
				report.Files = diff.Files{tc.file}
			}
			result := assessReviewPractices(t.Context(), root, cfg, vcs.Ref{Base: "main"}, nil, report, vcs.NewLocal(root, nil))
			for _, check := range result.Checks {
				if check.ID != "design-boundaries" {
					continue
				}
				if (check.State == practices.NotApplicable) != tc.inapplicable || len(check.Examined) != 0 {
					t.Fatalf("incorrect boundary applicability: %+v", check)
				}
				return
			}
			t.Fatal("boundary instrument did not report its state")
		})
	}
}

func TestEngineeringScopeRetainsFileLimitAndMarksSelectedProfile(t *testing.T) {
	cfg := config.Defaults()
	cfg.Review.MaxFiles = 3
	applyEngineeringScope(cfg)
	if cfg.Practices.Profile != "engineering" || cfg.Review.MaxFiles != 3 {
		t.Fatalf("profile selection lost the explicit scope: profile=%q files=%d", cfg.Practices.Profile, cfg.Review.MaxFiles)
	}
}

func TestPackageCoverageUsesSuccessfulTasksInsteadOfSharedFiles(t *testing.T) {
	a := practices.Target{Kind: practices.FileTarget, ID: "a.go"}
	b := practices.Target{Kind: practices.FileTarget, ID: "b.go"}
	tasks := []practices.DesignTask{
		{ID: "package:a", Source: a, Sources: []practices.Target{a}, Context: []practices.Target{b}, SourceDigest: strings.Repeat("a", 64), Purpose: "assess a"},
		{ID: "package:b", Source: b, Sources: []practices.Target{b}, Context: []practices.Target{a}, SourceDigest: strings.Repeat("b", 64), Purpose: "assess b"},
	}
	entries := []bundle.Entry{{File: &diff.File{Path: "a.go"}, Content: "package a"}, {File: &diff.File{Path: "b.go"}, Content: "package b"}}
	report := &review.Report{DesignExecution: &review.DesignExecution{DesignPacking: practices.DesignPacking{Design: practices.DesignPlan{Tasks: tasks}}}, Plan: &bundle.Plan{Batches: []bundle.Batch{{DesignTask: "package:a", Entries: entries}, {DesignTask: "package:b", Entries: entries}}}, AssessedDesignTasks: []string{"package:a"}}
	result := modelCheckResults([]practices.Check{{ID: "design", Planned: []practices.Target{a, b}}}, report)[0]
	if result.State != practices.Partial || len(result.Examined) != 1 || result.Examined[0].ID != "package:a" || len(result.Omitted) != 1 || result.Omitted[0].Target.ID != "package:b" {
		t.Fatalf("shared source became successful task coverage: %+v", result)
	}
	report.AssessedDesignTasks = append(report.AssessedDesignTasks, "package:b")
	completed := modelCheckResults([]practices.Check{{ID: "design"}}, report)[0]
	if completed.State != practices.Completed || len(completed.Examined) != 2 || len(completed.Tasks) != 2 {
		t.Fatalf("empty successful assessments lost task evidence: %+v", completed)
	}
	report.DesignExecution.Design.Errors = []string{"caller inventory truncated"}
	partial := modelCheckResults([]practices.Check{{ID: "design"}}, report)[0]
	if partial.State != practices.Partial || len(partial.FailedStages) == 0 {
		t.Fatalf("unknown graph scope was promoted: %+v", partial)
	}
}

func TestEngineeringAssessmentUsesFrozenSourceWithoutProviderReads(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init", "-q", "-b", "main")
	write(t, root, "go.mod", "module example.com/frozen\n\ngo 1.25\n")
	write(t, root, "a.go", "package frozen\n")
	git(t, root, "add", ".")
	git(t, root, "commit", "-qm", "feat: initial")
	provider := &assessmentReadTrap{Provider: vcs.NewLocal(root, nil)}
	report := &review.Report{Files: diff.Files{&diff.File{Path: "a.go"}}, Plan: &bundle.Plan{}, DesignExecution: &review.DesignExecution{Sources: []standards.File{
		{Path: "a.go", Src: []byte("package frozen\n")},
		{Path: "go.mod", Src: []byte("module example.com/frozen\n")},
		{Path: "background/b.go", Src: []byte("package background\n")},
	}}}
	cfg := config.Defaults()
	cfg.Practices.Boundaries = []config.PracticeBoundary{{From: "*", Forbid: []string{"forbidden"}}}
	result := assessReviewPractices(t.Context(), root, cfg, vcs.Ref{Base: "main", Head: vcs.Worktree}, nil, report, provider)
	if provider.reads != 0 {
		t.Fatalf("assessment reread frozen source %d times", provider.reads)
	}
	for _, check := range result.Checks {
		if check.ID == "snapshot" {
			t.Fatalf("frozen source became unavailable: %+v", check)
		}
		if check.ID == "design-boundaries" && (check.State != practices.Completed || len(check.Examined) != 1 || check.Examined[0].ID != "a.go") {
			t.Fatalf("boundary scope changed: %+v", check)
		}
	}
	if len(result.Design.Units) != 1 || result.Design.Units[0].ID != "example.com/frozen" {
		t.Fatalf("frozen module identity lost: %+v", result.Design)
	}
}

type assessmentReadTrap struct {
	vcs.Provider
	reads int
}

func (p *assessmentReadTrap) FileContent(context.Context, vcs.Ref, string) ([]byte, error) {
	p.reads++
	return nil, errors.New("source changed after capture")
}

func TestFrozenMetadataDoesNotInventAnOuterModuleIdentity(t *testing.T) {
	for _, excluded := range []bool{false, true} {
		execution := &review.DesignExecution{Sources: []standards.File{{Path: "go.mod", Src: []byte("module example.com/outer\n")}}}
		if excluded {
			execution.Excluded = []bundle.Skip{{Path: "nested/go.mod", Reason: bundle.ReasonIgnored}}
		} else {
			execution.Design.Errors = []string{"nested/go.mod: unavailable"}
		}
		files, problems := frozenDesignContext([]standards.File{{Path: "nested/a.go", Src: []byte("package nested\n")}}, execution)
		inventory, _ := practices.InspectDesign(files, nil)
		if len(problems) == 0 || len(inventory.Errors) == 0 || len(inventory.Units) != 0 {
			t.Fatalf("missing metadata invented a package: %+v; %v", inventory, problems)
		}
	}
}

func TestDesignCoveragePublishesThePlannerScopeLimitations(t *testing.T) {
	report := &review.Report{DesignExecution: &review.DesignExecution{DesignPacking: practices.DesignPacking{Design: practices.DesignPlan{Limitations: []string{"deeper implementation bodies were not supplied"}}}}}
	check := practices.Check{ID: "design"}
	applyPackageCoverage(&check, report)
	if !slices.Equal(check.Limitations, report.DesignExecution.Design.Limitations) {
		t.Fatalf("lost planner limits: %+v", check)
	}
	text := (practices.Report{Profile: "engineering", Checks: []practices.Check{check}}).Text()
	if !strings.Contains(text, "Scope limit: deeper implementation bodies were not supplied") {
		t.Fatalf("hidden planner limit: %s", text)
	}
	data, err := json.Marshal(check)
	if err != nil || !strings.Contains(string(data), `"limitations":["deeper implementation bodies were not supplied"]`) {
		t.Fatalf("JSON lost limits: %s %v", data, err)
	}
}

func TestSlopCoverageKeepsSuccessfulReadsAcrossSharedTaskFailures(t *testing.T) {
	source := practices.Target{Kind: practices.FileTarget, ID: "source.go"}
	tasks := []practices.DesignTask{
		{ID: "first", Source: source, Sources: []practices.Target{source}, SourceDigest: strings.Repeat("a", 64), Purpose: "first declaration"},
		{ID: "second", Source: source, Sources: []practices.Target{source}, SourceDigest: strings.Repeat("b", 64), Purpose: "second declaration"},
	}
	entry := bundle.Entry{File: &diff.File{Path: source.ID}, Content: "package fixture\nfunc A() {}\nfunc B() {}\n"}
	report := &review.Report{DesignExecution: &review.DesignExecution{DesignPacking: practices.DesignPacking{Design: practices.DesignPlan{Tasks: tasks}}}, Plan: &bundle.Plan{Batches: []bundle.Batch{{DesignTask: "first", Entries: []bundle.Entry{entry}}, {DesignTask: "second", Entries: []bundle.Entry{entry}}}}, AssessedDesignTasks: []string{"first"}, Incomplete: []string{source.ID}}
	checks := func() []practices.Check {
		return modelCheckResults([]practices.Check{{ID: "slop", Planned: []practices.Target{source}}, {ID: "design"}}, report)
	}
	got := checks()
	if got[0].State != practices.Completed || len(got[0].Examined) != 1 || got[1].State != practices.Partial || len(got[1].Examined) != 1 || len(got[1].Omitted) != 1 {
		t.Fatalf("shared failure erased completed evidence: %+v", got)
	}
	report.AssessedDesignTasks = nil
	report.Incomplete = nil
	for _, check := range checks() {
		if len(check.Examined) != 0 || check.State == practices.Completed {
			t.Fatalf("planned source claimed a read: %+v", check)
		}
	}
}
