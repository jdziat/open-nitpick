package main

import (
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nitpick-oss/nitpick/internal/insights"
	"github.com/nitpick-oss/nitpick/internal/server"
)

// insightsServer is a real insights component behind the real server, with one
// write token.
func insightsServer(t *testing.T) (url string, store *insights.Store) {
	t.Helper()
	store, err := insights.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	c := insights.NewComponent(store, []insights.Token{insights.NewToken("ci", insights.RoleWrite, "ci-secret")}, slog.New(slog.DiscardHandler))
	h, err := server.New(server.Config{Components: []server.Component{c}})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return ts.URL, store
}

func TestReviewReportsItsFindingsToTheInsightsServer(t *testing.T) {
	model := newFakeModel(t)
	repo := gitRepo(t, model)
	url, store := insightsServer(t)
	t.Setenv(EnvInsightsToken, "ci-secret")

	out, err := reviewJSON(t, repo, "-insights", url, "-insights-repo", "acme/api")
	if err != nil {
		t.Fatalf("review: %v", err)
	}
	got := store.Reviews(insights.Filter{})
	if len(got) != 1 {
		t.Fatalf("the server holds %d reviews, want the 1 that ran", len(got))
	}
	rv := got[0]
	published, _ := out["findings"].([]any)
	if rv.Repo != "acme/api" || len(rv.Findings) != len(published) || len(published) == 0 {
		t.Fatalf("server has %+v for a review that published %d findings", rv, len(published))
	}
	if rv.Result != out["result"] || rv.Source != "ci" || time.Since(rv.At) > time.Minute {
		t.Fatalf("result/source/time wrong: %+v vs %v", rv, out["result"])
	}
}

// Losing a data point must not fail a review that passed.
func TestAnUnreachableInsightsServerDoesNotFailTheReview(t *testing.T) {
	model := newFakeModel(t)
	repo := gitRepo(t, model)
	t.Setenv(EnvInsightsToken, "ci-secret")
	out, err := reviewJSON(t, repo, "-insights", "http://127.0.0.1:1", "-insights-repo", "acme/api")
	if err != nil {
		t.Fatalf("review failed because the insights server was down: %v", err)
	}
	if out["result"] == nil {
		t.Fatalf("no report printed")
	}
}

func TestAServerThatRefusesTheTokenDoesNotFailTheReviewEither(t *testing.T) {
	model := newFakeModel(t)
	repo := gitRepo(t, model)
	url, store := insightsServer(t)
	t.Setenv(EnvInsightsToken, "not-the-token")
	if _, err := reviewJSON(t, repo, "-insights", url, "-insights-repo", "acme/api"); err != nil {
		t.Fatalf("a 401 failed the review: %v", err)
	}
	if n := len(store.Reviews(insights.Filter{})); n != 0 {
		t.Fatalf("a review with a bad token was kept: %d", n)
	}
}

func TestInsightsFlagsAreRefusedBeforeAnyModelIsCalled(t *testing.T) {
	model := newFakeModel(t)
	repo := gitRepo(t, model)
	t.Setenv(EnvInsightsToken, "")
	for name, args := range map[string][]string{
		"no credential":    {"-insights", "http://127.0.0.1:1"},
		"repo without url": {"-insights-repo", "a/b"},
	} {
		before := model.calls.Load()
		if _, err := reviewJSON(t, repo, args...); err == nil {
			t.Errorf("%s: accepted", name)
		}
		if model.calls.Load() != before {
			t.Errorf("%s: a model was called before the flags were refused", name)
		}
	}
}
