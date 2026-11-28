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

	joined := studyJoin("scripted", fixture, 3, result, rec)
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
