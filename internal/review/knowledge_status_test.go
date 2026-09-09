package review

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/knowledge"
)

func discard() *slog.Logger { return slog.New(slog.DiscardHandler) }

// The distinction the status exists for. Off, skipped and failed all produce a
// review without retrieval, and an evaluation that cannot tell them apart
// records the control as the treatment.
func TestBuildKnowledgeSaysWhichKindOfNotRunningItIs(t *testing.T) {
	t.Setenv("SYNTHETIC_API_KEY", "syn_test")

	off := &config.Config{}
	_, status, err := BuildKnowledge(context.Background(), off, knowledge.IndexJSON(), discard())
	if err != nil || status.State != KnowledgeOff {
		t.Errorf("retrieval not asked for = %q (%v), want off", status.State, err)
	}

	noEmbedder := &config.Config{}
	noEmbedder.Review.Knowledge = true
	_, status, err = BuildKnowledge(context.Background(), noEmbedder, knowledge.IndexJSON(), discard())
	switch {
	case err != nil:
		t.Errorf("a missing models.embed should not be an error: %v", err)
	case status.State != KnowledgeSkipped:
		t.Errorf("no models.embed = %q, want skipped", status.State)
	case !strings.Contains(status.Reason, "models.embed"):
		t.Errorf("reason %q does not name what to fix", status.Reason)
	}

	// An embedder that builds and does not match the shipped index. The index
	// records synthetic/hf:nomic-ai/nomic-embed-text-v1.5, so any other model
	// is a mismatch, and a mismatch is failed rather than skipped: it is a
	// thing that should have worked.
	mismatch := &config.Config{}
	mismatch.Review.Knowledge = true
	mismatch.Models.Embed = &config.ModelSpec{Provider: "synthetic", Model: "hf:not-the-indexed-model"}
	_, status, err = BuildKnowledge(context.Background(), mismatch, knowledge.IndexJSON(), discard())
	if err == nil {
		t.Fatal("a mismatched embedding model built without error; the index identity check did not run")
	}
	if status.State != KnowledgeFailed {
		t.Errorf("a mismatched embedding model = %q, want failed", status.State)
	}
}

// "On" and "worked" are different claims, and only the second one licenses
// scoring a run as the retrieval arm.
func TestRetrievedSeparatesConfiguredFromServed(t *testing.T) {
	served := KnowledgeStatus{State: KnowledgeActive, Queries: 4}
	if !served.Retrieved() {
		t.Error("an active run that answered every batch should count as retrieved")
	}

	for _, s := range []KnowledgeStatus{
		{State: KnowledgeActive, Queries: 4, Failures: 1},
		{State: KnowledgeActive, Queries: 0},
		{State: KnowledgeSkipped, Reason: "no models.embed is configured"},
		{State: KnowledgeFailed, Reason: "the embedder could not be built"},
		{State: KnowledgeOff},
	} {
		if s.Retrieved() {
			t.Errorf("%s counted as retrieved", s)
		}
	}
}

// A batch the embedder refused turns the run's status, so a partial arm is
// never reported as a clean one.
func TestOneRefusedBatchFailsTheRunsStatus(t *testing.T) {
	k := &KnowledgeRetriever{status: KnowledgeStatus{State: KnowledgeActive, Model: "m", Entries: 3}}
	k.counts.query()
	k.counts.query()

	if got := k.Status(); got.State != KnowledgeActive || !got.Retrieved() {
		t.Fatalf("two clean queries = %s, want an active run that retrieved", got)
	}

	k.counts.failure()
	got := k.Status()
	if got.State != KnowledgeFailed {
		t.Errorf("a refused batch left the status %q, want failed", got.State)
	}
	if got.Retrieved() {
		t.Error("a run with a refused batch counted as retrieved")
	}
	if got.Failures != 1 || got.Queries != 2 {
		t.Errorf("counts = %d queries, %d failures; want 2 and 1", got.Queries, got.Failures)
	}
}

// A nil retriever is a review nobody asked retrieval about, not a missing
// answer.
func TestNilRetrieverIsOff(t *testing.T) {
	var k *KnowledgeRetriever
	if got := k.Status(); got.State != KnowledgeOff {
		t.Errorf("a nil retriever = %q, want off", got.State)
	}
}
