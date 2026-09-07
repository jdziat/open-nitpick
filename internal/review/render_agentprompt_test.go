package review

import (
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/bundle"
	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/diff"
)

func agentFinding() Finding {
	return Finding{
		Path: "internal/a.go", Line: 42, EndLine: 58,
		AlsoAt:    []LineSpan{{Line: 19}},
		Severity:  "warning",
		Class:     "correctness",
		Category:  "correctness",
		Title:     "Guard removed that a caller depends on",
		Rationale: "b.go calls this with a nil map on the error path.",
		Source:    "openrouter/qwen",
	}
}

// The block carries what an agent would otherwise re-derive, and nothing the
// review did not establish.
func TestTheAgentPromptCarriesTheAnchorSpansAndWhatTheReviewerRead(t *testing.T) {
	read := map[string][]string{"internal/a.go": {"internal/b.go", "internal/c.go"}}

	body := renderComment(agentFinding(), true, read)

	for _, want := range []string{
		"<details><summary>Fix prompt</summary>",
		"anchor:    internal/a.go:42-58",
		"also:      internal/a.go:19",
		"class:     correctness",
		"severity:  warning",
		"found by:  openrouter/qwen",
		"also read: internal/b.go, internal/c.go",
		"b.go calls this with a nil map on the error path.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the block is missing %q:\n%s", want, body)
		}
	}
}

// Off by default, and absent rather than empty when off.
func TestTheAgentPromptIsAbsentUnlessAskedFor(t *testing.T) {
	if body := renderComment(agentFinding(), true, nil); strings.Contains(body, "Fix prompt") {
		t.Errorf("a block was rendered with no prompt requested:\n%s", body)
	}

	cfg := config.Defaults()
	if cfg.Review.AgentPrompt {
		t.Error("review.agent_prompt defaults to on")
	}
}

// batchMates is what the reviewer had in front of it, taken from the plan
// rather than recomputed, and a file is never its own mate.
func TestBatchMatesNamesTheRestOfTheBatch(t *testing.T) {
	f := func(p string) *diff.File { return &diff.File{Path: p, Kind: diff.ChangeModified} }
	plan := &bundle.Plan{Batches: []bundle.Batch{
		{Entries: []bundle.Entry{{File: f("a.go")}, {File: f("b.go")}, {File: f("c.go")}}},
		{Entries: []bundle.Entry{{File: f("solo.go")}}},
	}}

	mates := batchMates(plan)

	if got := mates["a.go"]; len(got) != 2 || got[0] != "b.go" || got[1] != "c.go" {
		t.Errorf("a.go mates = %v", got)
	}
	if got := mates["solo.go"]; len(got) != 0 {
		t.Errorf("a file alone in its batch has mates %v", got)
	}
	if batchMates(nil) != nil {
		t.Error("a nil plan produced a map")
	}
}

// A finding with a one-click suggestion still gets the block: the suggestion
// does not say where else the finding reaches or what the reviewer read.
func TestAFindingWithASuggestionStillCarriesTheBlock(t *testing.T) {
	f := agentFinding()
	f.Suggestion = "if m == nil { return nil }"
	f.FixValidated = true

	body := renderComment(f, true, map[string][]string{"internal/a.go": {"internal/b.go"}})

	if !strings.Contains(body, "suggestion") {
		t.Error("the suggestion block was dropped")
	}
	if !strings.Contains(body, "Fix prompt") {
		t.Error("the fix prompt was suppressed by the suggestion")
	}
}

// Title and Rationale are model-authored and can quote the diff under review.
// A triple backtick in either must not close the block's fence early and spill
// the rest, the closing tag included, as rendered markdown.
func TestTheAgentPromptFenceOutlivesBackticksInModelText(t *testing.T) {
	f := agentFinding()
	f.Rationale = "the guard reads:\n```go\nif m == nil { return }\n```\nand b.go depends on it"

	body := renderComment(f, true, map[string][]string{"internal/a.go": {"internal/b.go"}})

	// The closing tag survives, which it cannot if the fence was broken.
	if !strings.Contains(body, "</details>") {
		t.Fatalf("the block did not close:\n%s", body)
	}

	// The fence is wider than anything inside it.
	start := strings.Index(body, "<details><summary>Fix prompt</summary>")
	block := body[start:]
	fence := block[strings.Index(block, "`"):]
	fence = fence[:strings.IndexFunc(fence, func(r rune) bool { return r != '`' })]
	if len(fence) < 4 {
		t.Errorf("fence is %d backticks against content holding three:\n%s", len(fence), body)
	}
}

// An analyzer read one file and knows nothing of the batch, so naming its
// mates would assert reading that did not happen.
func TestAnAnalyzerFindingDoesNotClaimItReadTheBatch(t *testing.T) {
	f := agentFinding()
	f.FromAnalyzer = true
	f.Source = "golangci-lint(errcheck)"

	body := renderComment(f, true, map[string][]string{"internal/a.go": {"internal/b.go"}})

	if strings.Contains(body, "also read:") {
		t.Errorf("an analyzer finding claimed it read the batch:\n%s", body)
	}
	if !strings.Contains(body, "found by:  golangci-lint(errcheck)") {
		t.Errorf("the analyzer is not named:\n%s", body)
	}
}
