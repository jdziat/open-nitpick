package review

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	llms "github.com/nocturnium/llm-go-sdk/v6"

	"github.com/jdziat/open-nitpick/internal/bundle"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

func TestGenerationResumeInvalidatesEmptyAnswerWhenCapabilitiesChange(t *testing.T) {
	for _, change := range []string{"search available", "reviewed ref changed"} {
		t.Run(change, func(t *testing.T) {
			model := &boundedTestModel{respond: func(_ int, _ []llms.Message, _ llms.CallOptions) (*llms.Response, error) {
				return &llms.Response{ToolCalls: []llms.ToolCall{boundedCall("submit", "submit_findings", `{"findings":[]}`)}}, nil
			}}
			e, client, _, tools := boundedTestEngine(t, model, &stubProvider{})
			e.Config.Review.GenerationLookups = true
			e.lookups = tools
			e.progress = &reviewProgress{current: map[string]json.RawMessage{}, rank: map[string]int{}}
			for run := range 3 {
				if run > 0 {
					e.progress.prior = e.progress.current
					e.progress.current = map[string]json.RawMessage{}
				}
				if run == 2 {
					switch change {
					case "search available":
						e.lookups = newRepoTools(gitRepo(t, map[string]string{"a.go": "package a"}), e.Provider, tools.ref)
					case "reviewed ref changed":
						e.lookups = newRepoTools("", e.Provider, vcs.Ref{Head: "another-commit"})
					}
				}
				findings, err := e.analyzeBatchWith(t.Context(), client, "system", "", bundle.Batch{}, false)
				wantCalls := 1
				if run == 2 {
					wantCalls = 2
				}
				if err != nil || len(findings) != 0 || model.n != wantCalls {
					t.Fatalf("run %d: findings=%+v calls=%d want=%d err=%v", run, findings, model.n, wantCalls, err)
				}
			}
		})
	}
}

func TestGenerationResumeRechecksEvidenceBeyondTheVisiblePrefix(t *testing.T) {
	root := gitRepo(t, map[string]string{"sibling.go": strings.Repeat("x", 7000) + "old"})
	e, _ := verifyEngine(t, &toolLLM{}, "")
	e.progress = &reviewProgress{current: map[string]json.RawMessage{}, rank: map[string]int{}}
	e.lookups = newRepoTools(root, nil, vcs.Ref{})
	args := map[string]any{"path": "sibling.go", "start_line": 1, "end_line": 1}
	answer, ok := runLookup(t.Context(), e.lookups, "read_file", args)
	if !ok || !strings.HasSuffix(answer, "old\n") {
		t.Fatalf("fixture did not expose complete evidence: %q", answer)
	}
	saved := storedGeneration{Complete: true, Lookups: []verdictLookup{{
		Tool: "read_file", Arguments: `{"path":"sibling.go","start_line":1,"end_line":1}`, Digest: boundedLookupDigest(answer),
	}}}
	saveStage(e.progress, "key", rankAnalysis, saved)
	e.progress.prior = e.progress.current
	e.progress.current = map[string]json.RawMessage{}
	e.lookups = newRepoTools(root, nil, vcs.Ref{})
	if _, reused := e.reuseGeneration(t.Context(), "key"); !reused {
		t.Fatal("unchanged evidence was not reused")
	}
	if err := os.WriteFile(filepath.Join(root, "sibling.go"), []byte(strings.Repeat("x", 7000)+"new"), 0o600); err != nil {
		t.Fatal(err)
	}
	e.lookups = newRepoTools(root, nil, vcs.Ref{})
	if _, reused := e.reuseGeneration(t.Context(), "key"); reused {
		t.Fatal("changed evidence outside the old digest prefix reused stale findings")
	}
}

func TestGenerationResumeRejectsIncompleteOrMalformedEvidence(t *testing.T) {
	root := gitRepo(t, map[string]string{"a.go": "package a"})
	e, _ := verifyEngine(t, &toolLLM{}, "")
	e.lookups = newRepoTools(root, nil, vcs.Ref{})
	for _, stored := range []storedGeneration{
		{Complete: false},
		{Complete: true, Lookups: []verdictLookup{{Tool: "read_file", Arguments: "{"}}},
		{Complete: true, Lookups: []verdictLookup{{Tool: "unknown", Arguments: "{}"}}},
	} {
		data, err := json.Marshal(stored)
		if err != nil {
			t.Fatal(err)
		}
		e.progress = &reviewProgress{prior: map[string]json.RawMessage{"key": data}, current: map[string]json.RawMessage{}, rank: map[string]int{}}
		if _, reused := e.reuseGeneration(t.Context(), "key"); reused {
			t.Fatalf("invalid evidence reused: %+v", stored)
		}
	}
}
