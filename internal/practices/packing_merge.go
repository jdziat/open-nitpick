package practices

import (
	"slices"

	llms "github.com/nocturnium/llm-go-sdk/v6"

	"github.com/jdziat/open-nitpick/internal/bundle"
)

// A token limit alone could combine thousands of tiny, independent questions.
const maxDesignTasksPerBatch = 64

func combineDesignBatches(first, next bundle.Batch, maxFiles, maxTokens int) (bundle.Batch, bool) {
	if len(first.DesignTaskIDs())+len(next.DesignTaskIDs()) > maxDesignTasksPerBatch {
		return bundle.Batch{}, false
	}
	combined := first
	combined.AdditionalDesignTasks = append(slices.Clone(first.AdditionalDesignTasks), next.DesignTaskIDs()...)
	combined.Assessment = first.Assessment + "\n\nAssess every task in this request, including this additional task:\n" + next.Assessment
	combined.Entries = slices.Clone(first.Entries)
	for _, entry := range next.Entries {
		at := slices.IndexFunc(combined.Entries, func(prior bundle.Entry) bool { return prior.File.Path == entry.File.Path })
		if at < 0 {
			combined.Entries = append(combined.Entries, entry)
			continue
		}
		prior := combined.Entries[at]
		// Only entries from one frozen packing operation may share source.
		if prior.Content != entry.Content || !slices.Equal(prior.Instructions, entry.Instructions) {
			return bundle.Batch{}, false
		}
		if !entry.SourceOnly {
			prior.File, prior.SourceOnly = entry.File, false
		}
		if len(prior.SourceSpans) == 0 || len(entry.SourceSpans) == 0 {
			prior.SourceSpans = nil
		} else {
			spans := append(slices.Clone(prior.SourceSpans), entry.SourceSpans...)
			slices.SortFunc(spans, func(a, b bundle.SourceSpan) int { return a.Start - b.Start })
			var merged []bundle.SourceSpan
			for _, span := range spans {
				if len(merged) > 0 && span.Start <= merged[len(merged)-1].End+1 {
					merged[len(merged)-1].End = max(merged[len(merged)-1].End, span.End)
				} else {
					merged = append(merged, span)
				}
			}
			prior.SourceSpans = merged
		}
		prior.Tokens = llms.DefaultTokenEstimator().EstimateTokens(bundle.Render(prior))
		combined.Entries[at] = prior
	}
	if len(combined.Entries) > maxFiles {
		return bundle.Batch{}, false
	}
	combined.Tokens = llms.DefaultTokenEstimator().EstimateTokens(bundle.RenderBatch(combined))
	if combined.Tokens > maxTokens {
		return bundle.Batch{}, false
	}
	return combined, true
}
