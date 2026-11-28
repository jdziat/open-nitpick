package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nitpick-oss/nitpick/internal/review"
	"github.com/nitpick-oss/nitpick/internal/vcs"
)

func TestInsightsPostSendsTheReportAndWhereItRan(t *testing.T) {
	var got struct {
		Repo, Ref, Commit string
		Report            struct {
			Result   string
			Complete bool
			Findings []struct{ Path, Severity string }
		}
	}
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(b, &got); err != nil || r.URL.Path != "/v1/reviews" {
			t.Errorf("bad request %s %q: %v", r.URL.Path, b, err)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	var report bytes.Buffer
	rep := &review.Report{Findings: []review.Finding{{Path: "a.go", Line: 3, Severity: "error", Title: "t"}}}
	if err := writeJSONReport(&report, resultFindings, rep); err != nil {
		t.Fatal(err)
	}
	f := insightsFlags{url: srv.URL, repo: "acme/api", token: "tok"}
	err := f.post(context.Background(), report.Bytes(), vcs.Ref{Owner: "acme", Repo: "api", Number: 7, Head: "abc1234"}, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer tok" || got.Repo != "acme/api" || got.Commit != "abc1234" || got.Ref != "pull/7" {
		t.Fatalf("auth %q, sent %+v", auth, got)
	}
	if got.Report.Result != "findings" || len(got.Report.Findings) != 1 || got.Report.Findings[0].Path != "a.go" {
		t.Fatalf("the report was not carried: %+v", got.Report)
	}
}

func TestInsightsRepoDefaultsToTheForgeRefThenTheEnvironment(t *testing.T) {
	f := insightsFlags{}
	if got := f.repoName(vcs.Ref{Owner: "o", Repo: "r"}); got != "o/r" {
		t.Fatalf("from the ref: %q", got)
	}
	t.Setenv("GITHUB_REPOSITORY", "env/repo")
	if got := f.repoName(vcs.Ref{}); got != "env/repo" {
		t.Fatalf("from the environment: %q", got)
	}
	f.repo = "flag/wins"
	if got := f.repoName(vcs.Ref{Owner: "o", Repo: "r"}); got != "flag/wins" {
		t.Fatalf("the flag must win: %q", got)
	}
}

func TestInsightsFlagsAreRefusedWhenTheyCouldDoNothing(t *testing.T) {
	t.Setenv(EnvInsightsToken, "")
	for name, f := range map[string]insightsFlags{
		"repo without url":  {repo: "a/b"},
		"url without token": {url: "https://x"},
	} {
		if err := f.check(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	t.Setenv(EnvInsightsToken, "tok")
	f := insightsFlags{url: "https://x"}
	if err := f.check(); err != nil || f.token != "tok" {
		t.Fatalf("a token from the environment must be taken: %v %q", err, f.token)
	}
	if err := (&insightsFlags{}).check(); err != nil {
		t.Fatalf("no flags is no insights: %v", err)
	}
}

func TestInsightsPostSaysWhyItFailedAndNeverEchoesTheToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "this token may read but not post reviews", http.StatusForbidden)
	}))
	defer srv.Close()
	f := insightsFlags{url: srv.URL, repo: "a/b", token: "super-secret-token"}
	err := f.post(context.Background(), []byte(`{"result":"clean","complete":true}`), vcs.Ref{}, time.Now())
	if err == nil || !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "may read but not post") {
		t.Fatalf("the refusal must say what the server said: %v", err)
	}
	if strings.Contains(err.Error(), "super-secret-token") {
		t.Fatalf("the token leaked into an error: %v", err)
	}
}

func TestInsightsPostGivesUpOnASlowServerInsteadOfHoldingTheReview(t *testing.T) {
	// The handler waits on a channel the test releases, not on the request's
	// context: a handler that has not read the body is never told the client left.
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-release }))
	defer srv.Close()
	defer close(release)
	f := insightsFlags{url: srv.URL, repo: "a/b", token: "t", timeout: 100 * time.Millisecond}
	start := time.Now()
	if err := f.post(context.Background(), []byte(`{}`), vcs.Ref{}, time.Now()); err == nil {
		t.Fatal("a server that never answers was reported as success")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("waited %v", time.Since(start))
	}
}
