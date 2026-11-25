package review

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/nitpick-oss/nitpick/internal/llm"
	"github.com/nitpick-oss/nitpick/internal/vcs"
)

func critiquedReview(t *testing.T, on bool, grades ...string) (*Report, []Finding) {
	t.Helper()
	fs := confident(twoFindings(), 0.9)
	reviewer := &deepLLM{ordinary: mustJSON(t, Result{Findings: fs}), grades: gradeAll(t, grades...)}
	e, _ := verifyEngine(t, &toolLLM{}, "")
	e.Roles.Review = llm.NewClientForTest(reviewer, e.Config.Models.Default)
	e.Config.Review.Critique = &on
	report, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	return report, fs
}

func TestReviewCritiqueWithholdsTheFindingItGradesUnsupported(t *testing.T) {
	report, fs := critiquedReview(t, true, "proven", "unsupported")
	if got := titles(report); len(got) != 1 || got[0] != fs[0].Title {
		t.Fatalf("published %v, want only %q", got, fs[0].Title)
	}
	var held []string
	for _, o := range report.Overruled {
		if o.Expert == deepCritiqueExpert {
			held = append(held, o.Finding.Title)
		}
	}
	if len(held) != 1 || held[0] != fs[1].Title {
		t.Errorf("the critique withheld %v, want only %q", held, fs[1].Title)
	}
}

func TestReviewCritiqueOffPublishesFindingsTheCritiqueWouldCut(t *testing.T) {
	report, _ := critiquedReview(t, false, "proven", "unsupported")
	if got := titles(report); len(got) != 2 {
		t.Errorf("published %v with review.critique off, want both findings", got)
	}
}

// A critique that cannot run has judged nothing. The review keeps every finding
// and names the stage, so a run whose filter did not work does not read as one
// whose filter found nothing to remove.
func TestReviewCritiqueThatFailsKeepsEveryFindingAndReportsTheStage(t *testing.T) {
	fs := confident(twoFindings(), 0.9)
	reviewer := &deepLLM{ordinary: mustJSON(t, Result{Findings: fs}), critiqueErr: errors.New("upstream 503")}
	e, _ := verifyEngine(t, &toolLLM{}, "")
	e.Roles.Review = llm.NewClientForTest(reviewer, e.Config.Models.Default)
	on := true
	e.Config.Review.Critique = &on
	report, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if got := titles(report); len(got) != 2 {
		t.Errorf("published %v after the critique failed, want both findings", got)
	}
	if !slices.Contains(report.FailedStages(), "critique") {
		t.Errorf("failed stages = %v, want the critique named", report.FailedStages())
	}
}
