package diff

import "testing"

func TestRemovedGuardHasSurvivingCommentAnchors(t *testing.T) {
	files, err := Parse([]byte("diff --git a/auth.go b/auth.go\n--- a/auth.go\n+++ b/auth.go\n@@ -1,6 +1,3 @@\n func Handle() {\n- if !authorized() {\n-  return\n- }\n  serve()\n }\n"))
	if err != nil {
		t.Fatal(err)
	}
	f := files[0]
	for _, n := range []int{1, 2} {
		if got, ok := f.NearestCommentableLine(n, 0); !ok || got != n {
			t.Errorf("removal boundary %d: got %d, %v", n, got, ok)
		}
	}
	if _, ok := f.NearestCommentableLine(3, 0); ok {
		t.Error("unrelated context became commentable")
	}
	if len(f.ChangedLines()) != 0 || f.IsChangedLine(2) {
		t.Error("surviving context must not become an added line")
	}
}

func TestRemovalAnchorsStayOnTheRightSideOfTheEdit(t *testing.T) {
	contextLine := func(n int) Line { return Line{Kind: LineContext, NewLine: n} }
	addedLine := func(n int) Line { return Line{Kind: LineAdded, NewLine: n} }
	removed := Line{Kind: LineRemoved, OldLine: 4}
	for _, tc := range []struct {
		name  string
		lines []Line
		want  []int
	}{
		{"start", []Line{removed, contextLine(1), contextLine(2)}, []int{1}},
		{"end", []Line{contextLine(7), contextLine(8), removed}, []int{8}},
		{"replacement", []Line{contextLine(1), removed, addedLine(2), contextLine(3)}, []int{2}},
		{"separate removal and addition", []Line{contextLine(1), removed, contextLine(2), contextLine(3), addedLine(4)}, []int{1, 2, 4}},
		{"shared boundary", []Line{removed, contextLine(1), removed, contextLine(2)}, []int{1, 2}},
		{"no surviving context", []Line{removed}, nil},
		{"unchanged", []Line{contextLine(1), contextLine(2)}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &File{Kind: ChangeModified, Hunks: []Hunk{{Lines: tc.lines}}}
			got := f.CommentableLines()
			if len(got) != len(tc.want) {
				t.Fatalf("anchors = %v, want %v", got, tc.want)
			}
			for i, n := range got {
				if n != tc.want[i] {
					t.Errorf("anchor %d = %d, want %d", i, n, tc.want[i])
				}
				if _, ok := f.Position(n); !ok {
					t.Errorf("anchor %d has no RIGHT-side position", n)
				}
			}
		})
	}
}
