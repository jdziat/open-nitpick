package review

import "testing"

func TestScrubRemovesTheMechanicalTells(t *testing.T) {
	cases := []struct{ in, want string }{
		{"The deferred close panics — resp is nil on error.", "The deferred close panics, resp is nil on error."},
		{"This is actually a problem.", "This is a problem."},
		{"Sure! Here's the answer: it leaks.", "It leaks."},
		{"It leaks. Let me know if you want more detail.", "It leaks."},
		{"Note that the caller is not checked.", "The caller is not checked."},
		{"The flow is parse -> review.", "The flow is parse to review."},
		{"It is very slow and quite fragile.", "It is slow and fragile."},
		{"Range 1990–2000 stays.", "Range 1990–2000 stays."},
		{"Nothing to change here.", "Nothing to change here."},
	}
	for _, tc := range cases {
		got, changed := Scrub(tc.in)
		if got != tc.want {
			t.Errorf("Scrub(%q) = %q, want %q", tc.in, got, tc.want)
		}
		if changed != (tc.in != tc.want) {
			t.Errorf("Scrub(%q) reported changed=%v", tc.in, changed)
		}
	}
}

func TestScrubLeavesCodeAlone(t *testing.T) {
	in := "Use `a — b` as written.\n\n```go\nx := a — b // actually fine\n```\n\nBut the prose — this part — is rewritten."
	got, changed := Scrub(in)
	if !changed {
		t.Fatal("the prose was not scrubbed")
	}
	for _, want := range []string{"`a — b`", "x := a — b // actually fine"} {
		if !contains(got, want) {
			t.Errorf("code was rewritten:\n%s", got)
		}
	}
	if contains(got, "prose — this") {
		t.Errorf("prose was not scrubbed:\n%s", got)
	}
}

func TestScrubFindingsCountsWhatItChanged(t *testing.T) {
	findings := []Finding{
		{Title: "Nil deref — on error", Rationale: "It actually panics."},
		{Title: "Clean", Rationale: "Nothing to do."},
	}
	if n := scrubFindings(findings); n != 2 {
		t.Errorf("changed = %d, want 2", n)
	}
	if findings[0].Title != "Nil deref, on error" || findings[0].Rationale != "It panics." {
		t.Errorf("finding = %+v", findings[0])
	}
	if findings[1].Title != "Clean" {
		t.Error("a clean finding was rewritten")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool { return indexOf(s, sub) >= 0 })()
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// A withheld finding is still published, under "reported, then withheld",
// so its title, rationale and the reason are scrubbed too.
func TestScrubOverruledCoversTheReason(t *testing.T) {
	o := []Overruled{{
		Finding: Finding{Title: "Nil deref — here", Rationale: "It actually panics."},
		Expert:  "correctness",
		Reason:  "Sure! The guard actually covers it.",
	}}
	if n := scrubOverruled(o); n != 3 {
		t.Errorf("changed = %d, want 3", n)
	}
	if o[0].Finding.Title != "Nil deref, here" || o[0].Reason != "The guard covers it." {
		t.Errorf("overruled = %+v", o[0])
	}
}
