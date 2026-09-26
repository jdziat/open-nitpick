package bundle

import (
	"github.com/jdziat/open-nitpick/internal/diff"
	"strings"
	"testing"
)

func TestSourceSpansPreserveBytesAndOriginalLineNumbers(t *testing.T) {
	source := "package example\r\nHIDDEN\n\tfunc Use() {}\nHIDDEN AGAIN\nlast"
	spans := []SourceSpan{{1, 1}, {3, 3}, {5, 5}}
	selected, err := SelectSourceSpans(source, spans)
	if err != nil || selected != "package example\r\n\tfunc Use() {}\nlast" {
		t.Fatalf("selection=%q err=%v", selected, err)
	}
	entry := Entry{File: &diff.File{Path: "a.go"}, Content: source, SourceSpans: spans, SourceOnly: true}
	rendered := Render(entry)
	if entry.HasContent() || strings.Contains(rendered, "HIDDEN") || !strings.Contains(rendered, "     3  \tfunc Use() {}") || !strings.Contains(rendered, "     5  last\n") {
		t.Fatalf("selection leaked source or lost original numbering: %s", rendered)
	}
}

func TestSourceSpansRejectInvalidOrUnavailableLines(t *testing.T) {
	for _, spans := range [][]SourceSpan{{{0, 1}}, {{2, 1}}, {{1, 3}}, {{1, 1}, {1, 2}}, {{2, 2}, {1, 1}}} {
		if _, err := SelectSourceSpans("one\ntwo\n", spans); err == nil {
			t.Fatalf("accepted %v", spans)
		}
	}
	if _, err := SelectSourceSpans("", []SourceSpan{{1, 1}}); err == nil {
		t.Fatal("invented a line in empty source")
	}
}

func TestSourceRangesRejectGapsAndAcceptAdjacentEvidence(t *testing.T) {
	spans := []SourceSpan{{2, 3}, {4, 5}, {8, 9}}
	for _, test := range []struct {
		start, end int
		want       bool
	}{{2, 5, true}, {3, 8, false}, {1, 2, false}, {8, 9, true}, {9, 10, false}, {3, 2, false}, {0, 0, false}} {
		if got := ContainsSourceRange(spans, test.start, test.end); got != test.want {
			t.Fatalf("%+v: got %v", test, got)
		}
	}
}
