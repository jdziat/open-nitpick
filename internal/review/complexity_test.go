package review

import (
	"strings"
	"testing"

	"github.com/jdziat/open-nitpick/internal/diff"
)

// hunks builds a changed file from one hunk per argument, each carrying the
// given number of added lines. Multiple hunks are what "scattered edits"
// scores, so the count has to be the caller's to set.
func hunks(path string, kind diff.ChangeKind, perHunk ...int) *diff.File {
	f := &diff.File{Path: path, Kind: kind}
	line := 1
	for _, n := range perHunk {
		h := diff.Hunk{NewStart: line, NewLines: n}
		for i := 0; i < n; i++ {
			h.Lines = append(h.Lines, diff.Line{Kind: diff.LineAdded, Content: "x := 1", NewLine: line})
			line++
		}
		f.Hunks = append(f.Hunks, h)
	}
	return f
}

func TestScatteredEditsScoreAboveOneHunk(t *testing.T) {
	// The same number of added lines, split across three hunks, is harder to
	// hold in the head than one contiguous block, so it ranks higher.
	scattered := hunks("a.go", diff.ChangeModified, 4, 4, 4)
	contiguous := hunks("a.go", diff.ChangeModified, 12)

	if Score(scattered).Score <= Score(contiguous).Score {
		t.Fatalf("scattered score %v, contiguous %v", Score(scattered).Score, Score(contiguous).Score)
	}
	if !strings.Contains(Score(scattered).Reasons(), "scattered edits") {
		t.Fatalf("reasons = %q, want scattered edits named", Score(scattered).Reasons())
	}
	if strings.Contains(Score(contiguous).Reasons(), "scattered edits") {
		t.Fatalf("a single-hunk change claimed scattered edits: %q", Score(contiguous).Reasons())
	}
}

func TestANewFileScoresAboveAnEquivalentModification(t *testing.T) {
	// New code has no prior review, so an added file outranks the same lines
	// changed in an existing one.
	added := hunks("pkg/new.go", diff.ChangeAdded, 10)
	modified := hunks("pkg/new.go", diff.ChangeModified, 10)

	if Score(added).Score <= Score(modified).Score {
		t.Fatalf("new file score %v, modified %v", Score(added).Score, Score(modified).Score)
	}
	if !strings.Contains(Score(added).Reasons(), "new file") {
		t.Fatalf("reasons = %q, want new file named", Score(added).Reasons())
	}
}

func TestADeletionScoresBelowAnEquivalentModification(t *testing.T) {
	// A deletion mostly needs its callers checked, which the related-context
	// walk does, so the deleted file itself ranks below the same change made.
	deleted := hunks("pkg/old.go", diff.ChangeDeleted, 10)
	modified := hunks("pkg/old.go", diff.ChangeModified, 10)

	if Score(deleted).Score >= Score(modified).Score {
		t.Fatalf("deletion score %v, modified %v", Score(deleted).Score, Score(modified).Score)
	}
	if !strings.Contains(Score(deleted).Reasons(), "deletion") {
		t.Fatalf("reasons = %q, want deletion named", Score(deleted).Reasons())
	}
}
