package knowledge

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
)

// The committed index.
//
// Vectors are computed once by `nitpick knowledge-index` and checked in, the
// way docs/configuration-reference.md is generated and checked. The
// alternative, embedding at every review, puts a credential and a per-run cost
// on every run for a corpus that only changes when someone edits a file.

// Embedder is the part of the SDK's embedding surface this package needs.
//
// An interface of one method rather than the SDK's, so a test can supply
// vectors without a provider, a credential or a network.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// Index is the corpus with a vector per entry.
type Index struct {
	// Model is the embedding model that produced every vector below, as
	// "provider/model".
	//
	// Recorded because vectors from two models are not comparable. Searching
	// one model's vectors with another model's query returns the nearest
	// neighbours of a point in a space it does not belong to: real numbers,
	// ordered, meaningless. Retrieval refuses rather than answering.
	Model string `json:"model"`

	// Dimensions the vectors carry, so a truncated file is caught on load
	// rather than at the first comparison.
	Dimensions int `json:"dimensions"`

	// Vectors keyed by entry id rather than positional, so an entry renamed
	// or removed is a load error naming it rather than a silent shift of every
	// vector after it onto the wrong entry.
	Vectors map[string][]float32 `json:"vectors"`
}

// Build embeds every entry and returns the index.
func Build(ctx context.Context, entries []Entry, model string, e Embedder) (*Index, error) {
	if len(entries) == 0 {
		return nil, fmt.Errorf("knowledge: nothing to index")
	}
	texts := make([]string, len(entries))
	for i, entry := range entries {
		texts[i] = entry.Text()
	}

	vecs, err := e.Embed(ctx, texts)
	if err != nil {
		return nil, fmt.Errorf("knowledge: embed corpus: %w", err)
	}
	if len(vecs) != len(entries) {
		return nil, fmt.Errorf("knowledge: embedded %d of %d entries", len(vecs), len(entries))
	}

	ix := &Index{Model: model, Vectors: make(map[string][]float32, len(entries))}
	for i, entry := range entries {
		if len(vecs[i]) == 0 {
			return nil, fmt.Errorf("knowledge: %s: empty vector", entry.ID)
		}
		if ix.Dimensions == 0 {
			ix.Dimensions = len(vecs[i])
		}
		if len(vecs[i]) != ix.Dimensions {
			return nil, fmt.Errorf("knowledge: %s: %d dimensions, others have %d",
				entry.ID, len(vecs[i]), ix.Dimensions)
		}
		ix.Vectors[entry.ID] = vecs[i]
	}
	return ix, nil
}

// MarshalIndex writes an index as the committed file.
//
// Indented and with sorted keys, because this file lands in pull requests and
// a diff nobody can read is a diff nobody reviews.
func MarshalIndex(ix *Index) ([]byte, error) {
	b, err := json.MarshalIndent(ix, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// LoadIndex reads a committed index and checks it against the corpus.
func LoadIndex(raw []byte, entries []Entry) (*Index, error) {
	var ix Index
	if err := json.Unmarshal(raw, &ix); err != nil {
		return nil, fmt.Errorf("knowledge: parse index: %w", err)
	}
	if ix.Model == "" {
		return nil, fmt.Errorf("knowledge: index names no model")
	}
	for _, e := range entries {
		v, ok := ix.Vectors[e.ID]
		if !ok {
			return nil, fmt.Errorf("knowledge: %s is in the corpus and not the index; regenerate it", e.ID)
		}
		if len(v) != ix.Dimensions {
			return nil, fmt.Errorf("knowledge: %s: %d dimensions, index says %d", e.ID, len(v), ix.Dimensions)
		}
	}
	return &ix, nil
}

// Hit is one retrieved entry and how close it was.
type Hit struct {
	Entry Entry
	Score float64
}

// Nearest returns the entries closest to a query vector, best first.
//
// Cosine over normalised vectors. The corpus is tens of entries, so a linear
// scan is the whole algorithm and an approximate index would be complexity
// bought against nothing.
func (ix *Index) Nearest(query []float32, entries []Entry, k int) ([]Hit, error) {
	if len(query) != ix.Dimensions {
		return nil, fmt.Errorf("knowledge: query has %d dimensions, the index has %d",
			len(query), ix.Dimensions)
	}
	hits := make([]Hit, 0, len(entries))
	for _, e := range entries {
		v, ok := ix.Vectors[e.ID]
		if !ok {
			continue
		}
		hits = append(hits, Hit{Entry: e, Score: cosine(query, v)})
	}
	// By score, then by id, so two entries that tie do not reorder between
	// runs and make a review unreproducible for no reason.
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].Entry.ID < hits[j].Entry.ID
	})
	if k > 0 && len(hits) > k {
		hits = hits[:k]
	}
	return hits, nil
}

// CheckModel refuses a query embedded by a different model than the index.
func (ix *Index) CheckModel(model string) error {
	if !strings.EqualFold(strings.TrimSpace(model), strings.TrimSpace(ix.Model)) {
		return fmt.Errorf("knowledge: the index was built with %q and this run embeds with %q; "+
			"vectors from two models are not comparable, so regenerate the index or name the model it was built with",
			ix.Model, model)
	}
	return nil
}

// cosine is the similarity of two vectors, 0 when either has no magnitude.
func cosine(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range a {
		x, y := float64(a[i]), float64(b[i])
		dot += x * y
		na += x * x
		nb += y * y
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}
