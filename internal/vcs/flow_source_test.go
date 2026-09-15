package vcs

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestLocalFileLimitRejectsOversizedSource(t *testing.T) {
	dir := newRepo(t)
	body := strings.Repeat("x", 1<<20)
	write(t, dir, "large.go", body)
	gitIn(t, dir, "add", "large.go")
	gitIn(t, dir, "commit", "-qm", "large file")
	for _, head := range []string{Worktree, "HEAD"} {
		local := NewLocal(dir, nil)
		data, err := local.FileContentLimit(context.Background(), Ref{Head: head}, "large.go", 32)
		if !errors.Is(err, ErrSourceLimit) || len(data) != 0 {
			t.Fatalf("head=%q bytes=%d error=%v, want bounded rejection", head, len(data), err)
		}
		data, err = local.FileContentLimit(context.Background(), Ref{Head: head}, "large.go", len(body))
		if err != nil || string(data) != body {
			t.Fatalf("head=%q exact limit bytes=%d error=%v", head, len(data), err)
		}
	}
}

func TestGitHubFileLimitRejectsBeforeDownloadingSource(t *testing.T) {
	requests := 0
	gh := newFakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/api/v3/repos/o/r/contents/large.go" || r.URL.Query().Get("ref") != "deadbeef" {
			t.Fatalf("unexpected source request: %s", r.URL)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"type": "file", "size": 1 << 20, "encoding": "none"})
	})
	data, err := gh.FileContentLimit(context.Background(), testRef().At("deadbeef"), "large.go", 32)
	if requests != 1 || !errors.Is(err, ErrSourceLimit) || len(data) != 0 {
		t.Fatalf("requests=%d bytes=%d error=%v, want metadata-only rejection", requests, len(data), err)
	}
}

func TestGitHubBaseEvidenceUsesBaseRepository(t *testing.T) {
	gh := newFakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/commits/") {
			_ = json.NewEncoder(w).Encode(map[string]any{"sha": "deadbeef"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"number": 7,
			"head":   map[string]any{"sha": "deadbeef", "repo": map[string]any{"full_name": "fork/project"}},
			"base":   map[string]any{"sha": "cafebabe", "repo": map[string]any{"full_name": "o/r"}},
		})
	})
	url, err := gh.SourceBase(context.Background(), testRef(), "cafebabe")
	if err != nil || !strings.HasSuffix(url, "/o/r/blob/cafebabe") {
		t.Fatalf("base URL=%q error=%v, want original repository", url, err)
	}
}
