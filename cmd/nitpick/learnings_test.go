package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/learnings"
)

func TestScriptedDraftRequiresIndividualAcceptanceBeforeActivation(t *testing.T) {
	dir := t.TempDir()
	feedback := learnings.Feedback{Repository: "example/project", SourceURL: "https://example.test/project/pull/1#comment-2", SourceRevision: "source", EvidenceDate: "2026-11-17", Classification: "convention", Rationale: "Operator confirmed the request contract.", Text: "The handler passes its request context."}
	entry := learnings.Entry{ID: "request-context", Title: "Request context", Body: "Pass request context to downstream work.", Paths: []string{"internal/http/**"}, Languages: []string{"go"}, Classes: []config.Class{config.ClassCorrectness}}
	write := func(name string, value any) string {
		t.Helper()
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	input, scripted := write("feedback.json", feedback), write("entry.json", entry)
	var draftOut bytes.Buffer
	if err := runLearnings(t.Context(), []string{"draft", "-input", input, "-scripted-entry", scripted}, &draftOut); err != nil {
		t.Fatal(err)
	}
	var draft learningDraftJSON
	if err := json.Unmarshal(draftOut.Bytes(), &draft); err != nil {
		t.Fatal(err)
	}
	if draft.Entry.ApprovedBy != "" || !draft.Usage.Metered || draft.Usage.Reason != "no model calls" {
		t.Fatalf("scripted draft was activated or failed to disclose its no-call path: %+v", draft)
	}
	path := write("draft.json", draft)
	var acceptedOut bytes.Buffer
	if err := runLearnings(t.Context(), []string{"accept", "-input", path, "-repository", feedback.Repository, "-reviewed-hash", draft.Entry.ContentHash, "-local-operator"}, &acceptedOut); err != nil {
		t.Fatal(err)
	}
	var accepted learnings.Manifest
	if err := json.Unmarshal(acceptedOut.Bytes(), &accepted); err != nil {
		t.Fatal(err)
	}
	if len(accepted.Entries) != 1 || !strings.HasPrefix(accepted.Entries[0].ApprovedBy, "local:") {
		t.Fatalf("accepted entry has no authenticated identity: %+v", accepted)
	}
	overlay, reason, err := learnings.Load(t.Context(), feedback.Repository, "trusted-base", func(_ context.Context, rev, path string) ([]byte, error) {
		if rev != "trusted-base" || path != learnings.ManifestPath {
			t.Fatalf("read untrusted policy %q:%q", rev, path)
		}
		return acceptedOut.Bytes(), nil
	})
	if err != nil || reason != "loaded" || len(overlay.ForPaths([]string{"internal/http/handler.go"}, map[config.Class]bool{config.ClassCorrectness: true})) != 1 {
		t.Fatalf("accepted manifest could not activate from trusted base: %v %q %v", overlay, reason, err)
	}
}

func TestDraftRefusesProviderAccessWithoutExplicitConsent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "feedback.json")
	if err := os.WriteFile(path, []byte(`{"text":"private thread"}`), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err := runLearnings(t.Context(), []string{"draft", "-input", path, "-provider", "openai", "-model", "test", "-config", "/does/not/exist"}, &out)
	if err == nil || !strings.Contains(err.Error(), "-allow-provider-feedback") || out.Len() != 0 {
		t.Fatalf("provider access happened before consent: %v %q", err, out.String())
	}
}
