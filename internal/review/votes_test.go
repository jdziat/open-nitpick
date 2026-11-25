package review

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	llms "github.com/nocturnium/llm-go-sdk/v6"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/llm"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

func sure(f Finding, confidence float64) Finding {
	f.Confidence = &confidence
	return f
}

func reported(path string, line int, title string) Finding {
	return Finding{Path: path, Line: line, Severity: "error", Category: "correctness", Class: "correctness",
		Title: title, Rationale: "the title says it"}
}

func TestVotingKeepsAFindingMostRunsReportAndDropsOneOnlyOneDid(t *testing.T) {
	a := reported("a.go", 10, "Nil map write in cache fill")
	b := reported("b.go", 40, "Lock released before the read completes")
	kept := voteFindings([][]Finding{{a, b}, {a}, {a}}, 2)
	if len(kept) != 1 || kept[0].Path != "a.go" {
		t.Fatalf("kept %+v, want only the finding all three runs reported", kept)
	}
}

func TestVotingCountsARunOnceHoweverOftenItReportsTheSameDefect(t *testing.T) {
	a := reported("a.go", 10, "Nil map write in cache fill")
	again := reported("a.go", 11, "Nil map write in the cache fill path")
	if kept := voteFindings([][]Finding{{a, again}, {}, {}}, 2); len(kept) != 0 {
		t.Fatalf("kept %+v, one run reporting a defect twice is not a majority", kept)
	}
}

func TestVotingKeepsTheMostConfidentReportOfADefect(t *testing.T) {
	low := sure(reported("a.go", 10, "Nil map write in cache fill"), 0.6)
	high := sure(reported("a.go", 10, "Nil map write in cache fill"), 0.9)
	kept := voteFindings([][]Finding{{low}, {high}, {}}, 2)
	if len(kept) != 1 || confidenceOf(kept[0]) != 0.9 {
		t.Fatalf("kept %+v, want the report made with confidence 0.9", kept)
	}
}

// roundsLLM answers each generation call with the next scripted result, in
// arrival order, which the tests make irrelevant by scripting symmetric votes.
type roundsLLM struct {
	mu      sync.Mutex
	answers []string
	failing int
	calls   int
}

func (r *roundsLLM) GenerateContent(_ context.Context, msgs []llms.Message, _ ...llms.CallOption) (*llms.Response, error) {
	var prompt strings.Builder
	for _, m := range msgs {
		prompt.WriteString(m.Content)
	}
	if !strings.Contains(prompt.String(), "Review the following changes") {
		return &llms.Response{Content: "{}"}, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	i := r.calls
	r.calls++
	if i < r.failing {
		return nil, errors.New("upstream 503")
	}
	return &llms.Response{Content: r.answers[(i-r.failing)%len(r.answers)]}, nil
}

func (r *roundsLLM) Stream(context.Context, []llms.Message, ...llms.CallOption) (<-chan llms.StreamChunk, error) {
	return nil, errors.New("not streamed")
}

func (r *roundsLLM) Provider() llms.Provider { return "scripted" }
func (r *roundsLLM) Model() string           { return "scripted" }

func votedReview(t *testing.T, runs int, model *roundsLLM) (*Report, error) {
	t.Helper()
	e, _ := verifyEngine(t, &toolLLM{}, "")
	e.Roles.Review = llm.NewClientForTest(model, e.Config.Models.Default)
	e.Config.Review.Votes = runs
	e.SkipTriage = true
	return e.Review(context.Background(), vcs.Ref{})
}

func TestVotedReviewKeepsWhatAMajorityOfRunsReported(t *testing.T) {
	fs := twoFindings()
	model := &roundsLLM{answers: []string{
		mustJSON(t, Result{Findings: fs}),
		mustJSON(t, Result{Findings: fs[:1]}),
		mustJSON(t, Result{Findings: fs[:1]}),
	}}
	report, err := votedReview(t, 3, model)
	if err != nil {
		t.Fatal(err)
	}
	if model.calls != 3 {
		t.Errorf("generation ran %d times, want 3", model.calls)
	}
	if got := titles(report); len(got) != 1 || got[0] != fs[0].Title {
		t.Errorf("published %v, want only %q", got, fs[0].Title)
	}
}

func TestVotesOfOneAndZeroReviewOnce(t *testing.T) {
	for _, runs := range []int{0, 1} {
		model := &roundsLLM{answers: []string{mustJSON(t, Result{Findings: twoFindings()})}}
		report, err := votedReview(t, runs, model)
		if err != nil {
			t.Fatal(err)
		}
		if model.calls != 1 || len(report.Findings) != 2 {
			t.Errorf("votes %d: %d generation calls and %d findings, want 1 and 2", runs, model.calls, len(report.Findings))
		}
	}
}

// Two of three runs failing leaves one answer, which cannot make a majority.
// Publishing nothing from that would read as a clean batch.
func TestVotedBatchFailsWhenTooFewRunsAnswerToMakeAMajority(t *testing.T) {
	model := &roundsLLM{failing: 2, answers: []string{mustJSON(t, Result{Findings: twoFindings()})}}
	report, err := votedReview(t, 3, model)
	if err == nil {
		t.Fatalf("a batch whose majority could not answer was reported as reviewed: %d findings", len(report.Findings))
	}
}

func TestVotedBatchSurvivesOneFailedRunWhenTheRestAgree(t *testing.T) {
	fs := twoFindings()
	model := &roundsLLM{failing: 1, answers: []string{mustJSON(t, Result{Findings: fs[:1]})}}
	report, err := votedReview(t, 3, model)
	if err != nil {
		t.Fatal(err)
	}
	if got := titles(report); len(got) != 1 {
		t.Errorf("published %v, want the finding both surviving runs reported", got)
	}
}

func TestVotesBeyondTheCapAreRejected(t *testing.T) {
	cfg := config.Defaults()
	cfg.Models.Default = config.ModelSpec{Provider: "openai", Model: "gpt-4o"}
	cfg.Review.Votes = 6
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "review.votes") {
		t.Fatalf("validate = %v, want review.votes rejected", err)
	}
}
