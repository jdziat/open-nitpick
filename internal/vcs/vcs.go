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

// ErrForbidden reports that the forge refused to let this token publish: on
// GitHub, the read-only GITHUB_TOKEN a pull_request event from a fork gets.
// A caller can still show the review somewhere the token can write.
var ErrForbidden = errors.New("vcs: the token may not publish here")

// ErrNoBaseRevision reports that a provider cannot name the revision a change
// is measured against.
//
// It is not a failure of the run. A caller that needed the base revision in
// order to avoid trusting the change under review falls back to something the
// change also did not write, built-in defaults, rather than carrying on with
// the change's own version.
var ErrNoBaseRevision = errors.New("vcs: base revision unavailable")

// ErrHeadMoved reports that the pull request was pushed to between the moment
// a proposal's file contents were read and the moment it was published.
//
// It matters because the failure it prevents is silent. A proposal carries
// whole files rather than a patch, so content read at the older revision would
// be re-asserted over the newer tree and quietly revert whatever landed in
// between, in a pull request that looks clean.
var ErrHeadMoved = errors.New("vcs: the pull request moved while the change was being prepared")

// ErrRefExists reports that the branch a proposal names is already there.
//
// Branch names are derived from the pull request and the finding, so the same
// request twice produces the same name. This is how a repeated ask is
// recognised rather than piling up branches.
var ErrRefExists = errors.New("vcs: the branch already exists")

// ErrOutsideChange reports that a proposal named a path the pull request under
// review does not touch.
var ErrOutsideChange = errors.New("vcs: the path is not part of this change")

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

	// HeadRepo and BaseRepo are the full names, owner/name, of the
	// repositories the two sides live in. They differ for a pull request from
	// a fork, which is the only way to tell one from here: a fork's head ref
	// looks like any other.
	HeadRepo string
	BaseRepo string

	// HeadMessage is the head commit's full message, when the provider can
	// read it; a [skip review] marker may sit there rather than in the
	// title or body.
	HeadMessage string

	Draft bool
}

// Comment is one inline review comment.
type Comment struct {
	// Path is repository-relative.
	Path string

	// Line is the line in the file the comment anchors to, interpreted
	// according to Side.
	Line int

	// StartLine, when set, makes the comment span StartLine through Line,
	// which is how a suggestion replacing several lines is delivered. Zero
	// is a single-line comment.
	StartLine int

	// Side selects the diff side: SideRight for additions and unchanged
	// lines, SideLeft for deletions.
	Side string

	// Body is the comment text, rendered as markdown by the provider.
	Body string

	// Fingerprint identifies the finding this comment reports independently
	// of its wording and line, so a later run on the same pull request can
	// recognise it as already posted. A provider that can carry it embeds it
	// in the published comment; empty means the comment is not recognisable
	// on a later run.
	Fingerprint string

	// Class is the finding's class, carried beside the fingerprint so a later
	// run can match a reworded finding of the same kind at the same place.
	Class string
}

// Diff sides for a review comment, matching GitHub's LEFT/right parameter.
const (
	SideLeft  = "LEFT"
	SideRight = "RIGHT"
)

// Review is a complete review to publish.
type Review struct {
	// Summary is the top-level walkthrough comment. It may be empty.
	Summary string

	// Comments are anchored to a line of the diff.
	Comments []Comment

	// Event selects how the review is submitted.
	Event ReviewEvent

	// Head is the revision this review looked at. A provider that can carry
	// it records it with the review, which is how the next run on the same
	// pull request knows what has already been reviewed.
	Head string

	// Spend is what this review was estimated to cost, in US dollars, and is
	// recorded with it for the same reason Head is: a ceiling that covers a
	// whole pull request has to know what earlier runs already spent. Zero
	// means nothing was priced, which is the case whenever no ceiling is
	// configured.
	Spend float64
}

// DirLister is implemented by providers that can name the entries of a
// directory at the reviewed revision. It is what lets a review find the file
// a Go package or a Python module lives in without guessing filenames.
//
// Separate from Provider for BaseResolver's reason: a provider that cannot
// list must be unable to, and the caller then attaches no related context
// rather than probing for files by name.
type DirLister interface {
	// ListDir returns the names of the entries directly under dir, with a
	// trailing slash on subdirectories. It returns ErrNotFound when dir does
	// not exist at the ref's head.
	ListDir(ctx context.Context, ref Ref, dir string) ([]string, error)
}

// PriorReview is what earlier runs of this tool left on a pull request.
type PriorReview struct {
	// Head is the revision the most recent earlier run reviewed, or empty
	// when no earlier run recorded one.
	Head string

	// Comments are the inline comments earlier runs published and that this
	// tool can recognise as its own.
	Comments []PriorComment

	// Spend totals what every earlier run recorded spending on this pull
	// request, in US dollars. It is a sum rather than the latest value: the
	// question a pull-request ceiling asks is what the branch has cost so far,
	// and each push answers for itself.
	//
	// Runs that published no spend contribute nothing, so a pull request first
	// reviewed without a ceiling starts a later ceiling from zero. That is the
	// safe direction only for coverage, not for the bill, and the run reports
	// the total it found so the gap is visible rather than assumed.
	Spend float64
}

// PriorComment is one inline comment an earlier run published.
type PriorComment struct {
	// ID is the forge's identifier for the comment, for a later run that
	// resolves its thread.
	ID int64

	Path string

	// Line is where the forge currently shows the comment, which moves as
	// the pull request is pushed to; zero when the forge no longer places it
	// on the diff.
	Line int

	// Fingerprint and Class are read back from the marker the comment was
	// published with. See Comment.Fingerprint.
	Fingerprint string
	Class       string

	// Body is the comment as published: the title, the rationale, any
	// suggestion, and the markers.
	//
	// A finding is not persisted anywhere. The fingerprint identifies one and
	// carries nothing, so this text is the only record of what a review said,
	// and a later run that wants to act on a finding rather than merely
	// recognise it has nothing else to read.
	Body string
}

// FileEdit is one file a proposal rewrites.
//
// Content is the entire new file. The forge's data API commits blobs rather
// than patches, so there is no way to express "these lines" here, and the
// whole file is what a reader of the proposal has to read.
type FileEdit struct {
	Path    string
	Content []byte
}

// Proposal is a set of file edits to publish as a branch and a pull request.
type Proposal struct {
	// Base is the commit the edits were read at, and the parent of the commit
	// they are written as. Read and write have to name the same revision, or
	// the result reverts whatever landed between them; see ErrHeadMoved.
	Base string

	// Branch is the ref to create, without refs/heads/. It is always new: a
	// proposal may not write to a branch that already exists.
	Branch string

	// Into is the branch the pull request targets.
	Into string

	Message string
	Title   string
	Body    string

	Edits []FileEdit

	// AllowPaths is the set of paths that may be written, which is the set the
	// pull request under review already changes.
	//
	// Empty refuses every edit. This is a containment boundary rather than a
	// filter, so its absence fails closed: the content being written was
	// produced by a model reading text a contributor wrote, and without this
	// the capability is an arbitrary write driven by that text.
	AllowPaths []string
}

// ProposedChange is what a published proposal became.
type ProposedChange struct {
	Branch    string
	CommitSHA string
	Number    int
	URL       string
}

// ChangeProposer is implemented by providers that can publish file edits as a
// branch and a pull request.
//
// Optional, like the other capabilities here: a caller feature-detects it and
// does without rather than assuming. It is the only interface in this package
// that writes to the repository rather than to a conversation about it.
type ChangeProposer interface {
	ProposeChange(ctx context.Context, ref Ref, p Proposal) (*ProposedChange, error)
}

// Permissioned is implemented by providers that can say what a person may do
// to the repository.
//
// This is not what the forge calls an author's association. GitHub's
// COLLABORATOR covers anyone invited to the repository, at read level
// included, so an association is a reasonable gate on spending and a weak one
// on writing.
type Permissioned interface {
	// Permission returns the forge's word for what login may do: on GitHub,
	// one of admin, write, read or none.
	Permission(ctx context.Context, ref Ref, login string) (string, error)
}

// ThreadComment is one comment on a review thread, for a conversation's
// context.
type ThreadComment struct {
	ID     int64
	Author string
	Body   string
}

// Conversationalist is implemented by providers that can carry the @mention
// conversation: reply on a thread or the conversation, read a thread, and
// acknowledge a comment with a reaction.
type Conversationalist interface {
	ReplyToReviewComment(ctx context.Context, ref Ref, commentID int64, body string) error
	CommentOnPullRequest(ctx context.Context, ref Ref, body string) error
	ThreadComments(ctx context.Context, ref Ref, rootID int64) ([]ThreadComment, error)
	React(ctx context.Context, ref Ref, commentID int64, inline bool, content string) error
}

// ThreadResolver is implemented by providers that can resolve the review
// threads this tool opened earlier, with a reply saying why. It returns the
// comment IDs whose threads were resolved.
type ThreadResolver interface {
	ResolveThreads(ctx context.Context, ref Ref, commentIDs []int64, reply string) ([]int64, error)
}

// PriorReviewer is implemented by providers that can read back what this tool
// published on a pull request earlier.
//
// It is separate from Provider for BaseResolver's reason: a provider or test
// double that cannot answer must be unable to, so the engine reviews the whole
// change rather than assuming that nothing was posted before.
type PriorReviewer interface {
	PriorReview(ctx context.Context, ref Ref) (*PriorReview, error)
}

// IncrementalDiffer is implemented by providers that can say which files
// changed between an earlier revision of the change and its current head.
type IncrementalDiffer interface {
	// ChangedSince returns the paths that differ between since and the ref's
	// current head. ok is false when the question cannot be answered, since
	// is no longer reachable from the head, as after a force push, in which
	// case the whole change has to be reviewed again.
	ChangedSince(ctx context.Context, ref Ref, since string) (paths []string, ok bool, err error)
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

	// PublishReview posts the summary and every comment as one submission.
	PublishReview(ctx context.Context, ref Ref, review Review) error

	// Name identifies the provider for logs and errors.
	Name() string
}

// BaseResolver names the revision a ref's change is measured against: the
// repository state the change under review did not author. It is separate from
// Provider so that an implementation with no way to answer is unable to answer
// and its caller falls back to defaults and says so, where a Provider method
// would oblige every implementation to return something, and the
// plausible-looking something is the head under review.
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

// SkipRequested reports whether the pull request's title, body or head
// commit message carries one of the markers, case-insensitively.
func SkipRequested(pr *PullRequest, markers []string) (string, bool) {
	if pr == nil {
		return "", false
	}
	text := strings.ToLower(pr.Title + "\n" + pr.Body + "\n" + pr.HeadMessage)
	for _, m := range markers {
		m = strings.TrimSpace(m)
		if m != "" && strings.Contains(text, strings.ToLower(m)) {
			return m, true
		}
	}
	return "", false
}
