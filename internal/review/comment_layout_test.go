package review

import (
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/vcs"
)

func TestADismissedFindingIsNotPostedAgain(t *testing.T) {
	again := Finding{Path: "a.go", Class: "correctness", Title: "Guard removed"}
	other := Finding{Path: "b.go", Class: "correctness", Title: "Something else"}
	prior := &vcs.PriorReview{Dismissed: []string{Fingerprint(again)}}

	publish, dismissed := dropDismissed([]Finding{again, other}, prior)
	if len(publish) != 1 || publish[0].Path != "b.go" {
		t.Errorf("publish = %+v, want only the finding nobody answered", publish)
	}
	if len(dismissed) != 1 || dismissed[0].Path != "a.go" {
		t.Errorf("dismissed = %+v, want the answered finding", dismissed)
	}
}

func TestNothingIsDroppedWithoutADismissal(t *testing.T) {
	f := []Finding{{Path: "a.go", Title: "x"}}
	for name, prior := range map[string]*vcs.PriorReview{"no prior": nil, "empty": {}} {
		publish, dismissed := dropDismissed(f, prior)
		if len(publish) != 1 || len(dismissed) != 0 {
			t.Errorf("%s: publish=%d dismissed=%d", name, len(publish), len(dismissed))
		}
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
