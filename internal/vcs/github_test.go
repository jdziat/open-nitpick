package vcs

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newFakeGitHub serves a stub API and returns a provider pointed at it.
func newFakeGitHub(t *testing.T, handler http.HandlerFunc) *GitHub {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v3/user" {
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 42, "login": "nitpick"})
			return
		}
		handler(w, r)
	}))
	t.Cleanup(server.Close)

	gh, err := NewGitHub(GitHubOptions{Token: "test-token", BaseURL: server.URL + "/"})
	if err != nil {
		t.Fatalf("NewGitHub: %v", err)
	}
	return gh
}

func testRef() Ref { return Ref{Owner: "o", Repo: "r", Number: 7} }

func TestGitHubRequiresToken(t *testing.T) {
	if _, err := NewGitHub(GitHubOptions{}); err == nil {
		t.Fatal("want an error when no token is supplied")
	}
}

// TestGitHubRequestsAreBounded: the review's only context comes from
// signal.NotifyContext and carries no deadline, so with http.DefaultClient, no
// timeout at all, a connection the far side accepts and never answers hangs the
// run forever, with nothing logged after "parsed diff" and no way to tell it
// apart from a slow model.
func TestGitHubRequestsAreBounded(t *testing.T) {
	gh, err := NewGitHub(GitHubOptions{Token: "t"})
	if err != nil {
		t.Fatalf("NewGitHub: %v", err)
	}

	if timeout := gh.client.Client().Timeout; timeout <= 0 {
		t.Errorf("http client timeout = %v; a stalled connection would never be broken", timeout)
	}
}

func TestGitHubValidatesRef(t *testing.T) {
	gh := newFakeGitHub(t, func(http.ResponseWriter, *http.Request) {
		t.Error("no request should be made for an invalid ref")
	})

	ctx := context.Background()
	if _, err := gh.PullRequest(ctx, Ref{}); err == nil {
		t.Error("want an error for a ref with no owner or repo")
	}
	if _, err := gh.Diff(ctx, Ref{Owner: "o", Repo: "r"}); err == nil {
		t.Error("want an error for a ref with no pull request number")
	}
}

func TestGitHubPullRequest(t *testing.T) {
	gh := newFakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/repos/o/r/commits/abc123") {
			// Read for a skip marker before anything else is fetched.
			_ = json.NewEncoder(w).Encode(map[string]any{"sha": "abc123", "commit": map[string]any{"message": "Add retry\n\n[skip review]"}})
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/repos/o/r/pulls/7") {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); !strings.Contains(got, "test-token") {
			t.Errorf("Authorization = %q, want the token", got)
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"number": 7,
			"title":  "Add retry",
			"body":   "Retries transient failures.",
			"draft":  true,
			"user":   map[string]any{"login": "alice"},
			"base":   map[string]any{"ref": "main"},
			"head":   map[string]any{"ref": "feature", "sha": "abc123", "repo": map[string]any{"html_url": "https://forge.example/fork/repo"}},
		})
	})

	pr, err := gh.PullRequest(context.Background(), testRef())
	if err != nil {
		t.Fatalf("PullRequest: %v", err)
	}

	if pr.Title != "Add retry" || pr.Author != "alice" || pr.HeadSHA != "abc123" {
		t.Errorf("pull request = %+v", pr)
	}
	if pr.SourceBaseURL != "https://forge.example/fork/repo/blob/abc123" {
		t.Fatalf("source URL did not preserve forge, fork and revision: %q", pr.SourceBaseURL)
	}
	if !pr.Draft {
		t.Error("Draft should be carried through")
	}
	if !strings.Contains(pr.HeadMessage, "[skip review]") {
		t.Errorf("HeadMessage = %q, want the head commit's message", pr.HeadMessage)
	}
	if m, ok := SkipRequested(pr, []string{"[skip review]"}); !ok || m != "[skip review]" {
		t.Errorf("SkipRequested = %q, %v", m, ok)
	}
}

func TestGitHubDiffRequestsDiffMediaType(t *testing.T) {
	const body = "diff --git a/a.go b/a.go\n"

	gh := newFakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		// Without this Accept header GitHub returns JSON, not a diff.
		if accept := r.Header.Get("Accept"); !strings.Contains(accept, "diff") {
			t.Errorf("Accept = %q, want the diff media type", accept)
		}
		_, _ = io.WriteString(w, body)
	})

	got, err := gh.Diff(context.Background(), testRef())
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if string(got) != body {
		t.Errorf("diff = %q", got)
	}
}

func TestGitHubFileContentMissingIsErrNotFound(t *testing.T) {
	gh := newFakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/contents/") {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"message":"Not Found"}`)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"head": map[string]any{"sha": "abc"}})
	})

	_, err := gh.FileContent(context.Background(), Ref{Owner: "o", Repo: "r", Number: 7, Head: "abc"}, "missing.go")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound so assembly can degrade gracefully", err)
	}
}

// capturedReview is the payload a review POST carried.
type capturedReview struct {
	Body     string `json:"body"`
	Event    string `json:"event"`
	Comments []struct {
		Path string `json:"path"`
		Line int    `json:"line"`
		Side string `json:"side"`
		Body string `json:"body"`
	} `json:"comments"`
}

func TestGitHubPublishReviewPostsOneReview(t *testing.T) {
	var (
		captured capturedReview
		posts    int
	)

	gh := newFakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		posts++

		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 1})
	})

	err := gh.PublishReview(context.Background(), testRef(), Review{
		Summary: "Walkthrough.",
		Comments: []Comment{
			{Path: "a.go", Line: 4, Side: SideRight, Body: "Problem here."},
			{Path: "b.go", Line: 9, Body: "Another."},
		},
	})
	if err != nil {
		t.Fatalf("PublishReview: %v", err)
	}

	// One review, not one request per comment: N comments would send N
	// notifications and cannot be resolved as a unit.
	if posts != 1 {
		t.Errorf("posts = %d, want 1", posts)
	}
	if len(captured.Comments) != 2 {
		t.Fatalf("comments = %d, want 2", len(captured.Comments))
	}
	if captured.Event != string(EventComment) {
		t.Errorf("event = %q, want COMMENT: the bot should not block merges by default", captured.Event)
	}
	// An unset side must still be sent, or GitHub rejects the comment.
	if captured.Comments[1].Side != SideRight {
		t.Errorf("side = %q, want RIGHT by default", captured.Comments[1].Side)
	}
	if !strings.Contains(captured.Body, DefaultBotMarker) {
		t.Error("published body should carry the bot marker")
	}
}

func TestGitHubPublishReviewTruncatesLongReviews(t *testing.T) {
	var captured capturedReview

	gh := newFakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&captured)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 1})
	})

	var comments []Comment
	for i := range maxCommentsPerReview + 10 {
		comments = append(comments, Comment{Path: "a.go", Line: i + 1, Body: fmt.Sprintf("c%d", i)})
	}

	if err := gh.PublishReview(context.Background(), testRef(), Review{Summary: "s", Comments: comments}); err != nil {
		t.Fatalf("PublishReview: %v", err)
	}

	if len(captured.Comments) != maxCommentsPerReview {
		t.Errorf("comments = %d, want the cap of %d", len(captured.Comments), maxCommentsPerReview)
	}
	// Silent truncation would misrepresent the review as complete.
	if !strings.Contains(captured.Body, "omitted") {
		t.Errorf("summary should say findings were omitted:\n%s", captured.Body)
	}
}

func TestGitHubPublishReviewFallsBackToSummary(t *testing.T) {
	// GitHub rejects a comment whose line is not in its computed diff. Losing
	// the entire review over one bad anchor would waste the whole run.
	var bodies []string

	gh := newFakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		var payload capturedReview
		_ = json.NewDecoder(r.Body).Decode(&payload)
		bodies = append(bodies, payload.Body)

		if len(payload.Comments) > 0 {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = io.WriteString(w, `{"message":"line must be part of the diff"}`)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 1})
	})

	err := gh.PublishReview(context.Background(), testRef(), Review{
		Summary:  "Walkthrough.",
		Comments: []Comment{{Path: "a.go", Line: 4, Body: "x"}},
	})
	if err != nil {
		t.Fatalf("PublishReview should fall back rather than fail: %v", err)
	}

	if len(bodies) != 2 {
		t.Fatalf("requests = %d, want an initial attempt and a summary-only fallback", len(bodies))
	}
	// The degradation must be visible, not silent.
	if !strings.Contains(bodies[1], "Inline comments could not be posted") {
		t.Errorf("fallback should explain itself:\n%s", bodies[1])
	}
}

func TestGitHubPublishReviewPropagatesRealErrors(t *testing.T) {
	gh := newFakeGitHub(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"message":"Bad credentials"}`)
	})

	err := gh.PublishReview(context.Background(), testRef(), Review{
		Summary:  "s",
		Comments: []Comment{{Path: "a.go", Line: 1, Body: "x"}},
	})
	if err == nil {
		t.Fatal("an auth failure must not be swallowed by the fallback")
	}
}

func TestRefFromEnv(t *testing.T) {
	cases := []struct {
		name   string
		env    map[string]string
		want   Ref
		wantOK bool
	}{
		{
			name:   "pull request event",
			env:    map[string]string{"GITHUB_REPOSITORY": "acme/widgets", "GITHUB_REF": "refs/pull/42/merge"},
			want:   Ref{Owner: "acme", Repo: "widgets", Number: 42},
			wantOK: true,
		},
		{
			name:   "explicit PR_NUMBER",
			env:    map[string]string{"GITHUB_REPOSITORY": "acme/widgets", "PR_NUMBER": "9"},
			want:   Ref{Owner: "acme", Repo: "widgets", Number: 9},
			wantOK: true,
		},
		{
			name: "push event has no pull request",
			env:  map[string]string{"GITHUB_REPOSITORY": "acme/widgets", "GITHUB_REF": "refs/heads/main"},
		},
		{
			name: "outside actions",
			env:  map[string]string{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := RefFromEnv(func(k string) string { return tc.env[k] })
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && got != tc.want {
				t.Errorf("ref = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestRefStringIdentifiesReviewTarget(t *testing.T) {
	if got := (Ref{Owner: "o", Repo: "r", Number: 3}).String(); got != "o/r#3" {
		t.Errorf("String = %q", got)
	}
	if got := (Ref{Base: "main", Head: "HEAD"}).String(); got != "main...HEAD" {
		t.Errorf("String = %q", got)
	}
}

// TestPublishAlwaysSendsBody is the regression test for review.summary: false
// rejecting every review.
//
// GitHub documents body as required for COMMENT and REQUEST_CHANGES events, so
// omitting it 422s the whole payload, inline comments included.
func TestPublishAlwaysSendsBody(t *testing.T) {
	var captured capturedReview

	gh := newFakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&captured)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 1})
	})

	// Summary disabled, which is a supported configuration.
	err := gh.PublishReview(context.Background(), testRef(), Review{
		Comments: []Comment{{Path: "a.go", Line: 4, Body: "x"}},
	})
	if err != nil {
		t.Fatalf("PublishReview: %v", err)
	}

	if strings.TrimSpace(captured.Body) == "" {
		t.Error("body must never be empty: GitHub requires it for COMMENT reviews")
	}
	if !strings.Contains(captured.Body, DefaultBotMarker) {
		t.Error("the generated body should still carry the bot marker")
	}
}

func TestSummaryFallbackIsBounded(t *testing.T) {
	// The original body can itself be why the review was rejected (a PR
	// deleting thousands of files produces an enormous skipped-files section),
	// so resending it verbatim would fail identically.
	huge := strings.Repeat("x", 200000)

	got := summaryFallback(huge, errors.New("body is too long"))
	if len(got) > maxSummaryBytes+1000 {
		t.Errorf("fallback body is %d bytes; it must be bounded or it will 422 again", len(got))
	}
	if !strings.Contains(got, "could not be posted") {
		t.Error("the fallback must still explain the degradation")
	}
}

// The conversation methods hit the endpoints a reply, a comment, a thread
// read and a reaction need, each marked or scoped as the tool's own.
func TestGitHubConversationMethods(t *testing.T) {
	var got []string
	gh := newFakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Method+" "+r.URL.Path)
		switch {
		case strings.HasSuffix(r.URL.Path, "/pulls/7/comments") && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": 20, "body": "finding", "user": map[string]any{"login": "open-nitpick[bot]"}},
				{"id": 21, "in_reply_to_id": 20, "body": "why?", "user": map[string]any{"login": "carol"}},
				{"id": 22, "body": "other thread", "user": map[string]any{"login": "dave"}},
			})
		case strings.HasSuffix(r.URL.Path, "/pulls/7/comments"):
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if s, _ := body["body"].(string); !strings.Contains(s, DefaultBotMarker) {
				t.Errorf("reply body lacks the marker: %v", body)
			}
			if n, _ := body["in_reply_to"].(float64); n != 20 {
				t.Errorf("reply is not on thread 20: %v", body)
			}
			got = append(got, "reply-to-20")
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 30})
		case strings.HasSuffix(r.URL.Path, "/issues/7/comments"):
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 31})
		case strings.Contains(r.URL.Path, "/reactions"):
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 1, "content": "eyes"})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	})
	ctx := context.Background()
	ref := testRef()
	if err := gh.ReplyToReviewComment(ctx, ref, 20, "answer"); err != nil {
		t.Fatal(err)
	}
	if err := gh.CommentOnPullRequest(ctx, ref, "hello"); err != nil {
		t.Fatal(err)
	}
	thread, err := gh.ThreadComments(ctx, ref, 20)
	if err != nil || len(thread) != 2 || thread[0].ID != 20 || thread[1].Author != "carol" {
		t.Errorf("thread = %+v, %v", thread, err)
	}
	if err := gh.React(ctx, ref, 21, true, "eyes"); err != nil {
		t.Fatal(err)
	}
	if err := gh.React(ctx, ref, 40, false, "+1"); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(got, "\n")
	for _, want := range []string{"reply-to-20", "/issues/7/comments", "/pulls/comments/21/reactions", "/issues/comments/40/reactions"} {
		if !strings.Contains(joined, want) {
			t.Errorf("no request to %s:\n%s", want, joined)
		}
	}
}

func TestGitHubMissingContentDoesNotBecomeAParentDirectoryError(t *testing.T) {
	parentReads := 0
	gh := newFakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/repos/o/r/contents/cmd/nitpick/go.mod":
			if r.URL.Query().Get("ref") != "abc" {
				t.Error("content read did not pin the head")
			}
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"message":"Not Found"}`)
		case "/api/v3/repos/o/r/contents/cmd/nitpick":
			parentReads++
			_, _ = io.WriteString(w, `[{"name":"main.go","type":"file"}]`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	_, err := gh.FileContent(t.Context(), Ref{Owner: "o", Repo: "r", Number: 7, Head: "abc"}, "cmd/nitpick/go.mod")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing file lost its classification: %v", err)
	}
	if parentReads != 0 {
		t.Fatalf("missing file caused %d parent directory reads", parentReads)
	}
}

func TestGitHubContentPreservesEmptyFilesAndAccessFailures(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusForbidden} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			gh := newFakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v3/repos/o/r/contents/empty.go" {
					t.Errorf("unexpected fallback request %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
					return
				}
				w.WriteHeader(status)
				if status == http.StatusOK {
					_, _ = io.WriteString(w, `{"type":"file","encoding":"base64","content":"","size":0}`)
				} else {
					_, _ = io.WriteString(w, `{"message":"denied"}`)
				}
			})
			body, err := gh.FileContent(t.Context(), Ref{Owner: "o", Repo: "r", Number: 7, Head: "abc"}, "empty.go")
			if status == http.StatusOK {
				if err != nil || len(body) != 0 {
					t.Fatalf("empty file became missing: %q %v", body, err)
				}
			} else if err == nil || errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), "403") {
				t.Fatalf("access failure became missing content: %v", err)
			}
		})
	}
}

func TestGitHubContentReadsInlineAndDownloadedBodies(t *testing.T) {
	const source = "package source\n"
	for _, tc := range []struct {
		name      string
		download  bool
		status    int
		body      string
		wantError bool
	}{
		{"inline", false, http.StatusOK, source, false},
		{"download", true, http.StatusOK, source, false},
		{"download failure", true, http.StatusNotFound, source, true},
		{"truncated download", true, http.StatusOK, "short", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			downloads := 0
			gh := newFakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v3/repos/o/r/contents/source.go":
					encoding, content := "base64", base64.StdEncoding.EncodeToString([]byte(source))
					if tc.download {
						encoding, content = "none", ""
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"type": "file", "name": "source.go", "encoding": encoding, "content": content, "size": len(source)})
				case "/api/v3/repos/o/r/contents/":
					_ = json.NewEncoder(w).Encode([]map[string]any{{"type": "file", "name": "source.go", "download_url": "http://" + r.Host + "/download"}})
				case "/download":
					downloads++
					w.WriteHeader(tc.status)
					_, _ = io.WriteString(w, tc.body)
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			})
			got, err := gh.FileContent(t.Context(), Ref{Owner: "o", Repo: "r", Number: 7, Head: "abc"}, "source.go")
			if tc.wantError {
				if err == nil || errors.Is(err, ErrNotFound) {
					t.Fatalf("known file download failure became absence or source: %q %v", got, err)
				}
			} else if err != nil || string(got) != source {
				t.Fatalf("source changed: %q %v", got, err)
			}
			expected := 0
			if tc.download {
				expected = 1
			}
			if downloads != expected {
				t.Fatalf("download requests=%d, want %d", downloads, expected)
			}
		})
	}
}

func TestGitHubNonFileCannotBecomeEmptySource(t *testing.T) {
	for _, metadata := range []string{
		`{"type":"file","size":0,"content":null,"download_url":null,"submodule_git_url":"https://github.com/o/dependency.git"}`,
		`{"type":"submodule","size":0,"content":null}`,
		`{"type":"symlink","size":0,"content":null,"target":"outside"}`,
	} {
		t.Run(metadata, func(t *testing.T) {
			gh := newFakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v3/repos/o/r/contents/vendor-module" {
					t.Errorf("unexpected fallback request %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
					return
				}
				_, _ = io.WriteString(w, metadata)
			})
			body, err := gh.FileContent(t.Context(), Ref{Owner: "o", Repo: "r", Number: 7, Head: "abc"}, "vendor-module")
			if err == nil || errors.Is(err, ErrNotFound) || len(body) != 0 {
				t.Fatalf("known submodule became source or absence: %q %v", body, err)
			}
		})
	}
}
