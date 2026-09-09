package review

import (
	"context"
	"fmt"
	"strings"

	"github.com/jdziat/open-nitpick/internal/bundle"
	"github.com/jdziat/open-nitpick/internal/diff"
	"github.com/jdziat/open-nitpick/internal/knowledge"
)

// Retrieved knowledge, attached to a batch.
//
// Per batch rather than per file: the entries are about the change, and
// repeating the same five under every file in a batch would spend the token
// budget on duplication and teach the model that the section is boilerplate.

// knowledgeSection renders the entries a batch should be judged against.
//
// The heading says what these are and what they are not. A model handed
// reference material beside a diff will otherwise report the reference as a
// finding, which is the failure this feature has to avoid to be worth having:
// a reviewer that invents defects out of a style guide is worse than one that
// misses them.
func knowledgeSection(hits []knowledge.Hit) string {
	if len(hits) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n#### Known patterns this change may touch\n\n")
	b.WriteString("Reference material, not findings. None of this was written about this " +
		"change: it is here because the change resembles it. Judge whether each applies " +
		"before reporting anything from it, and report nothing on the strength of this " +
		"section alone.\n\n")
	for _, h := range hits {
		fmt.Fprintf(&b, "##### %s\n\n%s\n\nSource: %s, read %s.\n\n",
			bundle.PromptSafe(h.Entry.Title),
			bundle.PromptSafe(h.Entry.Body),
			bundle.PromptSafe(h.Entry.Source),
			h.Entry.Checked.Format("2006-01-02"))
	}
	return b.String()
}

// retrieveKnowledge returns the entries closest to a batch, or nothing.
//
// Every failure here is nothing rather than an error. Retrieval is an addition
// to a review that worked without it, so an embedding provider that is down
// should cost the run its extra context and not the review, and the log says
// which happened.
func (e *Engine) retrieveKnowledge(ctx context.Context, b bundle.Batch, style bool) []knowledge.Hit {
	if e.Knowledge == nil {
		return nil
	}
	// Not for the style pass. It re-reviews the same batches with a different
	// prompt, so retrieving again pays a second embedding call per batch to
	// hand a style reviewer a corpus about correctness defects.
	if style {
		return nil
	}
	hits, err := e.Knowledge.ForBatch(ctx, b)
	if err != nil {
		e.log().Warn("knowledge retrieval failed; reviewing without it", "error", err)
		return nil
	}
	return hits
}

// KnowledgeRetriever is what the engine holds, so internal/review does not
// depend on how retrieval is configured.
type KnowledgeRetriever struct {
	R *knowledge.Retriever
}

// ForBatch retrieves against a batch's diffs.
//
// The query is the diff text rather than the whole file: a file is mostly
// unchanged code, and embedding it retrieves entries about the parts nobody
// touched.
func (k *KnowledgeRetriever) ForBatch(ctx context.Context, b bundle.Batch) ([]knowledge.Hit, error) {
	if k == nil || k.R == nil {
		return nil, nil
	}
	var q strings.Builder
	paths := make([]string, 0, len(b.Entries))
	for _, e := range b.Entries {
		if e.File == nil {
			continue
		}
		paths = append(paths, e.File.Path)
		// The changed lines only. A hunk's context lines are code nobody
		// touched, and embedding them retrieves entries about the parts of the
		// file the change left alone.
		for i := range e.File.Hunks {
			for _, l := range e.File.Hunks[i].Lines {
				if l.Kind == diff.LineAdded || l.Kind == diff.LineRemoved {
					q.WriteString(l.Content)
					q.WriteString("\n")
				}
			}
		}
	}
	if q.Len() == 0 {
		return nil, nil
	}
	return k.R.Retrieve(ctx, q.String(), knowledge.LanguagesOf(paths))
}

// ids names the retrieved entries for the log, so a review that consulted the
// corpus can be checked against what it was shown.
func ids(hits []knowledge.Hit) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.Entry.ID)
	}
	return out
}
