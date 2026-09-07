package linters

// The gate an analyzer severity has to be able to reach, exercised through the
// review path rather than against mapSeverity.
//
// mapSeverity is one expression away from the behaviour that matters, and the
// defect these tests exist for was invisible from there: the function returned a
// perfectly reasonable severity, and the hole was that review.fail_on accepts a
// level the function could not produce. Only the composition, analyzer output,
// mapping, publication policy, exit gate, can be wrong in that way, so that is
// what is asserted. The analyzer's JSON goes in at one end and a CI verdict
// comes out at the other; the only thing stubbed is process execution.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"

	llms "github.com/nocturnium/llm-go-sdk/v6"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/llm"
	"github.com/jdziat/open-nitpick/internal/review"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// gateDiff adds three lines to app.go, so a finding on line 2 is commentable.
const gateDiff = `diff --git a/app.go b/app.go
--- a/app.go
+++ b/app.go
@@ -0,0 +1,3 @@
+package app
+
+func F() { exec.Command(userInput).Run() }
`

// semgrepCritical is a real semgrep report shape, severity spelled exactly as
// semgrep spells it.
const semgrepCritical = `{"results":[{"check_id":"go.lang.security.audit.dangerous-exec-command",` +
	`"path":"app.go","start":{"line":2},` +
	`"extra":{"message":"Command built from user input","severity":"CRITICAL"}}]}`

// scriptedTriage is a triage model with two modes, and choosing the wrong one
// makes a test decorative. With rerate empty it echoes, reading each severity
// out of the rendered prompt, so the analyzer's level stays load-bearing to
// the gate. With rerate set it re-rates every finding to that level, the
// ordinary case, since triage.md rule 3 tells the pass to raise anything whose
// blast radius is larger than the original reviewer could see. A claim that a
// policy caps what a run acts on has to be asserted against the second mode.
// retitle rewords findings, changing review.Finding.Key(), the one case the
// ceiling cannot describe.
type scriptedTriage struct {
	mu sync.Mutex

	rerate  string
	retitle string

	// rendered is every triage prompt seen, so a test can assert on the level
	// the pipeline put in front of the model.
	rendered []string
}

// triageLine matches renderForTriage's "1. [severity] path:line, title".
var triageLine = regexp.MustCompile(`(?m)^\d+\. \[([^\]]+)\] ([^:\n]+):(\d+) — (.+)$`)

func (e *scriptedTriage) GenerateContent(_ context.Context, msgs []llms.Message, _ ...llms.CallOption) (*llms.Response, error) {
	var joined strings.Builder
	for _, m := range msgs {
		joined.WriteString(m.Content)
	}
	text := joined.String()

	if !strings.Contains(text, "triaging findings") {
		// The review pass. Every finding in these tests comes from the
		// analyzer, so the model contributes none.
		return &llms.Response{Content: `{"findings":[]}`}, nil
	}

	e.mu.Lock()
	e.rendered = append(e.rendered, text)
	e.mu.Unlock()

	out := review.Result{Summary: "Reviewed."}
	for _, m := range triageLine.FindAllStringSubmatch(text, -1) {
		line, err := strconv.Atoi(m[3])
		if err != nil {
			return nil, fmt.Errorf("unparseable rendered line %q: %w", m[3], err)
		}

		severity, title := m[1], m[4]
		if e.rerate != "" {
			severity = e.rerate
		}
		if e.retitle != "" {
			title = e.retitle
		}

		out.Findings = append(out.Findings, review.Finding{
			Path: m[2], Line: line, Severity: severity, Title: title,
			Category: "lint", Class: string(config.ClassSecurity),
		})
	}

	body, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	return &llms.Response{Content: string(body)}, nil
}

func (e *scriptedTriage) Stream(context.Context, []llms.Message, ...llms.CallOption) (<-chan llms.StreamChunk, error) {
	return nil, errors.New("not supported")
}

func (e *scriptedTriage) Provider() llms.Provider { return "scripted" }
func (e *scriptedTriage) Model() string           { return "scripted" }

func (e *scriptedTriage) prompts() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.rendered...)
}

// gateProvider serves one diff and swallows the published review.
type gateProvider struct{}

func (gateProvider) Name() string { return "stub" }

func (gateProvider) PullRequest(context.Context, vcs.Ref) (*vcs.PullRequest, error) {
	return &vcs.PullRequest{Number: 1, Title: "Run a command"}, nil
}

func (gateProvider) Diff(context.Context, vcs.Ref) ([]byte, error) { return []byte(gateDiff), nil }

func (gateProvider) FileContent(context.Context, vcs.Ref, string) ([]byte, error) {
	return nil, vcs.ErrNotFound
}

func (gateProvider) PublishReview(context.Context, vcs.Ref, vcs.Review) error { return nil }

// reviewSemgrepReport runs a full review whose only reporter is semgrep, from
// the analyzer's own JSON, with an echoing triage.
//
// The runner is stubbed at the process boundary and nowhere else: semgrep's
// output is parsed by semgrep's own parser, mapped by mapSeverity, normalized by
// the real Set, and published by the real engine.
func reviewSemgrepReport(t *testing.T, out string, tune func(*config.Config)) (*review.Report, *scriptedTriage) {
	t.Helper()
	return reviewSemgrepReportWith(t, out, &scriptedTriage{}, tune)
}

// reviewSemgrepReportWith is the same run under a caller-chosen triage double.
func reviewSemgrepReportWith(t *testing.T, out string, model *scriptedTriage, tune func(*config.Config)) (*review.Report, *scriptedTriage) {
	t.Helper()

	found, err := (&semgrep{}).findings([]byte(out), 0)
	if err != nil {
		t.Fatalf("parse semgrep output: %v", err)
	}
	if len(found) == 0 {
		t.Fatal("the analyzer report produced no findings; the test would prove nothing")
	}

	return reviewLinterFindings(t, "semgrep", found, model, tune), model
}

// reviewLinterFindings runs the real engine over findings an analyzer's own
// parser produced, stubbing only process execution and the forge.
func reviewLinterFindings(t *testing.T, runner string, found []Finding, model *scriptedTriage, tune func(*config.Config)) *review.Report {
	t.Helper()

	cfg := baseConfig()
	if tune != nil {
		tune(cfg)
	}

	set := New(".", cfg, nil)
	set.runners = []Runner{&fakeRunner{name: runner, detected: true, findings: found}}

	client := llm.NewClientForTest(model, cfg.Models.Default)

	engine := &review.Engine{
		Config:   cfg,
		Roles:    &llm.Roles{Review: client, Triage: client},
		Provider: gateProvider{},
		Linters:  func(*config.Config) review.LinterRunner { return set },
	}

	report, err := engine.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	return report
}

// TestFailOnCriticalGatesOnAnAnalyzerCritical is the regression test for a
// production policy hole.
//
// THE BUG: mapSeverity's codomain excluded critical, because "CRITICAL" folded
// onto error alongside "HIGH". review.fail_on accepts "critical", so a
// repository configured `fail_on: critical` got ZERO gating from semgrep, the
// only one of the four analyzers whose own scale HAS a critical, and from any
// golangci-lint whose severity settings name one. The build went green on a
// finding the analyzer itself called critical, which is the one case that
// configuration exists to stop, and nothing reported the gap.
//
// It runs from semgrep's report to the exit verdict because that is the only
// level the bug was visible at. Reverting mapSeverity publishes this finding at
// error, and error does not satisfy a critical gate, so Failed goes false here.
func TestFailOnCriticalGatesOnAnAnalyzerCritical(t *testing.T) {
	report, model := reviewSemgrepReport(t, semgrepCritical, func(c *config.Config) {
		c.Review.FailOn = config.SeverityCritical
	})

	if len(report.Findings) != 1 {
		t.Fatalf("findings = %+v, want the one semgrep reported", report.Findings)
	}

	f := report.Findings[0]
	if f.Severity != string(config.SeverityCritical) {
		t.Errorf("published at %q, want critical: semgrep printed CRITICAL and nothing in this "+
			"configuration asked for it to be reduced", f.Severity)
	}
	if !report.Failed(config.SeverityCritical) {
		t.Errorf("fail_on: critical did not fail on a finding semgrep called critical "+
			"(published %q). A repository configured this way is gating on nothing its "+
			"analyzers can produce", f.Severity)
	}

	// The whole chain has to carry it, not just the last hop: a triage model
	// shown "[error]" cannot echo a critical back.
	prompts := model.prompts()
	if len(prompts) != 1 {
		t.Fatalf("triage prompts = %d, want 1", len(prompts))
	}
	if !strings.Contains(prompts[0], "[critical]") {
		t.Errorf("triage was shown a level other than critical:\n%s", prompts[0])
	}

	// Fidelity is what makes the gate legitimate; a level we invented would
	// not be worth failing a build over.
	if f.RawSeverity != "CRITICAL" {
		t.Errorf("RawSeverity = %q, want %q: the analyzer's own word must survive the mapping",
			f.RawSeverity, "CRITICAL")
	}
	if !f.SeverityTranslated {
		t.Error("a linter finding must record that the level is read on OUR scale. semgrep's " +
			"CRITICAL is a judgement inside semgrep's vocabulary, and a matching spelling is " +
			"not a matching scale")
	}
}

// TestMaxSeverityHoldsAgainstATriageThatRaises pins the other half of the
// decision, against the pass that used to undo it.
//
// Folding CRITICAL in mapSeverity answered "may a deterministic tool fail this
// build?" for every repository at once, permanently and silently. The answer
// belongs to the operator, so it is configuration.
//
// THE BUG: moving the reduction to policy time put it in linters' normalize,
// which runs before triage. Triage is told to raise severities and the expert
// pass revises in both directions, and neither reapplied the ceiling, so an
// operator who wrote max_severity: warning had capped what triage was SHOWN and
// nothing else, and a re-rated semgrep finding still failed a critical gate. The
// first version of this test could not see that: it used the echoing double, and
// an echo can only return the level the ceiling already produced, so it passed
// against the defect. The triage here re-rates to critical, which is exactly
// what the shipped prompt permits.
func TestMaxSeverityHoldsAgainstATriageThatRaises(t *testing.T) {
	model := &scriptedTriage{rerate: string(config.SeverityCritical)}

	report, _ := reviewSemgrepReportWith(t, semgrepCritical, model, func(c *config.Config) {
		c.Review.FailOn = config.SeverityCritical
		c.Linters.MaxSeverity = config.SeverityWarning
	})

	if len(report.Findings) != 1 {
		t.Fatalf("findings = %+v, want the one semgrep reported", report.Findings)
	}

	f := report.Findings[0]
	if f.Severity != string(config.SeverityWarning) {
		t.Errorf("published at %q, want warning: a pass downstream of the ceiling re-rated the "+
			"finding and the ceiling did not hold", f.Severity)
	}
	if report.Failed(config.SeverityCritical) {
		t.Error("an operator who capped analyzer findings at warning still had the build failed " +
			"by one; the ceiling has to reach the gate or it is decoration")
	}
	if !report.Failed(config.SeverityWarning) {
		t.Error("the capped finding does not satisfy a warning gate either, so the cap deleted " +
			"the finding's weight rather than reducing it")
	}

	// The ceiling is applied before the prompt as well as after it, so the
	// model is never invited to escalate from a level the operator declined.
	prompts := model.prompts()
	if len(prompts) != 1 {
		t.Fatalf("triage prompts = %d, want 1", len(prompts))
	}
	if !strings.Contains(prompts[0], "[warning]") {
		t.Errorf("triage was shown a level the operator had already capped away:\n%s", prompts[0])
	}

	// Reducing is not forgetting, and a re-rating is not a retraction: semgrep
	// printed CRITICAL whatever triage decided afterwards. Publishing this
	// finding as semgrep's with no raw word would assert semgrep said "warning".
	if f.RawSeverity != "CRITICAL" {
		t.Errorf("RawSeverity = %q, want %q: capping is a policy decision about what we ACT on, "+
			"not permission to rewrite what the analyzer said", f.RawSeverity, "CRITICAL")
	}
	if !f.SeverityTranslated {
		t.Error("a re-rated analyzer finding came out claiming its level is the analyzer's own " +
			"word, which is the substitution this field pair exists to prevent")
	}
	if !strings.Contains(f.Source, "semgrep") {
		t.Errorf("Source = %q; the assertions above only matter because the finding is still "+
			"attributed to the analyzer", f.Source)
	}
}

// TestARewordedAnalyzerFindingIsNoLongerTheAnalyzers states the limit of the
// ceiling rather than leaving a reader to discover it.
//
// FromAnalyzer, Source and Class are all restored by Finding.Key(), which
// contains the title. A triage pass that rewrites the title past recognition
// loses all three together: the finding is published with no analyzer named, as
// triage's own. The ceiling is documented as a ceiling on findings ATTRIBUTED to
// an analyzer, and this is the run where that qualifier does work. Pinning it
// means widening the hole, restoring Source but not the marker, say, fails
// here instead of silently disabling a policy an operator set.
func TestARewordedAnalyzerFindingIsNoLongerTheAnalyzers(t *testing.T) {
	model := &scriptedTriage{
		rerate:  string(config.SeverityCritical),
		retitle: "Untraceable rewording of the same defect",
	}

	report, _ := reviewSemgrepReportWith(t, semgrepCritical, model, func(c *config.Config) {
		c.Review.FailOn = config.SeverityCritical
		c.Linters.MaxSeverity = config.SeverityWarning
	})

	if len(report.Findings) != 1 {
		t.Fatalf("findings = %+v, want the one reworded finding", report.Findings)
	}

	f := report.Findings[0]
	if f.Source != "" {
		t.Errorf("Source = %q: this test only describes a limit while the reworded finding is "+
			"unattributable. If attribution now survives a rewording, the ceiling must too",
			f.Source)
	}
	if f.Severity != string(config.SeverityCritical) {
		t.Errorf("published at %q, want critical: an unattributed finding is triage's own and "+
			"linters.max_severity has nothing to say about it", f.Severity)
	}

	// The published LEVEL was already asserted above; this asserts the
	// CONSEQUENCE, which is the thing an operator configured. The two came apart
	// once: the level was pinned here while nothing called Failed, so a reworded
	// analyzer finding escaping the ceiling and failing a critical gate was a
	// behaviour no test could see. An operator who sets max_severity believing
	// analyzers cannot fail a critical gate is wrong in exactly this case, and
	// the README says so.
	if !report.Failed(config.SeverityCritical) {
		t.Error("a reworded finding published at critical must also FAIL a critical gate; " +
			"asserting the level without the gate is how this escape stayed invisible")
	}
}

// TestAnAnalyzerCriticalIsAdvisoryUnderTheDefaultGate guards the direction the
// fix could have overshot in.
//
// Making critical reachable must not make a linter finding blocking by default.
// fail_on defaults to none precisely because this reviewer has not earned the
// right to stop merges, and an analyzer's severity is not a reason to revisit
// that.
func TestAnAnalyzerCriticalIsAdvisoryUnderTheDefaultGate(t *testing.T) {
	report, _ := reviewSemgrepReport(t, semgrepCritical, nil)

	if got := config.Defaults().Review.FailOn; got != config.SeverityNone {
		t.Fatalf("fail_on default = %q, want none; this test asserts the default is advisory", got)
	}
	if report.Failed(config.Defaults().Review.FailOn) {
		t.Error("a critical from an analyzer failed the run under the default advisory gate")
	}
}

// semgrepExperiment is a real semgrep report whose severity is outside the four
// levels semgrep documents. Values like this do occur, and this project cannot
// rank one.
const semgrepExperiment = `{"results":[{"check_id":"go.lang.correctness.experiment",` +
	`"path":"app.go","start":{"line":2},` +
	`"extra":{"message":"Command built from user input","severity":"EXPERIMENT"}}]}`

// TestAnUnreadableAnalyzerWordStillClearsAMinSeverityOfWarning is the
// publication consequence the mapping table cannot show on its own.
//
// THE BUG IT PINS: aligning the unreadable-word floor with a MODEL's, info,
// looked like a symmetry fix and was a silent deletion. A repository with
// review.min_severity: warning, which an operator sets to cut noise, published
// nothing at all for these findings; before the alignment it published them at
// warning. mapSeverity's return value alone cannot fail on that, because the
// level it returns is perfectly reasonable and the loss happens two passes later
// at the gate.
func TestAnUnreadableAnalyzerWordStillClearsAMinSeverityOfWarning(t *testing.T) {
	report, _ := reviewSemgrepReport(t, semgrepExperiment, func(c *config.Config) {
		c.Review.MinSeverity = config.SeverityWarning
	})

	if len(report.Findings) != 1 {
		t.Fatalf("findings = %+v, want the one semgrep reported. A word we could not read is our "+
			"translation failing, and making our own failure quieter deletes the finding",
			report.Findings)
	}
	if got := report.Findings[0].Severity; got != string(config.SeverityWarning) {
		t.Errorf("published at %q, want warning", got)
	}
}

// golangciNit is real golangci-lint 2.8.0 JSON, produced by running it over a
// bad Printf with `severity: {default: nit}` in .golangci.yml. The Severity
// field is that configured text verbatim, golangci-lint has no severity
// vocabulary of its own.
const golangciNit = `{"Issues":[{"FromLinter":"govet",` +
	`"Text":"printf: fmt.Printf format %d has arg \"x\" of wrong type string",` +
	`"Severity":"nit","Pos":{"Filename":"app.go","Line":2,"Column":14}}]}`

// TestAnAnalyzerNitIsBelowTheDefaultPublicationFloor records what honoring the
// word costs, so nobody rediscovers it in production.
//
// "nit" reaches our nit because golangci-lint prints operator text and folding
// it up to info would be an unjustified reduction pointed the other way. But nit
// is the ONE level below review.min_severity's default of info, so
// `severity.default: nit` in a .golangci.yml publishes no Go analyzer findings
// at all, including compile errors, which golangci-lint reports through
// typecheck and severity.default blankets like everything else. Both
// configurations are doing what they say; the combination is the surprise, and
// the second half of this test shows the operator's own floor is what decides
// it.
func TestAnAnalyzerNitIsBelowTheDefaultPublicationFloor(t *testing.T) {
	found, err := (&golangciLint{}).findings([]byte(golangciNit), 0)
	if err != nil {
		t.Fatalf("parse golangci-lint output: %v", err)
	}
	if len(found) != 1 || found[0].Severity != config.SeverityNit {
		t.Fatalf("findings = %+v, want one at nit", found)
	}

	silent := reviewLinterFindings(t, "golangci-lint", found, &scriptedTriage{}, nil)
	if len(silent.Findings) != 0 {
		t.Errorf("findings = %+v, want none: nit is below the default min_severity of %q",
			silent.Findings, config.Defaults().Review.MinSeverity)
	}

	heard := reviewLinterFindings(t, "golangci-lint", found, &scriptedTriage{}, func(c *config.Config) {
		c.Review.MinSeverity = config.SeverityNit
	})
	if len(heard.Findings) != 1 {
		t.Fatalf("findings = %+v, want the one issue: lowering the floor is what publishes it, "+
			"so the level itself is not being discarded", heard.Findings)
	}
	if got := heard.Findings[0].RawSeverity; got != "nit" {
		t.Errorf("RawSeverity = %q, want %q: the operator's own word is what we are honoring here",
			got, "nit")
	}
}
