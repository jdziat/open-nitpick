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
