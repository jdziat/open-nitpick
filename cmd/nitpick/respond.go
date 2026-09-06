package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/converse"
	"github.com/jdziat/open-nitpick/internal/llm"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// runRespond answers a comment that mentioned the reviewer. It reads the
// GitHub event that carried the comment (the Actions environment, or
// -event), works out what was asked, and either reviews the pull request
// again, resolves the thread, or answers the question in the thread with
// the change and the thread as context.
func runRespond(ctx context.Context, args []string) error {
	var (
		f         reviewFlags
		eventPath string
		eventName string
		mention   string
	)
	fs := flag.NewFlagSet("respond", flag.ContinueOnError)
	fs.StringVar(&f.repo, "repo", ".", "repository root (the checkout)")
	fs.StringVar(&f.configPath, "config", "", "path to .nitpick.yaml (default: <repo>/.nitpick.yaml)")
	fs.StringVar(&eventPath, "event", os.Getenv("GITHUB_EVENT_PATH"), "the event payload (default: GITHUB_EVENT_PATH)")
	fs.StringVar(&eventName, "event-name", os.Getenv("GITHUB_EVENT_NAME"), "the event name (default: GITHUB_EVENT_NAME)")
	fs.StringVar(&mention, "mention", "", "the handle to answer to (default: review.mention, @nitpick)")
	fs.StringVar(&f.owner, "owner", "", "GitHub repository owner (default: GITHUB_REPOSITORY)")
	fs.StringVar(&f.repoName, "repo-name", "", "GitHub repository name (default: GITHUB_REPOSITORY)")
	fs.BoolVar(&f.verbose, "v", false, "verbose logging")
	fs.StringVar(&f.logFormat, "log-format", "text", "log format: text or json")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: nitpick respond [flags]\n\nAnswers a pull request comment that mentions the reviewer: \"@nitpick review\" reviews the whole change again,\n\"@nitpick resolve\" on a thread resolves it, anything else is a question answered in the thread.\nRuns inside GitHub Actions on issue_comment and pull_request_review_comment events.\n\nFlags:")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if eventPath == "" {
		return errors.New("no event: set GITHUB_EVENT_PATH or -event")
	}
	payload, err := os.ReadFile(eventPath)
	if err != nil {
		return err
	}
	ev, err := converse.ParseEvent(eventName, payload)
	if err != nil {
		return err
	}
	repo, err := filepath.Abs(f.repo)
	if err != nil {
		return err
	}
	cfg, err := loadConfig(repo, f.configPath)
	if err != nil {
		return err
	}
	if mention == "" {
		mention = cfg.Review.Mention
	}
	log := newLogger(f.verbose, f.logFormat)

	kind, text, ok := converse.Command(ev.Body, mention)
	if !ok {
		log.Info("comment does not address the reviewer; nothing to do", "comment", ev.CommentID)
		return nil
	}
	ref, err := refForEvent(&f, ev)
	if err != nil {
		return err
	}
	gh, err := githubProvider(repo)
	if err != nil {
		return err
	}
	// The reviewer's own comments carry the marker; a mention inside one
	// is the reviewer quoting itself, not a request.
	if strings.Contains(ev.Body, gh.Bot) {
		log.Info("the comment is the reviewer's own; nothing to do", "comment", ev.CommentID)
		return nil
	}
	// Who is allowed to spend the repository's money by talking to the
	// reviewer. Checked before the reaction, not only before the model call: a
	// reaction tells a stranger the mention was seen, which is an invitation to
	// try again, and the whole point here is to be boring to poke at.
	if !cfg.Review.Respond.Allows(ev.Association) {
		log.Info("ignoring a mention from outside the allowed set; "+
			"answering it would spend this repository's model credit",
			"author", ev.Author, "association", strings.ToLower(ev.Association),
			"allowed", cfg.Review.Respond.String(), "comment", ev.CommentID)
		return nil
	}

	if cap := cfg.Review.Respond.MaxPerPullRequest; cap > 0 {
		answered, err := gh.CountBotComments(ctx, ref)
		switch {
		case err != nil:
			// Not fatal, and not a silent pass either: the cap exists to bound
			// spending, so a run that cannot count what it has already answered
			// says so rather than answering anyway without mentioning it.
			log.Warn("could not count prior answers, so the per-pull-request cap "+
				"is not being enforced on this comment", "error", err, "cap", cap)
		case answered >= cap:
			log.Info("this pull request has had its answers", "answered", answered, "cap", cap)
			return nil
		}
	}

	log.Info("answering a mention", "kind", kind, "pr", ref.Number, "comment", ev.CommentID, "author", ev.Author)
	if err := gh.React(ctx, ref, ev.CommentID, ev.Inline, "eyes"); err != nil {
		log.Warn("could not acknowledge the comment", "error", err)
	}

	switch kind {
	case converse.KindReview:
		// The whole change, not the increment: a person asking for a review
		// wants everything looked at again.
		reviewArgs := []string{"-repo", f.repo, "-owner", ref.Owner, "-repo-name", ref.Repo, "-pr", strconv.Itoa(ref.Number), "-full"}
		if f.configPath != "" {
			reviewArgs = append(reviewArgs, "-config", f.configPath)
		}
		err := runReview(ctx, reviewArgs)
		if err != nil && !errors.Is(err, errFindings) {
			_ = gh.React(ctx, ref, ev.CommentID, ev.Inline, "confused")
			return err
		}
		return gh.React(ctx, ref, ev.CommentID, ev.Inline, "+1")

	case converse.KindResolve:
		if !ev.Inline {
			return gh.CommentOnPullRequest(ctx, ref, fmt.Sprintf("@%s `%s resolve` works on an inline thread; on the conversation there is nothing to resolve.", ev.Author, mention))
		}
		resolved, err := gh.ResolveThreads(ctx, ref, []int64{ev.RootID}, fmt.Sprintf("Resolved on request by @%s.", ev.Author))
		if err != nil {
			return err
		}
		if len(resolved) == 0 {
			return gh.ReplyToReviewComment(ctx, ref, ev.RootID, "This thread is already resolved, or it is not one I can resolve.")
		}
		return gh.React(ctx, ref, ev.CommentID, ev.Inline, "+1")

	default:
		client, err := llm.Build(cfg.Models.ResolveModel(config.RoleReview))
		if err != nil {
			return err
		}
		pr, err := gh.PullRequest(ctx, ref)
		if err != nil {
			return err
		}
		diff, err := gh.Diff(ctx, ref)
		if err != nil {
			return err
		}
		c := converse.Context{Title: pr.Title, Body: pr.Body, Diff: string(diff)}
		if ev.Inline {
			c.Path = ev.Path
			if content, err := gh.FileContent(ctx, ref, ev.Path); err == nil {
				c.Excerpt = converse.Excerpt(string(content), ev.Line, 25)
			}
			if thread, err := gh.ThreadComments(ctx, ref, ev.RootID); err == nil {
				for _, t := range thread {
					if t.ID == ev.CommentID {
						continue
					}
					c.Thread = append(c.Thread, t.Author+": "+strings.TrimSpace(strings.ReplaceAll(t.Body, gh.Bot, "")))
				}
			}
		}
		answer, err := converse.Answer(ctx, client, c, text)
		if err != nil {
			_ = gh.React(ctx, ref, ev.CommentID, ev.Inline, "confused")
			return err
		}
		if ev.Inline {
			err = gh.ReplyToReviewComment(ctx, ref, ev.RootID, answer)
		} else {
			err = gh.CommentOnPullRequest(ctx, ref, fmt.Sprintf("@%s %s", ev.Author, answer))
		}
		if err != nil {
			return err
		}
		return gh.React(ctx, ref, ev.CommentID, ev.Inline, "+1")
	}
}

// refForEvent names the pull request a comment event is about: the number
// from the event, the repository from the flags or GITHUB_REPOSITORY. The
// review command's resolver cannot serve here, since on a comment event
// GITHUB_REF names a branch, not a pull request.
func refForEvent(f *reviewFlags, ev *converse.Event) (vcs.Ref, error) {
	owner, name := f.owner, f.repoName
	if owner == "" || name == "" {
		if o, n, ok := strings.Cut(os.Getenv("GITHUB_REPOSITORY"), "/"); ok {
			owner, name = o, n
		}
	}
	if owner == "" || name == "" {
		return vcs.Ref{}, errors.New("answering a comment needs -owner and -repo-name, or GITHUB_REPOSITORY")
	}
	if ev.Number <= 0 {
		return vcs.Ref{}, errors.New("the event names no pull request")
	}
	return vcs.Ref{Owner: owner, Repo: name, Number: ev.Number}, nil
}
