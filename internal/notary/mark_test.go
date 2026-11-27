package notary

import "testing"

// The page tells a person which answers were rejected, by the prefix the review
// engine writes. The engine's constant is unexported in another package, so this
// holds the page's copy to the documented value: the engine's own test pins its
// side, and a change to either must change both.
func TestTheUnusableMarkMatchesWhatTheReviewEngineWrites(t *testing.T) {
	if unusableMark != "!" {
		t.Errorf("unusableMark = %q; internal/review writes %q before a response it rejected or retried", unusableMark, "!")
	}
}
