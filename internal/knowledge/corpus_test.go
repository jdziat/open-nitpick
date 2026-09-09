package knowledge

import (
	"strings"
	"testing"
	"time"
)

// Every entry parses, and every entry carries what makes it checkable.
//
// The corpus is prose this repository publishes into a prompt, where a model
// treats it as true. An entry with no source is an assertion with nobody
// behind it, and one with no date cannot be told from one that was right three
// years ago.
func TestEveryEntryIsWellFormed(t *testing.T) {
	entries, err := Corpus()
	if err != nil {
		t.Fatalf("Corpus: %v", err)
	}
	if len(entries) < 10 {
		t.Fatalf("entries = %d, want a corpus worth retrieving from", len(entries))
	}

	seen := map[string]bool{}
	for _, e := range entries {
		if seen[e.ID] {
			t.Errorf("%s: duplicate id", e.ID)
		}
		seen[e.ID] = true

		if !strings.HasPrefix(e.Source, "https://") {
			t.Errorf("%s: source %q is not a link a reader can follow", e.ID, e.Source)
		}
		if e.Checked.After(time.Now()) {
			t.Errorf("%s: checked %s is in the future", e.ID, e.Checked.Format("2006-01-02"))
		}
		// A title that names a topic cannot be judged against a diff; one that
		// states a rule can. The test for that is crude on purpose: a verb.
		if len(strings.Fields(e.Title)) < 5 {
			t.Errorf("%s: title %q is a topic, not a rule", e.ID, e.Title)
		}
		if !strings.Contains(e.Body, "What to look for") {
			t.Errorf("%s: no \"What to look for\" section; an entry that does not say "+
				"what to search for in a diff is an essay", e.ID)
		}
	}
}

// An entry offered to the wrong language is a reason to invent a finding.
func TestLanguageFilterKeepsOtherLanguagesOut(t *testing.T) {
	entries, err := Corpus()
	if err != nil {
		t.Fatalf("Corpus: %v", err)
	}

	got := ForLanguages(entries, map[string]bool{"go": true})
	if len(got) == 0 {
		t.Fatal("no Go entries; the corpus should have several")
	}
	for _, e := range got {
		if !contains(e.Languages, "go") {
			t.Errorf("%s (%v) was offered to a Go change", e.ID, e.Languages)
		}
	}

	if n := len(ForLanguages(entries, nil)); n != 0 {
		t.Errorf("a change with no known language got %d entries, want 0", n)
	}
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

// EVERY committed index covers the committed corpus.
//
// An entry added without regenerating an index is unreachable through it:
// retrieval works, returns the others, and nothing says the new one was
// skipped. Each shipped index is checked, because an operator selecting the
// second one gets no warning that only the first was kept current.
func TestEveryCommittedIndexCoversTheCorpus(t *testing.T) {
	entries, err := Corpus()
	if err != nil {
		t.Fatalf("Corpus: %v", err)
	}
	models, err := ShippedModels()
	if err != nil {
		t.Fatalf("ShippedModels: %v", err)
	}
	if len(models) == 0 {
		t.Fatal("this build ships no index")
	}

	for _, model := range models {
		raw, err := SelectIndex(model)
		if err != nil {
			t.Fatalf("SelectIndex(%q): %v", model, err)
		}
		ix, err := LoadIndex(raw, entries)
		if err != nil {
			t.Fatalf("the committed index for %s does not match the corpus; "+
				"run `nitpick knowledge-index`: %v", model, err)
		}
		if len(ix.Vectors) != len(entries) {
			t.Errorf("%s: index has %d vectors for %d entries", model, len(ix.Vectors), len(entries))
		}
		if ix.Dimensions == 0 {
			t.Errorf("%s: the index reports no dimensions", model)
		}
		if ix.Entries != len(entries) {
			t.Errorf("%s: index records %d entries, the corpus has %d", model, ix.Entries, len(entries))
		}
	}
}

// An entry EDITED without regenerating keeps its vector under the same id, so
// every count above still agrees and the vector now describes a paragraph
// nobody wrote. The corpus hash is what catches it.
func TestAnEditedEntryInvalidatesTheIndex(t *testing.T) {
	entries, err := Corpus()
	if err != nil {
		t.Fatalf("Corpus: %v", err)
	}
	models, err := ShippedModels()
	if err != nil || len(models) == 0 {
		t.Fatalf("ShippedModels: %v", err)
	}
	raw, err := SelectIndex(models[0])
	if err != nil {
		t.Fatalf("SelectIndex: %v", err)
	}

	edited := append([]Entry(nil), entries...)
	edited[0].Body += "\n\nA sentence added after the vectors were computed."

	if _, err := LoadIndex(raw, edited); err == nil {
		t.Fatal("an edited entry loaded against stale vectors; retrieval would answer from text nobody wrote")
	}
}

// Correcting a citation does not move a vector, so it must not force a
// re-embed: an operator who has to regenerate for a URL will stop checking.
func TestACitationFixDoesNotInvalidateTheIndex(t *testing.T) {
	entries, err := Corpus()
	if err != nil {
		t.Fatalf("Corpus: %v", err)
	}
	recited := append([]Entry(nil), entries...)
	recited[0].Source = "https://example.invalid/moved"

	if CorpusHash(entries) != CorpusHash(recited) {
		t.Error("the corpus hash covers the citation; a URL fix now needs an embedding run")
	}
}
