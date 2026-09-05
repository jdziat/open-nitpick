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

	llms "github.com/nocturnium/llm-go-sdk/v6"
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
	opts, err := OptionsFromEnv()
	if err != nil {
		t.Fatalf("options: %v", err)
	}
	ctx := context.Background()

	// The corpus is named, not just counted. A held-out table and a tuning
	// table were textually identical, so the generalization number could not be
	// told apart from a training score by anyone reading the artifact later.
	t.Logf("corpus: %s", CorpusLabel(opts.Fixtures))
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

	// The cost ledger had NO caller anywhere outside its own tests: nothing built
	// one, nothing called ObserveScore, and nothing printed the table. A whole
	// accounting block, its price table, its staleness marks and its routing
	// bands existed as an artifact no run produced — which is also why three
	// mutations of ObserveScore left the suite green.
	//
	// A missing price table is reported and not fatal. A battery that refuses to
	// run because nobody recaptured a rate spends nothing and measures nothing,
	// and the ledger already says "unknown" per row rather than guessing.
	prices, perr := Prices()
	if perr != nil {
		t.Logf("NO COST ACCOUNTING THIS RUN: %v — the review results below are unaffected", perr)
	}
	ledger := NewCostLedger(prices)

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

				// Every term of the cost reading — the tokens, the detections,
				// the noise, the widest anchor — already sits on the Score, which
				// is what ObserveScore takes. Booking it here is what makes the
				// noise and anchor counts in the cost table the scorer's answer
				// rather than a second derivation free to disagree with the score
				// table printed above it.
				ledger.ObserveScore(score)

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

	printTable(t, opts.Fixtures, summaries)

	// The cost block, under the score table it is read beside. Its columns are
	// computed from the same Scores the table above was, so the two cannot
	// describe different runs.
	if prices != nil {
		for _, note := range ledger.ComparabilityNotes() {
			t.Log(note)
		}
		for _, note := range ledger.OrderingNotes() {
			t.Log(note)
		}
		t.Log(ledger.Table())
	}

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

	// Severity is reported per defect, not only as a total. Reducing severity
	// error means changing the prompt for the finding that got it wrong, and a
	// count says only that one of them did.
	calls := map[string][]SeverityCall{}
	for _, c := range s.Severity.Calls {
		calls[c.Defect.Why] = append(calls[c.Defect.Why], c)
	}

	for _, defect := range f.Defects {
		mark := "MISS"
		if s.Detected[defect.Why] {
			mark = "HIT "
		}
		t.Logf("    [%s] %s:%d %s", mark, defect.Path, defect.Line, defect.Why)

		// One verdict, at our full resolution. A second, banded one was printed
		// beside it for the withdrawn cross-tool columns; it is gone with them.
		for _, c := range calls[defect.Why] {
			t.Logf("           severity %s, planted %s: %s",
				c.Finding.Sev(), c.Defect.WantSeverity, strings.ToUpper(c.Verdict))
		}
	}

	for _, f := range s.Unmatched {
		t.Logf("    [NOISE] %s:%d [%s] %s", f.Path, f.Line, f.Severity, f.Title)
	}
}

// printTable renders the summary a human reads to decide whether the prompt is
// good enough.
func printTable(t *testing.T, corpus []Fixture, summaries []Summary) {
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
	// SEV is accurate/inflated/understated against each fixture's own
	// WantSeverity, summed over the runs. No judge is involved: this battery
	// measures the prompt against ground truth, and severity is part of that
	// ground truth even though nothing outside fixtures.go used to read it.
	//
	// It is graded once per LOCATED defect, so the three numbers add up to the
	// left-hand side of RECALL on the same row. RUNS is printed because NOISE
	// is a sum with no other divisor on the line, and because a row's counts
	// scale with how many times it was measured.
	//
	// A BAND A/I/U column stood beside SEV — the same triple after both
	// severities were coarsened into blocking/medium/low, so that this table and
	// the head-to-head could be read in the same units. Both it and the
	// head-to-head's version are withdrawn; the units they shared were maximised
	// by rating everything blocking. See NoCrossToolSeverityScore.
	b.WriteString(SummaryTableHeader + "\n")
	b.WriteString(strings.Repeat("-", len(SummaryTableHeader)) + "\n")

	for _, s := range summaries {
		recall := "n/a"
		if s.Total > 0 {
			recall = fmt.Sprintf("%d/%d", s.Matched, s.Total)
		}

		// n/a, not "yes", when no run produced a finding: silence is not
		// stability, and yes is the column's best value. See Summary.Stable.
		stable := "n/a"
		if ok, defined := s.Stable(); defined {
			stable = "yes"
			if !ok {
				stable = fmt.Sprintf("NO %v", s.FindingCounts)
			}
		}

		// Rendered by Summary rather than formatted here, because this cell is
		// one of the two renderings of the objective-severity metric and the
		// vocabulary withdrawal has to apply to both. Formatting it inline left
		// the withdrawal in the judged tables only; see Summary.SeverityCell.
		sev := s.SeverityCell()

		// ANCHOR is the widest single region any finding claimed. It is on the
		// row rather than in a log line because RECALL and NOISE are both
		// maximised without it: see CorpusTally.WidestAnchor.
		anchor := "—"
		if s.WidestAnchor > 0 {
			anchor = fmt.Sprintf("%d", s.WidestAnchor)
		}

		// L/DEF is the same anchor measurement summed per LOCATED defect rather
		// than maxed, which is what separates a reviewer that gestured once from
		// one that gestures everywhere — a distinction ANCHOR cannot make. n/a
		// rather than a number when the row located nothing: zero is the best
		// value here and finding nothing has not earned it. See Summary.Spread.
		spread := "n/a"
		if v, ok := s.Spread(); ok {
			spread = fmt.Sprintf("%.2f", v)
		}

		fmt.Fprintf(&b, "%-36s %-25s %-5d %-8s %-10s %-6d %-7s %-6s %-7s %d\n",
			truncate(s.Model, 36), truncate(s.Fixture, 25), s.Runs, recall, sev,
			s.NoiseTotal, anchor, spread, stable, s.Failed)
	}

	t.Log(b.String())

	// What the counts above can and cannot express, derived from the corpus that
	// produced them. RECALL and SEV are printed as counts rather than as
	// percentages for the reason this note states, and the note is here so a
	// reader is told the reason instead of having to notice it.
	t.Log(CorpusResolution(corpus))
	t.Log(RateLegend)

	// The legend belongs under this table too. Its own doc comment said it was
	// printed under every table carrying a severity column, and this one carries
	// SEV A/I/U and printed no legend at all — so one of the three tables met a
	// reader with a severity column and no retraction beside it, while the guard
	// asserting the retraction inspected the const rather than the output.
	t.Log(SeverityColumnLegend)

	// The description that replaced the withdrawn cross-tool severity score.
	// Printed here too, even though this battery runs no foreign reviewer: a
	// reader moving between the two tables should meet the same instrument, and
	// what our own models call each planted level is worth reading on its own.
	vocab := make([]VocabularyRow, 0, len(summaries))
	for _, s := range summaries {
		// A row that located nothing still belongs here once the denominators
		// are on it: it renders every planted level as "(0 of N located)", which
		// is the information the previous filter threw away. Only a row with
		// nothing planted AND nothing said is skipped — a clean fixture, which
		// has no severity to describe in either direction.
		if len(s.SevUsage) == 0 && len(s.SevPlantedLevels) == 0 {
			continue
		}
		vocab = append(vocab, VocabularyRow{
			Name: s.Model + " / " + s.Fixture, Usage: s.SevUsage, Planted: s.SevPlantedLevels,
		})
	}
	if len(vocab) > 0 {
		t.Log(SeverityVocabularyBlock(vocab))
	}
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
