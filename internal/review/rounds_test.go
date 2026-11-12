package review

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jdziat/open-nitpick/internal/bundle"
	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/practices"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

func roundsEngine(t *testing.T, p vcs.Provider, max int, stop config.Severity) *Engine {
	t.Helper()
	cfg := config.Defaults()
	cfg.Review.MaxRounds = max
	cfg.Review.StopAt = stop
	return &Engine{Config: cfg, Provider: p}
}

func TestTheFourthRunInAThreeRoundSeriesMakesNoModelCall(t *testing.T) {
	model := &scriptedLLM{fallback: mustJSON(t, Result{})}
	provider := &incrementalProvider{
		stubProvider: stubProvider{diff: engineDiff},
		head:         "new",
		prior: &vcs.PriorReview{Head: "old", Rounds: 3, Dismissed: []string{"a", "b"},
			Comments: []vcs.PriorComment{{ID: 1, Path: "app.go", Line: 4, Severity: "warning", Class: "correctness"}}},
		ok: true,
	}

	report, err := newEngine(t, model, provider, func(c *config.Config) { c.Review.MaxRounds = 3 }).
		Review(context.Background(), vcs.Ref{Number: 1})

	if !errors.Is(err, ErrRoundLimit) {
		t.Fatalf("err = %v, want ErrRoundLimit", err)
	}
	if model.calls != 0 {
		t.Errorf("model calls = %d, want 0 past the limit", model.calls)
	}
	if provider.published != nil {
		t.Error("a review was published past the limit, which is one more round on the pull request")
	}
	if report == nil || report.Rounds == nil || !report.Rounds.Limit {
		t.Fatalf("report = %+v, want a report carrying the limit state", report)
	}
	if got := report.Rounds.String(); got != "round 3 of 3: 1 unresolved, 2 declined" {
		t.Errorf("summary line = %q", got)
	}
}

func TestARunUnderTheLimitIsTheNextRound(t *testing.T) {
	p := &spendProvider{prior: &vcs.PriorReview{Rounds: 2}}
	e := roundsEngine(t, p, 3, "")
	report := &Report{}

	if err := e.checkRounds(context.Background(), vcs.Ref{Number: 1}, nil, report); err != nil {
		t.Fatalf("checkRounds: %v", err)
	}
	if report.Rounds.Round != 3 || report.Rounds.Limit {
		t.Errorf("state = %+v, want round 3 and no limit", report.Rounds)
	}
}

// The prior the incremental path reads is nil when review.incremental is off.
// A limit that depended on it would be unset in exactly that configuration and
// look the same as a limit nobody asked for.
func TestTheLimitHoldsWithIncrementalOff(t *testing.T) {
	p := &spendProvider{prior: &vcs.PriorReview{Rounds: 3}}
	e := roundsEngine(t, p, 3, "")
	e.Config.Review.Incremental = false

	err := e.checkRounds(context.Background(), vcs.Ref{Number: 1}, nil, &Report{})
	if !errors.Is(err, ErrRoundLimit) {
		t.Fatalf("err = %v, want ErrRoundLimit", err)
	}
	if !p.asked {
		t.Error("the forge was never asked how many rounds there were")
	}
}

func TestAnUncountableSeriesSaysSoAndNeverSettles(t *testing.T) {
	e := roundsEngine(t, &spendProvider{err: context.DeadlineExceeded}, 3, config.SeverityWarning)
	report := &Report{}
	if err := e.checkRounds(context.Background(), vcs.Ref{Number: 1}, nil, report); err != nil {
		t.Fatalf("an unreadable series stopped the run: %v", err)
	}
	if report.Rounds == nil || report.Rounds.Known || report.Rounds.Local {
		t.Fatalf("state = %+v, want an unknown series", report.Rounds)
	}
	if !strings.Contains(report.Rounds.String(), "not enforced") {
		t.Errorf("summary line = %q, want it to say the limit was not enforced", report.Rounds.String())
	}
	if report.Settled(config.SeverityWarning) {
		t.Error("an uncountable series was called settled")
	}
}

// A working tree has no pull request, so nothing can stand from an earlier
// round and stop_at judges the run alone. That is the loop issue #212 was
// filed about: an agent re-running review on its own checkout.
func TestALocalRunSettlesOnItsOwnFindings(t *testing.T) {
	// A provider that could read a prior review, asked about no pull request:
	// a dry run over a working tree looks like this.
	e := roundsEngine(t, &spendProvider{err: errors.New("no pull request")}, 3, config.SeverityWarning)
	report := &Report{}
	if err := e.checkRounds(context.Background(), vcs.Ref{}, nil, report); err != nil {
		t.Fatalf("checkRounds: %v", err)
	}
	if report.Rounds == nil || !report.Rounds.Local {
		t.Fatalf("state = %+v, want a local run", report.Rounds)
	}
	if !strings.Contains(report.Rounds.String(), "not counted") {
		t.Errorf("summary line = %q, want it to say rounds are not counted", report.Rounds.String())
	}
	report.Findings = []Finding{{Severity: "info"}}
	if !report.Settled(config.SeverityWarning) {
		t.Error("a local run with only infos was not settled")
	}
	report.Findings = append(report.Findings, Finding{Severity: "error"})
	if report.Settled(config.SeverityWarning) {
		t.Error("a local run with an error was settled")
	}
}

func TestNeitherKeySetLeavesRoundsAloneAndAsksNothing(t *testing.T) {
	p := &spendProvider{prior: &vcs.PriorReview{Rounds: 9}}
	report := &Report{}
	if err := roundsEngine(t, p, 0, "").checkRounds(context.Background(), vcs.Ref{Number: 1}, nil, report); err != nil {
		t.Fatal(err)
	}
	if report.Rounds != nil || p.asked {
		t.Errorf("rounds = %+v, asked = %v, want neither", report.Rounds, p.asked)
	}
}

func TestSettledNeedsNothingAtOrAboveTheStopSeverityToStand(t *testing.T) {
	known := func(unresolved ...vcs.PriorComment) *RoundState {
		return &RoundState{Known: true, Round: 2, Unresolved: unresolved}
	}
	info := Finding{Severity: "info"}
	warn := Finding{Severity: "warning"}
	cases := []struct {
		name string
		r    Report
		want bool
	}{
		{"only infos remain", Report{Rounds: known(), Findings: []Finding{info}}, true},
		{"a warning in this run", Report{Rounds: known(), Findings: []Finding{info, warn}}, false},
		{"a warning withheld as already reported", Report{Rounds: known(), AlreadyReported: []Finding{warn}}, false},
		{"an unresolved info thread", Report{Rounds: known(vcs.PriorComment{ID: 1, Severity: "info"})}, true},
		{"an unresolved warning thread", Report{Rounds: known(vcs.PriorComment{ID: 1, Severity: "warning"})}, false},
		{"an unresolved thread from before markers had severity", Report{Rounds: known(vcs.PriorComment{ID: 1})}, false},
		{"an unresolved thread with an unreadable severity", Report{Rounds: known(vcs.PriorComment{ID: 1, Severity: "none"})}, false},
		{"that warning thread closed by this run", Report{
			Rounds: known(vcs.PriorComment{ID: 1, Severity: "warning"}), Superseded: []vcs.PriorComment{{ID: 1}}}, true},
		{"a blocking practices violation with only infos left", Report{Rounds: known(), Findings: []Finding{info},
			Practices: &practices.Report{Profile: "engineering", Checks: []practices.Check{{ID: "commits", State: practices.Completed,
				Findings: []practices.Finding{{Title: "invalid subject", Blocking: true}}}}}}, false},
		{"a run that left a file unread", Report{Rounds: known(), Incomplete: []string{"big.go"}, Findings: []Finding{info}}, false},
		{"a run whose style stage failed", Report{Rounds: known(), Stages: []StageStatus{{Stage: "style", Reason: "timeout"}}, Findings: []Finding{info}}, false},
		{"a run whose triage alone failed", Report{Rounds: known(), Stages: []StageStatus{{Stage: "triage", Reason: "rate-limited"}}, Findings: []Finding{info}}, true},
		{"a bounded fast review", Report{Rounds: known(), FastReview: true, Findings: []Finding{info}}, false},
		{"the series could not be counted", Report{Rounds: &RoundState{}}, false},
		{"no rounds at all", Report{}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.r.Settled(config.SeverityWarning); got != c.want {
				t.Errorf("Settled = %v, want %v", got, c.want)
			}
		})
	}
	if (&Report{Rounds: known()}).Settled("") {
		t.Error("an empty stop severity settled the series")
	}
}

// A change that edits .nitpick.yaml is reviewed under the base policy, and
// stop_at decides whether CI goes green, so the change may not set it.
func TestAChangeCannotSettleItsOwnSeriesByWritingStopAt(t *testing.T) {
	engine := hostileEngine(t, findingModel(t), &stubProvider{diff: configEditDiff}, func(c *config.Config) {
		c.Review.StopAt = config.SeverityCritical
	})

	report, err := engine.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if report.Rounds != nil {
		t.Errorf("rounds = %+v, want none: the base policy sets neither key and the change's stop_at must not count", report.Rounds)
	}
}

// The operator's flags hold when the change's configuration is set aside; a
// flag written into the replaced configuration would be lost with it.
func TestTheOperatorsRoundFlagsSurviveAPolicyReplacement(t *testing.T) {
	engine := hostileEngine(t, findingModel(t), &stubProvider{diff: configEditDiff}, nil)
	three := 3
	engine.Rounds = RoundsOverride{MaxRounds: &three, StopAt: config.SeverityWarning}

	report, err := engine.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if report.Rounds == nil || report.Rounds.Max != 3 || report.Rounds.StopAt != config.SeverityWarning {
		t.Errorf("rounds = %+v, want the operator's max 3 and stop-at warning", report.Rounds)
	}
}

// A published thread has to say its severity, or every later run reads it as
// unknown and the series can never settle. A finished run's review has to say
// it was a round, or the limit never fires.
func TestAPublishedReviewCarriesSeverityAndItsRound(t *testing.T) {
	model := findingModel(t)
	provider := &stubProvider{diff: engineDiff}

	if _, err := newEngine(t, model, provider, nil).Review(context.Background(), vcs.Ref{}); err != nil {
		t.Fatalf("Review: %v", err)
	}
	if provider.published == nil || len(provider.published.Comments) == 0 {
		t.Fatal("nothing was published")
	}
	if got := provider.published.Comments[0].Severity; !config.Severity(got).IsFinding() {
		t.Errorf("comment severity = %q, want the finding's", got)
	}
	if !provider.published.Round {
		t.Error("a finished run's review is not marked as a round")
	}
}

func TestAFastReviewIsNotARound(t *testing.T) {
	if Render(&Report{FastReview: true}, nil, config.Defaults()).Round {
		t.Error("a fast review was marked as a round")
	}
}

// Style batches finish in whatever order they finish. The findings reach
// triage's prompt in the order analyzeStyle returns them, so it has to sort,
// or a pedantic run sends triage a different prompt each time.
func TestStyleFindingsReachTriageInAStableOrder(t *testing.T) {
	a, b := file("a.go", 3, 0), file("b.go", 3, 0)
	plan := &bundle.Plan{Batches: []bundle.Batch{
		{Entries: []bundle.Entry{entry(a, 10)}, Tokens: 10},
		{Entries: []bundle.Entry{entry(b, 10)}, Tokens: 10},
	}}
	finding := func(path string) string {
		return mustJSON(t, Result{Findings: []Finding{{Path: path, Line: 1, Severity: "info", Class: "style", Title: "naming in " + path}}})
	}

	// Which batch's call arrives first is up to the scheduler, and the one that
	// does is held until the other has finished, so across the repetitions each
	// path finishes last about half the time.
	for i := 0; i < 40; i++ {
		model := &scriptedLLM{byPrompt: map[string]string{"a.go": finding("a.go"), "b.go": finding("b.go")}}
		release := make(chan struct{})
		var arrived atomic.Int32
		model.onCall = func() {
			if arrived.Add(1) == 1 {
				<-release
				time.Sleep(20 * time.Millisecond)
				return
			}
			close(release)
		}
		e := newEngine(t, model, &stubProvider{}, nil)
		got, err := e.analyzeStyle(context.Background(), &vcs.PullRequest{}, plan, make(chan struct{}, 2))
		if err != nil {
			t.Fatalf("analyzeStyle: %v", err)
		}
		if len(got) != 2 || got[0].Path != "a.go" || got[1].Path != "b.go" {
			t.Fatalf("run %d: paths = %v, want a.go then b.go whatever order the batches finished in", i, paths(got))
		}
	}
}

func paths(fs []Finding) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.Path
	}
	return out
}

// -stop-at none lets one run turn off a configured stop_at; the series is not
// settled by a threshold that was switched off.
func TestStopAtNoneTurnsAConfiguredThresholdOffForOneRun(t *testing.T) {
	provider := &spendProvider{prior: &vcs.PriorReview{Rounds: 1}}
	e := roundsEngine(t, provider, 0, config.SeverityWarning)
	e.Rounds = RoundsOverride{StopAt: config.SeverityNone}
	report := &Report{Findings: []Finding{{Severity: "info"}}}
	if err := e.checkRounds(context.Background(), vcs.Ref{Number: 1}, nil, report); err != nil {
		t.Fatal(err)
	}
	if report.Rounds != nil || provider.asked {
		t.Errorf("rounds = %+v, asked = %v: with no limit and no threshold in force there is no series to track", report.Rounds, provider.asked)
	}
	if report.Settled(config.SeverityNone) {
		t.Error("a series was settled by a threshold the run turned off")
	}
}

// A provider that answers PriorReview with no value and no error is saying
// nothing came before, which makes this the first round and a countable series.
func TestAProviderReportingNoPriorReviewIsTheFirstRound(t *testing.T) {
	e := roundsEngine(t, &spendProvider{prior: nil}, 3, config.SeverityWarning)
	report := &Report{}
	if err := e.checkRounds(context.Background(), vcs.Ref{Number: 1}, nil, report); err != nil {
		t.Fatal(err)
	}
	if report.Rounds == nil || !report.Rounds.Known || report.Rounds.Round != 1 {
		t.Errorf("state = %+v, want a known series at round 1", report.Rounds)
	}
}
