package bundle

import (
	"context"
	"fmt"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/diff"
)

func prefetchFullFiles(ctx context.Context, cfg *config.Config, files diff.Files, fetch ContentFetcher, kept func() int) (ContentFetcher, func()) {
	if fetch == nil || !cfg.Review.IncludeFullFiles {
		return fetch, func() {}
	}
	var paths []string
	for _, file := range files {
		if _, skip := skipReason(cfg, file); !skip {
			paths = append(paths, file.Path)
		}
	}
	workCtx, cancel := context.WithCancel(ctx)
	var stream *fetchStream
	queued, consumed := 0, 0
	get := func(_ context.Context, name string) ([]byte, error) {
		if consumed >= len(paths) || paths[consumed] != name {
			return nil, fmt.Errorf("unexpected full-source prefetch for %s", name)
		}
		consumed++
		if stream == nil || stream.cursor == len(stream.want) {
			if stream != nil {
				stream.stop()
			}
			// A wave cannot contain more files than the remaining review slots.
			// Generated files reopen slots only after the ordered pass skips them.
			n := min(cfg.Review.MaxFiles-kept(), len(paths)-queued)
			if n <= 0 {
				return nil, fmt.Errorf("full-source prefetch budget exhausted for %s", name)
			}
			stream = newFetchStream(workCtx, paths[queued:queued+n], min(4, n), fetch)
			queued += n
		}
		got, ok := stream.next()
		if !ok {
			return nil, fmt.Errorf("full-source prefetch stopped for %s", name)
		}
		return got.content, got.err
	}
	stop := func() {
		cancel()
		if stream != nil {
			stream.stop()
		}
	}
	return get, stop
}
