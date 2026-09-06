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
