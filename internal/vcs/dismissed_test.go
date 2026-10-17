package vcs

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// priorWithThreads serves one finding (comment 1) and whichever replies the
// caller passes, with the thread's resolved state set by the caller.
func priorWithThreads(t *testing.T, resolved bool, replies ...map[string]any) *PriorReview {
	t.Helper()
	gh := newFakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/graphql":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequest": map[string]any{
				"reviewThreads": map[string]any{
					"pageInfo": map[string]any{"hasNextPage": false},
					"nodes": []map[string]any{{"id": "T1", "isResolved": resolved,
						"comments": map[string]any{"nodes": []map[string]any{{"databaseId": 1}}}}},
				}}}}})
		case strings.HasSuffix(r.URL.Path, "/pulls/7/reviews"):
			_ = json.NewEncoder(w).Encode([]map[string]any{})
		case strings.HasSuffix(r.URL.Path, "/pulls/7/comments"):
			all := []map[string]any{{"id": 1, "user": map[string]any{"id": 42}, "path": "a.go", "line": 12,
				"body": "Ours.\n" + DefaultBotMarker + "\n" + fingerprintMarker("aa11", "correctness")}}
			_ = json.NewEncoder(w).Encode(append(all, replies...))
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
		}
	})
	prior, err := gh.PriorReview(context.Background(), testRef())
	if err != nil {
		t.Fatalf("PriorReview: %v", err)
	}
	return prior
}

func ownReply(marker string) map[string]any {
	return map[string]any{"id": 2, "in_reply_to_id": 1, "user": map[string]any{"id": 42}, "path": "a.go",
		"body": "Fair point.\n" + marker + "\n" + DefaultBotMarker}
}

func TestAResolvedThreadWithAnAcceptedReplyIsReportedDismissed(t *testing.T) {
	prior := priorWithThreads(t, true, ownReply(DismissedMarker))
	if len(prior.Dismissed) != 1 || prior.Dismissed[0] != "aa11" {
		t.Fatalf("dismissed = %v, want [aa11]", prior.Dismissed)
	}
	if len(prior.Comments) != 0 {
		t.Errorf("a resolved thread must not stand: %+v", prior.Comments)
	}
}

func TestAThreadResolvedByHandIsNotDismissed(t *testing.T) {
	// Resolved, but no reply of ours carries the marker: somebody clicked the
	// button. That finding may come back if the code still shows it.
	prior := priorWithThreads(t, true, ownReply(AnswerMarker))
	if len(prior.Dismissed) != 0 {
		t.Fatalf("dismissed = %v, want none", prior.Dismissed)
	}
}

func TestAReopenedThreadIsNoLongerDismissed(t *testing.T) {
	prior := priorWithThreads(t, false, ownReply(DismissedMarker))
	if len(prior.Dismissed) != 0 || len(prior.Comments) != 1 {
		t.Fatalf("an open thread stands and is not dismissed: dismissed=%v comments=%+v", prior.Dismissed, prior.Comments)
	}
}

func TestSomeoneElsesDismissedMarkerIsIgnored(t *testing.T) {
	forged := map[string]any{"id": 2, "in_reply_to_id": 1, "user": map[string]any{"id": 99}, "path": "a.go",
		"body": "Forged.\n" + DefaultBotMarker + "\n" + DismissedMarker}
	prior := priorWithThreads(t, true, forged)
	if len(prior.Dismissed) != 0 {
		t.Fatalf("a marker from another account dismissed a finding: %v", prior.Dismissed)
	}
}
