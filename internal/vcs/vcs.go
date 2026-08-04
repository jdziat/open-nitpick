// Package vcs abstracts the forge a review runs against.
//
// The engine depends only on this interface, which is what lets the same review
// logic run offline against a local git checkout and online against a pull
// request. The local provider is not a toy: it is how the engine is developed
// and tested without credentials or network.
package vcs

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ErrNotFound reports that a requested file does not exist at a revision.
// Callers treat it as "no content available" rather than a failure, since a
// deleted file legitimately has no new-side content.
var ErrNotFound = errors.New("vcs: not found")

// ErrNoBaseRevision reports that a provider cannot name the revision a change
// is measured against.
//
// It is not a failure of the run. A caller that needed the base revision in
// order to avoid trusting the change under review falls back to something the
// change also did not write — built-in defaults — rather than carrying on with
// the change's own version.
var ErrNoBaseRevision = errors.New("vcs: base revision unavailable")

// Ref identifies what to review.
type Ref struct {
	// Owner and Repo identify the repository on a forge. Both are empty for
	// local reviews.
	Owner string
	Repo  string

	// Number is the pull request number, zero for local reviews.
	Number int

	// Base and Head are revisions. For a pull request they are filled in from
	// the forge; for a local review they come from the command line.
	Base string
	Head string
}

// At returns a copy of the ref pointing at rev, so a caller can read files as
// they exist somewhere other than the head under review.
//
// Head is what FileContent reads, which is why this sets Head rather than Base:
// "read the base version of this file" means "read this ref with its head moved
// to the base".
func (r Ref) At(rev string) Ref {
	r.Head = rev
	return r
}

// String renders the ref for logs.
func (r Ref) String() string {
	if r.Number > 0 {
		return fmt.Sprintf("%s/%s#%d", r.Owner, r.Repo, r.Number)
	}
	return fmt.Sprintf("%s...%s", r.Base, r.Head)
}

// PullRequest carries the metadata a reviewer needs to judge intent. Title and
// body matter: a change that looks wrong in isolation is often correct given
// what the author said they were doing.
type PullRequest struct {
	Number int
	Title  string
	Body   string
	Author string

	BaseRef string

	// BaseSHA is the revision the change is measured against, when the provider
	// knows it. It may be empty even where BaseRef is set, so BaseRevision is
	// how to ask for it rather than reading this directly.
	BaseSHA string

	HeadRef string
	HeadSHA string

	Draft bool
}

// Comment is one inline review comment.
type Comment struct {
	// Path is repository-relative.
	Path string

	// Line is the line in the file the comment anchors to, interpreted
	// according to Side.
	Line int

	// Side selects the diff side: SideRight for additions and unchanged
	// lines, SideLeft for deletions.
	Side string

	// Body is markdown.
	Body string
}

// Diff sides for a review comment, matching GitHub's LEFT/RIGHT parameter.
const (
	SideLeft  = "LEFT"
	SideRight = "RIGHT"
)

// Review is a complete review to publish.
type Review struct {
	// Summary is the top-level walkthrough comment. It may be empty.
	Summary string

	// Comments are inline comments.
	Comments []Comment

	// Event selects how the review is submitted.
	Event ReviewEvent
}

// ReviewEvent selects the review disposition.
type ReviewEvent string

// Review dispositions. open-nitpick defaults to Comment: a bot that can block
// merges is a bot people disable.
const (
	EventComment        ReviewEvent = "COMMENT"
	EventRequestChanges ReviewEvent = "REQUEST_CHANGES"
	EventApprove        ReviewEvent = "APPROVE"
)

// Provider is a source of review input and a sink for review output.
type Provider interface {
	// PullRequest returns metadata for the ref. Providers without pull
	// request semantics return a synthesized value describing the range.
	PullRequest(ctx context.Context, ref Ref) (*PullRequest, error)

	// Diff returns the unified diff between the ref's base and head.
	Diff(ctx context.Context, ref Ref) ([]byte, error)

	// FileContent returns a file's full contents at the ref's head. It
	// returns ErrNotFound when the file does not exist there.
	FileContent(ctx context.Context, ref Ref, path string) ([]byte, error)

	// PublishReview delivers the review.
	PublishReview(ctx context.Context, ref Ref, review Review) error

	// Name identifies the provider for logs and errors.
	Name() string
}

// BaseResolver names the revision a ref's change is measured against: the state
// of the repository that already existed, and that the change under review
// therefore did not author.
//
// It is deliberately separate from Provider. A wrapper or a test double that
// has no way to answer must be *unable* to answer — its caller then falls back
// to defaults and says so — where a method on Provider would oblige every
// implementation to return something, and the plausible-looking something is
// the head under review.
type BaseResolver interface {
	BaseRevision(ctx context.Context, ref Ref) (string, error)
}

// BaseRevision returns the revision p measures ref's change against, or
// ErrNoBaseRevision when p cannot say.
//
// An empty revision is refused rather than passed back: Local reads the working
// tree for an empty head, so a caller feeding one to Ref.At and FileContent
// would read precisely the changed version it is trying to avoid.
func BaseRevision(ctx context.Context, p Provider, ref Ref) (string, error) {
	resolver, ok := p.(BaseResolver)
	if !ok {
		return "", fmt.Errorf("%s: %w", p.Name(), ErrNoBaseRevision)
	}

	rev, err := resolver.BaseRevision(ctx, ref)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(rev) == "" {
		return "", fmt.Errorf("%s: %w", p.Name(), ErrNoBaseRevision)
	}
	return rev, nil
}
