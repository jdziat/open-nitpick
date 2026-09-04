package review

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// incrementalProvider is a stubProvider that remembers an earlier run.
type incrementalProvider struct {
	stubProvider
	head    string
	prior   *vcs.PriorReview
	changed []string
	ok      bool
}

func (p *incrementalProvider) PullRequest(context.Context, vcs.Ref) (*vcs.PullRequest, error) {
	return &vcs.PullRequest{Number: 1, Title: "Add retry", HeadSHA: p.head}, nil
}

func (p *incrementalProvider) PriorReview(context.Context, vcs.Ref) (*vcs.PriorReview, error) {
	return p.prior, nil
}

func (p *incrementalProvider) ChangedSince(context.Context, vcs.Ref, string) ([]string, bool, error) {
	return p.changed, p.ok, nil
}

const incrementalDiff = engineDiff + `diff --git a/other.go b/other.go
index 111..222 100644
--- a/other.go
+++ b/other.go
@@ -1,3 +1,4 @@
 package app

+var Unused = 1
 func Other() {}
`

func TestFingerprintIgnoresLineAndPunctuation(t *testing.T) {
	a := Finding{Path: "a.go", Line: 4, Class: "correctness", Title: "Ignored error from http.Get"}
	b := Finding{Path: "a.go", Line: 9, Class: "correctness", Title: "ignored error from http get!"}
	if Fingerprint(a) != Fingerprint(b) {
		t.Error("the same finding on a moved line, reworded only in punctuation, must fingerprint the same")
	}
	c := b
	c.Class = "style"
	if Fingerprint(a) == Fingerprint(c) {
		t.Error("a different class is a different finding")
	}
}

func TestAlreadyReportedMatchesByFingerprintOrPlace(t *testing.T) {
	f := Finding{Path: "a.go", Line: 10, Class: "correctness", Title: "Nil deref"}
	prior := &vcs.PriorReview{Comments: []vcs.PriorComment{
		{Path: "a.go", Line: 40, Fingerprint: Fingerprint(f), Class: "correctness"},
	}}
	if !alreadyReported(f, prior) {
		t.Error("a fingerprint match must count, however far the line moved")
	}

	reworded := Finding{Path: "a.go", Line: 11, Class: "correctness", Title: "Dereference of a nil response"}
	prior = &vcs.PriorReview{Comments: []vcs.PriorComment{{Path: "a.go", Line: 10, Class: "correctness", Fingerprint: "zzzz"}}}
	if !alreadyReported(reworded, prior) {
		t.Error("the same class one line away must count as the same finding")
	}
	far := reworded
	far.Line = 30
	if alreadyReported(far, prior) {
		t.Error("the same class twenty lines away is a different finding")
	}
	if alreadyReported(f, nil) {
		t.Error("nothing is already reported when nothing is known")
	}
}

func TestIncrementalReviewReadsOnlyChangedFilesAndWithholdsPosted(t *testing.T) {
	appFinding := Finding{
		Path: "app.go", Line: 4, Severity: "error", Category: "correctness", Class: "correctness",
		Title: "Ignored error from http.Get", Rationale: "resp may be nil, so the deferred Close panics.",
	}
	model := &scriptedLLM{byPrompt: map[string]string{
		"triaging findings":            mustJSON(t, Result{Summary: "Adds a retry path.", Findings: []Finding{appFinding}}),
		"Review the following changes": mustJSON(t, Result{Findings: []Finding{appFinding}}),
	}}
	provider := &incrementalProvider{
		stubProvider: stubProvider{diff: incrementalDiff},
		head:         "new",
		prior: &vcs.PriorReview{Head: "old", Comments: []vcs.PriorComment{
			{Path: "app.go", Line: 4, Fingerprint: Fingerprint(appFinding), Class: "correctness"},
		}},
		changed: []string{"app.go"},
		ok:      true,
	}

	report, err := newEngine(t, model, provider, nil).Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}

	if report.Incremental == nil {
		t.Fatal("expected an incremental note")
	}
	if got := strings.Join(report.Incremental.Reviewed, ","); got != "app.go" {
		t.Errorf("reviewed = %q, want only the file that moved", got)
	}
	if got := strings.Join(report.Incremental.Unchanged, ","); got != "other.go" {
		t.Errorf("unchanged = %q", got)
	}
	for _, p := range model.prompts() {
		if strings.Contains(p, "other.go") {
			t.Error("a file unchanged since the last review was sent to the model")
		}
	}

	if len(report.Findings) != 0 || len(report.AlreadyReported) != 1 {
		t.Errorf("published %d, withheld %d; the one finding was already posted", len(report.Findings), len(report.AlreadyReported))
	}
	if provider.published == nil {
		t.Fatal("nothing published: the summary must still record the head that was read")
	}
	if provider.published.Head != "new" {
		t.Errorf("published head = %q", provider.published.Head)
	}
	if !strings.Contains(provider.published.Summary, "already posted") ||
		!strings.Contains(provider.published.Summary, "changed since the review at `old`") {
		t.Errorf("summary does not disclose the incremental review:\n%s", provider.published.Summary)
	}
	if len(provider.published.Comments) != 0 {
		t.Errorf("comments = %d, want none re-posted", len(provider.published.Comments))
	}
}

func TestIncrementalFallsBackToFullReviewAfterForcePush(t *testing.T) {
	model := &scriptedLLM{fallback: mustJSON(t, Result{})}
	provider := &incrementalProvider{
		stubProvider: stubProvider{diff: incrementalDiff},
		head:         "new",
		prior:        &vcs.PriorReview{Head: "old"},
		ok:           false,
	}

	report, err := newEngine(t, model, provider, nil).Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if report.Incremental != nil {
		t.Error("an unanswerable comparison must review the whole change, with no incremental note")
	}
	if report.Plan.Files() != 2 {
		t.Errorf("reviewed %d files, want both", report.Plan.Files())
	}
}

func TestIncrementalCanBeSwitchedOff(t *testing.T) {
	model := &scriptedLLM{fallback: mustJSON(t, Result{})}
	provider := &incrementalProvider{
		stubProvider: stubProvider{diff: incrementalDiff},
		head:         "new",
		prior:        &vcs.PriorReview{Head: "old"},
		changed:      []string{"app.go"},
		ok:           true,
	}

	engine := newEngine(t, model, provider, func(c *config.Config) { c.Review.Incremental = false })
	report, err := engine.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if report.Incremental != nil || report.Plan.Files() != 2 {
		t.Error("review.incremental: false must read the whole change")
	}
}

func TestPublishedCommentsCarryFingerprints(t *testing.T) {
	f := Finding{Path: "app.go", Line: 4, Severity: "error", Class: "correctness", Title: "Ignored error"}
	model := &scriptedLLM{byPrompt: map[string]string{
		"triaging findings":            mustJSON(t, Result{Summary: "s", Findings: []Finding{f}}),
		"Review the following changes": mustJSON(t, Result{Findings: []Finding{f}}),
	}}
	provider := &stubProvider{diff: engineDiff}
	if _, err := newEngine(t, model, provider, nil).Review(context.Background(), vcs.Ref{}); err != nil {
		t.Fatal(err)
	}
	c := provider.published.Comments[0]
	if c.Fingerprint == "" || c.Class != "correctness" {
		t.Errorf("comment = %+v; without a fingerprint the next run cannot recognise it", c)
	}
}

// TestAReviewThatCannotBePublishedIsStillReturned: the review has happened and
// been paid for by the time publishing fails, so the caller gets the whole
// report beside ErrPublish and can print, gate and summarise it.
func TestAReviewThatCannotBePublishedIsStillReturned(t *testing.T) {
	f := Finding{Path: "app.go", Line: 4, Severity: "error", Class: "correctness", Title: "Ignored error"}
	model := &scriptedLLM{byPrompt: map[string]string{
		"triaging findings":            mustJSON(t, Result{Summary: "s", Findings: []Finding{f}}),
		"Review the following changes": mustJSON(t, Result{Findings: []Finding{f}}),
	}}
	provider := &stubProvider{diff: engineDiff, err: errors.New("403 forbidden")}
	report, err := newEngine(t, model, provider, nil).Review(context.Background(), vcs.Ref{})
	if !errors.Is(err, ErrPublish) {
		t.Fatalf("err = %v, want ErrPublish", err)
	}
	if report == nil || len(report.Findings) != 1 || len(report.Files) == 0 {
		t.Fatalf("report = %+v; the completed review must accompany the error", report)
	}
	rendered := Render(report, report.Files, nil)
	if len(rendered.Comments) != 1 {
		t.Errorf("the report's own diff must render its comments, got %d", len(rendered.Comments))
	}
}

// TestTriageMayNotLoseAFindingSilently: a finding triage neither publishes
// nor lists under dropped with a reason is restored; one it lists is withheld
// and disclosed as an overrule.
func TestTriageMayNotLoseAFindingSilently(t *testing.T) {
	kept := Finding{Path: "app.go", Line: 4, Severity: "error", Class: "correctness", Title: "Ignored error", Rationale: "resp is nil on failure."}
	lost := Finding{Path: "app.go", Line: 6, Severity: "nit", Class: "maintainability", Title: "Redundant copy", Rationale: "The slice is copied twice, costing an allocation per call."}
	listed := Finding{Path: "app.go", Line: 5, Severity: "warning", Class: "concurrency", Title: "Speculative", Rationale: "Might race if Get is called concurrently."}
	model := &scriptedLLM{byPrompt: map[string]string{
		"triaging findings": mustJSON(t, Result{Summary: "s", Findings: []Finding{kept},
			Dropped: []Drop{{Number: 2, Reason: "asserts Get is called concurrently, which the change does not show"}}}),
		"Review the following changes": mustJSON(t, Result{Findings: []Finding{kept, lost, listed}}),
	}}
	provider := &stubProvider{diff: engineDiff}
	report, err := newEngine(t, model, provider, func(c *config.Config) { c.Review.MinSeverity = config.SeverityNit }).Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	titles := map[string]bool{}
	for _, f := range report.Findings {
		titles[f.Title] = true
	}
	if !titles["Ignored error"] || !titles["Redundant copy"] {
		t.Errorf("published = %v; the finding triage lost without a reason must be restored", titles)
	}
	if titles["Speculative"] {
		t.Error("a finding triage dropped with a reason was published")
	}
	found := false
	for _, o := range report.Overruled {
		if o.Finding.Title == "Speculative" && strings.HasPrefix(o.Expert, "triage") && o.Reason != "" {
			found = true
		}
	}
	if !found {
		t.Errorf("the triage drop is not disclosed as an overrule: %+v", report.Overruled)
	}
}

// TestMultiLineSuggestionsAreCommittableOnlyWhenValidated: a fix whose range
// sits inside one hunk is published as a comment on that range with a
// suggestion block; one that leaves the hunk, or replaces lines with
// themselves, is published as a described change on the anchor alone.
func TestMultiLineSuggestionsAreCommittableOnlyWhenValidated(t *testing.T) {
	good := Finding{Path: "app.go", Line: 4, Severity: "error", Class: "correctness", Title: "Check the error",
		Suggestion: "\tresp, err := http.Get(\"http://x\")\n\tif err != nil {\n\t\treturn err\n\t}", FixEndLine: 5}
	outside := Finding{Path: "app.go", Line: 6, Severity: "warning", Class: "correctness", Title: "Leaves the hunk",
		Suggestion: "x\ny\nz\nw\nv", FixEndLine: 10}
	same := Finding{Path: "app.go", Line: 4, Severity: "info", Class: "maintainability", Title: "Identical",
		Suggestion: "\tresp, _ := http.Get(\"http://x\")\n\tdefer resp.Body.Close()", FixEndLine: 5}
	model := &scriptedLLM{byPrompt: map[string]string{
		"triaging findings":            mustJSON(t, Result{Summary: "s", Findings: []Finding{good, outside, same}}),
		"Review the following changes": mustJSON(t, Result{Findings: []Finding{good, outside, same}}),
	}}
	provider := &stubProvider{diff: engineDiff}
	report, err := newEngine(t, model, provider, func(c *config.Config) { c.Review.MinSeverity = config.SeverityNit }).Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	byTitle := map[string]Finding{}
	for _, f := range report.Findings {
		byTitle[f.Title] = f
	}
	if !byTitle["Check the error"].FixValidated {
		t.Error("a range inside one hunk must validate")
	}
	if byTitle["Leaves the hunk"].FixValidated || byTitle["Identical"].FixValidated {
		t.Error("a range leaving the hunk, or a no-op replacement, must not validate")
	}

	for _, c := range provider.published.Comments {
		switch {
		case strings.Contains(c.Body, "Check the error"):
			if c.StartLine != 4 || c.Line != 5 || !strings.Contains(c.Body, "```suggestion\n") {
				t.Errorf("validated fix not published on its range as a suggestion: start=%d line=%d\n%s", c.StartLine, c.Line, c.Body)
			}
		case strings.Contains(c.Body, "Leaves the hunk"):
			if c.StartLine != 0 || strings.Contains(c.Body, "```suggestion\n") {
				t.Errorf("an unvalidated fix was published as committable: start=%d\n%s", c.StartLine, c.Body)
			}
		}
	}
}
