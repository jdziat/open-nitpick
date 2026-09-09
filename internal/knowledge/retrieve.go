package knowledge

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/jdziat/open-nitpick/internal/config"
)

// Retriever answers a change with the entries it should be judged against.
type Retriever struct {
	Entries  []Entry
	Index    *Index
	Embedder Embedder

	// Model names what Embedder is, so retrieval can refuse a query the index
	// cannot answer.
	//
	// BuildKnowledge checks this once at construction, and that was the whole
	// guard until four bundles shipped. Index.Nearest compares widths, and a
	// width is not an identity: text-embedding-3-small and gemini-embedding-001
	// are both 1536, so one model's query against the other's vectors passes
	// every shape check and returns the nearest neighbours of a point in a
	// space it does not belong to. Real numbers, ordered, meaningless. The
	// check moves here so a Retriever assembled by any other caller cannot
	// skip it.
	Model string

	// Candidates is how many the vector search returns, and Keep how many
	// survive to the prompt. Keep is what costs tokens; Candidates is what
	// gives the reranker something to choose between.
	Candidates int
	Keep       int

	// Rerank picks from the candidates. Nil keeps the top Keep by cosine,
	// which is the control the measurement compares against.
	Rerank func(ctx context.Context, query string, hits []Hit, keep int) ([]Hit, error)

	// MinScore drops hits below a cosine, so a change resembling nothing in
	// the corpus retrieves nothing rather than its five least distant
	// entries.
	//
	// Zero is off, and off is what shipped and what was measured. Abstention
	// is a candidate improvement, not an established one: the failure it
	// prevents (five irrelevant entries in front of a reviewer) and the one it
	// creates (the entry that would have caught the defect, cut for scoring
	// 0.4) are both real, and which dominates is a measurement.
	MinScore float64

	// Versions is what the repository under review declares, keyed as an
	// entry's `applies:` clauses name it. Nil means nothing is known, and
	// nothing known keeps every entry.
	Versions map[string]string
}

// Retrieve returns the entries closest to a change.
func (r *Retriever) Retrieve(ctx context.Context, query string, langs map[string]bool, classes map[config.Class]bool) ([]Hit, error) {
	if r == nil || r.Index == nil || r.Embedder == nil {
		return nil, nil
	}
	// Identity before similarity. Empty means a caller that has not said what
	// it embeds with, and that is refused rather than assumed: the failure it
	// would cause is silent and looks like a bad corpus.
	if err := r.Index.CheckModel(r.Model); err != nil {
		return nil, err
	}
	// The language cut first, before any vector is compared. Similarity alone
	// puts Python's mutable default beside Go's slice aliasing, because both
	// are "a value shared when it looked copied", and an entry from another
	// language is a reason to invent a finding.
	pool := ForLanguages(r.Entries, langs)
	// Then the class cut, for the pass that is asking. Both are cuts rather
	// than ranking signals: an entry the pass cannot act on is wrong, not
	// distant, and cosine has no way to tell those apart.
	pool = ForClasses(pool, classes)
	// And the version cut, last of the three, for the same reason as the other
	// two: an entry about Go before 1.23 shown to a repository on 1.25 is not
	// a distant neighbour, it is a wrong one, and it arrives as reference
	// material a reviewer is asked to trust.
	pool = ForVersions(pool, r.Versions)
	if len(pool) == 0 {
		return nil, nil
	}

	vecs, err := r.Embedder.Embed(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("knowledge: embed the change: %w", err)
	}
	if len(vecs) != 1 {
		return nil, fmt.Errorf("knowledge: embedding the change returned %d vectors", len(vecs))
	}

	hits, err := r.Index.Nearest(vecs[0], pool, r.Candidates)
	if err != nil {
		return nil, err
	}
	hits = above(hits, r.MinScore)

	if r.Rerank == nil || len(hits) <= r.Keep {
		return truncate(hits, r.Keep), nil
	}

	ranked, err := r.Rerank(ctx, query, hits, r.Keep)
	if err != nil {
		// A reranker that failed leaves the cosine order, which is the state
		// this feature ships in without one. Losing the context entirely
		// because the optional half broke would be the wrong trade.
		return truncate(hits, r.Keep), nil
	}
	return truncate(above(ranked, r.MinScore), r.Keep), nil
}

// above drops hits below a cosine. A zero floor keeps everything, which is
// what shipped.
func above(hits []Hit, min float64) []Hit {
	if min <= 0 {
		return hits
	}
	out := hits[:0]
	for _, h := range hits {
		if h.Score >= min {
			out = append(out, h)
		}
	}
	return out
}

// Merge pools per-file results into one set, best score per entry, best first.
//
// An entry retrieved for four files is one entry: repeating it would spend
// four of the five slots on one fact and teach the model the section is
// boilerplate. The file kept is the one that retrieved it most strongly,
// which is the file a reader should look at first.
func Merge(sets [][]Hit, keep int) []Hit {
	best := map[string]Hit{}
	for _, set := range sets {
		for _, h := range set {
			if prior, ok := best[h.Entry.ID]; !ok || h.Score > prior.Score {
				best[h.Entry.ID] = h
			}
		}
	}
	out := make([]Hit, 0, len(best))
	for _, h := range best {
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Entry.ID < out[j].Entry.ID
	})
	return truncate(out, keep)
}

func truncate(hits []Hit, k int) []Hit {
	if k > 0 && len(hits) > k {
		return hits[:k]
	}
	return hits
}

// LanguagesOf reports the languages a set of paths covers, in the names the
// corpus front matter uses.
func LanguagesOf(paths []string) map[string]bool {
	out := map[string]bool{}
	for _, p := range paths {
		if l := languageOf(p); l != "" {
			out[l] = true
		}
	}
	return out
}

// languageOf names a path's language, empty when none is known.
//
// Deliberately narrow. A path this does not recognise contributes no language,
// so a change of unknown files retrieves nothing rather than everything.
func languageOf(p string) string {
	switch strings.ToLower(path.Ext(p)) {
	case ".go":
		return "go"
	case ".py":
		return "python"
	case ".ts", ".tsx":
		return "typescript"
	case ".js", ".jsx", ".mjs", ".cjs":
		return "javascript"
	case ".rs":
		return "rust"
	case ".sh", ".bash":
		return "shell"
	default:
		if base := strings.ToLower(path.Base(p)); base == "makefile" || strings.HasSuffix(base, ".mk") {
			return "shell"
		}
		return ""
	}
}
