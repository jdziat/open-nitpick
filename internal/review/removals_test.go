package review

import (
	"context"
	"github.com/jdziat/open-nitpick/internal/vcs"
	"testing"
)

func TestReviewPublishesARemovedGuardFinding(t *testing.T) {
	const patch = "diff --git a/auth.go b/auth.go\n--- a/auth.go\n+++ b/auth.go\n@@ -1,6 +1,3 @@\n func Handle() {\n- if !authorized() {\n-  return\n- }\n  serve()\n }\n"
	model := &scriptedLLM{fallback: mustJSON(t, Result{Findings: []Finding{{Path: "auth.go", Line: 2, Severity: "error", Category: "correctness", Title: "Removed authorization guard", Rationale: "An unauthorized caller now reaches serve."}}})}
	provider := &stubProvider{diff: patch, content: map[string]string{"auth.go": "func Handle() {\n serve()\n}\n"}}
	report, err := newEngine(t, model, provider, nil).Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Findings) != 1 {
		t.Fatalf("findings = %+v; removed guard must reach review and publication", report.Findings)
	}
	if provider.published == nil || len(provider.published.Comments) != 1 {
		t.Fatal("removed guard finding was not published")
	}
	c := provider.published.Comments[0]
	if c.Path != "auth.go" || c.Line != 2 || c.Side != "RIGHT" {
		t.Fatalf("anchor = %+v", c)
	}
}

// The fix-it button survives a removal-only finding.
//
// filterAnchors strips a suggestion when it moves a finding, because the text
// was written to replace the line the model chose and pasting it over a
// different one leaves the file uncompilable. That branch once implied
// movement: it snapped to added lines, and an added line returns earlier.
//
// Surviving context is commentable and is not an added line, so a finding
// placed exactly where the prompt above asks for one arrives at the branch
// having moved nowhere. Stripping it there takes the suggestion from every
// removal-only finding, which is the shape this file exists to publish, and
// nothing else in the suite notices: the finding is still published, still on
// the right line, and still says the right thing.
func TestARemovalOnlyFindingKeepsItsSuggestion(t *testing.T) {
	const patch = "diff --git a/auth.go b/auth.go\n--- a/auth.go\n+++ b/auth.go\n@@ -1,6 +1,3 @@\n func Handle() {\n- if !authorized() {\n-  return\n- }\n  serve()\n }\n"
	const suggestion = " if !authorized() {\n  return\n }\n serve()"
	model := &scriptedLLM{fallback: mustJSON(t, Result{Findings: []Finding{{
		Path: "auth.go", Line: 2, Severity: "error", Category: "correctness",
		Title: "Removed authorization guard", Rationale: "An unauthorized caller now reaches serve.",
		Suggestion: suggestion,
	}}})}
	provider := &stubProvider{diff: patch, content: map[string]string{"auth.go": "func Handle() {\n serve()\n}\n"}}
	report, err := newEngine(t, model, provider, nil).Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Findings) != 1 {
		t.Fatalf("findings = %+v", report.Findings)
	}
	if report.Findings[0].Line != 2 {
		t.Fatalf("the anchor moved to %d, so a stripped suggestion would be correct; "+
			"rewrite this test around a finding that stays put", report.Findings[0].Line)
	}
	if report.Findings[0].Suggestion != suggestion {
		t.Errorf("suggestion = %q, want it unchanged: the anchor never moved, so the text "+
			"still replaces the line it was written for", report.Findings[0].Suggestion)
	}
}
