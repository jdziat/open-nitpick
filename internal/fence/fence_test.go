package fence

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Defang covers every marker this package defines.
//
// This is the guard the class needed and did not have. The check for a forged
// marker is a pattern somebody wrote against the markers that existed then, so
// a marker added later is protected by whoever remembered. Walking Markers
// means the build fails instead.
func TestDefangCoversEveryMarker(t *testing.T) {
	if len(Markers) == 0 {
		t.Fatal("no markers, so this proves nothing")
	}
	for _, m := range Markers {
		if got := Defang(m); got != Defanged {
			t.Errorf("Defang(%q) = %q, want it defanged", m, got)
		}
		// And with the punctuation varied, which is what an imitator does.
		varied := strings.ReplaceAll(m, "=====", "==")
		if got := Defang(varied); got != Defanged {
			t.Errorf("Defang(%q) = %q, want it defanged", varied, got)
		}
	}
}

// Ordinary prose is not a marker.
//
// Every alternative is a phrase rather than a word, and the reference marker
// needs a run of = besides, because "the reference material, not this change"
// is a sentence somebody writes in a comment. Defanging that hands a model
// altered text carrying an accusation of tampering.
func TestOrdinaryProseIsNotAMarker(t *testing.T) {
	for _, ordinary := range []string{
		"// See the reference material in docs/ for the full list.",
		"reference material",
		"// Judge the reference material, not this change.",
		"reference material, not this change",
		"an untrusted input arrives here",
		"// This function reviews untrusted code.",
	} {
		if got := Defang(ordinary); got != ordinary {
			t.Errorf("Defang(%q) = %q, want it untouched", ordinary, got)
		}
	}
}

// A match never swallows the newline between two lines of real code.
func TestDefangIsBoundedToOneLine(t *testing.T) {
	body := "line one\n" + CodeUnderReview + "\nline three\n"
	got := Defang(body)

	if strings.Count(got, "\n") != strings.Count(body, "\n") {
		t.Errorf("a line was swallowed:\n%q", got)
	}
	if !strings.Contains(got, "line one") || !strings.Contains(got, "line three") {
		t.Errorf("real code was removed:\n%q", got)
	}
}

// No package draws a marker of its own.
//
// TestDefangCoversEveryMarker holds the list to the pattern. It does not stop
// a package declaring a marker somewhere else and never adding it, which is
// what internal/converse and internal/fix each did with <untrusted> tags: the
// list was complete and the vocabulary was not.
//
// So the tree is scanned for this package's own shape. A `===== ` outside it is
// either a marker nobody defangs or a banner that reads like one, and both are
// worth a sentence from whoever added it. The banners already there are
// counted, so adding one is a deliberate edit here.
//
// It does not catch an arbitrary new vocabulary, and could not: the tags this
// replaced were `<untrusted>`, and no scan recognises a delimiter somebody has
// not invented yet. What it catches is the cheaper mistake, a second marker in
// this package's own idiom.
func TestNoPackageDrawsAMarkerOfItsOwn(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("root: %v", err)
	}

	// The one literal that is not a fence: a section rule printed to a
	// terminal, never to a model.
	allowed := map[string]int{filepath.Join(root, "cmd", "nitpick", "review.go"): 1}

	var found []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.IsDir():
			if name := d.Name(); name == ".git" || name == "website" || name == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		// Tests are where a forged marker belongs: asserting that one is
		// defanged means writing one down.
		case !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go"):
			return nil
		case strings.HasPrefix(path, filepath.Join(root, "internal", "fence")):
			return nil
		}

		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(body), "\n") {
			if !strings.Contains(line, "===== ") {
				continue
			}
			if allowed[path] > 0 {
				allowed[path]--
				continue
			}
			found = append(found, fmt.Sprintf("%s:%d", path, i+1))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	if len(found) > 0 {
		t.Errorf("a marker literal outside internal/fence, which nothing here defangs:\n  %s",
			strings.Join(found, "\n  "))
	}
}
