package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/converse"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// dismissalForge is the part of the forge deciding who may close a finding.
type dismissalForge interface {
	PullRequest(ctx context.Context, ref vcs.Ref) (*vcs.PullRequest, error)
	BaseRevision(ctx context.Context, ref vcs.Ref) (string, error)
	FileContent(ctx context.Context, ref vcs.Ref, path string) ([]byte, error)
	IsTeamMember(ctx context.Context, org, slug, login string) (bool, error)
}

// mayDismiss reports whether login may close a finding on path. reason is safe
// to post; cause is the forge's error, for the log, since it can carry URLs.
// Every uncertain branch refuses, and CODEOWNERS is read at the base so a
// change cannot name its own author as owner.
func mayDismiss(ctx context.Context, gh dismissalForge, cfg config.DismissRespond,
	ref vcs.Ref, login, path string) (allowed bool, reason string, cause error) {

	byAssociation := cfg.EffectiveAuthority() == config.AuthorityAssociation
	if byAssociation && cfg.AuthorMayDismiss() {
		return true, "", nil
	}

	pr, err := gh.PullRequest(ctx, ref)
	if err != nil {
		return false, "the pull request could not be read to see who wrote it", err
	}
	// allow_author is about who is asking, so it holds under either authority:
	// turning it off means an author, owner or not, does not close findings on
	// their own change.
	if strings.EqualFold(login, pr.Author) {
		if cfg.AuthorMayDismiss() {
			return true, "", nil
		}
		return false, "authors may not close findings on their own change here (review.respond.dismiss.allow_author is off)", nil
	}
	if byAssociation {
		return true, "", nil
	}

	owners, found, err := ownersAtBase(ctx, gh, ref, path)
	if err != nil {
		return false, "CODEOWNERS could not be read from the base revision", err
	}
	// No CODEOWNERS, or no rule for this path: nobody is named, so the
	// association gate that already ran is the only one there is.
	if !found || len(owners) == 0 {
		return true, "", nil
	}

	// Named logins first: they need no API call, and a team lookup the token
	// may not make must not refuse someone the file names directly.
	var teams []string
	for _, o := range owners {
		name, ok := strings.CutPrefix(o, "@")
		if !ok {
			// An email address owner cannot be matched to a login.
			continue
		}
		if strings.Contains(name, "/") {
			teams = append(teams, name)
			continue
		}
		if strings.EqualFold(name, login) {
			return true, "", nil
		}
	}
	// Reading team membership needs a token with read:org; the default
	// Actions token has none, so a team-owned path refuses under it and the
	// reason says which team could not be checked.
	var lookupErr error
	var unchecked string
	for _, team := range teams {
		org, slug, _ := strings.Cut(team, "/")
		member, err := gh.IsTeamMember(ctx, org, slug, login)
		if err != nil {
			lookupErr, unchecked = err, "@"+team
			continue
		}
		if member {
			return true, "", nil
		}
	}
	if lookupErr != nil {
		return false, fmt.Sprintf("membership of %s could not be checked", unchecked), lookupErr
	}
	return false, fmt.Sprintf("%s is owned by %s", path, strings.Join(owners, ", ")), nil
}

// ownersAtBase returns the owners CODEOWNERS names for path at the base
// revision, and whether there was a CODEOWNERS file to consult.
func ownersAtBase(ctx context.Context, gh dismissalForge, ref vcs.Ref, path string) (owners []string, found bool, err error) {
	base, err := gh.BaseRevision(ctx, ref)
	if err != nil {
		return nil, false, fmt.Errorf("could not resolve the base revision to read CODEOWNERS from: %w", err)
	}
	at := vcs.Ref{Owner: ref.Owner, Repo: ref.Repo, Head: base}
	for _, p := range vcs.CodeownersPaths {
		data, err := gh.FileContent(ctx, at, p)
		if errors.Is(err, vcs.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, false, fmt.Errorf("could not read %s at the base revision: %w", p, err)
		}
		co := vcs.ParseCodeowners(data)
		return co.OwnersOf(path), !co.Empty(), nil
	}
	return nil, false, nil
}

// closeForge is the forge a close decision reads from and answers on.
type closeForge interface {
	dismissalForge
	ReplyToReviewComment(ctx context.Context, ref vcs.Ref, commentID int64, body string) error
}

// allowClose decides whether a reply or a resolve command on a finding's
// thread may go ahead, and answers a refused one the way its kind needs.
//
// A refused reply is only logged: most replies are people talking, and a
// refusal under each would be the reviewer policing the thread. A refused
// resolve was addressed to the reviewer, so it is answered in the thread with
// the reason.
func allowClose(ctx context.Context, gh closeForge, policy *config.Config, ref vcs.Ref,
	ev *converse.Event, kind converse.Kind, log *slog.Logger) (bool, error) {

	ok, why, cause := mayDismiss(ctx, gh, policy.Review.Respond.Dismiss, ref, ev.Author, ev.Path)
	if ok {
		return true, nil
	}
	log.Info("someone who may not close this finding tried to",
		"kind", kind, "author", ev.Author, "path", ev.Path, "reason", why, "cause", cause, "comment", ev.CommentID)
	if kind != converse.KindResolve {
		return false, nil
	}
	return false, gh.ReplyToReviewComment(ctx, ref, ev.RootID,
		fmt.Sprintf("@%s cannot close this finding: %s. An owner of the file or the author of the pull request can, or a pushed fix will close it.", ev.Author, why))
}

// resolveForge is the forge a resolve command acts on.
type resolveForge interface {
	closeForge
	threadReader
	ResolveThreads(ctx context.Context, ref vcs.Ref, commentIDs []int64, reply string) ([]int64, error)
	React(ctx context.Context, ref vcs.Ref, commentID int64, inline bool, content string) error
}

// resolveOnRequest answers "@open-nitpick resolve" on an inline thread. Only a
// thread that starts with a finding is gated: resolving a conversation between
// people closes no finding.
func resolveOnRequest(ctx context.Context, gh resolveForge, policy *config.Config, ref vcs.Ref,
	ev *converse.Event, log *slog.Logger) error {

	target, err := findingBeingAnswered(ctx, gh, ref, ev, log)
	if err != nil {
		return err
	}
	if target != nil {
		if ok, err := allowClose(ctx, gh, policy, ref, ev, converse.KindResolve, log); !ok {
			return err
		}
	}
	resolved, err := gh.ResolveThreads(ctx, ref, []int64{ev.RootID}, fmt.Sprintf("Resolved on request by @%s.", ev.Author))
	if err != nil {
		return err
	}
	if len(resolved) == 0 {
		return gh.ReplyToReviewComment(ctx, ref, ev.RootID, "This thread is already resolved, or it is not one I can resolve.")
	}
	return gh.React(ctx, ref, ev.CommentID, ev.Inline, "+1")
}
