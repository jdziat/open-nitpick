package review

import (
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/bundle"
	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/diff"
	"github.com/jdziat/open-nitpick/internal/vcs"
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
	if len(packed.Design.Errors) != 0 || len(packed.Plan.Batches) != 1 {
		t.Fatalf("assembly=%+v", packed.Design)
	}
	rendered := bundle.RenderBatch(packed.Plan.Batches[0])
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
