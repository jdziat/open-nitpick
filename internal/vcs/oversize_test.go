package vcs

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
)

func TestAnOversizeSummaryIsBoundedBeforeTheFirstSend(t *testing.T) {
	var mu sync.Mutex
	var reviews []capturedReview
	gh := newFakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		var c capturedReview
		_ = json.NewDecoder(r.Body).Decode(&c)
		mu.Lock()
		reviews = append(reviews, c)
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 1})
	})

	err := gh.PublishReview(context.Background(), testRef(), Review{
		Summary:  strings.Repeat("x", 200000),
		Head:     "abc123",
		Comments: []Comment{{Path: "a.go", Line: 4, Body: "finding"}},
	})
	if err != nil {
		t.Fatalf("PublishReview: %v", err)
	}
	if len(reviews) != 1 {
		t.Fatalf("%d reviews sent, want one carrying summary and comments", len(reviews))
	}
	if n := len(reviews[0].Body); n > maxSummaryBytes {
		t.Errorf("first body is %d bytes, over GitHub's limit once markers are counted", n)
	}
	if len(reviews[0].Comments) != 1 {
		t.Errorf("the inline comment was not sent with the review: %+v", reviews[0].Comments)
	}
}

func TestARefusedReviewKeepsEveryAnchorableComment(t *testing.T) {
	var mu sync.Mutex
	var alone []string
	var fallbackBody string
	gh := newFakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/pulls/7/reviews"):
			var c capturedReview
			_ = json.NewDecoder(r.Body).Decode(&c)
			if len(c.Comments) > 0 {
				w.WriteHeader(http.StatusUnprocessableEntity)
				_, _ = w.Write([]byte(`{"message":"Unprocessable"}`))
				return
			}
			fallbackBody = c.Body
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 2})
		case strings.HasSuffix(r.URL.Path, "/pulls/7/comments"):
			var c struct {
				Path string `json:"path"`
			}
			_ = json.NewDecoder(r.Body).Decode(&c)
			if c.Path == "off-diff.go" {
				w.WriteHeader(http.StatusUnprocessableEntity)
				_, _ = w.Write([]byte(`{"message":"line not in diff"}`))
				return
			}
			mu.Lock()
			alone = append(alone, c.Path)
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 3})
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
		}
	})

	err := gh.PublishReview(context.Background(), testRef(), Review{
		Summary: "walkthrough", Head: "abc123",
		Comments: []Comment{{Path: "a.go", Line: 4, Body: "one"}, {Path: "off-diff.go", Line: 9, Body: "two"}, {Path: "b.go", Line: 2, Body: "three"}},
	})
	if err != nil {
		t.Fatalf("PublishReview: %v", err)
	}
	if strings.Join(alone, ",") != "a.go,b.go" {
		t.Errorf("posted alone = %v, want the two that anchor", alone)
	}
	if !strings.Contains(fallbackBody, "2 of 3 inline comment(s)") {
		t.Errorf("the summary does not say what was posted separately:\n%s", fallbackBody)
	}
	if strings.Contains(fallbackBody, completionMarker("abc123")) {
		t.Error("a head with a comment missing was marked complete")
	}
}
