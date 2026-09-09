package knowledge

import (
	"context"
	"strings"
	"testing"
)

// fakeEmbedder returns a deterministic vector per text, with no network.
type fakeEmbedder struct {
	dims int
	err  error
}

func (f fakeEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	if f.err != nil {
		return nil, f.err
	}
	dims := f.dims
	if dims == 0 {
		dims = 8
	}
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v := make([]float32, dims)
		// A bag of characters. Enough that two similar texts land near each
		// other, which is all these tests need of it.
		for j, r := range t {
			v[(int(r)+j)%dims] += 1
		}
		out[i] = v
	}
	return out, nil
}

func testEntries() []Entry {
	return []Entry{
		{ID: "a", Title: "alpha", Body: "alpha body", Languages: []string{"go"}},
		{ID: "b", Title: "beta", Body: "beta body", Languages: []string{"go"}},
	}
}

// Vectors from two embedding models are not comparable. Searching one model's
// vectors with another's query returns real numbers in a sensible order that
// mean nothing, which is worse than an error because it looks like an answer.
func TestAnIndexBuiltByAnotherModelIsRefused(t *testing.T) {
	ix, err := Build(context.Background(), testEntries(), "openai/text-embedding-3-small", fakeEmbedder{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if err := ix.CheckModel("openai/text-embedding-3-small"); err != nil {
		t.Errorf("the index refused the model that built it: %v", err)
	}

	err = ix.CheckModel("gemini/text-embedding-004")
	if err == nil {
		t.Fatal("a query from a different embedding model was accepted")
	}
	for _, want := range []string{"not comparable", "text-embedding-3-small", "text-embedding-004"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
}

// An entry added to the corpus without regenerating the index would otherwise
// be silently unreachable: retrieval would work, and would never return it.
func TestAnEntryMissingFromTheIndexIsAnError(t *testing.T) {
	ix, err := Build(context.Background(), testEntries(), "m", fakeEmbedder{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	raw, err := MarshalIndex(ix)
	if err != nil {
		t.Fatalf("MarshalIndex: %v", err)
	}

	grown := append(testEntries(), Entry{ID: "c", Title: "gamma", Body: "gamma body"})
	_, err = LoadIndex(raw, grown)
	if err == nil {
		t.Fatal("an entry with no vector loaded without complaint")
	}
	if !strings.Contains(err.Error(), "c is in the corpus and not the index") {
		t.Errorf("the error does not name the entry: %v", err)
	}
}

// Ties order by id so a review is reproducible.
func TestNearestIsDeterministic(t *testing.T) {
	entries := testEntries()
	ix, err := Build(context.Background(), entries, "m", fakeEmbedder{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	query := make([]float32, ix.Dimensions) // every score is 0, so every pair ties

	first, err := ix.Nearest(query, entries, 2)
	if err != nil {
		t.Fatalf("Nearest: %v", err)
	}
	for i := 0; i < 20; i++ {
		again, err := ix.Nearest(query, entries, 2)
		if err != nil {
			t.Fatalf("Nearest: %v", err)
		}
		for j := range first {
			if again[j].Entry.ID != first[j].Entry.ID {
				t.Fatalf("order changed between runs: %s then %s", first[j].Entry.ID, again[j].Entry.ID)
			}
		}
	}
}

// A query of the wrong width is a bug upstream, and comparing it anyway would
// produce a score over whichever prefix happened to match.
func TestAQueryOfTheWrongWidthIsRefused(t *testing.T) {
	entries := testEntries()
	ix, err := Build(context.Background(), entries, "m", fakeEmbedder{dims: 8})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, err := ix.Nearest(make([]float32, 4), entries, 1); err == nil {
		t.Fatal("a four-dimensional query searched an eight-dimensional index")
	}
}
