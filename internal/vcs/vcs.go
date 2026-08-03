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
)

// ErrNotFound reports that a requested file does not exist at a revision.
// Callers treat it as "no content available" rather than a failure, since a
// deleted file legitimately has no new-side content.
var ErrNotFound = errors.New("vcs: not found")

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
