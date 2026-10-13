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

// hunkyFile is one file whose diff has a hunk per marker, so a model can be
// scripted to refuse any request that names more than one of them.
func hunkyFile(markers ...string) bundle.Batch {
	f := &diff.File{Path: "big.go", Kind: diff.ChangeModified}
	for i, m := range markers {
		f.Hunks = append(f.Hunks, diff.Hunk{
			NewStart: 10*i + 1, NewLines: 1,
			Lines: []diff.Line{{Kind: diff.LineAdded, NewLine: 10*i + 1, Content: "var " + m + " = 1"}},
		})
	}
	e := bundle.Entry{File: f, Tokens: 100}
	return bundle.Batch{Entries: []bundle.Entry{e}, Tokens: 100}
}

func TestALoneFileSplitsOnlyWhereItsDiffHasTwoHunks(t *testing.T) {
	if splittable(hunkyFile("A")) {
		t.Error("a one-hunk file is the smallest request there is")
	}
	if !splittable(hunkyFile("A", "B")) {
		t.Error("a two-hunk file should split between its hunks")
	}
	design := hunkyFile("A", "B")
	design.DesignTask = "task"
	if splittable(design) {
		t.Error("a design assessment stays one request however many hunks it has")
	}
	src := hunkyFile("A", "B")
	src.Entries[0].SourceOnly = true
	if splittable(src) {
		t.Error("supporting source has no diff to cut")
	}
}

func TestHalvingALoneFileKeepsEveryHunkExactlyOnce(t *testing.T) {
	for name, markers := range map[string][]string{
		"two":   {"A", "B"},
		"three": {"A", "B", "C"},
		"five":  {"A", "B", "C", "D", "E"},
	} {
		l, r := halves(hunkyFile(markers...))
		var got []string
		for _, side := range []bundle.Batch{l, r} {
			if len(side.Entries) != 1 || len(side.Entries[0].File.Hunks) == 0 {
				t.Fatalf("%s: a half is empty or lost its file: %+v", name, side)
			}
			if side.Entries[0].File.Path != "big.go" {
				t.Errorf("%s: half names %q", name, side.Entries[0].File.Path)
			}
			for _, h := range side.Entries[0].File.Hunks {
				got = append(got, strings.TrimSuffix(strings.TrimPrefix(h.Lines[0].Content, "var "), " = 1"))
			}
			if side.Tokens <= 0 || side.Tokens != side.Entries[0].Tokens {
				t.Errorf("%s: half tokens %d vs entry %d", name, side.Tokens, side.Entries[0].Tokens)
			}
		}
		if strings.Join(got, ",") != strings.Join(markers, ",") {
			t.Errorf("%s: halves hold %v, want %v in order", name, got, markers)
		}
	}
}

func TestHalvingALoneFileLeavesTheOriginalDiffUntouched(t *testing.T) {
	b := hunkyFile("A", "B", "C")
	halves(b)
	if n := len(b.Entries[0].File.Hunks); n != 3 {
		t.Errorf("halving rewrote the caller's file: %d hunks left, want 3", n)
	}
}

// refusesTwoHunks cannot answer a request that carries both A and B, and
// answers any request with one of them.
type refusesTwoHunks struct {
	mu     sync.Mutex
	whole  int
	single int
}

func (m *refusesTwoHunks) GenerateContent(_ context.Context, msgs []llms.Message, _ ...llms.CallOption) (*llms.Response, error) {
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
	if strings.Contains(text, "var A") && strings.Contains(text, "var B") {
		m.whole++
		return &llms.Response{Content: "", FinishReason: llms.FinishReasonLength}, nil
	}
	m.single++
	return &llms.Response{Content: `{"findings":[]}`}, nil
}

func (m *refusesTwoHunks) Stream(context.Context, []llms.Message, ...llms.CallOption) (<-chan llms.StreamChunk, error) {
	return nil, nil
}
func (m *refusesTwoHunks) Provider() llms.Provider { return "scripted" }
func (m *refusesTwoHunks) Model() string           { return "scripted" }

const twoHunkDiff = `diff --git a/big.go b/big.go
--- a/big.go
+++ b/big.go
@@ -1,2 +1,3 @@
 package app
+var A = 1
 var X = 0
@@ -40,2 +41,3 @@
 var Y = 0
+var B = 1
 var Z = 0
`

func TestALoneFileTheModelCannotAnswerWholeIsReviewedByHunkNotDropped(t *testing.T) {
	model := &refusesTwoHunks{}
	e := newEngine(t, &scriptedLLM{}, &stubProvider{diff: twoHunkDiff}, func(c *config.Config) {
		c.Review.TokenBudgetPerRequest = 1_000_000
	})
	e.Roles.Review = llm.NewClientForTest(model, e.Config.Models.Default)

	report, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if model.whole == 0 {
		t.Fatal("the file was never sent whole, so this test did not exercise the split")
	}
	if len(report.Incomplete) != 0 {
		t.Errorf("a file left unreviewed after halving by hunk: %v", report.Incomplete)
	}
	if model.single < 2 {
		t.Errorf("per-hunk requests = %d, want one per half", model.single)
	}
}

// A file with one hunk cannot be cut smaller. Zero findings from it would read
// as clean, so it has to be reported as not reviewed.
func TestAOneHunkFileTheModelCannotAnswerIsReportedUnreviewedNotClean(t *testing.T) {
	model := &refusesEverything{}
	e := newEngine(t, &scriptedLLM{}, &stubProvider{diff: oneHunkDiff}, func(c *config.Config) {
		c.Review.TokenBudgetPerRequest = 1_000_000
	})
	e.Roles.Review = llm.NewClientForTest(model, e.Config.Models.Default)

	report, err := e.Review(context.Background(), vcs.Ref{})
	if err == nil && len(report.Incomplete) == 0 {
		t.Fatal("an unanswerable single-hunk file produced a clean report")
	}
}

type refusesEverything struct{}

func (refusesEverything) GenerateContent(context.Context, []llms.Message, ...llms.CallOption) (*llms.Response, error) {
	return &llms.Response{Content: "", FinishReason: llms.FinishReasonLength}, nil
}
func (refusesEverything) Stream(context.Context, []llms.Message, ...llms.CallOption) (<-chan llms.StreamChunk, error) {
	return nil, nil
}
func (refusesEverything) Provider() llms.Provider { return "scripted" }
func (refusesEverything) Model() string           { return "scripted" }

const oneHunkDiff = `diff --git a/one.go b/one.go
--- a/one.go
+++ b/one.go
@@ -1,2 +1,3 @@
 package app
+var A = 1
 var X = 0
`
