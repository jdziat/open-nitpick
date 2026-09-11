package review

import (
	"context"
	"testing"

	"github.com/jdziat/open-nitpick/internal/bundle"
	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/practices"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

func TestPracticeAssessmentSeesRoutesBeforePublication(t *testing.T) {
	provider := &stubProvider{}
	engine := &Engine{Config: config.Defaults(), Provider: provider, routeDecisions: []RouteDecision{{Files: []string{"a.go"}, Reviewer: "selected-model"}}}
	called := false
	engine.AssessPractices = func(_ context.Context, _ vcs.Ref, _ *vcs.PullRequest, report *Report) *practices.Report {
		called = true
		if provider.published != nil {
			t.Fatal("assessment ran after publication")
		}
		if len(report.Routes) != 1 || report.Routes[0].Reviewer != "selected-model" {
			t.Fatalf("assessment did not receive executed model routes: %+v", report.Routes)
		}
		return nil
	}
	if err := engine.publish(context.Background(), vcs.Ref{}, &Report{Plan: &bundle.Plan{}}, nil); err != nil || !called {
		t.Fatalf("assessment was not exercised before publication: %v", err)
	}
}
