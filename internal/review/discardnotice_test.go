package review

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/bundle"
	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/diff"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// discards is the ledger of a run where an analyzer produced six findings and
// the review published none of them: four for reasons that are this
// repository's own policy, and two for a path that does not exist here.
func discards() []LinterDiscard {
	return []LinterDiscard{
		{Rule: "golangci-lint(errcheck)", Path: "sibling.go", Line: 4, Reason: DiscardNotInChange},
		{Rule: "golangci-lint(errcheck)", Path: "app.go", Line: 91, Reason: DiscardUnchangedLine},
		{Rule: "golangci-lint(staticcheck)", Path: "app.go", Line: 92, Reason: DiscardUnchangedLine},
		{Rule: "ruff(F401)", Path: "app.py", Line: 3, Reason: DiscardUnanchorable},
		{Rule: "golangci-lint(errcheck)", Path: "zz_generated.go", Line: 2, Reason: DiscardPathNotInCheckout},
		{Rule: "golangci-lint(ineffassign)", Path: "zz_generated.go", Line: 4, Reason: DiscardPathNotInCheckout},
	}
}

// TestDiscardedAnalyzerFindingsArePublishedOnThePullRequest is the disclosure
// half of closing the silent-drop sink.
//
// Counting a dropped finding inside the process is worth nothing on its own: the
// reader who has to know that a deterministic analyzer produced findings this
// review threw away is the one reading the pull request, and that is the same
// mistake the analyzer roster was already fixed for once, when the statuses
// existed and reached only a CI log.
func TestDiscardedAnalyzerFindingsArePublishedOnThePullRequest(t *testing.T) {
	report := &Report{Plan: &bundle.Plan{}, Discarded: discards()}

	// Through Render, because Render's output is the object that gets posted and
	// the failure being guarded against is a report field with no path to it.
	published := Render(report, diff.Files{}, config.Defaults()).Summary
	if published == "" {
		t.Fatal("a run that discarded six analyzer findings published nothing about it")
	}

	for _, want := range []string{
		string(DiscardNotInChange),
		string(DiscardUnchangedLine),
		string(DiscardUnanchorable),
		string(DiscardPathNotInCheckout),
	} {
		if !strings.Contains(published, want) {
			t.Errorf("the published review never gives the reason %q:\n%s", want, published)
		}
	}
}

// TestTheDiscardHeadlineIsVisibleWithoutExpandingIt: the ledger lives in a
// <details>, and a forge renders the <summary> whether or not anyone opens it.
//
// The total belongs there because it is the number a reader scrolling past can
// act on, and the not-in-checkout count belongs there separately because it is
// the only reason in the list that means something went wrong rather than that
// policy was applied.
func TestTheDiscardHeadlineIsVisibleWithoutExpandingIt(t *testing.T) {
	report := &Report{Plan: &bundle.Plan{}, Discarded: discards()}

	summary := renderSummary(report, nil)
	headline, _, ok := strings.Cut(summary, "</summary>")
	if !ok {
		t.Fatalf("no collapsed heading at all:\n%s", summary)
	}

	if !strings.Contains(headline, "6") {
		t.Errorf("the collapsed heading does not carry the total:\n%s", headline)
	}
	if !strings.Contains(headline, "2") || !strings.Contains(headline, "not in this checkout") {
		t.Errorf("the collapsed heading does not separate the drops that mean something went "+
			"wrong from the ones that are policy:\n%s", headline)
	}
}

// TestForgedPathsAreNamedIndividually is the one exception to counting.
//
// A count of the policy drops is enough — they are routinely in the dozens and a
// per-finding wall of text teaches a reader to collapse the block forever. A
// path that is not in the checkout is different in kind: nothing in a healthy
// tree reports one, and a count alone leaves nobody able to go and look.
func TestForgedPathsAreNamedIndividually(t *testing.T) {
	report := &Report{Plan: &bundle.Plan{}, Discarded: discards()}

	published := renderSummary(report, nil)

	if !strings.Contains(published, "zz_generated.go:2") ||
		!strings.Contains(published, "zz_generated.go:4") {
		t.Errorf("a path that is not in this checkout was counted but not named:\n%s", published)
	}

	// And the ordinary ones are NOT listed one by one, or the block is unusable
	// on the pull requests where it matters most.
	if strings.Contains(published, "sibling.go:4") {
		t.Errorf("routine policy drops are listed per finding; the block will be collapsed and "+
			"never opened:\n%s", published)
	}
}

// TestTheDiscardNoticeSurvivesSummariesBeingOff pins it to the same rule as the
// policy notice and the analyzer roster.
//
// review.summary asks for less narration. It is not permission to stop saying
// that a deterministic analyzer produced findings this review discarded — with
// it off and this suppressed, such a run publishes nothing at all and reads as
// clean.
func TestTheDiscardNoticeSurvivesSummariesBeingOff(t *testing.T) {
	cfg := config.Defaults()
	cfg.Review.Summary = false

	report := &Report{Plan: &bundle.Plan{}, Discarded: discards()}

	published := Render(report, diff.Files{}, cfg).Summary
	if !strings.Contains(published, string(DiscardPathNotInCheckout)) {
		t.Errorf("turning summaries off removed the record of discarded analyzer findings:\n%s",
			published)
	}
}

// TestADiscardedPathCannotLeaveItsBullet: Path and Rule are what the ANALYZER
// printed, and for the forged case the change under review chose them.
//
// A newline would break out of the bullet and leave the rest of the block
// reading as though it described something else, and raw HTML in a comment
// posted under this bot's name renders. Markdown is NOT neutralized here — see
// inline — which is why these two are wrapped in code spans and why the README
// records that residual rather than implying otherwise.
func TestADiscardedPathCannotLeaveItsBullet(t *testing.T) {
	report := &Report{Plan: &bundle.Plan{}, Discarded: []LinterDiscard{{
		Rule:   "golangci-lint(errcheck)",
		Path:   "zz.go\n\n</details>\n<img src=x onerror=alert(1)>",
		Line:   1,
		Reason: DiscardPathNotInCheckout,
	}}}

	published := renderSummary(report, nil)

	if strings.Contains(published, "<img") {
		t.Errorf("raw HTML from the analyzer's path reached the published comment:\n%s", published)
	}
	for _, line := range strings.Split(published, "\n") {
		if strings.Contains(line, "zz.go") && !strings.HasPrefix(strings.TrimSpace(line), "-") {
			t.Errorf("the path escaped its bullet and continued on its own line:\n%s", published)
		}
	}
}

// anchorDiff is a hunk whose changed line is far from its context lines: seven
// lines of context, then one addition.
//
// The distance is the fixture. filterAnchors snaps a finding onto the nearest
// ADDED line within three lines, so a finding on the first context line is
// outside snapping range — which is the only way to reach the drop branch with a
// line the diff genuinely carries.
const anchorDiff = "diff --git a/app.go b/app.go\n--- a/app.go\n+++ b/app.go\n@@ -1,7 +1,8 @@\n" +
	" one\n two\n three\n four\n five\n six\n seven\n+added\n"

// TestAnAnalyzerFindingDroppedByAnchoringIsCounted closes the SECOND silent
// sink, one function downstream of the one that was already fixed.
//
// Set.normalize's bare `continue` statements were replaced with counted, named
// discards. filterAnchors kept two of its own and runs immediately afterwards,
// and Report.Discarded was read BEFORE it — so a finding that survived
// normalization and died here was invisible in exactly the way the first fix was
// about, and the published headline said zero about a run that had dropped one.
//
// Reaching it needs linters.only_changed_lines off: with the default on,
// normalize drops a context-line finding first and counts it there. The defect
// is the same either way, and a report field frozen before a stage that writes
// to it is a bug regardless of which settings reach the stage.
func TestAnAnalyzerFindingDroppedByAnchoringIsCounted(t *testing.T) {
	files, err := parseDiff(anchorDiff)
	if err != nil {
		t.Fatal(err)
	}

	// A line the diff carries as CONTEXT. Position resolves — which is why
	// normalize passes it through — and it is not a changed line, which is why
	// this function drops it.
	if _, ok := files.Find("app.go").Position(1); !ok {
		t.Fatal("the fixture no longer carries line 1 as context; it would be dropped upstream")
	}

	e := &Engine{Config: baseCfg()}

	kept, dropped := e.filterAnchors([]Finding{{
		Path: "app.go", Line: 1, Severity: "warning", Title: "Error return value is not checked",
		FromAnalyzer: true, Source: "golangci-lint(errcheck)",
	}}, files)

	if len(kept) != 0 {
		t.Fatalf("the fixture no longer exercises the drop; kept = %+v", kept)
	}
	if len(dropped) != 1 {
		t.Fatalf("an analyzer finding was dropped and not recorded: %+v", dropped)
	}
	// The reason has to be the one that is true of THIS finding. The diff
	// carries the line, so "the diff does not carry it" would be a wrong
	// explanation published on a pull request.
	if dropped[0].Reason != DiscardUnchangedLine {
		t.Errorf("reason = %q, want %q: the diff carries this line as context",
			dropped[0].Reason, DiscardUnchangedLine)
	}
	if dropped[0].Rule != "golangci-lint(errcheck)" || dropped[0].Path != "app.go" || dropped[0].Line != 1 {
		t.Errorf("discard = %+v, want it to name the rule and position the analyzer reported", dropped[0])
	}
}

// TestAModelFindingDroppedByAnchoringIsNotCounted keeps the number meaning one
// thing.
//
// A model that anchors a finding two lines off is a model guessing, and it
// happens constantly — that is why this function snaps in the first place.
// LinterDiscard says "a deterministic analyzer reported this and the review threw
// it away"; filling it with model output would make the headline a count of
// something else and drown the case it exists to surface.
func TestAModelFindingDroppedByAnchoringIsNotCounted(t *testing.T) {
	files, err := parseDiff(anchorDiff)
	if err != nil {
		t.Fatal(err)
	}

	e := &Engine{Config: baseCfg()}

	kept, dropped := e.filterAnchors([]Finding{{
		Path: "app.go", Line: 1, Severity: "warning", Title: "this could be clearer",
	}}, files)

	if len(kept) != 0 {
		t.Fatalf("the fixture no longer exercises the drop; kept = %+v", kept)
	}
	if len(dropped) != 0 {
		t.Errorf("a model finding was recorded as a discarded analyzer finding: %+v", dropped)
	}
}

// uncovered is the ledger of a run where golangci-lint ran and covered less of
// the change than "ran" implies: one file the build excludes, one suppression
// the change added, and one module whose go directive switched off checks that
// would otherwise have run.
//
// The third is here because it is the one that does not describe a file of the
// change at all. It anchors to the module's go.mod, which the change need not
// have touched, and it is the one entry that means REDUCED coverage rather than
// absent coverage — so a renderer that assumed every entry names an unread file
// of the diff is wrong about it.
func uncovered() []LinterUncovered {
	return []LinterUncovered{
		{Linter: "golangci-lint", Path: "app_windows.go", Reason: UncoveredBuildExcluded},
		{Linter: "golangci-lint", Path: "app.go", Line: 1, Reason: UncoveredSuppressed},
		{Linter: "golangci-lint", Path: "go.mod", Line: 3, Reason: UncoveredLanguageVersion},
	}
}

// TestTheUncoveredPartsOfAChangeArePublished is the disclosure half of the
// third channel.
//
// "golangci-lint — ran" is true and is read as "the Go analyzer looked at this
// change". A build constraint on the changed file with one ordinary sibling
// beside it, or a //nolint on the package clause, both leave that line true and
// the change unread — with zero findings and a nil error, which is what a clean
// review looks like. Counting it inside the process is worth nothing: the reader
// who needs it is the one reading the pull request.
func TestTheUncoveredPartsOfAChangeArePublished(t *testing.T) {
	report := &Report{Plan: &bundle.Plan{}, Uncovered: uncovered()}

	published := Render(report, diff.Files{}, config.Defaults()).Summary

	for _, want := range []string{
		string(UncoveredBuildExcluded),
		string(UncoveredSuppressed),
		string(UncoveredLanguageVersion),
		// Named individually, because each is something a reviewer has to go and
		// look at and a count would leave nobody able to find them. go.mod:3 most
		// of all: it is nowhere in the diff, so a reader cannot reach it by
		// opening the change.
		"app_windows.go",
		"app.go:1",
		"go.mod:3",
	} {
		if !strings.Contains(published, want) {
			t.Errorf("the published review never says %q:\n%s", want, published)
		}
	}
}

// TestTheUncoveredHeadlineIsVisibleWithoutExpandingIt, and counts files.
//
// The ledger lives in a <details> and a forge renders the <summary> whether or
// not anyone opens it. It counts FILES because that is the fact — one file with
// three added suppressions is one file nothing was said about, and "3" would
// overstate it.
func TestTheUncoveredHeadlineIsVisibleWithoutExpandingIt(t *testing.T) {
	report := &Report{Plan: &bundle.Plan{}, Uncovered: append(uncovered(),
		LinterUncovered{Linter: "golangci-lint", Path: "app.go", Line: 9, Reason: UncoveredSuppressed},
	)}

	summary := renderSummary(report, nil)
	headline, _, ok := strings.Cut(summary, "</summary>")
	if !ok {
		t.Fatalf("no collapsed heading at all:\n%s", summary)
	}
	if !strings.Contains(headline, "3 file") {
		t.Errorf("the collapsed heading does not carry the file count:\n%s", headline)
	}

	// And it does not overstate what it found. Two of the three entries mean the
	// file went unread; the go directive means the file was read under a reduced
	// ruleset, and a headline saying an analyzer reported nothing about it would
	// be false about that one. A block that overstates is a block readers learn
	// to discount, which costs the entries that are not overstated.
	if strings.Contains(headline, "reported nothing about") {
		t.Errorf("the collapsed heading claims every entry went unreported, which is false of a "+
			"module whose go directive merely narrowed the ruleset:\n%s", headline)
	}
}

// TestTheUncoveredNoticeSurvivesSummariesBeingOff pins it to the same rule as
// the analyzer roster and the discard block.
//
// review.summary asks for less narration. It is not permission to stop saying
// that part of the change was never analyzed — which is precisely the state that
// otherwise publishes nothing at all and reads as clean.
func TestTheUncoveredNoticeSurvivesSummariesBeingOff(t *testing.T) {
	cfg := config.Defaults()
	cfg.Review.Summary = false

	report := &Report{Plan: &bundle.Plan{}, Uncovered: uncovered()}

	published := Render(report, diff.Files{}, cfg).Summary
	if !strings.Contains(published, string(UncoveredBuildExcluded)) {
		t.Errorf("turning summaries off removed the record of the parts of the change no "+
			"analyzer read:\n%s", published)
	}
}

// TestAnUncoveredPathCannotLeaveItsBullet: Path comes from the diff, and a
// filename can carry a newline or markup.
//
// Same rule as the discard block, for the same reason: a name that breaks out of
// its bullet leaves the rest of the notice reading as though it described
// something else, in a comment posted under this bot's name.
func TestAnUncoveredPathCannotLeaveItsBullet(t *testing.T) {
	report := &Report{Plan: &bundle.Plan{}, Uncovered: []LinterUncovered{{
		Linter: "golangci-lint",
		Path:   "zz.go\n\n</details>\n<img src=x onerror=alert(1)>",
		Reason: UncoveredBuildExcluded,
	}}}

	published := renderSummary(report, nil)

	if strings.Contains(published, "<img") {
		t.Errorf("raw HTML from a path reached the published comment:\n%s", published)
	}
	for _, line := range strings.Split(published, "\n") {
		if strings.Contains(line, "zz.go") && !strings.HasPrefix(strings.TrimSpace(line), "-") {
			t.Errorf("the path escaped its bullet and continued on its own line:\n%s", published)
		}
	}
}

// unanchorableLinter reports one analyzer finding the anchor filter must drop,
// and reports nothing discarded itself.
//
// Nothing discarded is the point: everything in Report.Discarded after this run
// came from the engine's own filter, so the assertion cannot pass on the
// analyzer set's contribution leaking through.
type unanchorableLinter struct{}

func (unanchorableLinter) Run(context.Context, diff.Files) ([]Finding, error) {
	return []Finding{{
		Path: "other.go", Line: 1, Severity: "warning", Category: "lint",
		Title: "Error return value is not checked", FromAnalyzer: true,
		Source: "golangci-lint(errcheck)",
	}}, nil
}

func (unanchorableLinter) Discarded() []LinterDiscard { return nil }

// TestTheEngineMergesItsOwnAnchorDropsIntoTheDiscardLedger is the WIRING half,
// and it is the half the defect actually lived in.
//
// filterAnchors returning its drops is worth nothing if Report.Discarded is
// assembled before it runs — which is exactly what was there: the field was
// filled from the analyzer set at the point the set was read, two statements
// above the first anchor pass. A unit test of filterAnchors alone stays green
// through that, so this drives Engine.Review end to end and reads the published
// comment.
func TestTheEngineMergesItsOwnAnchorDropsIntoTheDiscardLedger(t *testing.T) {
	empty := mustJSON(t, Result{Summary: "Nothing to report."})

	model := &scriptedLLM{byPrompt: map[string]string{
		"triaging findings":            empty,
		"Review the following changes": empty,
	}}
	provider := &stubProvider{diff: engineDiff}

	engine := newEngine(t, model, provider, nil)
	engine.Linters = func(*config.Config) LinterRunner { return unanchorableLinter{} }

	report, err := engine.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}

	if len(report.Findings) != 0 {
		t.Fatalf("the fixture no longer exercises the drop; findings = %+v", report.Findings)
	}
	if len(report.Discarded) != 1 {
		t.Fatalf("Discarded = %+v, want the analyzer finding the anchor filter dropped", report.Discarded)
	}
	if got := report.Discarded[0]; got.Rule != "golangci-lint(errcheck)" ||
		got.Path != "other.go" || got.Reason != DiscardNotInChange {
		t.Errorf("discard = %+v, want the rule, path and reason the drop actually had", got)
	}

	// And on the pull request, which is the only place it counts.
	if provider.published == nil {
		t.Fatal("nothing was published")
	}
	if !strings.Contains(provider.published.Summary, string(DiscardNotInChange)) {
		t.Errorf("the published review never says an analyzer finding was dropped:\n%s",
			provider.published.Summary)
	}
}

// uncoveringLinter reports no findings at all and one part of the change it
// never read — the exact shape a build-excluded file produces.
type uncoveringLinter struct{}

func (uncoveringLinter) Run(context.Context, diff.Files) ([]Finding, error) { return nil, nil }

func (uncoveringLinter) Uncovered() []LinterUncovered {
	return []LinterUncovered{{
		Linter: "golangci-lint", Path: "app_windows.go", Reason: UncoveredBuildExcluded,
	}}
}

// TestTheEngineCarriesUncoveredPartsThroughToThePullRequest is the wiring half
// of the third channel, and it is wired for the same reason the discard ledger
// is: a reporter nobody calls is a report field that is always empty, and an
// always-empty field renders as a run with nothing to say.
//
// It goes through Engine.Review rather than Render so that the optional
// interface, the assignment and the renderer are all on the path — which is
// where the equivalent defect for discards actually lived.
func TestTheEngineCarriesUncoveredPartsThroughToThePullRequest(t *testing.T) {
	empty := mustJSON(t, Result{Summary: "Nothing to report."})

	model := &scriptedLLM{byPrompt: map[string]string{
		"triaging findings":            empty,
		"Review the following changes": empty,
	}}
	provider := &stubProvider{diff: engineDiff}

	engine := newEngine(t, model, provider, nil)
	engine.Linters = func(*config.Config) LinterRunner { return uncoveringLinter{} }

	report, err := engine.Review(context.Background(), vcs.Ref{})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}

	if len(report.Uncovered) != 1 || report.Uncovered[0].Path != "app_windows.go" {
		t.Fatalf("Uncovered = %+v, want the file the analyzer never read", report.Uncovered)
	}
	if provider.published == nil {
		t.Fatal("nothing was published")
	}
	if !strings.Contains(provider.published.Summary, "app_windows.go") {
		t.Errorf("the published review reports a clean Go run over a file no analyzer read:\n%s",
			provider.published.Summary)
	}
}

// TestTheUncoveredListIsBoundedPerReason: an ordinary pull request can produce
// hundreds of entries for ONE reason, and a body the forge rejects is worse than
// a shorter list.
//
// `go mod vendor` adds a directory of Go files that review.ignore withholds from
// every analyzer, and a port adds a directory of _windows.go. GitHub caps a
// review body at 65536 bytes — summaryFallback exists because a pull request
// deleting thousands of files already produced an enormous skipped-files section
// — so an unbounded list here would trade the coverage notice for the whole
// review.
//
// The bound is per reason so that a bulk route cannot push the others off the
// end, and the headline still counts every file: the total a reader sees is
// never the truncated one.
func TestTheUncoveredListIsBoundedPerReason(t *testing.T) {
	const vendored = maxUncoveredPerReason * 3

	entries := []LinterUncovered{
		{Linter: "golangci-lint", Path: "app_windows.go", Reason: UncoveredBuildExcluded},
	}
	for i := range vendored {
		entries = append(entries, LinterUncovered{
			Linter: "golangci-lint",
			Path:   fmt.Sprintf("vendor/example.com/pkg%d/token.go", i),
			Reason: UncoveredNotSelected,
		})
	}

	published := renderSummary(&Report{Plan: &bundle.Plan{}, Uncovered: entries}, nil)

	if !strings.Contains(published, string(UncoveredNotSelected)) {
		t.Errorf("the notice never says why the vendored files went unanalyzed:\n%s", published)
	}
	if got := strings.Count(published, "vendor/example.com/"); got != maxUncoveredPerReason {
		t.Errorf("the notice lists %d vendored paths, want %d and a count for the rest",
			got, maxUncoveredPerReason)
	}
	if want := fmt.Sprintf("…and %d more", vendored-maxUncoveredPerReason); !strings.Contains(published, want) {
		t.Errorf("the notice never says %q, so the list reads as complete:\n%s", want, published)
	}
	// The other reason is one entry and must survive the flood intact: it is the
	// one nobody would find on their own.
	if !strings.Contains(published, "app_windows.go") {
		t.Errorf("the bulk reason pushed the single build-excluded file out of the notice:\n%s", published)
	}
	// And the headline counts what was there, not what was printed.
	if !strings.Contains(published, fmt.Sprintf("%d file(s)", vendored+1)) {
		t.Errorf("the collapsed heading reports the truncated count rather than the real one:\n%s", published)
	}
}

// TestAChangeNothingReviewedSaysSoRatherThanReadingClean is the emptiest review
// there is, and it used to be the cleanest-looking one.
//
// When every changed file is set aside there are no batches to send, so the
// engine returns before any model or analyzer is asked anything and the summary
// renders empty — at which point the forge publishes its own default body,
// "open-nitpick found nothing to comment on", for a change nothing read. One
// file under vendor/ reaches it, and vendored code is compiled into the binary.
func TestAChangeNothingReviewedSaysSoRatherThanReadingClean(t *testing.T) {
	report := &Report{Plan: &bundle.Plan{Skipped: []bundle.Skip{
		{Path: "vendor/example.com/evil/evil.go", Reason: bundle.ReasonIgnored},
		{Path: "logo.png", Reason: bundle.ReasonIgnored},
	}}}

	cfg := config.Defaults()
	cfg.Review.Summary = false

	published := Render(report, diff.Files{}, cfg).Summary

	if !strings.Contains(published, "Nothing in this change was reviewed") {
		t.Fatalf("a change nothing looked at publishes no summary at all, so the forge's default "+
			"body reports it as clean:\n%q", published)
	}
	// Counted rather than named: `go mod vendor` is hundreds of files, and the
	// reader owns the ignore list that produced them.
	if !strings.Contains(published, bundle.ReasonIgnored+": 2 file(s)") {
		t.Errorf("the notice does not say how much was set aside, or why:\n%s", published)
	}
	if strings.Contains(published, "evil.go") {
		t.Errorf("the notice names files it promised to count, which is what a vendor bump makes "+
			"unpublishable:\n%s", published)
	}
}

// TestAReviewThatRanSaysNothingAboutHavingReviewedNothing is the other
// direction: the notice above must not appear on an ordinary review.
//
// A false "nothing was reviewed" is worse than the silence it replaced — it
// tells a reader to discount findings that were produced by a real review.
func TestAReviewThatRanSaysNothingAboutHavingReviewedNothing(t *testing.T) {
	report := &Report{Plan: &bundle.Plan{
		Batches: []bundle.Batch{{}},
		Skipped: []bundle.Skip{{Path: "logo.png", Reason: bundle.ReasonIgnored}},
	}}

	if published := renderSummary(report, nil); strings.Contains(published, "Nothing in this change was reviewed") {
		t.Errorf("a review with batches to send claims nothing was reviewed:\n%s", published)
	}
}
