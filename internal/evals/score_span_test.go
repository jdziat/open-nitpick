//go:build eval

package evals

import (
	"testing"

	"github.com/jdziat/open-nitpick/internal/review"
)

// TestAnchorSpanScoring pins the rule that a multi-line anchor is measured from
// its nearest edge.
//
// The go-hardcoded-secret fixture is the case that forced this: the planted
// credential is on client.go:12, and Incumbent reported "client.go:11-12" on
// one run and "client.go:7" on another. Under start-only scoring the first is a
// hit and the second a miss, so an identical, correct finding scored either way
// depending on where the reviewer chose to begin its range.
func TestAnchorSpanScoring(t *testing.T) {
	const defectLine = 12

	cases := []struct {
		name      string
		line, end int
		want      bool
	}{
		{"span ends exactly on the defect", 11, 12, true},
		{"defect inside a wide span", 7, 20, true},
		{"single line on the defect", 12, 0, true},
		{"single line within tolerance", 9, 0, true},
		// The tolerance still applies outside the span, so a region that stops
		// short buys no extra slack at its edges.
		{"span stops well short", 1, 5, false},
		{"single line beyond tolerance", 7, 0, false},
		// A backwards range must not invert the comparison.
		{"end before start is treated as single line", 12, 4, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := review.Finding{Path: "client.go", Line: tc.line, EndLine: tc.end}

			if got := anchorDistance(f, defectLine) <= anchorTolerance; got != tc.want {
				t.Errorf("anchor %d-%d vs defect %d: matched=%v want %v (distance %d, tolerance %d)",
					tc.line, tc.end, defectLine, got, tc.want, anchorDistance(f, defectLine), anchorTolerance)
			}
		})
	}
}

// TestSpanLengthMakesVaguenessVisible: scoring from the nearest edge means a
// wide anchor can earn credit, so the width has to be reportable.
func TestSpanLengthMakesVaguenessVisible(t *testing.T) {
	if got := spanLength(review.Finding{Line: 10}); got != 1 {
		t.Errorf("single-line span = %d, want 1", got)
	}
	if got := spanLength(review.Finding{Line: 10, EndLine: 12}); got != 3 {
		t.Errorf("10-12 span = %d, want 3", got)
	}
	if got := spanLength(review.Finding{Line: 10, EndLine: 4}); got != 1 {
		t.Errorf("backwards span = %d, want 1", got)
	}
}

// TestCRParseLocationKeepsBothEnds guards the parse itself.
func TestCRParseLocationKeepsBothEnds(t *testing.T) {
	cases := map[string]struct {
		path      string
		line, end int
	}{
		"client.go:11-12":   {"client.go", 11, 12},
		"store.go:17":       {"store.go", 17, 0},
		"a/b/c.go:3-9":      {"a/b/c.go", 3, 9},
		"weird:name.go:5-5": {"weird:name.go", 5, 0}, // end == start is not a span
		"backwards.go:12-4": {"backwards.go", 12, 0}, // refused, not inverted
		"nolines.go":        {"nolines.go", 0, 0},
	}

	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			path, line, end := crParseLocation(in)
			if path != want.path || line != want.line || end != want.end {
				t.Errorf("got (%q,%d,%d), want (%q,%d,%d)", path, line, end, want.path, want.line, want.end)
			}
		})
	}
}

// TestAlsoAppliesIsScored is the regression test for a false MISS.
//
// Incumbent reported the SQL injection with its primary anchor on the import
// block -- because the fix it proposed deletes the fmt import -- and named the
// interpolation itself only under "Also applies to: 15-18". The planted defect
// is store.go:17. Reading the primary anchor alone put it 11 lines away and
// recorded Incumbent as having missed a defect it had explicitly located.
func TestAlsoAppliesIsScored(t *testing.T) {
	f := review.Finding{
		Path: "store.go", Line: 3, EndLine: 6,
		AlsoAt: []review.LineSpan{{Line: 15, EndLine: 18}},
	}

	if d := anchorDistance(f, 17); d != 0 {
		t.Errorf("distance to the defect = %d, want 0: line 17 is inside the 15-18 region", d)
	}
	// The primary region still counts for lines near IT.
	if d := anchorDistance(f, 4); d != 0 {
		t.Errorf("distance to line 4 = %d, want 0: it is inside the primary 3-6 region", d)
	}
	// The gap between the two regions is not silently covered.
	if d := anchorDistance(f, 11); d == 0 {
		t.Error("line 11 sits between the regions and must not read as inside one")
	}
	// Two tight regions are not one wide smear.
	if got := spanLength(f); got != 4 {
		t.Errorf("widest region = %d, want 4: 3-6 and 15-18 are both four lines, not a 16-line hull", got)
	}
}

// TestParseAlsoApplies covers the secondary-location line itself.
func TestParseAlsoApplies(t *testing.T) {
	cases := []struct {
		in   string
		path string
		want []review.LineSpan
	}{
		{"Also applies to: 15-18", "store.go", []review.LineSpan{{Line: 15, EndLine: 18}}},
		{"Also applies to: 22", "store.go", []review.LineSpan{{Line: 22}}},
		{"Also applies to: 15-18, 40-42", "store.go",
			[]review.LineSpan{{Line: 15, EndLine: 18}, {Line: 40, EndLine: 42}}},
		{"also applies to: 7-9", "store.go", []review.LineSpan{{Line: 7, EndLine: 9}}},
		// Same file named explicitly is kept; a different file is dropped rather
		// than attached to this finding's path.
		{"Also applies to: store.go:5-8", "store.go", []review.LineSpan{{Line: 5, EndLine: 8}}},
		{"Also applies to: other.go:5-8", "store.go", nil},
		{"fmt.Sprintf inserts name directly into SQL.", "store.go", nil},
		{"", "store.go", nil},
	}

	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := crParseAlsoApplies(tc.in, tc.path)
			if len(got) != len(tc.want) {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("span %d: got %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}
