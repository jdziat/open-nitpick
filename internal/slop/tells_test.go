package slop

import (
	"strings"
	"testing"
)

func rules(tells []Tell) string {
	var names []string
	for _, t := range tells {
		names = append(names, t.Rule)
	}
	return strings.Join(names, ",")
}

func TestProseTellsAreFoundOutsideCode(t *testing.T) {
	md := "# Title\n\nThis is fast, reliable, and secure — it genuinely works.\n\nThe flow is parse -> review – then post.\n\n```\nnot — scanned -> here\n```\n\n| a — b | c |\n\nSure! Here's the thing. Use `x — y` in code.\n\nRuns golangci-lint, ruff, and semgrep; noise 0.21 – 0.38 and $3 – $5.\n"
	got := Scan("README.md", md)
	want := "em-dash,filler-qualifier,triplet-rhythm,arrow-in-prose,en-dash-separator,chat-prose"
	if rules(got) != want {
		t.Errorf("rules = %s, want %s\n%+v", rules(got), want, got)
	}
	for _, tell := range got {
		if tell.Fix == "" || tell.Excerpt == "" {
			t.Errorf("tell without a fix or excerpt: %+v", tell)
		}
		if tell.Line == 8 || tell.Line == 11 {
			t.Errorf("a fenced or table line was scanned: %+v", tell)
		}
	}
}

func TestSourceTellsComeFromCommentsOnly(t *testing.T) {
	src := `package a

// Sure! Here's the function — it returns x.
var s = "an em — dash in a string is not a tell"

// increment the counter
counter++

// Add adds a and b. It takes a and b and returns their sum, which is the
// result of adding them, as one would expect from an add function that
// adds. The addition is performed by the + operator, which adds. This is
// the canonical way to add two integers in Go, and it is used here to add.
// Callers should pass a and b. The result is returned to the caller. The
// caller may then use the result. Nothing else happens in this function.
// It has no side effects and no allocations. It is safe to call from any
// goroutine. It does not panic.
func Add(a, b int) int {
	return a + b
}
`
	got := Scan("a.go", src)
	if !strings.Contains(rules(got), "chat-prose") || !strings.Contains(rules(got), "em-dash") {
		t.Errorf("comment tells missing: %s", rules(got))
	}
	for _, tell := range got {
		if tell.Line == 4 {
			t.Errorf("a string literal was scanned: %+v", tell)
		}
	}
	if !strings.Contains(rules(got), "restating-comment") {
		t.Errorf("the restating comment was not found: %+v", got)
	}
	if !strings.Contains(rules(got), "oversized-doc-comment") {
		t.Errorf("the oversized doc comment was not found: %+v", got)
	}
	// A short doc comment over a long function is not oversized.
	fine := "package a\n\n// F does the thing.\nfunc F() {\n\tx := 1\n\tx++\n\t_ = x\n}\n"
	if got := Scan("b.go", fine); len(got) != 0 {
		t.Errorf("clean source produced %+v", got)
	}
}

func TestSummaryCountsInRuleOrder(t *testing.T) {
	s := Summary([]Tell{{Rule: "chat-prose"}, {Rule: "em-dash"}, {Rule: "em-dash"}})
	if len(s) != 2 || s[0].Rule.Name != "em-dash" || s[0].Count != 2 || s[1].Rule.Name != "chat-prose" {
		t.Errorf("summary = %+v", s)
	}
}

// A Python line starting with * is not a comment, and an indented code
// block in Markdown is not prose.
func TestCommentMarkersFollowTheLanguage(t *testing.T) {
	py := "def f(*args):\n    *rest, last = args  # unpack — with a dash in a comment\n    return last\n"
	got := Scan("a.py", py)
	if len(got) != 1 || got[0].Rule != "em-dash" || got[0].Line != 2 {
		t.Errorf("python tells = %+v, want one em dash from the trailing comment only", got)
	}
	md := "Prose.\n\n    code — not scanned -> here\n\ttab code — not scanned\n"
	if got := Scan("x.md", md); len(got) != 0 {
		t.Errorf("indented code was scanned: %+v", got)
	}
}

// The rhetorical shapes a reader calls out as machine-written, which the
// word-level rules were blind to. Each of these was in this repository's own
// documentation when a reader identified it on sight.
func TestTheAntithesisShapeIsFound(t *testing.T) {
	for _, line := range []string{
		"It is not a nicety, it is a correctness matter.\n",
		"Surfacing skips is not decoration, that is the whole point.\n",
		"These were not a bug, but a design choice.\n",
	} {
		got := Scan("x.md", line)
		if !strings.Contains(rules(got), "antithesis") {
			t.Errorf("not found in %q: %+v", line, got)
		}
	}

	// A plain negation is not the shape. The tell is the pivot into a
	// restatement, not the word "not".
	if got := Scan("x.md", "The linters are not run from the repository.\n"); len(got) != 0 {
		t.Errorf("a plain negation was flagged: %+v", got)
	}
}

// One appositive tail is a sentence. Forty of them is a voice, and only a
// whole-file measure can tell the two apart.
func TestCadenceIsMeasuredOverAFileNotALine(t *testing.T) {
	dense := strings.Repeat(
		"The rule is simple: it holds everywhere.\nIt reads the diff, the config, and the tree.\n", 30)
	got := Scan("dense.md", dense)
	if !strings.Contains(rules(got), "prose-cadence") {
		t.Errorf("dense prose was not flagged: %+v", got)
	}

	// The same two sentences alone are not a cadence, and a file that short
	// has no rhythm to measure.
	if got := Scan("short.md", "The rule is simple: it holds everywhere.\n"); len(got) != 0 {
		t.Errorf("a single sentence was flagged: %+v", got)
	}

	sparse := strings.Repeat("The reviewer reads the diff and posts comments.\n", 60)
	if got := Scan("sparse.md", sparse); len(got) != 0 {
		t.Errorf("plain prose was flagged: %+v", got)
	}
}

// Fenced code, tables and headings carry colons and lists that are not prose,
// so counting them would flag every reference page.
func TestCadenceIgnoresCodeTablesAndHeadings(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 60; i++ {
		b.WriteString("| a: b | c, d, and e |\n")
		b.WriteString("## A heading: with a colon\n")
	}
	b.WriteString("```\ncfg := Config{a, b, and c}\nx is this: that\n```\n")
	if got := Scan("ref.md", b.String()); len(got) != 0 {
		t.Errorf("non-prose lines were counted: %+v", got)
	}
}

// A config key written in backticks is a value, not a cadence. Counting it
// would score a reference page by how many options it documents.
func TestCadenceIgnoresInlineCode(t *testing.T) {
	line := "Set `a, b, and c` to configure it.\n"
	if got := Scan("ref.md", strings.Repeat(line, 60)); len(got) != 0 {
		t.Errorf("inline code was counted: %+v", got)
	}
}

// The rule was prose-only, so a struct-field comment built out of parallel
// clauses was invisible. This is the shape a reader picks out of a diff first.
func TestCadenceReadsSourceComments(t *testing.T) {
	var b strings.Builder
	b.WriteString("package a\n\n")
	for i := 0; i < 60; i++ {
		b.WriteString("// The width is chosen per file: it has to be recorded.\n")
		b.WriteString("// A reader who cannot tell one, another, and a third apart is lost.\n")
		b.WriteString("var x int\n\n")
	}
	if got := Scan("a.go", b.String()); !strings.Contains(rules(got), "prose-cadence") {
		t.Errorf("dense comments were not flagged: %s", rules(got))
	}

	// commentLines must return comment text and nothing else. Code lines carry
	// colons and commas that are syntax, and cadence would score them.
	//
	// Call commentLines directly. Asserting through Scan would pass whether or
	// not the filter works: cadence skips indented lines, and a file this short
	// is under its 40-line floor, so Scan returns zero either way.
	code := []string{
		"package a",
		"var A, B, and C = 1, 2, 3",
		"type T struct{ X, Y, and Z int }",
		"// the only comment: it carries the voice",
		"func f() { m := map[string]int{\"a\": 1, \"b\": 2}; _ = m }",
	}
	lines := commentLines(code, "//")
	if len(lines) != 1 {
		t.Fatalf("commentLines returned %d line(s), want the one comment: %q", len(lines), lines)
	}
	if !strings.Contains(lines[0], "carries the voice") {
		t.Errorf("commentLines returned %q, want the comment body", lines[0])
	}
}

// A raw string literal holds other people's text. This package's own tests
// embed fixtures whose doc comments start with the Go comment marker.
func TestRawStringLiteralsAreNotScanned(t *testing.T) {
	src := "package a\n\n" +
		"var fixture = `\n" +
		"/** toCents takes DOLLARS — and it MUST NOT round. */\n" +
		"fun toCents(d: Double): Long = 0\n" +
		"`\n\n" +
		"// A real comment with an em dash — this one counts.\n" +
		"func f() {}\n"

	got := Scan("a.go", src)
	if len(got) != 1 {
		t.Fatalf("tells = %+v, want only the real comment's em dash", got)
	}
	if got[0].Line != 8 {
		t.Errorf("flagged line %d, want 8: the fixture's own text was scanned", got[0].Line)
	}
}

// Shouting is a closed list of ordinary words, not a shape. A name in capitals
// is a name.
func TestShoutingIsWordsNotShape(t *testing.T) {
	for _, line := range []string{
		"// The cap bounds what is READ, not what is SENT.\n",
		"// IT MUST BE RENDERED, and this paragraph said it was not.\n",
		"// how much of the file it is NOT being shown\n",
	} {
		if got := Scan("a.go", line); !strings.Contains(rules(got), "shouting-emphasis") {
			t.Errorf("not flagged: %q -> %s", line, rules(got))
		}
	}

	for _, line := range []string{
		"// a README that explains what the place is\n",
		"// exceeded review.MAX_FILES on the TOML PATH\n",
		"// parses SARIF from the HTTP endpoint, see GHSA and CVE ids\n",
	} {
		if got := Scan("a.go", line); strings.Contains(rules(got), "shouting-emphasis") {
			t.Errorf("a name was flagged: %q -> %+v", line, got)
		}
	}
}

// A comment that narrates its own history is a commit message that outlived
// its commit.
func TestChangelogCommentsAreFound(t *testing.T) {
	for _, line := range []string{
		"// That check used to live here, and rejecting a file was wrong.\n",
		"// THE BUG THIS FIXES: it was taken inside ScoreSeverity.\n",
		"// The note here outlived the fix by two changes.\n",
		"// It was previously refused its content outright.\n",
	} {
		if got := Scan("a.go", line); !strings.Contains(rules(got), "changelog-comment") {
			t.Errorf("not flagged: %q -> %s", line, rules(got))
		}
	}

	if got := Scan("a.go", "// fetchContent reads a file's contents at the reviewed revision.\n"); len(got) != 0 {
		t.Errorf("an ordinary doc comment was flagged: %+v", got)
	}
}
