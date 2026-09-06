package evals

import (
	"os"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/v2/internal/config"
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
			content, ok := f.Head[d.Path]
			if !ok {
				t.Errorf("%s: plant path %s is not in Head", f.Name, d.Path)
				continue
			}
			// The anchor is the line the fixture's comment names, not one
			// past it: this corpus sits outside the registries that check
			// anchors for the others.
			lines := strings.Split(content, "\n")
			want, known := slopAnchors[f.Name]
			if !known {
				t.Errorf("%s: no entry in slopAnchors; add the text of its anchored line", f.Name)
				continue
			}
			if d.Line < 1 || d.Line > len(lines) || !strings.Contains(lines[d.Line-1], want) {
				got := ""
				if d.Line >= 1 && d.Line <= len(lines) {
					got = lines[d.Line-1]
				}
				t.Errorf("%s: line %d is %q, want the line holding %q", f.Name, d.Line, got, want)
			}
		}
		if !Slop(f.Name) {
			t.Errorf("%s: Slop() does not recognise it", f.Name)
		}
	}
	// A plant's keywords name what only the planted file holds, so none may
	// appear in its control's files: a finding on the control that quoted
	// them would otherwise be credited to the plant's mechanism.
	for i := 0; i+1 < len(fixtures); i += 2 {
		control := fixtures[i+1]
		for _, d := range fixtures[i].Defects {
			for _, k := range d.Keywords {
				for p, content := range control.Head {
					if strings.Contains(strings.ToLower(content), strings.ToLower(k)) {
						t.Errorf("%s: keyword %q appears in control %s (%s)", fixtures[i].Name, k, control.Name, p)
					}
				}
			}
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

// slopAnchors is the text on each plant's anchored line.
var slopAnchors = map[string]string{
	"go-slop-restating-comments":       "Initialize the total",
	"python-slop-swallowed-exception":  "except Exception",
	"ts-slop-chat-prose":               "Sure! Here's",
	"go-slop-type-excluded-check":      "n != 0 || n == 0",
	"python-slop-test-asserts-nothing": "assert True",
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
