package vcs

import (
	"bytes"
	"context"
	"log/slog"
	"sort"
	"strings"
	"sync"
)

// checkoutTree is one commit's file list, read from the checkout in a single
// git call. Directory listings and file modes are answered from it, so a
// repository of ninety directories costs one process instead of ninety
// requests.
type checkoutTree struct {
	modes map[string]string
	dirs  map[string][]string
}

// checkoutIndex holds the trees read so far, keyed by commit.
//
// The lock guards the maps only. A tree is built outside it and then
// published, so one slow git call does not stall reads of other commits; two
// callers racing on an uncached commit read it twice and keep one.
type checkoutIndex struct {
	mu    sync.Mutex
	trees map[string]*checkoutTree
	heads map[string]string
}

// tree returns the commit's tree from the checkout, or nil when the checkout
// does not hold the commit. A nil answer sends the caller to the API.
//
// The commit is read from git objects and never the working tree: the review
// is of the commit named, and the checkout may have something else out.
func (g *GitHub) checkoutTree(ctx context.Context, sha string) *checkoutTree {
	if g.Checkout == "" || sha == "" || strings.HasPrefix(sha, "-") {
		return nil
	}
	g.index.mu.Lock()
	t, seen := g.index.trees[sha]
	g.index.mu.Unlock()
	if seen {
		return t
	}
	t = readCheckoutTree(ctx, g.Checkout, sha)
	// A failure caused by a cancelled run is not a fact about the checkout.
	if t == nil && ctx.Err() != nil {
		return nil
	}
	if t == nil {
		// Once per commit, because the miss is cached below. Without it a wrong
		// Checkout path looks like a slow review, not a misconfiguration.
		slog.Warn("checkout does not hold the reviewed commit; reading it through the API",
			"checkout", g.Checkout, "commit", short(sha))
	}
	g.index.mu.Lock()
	defer g.index.mu.Unlock()
	if kept, raced := g.index.trees[sha]; raced {
		return kept
	}
	if g.index.trees == nil {
		g.index.trees = map[string]*checkoutTree{}
	}
	g.index.trees[sha] = t
	return t
}

func readCheckoutTree(ctx context.Context, dir, sha string) *checkoutTree {
	local := &Local{Dir: dir}
	if _, err := local.gitRaw(ctx, "cat-file", "-e", sha+"^{commit}"); err != nil {
		return nil
	}
	out, err := local.gitRaw(ctx, "ls-tree", "-r", "-t", "-z", sha)
	if err != nil {
		return nil
	}
	t := &checkoutTree{modes: map[string]string{}, dirs: map[string][]string{}}
	for _, rec := range bytes.Split(out, []byte{0}) {
		meta, name, ok := strings.Cut(string(rec), "\t")
		if !ok || name == "" {
			continue
		}
		fields := strings.Fields(meta)
		if len(fields) < 2 {
			continue
		}
		parent := ""
		if i := strings.LastIndex(name, "/"); i >= 0 {
			parent = name[:i]
		}
		base := name[strings.LastIndex(name, "/")+1:]
		switch fields[1] {
		case "tree":
			t.dirs[parent] = append(t.dirs[parent], base+"/")
			if _, has := t.dirs[name]; !has {
				t.dirs[name] = nil
			}
		case "blob":
			t.modes[name] = fields[0]
			// The contents API types a symlink as a file, so it is listed here
			// too. Only reading one is refused, in regular below.
			t.dirs[parent] = append(t.dirs[parent], base)
		case "commit":
			// A submodule is typed as a file by the contents API as well, and has
			// no blob here to read.
			t.modes[name] = fields[0]
			t.dirs[parent] = append(t.dirs[parent], base)
		}
	}
	for _, names := range t.dirs {
		sort.Strings(names)
	}
	return t
}

// regular reports whether path is a regular file in the tree. A symlink or a
// submodule is not source, and the API path refuses them with its own error.
func (t *checkoutTree) regular(path string) bool {
	mode := t.modes[path]
	return mode == "100644" || mode == "100755"
}

// headSHA is the pull request's head commit, asked for once per pull request.
// It serves file and directory reads only. The approval check, Propose and its
// final re-read call PullRequest themselves, so a push during the run is still
// caught there.
//
// A read that names no revision used to fetch the pull request and its head
// commit again for every file, so a review reading four hundred files made
// well over a thousand requests. It also meant a push during the run could
// split the reads across two commits.
func (g *GitHub) headSHA(ctx context.Context, ref Ref) (string, error) {
	key := ref.String()
	g.index.mu.Lock()
	sha, ok := g.index.heads[key]
	g.index.mu.Unlock()
	if ok {
		return sha, nil
	}
	pr, err := g.PullRequest(ctx, ref)
	if err != nil {
		return "", err
	}
	if pr.HeadSHA == "" {
		return "", nil
	}
	g.index.mu.Lock()
	if g.index.heads == nil {
		g.index.heads = map[string]string{}
	}
	g.index.heads[key] = pr.HeadSHA
	g.index.mu.Unlock()
	return pr.HeadSHA, nil
}
