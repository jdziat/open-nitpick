package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/converse"
	"github.com/jdziat/open-nitpick/internal/fix"
	"github.com/jdziat/open-nitpick/internal/llm"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// Applying a finding, on `@open-nitpick fix`.
//
// This is the one command that writes to the repository, so it refuses more
// than the others and says why rather than going quiet. A question answered to
// nobody costs a model call; a write made for the wrong person costs
// something else.

// runFix applies one finding, or every published finding under `fix all`.
func runFix(ctx context.Context, gh *vcs.GitHub, cfg *config.Config, ref vcs.Ref, ev *converse.Event, all bool, log *slog.Logger) error {
	// The association gate the other commands use has already run. This is
	// the narrower one: it refuses "none" whatever the config says, because
	// opening a write to everyone is not a decision an operator makes on
	// purpose.
	if !cfg.Review.Respond.Fix.Allows(ev.Association) {
		return reply(ctx, gh, ref, ev, fmt.Sprintf(
			"@%s I only apply findings for people listed in `review.respond.fix.from`.", ev.Author))
	}

	// And the one that is actually about writing. An association says how the
	// forge describes someone; a permission says what they may do, and
	// COLLABORATOR covers read-level access.
	switch level, err := gh.Permission(ctx, ref, ev.Author); {
	case err != nil:
		log.Warn("could not read the asker's permission; refusing the fix", "author", ev.Author, "error", err)
		return reply(ctx, gh, ref, ev, fmt.Sprintf(
			"@%s I could not confirm you have write access, so I have not changed anything.", ev.Author))
	case level != "admin" && level != "write":
		return reply(ctx, gh, ref, ev, fmt.Sprintf(
			"@%s applying a finding writes to this repository, and that needs write access.", ev.Author))
	}

	spec, ok := cfg.Models.ResolveFix()
	if !ok {
		return reply(ctx, gh, ref, ev, fmt.Sprintf(
			"@%s no `models.fix` is configured. The model that reviews well is not the one that edits well, "+
				"so I will not use the reviewer for this.", ev.Author))
	}

	pr, err := gh.PullRequest(ctx, ref)
	if err != nil {
		return err
	}

	// Before the model call, not only at the write. ProposeChange refuses a
	// fork too, but by then the credit is spent reading code from outside
	// this repository.
	if pr.HeadRepo == "" || pr.BaseRepo == "" || pr.HeadRepo != pr.BaseRepo {
		return reply(ctx, gh, ref, ev, fmt.Sprintf(
			"@%s I do not apply findings on a pull request from a fork.", ev.Author))
	}

	findings, err := gatherFindings(ctx, gh, ref, ev, all)
	if err != nil {
		return err
	}
	if len(findings) == 0 {
		return reply(ctx, gh, ref, ev, fmt.Sprintf(
			"@%s I could not find a published finding to apply here.", ev.Author))
	}

	// Every file is read at the revision the proposal will be parented on, so
	// what the model saw and what the commit is written over are the same
	// tree. See vcs.ErrHeadMoved.
	files := map[string]string{}
	for _, f := range findings {
		if _, done := files[f.Path]; done {
			continue
		}
		content, err := gh.FileContent(ctx, ref.At(pr.HeadSHA), f.Path)
		if err != nil {
			log.Warn("could not read a file the finding names", "path", f.Path, "error", err)
			continue
		}
		files[f.Path] = string(content)
	}
	if len(files) == 0 {
		return reply(ctx, gh, ref, ev, fmt.Sprintf(
			"@%s I could not read the files those findings name.", ev.Author))
	}

	client, err := llm.BuildContext(ctx, spec)
	if err != nil {
		return err
	}
	// A client built outside Roles gets no logger otherwise, and a fix that
	// spent two minutes being rate limited would say only that it was slow.
	client.SetLogger(log)

	req := fix.Request{Findings: findings, Files: files}
	res, err := fix.Apply(ctx, client, req)
	if err != nil {
		return err
	}

	// Nothing changed is a result, and it is reported as one. Silence here
	// would read the same as a fix that was applied.
	if len(res.Edits) == 0 {
		return reply(ctx, gh, ref, ev, fmt.Sprintf(
			"@%s I did not change anything. %s", ev.Author, skippedNote(res)))
	}

	branch := fix.Branch(ref.Number, fingerprintFor(findings, all))
	body := fix.Body(req, res, pr.HeadSHA, ev.Author, ref.Number)

	proposal, ok := fix.Proposal(req, res, pr, branch, body)
	if !ok {
		return reply(ctx, gh, ref, ev, fmt.Sprintf("@%s I did not change anything.", ev.Author))
	}

	change, err := gh.ProposeChange(ctx, ref, proposal)
	switch {
	case errors.Is(err, vcs.ErrRefExists):
		return reply(ctx, gh, ref, ev, fmt.Sprintf(
			"@%s I have already opened `%s` for this. Nothing new was written.", ev.Author, branch))
	case errors.Is(err, vcs.ErrHeadMoved):
		return reply(ctx, gh, ref, ev, fmt.Sprintf(
			"@%s this pull request moved while I was writing, so I stopped rather than revert the push. Ask again.", ev.Author))
	case errors.Is(err, vcs.ErrNoWriteAccess):
		// Named separately because its remedy is a setting rather than a
		// retry, and the usual cause is worth saying: a workflow asking for
		// contents: write does not give the App installation behind its token
		// that permission. Said as the likely cause rather than the certain
		// one, since SSO enforcement refuses a write the same way.
		if rerr := reply(ctx, gh, ref, ev, fmt.Sprintf(
			"@%s the forge refused the write, so nothing was created. The usual cause is the App installation not holding `contents: write` even though the workflow asks for it. The run log has what the forge said.",
			ev.Author)); rerr != nil {
			return errors.Join(err, rerr)
		}
		return err
	case err != nil:
		// Every other failure says so in the thread as well. Without this the
		// asker sees nothing: the run goes red on a page they were not
		// watching, and the conversation they asked in stays silent, which
		// reads the same as a fix still being written.
		//
		// The text is fixed rather than the error, which carries forge
		// response bodies and the internal labels of whichever call failed.
		// The run log is where those belong. The error is returned afterwards
		// regardless, so the check still fails.
		if rerr := reply(ctx, gh, ref, ev, fmt.Sprintf(
			"@%s I could not write the change. The failure is in the workflow run log.", ev.Author)); rerr != nil {
			return errors.Join(err, rerr)
		}
		return err
	case change == nil:
		return reply(ctx, gh, ref, ev, fmt.Sprintf("@%s I did not change anything.", ev.Author))
	}

	log.Info("proposed a fix", "branch", change.Branch, "pull_request", change.Number)
	return reply(ctx, gh, ref, ev, fmt.Sprintf(
		"@%s I opened %s as a draft. It was never compiled or tested: its own checks are the verification.",
		ev.Author, change.URL))
}

// gatherFindings reads the findings to apply off the pull request.
func gatherFindings(ctx context.Context, gh *vcs.GitHub, ref vcs.Ref, ev *converse.Event, all bool) ([]fix.Finding, error) {
	if all {
		prior, err := gh.PriorReview(ctx, ref)
		if err != nil {
			return nil, err
		}
		out := make([]fix.Finding, 0, len(prior.Comments))
		for _, c := range prior.Comments {
			if c.Path == "" || c.Line == 0 {
				// The forge no longer places it on the diff, so there is no
				// anchor to apply it at.
				continue
			}
			out = append(out, fix.Finding{Path: c.Path, Line: c.Line, Body: c.Body, Fingerprint: c.Fingerprint})
		}
		return out, nil
	}

	if !ev.Inline {
		return nil, nil
	}
	thread, err := gh.ThreadComments(ctx, ref, ev.RootID)
	if err != nil {
		return nil, err
	}
	for _, c := range thread {
		if !strings.Contains(c.Body, gh.Bot) {
			continue
		}
		// The fingerprint comes from the comment, not from nothing. Without
		// it the branch is named after the pull request alone, so a second
		// fix on the same pull request collides with the first and is
		// reported as one already open.
		return []fix.Finding{{
			Path: ev.Path, Line: ev.Line, Body: c.Body,
			Fingerprint: vcs.FingerprintOf(c.Body),
		}}, nil
	}
	return nil, nil
}

// fingerprintFor names the branch: one finding by its own fingerprint, a whole
// pass by the pull request alone.
func fingerprintFor(findings []fix.Finding, all bool) string {
	if all || len(findings) != 1 {
		return ""
	}
	return findings[0].Fingerprint
}

// skippedNote says why nothing changed, when the model said.
func skippedNote(res fix.Result) string {
	if len(res.Skipped) == 0 {
		return "Nothing in those findings gave me a change to make."
	}
	var b strings.Builder
	b.WriteString("Reasons:")
	for path, why := range res.Skipped {
		fmt.Fprintf(&b, "\n- `%s`: %s", path, why)
	}
	return b.String()
}

// reply answers in the thread when there is one, and on the pull request
// otherwise. A refusal is said out loud: the person asked for a change and is
// owed the reason it did not happen.
func reply(ctx context.Context, gh *vcs.GitHub, ref vcs.Ref, ev *converse.Event, body string) error {
	if ev.Inline {
		return gh.ReplyToReviewComment(ctx, ref, ev.RootID, body)
	}
	return gh.CommentOnPullRequest(ctx, ref, body)
}
