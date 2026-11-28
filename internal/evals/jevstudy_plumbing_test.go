//go:build eval

package evals

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nitpick-oss/nitpick/internal/config"
	"github.com/nitpick-oss/nitpick/internal/jev"
	"github.com/nitpick-oss/nitpick/internal/llm"
	"github.com/nitpick-oss/nitpick/internal/review"
)

// fakeJev answers every state with the same scores, so the join and the labels are
// what the test is about and not the decision model.
type fakeJev struct{}

func (fakeJev) Classify(_ context.Context, state string) (jev.Scores, error) {
	if strings.Contains(state, "Invented complaint") {
		return jev.Scores{jev.Bug: 0.05, jev.Noise: 0.95}, nil
	}
	return jev.Scores{jev.Bug: 0.4, jev.Failure: 0.2, jev.Noise: 0.4}, nil
}

// The study joins three things by the finding's path, line and title: the finding
// the engine kept, the state the decision model was shown, and the planted defect
// ScoreRun matched. A mismatch in the join does not fail anything; it drops the
// finding or mislabels it, and the paid run reports a smaller or different sample
// with nothing to say why. This runs the whole path against a scripted reviewer.
func TestTheStudyJoinsAFindingToItsStateAndItsLabel(t *testing.T) {
	t.Setenv("NITPICK_TEST_STUDY_KEY", "test")
	finding := func(line int, title, rationale string) map[string]any {
		return map[string]any{
			"path": "fetch.go", "line": line, "severity": "error", "class": "correctness", "category": "correctness",
			"title": title, "rationale": rationale, "confidence": 0.9, "citation": "resp, _ := http.Get(url)", "suggestion": "",
		}
	}
	reply, _ := json.Marshal(map[string]any{"walkthrough": "w", "findings": []any{
		finding(10, "Ignored error from http.Get", "the error return is discarded, so resp is nil and the deferred close panics"),
		finding(10, "Invented complaint", "this line is fine but the reviewer disagrees about naming"),
	}})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": string(reply)}, "finish_reason": "stop"}},
			"usage":   map[string]int{"prompt_tokens": 2, "completion_tokens": 1, "total_tokens": 3},
		})
	}))
	defer server.Close()
	zero := 0
	spec := config.ModelSpec{Provider: "openai", Model: "gpt-4o", BaseURL: server.URL,
		APIKeyEnv: "NITPICK_TEST_STUDY_KEY", AllowPrivateEndpoint: true, MaxRetries: &zero}

	rec := &studyRecorder{inner: fakeJev{}}
	fixture := goNilDerefFixture()
	result := Run(t.Context(), Model{ID: "scripted"}, fixture, 0, Options{
		Decider:     rec,
		buildClient: func(config.ModelSpec) (*llm.Client, error) { return llm.Build(spec) },
		Tune:        func(c *config.Config) { c.Review.Jev.Threshold = 0.3 },
	})
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	if len(rec.states) == 0 {
		t.Fatalf("the engine never asked the decision model: stages %+v", result.Report.Stages)
	}

	filed := len(result.Report.Findings) + len(overruledFindings(result.Report))
	if len(rec.states) != filed {
		t.Errorf("the decision model was asked %d times for %d findings", len(rec.states), filed)
	}
	if len(overruledFindings(result.Report)) != 1 {
		t.Fatalf("setup: the fake should have withheld the invented finding, withheld %d", len(overruledFindings(result.Report)))
	}

	joined := studyJoin("scripted", fixture, 3, result, rec, "plain", false)
	if len(joined) != filed {
		t.Fatalf("the join kept %d of %d findings", len(joined), filed)
	}
	byTitle := map[string]studyObservation{}
	for _, o := range joined {
		byTitle[o.Title] = o
	}
	plant, invented := byTitle["Ignored error from http.Get"], byTitle["Invented complaint"]
	if !plant.Real || invented.Real {
		t.Errorf("labels: plant real=%v, invented real=%v, want true and false", plant.Real, invented.Real)
	}
	if plant.Model != "scripted" || plant.Fixture != fixture.Name || plant.Run != 3 {
		t.Errorf("identity lost: %+v", plant)
	}
	if math.Abs(plant.Jev["jev"]-0.6) > 1e-9 || math.Abs(invented.Jev["jev"]-0.05) > 1e-9 {
		t.Errorf("each finding must carry the score for its own state: plant %.2f (want 0.60), invented %.2f (want 0.05)", plant.Jev["jev"], invented.Jev["jev"])
	}
	for _, o := range joined {
		var claim string
		for _, line := range strings.Split(o.State, "\n") {
			if rest, ok := strings.CutPrefix(line, "Title: "); ok {
				claim = strings.TrimSpace(rest)
			}
		}
		if claim != o.Title {
			t.Errorf("the state joined to %q is for %q", o.Title, claim)
		}
	}
	if plant.Confidence != 0.9 || plant.Severity != 0.8 {
		t.Errorf("reviewer fields lost: confidence %.2f severity %.2f, want 0.90 and 0.80", plant.Confidence, plant.Severity)
	}

	for _, heading := range []string{"Review comment to check.", "What an investigator found", "Diff of the cited file:\n", "Whole file at the head commit:\n"} {
		if !strings.Contains(rec.states[0], heading) {
			t.Errorf("the state has no %q heading, so the ablations would remove nothing", heading)
		}
	}
	for _, c := range []struct{ name, heading, next string }{
		{"file", "Whole file at the head commit:\n", ""},
		{"diff", "Diff of the cited file:\n", "Whole file at the head commit:"},
	} {
		cut, ok := studyWithout(rec.states[0], c.heading, c.next)
		if !ok || len(cut) >= len(rec.states[0]) {
			t.Errorf("removing the %s did not shorten the state (%v)", c.name, ok)
		}
	}
	if _, ok := studyWithout("no headings here", "Whole file at the head commit:\n", ""); ok {
		t.Error("a state without the section reported that it removed one")
	}
}

// The repository check withholds a finding before the decision model sees it, so such
// a finding has a label and a verdict and no Jev score. Dropping it would leave only
// the findings the check kept, and the check would look as if it had withheld none.
func TestTheStudyKeepsAFindingTheRepositoryCheckWithheld(t *testing.T) {
	// The word the engine writes, which TestTheRepositoryCheckIsNamedByTheWordTheEvalStudyReads pins on
	// its side. Built from the constant below, the fixture could not tell this copy had moved.
	if verifyExpertName != "a repository check" {
		t.Fatalf("verifyExpertName = %q, want the word the engine attributes the check's withheld findings to", verifyExpertName)
	}
	fixture := goNilDerefFixture()
	kept := review.Finding{Path: "fetch.go", Line: 10, Title: "Ignored error from http.Get", Rationale: "the error return is discarded", Severity: "error"}
	refuted := review.Finding{Path: "fetch.go", Line: 10, Title: "Invented complaint", Rationale: "naming", Severity: "warning"}
	conf := 0.7
	refuted.Confidence = &conf
	report := &review.Report{
		Findings:  []review.Finding{kept},
		Overruled: []review.Overruled{{Finding: refuted, Expert: verifyExpertName, Reason: "no caller found"}},
	}
	rec := &studyRecorder{}
	rec.states = []string{"Review comment to check.\nFile: fetch.go:10\nTitle: Ignored error from http.Get\nClaim: x\n"}
	rec.scores = []jev.Scores{{jev.Bug: 0.5, jev.Noise: 0.2}}

	got := studyJoin("m", fixture, 1, RunResult{Report: report}, rec, "verify", true)
	byTitle := map[string]studyObservation{}
	for _, o := range got {
		byTitle[o.Title] = o
	}
	if len(got) != 2 {
		t.Fatalf("joined %d observations, want the kept finding and the refuted one", len(got))
	}
	k, r := byTitle["Ignored error from http.Get"], byTitle["Invented complaint"]
	if k.Verify != "kept" || r.Verify != "refuted" {
		t.Errorf("verdicts: kept finding %q, refuted finding %q", k.Verify, r.Verify)
	}
	if !k.Real || r.Real {
		t.Errorf("labels: kept real=%v, refuted real=%v, want true and false", k.Real, r.Real)
	}
	if _, scored := r.Jev["jev"]; scored || r.State != "" {
		t.Errorf("a finding the decision model never saw carries a score or a state: %+v", r)
	}
	if math.Abs(k.Support["jev"]-0.8) > 1e-9 {
		t.Errorf("support for the kept finding = %.3f, want 1 minus its noise probability, 0.800", k.Support["jev"])
	}
	if r.Confidence != 0.7 || r.Arm != "verify" {
		t.Errorf("reviewer fields or arm lost on the refuted finding: %+v", r)
	}
	// With verification off, a finding is never marked as kept by a check that did not run.
	if off := studyJoin("m", fixture, 1, RunResult{Report: report}, rec, "plain", false); len(off) > 0 && off[0].Verify != "" {
		t.Errorf("verdict %q recorded with verification off", off[0].Verify)
	}
}

// The study's headline numbers come from three small functions, so each is checked
// on a set whose answer can be counted by hand.
func TestTheStudyMetricsAgreeWithAHandCount(t *testing.T) {
	// real:    0.9 0.8 0.4        not real: 0.7 0.3
	// Of the six real/not-real pairs, 0.9 and 0.8 beat both, 0.4 beats 0.3 only: 5 of 6.
	set := []studyScored{{true, 0.9, "a"}, {true, 0.8, "a"}, {true, 0.4, "b"}, {false, 0.7, "a"}, {false, 0.3, "b"}}
	if got := studyAUC(set); math.Abs(got-5.0/6) > 1e-9 {
		t.Errorf("AUC = %.4f, want 5/6", got)
	}
	// A tie counts half.
	if got := studyAUC([]studyScored{{true, 0.5, ""}, {false, 0.5, ""}}); got != 0.5 {
		t.Errorf("a tied pair gave AUC %.2f, want 0.50", got)
	}
	// Nothing to compare is chance, not a perfect or a zero score.
	if got := studyAUC([]studyScored{{true, 0.9, ""}}); got != 0.5 {
		t.Errorf("a set with no not-real findings gave AUC %.2f, want 0.50", got)
	}
	// At 0.5: not-real withheld are 0.3 (one of two); real kept are 0.9 and 0.8 (two of three).
	wh, kept := studyWithheldAt(set, 0.5)
	if math.Abs(wh-0.5) > 1e-9 || math.Abs(kept-2.0/3) > 1e-9 {
		t.Errorf("at 0.5 withheld %.3f and kept %.3f, want 0.500 and 0.667", wh, kept)
	}
	// The boundary is inclusive: a finding exactly at the threshold is kept.
	if _, kept := studyWithheldAt([]studyScored{{true, 0.3, ""}}, 0.3); kept != 1 {
		t.Errorf("a real finding exactly at the threshold was dropped: kept %.2f", kept)
	}
	// A not-real finding exactly at the threshold is kept, so it is not counted as withheld.
	if wh, _ := studyWithheldAt([]studyScored{{false, 0.3, ""}, {false, 0.1, ""}}, 0.3); math.Abs(wh-0.5) > 1e-9 {
		t.Errorf("a not-real finding exactly at the threshold was withheld: withheld share %.2f, want 0.50", wh)
	}
	// 20 real findings scored 0.01..0.20: the highest threshold that keeps 95% is the second
	// lowest score, 0.02, since only one of twenty may fall under it.
	var twenty []studyScored
	for i := 1; i <= 20; i++ {
		twenty = append(twenty, studyScored{true, float64(i) / 100, ""})
	}
	if got := studyThreshold95(twenty); math.Abs(got-0.02) > 1e-9 {
		t.Errorf("threshold keeping 95%% = %.3f, want 0.020", got)
	}
}

// The check ran or it did not, and the log says which. Zero refutations from a check that
// never ran is the same output as zero from one that read every claim and agreed.
func TestTheStudyCountsTheReviewsWhereTheRepositoryCheckRan(t *testing.T) {
	var tally studyVerifyTally
	log := "time=x level=INFO msg=\"verification finished\" confirmed=3 refuted=1 uncertain=2 unchecked=0 withheld=1"
	if !tally.add(log) {
		t.Fatal("the engine's finishing line was not recognised")
	}
	if tally.reviews != 1 || tally.confirmed != 3 || tally.refuted != 1 || tally.uncertain != 2 || tally.withheld != 1 {
		t.Errorf("tally = %+v, want one review with 3 confirmed, 1 refuted, 2 uncertain, 1 withheld", tally)
	}
	if tally.add("time=x level=INFO msg=\"verifying against the repository\" findings=2 of=2") {
		t.Error("a line saying the check started was counted as it finishing")
	}
	if tally.add("") || tally.reviews != 1 {
		t.Errorf("a review with no log counted as one where the check ran: %+v", tally)
	}
}

// An arm that is not configured as named reports nothing wrong: the verify arm with the
// check off is the plain arm under another name, its zero refutations read as a finding,
// and the first run of this study did exactly that.
func TestTheVerifyArmTurnsTheRepositoryCheckOnAndThePlainArmDoesNot(t *testing.T) {
	plain, verify := config.Defaults(), config.Defaults()
	studyTune(false)(plain)
	studyTune(true)(verify)
	if plain.Review.Verify != "" {
		t.Errorf("the plain arm has review.verify %q, want it off", plain.Review.Verify)
	}
	if verify.Review.Verify != config.VerifyNotRefuted {
		t.Errorf("the verify arm has review.verify %q, want %q", verify.Review.Verify, config.VerifyNotRefuted)
	}
	for name, c := range map[string]*config.Config{"plain": plain, "verify": verify} {
		if !c.Review.Jev.Enabled() || c.Review.Jev.Threshold > 1e-6 {
			t.Errorf("the %s arm does not score every finding: jev threshold %v", name, c.Review.Jev.Threshold)
		}
	}
}
