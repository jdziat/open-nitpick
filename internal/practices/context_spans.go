package practices

import (
	"fmt"
	"slices"

	"github.com/jdziat/open-nitpick/internal/bundle"
)

func (task DesignTask) contextRanges() (map[string][]bundle.SourceSpan, error) {
	ranges := map[string][]bundle.SourceSpan{}
	groups := []struct {
		spans   []ContextSpan
		primary bool
	}{{task.SourceSpans, true}, {task.ContextSpans, false}}
	for _, group := range groups {
		for _, span := range group.spans {
			target := Target{Kind: FileTarget, ID: span.Path}
			if group.primary && !slices.Contains(task.Sources, target) || !group.primary && (!slices.Contains(task.Context, target) || slices.Contains(task.Sources, target)) {
				return nil, fmt.Errorf("source spans must name their declared source role: %s", span.Path)
			}
			prior := ranges[span.Path]
			if span.Start < 1 || span.End < span.Start || len(prior) > 0 && span.Start <= prior[len(prior)-1].End {
				return nil, fmt.Errorf("invalid or overlapping context spans: %s", span.Path)
			}
			ranges[span.Path] = append(prior, span.SourceSpan)
		}
	}
	for _, focus := range task.Focus {
		if spans := ranges[focus.Path]; len(spans) > 0 && !bundle.ContainsSourceRange(spans, focus.Start, focus.End) {
			return nil, fmt.Errorf("design focus lies outside supplied source spans: %s", focus.Path)
		}
	}
	return ranges, nil
}
