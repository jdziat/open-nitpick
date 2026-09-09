package knowledge

import (
	"context"
	"testing"
)

// An entry bounded to a version is not offered outside it.
//
// This is the one cut that can be checked against a fact rather than judged:
// go.mod states the version, so an entry whose claim is about Go before 1.23
// is wrong for a repository on 1.25 in a way cosine has no way to see.
func TestAnEntryOutsideItsVersionIsNotOffered(t *testing.T) {
	entries := []Entry{
		{ID: "before-1.23", Applies: []Constraint{{Name: "go", Op: "<", Version: "1.23"}}},
		{ID: "unbounded"},
	}

	for name, tc := range map[string]struct {
		versions map[string]string
		want     []string
	}{
		"on 1.25, the bounded entry is dropped": {
			versions: map[string]string{"go": "1.25"},
			want:     []string{"unbounded"},
		},
		"on 1.21, it applies": {
			versions: map[string]string{"go": "1.21"},
			want:     []string{"before-1.23", "unbounded"},
		},
		"on 1.23 itself, the boundary excludes it": {
			versions: map[string]string{"go": "1.23"},
			want:     []string{"unbounded"},
		},
		"a repository with no go.mod keeps everything": {
			versions: nil,
			want:     []string{"before-1.23", "unbounded"},
		},
		"a version for something else decides nothing": {
			versions: map[string]string{"rust": "1.80"},
			want:     []string{"before-1.23", "unbounded"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			got := ForVersions(entries, tc.versions)
			if len(got) != len(tc.want) {
				t.Fatalf("kept %d entries, want %d: %v", len(got), len(tc.want), ids(got))
			}
			for i, id := range tc.want {
				if got[i].ID != id {
					t.Errorf("kept[%d] = %q, want %q", i, got[i].ID, id)
				}
			}
		})
	}
}

// Versions compare as versions, not as strings.
//
// "1.9" sorts above "1.23" lexically, which would offer a 1.9 module every
// entry bounded below 1.23 and hide them from a 1.23 one. The linter roster
// uses go/version for this, and so does this.
func TestVersionsCompareNumerically(t *testing.T) {
	c := Constraint{Name: "go", Op: "<", Version: "1.23"}
	if !c.holds("1.9") {
		t.Error("1.9 < 1.23 is false, so the comparison is lexical")
	}
	if c.holds("1.25") {
		t.Error("1.25 < 1.23 is true, so the comparison is lexical")
	}
}

// A malformed applies: line fails the build rather than reaching a model.
//
// The corpus is checked by a test that parses every entry, so a clause nobody
// can evaluate is caught by whoever wrote it. Silently ignoring one would
// offer an entry its author had bounded. The name set is closed for the same
// reason: `golang < 1.23` would otherwise parse, never fire, and say nothing.
func TestAMalformedAppliesIsRefused(t *testing.T) {
	for name, value := range map[string]string{
		"no operator":             "go 1.23",
		"unknown operator":        "go ~> 1.23",
		"not a version":           "go >= banana",
		"empty":                   "",
		"one clause of two":       "go >= 1.21, go",
		"a name nothing resolves": "golang < 1.23",
		"a plausible typo":        "Go1 >= 1.21",
		// Comparison truncates to the language version, so this would silently
		// mean ">= 1.21" and be wider than its author wrote.
		"a patch release": "go >= 1.21.1",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseApplies("some-entry", value); err == nil {
				t.Errorf("applies: %q parsed, want an error", value)
			}
		})
	}
}

// Every clause must hold, so two bound a range.
func TestClausesAreConjunctive(t *testing.T) {
	e := Entry{Applies: []Constraint{
		{Name: "go", Op: ">=", Version: "1.21"},
		{Name: "go", Op: "<", Version: "1.23"},
	}}
	for v, want := range map[string]bool{"1.20": false, "1.21": true, "1.22": true, "1.23": false} {
		if got := e.AppliesTo(map[string]string{"go": v}); got != want {
			t.Errorf("go %s: applies = %v, want %v", v, got, want)
		}
	}
}

// The version cut reaches Retrieve, not only ForVersions.
//
// go-time-after-leak is bounded to Go before 1.23 and is the corpus's only
// bounded entry, so a repository on 1.25 must never be offered it however
// close the change is to it.
func TestRetrieveAppliesTheVersionCut(t *testing.T) {
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

	r := &Retriever{
		Entries:  entries,
		Index:    ix,
		Embedder: fixedEmbedder(ix.Dimensions),
		Model:    ix.Model,
		Keep:     len(entries),
	}
	modern := map[string]string{"go": "1.25"}

	hits, err := r.Retrieve(context.Background(), "time.After in a select loop",
		map[string]bool{"go": true}, everyClass(), modern)
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	for _, h := range hits {
		if h.Entry.ID == "go-time-after-leak" {
			t.Fatalf("an entry bounded to Go before 1.23 was offered to a 1.25 module: %v", hits)
		}
	}

	hits, err = r.Retrieve(context.Background(), "time.After in a select loop",
		map[string]bool{"go": true}, everyClass(), map[string]string{"go": "1.21"})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	var found bool
	for _, h := range hits {
		found = found || h.Entry.ID == "go-time-after-leak"
	}
	if !found {
		t.Error("the entry was not offered to a module inside its bound either, so the cut is not the version's doing")
	}
}

// PoolSize reports what the cuts allowed, which is what says whether Keep can
// bind at all.
//
// The numbers are the corpus's, and they are the reason no reranker ships: on
// five of six languages the pool is at or below Keep, so every entry reaches
// the prompt and there is nothing for a second model call to reorder. See
// docs/findings.md.
func TestPoolSizeReportsWhatTheCutsAllowed(t *testing.T) {
	entries, err := Corpus()
	if err != nil {
		t.Fatalf("Corpus: %v", err)
	}
	r := &Retriever{Entries: entries}

	for lang, want := range map[string]int{
		"go": 8, "python": 2, "typescript": 2, "javascript": 1, "rust": 1, "shell": 1,
	} {
		if got := r.PoolSize(map[string]bool{lang: true}, everyClass(), nil); got != want {
			t.Errorf("PoolSize(%s) = %d, want %d", lang, got, want)
		}
	}

	// The version cut counts too, so the number a run logs is that run's pool
	// rather than the corpus's size.
	if got := r.PoolSize(map[string]bool{"go": true}, everyClass(), map[string]string{"go": "1.25"}); got != 7 {
		t.Errorf("PoolSize(go) on a 1.25 module = %d, want 7", got)
	}

	// A language the corpus says nothing about reaches nothing, generic
	// entries included, which is what makes an empty pool an answer.
	if got := r.PoolSize(map[string]bool{"cobol": true}, everyClass(), nil); got != 0 {
		t.Errorf("PoolSize(cobol) = %d, want 0", got)
	}
}
