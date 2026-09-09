package fence

import (
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
