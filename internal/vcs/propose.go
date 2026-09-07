package vcs

import (
	"context"
	"errors"
	"fmt"
	"net/http"
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
	//
	// An unreported repository is refused rather than skipped. The forge sends
	// a null head repository for a fork that has since been deleted, and its
	// head commit is still reachable through the base repository's network, so
	// treating "unknown" as "same" is the fork case wearing a blank name.
	if pr.HeadRepo == "" || pr.BaseRepo == "" {
		return nil, fmt.Errorf("github: %w: %s does not report which repository its head is in",
			ErrForbidden, ref)
	}
	if pr.HeadRepo != pr.BaseRepo {
		return nil, fmt.Errorf("github: %w: %s is a pull request from %s", ErrForbidden, ref, pr.HeadRepo)
	}
	if strings.EqualFold(p.Branch, pr.HeadRef) || strings.EqualFold(p.Branch, pr.BaseRef) {
		return nil, fmt.Errorf("github: a proposal may not write to %s, the branch under review", p.Branch)
	}
	if p.Base != pr.HeadSHA {
		return nil, fmt.Errorf("github: %w: read at %s, now at %s", ErrHeadMoved, short(p.Base), short(pr.HeadSHA))
	}

	base, _, err := g.client.Git.GetCommit(ctx, ref.Owner, ref.Repo, p.Base)
	if err != nil {
		return nil, fmt.Errorf("github: read commit %s: %w", short(p.Base), err)
	}

	modes, err := g.treeModes(ctx, ref, base.GetTree().GetSHA())
	if err != nil {
		return nil, err
	}

	entries, err := treeEntries(p, modes)
	if err != nil {
		return nil, err
	}

	tree, _, err := g.client.Git.CreateTree(ctx, ref.Owner, ref.Repo, base.GetTree().GetSHA(), entries)
	if err != nil {
		return nil, writeErr("create tree", err)
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
		return nil, writeErr("create commit", err)
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
		return nil, writeErr("create "+fullRef, err)
	}

	// The last chance to notice a push that landed while this was being built.
	//
	// A re-read that FAILS is treated as moved. The alternative is opening the
	// pull request with no check at all on a transient error, which is the
	// failure this guard exists to prevent, arriving through a rate limit
	// rather than through a push.
	moved, err := g.PullRequest(ctx, ref)
	if err != nil || moved.HeadSHA != p.Base {
		now := "unknown"
		if err == nil {
			now = short(moved.HeadSHA)
		}

		// A branch that cannot be removed is named in the error. Silence here
		// would leave a commit that reverts someone's push sitting on a ref,
		// and the next identical ask would report it as the earlier proposal.
		if _, delErr := g.client.Git.DeleteRef(ctx, ref.Owner, ref.Repo, fullRef); delErr != nil {
			return nil, fmt.Errorf("github: %w: read at %s, now at %s, and %s could not be removed: %w",
				ErrHeadMoved, short(p.Base), now, p.Branch, delErr)
		}
		return nil, fmt.Errorf("github: %w: read at %s, now at %s", ErrHeadMoved, short(p.Base), now)
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
		// The branch goes too. Left behind it is a ref with nothing to link
		// to, and the next identical ask reports it as an earlier proposal
		// that does not exist.
		if _, delErr := g.client.Git.DeleteRef(ctx, ref.Owner, ref.Repo, fullRef); delErr != nil {
			return nil, fmt.Errorf("github: open the pull request: %w, and %s could not be removed: %w",
				err, p.Branch, delErr)
		}
		return nil, writeErr("open the pull request", err)
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
func treeEntries(p Proposal, modes map[string]string) ([]*github.TreeEntry, error) {
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

		// The mode comes from the tree rather than a constant. Writing every
		// edit as 100644 silently clears the executable bit on a script, and
		// the pull request looks clean until the file is run.
		mode, ok := modes[clean]
		if !ok {
			return nil, fmt.Errorf("github: %s is not in the revision this was read at", clean)
		}
		if mode != "100644" && mode != "100755" {
			// A symlink's content is its target and a submodule's is a
			// revision. Neither is a file a code-edit model should rewrite.
			return nil, fmt.Errorf("github: %s is mode %s, which this does not write", clean, mode)
		}

		out = append(out, &github.TreeEntry{
			Path:    github.Ptr(clean),
			Mode:    github.Ptr(mode),
			Type:    github.Ptr("blob"),
			Content: github.Ptr(string(e.Content)),
		})
	}
	return out, nil
}

// writeErr labels a failed write, naming a refused credential when that is
// what happened. An App installation's permissions are granted separately from
// the workflow that asks for them, and the forge reports the disagreement as a
// bare 403.
func writeErr(op string, err error) error {
	var resp *github.ErrorResponse
	if errors.As(err, &resp) && resp.Response != nil && resp.Response.StatusCode == http.StatusForbidden {
		return fmt.Errorf("github: %s: %w", op, ErrNoWriteAccess)
	}
	return fmt.Errorf("github: %s: %w", op, err)
}

// treeModes maps every path in a revision's tree to its file mode.
//
// One recursive read rather than one call per edit. A truncated tree is
// refused rather than half-used: a missing path would otherwise read as "this
// file is new", which is the one case a proposal must not accept.

func (g *GitHub) treeModes(ctx context.Context, ref Ref, treeSHA string) (map[string]string, error) {
	tree, _, err := g.client.Git.GetTree(ctx, ref.Owner, ref.Repo, treeSHA, true)
	if err != nil {
		return nil, fmt.Errorf("github: read tree %s: %w", short(treeSHA), err)
	}
	if tree.GetTruncated() {
		return nil, fmt.Errorf("github: the tree at %s is too large to read whole", short(treeSHA))
	}

	out := make(map[string]string, len(tree.Entries))
	for _, e := range tree.Entries {
		out[e.GetPath()] = e.GetMode()
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
