package knowledge

import (
	"context"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
)

// Selection reads the model recorded inside the file, not the filename, so a
// renamed or misnamed index cannot make a run query the wrong vectors.
func TestSelectIndexMatchesTheRecordedModel(t *testing.T) {
	models, err := ShippedModels()
	if err != nil {
		t.Fatalf("ShippedModels: %v", err)
	}
	if len(models) < 2 {
		t.Fatalf("this build ships %d index(es); the point of the directory is more than one", len(models))
	}

	for _, m := range models {
		raw, err := SelectIndex(m)
		if err != nil {
			t.Fatalf("SelectIndex(%q): %v", m, err)
		}
		got, err := indexModel(raw)
		if err != nil {
			t.Fatalf("indexModel(%q): %v", m, err)
		}
		if got != m {
			t.Errorf("SelectIndex(%q) returned the index for %q", m, got)
		}
	}

	// Case is not a difference between two names for one model.
	if _, err := SelectIndex(strings.ToUpper(models[0])); err != nil {
		t.Errorf("SelectIndex is case sensitive: %v", err)
	}
}

// An unsupported embedder is an answer rather than a puzzle: the error names
// what this build carries and what to do instead.
func TestSelectIndexNamesWhatItHas(t *testing.T) {
	_, err := SelectIndex("openai/text-embedding-3-large")
	if err == nil {
		t.Fatal("an unshipped embedding model selected an index")
	}
	for _, want := range []string{"text-embedding-3-large", "knowledge_index", "nitpick knowledge-index"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q: %v", want, err)
		}
	}
}

// Both shipped indexes describe the same corpus. Two that disagreed would mean
// one was regenerated and the other left behind, and an operator selecting the
// stale one gets no warning beyond the load error this pins.
func TestTheShippedIndexesAgreeOnTheCorpus(t *testing.T) {
	entries, err := Corpus()
	if err != nil {
		t.Fatalf("Corpus: %v", err)
	}
	models, err := ShippedModels()
	if err != nil {
		t.Fatalf("ShippedModels: %v", err)
	}

	want := CorpusHash(entries)
	for _, m := range models {
		raw, err := SelectIndex(m)
		if err != nil {
			t.Fatalf("SelectIndex(%q): %v", m, err)
		}
		ix, err := LoadIndex(raw, entries)
		if err != nil {
			t.Fatalf("LoadIndex(%q): %v", m, err)
		}
		if ix.Corpus != want {
			t.Errorf("%s was built from corpus %s, the tree has %s", m, ix.Corpus, want)
		}
		if ix.Built == "" {
			t.Errorf("%s records no build date", m)
		}
	}
}

// A width is not an identity, and four bundles make that reachable:
// text-embedding-3-small and gemini-embedding-001 are both 1536, so one
// model's query against the other's vectors passes every shape check and
// returns the nearest neighbours of a point in a space it does not belong to.
func TestRetrievalRefusesAQueryFromAnotherModelOfTheSameWidth(t *testing.T) {
	entries, err := Corpus()
	if err != nil {
		t.Fatalf("Corpus: %v", err)
	}
	raw, err := SelectIndex("openrouter/openai/text-embedding-3-small")
	if err != nil {
		t.Fatalf("SelectIndex: %v", err)
	}
	ix, err := LoadIndex(raw, entries)
	if err != nil {
		t.Fatalf("LoadIndex: %v", err)
	}

	// Same width, different model. Nothing about the vectors says so.
	r := &Retriever{
		Entries:  entries,
		Index:    ix,
		Embedder: fixedEmbedder(ix.Dimensions),
		Model:    "google/gemini-embedding-001",
		Keep:     5,
	}
	if _, err := r.Retrieve(context.Background(), "some changed lines", map[string]bool{"go": true}, everyClass(), nil); err == nil {
		t.Fatal("a query from another model of the same width was answered; the result would be ordered nonsense")
	}

	// And a retriever that does not say what it embeds with is refused too,
	// rather than assumed to match.
	r.Model = ""
	if _, err := r.Retrieve(context.Background(), "some changed lines", map[string]bool{"go": true}, everyClass(), nil); err == nil {
		t.Fatal("a retriever naming no model was answered")
	}

	r.Model = ix.Model
	if _, err := r.Retrieve(context.Background(), "some changed lines", map[string]bool{"go": true}, everyClass(), nil); err != nil {
		t.Fatalf("the index's own model was refused: %v", err)
	}
}

// fixedEmbedder returns one vector of the given width, so the test is about
// identity rather than about shape.
type fixedEmbedder int

func (f fixedEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range out {
		v := make([]float32, int(f))
		for j := range v {
			v[j] = 0.1
		}
		out[i] = v
	}
	return out, nil
}

func everyClass() map[config.Class]bool {
	out := map[config.Class]bool{}
	for _, c := range config.Classes() {
		out[c] = true
	}
	return out
}
