//go:build eval

// These tests call real models and cost real money, so they are behind the
// `eval` build tag and excluded from `go test ./...`.
//
//	make eval                            # default matrix
//	make eval MODELS=openai/gpt-4o-mini  # one model
//	make eval RUNS=3                     # measure run-to-run stability
//	make eval FIXTURES=go-nil-deref      # one fixture
package evals

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"

	llms "github.com/nocturnium/llm-go-sdk"
)

// evalConcurrency bounds in-flight reviews. Held well below any provider rate
// limit: the matrix is wide, and a 429 storm would read as model weakness.
const evalConcurrency = 6

// TestMain loads .env so the credential does not have to be exported by hand.
func TestMain(m *testing.M) {
	// Ignore the error: the key may legitimately come from the environment.
	_ = LoadDotEnv("../../.env")

	// Either variable, because the harness now builds its client through the
	// shipped openrouter provider and that provider accepts both. Demanding
	// only OPENROUTER_API_KEY here would refuse to start for an operator whose
	// review runs work fine.
	if strings.TrimSpace(os.Getenv(EnvAPIKey)) == "" && strings.TrimSpace(os.Getenv(llms.EnvLLMAPIKey)) == "" {
		fmt.Fprintf(os.Stderr, "evals: neither %s nor %s is set; put one in .env or export it\n",
			EnvAPIKey, llms.EnvLLMAPIKey)
		os.Exit(1)
	}

	os.Exit(m.Run())
}

// TestPrompts is the battery. It reviews every fixture with every model and
// reports three separate things, which must not be conflated:
//
//  1. INVARIANTS — properties open-nitpick must uphold no matter how a model
//     behaves (valid severities, placeable anchors, no corrupting suggestion).
//     A breach is a bug in this repo and fails the test.
//  2. RECALL — whether the prompt actually finds planted bugs. Reported always;
//     fails only when a model finds nothing across the entire corpus, which
//     means the prompt or the plumbing is broken rather than merely weak.
//  3. NOISE — findings explaining no planted defect. Reported, because the
//     acceptable level is a judgment call about this specific corpus.
func TestPrompts(t *testing.T) {
	opts := OptionsFromEnv()
	ctx := context.Background()

	t.Logf("models=%d fixtures=%d runs=%d", len(opts.Models), len(opts.Fixtures), opts.Runs)

	// Reviews are independent, so they run concurrently. Sequentially this
	// matrix takes longer than any sane test timeout: 12 models x 8 fixtures,
	// with the slowest model at ~130s per fixture, is over an hour of mostly
	// waiting on the network.
	type job struct {
		model   Model
		fixture Fixture
	}

	var jobs []job
	for _, m := range opts.Models {
		for _, f := range opts.Fixtures {
			jobs = append(jobs, job{m, f})
		}
	}

	var (
		mu        sync.Mutex
		summaries []Summary
		wg        sync.WaitGroup
		sem       = make(chan struct{}, evalConcurrency)
	)

	for _, j := range jobs {
		wg.Add(1)

		go func(j job) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			var scores []Score

			for run := 1; run <= opts.Runs; run++ {
				result := Run(ctx, j.model, j.fixture, run, opts)

				if err := CaptureResponses(opts.CaptureDir, j.model.ID, j.fixture.Name, run, result.Responses); err != nil {
					t.Errorf("capture responses: %v", err)
				}

				score := ScoreRun(result, j.fixture)
				scores = append(scores, score)

				mu.Lock()
				logRun(t, j.model.ID, j.fixture, score)

				for _, v := range score.Violations {
					if strings.HasPrefix(v, "review failed:") {
						// A provider error is reported, not asserted: an
						// upstream outage should not read as our bug.
						t.Logf("  PROVIDER ERROR [%s/%s]: %s", j.model.ID, j.fixture.Name, v)
						continue
					}
					t.Errorf("  INVARIANT VIOLATED [%s/%s]: %s", j.model.ID, j.fixture.Name, v)
				}
				mu.Unlock()
			}

			mu.Lock()
			summaries = append(summaries, Summarize(j.model.ID, j.fixture.Name, scores))
			mu.Unlock()
		}(j)
	}

	wg.Wait()

	printTable(t, summaries)
	assertCorpusRecall(t, summaries)
}

// logRun prints what one review actually produced.
func logRun(t *testing.T, model string, f Fixture, s Score) {
	t.Helper()

	if s.Err != nil {
		t.Logf("[%s/%s] run %d: FAILED after %s: %v", model, f.Name, s.Run, s.Duration.Round(1e8), s.Err)
		return
	}

	anchor := ""
	if s.WidestAnchor > 1 {
		// Only when it is not the ordinary single line, so the common case stays
		// quiet and a reviewer gesturing at a region stands out.
		anchor = fmt.Sprintf(", widest anchor %d lines", s.WidestAnchor)
	}

	t.Logf("[%s/%s] run %d: %d finding(s) in %s — detected %d/%d planted, %d unexplained%s",
		model, f.Name, s.Run, len(s.Findings()), s.Duration.Round(1e8), s.Matched, s.Total, len(s.Unmatched), anchor)

	for _, defect := range f.Defects {
		mark := "MISS"
		if s.Detected[defect.Why] {
			mark = "HIT "
		}
		t.Logf("    [%s] %s:%d %s", mark, defect.Path, defect.Line, defect.Why)
	}

	for _, f := range s.Unmatched {
		t.Logf("    [NOISE] %s:%d [%s] %s", f.Path, f.Line, f.Severity, f.Title)
	}
}

// printTable renders the summary a human reads to decide whether the prompt is
// good enough.
func printTable(t *testing.T, summaries []Summary) {
	t.Helper()

	if len(summaries) == 0 {
		return
	}

	sort.Slice(summaries, func(i, j int) bool {
		if summaries[i].Model != summaries[j].Model {
			return summaries[i].Model < summaries[j].Model
		}
		return summaries[i].Fixture < summaries[j].Fixture
	})

	var b strings.Builder
	b.WriteString("\n")
	b.WriteString("MODEL                                FIXTURE                   RECALL   NOISE  STABLE  FAILED\n")
	b.WriteString("-------------------------------------------------------------------------------------------\n")

	for _, s := range summaries {
		recall := "n/a"
		if s.Total > 0 {
			recall = fmt.Sprintf("%d/%d", s.Matched, s.Total)
		}

		stable := "yes"
		if !s.Stable() {
			stable = fmt.Sprintf("NO %v", s.FindingCounts)
		}

		fmt.Fprintf(&b, "%-36s %-25s %-8s %-6d %-7s %d\n",
			truncate(s.Model, 36), truncate(s.Fixture, 25), recall, s.NoiseTotal, stable, s.Failed)
	}

	t.Log(b.String())
}

// assertCorpusRecall fails a model that found nothing anywhere.
//
// Per-fixture recall is deliberately NOT asserted: models disagree about
// borderline findings and a hard threshold would make this suite flaky for no
// benefit. Finding zero planted defects across every fixture is different — it
// means the prompt, the schema, or the plumbing is broken.
func assertCorpusRecall(t *testing.T, summaries []Summary) {
	t.Helper()

	type totals struct{ matched, planted, failed, runs int }
	byModel := map[string]*totals{}

	for _, s := range summaries {
		agg, ok := byModel[s.Model]
		if !ok {
			agg = &totals{}
			byModel[s.Model] = agg
		}
		agg.matched += s.Matched
		agg.planted += s.Total
		agg.failed += s.Failed
		agg.runs += s.Runs
	}

	models := make([]string, 0, len(byModel))
	for m := range byModel {
		models = append(models, m)
	}
	sort.Strings(models)

	for _, model := range models {
		agg := byModel[model]

		if agg.failed == agg.runs && agg.runs > 0 {
			t.Errorf("%s: every run failed; the model or endpoint is unusable", model)
			continue
		}
		if agg.planted > 0 && agg.matched == 0 {
			t.Errorf("%s: found 0 of %d planted defects across the whole corpus — "+
				"the prompt or structured output is broken, not merely weak", model, agg.planted)
			continue
		}

		t.Logf("%s: detected %d/%d planted defects overall", model, agg.matched, agg.planted)
	}
}
