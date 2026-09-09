package evals

import (
	"os"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/knowledge"
)

// Rule 15 of docs/measurement.md: a corpus outside AllFixtures is not swept by
// the cross-fixture ground-truth tests, so it brings its own.

// Every plant has an entry that could supply its fact, and every control has
// none of its own.
//
// The pairing is the whole design. A run that finds the plants and stays quiet
// on the controls has retrieved something useful; one that finds both has
// learned to report whatever it was shown, which would be a loss dressed as a
// gain.
func TestTheKnowledgeCorpusIsPairedAndCovered(t *testing.T) {
	fixtures := KnowledgeFixtures()
	if len(fixtures) != 12 {
		t.Fatalf("fixtures = %d, want 12", len(fixtures))
	}

	plants, controls := 0, 0
	for _, f := range fixtures {
		if f.Clean() {
			controls++
			continue
		}
		plants++
		if len(f.Defects) != 1 {
			t.Errorf("%s: %d defects; one fact per fixture is what this corpus measures",
				f.Name, len(f.Defects))
		}
	}
	if plants != controls {
		t.Errorf("%d plants and %d controls, want them paired", plants, controls)
	}

	// The corpus this retrieves from has to be able to answer. Not a claim
	// that it does answer: that is what the measurement is for.
	entries, err := knowledge.Corpus()
	if err != nil {
		t.Fatalf("Corpus: %v", err)
	}
	langs := map[string]bool{}
	for _, e := range entries {
		for _, l := range e.Languages {
			langs[l] = true
		}
	}
	for _, f := range fixtures {
		got := knowledge.LanguagesOf(pathsOf(f))
		if len(got) == 0 {
			t.Errorf("%s: no file resolves to a language the corpus indexes", f.Name)
			continue
		}
		covered := false
		for l := range got {
			if langs[l] {
				covered = true
			}
		}
		if !covered {
			t.Errorf("%s: languages %v, and the corpus has no entry for any of them", f.Name, got)
		}
	}
}

// The Makefile names the whole corpus, so a fixture added here is one a run
// includes rather than one nothing spends.
func TestTheMakefileNamesTheWholeKnowledgeCorpus(t *testing.T) {
	src, err := os.ReadFile("../../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	var line string
	for _, l := range strings.Split(string(src), "\n") {
		if strings.HasPrefix(l, "KNOWLEDGE :=") {
			line = strings.TrimSpace(strings.TrimPrefix(l, "KNOWLEDGE :="))
		}
	}
	if line == "" {
		t.Fatal("the Makefile has no KNOWLEDGE line")
	}
	named := map[string]bool{}
	for _, n := range strings.Split(line, ",") {
		named[strings.TrimSpace(n)] = true
	}
	for _, f := range KnowledgeFixtures() {
		if !named[f.Name] {
			t.Errorf("the Makefile's KNOWLEDGE does not name %q", f.Name)
		}
	}
	if len(named) != len(KnowledgeFixtures()) {
		t.Errorf("KNOWLEDGE names %d fixtures, the corpus has %d", len(named), len(KnowledgeFixtures()))
	}
}

func pathsOf(f Fixture) []string {
	out := make([]string, 0, len(f.Head))
	for p := range f.Head {
		out = append(out, p)
	}
	return out
}
