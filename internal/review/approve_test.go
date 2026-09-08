package review

import (
	"context"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// approving returns a config with review.approve on and the analyzer gate as
// given, so each case below states only what it is about.
func approving(requireAnalyzers bool) *config.Config {
	cfg := config.Defaults()
	cfg.Review.Approve.Enabled = true
	cfg.Review.Approve.RequireAnalyzers = requireAnalyzers
	return cfg
}

// TestApprovalIsOffUntilItIsAskedFor pins the default, which is the whole
// reason this is a setting rather than a behaviour. A repository upgrading
// into this feature must not start approving its own pull requests.
func TestApprovalIsOffUntilItIsAskedFor(t *testing.T) {
	cfg := config.Defaults()
	if cfg.Review.Approve.Enabled {
		t.Fatal("review.approve.enabled defaults to true; upgrading would start approving")
	}
	if cfg.Review.Approve.RequireAnalyzers {
		t.Error("review.approve.require_analyzers defaults to true; the documented default is a clean review alone")
	}
	if got := reviewEvent(&Report{}, cfg); got != vcs.EventComment {
		t.Errorf("event on a clean review with approval off = %q, want %q", got, vcs.EventComment)
	}
}

// TestACleanReviewIsApproved is the case the setting exists for.
func TestACleanReviewIsApproved(t *testing.T) {
	if got := reviewEvent(&Report{}, approving(false)); got != vcs.EventApprove {
		t.Errorf("event = %q, want %q", got, vcs.EventApprove)
	}
}

// TestAFindingHoldsTheReviewAtAComment covers the obvious half.
func TestAFindingHoldsTheReviewAtAComment(t *testing.T) {
	r := &Report{Findings: []Finding{{Path: "a.go", Line: 1, Title: "boom"}}}
	if got := reviewEvent(r, approving(false)); got != vcs.EventComment {
		t.Errorf("event with one finding = %q, want %q", got, vcs.EventComment)
	}
}

// TestAPartlyFailedRunIsNotApproved is the case that makes this safe.
//
// A batch that failed published no findings for its files, so "no findings"
// and "nothing was read" are the same value here. Report.Incomplete is the
// only thing that tells them apart, and an approval that skips it converts a
// broken run into a green check.
func TestAPartlyFailedRunIsNotApproved(t *testing.T) {
	r := &Report{Incomplete: []string{"unreviewed.go"}}
	if got := reviewEvent(r, approving(false)); got != vcs.EventComment {
		t.Errorf("event on an incomplete run = %q, want %q: an unreviewed file is not a clean one", got, vcs.EventComment)
	}
}

// TestTheAnalyzerGateIsOptInAndBinds covers both directions of
// require_analyzers, since a gate that never fires and a gate that always
// fires are equally useless and look the same from one case.
func TestTheAnalyzerGateIsOptInAndBinds(t *testing.T) {
	skipped := &Report{Linters: []LinterStatus{{Linter: "golangci-lint", Outcome: LinterSkipped}}}
	if got := reviewEvent(skipped, approving(false)); got != vcs.EventApprove {
		t.Errorf("a skipped analyzer blocked approval with the gate off: %q", got)
	}
	if got := reviewEvent(skipped, approving(true)); got != vcs.EventComment {
		t.Errorf("a skipped analyzer passed the gate: %q, want %q", got, vcs.EventComment)
	}

	ran := &Report{Linters: []LinterStatus{{Linter: "golangci-lint", Outcome: LinterRan}}}
	if got := reviewEvent(ran, approving(true)); got != vcs.EventApprove {
		t.Errorf("an analyzer that ran failed the gate: %q, want %q", got, vcs.EventApprove)
	}
}

// TestACoverageGapFailsTheAnalyzerGate is the half a status line cannot show.
//
// golangciLint.Uncovered records a file a build constraint excluded or a
// suppression the change added, on a run whose status is "ran". Approving on
// the status alone would call that code checked.
func TestACoverageGapFailsTheAnalyzerGate(t *testing.T) {
	r := &Report{
		Linters:   []LinterStatus{{Linter: "golangci-lint", Outcome: LinterRan}},
		Uncovered: []LinterUncovered{{Linter: "golangci-lint", Path: "app_windows.go"}},
	}
	if got := reviewEvent(r, approving(true)); got != vcs.EventComment {
		t.Errorf("event with an uncovered file = %q, want %q", got, vcs.EventComment)
	}
	if got := reviewEvent(r, approving(false)); got != vcs.EventApprove {
		t.Errorf("the coverage list blocked approval with the gate off: %q", got)
	}
}

// TestAStandingFindingIsNotApproved covers the incremental case.
//
// An incremental run withholds a finding an earlier run posted, so Findings is
// empty while the comment thread it made is still open. Approving there
// describes a review nobody performed.
func TestAStandingFindingIsNotApproved(t *testing.T) {
	r := &Report{AlreadyReported: []Finding{{Path: "a.go", Line: 1, Title: "still open"}}}
	if got := reviewEvent(r, approving(false)); got != vcs.EventComment {
		t.Errorf("event with a withheld finding = %q, want %q", got, vcs.EventComment)
	}
}

// TestAStandingThreadIsNotApproved covers the case AlreadyReported cannot see.
//
// A narrowed run does not re-read every file, so it never re-produces the
// finding whose thread is still open on one it skipped: Findings and
// AlreadyReported are both empty and the pull request still carries a comment.
// PriorComments is what makes that visible.
func TestAStandingThreadIsNotApproved(t *testing.T) {
	r := &Report{PriorComments: 1}
	if got := reviewEvent(r, approving(false)); got != vcs.EventComment {
		t.Errorf("event beside a standing thread = %q, want %q", got, vcs.EventComment)
	}

	// The same run once it has closed that thread.
	r.Superseded = []vcs.PriorComment{{ID: 1, Path: "a.go"}}
	if got := reviewEvent(r, approving(false)); got != vcs.EventApprove {
		t.Errorf("event after superseding every prior comment = %q, want %q", got, vcs.EventApprove)
	}
}

// TestAnApprovalWithNoBodyIsStillPublished pins the guard in Engine.publish,
// through Engine.publish.
//
// A clean run with review.summary off renders no comments and no summary, so
// the early return on an empty body would drop the approval and log "nothing
// to publish". Asserting on Render alone would leave that revertible with
// every test still green, which is what the first version of this test did.
func TestAnApprovalWithNoBodyIsStillPublished(t *testing.T) {
	cfg := approving(false)
	cfg.Review.Summary = false

	rec := &capturingProvider{}
	e := &Engine{Config: cfg, Provider: rec}
	if err := e.publish(t.Context(), vcs.Ref{}, &Report{}, nil); err != nil {
		t.Fatalf("publish: %v", err)
	}

	if rec.published == nil {
		t.Fatal("publish() returned without calling PublishReview: the approval was dropped")
	}
	if rec.published.Event != vcs.EventApprove {
		t.Errorf("event = %q, want %q", rec.published.Event, vcs.EventApprove)
	}
	if len(rec.published.Comments) != 0 || rec.published.Summary != "" {
		t.Fatalf("this case is only interesting when the body is empty: %d comments, summary %q",
			len(rec.published.Comments), rec.published.Summary)
	}
}

// TestACleanCommentReviewWithNoBodyIsStillDropped is the other direction: the
// early return has to keep working for everything that is not a disposition.
func TestACleanCommentReviewWithNoBodyIsStillDropped(t *testing.T) {
	cfg := config.Defaults()
	cfg.Review.Summary = false

	rec := &capturingProvider{}
	e := &Engine{Config: cfg, Provider: rec}
	if err := e.publish(t.Context(), vcs.Ref{}, &Report{}, nil); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if rec.published != nil {
		t.Errorf("an empty comment review was published as %+v", rec.published)
	}
}

// capturingProvider is a Provider that records the review it was handed and
// answers everything else with a zero value. Only PublishReview is exercised
// here; the rest exists to satisfy the interface.
type capturingProvider struct {
	published *vcs.Review
}

func (c *capturingProvider) Name() string { return "capturing" }

func (c *capturingProvider) PullRequest(context.Context, vcs.Ref) (*vcs.PullRequest, error) {
	return &vcs.PullRequest{}, nil
}

func (c *capturingProvider) Diff(context.Context, vcs.Ref) ([]byte, error) { return nil, nil }

func (c *capturingProvider) FileContent(context.Context, vcs.Ref, string) ([]byte, error) {
	return nil, vcs.ErrNotFound
}

func (c *capturingProvider) PublishReview(_ context.Context, _ vcs.Ref, r vcs.Review) error {
	c.published = &r
	return nil
}

// TestAnEmptyRosterFailsTheAnalyzerGate closes the vacuous reading.
//
// "Every enabled analyzer ran" is satisfied by a report naming none, which is
// what -no-linters produces and what any run that built no analyzer set
// produces. The one setting that exists to forbid an unchecked approval would
// otherwise permit exactly that.
func TestAnEmptyRosterFailsTheAnalyzerGate(t *testing.T) {
	cfg := approving(true)
	if got := reviewEvent(&Report{}, cfg); got != vcs.EventComment {
		t.Errorf("event with no analyzers on the roster = %q, want %q", got, vcs.EventComment)
	}

	// An operator who turned the analyzers off asked for a review without
	// them, so the gate has nothing to hold out for.
	cfg.Linters.Mode = config.LinterOff
	if got := reviewEvent(&Report{}, cfg); got != vcs.EventApprove {
		t.Errorf("event with linters.mode off = %q, want %q", got, vcs.EventApprove)
	}
}
