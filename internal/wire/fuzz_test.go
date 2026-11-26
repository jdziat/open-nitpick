package wire

import (
	"slices"
	"testing"
)

// FuzzInputsHashSeparatesEveryDifferentShape asserts the property the length
// prefixes exist for: two Inputs that are not the same value never hash alike,
// however their bytes are split between fields and lists.
func FuzzInputsHashSeparatesEveryDifferentShape(f *testing.F) {
	f.Add("a", "b", "c", "d")
	f.Add("ab", "", "c", "")
	f.Add("", "", "", "")
	f.Add("settings", "batches", "x", "\x00")
	f.Fuzz(func(t *testing.T, a, b, c, d string) {
		shapes := []Inputs{
			{Tool: a, Provider: b, Model: c, System: d},
			{Tool: a + b, Provider: c, Model: d},
			{Tool: a, Provider: b + c, Model: d},
			{Settings: []string{a, b}, Batches: []string{c, d}},
			{Settings: []string{a}, Batches: []string{b, c, d}},
			{Settings: []string{a, b, c}, Batches: []string{d}},
			{Settings: []string{a + b}, Batches: []string{c, d}},
			{Settings: []string{a, b, c, d}},
			{Batches: []string{a, b, c, d}},
			{Batches: []string{a + b, c + d}},
			{Settings: []string{a, b}, Batches: []string{c + d}},
		}
		for i := range shapes {
			first := shapes[i].Hash()
			if again := shapes[i].Hash(); again != first {
				t.Fatalf("shape %d hashes differently from itself: %s then %s", i, first, again)
			}
			for j := i + 1; j < len(shapes); j++ {
				same := shapes[i].Tool == shapes[j].Tool && shapes[i].Provider == shapes[j].Provider &&
					shapes[i].Model == shapes[j].Model && shapes[i].System == shapes[j].System &&
					slices.Equal(shapes[i].Settings, shapes[j].Settings) && slices.Equal(shapes[i].Batches, shapes[j].Batches)
				if !same && first == shapes[j].Hash() {
					t.Fatalf("two different inputs share a hash:\n %+v\n %+v", shapes[i], shapes[j])
				}
			}
		}
	})
}

func FuzzBatchKeyNeverPanicsAndIsStable(f *testing.F) {
	f.Add("")
	f.Add("a prompt")
	f.Fuzz(func(t *testing.T, s string) {
		k := BatchKey(s)
		if len(k) != 16 || k != BatchKey(s) {
			t.Fatalf("BatchKey(%q) = %q", s, k)
		}
	})
}
