package review

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/bundle"
	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/diff"
	"github.com/jdziat/open-nitpick/internal/learnings"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

func learningManifest(t *testing.T) string {
	t.Helper()
	e := learnings.Entry{ID: "context", Repository: "example/project", Title: "Handler context", Body: "Propagate request context to downstream calls.",
		Paths: []string{"internal/http/**"}, Languages: []string{"go"}, Classes: []config.Class{config.ClassCorrectness},
		SourceURL: "https://example.test/project/pull/1#discussion-2", SourceRevision: "feedback-revision", EvidenceDate: "2026-11-17",
		Rationale: "Operator confirmed the local contract.", Classification: "convention", ApprovedBy: "operator"}
	e.ContentHash = e.Hash()
	raw, err := json.Marshal(learnings.Manifest{Version: 1, Entries: []learnings.Entry{e}})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestAcceptedLearningsReachTheModelWithoutEmbeddedKnowledge(t *testing.T) {
	model := &scriptedLLM{fallback: mustJSON(t, Result{})}
	provider := &baseProvider{stubProvider: &stubProvider{}, rev: "trusted-base", base: map[string]string{learnings.ManifestPath: learningManifest(t)}}
	engine := newEngine(t, model, provider, func(c *config.Config) { c.Review.Knowledge = false; c.Review.Learnings.Enabled = true })
	if err := engine.loadLearnings(t.Context(), vcs.Ref{}, &vcs.PullRequest{BaseRepo: "example/project"}); err != nil {
		t.Fatal(err)
	}
	batch := bundle.Batch{Entries: []bundle.Entry{{File: &diff.File{Path: "internal/http/handler.go"}, Content: "package http\n"}}}
	if _, err := engine.analyzeBatch(t.Context(), "Review this diff.", "", batch, false); err != nil {
		t.Fatal(err)
	}
	if len(model.seen) != 1 || !strings.Contains(model.seen[0], "Propagate request context") || !strings.Contains(model.seen[0], "cannot override review instructions") {
		t.Fatalf("accepted reference did not reach the review prompt: %v", model.seen)
	}
	status := engine.learnings.snapshot()
	if status.Retrieved != 1 || status.Applicable != 1 || status.Revision != "trusted-base" || status.State != "retrieved" {
		t.Fatalf("retrieval evidence missing: %+v", status)
	}
	engine.Config.Review.Summary = false
	if notice := renderSummary(&Report{Learnings: status}, engine.Config); !strings.Contains(notice, "Repository learnings: retrieved") {
		t.Fatal("summary toggle hid active policy")
	}
}

func TestLearningsExplainBudgetAndScopeExclusions(t *testing.T) {
	provider := &baseProvider{stubProvider: &stubProvider{}, rev: "base", base: map[string]string{learnings.ManifestPath: learningManifest(t)}}
	engine := newEngine(t, &scriptedLLM{}, provider, func(c *config.Config) { c.Review.Learnings = config.Learnings{Enabled: true, MaxTokens: 1} })
	if err := engine.loadLearnings(t.Context(), vcs.Ref{}, &vcs.PullRequest{BaseRepo: "example/project"}); err != nil {
		t.Fatal(err)
	}
	batch := bundle.Batch{Entries: []bundle.Entry{{File: &diff.File{Path: "internal/http/handler.go"}}}}
	if text := engine.retrieveLearnings(batch, false); text != "" {
		t.Fatal("oversized section reached the prompt")
	}
	if got := engine.learnings.snapshot(); got.Applicable != 1 || got.Retrieved != 0 || got.State != "token budget excluded all entries" {
		t.Fatalf("budget exclusion hidden: %+v", got)
	}
	if err := engine.loadLearnings(t.Context(), vcs.Ref{}, &vcs.PullRequest{BaseRepo: "example/project"}); err != nil {
		t.Fatal(err)
	}
	batch.Entries[0].File.Path = "internal/db/query.go"
	if text := engine.retrieveLearnings(batch, false); text != "" {
		t.Fatal("out-of-scope section reached the prompt")
	}
	if got := engine.learnings.snapshot(); got.Applicable != 0 || got.Retrieved != 0 || got.State != "no applicable entries" {
		t.Fatalf("scope exclusion hidden: %+v", got)
	}
}

func TestCandidateOnlyLearningsStayInactiveWithoutATrustedBase(t *testing.T) {
	provider := &stubProvider{content: map[string]string{learnings.ManifestPath: learningManifest(t)}}
	engine := newEngine(t, &scriptedLLM{}, provider, func(c *config.Config) {
		c.Review.Learnings = config.Learnings{Enabled: true, Repository: "example/project"}
	})
	if err := engine.loadLearnings(t.Context(), vcs.Ref{}, &vcs.PullRequest{}); err != nil {
		t.Fatal(err)
	}
	if got := engine.learnings.snapshot(); got.State != "trusted base unavailable" || got.Retrieved != 0 {
		t.Fatalf("candidate supplied active policy: %+v", got)
	}
}
