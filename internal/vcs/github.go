package vcs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/go-github/v74/github"
)

// GitHub reviews pull requests through the GitHub REST API.
type GitHub struct {
	client *github.Client

	// Bot marks published comments so a follow-up run can recognize its own
	// previous output rather than piling duplicates onto a pull request.
	Bot string
}

// GitHubOptions configure the provider.
type GitHubOptions struct {
	// Token authenticates the API. Required.
	Token string

	// BaseURL points at a GitHub Enterprise instance. Empty uses github.com.
	BaseURL string

	// Bot is the marker appended to published comments.
	Bot string
}

// DefaultBotMarker identifies comments this tool published.
const DefaultBotMarker = "<!-- open-nitpick -->"

// requestTimeout bounds a single API call.
//
// github.NewClient(nil) uses http.DefaultClient, which has no timeout at all,
// and the only context in play comes from signal.NotifyContext with no deadline
// — so a connection the far side accepts and never answers hangs the review
// forever with nothing logged after "parsed diff". It is generous because one
// of these calls streams a file body; the point is that there is a ceiling, not
// where it sits.
const requestTimeout = 2 * time.Minute

// NewGitHub builds a GitHub provider.
func NewGitHub(opts GitHubOptions) (*GitHub, error) {
	token := strings.TrimSpace(opts.Token)
	if token == "" {
		return nil, errors.New("github: a token is required (set GITHUB_TOKEN)")
	}

	client := github.NewClient(&http.Client{Timeout: requestTimeout}).WithAuthToken(token)

	if base := strings.TrimSpace(opts.BaseURL); base != "" {
		var err error
		client, err = client.WithEnterpriseURLs(base, base)
		if err != nil {
			return nil, fmt.Errorf("github: enterprise base url: %w", err)
		}
	}

	bot := opts.Bot
	if bot == "" {
		bot = DefaultBotMarker
	}

	return &GitHub{client: client, Bot: bot}, nil
}

// Name identifies the provider.
func (g *GitHub) Name() string { return "github" }

// PullRequest fetches pull request metadata.
func (g *GitHub) PullRequest(ctx context.Context, ref Ref) (*PullRequest, error) {
	if err := validateRef(ref); err != nil {
		return nil, err
	}

	pr, _, err := g.client.PullRequests.Get(ctx, ref.Owner, ref.Repo, ref.Number)
	if err != nil {
		return nil, fmt.Errorf("github: get pull request %s: %w", ref, err)
	}

	out := &PullRequest{
		Number:  pr.GetNumber(),
		Title:   pr.GetTitle(),
		Body:    pr.GetBody(),
		Author:  pr.GetUser().GetLogin(),
		BaseRef: pr.GetBase().GetRef(),
		BaseSHA: pr.GetBase().GetSHA(),
		HeadRef: pr.GetHead().GetRef(),
		HeadSHA: pr.GetHead().GetSHA(),
		Draft:   pr.GetDraft(),
	}
	return out, nil
}

// Diff fetches the pull request's unified diff.
func (g *GitHub) Diff(ctx context.Context, ref Ref) ([]byte, error) {
	if err := validateRef(ref); err != nil {
		return nil, err
	}

	raw, _, err := g.client.PullRequests.GetRaw(ctx, ref.Owner, ref.Repo, ref.Number,
		github.RawOptions{Type: github.Diff})
	if err != nil {
		return nil, fmt.Errorf("github: get diff for %s: %w", ref, err)
	}
	return []byte(raw), nil
}

// BaseRevision returns the commit the pull request is measured against.
//
// The SHA is preferred over the branch name because the branch moves: whatever
// is read at the base has to be the state the pull request diverged from, not
// whatever has landed on main since it was opened.
func (g *GitHub) BaseRevision(ctx context.Context, ref Ref) (string, error) {
	pr, err := g.PullRequest(ctx, ref)
	if err != nil {
		return "", err
	}

	if sha := strings.TrimSpace(pr.BaseSHA); sha != "" {
		return sha, nil
	}
	if name := strings.TrimSpace(pr.BaseRef); name != "" {
		return name, nil
	}
	return "", fmt.Errorf("github: %s: %w", ref, ErrNoBaseRevision)
}

// FileContent fetches a file at the pull request's head.
func (g *GitHub) FileContent(ctx context.Context, ref Ref, path string) ([]byte, error) {
	if err := validateRef(ref); err != nil {
		return nil, err
	}

	sha := ref.Head
	if sha == "" {
		pr, err := g.PullRequest(ctx, ref)
		if err != nil {
			return nil, err
		}
		sha = pr.HeadSHA
	}

	// GetContents caps inline content at 1 MB and returns a download URL
	// beyond that; DownloadContents handles both.
	reader, resp, err := g.client.Repositories.DownloadContents(ctx, ref.Owner, ref.Repo, path,
		&github.RepositoryContentGetOptions{Ref: sha})
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("%s at %s: %w", path, sha, ErrNotFound)
		}
		return nil, fmt.Errorf("github: get contents %s: %w", path, err)
	}
	defer func() { _ = reader.Close() }()

	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("github: read contents %s: %w", path, err)
	}
	return data, nil
}

// maxCommentsPerReview bounds a single published review.
//
// GitHub rejects oversized review payloads, and a pull request buried under a
// hundred bot comments is unreviewable anyway. Findings are sorted most severe
// first, so truncation drops the least important ones — and says so.
const maxCommentsPerReview = 40

// PublishReview submits the review.
//
// Comments are posted as a single review rather than individually: one review
// sends one notification and can be resolved as a unit, where N comments send N
// notifications.
func (g *GitHub) PublishReview(ctx context.Context, ref Ref, review Review) error {
	if err := validateRef(ref); err != nil {
		return err
	}

	comments := review.Comments
	truncated := 0
	if len(comments) > maxCommentsPerReview {
		truncated = len(comments) - maxCommentsPerReview
		comments = comments[:maxCommentsPerReview]
	}

	body := review.Summary
	if truncated > 0 {
		body += fmt.Sprintf("\n\n_%d further finding(s) were omitted to keep this review readable._", truncated)
	}
	if body != "" {
		body += "\n" + g.Bot
	}

	drafts := make([]*github.DraftReviewComment, 0, len(comments))
	for _, c := range comments {
		side := c.Side
		if side == "" {
			side = SideRight
		}

		drafts = append(drafts, &github.DraftReviewComment{
			Path: github.Ptr(c.Path),
			Line: github.Ptr(c.Line),
			Side: github.Ptr(side),
			Body: github.Ptr(c.Body + "\n" + g.Bot),
		})
	}

	event := string(review.Event)
	if event == "" {
		event = string(EventComment)
	}

	// GitHub documents body as required for COMMENT and REQUEST_CHANGES, so an
	// omitted body rejects the whole review — which is exactly what happened
	// with review.summary disabled.
	if strings.TrimSpace(body) == "" {
		body = defaultReviewBody(len(comments), g.Bot)
	}

	request := &github.PullRequestReviewRequest{
		Event:    github.Ptr(event),
		Comments: drafts,
		Body:     github.Ptr(body),
	}

	_, _, err := g.client.PullRequests.CreateReview(ctx, ref.Owner, ref.Repo, ref.Number, request)
	if err == nil {
		return nil
	}

	// A comment can still be rejected if the line is not part of the diff
	// GitHub computed. Rather than lose the whole review, fall back to posting
	// the summary alone: a reviewer gets the walkthrough and can act on it.
	if len(drafts) > 0 && isUnprocessable(err) {
		if _, _, fallbackErr := g.client.PullRequests.CreateReview(ctx, ref.Owner, ref.Repo, ref.Number,
			&github.PullRequestReviewRequest{
				Event: github.Ptr(string(EventComment)),
				Body:  github.Ptr(summaryFallback(body, err)),
			}); fallbackErr == nil {
			return nil
		}
	}

	return fmt.Errorf("github: create review on %s: %w", ref, err)
}

// defaultReviewBody is used when summaries are disabled, since GitHub requires
// a body for COMMENT and REQUEST_CHANGES reviews.
func defaultReviewBody(comments int, marker string) string {
	if comments == 0 {
		return "open-nitpick found nothing to comment on.\n" + marker
	}
	return fmt.Sprintf("open-nitpick left %d inline comment(s).\n%s", comments, marker)
}

// maxSummaryBytes keeps the fallback body inside GitHub's limit.
//
// The original body may itself be why the review was rejected — a PR deleting
// thousands of files produces an enormous skipped-files section — so resending
// it verbatim would fail identically and lose the review entirely.
const maxSummaryBytes = 60000

// summaryFallback explains why inline comments were dropped, so a silent
// degradation does not look like a clean review.
func summaryFallback(body string, cause error) string {
	if strings.TrimSpace(body) == "" {
		body = "open-nitpick could not attach inline comments to this pull request."
	}

	note := fmt.Sprintf("\n\n_Inline comments could not be posted (%v)._", truncateBytes(cause.Error(), 500))

	if len(body)+len(note) > maxSummaryBytes {
		body = truncateBytes(body, maxSummaryBytes-len(note)) + "\n\n_(summary truncated)_"
	}
	return body + note
}

// truncateBytes trims a string to at most n bytes on a rune boundary.
func truncateBytes(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// isUnprocessable reports whether the API rejected the payload's content
// rather than failing for a transport or auth reason.
func isUnprocessable(err error) bool {
	var apiErr *github.ErrorResponse
	if errors.As(err, &apiErr) && apiErr.Response != nil {
		return apiErr.Response.StatusCode == http.StatusUnprocessableEntity
	}
	return false
}

// validateRef checks that a ref identifies a pull request.
func validateRef(ref Ref) error {
	switch {
	case ref.Owner == "" || ref.Repo == "":
		return errors.New("github: owner and repo are required")
	case ref.Number <= 0:
		return errors.New("github: a pull request number is required")
	}
	return nil
}

// RefFromEnv builds a Ref from the environment GitHub Actions provides, so the
// Action wrapper needs no arguments.
func RefFromEnv(getenv func(string) string) (Ref, bool) {
	if getenv == nil {
		getenv = os.Getenv
	}

	repo := strings.TrimSpace(getenv("GITHUB_REPOSITORY"))
	owner, name, ok := strings.Cut(repo, "/")
	if !ok {
		return Ref{}, false
	}

	number, err := prNumberFromEnv(getenv)
	if err != nil {
		return Ref{}, false
	}

	return Ref{Owner: owner, Repo: name, Number: number}, true
}

// prNumberFromEnv reads the pull request number from the Actions environment.
func prNumberFromEnv(getenv func(string) string) (int, error) {
	// GITHUB_REF looks like refs/pull/123/merge on pull_request events.
	ref := getenv("GITHUB_REF")
	if rest, ok := strings.CutPrefix(ref, "refs/pull/"); ok {
		if num, _, ok := strings.Cut(rest, "/"); ok {
			return strconv.Atoi(num)
		}
	}

	if v := strings.TrimSpace(getenv("PR_NUMBER")); v != "" {
		return strconv.Atoi(v)
	}

	return 0, fmt.Errorf("no pull request number in the environment")
}
