package review

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/jdziat/open-nitpick/internal/bundle"
	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/diff"
	"github.com/jdziat/open-nitpick/internal/llm"
	"github.com/jdziat/open-nitpick/internal/vcs"
	llms "github.com/nocturnium/llm-go-sdk/v6"
)

func entries(tokens ...int) bundle.Batch {
	b := bundle.Batch{}
	for i, n := range tokens {
		b.Entries = append(b.Entries, bundle.Entry{File: &diff.File{Path: string(rune('a'+i)) + ".go"}, Tokens: n})
		b.Tokens += n
	}
	return b
}

func TestHalvesCutAtTheTokenMidpointAndNeverLeaveASideEmpty(t *testing.T) {
	for name, tc := range map[string]struct {
		tokens     []int
		left, rite int
	}{
		"even":            {[]int{10, 10, 10, 10}, 2, 2},
		"one heavy first": {[]int{90, 5, 5}, 1, 2},
		"one heavy last":  {[]int{5, 5, 90}, 2, 1},
		"two":             {[]int{1, 1}, 1, 1},
		"all in one half": {[]int{100, 1}, 1, 1},
	} {
		l, r := halves(entries(tc.tokens...))
		if len(l.Entries) != tc.left || len(r.Entries) != tc.rite {
			t.Errorf("%s: split %d/%d, want %d/%d", name, len(l.Entries), len(r.Entries), tc.left, tc.rite)
		}
		if l.Tokens+r.Tokens != entries(tc.tokens...).Tokens {
			t.Errorf("%s: halves lose tokens", name)
		}
	}
	if splittable(entries(5)) {
		t.Error("a single file has nothing to halve")
	}
	design := entries(5, 5)
	design.DesignTask = "task"
	if splittable(design) {
		t.Error("a design assessment is one indivisible request")
	}
}

// tooMuchLLM cannot answer a request that names both files, as a model whose
// output cap a long batch overran cannot, and answers each file on its own.
type tooMuchLLM struct {
	mu     sync.Mutex
	whole  int
	byFile int
}

func (m *tooMuchLLM) GenerateContent(_ context.Context, msgs []llms.Message, _ ...llms.CallOption) (*llms.Response, error) {
	var b strings.Builder
	for _, msg := range msgs {
		b.WriteString(msg.Content)
	}
	text := b.String()
	m.mu.Lock()
	defer m.mu.Unlock()
	if strings.Contains(text, "triaging findings") {
		return &llms.Response{Content: `{"summary":"","verdicts":[],"dropped":[]}`}, nil
	}
	if strings.Contains(text, "a.go") && strings.Contains(text, "b.go") {
		m.whole++
		return &llms.Response{Content: "the answer ran out of room and was cut mid"}, nil
	}
	m.byFile++
	return &llms.Response{Content: `{"findings":[]}`}, nil
}

func (m *tooMuchLLM) Stream(context.Context, []llms.Message, ...llms.CallOption) (<-chan llms.StreamChunk, error) {
	return nil, nil
}
func (m *tooMuchLLM) Provider() llms.Provider { return "scripted" }
func (m *tooMuchLLM) Model() string           { return "scripted" }

const splitDiff = `diff --git a/a.go b/a.go
--- a/a.go
+++ b/a.go
@@ -0,0 +1,2 @@
+package app
+var A = 1
diff --git a/b.go b/b.go
--- a/b.go
+++ b/b.go
@@ -0,0 +1,2 @@
+package app
+var B = 1
`

func TestABatchTheModelCannotAnswerWholeIsReviewedInHalvesNotDropped(t *testing.T) {
	model := &tooMuchLLM{}
	e := newEngine(t, &scriptedLLM{}, &stubProvider{diff: splitDiff}, func(c *config.Config) {
		c.Review.TokenBudgetPerRequest = 1_000_000
	})
	e.Roles.Review = llm.NewClientForTest(model, e.Config.Models.Default)

	report, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if model.whole == 0 {
		t.Fatal("the files were never sent together, so this test did not exercise the split")
	}
	if len(report.Incomplete) != 0 {
		t.Errorf("files left unreviewed after halving: %v", report.Incomplete)
	}
	if model.byFile < 2 {
		t.Errorf("single-file requests = %d, want one per half", model.byFile)
	}
}
