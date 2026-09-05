package evals

import (
	"os"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
)

// The slop corpus is pairs: every planted fixture has a control beside it,
// every plant is in the slop class, and every fixture is in EveryFixture
// under a unique name.
func TestSlopCorpusIsWellFormed(t *testing.T) {
	fixtures := SlopFixtures()
	if len(fixtures)%2 != 0 || len(fixtures) < 4 {
		t.Fatalf("slop corpus has %d fixtures; it is built as planted/control pairs", len(fixtures))
	}
	seen := map[string]bool{}
	for i, f := range fixtures {
		if seen[f.Name] {
			t.Errorf("duplicate fixture name %s", f.Name)
		}
		seen[f.Name] = true
		if i%2 == 0 && f.Clean() {
			t.Errorf("%s: expected a planted fixture at an even index", f.Name)
		}
		if i%2 == 1 && !f.Clean() {
			t.Errorf("%s: expected a clean control at an odd index", f.Name)
		}
		for _, d := range f.Defects {
			if d.Class != config.ClassSlop {
				t.Errorf("%s: plant class %s, want slop", f.Name, d.Class)
			}
			if _, ok := f.Head[d.Path]; !ok {
				t.Errorf("%s: plant path %s is not in Head", f.Name, d.Path)
			}
		}
		if !Slop(f.Name) {
			t.Errorf("%s: Slop() does not recognise it", f.Name)
		}
	}
	every := map[string]bool{}
	for _, f := range EveryFixture() {
		every[f.Name] = true
	}
	for _, f := range fixtures {
		if !every[f.Name] {
			t.Errorf("%s is not in EveryFixture", f.Name)
		}
	}
}

// The Makefile's SLOP line and SlopFixtures() name the same set, so
// `make eval-slop` runs the whole corpus and nothing else.
func TestTheMakefileNamesTheWholeSlopCorpus(t *testing.T) {
	src, err := os.ReadFile("../../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	var line string
	for _, l := range strings.Split(string(src), "\n") {
		if strings.HasPrefix(l, "SLOP :=") {
			line = strings.TrimSpace(strings.TrimPrefix(l, "SLOP :="))
		}
	}
	if line == "" {
		t.Fatal("the Makefile has no SLOP line")
	}
	named := map[string]bool{}
	for _, n := range strings.Split(line, ",") {
		named[strings.TrimSpace(n)] = true
	}
	corpus := map[string]bool{}
	for _, f := range SlopFixtures() {
		corpus[f.Name] = true
		if !named[f.Name] {
			t.Errorf("SlopFixtures() has %q and the Makefile's SLOP line does not", f.Name)
		}
	}
	for n := range named {
		if !corpus[n] {
			t.Errorf("the Makefile's SLOP line names %q, which is not in SlopFixtures()", n)
		}
	}
}
