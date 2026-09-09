package review

import (
	"context"
	"log/slog"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/knowledge"
	"github.com/jdziat/open-nitpick/internal/llm"
)

// Retrieval defaults, chosen rather than measured.
//
// Twenty candidates is enough that a reranker has something to choose between
// and small enough that the cosine scan stays trivial. Five kept is the number
// the token budget can carry beside a batch without displacing the diff.
const (
	knowledgeCandidates = 20
	knowledgeKeep       = 5
)

// BuildKnowledge constructs the retriever, or returns nil.
//
// Nil is the ordinary answer: retrieval is off unless review.knowledge is on
// and models.embed names a provider that can embed. Every reason to return nil
// is logged, because a feature that is silently doing nothing is worse than
// one that is off.
func BuildKnowledge(ctx context.Context, cfg *config.Config, index []byte, log *slog.Logger) (*KnowledgeRetriever, error) {
	if cfg == nil || !cfg.Review.Knowledge {
		return nil, nil
	}
	spec, ok := cfg.Models.ResolveEmbed()
	if !ok {
		log.Warn("review.knowledge is on and no models.embed is configured; reviewing without it")
		return nil, nil
	}

	entries, err := knowledge.Corpus()
	if err != nil {
		return nil, err
	}
	ix, err := knowledge.LoadIndex(index, entries)
	if err != nil {
		return nil, err
	}

	embedder, err := llm.BuildEmbedder(ctx, spec)
	if err != nil {
		return nil, err
	}
	if err := ix.CheckModel(embedder.Model()); err != nil {
		return nil, err
	}

	log.Info("knowledge retrieval on", "entries", len(entries), "model", embedder.Model())
	return &KnowledgeRetriever{R: &knowledge.Retriever{
		Entries:    entries,
		Index:      ix,
		Embedder:   embedder,
		Candidates: knowledgeCandidates,
		Keep:       knowledgeKeep,
	}}, nil
}
