package main

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/nitpick-oss/nitpick/internal/converse"
	"github.com/nitpick-oss/nitpick/internal/review"
	"github.com/nitpick-oss/nitpick/internal/vcs"
)

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

	// nil prior drops nothing; runImprove fails the read before Review.
	if got := dropPublished([]review.Finding{posted, fresh}, nil); len(got) != 2 {
		t.Errorf("a nil prior review dropped %d finding(s), want 0 dropped", 2-len(got))
	}
}

func TestImproveReadsPriorBeforeTheModelPass(t *testing.T) {
	body, err := os.ReadFile("improve.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(body)
	prior := strings.Index(src, "gh.PriorReview(ctx, ref)")
	pass := strings.Index(src, "engine.Review(ctx, ref)")
	if prior < 0 || pass < 0 {
		t.Fatal("runImprove must call both PriorReview and Review")
	}
	if prior > pass {
		t.Fatal("PriorReview must precede engine.Review")
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
