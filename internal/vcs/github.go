package vcs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
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

	// Checkout is a local clone with the pull request's history, when the
	// caller has one (the Action always does). The API refuses a diff of
	// more than 300 files; with a checkout the diff is taken from git
	// instead, which has no such limit.
	Checkout string
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

// requestTimeout bounds a single API call. github.NewClient(nil) uses
// http.DefaultClient, which has no timeout, and signal.NotifyContext supplies
// no deadline, so without this a connection the far side accepts and never
// answers hangs the review with nothing logged after "parsed diff". It is
// generous because one of these calls streams a file body; what matters is
// that a ceiling exists, not where it sits.
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
		// Empty when the forge does not report the repository, which is why
		// a fork check compares for INEQUALITY only when both are known.
		HeadRepo: pr.GetHead().GetRepo().GetFullName(),
		BaseRepo: pr.GetBase().GetRepo().GetFullName(),
		Draft:    pr.GetDraft(),
	}
	// The head commit's message, for a skip marker; a failure to read it
	// is not a failure to review, so it is logged by absence only.
	if c, _, err := g.client.Repositories.GetCommit(ctx, ref.Owner, ref.Repo, out.HeadSHA, nil); err == nil {
		out.HeadMessage = c.GetCommit().GetMessage()
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
		if g.Checkout != "" && strings.Contains(err.Error(), "too_large") {
			pr, prErr := g.PullRequest(ctx, ref)
			if prErr != nil {
				return nil, fmt.Errorf("github: get diff for %s: %w (and the pull request could not be read for a local diff: %w)", ref, err, prErr)
			}
			out, localErr := localDiff(ctx, g.Checkout, pr.BaseSHA, pr.HeadSHA)
			if localErr != nil {
				return nil, fmt.Errorf("github: get diff for %s: %w (and a local diff failed: %w)", ref, err, localErr)
			}
			return out, nil
		}
		return nil, fmt.Errorf("github: get diff for %s: %w", ref, err)
	}
	return []byte(raw), nil
}

// localDiff is the pull request's diff from a checkout: head against the
// merge base with base, which is what the API's diff shows.
func localDiff(ctx context.Context, checkout, base, head string) ([]byte, error) {
	return (&Local{Dir: checkout}).gitRaw(ctx, "diff", "--no-color", "--no-ext-diff", "--find-renames", base+"..."+head)
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

// ListDir names the entries of a directory at the pull request's head.
func (g *GitHub) ListDir(ctx context.Context, ref Ref, dir string) ([]string, error) {
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

	file, entries, resp, err := g.client.Repositories.GetContents(ctx, ref.Owner, ref.Repo, strings.Trim(dir, "/"),
		&github.RepositoryContentGetOptions{Ref: sha})
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("%s at %s: %w", dir, sha, ErrNotFound)
		}
		return nil, fmt.Errorf("github: list %s: %w", dir, err)
	}
	if file != nil {
		return nil, fmt.Errorf("%s is a file: %w", dir, ErrNotFound)
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		switch e.GetType() {
		case "dir":
			names = append(names, e.GetName()+"/")
		case "file":
			names = append(names, e.GetName())
		}
	}
	// The API's order is not documented; sorted, the same tree lists the
	// same way on every run, which is what makes a capped walk over it
	// deterministic.
	sort.Strings(names)
	return names, nil
}

// maxCommentsPerReview bounds a single published review.
//
// GitHub rejects oversized review payloads, and a pull request buried under a
// hundred bot comments is unreviewable anyway. Findings are sorted most severe
// first, so truncation drops the least important ones, and says so.
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
	// The head marker goes on the review body rather than on each comment
	// because a review with no comments still has to record what it read: the
	// next push must not re-review files this one found clean.
	if marker := headMarker(review.Head); marker != "" {
		body += "\n" + marker
	}
	if marker := spendMarker(review.Spend); marker != "" {
		body += "\n" + marker
	}

	drafts := make([]*github.DraftReviewComment, 0, len(comments))
	for _, c := range comments {
		side := c.Side
		if side == "" {
			side = SideRight
		}

		body := c.Body + "\n" + g.Bot
		if marker := fingerprintMarker(c.Fingerprint, c.Class); marker != "" {
			body += "\n" + marker
		}

		draft := &github.DraftReviewComment{
			Path: github.Ptr(c.Path),
			Line: github.Ptr(c.Line),
			Side: github.Ptr(side),
			Body: github.Ptr(body),
		}
		if c.StartLine > 0 && c.StartLine < c.Line {
			draft.StartLine = github.Ptr(c.StartLine)
			draft.StartSide = github.Ptr(side)
		}
		drafts = append(drafts, draft)
	}

	event := string(review.Event)
	if event == "" {
		event = string(EventComment)
	}

	// GitHub documents body as required for COMMENT and REQUEST_CHANGES, so an
	// omitted body rejects the whole review, which is exactly what happened
	// with review.summary disabled.
	if strings.TrimSpace(body) == "" {
		body = defaultReviewBody(len(comments), g.Bot)
		if marker := headMarker(review.Head); marker != "" {
			body += "\n" + marker
		}
		if marker := spendMarker(review.Spend); marker != "" {
			body += "\n" + marker
		}
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

	if isForbidden(err) {
		return fmt.Errorf("github: create review on %s: %w: %w", ref, ErrForbidden, err)
	}
	return fmt.Errorf("github: create review on %s: %w", ref, err)
}

// isForbidden reports whether the API refused the token's authority to write.
// 403 is the documented answer; 404 is what GitHub returns for a private
// repository the token cannot see at all, and for the review endpoint on a
// fork's pull request under a read-only token in some configurations.
func isForbidden(err error) bool {
	var apiErr *github.ErrorResponse
	if errors.As(err, &apiErr) && apiErr.Response != nil {
		return apiErr.Response.StatusCode == http.StatusForbidden || apiErr.Response.StatusCode == http.StatusNotFound
	}
	return false
}

// PriorReview reads back what earlier runs of this tool published on the pull
// request: the revision the latest run looked at, and every inline comment
// still carrying this tool's marker.
//
// Both come from the forge rather than from state kept anywhere else, because
// a GitHub Actions job has nowhere else. A comment a human deleted is gone from
// the answer, which is the right reading, deleting the bot's comment is how a
// reviewer asks for it not to be there, not for it to be re-posted.
func (g *GitHub) PriorReview(ctx context.Context, ref Ref) (*PriorReview, error) {
	if err := validateRef(ref); err != nil {
		return nil, err
	}

	out := &PriorReview{}

	// The head of the LATEST review this tool submitted. Reviews arrive oldest
	// first and a run on a stale commit may finish after a run on a newer one,
	// so the highest review id wins rather than the last page's last entry.
	var latestID int64
	opts := &github.ListOptions{PerPage: 100}
	for {
		reviews, resp, err := g.client.PullRequests.ListReviews(ctx, ref.Owner, ref.Repo, ref.Number, opts)
		if err != nil {
			return nil, fmt.Errorf("github: list reviews on %s: %w", ref, err)
		}
		for _, r := range reviews {
			body := r.GetBody()
			if !strings.Contains(body, g.Bot) {
				continue
			}
			// Spend is totalled over every run, so it is read before the
			// head check: the newest run owns Head, but each earlier one
			// still owns what it spent.
			if spent, ok := parseSpend(body); ok {
				out.Spend += spent
			}

			head, ok := parseHead(body)
			if !ok || r.GetID() < latestID {
				continue
			}
			latestID = r.GetID()
			out.Head = head
		}
		if resp == nil || resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	copts := &github.PullRequestListCommentsOptions{ListOptions: github.ListOptions{PerPage: 100}}
	for {
		comments, resp, err := g.client.PullRequests.ListComments(ctx, ref.Owner, ref.Repo, ref.Number, copts)
		if err != nil {
			return nil, fmt.Errorf("github: list review comments on %s: %w", ref, err)
		}
		for _, c := range comments {
			body := c.GetBody()
			if !strings.Contains(body, g.Bot) {
				continue
			}
			fp, class, ok := parseFingerprint(body)
			if !ok {
				continue
			}
			out.Comments = append(out.Comments, PriorComment{
				ID:          c.GetID(),
				Path:        c.GetPath(),
				Line:        c.GetLine(),
				Fingerprint: fp,
				Class:       class,
			})
		}
		if resp == nil || resp.NextPage == 0 {
			break
		}
		copts.Page = resp.NextPage
	}

	return out, nil
}

// ChangedSince names the files that differ between an earlier reviewed
// revision and the pull request's current head.
//
// A force push is the case that matters. The earlier head may no longer be
// reachable at all, in which case the API answers 404, or the branch may have
// been rebased so that the comparison reads "diverged"; either way the honest
// answer is that the question cannot be answered, and the caller reviews the
// whole change again rather than a guess at part of it.
func (g *GitHub) ChangedSince(ctx context.Context, ref Ref, since string) ([]string, bool, error) {
	if err := validateRef(ref); err != nil {
		return nil, false, err
	}
	since = strings.TrimSpace(since)
	if since == "" {
		return nil, false, nil
	}

	head := ref.Head
	if head == "" {
		pr, err := g.PullRequest(ctx, ref)
		if err != nil {
			return nil, false, err
		}
		head = pr.HeadSHA
	}

	var paths []string
	seen := map[string]bool{}
	opts := &github.ListOptions{PerPage: 100}
	for {
		cmp, resp, err := g.client.Repositories.CompareCommits(ctx, ref.Owner, ref.Repo, since, head, opts)
		if err != nil {
			if resp != nil && resp.StatusCode == http.StatusNotFound {
				// The earlier head is gone: rewritten away by a force push.
				return nil, false, nil
			}
			return nil, false, fmt.Errorf("github: compare %s...%s: %w", since, head, err)
		}

		switch cmp.GetStatus() {
		case "ahead", "identical":
			// The earlier head is an ancestor of this one (or is this one),
			// so the files between them are exactly what the pushes since
			// touched.
		default:
			// "diverged" or "behind": the branch was rewritten, and a file
			// list between two unrelated commits says nothing about what the
			// pull request's diff now contains.
			return nil, false, nil
		}

		for _, f := range cmp.Files {
			for _, p := range []string{f.GetFilename(), f.GetPreviousFilename()} {
				if p != "" && !seen[p] {
					seen[p] = true
					paths = append(paths, p)
				}
			}
		}

		if resp == nil || resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	return paths, true, nil
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
// The original body may itself be why the review was rejected, a PR deleting
// thousands of files produces an enormous skipped-files section, so resending
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

// ResolveThreads replies to each comment and resolves its review thread.
// Threads are a GraphQL notion: the REST comment id is matched to its thread
// through the thread's first comment, and a thread already resolved, or one
// this tool cannot find, is left alone and not reported as resolved.
func (g *GitHub) ResolveThreads(ctx context.Context, ref Ref, commentIDs []int64, reply string) ([]int64, error) {
	if err := validateRef(ref); err != nil {
		return nil, err
	}
	if len(commentIDs) == 0 {
		return nil, nil
	}
	threads, err := g.reviewThreads(ctx, ref)
	if err != nil {
		return nil, err
	}
	var resolved []int64
	for _, id := range commentIDs {
		th, ok := threads[id]
		if !ok || th.resolved {
			continue
		}
		if reply != "" {
			if _, _, err := g.client.PullRequests.CreateCommentInReplyTo(ctx, ref.Owner, ref.Repo, ref.Number, reply, id); err != nil {
				return resolved, fmt.Errorf("github: reply to comment %d on %s: %w", id, ref, err)
			}
		}
		if err := g.graphql(ctx, `mutation($id: ID!) { resolveReviewThread(input: {threadId: $id}) { thread { isResolved } } }`, map[string]any{"id": th.id}, nil); err != nil {
			return resolved, fmt.Errorf("github: resolve thread of comment %d on %s: %w", id, ref, err)
		}
		resolved = append(resolved, id)
	}
	return resolved, nil
}

type reviewThread struct {
	id       string
	resolved bool
}

// reviewThreads maps each thread's first comment (by REST id) to the thread.
func (g *GitHub) reviewThreads(ctx context.Context, ref Ref) (map[int64]reviewThread, error) {
	out := map[int64]reviewThread{}
	var cursor *string
	for {
		var resp struct {
			Repository struct {
				PullRequest struct {
					ReviewThreads struct {
						PageInfo struct {
							HasNextPage bool   `json:"hasNextPage"`
							EndCursor   string `json:"endCursor"`
						} `json:"pageInfo"`
						Nodes []struct {
							ID         string `json:"id"`
							IsResolved bool   `json:"isResolved"`
							Comments   struct {
								Nodes []struct {
									DatabaseID int64 `json:"databaseId"`
								} `json:"nodes"`
							} `json:"comments"`
						} `json:"nodes"`
					} `json:"reviewThreads"`
				} `json:"pullRequest"`
			} `json:"repository"`
		}
		q := `query($owner: String!, $repo: String!, $number: Int!, $after: String) {
  repository(owner: $owner, name: $repo) {
    pullRequest(number: $number) {
      reviewThreads(first: 100, after: $after) {
        pageInfo { hasNextPage endCursor }
        nodes { id isResolved comments(first: 1) { nodes { databaseId } } }
      }
    }
  }
}`
		if err := g.graphql(ctx, q, map[string]any{"owner": ref.Owner, "repo": ref.Repo, "number": ref.Number, "after": cursor}, &resp); err != nil {
			return nil, fmt.Errorf("github: list review threads on %s: %w", ref, err)
		}
		for _, n := range resp.Repository.PullRequest.ReviewThreads.Nodes {
			if len(n.Comments.Nodes) > 0 {
				out[n.Comments.Nodes[0].DatabaseID] = reviewThread{id: n.ID, resolved: n.IsResolved}
			}
		}
		page := resp.Repository.PullRequest.ReviewThreads.PageInfo
		if !page.HasNextPage {
			return out, nil
		}
		cursor = &page.EndCursor
	}
}

// graphql posts one query to the GraphQL endpoint beside the REST base URL,
// with the client's own authentication, and decodes data into out.
func (g *GitHub) graphql(ctx context.Context, query string, variables map[string]any, out any) error {
	endpoint := "https://api.github.com/graphql"
	if base := g.client.BaseURL; base != nil && base.Host != "api.github.com" {
		endpoint = base.Scheme + "://" + base.Host + "/api/graphql"
	}
	body, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := g.client.Client().Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return fmt.Errorf("graphql: %s: %w", resp.Status, err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("graphql: %s", resp.Status)
	}
	if len(envelope.Errors) > 0 {
		return fmt.Errorf("graphql: %s", envelope.Errors[0].Message)
	}
	if out != nil {
		return json.Unmarshal(envelope.Data, out)
	}
	return nil
}

// ReplyToReviewComment posts a reply on an inline thread, marked as this
// tool's so a later run recognises it.
func (g *GitHub) ReplyToReviewComment(ctx context.Context, ref Ref, commentID int64, body string) error {
	if err := validateRef(ref); err != nil {
		return err
	}
	if _, _, err := g.client.PullRequests.CreateCommentInReplyTo(ctx, ref.Owner, ref.Repo, ref.Number, body+"\n\n"+g.Bot, commentID); err != nil {
		return fmt.Errorf("github: reply on %s: %w", ref, err)
	}
	return nil
}

// CommentOnPullRequest posts a comment on the pull request's conversation.
func (g *GitHub) CommentOnPullRequest(ctx context.Context, ref Ref, body string) error {
	if err := validateRef(ref); err != nil {
		return err
	}
	if _, _, err := g.client.Issues.CreateComment(ctx, ref.Owner, ref.Repo, ref.Number, &github.IssueComment{Body: github.Ptr(body + "\n\n" + g.Bot)}); err != nil {
		return fmt.Errorf("github: comment on %s: %w", ref, err)
	}
	return nil
}

// ThreadComments returns a thread's comments, oldest first: the root and
// every reply to it.
func (g *GitHub) ThreadComments(ctx context.Context, ref Ref, rootID int64) ([]ThreadComment, error) {
	if err := validateRef(ref); err != nil {
		return nil, err
	}
	var out []ThreadComment
	opts := &github.PullRequestListCommentsOptions{ListOptions: github.ListOptions{PerPage: 100}}
	for {
		comments, resp, err := g.client.PullRequests.ListComments(ctx, ref.Owner, ref.Repo, ref.Number, opts)
		if err != nil {
			return nil, fmt.Errorf("github: list review comments on %s: %w", ref, err)
		}
		for _, c := range comments {
			if c.GetID() == rootID || c.GetInReplyTo() == rootID {
				out = append(out, ThreadComment{ID: c.GetID(), Author: c.GetUser().GetLogin(), Body: c.GetBody()})
			}
		}
		if resp == nil || resp.NextPage == 0 {
			return out, nil
		}
		opts.Page = resp.NextPage
	}
}

// AnswerMarker distinguishes an answer to a mention from every other comment
// the reviewer posts. The general bot marker sits on findings and on the
// summary too, so counting it would let a review that posted five findings
// exhaust a five-answer cap and refuse the first question anybody asked. The
// cap bounds how often the reviewer is addressed, and only these comments are.
const AnswerMarker = "<!-- nitpick:answer -->"

// CountAnswers counts the replies the reviewer has posted to mentions on a
// pull request, conversation comments and inline ones alike.
//
// It is how a per-pull-request answer cap is enforced without persisting a
// counter anywhere: the answers already posted are the record of how many
// times it has answered, and they survive a re-run, a new runner, and a
// cleared cache.
func (g *GitHub) CountAnswers(ctx context.Context, ref Ref) (int, error) {
	if err := validateRef(ref); err != nil {
		return 0, err
	}

	var n int

	opts := &github.IssueListCommentsOptions{ListOptions: github.ListOptions{PerPage: 100}}
	for {
		comments, resp, err := g.client.Issues.ListComments(ctx, ref.Owner, ref.Repo, ref.Number, opts)
		if err != nil {
			return 0, fmt.Errorf("github: list comments on %s: %w", ref, err)
		}
		for _, c := range comments {
			if strings.Contains(c.GetBody(), AnswerMarker) {
				n++
			}
		}
		if resp == nil || resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	rOpts := &github.PullRequestListCommentsOptions{ListOptions: github.ListOptions{PerPage: 100}}
	for {
		comments, resp, err := g.client.PullRequests.ListComments(ctx, ref.Owner, ref.Repo, ref.Number, rOpts)
		if err != nil {
			return 0, fmt.Errorf("github: list review comments on %s: %w", ref, err)
		}
		for _, c := range comments {
			if strings.Contains(c.GetBody(), AnswerMarker) {
				n++
			}
		}
		if resp == nil || resp.NextPage == 0 {
			return n, nil
		}
		rOpts.Page = resp.NextPage
	}
}

// React acknowledges a comment with a reaction (eyes while working, +1 when
// done), on an inline comment or a conversation comment.
func (g *GitHub) React(ctx context.Context, ref Ref, commentID int64, inline bool, content string) error {
	if err := validateRef(ref); err != nil {
		return err
	}
	var err error
	if inline {
		_, _, err = g.client.Reactions.CreatePullRequestCommentReaction(ctx, ref.Owner, ref.Repo, commentID, content)
	} else {
		_, _, err = g.client.Reactions.CreateIssueCommentReaction(ctx, ref.Owner, ref.Repo, commentID, content)
	}
	if err != nil {
		return fmt.Errorf("github: react on %s: %w", ref, err)
	}
	return nil
}
