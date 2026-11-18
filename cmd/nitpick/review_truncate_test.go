package main

import "testing"

func TestTruncateToHandlesNonPositiveWidth(t *testing.T) {
	for _, n := range []int{0, -3} {
		if got := truncateTo("go,python", n); got != "" {
			t.Fatalf("width %d gave %q, want empty rather than a panic", n, got)
		}
	}
	if got := truncateTo("abcdef", 4); got != "abc…" {
		t.Fatalf("got %q", got)
	}
}
