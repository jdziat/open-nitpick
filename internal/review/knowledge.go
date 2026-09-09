package review

import (
	"context"
	"fmt"
	"strings"

	"github.com/jdziat/open-nitpick/internal/bundle"
	"github.com/jdziat/open-nitpick/internal/config"
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
		fmt.Fprintf(&b, "##### %s\n\n%s\n\n",
			bundle.PromptSafe(h.Entry.Title),
			bundle.PromptSafe(h.Entry.Body))
		// Applicability where the entry states it. Rendered rather than
		// filtered on: nothing here knows the versions the change runs under,
		// and a filter fed a guess would silence an entry on the strength of
		// it. The model is already asked to judge whether an entry applies;
		// this is the sentence it judges with.
		if v := strings.TrimSpace(h.Entry.Versions); v != "" {
			fmt.Fprintf(&b, "Applies to: %s.\n\n", bundle.PromptSafe(v))
		}
		if len(h.Entry.Frameworks) > 0 {
			fmt.Fprintf(&b, "Frameworks: %s.\n\n", bundle.PromptSafe(strings.Join(h.Entry.Frameworks, ", ")))
		}
		fmt.Fprintf(&b, "Source: %s, read %s.\n\n",
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
	// The style pass used to retrieve nothing at all, because the corpus was
	// entirely about correctness and a second embedding call per batch bought
	// a style reviewer a set of defect rules. Now the classes decide: the
	// style pass gets style and maintainability entries and no others, and
	// declines only when the corpus has none, which is the corpus's answer
	// rather than a rule in the engine.
	hits, err := e.Knowledge.ForBatch(ctx, b, e.knowledgeClasses(style))
	if err != nil {
		// Still nothing rather than an error: the review worked before
		// retrieval existed and must survive its provider. The count is what
		// changed, so the report can say the arm did not run clean instead of
		// the log saying it once into a file nobody scores.
		e.log().Warn("knowledge retrieval failed; reviewing without it", "error", err)
		return nil
	}
	return hits
}

// KnowledgeRetriever is what the engine holds, so internal/review does not
// depend on how retrieval is configured.
type KnowledgeRetriever struct {
	R *knowledge.Retriever

	// status is what construction settled: the model, the entry count and,
	// when retrieval never got as far as answering, why.
	status KnowledgeStatus
	counts counters
}

// Status is what retrieval did, for the report.
//
// A nil retriever is off rather than a missing answer: the review ran, and
// nobody asked retrieval for anything.
func (k *KnowledgeRetriever) Status() KnowledgeStatus {
	if k == nil {
		return KnowledgeStatus{State: KnowledgeOff}
	}
	out := k.status
	out.Queries, out.Failures = k.counts.read()
	if out.Failures > 0 && out.State == KnowledgeActive {
		out.State = KnowledgeFailed
		out.Reason = "the embedder refused one or more batches"
	}
	return out
}

// ForBatch retrieves against a batch's diffs.
//
// The query is the diff text rather than the whole file: a file is mostly
// unchanged code, and embedding it retrieves entries about the parts nobody
// touched.
func (k *KnowledgeRetriever) ForBatch(ctx context.Context, b bundle.Batch, classes map[config.Class]bool) ([]knowledge.Hit, error) {
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
	k.counts.query()
	hits, err := k.R.Retrieve(ctx, q.String(), knowledge.LanguagesOf(paths), classes)
	if err != nil {
		k.counts.failure()
		return nil, err
	}
	return hits, nil
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

// knowledgeClasses names the entries a pass can act on.
//
// The defect pass takes everything the review taxonomy calls a defect, plus
// contract and tests. It does NOT take style: a style rule handed to the
// defect reviewer is the dilution config.GenerationLevel exists to prevent,
// arriving as reference material instead of as a prompt.
//
// Slop rides with the defect pass and only when review.slop is on, because
// that is when slop is a class the reviewer may publish. Retrieving entries
// for a class the filter will drop spends a slot in the prompt on a finding
// that cannot survive.
func (e *Engine) knowledgeClasses(style bool) map[config.Class]bool {
	if style {
		return map[config.Class]bool{
			config.ClassStyle:           true,
			config.ClassMaintainability: true,
		}
	}
	out := map[config.Class]bool{
		config.ClassCorrectness: true,
		config.ClassConcurrency: true,
		config.ClassSecurity:    true,
		config.ClassResource:    true,
		config.ClassDataLoss:    true,
		config.ClassContract:    true,
		config.ClassTests:       true,
		// Maintainability is in both. It is the one class the defect pass and
		// the style pass both publish, so an entry about it is useful to
		// either and belongs to neither alone.
		config.ClassMaintainability: true,
	}
	if e.Config != nil && e.Config.Review.Slop {
		out[config.ClassSlop] = true
	}
	return out
}
