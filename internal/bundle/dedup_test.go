package bundle

import (
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/diff"
)

// addedFile builds an entry for a file the change adds whole.
func wholeAddedFile(t *testing.T, path, body string) Entry {
	t.Helper()
	var lines []diff.Line
	for i, l := range strings.Split(strings.TrimSuffix(body, "\n"), "\n") {
		lines = append(lines, diff.Line{Kind: diff.LineAdded, Content: l, NewLine: i + 1})
	}
	return Entry{
		File: &diff.File{
			Path: path,
			Kind: diff.ChangeAdded,
			Hunks: []diff.Hunk{{
				OldStart: 0, OldLines: 0,
				NewStart: 1, NewLines: len(lines),
				Lines: lines,
			}},
		},
		Content: body,
	}
}

// An added file's content reaches the model once.
//
// Measured on the three files issue #81 used, staged as whole additions:
// 93,766 rendered characters with both sections and 47,546 with the diff
// alone, a 49.3% drop. That is a prompt-size measurement and not a latency
// one, which the issue is careful about and so is this.
//
// Its diff is every line of it, each carrying its new-file line number, so the
// full-file section that followed was the same file again. On the fixture in
// issue #81 that was 43,569 of 100,070 prompt characters.
func TestAnAddedFileIsNotSentTwice(t *testing.T) {
	body := "package a\n\nfunc F() int {\n\treturn 1\n}\n"
	got := Render(wholeAddedFile(t, "a.go", body))

	if strings.Contains(got, "Full file after the change") {
		t.Errorf("the full-file section was emitted for an added file:\n%s", got)
	}
	if n := strings.Count(got, "func F() int"); n != 1 {
		t.Errorf("the body appears %d times, want 1:\n%s", n, got)
	}
	// The diff is what carries it, so every line still has to be there with a
	// line number a finding can anchor to.
	if !strings.Contains(got, "     3 + func F() int {") {
		t.Errorf("the diff does not carry the numbered line:\n%s", got)
	}
}

// A modified file still gets both, because its diff carries only the hunks.
func TestAModifiedFileStillSendsTheWholeFile(t *testing.T) {
	e := wholeAddedFile(t, "b.go", "package b\n\nfunc G() {}\n")
	e.File.Kind = diff.ChangeModified

	got := Render(e)
	if !strings.Contains(got, "Full file after the change") {
		t.Errorf("a modified file lost its full-file section:\n%s", got)
	}
}

// A window of an added file is still a window, and its heading says how much
// of the file is missing.
func TestAWindowedAddedFileKeepsItsSection(t *testing.T) {
	e := wholeAddedFile(t, "c.go", "package c\n")
	e.Truncated = true
	e.ContextLines = 8

	got := Render(e)
	if !strings.Contains(got, "only the regions within 8 lines of an edit") {
		t.Errorf("a windowed added file lost its heading:\n%s", got)
	}
}
