package learnings

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
)

func TestDraftingRequiresConsentRedactsAndCannotApproveItself(t *testing.T) {
	f := Feedback{Repository: "example/project", SourceURL: "https://example.test/project/pull/1#discussion-2", SourceRevision: "source", EvidenceDate: "2026-11-17", Classification: "false_positive", Rationale: "Caller owns the lifecycle.", Text: "secret=token caller owns the lifecycle"}
	calls := 0
	generate := func(_ context.Context, text string) (Entry, error) {
		calls++
		if strings.Contains(text, "secret=token") {
			t.Fatal("private token reached the model")
		}
		e := acceptedEntry(t)
		e.ApprovedBy = "model-claimed-admin"
		return e, nil
	}
	redact := func(text string) string { return strings.ReplaceAll(text, "secret=token", "[REDACTED]") }
	if _, err := Propose(t.Context(), f, false, redact, generate); err == nil || calls != 0 {
		t.Fatalf("drafted without consent: %d %v", calls, err)
	}
	f.BotGenerated = true
	if _, err := Propose(t.Context(), f, true, redact, generate); err == nil || calls != 0 {
		t.Fatal("bot event trained the draft")
	}
	f.BotGenerated = false
	draft, err := Propose(t.Context(), f, true, redact, generate)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || draft.Entry.ApprovedBy != "" || draft.SourceExcerptHash == "" || draft.Kind != "draft" {
		t.Fatalf("model activated its draft: %+v", draft)
	}
	if err := validateEntry(draft.Entry, f.Repository); err == nil {
		t.Fatal("draft passed active-policy validation")
	}
	if _, err := Accept(t.Context(), draft, "unreviewed", func(context.Context, string) (string, error) {
		t.Fatal("unreviewed draft reached authorization")
		return "", nil
	}); err == nil {
		t.Fatal("unreviewed draft was accepted")
	}
	if _, err := Accept(t.Context(), draft, draft.Entry.ContentHash, func(context.Context, string) (string, error) { return "", errors.New("no write permission") }); err == nil {
		t.Fatal("unauthorized acceptance succeeded")
	}
	accepted, err := Accept(t.Context(), draft, draft.Entry.ContentHash, func(_ context.Context, repo string) (string, error) {
		if repo != f.Repository {
			t.Fatal("authorized the wrong repo")
		}
		return "current-operator", nil
	})
	if err != nil || accepted.ApprovedBy != "current-operator" || accepted.ContentHash == draft.Entry.ContentHash {
		t.Fatalf("approval not bound to content and identity: %+v %v", accepted, err)
	}
}

func TestFixedFeedbackCannotBecomeActiveSuppression(t *testing.T) {
	e := acceptedEntry(t)
	e.Classification = "fixed"
	e.ContentHash = e.Hash()
	overlay, _, err := Load(t.Context(), e.Repository, "base", func(context.Context, string, string) ([]byte, error) { return manifestBytes(t, e), nil })
	if err != nil {
		t.Fatal(err)
	}
	if len(overlay.ForPaths([]string{"internal/http/handler.go"}, map[config.Class]bool{config.ClassCorrectness: true})) != 0 {
		t.Fatal("fixed finding was turned into a suppression rule")
	}
}
