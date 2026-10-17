package converse

import "testing"

func TestOnlyAnExplicitResolveClosesAThread(t *testing.T) {
	cases := map[string]Outcome{
		"resolve":    OutcomeResolve,
		" Resolve ":  OutcomeResolve,
		"keep":       OutcomeKeep,
		"":           OutcomeKeep,
		"resolved":   OutcomeKeep,
		"approve":    OutcomeKeep,
		"resolve it": OutcomeKeep,
	}
	for in, want := range cases {
		if got := readVerdict(verdictJSON{Outcome: in, Reply: "ok"}).Outcome; got != want {
			t.Errorf("outcome %q = %q, want %q", in, got, want)
		}
	}
}

func TestAVerdictReplyIsNeverEmpty(t *testing.T) {
	got := readVerdict(verdictJSON{Outcome: "keep", Reply: "  Still reads past the end.  "})
	if got.Reply != "Still reads past the end." {
		t.Errorf("reply = %q", got.Reply)
	}
}
