package evals

import (
	"strings"
	"testing"
)

// TestPlantedDefectsPointAtRealLines validates the corpus itself.
//
// Every Defect.Line was hand-counted, and four of five were wrong — one pointed
// past the end of its file. Nothing failed, because the scorer's line tolerance
// silently absorbed the error. A wrong anchor corrupts every recall measurement
// taken with it, so the ground truth needs a test as much as the code does.
//
// This runs in `go test ./...`: it needs no network and no credentials.
func TestPlantedDefectsPointAtRealLines(t *testing.T) {
	for _, f := range Fixtures() {
		for _, d := range f.Defects {
			content, ok := f.Head[d.Path]
			if !ok {
				t.Errorf("%s: defect names %q, which is not in the fixture's head state", f.Name, d.Path)
				continue
			}

			lines := strings.Split(content, "\n")
			if len(lines) > 0 && lines[len(lines)-1] == "" {
				lines = lines[:len(lines)-1]
			}

			if d.Line < 1 || d.Line > len(lines) {
				t.Errorf("%s: defect at %s:%d is outside the file (%d lines): %s",
					f.Name, d.Path, d.Line, len(lines), d.Why)
			}
		}
	}
}

// TestPlantedDefectsAreOnTheRightLine pins each anchor to text that must appear
// on it, so a future edit to a fixture cannot silently shift the ground truth.
func TestPlantedDefectsAreOnTheRightLine(t *testing.T) {
	// fixture -> line -> substring the line must contain.
	want := map[string]map[int]string{
		"go-nil-deref":             {10: "http.Get"},
		"go-sql-injection":         {17: "Sprintf"},
		"go-hardcoded-secret":      {12: "sk-live"},
		"python-command-injection": {12: "os.system"},
		"capacity-hint-nit":        {17: "make("},
		"multi-defect":             {19: "os.Create", 25: "go func"},
	}

	byName := map[string]Fixture{}
	for _, f := range Fixtures() {
		byName[f.Name] = f
	}

	for name, lines := range want {
		f, ok := byName[name]
		if !ok {
			t.Errorf("fixture %q no longer exists; update this test", name)
			continue
		}

		for lineNo, needle := range lines {
			var content string
			for _, c := range f.Head {
				if strings.Contains(c, needle) {
					content = c
					break
				}
			}
			if content == "" {
				t.Errorf("%s: no head file contains %q", name, needle)
				continue
			}

			split := strings.Split(content, "\n")
			if lineNo > len(split) {
				t.Errorf("%s: line %d is past EOF", name, lineNo)
				continue
			}
			if !strings.Contains(split[lineNo-1], needle) {
				t.Errorf("%s: line %d is %q, expected it to contain %q",
					name, lineNo, strings.TrimSpace(split[lineNo-1]), needle)
			}
		}
	}
}

// TestKeywordsCannotRewardHallucination guards the corpus against keywords so
// generic that a wrong answer scores as a hit.
//
// capacity-hint-nit previously listed "off-by-one" and "one too" — but its loop
// bound is CORRECT. A model confidently reporting a bounds bug was wrong and
// scored full credit, in the one fixture built to catch exactly that.
func TestKeywordsCannotRewardHallucination(t *testing.T) {
	banned := map[string][]string{
		"capacity-hint-nit": {"off-by-one", "one too", "windows", "grow"},
	}

	for _, f := range Fixtures() {
		bad, ok := banned[f.Name]
		if !ok {
			continue
		}
		for _, d := range f.Defects {
			for _, kw := range d.Keywords {
				for _, b := range bad {
					if strings.EqualFold(kw, b) {
						t.Errorf("%s: keyword %q would credit a hallucinated defect", f.Name, kw)
					}
				}
			}
		}
	}
}
