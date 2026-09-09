package knowledge

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/jdziat/open-nitpick/internal/config"
)

// Retriever answers a change with the entries it should be judged against.
type Retriever struct {
	Entries  []Entry
	Index    *Index
	Embedder Embedder

	// Candidates is how many the vector search returns, and Keep how many
	// survive to the prompt. Keep is what costs tokens; Candidates is what
	// gives the reranker something to choose between.
	Candidates int
	Keep       int

	// Rerank picks from the candidates. Nil keeps the top Keep by cosine,
	// which is the control the measurement compares against.
	Rerank func(ctx context.Context, query string, hits []Hit, keep int) ([]Hit, error)
}

// Retrieve returns the entries closest to a change.
func (r *Retriever) Retrieve(ctx context.Context, query string, langs map[string]bool, classes map[config.Class]bool) ([]Hit, error) {
	if r == nil || r.Index == nil || r.Embedder == nil {
		return nil, nil
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
	return truncate(ranked, r.Keep), nil
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
