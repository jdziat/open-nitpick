package vcs

import "testing"

// The marker is the only durable record of what a run spent, so its two ends
// are tested together: a value written by one run and read by the next.
func TestTheSpendMarkerSurvivesARoundTrip(t *testing.T) {
	body := "A review.\n" + DefaultBotMarker + "\n" + headMarker("abc123") + "\n" + spendMarker(0.4212)

	got, ok := parseSpend(body)
	if !ok {
		t.Fatalf("no spend read back from:\n%s", body)
	}
	if got != 0.4212 {
		t.Errorf("spend = %v, want 0.4212", got)
	}

	// The head marker sits beside it and must still be readable.
	if head, ok := parseHead(body); !ok || head != "abc123" {
		t.Errorf("head = %q, ok = %v", head, ok)
	}
}

// A run that priced nothing writes no marker. A marker reading zero cannot be
// told apart from one written by a run whose estimate failed, and a later run
// would subtract a number nobody computed.
func TestNothingPricedWritesNoSpendMarker(t *testing.T) {
	for _, v := range []float64{0, -1} {
		if got := spendMarker(v); got != "" {
			t.Errorf("spendMarker(%v) = %q, want empty", v, got)
		}
	}

	if _, ok := parseSpend("a review with no marker\n" + DefaultBotMarker); ok {
		t.Error("a body with no spend marker read as one")
	}
	if _, ok := parseSpend("<!-- open-nitpick spend:0.000000 -->"); ok {
		t.Error("a zero marker was read as a spend to subtract")
	}
}
