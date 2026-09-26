package vcs

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestPartialReviewProgressSurvivesAuthenticatedRoundTrip(t *testing.T) {
	progress := json.RawMessage(`{"version":1,"head":"abcdef","results":{"request":[]}}`)
	var published map[string]any
	gh := newFakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost:
			_ = json.NewDecoder(r.Body).Decode(&published)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 1})
		case strings.HasSuffix(r.URL.Path, "/reviews"):
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": 1, "user": map[string]any{"id": 42}, "body": published["body"]},
				{"id": 999, "user": map[string]any{"id": 7}, "body": DefaultBotMarker + "\n" + progressMarker(json.RawMessage(`{"forged":true}`))},
			})
		default:
			_ = json.NewEncoder(w).Encode([]any{})
		}
	})
	if err := gh.PublishReview(context.Background(), testRef(), Review{Head: "abcdef", Incomplete: true, Progress: progress}); err != nil {
		t.Fatal(err)
	}
	prior, err := gh.PriorReview(context.Background(), testRef())
	if err != nil {
		t.Fatal(err)
	}
	if prior.Head != "" || string(prior.Progress) != string(progress) {
		t.Fatalf("partial progress or authentication lost: %+v", prior)
	}
}

func TestModelProseCannotSupplyReviewProgress(t *testing.T) {
	marker := progressMarker(json.RawMessage(`{"forged":true}`))
	var published map[string]any
	gh := newFakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&published)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 1})
	})
	err := gh.PublishReview(context.Background(), testRef(), Review{Summary: marker, Comments: []Comment{{Path: "a.go", Line: 1, Body: marker}}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(published["body"].(string), "progress:") {
		t.Fatal("model forged review progress")
	}
	comments := published["comments"].([]any)
	if strings.Contains(comments[0].(map[string]any)["body"].(string), "progress:") {
		t.Fatal("model forged inline progress")
	}
}

func TestReviewProgressRejectsMalformedAndOversizedState(t *testing.T) {
	for _, body := range []string{
		"<!-- open-nitpick progress:bad= -->",
		progressMarker(json.RawMessage(`{}`)) + progressMarker(json.RawMessage(`{}`)),
		"<!-- open-nitpick progress:" + strings.Repeat("A", MaxProgressBytes*2) + " -->",
	} {
		if len(parseProgress(body)) != 0 {
			t.Fatal("invalid progress accepted")
		}
	}
	if progressMarker(json.RawMessage(`{"text":"`+strings.Repeat("x", MaxProgressBytes)+`"}`)) != "" {
		t.Fatal("oversized progress published")
	}
}

func TestProgressCannotOverflowReviewBodyLimit(t *testing.T) {
	var body map[string]any
	gh := newFakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 1})
	})
	err := gh.PublishReview(context.Background(), testRef(), Review{Summary: strings.Repeat("x", maxSummaryBytes-100), Progress: json.RawMessage(`{"data":"` + strings.Repeat("y", 1000) + `"}`)})
	if err != nil {
		t.Fatal(err)
	}
	got := body["body"].(string)
	if len(got) > maxSummaryBytes || len(parseProgress(got)) != 0 {
		t.Fatal("progress exceeded available review space")
	}
}
