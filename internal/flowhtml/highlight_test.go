package flowhtml

import (
	"strings"
	"testing"
)

func TestHighlightGoMarksKeywordsStringsAndComments(t *testing.T) {
	got := highlightGo("package p\n// note\nfunc F() string { return \"hi\" }\n")
	for _, want := range []string{
		"<span class=\"k\">package</span>",
		"<span class=\"c\">// note</span>",
		"<span class=\"k\">func</span>",
		"<span class=\"f\">F</span>",
		"<span class=\"t\">string</span>",
		"<span class=\"s\">&#34;hi&#34;</span>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("highlight missing %q:\n%s", want, got)
		}
	}
}

func TestHighlightGoEscapesMarkupInSource(t *testing.T) {
	got := highlightGo("package p\nvar x = \"<script>alert(1)</script>\"\n")
	if strings.Contains(got, "<script>") {
		t.Fatalf("raw script tag survived highlighting:\n%s", got)
	}
	if !strings.Contains(got, "&lt;script&gt;") {
		t.Fatalf("markup was not escaped:\n%s", got)
	}
}

func TestHighlightGoEscapesUnparseableSourceAsPlainText(t *testing.T) {
	got := highlightGo("<<< not go at all >>> <img onerror=x>")
	for _, forbidden := range []string{"<img", "<script", "onerror=x>"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("unparseable source leaked %q:\n%s", forbidden, got)
		}
	}
	// Every angle bracket from the input must arrive as an entity; the only
	// literal tags in the output are the highlighter's own spans.
	if strings.Count(got, "&lt;") != 4 || strings.Count(got, "&gt;") != 4 {
		t.Fatalf("angle brackets were not all escaped:\n%s", got)
	}
	for _, tag := range tagsIn(t, got) {
		if !strings.HasPrefix(tag, "span") && tag != "/span" {
			t.Fatalf("unexpected tag %q in highlighted output:\n%s", tag, got)
		}
	}
}

func TestHighlightGoPreservesEveryInputByte(t *testing.T) {
	source := "package p\n\nfunc F() {\n\tg()\n}\n"
	got := highlightGo(source)
	stripped := strings.NewReplacer("&#34;", "\"", "&amp;", "&", "&lt;", "<", "&gt;", ">", "&#39;", "'").Replace(stripTags(t, got))
	if stripped != source {
		t.Fatalf("highlighting changed the text:\nwant %q\ngot  %q", source, stripped)
	}
}

func TestExtractSnippetCentersTheFocusLineWithinBounds(t *testing.T) {
	var b strings.Builder
	b.WriteString("package p\n")
	for i := 2; i <= 40; i++ {
		b.WriteString("// line\n")
	}
	got := extractSnippet("p.go", b.String(), 20, 4, 4)
	if got.FirstLine != 16 {
		t.Fatalf("FirstLine = %d, want 16", got.FirstLine)
	}
	if len(got.Lines) != 9 {
		t.Fatalf("line count = %d, want 9", len(got.Lines))
	}
	focused := 0
	for _, line := range got.Lines {
		if line.Focus {
			focused++
			if line.Number != 20 {
				t.Fatalf("focus marked line %d, want 20", line.Number)
			}
		}
	}
	if focused != 1 {
		t.Fatalf("focus line count = %d, want exactly 1", focused)
	}
}

func TestExtractSnippetClampsToTheStartAndEndOfAFile(t *testing.T) {
	source := "package p\nfunc F() {}\n"
	head := extractSnippet("p.go", source, 1, 10, 1)
	if head.FirstLine != 1 || head.Lines[0].Number != 1 {
		t.Fatalf("snippet ran off the top of the file: %+v", head)
	}
	tail := extractSnippet("p.go", source, 2, 1, 50)
	last := tail.Lines[len(tail.Lines)-1]
	if last.Number > 3 {
		t.Fatalf("snippet ran off the bottom of the file: last line %d", last.Number)
	}
}

// TestExtractSnippetReturnsNoLinesForEmptySource proves an empty snippet is
// empty because the file had nothing to show, not because the window silently
// fell outside it.
func TestExtractSnippetReturnsNoLinesForEmptySource(t *testing.T) {
	got := extractSnippet("p.go", "", 1, 3, 3)
	if len(got.Lines) != 1 || got.Lines[0].HTML != "" {
		t.Fatalf("empty source produced %d line(s): %+v", len(got.Lines), got.Lines)
	}
	if got.Path != "p.go" || got.Focus != 1 {
		t.Fatalf("empty snippet lost its identity: %+v", got)
	}
}

func TestBalanceSpansClosesASpanSplitAcrossLines(t *testing.T) {
	if got := balanceSpans("<span class=\"c\">/* open"); !strings.HasSuffix(got, "</span>") {
		t.Fatalf("unterminated span was not closed: %q", got)
	}
	if got := balanceSpans("still inside */</span>"); !strings.HasPrefix(got, "<span") {
		t.Fatalf("orphaned close was not paired: %q", got)
	}
	balanced := "<span class=\"k\">func</span>"
	if got := balanceSpans(balanced); got != balanced {
		t.Fatalf("balanced line was rewritten: %q", got)
	}
}

// tagsIn returns the name of every literal HTML tag in s.
func tagsIn(t *testing.T, s string) []string {
	t.Helper()
	var out []string
	for i := 0; i < len(s); i++ {
		if s[i] != '<' {
			continue
		}
		end := strings.IndexByte(s[i:], '>')
		if end < 0 {
			break
		}
		inner := s[i+1 : i+end]
		if space := strings.IndexByte(inner, ' '); space >= 0 {
			inner = inner[:space]
		}
		out = append(out, inner)
		i += end
	}
	return out
}

func stripTags(t *testing.T, s string) string {
	t.Helper()
	var b strings.Builder
	depth := 0
	for _, r := range s {
		switch {
		case r == '<':
			depth++
		case r == '>' && depth > 0:
			depth--
		case depth == 0:
			b.WriteRune(r)
		}
	}
	return b.String()
}
