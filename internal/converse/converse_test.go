package converse

import (
	"strings"
	"testing"
)

func TestParseEventReadsBothCommentShapes(t *testing.T) {
	issue := `{"issue":{"number":7,"pull_request":{"url":"x"}},"comment":{"id":11,"body":"@nitpick review","user":{"login":"alice"}}}`
	e, err := ParseEvent("issue_comment", []byte(issue))
	if err != nil || e.Number != 7 || e.CommentID != 11 || e.RootID != 11 || e.Author != "alice" || e.Inline {
		t.Errorf("issue_comment = %+v, %v", e, err)
	}
	notPR := `{"issue":{"number":8},"comment":{"id":12,"body":"@nitpick hi","user":{"login":"bob"}}}`
	if _, err := ParseEvent("issue_comment", []byte(notPR)); err == nil {
		t.Error("a comment on an issue that is not a pull request was accepted")
	}
	review := `{"pull_request":{"number":7},"comment":{"id":21,"in_reply_to_id":20,"path":"a.go","line":9,"body":"@nitpick why?","user":{"login":"carol"}}}`
	e, err = ParseEvent("pull_request_review_comment", []byte(review))
	if err != nil || !e.Inline || e.RootID != 20 || e.Path != "a.go" || e.Line != 9 {
		t.Errorf("review comment = %+v, %v", e, err)
	}
	root := `{"pull_request":{"number":7},"comment":{"id":30,"path":"a.go","line":9,"body":"@nitpick resolve","user":{"login":"carol"}}}`
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
		{"@nitpick review", KindReview, "review", true},
		{"please @NitPick re-review this", KindReview, "re-review this", true},
		{"@nitpick resolve", KindResolve, "resolve", true},
		{"@nitpick: fixed in the last push", KindResolve, "fixed in the last push", true},
		{"@nitpick why does this matter?", KindAsk, "why does this matter?", true},
		{"@nitpick", "", "", false},
		{"no mention here", "", "", false},
		{"email me at bob@nitpick.example", "", "", false},
	}
	for _, tc := range cases {
		kind, text, ok := Command(tc.body, "@nitpick")
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
