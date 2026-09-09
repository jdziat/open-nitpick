package review

import (
	"context"
	"log/slog"
	"strings"

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

// BuildKnowledge constructs the retriever and says what it did.
//
// The status is the point of the signature. Retrieval used to report itself by
// returning nil and logging why, which reads fine in a terminal and is
// unusable in a measurement: an arm whose embedder was never built and an arm
// that retrieved for every batch both produced a review, and only one of them
// is the treatment.
//
// The error is still returned, and callers that asked for retrieval should
// still treat construction as fatal. A status of failed exists for the run
// that got further than construction.
// repoRoot is where the versions an entry's `applies:` clauses are judged
// against are read from. Empty reads none, and none keeps every entry.
func BuildKnowledge(ctx context.Context, cfg *config.Config, repoRoot string, log *slog.Logger) (*KnowledgeRetriever, KnowledgeStatus, error) {
	if cfg == nil || !cfg.Review.Knowledge {
		return nil, KnowledgeStatus{State: KnowledgeOff}, nil
	}

	skip := func(reason string) (*KnowledgeRetriever, KnowledgeStatus, error) {
		log.Warn("review.knowledge is on and retrieval is not running; reviewing without it", "reason", reason)
		return nil, KnowledgeStatus{State: KnowledgeSkipped, Reason: reason}, nil
	}
	fail := func(reason string, err error) (*KnowledgeRetriever, KnowledgeStatus, error) {
		return nil, KnowledgeStatus{State: KnowledgeFailed, Reason: reason}, err
	}

	spec, ok := cfg.Models.ResolveEmbed()
	if !ok {
		return skip("no models.embed is configured")
	}

	entries, err := knowledge.Corpus()
	if err != nil {
		return fail("the corpus could not be read", err)
	}
	if len(entries) == 0 {
		return skip("the corpus is empty")
	}
	// The embedder first, because it names the model the index has to match.
	// Selecting an index before knowing what will query it is how a run ends
	// up comparing one model's vectors against another's.
	embedder, err := llm.BuildEmbedder(ctx, spec)
	if err != nil {
		return fail("the embedder could not be built", err)
	}

	raw, err := resolveIndex(cfg, embedder.Model())
	if err != nil {
		return fail("no index is available for this embedding model", err)
	}
	ix, err := knowledge.LoadIndex(raw, entries)
	if err != nil {
		return fail("the index could not be loaded", err)
	}
	if err := ix.CheckModel(embedder.Model()); err != nil {
		return fail("the index was built by a different embedding model", err)
	}

	log.Info("knowledge retrieval on",
		"entries", len(entries), "model", embedder.Model(), "root", repoRoot)
	return &KnowledgeRetriever{
		R: &knowledge.Retriever{
			Entries:    entries,
			Index:      ix,
			Embedder:   embedder,
			Model:      embedder.Model(),
			Candidates: knowledgeCandidates,
			Keep:       knowledgeKeep,
			MinScore:   cfg.Review.KnowledgeMinScore,
		},
		RepoRoot: repoRoot,
		status:   KnowledgeStatus{State: KnowledgeActive, Model: embedder.Model(), Entries: len(entries)},
	}, KnowledgeStatus{State: KnowledgeActive, Model: embedder.Model(), Entries: len(entries)}, nil
}

// resolveIndex picks the vectors this run queries: the operator's own file
// when review.knowledge_index names one, otherwise the shipped index built by
// the configured embedding model.
//
// An explicit path wins and is not second-guessed. An operator who named a
// file wants that file, and silently falling back to a shipped index when it
// cannot be read would answer a review from vectors they did not choose.
func resolveIndex(cfg *config.Config, model string) ([]byte, error) {
	if p := strings.TrimSpace(cfg.Review.KnowledgeIndex); p != "" {
		return knowledge.LoadIndexFile(p)
	}
	return knowledge.SelectIndex(model)
}
