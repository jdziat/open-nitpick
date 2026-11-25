package execution

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/nitpick-oss/nitpick/internal/vcs"
)

// ReadSnapshot fetches bounded source data at one immutable ref, without hooks.
func ReadSnapshot(ctx context.Context, provider vcs.Provider, ref vcs.Ref) (Snapshot, error) {
	if !commitID(ref.Head) {
		return Snapshot{}, errors.New("execution source needs a full revision")
	}
	lister, ok := provider.(vcs.SnapshotLister)
	if !ok {
		return Snapshot{}, errors.New("provider cannot enumerate an execution snapshot")
	}
	files := map[string][]byte{}
	queue := []string{""}
	entries, total := 0, 0
	for len(queue) > 0 {
		dir := queue[0]
		queue = queue[1:]
		names, err := lister.ListSnapshotDir(ctx, ref, dir)
		if err != nil {
			return Snapshot{}, fmt.Errorf("snapshot directory: %w", err)
		}
		entries += len(names)
		if entries > 1024 {
			return Snapshot{}, errors.New("execution snapshot exceeds 1024 entries")
		}
		for _, name := range names {
			base := strings.TrimSuffix(name, "/")
			if base == "" || strings.ContainsAny(base, "/\\") || base == "." || base == ".." {
				return Snapshot{}, errors.New("snapshot entry is not a basename")
			}
			if base == ".git" {
				continue
			}
			file := path.Join(dir, base)
			if strings.HasSuffix(name, "/") {
				queue = append(queue, file)
				continue
			}
			content, err := provider.FileContent(ctx, ref, file)
			if err != nil {
				return Snapshot{}, fmt.Errorf("snapshot file: %w", err)
			}
			total += len(content)
			if len(content) > 8<<20 || total > 48<<20 {
				return Snapshot{}, errors.New("execution snapshot exceeds source size limit")
			}
			files[file] = content
		}
	}
	return NewSnapshot(ref.Head, files)
}
