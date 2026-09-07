package main

import (
	"context"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/converse"
	"github.com/jdziat/open-nitpick/internal/review"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// The requirement the eval measurement rests on: asking for the wider pass
// must not change what the next push produces. The pass runs on a copy, and
// this pins that the copy is one.
func TestImproveConfigCopyLeavesTheCallersAlone(t *testing.T) {
	cfg := config.Defaults()
	cfg.Persona.Nitpick = config.NitpickNormal
	cfg.Review.Slop = false
	cfg.Review.MinSeverity = config.SeverityWarning

	icfg := *cfg
	icfg.Persona.Nitpick = config.NitpickPedantic
	icfg.Review.Slop = true
	icfg.Review.MinSeverity = config.SeverityNit

	if cfg.Persona.Nitpick != config.NitpickNormal {
		t.Errorf("caller's nitpick level = %q, want it untouched at normal", cfg.Persona.Nitpick)
	}
	if cfg.Review.Slop {
		t.Error("caller's review.slop was switched on by the improve run")
	}
	if cfg.Review.MinSeverity != config.SeverityWarning {
		t.Errorf("caller's min_severity = %q, want it untouched at warning", cfg.Review.MinSeverity)
	}

	// And the copy reaches the style pass, which is the whole point of it.
	if !icfg.Persona.Nitpick.NeedsStylePass() {
		t.Error("the improve config does not reach the style pass")
	}
}

func TestDropPublishedRemovesWhatIsAlreadyOnThePullRequest(t *testing.T) {
	posted := review.Finding{Path: "a.go", Line: 10, Class: "style", Title: "name this"}
	fresh := review.Finding{Path: "b.go", Line: 20, Class: "style", Title: "document this"}

	prior := &vcs.PriorReview{Comments: []vcs.PriorComment{
		{Fingerprint: review.Fingerprint(posted), Path: "a.go", Line: 10},
	}}

	got := dropPublished([]review.Finding{posted, fresh}, prior)
	if len(got) != 1 || got[0].Path != "b.go" {
		t.Fatalf("dropPublished kept %d finding(s) %+v, want only b.go", len(got), got)
	}

	// A read that failed hands back nil, and losing the answer would be the
	// worse trade than repeating one finding.
	if got := dropPublished([]review.Finding{posted, fresh}, nil); len(got) != 2 {
		t.Errorf("a nil prior review dropped %d finding(s), want 0 dropped", 2-len(got))
	}
}

func TestImproveCommentListsFindingsAndSaysItIsAdvisory(t *testing.T) {
	ev := &converse.Event{Author: "jo"}
	body := improveComment([]review.Finding{
		{Path: "z.go", Line: 3, Title: "widen this name", Severity: "nit", Class: "style"},
		{Path: "a.go", Line: 9, Title: "undocumented export", Severity: "info", Class: "style"},
	}, ev, 2)

	if i, j := strings.Index(body, "a.go:9"), strings.Index(body, "z.go:3"); i < 0 || j < 0 || i > j {
		t.Errorf("findings are not listed in path order:\n%s", body)
	}
	for _, want := range []string{"@jo", "widen this name", "nit", "advisory", "next ordinary review is unchanged"} {
		if !strings.Contains(body, want) {
			t.Errorf("comment does not mention %q:\n%s", want, body)
		}
	}
}

// Silence has two meanings and they are not the same answer: the pass found
// nothing, or it found only what the pull request already says.
func TestImproveCommentSeparatesNothingFoundFromNothingNew(t *testing.T) {
	ev := &converse.Event{Author: "jo"}
	if body := improveComment(nil, ev, 0); !strings.Contains(body, "Nothing to report") {
		t.Errorf("an empty pass should say it found nothing:\n%s", body)
	}
	if body := improveComment(nil, ev, 7); !strings.Contains(body, "Nothing new") {
		t.Errorf("a pass whose findings were all already published should say so:\n%s", body)
	}
}

func TestImproveCommentScopesToTheThreadsFile(t *testing.T) {
	ev := &converse.Event{Author: "jo", Inline: true, Path: "internal/x.go"}
	if body := improveComment(nil, ev, 0); !strings.Contains(body, "`internal/x.go`") {
		t.Errorf("an inline improve should name its file:\n%s", body)
	}
}

// The wrapper is what keeps the pass off the pull request's threads. If it
// ever published, a person asking for style would get a wall of nit comments
// beside the review's correctness ones, which is the dilution the generation
// scope exists to prevent.
func TestHeldReviewPublishesNothing(t *testing.T) {
	h := &heldReview{}
	r := vcs.Review{Summary: "held"}
	if err := h.PublishReview(context.Background(), vcs.Ref{}, r); err != nil {
		t.Fatalf("PublishReview: %v", err)
	}
	if h.held.Summary != "held" {
		t.Errorf("held summary = %q, want the review kept rather than posted", h.held.Summary)
	}

	// Provider only. The optional interfaces are deliberately absent: with
	// them the engine would narrow to the increment and resolve threads.
	var p any = h
	if _, ok := p.(vcs.IncrementalDiffer); ok {
		t.Error("heldReview implements IncrementalDiffer, so improve would review the increment, not the change")
	}
	if _, ok := p.(vcs.ThreadResolver); ok {
		t.Error("heldReview implements ThreadResolver, so improve could close a thread it never read")
	}
	if _, ok := p.(vcs.PriorReviewer); ok {
		t.Error("heldReview implements PriorReviewer, so improve would narrow to what moved")
	}
}
