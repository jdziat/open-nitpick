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

// The pattern reads back only what spendMarker writes. A review body is text
// somebody else may have authored, and a looser pattern would take a
// hand-written "spend:1" as a dollar already spent and shrink the next run's
// ceiling by it.
func TestOnlyTheFormThisToolWritesIsReadAsSpend(t *testing.T) {
	for _, body := range []string{
		"<!-- open-nitpick spend:1 -->",
		"<!-- open-nitpick spend:99 -->",
		"<!-- open-nitpick spend:1.5 -->",
		"<!-- open-nitpick spend:.500000 -->",
	} {
		if v, ok := parseSpend(body); ok {
			t.Errorf("parseSpend(%q) = %v, want it refused", body, v)
		}
	}

	if v, ok := parseSpend(spendMarker(1)); !ok || v != 1 {
		t.Errorf("the marker this tool writes did not read back: %v, %v", v, ok)
	}
}
