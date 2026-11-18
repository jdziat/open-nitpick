package bundle

import (
	"fmt"
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

func FuzzSourceSpansKeepSelectionAndRenderedBytes(f *testing.F) {
	for _, seed := range []struct {
		content                string
		first, last, next, end int
	}{
		{"", 0, 0, 0, 0},
		{"one\ntwo\n", 1, 1, 2, 2},
		{"one\ntwo", 1, 2, 0, 0},
		{"\n", 1, 1, 2, 2},
		{"one\r\né\x00\nz", 1, 1, 3, 3},
		{"a\nb\n", 2, 2, 1, 1},
		{"a\nb", 1, 1, 1, 2},
		{"a", -1, 10, 0, 0},
		{"a\n\nb\n", 1, 2, 3, 3},
	} {
		f.Add(seed.content, seed.first, seed.last, seed.next, seed.end)
	}
	f.Fuzz(func(t *testing.T, content string, first, last, next, end int) {
		var spans []SourceSpan
		if first != 0 || last != 0 {
			spans = append(spans, SourceSpan{first, last})
		}
		if next != 0 || end != 0 {
			spans = append(spans, SourceSpan{next, end})
		}
		lines := strings.SplitAfter(content, "\n")
		if lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
		var selected, rendered strings.Builder
		var expectedErr error
		prior := 0
		for _, span := range spans {
			if span.Start <= prior || span.End < span.Start || span.End > len(lines) {
				expectedErr = fmt.Errorf("invalid source span %d:%d for %d lines", span.Start, span.End, len(lines))
				break
			}
			selected.WriteString(strings.Join(lines[span.Start-1:span.End], ""))
			prior = span.End
		}
		got, err := SelectSourceSpans(content, spans)
		if expectedErr != nil {
			if err == nil || err.Error() != expectedErr.Error() || got != "" {
				t.Fatalf("invalid ranges: selection=%q, err=%v, want=%v", got, err, expectedErr)
			}
		} else if err != nil || got != selected.String() {
			t.Fatalf("selection=%q err=%v, want=%q", got, err, selected.String())
		}
		entry := Entry{File: &diff.File{Path: "source.go"}, Content: content, SourceOnly: true, SourceSpans: spans}
		rendered.WriteString("### Selected supporting source: source.go\nOnly the declared line ranges are shown. Findings here belong in the summary.\n")
		if expectedErr != nil {
			rendered.WriteString("Source ranges are unavailable.\n")
		} else {
			for _, span := range spans {
				fmt.Fprintf(&rendered, "\nLines %d-%d:\n```\n", span.Start, span.End)
				for index := span.Start - 1; index < span.End; index++ {
					fmt.Fprintf(&rendered, "%6d  %s", index+1, lines[index])
					if !strings.HasSuffix(lines[index], "\n") {
						rendered.WriteByte('\n')
					}
				}
				rendered.WriteString("```\n")
			}
		}
		if got := renderSourceSpans(entry); got != rendered.String() {
			t.Fatal("source range rendering changed bytes or original line numbers")
		}
	})
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

func TestEmptySourceSpansSelectNoBytes(t *testing.T) {
	got, err := SelectSourceSpans("HIDDEN\n", nil)
	if err != nil || got != "" {
		t.Fatalf("empty range selection=%q err=%v", got, err)
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
