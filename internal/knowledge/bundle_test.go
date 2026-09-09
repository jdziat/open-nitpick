package knowledge

import (
	"strings"
	"testing"
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
