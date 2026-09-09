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

// The committed index covers the committed corpus.
//
// An entry added without regenerating the index is unreachable: retrieval
// works, returns the other twelve, and nothing says the thirteenth was skipped.
// CI runs this, so the corpus and its vectors cannot drift apart quietly.
func TestTheCommittedIndexCoversTheCorpus(t *testing.T) {
	entries, err := Corpus()
	if err != nil {
		t.Fatalf("Corpus: %v", err)
	}
	ix, err := LoadIndex(IndexJSON(), entries)
	if err != nil {
		t.Fatalf("the committed index does not match the corpus; "+
			"run `nitpick knowledge-index`: %v", err)
	}
	if len(ix.Vectors) != len(entries) {
		t.Errorf("index has %d vectors for %d entries", len(ix.Vectors), len(entries))
	}
	if ix.Dimensions == 0 {
		t.Error("the index reports no dimensions")
	}
}
