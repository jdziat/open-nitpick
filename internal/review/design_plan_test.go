package review

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jdziat/open-nitpick/internal/bundle"
	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/diff"
	"github.com/jdziat/open-nitpick/internal/llm"
	"github.com/jdziat/open-nitpick/internal/vcs"
	llms "github.com/nocturnium/llm-go-sdk/v6"
)

func TestDesignAssemblyKeepsSiblingAndCallerSourceTogether(t *testing.T) {
	cfg := config.Defaults()
	tree := vcs.NewSnapshot(vcs.NewLocal(t.TempDir(), nil), map[string][]byte{
		"go.mod":             []byte("module example.com/app\n\ngo 1.25\n"),
		"store/read.go":      []byte("package store\nfunc Read() {}\n"),
		"store/state.go":     []byte("package store\nvar state int\n"),
		"service/service.go": []byte("package service\nimport _ \"example.com/app/store\"\n"),
	})
	e := &Engine{Config: cfg, Provider: tree}
	files := diff.Files{&diff.File{Path: "store/read.go", Kind: diff.ChangeModified}}
	packed := e.assembleDesign(t.Context(), vcs.Ref{Head: vcs.Worktree}, files, bundle.Reserve{Tokens: 100})
	if len(packed.Design.Errors) != 0 || len(packed.Plan.Batches) != 2 {
		t.Fatalf("assembly=%+v", packed.Design)
	}
	caller := slices.IndexFunc(packed.Plan.Batches, func(batch bundle.Batch) bool {
		return slices.Contains(batch.DesignTaskIDs(), "file:service/service.go")
	})
	if caller < 0 {
		t.Fatal("caller obligation was not assigned a request")
	}
	rendered := bundle.RenderBatch(packed.Plan.Batches[caller])
	for _, evidence := range []string{"var state int", "service/service.go", "package:example.com/app/store"} {
		if !strings.Contains(rendered, evidence) {
			t.Fatalf("same request lacks %q", evidence)
		}
	}
	tree.Unbudgeted = []string{"store/state.go"}
	limited := e.assembleDesign(t.Context(), vcs.Ref{Head: vcs.Worktree}, files, bundle.Reserve{Tokens: 100})
	if len(limited.Design.Errors) == 0 {
		t.Fatal("tree omission disappeared from assembly coverage")
	}
	for _, batch := range limited.Plan.Batches {
		if strings.Contains(bundle.RenderBatch(batch), "var state int") {
			t.Fatal("budget-excluded source reappeared as context")
		}
	}
}

func TestDesignBatchCompletionRetainsExactContextWithNoFindings(t *testing.T) {
	model := &scriptedLLM{fallback: `{"findings":[]}`}
	e := newEngine(t, model, &stubProvider{}, nil)
	batch := bundle.Batch{DesignTask: "package:fixture", Assessment: "task fixture", Entries: []bundle.Entry{{File: &diff.File{Path: "app.go", Kind: diff.ChangeModified}, Content: "package app\n"}}}
	plan := &bundle.Plan{Batches: []bundle.Batch{batch}}
	findings, missing, _, err := e.analyze(t.Context(), &vcs.PullRequest{}, plan, make(chan struct{}, 1))
	if err != nil || len(findings) != 0 || len(missing) != 0 || model.callCount() != 1 || len(e.assessedDesignTasks) != 1 || e.assessedDesignTasks[0] != batch.DesignTask {
		t.Fatalf("empty successful task lost execution evidence: findings=%v missing=%v err=%v plan=%+v", findings, missing, err, plan)
	}
	if !strings.Contains(strings.Join(model.prompts(), "\n"), bundle.RenderBatch(batch)) {
		t.Fatal("review omitted task metadata or source")
	}
}

func TestDesignClaimCarriesItsBatchContextIntoExpertValidation(t *testing.T) {
	model := &scriptedLLM{fallback: `{"findings":[{"path":"app.go","line":1,"severity":"warning","class":"correctness","title":"contract failure","rationale":"caller mismatch"}]}`}
	e := newEngine(t, model, &stubProvider{}, nil)
	batch := bundle.Batch{DesignTask: "package:fixture", Assessment: "exact task sentinel", Entries: []bundle.Entry{{File: &diff.File{Path: "app.go", Kind: diff.ChangeModified}, Content: "package app\n"}, {File: &diff.File{Path: "caller.go", Kind: diff.ChangeModified}, Content: "package caller\n", SourceOnly: true}}}
	findings, err := e.analyzeBatch(t.Context(), "Review source", "", batch, false)
	if err != nil || len(findings) != 1 || findings[0].TaskContext == nil {
		t.Fatalf("finding lost its producing task: %v %v", findings, err)
	}
	expert := &scriptedLLM{fallback: `{"verdict":"confirmed","reason":"contract is violated"}`}
	validator := newValidator(expert, config.Validation{Enabled: true})
	_, _, failures := validator.validateWithCoverage(t.Context(), findings, map[string]string{"app.go": "wrong task context"})
	prompt := strings.Join(expert.prompts(), "\n")
	if len(failures) != 0 || expert.callCount() != 1 || !strings.Contains(prompt, strings.TrimSpace(bundle.RenderBatch(batch))) || strings.Contains(prompt, "wrong task context") {
		t.Fatalf("expert saw a different task: failures=%v prompt=%s", failures, prompt)
	}
}

func TestDesignAssemblyRejectsAdditionThatDiffersFromFrozenSource(t *testing.T) {
	cfg := config.Defaults()
	tree := vcs.NewSnapshot(vcs.NewLocal(t.TempDir(), nil), map[string][]byte{"go.mod": []byte("module example.com/app\n\ngo 1.25\n"), "app.go": []byte("package app\nvar Answer = 2\n")})
	file := &diff.File{Path: "app.go", Kind: diff.ChangeAdded, Hunks: []diff.Hunk{{NewStart: 1, NewLines: 2, Lines: []diff.Line{{Kind: diff.LineAdded, Content: "package app", NewLine: 1}, {Kind: diff.LineAdded, Content: "var Answer = 1", NewLine: 2}}}}}
	e := &Engine{Config: cfg, Provider: tree}
	stale := e.assembleDesign(t.Context(), vcs.Ref{Head: vcs.Worktree}, diff.Files{file}, bundle.Reserve{})
	if len(stale.Design.Errors) == 0 || len(stale.Plan.Batches) != 0 {
		t.Fatalf("digest bound source hidden by the addition diff: %+v", stale.Design)
	}
	file.Hunks[0].Lines[1].Content = "var Answer = 2"
	matched := e.assembleDesign(t.Context(), vcs.Ref{Head: vcs.Worktree}, diff.Files{file}, bundle.Reserve{})
	if len(matched.Design.Errors) != 0 || len(matched.Plan.Batches) != 1 || !strings.Contains(bundle.RenderBatch(matched.Plan.Batches[0]), "var Answer = 2") {
		t.Fatalf("consistent source control failed: %+v", matched.Design)
	}
}

func TestEngineeringReviewExecutesEveryTaskAndRetainsBudgetOmissions(t *testing.T) {
	for _, limited := range []bool{false, true} {
		provider := &designReviewProvider{stubProvider: stubProvider{diff: `diff --git a/store/read.go b/store/read.go
--- a/store/read.go
+++ b/store/read.go
@@ -1,2 +1,2 @@
 package store
-func Read() {}
+func Read() { state++ }
`, content: map[string]string{
			"go.mod":             "module example.com/app\n\ngo 1.25\n",
			"store/read.go":      "package store\nfunc Read() { state++ }\n",
			"store/state.go":     "package store\nvar state int\n",
			"service/service.go": "package service\nimport _ \"example.com/app/store\"\n",
		}}}
		model := &scriptedLLM{fallback: `{"findings":[]}`}
		engine := newEngine(t, model, provider, func(cfg *config.Config) {
			cfg.Practices.Profile = "engineering"
			cfg.Persona.Nitpick = config.NitpickNormal
			cfg.Review.Summary = false
			if limited {
				cfg.Review.Budget = config.Budget{MaxSpend: 0.000001, Prices: config.BudgetPrices{Input: 1, Output: 1}, CompletionRatio: 1, Overhead: 1}
			}
		})
		report, err := engine.Review(t.Context(), vcs.Ref{})
		if err != nil || report == nil || report.DesignExecution == nil {
			t.Fatalf("engineering execution unavailable: report=%+v err=%v", report, err)
		}
		tasks := report.DesignExecution.Design.Tasks
		if len(tasks) != 3 || tasks[0].ID != "file:store/read.go" || tasks[0].SourceDigest == "" || len(report.Findings) != 0 || len(report.DesignExecution.Design.Errors) != 0 {
			t.Fatalf("package execution lost its declared evidence: %+v", report)
		}
		for _, task := range tasks {
			if task.SourceDigest == "" || (limited && len(task.Omitted) == 0) || (!limited && !slices.Contains(report.AssessedDesignTasks, task.ID)) {
				t.Fatalf("task lost binding or disposition: %+v", task)
			}
		}
		if limited {
			if model.callCount() != 0 || len(report.Plan.Batches) != 0 || len(report.AssessedDesignTasks) != 0 || len(tasks[0].Omitted) == 0 || report.Budget == nil {
				t.Fatalf("spending omission claimed completion: %+v", report)
			}
			continue
		}
		if model.callCount() != 2 || report.Plan.Files() != 1 || len(report.Plan.Batches) != 2 || len(report.AssessedDesignTasks) != len(tasks) {
			t.Fatalf("empty result lacks actual task execution: %+v; calls=%d", report, model.callCount())
		}
		prompt := strings.Join(model.prompts(), "\n")
		for _, source := range []string{"var state int", "service/service.go", "package:example.com/app/store"} {
			if !strings.Contains(prompt, source) {
				t.Fatalf("executed request lacks %q", source)
			}
		}
	}
}

type designReviewProvider struct {
	stubProvider
}

func (p *designReviewProvider) ListDir(_ context.Context, _ vcs.Ref, dir string) ([]string, error) {
	switch dir {
	case "":
		return []string{"go.mod", "store/", "service/"}, nil
	case "store":
		return []string{"read.go", "state.go"}, nil
	case "service":
		return []string{"service.go"}, nil
	default:
		return nil, vcs.ErrNotFound
	}
}

func TestMergedDesignClaimsKeepBothContextsUnderExpertLimit(t *testing.T) {
	model := &scriptedLLM{fallback: `{"verdict":"confirmed","reason":"both callers violate the contract"}`}
	validator := newValidator(model, config.Validation{Enabled: true})
	first := Finding{Path: "shared.go", Line: 1, Class: "correctness", Title: "contract failure", TaskContext: &TaskContext{ID: "first", Text: "first task exact source", Lines: map[string]int{"shared.go": 3}}}
	second := first
	second.TaskContext = &TaskContext{ID: "second", Text: "second task exact source", Lines: map[string]int{"shared.go": 3, "caller.go": 5}}
	deduplicated := dedupe([]Finding{first, second})
	if len(deduplicated) != 1 {
		t.Fatalf("same claim did not deduplicate: %+v", deduplicated)
	}
	first = deduplicated[0]
	if first.TaskContext == second.TaskContext || first.TaskContext.Lines["caller.go"] != 5 || second.TaskContext.Text != "second task exact source" {
		t.Fatalf("merging mutated or lost task evidence: %+v", first.TaskContext)
	}
	validator.PromptTokenLimit = 1
	kept, _, failures := validator.validateWithCoverage(t.Context(), []Finding{first}, nil)
	if len(kept) != 1 || len(failures) != 1 || model.callCount() != 0 || !strings.Contains(kept[0].Unresolved, "token limit") {
		t.Fatalf("oversized expert evidence lost its claim or ran anyway: kept=%+v failures=%v", kept, failures)
	}
	validator.PromptTokenLimit = 60000
	_, _, failures = validator.validateWithCoverage(t.Context(), []Finding{first}, nil)
	prompt := strings.Join(model.prompts(), "\n")
	if len(failures) != 0 || model.callCount() != 1 || !strings.Contains(prompt, "first task exact source") || !strings.Contains(prompt, "second task exact source") {
		t.Fatalf("expert lost merged task evidence: failures=%v prompt=%s", failures, prompt)
	}
}

func TestDesignAssemblyDoesNotAssignAnUnreadableNestedModuleToItsParent(t *testing.T) {
	cfg := config.Defaults()
	tree := vcs.NewSnapshot(vcs.NewLocal(t.TempDir(), nil), map[string][]byte{
		"go.mod":        []byte("module example.com/outer\n"),
		"nested/go.mod": []byte("module example.com/inner\n"),
		"nested/a.go":   []byte("package inner\n"),
	})
	tree.Unbudgeted = []string{"nested/go.mod"}
	packed := (&Engine{Config: cfg, Provider: tree}).assembleDesign(t.Context(), vcs.Ref{}, diff.Files{&diff.File{Path: "nested/a.go", Kind: diff.ChangeModified}}, bundle.Reserve{})
	if len(packed.Design.Errors) == 0 || len(packed.Design.Tasks) != 1 || packed.Design.Tasks[0].ID != "source:nested/a.go" {
		t.Fatalf("unreadable module inherited an invented package identity: %+v", packed.Design)
	}
	for _, file := range packed.Sources {
		if file.Path == "nested/go.mod" {
			t.Fatal("invented module bytes entered the frozen source")
		}
	}
}

func TestEngineeringReviewValidatesContextOnlyClaimAndPublishesSummary(t *testing.T) {
	for _, cancelTriage := range []bool{false, true} {
		provider := &designReviewProvider{stubProvider: stubProvider{diff: `diff --git a/store/read.go b/store/read.go
--- a/store/read.go
+++ b/store/read.go
@@ -1,2 +1,2 @@
 package store
-func Read() {}
+func Read() { state++ }
`, content: map[string]string{
			"go.mod":             "module example.com/app\n",
			"store/read.go":      "package store\nfunc Read() { state++ }\n",
			"store/state.go":     "package store\nvar state int\n",
			"service/service.go": "package service\nimport _ \"example.com/app/store\"\n",
		}}}
		finding := Finding{Path: "service/service.go", Line: 2, Class: "maintainability", Severity: "warning", Title: "Caller relies on implicit state", Rationale: "The changed read now mutates shared state."}
		model := &scriptedLLM{fallback: `{"findings":[]}`, byPrompt: map[string]string{
			`"id":"file:service/service.go"`: mustJSON(t, Result{Findings: []Finding{finding}}),
		}}
		triageModel := &scriptedLLM{fallback: mustJSON(t, TriageResult{Verdicts: verdictsFor([]Finding{finding})})}
		expert := &scriptedLLM{fallback: `{"verdict":"confirmed","reason":"the caller observes the shared mutation"}`}
		engine := newEngine(t, model, provider, func(cfg *config.Config) {
			cfg.Practices.Profile = "engineering"
			cfg.Validation.Enabled = true
			cfg.Validation.Classes = nil
			cfg.Persona.Nitpick = config.NitpickNormal
		})
		engine.Roles.Triage = llm.NewClientForTest(triageModel, engine.Config.Models.Default)
		engine.Roles.Validate = llm.NewClientForTest(expert, engine.Config.Models.Default)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		if cancelTriage {
			triage := &cancelAfterFirstDesign{cancel: cancel}
			triage.started.Store(1)
			engine.Roles.Triage = llm.NewClientForTest(triage, engine.Config.Models.Default)
		}
		report, err := engine.Review(ctx, vcs.Ref{})
		if cancelTriage {
			if !errors.Is(err, context.Canceled) || report == nil || len(report.Findings) != 1 || report.Findings[0].Title != finding.Title || len(report.AssessedDesignTasks) != len(report.DesignExecution.Design.Tasks) || len(report.Stages) != 1 || report.Stages[0].Stage != "triage" || provider.published != nil {
				t.Fatalf("triage cancellation lost evidence or published: report=%+v err=%v", report, err)
			}
			continue
		}
		if err != nil || len(report.Findings) != 1 || !report.Findings[0].SummaryOnly || len(report.Stages) != 0 || model.callCount() != 2 || triageModel.callCount() != 1 || expert.callCount() != 1 {
			t.Fatalf("context claim did not traverse all stages: report=%+v err=%v calls=%d", report, err, model.callCount())
		}
		if provider.published == nil || len(provider.published.Comments) != 0 || !strings.Contains(provider.published.Summary, finding.Title) {
			t.Fatalf("context claim did not reach summary: %+v", provider.published)
		}
		var expertPrompt string
		for _, request := range expert.prompts() {
			if strings.Contains(request, validationNeedle) {
				expertPrompt = request
			}
		}
		for _, evidence := range []string{"var state int", "service/service.go", "func Read() { state++ }"} {
			if !strings.Contains(expertPrompt, evidence) {
				t.Fatalf("expert missing %q from complete task: %s", evidence, expertPrompt)
			}
		}
	}
}

func TestEngineeringCancellationRetainsCompletedTaskEvidence(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	model := &cancelAfterFirstDesign{scriptedLLM: scriptedLLM{fallback: `{"findings":[]}`}, cancel: cancel}
	tree := vcs.NewSnapshot(vcs.NewLocal(t.TempDir(), nil), map[string][]byte{
		"first/a.py":  []byte("value = 1\n"),
		"first/b.py":  []byte("value = 2\n"),
		"second/a.py": []byte("value = 3\n"),
		"second/b.py": []byte("value = 4\n"),
	})
	engine := newEngine(t, &model.scriptedLLM, tree, func(cfg *config.Config) {
		cfg.Practices.Profile = "engineering"
		cfg.Persona.Nitpick = config.NitpickNormal
		cfg.Review.Concurrency = 1
	})
	engine.Roles.Review = llm.NewClientForTest(model, engine.Config.Models.Default)
	report, err := engine.Review(ctx, vcs.Ref{})
	if !errors.Is(err, context.Canceled) || report == nil || report.DesignExecution == nil || len(report.AssessedDesignTasks) == 0 || len(report.Incomplete) == 0 || len(report.Stages) == 0 {
		t.Fatalf("cancellation lost partial execution: report=%+v err=%v", report, err)
	}
	if len(report.DesignExecution.Design.Tasks) < 2 || len(report.Plan.Batches) < 2 || model.callCount() != 1 {
		t.Fatalf("cancellation invented or removed intended work: %+v calls=%d", report, model.callCount())
	}
	completed := slices.Clone(report.AssessedDesignTasks)
	slices.Sort(completed)
	matchingRequests := 0
	for _, batch := range report.Plan.Batches {
		ids := slices.Clone(batch.DesignTaskIDs())
		slices.Sort(ids)
		if slices.Equal(ids, completed) {
			matchingRequests++
		}
	}
	if matchingRequests != 1 {
		t.Fatalf("completion does not match exactly one successful request: %v", completed)
	}
	for _, id := range completed {
		found := false
		for _, task := range report.DesignExecution.Design.Tasks {
			if task.ID == id {
				found = task.SourceDigest != "" && len(task.Omitted) == 0
			}
		}
		if !found {
			t.Fatalf("completed task %q lacks intended evidence", id)
		}
	}
}

type cancelAfterFirstDesign struct {
	scriptedLLM
	started atomic.Int32
	cancel  context.CancelFunc
}

func (m *cancelAfterFirstDesign) GenerateContent(ctx context.Context, messages []llms.Message, options ...llms.CallOption) (*llms.Response, error) {
	if m.started.Add(1) > 1 {
		m.cancel()
		return nil, context.Canceled
	}
	return m.scriptedLLM.GenerateContent(ctx, messages, options...)
}

func TestDesignBatchKeepsSupportingSpansThroughExpertValidation(t *testing.T) {
	model := &scriptedLLM{fallback: `{"findings":[{"path":"caller.go","line":3,"severity":"warning","class":"correctness","title":"contract failure","rationale":"caller mismatch"}]}`}
	e := newEngine(t, model, &stubProvider{}, nil)
	batch := bundle.Batch{DesignTask: "package:fixture", Entries: []bundle.Entry{
		{File: &diff.File{Path: "app.go"}, Content: "package app\n"},
		{File: &diff.File{Path: "caller.go"}, Content: "package caller\nHIDDEN SENTINEL\nfunc Call() {}\n", SourceOnly: true, SourceSpans: []bundle.SourceSpan{{Start: 1, End: 1}, {Start: 3, End: 3}}},
	}}
	findings, err := e.analyzeBatch(t.Context(), "Review source", "", batch, false)
	if err != nil || len(findings) != 1 || model.callCount() != 1 {
		t.Fatalf("review failed: %v %v", findings, err)
	}
	if _, whole := findings[0].TaskContext.Lines["caller.go"]; whole {
		t.Fatal("span evidence promoted to whole file")
	}
	kept, _, unpublished := e.filterTaskAnchors(findings, nil)
	if len(kept) != 1 || len(unpublished) != 0 {
		t.Fatalf("valid supplied line rejected: %v %v", kept, unpublished)
	}
	expert := &scriptedLLM{fallback: `{"verdict":"confirmed","reason":"contract is violated"}`}
	validator := newValidator(expert, config.Validation{Enabled: true})
	_, _, failures := validator.validateWithCoverage(t.Context(), kept, map[string]string{"caller.go": "HIDDEN SENTINEL"})
	if len(failures) != 0 || expert.callCount() != 1 {
		t.Fatalf("expert did not validate: %v", failures)
	}
	for _, prompts := range [][]string{model.prompts(), expert.prompts()} {
		text := strings.Join(prompts, "\n")
		if strings.Contains(text, "HIDDEN SENTINEL") || !strings.Contains(text, "     3  func Call() {}") {
			t.Fatal("model evidence leaked or lost original line numbers")
		}
	}
	findings[0].Line = 2
	kept, _, unpublished = e.filterTaskAnchors(findings, nil)
	if len(kept) != 0 || len(unpublished) != 1 {
		t.Fatal("unseen gap passed engine evidence validation")
	}
}

func TestSharedDesignRequestCompletesEveryTaskOnlyAfterSuccess(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			model := &scriptedLLM{fallback: `{"findings":[]}`}
			if fail {
				model.err = errors.New("provider unavailable")
			}
			e := newEngine(t, model, &stubProvider{}, nil)
			batch := bundle.Batch{DesignTask: "first", AdditionalDesignTasks: []string{"second"}, Assessment: "first and second obligations", Entries: []bundle.Entry{{File: &diff.File{Path: "app.go"}, Content: "package app\n"}}}
			findings, missing, _, err := e.analyze(t.Context(), &vcs.PullRequest{}, &bundle.Plan{Batches: []bundle.Batch{batch}}, make(chan struct{}, 1))
			if len(findings) != 0 || model.callCount() == 0 {
				t.Fatalf("control did not execute: findings=%v calls=%d", findings, model.callCount())
			}
			if fail {
				if err == nil || len(missing) == 0 || len(e.assessedDesignTasks) != 0 {
					t.Fatalf("failed request claimed assessment: %v %v %v", err, missing, e.assessedDesignTasks)
				}
			} else if err != nil || len(missing) != 0 || !slices.Equal(e.assessedDesignTasks, []string{"first", "second"}) || model.callCount() != 1 {
				t.Fatalf("shared completion lost tasks or repeated requests: %v %v %v", err, missing, e.assessedDesignTasks)
			}
		})
	}
}

func TestTreeAssemblySeparatesRoutineExclusionsFromUnreadableSource(t *testing.T) {
	cfg := config.Defaults()
	tree := vcs.NewSnapshot(vcs.NewLocal(t.TempDir(), nil), map[string][]byte{"go.mod": []byte("module example.com/app\n"), "app.go": []byte("package app\nfunc Read(){}\n")})
	tree.Skipped = []vcs.TreeSkip{{Path: "asset.bin", Reason: "binary"}, {Path: "empty.txt", Reason: "empty"}}
	e := &Engine{Config: cfg, Provider: tree}
	files := diff.Files{&diff.File{Path: "app.go", Kind: diff.ChangeModified}}
	packed := e.assembleDesign(t.Context(), vcs.Ref{Head: vcs.Worktree}, files, bundle.Reserve{})
	if len(packed.Design.Errors) != 0 || len(packed.Plan.Batches) != 1 || len(packed.Excluded) != 2 {
		t.Fatalf("routine exclusions became failed scope: %+v", packed)
	}
	for _, skip := range tree.Skipped {
		if !slices.Contains(packed.Excluded, bundle.Skip{Path: skip.Path, Reason: skip.Reason}) {
			t.Fatalf("exclusion disappeared: %s", skip.Path)
		}
	}
	tree.Skipped = append(tree.Skipped, vcs.TreeSkip{Path: "unreadable.go", Reason: "unreadable: permission denied"})
	incomplete := e.assembleDesign(t.Context(), vcs.Ref{Head: vcs.Worktree}, files, bundle.Reserve{})
	if len(incomplete.Design.Errors) == 0 || len(incomplete.Excluded) != 2 {
		t.Fatalf("unreadable source became routine: %+v", incomplete)
	}
}

func TestEmptyModuleManifestCannotBecomeRoutineTreeExclusion(t *testing.T) {
	cfg := config.Defaults()
	tree := vcs.NewSnapshot(vcs.NewLocal(t.TempDir(), nil), map[string][]byte{"go.mod": []byte("module example.com/outer\n"), "nested/a.go": []byte("package inner\nfunc Read(){}\n")})
	tree.Skipped = []vcs.TreeSkip{{Path: "nested/go.mod", Reason: "empty"}}
	packed := (&Engine{Config: cfg, Provider: tree}).assembleDesign(t.Context(), vcs.Ref{Head: vcs.Worktree}, diff.Files{&diff.File{Path: "nested/a.go", Kind: diff.ChangeModified}}, bundle.Reserve{})
	if len(packed.Design.Errors) == 0 || len(packed.Excluded) != 0 || len(packed.Design.Tasks) != 1 || packed.Design.Tasks[0].ID != "source:nested/a.go" {
		t.Fatalf("empty manifest invented a parent package: %+v", packed)
	}
}
