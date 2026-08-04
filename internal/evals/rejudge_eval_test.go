//go:build eval

package evals

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
)

// TestRejudgeDump re-judges findings that were already collected, with a
// different judge, and prints the two rankings side by side.
//
// It runs no review. Every finding it submits came out of the dump named by
// NITPICK_EVAL_REJUDGE_DUMP, in the position it was recorded in, so the ONLY
// difference between the recorded verdicts and the new ones is which model was
// asked. That is the point: the published ranking rests on an OpenAI judge
// scoring three OpenAI contenders, and re-running the reviews under a different
// judge would change the findings and the judge together, leaving the two
// inseparable.
//
//	make rejudge REJUDGE=/tmp/findings.jsonl JUDGE=anthropic/claude-opus-5
//
// Pointing it at the SAME judge id is not a mistake, it is the other
// measurement: identical input judged twice by one model is that model's own
// variance, which is the noise floor any vendor comparison has to clear.
func TestRejudgeDump(t *testing.T) {
	path := strings.TrimSpace(os.Getenv(EnvRejudgeDump))
	if path == "" {
		t.Skipf("set %s to a dump file written by %s; this test re-judges recorded findings and runs no review",
			EnvRejudgeDump, EnvDump)
	}

	// Refusing to read the file another run is writing. NewDump truncates on
	// open, and the environment that produced a dump is usually still exported
	// in the shell that re-judges it, so this collision is the expected
	// accident rather than an exotic one. Re-judging a half-written file
	// silently measures whatever had been flushed.
	if writing := strings.TrimSpace(os.Getenv(EnvDump)); writing != "" {
		in, _ := filepath.Abs(path)
		out, _ := filepath.Abs(writing)
		if in == out {
			t.Fatalf("%s and %s both name %s: the dump writer truncates on open, so this would "+
				"re-judge a file being emptied. Point %s at a different path, or unset it.",
				EnvRejudgeDump, EnvDump, path, EnvDump)
		}
	}

	records, err := ReadDump(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if len(records) == 0 {
		t.Fatalf("%s is empty; there is nothing recorded to re-judge", path)
	}

	groups, warnings, err := GroupDump(records)
	if err != nil {
		t.Fatalf("reconstruct judged reviews: %v", err)
	}

	judge, err := NewJudge(strings.TrimSpace(os.Getenv(EnvJudgeModel)))
	if err != nil {
		t.Fatalf("build judge: %v", err)
	}

	baseline := strings.TrimSpace(os.Getenv(EnvBaselineJudge))
	if baseline == "" {
		baseline = DefaultJudgeModel
	}

	t.Logf("read %d record(s) from %s, reconstructed into %d judged review(s)", len(records), path, len(groups))
	t.Logf("baseline judge %s (asserted via %s), new judge %s", baseline, EnvBaselineJudge, judge.Model())
	for _, w := range warnings {
		t.Logf("WARNING: %s", w)
	}

	// The persona is not recorded in the dump, so the default is the only
	// honest choice — and it is the correct one for every dump the model
	// benchmark and the Incumbent benchmark produce, both of which hold the
	// persona at DefaultPersona. RejudgeReport says so when a variant appears.
	outcomes := Rejudge(context.Background(), judge, config.DefaultPersona(), groups, evalConcurrency)

	t.Log(RejudgeReport(baseline, judge.Model(), outcomes, warnings))

	var judged, recorded, produced int
	for _, o := range outcomes {
		if o.Err != nil || o.Result == nil {
			continue
		}
		judged++
		recorded += len(o.Group.Baseline)
		produced += len(o.Result.Verdicts)
	}

	if judged == 0 {
		t.Fatalf("the new judge assessed none of the %d reconstructed review(s); this is not a "+
			"comparison of two judges, it is one judge and an outage", len(groups))
	}
	if recorded > 0 && produced == 0 {
		t.Errorf("the new judge returned 0 verdicts against %d recorded ones: every agreement rate "+
			"and both precision columns are computed over an empty intersection, so the table "+
			"below is not a result", recorded)
	}
}
