package vcs

import (
	"context"
	"errors"
	"strings"

	"github.com/google/go-github/v74/github"
)

// SnapshotLister enumerates a complete directory of regular files and trees.
// Unlike browsing, execution must refuse omitted symlinks, submodules and pages.
type SnapshotLister interface {
	ListSnapshotDir(context.Context, Ref, string) ([]string, error)
}

// ListSnapshotDir reads Git tree modes without the Contents API's omissions.
func (g *GitHub) ListSnapshotDir(ctx context.Context, ref Ref, dir string) ([]string, error) {
	treeID := ref.Head
	parts := []string{}
	if dir != "" && dir != "." {
		parts = strings.Split(strings.TrimSuffix(dir, "/"), "/")
	}
	for _, part := range parts {
		tree, err := g.executionTree(ctx, ref, treeID)
		if err != nil {
			return nil, err
		}
		found := false
		for _, entry := range tree.Entries {
			if entry.GetPath() == part && entry.GetMode() == "040000" && entry.GetType() == "tree" {
				treeID = entry.GetSHA()
				found = true
				break
			}
		}
		if !found {
			return nil, errors.New("execution directory is absent from the pinned Git tree")
		}
	}
	tree, err := g.executionTree(ctx, ref, treeID)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(tree.Entries))
	for _, entry := range tree.Entries {
		switch {
		case entry.GetMode() == "040000" && entry.GetType() == "tree":
			names = append(names, entry.GetPath()+"/")
		case (entry.GetMode() == "100644" || entry.GetMode() == "100755") && entry.GetType() == "blob":
			names = append(names, entry.GetPath())
		default:
			return nil, errors.New("execution does not support symlinks, submodules or special files")
		}
	}
	return names, nil
}

func (g *GitHub) executionTree(ctx context.Context, ref Ref, id string) (*github.Tree, error) {
	tree, _, err := g.client.Git.GetTree(ctx, ref.Owner, ref.Repo, id, false)
	if err != nil {
		return nil, err
	}
	if tree == nil || tree.GetTruncated() {
		return nil, errors.New("execution Git tree is unavailable or truncated")
	}
	return tree, nil
}

// ListSnapshotDir reads Git modes so unsupported sources cannot vanish silently.
func (l *Local) ListSnapshotDir(ctx context.Context, ref Ref, dir string) ([]string, error) {
	head := ref.Head
	if dir != "" && dir != "." {
		head += ":" + strings.TrimSuffix(dir, "/")
	}
	out, err := l.git(ctx, "ls-tree", "-z", head)
	if err != nil {
		return nil, err
	}
	names := []string{}
	for _, record := range strings.Split(out, "\x00") {
		if record == "" {
			continue
		}
		parts := strings.SplitN(record, "\t", 2)
		if len(parts) != 2 {
			return nil, errors.New("malformed snapshot tree")
		}
		meta := strings.Fields(parts[0])
		if len(meta) != 3 {
			return nil, errors.New("malformed snapshot tree mode")
		}
		switch meta[0] {
		case "040000":
			names = append(names, parts[1]+"/")
		case "100644", "100755":
			names = append(names, parts[1])
		default:
			return nil, errors.New("execution does not support symlinks, submodules or special files")
		}
	}
	return names, nil
}
