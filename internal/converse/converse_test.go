package converse

import (
	"strings"
	"testing"
)

func TestParseEventReadsBothCommentShapes(t *testing.T) {
	issue := `{"issue":{"number":7,"pull_request":{"url":"x"}},"comment":{"id":11,"body":"@open-nitpick review","user":{"login":"alice"}}}`
	e, err := ParseEvent("issue_comment", []byte(issue))
	if err != nil || e.Number != 7 || e.CommentID != 11 || e.RootID != 11 || e.Author != "alice" || e.Inline {
		t.Errorf("issue_comment = %+v, %v", e, err)
	}
	notPR := `{"issue":{"number":8},"comment":{"id":12,"body":"@open-nitpick hi","user":{"login":"bob"}}}`
	if _, err := ParseEvent("issue_comment", []byte(notPR)); err == nil {
		t.Error("a comment on an issue that is not a pull request was accepted")
	}
	review := `{"pull_request":{"number":7},"comment":{"id":21,"in_reply_to_id":20,"path":"a.go","line":9,"body":"@open-nitpick why?","user":{"login":"carol"}}}`
	e, err = ParseEvent("pull_request_review_comment", []byte(review))
	if err != nil || !e.Inline || e.RootID != 20 || e.Path != "a.go" || e.Line != 9 {
		t.Errorf("review comment = %+v, %v", e, err)
	}
	root := `{"pull_request":{"number":7},"comment":{"id":30,"path":"a.go","line":9,"body":"@open-nitpick resolve","user":{"login":"carol"}}}`
	e, _ = ParseEvent("pull_request_review_comment", []byte(root))
	if e.RootID != 30 {
		t.Errorf("a thread root's RootID = %d, want its own id", e.RootID)
	}
	if _, err := ParseEvent("push", []byte(`{}`)); err == nil {
		t.Error("a push is not a comment event")
	}
}

func TestCommandReadsTheMention(t *testing.T) {
	cases := []struct {
		body string
		kind Kind
		text string
		ok   bool
	}{
		{"@open-nitpick review", KindReview, "review", true},
		{"please @Open-NitPick re-review this", KindReview, "re-review this", true},
		{"@open-nitpick resolve", KindResolve, "resolve", true},
		{"@open-nitpick: fixed in the last push", KindResolve, "fixed in the last push", true},
		{"@open-nitpick why does this matter?", KindAsk, "why does this matter?", true},
		{"@open-nitpick", "", "", false},
		{"no mention here", "", "", false},
		{"email me at bob@open-nitpick.example", "", "", false},
	}
	for _, tc := range cases {
		kind, text, ok := Command(tc.body, "@open-nitpick")
		if kind != tc.kind || text != tc.text || ok != tc.ok {
			t.Errorf("%q = %q, %q, %v; want %q, %q, %v", tc.body, kind, text, ok, tc.kind, tc.text, tc.ok)
		}
	}
}

func TestExcerptNumbersAndMarksTheLine(t *testing.T) {
	got := Excerpt("a\nb\nc\nd\ne\n", 3, 1)
	want := "     2  b\n>    3  c\n     4  d"
	if got != want {
		t.Errorf("excerpt =\n%s\nwant\n%s", got, want)
	}
	if Excerpt("a\n", 5, 1) != "" {
		t.Error("a line past the end has no excerpt")
	}
	if !strings.HasPrefix(Excerpt("a\nb\n", 1, 3), ">    1  a") {
		t.Error("the first line is marked when it is the line")
	}
}

// The association has to survive parsing, or the gate that reads it is
// checking a field that is always empty and refusing everyone.
func TestTheAuthorAssociationIsCarriedFromBothEvents(t *testing.T) {
	issue := `{"issue":{"number":7,"pull_request":{}},"comment":{"id":11,"body":"@open-nitpick review","author_association":"COLLABORATOR","user":{"login":"kim"}}}`
	ev, err := ParseEvent("issue_comment", []byte(issue))
	if err != nil {
		t.Fatalf("issue_comment: %v", err)
	}
	if ev.Association != "COLLABORATOR" {
		t.Errorf("Association = %q, want COLLABORATOR", ev.Association)
	}

	inline := `{"pull_request":{"number":7},"comment":{"id":12,"body":"@open-nitpick why","path":"a.go","line":3,"author_association":"NONE","user":{"login":"stranger"}}}`
	ev, err = ParseEvent("pull_request_review_comment", []byte(inline))
	if err != nil {
		t.Fatalf("pull_request_review_comment: %v", err)
	}
	if ev.Association != "NONE" {
		t.Errorf("Association = %q, want NONE", ev.Association)
	}
}

// "fix" asks the reviewer to do the work; "fixed" tells it the work is done.
// One letter apart, opposite meanings, and the second was already mapped
// before the first existed.
func TestFixIsNotResolve(t *testing.T) {
	for body, want := range map[string]Kind{
		"@open-nitpick fix":                    KindFix,
		"@open-nitpick fix all":                KindFix,
		"@open-nitpick apply this":             KindFix,
		"@open-nitpick Fix.":                   KindFix,
		"@open-nitpick fixed":                  KindResolve,
		"@open-nitpick fixed in the last push": KindResolve,
		"@open-nitpick done":                   KindResolve,
		"@open-nitpick why is this wrong?":     KindAsk,
	} {
		got, _, ok := Command(body, "@open-nitpick")
		if !ok {
			t.Errorf("%q was not read as a mention", body)
			continue
		}
		if got != want {
			t.Errorf("Command(%q) = %q, want %q", body, got, want)
		}
	}
}

// "fix all" covers every published finding; "fix" covers the thread it is on.
func TestFixesAllReadsTheAdverbAndNotThePrefix(t *testing.T) {
	for text, want := range map[string]bool{
		"fix all":              true,
		"fix ALL":              true,
		"fix all.":             true,
		"fix all of them":      true,
		"fix":                  false,
		"fix this one":         false,
		"fix allocation logic": false, // the prefix is not the word
		"":                     false,
	} {
		if got := FixesAll(text); got != want {
			t.Errorf("FixesAll(%q) = %v, want %v", text, got, want)
		}
	}
}

// The text a fix command returns still carries the verb, like every other
// kind, so a caller reading fields[1] sees the adverb rather than the verb.
func TestAFixCommandKeepsItsVerbInTheText(t *testing.T) {
	_, text, ok := Command("@open-nitpick fix all", "@open-nitpick")
	if !ok {
		t.Fatal("not read as a mention")
	}
	if text != "fix all" {
		t.Errorf("text = %q, want the whole remainder", text)
	}
	if !FixesAll(text) {
		t.Error("the text a command returns does not satisfy FixesAll")
	}
}
