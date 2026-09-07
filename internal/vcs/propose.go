package vcs

import (
	"context"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/google/go-github/v74/github"
)

// Publishing file edits as a branch and a pull request.
//
// This is the only code here that writes to a repository rather than to a
// conversation about one. Every other write this tool makes is additive: a
// comment, a reply, a reaction, a resolved thread. A proposal can destroy
// work, and the guards below are sized for that rather than for tidiness.
//
// It carries whole files because the forge's data API commits blobs and not
// patches. That is the source of most of the risk: content read at one
// revision, written over another, silently reverts anything that landed in
// between.

// branchPattern is the only shape a proposal may name.
//
// Nothing model-authored or comment-authored reaches a ref name. The pattern
// is narrow rather than merely safe, so a name that does not come from this
// package's own derivation cannot pass it.
var branchPattern = regexp.MustCompile(`^nitpick/fix/[a-z0-9][a-z0-9._-]*$`)

// deniedPaths are refused whatever the pull request changed.
//
// A pull request that legitimately edits a workflow or the reviewer's own
// configuration is the one a bot should least be writing to: both decide what
// runs and under what policy, and internal/config/basepolicy.go exists because
// a change may not supply the policy it is reviewed under.
func denied(p string) bool {
	switch {
	case strings.HasPrefix(p, ".github/"), p == ".github":
		return true
	case p == ".nitpick.yaml":
		return true
	}
	return strings.HasPrefix(p, ".git/") || p == ".git"
}

// cleanPath normalizes a repository-relative path, or reports why it is not one.
//
// The paths arriving here were written by a model reading text a contributor
// wrote, so this is a boundary rather than a convenience.
func cleanPath(p string) (string, error) {
	if !utf8.ValidString(p) {
		return "", errors.New("not valid UTF-8")
	}
	if strings.ContainsRune(p, '\\') {
		return "", errors.New("contains a backslash")
	}
	if strings.HasPrefix(p, "/") {
		return "", errors.New("is absolute")
	}

	out := path.Clean(p)
	switch {
	case out == "." || out == "":
		return "", errors.New("is empty")
	case strings.HasPrefix(out, "../"), out == "..":
		return "", errors.New("leaves the repository")
	}
	for _, part := range strings.Split(out, "/") {
		if part == ".." {
			return "", errors.New("leaves the repository")
		}
	}
	return out, nil
}

// ProposeChange publishes file edits as a new branch and a pull request.
//
// It refuses more than it accepts, and every refusal is here rather than in
// the caller. A guard that lives only in cmd is a guard a second caller does
// not get.
func (g *GitHub) ProposeChange(ctx context.Context, ref Ref, p Proposal) (*ProposedChange, error) {
	if err := validateRef(ref); err != nil {
		return nil, err
	}
	if !branchPattern.MatchString(p.Branch) {
		return nil, fmt.Errorf("github: %q is not a branch this may create", p.Branch)
	}
	if strings.TrimSpace(p.Base) == "" {
		return nil, errors.New("github: a proposal needs the revision its edits were read at")
	}
	if len(p.Edits) == 0 {
		return nil, errors.New("github: a proposal with no edits")
	}

	pr, err := g.PullRequest(ctx, ref)
	if err != nil {
		return nil, err
	}

	// A pull request from a fork is refused HERE and not only by the workflow.
	// This token cannot push to the fork, so the write would instead land a
	// branch in the base repository pointing at a commit parented on the
	// fork's head: every call succeeds and fork-authored code ends up on an
	// upstream branch under this tool's name.
	if pr.HeadRepo != "" && pr.BaseRepo != "" && pr.HeadRepo != pr.BaseRepo {
		return nil, fmt.Errorf("github: %w: %s is a pull request from %s", ErrForbidden, ref, pr.HeadRepo)
	}
	if strings.EqualFold(p.Branch, pr.HeadRef) || strings.EqualFold(p.Branch, pr.BaseRef) {
		return nil, fmt.Errorf("github: a proposal may not write to %s, the branch under review", p.Branch)
	}
	if p.Base != pr.HeadSHA {
		return nil, fmt.Errorf("github: %w: read at %s, now at %s", ErrHeadMoved, short(p.Base), short(pr.HeadSHA))
	}

	entries, err := treeEntries(p)
	if err != nil {
		return nil, err
	}

	base, _, err := g.client.Git.GetCommit(ctx, ref.Owner, ref.Repo, p.Base)
	if err != nil {
		return nil, fmt.Errorf("github: read commit %s: %w", short(p.Base), err)
	}

	tree, _, err := g.client.Git.CreateTree(ctx, ref.Owner, ref.Repo, base.GetTree().GetSHA(), entries)
	if err != nil {
		return nil, fmt.Errorf("github: create tree: %w", err)
	}

	// Nothing changed. Caught here rather than at the pull request, which
	// would otherwise refuse an empty comparison after a branch had already
	// been created and left behind.
	if tree.GetSHA() == base.GetTree().GetSHA() {
		return nil, nil
	}

	commit, _, err := g.client.Git.CreateCommit(ctx, ref.Owner, ref.Repo, &github.Commit{
		Message: github.Ptr(p.Message),
		Tree:    &github.Tree{SHA: tree.SHA},
		Parents: []*github.Commit{{SHA: github.Ptr(p.Base)}},
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("github: create commit: %w", err)
	}

	// CreateRef and never UpdateRef. A ref that exists is the same request
	// asked twice, since the name is derived from the pull request and the
	// finding; the caller links to what is already there. Using UpdateRef here
	// would make every other guard advisory.
	fullRef := "refs/heads/" + p.Branch
	if _, _, err := g.client.Git.CreateRef(ctx, ref.Owner, ref.Repo, &github.Reference{
		Ref:    github.Ptr(fullRef),
		Object: &github.GitObject{SHA: commit.SHA},
	}); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "already exists") {
			return nil, fmt.Errorf("github: %w: %s", ErrRefExists, p.Branch)
		}
		return nil, fmt.Errorf("github: create %s: %w", fullRef, err)
	}

	// The last chance to notice a push that landed while this was being
	// built. The branch is removed rather than left dangling, and the caller
	// is told why instead of receiving a pull request that reverts someone.
	if moved, err := g.PullRequest(ctx, ref); err == nil && moved.HeadSHA != p.Base {
		_, _ = g.client.Git.DeleteRef(ctx, ref.Owner, ref.Repo, fullRef)
		return nil, fmt.Errorf("github: %w: read at %s, now at %s",
			ErrHeadMoved, short(p.Base), short(moved.HeadSHA))
	}

	created, _, err := g.client.PullRequests.Create(ctx, ref.Owner, ref.Repo, &github.NewPullRequest{
		Title: github.Ptr(p.Title),
		Body:  github.Ptr(p.Body),
		Head:  github.Ptr(p.Branch),
		Base:  github.Ptr(p.Into),
		// A draft, always. Unverified model output must not be one click from
		// merged, and a draft requests no review from anyone.
		Draft:               github.Ptr(true),
		MaintainerCanModify: github.Ptr(true),
	})
	if err != nil {
		return nil, fmt.Errorf("github: open the pull request: %w", err)
	}

	return &ProposedChange{
		Branch:    p.Branch,
		CommitSHA: commit.GetSHA(),
		Number:    created.GetNumber(),
		URL:       created.GetHTMLURL(),
	}, nil
}

// treeEntries turns a proposal's edits into tree entries, refusing every path
// the pull request under review does not already change.
//
// An entry carrying neither content nor a blob SHA is how the forge's API
// spells DELETE, so content is asserted rather than assumed.
func treeEntries(p Proposal) ([]*github.TreeEntry, error) {
	allow := make(map[string]bool, len(p.AllowPaths))
	for _, a := range p.AllowPaths {
		if clean, err := cleanPath(a); err == nil {
			allow[clean] = true
		}
	}

	out := make([]*github.TreeEntry, 0, len(p.Edits))
	for _, e := range p.Edits {
		clean, err := cleanPath(e.Path)
		if err != nil {
			return nil, fmt.Errorf("github: path %q %w", e.Path, err)
		}
		if denied(clean) {
			return nil, fmt.Errorf("github: %w: %s decides what runs", ErrForbidden, clean)
		}
		if !allow[clean] {
			return nil, fmt.Errorf("github: %w: %s", ErrOutsideChange, clean)
		}
		if e.Content == nil {
			return nil, fmt.Errorf("github: %s carries no content, which would delete it", clean)
		}

		out = append(out, &github.TreeEntry{
			Path:    github.Ptr(clean),
			Mode:    github.Ptr("100644"),
			Type:    github.Ptr("blob"),
			Content: github.Ptr(string(e.Content)),
		})
	}
	return out, nil
}

// Permission reports what login may do to the repository.
func (g *GitHub) Permission(ctx context.Context, ref Ref, login string) (string, error) {
	if err := validateRef(ref); err != nil {
		return "", err
	}
	level, _, err := g.client.Repositories.GetPermissionLevel(ctx, ref.Owner, ref.Repo, login)
	if err != nil {
		return "", fmt.Errorf("github: read %s's permission on %s/%s: %w", login, ref.Owner, ref.Repo, err)
	}
	return level.GetPermission(), nil
}

// short renders a revision for a message.
func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
