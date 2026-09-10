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
