package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCheckoutSkipPolicyMustNotSkipReview(t *testing.T) {
	repo := t.TempDir()
	if e := os.WriteFile(filepath.Join(repo, ".nitpick.yaml"), []byte("models:\n  default:\n    provider: openai\n    model: test\nreview:\n  skip_markers: [BYPASS]\n"), 0600); e != nil {
		t.Fatal(e)
	}
	var diffReads atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.Header.Get("Accept"), "diff") {
			diffReads.Add(1)
			http.Error(w, "stop before model", 500)
			return
		}
		if strings.Contains(r.URL.Path, "/commits/") {
			if err := json.NewEncoder(w).Encode(map[string]any{"commit": map[string]any{"message": "normal commit"}}); err != nil {
				t.Error(err)
			}
			return
		}
		if err := json.NewEncoder(w).Encode(map[string]any{"number": 7, "title": "BYPASS", "head": map[string]any{"sha": "deadbeef"}, "base": map[string]any{"sha": "beef0001"}}); err != nil {
			t.Error(err)
		}
	}))
	defer srv.Close()
	t.Setenv("GITHUB_API_URL", srv.URL+"/")
	t.Setenv("GITHUB_TOKEN", "fake")
	t.Setenv("NITPICK_GITHUB_TOKEN", "")
	t.Setenv("NITPICK_NO_USER_CONFIG", "1")
	t.Setenv("GITHUB_OUTPUT", "")
	t.Setenv("GITHUB_STEP_SUMMARY", "")
	err := runReview(context.Background(), []string{"-repo", repo, "-owner", "o", "-repo-name", "r", "-pr", "7"})
	if err == nil && diffReads.Load() == 0 {
		t.Fatal("untrusted checkout skip_markers returned success before reading diff or base policy")
	}
}
