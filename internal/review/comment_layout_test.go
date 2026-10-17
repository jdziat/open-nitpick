package review

import (
	"context"
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

func TestADismissedFindingIsNotPostedAgain(t *testing.T) {
	again := Finding{Path: "a.go", Class: "correctness", Title: "Guard removed"}
	other := Finding{Path: "b.go", Class: "correctness", Title: "Something else"}
	publish, dismissed := dropDismissed([]Finding{again, other}, []string{Fingerprint(again)})
	if len(publish) != 1 || publish[0].Path != "b.go" {
		t.Errorf("publish = %+v, want only the finding nobody answered", publish)
	}
	if len(dismissed) != 1 || dismissed[0].Path != "a.go" {
		t.Errorf("dismissed = %+v, want the answered finding", dismissed)
	}
}

func TestNothingIsDroppedWithoutADismissal(t *testing.T) {
	f := []Finding{{Path: "a.go", Title: "x"}}
	for name, answered := range map[string][]string{"nil": nil, "empty": {}} {
		publish, dismissed := dropDismissed(f, answered)
		if len(publish) != 1 || len(dismissed) != 0 {
			t.Errorf("%s: publish=%d dismissed=%d", name, len(publish), len(dismissed))
		}
	}
}

type priorOnly struct {
	vcs.Provider
	prior *vcs.PriorReview
	reads int
}

func (p *priorOnly) PriorReview(context.Context, vcs.Ref) (*vcs.PriorReview, error) {
	p.reads++
	return p.prior, nil
}

func TestAFullReviewStillSeesWhatWasDismissed(t *testing.T) {
	provider := &priorOnly{prior: &vcs.PriorReview{Dismissed: []string{"aa11"}}}
	e := &Engine{Provider: provider}

	if got := e.dismissals(context.Background(), vcs.Ref{}, nil); len(got) != 1 || got[0] != "aa11" {
		t.Fatalf("a run with no prior read lost the dismissals: %v", got)
	}
	// A run that already holds the prior reads nothing a second time.
	provider.reads = 0
	held := &vcs.PriorReview{Dismissed: []string{"bb22"}}
	if got := e.dismissals(context.Background(), vcs.Ref{}, held); len(got) != 1 || got[0] != "bb22" || provider.reads != 0 {
		t.Fatalf("dismissals = %v after %d extra reads", got, provider.reads)
	}
}

func TestACommentLeadsWithSeverityThenTitleThenReason(t *testing.T) {
	body := composeComment(Finding{Severity: "warning", Category: "correctness", Title: "Guard removed",
		Rationale: "A caller depends on it.", Suggestion: "x := 1"}, true, nil, true)
	order := []string{"Warning", "correctness", "**Guard removed**", "A caller depends on it.", "```suggestion", ReplyInvitation}
	last := -1
	for _, want := range order {
		at := strings.Index(body[last+1:], want)
		if at < 0 {
			t.Fatalf("%q is missing or out of order in:\n%s", want, body)
		}
		last += at + 1
	}
}

func TestOnlyAPublishedCommentInvitesAReply(t *testing.T) {
	f := Finding{Severity: "info", Title: "x"}
	if body := composeComment(f, true, nil, true); !strings.Contains(body, ReplyInvitation) {
		t.Errorf("a published comment does not invite a reply:\n%s", body)
	}
	// A finding quoted inside the summary has no thread to reply in.
	if body := renderComment(f, true, nil); strings.Contains(body, ReplyInvitation) {
		t.Errorf("a quoted finding invites a reply that has nowhere to go:\n%s", body)
	}
}

func TestATitleCannotStartMarkupOfItsOwn(t *testing.T) {
	body := composeComment(Finding{Severity: "info", Title: "x\n\n# injected <b>"}, true, nil, true)
	if strings.Contains(body, "\n# injected") || strings.Contains(body, "<b>") {
		t.Errorf("the title escaped its line:\n%s", body)
	}
}

func TestTurningRepliesOffRemovesTheInvitation(t *testing.T) {
	files, err := parseDiff("diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1,2 +1,3 @@\n one\n+two\n three\n")
	if err != nil {
		t.Fatal(err)
	}
	report := &Report{Findings: []Finding{{Path: "a.go", Line: 2, Severity: "warning", Title: "x"}}}

	off := config.Defaults()
	no := false
	off.Review.Respond.Replies = &no
	on, disabled := Render(report, files, config.Defaults()), Render(report, files, off)

	if len(on.Comments) != 1 || len(disabled.Comments) != 1 {
		t.Fatalf("comments on=%d off=%d, want one each", len(on.Comments), len(disabled.Comments))
	}
	if !strings.Contains(on.Comments[0].Body, ReplyInvitation) {
		t.Errorf("replies are on and the invitation is missing:\n%s", on.Comments[0].Body)
	}
	if strings.Contains(disabled.Comments[0].Body, ReplyInvitation) {
		t.Errorf("replies are off and the comment still invites one:\n%s", disabled.Comments[0].Body)
	}
}
