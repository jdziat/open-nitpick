package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/converse"
	"github.com/jdziat/open-nitpick/internal/llm"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// threadReader is the part of the forge a reply lookup needs.
type threadReader interface {
	ThreadComments(ctx context.Context, ref vcs.Ref, rootID int64) ([]vcs.ThreadComment, error)
	ThreadResolved(ctx context.Context, ref vcs.Ref, rootID int64) (bool, error)
}

// replyTarget is a thread whose first comment is one of the reviewer's own
// findings, with every comment in it.
type replyTarget struct {
	root   vcs.ThreadComment
	thread []vcs.ThreadComment
}

// findingBeingAnswered returns the finding a reply sits under, or nil when the
// reply is in a thread that is not the reviewer's, or is already resolved.
//
// Nil with no error is the ordinary case and the common one: most replies on a
// pull request are people talking to each other. Each nil path logs why, so a
// reply that was ignored is a reply with a reason in the run log.
func findingBeingAnswered(ctx context.Context, gh threadReader, ref vcs.Ref, ev *converse.Event, log *slog.Logger) (*replyTarget, error) {
	thread, err := gh.ThreadComments(ctx, ref, ev.RootID)
	if err != nil {
		return nil, fmt.Errorf("read the thread a reply was left in: %w", err)
	}
	var root *vcs.ThreadComment
	for i := range thread {
		if thread[i].ID == ev.RootID {
			root = &thread[i]
			break
		}
	}
	// Written by this reviewer and carrying a fingerprint: the first says who
	// wrote it, the second that it is a finding and not an answer. A marker
	// alone is text anyone can paste into a comment.
	if root == nil || !root.Own || vcs.FingerprintOf(root.Body) == "" {
		log.Info("the reply is in a thread that does not start with a finding; nothing to do", "comment", ev.CommentID)
		return nil, nil
	}
	resolved, err := gh.ThreadResolved(ctx, ref, ev.RootID)
	if err != nil {
		return nil, fmt.Errorf("read whether the thread is resolved: %w", err)
	}
	if resolved {
		log.Info("the finding's thread is already resolved; nothing to do", "comment", ev.CommentID)
		return nil, nil
	}
	return &replyTarget{root: *root, thread: thread}, nil
}

// runReply reads a person's reply under a finding and either resolves the
// thread or says what would settle it.
func runReply(ctx context.Context, gh *vcs.GitHub, policy *config.Config, ref vcs.Ref,
	ev *converse.Event, target *replyTarget, diffBytes []byte, log *slog.Logger) error {

	client, err := llm.BuildContext(ctx, policy.Models.ResolveModel(config.RoleReview))
	if err != nil {
		return err
	}
	client.SetLogger(log)
	pr, err := gh.PullRequest(ctx, ref)
	if err != nil {
		return err
	}

	c := converse.Context{Title: pr.Title, Body: pr.Body, Diff: string(diffBytes), Path: ev.Path}
	if content, err := gh.FileContent(ctx, ref, ev.Path); err == nil {
		c.Excerpt = converse.Excerpt(string(content), ev.Line, 25)
	}
	for _, t := range target.thread {
		if t.ID == ev.CommentID {
			continue
		}
		c.Thread = append(c.Thread, t.Author+": "+strings.TrimSpace(strings.ReplaceAll(t.Body, gh.Bot, "")))
	}

	verdict, err := converse.Weigh(ctx, client, c, ev.Body)
	if err != nil {
		return err
	}
	log.Info("weighed a reply", "outcome", verdict.Outcome, "comment", ev.CommentID, "root", ev.RootID)

	// Marked as an answer, which is what review.respond.max_per_pull_request
	// counts, so replies draw from the same budget as questions.
	body := verdict.Reply + "\n\n" + vcs.AnswerMarker
	if verdict.Outcome != converse.OutcomeResolve {
		return gh.ReplyToReviewComment(ctx, ref, ev.RootID, body)
	}

	// The bot marker is added here because ResolveThreads posts its reply as
	// given, and a reply without it is a comment this reviewer cannot
	// recognise as its own on the next run.
	resolved, err := gh.ResolveThreads(ctx, ref, []int64{ev.RootID}, body+"\n"+vcs.DismissedMarker+"\n"+gh.Bot)
	if err != nil {
		return err
	}
	if len(resolved) == 0 {
		log.Info("the thread was resolved before the reply was weighed", "root", ev.RootID)
	}
	return nil
}
