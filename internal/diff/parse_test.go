package diff

import (
	"strings"
	"testing"
)

const modifiedDiff = `diff --git a/internal/app/server.go b/internal/app/server.go
index 1234567..89abcde 100644
--- a/internal/app/server.go
+++ b/internal/app/server.go
@@ -10,7 +10,9 @@ func (s *Server) Start() error {
 	s.mu.Lock()
 	defer s.mu.Unlock()

-	if s.listener == nil {
+	if s.listener == nil || s.closed {
+		s.metrics.Inc("start.invalid")
+
 		return ErrNotReady
 	}

`

func TestParseModifiedFile(t *testing.T) {
	files, err := Parse([]byte(modifiedDiff))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("files = %d, want 1", len(files))
	}

	f := files[0]
	if f.Path != "internal/app/server.go" {
		t.Errorf("Path = %q", f.Path)
	}
	if f.Kind != ChangeModified {
		t.Errorf("Kind = %q, want modified", f.Kind)
	}
	if len(f.Hunks) != 1 {
		t.Fatalf("hunks = %d, want 1", len(f.Hunks))
	}

	h := f.Hunks[0]
	if h.OldStart != 10 || h.OldLines != 7 || h.NewStart != 10 || h.NewLines != 9 {
		t.Errorf("hunk ranges = -%d,%d +%d,%d, want -10,7 +10,9", h.OldStart, h.OldLines, h.NewStart, h.NewLines)
	}
	if h.Header != "func (s *Server) Start() error {" {
		t.Errorf("Header = %q", h.Header)
	}

	stats := f.Stats()
	if stats.Added != 3 || stats.Removed != 1 {
		t.Errorf("stats = +%d/-%d, want +3/-1", stats.Added, stats.Removed)
	}
}

func TestNewLineNumbersTrackTheNewFile(t *testing.T) {
	files, err := Parse([]byte(modifiedDiff))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	h := files[0].Hunks[0]

	// The hunk starts at new line 10 with three context lines, so the removed
	// line sits between context line 12 and added line 13.
	want := []struct {
		kind    LineKind
		newLine int
		content string
	}{
		{LineContext, 10, "\ts.mu.Lock()"},
		{LineContext, 11, "\tdefer s.mu.Unlock()"},
		{LineContext, 12, ""},
		{LineRemoved, 0, "\tif s.listener == nil {"},
		{LineAdded, 13, "\tif s.listener == nil || s.closed {"},
		{LineAdded, 14, "\t\ts.metrics.Inc(\"start.invalid\")"},
		{LineAdded, 15, ""},
		{LineContext, 16, "\t\treturn ErrNotReady"},
		{LineContext, 17, "\t}"},
		{LineContext, 18, ""},
	}

	if len(h.Lines) != len(want) {
		t.Fatalf("lines = %d, want %d", len(h.Lines), len(want))
	}
	for i, w := range want {
		got := h.Lines[i]
		if got.Kind != w.kind {
			t.Errorf("line %d kind = %q, want %q", i, got.Kind, w.kind)
		}
		if got.NewLine != w.newLine {
			t.Errorf("line %d NewLine = %d, want %d", i, got.NewLine, w.newLine)
		}
		if got.Content != w.content {
			t.Errorf("line %d content = %q, want %q", i, got.Content, w.content)
		}
	}
}

func TestPositionsStartAtOneBelowTheFirstHunkHeader(t *testing.T) {
	// Per GitHub: "The line just below the '@@' line is position 1." The first
	// header is the zero point, not position 1. An off-by-one here puts every
	// comment in the file on the wrong line.
	files, err := Parse([]byte(modifiedDiff))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	lines := files[0].Hunks[0].Lines
	for i, l := range lines {
		want := i + 1
		if l.Position != want {
			t.Errorf("line %d position = %d, want %d", i, l.Position, want)
		}
	}
}

const multiHunkDiff = `diff --git a/a.go b/a.go
index 111..222 100644
--- a/a.go
+++ b/a.go
@@ -1,3 +1,4 @@
 package a

+var x = 1

@@ -20,3 +21,4 @@ func f() {
 	return
 }
+

`

func TestPositionsContinueAcrossHunks(t *testing.T) {
	files, err := Parse([]byte(multiHunkDiff))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	f := files[0]
	if len(f.Hunks) != 2 {
		t.Fatalf("hunks = %d, want 2", len(f.Hunks))
	}

	// Hunk 1's four lines occupy positions 1..4; its header is the zero point.
	last := f.Hunks[0].Lines[len(f.Hunks[0].Lines)-1]
	if last.Position != 4 {
		t.Errorf("last position of hunk 1 = %d, want 4", last.Position)
	}
	// The *second* header is an ordinary diff line and takes position 5, so
	// hunk 2's first line is 6.
	first := f.Hunks[1].Lines[0]
	if first.Position != 6 {
		t.Errorf("first position of hunk 2 = %d, want 6 (the second header consumes a position)", first.Position)
	}
}

func TestPositionResetsPerFile(t *testing.T) {
	two := modifiedDiff + multiHunkDiff

	files, err := Parse([]byte(two))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("files = %d, want 2", len(files))
	}

	// Positions are file-relative, so the second file starts over.
	if got := files[1].Hunks[0].Lines[0].Position; got != 1 {
		t.Errorf("first position of second file = %d, want 1", got)
	}
}

func TestParseAddedAndDeletedFiles(t *testing.T) {
	const d = `diff --git a/new.go b/new.go
new file mode 100644
index 0000000..abc1234
--- /dev/null
+++ b/new.go
@@ -0,0 +1,2 @@
+package main
+
diff --git a/gone.go b/gone.go
deleted file mode 100644
index abc1234..0000000
--- a/gone.go
+++ /dev/null
@@ -1,2 +0,0 @@
-package main
-
`

	files, err := Parse([]byte(d))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("files = %d, want 2", len(files))
	}

	if files[0].Kind != ChangeAdded || files[0].Path != "new.go" {
		t.Errorf("added file = %+v", files[0])
	}
	if files[1].Kind != ChangeDeleted || files[1].Path != "gone.go" {
		t.Errorf("deleted file = %+v", files[1])
	}
	// A deleted file has no commentable lines.
	if len(files[1].ChangedLines()) != 0 {
		t.Errorf("deleted file should expose no changed lines, got %v", files[1].ChangedLines())
	}
}

func TestParseRename(t *testing.T) {
	const d = `diff --git a/old/path.go b/new/path.go
similarity index 92%
rename from old/path.go
rename to new/path.go
index 111..222 100644
--- a/old/path.go
+++ b/new/path.go
@@ -1,2 +1,2 @@
 package p
-var a = 1
+var a = 2
`

	files, err := Parse([]byte(d))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	f := files[0]
	if f.Kind != ChangeRenamed {
		t.Errorf("Kind = %q, want renamed", f.Kind)
	}
	if f.OldPath != "old/path.go" || f.Path != "new/path.go" {
		t.Errorf("paths = %q -> %q", f.OldPath, f.Path)
	}
}

func TestParseBinaryFile(t *testing.T) {
	const d = `diff --git a/logo.png b/logo.png
index 111..222 100644
Binary files a/logo.png and b/logo.png differ
`

	files, err := Parse([]byte(d))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !files[0].Binary {
		t.Error("file should be marked binary")
	}
	if len(files[0].Hunks) != 0 {
		t.Error("binary file should have no hunks")
	}
}

func TestParseHunkWithoutCount(t *testing.T) {
	// "@@ -1 +1 @@" means one line on each side.
	const d = `diff --git a/a.txt b/a.txt
--- a/a.txt
+++ b/a.txt
@@ -1 +1 @@
-old
+new
`

	files, err := Parse([]byte(d))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	h := files[0].Hunks[0]
	if h.OldLines != 1 || h.NewLines != 1 {
		t.Errorf("counts = %d/%d, want 1/1 for an omitted count", h.OldLines, h.NewLines)
	}
	if h.Lines[1].NewLine != 1 {
		t.Errorf("added line number = %d, want 1", h.Lines[1].NewLine)
	}
}

func TestParseNoNewlineMarker(t *testing.T) {
	const d = `diff --git a/a.txt b/a.txt
--- a/a.txt
+++ b/a.txt
@@ -1 +1 @@
-old
\ No newline at end of file
+new
\ No newline at end of file
`

	files, err := Parse([]byte(d))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	lines := files[0].Hunks[0].Lines
	if len(lines) != 2 {
		t.Fatalf("lines = %d, want 2: the \\ marker annotates a line rather than being one", len(lines))
	}
	// The marker produces no Line, but GitHub still counts it as a physical
	// diff line, so it shifts every position after it.
	if lines[0].Position != 1 {
		t.Errorf("removed line position = %d, want 1", lines[0].Position)
	}
	if lines[1].Position != 3 {
		t.Errorf("added line position = %d, want 3 (the \\ marker consumes position 2)", lines[1].Position)
	}
}

func TestParseRejectsMalformedHunkHeader(t *testing.T) {
	// Guessing here would misplace every comment in the file, so it must fail.
	const d = `diff --git a/a.go b/a.go
--- a/a.go
+++ b/a.go
@@ -x,3 +1,3 @@
 ctx
`

	if _, err := Parse([]byte(d)); err == nil {
		t.Fatal("want error for a malformed hunk header")
	}
}

func TestParseRejectsUnexpectedLineInHunk(t *testing.T) {
	const d = `diff --git a/a.go b/a.go
--- a/a.go
+++ b/a.go
@@ -1,2 +1,2 @@
 ctx
?bogus
`

	_, err := Parse([]byte(d))
	if err == nil {
		t.Fatal("want error for an unrecognized diff line")
	}
	if !strings.Contains(err.Error(), "bogus") {
		t.Errorf("error should quote the offending line, got: %v", err)
	}
}

func TestParseEmptyDiff(t *testing.T) {
	files, err := Parse(nil)
	if err != nil {
		t.Fatalf("empty diff should not error: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("files = %d, want 0", len(files))
	}
}

func TestParseSkipsCommitPreamble(t *testing.T) {
	// `git show` output starts with commit metadata before the first file.
	const d = `commit abc123
Author: Someone <a@b.c>
Date:   Mon Jan 1 00:00:00 2024 +0000

    fix: a thing

diff --git a/a.go b/a.go
--- a/a.go
+++ b/a.go
@@ -1 +1 @@
-a
+b
`

	files, err := Parse([]byte(d))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(files) != 1 || files[0].Path != "a.go" {
		t.Errorf("files = %+v, want just a.go", files.Paths())
	}
}

func TestPositionLookup(t *testing.T) {
	files, _ := Parse([]byte(modifiedDiff))
	f := files[0]

	// An added line resolves. Line 13 is the fifth line of the hunk.
	pos, ok := f.Position(13)
	if !ok {
		t.Fatal("added line 13 should have a position")
	}
	if pos != 5 {
		t.Errorf("position = %d, want 5", pos)
	}

	// A context line resolves too: a model shown that line can comment on it.
	if _, ok := f.Position(10); !ok {
		t.Error("context line 10 should have a position")
	}

	// A line outside the diff does not.
	if _, ok := f.Position(999); ok {
		t.Error("line 999 is not in the diff and should not resolve")
	}
}

func TestSideAndAnchorLine(t *testing.T) {
	// line+side is the supported anchor; position is closing down. Deletions
	// must anchor to the old file on LEFT, everything else to the new file on
	// RIGHT, or comments land on unrelated code.
	files, _ := Parse([]byte(modifiedDiff))
	lines := files[0].Hunks[0].Lines

	removed := lines[3]
	if removed.Kind != LineRemoved {
		t.Fatalf("expected line 3 to be the removal, got %q", removed.Kind)
	}
	if removed.Side() != SideLeft {
		t.Errorf("removed line side = %q, want LEFT", removed.Side())
	}
	if removed.AnchorLine() != removed.OldLine {
		t.Errorf("removed line anchor = %d, want the old-file line %d", removed.AnchorLine(), removed.OldLine)
	}

	added := lines[4]
	if added.Side() != SideRight {
		t.Errorf("added line side = %q, want RIGHT", added.Side())
	}
	if added.AnchorLine() != 13 {
		t.Errorf("added line anchor = %d, want new-file line 13", added.AnchorLine())
	}

	// Unchanged lines are commentable on the right side.
	if lines[0].Side() != SideRight {
		t.Errorf("context line side = %q, want RIGHT", lines[0].Side())
	}
}

func TestIsChangedLine(t *testing.T) {
	files, _ := Parse([]byte(modifiedDiff))
	f := files[0]

	if !f.IsChangedLine(13) {
		t.Error("line 13 was added")
	}
	// Context lines were shown but not changed by this PR.
	if f.IsChangedLine(10) {
		t.Error("line 10 is context, not a change")
	}
}

func TestChangedLines(t *testing.T) {
	files, _ := Parse([]byte(modifiedDiff))

	got := files[0].ChangedLines()
	want := []int{13, 14, 15}
	if len(got) != len(want) {
		t.Fatalf("ChangedLines = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ChangedLines[%d] = %d, want %d", i, got[i], want[i])
		}
	}
}

func TestNearestCommentableLine(t *testing.T) {
	files, _ := Parse([]byte(modifiedDiff))
	f := files[0]

	cases := []struct {
		name    string
		line    int
		maxDist int
		want    int
		wantOK  bool
	}{
		{"exact hit", 14, 3, 14, true},
		{"one line late", 16, 3, 15, true},
		{"one line early", 12, 3, 13, true},
		{"too far", 100, 3, 0, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := f.NearestCommentableLine(tc.line, tc.maxDist)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && got != tc.want {
				t.Errorf("line = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestNearestCommentableLineIgnoresDeletedFiles(t *testing.T) {
	const d = `diff --git a/gone.go b/gone.go
deleted file mode 100644
--- a/gone.go
+++ /dev/null
@@ -1,2 +0,0 @@
-package main
-
`

	files, _ := Parse([]byte(d))
	if _, ok := files[0].NearestCommentableLine(1, 10); ok {
		t.Error("a deleted file has no commentable lines")
	}
}

func TestFilesFindAndPaths(t *testing.T) {
	files, _ := Parse([]byte(modifiedDiff + multiHunkDiff))

	if f := files.Find("a.go"); f == nil {
		t.Error("Find should locate a.go")
	}
	if f := files.Find("missing.go"); f != nil {
		t.Error("Find should return nil for an absent path")
	}

	paths := files.Paths()
	if len(paths) != 2 {
		t.Errorf("Paths = %v, want 2 entries", paths)
	}
}

func TestHunkStringShowsNewLineNumbers(t *testing.T) {
	// Line numbers in the rendered hunk are what let a model cite a location,
	// so they are load-bearing rather than cosmetic.
	files, _ := Parse([]byte(modifiedDiff))

	out := files[0].Hunks[0].String()
	if !strings.Contains(out, "@@ -10,7 +10,9 @@") {
		t.Errorf("rendered hunk should keep its header:\n%s", out)
	}
	if !strings.Contains(out, "13 + \tif s.listener == nil || s.closed {") {
		t.Errorf("added line should be rendered with its new line number:\n%s", out)
	}
	// Removed lines have no new-file line number to show.
	if !strings.Contains(out, "       - \tif s.listener == nil {") {
		t.Errorf("removed line should render without a line number:\n%s", out)
	}
}

func TestParseQuotedPath(t *testing.T) {
	const d = "diff --git \"a/dir/with space.go\" \"b/dir/with space.go\"\n" +
		"--- \"a/dir/with space.go\"\n" +
		"+++ \"b/dir/with space.go\"\n" +
		"@@ -1 +1 @@\n" +
		"-a\n" +
		"+b\n"

	files, err := Parse([]byte(d))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if files[0].Path != "dir/with space.go" {
		t.Errorf("Path = %q, want the unquoted path", files[0].Path)
	}
}
