package practices

import (
	"fmt"
	"slices"

	"github.com/jdziat/open-nitpick/internal/bundle"
)

func (task DesignTask) contextRanges() (map[string][]bundle.SourceSpan, error) {
	ranges := map[string][]bundle.SourceSpan{}
	for _, span := range task.ContextSpans {
		target := Target{Kind: FileTarget, ID: span.Path}
		if !slices.Contains(task.Context, target) || slices.Contains(task.Sources, target) {
			return nil, fmt.Errorf("source spans must name supporting context: %s", span.Path)
		}
		prior := ranges[span.Path]
		if span.Start < 1 || span.End < span.Start || (len(prior) > 0 && span.Start <= prior[len(prior)-1].End) {
			return nil, fmt.Errorf("invalid or overlapping context spans: %s", span.Path)
		}
		ranges[span.Path] = append(prior, span.SourceSpan)
	}
	return ranges, nil
}
