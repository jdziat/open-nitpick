package review

import (
	"context"
	"testing"

	"github.com/nitpick-oss/nitpick/internal/jev"
	"github.com/nitpick-oss/nitpick/internal/vcs"
)

// The decision model of review.jev is a model call outside llm.Client, so a
// ledger that only wraps clients never sees it. A replay that still asks it
// costs money, sends code to a third party again, and can come back with
// different scores and so different findings.
func TestAReplayDoesNotAskTheDecisionModelAgain(t *testing.T) {
	// Every finding scored one way, then every finding the other way.
	scoreAll := func(s map[string]float64) map[string]jev.Scores {
		out := map[string]jev.Scores{}
		for _, f := range twoFindings() {
			out[f.Title] = s
		}
		return out
	}
	store := fileStore(t)
	first := &fakeDecider{scores: scoreAll(likelyDefect)}
	e := jevEngine(t, &toolLLM{}, "", 0.3, first)
	e.Notary, e.NotarySource = store, "ci"
	live, err := e.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if first.calls() == 0 {
		t.Fatal("setup: the live review never asked the decision model, so the replay below proves nothing")
	}
	if !live.Notary.Complete {
		t.Fatalf("setup: the live review's record is not complete: %+v", live.Notary)
	}

	// A decision model that would now answer the opposite.
	second := &fakeDecider{scores: scoreAll(likelyNoise)}
	again := jevEngine(t, &toolLLM{}, "", 0.3, second)
	again.Notary, again.NotarySource = store, "ci"
	replay, err := again.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	if second.calls() != 0 {
		t.Errorf("the replay asked the decision model %d times", second.calls())
	}
	if replay.Notary.Mode != NotaryReplayed {
		t.Errorf("replay notary = %+v", replay.Notary)
	}
	if len(replay.Findings) != len(live.Findings) || len(replay.Overruled) != len(live.Overruled) {
		t.Errorf("the replay decided differently: %d findings and %d withheld, the review had %d and %d",
			len(replay.Findings), len(replay.Overruled), len(live.Findings), len(live.Overruled))
	}
}
