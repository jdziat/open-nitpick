package vcs

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestMarkersRoundTrip(t *testing.T) {
	body := "Problem here.\n" + DefaultBotMarker + "\n" + fingerprintMarker("0123abcd", "correctness")
	fp, class, ok := parseFingerprint(body)
	if !ok || fp != "0123abcd" || class != "correctness" {
		t.Errorf("parseFingerprint = %q %q %v", fp, class, ok)
	}

	head, ok := parseHead("Walkthrough.\n" + DefaultBotMarker + "\n" + headMarker("deadbeef"))
	if !ok || head != "deadbeef" {
		t.Errorf("parseHead = %q %v", head, ok)
	}

	if fingerprintMarker("", "x") != "" || headMarker("") != "" {
		t.Error("an empty fingerprint or head must produce no marker at all")
	}
	if _, _, ok := parseFingerprint("nothing here"); ok {
		t.Error("parseFingerprint matched a body with no marker")
	}
}

// TestGitHubPublishEmbedsMarkers: the fingerprint and head are what the next
// run reads back, so a review published without them is one that can never
// be reviewed incrementally.
func TestGitHubPublishEmbedsMarkers(t *testing.T) {
	var captured capturedReview
	gh := newFakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&captured)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 1})
	})

	err := gh.PublishReview(context.Background(), testRef(), Review{
		Summary:  "Walkthrough.",
		Head:     "abc123",
		Comments: []Comment{{Path: "a.go", Line: 4, Body: "Problem.", Fingerprint: "feed", Class: "security"}},
	})
	if err != nil {
		t.Fatalf("PublishReview: %v", err)
	}

	if !strings.Contains(captured.Body, headMarker("abc123")) {
		t.Errorf("review body lacks the head marker:\n%s", captured.Body)
	}
	if !strings.Contains(captured.Comments[0].Body, fingerprintMarker("feed", "security")) {
		t.Errorf("comment body lacks the fingerprint marker:\n%s", captured.Comments[0].Body)
	}
}

// TestGitHubPublishEmbedsHeadWithoutSummary: the default body GitHub requires
// when summaries are off must still carry the head, or a summary-less
// repository can never review incrementally.
func TestGitHubPublishEmbedsHeadWithoutSummary(t *testing.T) {
	var captured capturedReview
	gh := newFakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&captured)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 1})
	})

	if err := gh.PublishReview(context.Background(), testRef(), Review{Head: "abc123"}); err != nil {
		t.Fatalf("PublishReview: %v", err)
	}
	if !strings.Contains(captured.Body, headMarker("abc123")) {
		t.Errorf("default body lacks the head marker:\n%s", captured.Body)
	}
}

func TestGitHubPriorReviewReadsOwnMarkersOnly(t *testing.T) {
	gh := newFakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/graphql":
			http.Error(w, "thread status unavailable", http.StatusServiceUnavailable)
		case strings.HasSuffix(r.URL.Path, "/pulls/7/reviews"):
			_ = json.NewEncoder(w).Encode([]map[string]any{
				// A human review, and an older bot review; the newest bot
				// review by id wins even though it is listed first.
				{"id": 30, "user": map[string]any{"id": 42}, "body": "Older run.\n" + DefaultBotMarker + "\n" + headMarker("beef02") + "\n" + completionMarker("beef02")},
				{"id": 10, "body": "LGTM"},
				{"id": 20, "user": map[string]any{"id": 42}, "body": "Old run.\n" + DefaultBotMarker + "\n" + headMarker("beef01") + "\n" + completionMarker("beef01")},
			})
		case strings.HasSuffix(r.URL.Path, "/pulls/7/comments"):
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": 1, "user": map[string]any{"id": 42}, "path": "a.go", "line": 12, "body": "Ours.\n" + DefaultBotMarker + "\n" + fingerprintMarker("aa11", "correctness")},
				{"id": 2, "path": "b.go", "line": 3, "body": "A human's comment about open-nitpick"},
				{"id": 3, "user": map[string]any{"id": 42}, "path": "c.go", "line": 5, "body": "Ours, but from before fingerprints existed.\n" + DefaultBotMarker},
			})
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
		}
	})

	prior, err := gh.PriorReview(context.Background(), testRef())
	if err != nil {
		t.Fatalf("PriorReview: %v", err)
	}
	if prior.Head != "beef02" {
		t.Errorf("head = %q, want the newest bot review's", prior.Head)
	}
	if len(prior.Comments) != 1 {
		t.Fatalf("comments = %+v, want only the one carrying a fingerprint", prior.Comments)
	}
	c := prior.Comments[0]
	if c.Path != "a.go" || c.Line != 12 || c.Fingerprint != "aa11" || c.Class != "correctness" {
		t.Errorf("comment = %+v", c)
	}
}

func TestGitHubChangedSince(t *testing.T) {
	compare := func(status string, files ...string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if !strings.Contains(r.URL.Path, "/compare/old...head") {
				t.Errorf("unexpected path %s", r.URL.Path)
			}
			var list []map[string]any
			for _, f := range files {
				list = append(list, map[string]any{"filename": f})
			}
			list = append(list, map[string]any{"filename": "moved.go", "previous_filename": "orig.go"})
			_ = json.NewEncoder(w).Encode(map[string]any{"status": status, "files": list})
		}
	}
	ref := Ref{Owner: "o", Repo: "r", Number: 7, Head: "head"}

	gh := newFakeGitHub(t, compare("ahead", "a.go", "b.go"))
	paths, ok, err := gh.ChangedSince(context.Background(), ref, "old")
	if err != nil || !ok {
		t.Fatalf("ChangedSince = %v %v %v", paths, ok, err)
	}
	want := "a.go b.go moved.go orig.go"
	if got := strings.Join(paths, " "); got != want {
		t.Errorf("paths = %q, want %q (both names of a rename)", got, want)
	}

	gh = newFakeGitHub(t, compare("diverged", "a.go"))
	if _, ok, err := gh.ChangedSince(context.Background(), ref, "old"); ok || err != nil {
		t.Errorf("a diverged comparison must be unanswerable, got ok=%v err=%v", ok, err)
	}

	gh = newFakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Not Found"}`))
	})
	if _, ok, err := gh.ChangedSince(context.Background(), ref, "old"); ok || err != nil {
		t.Errorf("a vanished earlier head must be unanswerable, not an error: ok=%v err=%v", ok, err)
	}
}

func TestGitHubPublishesRangeComments(t *testing.T) {
	var raw map[string]any
	gh := newFakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&raw)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 1})
	})
	err := gh.PublishReview(context.Background(), testRef(), Review{Summary: "s", Comments: []Comment{
		{Path: "a.go", StartLine: 4, Line: 6, Body: "range"},
		{Path: "a.go", Line: 9, Body: "single"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	comments := raw["comments"].([]any)
	first := comments[0].(map[string]any)
	if first["start_line"] != float64(4) || first["line"] != float64(6) || first["start_side"] != "RIGHT" {
		t.Errorf("range comment = %v", first)
	}
	if _, has := comments[1].(map[string]any)["start_line"]; has {
		t.Error("a single-line comment must not carry start_line")
	}
}

// A prior comment carries the text it was published with.
//
// A finding is persisted nowhere and the fingerprint identifies one without
// carrying it, so this body is the only record of what the review said. A run
// that wants to act on a finding rather than recognise it has nothing else to
// read, and the API call already has the text in hand.
func TestGitHubPriorReviewKeepsTheCommentBody(t *testing.T) {
	const published = "**warning · correctness**\n\nGuard removed that a caller depends on\n\n" +
		"b.go calls this with a nil map on the error path."

	gh := newFakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/graphql":
			http.Error(w, "thread status unavailable", http.StatusServiceUnavailable)
		case strings.HasSuffix(r.URL.Path, "/pulls/7/reviews"):
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": 30, "user": map[string]any{"id": 42}, "body": "Run.\n" + DefaultBotMarker + "\n" + headMarker("beef02") + "\n" + completionMarker("beef02")},
			})
		case strings.HasSuffix(r.URL.Path, "/pulls/7/comments"):
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": 1, "user": map[string]any{"id": 42}, "path": "a.go", "line": 12,
					"body": published + "\n" + DefaultBotMarker + "\n" + fingerprintMarker("aa11", "correctness")},
			})
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
		}
	})

	prior, err := gh.PriorReview(context.Background(), testRef())
	if err != nil {
		t.Fatalf("PriorReview: %v", err)
	}
	if len(prior.Comments) != 1 {
		t.Fatalf("comments = %+v", prior.Comments)
	}

	body := prior.Comments[0].Body
	if body == "" {
		t.Fatal("the body was read and discarded")
	}
	if !strings.Contains(body, "Guard removed that a caller depends on") {
		t.Errorf("the title did not survive:\n%s", body)
	}
	if !strings.Contains(body, "b.go calls this with a nil map") {
		t.Errorf("the rationale did not survive:\n%s", body)
	}
}
