package bundle

import (
	"context"
	"github.com/jdziat/open-nitpick/internal/diff"
	"testing"
)

func TestAssembleReviewsARemovedGuard(t *testing.T) {
	files, err := diff.Parse([]byte("diff --git a/auth.go b/auth.go\n--- a/auth.go\n+++ b/auth.go\n@@ -1,5 +1,2 @@\n func Handle() {\n- if !authorized() {\n-  return\n- }\n  serve()\n"))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Assemble(context.Background(), baseConfig(), files, nil)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Files() != 1 {
		t.Fatalf("removed guard was not reviewed: %+v", plan.Skipped)
	}
}
