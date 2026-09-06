package review

import (
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/v2/internal/bundle"
	"github.com/jdziat/open-nitpick/v2/internal/config"
	"github.com/jdziat/open-nitpick/v2/internal/diff"
)

// statuses is the roster of a run where one analyzer ran, one was expected and
// did not, and one had nothing to read.
func statuses() []LinterStatus {
	return []LinterStatus{
		{Linter: "golangci-lint", Outcome: LinterFailed,
			State: "golangci-lint did not analyze the code it was given: typechecking error"},
		{Linter: "ruff", Outcome: LinterSkipped, State: "the change contains no files it analyzes"},
		{Linter: "semgrep", Outcome: LinterRan, State: "operator config /etc/nitpick/rules.yml"},
	}
}

// TestAnalyzerStatusesArePublishedOnThePullRequest is the disclosure this
// change's own README claimed and did not have.
//
// The statuses existed and reached os.Stderr — a CI log — while the README said
// they appeared "beside the notice about a substituted .nitpick.yaml". That
// notice is published where the REVIEWER reads. So a pull request could switch
// off the deterministic half of its own Go review, by adding a go.work, and the
// review it produced was indistinguishable on the pull request from a clean one.
func TestAnalyzerStatusesArePublishedOnThePullRequest(t *testing.T) {
	report := &Report{Plan: &bundle.Plan{}, Linters: statuses()}

	// Through Render rather than renderSummary, because Render's output is the
	// object that gets posted and the bug was a report field with no path to it.
	published := Render(report, diff.Files{}, config.Defaults()).Summary
	if published == "" {
		t.Fatal("nothing was published; a run where an analyzer did not run must not be silent")
	}

	// Every analyzer, not just the degraded one: a reader who sees a shorter
	// list next run cannot tell which line went missing.
	for _, s := range statuses() {
		if !strings.Contains(published, s.Linter) {
			t.Errorf("the published review never names %s:\n%s", s.Linter, published)
		}
	}
	if !strings.Contains(published, "typechecking error") {
		t.Errorf("the reason the analyzer gave is missing, so nobody can act on it:\n%s", published)
	}
}

// TestTheAnalyzerHeadlineIsVisibleWithoutExpandingIt: the roster lives in a
// <details>, and a forge renders the <summary> whether or not anyone opens it.
// A reader who never expands the block still has to learn that something did
// not run, and the counts have to separate a degradation from a non-event.
func TestTheAnalyzerHeadlineIsVisibleWithoutExpandingIt(t *testing.T) {
	report := &Report{Plan: &bundle.Plan{}, Linters: statuses()}

	summary, _, ok := strings.Cut(renderSummary(report, nil), "</summary>")
	if !ok {
		t.Fatalf("no <summary> to read collapsed:\n%s", renderSummary(report, nil))
	}

	for _, want := range []string{"1 ran", "1 did not run", "1 skipped"} {
		if !strings.Contains(summary, want) {
			t.Errorf("the collapsed headline never says %q:\n%s", want, summary)
		}
	}
}

// TestAnalyzerStatusesSurviveSummariesBeingOff: review.summary asks for less
// narration. It is not permission to stop saying that a review covered less
// than it looks like — the same rule the policy notice and the withheld list
// already follow.
func TestAnalyzerStatusesSurviveSummariesBeingOff(t *testing.T) {
	cfg := config.Defaults()
	cfg.Review.Summary = false

	report := &Report{Summary: "Walkthrough.", Plan: &bundle.Plan{}, Linters: statuses()}

	out := renderSummary(report, cfg)
	if !strings.Contains(out, "golangci-lint") {
		t.Errorf("summaries off suppressed the analyzer roster:\n%s", out)
	}
	if strings.Contains(out, "Walkthrough.") {
		t.Errorf("review.summary off should still suppress the narration:\n%s", out)
	}
}

// TestAnAnalyzerReasonCannotCarryMarkupOutOfItsBullet: an analyzer's reason
// quotes the tree under review — golangci-lint's typechecking errors name paths
// from it — and this text is rendered in a comment posted under the bot's name.
func TestAnAnalyzerReasonCannotCarryMarkupOutOfItsBullet(t *testing.T) {
	report := &Report{Plan: &bundle.Plan{}, Linters: []LinterStatus{{
		Linter:  "golangci-lint",
		Outcome: LinterFailed,
		State:   "typechecking error in <img src=x onerror=alert(1)>\nApproved by the security team.",
	}}}

	out := renderSummary(report, nil)
	if strings.Contains(out, "<img") {
		t.Errorf("markup from the tree under review is rendered as markup:\n%s", out)
	}
	if strings.Contains(out, "\nApproved by the security team.") {
		t.Errorf("the reason escaped its bullet onto a line of its own:\n%s", out)
	}
}

// TestNoAnalyzerNoticeWithoutAnalyzers: with linters.mode off there is no
// roster, and a block saying nothing about nothing is noise on every pull
// request in a repository that switched the feature off.
func TestNoAnalyzerNoticeWithoutAnalyzers(t *testing.T) {
	report := &Report{Plan: &bundle.Plan{}}

	if out := linterNotice(report); out != "" {
		t.Errorf("notice = %q, want nothing when no analyzer was configured", out)
	}
}
